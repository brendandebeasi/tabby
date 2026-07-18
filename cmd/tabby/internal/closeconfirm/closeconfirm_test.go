package closeconfirm

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// stripAnsi drops SGR escape sequences so we can measure visible cell width.
func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// computeBands must yield ordered, non-overlapping, in-range row bands for any
// reasonable popup height so View placement and mouse hit-testing stay honest.
func TestComputeBandsInvariants(t *testing.T) {
	for h := 8; h <= 60; h++ {
		b := computeBands(h)
		checks := []struct {
			name       string
			start, end int
		}{
			{"title", b.titleStart, b.titleEnd},
			{"keep", b.keepStart, b.keepEnd},
			{"close", b.closeStart, b.closeEnd},
		}
		for _, c := range checks {
			if c.start < 0 || c.end >= h || c.start > c.end {
				t.Fatalf("h=%d %s band out of range: [%d,%d]", h, c.name, c.start, c.end)
			}
		}
		if b.keepEnd >= b.closeStart {
			t.Fatalf("h=%d keep/close overlap: keepEnd=%d closeStart=%d", h, b.keepEnd, b.closeStart)
		}
		if b.closeEnd >= b.hintRow {
			t.Fatalf("h=%d close band collides with hint row: closeEnd=%d hint=%d", h, b.closeEnd, b.hintRow)
		}
		if b.hintRow != h-1 {
			t.Fatalf("h=%d hint row expected %d got %d", h, h-1, b.hintRow)
		}
	}
}

// A tap inside a button band must resolve to that button; a tap in the gap or
// title must do nothing (neither confirm nor quit-as-close).
func TestMouseHitTest(t *testing.T) {
	m := initialModel("@1")
	m.height = 24
	m.width = 40
	b := computeBands(m.height)

	press := func(y int) model {
		nm, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Y: y})
		return nm.(model)
	}

	// Middle of the Keep band -> dismiss without confirming.
	if got := press((b.keepStart + b.keepEnd) / 2); got.confirmed {
		t.Fatalf("tap on Keep band should not confirm")
	}
	// Middle of the Close band -> confirm.
	if got := press((b.closeStart + b.closeEnd) / 2); !got.confirmed {
		t.Fatalf("tap on Close band should confirm")
	}
	// The gap row between the two bands -> no confirm.
	gap := b.keepEnd + 1
	if gap < b.closeStart {
		if got := press(gap); got.confirmed {
			t.Fatalf("tap in gap should not confirm")
		}
	}
}

func TestKeyHandling(t *testing.T) {
	base := func() model {
		m := initialModel("@1")
		m.width, m.height = 40, 24
		return m
	}
	send := func(m model, key string) model {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		return nm.(model)
	}

	if got := send(base(), "y"); !got.confirmed {
		t.Fatalf("y should confirm close")
	}
	if got := send(base(), "n"); got.confirmed {
		t.Fatalf("n should not confirm")
	}
	// Default selection is Keep, so Enter must NOT close.
	m := base()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if nm.(model).confirmed {
		t.Fatalf("Enter on default (Keep) selection should not confirm")
	}
	// Move selection to Close, then Enter confirms.
	m = send(base(), "j") // down -> Close
	if m.selected != choiceClose {
		t.Fatalf("j should select Close")
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !nm.(model).confirmed {
		t.Fatalf("Enter on Close selection should confirm")
	}
}

// Every rendered row must be exactly `width` cells wide so the modal fills the
// popup surface with no dark tmux-default bleeding through.
func TestViewRowsFullWidth(t *testing.T) {
	m := initialModel("@1")
	m.width, m.height = 44, 20
	view := m.View()
	rows := strings.Split(view, "\n")
	if len(rows) != m.height {
		t.Fatalf("expected %d rows, got %d", m.height, len(rows))
	}
	for i, r := range rows {
		if w := runewidth.StringWidth(stripAnsi(r)); w != m.width {
			t.Fatalf("row %d width = %d, want %d (%q)", i, w, m.width, stripAnsi(r))
		}
	}
	// Sanity: the labels are present somewhere in the plain-text view.
	plain := stripAnsi(view)
	for _, want := range []string{"Close this window?", "Keep it open", "Close window"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q", want)
		}
	}
}
