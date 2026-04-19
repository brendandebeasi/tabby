// Package renderer provides shared infrastructure for tabby's bubbletea
// renderer binaries (sidebar, window-header, pane-header, sidebar-popup).
// It owns the bits that were copy-pasted-and-drifted across main.go files:
// socket connect with retry, JSON message send helpers, the receive loop,
// and the terminal reset escape sequences.
package renderer

import (
	"net"
	"time"
)

// Connect dials the daemon Unix socket at sockPath, retrying with a fixed
// delay between attempts. Returns the connection on the first success, or
// the last error after all retries fail.
//
// Callers typically pass retries=10 and delay=100ms to match the legacy
// behavior; tighter loops (5ms delay) are also reasonable when the daemon
// is known to have just started.
func Connect(sockPath string, retries int, delay time.Duration) (net.Conn, error) {
	var (
		conn net.Conn
		err  error
	)
	if retries < 1 {
		retries = 1
	}
	for i := 0; i < retries; i++ {
		conn, err = net.Dial("unix", sockPath)
		if err == nil {
			return conn, nil
		}
		if i < retries-1 {
			time.Sleep(delay)
		}
	}
	return nil, err
}
