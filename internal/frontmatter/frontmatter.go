// Package frontmatter reads and writes the YAML meta block of context files.
package frontmatter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"auto-context/internal/fsutil"
	"auto-context/internal/textx"

	yaml "go.yaml.in/yaml/v3"
)

// Meta is the frontmatter written by the binary. Commit (HEAD at ack time) is
// optional and, like every field, unvalidated repo content.
type Meta struct {
	Unit       string `yaml:"unit"`
	SourceHash string `yaml:"source_hash"`
	Commit     string `yaml:"commit,omitempty"`
	Updated    string `yaml:"updated"`
}

// ErrNoFrontmatter means the file has no closed frontmatter block of ours.
var ErrNoFrontmatter = errors.New("no frontmatter")

// ErrBroken means our frontmatter block is there but is not valid YAML.
var ErrBroken = errors.New("broken frontmatter")

// MaxSize caps the size of a context doc we read.
const MaxSize = 1 << 20

// oursRe recognizes the block we write, which always has both keys; any other
// leading "---" block (a horizontal rule, the user's own front matter, even
// with an `updated:` key) is left alone.
var oursRe = regexp.MustCompile(`(?m)^(unit|source_hash)[ \t]*:`)

// split returns the raw YAML text and the body, skipping a leading BOM. ok is
// false if there is no closed block.
func split(content string) (yml, body string, ok bool) {
	content = strings.TrimPrefix(content, textx.BOM)
	var rest string
	switch {
	case strings.HasPrefix(content, "---\n"):
		rest = content[4:]
	case strings.HasPrefix(content, "---\r\n"):
		rest = content[5:]
	default:
		return "", content, false
	}
	pos := 0
	for pos <= len(rest) {
		end := strings.IndexByte(rest[pos:], '\n')
		line, next := rest[pos:], len(rest)
		if end >= 0 {
			line, next = rest[pos:pos+end], pos+end+1
		}
		if strings.TrimSuffix(line, "\r") == "---" {
			return rest[:pos], rest[next:], true
		}
		if end < 0 {
			break
		}
		pos = next
	}
	return "", content, false
}

// Read returns the meta and body of path, read through fsutil.ReadDoc inside
// root. Without a block of ours it returns ErrNoFrontmatter and the whole
// content (without a leading BOM) as body; with an unparsable one, ErrBroken and the rest as body.
func Read(root, path string) (Meta, string, error) {
	_, data, err := fsutil.ReadDoc(root, path, MaxSize)
	if err != nil {
		return Meta{}, "", err
	}
	content := string(data)
	yml, body, ok := split(content)
	if !ok || !oursRe.MatchString(yml) {
		return Meta{}, strings.TrimPrefix(content, textx.BOM), ErrNoFrontmatter
	}
	var m Meta
	if err := yaml.Unmarshal([]byte(yml), &m); err != nil {
		return Meta{}, body, fmt.Errorf("%w: %v", ErrBroken, err)
	}
	return m, body, nil
}

// SetHash rewrites our frontmatter block or, if the file has none, adds it
// above the content; the rest is kept byte-for-byte, and a leading BOM stays
// first in the file. In a valid block of ours only the values of our keys
// change: they come first (unit, source_hash, commit, updated), the other keys
// follow unchanged in their order. A broken block is replaced as a whole.
// commit, if not empty, is recorded as the acked commit. path is resolved with
// fsutil.ResolveDoc and must stay inside root.
func SetHash(root, path, unit, hash, commit string, today time.Time) error {
	path, data, err := fsutil.ReadDoc(root, path, MaxSize)
	if err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	body, lead := string(data), ""
	if strings.HasPrefix(body, textx.BOM) {
		body, lead = body[len(textx.BOM):], textx.BOM
	}
	m := Meta{Unit: unit, SourceHash: hash, Commit: commit, Updated: today.Format("2006-01-02")}
	old := ""
	if yml, rest, ok := split(body); ok && oursRe.MatchString(yml) {
		// Parsed with LF line ends: yaml.v3 drops the comments of a CRLF
		// block. YAML folds line breaks in values to LF anyway, and the
		// block is written back with the file's line end below.
		body, old = rest, strings.ReplaceAll(yml, "\r\n", "\n")
	}
	y, err := render(old, m)
	if err != nil {
		return err
	}
	eol := textx.EOL(string(data))
	out := lead + "---" + eol + strings.ReplaceAll(string(y), "\n", eol) + "---" + eol + body
	return fsutil.WriteDoc(root, path, []byte(out), perm)
}

// ourKeys are the keys of our block, in the order they are written.
var ourKeys = []string{"unit", "source_hash", "commit", "updated"}

// render returns the YAML of our block with m written into old, the YAML of
// the current block of ours ("" if none). If old does not parse as a mapping,
// or the merged block would not read back as exactly m (anchors, aliases or
// merge keys that point into our replaced values), only m is written.
func render(old string, m Meta) ([]byte, error) {
	if old != "" {
		var check Meta
		var doc yaml.Node
		if yaml.Unmarshal([]byte(old), &check) == nil && yaml.Unmarshal([]byte(old), &doc) == nil &&
			doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
			if y, err := merge(&doc, m); err == nil {
				return y, nil
			}
		}
	}
	doc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	return merge(&doc, m)
}

// merge puts our keys with the values of m first in the mapping of doc, drops
// their old pairs, keeps every other node as is and checks that the result
// reads back as m.
func merge(doc *yaml.Node, m Meta) ([]byte, error) {
	vals := map[string]string{"unit": m.Unit, "source_hash": m.SourceHash, "commit": m.Commit, "updated": m.Updated}
	mapping := doc.Content[0]
	var content []*yaml.Node
	for _, k := range ourKeys {
		if k == "commit" && m.Commit == "" {
			continue
		}
		content = append(content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, scalar(vals[k]))
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if k := mapping.Content[i]; k.Kind == yaml.ScalarNode {
			if _, ours := vals[k.Value]; ours {
				continue
			}
		}
		content = append(content, mapping.Content[i], mapping.Content[i+1])
	}
	mapping.Content = content
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	var back Meta
	if err := yaml.Unmarshal(buf.Bytes(), &back); err != nil {
		return nil, err
	}
	if back != m {
		return nil, errors.New("frontmatter does not read back")
	}
	return buf.Bytes(), nil
}

// scalar returns a string node for s. s with control, format or line-break
// characters is double-quoted, so every such rune is escaped (the plain and
// literal styles of yaml.v3 lose some of them, e.g. a lone "\n"); invalid
// UTF-8 is written as !!binary, as yaml.Marshal does.
func scalar(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	switch {
	case !utf8.ValidString(s):
		_ = n.Encode(s) // a string always encodes
	case strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
	}) >= 0:
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

// MaxSummary caps the length of Summary in runes.
const MaxSummary = 800

// Summary returns the first non-empty line after the first "# " heading, cut
// to MaxSummary runes, or "" if absent or that line is itself a heading.
func Summary(body string) string {
	seen := false
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if !seen {
			seen = strings.HasPrefix(line, "# ")
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return ""
		}
		return capRunes(strings.TrimSpace(line), MaxSummary)
	}
	return ""
}

// capRunes returns the first n runes of s.
func capRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
