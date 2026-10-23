package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"auto-context/internal/fsutil"
)

// Lock excludes a second holder until the first unlocks; the second gives up
// after Wait.
func TestLockExcludes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	unlock, ok, err := Lock(p)
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	start := time.Now()
	if _, ok, err := Lock(p); ok || err != nil {
		t.Fatalf("second lock taken: %v %v", ok, err)
	}
	if d := time.Since(start); d < Wait || d > Wait+time.Second {
		t.Errorf("gave up after %v, want about %v", d, Wait)
	}
	unlock()
	unlock, ok, err = Lock(p)
	if err != nil || !ok {
		t.Fatalf("relock: %v %v", ok, err)
	}
	unlock()
}

// A lock path that cannot be opened is an error, not a held lock.
func TestLockError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing", "x.lock")
	if _, ok, err := Lock(p); ok || err == nil {
		t.Errorf("ok=%v err=%v, want an error", ok, err)
	}
}

// A symlink at the lock path (committed by a hostile repo) is refused: the
// lock would otherwise create, lock or remove the link's target.
func TestLockRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.lock")
	if err := os.Symlink("victim", p); err != nil {
		t.Skip("no symlinks:", err)
	}
	if _, ok, err := Lock(p); ok || !errors.Is(err, fsutil.ErrNotRegular) {
		t.Errorf("ok=%v err=%v, want ErrNotRegular", ok, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "victim")); err == nil {
		t.Error("link target created")
	}
}

// The O_EXCL fallback lock excludes a second holder, gives up after
// Wait, and replaces a stale lock file.
func TestLockExcl(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.lock")
	unlock, ok, err := lockExcl(p)
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, ok, err := lockExcl(p); ok || err != nil {
		t.Fatalf("second lock taken: %v %v", ok, err)
	}
	unlock()
	unlock, ok, err = lockExcl(p)
	if err != nil || !ok {
		t.Fatalf("relock: %v %v", ok, err)
	}
	_ = unlock // left behind, as by a killed hook
	old := time.Now().Add(-2 * staleAfter)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := lockExcl(p); !ok || err != nil {
		t.Fatalf("stale lock kept: %v %v", ok, err)
	}
}

// A stale lock replaced by a fresh one between the staleness check and the
// removal must not be removed: the new holder would lose its lock.
func TestStaleLockNotStolen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.lock")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	fired := false
	beforeRemove = func() {
		if fired {
			return
		}
		fired = true
		tmp := path + ".new"
		if err := os.WriteFile(tmp, nil, 0o644); err != nil {
			t.Error(err)
		}
		if err := os.Rename(tmp, path); err != nil { // fresh lock takes the place
			t.Error(err)
		}
	}
	defer func() { beforeRemove = nil }()
	_, ok, err := lockExcl(path)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want the fresh lock respected", ok, err)
	}
	if st, err := os.Stat(path); err != nil || time.Since(st.ModTime()) > time.Minute {
		t.Fatalf("fresh lock was removed: %v", err)
	}
}
