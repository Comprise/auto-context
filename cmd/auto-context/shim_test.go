package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"auto-context/internal/testutil"
)

// plugin lays out a plugin dir with the real shim and, if script is not
// empty, a fake binary for this platform running script.
func plugin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("shim test needs a POSIX sh")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("unsupported arch for the shim")
	}
	dir := t.TempDir()
	shim, err := os.ReadFile(filepath.Join("..", "..", "bin", "auto-context"))
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "bin/auto-context", string(shim))
	if script != "" {
		testutil.WriteFile(t, dir, "libexec/auto-context-"+runtime.GOOS+"-"+runtime.GOARCH, "#!/bin/sh\n"+script+"\n")
		if err := os.Chmod(filepath.Join(dir, "libexec", "auto-context-"+runtime.GOOS+"-"+runtime.GOARCH), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func shim(t *testing.T, dir, project string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(dir, "bin", "auto-context")}, args...)...)
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+project)
	cmd.Stdin = strings.NewReader("{}")
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func TestShimWithoutBinary(t *testing.T) {
	dir := plugin(t, "")
	active, inactive := t.TempDir(), t.TempDir()
	testutil.WriteFile(t, active, ".auto-context/config.yaml", "min_files: 1\n")

	code, out, _ := shim(t, dir, active, "hook", "session-start")
	if code != 0 || !strings.Contains(out, `"systemMessage":"auto-context: the binary`) {
		t.Errorf("active session-start: code=%d out=%q", code, out)
	}
	if code, out, _ := shim(t, dir, inactive, "hook", "session-start"); code != 0 || out != "" {
		t.Errorf("inactive session-start: code=%d out=%q", code, out)
	}
	if code, out, _ := shim(t, dir, active, "hook", "stop"); code != 0 || out != "" {
		t.Errorf("stop: code=%d out=%q", code, out)
	}
	if code, _, errs := shim(t, dir, active, "status"); code != 1 || !strings.Contains(errs, "was not found") {
		t.Errorf("status: code=%d stderr=%q", code, errs)
	}
}

// A crashing binary (a Go fatal error exits 2) never fails a hook, and the
// shim reports the exit through `hook-exit` so it reaches the log.
func TestShimHookCrashExitsZero(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	dir := plugin(t, `echo "$@" >> '`+calls+`'; exit 2`)
	if code, _, _ := shim(t, dir, t.TempDir(), "hook", "prompt-submit"); code != 0 {
		t.Errorf("hook exit = %d, want 0", code)
	}
	if data, _ := os.ReadFile(calls); !strings.Contains(string(data), "hook-exit prompt-submit 2") {
		t.Errorf("calls = %q", data)
	}
	if code, _, _ := shim(t, dir, t.TempDir(), "status"); code != 2 {
		t.Errorf("CLI exit = %d, want 2 passed through", code)
	}
}

// A session-start crash in an enabled project tells the user that tracking is
// off; partial output of the crashed binary is not passed on.
func TestShimSessionStartCrashNotice(t *testing.T) {
	dir := plugin(t, `[ "$1" = hook-exit ] && exit 0; echo '{"partial'; exit 2`)
	active := t.TempDir()
	testutil.WriteFile(t, active, "context/_root.md", "# root\n")
	code, out, _ := shim(t, dir, active, "hook", "session-start")
	if code != 0 || !strings.Contains(out, "session-start hook crashed") || strings.Contains(out, "partial") {
		t.Errorf("code=%d out=%q", code, out)
	}
	if _, out, _ := shim(t, dir, t.TempDir(), "hook", "session-start"); out != "" {
		t.Errorf("inactive project: out=%q", out)
	}
}

// The shim finds the project root as config.FindRoot does: a session in a
// subdirectory of an enabled repo gets the notice, one in a nested repo
// without markers does not.
func TestShimWithoutBinaryFindsRoot(t *testing.T) {
	dir := plugin(t, "")
	base := t.TempDir()
	testutil.WriteFile(t, base, "repo/context/_root.md", "# root\n")
	testutil.WriteFile(t, base, "repo/a/b/x.go", "package b\n")
	testutil.WriteFile(t, base, "repo/nested/y.go", "package y\n")
	for _, d := range []string{"repo/.git", "repo/nested/.git"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for sub, want := range map[string]bool{"repo/a/b": true, "repo/nested": false} {
		_, out, _ := shim(t, dir, filepath.Join(base, sub), "hook", "session-start")
		if got := strings.Contains(out, "docs tracking is off"); got != want {
			t.Errorf("%s: notice %v, want %v (%q)", sub, got, want, out)
		}
	}
}
