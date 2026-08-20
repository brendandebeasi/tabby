package daemon

import "testing"

func TestRendererSessionID(t *testing.T) {
	cases := []struct {
		startCmd string
		want     string
	}{
		{"printf '\\033[?25l' && exec -a sidebar-renderer '/bin/tabby' render sidebar -session '$209' -window '@1780' ", "$209"},
		{"tabby render sidebar -session $216 -window @1", "$216"},
		{"tabby render sidebar -window @1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := rendererSessionID(c.startCmd); got != c.want {
			t.Errorf("rendererSessionID(%q) = %q, want %q", c.startCmd, got, c.want)
		}
	}
}

func TestSessionAliveUsesCache(t *testing.T) {
	cache := map[string]bool{"$209": true, "$216": false}
	if !sessionAlive("$209", cache) {
		t.Error("cached live session reported dead")
	}
	if sessionAlive("$216", cache) {
		t.Error("cached dead session reported live")
	}
}
