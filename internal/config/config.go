// Package config loads, validates and updates the optional
// .auto-context/config.yaml, and finds the project root.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"auto-context/internal/fsutil"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
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
}

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

// languageRe accepts a BCP 47-style code only: the value is shown to Claude,
// so free text from a cloned repo must not pass.
var languageRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}$`)

// ValidLanguage reports whether s is an acceptable docs language code.
func ValidLanguage(s string) bool { return languageRe.MatchString(s) }

// Load reads root/.auto-context/config.yaml; absent (or null) fields keep
// their defaults. A bad setting (unknown key, wrong type, invalid value or pattern)
// is dropped and listed in Problems while the rest applies; a file that
// cannot be read or parsed gives Default() and the error.
func Load(root string) (Config, Problems, error) {
	data, err := fsutil.ReadFile(root, fsutil.Abs(root, FileName), MaxSize)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil, nil
	}
	if err != nil {
		return Default(), nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		var te *yaml.TypeError
		if !errors.As(err, &te) {
			if indexFileKeys(data) > 1 {
				return Default(), nil, ErrBadIndexFile // duplicated key
			}
			return Default(), nil, err
		}
	}
	if indexFileKeys(data) > 1 {
		return Default(), nil, ErrBadIndexFile // duplicated key
	}
	if v, ok := raw["index_file"]; ok && v != nil {
		if _, isStr := v.(string); !isStr {
			return Default(), nil, ErrBadIndexFile
		}
	}
	var pr Problems
	for k := range raw {
		if !knownKeys[k] {
			pr = append(pr, "unknown key "+safeKey(k))
		}
	}
	slices.Sort(pr)
	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		var te *yaml.TypeError
		if !errors.As(err, &te) {
			return Default(), nil, err
		}
		for _, e := range te.Errors {
			pr = append(pr, typeProblem(e)) // the field keeps its default
		}
	}
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

// typeProblem rewrites a yaml type error ("line 3: cannot unmarshal !!str
// `x` into int") without the value it quotes.
func typeProblem(e string) string {
	if line, _, ok := strings.Cut(e, ": "); ok && strings.HasPrefix(line, "line ") && strings.Trim(line[5:], "0123456789") == "" {
		return line + ": a value of the wrong type"
	}
	return "a value of the wrong type"
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

// fix replaces null lists by their defaults and invalid values by valid ones,
// returning what it changed.
func (c *Config) fix() Problems {
	def := Default()
	var pr Problems
	if c.MinFiles < 1 {
		pr = append(pr, fmt.Sprintf("min_files %d (must be at least 1)", c.MinFiles))
		c.MinFiles = def.MinFiles
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
		check    func(string) error
	}{
		{"code_extensions", &c.CodeExtensions, &def.CodeExtensions, func(e string) error {
			if len(e) < 2 || e[0] != '.' {
				return errors.New("must start with a dot")
			}
			return nil
		}},
		{"module_markers", &c.ModuleMarkers, &def.ModuleMarkers, func(m string) error {
			if m == "" || strings.ContainsAny(m, "/\\") {
				return errors.New("must be a file name")
			}
			return nil
		}},
		{"include", &c.Include, &def.Include, ValidatePattern},
		{"exclude", &c.Exclude, &def.Exclude, ValidatePattern},
		{"track", &c.Track, &def.Track, ValidatePattern},
	}
	for _, l := range lists {
		if *l.got == nil {
			*l.got = *l.def
			continue
		}
		var keep []string
		bad := false
		for i, v := range *l.got {
			if err := l.check(v); err != nil {
				// Values are not quoted: problems reach Claude's context.
				reason := err.Error()
				if !strings.HasPrefix(reason, "must") {
					reason = "invalid pattern"
				}
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
		}
	}
	return pr
}

// ValidatePattern checks an include/exclude/track glob: valid doublestar syntax,
// bounded length and brace groups.
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

// AddExclude appends pattern to exclude (no duplicates), creating the file if
// needed. Only the exclude key is edited; comments and other keys are kept.
func AddExclude(root, pattern string) error {
	cfg, _, err := Load(root)
	if err != nil {
		return err
	}
	if slices.Contains(cfg.Exclude, pattern) {
		return nil
	}
	// The plugin's directory and its .gitignore, which lets config.yaml be
	// committed, may not exist yet.
	if err := fsutil.EnsureStateDir(root); err != nil {
		return err
	}
	path := fsutil.Abs(root, FileName)
	data, err := fsutil.ReadFile(root, path, MaxSize)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Decode every document: Load reads only the first, the rest are kept as is.
	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var d yaml.Node
		if err := dec.Decode(&d); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		docs = append(docs, &d)
	}
	var prefix []byte
	if len(docs) == 0 {
		// Empty or comment-only file: the decoder drops comments, keep the bytes.
		if prefix = bytes.TrimRight(data, " \t\r\n"); len(prefix) > 0 {
			prefix = append(prefix, '\n')
		}
		docs = []*yaml.Node{{Kind: yaml.DocumentNode}}
	}
	doc := docs[0]
	if len(doc.Content) == 0 || doc.Content[0].Kind == yaml.ScalarNode && doc.Content[0].Tag == "!!null" {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level is not a mapping", FileName)
	}
	item := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pattern}
	found := false
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "exclude" {
			continue
		}
		found = true
		if v := m.Content[i+1]; v.Kind == yaml.SequenceNode {
			v.Content = append(v.Content, item)
		} else {
			var list yaml.Node
			if err := list.Encode(append(cfg.Exclude, pattern)); err != nil {
				return err
			}
			m.Content[i+1] = &list
		}
	}
	if !found {
		// A written exclude replaces the defaults, so spell them out.
		var list yaml.Node
		if err := list.Encode(append(cfg.Exclude, pattern)); err != nil {
			return err
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "exclude"}, &list)
	}
	buf := bytes.NewBuffer(prefix)
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return err
		}
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return fsutil.WriteAtomic(root, path, buf.Bytes(), 0o644)
}

// FindRoot walks up from cwd to a directory holding .git or FileName owned
// by the current user. It never climbs into
// $HOME from below it, so a stray ~/.auto-context/config.yaml (or a dotfiles
// ~/.git) does not make the whole home the project; without a marker it
// returns cwd.
func FindRoot(cwd string) string {
	home, _ := os.UserHomeDir()
	dir := cwd
	for {
		if home != "" && dir == home && dir != cwd {
			return cwd
		}
		for _, m := range []string{".git", FileName} {
			if fi, err := os.Stat(filepath.Join(dir, m)); err == nil && ownedByUser(fi) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

// indexFileKeys counts the top-level index_file keys in data (0 if unparsable).
func indexFileKeys(data []byte) int {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return 0
	}
	n := 0
	m := doc.Content[0].Content
	for i := 0; i+1 < len(m); i += 2 {
		if m[i].Value == "index_file" {
			n++
		}
	}
	return n
}
