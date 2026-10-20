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
// empty, a fake binary named name (under libexec) running script.
func plugin(t *testing.T, name, script string) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("shim test needs a POSIX sh")
	}
	dir := t.TempDir()
	shim, err := os.ReadFile(filepath.Join("..", "..", "bin", "auto-context"))
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "bin/auto-context", string(shim))
	if script != "" {
		testutil.WriteFile(t, dir, "libexec/"+name, "#!/bin/sh\n"+script+"\n")
		if err := os.Chmod(filepath.Join(dir, "libexec", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// hostBinary is the libexec name the shim picks on the machine running the test.
func hostBinary() string {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "auto-context-" + runtime.GOOS + "-unknown"
	}
	return "auto-context-" + runtime.GOOS + "-" + runtime.GOARCH
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
	dir := plugin(t, hostBinary(), "")
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
	dir := plugin(t, hostBinary(), `echo "$@" >> '`+calls+`'; exit 2`)
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
	dir := plugin(t, hostBinary(), `[ "$1" = hook-exit ] && exit 0; echo '{"partial'; exit 2`)
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
	dir := plugin(t, hostBinary(), "")
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

// The shim maps uname output to the libexec binary name. A fake uname first
// in PATH stands in for each platform.
func TestShimPlatformMapping(t *testing.T) {
	fake := t.TempDir()
	testutil.WriteFile(t, fake, "uname", "#!/bin/sh\ncase \"$1\" in -s) echo \"$FAKE_S\" ;; -m) echo \"$FAKE_M\" ;; esac\n")
	if err := os.Chmod(filepath.Join(fake, "uname"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, c := range []struct{ s, m, bin string }{
		{"Linux", "x86_64", "auto-context-linux-amd64"},
		{"Linux", "aarch64", "auto-context-linux-arm64"},
		{"Darwin", "arm64", "auto-context-darwin-arm64"},
		{"MINGW64_NT-10.0", "x86_64", "auto-context-windows-amd64.exe"},
	} {
		t.Run(c.bin, func(t *testing.T) {
			t.Setenv("FAKE_S", c.s)
			t.Setenv("FAKE_M", c.m)
			dir := plugin(t, c.bin, "echo ran")
			code, out, errs := shim(t, dir, t.TempDir(), "version")
			if code != 0 || strings.TrimSpace(out) != "ran" {
				t.Errorf("code=%d out=%q stderr=%q", code, out, errs)
			}
		})
	}
	t.Run("unknown arch", func(t *testing.T) {
		t.Setenv("FAKE_S", "Linux")
		t.Setenv("FAKE_M", "riscv64")
		dir := plugin(t, "", "")
		code, _, errs := shim(t, dir, t.TempDir(), "version")
		if code != 1 || !strings.Contains(errs, "binary for linux/unknown was not found") {
			t.Errorf("code=%d stderr=%q", code, errs)
		}
	})
}
