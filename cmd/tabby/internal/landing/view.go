package landing

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/brendandebeasi/tabby/pkg/textwidth"
)

// Layout breakpoints, in drawn columns of the whole pane.
const (
	twoColumnMin = 100 // wide enough for two side-by-side group columns
	compactMax   = 59  // at or below this, drop group headers
	wordmarkMin  = 34  // narrower than the banner plus a margin, so use the name
)

// Column counts the layout uses at a given width.
func columnsFor(width int) int {
	if width >= twoColumnMin {
		return 2
	}
	return 1
}

// gutter is the blank space between the two group columns.
const gutter = 4

var (
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	groupStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	filterStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	fallbackColor = "250"
)

// wordmark is the "TABBY" banner, one string per row. Drawn by hand rather
// than generated: a figlet library would be a new module and a vendor refresh
// for a single fixed banner.
var wordmark = []string{
	"##### #### ####  ####  #   #",
	"  #   #  # #   # #   #  # # ",
	"  #   #### ####  ####    #  ",
	"  #   #  # #   # #   #   #  ",
	"  #   #  # ####  ####    #  ",
}

// sweepPalette is the ramp the header animation cycles through. It runs warm
// to cool and back so the sweep reads as a continuous wave rather than a jump
// when it wraps.
var sweepPalette = []string{
	"#bcce5a", "#7fc47a", "#4ad9a6", "#39b8c4", "#5a8fce",
	"#7f6ad9", "#a85ace", "#c65a9e", "#d96a6a", "#ce9a5a",
}

// renderHeader draws the wordmark with a color sweep offset by phase.
//
// Each column takes its color from the palette indexed by (column + phase), so
// advancing phase by one per tick moves a band of color across the letters.
func renderHeader(width, phase int) string {
	if width < wordmarkMin {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(sweepPalette[phase%len(sweepPalette)])).
			Bold(true).
			Render("tabby")
	}
	var rows []string
	for _, row := range wordmark {
		var b strings.Builder
		for i, r := range []rune(row) {
			if r == ' ' {
				b.WriteRune(' ')
				continue
			}
			c := sweepPalette[(i+phase)%len(sweepPalette)]
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("#"))
		}
		rows = append(rows, b.String())
	}
	return strings.Join(rows, "\n")
}

// entryColor is the entry's configured color, or a neutral default for the
// entries the join found no appearance for.
func entryColor(e Entry) string {
	if e.Color != "" {
		return e.Color
	}
	return fallbackColor
}

// readableFg picks black or white text for a background, by relative
// luminance. Used only for the selected row, which fills with the entry color.
func readableFg(hex string) string {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return "255"
	}
	// Rec. 601 luma, good enough to decide between two extremes.
	lum := (299*r + 587*g + 114*b) / 1000
	if lum > 140 {
		return "#000000"
	}
	return "#ffffff"
}

func parseHex(hex string) (r, g, b int, ok bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff, true
}

// renderRow draws one entry clamped to exactly width columns.
//
// Width is measured with textwidth, not lipgloss.Width: several icons are
// emoji presentation sequences that draw two columns but measure one, and a
// row one column too wide wraps and scrolls the alt-screen frame.
func renderRow(e Entry, width int, selected bool) string {
	if width <= 0 {
		return ""
	}
	icon := e.Icon
	if icon == "" {
		icon = " "
	}
	iconW := textwidth.Display(icon)
	// Normalize every icon to a two-column cell so labels line up whether the
	// icon is an emoji, a Nerd Font glyph, or absent.
	if iconW < 2 {
		icon += strings.Repeat(" ", 2-iconW)
	}

	marker := "  "
	if selected {
		marker = "> "
	}

	left := marker + icon + " " + e.Label
	body := left
	if target := e.Target(); target != "" && target != e.Label {
		// Right-align the target, but only when there is enough room left for
		// it to be worth reading. Below that, the label alone is the row.
		pad := width - textwidth.Display(left) - textwidth.Display(target) - 1
		if pad >= 2 {
			shown := target
			if !selected {
				shown = dimStyle.Render(target)
			}
			body = left + strings.Repeat(" ", pad) + shown + " "
		}
	}

	color := entryColor(e)
	var st lipgloss.Style
	if selected {
		st = lipgloss.NewStyle().
			Background(lipgloss.Color(color)).
			Foreground(lipgloss.Color(readableFg(color))).
			Bold(true)
	} else {
		st = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	}

	plain := textwidth.Clamp(body, width)
	plain = textwidth.Pad(plain, width)
	return st.Render(plain)
}

// renderGroups lays the grouped entries out in one or two columns and returns
// the rows, already clamped to width, plus the row index the cursor landed on
// so the caller can scroll it into view. cursorRow is -1 if the cursor entry is
// not among the groups.
func renderGroups(groups []group, cursorEntry *Entry, width int, compact bool) (rows []string, cursorRow int) {
	if columnsFor(width) == 1 {
		return renderColumn(groups, cursorEntry, width, compact)
	}
	left, right := splitGroups(groups)
	colW := (width - gutter) / 2
	l, lc := renderColumn(left, cursorEntry, colW, compact)
	r, rc := renderColumn(right, cursorEntry, colW, compact)
	n := len(l)
	if len(r) > n {
		n = len(r)
	}
	cursorRow = -1
	if lc >= 0 {
		cursorRow = lc
	} else if rc >= 0 {
		cursorRow = rc
	}
	rows = make([]string, 0, n)
	for i := 0; i < n; i++ {
		lr := strings.Repeat(" ", colW)
		if i < len(l) {
			lr = l[i]
		}
		rr := ""
		if i < len(r) {
			rr = r[i]
		}
		rows = append(rows, lr+strings.Repeat(" ", gutter)+rr)
	}
	return rows, cursorRow
}

// splitGroups balances groups across two columns by row count, keeping each
// group whole so a group header never separates from its entries.
func splitGroups(groups []group) (left, right []group) {
	total := 0
	for _, g := range groups {
		total += len(g.entries) + 1
	}
	half := total / 2
	acc := 0
	for _, g := range groups {
		if acc < half || len(right) == 0 && acc+len(g.entries)+1 <= half {
			left = append(left, g)
		} else {
			right = append(right, g)
		}
		acc += len(g.entries) + 1
	}
	return left, right
}

func renderColumn(groups []group, cursorEntry *Entry, width int, compact bool) (rows []string, cursorRow int) {
	cursorRow = -1
	for gi, g := range groups {
		if gi > 0 {
			rows = append(rows, strings.Repeat(" ", width))
		}
		if g.name != "" && !compact {
			rows = append(rows, textwidth.Pad(groupStyle.Render(g.name), width))
		}
		for _, e := range g.entries {
			sel := cursorEntry != nil && sameEntry(*cursorEntry, e)
			if sel {
				cursorRow = len(rows)
			}
			rows = append(rows, renderRow(e, width, sel))
		}
	}
	return rows, cursorRow
}

func sameEntry(a, b Entry) bool {
	return a.Label == b.Label && a.Kind == b.Kind && a.Target() == b.Target()
}

// renderHint is the key legend at the foot of the page. It steps down to a
// shorter legend rather than being clipped mid-word in a narrow pane.
func renderHint(width int, filter string) string {
	if filter != "" {
		hint := "   enter open   esc clear"
		if width < 40 {
			hint = "   enter   esc"
		}
		return textwidth.Clamp(filterStyle.Render("/"+filter)+dimStyle.Render(hint), width)
	}
	long := "up/down move   enter open   type to filter   esc shell"
	short := "up/down   enter open   esc shell"
	tiny := "enter open   esc shell"
	s := long
	if width < textwidth.Display(long) {
		s = short
	}
	if width < textwidth.Display(short) {
		s = tiny
	}
	return textwidth.Clamp(dimStyle.Render(s), width)
}

// renderEmpty is what the page shows when the filter matches nothing.
func renderEmpty(filter string) string {
	return dimStyle.Render(fmt.Sprintf("no entry matches %q", filter))
}
