// Package units discovers documentation units in a repository and classifies paths.
package units

import (
	"errors"
	"path"
	"slices"
	"strings"
	"unicode"

	"auto-context/internal/config"
	"auto-context/internal/filestat"
)

// ErrUnknownUnit is wrapped by errors about a name that is no discovered unit.
var ErrUnknownUnit = errors.New("unknown unit")

// RootUnit is the unit of the repository root (files outside any other unit).
const RootUnit = "_root"

// Reason tells why a directory is a unit.
type Reason string

const (
	ReasonRoot    Reason = "root"
	ReasonInclude Reason = "include" // matches an include pattern
	ReasonModule  Reason = "module"  // holds a module marker such as go.mod
	ReasonFiles   Reason = "files"   // holds at least min_files source files
)

// Unit is a documentation unit: its path, why it is one and its source file count.
type Unit struct {
	Path      string
	Reason    Reason
	FileCount int
}

// Kind classifies a repository path: ignored, code, index doc or context doc.
type Kind int

const (
	KindIgnore     Kind = iota
	KindCode            // a source file of a unit
	KindIndexDoc        // <unit>/<index file> (CLAUDE.md or AGENTS.md)
	KindContextDoc      // context/<unit>.md
)

// Tree is the result of one Discover: units, their files and the stat data
// seen. It is immutable once Discover returns, so it may be read from several
// goroutines: the state matching spends (a glob budget, memos, warnings)
// lives in a matcher that Discover keeps to itself, and Classify makes a
// fresh one per call.
type Tree struct {
	root      string // as Discover resolved it
	cfg       config.Config
	units     map[string]Unit
	files     map[string][]string // unit -> code files (sorted)
	hashUnits map[string]Unit     // index view (index entries only); units itself when the views agree
	hashFiles map[string][]string // index view: unit -> code files (sorted); files itself likewise
	tracked   []string            // sorted
	pats      *patterns           // exclude/track patterns and .gitignore files Discover loaded
	warnCount int                 // all warnings, also those past maxWarnings
	warnings  []string
	viaGit    bool                     // files were listed by git, not walked
	stats     map[string]filestat.Stat // tracked file -> stat data seen by Discover
}

// Units returns all units: _root first, the rest sorted by Path.
func (t *Tree) Units() []Unit {
	out := make([]Unit, 0, len(t.units))
	for _, u := range t.units {
		if u.Path != RootUnit {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b Unit) int { return strings.Compare(a.Path, b.Path) })
	return append([]Unit{t.units[RootUnit]}, out...)
}

// IsUnit reports whether p is the path of a discovered unit.
func (t *Tree) IsUnit(p string) bool {
	_, ok := t.units[p]
	return ok
}

// UnitOf returns the nearest ancestor unit of rel's directory, else _root.
func (t *Tree) UnitOf(rel string) string { return unitOf(t.units, rel) }

// unitOf is UnitOf over the given units.
func unitOf(units map[string]Unit, rel string) string {
	dir := path.Dir(path.Clean(rel))
	for dir != "." && dir != "/" && dir != "" {
		if _, ok := units[dir]; ok {
			return dir
		}
		dir = path.Dir(dir)
	}
	return RootUnit
}

// Classify says what a repo path is to the plugin and which unit it belongs
// to: a unit's index file or context doc, a source of a unit, or nothing
// (KindIgnore, also for docs of directories that are no unit). It applies the
// same rules as Discover, so a file created since is classified too; each
// call has a glob budget of its own and records no warning. For many paths
// use a Classifier: its calls share one budget.
func (t *Tree) Classify(rel string) (Kind, string) { return t.classify(rel, nil) }

// Classifier classifies many paths against one tree with one glob budget
// (maxGlobWork) and one skipDir memo, so a pass over many paths stays
// bounded however hostile the .gitignore rules. It never changes the tree
// and is not safe for concurrent use: make one per pass (and goroutine).
type Classifier struct {
	t *Tree
	m *matcher
}

// Classifier returns a new Classifier over t.
func (t *Tree) Classifier() *Classifier {
	return &Classifier{t: t, m: newMatcher(t.pats, true, func(string) {})}
}

// Classify is Tree.Classify, spending the Classifier's budget.
func (c *Classifier) Classify(rel string) (Kind, string) { return c.t.classify(rel, c.m) }

// classify is Classify with matcher m, or a fresh one when m is nil (only
// made when the .gitignore rules are needed). Warnings go nowhere: the tree
// stays as Discover built it.
func (t *Tree) classify(rel string, m *matcher) (Kind, string) {
	rel = path.Clean(rel)
	if path.Base(rel) == t.IndexFile() {
		dir := path.Dir(rel)
		if dir == "." {
			return KindIndexDoc, RootUnit
		}
		if t.IsUnit(dir) {
			return KindIndexDoc, dir
		}
		return KindIgnore, ""
	}
	if strings.HasPrefix(rel, "context/") && strings.HasSuffix(rel, ".md") {
		name := strings.TrimSuffix(strings.TrimPrefix(rel, "context/"), ".md")
		if t.IsUnit(name) {
			return KindContextDoc, name
		}
		return KindIgnore, ""
	}
	if strings.HasPrefix(rel, "context/") || strings.HasPrefix(rel, ".auto-context/") {
		return KindIgnore, ""
	}
	if !t.isSource(rel) {
		return KindIgnore, ""
	}
	if m == nil {
		m = newMatcher(t.pats, false, func(string) {})
	}
	if m.untracked(rel) {
		return KindIgnore, ""
	}
	return KindCode, t.UnitOf(rel)
}

// Files returns the sorted source files of unit in the full view: tracked
// and untracked, not ignored files.
func (t *Tree) Files(unit string) []string { return slices.Clone(t.files[unit]) }

// HashFiles returns the sorted source files of unit in the index view, the
// input of source_hash: in git mode only files in git's index (untracked
// ones and local excludes do not count), when walking the same as Files.
// It is nil when unit is no unit of the index view.
func (t *Tree) HashFiles(unit string) []string {
	if _, ok := t.hashUnits[unit]; !ok {
		return nil
	}
	return append([]string{}, t.hashFiles[unit]...)
}

// HashUnits returns the paths of the index view's units, without _root,
// sorted: a directory that is a unit only through untracked files is not one.
func (t *Tree) HashUnits() []string {
	out := make([]string, 0, len(t.hashUnits))
	for p := range t.hashUnits {
		if p != RootUnit {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// Tracked returns every file Discover tracked (sources, index files,
// context docs), sorted.
func (t *Tree) Tracked() []string { return slices.Clone(t.tracked) }

// Warnings lists problems Discover worked around (e.g. a skipped .gitignore).
func (t *Tree) Warnings() []string { return slices.Clone(t.warnings) }

// WarningCount is the number of warnings, including those Warnings omits.
func (t *Tree) WarningCount() int { return t.warnCount }

// Stat returns the mtime and size Discover read for a tracked file.
func (t *Tree) Stat(rel string) (mtimeNs, size int64, ok bool) {
	s, ok := t.stats[rel]
	return s.MtimeNs, s.Size, ok
}

// StatsView returns the stat data Discover saw for every tracked file. It is
// the tree's own map, not a copy (with many files a copy is a large share
// of a hook's memory): callers must not modify it.
func (t *Tree) StatsView() map[string]filestat.Stat { return t.stats }

// maxWarnings caps Tree.warnings; a "more warnings omitted" line follows.
const maxWarnings = 20

// warn records a warning; only Discover calls it, while it builds the tree.
func (t *Tree) warn(msg string) {
	t.warnCount++
	switch {
	case len(t.warnings) < maxWarnings:
		t.warnings = append(t.warnings, msg)
	case len(t.warnings) == maxWarnings:
		t.warnings = append(t.warnings, "more warnings omitted")
	}
}

// ContextPath is the repo path of unit's context doc.
func ContextPath(unit string) string { return "context/" + unit + ".md" }

// IndexFile is the name of the per-unit index file (CLAUDE.md or AGENTS.md),
// taken from the config Discover was given.
func (t *Tree) IndexFile() string { return t.cfg.IndexFile }

// Limits on a name SafeName accepts, in bytes.
const (
	maxNameLen    = 128
	maxSegmentLen = 64
)

// SafeName reports whether a repo path may be shown as is: unit names end up
// in the root index file, in Claude's context and in suggested commands, so
// only letters, digits, marks and "._-@+/" are allowed (no spaces), no
// segment starts with "-" (it would read as an option), and the name is at
// most 128 bytes, each segment at most 64.
func SafeName(p string) bool {
	if p == RootUnit || len(p) > maxNameLen {
		return false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if len(seg) > maxSegmentLen || strings.HasPrefix(seg, "-") {
			return false
		}
	}
	for _, r := range p {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && !strings.ContainsRune("._-@+/", r) {
			return false
		}
	}
	return true
}
