package daemon

import (
	"testing"
	"time"
)

func TestParseAttachedClients(t *testing.T) {
	out := "/dev/ttys000|185|52|1786229090\n/dev/ttys016|164|44|1786930934\n"
	got := parseAttachedClients(out)
	if len(got) != 2 {
		t.Fatalf("want 2 clients, got %d", len(got))
	}
	if got[0].TTY != "/dev/ttys000" || got[0].Width != 185 || got[0].Height != 52 {
		t.Errorf("bad first client: %+v", got[0])
	}
	if got[1].Activity != 1786930934 {
		t.Errorf("bad activity: %+v", got[1])
	}
}

func TestParseAttachedClientsSkipsMalformed(t *testing.T) {
	out := "bad\n/dev/ttys1|abc|44|1\n/dev/ttys2|164|44|1786930934\n/dev/ttys3|0|44|1\n"
	got := parseAttachedClients(out)
	if len(got) != 1 || got[0].TTY != "/dev/ttys2" {
		t.Fatalf("want only the well-formed row, got %+v", got)
	}
}

func TestSelectStaleClientsPrunesIdleMismatched(t *testing.T) {
	now := time.Unix(1786930934, 0)
	clients := []attachedClient{
		{TTY: "/dev/ttys000", Width: 185, Height: 52, Activity: now.Add(-9 * time.Hour).Unix()},
		{TTY: "/dev/ttys016", Width: 164, Height: 44, Activity: now.Unix()},
	}
	stale := selectStaleClients(clients, now, staleClientIdleThreshold)
	if len(stale) != 1 || stale[0].TTY != "/dev/ttys000" {
		t.Fatalf("want ttys000 pruned, got %+v", stale)
	}
}

func TestSelectStaleClientsKeepsSoleClient(t *testing.T) {
	now := time.Unix(1786930934, 0)
	clients := []attachedClient{
		{TTY: "/dev/ttys000", Width: 185, Height: 52, Activity: now.Add(-100 * time.Hour).Unix()},
	}
	if stale := selectStaleClients(clients, now, staleClientIdleThreshold); stale != nil {
		t.Fatalf("must never detach the only client, got %+v", stale)
	}
}

func TestSelectStaleClientsKeepsSameSize(t *testing.T) {
	now := time.Unix(1786930934, 0)
	clients := []attachedClient{
		{TTY: "/dev/ttys000", Width: 164, Height: 44, Activity: now.Add(-100 * time.Hour).Unix()},
		{TTY: "/dev/ttys016", Width: 164, Height: 44, Activity: now.Unix()},
	}
	if stale := selectStaleClients(clients, now, staleClientIdleThreshold); stale != nil {
		t.Fatalf("same-size clients do not fight over geometry, got %+v", stale)
	}
}

func TestSelectStaleClientsKeepsRecentlyActive(t *testing.T) {
	now := time.Unix(1786930934, 0)
	clients := []attachedClient{
		{TTY: "/dev/ttys000", Width: 185, Height: 52, Activity: now.Add(-5 * time.Minute).Unix()},
		{TTY: "/dev/ttys016", Width: 164, Height: 44, Activity: now.Unix()},
	}
	if stale := selectStaleClients(clients, now, staleClientIdleThreshold); stale != nil {
		t.Fatalf("a client in active use must be kept, got %+v", stale)
	}
}

func TestSelectStaleClientsNeverPrunesNewest(t *testing.T) {
	// All clients idle past the threshold: the most recent one still survives.
	now := time.Unix(1786930934, 0)
	clients := []attachedClient{
		{TTY: "/dev/ttys000", Width: 185, Height: 52, Activity: now.Add(-50 * time.Hour).Unix()},
		{TTY: "/dev/ttys016", Width: 164, Height: 44, Activity: now.Add(-9 * time.Hour).Unix()},
	}
	stale := selectStaleClients(clients, now, staleClientIdleThreshold)
	if len(stale) != 1 || stale[0].TTY != "/dev/ttys000" {
		t.Fatalf("newest must survive, got %+v", stale)
	}
}

func TestSelectStaleClientsMobileDropout(t *testing.T) {
	// A phone/SSH client that dropped without detaching: narrow, long idle.
	now := time.Unix(1786930934, 0)
	clients := []attachedClient{
		{TTY: "/dev/ttys020", Width: 80, Height: 25, Activity: now.Add(-30 * time.Hour).Unix()},
		{TTY: "/dev/ttys016", Width: 164, Height: 44, Activity: now.Unix()},
	}
	stale := selectStaleClients(clients, now, staleClientIdleThreshold)
	if len(stale) != 1 || stale[0].TTY != "/dev/ttys020" {
		t.Fatalf("want dropped mobile client pruned, got %+v", stale)
	}
}
