package sidebar

import "testing"

// Emoji with a variation selector (keycaps, ▶️, ⚠️) are drawn as two columns
// but measured as one by rune-based width. A picker row that under-measures
// overflows its pane, wraps, and scrolls the alt-screen frame.
func TestDisplayWidthCountsEmojiPresentationAsTwoColumns(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1️⃣", 2},
		{"▶️", 2},
		{"⚠️", 2},
		{"🐈", 2},
		{"💰", 2},
		{"a", 1},
		{"├", 1},
		{"", 0},
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateToWidthNeverExceedsTarget(t *testing.T) {
	for _, s := range []string{
		"1️⃣  keycap one",
		"▶️  play marker",
		"⚠️  warning",
		"🐈  cat tab",
		"plain ascii row",
	} {
		for w := 0; w <= 20; w++ {
			if got := displayWidth(truncateToWidth(s, w)); got > w {
				t.Errorf("truncateToWidth(%q, %d) drew %d columns", s, w, got)
			}
		}
	}
}

func TestClampToWidthNeverExceedsTarget(t *testing.T) {
	for _, s := range []string{
		"1️⃣  keycap one",
		"\033[31m▶️  colored\033[0m",
		"⚠️  warning",
	} {
		for w := 0; w <= 20; w++ {
			if got := displayWidth(stripAnsi(clampToWidth(s, w))); got > w {
				t.Errorf("clampToWidth(%q, %d) drew %d columns", s, w, got)
			}
		}
	}
}
