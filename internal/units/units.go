// Package units discovers documentation units in a repository and classifies paths.
package units

import (
	"maps"
	"path"
	"slices"
	"strings"

	"auto-context/internal/config"
)

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

type Unit struct {
	Path      string
	Reason    Reason
	FileCount int
}

type Kind int

const (
	KindIgnore     Kind = iota
	KindCode            // a source file of a unit
	KindIndexDoc        // <unit>/CLAUDE.md
	KindContextDoc      // context/<unit>.md
)

// Stat is the change-detection fingerprint of one tracked file.
type Stat struct {
	MtimeNs int64
	Size    int64
}

// Tree is the result of one Discover: units, their files and the stat data
// seen. It is not safe for concurrent use (matching .gitignore rules spends a
// shared work budget); a hook builds and uses one on a single goroutine.
type Tree struct {
	root          string // as Discover resolved it
	cfg           config.Config
	units         map[string]Unit
	files         map[string][]string   // unit -> code files (sorted)
	tracked       []string              // sorted
	ignores       map[string]ignoreFile // dir ("." for root) -> its .gitignore
	rules         int                   // .gitignore rules loaded, against maxRulesTotal
	globRules     int                   // of them with wildcards, against maxGlobRules
	globBudget    int                   // doublestar work left (maxGlobWork)
	budgetWarned  bool                  // the budget warning was given
	excludes      patternSet            // cfg.Exclude, compiled
	tracks        patternSet            // cfg.Track, compiled
	ctxCodeWarned bool                  // the context/-holds-code warning was given
	warnCount     int                   // all warnings, also those past maxWarnings
	warnings      []string
	viaGit        bool            // files were listed by git, not walked
	stats         map[string]Stat // tracked file -> stat data seen by Discover
	skipped       map[string]bool // skipDir results per directory
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

func (t *Tree) IsUnit(p string) bool {
	_, ok := t.units[p]
	return ok
}

// UnitOf returns the nearest ancestor unit of rel's directory, else _root.
func (t *Tree) UnitOf(rel string) string {
	dir := path.Dir(path.Clean(rel))
	for dir != "." && dir != "/" && dir != "" {
		if t.IsUnit(dir) {
			return dir
		}
		dir = path.Dir(dir)
	}
	return RootUnit
}

// Classify says what a repo path is to the plugin and which unit it belongs
// to: a unit's CLAUDE.md or context doc, a source of a unit, or nothing
// (KindIgnore, also for docs of directories that are no unit). It applies the
// same rules as Discover, so a file created since is classified too.
func (t *Tree) Classify(rel string) (Kind, string) {
	rel = path.Clean(rel)
	if path.Base(rel) == "CLAUDE.md" {
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
	if t.isSource(rel) && !t.untracked(rel) {
		return KindCode, t.UnitOf(rel)
	}
	return KindIgnore, ""
}

// Files returns the sorted source files of unit.
func (t *Tree) Files(unit string) []string { return slices.Clone(t.files[unit]) }

// Tracked returns every file Discover tracked (sources, CLAUDE.md files,
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

// Stats returns the stat data Discover saw for every tracked file.
func (t *Tree) Stats() map[string]Stat { return maps.Clone(t.stats) }

// maxWarnings caps Tree.warnings; a "more warnings omitted" line follows.
const maxWarnings = 20

func (t *Tree) warn(msg string) {
	t.warnCount++
	switch {
	case len(t.warnings) < maxWarnings:
		t.warnings = append(t.warnings, msg)
	case len(t.warnings) == maxWarnings:
		t.warnings = append(t.warnings, "more warnings omitted")
	}
}

func DocPaths(unit string) (claudeMD, contextMD string) {
	if unit == RootUnit {
		return "CLAUDE.md", "context/_root.md"
	}
	return unit + "/CLAUDE.md", "context/" + unit + ".md"
}
