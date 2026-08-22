package newwindow

import (
	"strings"
	"testing"

	tabbycfg "github.com/brendandebeasi/tabby/pkg/config"
)

func TestLandingEnabledDefaultsOff(t *testing.T) {
	if LandingEnabled(nil) {
		t.Error("a nil config must not open new tabs on the launcher")
	}
	if LandingEnabled(&tabbycfg.Config{}) {
		t.Error("an unset landing.enabled must not open new tabs on the launcher")
	}
	off := false
	if LandingEnabled(&tabbycfg.Config{Landing: tabbycfg.Landing{Enabled: &off}}) {
		t.Error("landing.enabled: false must not open new tabs on the launcher")
	}
	on := true
	if !LandingEnabled(&tabbycfg.Config{Landing: tabbycfg.Landing{Enabled: &on}}) {
		t.Error("landing.enabled: true should open new tabs on the launcher")
	}
}

// A configured command does not turn the feature on by itself. Someone reading
// their own config should be able to leave the value in place and switch it off
// with the one key that says off.
func TestLandingCommandDoesNotImplyEnabled(t *testing.T) {
	cfg := &tabbycfg.Config{Landing: tabbycfg.Landing{Command: `eval "$(launcher)"`}}
	if LandingEnabled(cfg) {
		t.Error("landing.command alone must not enable the launcher")
	}
	off := false
	cfg.Landing.Enabled = &off
	if LandingEnabled(cfg) {
		t.Error("landing.enabled: false must win over a configured command")
	}
}

// The command is typed into an interactive shell, so it must survive a path
// with spaces and must eval rather than run in a subshell: a chosen `cd` has to
// take effect in the shell that stays behind.
func TestLandingCommandShape(t *testing.T) {
	got := LandingCommand(nil)
	if !strings.HasPrefix(got, `eval "$(`) || !strings.HasSuffix(got, ` landing)"`) {
		t.Fatalf("LandingCommand(nil) = %q", got)
	}
	if !strings.Contains(got, "'") {
		t.Errorf("executable path is not quoted: %q", got)
	}
	if got := LandingCommand(&tabbycfg.Config{}); !strings.HasSuffix(got, ` landing)"`) {
		t.Errorf("an empty landing.command should fall back to tabby's own launcher, got %q", got)
	}
}

// A configured value is a shell command line, so it is delivered byte for byte:
// the quoting in it is the user's and the shell receiving it is what has to see
// it intact. Wrapping or re-quoting here would break the eval it needs.
func TestLandingCommandUsesConfiguredValueVerbatim(t *testing.T) {
	cases := []string{
		`eval "$(pounce --target current)"`,
		`eval "$('/opt/my launcher/pounce' --target current)"`,
		`eval "$(launcher --label "it's here")"`,
	}
	for _, want := range cases {
		got := LandingCommand(&tabbycfg.Config{Landing: tabbycfg.Landing{Command: want}})
		if got != want {
			t.Errorf("LandingCommand() = %q, want %q", got, want)
		}
	}
	// Surrounding whitespace is config noise, not part of the command.
	padded := &tabbycfg.Config{Landing: tabbycfg.Landing{Command: "  eval \"$(pounce)\"\n"}}
	if got := LandingCommand(padded); got != `eval "$(pounce)"` {
		t.Errorf("LandingCommand() = %q, want the trimmed command", got)
	}
	// A value that is only whitespace is not a command.
	blank := &tabbycfg.Config{Landing: tabbycfg.Landing{Command: "   "}}
	if got := LandingCommand(blank); !strings.HasSuffix(got, ` landing)"`) {
		t.Errorf("a blank landing.command should fall back to tabby's own launcher, got %q", got)
	}
}

// A tab that inherited a remote destination goes to that host and shows no
// landing command, whatever landing is set to. And with landing off, a new tab
// types nothing at all — the behaviour before any of this existed.
func TestNewTabCommand(t *testing.T) {
	on := true
	enabled := &tabbycfg.Config{Landing: tabbycfg.Landing{
		Enabled: &on,
		Command: `eval "$(pounce --target current)"`,
	}}

	if got := NewTabCommand(nil, ""); got != "" {
		t.Errorf("no config and no inherited command should type nothing, got %q", got)
	}
	if got := NewTabCommand(&tabbycfg.Config{}, ""); got != "" {
		t.Errorf("landing off should type nothing, got %q", got)
	}
	if got := NewTabCommand(enabled, ""); got != enabled.Landing.Command {
		t.Errorf("landing on should type the configured command, got %q", got)
	}

	const inherited = "ssh gunpowder"
	for name, cfg := range map[string]*tabbycfg.Config{
		"landing on":  enabled,
		"landing off": {},
		"no config":   nil,
	} {
		if got := NewTabCommand(cfg, inherited); got != inherited {
			t.Errorf("%s: inherited remote command should win, got %q", name, got)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/tabby": `'/usr/local/bin/tabby'`,
		"/opt/my tabby/tabby":  `'/opt/my tabby/tabby'`,
		"/tmp/it's":            `'/tmp/it'\''s'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
