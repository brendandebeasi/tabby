package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestReadControlLineTrimNewline(t *testing.T) {
	input := "%output %1 hello world\n"
	reader := bufio.NewReader(strings.NewReader(input))

	line, err := readControlLine(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(line) != strings.TrimRight(input, "\n") {
		t.Fatalf("expected trimmed line, got %q", string(line))
	}
}

func TestReadControlLineRejectsOversize(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), maxControlLineBytes+1)
	reader := bufio.NewReader(bytes.NewReader(payload))

	_, err := readControlLine(reader)
	if err == nil {
		t.Fatalf("expected error for oversized line")
	}
}

func TestHexValue_Digits(t *testing.T) {
	for b := byte('0'); b <= '9'; b++ {
		if got := hexValue(b); got != int(b-'0') {
			t.Fatalf("hexValue(%c) = %d, want %d", b, got, int(b-'0'))
		}
	}
}

func TestHexValue_LowercaseHex(t *testing.T) {
	for i, b := range []byte("abcdef") {
		if got := hexValue(b); got != 10+i {
			t.Fatalf("hexValue(%c) = %d, want %d", b, got, 10+i)
		}
	}
}

func TestHexValue_UppercaseHex(t *testing.T) {
	for i, b := range []byte("ABCDEF") {
		if got := hexValue(b); got != 10+i {
			t.Fatalf("hexValue(%c) = %d, want %d", b, got, 10+i)
		}
	}
}

func TestHexValue_Invalid(t *testing.T) {
	for _, b := range []byte("gGzZ !") {
		if got := hexValue(b); got != -1 {
			t.Fatalf("hexValue(%c) = %d, want -1", b, got)
		}
	}
}

func TestUnescapeControlData_NoEscapes(t *testing.T) {
	input := []byte("hello world")
	got := unescapeControlData(input)
	if string(got) != "hello world" {
		t.Fatalf("unescapeControlData plain = %q, want %q", got, "hello world")
	}
}

func TestUnescapeControlData_Newline(t *testing.T) {
	input := []byte(`hello\nworld`)
	got := unescapeControlData(input)
	if string(got) != "hello\nworld" {
		t.Fatalf("unescapeControlData \\n = %q, want newline", got)
	}
}

func TestUnescapeControlData_Tab(t *testing.T) {
	input := []byte(`a\tb`)
	got := unescapeControlData(input)
	if string(got) != "a\tb" {
		t.Fatalf("unescapeControlData \\t = %q, want tab", got)
	}
}

func TestUnescapeControlData_CarriageReturn(t *testing.T) {
	input := []byte(`a\rb`)
	got := unescapeControlData(input)
	if string(got) != "a\rb" {
		t.Fatalf("unescapeControlData \\r = %q, want CR", got)
	}
}

func TestUnescapeControlData_Escape(t *testing.T) {
	input := []byte(`\e[31m`)
	got := unescapeControlData(input)
	if len(got) < 1 || got[0] != 0x1b {
		t.Fatalf("unescapeControlData \\e should produce ESC byte, got %q", got)
	}
}

func TestUnescapeControlData_BackslashBackslash(t *testing.T) {
	input := []byte(`a\\b`)
	got := unescapeControlData(input)
	if string(got) != `a\b` {
		t.Fatalf("unescapeControlData \\\\ = %q, want single backslash", got)
	}
}

func TestUnescapeControlData_HexEscape(t *testing.T) {
	input := []byte(`\x41`)
	got := unescapeControlData(input)
	if string(got) != "A" {
		t.Fatalf("unescapeControlData \\x41 = %q, want 'A'", got)
	}
}

func TestUnescapeControlData_OctalEscape(t *testing.T) {
	input := []byte(`\101`)
	got := unescapeControlData(input)
	if string(got) != "A" {
		t.Fatalf("unescapeControlData \\101 = %q, want 'A'", got)
	}
}

func TestUnescapeControlData_UnknownEscape(t *testing.T) {
	input := []byte(`\q`)
	got := unescapeControlData(input)
	if string(got) != `\q` {
		t.Fatalf("unescapeControlData \\q = %q, want literal \\q", got)
	}
}

func TestUnescapeControlData_EmptyInput(t *testing.T) {
	got := unescapeControlData([]byte{})
	if len(got) != 0 {
		t.Fatalf("unescapeControlData empty = %q, want empty", got)
	}
}

func TestUnescapeControlData_TrailingBackslash(t *testing.T) {
	input := []byte("abc\\")
	got := unescapeControlData(input)
	if len(got) < 1 {
		t.Fatal("unescapeControlData trailing backslash should not panic")
	}
}
