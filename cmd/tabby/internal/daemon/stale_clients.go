package daemon

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// staleClientIdleThreshold is how long an attached client must have been
// silent before it is eligible for pruning. A client that is merely idle is
// left alone no matter how old it is -- a terminal left open overnight is
// normal and detaching it would be hostile. Only idleness *combined* with a
// size disagreement (see pruneStaleClients) makes a client a problem.
const staleClientIdleThreshold = 8 * time.Hour

// attachedClient is one row of `tmux list-clients`.
type attachedClient struct {
	TTY      string
	Width    int
	Height   int
	Activity int64 // unix seconds
}

// parseAttachedClients parses `tmux list-clients` output formatted as
// "tty|width|height|activity". Malformed rows are skipped rather than
// failing the whole parse, so one odd client cannot disable pruning.
func parseAttachedClients(out string) []attachedClient {
	var clients []attachedClient
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}
		w, err1 := strconv.Atoi(strings.TrimSpace(parts[1]))
		h, err2 := strconv.Atoi(strings.TrimSpace(parts[2]))
		act, err3 := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || w <= 0 || h <= 0 {
			continue
		}
		clients = append(clients, attachedClient{
			TTY: strings.TrimSpace(parts[0]), Width: w, Height: h, Activity: act,
		})
	}
	return clients
}

// selectStaleClients decides which clients should be detached.
//
// A client is pruned only when all of these hold:
//
//   - more than one client is attached (never detach the only client, which
//     would drop the user out of tmux entirely);
//   - its geometry differs from the most recently active client's, so with
//     window-size latest it is actively fighting over window size;
//   - it has been idle longer than staleClientIdleThreshold.
//
// The most recently active client is always kept -- that is the one the user
// is presumed to be sitting at. This is what stops forgotten desktop
// attachments and dropped mobile/SSH sessions (which never send a clean
// detach) from oscillating window geometry.
func selectStaleClients(clients []attachedClient, now time.Time, idleThreshold time.Duration) []attachedClient {
	if len(clients) < 2 {
		return nil
	}
	newest := clients[0]
	for _, c := range clients[1:] {
		if c.Activity > newest.Activity {
			newest = c
		}
	}
	var stale []attachedClient
	for _, c := range clients {
		if c.TTY == newest.TTY {
			continue
		}
		if c.Width == newest.Width && c.Height == newest.Height {
			continue // agrees on size; harmless
		}
		if now.Sub(time.Unix(c.Activity, 0)) < idleThreshold {
			continue // still in use
		}
		stale = append(stale, c)
	}
	return stale
}

// listAttachedClients queries tmux for currently attached clients.
func listAttachedClients() ([]attachedClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	out, err := tmux.CmdContext(ctx,
		listClientsArgs("#{client_tty}|#{client_width}|#{client_height}|#{client_activity}")...).Output()
	if err != nil {
		return nil, err
	}
	return parseAttachedClients(string(out)), nil
}

// PruneStaleClients detaches idle clients whose geometry disagrees with the
// active client. Returns the ttys detached. Safe to call on a timer.
func (c *Coordinator) PruneStaleClients() []string {
	clients, err := listAttachedClients()
	if err != nil {
		return nil
	}
	stale := selectStaleClients(clients, time.Now(), staleClientIdleThreshold)
	if len(stale) == 0 {
		return nil
	}
	var detached []string
	for _, sc := range stale {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		err := tmux.CmdContext(ctx, "detach-client", "-t", sc.TTY).Run()
		cancel()
		if err != nil {
			continue
		}
		detached = append(detached, sc.TTY)
		logEvent("STALE_CLIENT_DETACH tty=%s size=%dx%d idle=%s",
			sc.TTY, sc.Width, sc.Height,
			time.Since(time.Unix(sc.Activity, 0)).Round(time.Minute))
	}
	return detached
}
