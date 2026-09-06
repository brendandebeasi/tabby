package daemon

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"
	"github.com/rivo/uniseg"
)

// collapsibleWidgetNames lists the sidebar widgets that can carry a disclosure
// icon. Order fixes the option's serialisation so a save doesn't churn.
var collapsibleWidgetNames = []string{"clock", "pet", "git", "session", "claude", "teamclaude", "kimi"}

// widgetCollapseLabel names a widget on the single row it leaves behind once
// collapsed.
var widgetCollapseLabel = map[string]string{
	"clock":      "time",
	"pet":        "pet",
	"git":        "git",
	"session":    "session",
	"claude":     "claude",
	"teamclaude": "teamclaude",
	"kimi":       "kimi",
}

// widgetCollapseZoneIDs returns the BubbleZone ids for every widget disclosure
// icon, in the "target:action" form scanZoneRegions splits on.
func widgetCollapseZoneIDs() []string {
	ids := make([]string, 0, len(collapsibleWidgetNames))
	for _, name := range collapsibleWidgetNames {
		ids = append(ids, name+":toggle_widget")
	}
	return ids
}

// The icons are only clickable if scanZoneRegions is told to look for them, so
// register their ids alongside the hand-written ones.
func init() { widgetZoneIDs = append(widgetZoneIDs, widgetCollapseZoneIDs()...) }

// widgetCollapsible reports whether name is listed in
// sidebar.collapsible_widgets (or covered by "all").
func (c *Coordinator) widgetCollapsible(name string) bool {
	for _, entry := range c.config.Sidebar.CollapsibleWidgets {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "all" || entry == name {
			return true
		}
	}
	return false
}

// widgetDisclosureCell returns the disclosure glyph padded to the shared cell
// width, reusing the window list's icons so a widget toggles with the same
// affordance as a tab group. Both states occupy the same columns, so the hit
// zone doesn't move out from under the pointer between clicks.
func (c *Coordinator) widgetDisclosureCell(collapsed bool) (string, int) {
	expanded := c.config.Sidebar.Colors.DisclosureExpanded
	if expanded == "" {
		expanded = "⊟"
	}
	collapsedIcon := c.config.Sidebar.Colors.DisclosureCollapsed
	if collapsedIcon == "" {
		collapsedIcon = "⊞"
	}
	w := disclosureCellWidth(expanded, collapsedIcon)

	icon := expanded
	if collapsed {
		icon = collapsedIcon
	}
	if pad := w - uniseg.StringWidth(icon); pad > 0 {
		icon += strings.Repeat(" ", pad)
	}
	return icon, w
}

// collapsibleWidget hangs a disclosure icon off the top-left of a widget. An
// expanded widget keeps every row it had — the icon is overlaid on the first
// one (a margin or divider row) rather than costing a row of its own. A
// collapsed widget renders as the icon and its name alone; render is never
// called.
//
// Read lock-free, like collapsedGroups: the render path already holds
// stateMu.RLock and RWMutex is not reentrant.
func (c *Coordinator) collapsibleWidget(clientID, name string, width int, render func() string) string {
	if !c.widgetCollapsible(name) {
		return render()
	}
	if c.collapsedWidgets[name] {
		return c.renderCollapsedWidgetRow(clientID, name, width)
	}
	return c.overlayWidgetDisclosure(name, render())
}

// overlayWidgetDisclosure writes the expanded icon over the first cells of a
// widget's first line.
func (c *Coordinator) overlayWidgetDisclosure(name, body string) string {
	if body == "" {
		return body
	}
	icon, iconW := c.widgetDisclosureCell(false)
	marked := zone.Mark(name+":toggle_widget", paintFg(icon, c.getDisclosureFgWithFallback(c.config.Sidebar.Colors.DisclosureFg)))

	first, rest, multiline := strings.Cut(body, "\n")
	// The first line may already carry colour (a divider) — drop its leading
	// cells with an ANSI-aware cut so the escape sequences survive.
	out := marked + ansi.TruncateLeft(first, iconW, "")
	if multiline {
		out += "\n" + rest
	}
	return out
}

// renderCollapsedWidgetRow draws the one row a collapsed widget leaves behind:
// the disclosure icon plus the widget's name, so it can be found and reopened.
func (c *Coordinator) renderCollapsedWidgetRow(clientID, name string, width int) string {
	if width < 1 {
		width = 1
	}
	icon, _ := c.widgetDisclosureCell(true)

	label := widgetCollapseLabel[name]
	if label == "" {
		label = name
	}

	// Size the row to the sidebar BEFORE colouring it: constrainWidgetWidth's
	// truncation is not ANSI-aware, so an over-wide coloured line renders blank.
	text := icon + " " + label
	if uniseg.StringWidth(text) > width {
		text = icon
	}
	if pad := width - uniseg.StringWidth(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}

	fg := c.getDisclosureFgWithFallback(c.config.Sidebar.Colors.DisclosureFg)
	bg := c.chromeBGForClientLocked(clientID)

	return zone.Mark(name+":toggle_widget", paintOn(text, fg, bg)) + "\n"
}

// loadCollapsedWidgets restores the collapsed set from the tmux option, so the
// choice survives a daemon restart and is shared by every daemon on the server.
func (c *Coordinator) loadCollapsedWidgets() {
	out, err := tmuxOutputCtx("show-options", "-v", "-q", "@tabby_collapsed_widgets")
	if err != nil {
		return
	}
	collapsed := make(map[string]bool)
	for _, name := range strings.Split(strings.TrimSpace(string(out)), ",") {
		if name = strings.TrimSpace(name); name != "" {
			collapsed[name] = true
		}
	}
	c.stateMu.Lock()
	c.collapsedWidgets = collapsed
	c.stateMu.Unlock()
}

// saveCollapsedWidgets writes the collapsed set back to the tmux option.
func (c *Coordinator) saveCollapsedWidgets() {
	c.stateMu.RLock()
	names := make([]string, 0, len(c.collapsedWidgets))
	for name, on := range c.collapsedWidgets {
		if on {
			names = append(names, name)
		}
	}
	c.stateMu.RUnlock()
	sort.Strings(names)

	if len(names) == 0 {
		tmuxRun("set-option", "-gu", "@tabby_collapsed_widgets")
		return
	}
	tmuxRun("set-option", "-gq", "@tabby_collapsed_widgets", strings.Join(names, ","))
}
