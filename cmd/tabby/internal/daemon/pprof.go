package daemon

import (
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"
)

// PprofAddrEnv names the environment variable that turns the daemon's
// profiler on. It is off unless set, so the listener costs nothing in normal
// operation.
//
// It exists because the usual out-of-band options don't work here. macOS
// `sample` walks a Go process's stacks down to runtime.asmcgocall and stops,
// so it reports where the runtime parked a goroutine rather than which of the
// daemon's own tickers is doing the work — the reason an earlier attempt to
// attribute this daemon's idle CPU guessed wrong.
//
//	TABBY_PPROF_ADDR=127.0.0.1:6060 tabby daemon -session '$1'
//	go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/profile
const PprofAddrEnv = "TABBY_PPROF_ADDR"

// pprofLoopbackOnly rejects any address that would expose the profiler off the
// machine. The endpoints leak memory contents and allow a CPU profile to be
// started by anyone who can reach them, and a daemon runs unattended for days.
func pprofLoopbackOnly(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// registerPprofHandlers wires the profiling endpoints onto mux. Split out from
// startPprof so a test can exercise the handler set without depending on which
// port the kernel hands out.
func registerPprofHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

// startPprof serves net/http/pprof when PprofAddrEnv is set to a loopback
// address. Failures are logged and ignored: a daemon that cannot profile
// itself should still run.
func startPprof() {
	addr := strings.TrimSpace(os.Getenv(PprofAddrEnv))
	if addr == "" {
		return
	}
	if !pprofLoopbackOnly(addr) {
		logEvent("PPROF_REFUSED addr=%s reason=not_loopback", addr)
		return
	}

	mux := http.NewServeMux()
	registerPprofHandlers(mux)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logEvent("PPROF_LISTEN_FAILED addr=%s err=%v", addr, err)
		return
	}
	logEvent("PPROF_LISTENING addr=%s", ln.Addr())

	srv := &http.Server{
		Handler: mux,
		// A CPU profile is a long read by design, so only the header has a
		// deadline; without one an abandoned connection pins a goroutine for
		// the daemon's whole multi-day life.
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logEvent("PPROF_SERVE_ENDED err=%v", err)
		}
	}()
}
