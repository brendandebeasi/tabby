package main

import (
	"os"
	"strings"
	"testing"
)

func withArgs(args []string, fn func()) {
	old := os.Args
	os.Args = args
	defer func() { os.Args = old }()
	fn()
}

func TestStripANSI_NoEscapes(t *testing.T) {
	if got := stripANSI("hello"); got != "hello" {
		t.Fatalf("stripANSI plain text = %q, want %q", got, "hello")
	}
}

func TestStripANSI_ColorCode(t *testing.T) {
	input := "\x1b[31mred\x1b[0m"
	got := stripANSI(input)
	if got != "red" {
		t.Fatalf("stripANSI color code = %q, want %q", got, "red")
	}
}

func TestStripANSI_Empty(t *testing.T) {
	if got := stripANSI(""); got != "" {
		t.Fatalf("stripANSI empty = %q, want empty", got)
	}
}

func TestStripANSI_256Color(t *testing.T) {
	input := "\x1b[38;5;196mtext\x1b[0m"
	got := stripANSI(input)
	if got != "text" {
		t.Fatalf("stripANSI 256-color = %q, want %q", got, "text")
	}
}

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

func TestParseFlags_All(t *testing.T) {
	got := parseFlags("M!~")
	if !strings.Contains(got, "🔔") || !strings.Contains(got, "●") || !strings.Contains(got, "🔇") {
		t.Fatalf("parseFlags(\"M!~\") = %q, want all three indicators", got)
	}
}

func TestParseFlags_NoMatch(t *testing.T) {
	if got := parseFlags("XYZ"); got != "" {
		t.Fatalf("parseFlags(\"XYZ\") = %q, want empty", got)
	}
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

func TestMain_WithFlags(t *testing.T) {
	withArgs([]string{"cmd", "active", "1", "zsh", "M!~"}, main)
}

func TestMain_ANSIInName(t *testing.T) {
	withArgs([]string{"cmd", "active", "1", "\x1b[31mred\x1b[0m"}, main)
}
