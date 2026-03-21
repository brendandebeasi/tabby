package main

import (
	"strings"
	"sync"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	zone "github.com/lrstanley/bubblezone"
	"github.com/stretchr/testify/assert"
)

var renderTestOnce sync.Once

func newRenderCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	renderTestOnce.Do(func() { zone.NewGlobal() })
	c := newTestCoordinator(t)
	c.bgDetector = colors.NewBackgroundDetector(colors.ThemeModeAuto)
	return c
}

func TestRenderForClient_CollapsedSidebar(t *testing.T) {
	c := newRenderCoordinator(t)
	c.sidebarCollapsed = true
	payload := c.RenderForClient("test-client", 1, 24)
	assert.NotNil(t, payload)
	assert.Equal(t, 1, payload.Width)
	assert.Equal(t, 24, payload.Height)
	assert.NotEmpty(t, payload.Regions)
}

func TestRenderForClient_CollapsedSidebarZeroWidth(t *testing.T) {
	c := newRenderCoordinator(t)
	c.sidebarCollapsed = true
	payload := c.RenderForClient("test-client", 0, 10)
	assert.NotNil(t, payload)
	assert.GreaterOrEqual(t, payload.Width, 1)
}

func TestRenderForClient_EmptyWindowsAndGroups(t *testing.T) {
	c := newRenderCoordinator(t)
	payload := c.RenderForClient("test-client", 30, 24)
	assert.NotNil(t, payload)
	assert.Equal(t, 30, payload.Width)
}

func TestRenderForClient_SmallHeight(t *testing.T) {
	c := newRenderCoordinator(t)
	payload := c.RenderForClient("test-client", 30, 3)
	assert.NotNil(t, payload)
}

func TestRenderForClient_SmallWidth(t *testing.T) {
	c := newRenderCoordinator(t)
	payload := c.RenderForClient("test-client", 2, 24)
	assert.NotNil(t, payload)
	assert.GreaterOrEqual(t, payload.Width, 10)
}

func TestRenderForClient_WithWindows(t *testing.T) {
	c := newRenderCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{
		testWindow("bash", true, "bash"),
		testWindow("vim", false, "vim"),
	}
	c.grouped = []grouping.GroupedWindows{{
		Name:    "Default",
		Theme:   config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1", ActiveBg: "#3498db", ActiveFg: "#ffffff"},
		Windows: c.windows,
	}}
	c.stateMu.Unlock()
	payload := c.RenderForClient("test-client", 30, 24)
	assert.NotNil(t, payload)
	assert.NotEmpty(t, payload.Content)
}

func TestRenderForClient_ContentContainsWindowNames(t *testing.T) {
	c := newRenderCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("mywindow", true, "bash")}
	c.grouped = []grouping.GroupedWindows{{
		Name:    "Default",
		Theme:   config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1"},
		Windows: c.windows,
	}}
	c.stateMu.Unlock()
	payload := c.RenderForClient("test-client", 30, 24)
	assert.NotNil(t, payload)
	assert.Contains(t, payload.Content, "mywindow")
}

func TestRenderHeaderForClient_EmptyClientID(t *testing.T) {
	c := newRenderCoordinator(t)
	payload := c.RenderHeaderForClient("", 80, 1)
	assert.Nil(t, payload)
}

func TestRenderHeaderForClient_NonHeaderClientID(t *testing.T) {
	c := newRenderCoordinator(t)
	payload := c.RenderHeaderForClient("renderer:@1", 80, 1)
	assert.NotNil(t, payload)
}

func TestRenderHeaderForClient_PaneNotFound(t *testing.T) {
	c := newRenderCoordinator(t)
	payload := c.RenderHeaderForClient("header:%99", 80, 1)
	assert.NotNil(t, payload)
	assert.Equal(t, strings.Repeat(" ", 80), payload.Content)
}

func TestRenderHeaderForClient_PaneFound(t *testing.T) {
	c := newRenderCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{{
		ID:    "@1",
		Index: 1,
		Name:  "testwin",
		Panes: []tmux.Pane{
			{ID: "%1", Command: "bash", Active: true},
		},
	}}
	c.grouped = []grouping.GroupedWindows{{
		Name:    "Default",
		Theme:   config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1", ActiveBg: "#3498db"},
		Windows: c.windows,
	}}
	c.stateMu.Unlock()
	payload := c.RenderHeaderForClient("header:%1", 80, 1)
	assert.NotNil(t, payload)
	assert.Equal(t, 80, payload.Width)
}

func TestRenderHeaderForClient_SmallWidth(t *testing.T) {
	c := newRenderCoordinator(t)
	payload := c.RenderHeaderForClient("header:%1", 2, 1)
	assert.NotNil(t, payload)
	assert.GreaterOrEqual(t, payload.Width, 5)
}

func TestGenerateSidebarHeader_EmptyConfig(t *testing.T) {
	c := newRenderCoordinator(t)
	content, regions := c.generateSidebarHeader(30, "test-client")
	assert.NotEmpty(t, content)
	_ = regions
}

func TestGenerateSidebarHeader_WithTitle(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Sidebar.Header.Text = "MY SIDEBAR"
	content, _ := c.generateSidebarHeader(30, "test-client")
	assert.Contains(t, content, "MY SIDEBAR")
}

func TestGenerateSidebarHeader_WithActiveWindow(t *testing.T) {
	c := newRenderCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{{
		ID:     "@1",
		Active: true,
		Group:  "Default",
	}}
	c.stateMu.Unlock()
	content, _ := c.generateSidebarHeader(30, "test-client")
	assert.NotEmpty(t, content)
}

func TestGenerateMainContent_EmptyGrouped(t *testing.T) {
	c := newRenderCoordinator(t)
	content, regions := c.generateMainContent("test-client", 30, 24)
	_ = content
	_ = regions
}

func TestGenerateMainContent_WithWindows(t *testing.T) {
	c := newRenderCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{
		testWindow("win1", true, "bash"),
		testWindow("win2", false, "vim"),
	}
	c.grouped = []grouping.GroupedWindows{{
		Name:    "Default",
		Theme:   config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1", ActiveBg: "#3498db", ActiveFg: "#ffffff"},
		Windows: c.windows,
	}}
	c.stateMu.Unlock()
	content, regions := c.generateMainContent("test-client", 30, 24)
	assert.NotEmpty(t, content)
	assert.NotEmpty(t, regions)
}

func TestGenerateMainContent_ActiveWindowMatchesClient(t *testing.T) {
	c := newRenderCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("active-win", true, "bash")}
	c.grouped = []grouping.GroupedWindows{{
		Name:    "Default",
		Theme:   config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1"},
		Windows: c.windows,
	}}
	c.stateMu.Unlock()
	content, _ := c.generateMainContent("@active-win", 30, 24)
	assert.Contains(t, content, "active-win")
}

func TestGenerateMainContent_WithPanes(t *testing.T) {
	c := newRenderCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{{
		ID:     "@1",
		Name:   "multiPane",
		Active: true,
		Panes: []tmux.Pane{
			{ID: "%1", Command: "bash", Active: true, Width: 80, Height: 12, Top: 0},
			{ID: "%2", Command: "vim", Active: false, Width: 80, Height: 12, Top: 12},
		},
	}}
	c.grouped = []grouping.GroupedWindows{{
		Name:    "Default",
		Theme:   config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1"},
		Windows: c.windows,
	}}
	c.stateMu.Unlock()
	content, _ := c.generateMainContent("test-client", 30, 24)
	assert.NotEmpty(t, content)
}

func TestRenderClockWidget(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Widgets.Clock.Enabled = true
	result := c.renderClockWidget(30)
	assert.NotEmpty(t, result)
}

func TestRenderClockWidget_CustomFormat(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Widgets.Clock.Enabled = true
	c.config.Widgets.Clock.Format = "15:04"
	result := c.renderClockWidget(30)
	assert.NotEmpty(t, result)
}

func TestRenderGitWidget_NotARepo(t *testing.T) {
	c := newRenderCoordinator(t)
	c.isGitRepo = false
	result := c.renderGitWidget(30)
	_ = result
}

func TestRenderGitWidget_IsRepo(t *testing.T) {
	c := newRenderCoordinator(t)
	c.isGitRepo = true
	c.gitBranch = "main"
	c.gitDirty = 3
	result := c.renderGitWidget(30)
	assert.NotEmpty(t, result)
}

func TestRenderSessionWidget(t *testing.T) {
	c := newRenderCoordinator(t)
	c.sessionName = "my-session"
	c.sessionClients = 2
	c.windowCount = 5
	result := c.renderSessionWidget(30)
	_ = result
}

func TestRenderPetWidget_Disabled(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Widgets.Pet.Enabled = false
	result := c.renderPetWidget(30)
	assert.Empty(t, result)
}

func TestRenderPetWidget_Enabled(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Widgets.Pet.Enabled = true
	c.lastWidth = 30
	result := c.renderPetWidget(30)
	_ = result
}

func TestRenderWidgetZone_EmptyEntries(t *testing.T) {
	c := newRenderCoordinator(t)
	content, regions := c.renderWidgetZone(nil, 30)
	assert.Empty(t, content)
	assert.Empty(t, regions)
}

func TestRenderWidgetZone_WithEntry(t *testing.T) {
	c := newRenderCoordinator(t)
	entries := []widgetEntry{{
		name:    "clock",
		zone:    "bottom",
		content: "12:00:00",
	}}
	content, _ := c.renderWidgetZone(entries, 30)
	assert.Contains(t, content, "12:00:00")
}

func TestGenerateWidgetZones_NoWidgets(t *testing.T) {
	c := newRenderCoordinator(t)
	top, topR, bottom, bottomR := c.generateWidgetZones(30)
	assert.Empty(t, top)
	assert.Empty(t, topR)
	assert.NotEmpty(t, bottom)
	_ = bottomR
}

func TestRenderSidebarResizeButtons(t *testing.T) {
	c := newRenderCoordinator(t)
	result := c.renderSidebarResizeButtons(30)
	assert.NotEmpty(t, result)
}

func TestRenderPinnedActionButtons_NarrowWidth(t *testing.T) {
	c := newRenderCoordinator(t)
	result := c.renderPinnedActionButtons(5)
	_ = result
}

func TestRenderPinnedActionButtons_WideWidth(t *testing.T) {
	c := newRenderCoordinator(t)
	result := c.renderPinnedActionButtons(40)
	_ = result
}

func TestRenderTouchButton(t *testing.T) {
	c := newRenderCoordinator(t)
	result := c.renderTouchButton(30, "Test", "#3498db")
	assert.NotEmpty(t, result)
}
