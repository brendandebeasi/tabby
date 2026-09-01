package tmux

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDisplayedWindowIDs_OnlyAttachedSessions(t *testing.T) {
	restoreState(t)
	mock := newMock()
	// Two attached terminals sitting on different windows of grouped
	// sessions, plus a detached session nobody is looking at.
	mock.set("list-sessions", fields("1", "@935")+"\n"+
		fields("1", "@840")+"\n"+
		fields("0", "@777")+"\n", nil)
	DefaultRunner = mock

	ids, err := DisplayedWindowIDs()
	assert.NoError(t, err)
	assert.Equal(t, []string{"@935", "@840"}, ids)
}

// Grouped sessions on the SAME window must collapse to one entry, or the
// animation tick would send that sidebar two identical frames per tick.
func TestDisplayedWindowIDs_Deduplicates(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-sessions", fields("1", "@935")+"\n"+
		fields("2", "@935")+"\n"+
		fields("1", "@840")+"\n", nil)
	DefaultRunner = mock

	ids, err := DisplayedWindowIDs()
	assert.NoError(t, err)
	assert.Equal(t, []string{"@935", "@840"}, ids)
}

func TestDisplayedWindowIDs_NoneAttached(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-sessions", fields("0", "@935")+"\n", nil)
	DefaultRunner = mock

	ids, err := DisplayedWindowIDs()
	assert.NoError(t, err)
	assert.Empty(t, ids)
}

func TestDisplayedWindowIDs_SkipsMalformedLines(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-sessions", fields("1", "@935")+"\n"+
		"garbage\n"+
		fields("1", "")+"\n"+
		fields("", "@840")+"\n", nil)
	DefaultRunner = mock

	ids, err := DisplayedWindowIDs()
	assert.NoError(t, err)
	assert.Equal(t, []string{"@935"}, ids)
}

func TestDisplayedWindowIDs_ErrorPropagates(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-sessions", "", errors.New("no server"))
	DefaultRunner = mock

	_, err := DisplayedWindowIDs()
	assert.Error(t, err)
}
