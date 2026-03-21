package tmux

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// listPanesFields builds a \x1f-delimited line for ListPanesForWindow output.
// Fields: pane_id, pane_index, pane_active, pane_current_command, pane_title,
//
//	pane_pid, pane_last_activity, @tabby_pane_title, pane_top, pane_left,
//	pane_current_path, @tabby_pane_collapsed, @tabby_pane_prev_height,
//	pane_start_command, pane_dead
func listPanesFields(parts ...string) string {
	return fields(parts...)
}

func TestListPanesForWindow_BasicParse(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", listPanesFields(
		"%0", "0", "1", "bash", "My Title",
		"99999", "1700000000", "", "0", "0",
		"/home/user", "", "", "bash", "0",
	)+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Len(t, panes, 1)
	assert.Equal(t, "%0", panes[0].ID)
	assert.Equal(t, 0, panes[0].Index)
	assert.True(t, panes[0].Active)
	assert.Equal(t, "bash", panes[0].Command)
	assert.Equal(t, "My Title", panes[0].Title)
	assert.Equal(t, "/home/user", panes[0].CurrentPath)
}

func TestListPanesForWindow_ErrorPropagated(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", "", fmt.Errorf("tmux not running"))
	DefaultRunner = mock

	_, err := ListPanesForWindow(1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tmux not running")
}

func TestListPanesForWindow_EmptyOutput(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", "", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Empty(t, panes)
}

func TestListPanesForWindow_SkipsSidebarPane(t *testing.T) {
	restoreState(t)
	mock := newMock()
	sidebarLine := listPanesFields(
		"%1", "1", "0", "sidebar-renderer", "Sidebar",
		"99998", "1700000000", "", "0", "40",
		"/tmp", "", "", "sidebar-renderer", "0",
	)
	normalLine := listPanesFields(
		"%0", "0", "1", "bash", "Shell",
		"99997", "1700000000", "", "0", "0",
		"/home/user", "", "", "bash", "0",
	)
	mock.set("list-panes", normalLine+"\n"+sidebarLine+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Len(t, panes, 1, "sidebar-renderer pane should be filtered out")
	assert.Equal(t, "bash", panes[0].Command)
}

func TestListPanesForWindow_SkipsDeadPane(t *testing.T) {
	restoreState(t)
	mock := newMock()
	deadLine := listPanesFields(
		"%2", "2", "0", "bash", "dead",
		"99996", "1700000000", "", "0", "0",
		"/tmp", "", "", "bash", "1",
	)
	aliveLine := listPanesFields(
		"%0", "0", "1", "bash", "alive",
		"99995", "1700000000", "", "0", "0",
		"/home", "", "", "bash", "0",
	)
	mock.set("list-panes", aliveLine+"\n"+deadLine+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Len(t, panes, 1)
	assert.Equal(t, "alive", panes[0].Title)
}

func TestListPanesForWindow_BusyPaneDetected(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", listPanesFields(
		"%0", "0", "1", "make", "Build",
		"99994", "1700000000", "", "0", "0",
		"/src", "", "", "make", "0",
	)+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Len(t, panes, 1)
	assert.True(t, panes[0].Busy)
}

func TestListPanesForWindow_IdlePaneNotBusy(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", listPanesFields(
		"%0", "0", "1", "bash", "Shell",
		"99993", "1700000000", "", "0", "0",
		"/home", "", "", "bash", "0",
	)+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Len(t, panes, 1)
	assert.False(t, panes[0].Busy)
}

func TestListPanesForWindow_SessionTarget(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", listPanesFields(
		"%0", "0", "1", "bash", "Shell",
		"99992", "1700000000", "", "0", "0",
		"/home", "", "", "bash", "0",
	)+"\n", nil)
	DefaultRunner = mock
	sessionTarget = "mysession"

	_, err := ListPanesForWindow(3)
	assert.NoError(t, err)

	found := false
	for _, call := range mock.calls {
		for _, arg := range call {
			if arg == "mysession:3" {
				found = true
			}
		}
	}
	assert.True(t, found, "session target should be included in tmux target")
}

func TestListPanesForWindow_ShortLineTooFewFields(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", "%0\x1f0\x1f1\x1fbash\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Empty(t, panes, "lines with < 5 fields should be skipped")
}

func TestListPanesForWindow_LockedTitle(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", listPanesFields(
		"%0", "0", "1", "vim", "default",
		"99991", "1700000000", "my-locked-title", "0", "0",
		"/home", "", "", "vim", "0",
	)+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Len(t, panes, 1)
	assert.Equal(t, "my-locked-title", panes[0].LockedTitle)
}

func TestListPanesForWindow_NormalPaneNotCollapsed(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-panes", listPanesFields(
		"%0", "0", "1", "bash", "Shell",
		"99990", "1700000000", "", "0", "0",
		"/home", "", "", "bash", "0",
	)+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(1)
	assert.NoError(t, err)
	assert.Len(t, panes, 1)
	assert.False(t, panes[0].Collapsed)
}
