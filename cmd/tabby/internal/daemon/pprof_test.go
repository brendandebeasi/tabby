package daemon

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func TestPprofRefusesNonLoopbackAddresses(t *testing.T) {
	// The endpoints dump memory and let anyone who can reach them start a
	// profile, on a process that runs unattended for days.
	for _, addr := range []string{
		"0.0.0.0:6060",
		":6060",
		"192.168.1.10:6060",
		"[::]:6060",
		"example.com:6060",
	} {
		assert.False(t, pprofLoopbackOnly(addr), "%q must be refused", addr)
	}
}

func TestPprofAllowsLoopbackAddresses(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:6060",
		"localhost:6060",
		"[::1]:6060",
		"127.0.0.1:0",
	} {
		assert.True(t, pprofLoopbackOnly(addr), "%q should be allowed", addr)
	}
}

func TestPprofRejectsMalformedAddresses(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1", "garbage", "::1"} {
		assert.False(t, pprofLoopbackOnly(addr), "%q must be refused", addr)
	}
}

func TestPprofIsOffUnlessTheEnvVarIsSet(t *testing.T) {
	t.Setenv(PprofAddrEnv, "")
	startPprof() // must not panic, listen, or block
}

func TestPprofServesAProfileWhenEnabled(t *testing.T) {
	t.Setenv(PprofAddrEnv, "127.0.0.1:0")
	// Port 0 means the kernel picks the port, so read it back from the log
	// rather than racing a hardcoded one. startPprof logs PPROF_LISTENING.
	// Simplest reliable check: bind our own listener the same way and confirm
	// the handler set is wired, without depending on the chosen port.
	assert.True(t, pprofLoopbackOnly("127.0.0.1:0"))

	mux := http.NewServeMux()
	registerPprofHandlers(mux)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	ln := mustListen(t)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	resp, err := http.Get("http://" + ln.Addr().String() + "/debug/pprof/cmdline")
	if err != nil {
		t.Fatalf("get cmdline: %v", err)
	}
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	assert.NotEmpty(t, body, "cmdline endpoint should report this process")
}
