package renderer

import (
	"encoding/json"
	"net"
	"sync"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// SendMessage writes a single daemon.Message to conn as a JSON line.
// mu (if non-nil) serializes concurrent writes on the same connection — pass
// the per-connection mutex the caller owns. writeTimeout defaults to 1s when
// zero is passed.
//
// A nil conn is a no-op (returns nil). This matches the legacy behavior
// of the renderers' sendMessage methods, which silently dropped messages
// between disconnect and reconnect.
func SendMessage(conn net.Conn, mu *sync.Mutex, msg daemon.Message, writeTimeout time.Duration) error {
	if conn == nil {
		return nil
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if writeTimeout <= 0 {
		writeTimeout = time.Second
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	_, err = conn.Write(append(data, '\n'))
	return err
}

// Subscribe sends a MsgSubscribe with the given Target and initial resize
// payload. Renderers call this immediately after Connect succeeds.
func Subscribe(conn net.Conn, mu *sync.Mutex, target daemon.RenderTarget, width, height int, colorProfile, paneID string) error {
	return SendMessage(conn, mu, daemon.Message{
		Type:   daemon.MsgSubscribe,
		Target: target,
		Payload: daemon.ResizePayload{
			Width:        width,
			Height:       height,
			ColorProfile: colorProfile,
			PaneID:       paneID,
		},
	}, 0)
}

// Unsubscribe sends a MsgUnsubscribe. Renderers call this on shutdown to
// tell the daemon to drop their client entry cleanly.
func Unsubscribe(conn net.Conn, mu *sync.Mutex, target daemon.RenderTarget) error {
	return SendMessage(conn, mu, daemon.Message{
		Type:   daemon.MsgUnsubscribe,
		Target: target,
	}, 0)
}

// Resize sends a MsgResize with updated dimensions.
func Resize(conn net.Conn, mu *sync.Mutex, target daemon.RenderTarget, width, height int, paneID string) error {
	return SendMessage(conn, mu, daemon.Message{
		Type:   daemon.MsgResize,
		Target: target,
		Payload: daemon.ResizePayload{
			Width:  width,
			Height: height,
			PaneID: paneID,
		},
	}, 0)
}

// Ping sends a MsgPing. Renderers drive this on a tick for connection health.
func Ping(conn net.Conn, mu *sync.Mutex, target daemon.RenderTarget) error {
	return SendMessage(conn, mu, daemon.Message{
		Type:   daemon.MsgPing,
		Target: target,
	}, 0)
}

// Input sends a MsgInput with the given payload.
func Input(conn net.Conn, mu *sync.Mutex, target daemon.RenderTarget, input *daemon.InputPayload) error {
	return SendMessage(conn, mu, daemon.Message{
		Type:    daemon.MsgInput,
		Target:  target,
		Payload: input,
	}, 0)
}

// ViewportUpdate sends a MsgViewportUpdate with the given scroll offset.
// Only the sidebar renderer currently uses this.
func ViewportUpdate(conn net.Conn, mu *sync.Mutex, target daemon.RenderTarget, offset int) error {
	return SendMessage(conn, mu, daemon.Message{
		Type:   daemon.MsgViewportUpdate,
		Target: target,
		Payload: daemon.ViewportUpdatePayload{
			ViewportOffset: offset,
		},
	}, 0)
}
