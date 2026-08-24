package daemon

import (
	"strings"
	"testing"
)

// The exact strings matter: they survive one round of tmux command parsing
// before the shell sees them, so a stray quote silently truncates a
// multi-word group name instead of failing loudly.
func TestNewGroupMenuCommandWithWindow(t *testing.T) {
	got := newGroupMenuCommand("/opt/tabby hook", "@42")
	want := `command-prompt -p 'New group name:' "run-shell '/opt/tabby hook new-group \"%%\" @42'"`
	if got != want {
		t.Fatalf("newGroupMenuCommand:\n got %s\nwant %s", got, want)
	}
}

func TestNewGroupMenuCommandWithoutWindow(t *testing.T) {
	got := newGroupMenuCommand("/opt/tabby hook", "")
	want := `command-prompt -p 'New group name:' "run-shell '/opt/tabby hook new-group \"%%\"'"`
	if got != want {
		t.Fatalf("newGroupMenuCommand:\n got %s\nwant %s", got, want)
	}
}

// A bare %% is what the group menu used to emit, and it is what made
// "Side Projects" arrive as the group "Side".
func TestNewGroupMenuCommandQuotesSubstitution(t *testing.T) {
	got := newGroupMenuCommand("/opt/tabby hook", "@1")
	if strings.Contains(got, ` %% `) {
		t.Fatalf("substitution left unquoted: %s", got)
	}
	if !strings.Contains(got, `\"%%\"`) {
		t.Fatalf("substitution not wrapped in escaped quotes: %s", got)
	}
}
