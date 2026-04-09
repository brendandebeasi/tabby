// tabby-hook is a thin CLI that sends commands to the tabby-daemon via Unix socket.
// It replaces shell scripts that were invoked by tmux keybindings and menus.
//
// Usage: tabby-hook <action> [args...]
//
// Actions:
//
//	delete-group <name>
//	rename-group <old-name> <new-name>
//	set-group-color <name> <color>
//	set-group-marker <name> <marker>
//	set-group-working-dir <name> <dir>
//	toggle-group-collapse <name> <collapse|expand>
//	toggle-pane-collapse [-t <pane-id>]
//	toggle-sidebar
//	new-group
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

type message struct {
	Type     string      `json:"type"`
	ClientID string      `json:"client_id,omitempty"`
	Payload  interface{} `json:"payload,omitempty"`
}

type inputPayload struct {
	Type           string `json:"type"`
	ResolvedAction string `json:"resolved_action"`
	ResolvedTarget string `json:"resolved_target,omitempty"`
	PickerValue    string `json:"picker_value,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tabby-hook <action> [args...]")
		os.Exit(1)
	}

	action := os.Args[1]
	args := os.Args[2:]

	var target, value string

	switch action {
	case "delete-group":
		if len(args) < 1 {
			fatal("Usage: tabby-hook delete-group <name>")
		}
		target = args[0]

	case "rename-group":
		if len(args) < 2 {
			fatal("Usage: tabby-hook rename-group <old-name> <new-name>")
		}
		target = args[0]
		value = args[1]

	case "set-group-color":
		if len(args) < 2 {
			fatal("Usage: tabby-hook set-group-color <name> <color>")
		}
		target = args[0]
		value = args[1]

	case "set-group-marker":
		if len(args) < 2 {
			fatal("Usage: tabby-hook set-group-marker <name> <marker>")
		}
		target = args[0]
		value = args[1]

	case "set-group-working-dir":
		if len(args) < 2 {
			fatal("Usage: tabby-hook set-group-working-dir <name> <dir>")
		}
		target = args[0]
		value = args[1]

	case "toggle-group-collapse":
		if len(args) < 2 {
			fatal("Usage: tabby-hook toggle-group-collapse <name> <collapse|expand>")
		}
		target = args[0]
		value = args[1]

	case "toggle-pane-collapse":
		// Parse -t flag
		target = os.Getenv("TMUX_PANE")
		for i := 0; i < len(args); i++ {
			if (args[i] == "-t" || args[i] == "--target") && i+1 < len(args) {
				target = args[i+1]
				break
			}
		}

	case "kill-pane":
		// Parse -t flag
		target = os.Getenv("TMUX_PANE")
		for i := 0; i < len(args); i++ {
			if (args[i] == "-t" || args[i] == "--target") && i+1 < len(args) {
				target = args[i+1]
				break
			}
		}

	case "toggle-sidebar":
		// No args needed

	case "new-group":
		if len(args) > 0 {
			target = args[0]
		}

	default:
		fatal("Unknown action: " + action)
	}

	// Convert action to daemon's action format (kebab-case -> snake_case)
	daemonAction := strings.ReplaceAll(action, "-", "_")

	if err := sendAction(daemonAction, target, value); err != nil {
		fmt.Fprintf(os.Stderr, "tabby-hook: %v\n", err)
		os.Exit(1)
	}
}

func sendAction(action, target, value string) error {
	sessionID, err := getSessionID()
	if err != nil {
		return fmt.Errorf("failed to get session ID: %w", err)
	}

	sockPath := fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("daemon not running (socket %s): %w", sockPath, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	msg := message{
		Type: "input",
		Payload: inputPayload{
			Type:           "action",
			ResolvedAction: action,
			ResolvedTarget: target,
			PickerValue:    value,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	_, err = conn.Write(data)
	return err
}

func getSessionID() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
