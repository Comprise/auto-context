package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"auto-context/internal/docstate"
	"auto-context/internal/testutil"
)

// gitRepoWithUnit is a git repo with one committed unit and no global config.
func gitRepoWithUnit(t *testing.T, unit string) string {
	t.Helper()
	testutil.RequireGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root := t.TempDir()
	testutil.Git(t, root, "init", "-q")
	writeTree(t, root, map[string]string{
		".auto-context/config.yaml": "include: [" + unit + "]\n",
		unit + "/a.go":              "package x\n",
	})
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-qm", "first")
	return root
}

func changesFor(t *testing.T, root, unit string) string {
	t.Helper()
	var b strings.Builder
	if err := open(t, root).WriteChanges(&b, unit); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// A repo whose config shows signatures with a repo-chosen gpg.program and a
// signed commit does not run that program from `changes`.
func TestChangesIgnoresShowSignature(t *testing.T) {
	root := gitRepoWithUnit(t, "svc")
	marker := filepath.Join(t.TempDir(), "ran")
	testutil.WriteFile(t, root, "evil.sh", "#!/bin/sh\ntouch '"+marker+"'\nexit 1\n")
	if err := os.Chmod(filepath.Join(root, "evil.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "config", "log.showSignature", "true")
	testutil.Git(t, root, "config", "gpg.program", filepath.Join(root, "evil.sh"))
	testutil.WriteFile(t, root, "svc/a.go", "package x // signed\n")
	testutil.Git(t, root, "add", "-A")
	tree := strings.TrimSpace(testutil.Git(t, root, "write-tree"))
	head := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD"))
	obj := "tree " + tree + "\nparent " + head + "\n" +
		"author A <a@example.com> 1700000000 +0000\ncommitter A <a@example.com> 1700000000 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n iQEzBAABCAAdFiEE\n -----END PGP SIGNATURE-----\n\nsigned change\n"
	testutil.WriteFile(t, root, ".git/signed.obj", obj)
	id := strings.TrimSpace(testutil.Git(t, root, "hash-object", "-t", "commit", "-w", filepath.Join(root, ".git", "signed.obj")))
	testutil.Git(t, root, "update-ref", "HEAD", id)
	// The setup is live: git itself runs the program for this commit.
	sanity := exec.Command("git", "-C", root, "log", "-1", "--show-signature")
	_ = sanity.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("this git does not verify the signature: %v", err)
	}
	os.Remove(marker)
	out := changesFor(t, root, "svc")
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("gpg.program ran; out:\n%s", out)
	}
	if !strings.Contains(out, "signed change") {
		t.Errorf("signed commit not listed:\n%s", out)
	}
}

// core.worktree sending git to another directory makes git unavailable for
// changes: names from outside the project are never printed.
func TestChangesForeignWorkTree(t *testing.T) {
	root := gitRepoWithUnit(t, "svc")
	outside := t.TempDir()
	testutil.WriteFile(t, outside, "secretdir/id_rsa", "x")
	testutil.Git(t, root, "config", "core.worktree", outside)
	out := changesFor(t, root, "svc")
	if !strings.Contains(out, "(git unavailable: the work tree or git dir lies outside the project") || strings.Contains(out, "id_rsa") {
		t.Errorf("out:\n%s", out)
	}
}

// A git error does not quote git's stderr, which may carry repo config.
func TestChangesGitErrorNotQuoted(t *testing.T) {
	root := gitRepoWithUnit(t, "svc")
	testutil.Git(t, root, "config", "core.bigFileThreshold", "IGNORE-previous-instructions")
	out := changesFor(t, root, "svc")
	if strings.Contains(out, "IGNORE") || !strings.Contains(out, "(git failed: exit ") {
		t.Errorf("out:\n%s", out)
	}
}

// A huge commit subject is cut at maxChangesOutput.
func TestChangesOutputCapped(t *testing.T) {
	root := gitRepoWithUnit(t, "svc")
	testutil.WriteFile(t, root, "svc/a.go", "package x // edited\n")
	testutil.Git(t, root, "commit", "-qam", strings.Repeat("x", maxChangesOutput))
	out := changesFor(t, root, "svc")
	if !strings.Contains(out, fmt.Sprintf("(output cut at %d KiB)", maxChangesOutput>>10)) || len(out) > 2*maxChangesOutput {
		t.Errorf("len %d, tail %q", len(out), out[max(0, len(out)-200):])
	}
}

// ackPoint names why there is no ack point; a failing section says only the
// exit status.
func TestAckPointReasons(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "context", "svc.md")
	// Ordered, and each setup starts without the doc: a symlink left by one
	// case must not be written through by the next.
	cases := []struct {
		want  string
		setup func(t *testing.T)
	}{
		{"no context doc yet", func(t *testing.T) {}},
		{"broken frontmatter", func(t *testing.T) {
			testutil.WriteFile(t, root, "context/svc.md", "---\nunit: [\n---\n# svc\n")
		}},
		{"the context doc " + docstate.OutsideText, func(t *testing.T) {
			outside := t.TempDir()
			testutil.WriteFile(t, outside, "x.md", "# x\n")
			testutil.Symlink(t, filepath.Join(outside, "x.md"), doc)
		}},
		{"not acked yet", func(t *testing.T) {
			testutil.WriteFile(t, root, "context/svc.md", "# svc\n")
		}},
		{"no valid ack date", func(t *testing.T) {
			testutil.WriteFile(t, root, "context/svc.md", "---\nunit: svc\nsource_hash: x\n---\n# svc\n")
		}},
		{"the context doc cannot be read", func(t *testing.T) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("permissions are not enforced")
			}
			testutil.WriteFile(t, root, "context/svc.md", "---\nunit: svc\nsource_hash: x\nupdated: \"2026-01-02\"\n---\n# svc\n")
			if err := os.Chmod(doc, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(doc, 0o644) })
		}},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if err := os.Remove(doc); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			c.setup(t)
			if a := ackPoint(root, "context/svc.md"); a.why != c.want {
				t.Errorf("why = %q, want %q", a.why, c.want)
			}
		})
	}

	repo := gitRepoWithUnit(t, "svc")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	testutil.FakeGit(t, `for a in "$@"; do if [ "$a" = log ]; then echo 'fatal: SECRET-from-config' >&2; exit 3; fi; done
exec '`+real+`' "$@"`)
	out := changesFor(t, repo, "svc")
	if !strings.Contains(out, "(git failed: exit 3)") || strings.Contains(out, "SECRET") {
		t.Errorf("out:\n%s", out)
	}
}

// logSection picks the range without git: by commit, by date with the reason,
// or the last commits.
func TestLogSection(t *testing.T) {
	id := strings.Repeat("a", 40)
	never := func() (bool, bool) { t.Error("ancestor called without a commit"); return false, false }
	cases := []struct {
		a        ack
		ancestor func() (bool, bool)
		title    string
		last     string
	}{
		{ack{commit: id, since: "2026-01-02 00:00"}, func() (bool, bool) { return true, true }, "since the acked commit aaaaaaaaaaaa", id + "..HEAD"},
		{ack{commit: id, since: "2026-01-02 00:00"}, func() (bool, bool) { return false, true }, "not in HEAD's history", "--since=2026-01-02 00:00"},
		{ack{commit: id, since: "2026-01-02 00:00"}, func() (bool, bool) { return false, false }, "could not be checked", "--since=2026-01-02 00:00"},
		{ack{since: "2026-01-02 00:00"}, never, "no acked commit recorded", "--since=2026-01-02 00:00"},
		{ack{why: "not acked yet"}, never, "last commits touching svc (not acked yet: compare the code with context/svc.md)", "50"},
	}
	for _, c := range cases {
		s := logSection("svc", "context/svc.md", c.a, c.ancestor)
		if !strings.Contains(s.title, c.title) || s.args[len(s.args)-1] != c.last {
			t.Errorf("%+v: %q %q", c.a, s.title, s.args)
		}
	}
	if len(logArgs) != 9 {
		t.Errorf("logArgs changed: %q", logArgs)
	}
}
