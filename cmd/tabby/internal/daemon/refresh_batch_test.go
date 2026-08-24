package daemon

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// joinBatch renders one batch the way tmux would receive it, for readable
// assertions.
func joinBatch(b []string) string { return strings.Join(b, " ") }

func TestAIToolOptionBatchesEmpty(t *testing.T) {
	assert.Nil(t, aiToolOptionBatches(nil))
	assert.Nil(t, aiToolOptionBatches([]tmuxSetOption{}))
}

// The whole reason for grouping per window rather than emitting one combined
// argv: tmux aborts a ";"-chain at the first failing command, so a window that
// died since the snapshot must not be able to take other windows' indicators
// down with it. runTmuxBatches can only retry at batch granularity.
func TestAIToolOptionBatchesGroupPerWindow(t *testing.T) {
	batches := aiToolOptionBatches([]tmuxSetOption{
		{windowID: "@1", key: "@tabby_busy", value: "1"},
		{windowID: "@2", key: "@tabby_bell", value: "1"},
		{windowID: "@1", key: "@tabby_input", unset: true},
	})

	if !assert.Len(t, batches, 2, "one batch per distinct window") {
		return
	}
	// First-seen order, so writes land in the order they were queued.
	assert.Equal(t,
		"set-option -w -t @1 @tabby_busy 1 ; set-option -w -t @1 -u @tabby_input",
		joinBatch(batches[0]))
	assert.Equal(t,
		"set-option -w -t @2 @tabby_bell 1",
		joinBatch(batches[1]))

	for _, b := range batches {
		targets := map[string]bool{}
		for i, a := range b {
			if a == "-t" && i+1 < len(b) {
				targets[b[i+1]] = true
			}
		}
		assert.Len(t, targets, 1, "a batch must not span windows: %s", joinBatch(b))
	}
}

// An unset op uses -u and carries no value; a set op carries the value and no
// -u. Mixing these up silently clears an indicator instead of raising it.
func TestAIToolOptionBatchesUnsetVsSet(t *testing.T) {
	batches := aiToolOptionBatches([]tmuxSetOption{
		{windowID: "@1", key: "@tabby_bell", value: "1"},
	})
	assert.Equal(t, "set-option -w -t @1 @tabby_bell 1", joinBatch(batches[0]))

	batches = aiToolOptionBatches([]tmuxSetOption{
		{windowID: "@1", key: "@tabby_bell", value: "ignored", unset: true},
	})
	assert.Equal(t, "set-option -w -t @1 -u @tabby_bell", joinBatch(batches[0]))
	assert.NotContains(t, joinBatch(batches[0]), "ignored",
		"unset must drop the value, not pass it through")
}

func TestWindowRenameBatchesEmpty(t *testing.T) {
	assert.Nil(t, windowRenameBatches(nil))
}

// The rename and the @tabby_name_locked reset have to stay together and in this
// order. A rename that landed without its reset would look like an explicit
// user rename and that window would stop tracking its directory for good.
func TestWindowRenameBatchesPairRenameWithUnlock(t *testing.T) {
	batches := windowRenameBatches([]tmuxWindowRename{
		{windowID: "@1", desiredName: "tabby"},
		{windowID: "@2", desiredName: "notes"},
	})

	if !assert.Len(t, batches, 2, "one batch per window keeps failures isolated") {
		return
	}
	assert.Equal(t,
		"rename-window -t @1 tabby ; set-window-option -t @1 @tabby_name_locked 0",
		joinBatch(batches[0]))
	assert.Equal(t,
		"rename-window -t @2 notes ; set-window-option -t @2 @tabby_name_locked 0",
		joinBatch(batches[1]))
}

// A name with spaces has to survive as ONE argv element. Flattening must not
// let it split into extra tmux arguments.
func TestWindowRenameBatchesKeepsSpacedNameIntact(t *testing.T) {
	batches := windowRenameBatches([]tmuxWindowRename{
		{windowID: "@1", desiredName: "tabby | notes"},
	})
	assert.Equal(t, []string{
		"rename-window", "-t", "@1", "tabby | notes",
		";", "set-window-option", "-t", "@1", "@tabby_name_locked", "0",
	}, batches[0])

	flat := flattenTmuxBatches(batches)
	assert.Contains(t, flat, "tabby | notes", "name must stay a single argument")
}

// flattenTmuxBatches is what turns the per-window batches into the single
// happy-path fork, so the separator between batches has to be there.
func TestFlattenSeparatesBatches(t *testing.T) {
	flat := flattenTmuxBatches(aiToolOptionBatches([]tmuxSetOption{
		{windowID: "@1", key: "@tabby_busy", value: "1"},
		{windowID: "@2", key: "@tabby_busy", value: "1"},
	}))
	assert.Equal(t,
		"set-option -w -t @1 @tabby_busy 1 ; set-option -w -t @2 @tabby_busy 1",
		strings.Join(flat, " "))
}
