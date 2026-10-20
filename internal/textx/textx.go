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

// Cap keeps at most max runes of s and appends "…" when it cuts.
func Cap(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// Line makes s one clean line of at most max runes, with runs of whitespace
// collapsed.
func Line(s string, max int) string {
	return Cap(strings.Join(strings.Fields(Clean(s)), " "), max)
}

// Err is err's text as one clean line for a message (at most 200 runes).
func Err(err error) string { return Line(err.Error(), 200) }
