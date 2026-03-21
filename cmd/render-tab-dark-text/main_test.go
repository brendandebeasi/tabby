package main

import (
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

func TestParseFlags_AllIndicators(t *testing.T) {
	got := parseFlags("M!~")
	if !strings.Contains(got, "🔔") || !strings.Contains(got, "●") || !strings.Contains(got, "🔇") {
		t.Fatalf("parseFlags(\"M!~\") = %q, want all indicators", got)
	}
}
