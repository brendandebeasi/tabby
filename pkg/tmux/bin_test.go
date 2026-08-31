package tmux

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// resetBin puts the package-level resolution back to its pre-Bin state so a
// test can drive it with its own stub. Restores the real one on cleanup.
func resetBin(t *testing.T, stub func(string) (string, error)) {
	t.Helper()
	prev := lookPath
	t.Cleanup(func() {
		lookPath = prev
		binOnce = sync.Once{}
		binPath = ""
	})
	lookPath = stub
	binOnce = sync.Once{}
	binPath = ""
}

func TestBinResolvesOnce(t *testing.T) {
	calls := 0
	resetBin(t, func(name string) (string, error) {
		calls++
		return "/opt/fake/bin/" + name, nil
	})

	for i := 0; i < 50; i++ {
		if got := Bin(); got != "/opt/fake/bin/tmux" {
			t.Fatalf("Bin() = %q, want /opt/fake/bin/tmux", got)
		}
	}
	if calls != 1 {
		t.Fatalf("resolved the tmux path %d times, want 1: the whole point is to stop stat-ing PATH per exec", calls)
	}
}

func TestBinResolvesOnceUnderConcurrentUse(t *testing.T) {
	calls := 0
	resetBin(t, func(name string) (string, error) {
		calls++
		return "/opt/fake/bin/" + name, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Bin()
		}()
	}
	wg.Wait()

	if calls != 1 {
		t.Fatalf("resolved the tmux path %d times across goroutines, want 1", calls)
	}
}

func TestBinFallsBackToBareNameWhenLookupFails(t *testing.T) {
	resetBin(t, func(string) (string, error) {
		return "", errors.New("not found")
	})

	if got := Bin(); got != "tmux" {
		t.Fatalf("Bin() = %q, want the bare name so exec.Command reports the lookup error itself", got)
	}
}

func TestCmdBuildsTheSameArgvAsExecCommand(t *testing.T) {
	resetBin(t, func(name string) (string, error) {
		return "/opt/fake/bin/" + name, nil
	})

	got := Cmd("list-windows", "-a", "-F", "#{window_id}")
	want := []string{"/opt/fake/bin/tmux", "list-windows", "-a", "-F", "#{window_id}"}
	if len(got.Args) != len(want) {
		t.Fatalf("Args = %q, want %q", got.Args, want)
	}
	for i := range want {
		if got.Args[i] != want[i] {
			t.Fatalf("Args = %q, want %q", got.Args, want)
		}
	}
	if got.Path != want[0] {
		t.Fatalf("Path = %q, want %q", got.Path, want[0])
	}
}

func TestCmdContextCarriesTheContext(t *testing.T) {
	resetBin(t, func(name string) (string, error) {
		return "/opt/fake/bin/" + name, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := CmdContext(ctx, "display-message", "-p", "x")
	if err := cmd.Run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() on a cancelled context = %v, want context.Canceled", err)
	}
}

// The real binary has to be findable and absolute, or every rewritten call
// site quietly falls back to a PATH search and the change buys nothing.
func TestBinIsAbsoluteForTheRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if got := Bin(); !filepath.IsAbs(got) {
		t.Fatalf("Bin() = %q, want an absolute path", got)
	}
}

// Building the command, not running it. The gap is the PATH search, so it
// widens with the length of the caller's PATH.
func BenchmarkCmdBareName(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}").Path
	}
}

func BenchmarkCmdResolvedOnce(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Cmd("list-windows", "-a", "-F", "#{window_id}").Path
	}
}
