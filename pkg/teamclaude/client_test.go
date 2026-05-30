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
      "remaining": { "session": 0.99, "weekly": 0.72, "tokens": null },
      "usage": { "totalRequests": 5317, "lastUsed": "2026-05-30T00:49:47.547Z" },
      "rateLimitedUntil": null
    },
    {
      "name": "b@debea.si",
      "type": "oauth",
      "orgName": null,
      "status": "active",
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
