package renderer

import (
	"bufio"
	"encoding/json"
	"net"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// ReceiveMessages reads JSON-line daemon.Message values from conn and calls
// deliver for each successfully parsed message. Returns when the connection
// closes, the scanner errors, or deliver returns false (signalling "stop").
//
// This is the shared receive loop for all bubbletea renderers. They each
// wire deliver to convert the incoming daemon.Message into their own
// tea.Msg types (renderMsg, menuMsg, etc.) and forward to their program.
//
// The scanner buffer is sized for large render payloads (up to 1MB).
func ReceiveMessages(conn net.Conn, deliver func(daemon.Message) bool) {
	if conn == nil || deliver == nil {
		return
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg daemon.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if !deliver(msg) {
			return
		}
	}
}

// DecodePayload unmarshals msg.Payload into the supplied concrete type.
// Useful inside deliver callbacks: `var rp daemon.RenderPayload;
// renderer.DecodePayload(msg, &rp)`.
func DecodePayload(msg daemon.Message, out interface{}) bool {
	if msg.Payload == nil {
		return false
	}
	raw, err := json.Marshal(msg.Payload)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}
