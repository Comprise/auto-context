//go:build unix

package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A git that leaves a child holding its stdout is still stopped near
// Timeout, and the child dies with it: the whole process group is killed,
// not just git (WaitDelay alone would return but leave the child running).
func TestRunTimeoutWithGrandchild(t *testing.T) {
	bin := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "pid")
	fake := "#!/bin/sh\nPATH=/usr/bin:/bin\nsleep 30 &\necho $! > '" + pidFile + "'\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	old := Timeout
	Timeout = 200 * time.Millisecond
	defer func() { Timeout = old }()
	start := time.Now()
	_, err := Run(t.TempDir(), 0, "version")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Run took %v", d)
	}
	var e *Error
	if !errors.As(err, &e) || !e.TimedOut {
		t.Errorf("err = %v", err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	// The orphan is reaped by init (or a subreaper) shortly after the kill.
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if dead(pid) {
			return
		}
		if time.Now().After(end) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("grandchild %d still alive after Run returned", pid)
		}
	}
}

// dead reports whether pid is gone or a zombie nobody reaped yet (a
// container without an init leaves those behind).
func dead(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	_, rest, ok := strings.Cut(string(b), ") ")
	return ok && strings.HasPrefix(rest, "Z")
}
