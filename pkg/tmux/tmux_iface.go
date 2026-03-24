package tmux

// Runner executes tmux commands and returns their stdout output.
// The production implementation calls tmux directly via exec.Command.
// Tests substitute a mock that returns canned output.
type Runner interface {
	Run(args ...string) ([]byte, error)
}

type execRunner struct{}

func (e *execRunner) Run(args ...string) ([]byte, error) {
	return tmuxOutput(args...)
}

// DefaultRunner is the package-level Runner used by all tmux list functions.
// Swap it in tests with a mock before calling ListWindows, ListAllPanes, etc.
var DefaultRunner Runner = &execRunner{}
