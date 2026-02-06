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
	"syscall"
	"time"

	"github.com/b/tmux-tabs/pkg/perf"
)

// ClientInfo tracks per-client state for renderers
type ClientInfo struct {
	Conn           net.Conn
	Width          int
	Height         int
	ViewportOffset int
	ColorProfile   string // "Ascii", "ANSI", "ANSI256", "TrueColor"
	lastContentHash uint32 // hash of last sent content to deduplicate renders
}

// Server is the daemon server that manages connected renderers
type Server struct {
	socketPath string
	pidPath    string
	listener   net.Listener
	clients    map[string]*ClientInfo
	clientsMu  sync.RWMutex
	done       chan struct{}

	// Render state
	sequenceNum uint64
	seqMu       sync.Mutex

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

	// Debug logging callback (set by daemon for diagnostics)
	DebugLog func(format string, args ...interface{})
}

// NewServer creates a new daemon server
func NewServer(sessionID string) *Server {
	return &Server{
		socketPath:  SocketPath(sessionID),
		pidPath:     PidPath(sessionID),
		clients:     make(map[string]*ClientInfo),
		done:        make(chan struct{}),
		sequenceNum: 1,
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

	scanner := bufio.NewScanner(conn)
	// Increase scanner buffer for large render payloads
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var clientID string

	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case MsgSubscribe:
			clientID = msg.ClientID
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
				Width:        width,
				Height:       height,
				ColorProfile: colorProfile,
			}
			s.clientsMu.Unlock()
			if s.OnConnect != nil {
				s.OnConnect(clientID, paneID)
			}
			// Send initial render
			s.sendRenderToClient(clientID)

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
					s.clientsMu.Lock()
					if client, ok := s.clients[clientID]; ok {
						client.Width = resize.Width
						client.Height = resize.Height
					}
					s.clientsMu.Unlock()
					// Notify and re-render
					if s.OnResize != nil {
						s.OnResize(clientID, resize.Width, resize.Height, resize.PaneID)
					}
					s.sendRenderToClient(clientID)
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
			if msg.Payload != nil {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var input InputPayload
				if json.Unmarshal(payloadBytes, &input) == nil {
					if s.DebugLog != nil {
						s.DebugLog("SOCKET_INPUT client=%s type=%s btn=%s action=%s", clientID, input.Type, input.Button, input.Action)
					}
					if s.OnInput != nil {
						// Recover from panics in input handler to avoid killing client goroutine
						func() {
							defer func() {
								if r := recover(); r != nil {
									fmt.Fprintf(os.Stderr, "PANIC in OnInput (client=%s): %v\n", clientID, r)
								}
							}()
							s.OnInput(clientID, &input)
						}()
					}
				}
			}

		case MsgPing:
			s.sendMessage(conn, Message{Type: MsgPong})
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

// BroadcastRender sends render payloads to all connected renderers
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

	for _, id := range clientIDs {
		s.sendRenderToClient(id)
	}
}

// RenderActiveWindowOnly sends render only to the active window's sidebar and headers.
// This is an optimization for animation ticks - hidden windows don't need constant updates.
// activeWindowID is the tmux window ID like "@1", "@4", etc.
func (s *Server) RenderActiveWindowOnly(activeWindowID string) {
	t := perf.Start("RenderActiveOnly")
	defer t.Stop()

	s.clientsMu.RLock()
	clientIDs := make([]string, 0, len(s.clients))
	for id := range s.clients {
		clientIDs = append(clientIDs, id)
	}
	s.clientsMu.RUnlock()

	for _, id := range clientIDs {
		// Render if: sidebar for active window, or header in active window
		// ClientID format: "@1" for sidebar, "header:%123" for pane headers
		if id == activeWindowID {
			s.sendRenderToClient(id)
		}
		// Note: headers are per-pane, not per-window, so we skip them during
		// animation-only renders. They'll get updated on window state changes.
	}
}

// sendRenderToClient generates and sends render content to a specific client
func (s *Server) sendRenderToClient(clientID string) {
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
		// Don't log dedup skips - too noisy
		return
	}
	s.clientsMu.RUnlock()

	// Update hash and get fresh conn reference under lock
	s.clientsMu.Lock()
	client, ok = s.clients[clientID]
	if !ok {
		s.clientsMu.Unlock()
		return
	}
	client.lastContentHash = contentHash
	conn := client.Conn
	s.clientsMu.Unlock()

	// Set sequence number
	s.seqMu.Lock()
	render.SequenceNum = s.sequenceNum
	s.sequenceNum++
	s.seqMu.Unlock()

	msg := Message{
		Type:     MsgRender,
		ClientID: clientID,
		Payload:  render,
	}
	s.sendMessage(conn, msg)
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

// SendMenuToClient sends a context menu to a specific renderer client
func (s *Server) SendMenuToClient(clientID string, menu *MenuPayload) {
	s.clientsMu.RLock()
	client, ok := s.clients[clientID]
	if !ok {
		s.clientsMu.RUnlock()
		return
	}
	conn := client.Conn
	s.clientsMu.RUnlock()

	msg := Message{
		Type:     MsgMenu,
		ClientID: clientID,
		Payload:  menu,
	}
	s.sendMessage(conn, msg)
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
