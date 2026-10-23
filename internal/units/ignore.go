package units

import (
	"errors"
	"fmt"
	"iter"
	"os"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"auto-context/internal/fsutil"

	"github.com/bmatcuk/doublestar/v4"
)

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
	literal := func(p string) bool { return p != "" && !strings.ContainsAny(p, globMeta) }
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
	under    bool   // ends in "/**": what is inside X, not X itself (matchInside)
	// head and tail are literal text every match starts and ends with, mid
	// the longest literal run it contains: cheap tests before doublestar.
	head, mid, tail string
	suffix          string // the literal after "*" of a suffix rule, unescaped
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
func parseIgnore(lines iter.Seq[string]) ignoreFile {
	f := ignoreFile{exact: map[string][]int{}, ext: map[string][]int{}}
	for l := range lines {
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
		// "\/" is "/" to git; a rule that then starts with "/" (like "//x")
		// or holds "//" asks for a path no clean path is: git never matches it.
		if l = normalizeRule(l); strings.HasPrefix(l, "/") || emptySegment(l) {
			continue
		}
		if rest, ok := strings.CutPrefix(l, "**/"); ok && !strings.Contains(rest, "/") {
			l, r.anchored = rest, false // "**/x" and "/**/x" are "x" at any depth
		}
		// Only one trailing "/" is cut: a rule still ending in "/" asks for a
		// path ending in "/", which git never matches.
		if l == "" || strings.HasSuffix(l, "/") {
			continue
		}
		r.anchored = r.anchored || strings.Contains(l, "/")
		r.glob = strings.ContainsAny(l, "*?[\\")
		r.under = strings.HasSuffix(l, "/**")
		p := l
		if r.glob {
			var ok bool
			if p, ok = globPattern(l); !ok {
				continue // a class with no member: git matches nothing either
			}
			if len(p) > maxRuleLen || !doublestar.ValidatePattern(p) {
				f.invalid++
				continue
			}
		}
		r.pattern = p
		r.suffix = l[1:]
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

// normalizeRule rewrites a rule (its leading "/" and one trailing "/" cut)
// as git's wildmatch reads it: an escaped "/" is a "/"; a run of unescaped
// "*" is "**" between slashes (or the ends of the rule) when it is two or
// more, any other run is "*"; and "**" segments in a row are one.
func normalizeRule(l string) string {
	if !strings.Contains(l, "**") && !strings.Contains(l, "\\/") {
		return l
	}
	var b strings.Builder
	for i := 0; i < len(l); i++ {
		c := l[i]
		if c == '\\' {
			if i+1 < len(l) && l[i+1] == '/' {
				continue // the "/" is written next
			}
			b.WriteByte(c)
			if i+1 < len(l) {
				i++
				b.WriteByte(l[i])
			}
			continue
		}
		if c != '*' {
			b.WriteByte(c)
			continue
		}
		j := i + 1
		for j < len(l) && l[j] == '*' {
			j++
		}
		switch {
		case j-i < 2 || (i > 0 && l[i-1] != '/'):
			b.WriteByte('*')
		case j == len(l) || l[j] == '/':
			b.WriteString("**")
		case strings.HasPrefix(l[j:], "\\/"):
			// Before an escaped "/", "**" may not stand for no directory, as
			// it may before a plain one: at least one.
			b.WriteString("*/**")
		default:
			b.WriteByte('*')
		}
		i = j - 1
	}
	// "**/**" is "**": merged, so "X/**" rules never have an X that can match
	// nothing (see ignoreRule.under).
	segs := strings.Split(b.String(), "/")
	segs = slices.CompactFunc(segs, func(a, b string) bool { return a == "**" && b == "**" })
	return strings.Join(segs, "/")
}

// emptySegment reports whether rule l holds "//" outside a bracket class.
func emptySegment(l string) bool {
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case '\\':
			i++
		case '[':
			if _, n, _ := bracketClass(l[i+1:]); n > 0 {
				i += n
			}
		case '/':
			if i+1 < len(l) && l[i+1] == '/' {
				return true
			}
		}
	}
	return false
}

// posixClasses are the ASCII ranges of the [:name:] classes git's wildmatch
// knows (its ctype is ASCII only).
var posixClasses = map[string][][2]rune{
	"alnum":  {{'0', '9'}, {'A', 'Z'}, {'a', 'z'}},
	"alpha":  {{'A', 'Z'}, {'a', 'z'}},
	"blank":  {{'\t', '\t'}, {' ', ' '}},
	"cntrl":  {{0, 0x1f}, {0x7f, 0x7f}},
	"digit":  {{'0', '9'}},
	"graph":  {{'!', '~'}},
	"lower":  {{'a', 'z'}},
	"print":  {{' ', '~'}},
	"punct":  {{'!', '/'}, {':', '@'}, {'[', '`'}, {'{', '~'}},
	"space":  {{'\t', '\n'}, {'\r', '\r'}, {' ', ' '}},
	"upper":  {{'A', 'Z'}},
	"xdigit": {{'0', '9'}, {'A', 'F'}, {'a', 'f'}},
}

// globPattern turns a git glob rule into a doublestar pattern: braces, literal
// in git, are escaped, and bracket classes are rewritten member by member as
// git's wildmatch reads them. Other bytes, not UTF-8 ones included (a Latin-1
// .gitignore), are kept as written. ok is false when a class has no member
// left, so the rule matches nothing.
func globPattern(l string) (p string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(l); i++ {
		switch c := l[i]; c {
		case '\\':
			b.WriteByte(c)
			if i+1 < len(l) {
				i++
				b.WriteByte(l[i])
			}
		case '{', '}':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '[':
			class, n, ok := bracketClass(l[i+1:])
			if n < 0 { // unterminated: left as written
				b.WriteByte(c)
				continue
			}
			if !ok {
				return "", false
			}
			b.WriteString(class)
			i += n
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// bracketClass rewrites the class whose body starts s (just after "[") the
// way git reads it: "!" or "^" negates; the first member may be "]"; a
// backslash escapes a member; "-" between members makes a range, which holds
// its first end even when reversed; and a class never matches "/" (git
// matches with WM_PATHNAME), so "/" is dropped from the members, or added to
// them when negated. A member is a UTF-8 character or, where the bytes are
// not UTF-8, one byte, written back as it was; "[:name:]" is a POSIX class,
// turned into its ASCII ranges ("[:" without ":]" is a "[" member, an unknown
// name makes the rule match nothing, as in git). n is the bytes consumed
// through the closing "]", or -1 for a class left to doublestar as written
// (unterminated). ok is false when no member is left.
func bracketClass(s string) (class string, n int, ok bool) {
	type end struct {
		v   rune   // for ordering: the character, or the byte value
		raw string // as written, without the escape
	}
	type span struct{ lo, hi end }
	var spans []span
	i, negated := 0, false
	if i < len(s) && (s[i] == '!' || s[i] == '^') {
		i, negated = i+1, true
	}
	member := func() (end, bool) {
		if i < len(s) && s[i] == '\\' {
			i++
		}
		if i >= len(s) {
			return end{}, false
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			r = rune(s[i])
		}
		e := end{r, s[i : i+size]}
		i += size
		return e, true
	}
	for first := true; ; first = false {
		if i >= len(s) {
			return "", -1, false
		}
		if s[i] == ']' && !first {
			break
		}
		if s[i] == '[' && i+1 < len(s) && s[i+1] == ':' {
			// As git: the name runs to the next "]", which must follow a ":".
			j := strings.IndexByte(s[i+2:], ']')
			if j < 0 {
				return "", -1, false
			}
			if j > 0 && s[i+2+j-1] == ':' {
				ranges, known := posixClasses[s[i+2:i+2+j-1]]
				if !known {
					return "", 0, false
				}
				for _, r := range ranges {
					spans = append(spans, span{end{r[0], string(r[0])}, end{r[1], string(r[1])}})
				}
				i += 2 + j + 1
				continue // a "-" after a class is a member, not a range
			}
		}
		lo, ok := member()
		if !ok {
			return "", -1, false
		}
		spans = append(spans, span{lo, lo})
		if i+1 < len(s) && s[i] == '-' && s[i+1] != ']' {
			i++
			hi, ok := member()
			if !ok {
				return "", -1, false
			}
			spans = append(spans, span{lo, hi})
		}
	}
	var b strings.Builder
	b.WriteByte('[')
	if negated {
		b.WriteByte('!')
	}
	members := 0
	write := func(e end) {
		if len(e.raw) == 1 && strings.IndexByte("\\[]-!^{}", e.raw[0]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteString(e.raw)
	}
	add := func(lo, hi end) {
		if lo.v > hi.v {
			return
		}
		members++
		write(lo)
		if hi.v != lo.v {
			b.WriteByte('-')
			write(hi)
		}
	}
	dot, zero := end{'.', "."}, end{'0', "0"}
	for _, sp := range spans {
		if sp.lo.v <= '/' && '/' <= sp.hi.v {
			add(sp.lo, dot)
			add(zero, sp.hi)
		} else {
			add(sp.lo, sp.hi)
		}
	}
	if negated {
		b.WriteString("\\/")
		members++
	}
	b.WriteByte(']')
	return b.String(), i + 1, members > 0
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
			if c == '\\' {
				i++ // an escaped member, "]" included
			} else if c == ']' {
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
		if i > best && ok(i) && strings.HasSuffix(base, f.rules[i].suffix) {
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
		if r.under {
			if !matchInside(strings.TrimSuffix(r.pattern, "/**"), target, budget) {
				continue
			}
		} else if !doublestar.MatchUnvalidated(r.pattern, target) {
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

// matchInside reports whether target is inside a directory matching x, as
// git reads "x/**": doublestar's "x/**" also matches x itself, and testing
// x against target on its own is wrong when x matches both a path and a
// directory above it ("**/*/**" and "a/a").
func matchInside(x, target string, budget *int) bool {
	for k := strings.IndexByte(target, '/'); k >= 0; k = nextSlash(target, k) {
		*budget -= len(x) * k
		if doublestar.MatchUnvalidated(x, target[:k]) {
			return true
		}
	}
	return false
}

func nextSlash(s string, k int) int {
	if j := strings.IndexByte(s[k+1:], '/'); j >= 0 {
		return k + 1 + j
	}
	return -1
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
	f := parseIgnore(strings.SplitSeq(strings.TrimPrefix(string(data), "\ufeff"), "\n"))
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
