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
	"sort"
	"strings"
)

// Status is the top-level payload returned by GET /teamclaude/status. It maps
// onto accountManager.getStatus() in the teamclaude server.
type Status struct {
	CurrentAccount  string    `json:"currentAccount"`
	SwitchThreshold float64   `json:"switchThreshold"`
	Accounts        []Account `json:"accounts"`
}

// AnyActiveExtraUsage reports whether ANY account is currently drawing on its
// extra-usage budget — the proxy has fallen past normal quota onto a paid
// overage tier. Useful for header-level "we're in extra-usage territory"
// indicators.
func (s *Status) AnyActiveExtraUsage() bool {
	if s == nil {
		return false
	}
	for _, a := range s.Accounts {
		if a.IsActiveExtraUsage {
			return true
		}
	}
	return false
}

// Account is one managed proxy account.
type Account struct {
	Name             string    `json:"name"`
	Type             string    `json:"type"`    // "oauth" | "apikey"
	OrgName          string    `json:"orgName"` // may be empty
	Status           string    `json:"status"`  // "active" | ...
	Priority         int       `json:"priority"`
	Quota            Quota     `json:"quota"`
	Remaining        Remaining `json:"remaining"`
	Usage            Usage     `json:"usage"`
	RateLimitedUntil *string   `json:"rateLimitedUntil"` // ISO time, or null

	// Extra-usage / overage fields. teamclaude's account-manager surfaces a
	// per-account dollar budget that can be drawn down once normal quota is
	// exhausted (priority > 0 accounts are reached only after primaries hit
	// the switch threshold). All three are zero/nil when extra usage isn't
	// configured for the account or no usage window data is available.
	//
	//   ExtraUsageLimit:     total $ ceiling for this fallback budget.
	//   ExtraUsageRemaining: $ left in the current window (limit - cost).
	//   IsActiveExtraUsage:  this account is the currently-selected one AND
	//                        priority > 0, i.e. extra-usage charging right now.
	ExtraUsageLimit     *float64 `json:"extraUsageLimit"`
	ExtraUsageRemaining *float64 `json:"extraUsageRemaining"`
	IsActiveExtraUsage  bool     `json:"isActiveExtraUsage"`
}

// HasExtraUsageBudget reports whether an extra-usage dollar budget is
// configured on this account (regardless of whether it is currently active).
func (a Account) HasExtraUsageBudget() bool {
	return a.ExtraUsageLimit != nil
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

// DegradedModel is the per-model degradation record from GET /teamclaude/models.
// The teamclaude fork counts consecutive 529s/timeouts per model; once a model
// accumulates enough strikes it is marked degraded for a cooldown window and
// requests for it are transparently routed to a smaller fallback model.
type DegradedModel struct {
	// Strikes is the running count of consecutive overload responses. A model
	// can have strikes building up without yet being actively degraded.
	Strikes int `json:"strikes"`
	// Until is the epoch-millisecond timestamp the degradation (and thus the
	// active downgrade routing) expires. 0 means "strikes building, not yet
	// actively degraded"; Until > now means the model is being downgraded now.
	Until int64 `json:"until"`
}

// Models is the payload returned by GET /teamclaude/models: a map of model name
// to its degradation record. An empty map means every model is healthy.
type Models map[string]DegradedModel

// ActiveDegradations returns the names of models that are actively being
// downgraded right now (Until in the future relative to nowMs), sorted so the
// widget, the state hash, and the popup all agree on ordering. Pass
// time.Now().UnixMilli() for nowMs.
func (m Models) ActiveDegradations(nowMs int64) []string {
	var out []string
	for name, d := range m {
		if d.Until > nowMs {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// FallbackMap mirrors the teamclaude server's FALLBACK_MAP (server.js): the
// smaller model each model is downgraded to when degraded. Kept here purely for
// display (the popup shows "model -> fallback"); the server owns the real
// routing. Keep in sync with github.com/brendandebeasi/teamclaude src/server.js.
var FallbackMap = map[string]string{
	"claude-opus-4-9":            "claude-opus-4-8",
	"claude-opus-4-8":            "claude-opus-4-7",
	"claude-3-5-sonnet-20241022": "claude-3-5-sonnet-20240620",
	"claude-3-5-sonnet-20240620": "claude-3-5-haiku-20241022",
	"claude-3-5-haiku-20241022":  "claude-3-haiku-20240307",
}

// FetchModels retrieves the degraded-model map from a teamclaude proxy at
// baseURL. Auth mirrors Fetch (x-api-key when apiKey is non-empty). Older
// teamclaude servers predate this endpoint and return 404 — that is treated as
// "no degradation" (empty map, nil error) so callers degrade gracefully until
// the fork is deployed. Any other non-200 is a real error.
func FetchModels(ctx context.Context, baseURL, apiKey string) (Models, error) {
	url := strings.TrimRight(baseURL, "/") + "/teamclaude/models"
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

	if resp.StatusCode == http.StatusNotFound {
		// Older server without the /teamclaude/models endpoint.
		return Models{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("teamclaude: status %d from %s", resp.StatusCode, url)
	}

	var models Models
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("teamclaude: decode models: %w", err)
	}
	if models == nil {
		models = Models{}
	}
	return models, nil
}
