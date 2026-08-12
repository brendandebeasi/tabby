package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func TestResolveAutoTheme(t *testing.T) {
	tests := []struct {
		name string
		at   config.AutoTheme
		want string
	}{
		{
			name: "disabled yields no override",
			at:   config.AutoTheme{Enabled: false, Mode: "dark", Light: "day", Dark: "night"},
			want: "",
		},
		{
			name: "light mode picks the light theme",
			at:   config.AutoTheme{Enabled: true, Mode: "light", Light: "day", Dark: "night"},
			want: "day",
		},
		{
			name: "dark mode picks the dark theme",
			at:   config.AutoTheme{Enabled: true, Mode: "dark", Light: "day", Dark: "night"},
			want: "night",
		},
		{
			name: "unrecognized mode falls back to dark",
			at:   config.AutoTheme{Enabled: true, Mode: "system", Light: "day", Dark: "night"},
			want: "night",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAutoTheme(&config.Config{AutoTheme: tc.at})
			if got != tc.want {
				t.Errorf("resolveAutoTheme() = %q, want %q", got, tc.want)
			}
		})
	}
}
