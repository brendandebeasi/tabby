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

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/clip"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/cyclepane"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/daemon"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/dashboard"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/dashboardlayout"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/dev"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/hook"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/landing"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/managegroup"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/newwindow"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/panepicker"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/pet"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/renderdispatch"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/setup"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/theme"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/tmuxhooks"
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
	{"clip", "push text up to the clipboard of the device you are sitting at", clip.Run},
	{"cycle-pane", "cycle the active content pane and dim inactive panes", cyclepane.Run},
	{"daemon", "run the tabby daemon (socket server + coordinator)", daemon.Run},
	{"dashboard", "toggle the all-panes dashboard (gather panes into a tiled grid)", dashboard.Run},
	{"dashboard-layout", "open the dashboard layout-style picker popup", dashboardlayout.Run},
	{"dev", "developer commands: reload, status", dev.Run},
	{"hook", "tmux hook dispatcher (split-pane, kill-pane, resize, etc.)", hook.Run},
	{"install-hooks", "register tabby's global tmux hooks (called from tabby.tmux)", tmuxhooks.Run},
	{"landing", "full-pane new-tab launcher over landing.yaml", landing.Run},
	{"manage-group", "edit window-group entries in the tabby config file", managegroup.Run},
	{"new-window", "create a new tmux window with sidebar", newwindow.Run},
	{"pane-picker", "interactive pane picker TUI", panepicker.Run},
	{"pet", "interact with the cat: ask, traits, forget", pet.Run},
	{"render", "spawn a renderer: sidebar | window-header | pane-header | sidebar-popup | pet-qa-popup | dash-layout-popup", renderdispatch.Run},
	{"setup", "interactive configuration wizard", setup.Run},
	{"theme", "show or toggle the light/dark theme for this session", theme.Run},
	{"toggle", "enable or disable the tabby sidebar for this session", toggle.Run},
	{"watchdog", "supervise the tabby daemon, restarting on crash", watchdog.Run},
}

// batch is registered from init rather than as a literal in the subcommands
// initializer: runBatch reads subcommands, and a var whose initializer
// references a function that reads that same var is an initialization cycle.
func init() {
	subcommands = append(subcommands, subcommand{
		"batch", "run several subcommands in one process, separated by --", runBatch,
	})
}

func lookup(name string) *subcommand {
	for i := range subcommands {
		if subcommands[i].name == name {
			return &subcommands[i]
		}
	}
	return nil
}

// runBatch runs several subcommands in a single process. Segments are
// separated by `--`:
//
//	tabby batch -- hook after-select-window '@9' -- cycle-pane --ensure-content
//
// This exists to collapse tmux hook bodies. A hook like after-select-window
// fires once per session a window is linked into, so in an 8-session grouped
// set its three separate `tabby` invocations cost 24 process spawns per window
// switch. Batching makes that 8. The fork itself is the expense — the work each
// segment does is unchanged.
//
// Segments are independent housekeeping: a failing one is logged to stderr and
// the rest still run, matching the `; true` guard the hook bodies already carry.
// The batch's own exit status is always 0 for the same reason.
func runBatch(args []string) int {
	for _, seg := range splitSegments(args) {
		sc := lookup(seg[0])
		switch {
		case sc == nil:
			fmt.Fprintf(os.Stderr, "tabby batch: unknown subcommand %q\n", seg[0])
		case sc.name == "batch":
			fmt.Fprintln(os.Stderr, "tabby batch: cannot nest batch")
		default:
			if code := sc.run(seg[1:]); code != 0 {
				fmt.Fprintf(os.Stderr, "tabby batch: %s exited %d\n", seg[0], code)
			}
		}
	}
	return 0
}

// splitSegments cuts args on `--` into non-empty segments. A leading `--` is
// optional, so both `batch -- a -- b` and `batch a -- b` parse the same way.
func splitSegments(args []string) [][]string {
	var segs [][]string
	var cur []string
	for _, a := range args {
		if a == "--" {
			if len(cur) > 0 {
				segs = append(segs, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, a)
	}
	if len(cur) > 0 {
		segs = append(segs, cur)
	}
	return segs
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
	if sc := lookup(name); sc != nil {
		os.Exit(sc.run(os.Args[2:]))
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
