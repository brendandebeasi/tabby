// Package dashboardlayout implements the `tabby dashboard-layout` subcommand: a
// thin client (bound to prefix+L) that asks the daemon to open the ASCII
// dashboard layout-style picker popup.
//
// The picker itself (an ASCII preview of each native tmux arrangement) is the
// `tabby render dash-layout-popup` binary; the daemon launches it via
// display-popup so the popup attaches to the user's client. This subcommand
// only nudges the daemon — it sends MsgInput{ResolvedAction:"dashboard_layout_picker"}
// over the daemon's unix socket, mirroring the `dashboard` subcommand.
package dashboardlayout

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// Run is the subcommand entry point. It takes no arguments.
func Run(args []string) int {
	_ = args

	sessionID := tmuxValue("display-message", "-p", "#{session_id}")
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "dashboard-layout: could not determine tmux session")
		return 1
	}

	sockPath := daemon.SocketPath(sessionID)
	// Retry briefly so a transient daemon-down (e.g. during a watchdog respawn
	// or a build sync) doesn't surface as a "returned 1" in tmux's run-shell
	// output. Mirrors the `dashboard` subcommand's dial loop.
	var conn net.Conn
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		conn, err = net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard-layout: daemon not running (socket %s): %v\n", sockPath, err)
		return 1
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	msg := daemon.Message{
		Type:   daemon.MsgInput,
		Target: daemon.RenderTarget{Kind: daemon.TargetHook, Instance: "tabby-dashboard-layout"},
		Payload: daemon.InputPayload{
			Type:           "action",
			ResolvedAction: "dashboard_layout_picker",
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard-layout: marshal failed: %v\n", err)
		return 1
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "dashboard-layout: write failed: %v\n", err)
		return 1
	}
	return 0
}

func tmuxValue(args ...string) string {
	out, err := tmux.Cmd(args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
