//go:build unix

package docstate

import (
	"errors"
	"os"
	"path/filepath"
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
	err := Ack(root, tr, cache(root), "ok", time.Now(), nil)
	if !errors.Is(err, fsutil.ErrOutside) {
		t.Errorf("err = %v, want ErrOutside", err)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "o.md")); string(data) != "# o\n\nOutside.\n" {
		t.Errorf("outside file written: %q", data)
	}
}
