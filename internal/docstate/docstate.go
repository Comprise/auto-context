// Package docstate computes ok/stale/missing state of unit context docs.
package docstate

import (
	"errors"
	"fmt"
	"os"
	"time"

	"auto-context/internal/frontmatter"
	"auto-context/internal/fsutil"
	"auto-context/internal/hashing"
	"auto-context/internal/units"
)

type State string

const (
	OK      State = "ok"
	Stale   State = "stale"
	Missing State = "missing"
)

type UnitStatus struct {
	Unit        string
	State       State
	ContextPath string // slash-separated, relative to root
	Summary     string
	// Note explains a Stale state that is not a plain hash mismatch; empty
	// otherwise. Detail is the error text for NoteUnreadable and NoteHashError.
	Note   Note
	Detail string
}

// OutsideText describes a refused doc (NoteOutside, DocRefused) in messages.
const OutsideText = "links outside the repository, into .git, to a non-.md file, or nowhere"

// Note is why a unit is Stale beyond a hash mismatch.
type Note string

const (
	NoteNotAcked   Note = "not acked yet"
	NoteOutside    Note = "symlink outside repo" // links outside the repo, into .git, to a non-.md file, or nowhere
	NoteBroken     Note = "broken frontmatter"
	NoteUnreadable Note = "unreadable doc"
	NoteHashError  Note = "cannot hash sources"
)

// NoteText is the note as `status` prints it, with the detail if any.
func (s UnitStatus) NoteText() string {
	if s.Detail != "" {
		return string(s.Note) + ": " + s.Detail
	}
	return string(s.Note)
}

// StatusOf returns the status of one unit; Note is set only when State is
// Stale (a doc or sources that cannot be read). It updates c but does not
// save it.
func StatusOf(root string, t *units.Tree, c *hashing.Cache, unit string) UnitStatus {
	_, ctx := units.DocPaths(unit)
	st := UnitStatus{Unit: unit, ContextPath: ctx, State: Stale}
	meta, body, err := frontmatter.Read(root, fsutil.Abs(root, ctx))
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.State = Missing
	case errors.Is(err, fsutil.ErrOutside):
		// Never read such a doc: its text would go into the root index.
		st.Note = NoteOutside
	case errors.Is(err, frontmatter.ErrNoFrontmatter):
		st.Summary = frontmatter.Summary(body)
		st.Note = NoteNotAcked
	case errors.Is(err, frontmatter.ErrBroken):
		st.Summary = frontmatter.Summary(body)
		st.Note = NoteBroken
	case err != nil:
		st.Note, st.Detail = NoteUnreadable, err.Error()
	default:
		st.Summary = frontmatter.Summary(body)
		if h, herr := sourceHash(root, t, c, unit); herr != nil {
			st.Note, st.Detail = NoteHashError, herr.Error()
		} else if meta.SourceHash == h {
			st.State = OK
		}
	}
	return st
}

// sourceHash hashes a unit's files; for _root it also covers the list of units,
// so adding or removing a unit makes the root docs stale.
func sourceHash(root string, t *units.Tree, c *hashing.Cache, unit string) (string, error) {
	var extra []string
	if unit == units.RootUnit {
		for _, u := range t.Units() {
			if u.Path != units.RootUnit {
				extra = append(extra, u.Path)
			}
		}
	}
	return hashing.SourceHash(root, t.Files(unit), extra, c)
}

// DocKind is what context/<unit>.md is on disk.
type DocKind int

const (
	DocAbsent     DocKind = iota
	DocPresent            // a regular file we may read and write
	DocRefused            // links outside the repo, into .git, to a non-.md file, or nowhere
	DocUnreadable         // exists but cannot be inspected (e.g. permissions)
)

// Doc resolves unit's context doc and tells what it is; path is set for
// DocPresent, err for DocUnreadable.
func Doc(root, unit string) (kind DocKind, path string, err error) {
	_, ctx := units.DocPaths(unit)
	p, err := fsutil.ResolveDoc(root, fsutil.Abs(root, ctx))
	if errors.Is(err, fsutil.ErrOutside) {
		return DocRefused, "", nil
	}
	if err != nil {
		return DocUnreadable, "", err
	}
	fi, err := os.Stat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return DocAbsent, "", nil
	case err != nil:
		return DocUnreadable, "", err
	case !fi.Mode().IsRegular():
		return DocRefused, "", nil
	}
	return DocPresent, p, nil
}

// Ack records the current source hash of unit in its context file. A doc that
// already records it is left untouched, so repeated acks (auto-ack on every
// Stop, a parallel session) do not rewrite it.
func Ack(root string, t *units.Tree, c *hashing.Cache, unit string, now time.Time) error {
	if !t.IsUnit(unit) {
		return fmt.Errorf("unknown unit %s", unit)
	}
	kind, path, err := Doc(root, unit)
	switch kind {
	case DocAbsent:
		return fmt.Errorf("no context/%s.md: create docs or run auto-context ack --exclude %s", unit, unit)
	case DocRefused:
		return fmt.Errorf("context/%s.md: %w", unit, fsutil.ErrOutside)
	case DocUnreadable:
		return err
	}
	h, err := sourceHash(root, t, c, unit)
	if err != nil {
		return err
	}
	if meta, _, err := frontmatter.Read(root, path); err == nil && meta.SourceHash == h && meta.Unit == unit {
		return nil
	}
	return frontmatter.SetHash(root, path, unit, h, now)
}
