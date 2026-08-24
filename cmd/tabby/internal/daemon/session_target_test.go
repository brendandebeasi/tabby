package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionTarget(t *testing.T) {
	cases := []struct {
		name string
		sess string
		want string
	}{
		{"session id", "$1", "$1:"},
		{"session name", "infras-1", "infras-1:"},
		{"whitespace trimmed", "  $2  ", "$2:"},
		{"empty stays empty", "", ""},
		{"whitespace only stays empty", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionTarget(tc.sess); got != tc.want {
				t.Fatalf("sessionTarget(%q) = %q, want %q", tc.sess, got, tc.want)
			}
		})
	}
}

func TestDisplayMessageArgs(t *testing.T) {
	got := displayMessageArgs("$1", "#{window_id}")
	want := []string{"display-message", "-t", "$1:", "-p", "#{window_id}"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", got, want)
	}

	// No session: the -t flag must be omitted entirely rather than passed
	// empty, which tmux rejects.
	got = displayMessageArgs("", "#{window_id}")
	want = []string{"display-message", "-p", "#{window_id}"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for _, a := range got {
		if a == "-t" {
			t.Fatalf("empty session produced a -t flag: %v", got)
		}
	}
}

func TestCoordinatorSessionTargetAndSessionID(t *testing.T) {
	c := &Coordinator{sessionID: "$3"}
	if got := c.sessionTarget(); got != "$3:" {
		t.Fatalf("sessionTarget() = %q, want %q", got, "$3:")
	}
	if got := c.SessionID(); got != "$3" {
		t.Fatalf("SessionID() = %q, want %q", got, "$3")
	}
	if got := strings.Join(c.displayMessageArgs("#{pane_id}"), " "); got != "display-message -t $3: -p #{pane_id}" {
		t.Fatalf("displayMessageArgs() = %q", got)
	}
}

// TestNoUnqualifiedDisplayMessage is the guard that keeps the fix from eroding.
// An unqualified `display-message -p` does not answer for this daemon's session
// — tmux answers for the most recently active client's session instead — so
// every read has to carry either `-t <target>` (a session/window/pane) or
// `-c <tty>` (a specific client). See session_target.go for the mechanism.
//
// The check is textual because the mistake is textual: someone types the
// familiar `display-message", "-p"` and nothing complains until focus starts
// landing on the wrong window in a grouped session.
func TestNoUnqualifiedDisplayMessage(t *testing.T) {
	// session_target.go owns the one legitimate unqualified read: discovering
	// which session we belong to when the -session flag was empty. There is no
	// target to qualify with at that point.
	allowed := map[string]bool{"session_target.go": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if allowed[name] {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if !strings.Contains(line, `"display-message"`) {
				continue
			}
			// A qualified call names a target or a client on the same line.
			if strings.Contains(line, `"-t"`) || strings.Contains(line, `"-c"`) {
				continue
			}
			// display-message without -p is a user-facing notification, not a
			// query, so there is nothing to resolve incorrectly -- unless the
			// line is building an argv piecewise, where the -p arrives on a
			// later line and the qualification may never arrive at all. That
			// shape hid a live bug: `args := []string{"display-message"}`
			// followed by a conditional `-c`, so a session with no clients of
			// its own queried unqualified on every tick.
			if !strings.Contains(line, `"-p"`) {
				argv := strings.Contains(line, `[]string{"display-message"`) ||
					strings.Contains(line, `append(args, "display-message"`)
				if !argv {
					continue
				}
			}
			offenders = append(offenders, filepath.Join(".", name)+":"+itoa(i+1)+": "+trimmed)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("unqualified display-message -p (tmux will answer for another session; use "+
			"c.ActiveWindowID/c.DisplayMessage or displayMessageArgs):\n%s",
			strings.Join(offenders, "\n"))
	}
}

// TestNoUnscopedListClients is the same guard for `list-clients`. The failure
// mode is the sibling of the display-message one: an unscoped list-clients
// returns every client on the tmux server, so a daemon can elect a client that
// belongs to another session, measure its geometry, and trust its active
// window. That is how cross-session resize storms and focus theft start.
//
// A few reads genuinely want the whole server. Those carry a `server-wide
// list-clients:` comment saying why, and this test accepts that marker on the
// call line or on any of the three lines above it — close enough that the
// justification cannot drift away from the call it justifies.
func TestNoUnscopedListClients(t *testing.T) {
	// session_target.go owns listClientsArgs, which is the scoping helper
	// itself and necessarily contains the bare verb.
	allowed := map[string]bool{"session_target.go": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if allowed[name] {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if !strings.Contains(line, `"list-clients"`) {
				continue
			}
			if strings.Contains(line, `"-t"`) {
				continue // scoped to a session already
			}
			marked := false
			for j := i; j >= 0 && j >= i-3; j-- {
				if strings.Contains(lines[j], "server-wide list-clients:") {
					marked = true
					break
				}
			}
			if marked {
				continue
			}
			offenders = append(offenders, filepath.Join(".", name)+":"+itoa(i+1)+": "+trimmed)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("unscoped list-clients (returns other sessions' clients; use "+
			"listClientsArgs, or add a `server-wide list-clients:` comment "+
			"explaining why every client is wanted):\n%s",
			strings.Join(offenders, "\n"))
	}
}

// TestNoUnqualifiedListWindows completes the set. `list-windows` with neither
// -a nor -t resolves its target the same way display-message does, so it lists
// whichever session the last active client is on. That is wrong even between
// grouped sessions, because the holding sessions (_tabby_limbo,
// _tabby_minimized) are not grouped and hold windows the daemon must not treat
// as its own -- and the kill_window handler picked its focus neighbor out of
// exactly this list.
//
// -a is the legitimate unqualified form: it says "every session" out loud.
func TestNoUnqualifiedListWindows(t *testing.T) {
	allowed := map[string]bool{"session_target.go": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if allowed[name] {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if !strings.Contains(line, `"list-windows"`) {
				continue
			}
			if strings.Contains(line, `"-a"`) || strings.Contains(line, `"-t"`) {
				continue
			}
			offenders = append(offenders, filepath.Join(".", name)+":"+itoa(i+1)+": "+trimmed)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("list-windows with neither -a nor -t (lists another session's "+
			"windows; use listWindowsArgs for our own, or -a to say every "+
			"session on purpose):\n%s", strings.Join(offenders, "\n"))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
