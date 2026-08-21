package newwindow

import (
	"strings"
	"testing"

	tabbycfg "github.com/brendandebeasi/tabby/pkg/config"
)

func TestLandingEnabledDefaultsOff(t *testing.T) {
	if landingEnabled(nil) {
		t.Error("a nil config must not open new tabs on the launcher")
	}
	if landingEnabled(&tabbycfg.Config{}) {
		t.Error("an unset landing.enabled must not open new tabs on the launcher")
	}
	off := false
	if landingEnabled(&tabbycfg.Config{Landing: tabbycfg.Landing{Enabled: &off}}) {
		t.Error("landing.enabled: false must not open new tabs on the launcher")
	}
	on := true
	if !landingEnabled(&tabbycfg.Config{Landing: tabbycfg.Landing{Enabled: &on}}) {
		t.Error("landing.enabled: true should open new tabs on the launcher")
	}
}

// The command is typed into an interactive shell, so it must survive a path
// with spaces and must eval rather than run in a subshell: a chosen `cd` has to
// take effect in the shell that stays behind.
func TestLandingCommandShape(t *testing.T) {
	got := landingCommand()
	if !strings.HasPrefix(got, `eval "$(`) || !strings.HasSuffix(got, ` landing)"`) {
		t.Fatalf("landingCommand() = %q", got)
	}
	if !strings.Contains(got, "'") {
		t.Errorf("executable path is not quoted: %q", got)
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
