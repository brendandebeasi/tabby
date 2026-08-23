package daemon

import "testing"

func TestPaneIDLessOrdersNumerically(t *testing.T) {
	if !paneIDLess("%9", "%10") {
		t.Fatal("%9 must sort before %10")
	}
	if paneIDLess("%10", "%9") {
		t.Fatal("%10 must not sort before %9")
	}
	if !paneIDLess("%parked", "%zzz") {
		t.Fatal("non-numeric ids should fall back to string order")
	}
}

// A five-session group makes tmux report every pane five times; the dedup
// paths must see one row per pane or they reap the only real header.
func TestUniqueByPaneID(t *testing.T) {
	rows := []string{
		"@1|||%10|||window-header|||cmd",
		"@1|||%10|||window-header|||cmd",
		"@1|||%10|||window-header|||cmd",
		"@1|||%11|||window-header|||cmd",
		"",
		"@1", // too few fields
	}
	got := uniqueByPaneID(rows, 1)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(got), got)
	}
	if got[0] != rows[0] || got[1] != rows[3] {
		t.Fatalf("wrong rows kept: %v", got)
	}
}

func TestHeaderOwnedByLivePeer(t *testing.T) {
	const self = "$209"
	live := map[string]bool{"$233": true, "$999": false}
	daemons := map[string]bool{"$233": true, "$999": false}

	cases := []struct {
		name     string
		startCmd string
		want     bool
	}{
		{"own header", "tabby window-header -session '$209' -window '@1'", false},
		{"no session flag", "tabby window-header -window '@1'", false},
		{"live peer", "tabby window-header -session '$233' -window '@1'", true},
		{"dead peer", "tabby window-header -session '$999' -window '@1'", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := headerOwnedByLivePeer(tc.startCmd, self, live, daemons)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
