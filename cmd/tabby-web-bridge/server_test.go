package main

import "testing"

func TestMakePtyFrame(t *testing.T) {
	paneID := "%12"
	data := []byte("ls -la")
	frame := makePtyFrame(ptyFrameData, paneID, data)
	if len(frame) != 1+len(paneID)+1+len(data) {
		t.Fatalf("unexpected frame length: %d", len(frame))
	}
	if frame[0] != ptyFrameData {
		t.Fatalf("expected frame type %d, got %d", ptyFrameData, frame[0])
	}
	if string(frame[1:1+len(paneID)]) != paneID {
		t.Fatalf("pane id mismatch: %q", string(frame[1:1+len(paneID)]))
	}
	if frame[1+len(paneID)] != 0x00 {
		t.Fatalf("missing separator byte")
	}
	if string(frame[1+len(paneID)+1:]) != string(data) {
		t.Fatalf("payload mismatch: %q", string(frame[1+len(paneID)+1:]))
	}
}
