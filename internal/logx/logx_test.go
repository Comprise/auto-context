package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"auto-context/internal/filelock"
)

func TestLogRotates(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log"), make([]byte, 1<<20+1), 0o644); err != nil {
		t.Fatal(err)
	}
	Errorf(root, "boom %d", 1)
	if _, err := os.Stat(filepath.Join(dir, "log.1")); err != nil {
		t.Fatalf("log.1 missing: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "log"))
	if strings.Count(string(got), "\n") != 1 || !strings.HasSuffix(string(got), " boom 1\n") {
		t.Fatalf("log = %q", got)
	}
}

func TestLogCreatesDir(t *testing.T) {
	root := t.TempDir()
	Errorf(root, "x")
	if _, err := os.Stat(filepath.Join(root, ".auto-context", "log")); err != nil {
		t.Fatal(err)
	}
}

// A cloned repo must not make the hook log append to a file outside itself.
func TestErrorfRefusesSymlinks(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "rc")
	if err := os.WriteFile(victim, []byte("ORIGINAL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileLink := t.TempDir()
	if err := os.Mkdir(filepath.Join(fileLink, ".auto-context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(fileLink, ".auto-context", "log")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	dirLink := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dirLink, ".auto-context")); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{fileLink, dirLink} {
		Errorf(root, "boom")
	}
	if data, _ := os.ReadFile(victim); string(data) != "ORIGINAL\n" {
		t.Errorf("victim = %q", data)
	}
	if _, err := os.Stat(filepath.Join(outside, "log")); !os.IsNotExist(err) {
		t.Errorf("log created outside: %v", err)
	}
}

// Each entry is one line: newlines and control characters from the message
// (e.g. quoted from an attacker's config) are replaced.
func TestErrorfSingleLine(t *testing.T) {
	root := t.TempDir()
	Errorf(root, "config: %s", "a\nline 2: `$(sh a)`\r\x1b[31m")
	data, err := os.ReadFile(filepath.Join(root, ".auto-context", "log"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSuffix(string(data), "\n")
	if strings.ContainsAny(got, "\n\r\x1b") {
		t.Errorf("entry not sanitized: %q", got)
	}
	if !strings.HasSuffix(got, "config: a line 2: `$(sh a)`  [31m") {
		t.Errorf("entry = %q", got)
	}
}

// When the log cannot be written (here: .auto-context links outside), the
// entry goes to stderr instead of vanishing.
func TestErrorfFallsBackToStderr(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".auto-context")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	var buf strings.Builder
	old := Stderr
	Stderr = &buf
	defer func() { Stderr = old }()
	Errorf(root, "boom %d", 1)
	if !strings.Contains(buf.String(), "auto-context: boom 1") {
		t.Errorf("stderr = %q", buf.String())
	}
}

func TestErrorfStripsFormatChars(t *testing.T) {
	root := t.TempDir()
	Errorf(root, "a\u202eb\u200bc")
	data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "log"))
	if strings.ContainsAny(string(data), "\u202e\u200b") {
		t.Errorf("format chars kept: %q", data)
	}
}

// log.1 linking outside the repo is never written, even on rotation.
func TestErrorfRefusesLinkedRotation(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "v")
	if err := os.WriteFile(victim, []byte("V"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log"), make([]byte, maxSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "log.1")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	var buf strings.Builder
	old := Stderr
	Stderr = &buf
	defer func() { Stderr = old }()
	Errorf(root, "x")
	if data, _ := os.ReadFile(victim); string(data) != "V" {
		t.Errorf("victim = %q", data)
	}
}

// Entries counts the rotated log too.
func TestEntriesReadsRotatedLog(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log.1"), []byte("2026-01-01T00:00:00Z old\nnot a log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Errorf(root, "new")
	if got, err := Entries(root); err != nil || len(got) != 2 {
		t.Errorf("entries = %v, %v", got, err)
	}
}

// A failed rotation is an entry of its own: the log would grow unnoticed.
func TestLogRotationFailureLogged(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs directory permissions")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log"), []byte(strings.Repeat("x", 1<<20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil { // the rename fails, appending still works
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	Errorf(root, "boom")
	if n, err := Entries(root); err != nil || len(n) != 2 {
		t.Errorf("entries = %d, %v", len(n), err)
	}
	// Later writes that fail to rotate again do not repeat the note.
	Errorf(root, "boom again")
	data, err := os.ReadFile(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), rotationFailed); n != 1 {
		t.Errorf("%d rotation-failure entries, want 1", n)
	}
	if n, err := Entries(root); err != nil || len(n) != 3 {
		t.Errorf("entries = %d, %v", len(n), err)
	}
}

// Writable writes nothing: no log appears, no probe stays.
func TestWritableLeavesNothing(t *testing.T) {
	root := t.TempDir()
	if err := Writable(root); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(filepath.Join(root, ".auto-context")); len(ents) != 0 {
		t.Errorf("left %v", ents)
	}
	Errorf(root, "x")
	if err := Writable(root); err != nil {
		t.Fatal(err)
	}
	if n, _ := Entries(root); len(n) != 1 {
		t.Errorf("entries = %d", len(n))
	}
}

// log.1 leading outside the repo makes the log unusable, as for Errorf.
func TestWritableRefusesLinkedRotation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "x"), filepath.Join(dir, "log.1")); err != nil {
		t.Skip(err)
	}
	if err := Writable(root); err == nil {
		t.Error("Writable accepted a log.1 outside the repo")
	}
}

// With the state dir present and no log, Writable probes and leaves the
// directory empty.
func TestWritableExistingDirNoLog(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Writable(root); err != nil {
		t.Fatal(err)
	}
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Errorf("dir holds %v (err %v)", ents, err)
	}
}

// A read-only state dir without a log is reported as not writable.
func TestWritableReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs directory permissions")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if err := Writable(root); err == nil {
		t.Error("Writable = nil for a read-only dir")
	}
}

// Parallel hooks rotating the same oversized log must not drop the rotated
// content: only one of them rotates, the others append.
func TestConcurrentRotationKeepsLog(t *testing.T) {
	const writers = 8
	for trial := range 30 {
		root := t.TempDir()
		dir := filepath.Join(root, ".auto-context")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		old := "2026-01-01T00:00:00Z OLD-MARKER\n"
		old += strings.Repeat("x", maxSize+1-len(old)) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "log"), []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := range writers {
			wg.Go(func() { Errorf(root, "w%d", i) })
		}
		wg.Wait()
		var all string
		for _, name := range []string{"log.1", "log"} {
			data, _ := os.ReadFile(filepath.Join(dir, name))
			all += string(data)
		}
		if !strings.Contains(all, "OLD-MARKER") {
			t.Fatalf("trial %d: rotated log lost", trial)
		}
		for i := range writers {
			if !strings.Contains(all, fmt.Sprintf(" w%d\n", i)) {
				t.Fatalf("trial %d: entry w%d lost", trial, i)
			}
		}
	}
}

// A rotation lock held elsewhere (a hung hook) delays a write by at most
// filelock.Wait: the entry is appended, the rotation is left for later.
func TestRotationLockHeldDoesNotHang(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log"), make([]byte, maxSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, ok, err := filelock.Lock(filepath.Join(dir, "log.lock"))
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer unlock()
	start := time.Now()
	Errorf(root, "late")
	if d := time.Since(start); d > filelock.Wait+time.Second {
		t.Errorf("Errorf took %v", d)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "log"))
	if !strings.HasSuffix(string(data), " late\n") {
		t.Error("entry not appended")
	}
}

// A symlinked log.lock (committed by a hostile repo) is not used: the
// rotation is refused instead of locking or deleting the link's target.
func TestRotationRefusesSymlinkLock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".auto-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log"), make([]byte, maxSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("include: [a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("config.yaml", filepath.Join(dir, "log.lock")); err != nil {
		t.Skip("no symlinks:", err)
	}
	// Hold the target's flock, so locking through the link would wait. The
	// Windows lock is an O_EXCL file at the path: an existing target cannot
	// be locked there, and the target's content check below is what
	// catches a lock that went through the link (its unlock deletes it).
	if runtime.GOOS != "windows" {
		unlock, ok, err := filelock.Lock(cfg)
		if err != nil || !ok {
			t.Fatalf("lock: %v %v", ok, err)
		}
		defer unlock()
	}
	start := time.Now()
	Errorf(root, "late")
	if d := time.Since(start); d > filelock.Wait/2 {
		t.Errorf("Errorf waited %v: it locked through the symlink", d)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "log"))
	if !strings.HasSuffix(string(data), " late\n") || !strings.Contains(string(data), rotationFailed) {
		t.Errorf("want the entry and a rotation failure, log tail %q", data[max(len(data)-200, 0):])
	}
	if data, err := os.ReadFile(cfg); err != nil || string(data) != "include: [a]\n" {
		t.Errorf("link target changed: %q %v", data, err)
	}
}

// In an active project without .auto-context the first entry creates the
// state dir together with its .gitignore.
func TestWriteCreatesStateDirGitignore(t *testing.T) {
	root := t.TempDir()
	Errorf(root, "x")
	if _, err := os.Stat(filepath.Join(root, ".auto-context", ".gitignore")); err != nil {
		t.Fatal(err)
	}
}
