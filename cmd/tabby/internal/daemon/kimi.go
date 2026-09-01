package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/kimicode"
	"github.com/brendandebeasi/tabby/pkg/textwidth"
	"github.com/charmbracelet/lipgloss"
)

// kimiURL resolves the coding API base URL from config, falling back to an
// environment variable so it can be injected from a secret manager.
func (c *Coordinator) kimiURL() string {
	if u := c.config.Widgets.Kimi.URL; u != "" {
		return u
	}
	return os.Getenv("TABBY_KIMI_URL")
}

// kimiAPIKey resolves the API key from config, falling back to an environment
// variable so the secret can stay out of config.yaml.
func (c *Coordinator) kimiAPIKey() string {
	if k := c.config.Widgets.Kimi.APIKey; k != "" {
		return k
	}
	return os.Getenv("TABBY_KIMI_API_KEY")
}

// RefreshKimi refreshes the cached Kimi for Coding quota. Mirrors
// RefreshTeamClaude: returns immediately, HTTP runs in a detached goroutine,
// throttled to UpdateInterval, coalesced to one in-flight request, and a
// render is triggered only when the displayed state actually changes.
func (c *Coordinator) RefreshKimi() {
	cfg := c.config.Widgets.Kimi
	url := c.kimiURL()
	if !cfg.Enabled || url == "" {
		return
	}

	interval := time.Duration(cfg.UpdateInterval) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	c.stateMu.RLock()
	fetchedAt := c.kimiFetchedAt
	c.stateMu.RUnlock()
	if !fetchedAt.IsZero() && time.Since(fetchedAt) < interval {
		return
	}

	if !c.kimiFetching.CompareAndSwap(false, true) {
		return
	}

	apiKey := c.kimiAPIKey()
	prevHash := c.GetKimiStateHash()
	go func() {
		defer c.kimiFetching.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		usages, err := kimicode.Fetch(ctx, url, apiKey)

		c.stateMu.Lock()
		c.kimiFetchedAt = time.Now()
		c.kimiErr = err
		if err == nil {
			c.kimiUsages = usages
		}
		c.stateMu.Unlock()

		if c.GetKimiStateHash() != prevHash && c.OnRefreshLayout != nil {
			c.OnRefreshLayout()
		}
	}()
}

// GetKimiStateHash returns a cheap fingerprint of the cached quota state so
// the loop can skip re-rendering when nothing changed.
func (c *Coordinator) GetKimiStateHash() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.kimiUsages == nil {
		if c.kimiErr != nil {
			return "err:" + c.kimiErr.Error()
		}
		return "nil"
	}
	fracStr := func(q *kimicode.Quota) string {
		f, ok := q.RemainingFrac()
		if !ok {
			return "-1"
		}
		return fmt.Sprintf("%.2f", f)
	}
	return fmt.Sprintf("s=%s;w=%s", fracStr(c.kimiUsages.Session()), fracStr(c.kimiUsages.Weekly()))
}

// renderKimiWidget renders Kimi for Coding session (5h) and weekly (7d) quota
// left, from the cached usages. Data is fetched off the render path by
// RefreshKimi; here we only read the cache (without taking stateMu — the
// caller already holds RLock; see renderTeamClaudeWidget for the rationale).
func (c *Coordinator) renderKimiWidget(clientID string, width int) string {
	kCfg := c.config.Widgets.Kimi
	if !kCfg.Enabled {
		return ""
	}

	usages := c.kimiUsages
	fetchErr := c.kimiErr

	var result strings.Builder

	for i := 0; i < kCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	divider := kCfg.Divider
	if divider == "" {
		divider = "-"
	}
	dividerFg := c.getInactiveTextColorWithFallback(kCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}
	if dw := lipgloss.Width(divider); dw > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dw)) + "\n")
	}

	for i := 0; i < kCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	labelFg := c.getInactiveTextColorWithFallback(kCfg.Fg)

	style := kCfg.Style
	if style == "" {
		style = "nerd"
	}
	icon := ""
	switch style {
	case "nerd":
		icon = " " // nf-fa-moon_o
	case "emoji":
		icon = "🌙 "
	case "ascii":
		icon = "[K] "
	}

	showSession := kCfg.ShowSession
	showWeekly := kCfg.ShowWeekly
	if !showSession && !showWeekly {
		showSession, showWeekly = true, true
	}
	var legendParts []string
	if showSession {
		legendParts = append(legendParts, "S")
	}
	if showWeekly {
		legendParts = append(legendParts, "W")
	}
	header := icon + "Kimi [" + strings.Join(legendParts, "/") + "]"
	headerText := textwidth.Truncate(header, width, "")
	result.WriteString(paintFg(headerText, labelFg) + "\n")

	switch {
	case usages == nil && fetchErr != nil:
		result.WriteString(paintFg("  unreachable", labelFg) + "\n")
	case usages == nil:
		result.WriteString(paintFg("  …", labelFg) + "\n")
	default:
		termBg := c.chromeBGForClientLocked(clientID)
		inBar := func(f *float64, resetMs int64, bw int) string {
			return renderQuotaBar(f, resetMs, bw, kCfg.BarFg, labelFg, termBg)
		}
		var cells []quotaCell
		if showSession {
			cells = append(cells, kimiQuotaCell(usages.Session()))
		}
		if showWeekly {
			cells = append(cells, kimiQuotaCell(usages.Weekly()))
		}
		result.WriteString(renderQuotaCells(cells, width, labelFg, inBar))
	}

	for i := 0; i < kCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}
	for i := 0; i < kCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// kimiQuotaCell converts a Kimi quota window into the shared quotaCell shape:
// nil fraction when the window is missing or unparsable.
func kimiQuotaCell(q *kimicode.Quota) quotaCell {
	var frac *float64
	if f, ok := q.RemainingFrac(); ok {
		frac = &f
	}
	return quotaCell{frac: frac, reset: q.ResetMs()}
}
