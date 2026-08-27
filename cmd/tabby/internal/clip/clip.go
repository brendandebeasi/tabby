// Package clip is the `tabby clip` subcommand: it pushes bytes from this
// machine up to the clipboard of whatever device you are sitting in front of.
//
// The transport is OSC 52, the terminal escape for "set the system
// clipboard". It travels the same path the output of any program in the pane
// travels — tmux, then mosh or ssh, then the terminal app — so it reaches the
// outermost device without any helper process, port, or open socket. That
// matters because the outermost device is often a phone, where nothing can
// run outside the terminal app.
//
// The direction is deliberate. OSC 52 also defines a query form for reading
// the clipboard back, but mosh implements only the write half, so pulling a
// clip down from the phone needs a different mechanism entirely. Pushing up
// works today, over every link in the chain, and is what this command does.
//
// On a remote host with no tabby binary — the client-* boxes — the shell
// function in scripts/tabby-clip.sh emits the same escape. The two are kept
// deliberately interchangeable so a skill or agent can call either one.
package clip

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Run is the subcommand entry point. args are the tokens following "clip".
func Run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return 0
	case "send":
		return runSend(args[1:])
	}
	fmt.Fprintf(os.Stderr, "tabby clip: unknown argument %q\n\n", args[0])
	usage(os.Stderr)
	return 2
}

// sendOpts is the parsed form of `tabby clip send`'s flags.
type sendOpts struct {
	text        string
	textSet     bool
	file        string
	pane        string
	paneSet     bool
	lines       int
	tty         string
	max         int
	passthrough bool
	quiet       bool
}

func runSend(args []string) int {
	opts := sendOpts{lines: 100, max: MaxPayload}
	if code, stop := parseSend(args, &opts); stop {
		return code
	}

	payload, err := readSource(&opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tabby clip:", err)
		return 1
	}
	// An empty send would clear the clipboard rather than fill it, which is
	// never what the caller meant and is destructive of whatever they had
	// copied. Treat it as a no-op and say so.
	if len(payload) == 0 {
		if !opts.quiet {
			fmt.Fprintln(os.Stderr, "tabby clip: nothing to send")
		}
		return 0
	}

	payload, truncated := Truncate(payload, opts.max)

	target, err := resolveTarget(&opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tabby clip:", err)
		return 1
	}
	defer target.Close()

	if _, err := io.WriteString(target, Encode(payload, opts.passthrough)); err != nil {
		fmt.Fprintln(os.Stderr, "tabby clip: write:", err)
		return 1
	}

	if !opts.quiet {
		msg := fmt.Sprintf("sent %d bytes to the clipboard", len(payload))
		if truncated {
			msg += fmt.Sprintf(" (truncated to the last %d)", opts.max)
		}
		fmt.Fprintln(os.Stderr, "tabby clip: "+msg)
	}
	return 0
}

// parseSend fills opts from args. stop is true when the caller should return
// immediately with code — on a parse error, or after printing help.
func parseSend(args []string, opts *sendOpts) (code int, stop bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// next consumes the value belonging to a flag that takes one.
		next := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "tabby clip send: %s needs a value\n", arg)
				return "", false
			}
			i++
			return args[i], true
		}
		switch arg {
		case "-h", "--help":
			usage(os.Stdout)
			return 0, true
		case "--text":
			v, ok := next()
			if !ok {
				return 2, true
			}
			opts.text, opts.textSet = v, true
		case "--file":
			v, ok := next()
			if !ok {
				return 2, true
			}
			opts.file = v
		case "--pane":
			opts.paneSet = true
			// The target is optional: `--pane` alone means the pane we are
			// running in. Only swallow the next token if it is not itself a
			// flag, so `--pane --quiet` keeps its obvious meaning.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.pane = args[i]
			}
		case "--lines":
			v, ok := next()
			if !ok {
				return 2, true
			}
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "tabby clip send: --lines wants a positive number, got %q\n", v)
				return 2, true
			}
			opts.lines = n
		case "--tty":
			v, ok := next()
			if !ok {
				return 2, true
			}
			opts.tty = v
		case "--max":
			v, ok := next()
			if !ok {
				return 2, true
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				fmt.Fprintf(os.Stderr, "tabby clip send: --max wants a non-negative number, got %q\n", v)
				return 2, true
			}
			opts.max = n
		case "--passthrough":
			opts.passthrough = true
		case "-q", "--quiet":
			opts.quiet = true
		default:
			fmt.Fprintf(os.Stderr, "tabby clip send: unknown flag %q\n\n", arg)
			usage(os.Stderr)
			return 2, true
		}
	}
	if !oneSource(opts) {
		fmt.Fprintln(os.Stderr, "tabby clip send: pick one of --text, --file, --pane")
		return 2, true
	}
	return 0, false
}

// oneSource reports whether at most one input flag was named. Two sources is
// not a thing we can guess our way out of, so it is an error rather than a
// precedence rule nobody would remember.
func oneSource(opts *sendOpts) bool {
	n := 0
	for _, set := range []bool{opts.textSet, opts.file != "", opts.paneSet} {
		if set {
			n++
		}
	}
	return n <= 1
}

// readSource produces the bytes to send, from whichever input was selected.
// With no source flag it reads stdin, which is the form a pipeline uses.
func readSource(opts *sendOpts) ([]byte, error) {
	switch {
	case opts.textSet:
		return []byte(opts.text), nil
	case opts.file != "":
		b, err := os.ReadFile(opts.file)
		if err != nil {
			return nil, err
		}
		return b, nil
	case opts.paneSet:
		return capturePane(opts.pane, opts.lines)
	}
	return io.ReadAll(os.Stdin)
}

// capturePane reads the last n lines of a pane's scrollback.
//
// -J rejoins lines that tmux wrapped for display, so a long command line or a
// pasted URL comes back as one line rather than as however many the pane
// happened to be wide.
func capturePane(target string, n int) ([]byte, error) {
	args := []string{"capture-pane", "-p", "-J", "-S", "-" + strconv.Itoa(n)}
	if target == "" {
		target = os.Getenv("TMUX_PANE")
	}
	if target != "" {
		args = append(args, "-t", target)
	}
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("capture-pane: %w", err)
	}
	// capture-pane pads every line out to the pane width and keeps blank
	// lines below the cursor. Both are noise in a clipboard.
	return []byte(trimCapture(string(out))), nil
}

// trimCapture strips trailing whitespace from each captured line and drops
// the empty ones at the end.
func trimCapture(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// resolveTarget opens the file the escape should be written to.
//
// The escape has to enter a stream tmux is reading, which means a pane's pty.
// Preference order: an explicit --tty, then the pty of the pane we were
// invoked from, then our own controlling terminal. The middle case is what
// makes this work from a key binding, where tmux runs the command with no
// controlling terminal of its own but does set TMUX_PANE.
func resolveTarget(opts *sendOpts) (*os.File, error) {
	path := opts.tty
	if path == "" {
		path = paneTTY(opts.pane)
	}
	if path == "" {
		path = "/dev/tty"
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// paneTTY asks tmux for a pane's pty path, returning "" when there is no
// tmux to ask or the pane has gone away.
func paneTTY(target string) string {
	if target == "" {
		target = os.Getenv("TMUX_PANE")
	}
	if target == "" {
		return ""
	}
	out, err := exec.Command("tmux", "display", "-p", "-t", target, "#{pane_tty}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: tabby clip send [source] [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Push bytes up to the clipboard of the device you are sitting at,")
	fmt.Fprintln(w, "through tmux, mosh/ssh and the terminal app, via OSC 52.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Sources (pick one; stdin is the default):")
	fmt.Fprintln(w, "  --text STR        send a literal string")
	fmt.Fprintln(w, "  --file PATH       send a file's contents")
	fmt.Fprintln(w, "  --pane [TARGET]   send a pane's scrollback tail (default: this pane)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --lines N         lines to capture with --pane (default 100)")
	fmt.Fprintln(w, "  --tty PATH        write the escape here instead of the pane's pty")
	fmt.Fprintf(w, "  --max N           cap the payload at N bytes, keeping the tail (default %d)\n", MaxPayload)
	fmt.Fprintln(w, "  --passthrough     wrap in a tmux DCS envelope, for a nested remote tmux")
	fmt.Fprintln(w, "  -q, --quiet       do not report what was sent")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Requires `set -g set-clipboard on` in tmux, and a terminal that honours")
	fmt.Fprintln(w, "OSC 52. See docs/wiki/SSH-and-Remote-Hosts.md for the mosh/iOS setup.")
}
