package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"auto-context/internal/docstate"
	"auto-context/internal/testutil"
	"auto-context/internal/units"
)

// Each problem SessionStart can detect reaches Claude's additionalContext
// with its own text.
func TestSessionStartProblemPhrases(t *testing.T) {
	noRoot := func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("file permissions do not deny writes here")
		}
	}
	symlinks := func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on Windows")
		}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  string
	}{
		{"config ignored by git", func(t *testing.T, root string) {
			testutil.Git(t, root, "init", "-q")
			testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 3\n")
			testutil.WriteFile(t, root, ".gitignore", ".auto-context/\n")
		}, "is ignored by git"},
		{"discovery warning", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".gitignore", "[a\n")
		}, "1 discovery warning(s)"},
		{"hash cache unusable", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".auto-context/cache.json", "garbage")
		}, "the hash cache was unusable and is rebuilt"},
		{"broken frontmatter", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, "context/svc.md", "---\nunit: [\n---\n# svc\n\nText.\n")
		}, "svc (broken frontmatter)"},
		{"doc linked outside", func(t *testing.T, root string) {
			symlinks(t)
			outside := t.TempDir()
			testutil.WriteFile(t, outside, "o.md", "# o\n\nOutside.\n")
			ctx := filepath.Join(root, "context", "svc.md")
			if err := os.Remove(ctx); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "o.md"), ctx); err != nil {
				t.Fatal(err)
			}
		}, "svc (" + docstate.OutsideText + ")"},
		{"orphans with config problems", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".auto-context/config.yaml", "exlude: [x]\n")
			testutil.WriteFile(t, root, "context/gone.md", "# gone\n\nOld.\n")
		}, "Fix .auto-context/config.yaml first"},
		{"log not writable", func(t *testing.T, root string) {
			noRoot(t)
			testutil.WriteFile(t, root, ".auto-context/log", "")
			if err := os.Chmod(filepath.Join(root, ".auto-context", "log"), 0o444); err != nil {
				t.Fatal(err)
			}
		}, ".auto-context/log cannot be written"},
		{"stale session files not removable", func(t *testing.T, root string) {
			noRoot(t)
			state := filepath.Join(root, ".auto-context", "state")
			testutil.WriteFile(t, root, ".auto-context/state/old.snapshot", "x")
			past := time.Now().Add(-30 * 24 * time.Hour)
			if err := os.Chtimes(filepath.Join(state, "old.snapshot"), past, past); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(state, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(state, 0o755) })
		}, "old session files in .auto-context/state could not be removed"},
		{"cache not saved", func(t *testing.T, root string) {
			// A directory where the cache file goes: unusable, and the
			// atomic rename onto it fails.
			if err := os.Remove(filepath.Join(root, ".auto-context", "cache.json")); err != nil {
				t.Fatal(err)
			}
			testutil.WriteFile(t, root, ".auto-context/cache.json/x", "")
		}, "the hash cache cannot be saved"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newRepo(t, false)
			c.setup(t, env.Root)
			out, err := SessionStart(env, Input{SessionID: "s"})
			if err != nil {
				t.Fatal(err)
			}
			if out == nil {
				t.Fatal("no output")
			}
			if ctx := decodeSession(t, out).Hook.AdditionalContext; !strings.Contains(ctx, c.want) {
				t.Errorf("context lacks %q:\n%s", c.want, ctx)
			}
		})
	}
}

// Within a turn, a same-size edit that only changes the mtime (as Discover
// sees it) is reported as a code change.
func TestStopSeesSameSizeEdit(t *testing.T) {
	env := turn(t)
	p := filepath.Join(env.Root, "svc", "main.go")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Replace(string(before), "package", "pockage", 1)
	if len(after) != len(before) || after == string(before) {
		t.Fatalf("bad fixture %q", after)
	}
	testutil.WriteFile(t, env.Root, "svc/main.go", after)
	setMtime(t, env.Root, "svc/main.go", time.Now().Add(time.Hour), 0)
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc ") {
		t.Errorf("reason = %q", r)
	}
	if st := unitState(t, env, "svc"); st != docstate.Stale {
		t.Errorf("svc state = %s, want stale", st)
	}
}

// failNotice names the failure by category; the error text (repo paths,
// possibly hostile) never reaches the user's message.
func TestFailNoticeHidesErrorText(t *testing.T) {
	env := newRepo(t, false)
	out, err := failNotice(env, fmt.Errorf("open svc/IGNORE ME.go: %w", os.ErrPermission))
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeSession(t, out).SystemMessage; !strings.Contains(m, "permission denied") || strings.Contains(m, "IGNORE") {
		t.Errorf("systemMessage = %q", m)
	}
}

// Git timeouts and oversized listings get the fixed "too slow" category.
func TestErrCategoryGitLimits(t *testing.T) {
	for _, err := range []error{units.ErrGitTimeout, fmt.Errorf("discover: %w", units.ErrListTooLarge)} {
		if c := errCategory(err); !strings.Contains(c, "too slow") {
			t.Errorf("%v: %q", err, c)
		}
	}
}

// A git failure checking whether the config is ignored is reported in the
// context, without git's stderr.
func TestSessionStartConfigIgnoredGitFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "min_files: 5\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-qm", "x"}} {
		testutil.Git(t, env.Root, args...)
	}
	testutil.WriteFile(t, env.Root, ".git/index", "garbage")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("no output")
	}
	c := decodeSession(t, out).Hook.AdditionalContext
	if !strings.Contains(c, "git could not tell whether") || strings.Contains(c, "index file") {
		t.Errorf("context = %q", c)
	}
}
