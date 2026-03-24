package main

import (
	"os"
	"strings"
	"testing"
)

func TestParseFlags_Empty(t *testing.T) {
	if got := parseFlags(""); got != "" {
		t.Fatalf("parseFlags(\"\") = %q, want empty", got)
	}
}

func TestParseFlags_Bell(t *testing.T) {
	got := parseFlags("M")
	if !strings.Contains(got, "🔔") {
		t.Fatalf("parseFlags(\"M\") = %q, want 🔔", got)
	}
}

func TestParseFlags_Activity(t *testing.T) {
	got := parseFlags("!")
	if !strings.Contains(got, "●") {
		t.Fatalf("parseFlags(\"!\") = %q, want ●", got)
	}
}

func TestParseFlags_Silence(t *testing.T) {
	got := parseFlags("~")
	if !strings.Contains(got, "🔇") {
		t.Fatalf("parseFlags(\"~\") = %q, want 🔇", got)
	}
}

func TestParseFlags_Multiple(t *testing.T) {
	got := parseFlags("M!")
	if !strings.Contains(got, "🔔") || !strings.Contains(got, "●") {
		t.Fatalf("parseFlags(\"M!\") = %q, want bell and activity", got)
	}
}

func TestParseFlags_Unknown(t *testing.T) {
	if got := parseFlags("Z"); got != "" {
		t.Fatalf("parseFlags(\"Z\") = %q, want empty", got)
	}
}

func withArgs(args []string, fn func()) {
	old := os.Args
	os.Args = args
	defer func() { os.Args = old }()
	fn()
}

func TestMain_ActiveDefault(t *testing.T) {
	withArgs([]string{"cmd", "active", "1", "zsh"}, main)
}

func TestMain_InactiveDefault(t *testing.T) {
	withArgs([]string{"cmd", "inactive", "2", "vim"}, main)
}

func TestMain_ActiveSDPrefix(t *testing.T) {
	withArgs([]string{"cmd", "active", "3", "SD|sidebar"}, main)
}

func TestMain_InactiveSDPrefix(t *testing.T) {
	withArgs([]string{"cmd", "inactive", "3", "SD|sidebar"}, main)
}

func TestMain_ActiveGPPrefix(t *testing.T) {
	withArgs([]string{"cmd", "active", "4", "GP|gamepad"}, main)
}

func TestMain_InactiveGPPrefix(t *testing.T) {
	withArgs([]string{"cmd", "inactive", "4", "GP|gamepad"}, main)
}

func TestMain_WithFlags(t *testing.T) {
	withArgs([]string{"cmd", "active", "1", "zsh", "M!~"}, main)
}
