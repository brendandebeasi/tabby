package main

import "testing"

func TestBuildBorderStyle(t *testing.T) {
	if got := buildBorderStyle("", "#000"); got != "" {
		t.Fatalf("buildBorderStyle empty fg = %q, want empty", got)
	}

	if got := buildBorderStyle("#fff", ""); got != "fg=#fff" {
		t.Fatalf("buildBorderStyle without bg = %q, want %q", got, "fg=#fff")
	}

	if got := buildBorderStyle("#fff", "#000"); got != "fg=#fff,bg=#000" {
		t.Fatalf("buildBorderStyle with bg = %q, want %q", got, "fg=#fff,bg=#000")
	}
}

func TestShortenPath(t *testing.T) {
	home := "/Users/b"

	if got := shortenPath("/", home); got != "/" {
		t.Fatalf("shortenPath root = %q, want /", got)
	}
	if got := shortenPath(home, home); got != "~" {
		t.Fatalf("shortenPath home = %q, want ~", got)
	}
	if got := shortenPath("/Users/b/git/tabby", home); got != "tabby" {
		t.Fatalf("shortenPath normal path = %q, want tabby", got)
	}
}
