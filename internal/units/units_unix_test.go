//go:build unix

package units

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"auto-context/internal/config"
)

// A FIFO .gitignore must not block discovery.
func TestGitignoreFIFO(t *testing.T) {
	root := t.TempDir()
	mkfiles(t, root, "a.go")
	if err := syscall.Mkfifo(filepath.Join(root, ".gitignore"), 0o644); err != nil {
		t.Skip("mkfifo unsupported:", err)
	}
	done := make(chan struct{})
	go func() { discover(t, root, config.Default()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Discover blocked on a FIFO .gitignore")
	}
}

// An unreadable directory is skipped with a warning, not silently.
func TestUnreadableDirWarns(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	mkfiles(t, root, "a.go", "locked/b.go")
	if err := os.Chmod(filepath.Join(root, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "locked"), 0o755)
	tr := discover(t, root, config.Default())
	if w := strings.Join(tr.Warnings(), "\n"); !strings.Contains(w, "locked") {
		t.Errorf("warnings = %q", w)
	}
}

// Git mode: a listed file under a hostile directory that cannot be lstat'ed
// is warned about without quoting the directory name.
func TestGitWarningHidesHostilePath(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "p", hostileDir), 0o755); err != nil {
		t.Skip("file system rejects the name:", err)
	}
	mkfiles(t, root, "a.go", "p/"+hostileDir+"/x.go")
	gitInit(t, root)
	if out, err := exec.Command("git", "-C", root, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if err := os.Chmod(filepath.Join(root, "p"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "p"), 0o755) })
	tr := discover(t, root, config.Default())
	if !tr.viaGit {
		t.Fatal("git listing not used")
	}
	checkHostileWarnings(t, tr.Warnings())
}

// Warnings name repo-relative paths only, never the absolute root.
func TestWarningsHaveNoAbsolutePath(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root, outside := t.TempDir(), t.TempDir()
	mkfiles(t, root, "a.go", "locked/b.go", "sub/c.go")
	mkfiles(t, outside, "ignore")
	if err := os.Symlink(filepath.Join(outside, "ignore"), filepath.Join(root, "sub", ".gitignore")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := os.Chmod(filepath.Join(root, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "locked"), 0o755) })
	t.Setenv("PATH", t.TempDir()) // no git: walk mode
	w := discover(t, root, config.Default()).Warnings()
	if len(w) < 2 {
		t.Fatalf("warnings = %q, want two", w)
	}
	for _, s := range w {
		if strings.Contains(s, root) || strings.Contains(s, outside) || strings.Contains(s, os.TempDir()) {
			t.Errorf("warning has an absolute path: %q", s)
		}
	}
}
