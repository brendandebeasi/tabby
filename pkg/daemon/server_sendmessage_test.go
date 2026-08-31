package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
)

func sendMessageCases() []Message {
	return []Message{
		{Type: MessageType("render"), Target: RenderTarget{Kind: TargetSidebar, WindowID: "@7"}},
		{Type: MessageType("render"), Target: RenderTarget{Kind: TargetSidebar, WindowID: "@7"}, Payload: map[string]any{
			"content": "line one\nline two\n",
			"width":   26,
		}},
		// Characters json.Marshal HTML-escapes, so the pooled encoder has to
		// escape them the same way or clients see different bytes.
		{Type: MessageType("render"), Payload: map[string]any{"content": `<b>&"x"</b> & more`}},
		{Type: MessageType("render"), Payload: map[string]any{"content": "\x1b[48;2;1;2;3m tab \x1b[0m ☁️ 1️⃣"}},
		{Type: MessageType("render"), Payload: map[string]any{"content": strings.Repeat("padding ", 4096)}},
	}
}

// The pooled encoder replaced json.Marshal plus a manual newline. The bytes on
// the wire have to be identical, or every client parsing them is now guessing.
func TestSendMessageWireBytesMatchMarshalPlusNewline(t *testing.T) {
	for i, msg := range sendMessageCases() {
		want, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("case %d: marshal: %v", i, err)
		}
		want = append(want, '\n')

		got := captureSend(t, msg)
		if got != string(want) {
			t.Fatalf("case %d:\n got %q\nwant %q", i, got, want)
		}
	}
}

// A pooled buffer that is not reset, or is shared across goroutines, shows up
// as one payload carrying a fragment of another.
func TestSendMessageIsSafeUnderConcurrentSends(t *testing.T) {
	cases := sendMessageCases()
	want := make([]string, len(cases))
	for i, msg := range cases {
		b, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want[i] = string(b) + "\n"
	}

	var wg sync.WaitGroup
	errs := make(chan string, 200)
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, msg := range cases {
				if got := captureSend(t, msg); got != want[i] {
					errs <- got
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	if bad, ok := <-errs; ok {
		t.Fatalf("a concurrent send produced the wrong bytes: %q", bad)
	}
}

// An outsized message must not pin its buffer in the pool. Send a big one, then
// a small one, and check the small one is still correct: the size guard drops
// the grown buffer rather than reusing it.
func TestSendMessageSurvivesAnOutsizedPayload(t *testing.T) {
	huge := Message{Type: MessageType("render"), Payload: map[string]any{
		"content": strings.Repeat("x", maxPooledMsgBuf+1024),
	}}
	if got := captureSend(t, huge); len(got) < maxPooledMsgBuf {
		t.Fatalf("outsized send returned %d bytes, want at least %d", len(got), maxPooledMsgBuf)
	}

	small := Message{Type: MessageType("render"), Target: RenderTarget{Kind: TargetSidebar, WindowID: "@7"}}
	want, err := json.Marshal(small)
	if err != nil {
		t.Fatal(err)
	}
	if got := captureSend(t, small); got != string(want)+"\n" {
		t.Fatalf("send after an outsized payload:\n got %q\nwant %q", got, string(want)+"\n")
	}
}

// captureSend runs sendMessage over a real socket pair and returns the line
// that came out the other end.
func captureSend(t *testing.T, msg Message) string {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var (
		s      Server
		out    string
		readMu sync.WaitGroup
	)

	readMu.Add(1)
	go func() {
		defer readMu.Done()
		line, err := bufio.NewReaderSize(client, 1<<21).ReadString('\n')
		if err != nil {
			t.Errorf("read: %v", err)
		}
		out = line
	}()

	if err := s.sendMessage(server, msg); err != nil {
		t.Fatalf("sendMessage: %v", err)
	}
	readMu.Wait()
	return out
}
