package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/filestat"
	"auto-context/internal/testutil"
	"auto-context/internal/units"
)

func TestDiffDetectsAll(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "same.go", "same")
	testutil.WriteFile(t, root, "content.go", "aaaa")
	testutil.WriteFile(t, root, "size.go", "aa")
	testutil.WriteFile(t, root, "gone.go", "x")
	testutil.WriteFile(t, root, "old.go", "y")
	all := []string{"same.go", "content.go", "size.go", "gone.go", "old.go"}
	before, err := take(root, all)
	if err != nil {
		t.Fatal(err)
	}

	testutil.WriteFile(t, root, "content.go", "bbbb")
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(filepath.Join(root, "content.go"), future, future); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "size.go", "aaaaaa")
	os.Remove(filepath.Join(root, "gone.go"))
	if err := os.Rename(filepath.Join(root, "old.go"), filepath.Join(root, "new.go")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "fresh.go", "z")
	after, err := take(root, []string{"same.go", "content.go", "size.go", "new.go", "fresh.go", "gone.go"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"content.go", "fresh.go", "gone.go", "new.go", "old.go", "size.go"}
	if got := Diff(before, after); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := Diff(nil, nil); len(got) != 0 {
		t.Fatalf("nil diff: %v", got)
	}
	if got := Diff(nil, after); len(got) != len(after) {
		t.Fatalf("nil before: %v", got)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "a/b.go", "hello")
	s, err := take(root, []string{"a/b.go", "missing.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 {
		t.Fatalf("missing file must be skipped: %v", s)
	}
	if _, err := LoadSnapshot(root, "sess"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
	if err := s.Save(root, "sess"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSnapshot(root, "sess")
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("got %v %v want %v", got, err, s)
	}
}

func TestPathSanitized(t *testing.T) {
	root := t.TempDir()
	p := SnapshotPath(root, "../x")
	dir := filepath.Join(root, ".auto-context", "state")
	if filepath.Dir(p) != dir || !strings.HasSuffix(p, ".snapshot") {
		t.Fatalf("bad path %s", p)
	}
	if SanitizeID("") != "_" || SanitizeID("a-b_C9") != "a-b_C9" {
		t.Fatal("sanitize")
	}
}

// take stats every tracked file, as Discover does.
func take(root string, tracked []string) (Snapshot, error) {
	m := make(map[string]filestat.Stat, len(tracked))
	for _, rel := range tracked {
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		m[rel] = filestat.Of(fi)
	}
	return FromMap(m), nil
}

func BenchmarkTake(b *testing.B) {
	root := b.TempDir()
	for d := 0; d < 500; d++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%03d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < 100; f++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.go", f)), []byte("package x\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	cfg := config.Default()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree, err := units.DiscoverContext(context.Background(), root, cfg)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := take(root, tree.Tracked()); err != nil {
			b.Fatal(err)
		}
	}
}

// Distinct session ids never share state files, and long ids stay short.
func TestSanitizeIDDistinct(t *testing.T) {
	a, b := SanitizeID("a/b"), SanitizeID("a_b")
	if a == b {
		t.Errorf("a/b and a_b collide: %q", a)
	}
	if long := SanitizeID(strings.Repeat("x", 500)); len(long) > 80 {
		t.Errorf("long id gives %d-byte name", len(long))
	}
}

// Old session files left by interrupted turns are removed.
func TestRemoveStale(t *testing.T) {
	root := t.TempDir()
	old, fresh := SnapshotPath(root, "old"), SnapshotPath(root, "fresh")
	for _, id := range []string{"old", "fresh"} {
		if err := (Snapshot{}).Save(root, id); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStale(root, 7*24*time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old snapshot kept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh snapshot removed: %v", err)
	}
}

// Paths with spaces and newlines survive; a file without the header is an error.
func TestSnapshotFormat(t *testing.T) {
	root := t.TempDir()
	s := FromMap(map[string]filestat.Stat{"a b/c.go": {MtimeNs: 1, Size: 2}, "x\ny.go": {MtimeNs: -3, Size: 0}})
	if err := s.Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSnapshot(root, "s"); err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("got %v %v", got, err)
	}
	if err := os.WriteFile(SnapshotPath(root, "old"), []byte(`{"a.go":{"m":1,"s":2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(root, "old"); err == nil {
		t.Error("headerless snapshot accepted")
	}
}

// A truncated or non-numeric record is an error naming the file.
func TestSnapshotCorruptRecords(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"truncated": "1 2 a.go", "number": "x 2 a.go\x00", "fields": "12a.go\x00"} {
		if err := (Snapshot{}).Save(root, name); err != nil {
			t.Fatal(err)
		}
		p := SnapshotPath(root, name)
		if err := os.WriteFile(p, []byte(snapshotHeader+body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSnapshot(root, name); err == nil || !strings.Contains(err.Error(), p) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// A non-numeric size field is an error, not a zero size.
func TestSnapshotBadSize(t *testing.T) {
	root := t.TempDir()
	if err := (Snapshot{}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SnapshotPath(root, "s"), []byte(snapshotHeader+"1 2 ok.go\x001 big a.go\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadSnapshot(root, "s"); err == nil {
		t.Errorf("bad size accepted: %v", s)
	}
}

// A stale directory in the state dir is neither removed nor entered, and does
// not stop the removal of stale files.
func TestRemoveStaleSkipsDirectories(t *testing.T) {
	root := t.TempDir()
	if err := (Snapshot{}).Save(root, "old"); err != nil {
		t.Fatal(err)
	}
	old := SnapshotPath(root, "old")
	dir := filepath.Join(filepath.Dir(old), "somedir")
	testutil.WriteFile(t, root, ".auto-context/state/somedir/inner.snapshot", "x")
	inner := filepath.Join(dir, "inner.snapshot")
	past := time.Now().Add(-30 * 24 * time.Hour)
	for _, p := range []string{old, inner, dir} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveStale(root, 7*24*time.Hour, time.Now()); err != nil {
		t.Fatalf("RemoveStale: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("stale file kept: %v", err)
	}
	if _, err := os.Stat(inner); err != nil {
		t.Errorf("file inside a directory touched: %v", err)
	}
}

func TestSnapshotTime(t *testing.T) {
	root := t.TempDir()
	if err := (Snapshot{}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(SnapshotPath(root, "s"), t0, t0); err != nil {
		t.Fatal(err)
	}
	if got, err := SnapshotTime(root, "s"); err != nil || got != t0.UnixNano() {
		t.Errorf("got %d, %v", got, err)
	}
	if _, err := SnapshotTime(root, "none"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
}

// Snapshots go after 24h even with a longer maxAge; a journal waits for maxAge.
func TestRemoveStaleSnapshots24h(t *testing.T) {
	root := t.TempDir()
	if err := (Snapshot{}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	if err := (Journal{}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{SnapshotPath(root, "s"), journalPath(root, "s")} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveStale(root, 7*24*time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SnapshotPath(root, "s")); !os.IsNotExist(err) {
		t.Errorf("old snapshot kept: %v", err)
	}
	if _, err := os.Stat(journalPath(root, "s")); err != nil {
		t.Errorf("journal removed before maxAge: %v", err)
	}
}

// A snapshot of the same format written before snapshots were sorted (in
// map order, maybe with a path twice) loads sorted, the last record of a
// path winning, and diffs as before.
func TestSnapshotLoadsUnsorted(t *testing.T) {
	root := t.TempDir()
	if err := (Snapshot{}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	body := snapshotHeader + "1 1 z.go\x002 2 a.go\x003 3 m.go\x004 4 a.go\x00"
	if err := os.WriteFile(SnapshotPath(root, "s"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSnapshot(root, "s")
	if err != nil {
		t.Fatal(err)
	}
	want := Snapshot{{"a.go", filestat.Stat{MtimeNs: 4, Size: 4}}, {"m.go", filestat.Stat{MtimeNs: 3, Size: 3}}, {"z.go", filestat.Stat{MtimeNs: 1, Size: 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if st, ok := got.Get("m.go"); !ok || st.Size != 3 || got.Has("b.go") {
		t.Errorf("Get(m.go) = %v %v, Has(b.go) = %v", st, ok, got.Has("b.go"))
	}
	after := FromMap(map[string]filestat.Stat{"a.go": {MtimeNs: 4, Size: 4}, "b.go": {MtimeNs: 1, Size: 1}, "m.go": {MtimeNs: 9, Size: 3}})
	if d := Diff(got, after); !reflect.DeepEqual(d, []string{"b.go", "m.go", "z.go"}) {
		t.Errorf("Diff = %v", d)
	}
}

// FromTree keeps the tree's order and skips files without stat data.
func TestFromTree(t *testing.T) {
	stats := map[string]filestat.Stat{"a.go": {MtimeNs: 1}, "b/c.go": {MtimeNs: 2}}
	got := FromTree([]string{"a.go", "b/c.go", "gone.go"}, stats)
	want := Snapshot{{"a.go", filestat.Stat{MtimeNs: 1}}, {"b/c.go", filestat.Stat{MtimeNs: 2}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Diff normalizes a hand-built, unsorted snapshot (into a copy) instead of
// merging it wrongly.
func TestDiffUnsortedInput(t *testing.T) {
	before := Snapshot{{"b.go", filestat.Stat{MtimeNs: 1}}, {"a.go", filestat.Stat{MtimeNs: 1}}}
	after := FromMap(map[string]filestat.Stat{"a.go": {MtimeNs: 1}, "b.go": {MtimeNs: 2}})
	if d := Diff(before, after); !reflect.DeepEqual(d, []string{"b.go"}) {
		t.Errorf("Diff = %v", d)
	}
	if before[0].Path != "b.go" {
		t.Error("Diff reordered its input")
	}
}
