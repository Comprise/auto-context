package project

import (
	"auto-context/internal/frontmatter"
	"auto-context/internal/fsutil"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/testutil"
)

func TestOpenAckRefreshSave(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc]\nexlude: [x]\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nThe service.\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.cfgProblems == nil || p.cfgErr != nil || p.ConfigBroken() || !p.tree.IsUnit("svc") {
		t.Fatalf("CfgProblems=%v CfgErr=%v svc unit=%v", p.cfgProblems, p.cfgErr, p.tree.IsUnit("svc"))
	}
	if err := p.Ack("svc", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := p.RefreshIndex(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); !strings.Contains(string(data), "- `svc` — The service.") {
		t.Errorf("index:\n%s", data)
	}
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	// Entries of files that are gone are pruned on the next save.
	if err := os.Remove(filepath.Join(root, "svc", "a.go")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "svc/b.go", "package svc\n")
	p, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	p.Statuses() // loads the cache, as SessionStart does
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "cache.json")); strings.Contains(string(data), "svc/a.go") {
		t.Errorf("cache not pruned: %s", data)
	}
}

// A hook that hashes nothing never reads or writes the cache.
func TestCacheIsLazy(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc]\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	testutil.WriteFile(t, root, ".auto-context/cache.json", "garbage")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "cache.json")); string(data) != "garbage" {
		t.Errorf("cache touched: %q", data)
	}
	if p.CacheErr() == nil {
		t.Error("CacheErr does not load the cache")
	}
}

// trustedFilters fails closed when git cannot tell the scopes, and trusts
// filters given on the command line (GIT_CONFIG_*).
func TestTrustedFiltersScopes(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	if runtime.GOOS == "windows" {
		t.Skip("fake git needs a POSIX sh")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Run("command scope", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "filter.lfs.clean")
		t.Setenv("GIT_CONFIG_VALUE_0", "cat")
		if ok, why := trustedFilters(root); !ok {
			t.Errorf("command-line filter not trusted: %s", why)
		}
	})
	t.Run("git cannot show scopes", func(t *testing.T) {
		bin := t.TempDir()
		testutil.WriteFile(t, bin, "git", "#!/bin/sh\ncase \"$*\" in *--show-scope*) echo 'unknown option' >&2; exit 129;; esac\nexec '"+real+"' \"$@\"\n")
		if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if ok, why := trustedFilters(root); ok || !strings.Contains(why, "could not be checked") {
			t.Errorf("ok=%v why=%q", ok, why)
		}
	})
}

// WriteChanges works without the CLI: unknown units are an error, a missing
// git a note.
func TestWriteChanges(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc]\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := p.WriteChanges(&b, "nope"); err == nil {
		t.Error("unknown unit accepted")
	}
	t.Setenv("PATH", t.TempDir())
	if err := p.WriteChanges(&b, "svc"); err != nil || !strings.Contains(b.String(), "(git unavailable:") {
		t.Errorf("err=%v out=%q", err, b.String())
	}
}

// The plugin's .gitignore keeps local state out of git but lets the config be
// committed; a user's .gitignore that hides the config is detected.
func TestConfigCommittable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := config.AddExclude(root, "gen/**"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, ".auto-context/cache.json", "{}")
	ignored := func(rel string) bool {
		return exec.Command("git", "-C", root, "check-ignore", "-q", rel).Run() == nil
	}
	if ignored(config.FileName) || ignored(".auto-context/.gitignore") || !ignored(".auto-context/cache.json") {
		t.Errorf("config ignored=%v gitignore ignored=%v cache ignored=%v",
			ignored(config.FileName), ignored(".auto-context/.gitignore"), ignored(".auto-context/cache.json"))
	}
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if ign, err := p.ConfigIgnored(); ign || err != nil {
		t.Error("config reported as ignored")
	}
	testutil.WriteFile(t, root, ".gitignore", ".auto-context/\n")
	if ign, err := p.ConfigIgnored(); !ign || err != nil {
		t.Error("config hidden by the user's .gitignore not detected")
	}
}

// The old location is not read: such a project is not enabled by it.
func TestOldConfigLocationIgnored(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context.yaml", "include: [svc]\n")
	if Active(root) {
		t.Error(".auto-context.yaml enables the plugin")
	}
}

// Only a real date from the frontmatter reaches git.
func TestAckDate(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		"---\nunit: svc\nsource_hash: x\nupdated: \"2026-01-02\"\n---\n# svc\n": "2026-01-02 00:00",
		"---\nunit: svc\nsource_hash: x\nupdated: \"yesterday\"\n---\n# svc\n":  "",
		"---\nunit: svc\nsource_hash: x\nupdated: \"2026-13-01\"\n---\n# svc\n": "",
		"# svc\nno frontmatter\n": "",
	}
	for content, want := range cases {
		testutil.WriteFile(t, root, "context/svc.md", content)
		if since, why := ackDate(root, "context/svc.md"); since != want || (want == "" && why == "") {
			t.Errorf("%q: since=%q why=%q, want %q", content, since, why, want)
		}
	}
	if since, why := ackDate(root, "context/none.md"); since != "" || why != "no context doc yet" {
		t.Errorf("missing: %q %q", since, why)
	}
}

// WriteChanges lists only commits since the ack day.
func TestWriteChangesSinceAckDate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(nil, "init", "-q")
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc]\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	git(nil, "add", "-A")
	old := []string{"GIT_AUTHOR_DATE=2020-01-01T00:00:00", "GIT_COMMITTER_DATE=2020-01-01T00:00:00"}
	git(old, "commit", "-qm", "ancient change")
	testutil.WriteFile(t, root, "svc/a.go", "package svc // new\n")
	git(nil, "commit", "-qam", "recent change")
	testutil.WriteFile(t, root, "context/svc.md", "---\nunit: svc\nsource_hash: x\nupdated: \"2021-06-01\"\n---\n# svc\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := p.WriteChanges(&b, "svc"); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); strings.Contains(out, "ancient change") || !strings.Contains(out, "recent change") {
		t.Errorf("out:\n%s", out)
	}
}

func TestShowNames(t *testing.T) {
	var many []string
	for i := range 25 {
		many = append(many, fmt.Sprintf("context/u%d.md", i))
	}
	shown, hidden := ShowNames(append([]string{"context/a\n.md"}, many...))
	if len(shown) != 20 || hidden != 6 || shown[0] != "context/u0.md" {
		t.Errorf("shown %v, hidden %d", shown, hidden)
	}
}

// committedRepo is a git repo with a committed config and a corrupt index,
// so git check-ignore fails with exit 128.
func committedRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 5\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-qm", "x"}} {
		testutil.Git(t, root, args...)
	}
	testutil.WriteFile(t, root, ".git/index", "garbage")
	return root
}

// A git failure other than "not ignored" or "no repo" is returned as an
// error, not taken for "not ignored".
func TestConfigIgnoredGitFailure(t *testing.T) {
	p, err := Open(committedRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if ign, err := p.ConfigIgnored(); ign || err == nil {
		t.Errorf("ignored=%v err=%v, want false and an error", ign, err)
	}
}

// Without git on PATH, ConfigIgnored says "not ignored" and no error.
func TestConfigIgnoredWithoutGit(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 5\n")
	testutil.WriteFile(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	t.Setenv("PATH", t.TempDir())
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if ign, err := p.ConfigIgnored(); ign || err != nil {
		t.Errorf("ignored=%v err=%v", ign, err)
	}
}

func TestUpdateIndexUsesIndexFile(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "index_file: AGENTS.md\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n\nRoot.\n")
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/a.go", "package svc\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nThe service.\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.IndexFile() != "AGENTS.md" {
		t.Fatalf("IndexFile = %q", p.IndexFile())
	}
	if err := p.RefreshIndex(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || !strings.Contains(string(b), "- `svc` — The service.") {
		t.Fatalf("AGENTS.md = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md was created")
	}
}

func TestUpdateIndexInactiveWritesNothing(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"f1", "f2", "f3", "f4", "f5"} {
		testutil.WriteFile(t, root, "pkg/"+f+".go", "package pkg\n")
	}
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RefreshIndex(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md was created in an inactive project")
	}
}

func TestOrphans(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/main.go", "package svc\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n")
	testutil.WriteFile(t, root, "context/gone.md", "# gone\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Orphans(); !slices.Equal(got, []string{"context/gone.md"}) {
		t.Errorf("Orphans = %v", got)
	}
}

func TestAckTouchedMixed(t *testing.T) {
	root := t.TempDir()
	for _, u := range []string{"svc", "lib", "big"} {
		testutil.WriteFile(t, root, u+"/go.mod", "module "+u+"\n")
		testutil.WriteFile(t, root, u+"/main.go", "package "+u+"\n")
	}
	testutil.WriteFile(t, root, "nounit/readme.txt", "x\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nThe service.\n")
	testutil.WriteFile(t, root, "context/big.md", strings.Repeat("x", frontmatter.MaxSize+1))
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	acked, failed, indexErr := p.AckTouched([]string{"svc", "lib", "nounit", "big"}, time.Now())
	if !slices.Equal(acked, []string{"svc"}) {
		t.Errorf("acked = %v", acked)
	}
	if len(failed) != 1 || !errors.Is(failed["big"], fsutil.ErrTooLarge) {
		t.Errorf("failed = %v", failed)
	}
	if indexErr != nil {
		t.Errorf("indexErr = %v", indexErr)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); !strings.Contains(string(data), "`svc`") {
		t.Errorf("index:\n%s", data)
	}
}

func TestAckTouchedEmptyKeepsIndex(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nThe service.\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !Active(root) {
		t.Fatal("project not active")
	}
	index := filepath.Join(root, "CLAUDE.md")
	if _, _, err := p.AckTouched(nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(index); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("index written for empty touched: %v", err)
	}
	// The counterpart: a touched unit refreshes the index.
	if _, _, err := p.AckTouched([]string{"svc"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(index); !strings.Contains(string(data), "`svc`") {
		t.Errorf("index not written for touched units:\n%s", data)
	}
}

func TestUpToDateAfterSourceEdit(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/main.go", "package svc\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nThe service.\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ack("svc", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !p.UpToDate("svc") {
		t.Fatal("not up to date after Ack")
	}
	testutil.WriteFile(t, root, "svc/main.go", "package svc\n\nvar changed = 1\n")
	p, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.UpToDate("svc") {
		t.Error("up to date after a source edit")
	}
}

func TestWarnings(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if w := p.Warnings(); w != nil {
		t.Errorf("clean repo: %v", w)
	}
	testutil.WriteFile(t, root, ".gitignore", "*"+strings.Repeat("a", 599)+"\n")
	p, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) == 0 {
		t.Error("no warning for an over-long .gitignore line")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteChangesReportsWriteError(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	p, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.WriteChanges(failingWriter{}, "svc"); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Errorf("err = %v", err)
	}
}
