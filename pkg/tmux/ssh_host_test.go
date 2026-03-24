package tmux

import "testing"

func TestParseSSHHost(t *testing.T) {
	tests := []struct {
		cmdline string
		want    string
	}{
		{"ssh myhost", "myhost"},
		{"ssh user@myhost", "myhost"},
		{"ssh -p 22 myhost", "myhost"},
		{"ssh -p 22 user@myhost", "myhost"},
		{"ssh -i ~/.ssh/id_rsa user@myhost", "myhost"},
		{"ssh -J jumphost user@target", "target"},
		{"ssh -o StrictHostKeyChecking=no myhost", "myhost"},
		{"ssh -L 8080:localhost:80 user@myhost", "myhost"},
		{"ssh -D 1080 -p 2222 -l admin myhost", "myhost"},
		{"ssh -46 myhost", "myhost"},
		{"ssh", ""},
		{"bash", ""},
		{"vim", ""},
		{"", ""},
		{"ssh -p", ""},
		{"ssh myhost:path", "myhost"},
		{"ssh user@myhost:22", "myhost"},
	}
	for _, tt := range tests {
		got := parseSSHHost(tt.cmdline)
		if got != tt.want {
			t.Errorf("parseSSHHost(%q) = %q, want %q", tt.cmdline, got, tt.want)
		}
	}
}
