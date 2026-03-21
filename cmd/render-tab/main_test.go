package main

import (
	"strings"
	"testing"
)

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
