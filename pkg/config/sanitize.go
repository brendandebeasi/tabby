package config

import (
	"reflect"
	"strconv"
	"strings"
)

// sanitizeColors blanks colour values that would crash a client binary.
//
// termenv treats an all-numeric colour string as a palette index. Under the
// ANSI profile it maps that index down to one of the 16 basic colours by
// indexing a 256-entry table without a bounds check, so `fg: "700"` in the
// config panics every renderer that runs under a 16-colour terminal — the
// sidebar, the popups, the pane and window headers. The daemon itself is safe
// because it pins termenv.TrueColor, but the clients adopt whatever profile
// their terminal reports, so a single typo in config.yaml takes the UI down on
// exactly the terminals least able to report why.
//
// Rather than enumerate the ~70 colour fields by hand (and miss the next one
// added), walk the config reflectively and check every string field whose yaml
// tag names it a colour. Out-of-range indices become "", which every consumer
// already reads as "unset" — applyDefaults then fills in the real default.
//
// Only numeric-but-out-of-range values are rejected. Hex, palette names and
// anything else termenv declines to parse are left exactly as they were: those
// are already handled (termenv returns no colour, tmux resolves names itself),
// and silently dropping them would change behaviour rather than protect it.
func sanitizeColors(cfg *Config) {
	if cfg == nil {
		return
	}
	sanitizeColorsValue(reflect.ValueOf(cfg), false)
}

// sanitizeColorsValue walks v, cleaning string leaves reached through a field
// tagged as a colour. isColor is inherited by elements so that a []string of
// colours is cleaned too.
func sanitizeColorsValue(v reflect.Value, isColor bool) {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			sanitizeColorsValue(v.Elem(), isColor)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).PkgPath != "" { // unexported
				continue
			}
			sanitizeColorsValue(v.Field(i), isColorTag(t.Field(i).Tag.Get("yaml")))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			sanitizeColorsValue(v.Index(i), isColor)
		}
	case reflect.Map:
		// Map values aren't addressable, so clean a copy and write it back.
		for _, k := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(k))
			sanitizeColorsValue(elem, isColor)
			v.SetMapIndex(k, elem)
		}
	case reflect.String:
		if isColor && v.CanSet() && !validColor(v.String()) {
			v.SetString("")
		}
	}
}

// isColorTag reports whether a yaml tag names a colour field: exactly "fg",
// "bg" or "color", or something ending in "_fg", "_bg" or "_color". The
// underscore matters — it keeps "hide_predefined_colors" out.
func isColorTag(tag string) bool {
	name, _, _ := strings.Cut(tag, ",")
	for _, suffix := range []string{"fg", "bg", "color"} {
		if name == suffix || strings.HasSuffix(name, "_"+suffix) {
			return true
		}
	}
	return false
}

// validColor reports whether s is safe to hand to termenv. Anything termenv
// reads as a palette index must be in range; everything else passes through.
func validColor(s string) bool {
	n, err := strconv.Atoi(s)
	if err != nil {
		return true
	}
	return n >= 0 && n <= 255
}
