package daemon

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Colouring a string through lipgloss re-derives the escape sequence from
// scratch every single time. Style.Render asks termenv to resolve the colour
// (Profile.Convert) and then to emit it (RGBColor.Sequence), and both of those
// parse the "#rrggbb" string with fmt.Sscanf -- so a hex colour is scanned
// twice per render, and fmt's scan state is not free. Across a sidebar frame
// that came to ~90MB of the daemon's allocation, roughly a tenth of everything
// it allocated, to compute the same handful of escape sequences over and over.
//
// The sequence only depends on the colours, the weight, and the active colour
// profile, so it is derived once per combination and kept. See paint.

// paintKey identifies one colour/weight combination. The profile is part of
// the key because it decides how a hex colour is downsampled: the daemon pins
// TrueColor at startup, but tests switch profiles, and a stale Ascii-profile
// entry would silently strip colour from a real frame.
type paintKey struct {
	profile termenv.Profile
	fg, bg  string
	bold    bool
	faint   bool
}

// paintWrap is the escape sequence that goes around the text, split at the
// point where the text sits. Both halves are empty under the Ascii profile,
// which makes the fast path a no-op rather than a special case.
type paintWrap struct{ prefix, suffix string }

var (
	paintCache  sync.Map // paintKey -> paintWrap
	paintCacheN atomic.Int64
)

// paintCacheMax bounds the cache. The key set is normally tiny -- a theme's
// worth of colours -- but per-pane indicator colours and gradient ramps can
// widen it, so the whole map is dropped at the cap the way smallButtonCache
// does, costing one re-derivation per live colour.
const paintCacheMax = 512

// paintProbe stands in for the text while the wrapper is extracted. NUL
// survives Render untouched (it is not a tab, a carriage return, or a newline,
// the only bytes Render rewrites) and cannot occur inside a CSI sequence, so
// splitting on it recovers the prefix and suffix exactly.
const paintProbe = "\x00"

// paintStyle builds the lipgloss style for a key. It is the slow path, used to
// derive a wrapper once and to render anything the fast path declines.
func paintStyle(k paintKey) lipgloss.Style {
	style := lipgloss.NewStyle()
	if k.fg != "" {
		style = style.Foreground(lipgloss.Color(k.fg))
	}
	if k.bg != "" {
		style = style.Background(lipgloss.Color(k.bg))
	}
	if k.bold {
		style = style.Bold(true)
	}
	if k.faint {
		style = style.Faint(true)
	}
	return style
}

// paintWrapFor returns the cached escape wrapper for a key, deriving it on a
// miss. The second result is false if the wrapper could not be recovered, in
// which case the caller must fall back to lipgloss.
func paintWrapFor(k paintKey) (paintWrap, bool) {
	if v, ok := paintCache.Load(k); ok {
		return v.(paintWrap), true
	}
	rendered := paintStyle(k).Render(paintProbe)
	i := strings.Index(rendered, paintProbe)
	if i < 0 {
		return paintWrap{}, false
	}
	w := paintWrap{prefix: rendered[:i], suffix: rendered[i+len(paintProbe):]}
	if paintCacheN.Load() >= paintCacheMax {
		paintCache.Clear()
		paintCacheN.Store(0)
	}
	if _, loaded := paintCache.LoadOrStore(k, w); !loaded {
		paintCacheN.Add(1)
	}
	return w, true
}

// paint returns text coloured with fg on bg, bold if asked, identically to
// lipgloss.NewStyle().Foreground(fg).Background(bg).Bold(bold).Render(text)
// but without re-parsing the colours. An empty fg or bg means "leave it
// alone", matching an unset lipgloss style.
//
// Text containing a newline, carriage return, or tab goes back through
// lipgloss: Render styles each line separately and rewrites tabs, so a single
// wrapper would not reproduce it. Those are rare in a sidebar row.
func paint(text, fg, bg string, bold bool) string {
	return paintFull(text, fg, bg, bold, false)
}

// paintFull is paint with the dim attribute as well, for the indicator colours
// that fade when a window is minimised.
func paintFull(text, fg, bg string, bold, faint bool) string {
	k := paintKey{profile: lipgloss.ColorProfile(), fg: fg, bg: bg, bold: bold, faint: faint}
	if strings.ContainsAny(text, "\n\r\t") {
		return paintStyle(k).Render(text)
	}
	w, ok := paintWrapFor(k)
	if !ok {
		return paintStyle(k).Render(text)
	}
	if w.prefix == "" && w.suffix == "" {
		return text
	}
	return w.prefix + text + w.suffix
}

// paintOpen returns just the opening escape sequence for a colour combination,
// for callers that splice it into a line themselves rather than wrapping a
// string. Empty under the Ascii profile, and empty if the sequence could not
// be derived, so a caller that concatenates it unconditionally stays correct.
func paintOpen(fg, bg string, bold bool) string {
	w, ok := paintWrapFor(paintKey{profile: lipgloss.ColorProfile(), fg: fg, bg: bg, bold: bold})
	if !ok {
		return ""
	}
	return w.prefix
}

// paintFg colours text with a foreground only.
func paintFg(text, fg string) string { return paint(text, fg, "", false) }

// paintFgBold colours text with a bold foreground only.
func paintFgBold(text, fg string) string { return paint(text, fg, "", true) }

// paintOn colours text with a foreground over a background.
func paintOn(text, fg, bg string) string { return paint(text, fg, bg, false) }
