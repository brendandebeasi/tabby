package main

import (
	"net/http"
	"os"
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

func TestDefaultTokenPath_NonEmpty(t *testing.T) {
	got, err := DefaultTokenPath()
	if err != nil {
		t.Fatalf("DefaultTokenPath error: %v", err)
	}
	if got == "" {
		t.Fatal("DefaultTokenPath should return non-empty path")
	}
}

func TestRegenerateToken_WritesFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/web-token"
	token, err := RegenerateToken(path)
	if err != nil {
		t.Fatalf("RegenerateToken error: %v", err)
	}
	if len(token) < 20 {
		t.Fatalf("RegenerateToken returned short token: %q", token)
	}
}

func TestLoadOrGenerateToken_FromExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/token"
	expected := "my-existing-token"
	if err := writeFile(path, expected+"\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, err := LoadOrGenerateToken(path)
	if err != nil {
		t.Fatalf("LoadOrGenerateToken error: %v", err)
	}
	if got != expected {
		t.Fatalf("LoadOrGenerateToken = %q, want %q", got, expected)
	}
}

func TestLoadOrGenerateToken_GeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/missing-token"
	got, err := LoadOrGenerateToken(path)
	if err != nil {
		t.Fatalf("LoadOrGenerateToken error: %v", err)
	}
	if len(got) < 20 {
		t.Fatalf("LoadOrGenerateToken returned short token: %q", got)
	}
}

func TestLoadOrGenerateToken_RegeneratesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty-token"
	if err := writeFile(path, "\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, err := LoadOrGenerateToken(path)
	if err != nil {
		t.Fatalf("LoadOrGenerateToken error: %v", err)
	}
	if len(got) < 20 {
		t.Fatalf("expected generated token, got %q", got)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}
