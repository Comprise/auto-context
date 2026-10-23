// Package config loads, validates and updates the optional
// .auto-context/config.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"auto-context/internal/fsutil"

	"github.com/bmatcuk/doublestar/v4"
	yaml "go.yaml.in/yaml/v3"
)

// FileName is the config file, relative to the root. It lives in the
// plugin's directory and is committed; .auto-context/.gitignore keeps the
// rest of that directory (local state) out of git.
const FileName = ".auto-context/config.yaml"

// The two names a unit's index file may have: CLAUDE.md for Claude Code,
// AGENTS.md for Codex CLI.
const (
	IndexClaude = "CLAUDE.md"
	IndexAgents = "AGENTS.md"
)

// Config holds the settings of .auto-context/config.yaml.
type Config struct {
	MinFiles       int      `yaml:"min_files"`
	CodeExtensions []string `yaml:"code_extensions"`
	ModuleMarkers  []string `yaml:"module_markers"`
	Include        []string `yaml:"include"`
	Exclude        []string `yaml:"exclude"`
	// Track lists globs of files that are not code (by extension) but still
	// count as their unit's sources, e.g. workflows or a shell shim.
	Track            []string `yaml:"track"`
	MaxSessionNotice int      `yaml:"max_session_notice"`
	// Language is the language code unit docs are written in (e.g. "ru",
	// "pt-BR"); empty means the language of the existing docs.
	Language string `yaml:"language"`
	// IndexFile is the name of the per-unit index file: CLAUDE.md (default)
	// or AGENTS.md. Any other value breaks the config.
	IndexFile string `yaml:"index_file"`
	// DiscoveryReset is set when Load or Validate dropped or reset a setting
	// that decides the units (include, exclude, track, min_files,
	// code_extensions, module_markers): the units differ from what the file
	// asks for.
	DiscoveryReset bool `yaml:"-"`
}

// Default returns the configuration used when no config file overrides it.
func Default() Config {
	return Config{
		MinFiles: 5,
		CodeExtensions: []string{
			".go", ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".svelte",
			".py", ".rs", ".java", ".kt", ".scala", ".swift", ".dart", ".rb", ".php", ".cs", ".fs",
			".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".m", ".mm",
			".ex", ".exs", ".erl", ".hs", ".clj", ".lua", ".zig",
			".sh", ".sql", ".proto", ".graphql", ".tf",
		},
		ModuleMarkers:    []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "pom.xml", "build.gradle"},
		Include:          []string{},
		Exclude:          []string{"vendor/**", "node_modules/**", "**/testdata/**", "dist/**", "build/**"},
		Track:            []string{},
		MaxSessionNotice: 10,
		IndexFile:        IndexClaude,
	}
}

// ErrBadIndexFile is what Load returns for an index_file other than the two
// allowed names.
var ErrBadIndexFile = errors.New("index_file must be " + IndexClaude + " or " + IndexAgents)

// MaxSize caps the size of the config file we read.
const MaxSize = 1 << 20

// Limits that keep a hostile or mistyped config from hanging the hooks:
// doublestar expands every {a,b} group recursively, so stacked groups cost
// exponential time per path.
const (
	maxPatternLen    = 256
	maxBraceGroups   = 8
	maxSessionNotice = 100
)

// Problems lists settings of the config file that were ignored. Load returns
// it alongside a config that still applies every valid setting.
type Problems []string

func (p Problems) Error() string { return FileName + ": ignored " + strings.Join(p, "; ") }

var knownKeys = map[string]bool{
	"min_files": true, "code_extensions": true, "module_markers": true,
	"include": true, "exclude": true, "track": true, "max_session_notice": true,
	"language": true, "index_file": true,
}

// discoveryKeys are the settings that decide the units: dropping or resetting
// one of them sets Config.DiscoveryReset.
var discoveryKeys = map[string]bool{
	"min_files": true, "code_extensions": true, "module_markers": true,
	"include": true, "exclude": true, "track": true,
}

// languageRe accepts a BCP 47-style code only: the value is shown to Claude,
// so free text from a cloned repo must not pass.
var languageRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}$`)

// ValidLanguage reports whether s is an acceptable docs language code.
func ValidLanguage(s string) bool { return languageRe.MatchString(s) }

// Load reads root/.auto-context/config.yaml; absent (or null) fields keep
// their defaults. A bad setting (unknown or repeated key, wrong type, invalid
// value or pattern) is dropped and listed in Problems while the rest applies;
// a file that cannot be read or parsed gives Default() and the error.
func Load(root string) (Config, Problems, error) {
	data, err := fsutil.ReadFile(root, fsutil.Abs(root, FileName), MaxSize)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil, nil
	}
	if err != nil {
		return Default(), nil, err
	}
	return parse(data)
}

// parse is Load on the file's contents. The text is parsed once, into a
// yaml.Node; only the first document counts.
func parse(data []byte) (Config, Problems, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Default(), nil, err
	}
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return Default(), nil, nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return Default(), Problems{fmt.Sprintf("line %d: the top level is not a mapping", m.Line)}, nil
	}
	cfg := Default()
	pairs, pr, err := cfg.topKeys(m)
	if err != nil {
		return Default(), nil, err
	}
	typePr, err := cfg.decode(pairs)
	if err != nil {
		return Default(), nil, err
	}
	pr = append(pr, typePr...)
	if cfg.IndexFile != "" && cfg.IndexFile != IndexClaude && cfg.IndexFile != IndexAgents {
		// Which file holds the index decides where every doc lives: no guessing.
		return Default(), nil, ErrBadIndexFile
	}
	pr = append(pr, cfg.fix()...)
	if len(pr) == 0 {
		pr = nil
	}
	return cfg, pr, nil
}

// topKeys returns the pairs of the top-level mapping m to decode: the first
// occurrence of each known key, and every merge key (<<). Unknown keys (also
// those of merged mappings) and repeated keys are Problems, a repeated key
// keeps its first value; a repeated or non-string index_file is
// ErrBadIndexFile.
func (c *Config) topKeys(m *yaml.Node) ([]*yaml.Node, Problems, error) {
	var pairs []*yaml.Node
	var unknown, pr Problems
	seen := map[string]bool{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if isMerge(k) {
			for _, mm := range mergedMaps(v) {
				for j := 0; j+1 < len(mm.Content); j += 2 {
					name := keyName(mm.Content[j])
					switch {
					case isMerge(mm.Content[j]):
					case !knownKeys[name]:
						unknown = append(unknown, "unknown key "+safeKey(name))
					case name == "index_file" && !stringOrNull(mm.Content[j+1]):
						return nil, nil, ErrBadIndexFile
					}
				}
			}
			pairs = append(pairs, k, v)
			continue
		}
		name := keyName(k)
		switch {
		case !knownKeys[name]:
			unknown = append(unknown, "unknown key "+safeKey(name))
		case seen[name]:
			if name == "index_file" {
				return nil, nil, ErrBadIndexFile
			}
			pr = append(pr, fmt.Sprintf("line %d: duplicate key %s", k.Line, name))
			c.DiscoveryReset = c.DiscoveryReset || discoveryKeys[name]
		case name == "index_file" && !stringOrNull(v):
			return nil, nil, ErrBadIndexFile
		default:
			seen[name] = true
			pairs = append(pairs, k, v)
		}
	}
	slices.Sort(unknown)
	return pairs, append(slices.Compact(unknown), pr...), nil
}

// decode decodes pairs over c as one mapping, so merge keys behave as yaml.v3
// defines (explicit keys win over merged ones). A value of the wrong type is
// a Problem; yaml.v3 leaves its field half-decoded (a list keeps the elements
// that decoded: `exclude: [{a: b}]` gave an empty list), so each pair that
// fails on its own resets its field to the default.
func (c *Config) decode(pairs []*yaml.Node) (Problems, error) {
	err := mapping(pairs...).Decode(c)
	var te *yaml.TypeError
	if err == nil || !errors.As(err, &te) {
		return nil, err
	}
	explicit := map[string]bool{}
	for i := 0; i < len(pairs); i += 2 {
		if !isMerge(pairs[i]) {
			explicit[keyName(pairs[i])] = true
		}
	}
	bad := func(k, v *yaml.Node) bool {
		var probe Config
		return mapping(k, v).Decode(&probe) != nil
	}
	def := Default()
	var pr Problems
	for i := 0; i+1 < len(pairs); i += 2 {
		k, v := pairs[i], pairs[i+1]
		if !bad(k, v) {
			continue
		}
		if !isMerge(k) {
			name := keyName(k)
			c.reset(&def, name)
			pr = append(pr, fmt.Sprintf("line %d: %s has a value of the wrong type", k.Line, name))
			continue
		}
		found := false
		for _, mm := range mergedMaps(v) {
			for j := 0; j+1 < len(mm.Content); j += 2 {
				mk, mv := mm.Content[j], mm.Content[j+1]
				name := keyName(mk)
				if isMerge(mk) || !knownKeys[name] || !bad(mk, mv) {
					continue
				}
				found = true
				pr = append(pr, fmt.Sprintf("line %d: %s has a value of the wrong type", mk.Line, name))
				if !explicit[name] { // an explicit key was decoded instead
					c.reset(&def, name)
				}
			}
		}
		if !found {
			// Something below the merge key failed, not one setting.
			pr = append(pr, fmt.Sprintf("line %d: << has a value of the wrong type", k.Line))
			c.DiscoveryReset = true
		}
	}
	return pr, nil
}

// reset sets the field of c whose yaml key is key to its value in def;
// other keys change nothing.
func (c *Config) reset(def *Config, key string) {
	cv, dv := reflect.ValueOf(c).Elem(), reflect.ValueOf(def).Elem()
	for i := range cv.NumField() {
		if cv.Type().Field(i).Tag.Get("yaml") == key {
			cv.Field(i).Set(dv.Field(i))
			c.DiscoveryReset = c.DiscoveryReset || discoveryKeys[key]
		}
	}
}

// mapping is a mapping node of the given key/value pairs.
func mapping(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: pairs}
}

// resolve follows an alias to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	for i := 0; i < 8 && n.Kind == yaml.AliasNode && n.Alias != nil; i++ {
		n = n.Alias
	}
	return n
}

func isMerge(k *yaml.Node) bool { return k.Kind == yaml.ScalarNode && k.Tag == "!!merge" }

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

// keyName is the text of a mapping key, "" for a key that is not a scalar.
func keyName(k *yaml.Node) string {
	if k = resolve(k); k.Kind == yaml.ScalarNode {
		return k.Value
	}
	return ""
}

func stringOrNull(v *yaml.Node) bool {
	v = resolve(v)
	return v.Kind == yaml.ScalarNode && (v.Tag == "!!str" || v.Tag == "!!null")
}

// mergedMaps lists the mappings a merge key's value brings in: a mapping or
// a list of them, possibly through aliases.
func mergedMaps(v *yaml.Node) []*yaml.Node {
	v = resolve(v)
	switch v.Kind {
	case yaml.MappingNode:
		return []*yaml.Node{v}
	case yaml.SequenceNode:
		var out []*yaml.Node
		for _, e := range v.Content {
			if e = resolve(e); e.Kind == yaml.MappingNode {
				out = append(out, e)
			}
		}
		return out
	}
	return nil
}

// Validate applies the same checks as Load to a Config built in code:
// invalid values are replaced and returned as Problems.
func (c *Config) Validate() Problems { return c.fix() }

// safeKey is a config key as a problem may name it: unknown keys come from
// the file, so only short lowercase words are shown.
func safeKey(k string) string {
	if len(k) <= 32 && strings.Trim(k, "abcdefghijklmnopqrstuvwxyz_") == "" && k != "" {
		return k
	}
	return "(name not shown)"
}

// Describe names why the config could not be loaded without quoting the
// file: "a YAML syntax error at line N", or a fixed file-error category.
func Describe(err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "yaml: line "); ok {
		if n, _, ok := strings.Cut(rest, ":"); ok && n != "" && strings.Trim(n, "0123456789") == "" {
			return "a YAML syntax error at line " + n
		}
	}
	switch {
	case errors.Is(err, ErrBadIndexFile):
		return ErrBadIndexFile.Error()
	case strings.HasPrefix(msg, "yaml: "):
		return "a YAML syntax error"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, fsutil.ErrOutside):
		return "it leads outside the repository or into .git"
	case errors.Is(err, fsutil.ErrNotRegular):
		return "not a regular file"
	case errors.Is(err, fsutil.ErrTooLarge):
		return "larger than 1 MiB"
	}
	return "unreadable"
}

// check reports whether a list entry is valid and, if not, why. The reason
// is fixed text that never quotes the entry: problems reach Claude's context.
type check func(string) (reason string, ok bool)

func patternCheck(p string) (string, bool) { return "invalid pattern", ValidatePattern(p) == nil }

// fix replaces null lists by their defaults and invalid values by valid ones,
// returning what it changed.
func (c *Config) fix() Problems {
	def := Default()
	var pr Problems
	if c.MinFiles < 1 {
		pr = append(pr, fmt.Sprintf("min_files %d (must be at least 1)", c.MinFiles))
		c.MinFiles = def.MinFiles
		c.DiscoveryReset = true
	}
	if c.MaxSessionNotice < 0 || c.MaxSessionNotice > maxSessionNotice {
		pr = append(pr, fmt.Sprintf("max_session_notice %d (must be 0..%d)", c.MaxSessionNotice, maxSessionNotice))
		c.MaxSessionNotice = def.MaxSessionNotice
	}
	if c.IndexFile == "" {
		c.IndexFile = def.IndexFile
	}
	if c.Language != "" && !ValidLanguage(c.Language) {
		pr = append(pr, "language (must be a language code such as ru, en or pt-BR)")
		c.Language = def.Language
	}
	lists := []struct {
		name     string
		got, def *[]string
		check    check
	}{
		{"code_extensions", &c.CodeExtensions, &def.CodeExtensions, func(e string) (string, bool) {
			return "must start with a dot", len(e) >= 2 && e[0] == '.'
		}},
		{"module_markers", &c.ModuleMarkers, &def.ModuleMarkers, func(m string) (string, bool) {
			return "must be a file name", m != "" && !strings.ContainsAny(m, "/\\")
		}},
		{"include", &c.Include, &def.Include, patternCheck},
		{"exclude", &c.Exclude, &def.Exclude, patternCheck},
		{"track", &c.Track, &def.Track, patternCheck},
	}
	for _, l := range lists {
		if *l.got == nil {
			*l.got = *l.def
			continue
		}
		var keep []string
		bad := false
		for i, v := range *l.got {
			if reason, ok := l.check(v); !ok {
				pr = append(pr, fmt.Sprintf("%s entry %d (%s)", l.name, i+1, reason))
				bad = true
				continue
			}
			keep = append(keep, v)
		}
		if bad && len(keep) == 0 {
			keep = *l.def // nothing valid left: fall back rather than to "nothing"
		}
		if bad {
			*l.got = keep
			c.DiscoveryReset = true
		}
	}
	return pr
}

// ValidatePattern checks an include/exclude/track glob: valid doublestar syntax,
// bounded length and brace groups. Its error quotes the pattern: for the CLI,
// not for Problems.
func ValidatePattern(p string) error {
	switch {
	case len(p) > maxPatternLen:
		return fmt.Errorf("pattern longer than %d bytes", maxPatternLen)
	case strings.Count(p, "{") > maxBraceGroups:
		return fmt.Errorf("pattern %q (more than %d {} groups)", p, maxBraceGroups)
	case !doublestar.ValidatePattern(p):
		return fmt.Errorf("invalid pattern %q", p)
	}
	return nil
}

// EscapePattern returns a doublestar pattern that matches dir literally:
// \ * ? [ ] { } are escaped with a backslash.
func EscapePattern(dir string) string {
	var b strings.Builder
	for _, r := range dir {
		if strings.ContainsRune(`\*?[]{}`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// AddExclude appends pattern to exclude (no duplicates), creating the file if
// needed. Only the exclude key is edited; comments and other keys are kept.
// A config that cannot be loaded is an error naming the file and a fixed
// reason (Describe).
func AddExclude(root, pattern string) error {
	cfg, _, err := Load(root)
	if err != nil {
		return unusable(err)
	}
	if slices.Contains(cfg.Exclude, pattern) {
		return nil
	}
	path := fsutil.Abs(root, FileName)
	data, err := fsutil.ReadFile(root, path, MaxSize)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return unusable(err)
	}
	out, err := withExclude(data, cfg.Exclude, pattern)
	if err != nil {
		return err
	}
	// The plugin's directory and its .gitignore, which lets config.yaml be
	// committed, may not exist yet.
	if err := fsutil.EnsureStateDir(root); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	return fsutil.WriteAtomic(root, path, out, perm)
}

// unusable is the error for a config file AddExclude cannot edit.
func unusable(err error) error { return fmt.Errorf("%s cannot be used: %s", FileName, Describe(err)) }

// withExclude returns data with pattern added to its exclude list, whose
// effective value (what Load made of it) is effective. The yaml.Node edit
// decides the result; when a plain text edit (one line into the list, or the
// key appended at the end) loads the same, that text is used, so blank lines,
// directives and indentation stay as written. Otherwise the re-encoded file
// gets back its directives and the blank lines between top-level keys.
func withExclude(data []byte, effective []string, pattern string) ([]byte, error) {
	docs, err := decodeDocs(data)
	if err != nil {
		return nil, unusable(err)
	}
	text, textOK := textEdit(data, docs, effective, pattern) // before docs change
	encoded, err := nodeEdit(data, docs, effective, pattern)
	if err != nil {
		return nil, err
	}
	if textOK && sameConfig(text, encoded) {
		return text, nil
	}
	if kept := keepLayout(data, encoded); sameConfig(kept, encoded) {
		return kept, nil
	}
	return encoded, nil
}

// decodeDocs decodes every YAML document of data: Load reads only the first,
// AddExclude keeps the rest as they are.
func decodeDocs(data []byte) ([]*yaml.Node, error) {
	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var d yaml.Node
		if err := dec.Decode(&d); errors.Is(err, io.EOF) {
			return docs, nil
		} else if err != nil {
			return nil, err
		}
		docs = append(docs, &d)
	}
}

// sameConfig reports whether two config texts load without error into the
// same Config and have as many documents.
func sameConfig(a, b []byte) bool {
	ca, _, errA := parse(a)
	cb, _, errB := parse(b)
	da, errDA := decodeDocs(a)
	db, errDB := decodeDocs(b)
	return errA == nil && errB == nil && errDA == nil && errDB == nil &&
		len(da) == len(db) && reflect.DeepEqual(ca, cb)
}

// nodeEdit adds pattern to the exclude key of the first document and
// re-encodes all of them.
func nodeEdit(data []byte, docs []*yaml.Node, effective []string, pattern string) ([]byte, error) {
	var prefix []byte
	if len(docs) == 0 {
		// Empty or comment-only file: the decoder drops comments, keep the bytes.
		if prefix = bytes.TrimRight(data, " \t\r\n"); len(prefix) > 0 {
			prefix = append(prefix, '\n')
		}
		docs = []*yaml.Node{{Kind: yaml.DocumentNode}}
	}
	doc := docs[0]
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		doc.Content = []*yaml.Node{mapping()}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level is not a mapping", FileName)
	}
	var list yaml.Node
	if err := list.Encode(append(slices.Clone(effective), pattern)); err != nil {
		return nil, err
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "exclude"}
	setExclude(doc, m, key, &list, pattern)
	buf := bytes.NewBuffer(prefix)
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// setExclude edits the first exclude key of m (the one Load reads), or adds
// key: list. A list Load can use gets pattern appended; any other value
// (not a list, an alias, or a list with no valid entry, where Load falls back
// to the defaults) is replaced by list, the effective list plus pattern. A
// key given through an alias becomes a plain key, and aliases of anchors in
// the old value become copies of it, so no other key changes.
func setExclude(doc, m, key, list *yaml.Node, pattern string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if isMerge(k) || keyName(k) != "exclude" {
			continue
		}
		if k.Kind == yaml.AliasNode {
			m.Content[i] = key
		}
		unalias(doc, anchors(v, nil))
		if v.Kind == yaml.SequenceNode && !deadList(v) {
			v.Content = append(v.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pattern})
		} else {
			m.Content[i+1] = list
		}
		return
	}
	// A written exclude replaces the defaults, so spell them out.
	m.Content = append(m.Content, key, list)
}

// anchors adds n and the nodes below it that carry an anchor to set.
func anchors(n *yaml.Node, set map[*yaml.Node]bool) map[*yaml.Node]bool {
	if n.Kind == yaml.AliasNode {
		return set
	}
	if n.Anchor != "" {
		if set == nil {
			set = map[*yaml.Node]bool{}
		}
		set[n] = true
	}
	for _, c := range n.Content {
		set = anchors(c, set)
	}
	return set
}

// unalias replaces every alias below n of a node in targets by a copy of it.
func unalias(n *yaml.Node, targets map[*yaml.Node]bool) {
	if len(targets) == 0 {
		return
	}
	for i, c := range n.Content {
		if c.Kind == yaml.AliasNode {
			if targets[c.Alias] {
				n.Content[i] = copyNode(c.Alias)
			}
			continue
		}
		unalias(c, targets)
	}
}

// copyNode deep-copies n without its anchors; aliases inside stay aliases.
func copyNode(n *yaml.Node) *yaml.Node {
	cp := *n
	cp.Anchor = ""
	cp.Content = nil
	for _, c := range n.Content {
		if c.Kind == yaml.AliasNode {
			a := *c
			cp.Content = append(cp.Content, &a)
		} else {
			cp.Content = append(cp.Content, copyNode(c))
		}
	}
	return &cp
}

// deadList reports whether a non-empty list has no entry Load would keep, so
// Load falls back to the defaults and an item appended to it would drop them.
func deadList(seq *yaml.Node) bool {
	var list []string
	if err := seq.Decode(&list); err != nil {
		return true // wrong types: rewrite from what Load made of them
	}
	for _, p := range list {
		if ValidatePattern(p) == nil {
			return false
		}
	}
	return len(list) > 0
}

// textEdit is the edit as text, for the two usual cases: a block list Load
// can use gets one line after its last entry, and a file without the key
// (one document, a block mapping or nothing) gets it appended. ok is false
// for anything else.
func textEdit(data []byte, docs []*yaml.Node, effective []string, pattern string) (out []byte, ok bool) {
	eol := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		eol = "\r\n"
	}
	if len(docs) > 0 && len(docs[0].Content) > 0 && !isNull(docs[0].Content[0]) {
		m := docs[0].Content[0]
		if m.Kind != yaml.MappingNode || m.Style&yaml.FlowStyle != 0 || m.Column != 1 {
			return nil, false
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if k := m.Content[i]; !isMerge(k) && keyName(k) == "exclude" {
				return appendLine(data, k, m.Content[i+1], pattern, eol)
			}
		}
	}
	if len(docs) > 1 {
		return nil, false
	}
	var b strings.Builder
	b.WriteString("exclude:" + eol)
	for _, e := range append(slices.Clone(effective), pattern) {
		s, ok := scalarText(e)
		if !ok {
			return nil, false
		}
		b.WriteString("  - " + s + eol)
	}
	out = slices.Clone(data)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, eol...)
	}
	return append(out, b.String()...), true
}

// appendLine inserts "- pattern" after the last entry of the block list v,
// the value of key k, if Load can use the list as it is.
func appendLine(data []byte, k, v *yaml.Node, pattern, eol string) ([]byte, bool) {
	if k.Kind == yaml.AliasNode || v.Kind != yaml.SequenceNode || v.Style&yaml.FlowStyle != 0 ||
		v.Anchor != "" || len(v.Content) == 0 || deadList(v) {
		return nil, false
	}
	last := v.Content[len(v.Content)-1]
	if last.Kind != yaml.ScalarNode || last.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return nil, false
	}
	s, ok := scalarText(pattern)
	lines := bytes.SplitAfter(data, []byte("\n"))
	if !ok || v.Column < 1 || last.Line < 1 || last.Line > len(lines) {
		return nil, false
	}
	var out []byte
	for _, l := range lines[:last.Line] {
		out = append(out, l...)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, eol...)
	}
	out = append(out, strings.Repeat(" ", v.Column-1)+"- "+s+eol...)
	for _, l := range lines[last.Line:] {
		out = append(out, l...)
	}
	return out, true
}

// scalarText is s as a one-line YAML string scalar.
func scalarText(s string) (string, bool) {
	b, err := yaml.Marshal(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s})
	t := strings.TrimSuffix(string(b), "\n")
	return t, err == nil && !strings.ContainsAny(t, "\r\n")
}

// keepLayout puts back into encoded, the re-encoded orig, what the encoder
// drops: the directives before the first "---", and the blank lines above the
// top-level keys of the first document.
func keepLayout(orig, encoded []byte) []byte {
	var oDoc, eDoc yaml.Node
	out := slices.Clone(encoded)
	if yaml.Unmarshal(orig, &oDoc) == nil && yaml.Unmarshal(encoded, &eDoc) == nil &&
		len(oDoc.Content) > 0 && len(eDoc.Content) > 0 &&
		oDoc.Content[0].Kind == yaml.MappingNode && eDoc.Content[0].Kind == yaml.MappingNode {
		oLines := bytes.SplitAfter(orig, []byte("\n"))
		eLines := bytes.SplitAfter(encoded, []byte("\n"))
		om, em := oDoc.Content[0].Content, eDoc.Content[0].Content
		blank := make(map[int]int) // line index in encoded -> blank lines above it
		for i := 0; i+1 < len(om) && i+1 < len(em); i += 2 {
			if keyName(om[i]) != keyName(em[i]) {
				break
			}
			o, e := startLine(om[i]), startLine(em[i])
			n := 0
			for j := o - 1; j >= 0 && j < len(oLines) && len(bytes.TrimSpace(oLines[j])) == 0; j-- {
				n++
			}
			if n > 0 && e > 0 && e < len(eLines) {
				blank[e] = n
			}
		}
		out = nil
		for i, l := range eLines {
			for range blank[i] {
				out = append(out, '\n')
			}
			out = append(out, l...)
		}
	}
	var dirs []byte
	for _, l := range bytes.SplitAfter(orig, []byte("\n")) {
		t := bytes.TrimSpace(l)
		if bytes.HasPrefix(t, []byte("---")) {
			if len(dirs) > 0 {
				return append(append(dirs, "---\n"...), out...)
			}
			break
		}
		if bytes.HasPrefix(t, []byte("%")) {
			dirs = append(dirs, bytes.TrimRight(l, "\r\n")...)
			dirs = append(dirs, '\n')
		} else if len(t) > 0 && t[0] != '#' {
			break
		}
	}
	return out
}

// startLine is the 0-based index of the first line of key k with its head
// comment.
func startLine(k *yaml.Node) int {
	n := k.Line - 1
	if k.HeadComment != "" {
		n -= strings.Count(k.HeadComment, "\n") + 1
	}
	return n
}
