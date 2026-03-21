package daemon

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBroadcastRender_NoClients(t *testing.T) {
	s := newTestServer(t)
	assert.NotPanics(t, func() { s.BroadcastRender() })
}

func TestBroadcastRender_NilCallbackNoClients(t *testing.T) {
	s := newTestServer(t)
	s.clients["@1"] = &ClientInfo{Width: 80, Height: 24}
	assert.NotPanics(t, func() { s.BroadcastRender() })
}

func TestBroadcastRender_CallsRenderForEachClient(t *testing.T) {
	s := newTestServer(t)
	s.clients["@1"] = &ClientInfo{Width: 80, Height: 24}
	s.clients["@2"] = &ClientInfo{Width: 80, Height: 24}

	called := make(map[string]bool)
	s.OnRenderNeeded = func(clientID string, _, _ int) *RenderPayload {
		called[clientID] = true
		return nil
	}

	s.BroadcastRender()
	assert.True(t, called["@1"])
	assert.True(t, called["@2"])
}

func TestBroadcastRender_DebugLogCalled(t *testing.T) {
	s := newTestServer(t)
	s.clients["@1"] = &ClientInfo{Width: 80, Height: 24}

	var logCalls int
	s.DebugLog = func(format string, args ...interface{}) { logCalls++ }
	s.BroadcastRender()
	assert.Greater(t, logCalls, 0)
}

func TestSendRenderToClient_ClientNotFound(t *testing.T) {
	s := newTestServer(t)
	assert.NotPanics(t, func() { s.SendRenderToClient("ghost") })
}

func TestSendRenderToClient_NilCallback(t *testing.T) {
	s := newTestServer(t)
	s.clients["@1"] = &ClientInfo{Width: 80, Height: 24}
	assert.NotPanics(t, func() { s.SendRenderToClient("@1") })
}

func TestSendRenderToClient_CallbackReturnsNil(t *testing.T) {
	s := newTestServer(t)
	s.clients["@1"] = &ClientInfo{Width: 80, Height: 24}
	s.OnRenderNeeded = func(clientID string, _, _ int) *RenderPayload { return nil }
	assert.NotPanics(t, func() { s.SendRenderToClient("@1") })
}

func TestSendRenderToClient_DeduplicatesSameContent(t *testing.T) {
	s := newTestServer(t)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	s.clients["@1"] = &ClientInfo{Conn: serverConn, Width: 80, Height: 24}

	callCount := 0
	s.OnRenderNeeded = func(clientID string, _, _ int) *RenderPayload {
		callCount++
		return &RenderPayload{Content: "stable content"}
	}

	received := make(chan []byte, 10)
	go func() {
		buf := make([]byte, 4096)
		for {
			clientConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, err := clientConn.Read(buf)
			if err != nil {
				return
			}
			cp := make([]byte, n)
			copy(cp, buf[:n])
			received <- cp
		}
	}()

	s.SendRenderToClient("@1")
	s.SendRenderToClient("@1")
	s.SendRenderToClient("@1")

	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 1, len(received), "duplicate content should only be sent once")
}

func TestSendRenderToClient_SendsOnContentChange(t *testing.T) {
	s := newTestServer(t)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	s.clients["@1"] = &ClientInfo{Conn: serverConn, Width: 80, Height: 24}

	content := "first"
	s.OnRenderNeeded = func(clientID string, _, _ int) *RenderPayload {
		return &RenderPayload{Content: content}
	}

	received := make(chan []byte, 10)
	go func() {
		buf := make([]byte, 4096)
		for {
			clientConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, err := clientConn.Read(buf)
			if err != nil {
				return
			}
			cp := make([]byte, n)
			copy(cp, buf[:n])
			received <- cp
		}
	}()

	s.SendRenderToClient("@1")
	content = "second"
	s.SendRenderToClient("@1")

	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 2, len(received), "changed content should trigger two sends")
}

func TestSendRenderToClient_SequenceNumberIncremented(t *testing.T) {
	s := newTestServer(t)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	s.clients["@1"] = &ClientInfo{Conn: serverConn, Width: 80, Height: 24}

	contentIdx := 0
	contents := []string{"alpha", "beta"}
	s.OnRenderNeeded = func(clientID string, _, _ int) *RenderPayload {
		c := contents[contentIdx]
		contentIdx++
		return &RenderPayload{Content: c}
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if _, err := clientConn.Read(buf); err != nil {
				return
			}
		}
	}()

	before := s.sequenceNum
	s.SendRenderToClient("@1")
	s.SendRenderToClient("@1")
	time.Sleep(20 * time.Millisecond)
	assert.Greater(t, s.sequenceNum, before)
}

func TestSendMessage_NilConn(t *testing.T) {
	s := newTestServer(t)
	err := s.sendMessage(nil, Message{Type: MsgPong})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil connection")
}

func TestSendMessage_ValidConn(t *testing.T) {
	s := newTestServer(t)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	received := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := clientConn.Read(buf)
		received <- string(buf[:n])
	}()

	err := s.sendMessage(serverConn, Message{Type: MsgPong, ClientID: "test"})
	assert.NoError(t, err)

	data := <-received
	assert.Contains(t, data, string(MsgPong))
}

func TestSendMessage_ClosedConn(t *testing.T) {
	s := newTestServer(t)
	serverConn, clientConn := net.Pipe()
	clientConn.Close()
	defer serverConn.Close()

	err := s.sendMessage(serverConn, Message{Type: MsgPong})
	assert.Error(t, err)
}
