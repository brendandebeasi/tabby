package daemon

import "testing"

// A grouped session pair shares one window set, and tmux attaches the client
// to only one of the two sessions. The daemon whose renderers own the sidebars
// can be the peer, whose active-window flag names a window nobody is looking
// at -- so a drag went unexamined by the adopt guards and the per-window loop
// restored it (AUDIT_WIDTH_DRIFT ... @1952=44 global=25, then WIDTH_SYNC_PLAN
// client=@1952 active=@1914 current=44 target=25).
func TestWidthAdoptSubject(t *testing.T) {
	managed := map[string]bool{"@1910": true, "@1914": true, "@1952": true}

	cases := []struct {
		name           string
		sessionActive  string
		clientDisplays string
		managed        map[string]bool
		want           string
	}{
		{
			name:           "grouped_peer_client_elsewhere",
			sessionActive:  "@1914",
			clientDisplays: "@1952",
			managed:        managed,
			want:           "@1952",
		},
		{
			name:           "ungrouped_agrees",
			sessionActive:  "@1914",
			clientDisplays: "@1914",
			managed:        managed,
			want:           "@1914",
		},
		{
			name:           "no_client_attached",
			sessionActive:  "@1914",
			clientDisplays: "",
			managed:        managed,
			want:           "@1914",
		},
		{
			name:           "client_in_unrelated_session",
			sessionActive:  "@1914",
			clientDisplays: "@538",
			managed:        managed,
			want:           "@1914",
		},
		{
			name:           "displayed_window_opted_out_of_width_sync",
			sessionActive:  "@1914",
			clientDisplays: "@1952",
			managed:        map[string]bool{"@1914": true, "@1952": false},
			want:           "@1914",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := widthAdoptSubject(tc.sessionActive, tc.clientDisplays, tc.managed)
			if got != tc.want {
				t.Fatalf("widthAdoptSubject(%q, %q) = %q, want %q",
					tc.sessionActive, tc.clientDisplays, got, tc.want)
			}
		})
	}
}
