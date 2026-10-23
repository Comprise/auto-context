package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

func TestScrubEnv(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "/x")
	t.Setenv("GIT_DIR", "/nonexistent")
	t.Setenv("CODEX_HOME", "/y")
	ScrubEnv()
	for _, k := range []string{"CLAUDE_PROJECT_DIR", "GIT_DIR", "CODEX_HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s still set to %q", k, v)
		}
	}
	if got := os.Getenv("GIT_CONFIG_GLOBAL"); got != os.DevNull {
		t.Errorf("GIT_CONFIG_GLOBAL = %q", got)
	}
	if got := os.Getenv("GIT_AUTHOR_NAME"); got != "t" {
		t.Errorf("GIT_AUTHOR_NAME = %q", got)
	}
}

func TestGitCommits(t *testing.T) {
	d := t.TempDir()
	Git(t, d, "init", "-q")
	WriteFile(t, d, "a/b.txt", "x\n")
	Git(t, d, "add", "-A")
	Git(t, d, "commit", "-qm", "x")
	if out := Git(t, d, "log", "--format=%an"); out != "t\n" {
		t.Errorf("author = %q", out)
	}
}

func TestScrubEnvCeiling(t *testing.T) {
	RequireGit(t)
	old, had := os.LookupEnv("GIT_CEILING_DIRECTORIES")
	t.Cleanup(func() {
		if had {
			os.Setenv("GIT_CEILING_DIRECTORIES", old)
		} else {
			os.Unsetenv("GIT_CEILING_DIRECTORIES")
		}
	})
	outer := t.TempDir()
	Git(t, outer, "init", "-q")
	tmp := filepath.Join(outer, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	ScrubEnv()
	d := t.TempDir()
	out, err := exec.Command("git", "-C", d, "rev-parse", "--git-dir").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not a git repository") {
		t.Fatalf("git found the outer repo: err=%v out=%q", err, out)
	}
	// A repo created inside the temp dir is still found.
	Git(t, d, "init", "-q")
	WriteFile(t, d, "sub/f.txt", "x")
	Git(t, filepath.Join(d, "sub"), "rev-parse", "--git-dir")
}

func TestSymlinkCreatesParent(t *testing.T) {
	d := t.TempDir()
	WriteFile(t, d, "target.txt", "hi")
	link := filepath.Join(d, "a", "b", "link")
	Symlink(t, filepath.Join(d, "target.txt"), link)
	b, err := os.ReadFile(link)
	if err != nil || string(b) != "hi" {
		t.Fatalf("read through link: %q %v", b, err)
	}
}

func TestFakeGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script git")
	}
	FakeGit(t, "echo fake \"$1\"; exit 3")
	out, err := exec.Command("git", "x").Output()
	if string(out) != "fake x\n" {
		t.Errorf("out = %q", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
		t.Errorf("err = %v", err)
	}
}

// The developer's default global excludes ($XDG_CONFIG_HOME/git/ignore,
// read even with GIT_CONFIG_GLOBAL set) do not reach the tests.
func TestScrubEnvHidesGlobalExcludes(t *testing.T) {
	RequireGit(t)
	xdg := t.TempDir()
	WriteFile(t, xdg, "git/ignore", "*.zz\n")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	ScrubEnv()
	repo := t.TempDir()
	Git(t, repo, "init", "-q")
	WriteFile(t, repo, "a.zz", "x")
	if out := Git(t, repo, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(out, "a.zz") {
		t.Errorf("a.zz hidden by the developer's global excludes: %q", out)
	}
}
