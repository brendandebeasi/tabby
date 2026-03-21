package main

import (
	"net/http"
	"testing"
)

func TestWebBridgePidPath_Default(t *testing.T) {
	got := webBridgePidPath("")
	if got == "" {
		t.Fatal("webBridgePidPath empty should return non-empty path")
	}
	if got[len(got)-4:] != ".pid" {
		t.Fatalf("webBridgePidPath should end in .pid, got %q", got)
	}
}

func TestWebBridgePidPath_WithSession(t *testing.T) {
	got := webBridgePidPath("$42")
	if got == "" {
		t.Fatal("webBridgePidPath with session should return non-empty path")
	}
	if got[len(got)-4:] != ".pid" {
		t.Fatalf("webBridgePidPath should end in .pid, got %q", got)
	}
}

func TestIsLoopbackRequest_Loopback(t *testing.T) {
	r := &http.Request{RemoteAddr: "127.0.0.1:54321"}
	if !isLoopbackRequest(r) {
		t.Fatal("127.0.0.1 should be loopback")
	}
}

func TestIsLoopbackRequest_IPv6Loopback(t *testing.T) {
	r := &http.Request{RemoteAddr: "[::1]:54321"}
	if !isLoopbackRequest(r) {
		t.Fatal("::1 should be loopback")
	}
}

func TestIsLoopbackRequest_PublicIP(t *testing.T) {
	r := &http.Request{RemoteAddr: "192.168.1.100:54321"}
	if isLoopbackRequest(r) {
		t.Fatal("192.168.1.100 should not be loopback")
	}
}

func TestIsLoopbackRequest_InvalidAddr(t *testing.T) {
	r := &http.Request{RemoteAddr: "not-an-ip"}
	if isLoopbackRequest(r) {
		t.Fatal("invalid address should not be loopback")
	}
}

func TestGenerateToken_Length(t *testing.T) {
	token, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken failed: %v", err)
	}
	if len(token) < 20 {
		t.Fatalf("generateToken should return long token, got len=%d", len(token))
	}
}

func TestGenerateToken_Unique(t *testing.T) {
	t1, _ := generateToken()
	t2, _ := generateToken()
	if t1 == t2 {
		t.Fatal("generateToken should return unique tokens each call")
	}
}
