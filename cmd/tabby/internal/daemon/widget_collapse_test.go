package daemon

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestCollapsibleWidget_UnlistedWidgetRendersBare(t *testing.T) {
	c := newRenderCoordinator(t)
	out := c.collapsibleWidget("@1", "clock", 20, func() string { return "BODY\n" })
	assert.Equal(t, "BODY\n", out)
}

// The icon rides on the widget's own first row — collapsing must not cost the
// widget a row while it is open.
func TestCollapsibleWidget_ExpandedOverlaysIconWithoutAddingRow(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Sidebar.CollapsibleWidgets = []string{"clock"}
	body := "\n----\nBODY\n"
	out := c.collapsibleWidget("@1", "clock", 20, func() string { return body })
	assert.Contains(t, out, "⊟")
	assert.Contains(t, out, "----")
	assert.Contains(t, out, "BODY")
	assert.Equal(t, strings.Count(body, "\n"), strings.Count(out, "\n"))
}

// The overlay eats the leading cells of the first line rather than shifting it
// right, so the widget's remaining rows stay in their columns.
func TestCollapsibleWidget_ExpandedOverlayReplacesLeadingCells(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Sidebar.CollapsibleWidgets = []string{"all"}
	out := c.collapsibleWidget("@1", "git", 20, func() string { return "──────\n" })
	first, _, _ := strings.Cut(out, "\n")
	assert.Equal(t, 6, lipgloss.Width(first))
}

// A widget that rendered nothing (the git widget outside a repo) gets no icon:
// there is nothing to collapse.
func TestCollapsibleWidget_EmptyBodyStaysEmpty(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Sidebar.CollapsibleWidgets = []string{"all"}
	assert.Equal(t, "", c.collapsibleWidget("@1", "git", 20, func() string { return "" }))
}

// Collapsing must skip the body entirely: the point is to reclaim the rows and
// to stop paying to render them.
func TestCollapsibleWidget_CollapsedDropsBody(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Sidebar.CollapsibleWidgets = []string{"all"}
	c.collapsedWidgets = map[string]bool{"teamclaude": true}
	rendered := false
	out := c.collapsibleWidget("@1", "teamclaude", 20, func() string {
		rendered = true
		return "BODY\n"
	})
	assert.False(t, rendered)
	assert.NotContains(t, out, "BODY")
	assert.Contains(t, out, "⊞")
	assert.Contains(t, out, "teamclaude")
	assert.Equal(t, 1, strings.Count(out, "\n"))
}

// The disclosure icon is the click target, so its zone id has to be one
// scanZoneRegions knows to look for.
func TestWidgetCollapseZoneIDsRegistered(t *testing.T) {
	for _, id := range widgetCollapseZoneIDs() {
		assert.Contains(t, widgetZoneIDs, id)
	}
	assert.Contains(t, widgetCollapseZoneIDs(), "clock:toggle_widget")
}

func TestRenderClockWidget_SingleLine(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Widgets.Clock.Enabled = true
	c.config.Widgets.Clock.Format = "15:04"
	c.config.Widgets.Clock.ShowDate = true
	c.config.Widgets.Clock.DateFmt = "Mon"

	twoLine := c.renderClockWidget("@1", 30)
	c.config.Widgets.Clock.SingleLine = true
	oneLine := c.renderClockWidget("@1", 30)

	assert.Equal(t, strings.Count(twoLine, "\n")-1, strings.Count(oneLine, "\n"))
}

// A sidebar too narrow for "time  date" drops the date rather than wrapping,
// which would spend the second row the setting exists to save.
func TestRenderClockWidget_SingleLineTooNarrowDropsDate(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Widgets.Clock.Enabled = true
	c.config.Widgets.Clock.Format = "15:04:05"
	c.config.Widgets.Clock.ShowDate = true
	c.config.Widgets.Clock.DateFmt = "Monday January 2"
	c.config.Widgets.Clock.SingleLine = true

	out := c.renderClockWidget("@1", 10)
	assert.Equal(t, 1, strings.Count(out, "\n"))
	assert.NotContains(t, out, "January")
}

func TestGenerateSidebarHeader_PaddingTopShiftsClickRegion(t *testing.T) {
	c := newRenderCoordinator(t)
	c.config.Sidebar.Header.Height = config.Ptr(3)
	c.config.Sidebar.Header.PaddingTop = config.Ptr(2)
	c.config.Sidebar.Header.PaddingBottom = config.Ptr(1)

	content, regions := c.generateSidebarHeader(30, "@1")
	assert.Equal(t, 6, strings.Count(content, "\n"))
	assert.Len(t, regions, 1)
	assert.Equal(t, 2, regions[0].StartLine)
	assert.Equal(t, 4, regions[0].EndLine)
}
