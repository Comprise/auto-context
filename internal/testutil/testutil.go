// Package testutil holds helpers shared by tests; import it from _test.go files only.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var scrubbed = []string{
	"CLAUDE_PROJECT_DIR", "CLAUDE_PLUGIN_ROOT", "CLAUDE_PLUGIN_DATA",
	"PLUGIN_ROOT", "PLUGIN_DATA", "CODEX_HOME",
}

// ScrubEnv removes the agent and GIT_* variables of the developer's session
// and pins a git identity, so tests behave the same everywhere. HOME is left
// alone: the root lookup and the shim depend on it, and tests set it themselves.
func ScrubEnv() {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	for _, k := range scrubbed {
		os.Unsetenv(k)
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "t",
		"GIT_AUTHOR_EMAIL":    "t@t",
		"GIT_COMMITTER_NAME":  "t",
		"GIT_COMMITTER_EMAIL": "t@t",
	} {
		os.Setenv(k, v)
	}
}

// Main is the body of TestMain: scrub the environment, then run the tests.
func Main(m *testing.M) {
	ScrubEnv()
	os.Exit(m.Run())
}

// WriteFile writes content to root/rel (rel uses "/"), creating directories.
func WriteFile(t testing.TB, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// RequireGit skips the test when git is not installed.
func RequireGit(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// Git runs `git -C dir args...` and returns its stdout; a failure is fatal.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	RequireGit(t)
	var stderr strings.Builder
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s%s", args, err, out, stderr.String())
	}
	return string(out)
}
