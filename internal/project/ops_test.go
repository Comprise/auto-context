package project

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"auto-context/internal/testutil"
)

// Writes that would act on the default units are refused while the config
// cannot be parsed.
func TestBrokenConfigRefusesWrites(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc\n")
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nThe service.\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ConfigBroken() {
		t.Fatal("config not broken")
	}
	if err := p.Ack("svc", time.Now()); !errors.Is(err, ErrConfigBroken) {
		t.Errorf("Ack: %v", err)
	}
	if _, err := p.UpdateIndex(p.Statuses()); !errors.Is(err, ErrConfigBroken) {
		t.Errorf("UpdateIndex: %v", err)
	}
	if err := p.WriteChanges(io.Discard, "svc"); !errors.Is(err, ErrConfigBroken) {
		t.Errorf("WriteChanges: %v", err)
	}
}

// WriteChanges limits git to the unit's own directory: a sibling whose name
// starts the same is not listed. A bracketed name such as a[1] cannot be a
// unit at all (units.SafeName rejects every glob-magic character), so it is
// checked to stay out of a1's output too.
func TestWriteChangesLiteralPathspec(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		testutil.Git(t, root, args...)
	}
	git("init", "-q")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [a1, a10, \"a[1]\"]\n")
	testutil.WriteFile(t, root, "a1/own.go", "package a\n")
	testutil.WriteFile(t, root, "a10/sibling.go", "package a\n")
	testutil.WriteFile(t, root, "a[1]/bracket.go", "package a\n")
	git("add", "-A")
	git("commit", "-qm", "first")
	testutil.WriteFile(t, root, "a1/own.go", "package a // edited\n")
	testutil.WriteFile(t, root, "a10/sibling.go", "package a // edited\n")
	testutil.WriteFile(t, root, "a[1]/bracket.go", "package a // edited\n")
	testutil.WriteFile(t, root, "a1/new.go", "package a\n")
	testutil.WriteFile(t, root, "a10/new10.go", "package a\n")
	testutil.WriteFile(t, root, "a[1]/newb.go", "package a\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.tree.IsUnit("a[1]") || !p.tree.IsUnit("a1") || !p.tree.IsUnit("a10") {
		t.Fatalf("units: %v", p.tree.Units())
	}
	var b strings.Builder
	if err := p.WriteChanges(&b, "a1"); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "own.go") || !strings.Contains(out, "new.go") {
		t.Errorf("own files missing:\n%s", out)
	}
	for _, f := range []string{"sibling.go", "new10.go", "bracket.go", "newb.go"} {
		if strings.Contains(out, f) {
			t.Errorf("%s listed for a1:\n%s", f, out)
		}
	}
}

// Prewarm's entries survive SaveCache and are there for the next Open.
func TestPrewarmPersists(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc]\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	testutil.WriteFile(t, root, "svc/b.go", "package svc\n")
	// Old mtimes: entries of files hashed right after a write are racy and
	// not kept across runs.
	old := time.Now().Add(-time.Hour)
	for _, f := range []string{"svc/a.go", "svc/b.go"} {
		if err := os.Chtimes(filepath.Join(root, f), old, old); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	files := p.tree.Files("svc")
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	p.Prewarm(files)
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	q, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if n := q.Cache().Len(); n != len(files) || q.CacheErr() != nil {
		t.Errorf("reopened cache has %d entries (err %v), want %d", n, q.CacheErr(), len(files))
	}
	data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "cache.json"))
	for _, f := range files {
		if !strings.Contains(string(data), f) {
			t.Errorf("cache file lacks %s: %s", f, data)
		}
	}
}
