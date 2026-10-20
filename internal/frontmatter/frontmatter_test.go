package frontmatter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var day = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "u.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadRoundTrip(t *testing.T) {
	p := write(t, "# T\nbody\n")
	if err := SetHash(filepath.Dir(p), p, "internal/foo", "abc123", day); err != nil {
		t.Fatal(err)
	}
	m, body, err := Read(filepath.Dir(p), p)
	if err != nil {
		t.Fatal(err)
	}
	want := Meta{Unit: "internal/foo", SourceHash: "abc123", Updated: "2026-10-19"}
	if m != want {
		t.Fatalf("meta = %+v, want %+v", m, want)
	}
	if body != "# T\nbody\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestReadNoFrontmatter(t *testing.T) {
	for _, c := range []string{"# T\ntext\n", "---\nunit: x\nno close\n", ""} {
		p := write(t, c)
		_, body, err := Read(filepath.Dir(p), p)
		if !errors.Is(err, ErrNoFrontmatter) {
			t.Fatalf("%q: err = %v", c, err)
		}
		if body != c {
			t.Fatalf("body = %q, want %q", body, c)
		}
	}
}

func TestReadBrokenYAML(t *testing.T) {
	p := write(t, "---\nunit: [oops\n---\nbody\n")
	if _, _, err := Read(filepath.Dir(p), p); err == nil || errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("err = %v", err)
	}
}

func TestSetHashPreservesBody(t *testing.T) {
	body := "# T\r\ntext\r\n---\nmore\n---\n"
	p := write(t, "---\r\nunit: old\r\nsource_hash: x\r\n---\r\n"+body)
	if err := SetHash(filepath.Dir(p), p, "u", "h", day); err != nil {
		t.Fatal(err)
	}
	_, got, err := Read(filepath.Dir(p), p)
	if err != nil {
		t.Fatal(err)
	}
	if got != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
}

func TestSetHashAddsBlock(t *testing.T) {
	p := write(t, "# T\ntext\n")
	if err := SetHash(filepath.Dir(p), p, "u", "h", day); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	want := "---\nunit: u\nsource_hash: h\nupdated: \"2026-10-19\"\n---\n# T\ntext\n"
	if string(data) != want {
		t.Fatalf("got %q", data)
	}
}

func TestSetHashRepairsBrokenYAML(t *testing.T) {
	p := write(t, "---\nunit: [oops\n---\nbody\n")
	if err := SetHash(filepath.Dir(p), p, "u", "h", day); err != nil {
		t.Fatal(err)
	}
	m, body, err := Read(filepath.Dir(p), p)
	if err != nil || m.Unit != "u" || body != "body\n" {
		t.Fatalf("m=%+v body=%q err=%v", m, body, err)
	}
}

func TestSetHashKeepsPerm(t *testing.T) {
	p := write(t, "x\n")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetHash(filepath.Dir(p), p, "u", "h", day); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 && filepath.Separator == '/' {
		t.Fatalf("perm = %v", st.Mode().Perm())
	}
}

func TestSetHashMissingFile(t *testing.T) {
	dir := t.TempDir()
	err := SetHash(dir, filepath.Join(dir, "nope.md"), "u", "h", day)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestSummary(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "# Title\nSummary line.\nmore\n", "Summary line."},
		{"blank lines", "# Title\n\n\n  Text here \nx\n", "Text here"},
		{"no heading", "just text\n", ""},
		{"crlf", "# Title\r\nText\r\n", "Text"},
		{"next is heading", "# Title\n\n## Sub\ntext\n", ""},
		{"preamble", "intro\n# Title\nS\n", "S"},
		{"heading only", "# Title\n", ""},
	}
	for _, c := range cases {
		if got := Summary(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A leading "---" block that is not ours (no unit/source_hash/updated key) is
// the user's text: Read treats the file as having no frontmatter, and SetHash
// adds our block above it instead of replacing it.
func TestUserBlockIsNotOurs(t *testing.T) {
	content := "---\nIntro the user wrote: it matters.\n---\n# svc\nsum\n"
	p := write(t, content)
	if _, body, err := Read(filepath.Dir(p), p); !errors.Is(err, ErrNoFrontmatter) || body != content {
		t.Fatalf("Read: body=%q err=%v, want whole content and ErrNoFrontmatter", body, err)
	}
	if err := SetHash(filepath.Dir(p), p, "svc", "h", day); err != nil {
		t.Fatal(err)
	}
	m, body, err := Read(filepath.Dir(p), p)
	if err != nil || m.SourceHash != "h" || body != content {
		t.Fatalf("after SetHash: m=%+v body=%q err=%v", m, body, err)
	}
}

func TestReadBrokenIsMarked(t *testing.T) {
	p := write(t, "---\nsource_hash: [oops\n---\nbody\n")
	if _, _, err := Read(filepath.Dir(p), p); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v, want ErrBroken", err)
	}
}

// A context doc linked to a non-.md file (e.g. .env) is never read.
func TestReadRefusesNonMarkdownLink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("# S\nAPI_KEY=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "u.md")
	if err := os.Symlink(".env", link); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if _, body, err := Read(dir, link); err == nil || body != "" {
		t.Fatalf("read through link: body=%q err=%v", body, err)
	}
	if err := SetHash(dir, link, "u", "h", day); err == nil {
		t.Fatal("SetHash wrote through a link to .env")
	}
}

func TestSetHashKeepsCRLF(t *testing.T) {
	for _, in := range []string{"---\r\nunit: old\r\n---\r\n# T\r\n", "# T\r\ntext\r\n"} {
		p := write(t, in)
		if err := SetHash(filepath.Dir(p), p, "u", "h", day); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		if strings.Contains(strings.ReplaceAll(string(data), "\r\n", ""), "\n") {
			t.Fatalf("bare LF in %q", data)
		}
		m, _, err := Read(filepath.Dir(p), p)
		if err != nil || m.Unit != "u" || m.SourceHash != "h" {
			t.Fatalf("m=%+v err=%v", m, err)
		}
	}
}
