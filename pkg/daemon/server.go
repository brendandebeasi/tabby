package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/brendandebeasi/tabby/pkg/perf"
)

// ClientInfo tracks per-client state for renderers
type ClientInfo struct {
	Conn            net.Conn
	Target          RenderTarget
	Width           int
	Height          int
	ViewportOffset  int
	ColorProfile    string     // "Ascii", "ANSI", "ANSI256", "TrueColor"
	lastContentHash uint32     // hash of last sent content to deduplicate renders
	writeMu         sync.Mutex // serialises concurrent writes to Conn
}

// Server is the daemon server that manages connected renderers
type InputAnonStats struct {
	lastDropLogNano int64
	dropCount       uint64
}

type Server struct {
	socketPath string
	pidPath    string
	listener   net.Listener
	clients    map[string]*ClientInfo
	clientsMu  sync.RWMutex
	done       chan struct{}

	anonInputStats sync.Map

	// Render state
	sequenceNum uint64
	seqMu       sync.Mutex

	// Render batching - coalesces rapid render requests into single frames
	renderPending    map[string]bool // clients with pending render
	renderBatchTimer *time.Timer     // single timer for batch flush
	renderBatchDelay time.Duration   // batch window (e.g., 16ms for ~60fps)
	renderBatchMu    sync.Mutex      // protects render batching state

	// Callback for rendering - called when a client needs content
	// The callback receives clientID, width, height and returns RenderPayload
	OnRenderNeeded func(clientID string, width, height int) *RenderPayload

	// Callback for new client connections
	OnConnect func(clientID string, paneID string)

	// Callback for handling input events
	OnInput func(clientID string, input *InputPayload)

	// Callback for resize events
	OnResize func(clientID string, width, height int, paneID string)

	// Callback for client disconnect
	OnDisconnect func(clientID string)

	// Callback for tmux-hook deliveries from `tabby hook` CLI. The daemon
	// wires this to push a TmuxHookEvent onto its event loop, replacing the
	// SIGUSR1 / SIGUSR2 signaling path used by pre-Step-4 hook subcommands.
	// See /Users/b/.claude/plans/nifty-jingling-tulip.md Step 4.
	OnHook func(p *HookPayload)

	// Debug logging callback (set by daemon for diagnostics)
	DebugLog func(format string, args ...interface{})
}

// NewServer creates a new daemon server
func NewServer(sessionID string) *Server {
	return &Server{
		socketPath:       SocketPath(sessionID),
		pidPath:          PidPath(sessionID),
		clients:          make(map[string]*ClientInfo),
		done:             make(chan struct{}),
		sequenceNum:      1,
		renderPending:    make(map[string]bool),
		renderBatchDelay: 16 * time.Millisecond, // ~60fps
	}
}

// Start begins listening for client connections
func (s *Server) Start() error {
	// Check if another daemon is already running
	if err := s.checkAndClaimPid(); err != nil {
		return err
	}

	// Remove stale socket if exists (safe now that we own the pidfile)
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		os.Remove(s.pidPath) // Clean up pidfile on failure
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	s.listener = listener

	// Accept connections in goroutine
	go s.acceptLoop()

	return nil
}

// checkAndClaimPid checks for existing daemon and claims pidfile
func (s *Server) checkAndClaimPid() error {
	// Check if pidfile exists and process is still running
	if data, err := os.ReadFile(s.pidPath); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			// Check if process is still alive
			if process, err := os.FindProcess(pid); err == nil {
				// On Unix, FindProcess always succeeds, so we need to send signal 0
				if err := process.Signal(syscall.Signal(0)); err == nil {
					// Process is still running
					return fmt.Errorf("daemon already running with pid %d", pid)
				}
			}
		}
		// Stale pidfile, remove it
		os.Remove(s.pidPath)
	}

	// Write our pid
	pid := os.Getpid()
	if err := os.WriteFile(s.pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("failed to write pidfile: %w", err)
	}

	return nil
}

// Stop shuts down the server
func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}

	// Stop render batch timer
	s.renderBatchMu.Lock()
	if s.renderBatchTimer != nil {
		s.renderBatchTimer.Stop()
		s.renderBatchTimer = nil
	}
	s.renderBatchMu.Unlock()

	s.clientsMu.Lock()
	for id, client := range s.clients {
		client.Conn.Close()
		delete(s.clients, id)
	}
	s.clientsMu.Unlock()

	// Only remove socket and PID file if we still own them
	// (prevents a departing daemon from deleting a new daemon's socket)
	myPid := os.Getpid()
	if data, err := os.ReadFile(s.pidPath); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil && pid == myPid {
			os.Remove(s.socketPath)
			os.Remove(s.pidPath)
		}
		// else: another daemon owns these files, don't touch them
	} else {
		// PID file missing/unreadable - clean up socket as a fallback
		os.Remove(s.socketPath)
	}
}

// ClientCount returns the number of connected clients
func (s *Server) ClientCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

// GetSocketPath returns the socket path
func (s *Server) GetSocketPath() string {
	return s.socketPath
}

func (s *Server) recordAnonymousInput(sourceKey string) (uint64, bool) {
	if sourceKey == "" {
		sourceKey = "unknown"
	}
	value, _ := s.anonInputStats.LoadOrStore(sourceKey, &InputAnonStats{})
	stats := value.(*InputAnonStats)
	count := atomic.AddUint64(&stats.dropCount, 1)
	now := time.Now().UnixNano()
	last := atomic.LoadInt64(&stats.lastDropLogNano)
	if now-last < int64(750*time.Millisecond) {
		return count, false
	}
	if atomic.CompareAndSwapInt64(&stats.lastDropLogNano, last, now) {
		return count, true
	}
	return count, false
}

// acceptLoop handles incoming connections
func (s *Server) acceptLoop() {
	for {
		select {
		case <-s.done:
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}

		go s.handleClient(conn)
	}
}

// handleClient processes messages from a client
func (s *Server) handleClient(conn net.Conn) {
	defer conn.Close()

	remoteAddr := ""
	if conn != nil && conn.RemoteAddr() != nil {
		remoteAddr = conn.RemoteAddr().String()
	}
	scanner := bufio.NewScanner(conn)
	// Increase scanner buffer for large render payloads
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var clientID string

	for scanner.Scan() {
		raw := scanner.Bytes()
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			if s.DebugLog != nil {
				s.DebugLog("SOCKET_PARSE_ERR client=%s remote=%s bytes=%d err=%v", clientID, remoteAddr, len(raw), err)
			}
			continue
		}

		switch msg.Type {
		case MsgSubscribe:
			if err := msg.Target.Valid(); err != nil {
				if s.DebugLog != nil {
					s.DebugLog("SOCKET_SUBSCRIBE_DROP reason=invalid_target remote=%s err=%v", remoteAddr, err)
				}
				return
			}
			clientID = msg.Target.Key()
			if s.DebugLog != nil {
				s.DebugLog("SOCKET_SUBSCRIBE client=%s kind=%s remote=%s", clientID, msg.Target.Kind, remoteAddr)
			}
			// Parse resize payload if included
			width, height := 80, 24 // defaults
			colorProfile := "ANSI256"
			paneID := ""
			if msg.Payload != nil {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var resize ResizePayload
				if json.Unmarshal(payloadBytes, &resize) == nil {
					if resize.Width > 0 {
						width = resize.Width
					}
					if resize.Height > 0 {
						height = resize.Height
					}
					if resize.ColorProfile != "" {
						colorProfile = resize.ColorProfile
					}
					paneID = resize.PaneID
				}
			}
			s.clientsMu.Lock()
			s.clients[clientID] = &ClientInfo{
				Conn:         conn,
				Target:       msg.Target,
				Width:        width,
				Height:       height,
				ColorProfile: colorProfile,
			}
			s.clientsMu.Unlock()
			if s.OnConnect != nil {
				s.OnConnect(clientID, paneID)
			}
			// Send initial render
			s.SendRenderToClient(clientID)

		case MsgUnsubscribe:
			s.clientsMu.Lock()
			delete(s.clients, clientID)
			s.clientsMu.Unlock()
			return

		case MsgResize:
			if msg.Payload != nil {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var resize ResizePayload
				if json.Unmarshal(payloadBytes, &resize) == nil {
					// Check if size actually changed
					s.clientsMu.RLock()
					client, exists := s.clients[clientID]
					sameSize := exists && client.Width == resize.Width && client.Height == resize.Height
					s.clientsMu.RUnlock()

					if sameSize {
						continue
					}

					// Update client dimensions
					s.clientsMu.Lock()
					if client, ok := s.clients[clientID]; ok {
						client.Width = resize.Width
						client.Height = resize.Height
						if resize.ColorProfile != "" {
							client.ColorProfile = resize.ColorProfile
						}
					}
					s.clientsMu.Unlock()

					if s.DebugLog != nil {
						s.DebugLog("RESIZE_APPLY client=%s width=%d height=%d pane=%s", clientID, resize.Width, resize.Height, resize.PaneID)
					}
					if s.OnResize != nil {
						s.OnResize(clientID, resize.Width, resize.Height, resize.PaneID)
					}
					s.SendRenderToClient(clientID)
				}
			}

		case MsgViewportUpdate:
			if msg.Payload != nil {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var vp ViewportUpdatePayload
				if json.Unmarshal(payloadBytes, &vp) == nil {
					s.clientsMu.Lock()
					if client, ok := s.clients[clientID]; ok {
						client.ViewportOffset = vp.ViewportOffset
					}
					s.clientsMu.Unlock()
				}
			}

		case MsgInput:
			// Typed identity: every input must arrive on a subscribed
			// connection OR carry a valid Target. No silent fallback.
			if clientID == "" {
				if err := msg.Target.Valid(); err != nil {
					if s.DebugLog != nil {
						s.DebugLog("SOCKET_INPUT_DROP reason=invalid_target remote=%s err=%v", remoteAddr, err)
						count, shouldLog := s.recordAnonymousInput(remoteAddr)
						if shouldLog {
							s.DebugLog("SOCKET_INPUT_ANON source=%s count=%d err=%v", remoteAddr, count, err)
						}
					}
					continue
				}
				clientID = msg.Target.Key()
			}
			if msg.Payload == nil {
				if s.DebugLog != nil {
					s.DebugLog("SOCKET_INPUT_DROP reason=nil_payload client=%s remote=%s", clientID, remoteAddr)
				}
				continue
			}
			payloadBytes, err := json.Marshal(msg.Payload)
			if err != nil {
				if s.DebugLog != nil {
					s.DebugLog("SOCKET_INPUT_DROP reason=payload_marshal client=%s remote=%s err=%v", clientID, remoteAddr, err)
				}
				continue
			}
			var input InputPayload
			if err := json.Unmarshal(payloadBytes, &input); err != nil {
				if s.DebugLog != nil {
					s.DebugLog("SOCKET_INPUT_DROP reason=payload_unmarshal client=%s remote=%s bytes=%d err=%v", clientID, remoteAddr, len(payloadBytes), err)
				}
				continue
			}
			if s.DebugLog != nil {
				s.DebugLog("SOCKET_INPUT client=%s type=%s btn=%s action=%s resolved=%s x=%d y=%d target=%s pane=%s sourcePane=%s remote=%s", clientID, input.Type, input.Button, input.Action, input.ResolvedAction, input.MouseX, input.MouseY, input.ResolvedTarget, input.PaneID, input.SourcePaneID, remoteAddr)
			}
			if s.OnInput != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							fmt.Fprintf(os.Stderr, "PANIC in OnInput (client=%s): %v\n", clientID, r)
						}
					}()
					s.OnInput(clientID, &input)
				}()
			}

		case MsgPing:
			s.clientsMu.RLock()
			var writeMu *sync.Mutex
			if client, ok := s.clients[clientID]; ok {
				writeMu = &client.writeMu
			}
			s.clientsMu.RUnlock()
			if writeMu != nil {
				writeMu.Lock()
				s.sendMessage(conn, Message{Type: MsgPong})
				writeMu.Unlock()
			}

		case MsgHook:
			// MsgHook carries a tmux-hook delivery from `tabby hook`. The
			// hook CLI dials the socket, sends one Message, and disconnects;
			// it does not subscribe, so clientID may be "". That's fine —
			// the payload itself identifies which hook fired.
			if msg.Payload == nil {
				if s.DebugLog != nil {
					s.DebugLog("SOCKET_HOOK_DROP reason=nil_payload remote=%s", remoteAddr)
				}
				continue
			}
			payloadBytes, err := json.Marshal(msg.Payload)
			if err != nil {
				if s.DebugLog != nil {
					s.DebugLog("SOCKET_HOOK_DROP reason=payload_marshal remote=%s err=%v", remoteAddr, err)
				}
				continue
			}
			var hp HookPayload
			if err := json.Unmarshal(payloadBytes, &hp); err != nil {
				if s.DebugLog != nil {
					s.DebugLog("SOCKET_HOOK_DROP reason=payload_unmarshal remote=%s bytes=%d err=%v", remoteAddr, len(payloadBytes), err)
				}
				continue
			}
			if s.DebugLog != nil {
				s.DebugLog("SOCKET_HOOK kind=%s args=%v remote=%s", hp.Kind, hp.Args, remoteAddr)
			}
			if s.OnHook != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							fmt.Fprintf(os.Stderr, "PANIC in OnHook (kind=%s): %v\n", hp.Kind, r)
						}
					}()
					s.OnHook(&hp)
				}()
			}
		}
	}

	// Client disconnected
	if clientID != "" {
		s.clientsMu.Lock()
		delete(s.clients, clientID)
		s.clientsMu.Unlock()
		// Notify callback
		if s.OnDisconnect != nil {
			s.OnDisconnect(clientID)
		}
	}
}

// BroadcastRender queues render payloads for all connected renderers.
// Renders are batched and sent after renderBatchDelay to coalesce rapid requests.
func (s *Server) BroadcastRender() {
	t := perf.Start("BroadcastRender")
	defer t.Stop()

	s.clientsMu.RLock()
	clientIDs := make([]string, 0, len(s.clients))
	for id := range s.clients {
		clientIDs = append(clientIDs, id)
	}
	s.clientsMu.RUnlock()

	if s.DebugLog != nil {
		s.DebugLog("BROADCAST_RENDER clients=%d ids=%v", len(clientIDs), clientIDs)
	}

	// Queue all clients for batched render
	s.renderBatchMu.Lock()
	for _, id := range clientIDs {
		s.renderPending[id] = true
	}
	s.scheduleRenderFlushLocked()
	s.renderBatchMu.Unlock()
}

// RenderActiveWindowOnly sends render only to the active window's sidebar and headers.
// This is an optimization for animation ticks - hidden windows don't need constant updates.
// activeWindowID is the tmux window ID like "@1", "@4", etc.
func (s *Server) RenderActiveWindowOnly(activeWindowID string) {
	t := perf.Start("RenderActiveOnly")
	defer t.Stop()

	s.clientsMu.RLock()
	var matches []string
	for id, client := range s.clients {
		// Sidebar renderers are keyed per-window; render only the one for the active window.
		// Headers are per-pane; animation-tick updates are not sent to them.
		if client.Target.Kind == TargetSidebar && client.Target.WindowID == activeWindowID {
			matches = append(matches, id)
		}
	}
	s.clientsMu.RUnlock()

	for _, id := range matches {
		s.SendRenderToClient(id)
	}
}

// scheduleRenderFlushLocked starts the batch timer if not already running.
// Must be called with renderBatchMu held.
func (s *Server) scheduleRenderFlushLocked() {
	if s.renderBatchTimer != nil {
		return // timer already running
	}
	s.renderBatchTimer = time.AfterFunc(s.renderBatchDelay, s.flushPendingRenders)
}

// flushPendingRenders sends renders to all clients with pending requests.
// Called by the batch timer.
func (s *Server) flushPendingRenders() {
	s.renderBatchMu.Lock()
	s.renderBatchTimer = nil
	pending := s.renderPending
	s.renderPending = make(map[string]bool)
	s.renderBatchMu.Unlock()

	if len(pending) == 0 {
		return
	}

	if s.DebugLog != nil {
		s.DebugLog("RENDER_BATCH_FLUSH clients=%d", len(pending))
	}

	// Render all pending clients in parallel
	var wg sync.WaitGroup
	for clientID := range pending {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			s.sendRenderToClientImmediate(id)
		}(clientID)
	}
	wg.Wait()
}

// SendRenderToClient queues a render for a specific client.
// The actual render is batched and sent after renderBatchDelay.
func (s *Server) SendRenderToClient(clientID string) {
	s.renderBatchMu.Lock()
	s.renderPending[clientID] = true
	s.scheduleRenderFlushLocked()
	s.renderBatchMu.Unlock()
}

// sendRenderToClientImmediate generates and sends render content to a specific client immediately.
// This is the internal implementation called by flushPendingRenders.
func (s *Server) sendRenderToClientImmediate(clientID string) {
	s.clientsMu.RLock()
	client, ok := s.clients[clientID]
	if !ok {
		s.clientsMu.RUnlock()
		if s.DebugLog != nil {
			s.DebugLog("RENDER_SKIP client=%s reason=not_found", clientID)
		}
		return
	}
	width := client.Width
	height := client.Height
	s.clientsMu.RUnlock()

	if s.OnRenderNeeded == nil {
		if s.DebugLog != nil {
			s.DebugLog("RENDER_SKIP client=%s reason=no_callback", clientID)
		}
		return
	}

	// Get render payload from callback (may take time)
	render := s.OnRenderNeeded(clientID, width, height)
	if render == nil {
		if s.DebugLog != nil {
			s.DebugLog("RENDER_SKIP client=%s reason=nil_payload", clientID)
		}
		return
	}
	if s.DebugLog != nil {
		s.DebugLog("RENDER_SEND client=%s client_w=%d client_h=%d payload_w=%d payload_h=%d seq=%d", clientID, width, height, render.Width, render.Height, render.SequenceNum)
	}

	// Deduplicate: skip sending if content hasn't changed
	contentHash := hashContent(render.Content)
	s.clientsMu.RLock()
	client, ok = s.clients[clientID]
	if !ok {
		// Client disconnected during render
		s.clientsMu.RUnlock()
		if s.DebugLog != nil {
			s.DebugLog("RENDER_SKIP client=%s reason=disconnected_during_render", clientID)
		}
		return
	}
	if client.lastContentHash == contentHash {
		s.clientsMu.RUnlock()
		return
	}
	s.clientsMu.RUnlock()

	// Update hash and get fresh conn + writeMu reference under lock
	s.clientsMu.Lock()
	client, ok = s.clients[clientID]
	if !ok {
		s.clientsMu.Unlock()
		return
	}
	client.lastContentHash = contentHash
	conn := client.Conn
	writeMu := &client.writeMu
	s.clientsMu.Unlock()

	// Set sequence number
	s.seqMu.Lock()
	render.SequenceNum = s.sequenceNum
	s.sequenceNum++
	s.seqMu.Unlock()

	// Pull the typed target for outgoing messages.
	s.clientsMu.RLock()
	target := RenderTarget{}
	if ci, ok := s.clients[clientID]; ok {
		target = ci.Target
	}
	s.clientsMu.RUnlock()

	msg := Message{
		Type:    MsgRender,
		Target:  target,
		Payload: render,
	}
	// Serialise writes per-client so parallel BroadcastRender goroutines
	// cannot interleave bytes on the same connection.
	writeMu.Lock()
	s.sendMessage(conn, msg)
	writeMu.Unlock()
}

// hashContent returns a simple FNV-like hash of content for deduplication
func hashContent(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

// GetClientInfo returns info about a specific client
func (s *Server) GetClientInfo(clientID string) *ClientInfo {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	if client, ok := s.clients[clientID]; ok {
		// Return a copy
		return &ClientInfo{
			Width:          client.Width,
			Height:         client.Height,
			ViewportOffset: client.ViewportOffset,
			ColorProfile:   client.ColorProfile,
		}
	}
	return nil
}

// GetAllClientIDs returns all connected client IDs
func (s *Server) GetAllClientIDs() []string {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	ids := make([]string, 0, len(s.clients))
	for id := range s.clients {
		ids = append(ids, id)
	}
	return ids
}

// UpdateClientSize updates the stored width/height for a client
// This is used to sync sizes from tmux on resize events
func (s *Server) UpdateClientSize(clientID string, width, height int) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if client, ok := s.clients[clientID]; ok {
		// Only clear content hash if size actually changed
		if client.Width != width || client.Height != height {
			client.Width = width
			client.Height = height
			client.lastContentHash = 0
		}
	}
}

func (s *Server) UpdateClientWidth(clientID string, width int) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if client, ok := s.clients[clientID]; ok {
		// Only clear content hash if width actually changed
		if client.Width != width {
			client.Width = width
			client.lastContentHash = 0
		}
	}
}

// SendMenuToClient sends a context menu to a specific renderer client
func (s *Server) SendMenuToClient(clientID string, menu *MenuPayload) {
	s.clientsMu.RLock()
	client, ok := s.clients[clientID]
	if !ok {
		s.clientsMu.RUnlock()
		return
	}
	conn := client.Conn
	target := client.Target
	writeMu := &client.writeMu
	s.clientsMu.RUnlock()

	msg := Message{
		Type:    MsgMenu,
		Target:  target,
		Payload: menu,
	}
	writeMu.Lock()
	s.sendMessage(conn, msg)
	writeMu.Unlock()
}

func (s *Server) SendMarkerPickerToClient(clientID string, picker *MarkerPickerPayload) {
	s.clientsMu.RLock()
	client, ok := s.clients[clientID]
	if !ok {
		s.clientsMu.RUnlock()
		return
	}
	conn := client.Conn
	target := client.Target
	writeMu := &client.writeMu
	s.clientsMu.RUnlock()

	msg := Message{
		Type:    MsgMarkerPicker,
		Target:  target,
		Payload: picker,
	}
	writeMu.Lock()
	s.sendMessage(conn, msg)
	writeMu.Unlock()
}

func (s *Server) SendColorPickerToClient(clientID string, picker *ColorPickerPayload) {
	s.clientsMu.RLock()
	client, ok := s.clients[clientID]
	if !ok {
		s.clientsMu.RUnlock()
		return
	}
	conn := client.Conn
	target := client.Target
	writeMu := &client.writeMu
	s.clientsMu.RUnlock()

	msg := Message{
		Type:    MsgColorPicker,
		Target:  target,
		Payload: picker,
	}
	writeMu.Lock()
	s.sendMessage(conn, msg)
	writeMu.Unlock()
}

// colorProfileOrder defines the capability order (lowest to highest)
var colorProfileOrder = map[string]int{
	"Ascii":     0,
	"ANSI":      1,
	"ANSI256":   2,
	"TrueColor": 3,
}

// GetMinColorProfile returns the minimum color profile among all connected clients
func (s *Server) GetMinColorProfile() string {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	if len(s.clients) == 0 {
		return "ANSI256" // default
	}

	minProfile := "TrueColor"
	minOrder := colorProfileOrder[minProfile]

	for _, client := range s.clients {
		profile := client.ColorProfile
		if profile == "" {
			profile = "ANSI256"
		}
		order, ok := colorProfileOrder[profile]
		if !ok {
			order = 2 // default to ANSI256
		}
		if order < minOrder {
			minOrder = order
			minProfile = profile
		}
	}

	return minProfile
}

// sendMessage sends a message to a client
func (s *Server) sendMessage(conn net.Conn, msg Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in sendMessage: %v", r)
		}
	}()
	if conn == nil {
		return fmt.Errorf("nil connection")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = conn.Write(append(data, '\n'))
	return err
}
