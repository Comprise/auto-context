package gitx

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func TestRunAndErrors(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	if _, err := Run(dir, 0, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if res, err := Run(dir, 0, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(string(res.Out)) != "true" {
		t.Fatalf("out=%q err=%v", res.Out, err)
	}
	_, err := Run(dir, 0, "log")
	if Code(err) != 128 || !strings.Contains(err.Error(), "does not have any commits") {
		t.Errorf("log in empty repo: code=%d err=%v", Code(err), err)
	}
	if Code(nil) != 0 {
		t.Error("Code(nil) != 0")
	}
}

func TestRunLimit(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	if _, err := Run(dir, 0, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	res, err := Run(dir, 5, "rev-parse", "--git-dir", "--is-inside-work-tree")
	if err != nil || len(res.Out) != 5 || !res.Truncated {
		t.Errorf("out=%q truncated=%v err=%v", res.Out, res.Truncated, err)
	}
}

// A repo-local core.fsmonitor command never runs.
func TestRunIgnoresFSMonitor(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	if _, err := Run(dir, 0, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	marker := dir + "/pwned"
	if out, err := exec.Command("git", "-C", dir, "config", "core.fsmonitor", "touch "+marker).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	Run(dir, 0, "status", "--short")
	Run(dir, 0, "ls-files", "-co", "--exclude-standard")
	if _, err := os.Stat(marker); err == nil {
		t.Error("fsmonitor command was executed")
	}
}

func TestNotRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Run(t.TempDir(), 0, "version")
	if !NotRun(err) || Code(err) != -1 {
		t.Errorf("err = %v", err)
	}
	if NotRun(nil) {
		t.Error("NotRun(nil)")
	}
}

// git messages are English whatever the user's locale: callers match them.
func TestRunForcesEnglish(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("LC_ALL", "ru_RU.UTF-8")
	_, err := Run(t.TempDir(), 0, "rev-parse", "HEAD")
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("err = %v", err)
	}
}

// The caller's GIT_DIR and friends do not redirect the plugin's git calls.
func TestRunIgnoresCallerRepoVars(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Setenv("GIT_DIR", "/nonexistent")
	t.Setenv("GIT_WORK_TREE", "/nonexistent")
	if _, err := Run(dir, 0, "rev-parse", "--is-inside-work-tree"); err != nil {
		t.Errorf("caller's GIT_DIR applied: %v", err)
	}
}

func TestRunTimeout(t *testing.T) {
	needGit(t)
	old := Timeout
	Timeout = time.Nanosecond
	defer func() { Timeout = old }()
	_, err := Run(t.TempDir(), 0, "version")
	var e *Error
	if !errors.As(err, &e) || !e.TimedOut || Code(err) != -1 {
		t.Errorf("err = %v", err)
	}
}

// Past the process deadline git is not started at all.
func TestRunPastDeadline(t *testing.T) {
	SetDeadline(time.Now().Add(-time.Second))
	defer SetDeadline(time.Time{})
	_, err := Run(t.TempDir(), 0, "version")
	var e *Error
	if !errors.As(err, &e) || !e.TimedOut || e.NotRun {
		t.Errorf("err = %v", err)
	}
}

func TestBrief(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{errors.New("x"), "failed"},
		{&Error{TimedOut: true}, "timed out"},
		{&Error{NotRun: true}, "could not be run"},
		{&Error{Code: 3}, "exit 3"},
		{&Error{Code: -1}, "killed"},
	} {
		if got := Brief(c.err); got != c.want {
			t.Errorf("Brief(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
