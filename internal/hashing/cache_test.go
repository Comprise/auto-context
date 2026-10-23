package hashing

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"auto-context/internal/filelock"
	"auto-context/internal/testutil"
)

// cacheFileName is the on-disk name of the current cache version.
const cacheFileName = "cache.v5.bin"

func readCacheFile(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".auto-context", cacheFileName))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The cache is a binary file with its version in the name and the header;
// what Save writes, LoadCache reads back (racy entries aside).
func TestCacheBinaryRoundTrip(t *testing.T) {
	r := t.TempDir()
	rels := []string{"a.go", "dir/b.go", "юникод/ж.go", strings.Repeat("d/", 60) + "deep.go", "big.bin"}
	for i, rel := range rels {
		testutil.WriteFile(t, r, rel, strings.Repeat("x", i*100))
		makeOld(t, r, rel)
	}
	testutil.WriteFile(t, r, "fresh.go", "racy") // just written: racy, dropped on load
	c := NewCache()
	mustHash(t, r, append([]string{"fresh.go"}, rels...), c)
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	data := readCacheFile(t, r)
	if !bytes.HasPrefix(data, []byte("acache 5\n")) {
		t.Fatalf("header = %q", data[:min(len(data), 16)])
	}
	got, err := LoadCache(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != len(rels) || got.Dirty() {
		t.Fatalf("loaded %d entries (dirty %v), want %d", got.Len(), got.Dirty(), len(rels))
	}
	for _, rel := range rels {
		if got.entries[rel] != c.entries[rel] {
			t.Errorf("%s: got %+v want %+v", rel, got.entries[rel], c.entries[rel])
		}
	}
	if _, ok := got.entries["fresh.go"]; ok {
		t.Error("racy entry kept")
	}
}

// A truncated or otherwise broken binary cache is reported and replaced.
func TestCacheBinaryCorrupt(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "a")
	makeOld(t, r, "a.go")
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	good := readCacheFile(t, r)
	ok := rec("a.go", 0)
	for name, data := range map[string]string{
		"truncated":          string(good[:len(good)-5]),
		"wrong header":       "acache 9\n" + string(good[len("acache 5\n"):]),
		"garbage":            "garbage",
		"unsorted":           cacheHeader + rec("b.go", 0) + ok,
		"duplicate":          cacheHeader + ok + ok,
		"unknown flag":       cacheHeader + rec("a.go", 2),
		"long path":          cacheHeader + rec(strings.Repeat("a", 64<<10+1), 0),
		"empty path":         cacheHeader + rec("", 0),
		"absolute":           cacheHeader + rec("/etc/passwd", 0),
		"dotdot":             cacheHeader + rec("../x.go", 0),
		"inner dotdot":       cacheHeader + rec("a/../../x.go", 0),
		"backslash":          cacheHeader + rec(`a\b.go`, 0),
		"nul":                cacheHeader + rec("a\x00b.go", 0),
		"overflowing length": cacheHeader + strings.Repeat("\xff", 11) + "\x01",
		"overflowing mtime":  cacheHeader + "\x04a.go" + strings.Repeat("\xff", 11) + "\x01",
		"huge length":        cacheHeader + "\xff\xff\xff\xff\x0f" + "a.go",
	} {
		testutil.WriteFile(t, r, ".auto-context/"+cacheFileName, data)
		c, err := LoadCache(r)
		if err == nil || c.Len() != 0 || !c.Dirty() {
			t.Errorf("%s: err=%v len=%d dirty=%v, want an error and an empty dirty cache", name, err, c.Len(), c.Dirty())
		}
	}
}

// rec is one cache record of path with flags (mtime 1, size 1, zero sum).
func rec(path string, flags byte) string {
	b := binary.AppendUvarint(nil, uint64(len(path)))
	b = append(b, path...)
	b = binary.AppendVarint(b, 1)
	b = binary.AppendUvarint(b, 1)
	b = append(b, make([]byte, 32)...)
	return string(append(b, flags))
}

// A record that parses is accepted (rec itself is sound).
func TestCacheRecordAccepted(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, ".auto-context/"+cacheFileName, cacheHeader+rec("a.go", 0)+rec("b/c.go", 1))
	if c, err := LoadCache(r); err != nil || c.Len() != 1 {
		t.Errorf("len=%d err=%v, want a.go (b/c.go is racy)", c.Len(), err)
	}
}

// Cache files of other versions are neither read nor touched (A-M4): two
// plugin versions in one tree keep their own caches.
func TestCacheIgnoresOtherVersions(t *testing.T) {
	r := t.TempDir()
	old := `{"version":4,"entries":{"a.go":{"mtime_ns":1,"size":1,"sum":"x"}}}`
	other := "acache 6\nwhatever"
	testutil.WriteFile(t, r, ".auto-context/cache.json", old)
	testutil.WriteFile(t, r, ".auto-context/cache.v6.bin", other)
	testutil.WriteFile(t, r, "a.go", "a")
	makeOld(t, r, "a.go")
	c, err := LoadCache(r)
	if err != nil || c.Len() != 0 || c.Dirty() {
		t.Fatalf("err=%v len=%d dirty=%v", err, c.Len(), c.Dirty())
	}
	mustHash(t, r, []string{"a.go"}, c)
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"cache.json": old, "cache.v6.bin": other} {
		if data, err := os.ReadFile(filepath.Join(r, ".auto-context", name)); err != nil || string(data) != want {
			t.Errorf("%s changed: %q %v", name, data, err)
		}
	}
	if c, err := LoadCache(r); err != nil || c.Len() != 1 {
		t.Errorf("own cache: len=%d err=%v", c.Len(), err)
	}
}

// Save merges the file on disk: entries another process saved meanwhile are
// kept while they still match their files; those of files changed, deleted
// or pruned since are not.
func TestCacheSaveMerges(t *testing.T) {
	r := t.TempDir()
	for _, rel := range []string{"mine.go", "theirs.go", "changed.go", "gone.go", "pruned.go"} {
		testutil.WriteFile(t, r, rel, rel)
		makeOld(t, r, rel)
	}
	mine, err := LoadCache(r)
	if err != nil {
		t.Fatal(err)
	}
	theirs := NewCache()
	mustHash(t, r, []string{"theirs.go", "changed.go", "gone.go", "pruned.go"}, theirs)
	if err := theirs.Save(r); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, r, "changed.go", "changed, longer")
	if err := os.Remove(filepath.Join(r, "gone.go")); err != nil {
		t.Fatal(err)
	}
	mustHash(t, r, []string{"mine.go"}, mine)
	mine.Prune([]string{"mine.go", "theirs.go", "changed.go", "gone.go"})
	if err := mine.Save(r); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(r)
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{"mine.go": true, "theirs.go": true, "changed.go": false, "gone.go": false, "pruned.go": false} {
		if _, ok := got.entries[rel]; ok != want {
			t.Errorf("%s in merged cache: %v, want %v", rel, ok, want)
		}
	}
}

// With trusted stats the merge keeps only entries of files in the tree that
// match its stats.
func TestCacheSaveMergesWithStats(t *testing.T) {
	r := t.TempDir()
	for _, rel := range []string{"a.go", "b.go", "untracked.go"} {
		testutil.WriteFile(t, r, rel, rel)
		makeOld(t, r, rel)
	}
	theirs := NewCache()
	mustHash(t, r, []string{"b.go", "untracked.go"}, theirs)
	if err := theirs.Save(r); err != nil {
		t.Fatal(err)
	}
	mine := NewCache()
	mine.UseStats(func(rel string) (int64, int64, bool) {
		if rel == "untracked.go" {
			return 0, 0, false
		}
		fi, err := os.Lstat(filepath.Join(r, rel))
		if err != nil {
			return 0, 0, false
		}
		return fi.ModTime().UnixNano(), fi.Size(), true
	})
	mustHash(t, r, []string{"a.go"}, mine)
	if err := mine.Save(r); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadCache(r)
	if _, ok := got.entries["b.go"]; !ok || got.Len() != 2 {
		t.Errorf("entries = %v, want a.go and b.go", got.entries)
	}
}

// Two processes that loaded the same cache and then hash different files
// both keep their work: Save merges under a lock (P-L6).
func TestCacheMergeConcurrentSave(t *testing.T) {
	r := t.TempDir()
	const n = 40
	var sets [2][]string
	for p := range 2 {
		for i := range n {
			rel := fmt.Sprintf("p%d/f%02d.go", p, i)
			testutil.WriteFile(t, r, rel, rel)
			makeOld(t, r, rel)
			sets[p] = append(sets[p], rel)
		}
	}
	var cmds [2]*exec.Cmd
	var outs [2]bytes.Buffer
	for p := range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCacheHelperProcess$")
		cmd.Env = append(os.Environ(), "AC_CACHE_HELPER=1", "AC_CACHE_ROOT="+r,
			"AC_CACHE_ID="+fmt.Sprint(p), "AC_CACHE_FILES="+strings.Join(sets[p], ","))
		cmd.Stdout, cmd.Stderr = &outs[p], &outs[p]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[p] = cmd
	}
	// Both have loaded the (empty) cache before either saves.
	deadline := time.Now().Add(30 * time.Second)
	for p := range 2 {
		for {
			if _, err := os.Stat(filepath.Join(r, fmt.Sprintf("ready%d", p))); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("helper %d not ready", p)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(r, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for p, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", p, err, outs[p].String())
		}
	}
	got, err := LoadCache(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range sets {
		for _, rel := range set {
			if _, ok := got.entries[rel]; !ok {
				t.Errorf("%s lost", rel)
			}
		}
	}
}

// TestCacheHelperProcess is a helper for TestCacheMergeConcurrentSave: it
// loads the cache, waits for the go signal, hashes its files and saves.
func TestCacheHelperProcess(t *testing.T) {
	if os.Getenv("AC_CACHE_HELPER") != "1" {
		return // not a helper run: a skip would trip the CI skip guard
	}
	r := os.Getenv("AC_CACHE_ROOT")
	c, err := LoadCache(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r, "ready"+os.Getenv("AC_CACHE_ID")), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(r, "go")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := SourceHash(context.Background(), r, strings.Split(os.Getenv("AC_CACHE_FILES"), ","), nil, c); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
}

// A lock held elsewhere (a hung session) delays Save by at most about
// filelock.Wait: it then saves unguarded.
func TestCacheSaveLockHeld(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "a")
	makeOld(t, r, "a.go")
	if err := os.MkdirAll(filepath.Join(r, ".auto-context"), 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, ok, err := filelock.Lock(filepath.Join(r, ".auto-context", cacheFileName+".lock"))
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer unlock()
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	start := time.Now()
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > filelock.Wait+2*time.Second {
		t.Errorf("Save took %v with the lock held", d)
	}
	if got, err := LoadCache(r); err != nil || got.Len() != 1 {
		t.Errorf("not saved: len=%d err=%v", got.Len(), err)
	}
}

// A symlinked lock file (committed by a hostile repo) is not used: locking
// through it would lock (flock) or, on unlock, delete (the O_EXCL fallback)
// the link's target. A link inside the state dir passes fsutil.Resolve, so
// the lexical path is checked: Save does not wait for the target's lock.
func TestCacheSaveRefusesSymlinkLock(t *testing.T) {
	r := t.TempDir()
	testutil.WriteFile(t, r, "a.go", "a")
	makeOld(t, r, "a.go")
	testutil.WriteFile(t, r, ".auto-context/config.yaml", "include: [a]\n")
	if err := os.Symlink("config.yaml", filepath.Join(r, ".auto-context", cacheFileName+".lock")); err != nil {
		t.Skip("no symlinks:", err)
	}
	unlock, ok, err := filelock.Lock(filepath.Join(r, ".auto-context", "config.yaml"))
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer unlock()
	c := NewCache()
	mustHash(t, r, []string{"a.go"}, c)
	start := time.Now()
	if err := c.Save(r); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > filelock.Wait/2 {
		t.Errorf("Save waited %v: it locked through the symlink", d)
	}
	if data, err := os.ReadFile(filepath.Join(r, ".auto-context", "config.yaml")); err != nil || string(data) != "include: [a]\n" {
		t.Errorf("link target changed: %q %v", data, err)
	}
	if got, err := LoadCache(r); err != nil || got.Len() != 1 {
		t.Errorf("not saved: len=%d err=%v", got.Len(), err)
	}
}

// CachePath names the file Save writes.
func TestCachePathExported(t *testing.T) {
	if want := filepath.Join("r", ".auto-context", cacheFileName); CachePath("r") != want {
		t.Errorf("CachePath = %q, want %q", CachePath("r"), want)
	}
}

// bigCache is a cache of n entries with paths like a repository's.
func bigCache(n int) *Cache {
	c := &Cache{entries: make(map[string]Entry, n), dirty: true}
	for i := range n {
		e := Entry{MtimeNs: 1_700_000_000_000_000_000 + int64(i), Size: int64(i * 37)}
		e.Sum[0], e.Sum[1], e.Sum[31] = byte(i), byte(i>>8), byte(i>>16)
		c.entries[fmt.Sprintf("pkg/mod%03d/sub/file_%06d.go", i%500, i)] = e
	}
	return c
}

// BenchmarkLoadSave200k: loading a 200k-entry cache must take well under
// 50 ms (P-M4).
func BenchmarkLoadSave200k(b *testing.B) {
	r := b.TempDir()
	c := bigCache(200_000)
	if err := c.Save(r); err != nil {
		b.Fatal(err)
	}
	b.Run("Load", func(b *testing.B) {
		for b.Loop() {
			got, err := LoadCache(r)
			if err != nil || got.Len() != 200_000 {
				b.Fatalf("len=%d err=%v", got.Len(), err)
			}
		}
	})
	b.Run("Save", func(b *testing.B) {
		for b.Loop() {
			c.dirty = true
			if err := c.Save(r); err != nil {
				b.Fatal(err)
			}
		}
	})
}
