// Package theme is the `tabby theme` subcommand: it reports and flips the
// light/dark theme selection for the current session.
//
// The daemon owns the selection, so every op is a single request/response
// over the session's unix socket. The daemon persists the choice to
// config.yaml and repaints attached renderers.
package theme

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// Run is the subcommand entry point. args are the tokens following "theme".
func Run(args []string) int {
	req := &daemon.ThemeRequest{Op: daemon.ThemeOpToggle}
	switch {
	case len(args) == 0:
		req.Op = daemon.ThemeOpGet
	case args[0] == "toggle":
		req.Op = daemon.ThemeOpToggle
	case args[0] == "light" || args[0] == "dark":
		req.Op = daemon.ThemeOpSet
		req.Mode = args[0]
	case args[0] == "-h" || args[0] == "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "tabby theme: unknown argument %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}

	resp, err := request(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "Error:", resp.Error)
		return 1
	}

	if req.Op == daemon.ThemeOpGet && !resp.Enabled {
		fmt.Printf("theme: %s (light/dark toggle disabled — run `tabby setup` to pair two themes)\n", resp.Theme)
		return 0
	}
	fmt.Printf("%s → %s\n", resp.Mode, resp.Theme)
	return 0
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: tabby theme [toggle|light|dark]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  (no args)  show the current selection")
	fmt.Fprintln(w, "  toggle     switch between the configured light and dark themes")
	fmt.Fprintln(w, "  light      select the light theme")
	fmt.Fprintln(w, "  dark       select the dark theme")
}

// request dials the daemon socket for the current tmux session, sends one
// MsgTheme, reads one response, and returns the parsed ThemeResponse.
func request(req *daemon.ThemeRequest) (*daemon.ThemeResponse, error) {
	sessionID, err := currentSessionID()
	if err != nil {
		return nil, fmt.Errorf("tabby daemon not running in this session — start tabby first.")
	}
	conn, err := net.DialTimeout("unix", daemon.SocketPath(sessionID), 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("tabby daemon not running in this session — start tabby first.")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	data, err := json.Marshal(daemon.Message{Type: daemon.MsgTheme, Payload: req})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("daemon closed connection without a response")
	}
	var respMsg daemon.Message
	if err := json.Unmarshal(scanner.Bytes(), &respMsg); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if respMsg.Type != daemon.MsgTheme {
		return nil, fmt.Errorf("unexpected response type %q", respMsg.Type)
	}
	payloadBytes, err := json.Marshal(respMsg.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode response payload: %w", err)
	}
	var resp daemon.ThemeResponse
	if err := json.Unmarshal(payloadBytes, &resp); err != nil {
		return nil, fmt.Errorf("decode response payload: %w", err)
	}
	return &resp, nil
}

// currentSessionID returns the tmux session id ("$2") for the caller's pane.
func currentSessionID() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("no active tmux session")
	}
	return id, nil
}
