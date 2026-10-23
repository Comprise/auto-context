package project

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"auto-context/internal/docstate"
	"auto-context/internal/frontmatter"
	"auto-context/internal/hashing"
	"auto-context/internal/testutil"

	"github.com/bmatcuk/doublestar/v4"
)

// Writes that would act on the default units are refused while the config
// cannot be parsed.
func TestBrokenConfigRefusesWrites(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc\n")
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nThe service.\n")
	p, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ConfigBroken() {
		t.Fatal("config not broken")
	}
	if err := p.Ack("svc", time.Now()); !errors.Is(err, ErrConfigBroken) {
		t.Errorf("Ack: %v", err)
	}
	if _, err := p.UpdateIndex(p.Statuses()); !errors.Is(err, ErrConfigBroken) {
		t.Errorf("UpdateIndex: %v", err)
	}
	if err := p.WriteChanges(io.Discard, "svc"); !errors.Is(err, ErrConfigBroken) {
		t.Errorf("WriteChanges: %v", err)
	}
}

// WriteChanges limits git to the unit's own directory: a sibling whose name
// starts the same is not listed. A bracketed name such as a[1] cannot be a
// unit at all (units.SafeName rejects every glob-magic character), so it is
// checked to stay out of a1's output too.
func TestWriteChangesLiteralPathspec(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		testutil.Git(t, root, args...)
	}
	git("init", "-q")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [a1, a10, \"a[1]\"]\n")
	testutil.WriteFile(t, root, "a1/own.go", "package a\n")
	testutil.WriteFile(t, root, "a10/sibling.go", "package a\n")
	testutil.WriteFile(t, root, "a[1]/bracket.go", "package a\n")
	git("add", "-A")
	git("commit", "-qm", "first")
	testutil.WriteFile(t, root, "a1/own.go", "package a // edited\n")
	testutil.WriteFile(t, root, "a10/sibling.go", "package a // edited\n")
	testutil.WriteFile(t, root, "a[1]/bracket.go", "package a // edited\n")
	testutil.WriteFile(t, root, "a1/new.go", "package a\n")
	testutil.WriteFile(t, root, "a10/new10.go", "package a\n")
	testutil.WriteFile(t, root, "a[1]/newb.go", "package a\n")
	p, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if p.tree.IsUnit("a[1]") || !p.tree.IsUnit("a1") || !p.tree.IsUnit("a10") {
		t.Fatalf("units: %v", p.tree.Units())
	}
	var b strings.Builder
	if err := p.WriteChanges(&b, "a1"); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "own.go") || !strings.Contains(out, "new.go") {
		t.Errorf("own files missing:\n%s", out)
	}
	for _, f := range []string{"sibling.go", "new10.go", "bracket.go", "newb.go"} {
		if strings.Contains(out, f) {
			t.Errorf("%s listed for a1:\n%s", f, out)
		}
	}
}

// Prewarm's entries survive SaveCache and are there for the next Open.
func TestPrewarmPersists(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc]\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	testutil.WriteFile(t, root, "svc/b.go", "package svc\n")
	// Old mtimes: entries of files hashed right after a write are racy and
	// not kept across runs.
	old := time.Now().Add(-time.Hour)
	for _, f := range []string{"svc/a.go", "svc/b.go"} {
		if err := os.Chtimes(filepath.Join(root, f), old, old); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	files := p.tree.Files("svc")
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	p.Prewarm(files)
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	q, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if n := q.Cache().Len(); n != len(files) || q.CacheErr() != nil {
		t.Errorf("reopened cache has %d entries (err %v), want %d", n, q.CacheErr(), len(files))
	}
	data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "cache.v5.bin"))
	for _, f := range files {
		if !strings.Contains(string(data), f) {
			t.Errorf("cache file lacks %s: %s", f, data)
		}
	}
}

// writeTree writes files (path -> content) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		testutil.WriteFile(t, root, rel, content)
	}
}

// open opens root or fails the test.
func open(t *testing.T, root string) *Project {
	t.Helper()
	p, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// age sets the mtime of the files under root an hour back, so the hash
// cache keeps their entries (fresh files are racy).
func age(t *testing.T, root string, rels ...string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, rel := range rels {
		if err := os.Chtimes(filepath.Join(root, rel), old, old); err != nil {
			t.Fatal(err)
		}
	}
}

// SaveCache in a project enabled by context/_root.md alone creates the
// plugin's .gitignore with the state dir, so the cache never shows as
// untracked.
func TestSaveCacheCreatesGitignore(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"context/_root.md": "# root\n",
		"svc/go.mod":       "module svc\n",
		"svc/a.go":         "package svc\n",
	})
	p := open(t, root)
	p.Statuses()
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".auto-context/.gitignore", ".auto-context/cache.v5.bin"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
}

// A directory with glob metacharacters is excluded literally: the pattern
// written matches the directory and nothing else. (Such a name never passes
// units.SafeName, so it cannot be a unit; the pattern is what Exclude owns.)
func TestExcludeGlobMetachars(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [libv]\n",
		"lib[v1]/a.go":              "package lib\n",
		"libv/b.go":                 "package lib\n",
	})
	p := open(t, root)
	if !p.IsUnit("libv") {
		t.Fatalf("libv not a unit: %v", p.Units())
	}
	if err := p.Exclude("lib[v1]"); err != nil {
		t.Fatal(err)
	}
	ex := p.Config().Exclude
	if !slices.Contains(ex, `lib\[v1\]`) {
		t.Fatalf("exclude = %v", ex)
	}
	// End to end: the reloaded tree (and a fresh Open) keep the sibling.
	if !p.IsUnit("libv") || !open(t, root).IsUnit("libv") {
		t.Errorf("libv excluded: %v", p.Units())
	}
	for name, want := range map[string]bool{"lib[v1]": true, "libv": false, "lib1": false} {
		if got, err := doublestar.Match(`lib\[v1\]`, name); err != nil || got != want {
			t.Errorf("match %s = %v (%v), want %v", name, got, err, want)
		}
	}
}

// A name that differs from the directory only in case is refused, also on a
// case-insensitive file system, where the pattern would never match.
func TestExcludeCaseMismatch(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "min_files: 5\n",
		"internal/hooks/a.go":       "package hooks\n",
	})
	before, _ := os.ReadFile(filepath.Join(root, ".auto-context", "config.yaml"))
	err := open(t, root).Exclude("Internal/Hooks")
	if err == nil || err.Error() != "no directory Internal/Hooks (names are case-sensitive)" {
		t.Errorf("err = %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, ".auto-context", "config.yaml")); string(before) != string(after) {
		t.Errorf("config changed:\n%s", after)
	}
}

// Exclude refuses what is not a directory inside the repository, the root
// unit and a pattern over the length limit, leaving the config byte for byte.
func TestExcludeRefuses(t *testing.T) {
	long := strings.Repeat("d", 60) + "/" + strings.Repeat("e", 60) + "/" + strings.Repeat("f", 60) + "/" +
		strings.Repeat("g", 60) + "/" + strings.Repeat("h", 60)
	cases := map[string]func(t *testing.T, root string) string{
		"link outside": func(t *testing.T, root string) string {
			testutil.Symlink(t, t.TempDir(), filepath.Join(root, "ext"))
			return "ext"
		},
		"file": func(t *testing.T, root string) string {
			testutil.WriteFile(t, root, "f", "")
			return "f"
		},
		"missing": func(t *testing.T, root string) string { return "nope" },
		"root":    func(t *testing.T, root string) string { return "_root" },
		"pattern too long": func(t *testing.T, root string) string {
			testutil.WriteFile(t, root, long+"/a.go", "package a\n")
			return long
		},
		"unreadable parent": func(t *testing.T, root string) string {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("permissions are not enforced")
			}
			testutil.WriteFile(t, root, "locked/sub/a.go", "package a\n")
			locked := filepath.Join(root, "locked")
			if err := os.Chmod(locked, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(locked, 0o755) })
			return "locked/sub"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, map[string]string{
				".auto-context/config.yaml": "# keep me\nmin_files: 5\nexclude: [gen]\n",
				"svc/go.mod":                "module svc\n",
			})
			dir := setup(t, root)
			cfg := filepath.Join(root, ".auto-context", "config.yaml")
			before, _ := os.ReadFile(cfg)
			if err := open(t, root).Exclude(dir); err == nil {
				t.Fatal("accepted")
			}
			if after, _ := os.ReadFile(cfg); string(before) != string(after) {
				t.Errorf("config changed:\n%s", after)
			}
		})
	}
}

// After Exclude the project reflects the new config: the unit is gone.
func TestExcludeReloads(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [svc, lib]\n",
		"svc/a.go":                  "package svc\n",
		"lib/a.go":                  "package lib\n",
	})
	p := open(t, root)
	if !p.IsUnit("lib") {
		t.Fatal("lib not a unit")
	}
	p.Statuses() // the cache is loaded before the reload
	if err := p.Exclude("lib"); err != nil {
		t.Fatal(err)
	}
	if p.IsUnit("lib") || !p.IsUnit("svc") || !slices.Contains(p.Config().Exclude, "lib") {
		t.Errorf("not reloaded: units %v, exclude %v", p.Units(), p.Config().Exclude)
	}
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
}

// Settings that decide the units were dropped: acks are refused, as for a
// broken config, since the hash would be of units the user did not mean.
func TestAckRefusedOnDiscoveryProblems(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [svc]\nexclude: [\"a[\"]\n",
		"svc/a.go":                  "package svc\n",
		"context/svc.md":            "# svc\n\nThe service.\n",
	})
	p := open(t, root)
	if p.ConfigBroken() || !p.Config().DiscoveryReset {
		t.Fatalf("broken=%v reset=%v", p.ConfigBroken(), p.Config().DiscoveryReset)
	}
	if err := p.Ack("svc", time.Now()); !errors.Is(err, ErrConfigDegraded) {
		t.Errorf("Ack: %v", err)
	}
	acked, failed, _ := p.AckTouched([]string{"svc"}, time.Now())
	if len(acked) != 0 || !errors.Is(failed["svc"], ErrConfigDegraded) {
		t.Errorf("AckTouched: acked %v failed %v", acked, failed)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "context", "svc.md")); strings.Contains(string(data), "source_hash") {
		t.Errorf("doc acked:\n%s", data)
	}
	// A dropped setting that does not decide the units does not block.
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc]\nlanguage: \"x y\"\n")
	if err := open(t, root).Ack("svc", time.Now()); err != nil {
		t.Errorf("Ack with a non-discovery problem: %v", err)
	}
}

// Upgrade rewrites the docs that are up to date by the old hash scheme and
// leaves the others alone.
func TestUpgrade(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [svc, lib, new]\n",
		"svc/a.go":                  "package svc\n",
		"lib/a.go":                  "package lib\n",
		"new/a.go":                  "package lib\n",
		"context/new.md":            "# new\n\nNew.\n",
	})
	p := open(t, root)
	old, err := hashing.SourceHash(context.Background(), root, p.Files("svc"), nil, hashing.NewCache())
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "context/svc.md", "---\nunit: svc\nsource_hash: "+old+"\nupdated: \"2026-01-01\"\n---\n# svc\n\nS.\n")
	testutil.WriteFile(t, root, "context/lib.md", "---\nunit: lib\nsource_hash: sha256:00\nupdated: \"2026-01-01\"\n---\n# lib\n\nL.\n")
	p = open(t, root)
	acked, failed := p.Upgrade(time.Now())
	if !slices.Equal(acked, []string{"svc"}) || len(failed) != 0 {
		t.Fatalf("acked %v failed %v", acked, failed)
	}
	p = open(t, root)
	for _, st := range p.Statuses() {
		switch st.Unit {
		case "svc":
			if st.State != docstate.OK || st.Note != "" {
				t.Errorf("svc: %+v", st)
			}
		case "lib":
			if st.State != docstate.Stale {
				t.Errorf("lib: %+v", st)
			}
		}
	}
	if m, _, err := frontmatter.Read(root, filepath.Join(root, "context", "svc.md")); err != nil || !strings.HasPrefix(m.SourceHash, docstate.HashPrefix) {
		t.Errorf("svc meta %+v err %v", m, err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "context", "new.md")); strings.Contains(string(data), "source_hash") {
		t.Errorf("new.md acked:\n%s", data)
	}
	// With degraded settings nothing is upgraded.
	testutil.WriteFile(t, root, "context/svc.md", "---\nunit: svc\nsource_hash: "+old+"\nupdated: \"2026-01-01\"\n---\n# svc\n\nS.\n")
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc, lib, new]\nmin_files: -1\n")
	acked, failed = open(t, root).Upgrade(time.Now())
	if len(acked) != 0 || !errors.Is(failed["svc"], ErrConfigDegraded) {
		t.Errorf("degraded: acked %v failed %v", acked, failed)
	}
}

// Prewarm with nothing to hash does not load the cache.
func TestPrewarmSkipsWithoutFiles(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml":  "include: [svc]\n",
		"svc/a.go":                   "package svc\n",
		".auto-context/cache.v5.bin": "garbage",
	})
	p := open(t, root)
	p.Prewarm(nil)
	if p.cache != nil {
		t.Error("cache loaded")
	}
}

// Statuses hashes ahead only the units whose docs record a hash: the others
// need no hash.
func TestStatusesPrewarmOnlyDocumented(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [svc, lib]\n",
		"svc/a.go":                  "package svc\n",
		"lib/a.go":                  "package lib\n",
		"lib/b.go":                  "package lib\n",
		"lib/c.go":                  "package lib\n",
		"context/svc.md":            "# svc\n\nS.\n",
	})
	age(t, root, "svc/a.go", "lib/a.go", "lib/b.go", "lib/c.go")
	if err := open(t, root).Ack("svc", time.Now()); err != nil {
		t.Fatal(err)
	}
	p := open(t, root)
	for _, st := range p.Statuses() {
		if st.Unit == "svc" && st.State != docstate.OK {
			t.Fatalf("svc: %+v", st)
		}
	}
	if n := p.Cache().Len(); n != 1 {
		t.Errorf("cache has %d entries, want 1 (svc/a.go)", n)
	}
}

// RefreshIndex takes the summaries from the docs without hashing anything.
func TestRefreshIndexNoHashing(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [svc]\n",
		"svc/a.go":                  "package svc\n",
		"context/svc.md":            "# svc\n\nThe service.\n",
	})
	p := open(t, root)
	if err := p.RefreshIndex(); err != nil {
		t.Fatal(err)
	}
	if p.cache != nil {
		t.Error("RefreshIndex loaded the hash cache")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); !strings.Contains(string(data), "- `svc` — The service.") {
		t.Errorf("index:\n%s", data)
	}
	if _, _, err := p.AckTouched([]string{"nounit"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if p.cache != nil {
		t.Error("AckTouched without acks loaded the hash cache")
	}
}

// A v1.0.0 project has cache.json but no .auto-context/.gitignore: even an
// unchanged cache gets it on save.
func TestSaveCacheCleanCacheAddsGitignore(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"context/_root.md": "---\nunit: _root\nsource_hash: sha256:00\n---\n# root\n",
		"a.go":             "package a\n",
	})
	age(t, root, "a.go")
	p := open(t, root)
	p.Statuses()
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(root, ".auto-context", ".gitignore")
	if err := os.Remove(gi); err != nil {
		t.Fatal(err)
	}
	p = open(t, root)
	p.Statuses()
	if p.Cache().Dirty() {
		t.Fatal("cache dirty: the test needs a clean one")
	}
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gi); err != nil {
		t.Errorf(".gitignore not restored: %v", err)
	}
}

// With degraded discovery settings the root index is not rewritten either:
// RefreshIndex, UpdateIndex and AckTouched report ErrConfigDegraded.
func TestIndexRefusedOnDiscoveryProblems(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [svc]\nmin_files: -1\n",
		"svc/a.go":                  "package svc\n",
		"context/svc.md":            "# svc\n\nThe service.\n",
	})
	p := open(t, root)
	if !p.Config().DiscoveryReset {
		t.Fatal("config not degraded")
	}
	if err := p.RefreshIndex(); !errors.Is(err, ErrConfigDegraded) {
		t.Errorf("RefreshIndex: %v", err)
	}
	if _, err := p.UpdateIndex(p.Statuses()); !errors.Is(err, ErrConfigDegraded) {
		t.Errorf("UpdateIndex: %v", err)
	}
	if _, _, indexErr := p.AckTouched([]string{"svc"}, time.Now()); !errors.Is(indexErr, ErrConfigDegraded) {
		t.Errorf("AckTouched index error: %v", indexErr)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("index written: %v", err)
	}
}
