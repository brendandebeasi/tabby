package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

type SidebarBridge struct {
	sessionID string
	conn      net.Conn
	sendMu    sync.Mutex
	onMessage func(msg daemon.Message)
	stopOnce  sync.Once
}

func NewSidebarBridge(sessionID string, onMessage func(msg daemon.Message)) *SidebarBridge {
	return &SidebarBridge{
		sessionID: sessionID,
		onMessage: onMessage,
	}
}

func (s *SidebarBridge) Start() error {
	sockPath := daemon.SocketPath(s.sessionID)
	var conn net.Conn
	var err error
	for i := 0; i < 10; i++ {
		conn, err = net.Dial("unix", sockPath)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	s.conn = conn

	if err := s.Send(daemon.Message{Type: daemon.MsgSubscribe, ClientID: "web-bridge"}); err != nil {
		return err
	}

	go s.readLoop()
	return nil
}

func (s *SidebarBridge) Stop() {
	s.stopOnce.Do(func() {
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

func (s *SidebarBridge) Send(msg daemon.Message) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.conn == nil {
		return fmt.Errorf("sidebar bridge not connected")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	_, err = s.conn.Write(append(data, '\n'))
	return err
}

func (s *SidebarBridge) readLoop() {
	scanner := bufio.NewScanner(s.conn)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg daemon.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			log.Printf("sidebar decode error: %v", err)
			continue
		}
		if s.onMessage != nil {
			s.onMessage(msg)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("sidebar read error: %v", err)
	}
}
