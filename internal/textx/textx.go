// Package textx cleans text that may come from a repository before the plugin
// shows it: in the log, on the terminal or in Claude's context.
package textx

import (
	"strings"
	"unicode"
	"unicode/utf8"
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

// StripControls drops control characters (C0, DEL, C1, and raw 0x80-0x9F
// bytes that are not valid UTF-8) from multi-line text, keeping "\n" and "\t".
// Everything else, invalid UTF-8 included, stays byte for byte; text without
// control characters is returned as is.
func StripControls(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		drop := (r != '\n' && r != '\t' && unicode.IsControl(r)) ||
			(r == utf8.RuneError && size == 1 && b[i] >= 0x80 && b[i] <= 0x9f)
		if drop && out == nil {
			out = append(make([]byte, 0, len(b)), b[:i]...)
		}
		if !drop && out != nil {
			out = append(out, b[i:i+size]...)
		}
		i += size
	}
	if out == nil {
		return b
	}
	return out
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
