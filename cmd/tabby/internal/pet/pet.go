// Package pet implements the `tabby pet` subcommand: a CLI front-end for
// the cat's personality-building Q&A loop.
//
// Subcommands:
//
//	tabby pet ask                    print the pending question (or a friendly
//	                                 "nothing to ask" message)
//	tabby pet ask --answer "..."     submit an answer; print confirmation
//	tabby pet traits                 list all distilled traits
//	tabby pet forget <id>            remove an answer and any derived trait
//
// All commands talk to the daemon over the existing unix socket using a
// single MsgPetQA request/response per invocation. The daemon owns the
// in-memory pet state and the pet.json file; this package never touches
// either directly.
//
// Phase 1 of the Q&A loop; see
// /Users/b/.claude/plans/wiggly-discovering-starlight.md.
package pet

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// stripControl removes control runes from s. Mirrors the popup's input
// filter so CLI-submitted answers can't contain bytes the popup would
// reject. Keeps spaces and printable Unicode; drops tab, newline, BEL,
// and other C0/C1 controls.
func stripControl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == ' ' || unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Run dispatches the `tabby pet <op> ...` subcommands. Returns the exit
// code main should propagate.
func Run(args []string) int {
	if len(args) == 0 {
		usage(os.Stdout)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return 0
	case "ask":
		return runAsk(args[1:])
	case "traits":
		return runTraits(args[1:])
	case "forget":
		return runForget(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "tabby pet: unknown subcommand %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: tabby pet <ask|traits|forget> [args...]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  ask                  print the cat's pending question, if any")
	fmt.Fprintln(w, "  ask --answer \"...\"   submit an answer to the pending question")
	fmt.Fprintln(w, "  traits               list everything the cat has learned about you")
	fmt.Fprintln(w, "  forget <id>          remove an answer and any traits derived from it")
}

// ── ask ─────────────────────────────────────────────────────────────────

func runAsk(args []string) int {
	fs := flag.NewFlagSet("tabby pet ask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	answer := fs.String("answer", "", "submit an answer to the pending question (case-sensitive for choice kind)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *answer == "" {
		// Read-only path: print the pending question.
		resp, err := request(&daemon.PetQARequest{Op: daemon.PetQAOpGetPending})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if !resp.OK {
			fmt.Fprintln(os.Stderr, "tabby pet ask:", resp.Error)
			return 1
		}
		if resp.Pending == nil {
			fmt.Println("the cat has nothing to ask right now.")
			return 0
		}
		printPending(os.Stdout, resp.Pending)
		return 0
	}

	// Submit path: send the answer. The daemon validates against the
	// pending question and may produce a trait.
	//
	// Filter control characters to match the popup's allowlist (printable
	// runes + space; tab/newline/etc dropped). The popup's TUI input loop
	// already filters these — we mirror it here so a CLI submission can't
	// inject control chars that the popup would reject. The daemon also
	// caps total length; this only normalises content.
	cleaned := stripControl(*answer)
	resp, err := request(&daemon.PetQARequest{Op: daemon.PetQAOpAnswer, Answer: cleaned})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "tabby pet ask:", resp.Error)
		return 1
	}
	fmt.Println("thanks — the cat heard you.")
	if resp.NewTrait != nil {
		fmt.Printf("  learned: %s\n", resp.NewTrait.Text)
	}
	return 0
}

// printPending renders a pending question to w. choice-kind questions get
// numbered choices for human readability; free_text questions just print
// the question text. The number prefix is purely cosmetic — submitting
// requires the exact choice string, not the index.
func printPending(w io.Writer, p *daemon.PendingQuestion) {
	fmt.Fprintln(w, p.Text)
	switch p.Kind {
	case "choice":
		fmt.Fprintln(w)
		for i, c := range p.Choices {
			fmt.Fprintf(w, "  %d. %s\n", i+1, c)
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "to answer:  tabby pet ask --answer \"<choice text>\"")
	case "free_text":
		fmt.Fprintln(w)
		fmt.Fprintln(w, "to answer:  tabby pet ask --answer \"<your response>\"")
	default:
		// Future question kinds (e.g. LLM-generated) fall through silently;
		// the daemon will still accept any non-empty answer.
	}
}

// ── traits ──────────────────────────────────────────────────────────────

func runTraits(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "Usage: tabby pet traits")
		return 2
	}
	resp, err := request(&daemon.PetQARequest{Op: daemon.PetQAOpListTraits})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "tabby pet traits:", resp.Error)
		return 1
	}
	if len(resp.Traits) == 0 {
		fmt.Println("your cat hasn't learned anything yet.")
		return 0
	}
	for _, t := range resp.Traits {
		// Format: "<confidence:0.X> <text>  (source: <id>)" — matches the
		// spec in the agent brief. Confidence printed to one decimal to
		// keep the column tidy; %.1f rounds 1.0 to "1.0" so the column
		// width is stable.
		fmt.Printf("<confidence:%.1f> %s  (source: %s)\n", t.Confidence, t.Text, t.Source)
	}
	return 0
}

// ── forget ──────────────────────────────────────────────────────────────

func runForget(args []string) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(os.Stderr, "Usage: tabby pet forget <id>")
		return 2
	}
	id := args[0]
	resp, err := request(&daemon.PetQARequest{Op: daemon.PetQAOpForget, ID: id})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "tabby pet forget:", resp.Error)
		return 1
	}
	if resp.Removed {
		fmt.Printf("forgot answer %q (and any traits derived from it).\n", id)
	} else {
		// OK=true with Removed=false shouldn't happen with the current
		// daemon — ForgetAnswer returns an error on missing id, which
		// would set OK=false above. Keep this branch for forward-compat.
		fmt.Printf("no change: %q didn't match any stored answer.\n", id)
	}
	return 0
}

// ── socket transport ────────────────────────────────────────────────────

// request dials the daemon socket for the current tmux session, sends one
// MsgPetQA, reads one response, and returns the parsed PetQAResponse. The
// "daemon not running" condition is reported as a stable user-facing
// message so the CLI behaves predictably outside an active tabby session.
func request(req *daemon.PetQARequest) (*daemon.PetQAResponse, error) {
	sessionID, err := currentSessionID()
	if err != nil {
		return nil, fmt.Errorf("tabby daemon not running in this session — start tabby first.")
	}
	sockPath := daemon.SocketPath(sessionID)
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("tabby daemon not running in this session — start tabby first.")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	msg := daemon.Message{
		Type:    daemon.MsgPetQA,
		Payload: req,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Daemon writes one JSON-encoded Message followed by '\n'. Use a
	// scanner with a generous buffer in case the trait list grows; the
	// server's render scanner uses 1 MiB so we match that ceiling.
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
	if respMsg.Type != daemon.MsgPetQA {
		return nil, fmt.Errorf("unexpected response type %q", respMsg.Type)
	}
	// Payload comes back as map[string]interface{}; re-marshal then
	// unmarshal into the typed struct.
	payloadBytes, err := json.Marshal(respMsg.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode response payload: %w", err)
	}
	var resp daemon.PetQAResponse
	if err := json.Unmarshal(payloadBytes, &resp); err != nil {
		return nil, fmt.Errorf("decode response payload: %w", err)
	}
	return &resp, nil
}

// currentSessionID returns the tmux session id ("$2") for the caller's
// pane. Mirrors hook.getSessionID — kept local so this package doesn't
// import the hook package for one helper.
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
