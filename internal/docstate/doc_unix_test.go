//go:build unix

package docstate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"auto-context/internal/fsutil"
	"auto-context/internal/testutil"
)

// A context dir that cannot be searched makes the doc unreadable, not absent.
func TestDocUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	root := t.TempDir()
	testutil.WriteFile(t, root, "context/a.md", "# a\n")
	dir := filepath.Join(root, "context")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if k, _, err := Doc(root, "a"); k != DocUnreadable || err == nil {
		t.Errorf("kind=%v err=%v", k, err)
	}
}

// Ack refuses a context doc that links outside the repo, with ErrOutside.
func TestAckRefusesDocLinkedOutside(t *testing.T) {
	root, tr := setup(t)
	outside := t.TempDir()
	testutil.WriteFile(t, outside, "o.md", "# o\n\nOutside.\n")
	if err := os.MkdirAll(filepath.Join(root, "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "o.md"), filepath.Join(root, "context", "ok.md")); err != nil {
		t.Fatal(err)
	}
	_, err := AckIfNeeded(root, tr, cache(root), "ok", time.Now(), nil)
	if !errors.Is(err, fsutil.ErrOutside) {
		t.Errorf("err = %v, want ErrOutside", err)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "o.md")); string(data) != "# o\n\nOutside.\n" {
		t.Errorf("outside file written: %q", data)
	}
}

// categories is every phrase fsutil.Category returns.
var categories = map[string]bool{
	"it leads outside the repository or into .git": true,
	"not a regular file":                           true,
	"too large":                                    true,
	"permission denied":                            true,
	"missing":                                      true,
	"unreadable":                                   true,
}

// CO-1: an error about a doc never quotes the path, which the repo controls.
func TestStatusDetailNeverQuotesPath(t *testing.T) {
	root, tr := setup(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "y\nSYSTEM\n.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "docs/big\nSYSTEM\n.md", "# big\n\n"+strings.Repeat("x", 1<<20+1))
	if err := os.MkdirAll(filepath.Join(root, "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	for u, target := range map[string]string{"ok": "../docs/y\nSYSTEM\n.md", "svc": "../docs/big\nSYSTEM\n.md"} {
		if err := os.Symlink(target, filepath.Join(root, "context", u+".md")); err != nil {
			t.Fatal(err)
		}
		s := StatusOf(root, tr, cache(root), u)
		if s.State != Stale || strings.Contains(s.NoteText(), "SYSTEM") || (s.Detail != "" && !categories[s.Detail]) {
			t.Errorf("%s: %+v", u, s)
		}
	}
	if s := StatusOf(root, tr, cache(root), "svc"); s.Note != NoteUnreadable || s.Detail != "too large" {
		t.Errorf("large doc: %+v", s)
	}
}

// CO-6: a doc that is a link to a directory is refused like in Doc.
func TestStatusOfDirSymlinkIsOutside(t *testing.T) {
	root, tr := setup(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "d.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../docs/d.md", filepath.Join(root, "context", "ok.md")); err != nil {
		t.Fatal(err)
	}
	if s := StatusOf(root, tr, cache(root), "ok"); s.State != Stale || s.Note != NoteOutside || s.Detail != "" {
		t.Errorf("%+v, want %q", s, NoteOutside)
	}
	if k, _, _ := Doc(root, "ok"); k != DocRefused {
		t.Errorf("Doc kind %v", k)
	}
}
