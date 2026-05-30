// Package teamclaude is a tiny HTTP client for a teamclaude proxy's status
// endpoint. teamclaude (https://github.com/karpeleslab/teamclaude) is a
// multi-account Claude proxy that rotates between accounts based on quota; its
// server exposes GET /teamclaude/status with per-account session (5h) and
// weekly (7d) quota utilization. The tabby TeamClaude sidebar widget consumes
// this to show how much quota each managed account has left.
//
// The package deliberately depends on the standard library only, mirroring
// teamclaude's own zero-dependency design.
package teamclaude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Status is the top-level payload returned by GET /teamclaude/status. It maps
// onto accountManager.getStatus() in the teamclaude server.
type Status struct {
	CurrentAccount  string    `json:"currentAccount"`
	SwitchThreshold float64   `json:"switchThreshold"`
	Accounts        []Account `json:"accounts"`
}

// Account is one managed proxy account.
type Account struct {
	Name             string    `json:"name"`
	Type             string    `json:"type"`    // "oauth" | "apikey"
	OrgName          string    `json:"orgName"` // may be empty
	Status           string    `json:"status"`  // "active" | ...
	Quota            Quota     `json:"quota"`
	Remaining        Remaining `json:"remaining"`
	Usage            Usage     `json:"usage"`
	RateLimitedUntil *string   `json:"rateLimitedUntil"` // ISO time, or null
}

// Quota holds the raw quota window data. We only consume the reset timestamps
// (epoch milliseconds) here; utilization is exposed pre-computed via Remaining.
type Quota struct {
	Unified5hReset int64 `json:"unified5hReset"` // session window reset (ms epoch), 0 if unknown
	Unified7dReset int64 `json:"unified7dReset"` // weekly window reset (ms epoch), 0 if unknown
}

// Remaining holds the fraction (0..1) of quota left in each window. The server
// pre-computes these from utilization, so we consume them directly. Pointers
// distinguish "unknown" (null) from a real 0.0.
type Remaining struct {
	Session *float64 `json:"session"` // 5h window
	Weekly  *float64 `json:"weekly"`  // 7d window
}

// Usage holds cumulative per-account counters.
type Usage struct {
	TotalRequests int    `json:"totalRequests"`
	LastUsed      string `json:"lastUsed"` // ISO time, or empty
}

// RateLimited reports whether the account is currently rate-limited.
func (a Account) RateLimited() bool {
	return a.RateLimitedUntil != nil && *a.RateLimitedUntil != ""
}

// Fetch retrieves the status from a teamclaude proxy at baseURL (e.g.
// "http://192.168.23.102:8081"). When apiKey is non-empty it is sent as the
// x-api-key header (the proxy skips auth for localhost but requires it
// otherwise). The caller controls the timeout via ctx.
func Fetch(ctx context.Context, baseURL, apiKey string) (*Status, error) {
	url := strings.TrimRight(baseURL, "/") + "/teamclaude/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("teamclaude: status %d from %s", resp.StatusCode, url)
	}

	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("teamclaude: decode: %w", err)
	}
	return &status, nil
}
