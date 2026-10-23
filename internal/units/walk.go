package units

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/fsutil"

	"github.com/bmatcuk/doublestar/v4"
)

// isSource reports whether rel is a source file by name: it matches a track
// glob, is a module marker (go.mod, package.json…: dependencies are part of a
// unit), or has a code extension and is not Markdown.
func (t *Tree) isSource(rel string) bool {
	if t.tracks.match(rel) || slices.Contains(t.cfg.ModuleMarkers, path.Base(rel)) {
		return true
	}
	ext := path.Ext(rel)
	return ext != ".md" && slices.Contains(t.cfg.CodeExtensions, ext)
}

func (t *Tree) excluded(rel string) bool { return t.excludes.match(rel) }

// untracked reports whether rel, or a directory it is in, matches exclude or a
// .gitignore.
func (t *Tree) untracked(rel string) bool {
	return t.excludedPath(rel) || t.gitIgnored(rel, false)
}

// skipDir reports whether the walk must not enter dir at all (memoized).
func (t *Tree) skipDir(dir string) bool {
	if v, ok := t.skipped[dir]; ok {
		return v
	}
	v := t.gitIgnored(dir, true)
	for _, p := range t.cfg.Exclude {
		if v {
			break
		}
		if x, ok := strings.CutSuffix(p, "/**"); ok {
			v, _ = doublestar.Match(x, dir)
		}
	}
	t.skipped[dir] = v
	return v
}

type dirInfo struct {
	code   []string // tracked code files directly in dir
	marker bool
}

// Discover lists the repository's files once and builds the unit tree. Inside
// a git work tree of its own (root may be below its top level) it asks git:
// exact .gitignore semantics and .git/info/exclude. Outside git, when git
// lists nothing because the root itself is ignored, or when git's work tree
// or git dir lies outside the project, it walks the tree with its own
// .gitignore matcher; if git fails it walks too, with a warning. A git
// timeout or more than maxFiles files is an error: walking would be slower.
//
// The tree has two views: the full one (Units, Files, Tracked, Stats) of
// tracked and untracked files, and the index view (HashUnits, HashFiles) of
// files in git's index only. Without git they are the same.
//
// Discover is DiscoverContext without a deadline.
func Discover(root string, cfg config.Config) (*Tree, error) {
	return DiscoverContext(context.Background(), root, cfg)
}

// DiscoverContext is Discover bounded by ctx (a hook's budget, an
// interrupted CLI): an already ended ctx or one ending during the walk gives
// ErrTimeUp, one ending while git runs ErrGitTimeout.
func DiscoverContext(ctx context.Context, root string, cfg config.Config) (*Tree, error) {
	if ctx.Err() != nil {
		return nil, ErrTimeUp
	}
	cfg.Validate() // a Config not from config.Load is checked too

	t := &Tree{cfg: cfg, units: map[string]Unit{}, files: map[string][]string{}, ignores: map[string]ignoreFile{},
		stats: map[string]Stat{}, skipped: map[string]bool{},
		excludes: compilePatterns(cfg.Exclude), tracks: compilePatterns(cfg.Track), globBudget: maxGlobWork}

	// Resolve a symlinked root so WalkDir does not stop at the link itself.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t.root = root

	dirs := map[string]*dirInfo{".": {}}
	var idxDirs map[string]*dirInfo // index view, when it differs from dirs
	files, other, ok, warning, err := gitFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		t.warn(warning)
	}
	if ok {
		t.viaGit = true
		// As in the walk, a listed file counts only under real directories:
		// one replaced by a symlink (or a file, or gone) hides what git lists
		// under it, so nothing outside the repository is read.
		ex := existence{root: root, real: map[string]bool{".": true}, warned: map[string]bool{}}
		var kept []string
		var keptOther []bool
		for i, rel := range files {
			if strings.HasPrefix(rel, ".auto-context/") || t.excludedPath(rel) || !ex.realDir(t, path.Dir(rel)) {
				continue
			}
			kept = append(kept, rel)
			keptOther = append(keptOther, other[i])
		}
		if slices.Contains(keptOther, true) {
			idxDirs = map[string]*dirInfo{".": {}}
		}
		// Lstat the files that can matter in parallel: on large trees the
		// syscalls dominate. git lists clean paths: concatenation is enough.
		stats := parallelLstat(root, kept, t.interesting)
		for i, rel := range kept {
			st := stats[i]
			idx := idxDirs
			if keptOther[i] {
				idx = nil // untracked: full view only
			}
			// A directory exists only through a file of its own (or below)
			// that exists: the index lists files deleted from the work tree
			// too. A known directory has its ancestors already.
			if dir := path.Dir(rel); dir != "." && !isContext(dir) && (dirs[dir] == nil || (idx != nil && idx[dir] == nil)) && ex.exists(t, rel, st) {
				addDirs(dirs, dir)
				if idx != nil {
					addDirs(idx, dir)
				}
			}
			t.addFile(rel, dirs, idx, true, func() (fs.FileInfo, error) { return st.info, st.err })
		}
	} else if err := t.walk(ctx, root, dirs); err != nil {
		return nil, err
	}
	slices.Sort(t.tracked)

	t.units, t.files = t.view(dirs)
	t.hashUnits, t.hashFiles = t.units, t.files
	if idxDirs != nil {
		t.hashUnits, t.hashFiles = t.view(idxDirs)
	}
	return t, nil
}

// addDirs records dir and its ancestors up to a known one (or the top).
func addDirs(dirs map[string]*dirInfo, dir string) {
	for ; dir != "." && !isContext(dir) && dirs[dir] == nil; dir = path.Dir(dir) {
		dirs[dir] = &dirInfo{}
	}
}

// view qualifies the units among dirs and assigns their code files: the
// units (with _root and file counts) and unit -> sorted files.
func (t *Tree) view(dirs map[string]*dirInfo) (map[string]Unit, map[string][]string) {
	units := map[string]Unit{RootUnit: {Path: RootUnit, Reason: ReasonRoot}}
	files := map[string][]string{}
	for dir, di := range dirs {
		if dir == "." || t.excluded(dir) || !SafeName(dir) {
			continue
		}
		var reason Reason
		switch {
		case matchesAny(t.cfg.Include, dir):
			reason = ReasonInclude
		case di.marker:
			reason = ReasonModule
		case len(di.code) >= t.cfg.MinFiles:
			reason = ReasonFiles
		}
		if reason != "" {
			units[dir] = Unit{Path: dir, Reason: reason}
		}
	}
	for _, di := range dirs {
		for _, f := range di.code {
			u := unitOf(units, f)
			files[u] = append(files[u], f)
		}
	}
	for u := range files {
		slices.Sort(files[u])
	}
	for k, u := range units {
		u.FileCount = len(files[k])
		units[k] = u
	}
	return units, files
}

// existence tells, in git mode, what of a listed path is in the work tree as
// the walk would see it. Each directory is lstat'ed once.
type existence struct {
	root   string
	real   map[string]bool // directory -> it and its parents are real directories
	warned map[string]bool // directories warned about
}

// realDir reports whether dir and every directory above it is a directory,
// not a symlink, a file or missing. Errors other than "missing" warn once.
func (e *existence) realDir(t *Tree, dir string) bool {
	if v, ok := e.real[dir]; ok {
		return v
	}
	v := e.realDir(t, path.Dir(dir))
	if v {
		fi, err := os.Lstat(fsutil.Abs(e.root, dir))
		v = err == nil && fi.IsDir()
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			t.warn("skipped directory " + shown(dir) + ": " + errKind(err))
		}
	}
	e.real[dir] = v
	return v
}

// exists reports whether a listed file in a real directory is there and no
// symlink. Files parallelLstat stat'ed are known; the others are stat'ed
// here, only while their directory is unknown (the caller's check).
func (e *existence) exists(t *Tree, rel string, st lstatResult) bool {
	if !errors.Is(st.err, errNotStated) {
		return st.err == nil && st.info.mode&fs.ModeSymlink == 0
	}
	dir := path.Dir(rel)
	if e.warned[dir] {
		return false
	}
	fi, err := os.Lstat(fsutil.Abs(e.root, rel))
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
		t.warn("skipped directory " + shown(dir) + ": " + errKind(err))
		e.warned[dir] = true
	}
	return err == nil && fi.Mode()&fs.ModeSymlink == 0
}

func isContext(dir string) bool { return dir == "context" || strings.HasPrefix(dir, "context/") }

// excludedPath reports whether rel, or a directory it is in, is cut off by an
// exclude pattern of the form "X/**" (what skipDir does during the walk).
func (t *Tree) excludedPath(rel string) bool {
	if t.excluded(rel) {
		return true
	}
	for d := path.Dir(rel); d != "."; d = path.Dir(d) {
		if t.skipDir(d) {
			return true
		}
	}
	return false
}

// fileClass is what a listed file can be to discovery.
type fileClass struct {
	ctxDoc   bool // a file in the top-level context/ directory
	indexDoc bool // the index file (CLAUDE.md or the configured name)
	marker   bool // a module marker, outside context/
	source   bool // a source file, outside context/ and not an index file
}

func (c fileClass) any() bool { return c.ctxDoc || c.indexDoc || c.marker || c.source }

// classifyFile tells what rel is: a context doc, an index file, a module
// marker or a source.
func (t *Tree) classifyFile(rel string) fileClass {
	dir, name := path.Dir(rel), path.Base(rel)
	var c fileClass
	c.ctxDoc = isContext(dir)
	c.indexDoc = name == t.IndexFile()
	c.marker = !c.ctxDoc && slices.Contains(t.cfg.ModuleMarkers, name)
	c.source = !c.ctxDoc && !c.indexDoc && t.isSource(rel)
	return c
}

// interesting reports whether addFile looks at rel at all: a context doc, an
// index file, a module marker or a source.
func (t *Tree) interesting(rel string) bool { return t.classifyFile(rel).any() }

// errNotStated marks a path parallelLstat skipped.
var errNotStated = errors.New("not stated")

// statInfo is the part of an fs.FileInfo discovery uses: kept per listed
// file, a full one would cost a few hundred bytes each on large trees.
type statInfo struct {
	mode        fs.FileMode
	mtime, size int64
}

func (s statInfo) Name() string       { return "" }
func (s statInfo) Size() int64        { return s.size }
func (s statInfo) Mode() fs.FileMode  { return s.mode }
func (s statInfo) ModTime() time.Time { return time.Unix(0, s.mtime) }
func (s statInfo) IsDir() bool        { return s.mode.IsDir() }
func (s statInfo) Sys() any           { return nil }

type lstatResult struct {
	info statInfo
	err  error
}

// parallelLstat stats root/rel for every rel that want accepts, on a few
// goroutines handing out indices with a counter; the others get errNotStated.
// want must be safe for concurrent use.
func parallelLstat(root string, rels []string, want func(string) bool) []lstatResult {
	out := make([]lstatResult, len(rels))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), 8) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(rels) {
					return
				}
				if !want(rels[i]) {
					out[i].err = errNotStated // addFile never asks; fail safe if it did
					continue
				}
				fi, err := os.Lstat(root + string(filepath.Separator) + filepath.FromSlash(rels[i]))
				if err != nil {
					out[i].err = err
					continue
				}
				out[i].info = statInfo{fi.Mode(), fi.ModTime().UnixNano(), fi.Size()}
			}
		}()
	}
	wg.Wait()
	return out
}

// addFile records one listed file. Only files that can matter (context docs,
// index files, module markers, sources) are checked against the ignore rules
// and stat'ed; an ignored or excluded marker makes no unit, as with git.
// checked means the caller already applied excludes and .gitignore. idx, if
// not nil, is the index view's dirs, which get the file too.
func (t *Tree) addFile(rel string, dirs, idx map[string]*dirInfo, checked bool, stat func() (fs.FileInfo, error)) {
	dir := path.Dir(rel)
	c := t.classifyFile(rel)
	ctxDoc, isDoc, isMarker, isSrc := c.ctxDoc, c.indexDoc, c.marker, c.source
	if !c.any() {
		return
	}
	// In git mode the caller already checked excludes (git applied .gitignore);
	// the walk reaches rel only through directories it did not skip.
	if !checked && (t.excluded(rel) || t.gitIgnored(rel, false)) {
		return
	}
	fi, err := stat()
	if err != nil {
		// ENOTDIR: a listed file's directory was replaced by a file.
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			t.warn("skipped " + shown(rel) + ": " + errKind(err))
		}
		return
	}
	if ctxDoc && fi.Mode()&fs.ModeSymlink != 0 {
		// A context doc may link to another .md in the repo (status and ack
		// follow it): track the target's stat, so editing it is a docs edit.
		if p, err := fsutil.ResolveDoc(t.root, fsutil.Abs(t.root, rel)); err == nil {
			if target, err := os.Stat(p); err == nil && target.Mode().IsRegular() {
				fi = target
			}
		}
	}
	if !fi.Mode().IsRegular() {
		return // a symlink or a submodule
	}
	if ctxDoc {
		if ext := path.Ext(rel); ext != ".md" && slices.Contains(t.cfg.CodeExtensions, ext) && !t.ctxCodeWarned {
			t.ctxCodeWarned = true
			t.warn("context/ holds code files (e.g. " + shown(rel) + "); they belong to no unit: the top-level context/ directory is reserved for unit docs")
		}
		t.track(rel, fi)
		return
	}
	note(dirs, dir, isMarker, isSrc, rel)
	if idx != nil {
		note(idx, dir, isMarker, isSrc, rel)
	}
	if isDoc || isSrc {
		t.track(rel, fi)
	}
}

// note records in dirs that dir exists and holds a marker or the source rel.
func note(dirs map[string]*dirInfo, dir string, marker, src bool, rel string) {
	di := dirs[dir]
	if di == nil {
		di = &dirInfo{}
		dirs[dir] = di
	}
	if marker {
		di.marker = true
	}
	if src {
		di.code = append(di.code, rel)
	}
}

func (t *Tree) track(rel string, fi fs.FileInfo) {
	t.tracked = append(t.tracked, rel)
	t.stats[rel] = Stat{MtimeNs: fi.ModTime().UnixNano(), Size: fi.Size()}
}

// walkCheckEvery is how many entries the walk visits between checks of its
// context.
const walkCheckEvery = 256

// walk lists files without git, applying the .gitignore files itself. The
// files that can matter are lstat'ed in parallel after the walk, as in git
// mode.
func (t *Tree) walk(ctx context.Context, root string, dirs map[string]*dirInfo) error {
	t.loadGitignore(root, ".")
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	var cands []string // interesting files, neither excluded nor ignored
	n := 0             // regular files seen, against maxFiles
	seen := 0          // entries seen, for the ctx check
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if seen++; seen%walkCheckEvery == 0 && ctx.Err() != nil {
			return ErrTimeUp
		}
		// WalkDir yields root and paths under it: cutting the prefix is Rel.
		rel := "."
		if p != root {
			var ok bool
			if rel, ok = strings.CutPrefix(p, prefix); !ok {
				return fmt.Errorf("walk: %s is not under %s", p, root)
			}
		}
		rel = filepath.ToSlash(rel)
		if err != nil {
			if p == root {
				return err
			}
			t.warn("skipped " + shown(rel) + ": " + errKind(err))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil // unreadable entry: skip it, keep discovering
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// Symlinks are skipped, except a context doc, which may stand for a
			// doc elsewhere in the repo (addFile checks where it leads).
			if isContext(path.Dir(rel)) && t.interesting(rel) && !t.excluded(rel) && !t.gitIgnored(rel, false) {
				cands = append(cands, rel)
			}
			return nil
		}
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if d.Name() == ".git" || rel == ".auto-context" || t.skipDir(rel) || nestedRepo(p) {
				return filepath.SkipDir
			}
			t.loadGitignore(root, rel)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if n++; n > maxFiles {
			return ErrListTooLarge
		}
		// As in git mode, a directory exists only through a file of its own
		// (or below) that is neither excluded nor ignored. A known directory
		// has its ancestors already: .gitignore is checked once per directory.
		dir := path.Dir(rel)
		want := t.interesting(rel)
		if (!want && dirs[dir] != nil) || t.excluded(rel) || t.gitIgnored(rel, false) {
			return nil
		}
		for ; dir != "." && !isContext(dir) && dirs[dir] == nil; dir = path.Dir(dir) {
			dirs[dir] = &dirInfo{}
		}
		if want {
			cands = append(cands, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	stats := parallelLstat(root, cands, func(string) bool { return true })
	for i, rel := range cands {
		st := stats[i]
		t.addFile(rel, dirs, nil, true, func() (fs.FileInfo, error) { return st.info, st.err })
	}
	return nil
}

// nestedRepo reports whether dir holds a .git (a directory, or the file of a
// worktree or submodule): git does not list what is in such a directory.
func nestedRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

func matchesAny(patterns []string, p string) bool {
	for _, pat := range patterns {
		if ok, _ := doublestar.Match(pat, p); ok {
			return true
		}
	}
	return false
}

// shown is a repo path as a warning may name it: warnings reach Claude's
// context through `auto-context status`, so only names SafeName accepts.
func shown(p string) string {
	if SafeName(p) {
		return p
	}
	return "a path with unusual characters"
}

// errKind names a file error with a fixed category: its text holds the path.
func errKind(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, fsutil.ErrOutside):
		return "leads outside the repository or into .git"
	case errors.Is(err, fsutil.ErrNotRegular):
		return "not a regular file"
	case errors.Is(err, fsutil.ErrTooLarge):
		return "too large"
	}
	return "unreadable"
}
