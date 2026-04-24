package tmux

import "testing"

func TestParseRemoteHost(t *testing.T) {
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
		{"mosh myhost", "myhost"},
		{"mosh user@myhost", "myhost"},
		{"mosh -p 60001 user@myhost", "myhost"},
		{"mosh --ssh=\"ssh -p 22\" myhost", "myhost"},
		{"mosh-client 1.2.3.4 60001", "1.2.3.4"},
		{"/usr/bin/ssh user@myhost", "myhost"},
	}
	for _, tt := range tests {
		got := parseRemoteHost(tt.cmdline)
		if got != tt.want {
			t.Errorf("parseRemoteHost(%q) = %q, want %q", tt.cmdline, got, tt.want)
		}
	}
}
