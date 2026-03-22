package daemon

import (
	"encoding/json"
	"testing"
)

func TestViewportUpdatePayloadRoundTrip(t *testing.T) {
	cases := []ViewportUpdatePayload{
		{ViewportOffset: 0},
		{ViewportOffset: 10},
		{ViewportOffset: 99},
	}
	for _, c := range cases {
		data, _ := json.Marshal(c)
		var out ViewportUpdatePayload
		json.Unmarshal(data, &out)
		if out != c {
			t.Errorf("round-trip failed: in=%+v out=%+v", c, out)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsAt(s, sub))
}

func containsAt(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
