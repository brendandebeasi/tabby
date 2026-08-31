package tmux

import (
	"context"
	"os/exec"
	"sync"
)

// lookPath is exec.LookPath, indirected so the resolution test can stub it.
var lookPath = exec.LookPath

var (
	binOnce sync.Once
	binPath string
)

// Bin returns the absolute path to the tmux binary.
//
// exec.Command re-runs LookPath every time it is handed a bare name, and
// LookPath stats "tmux" under every entry of PATH until one hits. The daemon
// execs tmux thousands of times a minute, so that is a stack of stat syscalls
// and a fresh string per entry, every time, to re-derive an answer that cannot
// change. Resolve it once instead.
//
// If the lookup fails, this hands back the bare name. exec.Command will then
// redo the lookup, fail the same way, and report the same error it always did,
// so a tmux that is missing at startup but present later still works.
func Bin() string {
	binOnce.Do(func() {
		binPath = "tmux"
		if p, err := lookPath("tmux"); err == nil {
			binPath = p
		}
	})
	return binPath
}

// Cmd builds an *exec.Cmd for a tmux invocation, skipping the PATH search.
// It is a drop-in for exec.Command("tmux", args...).
func Cmd(args ...string) *exec.Cmd {
	return exec.Command(Bin(), args...)
}

// CmdContext is Cmd with a context, a drop-in for
// exec.CommandContext(ctx, "tmux", args...).
func CmdContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, Bin(), args...)
}
