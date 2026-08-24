package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeGlob(t *testing.T) {
	t.Run("no prefix", func(t *testing.T) {
		os.Unsetenv("TABBY_RUNTIME_PREFIX")
		assert.Equal(t, "/tmp/tabby-daemon-*.sock", RuntimeGlob(".sock"))
	})
	t.Run("with prefix", func(t *testing.T) {
		t.Setenv("TABBY_RUNTIME_PREFIX", "demo-")
		assert.Equal(t, "/tmp/demo-tabby-daemon-*-events.log", RuntimeGlob("-events.log"))
	})
}

// TABBY_RUNTIME_PREFIX only isolates an instance if EVERY participant derives
// its runtime paths the same way. The daemon builds its socket and pidfile from
// RuntimePath, so a client that formats "/tmp/tabby-daemon-%s.sock" by hand
// looks for a prefixed instance's daemon under the unprefixed name -- and
// finding nothing there, spawns a second daemon or kills the wrong one. Session
// ids are per-server ($0, $1, ...), so two tmux servers collide on them
// routinely; the prefix is the only thing keeping their runtime files apart.
//
// String literals in comments and in this package's own path builders are fine;
// anything else that names the runtime directory must go through the helpers.
func TestNoHardcodedRuntimePaths(t *testing.T) {
	allowed := map[string]bool{
		filepath.Join("pkg", "daemon", "protocol.go"): true,
	}
	var offenders []string
	for _, root := range []string{"cmd", "pkg"} {
		err := filepath.Walk(filepath.Join("..", "..", root), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")
			if allowed[filepath.FromSlash(rel)] {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for i, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if strings.Contains(line, `"/tmp/tabby-daemon-`) {
					offenders = append(offenders, rel+":"+strconv.Itoa(i+1))
				}
			}
			return nil
		})
		assert.NoError(t, err)
	}
	assert.Empty(t, offenders,
		"use daemon.RuntimePath/SocketPath/PidPath/RuntimeGlob so TABBY_RUNTIME_PREFIX actually isolates the instance")
}
