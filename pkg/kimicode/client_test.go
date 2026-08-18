package kimicode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const samplePayload = `{
  "user": {"userId": "u1", "membership": {"level": "LEVEL_STANDARD"}},
  "usage": {"limit": "100", "used": "1", "remaining": "99", "resetTime": "2026-08-25T16:03:43.666124Z"},
  "limits": [{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
              "detail": {"limit": "100", "used": "6", "remaining": "94", "resetTime": "2026-08-18T18:03:43.666124Z"}}],
  "parallel": {"limit": "30"}
}`

func TestFetchParsesUsages(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coding/v1/usages" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(json.RawMessage(samplePayload))
	}))
	defer srv.Close()

	u, err := Fetch(context.Background(), srv.URL+"/coding/v1", "test-key")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}

	sess := u.Session()
	if sess == nil {
		t.Fatal("Session() = nil")
	}
	if frac, ok := sess.RemainingFrac(); !ok || frac < 0.93 || frac > 0.95 {
		t.Errorf("session frac = %v, %v; want ~0.94, true", frac, ok)
	}
	wk := u.Weekly()
	if frac, ok := wk.RemainingFrac(); !ok || frac != 0.99 {
		t.Errorf("weekly frac = %v, %v; want 0.99, true", frac, ok)
	}
	if ms := wk.ResetMs(); ms <= 0 {
		t.Errorf("weekly ResetMs = %d, want > 0", ms)
	}
	if u.User.Membership.Level != "LEVEL_STANDARD" {
		t.Errorf("membership = %q", u.User.Membership.Level)
	}
}

func TestFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL, "bad"); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestRemainingFracGuards(t *testing.T) {
	var nilQ *Quota
	if _, ok := nilQ.RemainingFrac(); ok {
		t.Error("nil quota should not be ok")
	}
	q := &Quota{Limit: "0", Remaining: "0"}
	if _, ok := q.RemainingFrac(); ok {
		t.Error("zero limit should not be ok")
	}
	q = &Quota{Limit: "100", Remaining: "abc"}
	if _, ok := q.RemainingFrac(); ok {
		t.Error("unparsable remaining should not be ok")
	}
	if ms := (&Quota{}).ResetMs(); ms != 0 {
		t.Errorf("empty ResetTime should give 0, got %d", ms)
	}
}
