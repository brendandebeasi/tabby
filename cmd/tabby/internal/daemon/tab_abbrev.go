package daemon

// Automatic directory-name abbreviation for sidebar tab labels. Deterministic,
// no AI: a folder name is compressed to a short upper-case code that prefixes
// the tab's AI summary (e.g. "tabby" + "refactor auth" -> "TBY refactor auth").
// An explicit config entry (tab_names.abbreviations, "CODE>Folder") overrides
// the derived code for a given folder.

import (
	"strings"
	"unicode"
)

// tabAbbreviation returns the short code for a directory's folder name: an
// explicit config override if one exists, otherwise an auto-derived code.
// Returns "" for the empty/root/home folder so those tabs keep their plain name.
func (c *Coordinator) tabAbbreviation(folder string) string {
	folder = strings.TrimSpace(folder)
	if folder == "" || folder == "/" || folder == "~" {
		return ""
	}
	if code, ok := c.dirAbbreviation(folder); ok {
		return code // explicit config override
	}
	return abbreviateFolder(folder)
}

// abbreviateWindowName abbreviates a window's name as a fallback project code,
// used by windowDirCode only when no project DIRECTORY can be resolved (e.g. a
// $HOME window, or a window with no content cwd). Composite names — panes
// spanning different dirs, joined by " | " — are abbreviated segment-by-segment
// with the separator preserved, e.g. "api | web" -> "API | WEB". Returns "" for
// empty/home/root names so the tab keeps its plain label.
func (c *Coordinator) abbreviateWindowName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "~" || name == "/" {
		return ""
	}
	segs := strings.Split(name, " | ")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		if code := c.tabAbbreviation(strings.TrimSpace(seg)); code != "" {
			out = append(out, code)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " | ")
}

// abbreviateFolder compresses a single directory name to a short upper-case code:
//   - multi-word names (separated by -_. space or camelCase) -> initials, e.g.
//     "claude-flow" -> "CF", "myProjectName" -> "MPN" (max 4)
//   - short single words (<=4 runes) -> the word upper-cased, e.g. "src" -> "SRC"
//   - long single words -> first letter + following consonants, consecutive dups
//     collapsed, e.g. "tabby" -> "TBY", "config" -> "CNF" (max 3)
func abbreviateFolder(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	words := splitWords(name)
	if len(words) >= 2 {
		var b []rune
		for _, w := range words {
			r := []rune(w)
			if len(r) == 0 {
				continue
			}
			b = append(b, unicode.ToUpper(r[0]))
			if len(b) >= 4 {
				break
			}
		}
		return string(b)
	}

	w := []rune(words[0])
	if len(w) <= 4 {
		return strings.ToUpper(words[0])
	}

	out := []rune{unicode.ToUpper(w[0])}
	last := unicode.ToLower(w[0])
	for _, r := range w[1:] {
		if len(out) >= 3 {
			break
		}
		lr := unicode.ToLower(r)
		if isVowelRune(lr) {
			last = lr
			continue
		}
		if lr == last {
			continue // collapse consecutive duplicate consonants ("tabby" -> TBY)
		}
		out = append(out, unicode.ToUpper(r))
		last = lr
	}
	return string(out)
}

// splitWords breaks a name on -_. space separators and camelCase boundaries.
func splitWords(s string) []string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		switch r {
		case '-', '_', '.', ' ':
			b.WriteRune(' ')
			continue
		}
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
			b.WriteRune(' ') // camelCase boundary
		}
		b.WriteRune(r)
	}
	return strings.Fields(b.String())
}

func isVowelRune(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}
