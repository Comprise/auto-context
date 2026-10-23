package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"auto-context/internal/testutil"
)

func initRepo(t *testing.T, dir string) {
	t.Helper()
	testutil.Git(t, dir, "init", "-q")
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Git(t, dir, "add", ".")
	testutil.Git(t, dir, "commit", "-qm", "init")
}

func wantWorkTree(t *testing.T, root string, want bool) {
	t.Helper()
	ok, err := WorkTreeOK(root)
	if err != nil || ok != want {
		t.Errorf("WorkTreeOK(%s) = %v, %v; want %v", root, ok, err, want)
	}
}

func TestWorkTreeOK(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	initRepo(t, root)
	wantWorkTree(t, root, true)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wantWorkTree(t, sub, true)
	if _, err := WorkTreeOK(t.TempDir()); !NotRepo(err) {
		t.Errorf("outside a repo: err = %v", err)
	}
}

// core.worktree resolves against the git dir: ../other is root/other, a
// subdirectory, and ../../other is beside root. Neither contains root.
func TestWorkTreeOKCoreWorktreeOutside(t *testing.T) {
	needGit(t)
	for _, wt := range []string{"../other", "../../other"} {
		t.Run(wt, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "r")
			if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(base, "other"), 0o755); err != nil {
				t.Fatal(err)
			}
			initRepo(t, root)
			testutil.Git(t, root, "config", "core.worktree", wt)
			wantWorkTree(t, root, false)
		})
	}
}

// A .git file pointing at another repository's git dir.
func TestWorkTreeOKForeignGitdir(t *testing.T) {
	needGit(t)
	base := t.TempDir()
	other := filepath.Join(base, "other")
	root := filepath.Join(base, "r")
	for _, d := range []string{other, root} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	initRepo(t, other)
	testutil.WriteFile(t, root, ".git", "gitdir: "+filepath.Join(other, ".git")+"\n")
	wantWorkTree(t, root, false)
}

func TestWorkTreeOKLinkedWorktree(t *testing.T) {
	needGit(t)
	base := t.TempDir()
	main := filepath.Join(base, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, main)
	wt := filepath.Join(base, "wt")
	testutil.Git(t, main, "worktree", "add", "-q", wt)
	wantWorkTree(t, wt, true)
	wantWorkTree(t, main, true)
}

func TestWorkTreeOKSubmodule(t *testing.T) {
	needGit(t)
	base := t.TempDir()
	lib := filepath.Join(base, "lib")
	super := filepath.Join(base, "super")
	for _, d := range []string{lib, super} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		initRepo(t, d)
	}
	testutil.Git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	root := filepath.Join(super, "lib")
	if fi, err := os.Lstat(filepath.Join(root, ".git")); err != nil || fi.IsDir() {
		t.Fatalf("submodule .git is not a file: %v", err)
	}
	wantWorkTree(t, root, true)
}

// A .git file pointing at an ancestor repository's linked-worktree dir is
// not a submodule: it would show the ancestor's commits and files.
func TestWorkTreeOKAncestorWorktreeGitdir(t *testing.T) {
	needGit(t)
	base := t.TempDir()
	main := filepath.Join(base, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, main)
	testutil.Git(t, main, "worktree", "add", "-q", filepath.Join(base, "wt"))
	root := filepath.Join(main, "evil")
	testutil.WriteFile(t, root, ".git", "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
	wantWorkTree(t, root, false)
}

// A submodule checked out in a linked worktree, both beside and inside the
// main repository: its git dir is <main>/.git/worktrees/<n>/modules/<name>.
func TestWorkTreeOKSubmoduleInLinkedWorktree(t *testing.T) {
	needGit(t)
	base := t.TempDir()
	lib := filepath.Join(base, "lib")
	main := filepath.Join(base, "main")
	for _, d := range []string{lib, main} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		initRepo(t, d)
	}
	testutil.Git(t, main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	testutil.Git(t, main, "commit", "-qm", "submodule")
	for _, wt := range []string{filepath.Join(base, "wt"), filepath.Join(main, ".wt", "in")} {
		testutil.Git(t, main, "worktree", "add", "-q", wt)
		testutil.Git(t, wt, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "-q")
		gitDir := testutil.Git(t, filepath.Join(wt, "lib"), "rev-parse", "--absolute-git-dir")
		if !strings.Contains(gitDir, filepath.Join("worktrees", filepath.Base(wt), "modules")) {
			t.Fatalf("unexpected submodule git dir %q", gitDir)
		}
		wantWorkTree(t, filepath.Join(wt, "lib"), true)
	}
}

// core.worktree pointing above root makes the top level an ancestor while
// the git dir stays under root: refused. A root that is simply a
// subdirectory of the work tree (git dir at <top level>/.git) is fine.
func TestWorkTreeOKCoreWorktreeAbove(t *testing.T) {
	needGit(t)
	base := t.TempDir()
	root := filepath.Join(base, "x", "r")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, root)
	testutil.Git(t, root, "config", "core.worktree", base)
	wantWorkTree(t, root, false)

	outer := t.TempDir()
	initRepo(t, outer)
	sub := filepath.Join(outer, "x", "r")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wantWorkTree(t, sub, true)
}
