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
