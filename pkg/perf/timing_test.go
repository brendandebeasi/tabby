package perf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStart(t *testing.T) {
	t.Run("returns_non_nil_timer", func(t *testing.T) {
		timer := Start("op")
		assert.NotNil(t, timer)
	})

	t.Run("stores_name", func(t *testing.T) {
		timer := Start("my-operation")
		assert.Equal(t, "my-operation", timer.name)
	})

	t.Run("start_time_is_set", func(t *testing.T) {
		before := time.Now()
		timer := Start("op")
		after := time.Now()
		assert.False(t, timer.start.IsZero())
		assert.True(t, !timer.start.Before(before))
		assert.True(t, !timer.start.After(after))
	})
}

func TestTimerStop(t *testing.T) {
	t.Run("returns_non_negative_duration", func(t *testing.T) {
		timer := Start("op")
		elapsed := timer.Stop()
		assert.GreaterOrEqual(t, elapsed, time.Duration(0))
	})

	t.Run("elapsed_increases_with_sleep", func(t *testing.T) {
		timer := Start("op")
		time.Sleep(2 * time.Millisecond)
		elapsed := timer.Stop()
		assert.GreaterOrEqual(t, elapsed, time.Millisecond)
	})
}

func TestTrack(t *testing.T) {
	t.Run("executes_the_function", func(t *testing.T) {
		called := false
		Track("op", func() { called = true })
		assert.True(t, called)
	})

	t.Run("returns_non_negative_elapsed", func(t *testing.T) {
		elapsed := Track("op", func() {})
		assert.GreaterOrEqual(t, elapsed, time.Duration(0))
	})

	t.Run("elapsed_reflects_function_duration", func(t *testing.T) {
		elapsed := Track("op", func() {
			time.Sleep(2 * time.Millisecond)
		})
		assert.GreaterOrEqual(t, elapsed, time.Millisecond)
	})
}

func TestIsEnabled(t *testing.T) {
	assert.Equal(t, enabled, IsEnabled())
}

func TestLogNoop(t *testing.T) {
	assert.NotPanics(t, func() {
		Log("test message %s %d", "hello", 42)
	})
}
