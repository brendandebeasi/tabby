// Package dashboard implements the `tabby dashboard` subcommand: a toggle that
// asks the daemon to gather every content pane into a single tiled "dashboard"
// window (glance at everything, dive in with native tmux zoom), or to restore
// the panes to their original windows if already gathered.
//
// The actual gather/restore work lives in the daemon coordinator (dashboard.go)
// so it can suppress tabby's own layout reconcile while moving panes. This
// subcommand is a thin client that sends a MsgInput{ResolvedAction:"dashboard_toggle"}
// over the daemon's unix socket, mirroring the hook package's sendAction.
package dashboard

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// Run is the subcommand entry point. It takes no arguments.
func Run(args []string) int {
	_ = args

	sessionID := tmuxValue("display-message", "-p", "#{session_id}")
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "dashboard: could not determine tmux session")
		return 1
	}

	sockPath := fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard: daemon not running (socket %s): %v\n", sockPath, err)
		return 1
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	msg := daemon.Message{
		Type:   daemon.MsgInput,
		Target: daemon.RenderTarget{Kind: daemon.TargetHook, Instance: "tabby-dashboard"},
		Payload: daemon.InputPayload{
			Type:           "action",
			ResolvedAction: "dashboard_toggle",
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard: marshal failed: %v\n", err)
		return 1
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "dashboard: write failed: %v\n", err)
		return 1
	}
	return 0
}

func tmuxValue(args ...string) string {
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
