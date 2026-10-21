package hashing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"auto-context/internal/testutil"
)

func hex256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// makeOld sets the mtime of rel an hour back, so its cache entry is not racy.
func makeOld(t *testing.T, root, rel string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, rel), old, old); err != nil {
		t.Fatal(err)
	}
}

func mustHash(t *testing.T, root string, files []string, c *Cache) string {
	t.Helper()
	h, err := SourceHash(root, files, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestSourceHashStable(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, r := range []string{a, b} {
		testutil.WriteFile(t, r, "x/a.go", "aaa")
		testutil.WriteFile(t, r, "x/b.go", "bbb")
	}
	files := []string{"x/a.go", "x/b.go"}
	ha := mustHash(t, a, files, NewCache())
	hb := mustHash(t, b, []string{"x/b.go", "x/a.go"}, NewCache())
	if ha != hb {
		t.Fatalf("hashes differ: %s vs %s", ha, hb)
	}
	if !strings.HasPrefix(ha, "sha256:") {
		t.Fatalf("missing prefix: %s", ha)
	}
	if files[0] != "x/a.go" {
		t.Fatal("input mutated")
	}
	in := []string{"x/b.go", "x/a.go"}
	mustHash(t, a, in, NewCache())
	if in[0] != "x/b.go" {
		t.Fatal("input mutated")
	}
}

func TestSourceHashChangesOnContent(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "one")
	h1 := mustHash(t, r, []string{"a.go"}, NewCache())
	testutil.WriteFile(t, r, "a.go", "three")
	h2 := mustHash(t, r, []string{"a.go"}, NewCache())
	if h1 == h2 {
		t.Fatal("hash did not change")
	}
}

func TestSourceHashChangesOnRename(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "same")
	testutil.WriteFile(t, r, "b.go", "same")
	if mustHash(t, r, []string{"a.go"}, NewCache()) == mustHash(t, r, []string{"b.go"}, NewCache()) {
		t.Fatal("hash ignores path")
	}
}

func TestSourceHashMissingFile(t *testing.T) {
	if _, err := SourceHash(t.TempDir(), []string{"nope.go"}, nil, NewCache()); err == nil {
		t.Fatal("expected error")
	}
}

func TestCacheAvoidsRead(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "content")
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	e, ok := c.entries["a.go"]
	if !ok {
		t.Fatal("cache not populated")
	}
	e.Sum = "x"
	c.entries["a.go"] = e
	want := "sha256:" + hex256("a.go"+"\x00"+"x"+"\n")
	if got := mustHash(t, r, []string{"a.go"}, c); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCacheInvalidatedOnChange(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "content")
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	testutil.WriteFile(t, r, "a.go", "content-longer")
	want := "sha256:" + hex256("a.go"+"\x00"+hex256("content-longer")+"\n")
	if got := mustHash(t, r, []string{"a.go"}, c); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "content")
	makeOld(t, r, "a.go") // not racy: kept by LoadCache
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(r)
	if err != nil || !reflect.DeepEqual(got.entries, c.entries) {
		t.Fatalf("got %+v err %v want %+v", got, err, c)
	}
}

func TestLoadCacheMissingOrCorrupt(t *testing.T) {
	r := t.TempDir()
	if c, err := LoadCache(r); err != nil || len(c.entries) != 0 {
		t.Fatalf("missing: %v %v", c, err)
	}
	testutil.WriteFile(t, r, ".auto-context/cache.json", "{not json")
	c, err := LoadCache(r)
	if err == nil || len(c.entries) != 0 {
		t.Fatalf("corrupt: entries=%v err=%v, want empty and an error", c.entries, err)
	}
	c.entries["a"] = Entry{} // must be usable
	// A cache of another version (sums made without CRLF normalization) is dropped.
	testutil.WriteFile(t, r, ".auto-context/cache.json", `{"entries":{"a.go":{"sum":"x"}}}`)
	if c, err := LoadCache(r); err != nil || len(c.entries) != 0 {
		t.Errorf("old version kept: %v %v", c.entries, err)
	}
}

// CRLF and LF checkouts of the same file hash the same.
func TestSourceHashNormalizesLineEndings(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	testutil.WriteFile(t, a, "x.go", "package x\nfunc F() {}\n")
	testutil.WriteFile(t, b, "x.go", "package x\r\nfunc F() {}\r\n")
	if ha, hb := mustHash(t, a, []string{"x.go"}, NewCache()), mustHash(t, b, []string{"x.go"}, NewCache()); ha != hb {
		t.Errorf("LF %s != CRLF %s", ha, hb)
	}
	// A lone CR, and one at a chunk boundary, are kept.
	c := t.TempDir()
	testutil.WriteFile(t, c, "x.go", "a\rb\r")
	testutil.WriteFile(t, a, "y.go", "a\nb\n")
	if mustHash(t, c, []string{"x.go"}, NewCache()) == mustHash(t, a, []string{"y.go"}, NewCache()) {
		t.Error("lone CR treated as a line break")
	}
}

func TestLFWriterBoundary(t *testing.T) {
	var out strings.Builder
	w := &lfWriter{w: &out}
	for _, chunk := range []string{"a\r", "\nb\r", "c\r"} {
		w.Write([]byte(chunk))
	}
	w.flush()
	if out.String() != "a\nb\rc\r" {
		t.Errorf("got %q", out.String())
	}
}

// Extra lines (the unit list of _root) change the hash.
func TestSourceHashExtra(t *testing.T) {
	r := t.TempDir()
	if h1, _ := SourceHash(r, nil, []string{"a"}, NewCache()); h1 == mustHash(t, r, nil, NewCache()) {
		t.Error("extra ignored")
	}
}

// Save skips the write when nothing changed.
func TestSaveOnlyWhenDirty(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "x")
	makeOld(t, r, "a.go")
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r, ".auto-context", "cache.json")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatal(err)
	}
	c2, _ := LoadCache(r)
	mustHash(t, r, []string{"a.go"}, c2)
	if err := c2.Save(r); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.ModTime().After(past.Add(time.Minute)) {
		t.Error("unchanged cache rewritten")
	}
}

// Prune drops entries of files no longer tracked.
func TestPrune(t *testing.T) {
	c := &Cache{entries: map[string]Entry{"a.go": {}, "gone.go": {}}}
	c.Prune([]string{"a.go", "b.go"})
	if _, ok := c.entries["gone.go"]; ok || len(c.entries) != 1 {
		t.Errorf("entries = %v", c.entries)
	}
}

// A corrupt cache file is replaced on the next save even if nothing was hashed.
func TestCorruptCacheIsRewritten(t *testing.T) {
	r := t.TempDir()
	if err := os.MkdirAll(filepath.Join(r, ".auto-context"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r, ".auto-context", "cache.json")
	if err := os.WriteFile(p, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCache(r)
	if err == nil {
		t.Fatal("no error for a corrupt cache")
	}
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadCache(r); err != nil || c.Len() != 0 {
		t.Errorf("after save: %v %v", c.Len(), err)
	}
}

// The fast CRLF path matches a byte-by-byte reference for any chunking.
func TestLFWriterMatchesReference(t *testing.T) {
	ref := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	inputs := []string{"", "\r", "\n", "\r\n", "\r\r\n", "a\rb", "a\r\nb\r", "\r\r\r", "x\r\n\r\ny\r"}
	for _, in := range inputs {
		for size := 1; size <= len(in)+1; size++ {
			var out strings.Builder
			w := &lfWriter{w: &out}
			for i := 0; i < len(in); i += size {
				if _, err := w.Write([]byte(in[i:min(i+size, len(in))])); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.flush(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != ref(in) {
				t.Errorf("%q in chunks of %d: got %q, want %q", in, size, got, ref(in))
			}
		}
	}
}

// With trusted stats, a cached file is not touched on disk.
func TestUseStatsSkipsLstat(t *testing.T) {
	r := t.TempDir()
	if err := os.WriteFile(filepath.Join(r, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	h1 := mustHash(t, r, []string{"a.go"}, c)
	e := c.entries["a.go"]
	if err := os.Remove(filepath.Join(r, "a.go")); err != nil {
		t.Fatal(err)
	}
	c.UseStats(func(string) (int64, int64, bool) { return e.MtimeNs, e.Size, true })
	if h2 := mustHash(t, r, []string{"a.go"}, c); h2 != h1 {
		t.Errorf("hash changed: %s %s", h1, h2)
	}
}

// Prewarm fills the cache with the same sums SourceHash computes cold.
func TestPrewarmMatchesColdHash(t *testing.T) {
	r := t.TempDir()
	var files []string
	stats := map[string][2]int64{}
	for i := range 20 {
		name := fmt.Sprintf("f%d.go", i)
		p := filepath.Join(r, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("x\r\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Lstat(p)
		stats[name] = [2]int64{fi.ModTime().UnixNano(), fi.Size()}
		files = append(files, name)
	}
	cold := mustHash(t, r, files, NewCache())
	c := NewCache()
	c.UseStats(func(rel string) (int64, int64, bool) { s, ok := stats[rel]; return s[0], s[1], ok })
	saved := 0
	c.Prewarm(r, files, func() { saved++ }, 0)
	if c.Len() != len(files) || saved == 0 {
		t.Fatalf("entries=%d saves=%d", c.Len(), saved)
	}
	for _, f := range files {
		if err := os.Remove(filepath.Join(r, f)); err != nil {
			t.Fatal(err)
		}
	}
	// Every sum now comes from the cache.
	if warm := mustHash(t, r, files, c); warm != cold {
		t.Errorf("warm %s != cold %s", warm, cold)
	}
}

// A size change with the same mtime is a change.
func TestCacheSeesSizeChangeWithSameMtime(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "a.go")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Lstat(p)
	c := NewCache()
	h1 := mustHash(t, r, []string{"a.go"}, c)
	if err := os.WriteFile(p, []byte("three"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if h2 := mustHash(t, r, []string{"a.go"}, c); h2 == h1 {
		t.Error("size change not seen")
	}
}

// With trusted stats, a same-size edit with a new mtime is re-hashed.
func TestUseStatsSeesMtimeChangeWithSameSize(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "a.go")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	h1 := mustHash(t, r, []string{"a.go"}, c)
	e := c.entries["a.go"]
	if err := os.WriteFile(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Unix(0, e.MtimeNs).Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	c.UseStats(func(string) (int64, int64, bool) { return later.UnixNano(), e.Size, true })
	if h2 := mustHash(t, r, []string{"a.go"}, c); h2 == h1 {
		t.Error("same-size edit with a new mtime not re-hashed")
	}
}

// Documents the design: UseStats trusts the given stat, so a same-size edit
// whose mtime is unchanged keeps the cached sum (an mtime/size cache).
func TestUseStatsTrustsUnchangedStat(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "a.go")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	h1 := mustHash(t, r, []string{"a.go"}, c)
	e := c.entries["a.go"]
	if err := os.WriteFile(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.UseStats(func(string) (int64, int64, bool) { return e.MtimeNs, e.Size, true })
	if h2 := mustHash(t, r, []string{"a.go"}, c); h2 != h1 {
		t.Error("trusted unchanged stat did not hit the cache")
	}
}

// Racily clean (as in git): a same-size rewrite within the mtime tick in which
// the file was hashed keeps mtime and size, so an entry hashed that close to
// its mtime is not trusted by a later run.
func TestRacyEntryRehashedNextRun(t *testing.T) {
	for _, stats := range []bool{false, true} {
		r := t.TempDir()
		p := filepath.Join(r, "a.go")
		if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Lstat(p)
		c := NewCache()
		h1 := mustHash(t, r, []string{"a.go"}, c)
		if err := c.Save(r); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("two"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil { // same tick
			t.Fatal(err)
		}
		c2, err := LoadCache(r)
		if err != nil {
			t.Fatal(err)
		}
		if stats {
			c2.UseStats(func(string) (int64, int64, bool) { return fi.ModTime().UnixNano(), fi.Size(), true })
		}
		if h2 := mustHash(t, r, []string{"a.go"}, c2); h2 == h1 {
			t.Errorf("stats=%v: racy entry trusted after reload", stats)
		}
	}
}

// An entry hashed well after its mtime is trusted across runs.
func TestOldEntryTrustedNextRun(t *testing.T) {
	r := t.TempDir()
	if err := os.WriteFile(filepath.Join(r, "a.go"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeOld(t, r, "a.go")
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	c2, _ := LoadCache(r)
	if c2.Len() != 1 {
		t.Fatalf("entries = %d", c2.Len())
	}
}
