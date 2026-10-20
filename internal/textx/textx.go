// Package textx cleans text that may come from a repository before the plugin
// shows it: in the log, on the terminal or in Claude's context.
package textx

import (
	"strings"
	"unicode"
)

// Clean turns control and format characters (newlines, ANSI escapes, bidi
// overrides) into spaces.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}

// Cap keeps at most limit runes of s and appends "…" when it cuts.
func Cap(s string, limit int) string {
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// Line makes s one clean line of at most limit runes, with runs of whitespace
// collapsed.
func Line(s string, limit int) string {
	return Cap(strings.Join(strings.Fields(Clean(s)), " "), limit)
}

// Err is err's text as one clean line for a message (at most 200 runes).
func Err(err error) string { return Line(err.Error(), 200) }
