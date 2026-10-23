// Package docstate computes ok/stale/missing state of unit context docs.
package docstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"auto-context/internal/frontmatter"
	"auto-context/internal/fsutil"
	"auto-context/internal/hashing"
	"auto-context/internal/units"
)

// State is the freshness of a unit's context doc: ok, stale or missing.
type State string

const (
	OK      State = "ok"
	Stale   State = "stale"
	Missing State = "missing"
)

// UnitStatus is the doc state of one unit with its context path and summary.
type UnitStatus struct {
	Unit        string
	State       State
	ContextPath string // slash-separated, relative to root
	Summary     string
	// Note explains a Stale state that is not a plain hash mismatch, or an OK
	// one recorded in the old hash scheme (NoteOldScheme); empty otherwise.
	// Detail is a fixed category (fsutil.Category) for NoteUnreadable and
	// NoteHashError: error texts quote repository paths, so they stay out.
	Note   Note
	Detail string
}

// OutsideText describes a refused doc (NoteOutside, DocRefused) in messages.
const OutsideText = "links outside the repository, into .git, to a non-.md file, or nowhere"

// HashPrefix marks source_hash of scheme v2, hashed over the index view
// (Tree.HashFiles, Tree.HashUnits). A hash without it is of the old scheme:
// over the full view, untracked files included.
const HashPrefix = "v2:"

// Note is why a unit is Stale beyond a hash mismatch, or NoteOldScheme.
type Note string

const (
	NoteNotAcked   Note = "not acked yet"
	NoteOutside    Note = "unusable symlink" // links outside the repo, into .git, to a non-.md file, to a non-file, or nowhere
	NoteBroken     Note = "broken frontmatter"
	NoteUnreadable Note = "unreadable doc"
	NoteHashError  Note = "cannot hash sources"
	// NoteOldScheme goes with OK: the doc records a hash of the old scheme
	// that still matches the code; ack rewrites it in scheme v2.
	NoteOldScheme Note = "old hash scheme"
)

// TimeUp is the Detail of NoteHashError when the caller's context ran out
// (a hook's budget) before the unit's sources were hashed.
const TimeUp = "the hook's time is up"

// Interrupted is the Detail of NoteHashError when the caller's context was
// cancelled (an interrupted CLI command) before the unit's sources were hashed.
const Interrupted = "interrupted"

// NoteText is the note as `status` prints it, with the detail if any.
func (s UnitStatus) NoteText() string {
	if s.Detail != "" {
		return string(s.Note) + ": " + s.Detail
	}
	return string(s.Note)
}

// OldScheme reports whether st is up to date but recorded in the old hash
// scheme, so an ack would only upgrade it.
func OldScheme(st UnitStatus) bool { return st.State == OK && st.Note == NoteOldScheme }

// DocText is a unit's context doc as frontmatter.Read returned it.
type DocText struct {
	Meta frontmatter.Meta
	Body string
	Err  error
}

// ReadDoc reads unit's context doc for StatusOfDoc.
func ReadDoc(root, unit string) DocText {
	meta, body, err := frontmatter.Read(root, fsutil.Abs(root, units.ContextPath(unit)))
	return DocText{Meta: meta, Body: body, Err: err}
}

// StatusOf returns the status of one unit; Note is set when State is Stale
// (a doc or sources that cannot be read, or hashing cut short by ctx: Detail
// TimeUp on a deadline, Interrupted on a cancel) or, as NoteOldScheme, when it is OK by the old hash scheme. It
// updates c but does not save it.
func StatusOf(ctx context.Context, root string, t *units.Tree, c *hashing.Cache, unit string) UnitStatus {
	return StatusOfDoc(ctx, root, t, c, unit, ReadDoc(root, unit))
}

// StatusOfDoc is StatusOf with unit's doc already read (ReadDoc), so a
// caller that inspected the doc does not read it again.
func StatusOfDoc(ctx context.Context, root string, t *units.Tree, c *hashing.Cache, unit string, d DocText) UnitStatus {
	st := UnitStatus{Unit: unit, ContextPath: units.ContextPath(unit), State: Stale}
	meta, body, err := d.Meta, d.Body, d.Err
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.State = Missing
	case errors.Is(err, fsutil.ErrOutside), errors.Is(err, fsutil.ErrNotRegular):
		// Never read such a doc: its text would go into the root index. A
		// link to a directory is refused as Doc refuses it.
		st.Note = NoteOutside
	case errors.Is(err, frontmatter.ErrNoFrontmatter):
		st.Summary = frontmatter.Summary(body)
		st.Note = NoteNotAcked
	case errors.Is(err, frontmatter.ErrBroken):
		st.Summary = frontmatter.Summary(body)
		st.Note = NoteBroken
	case err != nil:
		st.Note, st.Detail = NoteUnreadable, fsutil.Category(err)
	default:
		st.Summary = frontmatter.Summary(body)
		legacy := !strings.HasPrefix(meta.SourceHash, HashPrefix)
		hash := sourceHash
		if legacy {
			hash = legacyHash
		}
		h, herr := hash(ctx, root, t, c, unit)
		switch {
		case errors.Is(herr, context.DeadlineExceeded):
			st.Note, st.Detail = NoteHashError, TimeUp
		case errors.Is(herr, context.Canceled):
			st.Note, st.Detail = NoteHashError, Interrupted
		case herr != nil:
			st.Note, st.Detail = NoteHashError, fsutil.Category(herr)
		case meta.SourceHash == h:
			st.State = OK
			if legacy {
				st.Note = NoteOldScheme
			}
		}
	}
	return st
}

// SummaryOf returns the summary of unit's doc without hashing its sources;
// "" when the doc cannot be read or is refused.
func SummaryOf(root, unit string) string {
	_, body, err := frontmatter.Read(root, fsutil.Abs(root, units.ContextPath(unit)))
	if err != nil && !errors.Is(err, frontmatter.ErrNoFrontmatter) && !errors.Is(err, frontmatter.ErrBroken) {
		return ""
	}
	return frontmatter.Summary(body)
}

// sourceHash is the scheme v2 hash of a unit: its files in the index view;
// for _root also the index view's units, so adding or removing a unit makes
// the root docs stale. A unit only of the full view (untracked files only)
// hashes an empty list.
func sourceHash(ctx context.Context, root string, t *units.Tree, c *hashing.Cache, unit string) (string, error) {
	var extra []string
	if unit == units.RootUnit {
		extra = t.HashUnits()
	}
	h, err := hashing.SourceHash(ctx, root, t.HashFiles(unit), extra, c)
	if err != nil {
		return "", err
	}
	return HashPrefix + h, nil
}

// legacyHash is the old scheme hash, as v1.0.0 recorded it: the full view's
// files and, for _root, all other units.
func legacyHash(ctx context.Context, root string, t *units.Tree, c *hashing.Cache, unit string) (string, error) {
	var extra []string
	if unit == units.RootUnit {
		for _, u := range t.Units() {
			if u.Path != units.RootUnit {
				extra = append(extra, u.Path)
			}
		}
	}
	return hashing.SourceHash(ctx, root, t.Files(unit), extra, c)
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
	ctx := units.ContextPath(unit)
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

// AckIfNeeded records the current scheme v2 source hash of unit in its
// context file, with the commit head returns (nil or "" records none), and
// tells whether it wrote the file. A doc that already records the hash is left
// untouched, commit included, so repeated acks (auto-ack on every Stop, a
// parallel session) do not rewrite it; head is called only to rewrite. A doc
// of the old scheme is always rewritten.
func AckIfNeeded(ctx context.Context, root string, t *units.Tree, c *hashing.Cache, unit string, now time.Time, head func() string) (written bool, err error) {
	if !t.IsUnit(unit) {
		return false, fmt.Errorf("%w %s", units.ErrUnknownUnit, unit)
	}
	kind, path, err := Doc(root, unit)
	switch kind {
	case DocAbsent:
		if unit == units.RootUnit {
			return false, errors.New("no context/_root.md: create it (the root unit cannot be excluded)")
		}
		return false, fmt.Errorf("no context/%s.md: create docs or run auto-context ack --exclude %s", unit, unit)
	case DocRefused:
		return false, fmt.Errorf("context/%s.md: %w", unit, fsutil.ErrOutside)
	case DocUnreadable:
		return false, err
	}
	h, err := sourceHash(ctx, root, t, c, unit)
	if err != nil {
		return false, err
	}
	if meta, _, err := frontmatter.Read(root, path); err == nil && meta.SourceHash == h && meta.Unit == unit {
		return false, nil
	}
	commit := ""
	if head != nil {
		commit = head()
	}
	if err := frontmatter.SetHash(root, path, unit, h, commit, now); err != nil {
		return false, err
	}
	return true, nil
}
