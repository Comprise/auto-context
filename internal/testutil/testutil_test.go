package testutil

import (
	"os"
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
