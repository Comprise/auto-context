//go:build linux

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helperRunEnv = "AC_GITX_HELPER_DIR"

// TestHelperRun is the hook stand-in for TestRunGitDiesWithKilledHook: it
// runs a git that never ends. Outside a helper run it returns at once.
func TestHelperRun(t *testing.T) {
	dir := os.Getenv(helperRunEnv)
	if dir == "" {
		return
	}
	Run(context.Background(), dir, 0, "version")
}

// git runs in its own process group, so killing the hook's group misses it;
// on Linux Pdeathsig still kills git when the hook process dies.
func TestRunGitDiesWithKilledHook(t *testing.T) {
	bin := t.TempDir()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	fake := "#!/bin/sh\necho $$ > '" + pidFile + ".tmp'\nmv '" + pidFile + ".tmp' '" + pidFile + "'\nexec /bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRun$", "-test.count=1")
	cmd.Env = append(os.Environ(), helperRunEnv+"="+dir, "PATH="+bin+":/usr/bin:/bin")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	var pid int
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err = strconv.Atoi(strings.TrimSpace(string(b))); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(end) {
			cmd.Process.Kill()
			t.Fatal("fake git did not start")
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if dead(pid) {
			return
		}
		if time.Now().After(end) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("git %d outlived the killed hook", pid)
		}
	}
}
