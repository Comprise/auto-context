package frontmatter

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzSetHashRoundTrip: after SetHash, Read returns exactly the written Meta,
// and a second SetHash with the same values changes nothing.
func FuzzSetHashRoundTrip(f *testing.F) {
	f.Add("# T\nbody\n", "internal/foo", "v2:sha256:ab", "")
	f.Add("---\r\nunit: old\r\nsource_hash: x\r\ntags: [a]\r\n---\r\n# T\r\n", "u", "h", "c")
	f.Add("\ufeff---\nunit: [oops\n---\nbody\n", "u", "h", "")
	f.Add("---\ntitle: X\n---\n# T\n", "a: b", "- x", "2026-10-19")
	f.Add("---\nunit: &a x\nfoo: *a\n---\n", "u\n---\n", "h", "c")
	f.Fuzz(func(t *testing.T, content, unit, hash, commit string) {
		dir := t.TempDir()
		p := filepath.Join(dir, "u.md")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := SetHash(dir, p, unit, hash, commit, day); err != nil {
			t.Fatalf("SetHash: %v", err)
		}
		m, _, err := Read(dir, p)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		want := Meta{Unit: unit, SourceHash: hash, Commit: commit, Updated: "2026-10-19"}
		if m != want {
			t.Fatalf("meta = %+v, want %+v", m, want)
		}
		first, _ := os.ReadFile(p)
		if err := SetHash(dir, p, unit, hash, commit, day); err != nil {
			t.Fatalf("second SetHash: %v", err)
		}
		if second, _ := os.ReadFile(p); string(second) != string(first) {
			t.Fatalf("second SetHash changed the file:\n%q\n%q", first, second)
		}
	})
}
