// Command tabby is the unified entry point for the tabby tmux plugin.
// It dispatches to one of several subcommand handlers based on os.Args[1].
//
// Each subcommand's implementation lives in cmd/tabby/internal/<name>/ as
// its own Go package with an exported Run(args []string) int function.
// Subcommands that need to spawn a sibling (e.g. toggle starting the
// daemon) invoke this same binary with a different subcommand:
//
//	exe, _ := os.Executable()
//	exec.Command(exe, "daemon", "-session", id, ...)
//
// The per-frame render-tab*/render-status* binaries are NOT merged here;
// they live in a separate tabby-render binary because tmux invokes them
// hundreds of times per second from format strings and subcommand dispatch
// would add measurable latency.
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/cyclepane"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/daemon"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/dev"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/hook"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/managegroup"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/newwindow"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/panepicker"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/renderdispatch"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/setup"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/toggle"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/watchdog"
)

// subcommand is a single dispatchable entry. Run is invoked with the
// arguments after the subcommand name (os.Args[2:]) and returns the
// exit code the tabby process should use.
type subcommand struct {
	name    string
	summary string
	run     func(args []string) int
}

var subcommands = []subcommand{
	{"cycle-pane", "cycle the active content pane and dim inactive panes", cyclepane.Run},
	{"daemon", "run the tabby daemon (socket server + coordinator)", daemon.Run},
	{"dev", "developer commands: reload, status", dev.Run},
	{"hook", "tmux hook dispatcher (split-pane, kill-pane, resize, etc.)", hook.Run},
	{"manage-group", "edit window-group entries in the tabby config file", managegroup.Run},
	{"new-window", "create a new tmux window with sidebar", newwindow.Run},
	{"pane-picker", "interactive pane picker TUI", panepicker.Run},
	{"render", "spawn a renderer: sidebar | window-header | pane-header | sidebar-popup", renderdispatch.Run},
	{"setup", "interactive configuration wizard", setup.Run},
	{"toggle", "enable or disable the tabby sidebar for this session", toggle.Run},
	{"watchdog", "supervise the tabby daemon, restarting on crash", watchdog.Run},
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	name := os.Args[1]
	if name == "-h" || name == "--help" || name == "help" {
		usage(os.Stdout)
		os.Exit(0)
	}
	for _, sc := range subcommands {
		if sc.name == name {
			os.Exit(sc.run(os.Args[2:]))
		}
	}
	fmt.Fprintf(os.Stderr, "tabby: unknown subcommand %q\n\n", name)
	usage(os.Stderr)
	os.Exit(2)
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: tabby <subcommand> [args...]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	names := make([]string, 0, len(subcommands))
	for _, sc := range subcommands {
		names = append(names, sc.name)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, sc := range subcommands {
			if sc.name == n {
				fmt.Fprintf(w, "  %-14s  %s\n", sc.name, sc.summary)
				break
			}
		}
	}
}
