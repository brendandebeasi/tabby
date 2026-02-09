package main

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/b/tmux-tabs/pkg/daemon"
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
	cfg        ServerConfig
	pty        *ControlModeSession
	sidebar    *SidebarBridge
	clients    map[*ClientConn]struct{}
	mu         sync.RWMutex
	httpServer *http.Server
	upgrader   websocket.Upgrader
}

type ClientConn struct {
	conn  *websocket.Conn
	panes map[string]struct{}
	mu    sync.Mutex
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

func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		cfg:     cfg,
		clients: make(map[*ClientConn]struct{}),
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

	return nil
}

func (s *Server) Stop() {
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
		conn:  conn,
		panes: make(map[string]struct{}),
	}
	s.addClient(client)

	go s.readLoop(client)
}

func (s *Server) readLoop(client *ClientConn) {
	defer func() {
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
		return s.handleSidebarMessage(msg)
	case "control":
		return s.handleControlMessage(client, msg)
	default:
		return nil
	}
}

func (s *Server) handleSidebarMessage(msg WebSocketMessage) error {
	switch msg.Type {
	case "input":
		var payload daemon.InputPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
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
		client.mu.Lock()
		client.panes[payload.PaneID] = struct{}{}
		client.mu.Unlock()
		return nil
	case "detach":
		var payload ControlAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return err
		}
		client.mu.Lock()
		delete(client.panes, payload.PaneID)
		client.mu.Unlock()
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

	s.mu.RLock()
	for client := range s.clients {
		if !client.hasPane(paneID) {
			continue
		}
		client.sendBinary(frame)
	}
	s.mu.RUnlock()
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
	c.mu.Lock()
	_, ok := c.panes[paneID]
	c.mu.Unlock()
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

func intToString(value int) string {
	return strconv.Itoa(value)
}
