// Package frontmatter reads and writes the YAML meta block of context files.
package frontmatter

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"auto-context/internal/fsutil"

	"gopkg.in/yaml.v3"
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

// bom is the UTF-8 byte order mark some editors put first in a file.
const bom = "\ufeff"

// split returns the raw YAML text and the body, skipping a leading BOM. ok is
// false if there is no closed block.
func split(content string) (yml, body string, ok bool) {
	content = strings.TrimPrefix(content, bom)
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
		return Meta{}, strings.TrimPrefix(content, bom), ErrNoFrontmatter
	}
	var m Meta
	if err := yaml.Unmarshal([]byte(yml), &m); err != nil {
		return Meta{}, body, fmt.Errorf("%w: %v", ErrBroken, err)
	}
	return m, body, nil
}

// eolOf returns "\r\n" if the first line of s ends with it, else "\n".
func eolOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 && s[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// SetHash rewrites our frontmatter block (even a broken one) or, if the file
// has none, adds it above the content; the rest is kept byte-for-byte, and a
// leading BOM stays first in the file. commit, if not empty, is recorded as
// the acked commit. path is resolved with fsutil.ResolveDoc and must stay
// inside root.
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
	if strings.HasPrefix(body, bom) {
		body, lead = body[len(bom):], bom
	}
	if yml, rest, ok := split(body); ok && oursRe.MatchString(yml) {
		body = rest
	}
	y, err := yaml.Marshal(Meta{Unit: unit, SourceHash: hash, Commit: commit, Updated: today.Format("2006-01-02")})
	if err != nil {
		return err
	}
	eol := eolOf(string(data))
	out := lead + "---" + eol + strings.ReplaceAll(string(y), "\n", eol) + "---" + eol + body
	return fsutil.WriteDoc(root, path, []byte(out), perm)
}

// Summary returns the first non-empty line after the first "# " heading,
// or "" if absent or that line is itself a heading.
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
		return strings.TrimSpace(line)
	}
	return ""
}
