package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"auto-context/internal/testutil"
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

// markerScript writes an executable script that creates marker and fails.
func markerScript(t *testing.T, dir, marker string) string {
	t.Helper()
	path := filepath.Join(dir, "evil.sh")
	body := fmt.Sprintf("#!/bin/sh\ntouch '%s'\nexit 1\n", marker)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// plainGit runs git without Run's protections (the attacker's baseline).
// Its error is ignored on purpose: the failed fetch makes git exit non-zero,
// and only the marker the transport leaves behind matters.
func plainGit(dir string, args ...string) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	_ = cmd.Run()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The PoC of S-1: a promisor remote whose transport runs a repo-chosen
// command once ls-files needs a missing blob (a skip-worktree .gitignore).
func TestRunNoLazyFetch(t *testing.T) {
	needGit(t)
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	for _, c := range []struct {
		name string
		cfg  func(script string) [][2]string
	}{
		{"uploadpack", func(s string) [][2]string {
			return [][2]string{{"remote.origin.url", "/nonexistent/dir"}, {"remote.origin.uploadpack", s}}
		}},
		{"sshCommand", func(s string) [][2]string {
			return [][2]string{{"remote.origin.url", "ssh://host.invalid/r"}, {"core.sshCommand", s}}
		}},
		{"ext", func(s string) [][2]string {
			return [][2]string{{"remote.origin.url", "ext::" + s}, {"protocol.ext.allow", "always"}}
		}},
		{"gitProxy", func(s string) [][2]string {
			return [][2]string{{"remote.origin.url", "git://host.invalid/r"}, {"core.gitProxy", s}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			testutil.Git(t, root, "init", "-q")
			testutil.WriteFile(t, root, "a.txt", "a\n")
			testutil.WriteFile(t, root, ".gitignore", "secret*\n")
			testutil.Git(t, root, "add", ".")
			testutil.Git(t, root, "commit", "-qm", "init")
			blob := strings.TrimSpace(testutil.Git(t, root, "rev-parse", ":.gitignore"))
			testutil.Git(t, root, "update-index", "--skip-worktree", ".gitignore")
			if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(root, ".git", "objects", blob[:2], blob[2:])); err != nil {
				t.Fatal(err)
			}
			testutil.Git(t, root, "config", "core.repositoryformatversion", "1")
			if err := exec.Command("git", "-C", root, "config", "extensions.partialClone", "origin").Run(); err != nil {
				t.Skip("extensions.partialClone not supported")
			}
			testutil.Git(t, root, "config", "remote.origin.promisor", "true")
			marker := filepath.Join(t.TempDir(), "pwned")
			script := markerScript(t, t.TempDir(), marker)
			for _, kv := range c.cfg(script) {
				testutil.Git(t, root, "config", kv[0], kv[1])
			}
			// ls-files is the hooks' call (older git reads the skip-worktree
			// .gitignore from the index there); status and cat-file reach the
			// missing blob on any git.
			triggers := [][]string{
				{"ls-files", "-co", "--exclude-standard", "-z"},
				{"status", "--porcelain"},
				{"cat-file", "-e", blob},
			}
			for _, args := range triggers {
				plainGit(root, args...)
			}
			if !exists(marker) {
				t.Skip("this git does not lazy-fetch here; the PoC does not apply")
			}
			for _, args := range triggers {
				os.Remove(marker)
				Run(root, 0, args...)
				if exists(marker) {
					t.Errorf("the transport command ran under Run %v", args)
				}
			}
		})
	}
}

// log.showSignature from the repo config never runs gpg.program.
func TestRunIgnoresShowSignature(t *testing.T) {
	needGit(t)
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	root := t.TempDir()
	testutil.Git(t, root, "init", "-q")
	testutil.WriteFile(t, root, "a.txt", "a\n")
	testutil.Git(t, root, "add", ".")
	tree := strings.TrimSpace(testutil.Git(t, root, "write-tree"))
	obj := "tree " + tree + "\n" +
		"author t <t@t> 0 +0000\n" +
		"committer t <t@t> 0 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n" +
		" \n" +
		" iQEzBAABCAAdFiEE\n" +
		" -----END PGP SIGNATURE-----\n" +
		"\n" +
		"signed\n"
	objFile := filepath.Join(t.TempDir(), "commit")
	if err := os.WriteFile(objFile, []byte(obj), 0o644); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(testutil.Git(t, root, "hash-object", "-t", "commit", "-w", objFile))
	testutil.Git(t, root, "update-ref", "HEAD", id)
	marker := filepath.Join(t.TempDir(), "pwned")
	testutil.Git(t, root, "config", "log.showSignature", "true")
	testutil.Git(t, root, "config", "gpg.program", markerScript(t, t.TempDir(), marker))
	plainGit(root, "log", "-n1")
	if !exists(marker) {
		t.Skip("this git does not verify signatures in log; the PoC does not apply")
	}
	os.Remove(marker)
	if _, err := Run(root, 0, "log", "-n1"); err != nil {
		t.Fatal(err)
	}
	if exists(marker) {
		t.Error("gpg.program ran under Run")
	}
}

func TestNotRepo(t *testing.T) {
	needGit(t)
	_, err := Run(t.TempDir(), 0, "rev-parse", "--show-toplevel")
	if !NotRepo(err) {
		t.Errorf("NotRepo(%v) = false", err)
	}
	for _, err := range []error{
		nil,
		errors.New("not a git repository"),
		&Error{Code: 128, Stderr: "fatal: bad revision"},
		&Error{Code: 1, Stderr: "fatal: not a git repository"},
	} {
		if NotRepo(err) {
			t.Errorf("NotRepo(%v) = true", err)
		}
	}
}
