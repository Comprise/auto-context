package units

import (
	"bytes"
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
	"unicode"

	"auto-context/internal/config"
	"auto-context/internal/fsutil"
	"auto-context/internal/gitx"

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

// patternSet is a compiled list of doublestar patterns. The common shapes are
// matched with string operations: a literal path, "X/**" (X and everything
// under it) and "**/X/**" (a directory named X at any depth).
type patternSet struct {
	exact    map[string]bool
	under    []string // X, with underSl holding "X/"
	underSl  []string
	anyDepth []string // X, with anySl holding "/X/"
	anySl    []string
	globs    []string
}

func compilePatterns(patterns []string) patternSet {
	s := patternSet{exact: map[string]bool{}}
	literal := func(p string) bool { return p != "" && !strings.ContainsAny(p, "*?[]{}\\") }
	for _, p := range patterns {
		if x, ok := strings.CutSuffix(p, "/**"); ok {
			if y, ok := strings.CutPrefix(x, "**/"); ok && literal(y) && !strings.Contains(y, "/") {
				s.anyDepth = append(s.anyDepth, y)
				s.anySl = append(s.anySl, "/"+y+"/")
				continue
			}
			if literal(x) {
				s.under = append(s.under, x)
				s.underSl = append(s.underSl, x+"/")
				continue
			}
		}
		if literal(p) {
			s.exact[p] = true
			continue
		}
		s.globs = append(s.globs, p)
	}
	return s
}

func (s patternSet) match(rel string) bool {
	if s.exact[rel] {
		return true
	}
	for i, x := range s.under {
		if rel == x || strings.HasPrefix(rel, s.underSl[i]) {
			return true
		}
	}
	for i, x := range s.anyDepth {
		sl := s.anySl[i] // "/X/"
		if rel == x || strings.HasPrefix(rel, sl[1:]) || strings.HasSuffix(rel, sl[:len(sl)-1]) || strings.Contains(rel, sl) {
			return true
		}
	}
	return matchesAny(s.globs, rel)
}

// gitIgnored checks rel against the .gitignore of every ancestor directory,
// each relative to its own directory. As in git, the deepest file with a
// matching rule decides, and within a file the last matching rule.
func (t *Tree) gitIgnored(rel string, isDir bool) bool {
	for i := len(rel) - 1; i >= -1; i-- {
		if i >= 0 && rel[i] != '/' {
			continue
		}
		dir := "."
		if i >= 0 {
			dir = rel[:i]
		}
		gi, ok := t.ignores[dir]
		if !ok {
			continue
		}
		ignored, matched := gi.match(rel[i+1:], isDir, &t.globBudget)
		if t.globBudget <= 0 && !t.budgetWarned {
			t.budgetWarned = true
			t.warn(".gitignore pattern rules took too long; the rest were skipped, so some paths may be tracked or ignored wrongly")
		}
		if matched {
			return ignored
		}
	}
	return false
}

// ignoreRule is one .gitignore line.
type ignoreRule struct {
	pattern  string // as written; for glob rules { and } escaped (literal in git)
	negated  bool   // "!": re-includes
	dirOnly  bool   // trailing "/"
	anchored bool   // contains "/": matched against the path, else the base name
	glob     bool   // has *, ? or [ ; else an exact name or path
	under    bool   // ends in "/**": what is inside X, not X itself
	// head and tail are literal text every match starts and ends with, mid
	// the longest literal run it contains: cheap tests before doublestar.
	head, mid, tail string
}

// ignoreFile is one parsed .gitignore. Rules that need no glob matching (an
// exact base name, or "*" plus a literal suffix) are indexed, so a long file
// of simple rules costs a map lookup and a few suffix checks per path.
type ignoreFile struct {
	rules   []ignoreRule
	exact   map[string][]int // base name -> indices of unanchored exact rules
	ext     map[string][]int // extension -> indices of unanchored "*.ext" rules
	suffix  []int            // other unanchored "*<literal>" rules
	other   []int            // everything else, matched with doublestar
	invalid int              // lines doublestar cannot parse or too long, dropped
}

// maxGlobWork bounds doublestar work per Discover (pattern × path length
// summed over calls, about a second): past it the remaining pattern rules are
// skipped, with a warning. A variable so tests can lower it.
var maxGlobWork = 300_000_000

// maxRuleLen caps a pattern rule: doublestar's cost grows with its length.
const maxRuleLen = 512

// globMeta are the characters that end a literal run in a pattern; "]"
// too, so a class body ("[cod]") is never taken for literal text.
const globMeta = "*?[]\\{}"

// parseIgnore parses .gitignore lines.
func parseIgnore(lines []string) ignoreFile {
	f := ignoreFile{exact: map[string][]int{}, ext: map[string][]int{}}
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		// Trailing spaces are dropped unless escaped.
		for strings.HasSuffix(l, " ") && !strings.HasSuffix(l, "\\ ") {
			l = l[:len(l)-1]
		}
		var r ignoreRule
		if rest, ok := strings.CutPrefix(l, "!"); ok {
			r.negated, l = true, rest
		} else if strings.HasPrefix(l, "\\!") || strings.HasPrefix(l, "\\#") {
			l = l[1:]
		}
		if rest, ok := strings.CutSuffix(l, "/"); ok {
			r.dirOnly, l = true, rest
		}
		if rest, ok := strings.CutPrefix(l, "/"); ok {
			r.anchored, l = true, rest
		}
		if rest, ok := strings.CutPrefix(l, "**/"); ok && !strings.Contains(rest, "/") {
			l, r.anchored = rest, false // "**/x" and "/**/x" are "x" at any depth
		}
		if l == "" {
			continue
		}
		r.anchored = r.anchored || strings.Contains(l, "/")
		r.glob = strings.ContainsAny(l, "*?[\\")
		r.under = strings.HasSuffix(l, "/**")
		p := l
		if r.glob {
			// Braces are literal in git but alternatives in doublestar.
			p = strings.NewReplacer("{", "\\{", "}", "\\}").Replace(l)
			if len(p) > maxRuleLen || !doublestar.ValidatePattern(p) {
				f.invalid++
				continue
			}
		}
		r.pattern = p
		if k := strings.IndexAny(p, globMeta); k >= 0 {
			r.head = p[:k]
			t := strings.LastIndexAny(p, globMeta)
			r.tail = p[t+1:]
			// "**/" may stand for no directory at all: "**/a/b" matches "a/b".
			if strings.HasSuffix(p[:t+1], "**") {
				r.tail = strings.TrimPrefix(r.tail, "/")
			}
			// A leading "/" may be the one "**/" stands for: drop it too.
			r.mid = strings.TrimPrefix(longestLiteral(p), "/")
		}
		idx := len(f.rules)
		f.rules = append(f.rules, r)
		switch {
		case !r.anchored && !r.glob:
			f.exact[l] = append(f.exact[l], idx)
		case !r.anchored && strings.HasPrefix(l, "*") && !strings.ContainsAny(l[1:], "*?[\\"):
			if e := l[1:]; strings.HasPrefix(e, ".") && strings.Count(e, ".") == 1 {
				f.ext[e] = append(f.ext[e], idx) // "*.log": looked up by extension
			} else {
				f.suffix = append(f.suffix, idx)
			}
		default:
			f.other = append(f.other, idx)
		}
	}
	return f
}

// longestLiteral returns the longest run of literal text in pattern p, outside
// character classes and escapes: every match must contain it.
func longestLiteral(p string) string {
	best, start, inClass := "", -1, false
	flush := func(end int) {
		if start >= 0 && end-start > len(best) {
			best = p[start:end]
		}
		start = -1
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			flush(i)
			inClass = true
		case c == '\\':
			flush(i)
			i++ // the escaped character ends the run too (conservative)
		case strings.IndexByte(globMeta, c) >= 0:
			flush(i)
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if !inClass {
		flush(len(p))
	}
	return best
}

// match reports whether the last rule of f matching sub (a path relative to the
// .gitignore's directory) ignores it, and whether any rule matched. budget is
// the doublestar work left (pattern length × target length per call); at zero
// the remaining glob rules are skipped.
func (f *ignoreFile) match(sub string, isDir bool, budget *int) (ignored, matched bool) {
	base := sub[strings.LastIndexByte(sub, '/')+1:]
	best := -1
	ok := func(i int) bool { return !f.rules[i].dirOnly || isDir }
	for _, i := range f.exact[base] {
		if i > best && ok(i) {
			best = i
		}
	}
	for _, i := range f.ext[path.Ext(base)] {
		if i > best && ok(i) {
			best = i
		}
	}
	for _, i := range f.suffix {
		if i > best && ok(i) && strings.HasSuffix(base, f.rules[i].pattern[1:]) {
			best = i
		}
	}
	// Later rules win: scan the glob rules from the end, down to best.
	for k := len(f.other) - 1; k >= 0 && f.other[k] > best && *budget > 0; k-- {
		i := f.other[k]
		r := &f.rules[i]
		if !ok(i) {
			continue
		}
		target := base
		if r.anchored {
			target = sub
		}
		if !r.glob {
			if target == r.pattern {
				best = i
				break
			}
			continue
		}
		if !strings.HasPrefix(target, r.head) || !strings.HasSuffix(target, r.tail) || !strings.Contains(target, r.mid) {
			continue
		}
		*budget -= len(r.pattern) * len(target)
		if !doublestar.MatchUnvalidated(r.pattern, target) {
			continue
		}
		// doublestar's "X/**" also matches X; git's only what is inside X.
		if r.under && doublestar.MatchUnvalidated(strings.TrimSuffix(r.pattern, "/**"), target) {
			continue
		}
		best = i
		break
	}
	if best < 0 {
		return false, false
	}
	return !f.rules[best].negated, true
}

// Limits on .gitignore files, so a hostile repo cannot make every hook slow:
// glob rules cost a doublestar match per path, the others a lookup.
const (
	maxGitignoreSize = 256 << 10
	maxRulesPerFile  = 5000
	maxRulesTotal    = 10000
	maxGlobRules     = 1000 // rules matched by pattern (with "/" or a wildcard beyond "*<suffix>"), in all files
)

// loadGitignore parses dir/.gitignore, if any, into t.ignores. A file that is
// not a regular in-repo file, or exceeds the limits, is skipped with a warning.
func (t *Tree) loadGitignore(root, dir string) {
	rel := path.Join(dir, ".gitignore")
	full := fsutil.Abs(root, rel)
	if _, err := os.Lstat(full); errors.Is(err, os.ErrNotExist) {
		return // the common case: no full path resolution needed
	}
	data, err := fsutil.ReadFile(root, full, maxGitignoreSize)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.warn("skipped " + shown(rel) + ": " + errKind(err))
		return
	}
	f := parseIgnore(strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n"))
	if f.invalid > 0 {
		t.warn(fmt.Sprintf("%s: %d invalid or overlong rule(s) ignored", shown(rel), f.invalid))
	}
	rules := len(f.rules)
	if rules > maxRulesPerFile || t.rules+rules > maxRulesTotal || t.globRules+len(f.other) > maxGlobRules {
		t.warn(fmt.Sprintf("skipped %s: %d rules, %d matched by pattern (limits: %d per file, %d total, %d matched by pattern)", shown(rel), rules, len(f.other), maxRulesPerFile, maxRulesTotal, maxGlobRules))
		return
	}
	t.rules += rules
	t.globRules += len(f.other)
	t.ignores[dir] = f
}

// SafeName reports whether a repo path may be shown as is: unit names end up
// in the root CLAUDE.md, in Claude's context and in suggested commands, so
// only letters, digits, marks, spaces and "._-@+/" are allowed.
func SafeName(p string) bool {
	if p == RootUnit {
		return false
	}
	for _, r := range p {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && !strings.ContainsRune("._-@+/ ", r) {
			return false
		}
	}
	return true
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
// a git work tree (root may be below its top level) it asks git: exact
// .gitignore semantics and .git/info/exclude. Outside git, or when git lists
// nothing because the root itself is ignored, it walks the tree with its own
// .gitignore matcher; if git fails it walks too, with a warning. A git
// timeout or a listing over maxList is an error: walking would be slower.
func Discover(root string, cfg config.Config) (*Tree, error) {
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
	files, ok, warning, err := gitFiles(root)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		t.warn(warning)
	}
	if ok {
		t.viaGit = true
		// The index lists files deleted from the work tree too; a directory
		// they leave behind in the index is not a directory any more.
		gone := map[string]bool{}
		var kept []string
		for _, rel := range files {
			if strings.HasPrefix(rel, ".auto-context/") || t.excludedPath(rel) {
				continue
			}
			for d := path.Dir(rel); d != "." && !isContext(d) && dirs[d] == nil && !gone[d]; d = path.Dir(d) {
				if fi, err := os.Lstat(fsutil.Abs(root, d)); err != nil || !fi.IsDir() {
					if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
						t.warn("skipped directory " + shown(d) + ": " + errKind(err))
					}
					gone[d] = true
					continue
				}
				dirs[d] = &dirInfo{}
			}
			kept = append(kept, rel)
		}
		// Lstat the files that can matter in parallel: on large trees the
		// syscalls dominate. git lists clean paths: concatenation is enough.
		stats := parallelLstat(root, kept, t.interesting)
		for i, rel := range kept {
			st := stats[i]
			t.addFile(rel, dirs, true, func() (fs.FileInfo, error) { return st.info, st.err })
		}
	} else if err := t.walk(root, dirs); err != nil {
		return nil, err
	}
	slices.Sort(t.tracked)

	t.units[RootUnit] = Unit{Path: RootUnit, Reason: ReasonRoot}
	for dir, di := range dirs {
		if dir == "." || t.excluded(dir) || !SafeName(dir) {
			continue
		}
		var reason Reason
		switch {
		case matchesAny(cfg.Include, dir):
			reason = ReasonInclude
		case di.marker:
			reason = ReasonModule
		case len(di.code) >= cfg.MinFiles:
			reason = ReasonFiles
		}
		if reason != "" {
			t.units[dir] = Unit{Path: dir, Reason: reason}
		}
	}
	for _, di := range dirs {
		for _, f := range di.code {
			u := t.UnitOf(f)
			t.files[u] = append(t.files[u], f)
		}
	}
	for u := range t.files {
		slices.Sort(t.files[u])
	}
	for k, u := range t.units {
		u.FileCount = len(t.files[k])
		t.units[k] = u
	}
	return t, nil
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

// interesting reports whether addFile looks at rel at all: a context doc, a
// CLAUDE.md, a module marker or a source.
func (t *Tree) interesting(rel string) bool {
	dir, name := path.Dir(rel), path.Base(rel)
	if isContext(dir) || name == "CLAUDE.md" {
		return true
	}
	return slices.Contains(t.cfg.ModuleMarkers, name) || t.isSource(rel)
}

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
// CLAUDE.md, module markers, sources) are checked against the ignore rules
// and stat'ed; an ignored or excluded marker makes no unit, as with git.
// checked means the caller already applied excludes and .gitignore.
func (t *Tree) addFile(rel string, dirs map[string]*dirInfo, checked bool, stat func() (fs.FileInfo, error)) {
	dir, name := path.Dir(rel), path.Base(rel)
	ctxDoc := isContext(dir)
	isDoc := name == "CLAUDE.md"
	isMarker := !ctxDoc && slices.Contains(t.cfg.ModuleMarkers, name)
	isSrc := !ctxDoc && !isDoc && t.isSource(rel)
	if !ctxDoc && !isDoc && !isMarker && !isSrc {
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
	di := dirs[dir]
	if di == nil {
		di = &dirInfo{}
		dirs[dir] = di
	}
	if isMarker {
		di.marker = true
	}
	switch {
	case isDoc:
		t.track(rel, fi)
	case isSrc:
		di.code = append(di.code, rel)
		t.track(rel, fi)
	}
}

func (t *Tree) track(rel string, fi fs.FileInfo) {
	t.tracked = append(t.tracked, rel)
	t.stats[rel] = Stat{MtimeNs: fi.ModTime().UnixNano(), Size: fi.Size()}
}

// walk lists files without git, applying the .gitignore files itself.
func (t *Tree) walk(root string, dirs map[string]*dirInfo) error {
	t.loadGitignore(root, ".")
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
			if isContext(path.Dir(rel)) {
				t.addFile(rel, dirs, false, d.Info)
			}
			return nil
		}
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if d.Name() == ".git" || rel == ".auto-context" || t.skipDir(rel) {
				return filepath.SkipDir
			}
			t.loadGitignore(root, rel)
			if !isContext(rel) && dirs[rel] == nil {
				dirs[rel] = &dirInfo{}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		t.addFile(rel, dirs, false, d.Info)
		return nil
	})
}

// maxList caps the git file listing (about 2M paths). A variable so tests
// can lower it.
var maxList = 256 << 20

// ErrGitTimeout stops discovery when git times out listing the files.
var ErrGitTimeout = errors.New("git timed out listing the files; the repository is too large or slow for auto-context (add big generated directories to .gitignore)")

// ErrListTooLarge stops discovery when git lists more than maxList: walking
// such a tree instead would take longer than the hook timeouts.
var ErrListTooLarge = errors.New("git ls-files lists more than 256 MiB of paths; the repository is too large for auto-context (add big generated directories to .gitignore)")

// gitFiles lists tracked and untracked, not ignored files under root (slash
// paths relative to root) when root is inside a git work tree. ok is false
// outside git; a warning explains a git failure where git was expected (a
// crash included). err is ErrGitTimeout or ErrListTooLarge.
func gitFiles(root string) (files []string, ok bool, warning string, err error) {
	res, gerr := gitx.Run(root, maxList, "ls-files", "-co", "--exclude-standard", "-z")
	if gerr != nil {
		_, statErr := os.Lstat(filepath.Join(root, ".git"))
		// Quiet only without a .git in root: with one, "not a git repository"
		// means a broken repo (e.g. a gitfile pointing nowhere).
		notRepo := gitx.Code(gerr) == 128 && strings.Contains(gerr.Error(), "not a git repository")
		if statErr != nil && (notRepo || gitx.NotRun(gerr)) {
			return nil, false, "", nil // not a git repo, or no git: walk quietly
		}
		var ge *gitx.Error
		if errors.As(gerr, &ge) && ge.TimedOut {
			// Walking a tree git could not list in time would miss the hook timeout too.
			return nil, false, "", ErrGitTimeout
		}
		return nil, false, "git ls-files failed (" + gitx.Brief(gerr) + "); walked the tree instead, so .git/info/exclude and global git excludes are not applied", nil
	}
	if res.Truncated {
		return nil, false, "", ErrListTooLarge
	}
	// Entry by entry, without a string copy of the whole listing.
	var prev []byte
	for out := res.Out; len(out) > 0; {
		f, rest, _ := bytes.Cut(out, []byte{0})
		out = rest
		if len(f) > 0 && !bytes.Equal(f, prev) { // unmerged entries repeat, next to each other
			files = append(files, string(f))
		}
		prev = f
	}
	if len(files) == 0 {
		// An empty listing can mean the root itself is ignored by the
		// enclosing repository: git then lists nothing under it.
		_, cerr := gitx.Run(root, 0, "check-ignore", "-q", ".")
		var ge *gitx.Error
		switch {
		case cerr == nil:
			return nil, false, "the root is ignored by the enclosing git repository; walked the tree instead", nil
		case gitx.Code(cerr) == 1: // not ignored: the repository is empty
		case errors.As(cerr, &ge) && ge.TimedOut:
			return nil, false, "", ErrGitTimeout
		default:
			// An empty list would drop every unit (and the index with it).
			return nil, false, "git check-ignore failed (" + gitx.Brief(cerr) + ") after an empty file list; walked the tree instead", nil
		}
	}
	return files, true, "", nil
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
