// Package kimicode is a tiny HTTP client for the Kimi for Coding quota
// endpoint. Kimi for Coding (api.kimi.com/coding/v1) exposes GET /usages,
// which reports the account's weekly quota plus per-window rate limits (the
// 300-minute / 5h session window). The tabby Kimi sidebar widget consumes
// this to show how much session and weekly headroom is left.
//
// The package deliberately depends on the standard library only, mirroring
// pkg/teamclaude's zero-dependency design.
package kimicode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Usages is the payload returned by GET <base>/usages.
type Usages struct {
	User struct {
		Membership struct {
			Level string `json:"level"` // e.g. "LEVEL_STANDARD"
		} `json:"membership"`
	} `json:"user"`
	Usage   Quota         `json:"usage"`   // weekly (7d) window
	Limits  []WindowLimit `json:"limits"`  // rate-limit windows; the 300-minute one is the session (5h) window
	Parallel struct {
		Limit string `json:"limit"`
	} `json:"parallel"`
}

// Quota is one quota window. Values arrive as decimal strings; ResetTime is
// RFC3339.
type Quota struct {
	Limit     string `json:"limit"`
	Used      string `json:"used"`
	Remaining string `json:"remaining"`
	ResetTime string `json:"resetTime"`
}

// WindowLimit pairs a rate-limit window descriptor with its quota detail.
type WindowLimit struct {
	Window struct {
		Duration int    `json:"duration"`
		TimeUnit string `json:"timeUnit"` // e.g. "TIME_UNIT_MINUTE"
	} `json:"window"`
	Detail Quota `json:"detail"`
}

// Session returns the session (300-minute / 5h) window quota, or nil when the
// server reported no rate-limit windows.
func (u *Usages) Session() *Quota {
	if u == nil {
		return nil
	}
	for _, l := range u.Limits {
		if l.Window.TimeUnit == "TIME_UNIT_MINUTE" && l.Window.Duration == 300 {
			q := l.Detail
			return &q
		}
	}
	if len(u.Limits) > 0 {
		q := u.Limits[0].Detail
		return &q
	}
	return nil
}

// Weekly returns the weekly (7d) window quota.
func (u *Usages) Weekly() *Quota {
	if u == nil {
		return nil
	}
	return &u.Usage
}

// RemainingFrac returns the fraction (0..1) of quota left in the window.
// ok is false when the values are missing or unparsable, or the limit is 0.
func (q *Quota) RemainingFrac() (frac float64, ok bool) {
	if q == nil {
		return 0, false
	}
	rem, err := strconv.ParseFloat(strings.TrimSpace(q.Remaining), 64)
	if err != nil {
		return 0, false
	}
	lim, err := strconv.ParseFloat(strings.TrimSpace(q.Limit), 64)
	if err != nil || lim <= 0 {
		return 0, false
	}
	return rem / lim, true
}

// ResetMs returns the window reset time as epoch milliseconds, 0 when unknown.
func (q *Quota) ResetMs() int64 {
	if q == nil || q.ResetTime == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, q.ResetTime)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// Fetch retrieves the quota usage from a Kimi for Coding base URL (e.g.
// "https://api.kimi.com/coding/v1"). apiKey is sent as a Bearer token. The
// caller controls the timeout via ctx.
func Fetch(ctx context.Context, baseURL, apiKey string) (*Usages, error) {
	url := strings.TrimRight(baseURL, "/") + "/usages"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kimicode: status %d from %s", resp.StatusCode, url)
	}

	var usages Usages
	if err := json.NewDecoder(resp.Body).Decode(&usages); err != nil {
		return nil, fmt.Errorf("kimicode: decode: %w", err)
	}
	return &usages, nil
}
