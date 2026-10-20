// Package rootindex maintains the units index section in the root index file (CLAUDE.md or
// AGENTS.md).
package rootindex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"auto-context/internal/fsutil"
	"auto-context/internal/textx"
	"auto-context/internal/units"
)

const (
	openMarker  = "<!-- auto-context:units -->"
	closeMarker = "<!-- /auto-context:units -->"
	header      = "## Units (details: context/<unit>.md)"
)

// Entry is one unit line of the root index: unit path and summary.
type Entry struct {
	Unit, Summary string
}

// ErrBrokenMarkers reports unbalanced units markers in the root index file.
var ErrBrokenMarkers = errors.New("unbalanced auto-context:units markers in the root index file")

// maxSummary caps a unit summary in the index (runes); MaxSize caps the root
// index file we read.
const (
	maxSummary = 200
	MaxSize    = 2 << 20
)

// Render returns the index section including markers and a trailing newline.
func Render(entries []Entry) string { return render(header, entries, "\n") }

// eolOf returns "\r\n" if the first line of s ends with it, else "\n".
func eolOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 && s[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

func render(head string, entries []Entry, eol string) string {
	var b strings.Builder
	b.WriteString(openMarker + eol + head + eol)
	for _, e := range entries {
		if e.Unit == units.RootUnit || strings.ContainsAny(e.Unit, "`\r\n") {
			continue
		}
		b.WriteString("- `" + e.Unit + "`")
		if sum := sanitize(e.Summary); sum != "" {
			b.WriteString(" — " + sum)
		}
		b.WriteString(eol)
	}
	b.WriteString(closeMarker + eol)
	return b.String()
}

// sanitize keeps a summary from breaking the section or hiding text: no
// control or format characters (ANSI escapes, bidi overrides), no HTML comment
// delimiters (they would fake our markers), at most maxSummary runes.
func sanitize(s string) string {
	s = textx.Clean(s)
	for strings.Contains(s, "<!--") || strings.Contains(s, "-->") {
		s = strings.ReplaceAll(strings.ReplaceAll(s, "<!--", ""), "-->", "")
	}
	return textx.Cap(strings.TrimSpace(s), maxSummary)
}

// markers returns the line of the first open marker and of the first close
// marker (-1 if absent); markers are whole lines.
func markers(lines []string) (oi, ci int) {
	oi, ci = -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case openMarker:
			if oi < 0 && ci < 0 {
				oi = i
			}
		case closeMarker:
			if ci < 0 {
				ci = i
			}
		}
	}
	return oi, ci
}

// Check reports, without writing, what Update would refuse: ErrBrokenMarkers,
// or a root index file (name) that links outside the repo (fsutil.ErrOutside).
func Check(root, name string) error {
	path, err := fsutil.ResolveDoc(root, filepath.Join(root, name))
	if err != nil {
		return err
	}
	_, data, err := fsutil.ReadDoc(root, path, MaxSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if oi, ci := markers(strings.SplitAfter(string(data), "\n")); (oi >= 0 || ci >= 0) && (oi < 0 || ci < oi) {
		return ErrBrokenMarkers
	}
	return nil
}

// Update rewrites the section in <root>/<name> and reports whether the file
// changed. Markers count only as whole lines; a heading line right after the
// open marker is the user's and is kept.
func Update(root, name string, entries []Entry) (bool, error) {
	// An index file linking outside the repo, into .git or to a non-.md file is
	// neither read nor written; a link to another .md in the repo is followed.
	path, err := fsutil.ResolveDoc(root, filepath.Join(root, name))
	if err != nil {
		return false, err
	}
	perm := os.FileMode(0o644)
	var cur string
	_, data, err := fsutil.ReadDoc(root, path, MaxSize)
	switch {
	case err == nil:
		cur = string(data)
		if st, serr := os.Stat(path); serr == nil {
			perm = st.Mode().Perm()
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	}

	eol := eolOf(cur)
	lines := strings.SplitAfter(cur, "\n")
	oi, ci := markers(lines)
	var next string
	switch {
	case oi < 0 && ci < 0:
		next = cur
		if next != "" && !strings.HasSuffix(next, "\n"+eol) {
			if strings.HasSuffix(next, "\n") {
				next += eol
			} else {
				next += eol + eol
			}
		}
		next += render(header, entries, eol)
	case oi < 0 || ci < oi:
		return false, ErrBrokenMarkers
	default:
		head := header
		if oi+1 < ci && strings.HasPrefix(strings.TrimSpace(lines[oi+1]), "#") {
			head = strings.TrimRight(lines[oi+1], "\r\n")
		}
		next = strings.Join(lines[:oi], "") + render(head, entries, eol) + strings.Join(lines[ci+1:], "")
	}
	if next == cur {
		return false, nil
	}
	if err := fsutil.WriteDoc(root, path, []byte(next), perm); err != nil {
		return false, err
	}
	return true, nil
}
