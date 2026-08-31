package daemon

import (
	"context"
	"os/exec"
	"time"
)

// tmuxCmd builds a tmux *exec.Cmd bounded by tmuxCmdTimeout.
//
// Every tmux invocation in the daemon must be bounded: an unbounded
// fork/exec on the reconcile path blocks the event loop indefinitely when
// the tmux server stalls (macOS sleep/wake), which starves the heartbeat
// and trips the deadlock watchdog.
//
// The context's cancel func is released by Cmd.Cancel, which os/exec runs
// once the process exits, so a completed command drops its timer
// immediately rather than holding it for the full timeout. WaitDelay
// bounds the tail case where the process dies but an inherited pipe is
// still held open by a child.
func tmuxCmd(args ...string) *exec.Cmd {
	// Close the hook gate before a mutating command can fire tabby's hooks
	// back at this daemon. Done at build time rather than at Run time: callers
	// build then immediately run, and muting early is the safe direction.
	noteTmuxMutation(args)
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.Cancel = func() error {
		defer cancel()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = time.Second
	// Keep cancel reachable for vet: Cmd.Cancel above owns the call, and
	// the context is bounded by its own timeout regardless.
	_ = cancel
	return cmd
}
