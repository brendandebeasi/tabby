package main

import (
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

func newTestServer(token, user, pass string) *Server {
	return &Server{
		cfg: ServerConfig{
			Token:    token,
			AuthUser: user,
			AuthPass: pass,
		},
		clients: make(map[*ClientConn]struct{}),
		stopCh:  make(chan struct{}),
	}
}

func TestValidateToken_Match(t *testing.T) {
	s := newTestServer("secret", "", "")
	r := &http.Request{URL: &url.URL{RawQuery: "token=secret"}}
	if !s.validateToken(r) {
		t.Fatal("matching token should validate")
	}
}

func TestValidateToken_Mismatch(t *testing.T) {
	s := newTestServer("secret", "", "")
	r := &http.Request{URL: &url.URL{RawQuery: "token=wrong"}}
	if s.validateToken(r) {
		t.Fatal("wrong token should not validate")
	}
}

func TestValidateToken_Empty(t *testing.T) {
	s := newTestServer("secret", "", "")
	r := &http.Request{URL: &url.URL{RawQuery: ""}}
	if s.validateToken(r) {
		t.Fatal("empty token should not validate")
	}
}

func TestValidateAuth_BasicAuth(t *testing.T) {
	s := newTestServer("", "alice", "hunter2")
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth("alice", "hunter2")
	if !s.validateAuth(r, false) {
		t.Fatal("correct basic auth should validate")
	}
}

func TestValidateAuth_WrongBasicAuth(t *testing.T) {
	s := newTestServer("", "alice", "hunter2")
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth("alice", "wrong")
	if s.validateAuth(r, false) {
		t.Fatal("wrong basic auth should not validate")
	}
}

func TestValidateAuth_QueryParam_Allowed(t *testing.T) {
	s := newTestServer("", "alice", "hunter2")
	r := &http.Request{URL: &url.URL{RawQuery: "user=alice&pass=hunter2"}}
	if !s.validateAuth(r, true) {
		t.Fatal("correct query params should validate when allowed")
	}
}

func TestValidateAuth_QueryParam_NotAllowed(t *testing.T) {
	s := newTestServer("", "alice", "hunter2")
	r := &http.Request{URL: &url.URL{RawQuery: "user=alice&pass=hunter2"}}
	if s.validateAuth(r, false) {
		t.Fatal("query params should not validate when not allowed")
	}
}

func TestCheckOrigin_NoOrigin(t *testing.T) {
	s := newTestServer("", "", "")
	r := &http.Request{Header: http.Header{}, Host: "localhost:8080"}
	if !s.checkOrigin(r) {
		t.Fatal("no origin header should be allowed")
	}
}

func TestCheckOrigin_SameHost(t *testing.T) {
	s := newTestServer("", "", "")
	r := &http.Request{
		Header: http.Header{"Origin": []string{"http://localhost:8080"}},
		Host:   "localhost:8080",
	}
	if !s.checkOrigin(r) {
		t.Fatal("same-host origin should be allowed")
	}
}

func TestCheckOrigin_LoopbackOrigin(t *testing.T) {
	s := newTestServer("", "", "")
	r := &http.Request{
		Header: http.Header{"Origin": []string{"http://127.0.0.1:3000"}},
		Host:   "localhost:8080",
	}
	if !s.checkOrigin(r) {
		t.Fatal("127.0.0.1 origin should be allowed")
	}
}

func TestCheckOrigin_ForeignOrigin(t *testing.T) {
	s := newTestServer("", "", "")
	r := &http.Request{
		Header: http.Header{"Origin": []string{"http://evil.com"}},
		Host:   "localhost:8080",
	}
	if s.checkOrigin(r) {
		t.Fatal("foreign origin should not be allowed")
	}
}

func TestHasPane_Found(t *testing.T) {
	c := &ClientConn{
		panes: map[string]struct{}{"%1": {}},
		mu:    sync.RWMutex{},
	}
	if !c.hasPane("%1") {
		t.Fatal("hasPane should find %1")
	}
}

func TestHasPane_NotFound(t *testing.T) {
	c := &ClientConn{
		panes: map[string]struct{}{"%1": {}},
		mu:    sync.RWMutex{},
	}
	if c.hasPane("%99") {
		t.Fatal("hasPane should not find %99")
	}
}

func TestHasPane_EmptyPanes(t *testing.T) {
	c := &ClientConn{
		panes: map[string]struct{}{},
		mu:    sync.RWMutex{},
	}
	if c.hasPane("%1") {
		t.Fatal("hasPane should not find anything in empty map")
	}
}

func TestIndexByte_Found(t *testing.T) {
	data := []byte("hello\x00world")
	if got := indexByte(data, '\x00'); got != 5 {
		t.Fatalf("indexByte = %d, want 5", got)
	}
}

func TestIndexByte_NotFound(t *testing.T) {
	data := []byte("hello")
	if got := indexByte(data, '\x00'); got != -1 {
		t.Fatalf("indexByte = %d, want -1", got)
	}
}

func TestIndexByte_Empty(t *testing.T) {
	if got := indexByte(nil, 'x'); got != -1 {
		t.Fatalf("indexByte empty = %d, want -1", got)
	}
}

func TestHashBinary_Stable(t *testing.T) {
	data := []byte("hello world")
	h1 := hashBinary(data)
	h2 := hashBinary(data)
	if h1 != h2 {
		t.Fatal("hashBinary should be deterministic")
	}
}

func TestHashBinary_Different(t *testing.T) {
	h1 := hashBinary([]byte("abc"))
	h2 := hashBinary([]byte("xyz"))
	if h1 == h2 {
		t.Fatal("different inputs should produce different hashes")
	}
}

func TestHashBinary_Empty(t *testing.T) {
	h := hashBinary(nil)
	if h != 0 {
		t.Fatalf("hashBinary(nil) = %d, want 0", h)
	}
}

func TestIntToString(t *testing.T) {
	if intToString(42) != "42" {
		t.Fatal("intToString(42) should be '42'")
	}
	if intToString(0) != "0" {
		t.Fatal("intToString(0) should be '0'")
	}
	if intToString(-1) != "-1" {
		t.Fatal("intToString(-1) should be '-1'")
	}
}

func TestNewServer_ReturnsServer(t *testing.T) {
	cfg := ServerConfig{
		Host:      "127.0.0.1",
		Port:      9999,
		SessionID: "test",
		Token:     "tok",
	}
	s := NewServer(cfg)
	if s == nil {
		t.Fatal("NewServer should return non-nil")
	}
	if s.cfg.Token != "tok" {
		t.Fatalf("server token = %q, want 'tok'", s.cfg.Token)
	}
	if s.clients == nil {
		t.Fatal("server clients map should be initialized")
	}
}

func TestAddRemoveClient(t *testing.T) {
	s := newTestServer("", "", "")
	c := &ClientConn{panes: map[string]struct{}{}, mu: sync.RWMutex{}}
	s.addClient(c)
	s.mu.RLock()
	_, found := s.clients[c]
	s.mu.RUnlock()
	if !found {
		t.Fatal("addClient should add client to map")
	}
	s.removeClient(c)
	s.mu.RLock()
	_, found = s.clients[c]
	s.mu.RUnlock()
	if found {
		t.Fatal("removeClient should remove client from map")
	}
}

func TestNewSidebarBridge_ReturnsNonNil(t *testing.T) {
	sb := NewSidebarBridge("test-session", nil)
	if sb == nil {
		t.Fatal("NewSidebarBridge should return non-nil")
	}
	if sb.sessionID != "test-session" {
		t.Fatalf("sessionID = %q, want 'test-session'", sb.sessionID)
	}
}

func TestMakeWebSidebarClientID(t *testing.T) {
	got := makeWebSidebarClientID("@1", "abc123")
	if got != "@1#web-abc123" {
		t.Fatalf("makeWebSidebarClientID = %q, want %q", got, "@1#web-abc123")
	}
}

func TestMakeWebSidebarClientID_Empty(t *testing.T) {
	got := makeWebSidebarClientID("", "")
	if got != "#web-" {
		t.Fatalf("makeWebSidebarClientID empty = %q", got)
	}
}

func TestSidebarBridge_Stop_NilConn(t *testing.T) {
	sb := NewSidebarBridge("session", nil)
	sb.Stop()
	sb.Stop()
}

func TestSidebarBridge_Send_NilConn(t *testing.T) {
	sb := NewSidebarBridge("session", nil)
	err := sb.Send(daemon.Message{Type: "ping"})
	if err == nil {
		t.Fatal("Send with nil conn should return error")
	}
}

func TestSidebarBridge_ClearClient_Empty(t *testing.T) {
	sb := NewSidebarBridge("session", nil)
	if err := sb.ClearClient(""); err != nil {
		t.Fatalf("ClearClient empty should return nil, got %v", err)
	}
}

func TestSidebarBridge_ClearClient_NonMatching(t *testing.T) {
	sb := NewSidebarBridge("session", nil)
	sb.clientID = "other-client"
	if err := sb.ClearClient("different-client"); err != nil {
		t.Fatalf("ClearClient non-matching should return nil, got %v", err)
	}
}

func TestSidebarBridge_SwitchClient_Empty(t *testing.T) {
	sb := NewSidebarBridge("session", nil)
	if err := sb.SwitchClient(""); err != nil {
		t.Fatalf("SwitchClient empty should return nil, got %v", err)
	}
}

func TestSidebarBridge_SwitchClient_Same(t *testing.T) {
	sb := NewSidebarBridge("session", nil)
	sb.clientID = "my-client"
	if err := sb.SwitchClient("my-client"); err != nil {
		t.Fatalf("SwitchClient same should return nil, got %v", err)
	}
}

func TestCheckOrigin_InvalidURL(t *testing.T) {
	s := newTestServer("", "", "")
	r := &http.Request{
		Header: http.Header{"Origin": []string{"://bad-url"}},
		Host:   "localhost:8080",
	}
	if s.checkOrigin(r) {
		t.Fatal("invalid origin URL should be rejected")
	}
}

func TestCheckOrigin_EmptyHostname(t *testing.T) {
	s := newTestServer("", "", "")
	r := &http.Request{
		Header: http.Header{"Origin": []string{"file://"}},
		Host:   "localhost:8080",
	}
	if s.checkOrigin(r) {
		t.Fatal("origin with empty hostname should be rejected")
	}
}
