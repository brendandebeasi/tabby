package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestValidColor(t *testing.T) {
	valid := []string{"", "0", "255", "15", "#ff8800", "#fff", "red", "colour4", " 12", "12 ", "1e3", "0x10"}
	for _, s := range valid {
		if !validColor(s) {
			t.Errorf("validColor(%q) = false, want true", s)
		}
	}
	invalid := []string{"256", "700", "999999", "-1", "+300"}
	for _, s := range invalid {
		if validColor(s) {
			t.Errorf("validColor(%q) = true, want false", s)
		}
	}
}

func TestIsColorTag(t *testing.T) {
	yes := []string{"fg", "bg", "color", "active_fg", "border_bg", "handle_color", "bg,omitempty"}
	for _, tag := range yes {
		if !isColorTag(tag) {
			t.Errorf("isColorTag(%q) = false, want true", tag)
		}
	}
	no := []string{"", "icon", "colors", "hide_predefined_colors", "name", "background"}
	for _, tag := range no {
		if isColorTag(tag) {
			t.Errorf("isColorTag(%q) = true, want false", tag)
		}
	}
}

// TestSanitizeColorsReachesNestedFields plants an out-of-range index in a
// top-level field, a nested struct and a slice element, and checks all three
// are cleaned — the walk is the whole point, a single-field test would pass
// against a hand-written enumeration too.
func TestSanitizeColorsReachesNestedFields(t *testing.T) {
	cfg := &Config{}
	cfg.Sidebar.Colors.Bg = "700"
	cfg.Sidebar.Colors.HeaderFg = "#ff8800"
	cfg.Groups = []Group{{Name: "a", Theme: Theme{Bg: "999999", Fg: "12"}}}

	sanitizeColors(cfg)

	if got := cfg.Sidebar.Colors.Bg; got != "" {
		t.Errorf("Sidebar.Colors.Bg = %q, want cleared", got)
	}
	if got := cfg.Sidebar.Colors.HeaderFg; got != "#ff8800" {
		t.Errorf("Sidebar.Colors.HeaderFg = %q, want untouched", got)
	}
	if got := cfg.Groups[0].Theme.Bg; got != "" {
		t.Errorf("Groups[0].Theme.Bg = %q, want cleared", got)
	}
	if got := cfg.Groups[0].Theme.Fg; got != "12" {
		t.Errorf("Groups[0].Theme.Fg = %q, want untouched", got)
	}
	if got := cfg.Groups[0].Name; got != "a" {
		t.Errorf("Groups[0].Name = %q, want untouched", got)
	}
}

// TestSanitizeColorsFromYAML checks the cleaning happens on the real load path
// rather than only when sanitizeColors is called directly.
func TestSanitizeColorsFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "sidebar:\n  colors:\n    bg: \"700\"\n    header_fg: \"#ff8800\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sidebar.Colors.Bg == "700" {
		t.Error("LoadConfig left an out-of-range palette index in place")
	}
	if cfg.Sidebar.Colors.HeaderFg != "#ff8800" {
		t.Errorf("LoadConfig changed a valid colour to %q", cfg.Sidebar.Colors.HeaderFg)
	}
}

// TestEveryColorFieldIsSanitized walks the Config type and asserts that every
// string field whose yaml tag names a colour actually gets cleaned. It fails
// when a new colour field arrives with a tag the walk doesn't recognise.
func TestEveryColorFieldIsSanitized(t *testing.T) {
	var n int
	var walk func(t reflect.Type, path string)
	seen := map[reflect.Type]bool{}
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			ft := f.Type
			for ft.Kind() == reflect.Ptr || ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.String {
				lower := strings.ToLower(name)
				looksLikeColor := strings.HasSuffix(lower, "fg") || strings.HasSuffix(lower, "bg") || strings.HasSuffix(lower, "color")
				if looksLikeColor && !isColorTag(name) {
					t.Errorf("%s.%s (yaml:%q) looks like a colour but isColorTag says no", path, f.Name, name)
				}
				if isColorTag(name) {
					n++
				}
				continue
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(Config{}), "Config")
	if n < 60 {
		t.Errorf("only %d colour fields recognised; the tag rule probably stopped matching", n)
	}
}

// TestSanitizeColorsMatchesTermenv checks the property that matters against the
// real library rather than against a remembered constant: nothing validColor
// lets through may panic termenv. ANSI is the profile that crashes — it maps a
// palette index down to one of 16 colours by indexing a 256-entry table with no
// bounds check — so that is the profile to probe.
//
// The converse does not hold, and deliberately so. A negative index doesn't
// panic; termenv computes 30+i and emits a nonsense SGR code (29 for "-1",
// which is "not crossed out"). validColor rejects those too: they are typos
// that would silently paint nothing rather than crash.
func TestSanitizeColorsMatchesTermenv(t *testing.T) {
	for i := -300; i <= 300; i++ {
		s := strconv.Itoa(i)
		if validColor(s) && !termenvSurvives(s) {
			t.Errorf("validColor(%q) = true but termenv panics on it", s)
		}
	}
	// And the range really is the panic boundary, not somewhere short of it.
	if termenvSurvives("256") {
		t.Error(`termenv no longer panics on "256"; validColor's ceiling may be stale`)
	}
	if !termenvSurvives("255") {
		t.Error(`termenv now panics on "255"; validColor's ceiling is too high`)
	}
}

// termenvSurvives reports whether rendering with s as a foreground colour under
// the 16-colour profile returns instead of panicking.
func termenvSurvives(s string) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	_ = termenv.ANSI.Convert(termenv.ANSI.Color(s)).Sequence(false)
	return true
}
