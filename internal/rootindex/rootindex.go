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

// ErrBrokenMarkers reports unbalanced or repeated units markers in the root
// index file.
var ErrBrokenMarkers = errors.New("unbalanced or repeated auto-context:units markers in the root index file")

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

// bom is the UTF-8 byte order mark; a file may start with it, and it is kept.
const bom = "\ufeff"

// fence reports the fence character and run length if l opens or closes a
// code fence: 3 or more backticks or tildes after any indent (a fence in a
// list item is indented by the item, and CommonMark's 3-space limit is
// relative to it). info is the rest of the line after the run.
func fence(l string) (c byte, n int, info string) {
	t := strings.TrimLeft(l, " \t")
	if t == "" || (t[0] != '`' && t[0] != '~') {
		return 0, 0, ""
	}
	for n < len(t) && t[n] == t[0] {
		n++
	}
	if n < 3 {
		return 0, 0, ""
	}
	return t[0], n, strings.TrimSpace(t[n:])
}

// fenced marks the lines inside closed code fences (fence lines included)
// and returns the fence of the first opener that is never closed ("" if
// none). An unclosed fence hides nothing: Update closes it before appending
// a section, so the section is not swallowed by a fence closed later.
func fenced(lines []string) (in []bool, open string) {
	in = make([]bool, len(lines))
	for i := 0; i < len(lines); i++ {
		c, n, info := fence(lines[i])
		if c == 0 || (c == '`' && strings.ContainsRune(info, '`')) {
			continue
		}
		closed := false
		for j := i + 1; j < len(lines); j++ {
			if cc, nn, rest := fence(lines[j]); cc == c && nn >= n && rest == "" {
				for k := i; k <= j; k++ {
					in[k] = true
				}
				i, closed = j, true
				break
			}
		}
		if !closed && open == "" {
			open = strings.Repeat(string(c), n)
		}
	}
	return in, open
}

// markers returns the line of the open and of the close marker, -1 if both
// are absent. Markers are whole lines outside code fences (a leading BOM is
// ignored); a lone, misordered or repeated marker is ErrBrokenMarkers.
func markers(lines []string) (oi, ci int, err error) {
	oi, ci = -1, -1
	no, nc := 0, 0
	in, _ := fenced(lines)
	for i, l := range lines {
		if in[i] {
			continue
		}
		if i == 0 {
			l = strings.TrimPrefix(l, bom)
		}
		switch strings.TrimSpace(l) {
		case openMarker:
			oi, no = i, no+1
		case closeMarker:
			ci, nc = i, nc+1
		}
	}
	if no != nc || no > 1 || ci < oi {
		return -1, -1, ErrBrokenMarkers
	}
	return oi, ci, nil
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
	_, _, err = markers(strings.SplitAfter(strings.TrimPrefix(string(data), bom), "\n"))
	return err
}

// Update rewrites the section in <root>/<name> and reports whether the file
// changed. Markers count only as whole lines outside code fences; a heading line right after the
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
	lines := strings.SplitAfter(strings.TrimPrefix(cur, bom), "\n")
	oi, ci, err := markers(lines)
	if err != nil {
		return false, err
	}
	var next string
	if oi < 0 {
		next = cur
		if _, open := fenced(lines); open != "" {
			// Close the file's dangling fence (it ran to the end anyway), or a
			// fence the user closes later would swallow the section.
			if !strings.HasSuffix(next, "\n") {
				next += eol
			}
			next += open + eol
		}
		if next != "" && !strings.HasSuffix(next, "\n"+eol) {
			if strings.HasSuffix(next, "\n") {
				next += eol
			} else {
				next += eol + eol
			}
		}
		next += render(header, entries, eol)
	} else {
		head := header
		if oi+1 < ci && strings.HasPrefix(strings.TrimSpace(lines[oi+1]), "#") {
			head = strings.TrimRight(lines[oi+1], "\r\n")
		}
		next = strings.Join(lines[:oi], "") + render(head, entries, eol) + strings.Join(lines[ci+1:], "")
		if strings.HasPrefix(cur, bom) {
			next = bom + next
		}
	}
	if next == cur {
		return false, nil
	}
	if err := fsutil.WriteDoc(root, path, []byte(next), perm); err != nil {
		return false, err
	}
	return true, nil
}
