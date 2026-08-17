package daemon

import (
	"testing"
	"time"
)

// A window that just appeared must not have its sidebar width adopted as the
// global width. This reproduces the observed failure where a new window
// spawned with a transient width of 21 and dragged every other sidebar down
// with it (WIDTH_SYNC_ADOPT active=@1649 from=43 to=21).
func TestIsWindowSettling(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		firstSeen map[string]time.Time
		active    string
		want      bool
	}{
		{
			name:      "brand new window is settling",
			firstSeen: map[string]time.Time{"@1649": now.Add(-100 * time.Millisecond)},
			active:    "@1649",
			want:      true,
		},
		{
			name:      "second sync pass mid-spawn is still settling",
			firstSeen: map[string]time.Time{"@1649": now.Add(-600 * time.Millisecond)},
			active:    "@1649",
			want:      true,
		},
		{
			name:      "settled window may be adopted",
			firstSeen: map[string]time.Time{"@1649": now.Add(-sidebarWindowSettlePeriod - time.Second)},
			active:    "@1649",
			want:      false,
		},
		{
			name:      "boundary is exclusive",
			firstSeen: map[string]time.Time{"@1649": now.Add(-sidebarWindowSettlePeriod)},
			active:    "@1649",
			want:      false,
		},
		{
			name:      "unknown window is not settling",
			firstSeen: map[string]time.Time{"@1": now},
			active:    "@1649",
			want:      false,
		},
		{
			name:      "no active window",
			firstSeen: map[string]time.Time{"@1649": now},
			active:    "",
			want:      false,
		},
		{
			name:      "nil map",
			firstSeen: nil,
			active:    "@1649",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowSettling(tt.firstSeen, tt.active, now); got != tt.want {
				t.Errorf("isWindowSettling() = %v, want %v", got, tt.want)
			}
		})
	}
}
