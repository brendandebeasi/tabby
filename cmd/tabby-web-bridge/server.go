package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

const (
	ptyFrameData  byte = 0x01
	ptyFrameInput byte = 0x02
)

type ServerConfig struct {
	Host      string
	Port      int
	SessionID string
	Token     string
	AuthUser  string
	AuthPass  string
}

type Server struct {
	cfg          ServerConfig
	pty          *ControlModeSession
	sidebar      *SidebarBridge
	clients      map[*ClientConn]struct{}
	clientSeq    uint64
	stopCh       chan struct{}
	mu           sync.RWMutex
	httpServer   *http.Server
	upgrader     websocket.Upgrader
	lastRender   []byte
	lastRenderMu sync.RWMutex
}

type ClientConn struct {
	id               string
	conn             *websocket.Conn
	panes            map[string]struct{}
	attachedPaneID   string
	sidebarClientID  string
	lastSnapshotPane string
	lastSnapshotHash uint32
	lastPtyOutputAt  int64
	ptyMode          string
	mu               sync.RWMutex
}

type WebSocketMessage struct {
	Channel string          `json:"channel"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type ControlAttachPayload struct {
	PaneID string `json:"paneId"`
}

type ControlResizePayload struct {
	PaneID string `json:"paneId"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

type ControlAttachedPanePayload struct {
	PaneID string `json:"paneId"`
}

type ControlPtyHealthPayload struct {
	Mode    string `json:"mode"`
	Healthy bool   `json:"healthy"`
}

func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		cfg:     cfg,
		clients: make(map[*ClientConn]struct{}),
		stopCh:  make(chan struct{}),
	}

	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     s.checkOrigin,
	}

	s.pty = NewControlModeSession(cfg.SessionID, s.onPtyOutput)
	s.sidebar = NewSidebarBridge(cfg.SessionID, s.onDaemonMessage)
	return s
}

func (s *Server) Start() error {
	if err := s.pty.Start(); err != nil {
		return err
	}
	if err := s.sidebar.Start(); err != nil {
		s.pty.Stop()
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/connect", s.handleConnect)
	mux.HandleFunc("/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/ws", s.handleWebSocket)

	addr := net.JoinHostPort(s.cfg.Host, intToString(s.cfg.Port))
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server error: %v", err)
		}
	}()
	go s.snapshotLoop()

	return nil
}

func (s *Server) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
	if s.sidebar != nil {
		s.sidebar.Stop()
	}
	if s.pty != nil {
		s.pty.Stop()
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.validateAuth(r, true) || !s.validateToken(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}

	client := &ClientConn{
		id:    intToString(int(atomic.AddUint64(&s.clientSeq, 1))),
		conn:  conn,
		panes: make(map[string]struct{}),
	}
	s.addClient(client)

	// Replay last sidebar render so the client doesn't wait for the next event
	s.lastRenderMu.RLock()
	lastRender := s.lastRender
	s.lastRenderMu.RUnlock()
	if lastRender != nil {
		client.sendText(lastRender)
	}

	go s.readLoop(client)
}

func (s *Server) readLoop(client *ClientConn) {
	defer func() {
		client.mu.Lock()
		sidebarClientID := client.sidebarClientID
		client.sidebarClientID = ""
		client.mu.Unlock()
		if sidebarClientID != "" {
			if err := s.sidebar.ClearClient(sidebarClientID); err != nil {
				log.Printf("clear sidebar client failed: %v", err)
			}
		}
		s.removeClient(client)
		_ = client.conn.Close()
	}()

	for {
		msgType, data, err := client.conn.ReadMessage()
		if err != nil {
			return
		}

		switch msgType {
		case websocket.TextMessage:
			if err := s.handleTextMessage(client, data); err != nil {
				log.Printf("ws text message error: %v", err)
			}
		case websocket.BinaryMessage:
			if err := s.handleBinaryMessage(client, data); err != nil {
				log.Printf("ws binary message error: %v", err)
			}
		}
	}
}

func (s *Server) handleTextMessage(client *ClientConn, data []byte) error {
	var msg WebSocketMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}

	switch msg.Channel {
	case "sidebar":
		return s.handleSidebarMessage(client, msg)
	case "control":
		return s.handleControlMessage(client, msg)
	default:
		return nil
	}
}

func (s *Server) handleSidebarMessage(client *ClientConn, msg WebSocketMessage) error {
	switch msg.Type {
	case "input":
		var payload daemon.InputPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		s.maybeSwitchSidebarClientFromInput(client, &payload)
		return s.sidebar.Send(daemon.Message{Type: daemon.MsgInput, Payload: payload})
	case "resize":
		var payload daemon.ResizePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		return s.sidebar.Send(daemon.Message{Type: daemon.MsgResize, Payload: payload})
	case "viewport_update":
		var payload daemon.ViewportUpdatePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		return s.sidebar.Send(daemon.Message{Type: daemon.MsgViewportUpdate, Payload: payload})
	case "menu_select":
		var payload daemon.MenuSelectPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		return s.sidebar.Send(daemon.Message{Type: daemon.MsgMenuSelect, Payload: payload})
	default:
		return nil
	}
}

func (s *Server) maybeSwitchSidebarClientFromInput(client *ClientConn, input *daemon.InputPayload) {
	if client == nil || input == nil {
		return
	}

	var windowID string
	var err error

	switch input.ResolvedAction {
	case "select_window":
		windowID, err = resolveWindowIDForTarget(input.ResolvedTarget)
	case "select_pane", "header_select_pane":
		windowID, err = resolveWindowIDForPane(input.ResolvedTarget)
	default:
		return
	}

	if err != nil || windowID == "" {
		if err != nil {
			log.Printf("resolve sidebar client from input failed: %v", err)
		}
		return
	}

	nextSidebarClientID := makeWebSidebarClientID(windowID, client.id)

	client.mu.Lock()
	prev := client.sidebarClientID
	if prev == nextSidebarClientID {
		client.mu.Unlock()
		return
	}
	client.sidebarClientID = nextSidebarClientID
	client.mu.Unlock()

	if err := s.sidebar.SwitchClient(nextSidebarClientID); err != nil {
		log.Printf("switch sidebar client from input failed: %v", err)
		return
	}

	s.maybeSwitchAttachedPaneFromInput(client, input, windowID)

	s.lastRenderMu.Lock()
	s.lastRender = nil
	s.lastRenderMu.Unlock()
}

func (s *Server) maybeSwitchAttachedPaneFromInput(client *ClientConn, input *daemon.InputPayload, windowID string) {
	if client == nil || input == nil {
		return
	}

	var nextPaneID string
	switch input.ResolvedAction {
	case "select_pane", "header_select_pane":
		nextPaneID = strings.TrimSpace(input.ResolvedTarget)
	case "select_window":
		if windowID == "" {
			return
		}
		paneID, err := resolveActivePaneForWindow(windowID)
		if err != nil {
			log.Printf("resolve active pane for window failed: %v", err)
			return
		}
		nextPaneID = paneID
	default:
		return
	}

	if nextPaneID == "" || !strings.HasPrefix(nextPaneID, "%") {
		return
	}

	client.mu.Lock()
	if client.attachedPaneID == nextPaneID {
		client.mu.Unlock()
		return
	}
	client.panes = map[string]struct{}{nextPaneID: struct{}{}}
	client.attachedPaneID = nextPaneID
	client.lastSnapshotPane = ""
	client.lastSnapshotHash = 0
	client.mu.Unlock()

	client.sendControlMessage("attached_pane", ControlAttachedPanePayload{PaneID: nextPaneID})
	s.pty.SelectPane(nextPaneID)
	s.setClientPtyMode(client, "snapshot", false)
	go s.pushPaneSnapshot(client, nextPaneID, true)
}

func (s *Server) handleControlMessage(client *ClientConn, msg WebSocketMessage) error {
	switch msg.Type {
	case "attach":
		var payload ControlAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		if payload.PaneID == "" {
			return nil
		}

		windowID, err := resolveWindowIDForPane(payload.PaneID)
		if err != nil {
			return err
		}
		sidebarClientID := makeWebSidebarClientID(windowID, client.id)
		client.mu.Lock()
		client.panes = map[string]struct{}{payload.PaneID: struct{}{}}
		client.attachedPaneID = payload.PaneID
		client.lastSnapshotPane = ""
		client.lastSnapshotHash = 0
		prevSidebarClientID := client.sidebarClientID
		client.sidebarClientID = sidebarClientID
		client.mu.Unlock()

		if err := s.sidebar.SwitchClient(sidebarClientID); err != nil {
			return err
		}
		client.sendControlMessage("attached_pane", ControlAttachedPanePayload{PaneID: payload.PaneID})
		s.pty.SelectPane(payload.PaneID)
		s.setClientPtyMode(client, "snapshot", false)
		go s.pushPaneSnapshot(client, payload.PaneID, true)
		if prevSidebarClientID != "" && prevSidebarClientID != sidebarClientID {
			s.lastRenderMu.Lock()
			s.lastRender = nil
			s.lastRenderMu.Unlock()
		}
		return nil
	case "detach":
		var payload ControlAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		var clearSidebarClientID string
		client.mu.Lock()
		delete(client.panes, payload.PaneID)
		if client.attachedPaneID == payload.PaneID {
			client.attachedPaneID = ""
		}
		if len(client.panes) == 0 {
			clearSidebarClientID = client.sidebarClientID
			client.sidebarClientID = ""
		}
		client.mu.Unlock()
		if clearSidebarClientID != "" {
			if err := s.sidebar.ClearClient(clearSidebarClientID); err != nil {
				return err
			}
		}
		return nil
	case "resize":
		var payload ControlResizePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		if payload.PaneID == "" {
			return nil
		}
		s.pty.Resize(payload.PaneID, payload.Cols, payload.Rows)
		return nil
	default:
		return nil
	}
}

func (s *Server) handleBinaryMessage(client *ClientConn, data []byte) error {
	if len(data) < 3 {
		return nil
	}
	frameType := data[0]
	if frameType != ptyFrameInput {
		return nil
	}
	sep := indexByte(data[1:], 0x00)
	if sep < 0 {
		return nil
	}
	sep = sep + 1
	paneID := string(data[1:sep])
	payload := data[sep+1:]
	if paneID == "" {
		return nil
	}
	s.pty.SendKeys(paneID, payload)
	return nil
}

func (s *Server) onPtyOutput(paneID string, data []byte) {
	frame := makePtyFrame(ptyFrameData, paneID, data)
	now := time.Now().UnixNano()

	s.mu.RLock()
	for client := range s.clients {
		if !client.hasPane(paneID) {
			continue
		}
		client.mu.Lock()
		client.lastPtyOutputAt = now
		client.mu.Unlock()
		s.setClientPtyMode(client, "streaming", true)
		client.sendBinary(frame)
	}
	s.mu.RUnlock()
}

func (s *Server) snapshotLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.RLock()
			clients := make([]*ClientConn, 0, len(s.clients))
			for c := range s.clients {
				clients = append(clients, c)
			}
			s.mu.RUnlock()

			focusWindowID, focusPaneID, focusErr := resolveActiveFocusForSession(s.cfg.SessionID)
			if focusErr != nil {
				focusWindowID = ""
				focusPaneID = ""
			}

			for _, client := range clients {
				paneID := s.syncClientFocus(client, focusWindowID, focusPaneID)
				client.mu.Lock()
				lastOutputAt := client.lastPtyOutputAt
				client.mu.Unlock()
				if paneID == "" {
					continue
				}
				if lastOutputAt != 0 && time.Since(time.Unix(0, lastOutputAt)) < 2500*time.Millisecond {
					continue
				}
				if s.pushPaneSnapshot(client, paneID, false) {
					s.setClientPtyMode(client, "snapshot", false)
				}
			}
		}
	}
}

func (s *Server) syncClientFocus(client *ClientConn, focusWindowID, focusPaneID string) string {
	if client == nil {
		return ""
	}

	client.mu.Lock()
	prevSidebarClientID := client.sidebarClientID
	currentPaneID := client.attachedPaneID
	targetPaneID := currentPaneID
	if strings.HasPrefix(focusPaneID, "%") {
		targetPaneID = focusPaneID
	}

	changedPane := false
	if targetPaneID != "" && targetPaneID != currentPaneID {
		client.panes = map[string]struct{}{targetPaneID: struct{}{}}
		client.attachedPaneID = targetPaneID
		client.lastSnapshotPane = ""
		client.lastSnapshotHash = 0
		changedPane = true
	}

	targetSidebarClientID := client.sidebarClientID
	changedSidebar := false
	if strings.HasPrefix(focusWindowID, "@") {
		desiredSidebarClientID := makeWebSidebarClientID(focusWindowID, client.id)
		if desiredSidebarClientID != client.sidebarClientID {
			client.sidebarClientID = desiredSidebarClientID
			targetSidebarClientID = desiredSidebarClientID
			changedSidebar = true
		}
	}

	resultPaneID := client.attachedPaneID
	client.mu.Unlock()

	if changedSidebar && targetSidebarClientID != "" {
		if err := s.sidebar.SwitchClient(targetSidebarClientID); err != nil {
			log.Printf("sync sidebar client failed: %v", err)
		}
		if prevSidebarClientID != "" && prevSidebarClientID != targetSidebarClientID {
			s.lastRenderMu.Lock()
			s.lastRender = nil
			s.lastRenderMu.Unlock()
		}
	}

	if changedPane && resultPaneID != "" {
		client.sendControlMessage("attached_pane", ControlAttachedPanePayload{PaneID: resultPaneID})
		s.pty.SelectPane(resultPaneID)
		go s.pushPaneSnapshot(client, resultPaneID, true)
	}

	return resultPaneID
}

func (s *Server) pushPaneSnapshot(client *ClientConn, paneID string, sendReset bool) bool {
	data, err := s.pty.CapturePaneData(paneID)
	if err != nil || len(data) == 0 {
		return false
	}

	hash := hashBinary(data)
	client.mu.Lock()
	if client.lastSnapshotPane == paneID && client.lastSnapshotHash == hash {
		client.mu.Unlock()
		return false
	}
	client.lastSnapshotPane = paneID
	client.lastSnapshotHash = hash
	client.mu.Unlock()

	if sendReset {
		client.sendControlMessage("pty_reset", struct{}{})
	}
	client.sendBinary(makePtyFrame(ptyFrameData, paneID, data))
	return true
}

func (s *Server) setClientPtyMode(client *ClientConn, mode string, healthy bool) {
	if client == nil || mode == "" {
		return
	}

	client.mu.Lock()
	if client.ptyMode == mode {
		client.mu.Unlock()
		return
	}
	client.ptyMode = mode
	client.mu.Unlock()

	client.sendControlMessage("pty_health", ControlPtyHealthPayload{Mode: mode, Healthy: healthy})
}

func (s *Server) onDaemonMessage(msg daemon.Message) {
	payload, err := json.Marshal(msg.Payload)
	if err != nil {
		return
	}

	out := WebSocketMessage{
		Channel: "sidebar",
		Type:    string(msg.Type),
		Payload: payload,
	}
	data, err := json.Marshal(out)
	if err != nil {
		return
	}

	if msg.Type == daemon.MsgRender {
		s.lastRenderMu.Lock()
		s.lastRender = data
		s.lastRenderMu.Unlock()
	}

	s.mu.RLock()
	for client := range s.clients {
		client.sendText(data)
	}
	s.mu.RUnlock()
}

func (s *Server) addClient(client *ClientConn) {
	s.mu.Lock()
	s.clients[client] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeClient(client *ClientConn) {
	s.mu.Lock()
	delete(s.clients, client)
	s.mu.Unlock()
}

func (c *ClientConn) hasPane(paneID string) bool {
	c.mu.RLock()
	_, ok := c.panes[paneID]
	c.mu.RUnlock()
	return ok
}

func (c *ClientConn) sendText(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.WriteMessage(websocket.TextMessage, data)
}

func (c *ClientConn) sendBinary(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (c *ClientConn) sendControlMessage(messageType string, payload interface{}) {
	out := WebSocketMessage{Channel: "control", Type: messageType}
	data, err := json.Marshal(payload)
	if err == nil {
		out.Payload = data
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return
	}
	c.sendText(encoded)
}

func (s *Server) validateToken(r *http.Request) bool {
	token := r.URL.Query().Get("token")
	return token != "" && token == s.cfg.Token
}

func (s *Server) validateAuth(r *http.Request, allowQuery bool) bool {
	user, pass, ok := r.BasicAuth()
	if ok {
		return user == s.cfg.AuthUser && pass == s.cfg.AuthPass
	}
	if !allowQuery {
		return false
	}
	user = r.URL.Query().Get("user")
	pass = r.URL.Query().Get("pass")
	return user != "" && pass != "" && user == s.cfg.AuthUser && pass == s.cfg.AuthPass
}

func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := originURL.Hostname()
	if originHost == "" {
		return false
	}
	requestHost, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		requestHost = r.Host
	}
	if originHost == requestHost {
		return true
	}
	if originHost == "localhost" || originHost == "127.0.0.1" {
		return true
	}
	return false
}

func makePtyFrame(frameType byte, paneID string, data []byte) []byte {
	frame := make([]byte, 0, 1+len(paneID)+1+len(data))
	frame = append(frame, frameType)
	frame = append(frame, []byte(paneID)...)
	frame = append(frame, 0x00)
	frame = append(frame, data...)
	return frame
}

func indexByte(data []byte, b byte) int {
	for i, v := range data {
		if v == b {
			return i
		}
	}
	return -1
}

func hashBinary(data []byte) uint32 {
	var h uint32
	for _, b := range data {
		h = h*31 + uint32(b)
	}
	return h
}

func intToString(value int) string {
	return strconv.Itoa(value)
}

func resolveWindowIDForPane(paneID string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{window_id}").Output()
	if err != nil {
		return "", err
	}
	windowID := strings.TrimSpace(string(out))
	if windowID == "" {
		return "", errors.New("empty window id for pane")
	}
	return windowID, nil
}

func resolveWindowIDForTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("empty target")
	}
	if strings.HasPrefix(target, "@") {
		return target, nil
	}
	if strings.HasPrefix(target, "%") {
		return resolveWindowIDForPane(target)
	}
	out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_index} #{window_id}").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == target {
			return fields[1], nil
		}
	}
	return "", errors.New("window target not found")
}

func resolveActivePaneForWindow(windowID string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	paneID := strings.TrimSpace(string(out))
	if paneID == "" {
		return "", errors.New("empty pane id for window")
	}
	return paneID, nil
}

func resolveActiveFocusForSession(sessionID string) (string, string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", sessionID, "#{window_id} #{pane_id}").Output()
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return "", "", errors.New("failed to parse active focus")
	}
	return fields[0], fields[1], nil
}

func makeWebSidebarClientID(windowID, clientInstanceID string) string {
	return fmt.Sprintf("%s#web-%s", windowID, clientInstanceID)
}
