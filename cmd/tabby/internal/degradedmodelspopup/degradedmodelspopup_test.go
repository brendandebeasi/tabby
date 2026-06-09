package degradedmodelspopup

import (
	"strings"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/teamclaude"
)

func TestBuildRowsSkipsInactiveAndMapsFallback(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	nowMs := now.UnixMilli()
	models := teamclaude.Models{
		"claude-opus-4-8":           {Strikes: 3, Until: nowMs + 4*60*1000}, // active, ~4m left
		"claude-3-5-haiku-20241022": {Strikes: 1, Until: 0},                 // strikes building, not active
		"claude-opus-4-7":           {Strikes: 5, Until: nowMs - 1000},      // expired
	}

	rows := buildRows(models, now)
	if len(rows) != 1 {
		t.Fatalf("buildRows returned %d rows, want 1 (only the active one)", len(rows))
	}
	r := rows[0]
	if r.model != "claude-opus-4-8" {
		t.Errorf("row model = %q, want claude-opus-4-8", r.model)
	}
	if r.fallback != "claude-opus-4-7" {
		t.Errorf("row fallback = %q, want claude-opus-4-7", r.fallback)
	}
	if r.strikes != 3 {
		t.Errorf("row strikes = %d, want 3", r.strikes)
	}
	if r.resetIn <= 3*time.Minute || r.resetIn > 4*time.Minute {
		t.Errorf("row resetIn = %v, want ~4m", r.resetIn)
	}
}

func TestViewDegradedShowsFallbackAndCountdown(t *testing.T) {
	m := model{
		currentAccount: "me@example.com",
		rows: []degradedRow{
			{model: "claude-opus-4-8", fallback: "claude-opus-4-7", strikes: 3, resetIn: 4 * time.Minute},
		},
	}
	// Strip ANSI so we assert on visible text only.
	out := stripANSI(m.View())
	for _, want := range []string{"overload", "opus-4-8", "opus-4-7", "→", "strikes 3", "resets in 4m", "Anthropic status", "DownDetector"} {
		if !strings.Contains(out, want) {
			t.Errorf("View output missing %q\n---\n%s", want, out)
		}
	}
}

func TestViewHealthy(t *testing.T) {
	out := stripANSI(model{}.View())
	if !strings.Contains(out, "All models healthy") {
		t.Errorf("empty model View should say healthy, got:\n%s", out)
	}
}

// stripANSI removes CSI escape sequences for plain-text assertions.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			// skip until a letter terminates the CSI sequence
			for i < len(s) && !((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
