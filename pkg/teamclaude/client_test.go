package teamclaude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// realPayload is a capture of a live GET /teamclaude/status response, trimmed
// to the fields the client parses.
const realPayload = `{
  "currentAccount": "brendan@gunpowder.tech (brendan@gunpowder.tech's Organization)",
  "switchThreshold": 0.85,
  "accounts": [
    {
      "name": "brendan@gunpowder.tech (brendan@gunpowder.tech's Organization)",
      "type": "oauth",
      "orgName": "brendan@gunpowder.tech's Organization",
      "status": "active",
      "priority": 0,
      "extraUsageLimit": null,
      "extraUsageRemaining": null,
      "isActiveExtraUsage": false,
      "remaining": { "session": 0.99, "weekly": 0.72, "tokens": null },
      "usage": { "totalRequests": 5317, "lastUsed": "2026-05-30T00:49:47.547Z" },
      "rateLimitedUntil": null
    },
    {
      "name": "b@debea.si",
      "type": "oauth",
      "orgName": null,
      "status": "active",
      "priority": 1,
      "extraUsageLimit": 10.00,
      "extraUsageRemaining": 7.66,
      "isActiveExtraUsage": true,
      "remaining": { "session": 1, "weekly": 0.97 },
      "usage": { "totalRequests": 60, "lastUsed": "2026-05-29T20:11:39.954Z" },
      "rateLimitedUntil": "2026-05-30T01:00:00.000Z"
    }
  ]
}`

func TestFetch(t *testing.T) {
	var gotKey string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realPayload))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// Trailing slash on baseURL should be tolerated.
	st, err := Fetch(ctx, srv.URL+"/", "tc-secret")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if gotPath != "/teamclaude/status" {
		t.Errorf("path = %q, want /teamclaude/status", gotPath)
	}
	if gotKey != "tc-secret" {
		t.Errorf("x-api-key = %q, want tc-secret", gotKey)
	}
	if st.SwitchThreshold != 0.85 {
		t.Errorf("switchThreshold = %v, want 0.85", st.SwitchThreshold)
	}
	if len(st.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(st.Accounts))
	}

	a0 := st.Accounts[0]
	if a0.Remaining.Session == nil || *a0.Remaining.Session != 0.99 {
		t.Errorf("account0 session remaining = %v, want 0.99", a0.Remaining.Session)
	}
	if a0.Remaining.Weekly == nil || *a0.Remaining.Weekly != 0.72 {
		t.Errorf("account0 weekly remaining = %v, want 0.72", a0.Remaining.Weekly)
	}
	if a0.RateLimited() {
		t.Errorf("account0 should not be rate-limited")
	}
	if a0.Usage.TotalRequests != 5317 {
		t.Errorf("account0 totalRequests = %d, want 5317", a0.Usage.TotalRequests)
	}

	a1 := st.Accounts[1]
	if a1.OrgName != "" {
		t.Errorf("account1 orgName = %q, want empty (null)", a1.OrgName)
	}
	if !a1.RateLimited() {
		t.Errorf("account1 should be rate-limited")
	}

	// Extra-usage fields. account0 has no overage budget; account1 does and
	// is the active extra-usage account.
	if a0.HasExtraUsageBudget() {
		t.Errorf("account0 should not have an extra-usage budget")
	}
	if a0.IsActiveExtraUsage {
		t.Errorf("account0 should not be active extra-usage")
	}
	if !a1.HasExtraUsageBudget() {
		t.Errorf("account1 should have an extra-usage budget")
	}
	if a1.ExtraUsageLimit == nil || *a1.ExtraUsageLimit != 10.00 {
		t.Errorf("account1 extraUsageLimit = %v, want 10.00", a1.ExtraUsageLimit)
	}
	if a1.ExtraUsageRemaining == nil || *a1.ExtraUsageRemaining != 7.66 {
		t.Errorf("account1 extraUsageRemaining = %v, want 7.66", a1.ExtraUsageRemaining)
	}
	if !a1.IsActiveExtraUsage {
		t.Errorf("account1 should be active extra-usage")
	}
	if a1.Priority != 1 {
		t.Errorf("account1 priority = %d, want 1", a1.Priority)
	}
	if !st.AnyActiveExtraUsage() {
		t.Errorf("Status.AnyActiveExtraUsage = false, want true (account1 is active extra)")
	}

	// Nil receiver guard.
	var nilStatus *Status
	if nilStatus.AnyActiveExtraUsage() {
		t.Errorf("nil Status.AnyActiveExtraUsage should be false")
	}
}

func TestFetchNoKeyOmitsHeader(t *testing.T) {
	hasKey := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasKey = r.Header["X-Api-Key"]
		_, _ = w.Write([]byte(`{"accounts":[]}`))
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.URL, ""); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if hasKey {
		t.Errorf("x-api-key header should be absent when apiKey is empty")
	}
}

func TestFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.URL, "bad"); err == nil {
		t.Errorf("expected error on 401, got nil")
	}
}

func TestFetchModelsDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/teamclaude/models" {
			t.Errorf("path = %q, want /teamclaude/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"claude-opus-4-8":{"strikes":3,"until":9999999999999},"claude-3-5-haiku-20241022":{"strikes":1,"until":0}}`))
	}))
	defer srv.Close()

	m, err := FetchModels(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(m))
	}
	if m["claude-opus-4-8"].Strikes != 3 {
		t.Errorf("opus-4-8 strikes = %d, want 3", m["claude-opus-4-8"].Strikes)
	}
	// nowMs in the past relative to the far-future Until => opus is active,
	// haiku (Until 0) is not.
	active := m.ActiveDegradations(1)
	if len(active) != 1 || active[0] != "claude-opus-4-8" {
		t.Errorf("ActiveDegradations = %v, want [claude-opus-4-8]", active)
	}
	if FallbackMap["claude-opus-4-8"] != "claude-opus-4-7" {
		t.Errorf("FallbackMap[opus-4-8] = %q, want claude-opus-4-7", FallbackMap["claude-opus-4-8"])
	}
}

func TestFetchModelsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m, err := FetchModels(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("len(models) = %d, want 0", len(m))
	}
	if got := m.ActiveDegradations(time.Now().UnixMilli()); len(got) != 0 {
		t.Errorf("ActiveDegradations = %v, want empty", got)
	}
}

// TestFetchModels404 covers the older teamclaude server that predates the
// /teamclaude/models endpoint: 404 must be treated as "no degradation".
func TestFetchModels404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m, err := FetchModels(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatalf("FetchModels on 404 should not error, got %v", err)
	}
	if len(m) != 0 {
		t.Errorf("FetchModels on 404 = %v, want empty map", m)
	}
}

func TestFetchModelsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := FetchModels(context.Background(), srv.URL, "k"); err == nil {
		t.Errorf("expected error on 500, got nil")
	}
}
