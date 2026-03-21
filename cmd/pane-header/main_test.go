package main

import (
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	tea "github.com/charmbracelet/bubbletea"
)

func TestMain(m *testing.M) {
	debugLog = log.New(io.Discard, "", 0)
	os.Exit(m.Run())
}

// TestStripAnsi verifies ANSI escape code stripping
func TestStripAnsi(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no ansi",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "simple color",
			input:    "\x1b[31mred\x1b[0m",
			expected: "red",
		},
		{
			name:     "multiple colors",
			input:    "\x1b[31mred\x1b[32mgreen\x1b[0m",
			expected: "redgreen",
		},
		{
			name:     "256 color",
			input:    "\x1b[38;5;196mtext\x1b[0m",
			expected: "text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripAnsi(tt.input)
			if result != tt.expected {
				t.Errorf("stripAnsi(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestAbsInt verifies absolute value function
func TestAbsInt(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 0},
		{5, 5},
		{-5, 5},
		{-1, 1},
		{100, 100},
		{-100, 100},
	}

	for _, tt := range tests {
		result := absInt(tt.input)
		if result != tt.expected {
			t.Errorf("absInt(%d) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

// TestClickableRegionBounds verifies region coordinate handling
func TestClickableRegionBounds(t *testing.T) {
	region := daemon.ClickableRegion{
		StartLine: 0,
		EndLine:   0,
		StartCol:  5,
		EndCol:    15,
		Action:    "test_action",
		Target:    "test_target",
	}

	// Test point inside region
	x, y := 10, 0
	if y < region.StartLine || y > region.EndLine {
		t.Errorf("Point (%d,%d) Y should be within lines %d-%d", x, y, region.StartLine, region.EndLine)
	}
	if x < region.StartCol || x >= region.EndCol {
		t.Errorf("Point (%d,%d) X should be within cols %d-%d", x, y, region.StartCol, region.EndCol)
	}

	// Test point outside region (left)
	x = 2
	if x >= region.StartCol && x < region.EndCol {
		t.Errorf("Point (%d,%d) should be outside region cols %d-%d", x, y, region.StartCol, region.EndCol)
	}

	// Test point outside region (right)
	x = 20
	if x >= region.StartCol && x < region.EndCol {
		t.Errorf("Point (%d,%d) should be outside region cols %d-%d", x, y, region.StartCol, region.EndCol)
	}
}

// TestRendererModelDefaults verifies default model values
func TestRendererModelDefaults(t *testing.T) {
	model := rendererModel{
		width:  80,
		height: 1,
	}

	if model.width != 80 {
		t.Errorf("Expected default width 80, got %d", model.width)
	}
	if model.height != 1 {
		t.Errorf("Expected default height 1 for header, got %d", model.height)
	}
	if model.connected {
		t.Error("Expected connected to be false by default")
	}
}

// TestSpinnerFrames verifies spinner animation frames exist
func TestSpinnerFrames(t *testing.T) {
	if len(spinnerFrames) == 0 {
		t.Error("spinnerFrames should not be empty")
	}
	if len(spinnerFrames) < 2 {
		t.Error("spinnerFrames should have multiple frames for animation")
	}
}

func TestView_Disconnected(t *testing.T) {
	m := rendererModel{connected: false, content: "Hello", width: 10}
	got := m.View()
	if got != "" {
		t.Fatalf("View() disconnected should return empty, got %q", got)
	}
}

func TestView_EmptyContent(t *testing.T) {
	m := rendererModel{connected: true, content: "", width: 10}
	got := m.View()
	if got != "" {
		t.Fatalf("View() with empty content should return empty, got %q", got)
	}
}

func TestView_PadsToWidth(t *testing.T) {
	m := rendererModel{connected: true, content: "Hi", width: 10}
	got := m.View()
	if len(got) < 10 {
		t.Fatalf("View() should pad to width=10, got len=%d: %q", len(got), got)
	}
	if !strings.HasPrefix(got, "Hi") {
		t.Fatalf("View() should start with content, got %q", got)
	}
}

func TestView_TakesFirstLineOnly(t *testing.T) {
	m := rendererModel{connected: true, content: "Line1\nLine2\nLine3", width: 10}
	got := m.View()
	if strings.Contains(got, "Line2") || strings.Contains(got, "Line3") {
		t.Fatalf("View() should only show first line, got %q", got)
	}
	if !strings.HasPrefix(got, "Line1") {
		t.Fatalf("View() should start with first line, got %q", got)
	}
}

func TestView_NoTruncation(t *testing.T) {
	m := rendererModel{connected: true, content: "VeryLongContent", width: 5}
	got := m.View()
	if !strings.Contains(got, "VeryLongContent") {
		t.Fatalf("View() should not truncate content, got %q", got)
	}
}

func TestProcessMouseClick_NoRegions(t *testing.T) {
	m := rendererModel{width: 80, regions: nil}
	result, cmd := m.processMouseClick(5, 0, tea.MouseButtonLeft, false)
	if result == nil {
		t.Fatal("processMouseClick should return non-nil model")
	}
	_ = cmd
}

func TestProcessMouseClick_HitRegion(t *testing.T) {
	m := rendererModel{
		width: 80,
		regions: []daemon.ClickableRegion{
			{StartLine: 0, EndLine: 0, StartCol: 0, EndCol: 20, Action: "select_window", Target: "1"},
		},
	}
	result, _ := m.processMouseClick(10, 0, tea.MouseButtonLeft, false)
	if result == nil {
		t.Fatal("processMouseClick with matching region should return non-nil")
	}
}

func TestProcessMouseClick_MissRegion(t *testing.T) {
	m := rendererModel{
		width: 80,
		regions: []daemon.ClickableRegion{
			{StartLine: 0, EndLine: 0, StartCol: 0, EndCol: 20, Action: "select_window", Target: "1"},
		},
	}
	result, _ := m.processMouseClick(50, 0, tea.MouseButtonLeft, false)
	if result == nil {
		t.Fatal("processMouseClick with miss should return non-nil")
	}
}

func TestProcessMouseClick_RightButton(t *testing.T) {
	m := rendererModel{width: 80}
	result, _ := m.processMouseClick(5, 0, tea.MouseButtonRight, false)
	if result == nil {
		t.Fatal("right-click should return non-nil")
	}
}

func TestProcessMouseClick_SimulatedRightClick(t *testing.T) {
	m := rendererModel{width: 80}
	result, _ := m.processMouseClick(5, 0, tea.MouseButtonLeft, true)
	if result == nil {
		t.Fatal("simulated right-click should return non-nil")
	}
}

func TestProcessMouseClick_FullWidthRegion(t *testing.T) {
	m := rendererModel{
		width: 80,
		regions: []daemon.ClickableRegion{
			{StartLine: 0, EndLine: 0, StartCol: 0, EndCol: 0, Action: "select_window", Target: "2"},
		},
	}
	result, _ := m.processMouseClick(40, 0, tea.MouseButtonLeft, false)
	if result == nil {
		t.Fatal("full-width region click should return non-nil")
	}
}

func TestProcessMouseClick_MultipleRegions(t *testing.T) {
	m := rendererModel{
		width: 80,
		regions: []daemon.ClickableRegion{
			{StartLine: 0, EndLine: 0, StartCol: 0, EndCol: 30, Action: "select_window", Target: "1"},
			{StartLine: 0, EndLine: 0, StartCol: 30, EndCol: 60, Action: "select_pane", Target: "%5"},
			{StartLine: 0, EndLine: 0, StartCol: 60, EndCol: 80, Action: "button", Target: "close"},
		},
	}
	for _, x := range []int{10, 40, 70} {
		result, _ := m.processMouseClick(x, 0, tea.MouseButtonLeft, false)
		if result == nil {
			t.Fatalf("click at x=%d should return non-nil", x)
		}
	}
}

func TestUpdate_DisconnectedMsg(t *testing.T) {
	m := rendererModel{connected: true}
	result, cmd := m.Update(disconnectedMsg{})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	if cmd == nil {
		t.Fatal("Update with disconnectedMsg should return a reconnect cmd")
	}
	resultModel := result.(rendererModel)
	if resultModel.connected {
		t.Error("Model should be disconnected after disconnectedMsg")
	}
}

func TestUpdate_RenderMsg(t *testing.T) {
	m := rendererModel{}
	payload := &daemon.RenderPayload{
		Content:     "test content",
		SequenceNum: 42,
	}
	result, _ := m.Update(renderMsg{payload: payload})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	resultModel := result.(rendererModel)
	if resultModel.content != "test content" {
		t.Errorf("Expected content 'test content', got %q", resultModel.content)
	}
	if resultModel.sequenceNum != 42 {
		t.Errorf("Expected sequenceNum 42, got %d", resultModel.sequenceNum)
	}
}

func TestUpdate_RenderMsg_SetsRegionsAndFlags(t *testing.T) {
	m := rendererModel{}
	payload := &daemon.RenderPayload{
		Content:     "header",
		SidebarBg:   "#1e1e1e",
		TerminalBg:  "#000000",
		IsTouchMode: true,
		Regions: []daemon.ClickableRegion{
			{StartLine: 0, EndLine: 0, StartCol: 0, EndCol: 40, Action: "select_window", Target: "1"},
		},
	}
	result, _ := m.Update(renderMsg{payload: payload})
	rm := result.(rendererModel)
	if len(rm.regions) != 1 {
		t.Errorf("Expected 1 region, got %d", len(rm.regions))
	}
	if !rm.isTouchMode {
		t.Error("Expected isTouchMode=true")
	}
	if rm.sidebarBg != "#1e1e1e" {
		t.Errorf("Expected sidebarBg '#1e1e1e', got %q", rm.sidebarBg)
	}
}

func TestUpdate_KeyMsgQ(t *testing.T) {
	m := rendererModel{}
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	result, cmd := m.Update(msg)
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	if cmd == nil {
		t.Fatal("Update with 'q' should return a Quit cmd")
	}
}

func TestUpdate_KeyMsgCtrlC(t *testing.T) {
	m := rendererModel{}
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	result, cmd := m.Update(msg)
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	if cmd == nil {
		t.Fatal("Update with ctrl+c should return a Quit cmd")
	}
}

func TestUpdate_KeyMsgOther(t *testing.T) {
	m := rendererModel{}
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	result, cmd := m.Update(msg)
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	_ = cmd
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := rendererModel{connected: false}
	msg := tea.WindowSizeMsg{Width: 100, Height: 2}
	result, _ := m.Update(msg)
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	rm := result.(rendererModel)
	if rm.width != 100 {
		t.Errorf("Expected width 100, got %d", rm.width)
	}
	if rm.height != 2 {
		t.Errorf("Expected height 2, got %d", rm.height)
	}
}

func TestUpdate_FocusMsg(t *testing.T) {
	m := rendererModel{}
	result, cmd := m.Update(tea.FocusMsg{})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	_ = cmd
}

func TestUpdate_BlurMsg(t *testing.T) {
	m := rendererModel{}
	result, cmd := m.Update(tea.BlurMsg{})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	_ = cmd
}

func TestUpdate_LongPressMsg_NotActive(t *testing.T) {
	m := rendererModel{longPressActive: false, width: 80}
	result, cmd := m.Update(longPressMsg{X: 5, Y: 0})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	_ = cmd
}

func TestUpdate_LongPressMsg_ActiveNoMovement(t *testing.T) {
	m := rendererModel{
		longPressActive: true,
		mouseDownPos:    struct{ X, Y int }{5, 0},
		width:           80,
	}
	result, _ := m.Update(longPressMsg{X: 5, Y: 0})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
}

func TestUpdate_LongPressMsg_ActiveLargeMovement(t *testing.T) {
	m := rendererModel{
		longPressActive: true,
		mouseDownPos:    struct{ X, Y int }{5, 0},
		width:           80,
	}
	result, cmd := m.Update(longPressMsg{X: 60, Y: 0})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	_ = cmd
}

func TestUpdate_UnknownMsg(t *testing.T) {
	type unknownMsg struct{}
	m := rendererModel{}
	result, cmd := m.Update(unknownMsg{})
	if result == nil {
		t.Fatal("Update should return non-nil model")
	}
	_ = cmd
}

func TestHandleMouse_NotConnected(t *testing.T) {
	m := rendererModel{connected: false, width: 80}
	msg := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 5, Y: 0}
	result, cmd := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse should return non-nil even when disconnected")
	}
	_ = cmd
}

func TestHandleMouse_PressLeftNonTouch(t *testing.T) {
	m := rendererModel{connected: true, width: 80, isTouchMode: false}
	msg := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 5, Y: 0}
	result, cmd := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse should return non-nil model")
	}
	_ = cmd
}

func TestHandleMouse_PressRight(t *testing.T) {
	m := rendererModel{connected: true, width: 80}
	msg := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: 5, Y: 0}
	result, _ := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse right press should return non-nil")
	}
}

func TestHandleMouse_PressShiftLeft(t *testing.T) {
	m := rendererModel{connected: true, width: 80, isTouchMode: false}
	msg := tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		Shift:  true,
		X:      5,
		Y:      0,
	}
	result, _ := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse shift+left should return non-nil")
	}
}

func TestHandleMouse_PressCtrlLeft(t *testing.T) {
	m := rendererModel{connected: true, width: 80, isTouchMode: false}
	msg := tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		Ctrl:   true,
		X:      5,
		Y:      0,
	}
	result, _ := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse ctrl+left should return non-nil")
	}
}

func TestHandleMouse_ReleaseSkipNextRelease(t *testing.T) {
	m := rendererModel{connected: true, skipNextRelease: true}
	msg := tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 5, Y: 0}
	result, _ := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse release should return non-nil")
	}
	rm := result.(rendererModel)
	if rm.skipNextRelease {
		t.Error("skipNextRelease should be cleared after release")
	}
}

func TestHandleMouse_ReleaseNonTouchNoDownTime(t *testing.T) {
	m := rendererModel{connected: true, isTouchMode: false}
	msg := tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 5, Y: 0}
	result, cmd := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse should return non-nil")
	}
	_ = cmd
}

func TestHandleMouse_MotionNoLongPress(t *testing.T) {
	m := rendererModel{connected: true, longPressActive: false}
	msg := tea.MouseMsg{Action: tea.MouseActionMotion, X: 5, Y: 0}
	result, _ := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse motion should return non-nil")
	}
}

func TestHandleMouse_MotionCancelsLongPress(t *testing.T) {
	m := rendererModel{
		connected:       true,
		longPressActive: true,
		mouseDownPos:    struct{ X, Y int }{0, 0},
	}
	msg := tea.MouseMsg{Action: tea.MouseActionMotion, X: 50, Y: 0}
	result, _ := m.handleMouse(msg)
	if result == nil {
		t.Fatal("handleMouse motion should return non-nil")
	}
	rm := result.(rendererModel)
	if rm.longPressActive {
		t.Error("longPressActive should be cleared after large movement")
	}
}

func TestSendFunctions_NilConn(t *testing.T) {
	m := rendererModel{}
	m.sendMessage(daemon.Message{})
	m.sendSubscribe()
	m.sendUnsubscribe()
	m.sendResize()
	m.sendInput(&daemon.InputPayload{})
	m.sendPing()
}

func TestInit_ReturnsCmd(t *testing.T) {
	m := rendererModel{}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a non-nil Cmd")
	}
}
