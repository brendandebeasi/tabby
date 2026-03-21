package perf

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func withEnabled(t *testing.T, fn func()) {
	t.Helper()
	origEnabled := enabled
	origLogFile := logFile

	dir := t.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "perf.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("could not create temp log file: %v", err)
	}

	enabled = true
	logFile = f

	t.Cleanup(func() {
		f.Close()
		enabled = origEnabled
		logFile = origLogFile
	})

	fn()
}

func TestTimerStop_EnabledWritesToLog(t *testing.T) {
	withEnabled(t, func() {
		timer := Start("test-op")
		time.Sleep(time.Millisecond)
		elapsed := timer.Stop()
		assert.GreaterOrEqual(t, elapsed, time.Duration(0))

		_ = logFile.Sync()
		info, err := logFile.Stat()
		assert.NoError(t, err)
		assert.Greater(t, info.Size(), int64(0), "log file should have content after Stop()")
	})
}

func TestLog_EnabledWritesToLog(t *testing.T) {
	withEnabled(t, func() {
		Log("test message %s %d", "hello", 42)

		_ = logFile.Sync()
		info, err := logFile.Stat()
		assert.NoError(t, err)
		assert.Greater(t, info.Size(), int64(0), "log file should have content after Log()")
	})
}

func TestLog_EnabledMultipleWrites(t *testing.T) {
	withEnabled(t, func() {
		Log("first")
		Log("second")
		Log("third")

		_ = logFile.Sync()
		info, err := logFile.Stat()
		assert.NoError(t, err)
		assert.Greater(t, info.Size(), int64(0))
	})
}

func TestTrack_EnabledLogsElapsed(t *testing.T) {
	withEnabled(t, func() {
		elapsed := Track("enabled-op", func() {
			time.Sleep(time.Millisecond)
		})
		assert.GreaterOrEqual(t, elapsed, time.Millisecond)

		_ = logFile.Sync()
		info, err := logFile.Stat()
		assert.NoError(t, err)
		assert.Greater(t, info.Size(), int64(0))
	})
}

func TestIsEnabled_TrueWhenEnabled(t *testing.T) {
	withEnabled(t, func() {
		assert.True(t, IsEnabled())
	})
}
