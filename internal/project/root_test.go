package project

import (
	"os"
	"path/filepath"
	"testing"

	"auto-context/internal/config"
	"auto-context/internal/testutil"
)

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	cwd := filepath.Join(root, "a", "b")
	os.MkdirAll(cwd, 0o755)
	if got := FindRoot(cwd); got != root {
		t.Fatalf("got %q want %q", got, root)
	}
	bare := t.TempDir()
	if got := FindRoot(bare); got != bare {
		t.Fatalf("got %q want %q", got, bare)
	}
	marked := t.TempDir()
	testutil.WriteFile(t, marked, config.FileName, "")
	sub := filepath.Join(marked, "x")
	os.MkdirAll(sub, 0o755)
	if got := FindRoot(sub); got != marked {
		t.Fatalf("got %q want %q", got, marked)
	}
}

// $HOME with a trailing slash still stops the climb into the home directory.
func TestFindRootHomeTrailingSlash(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, "proj")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home+"/")
	if got := FindRoot(cwd); got != cwd {
		t.Errorf("FindRoot = %q, want %q", got, cwd)
	}
}

// $HOME given through a symlink still stops the climb into the home directory.
func TestFindRootHomeSymlink(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	cwd := filepath.Join(home, "proj")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	if got := FindRoot(cwd); got != cwd {
		t.Errorf("FindRoot = %q, want %q", got, cwd)
	}
	// The home itself may still be the project.
	if got := FindRoot(home); got != home {
		t.Errorf("FindRoot(home) = %q", got)
	}
}
