package fsutil

import (
	"runtime"

	"auto-context/internal/testutil"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAtomicCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a", "b", "c.txt")
	if err := WriteAtomic(dir, p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q, err %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
	if err := WriteAtomic(dir, p, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(p)
	if string(got) != "bye" {
		t.Fatalf("overwrite got %q", got)
	}
}

// EnsureStateDir hides .auto-context/ from git with its own .gitignore and
// never edits the user's .gitignore.
func TestEnsureStateDirIgnoresItself(t *testing.T) {
	root := t.TempDir()
	gi := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gi, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := EnsureStateDir(root); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(gi); string(got) != "bin" {
		t.Errorf("user .gitignore changed: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, StateDir, ".gitignore")); string(got) != stateGitignore {
		t.Errorf("state .gitignore = %q", got)
	}
	if st, err := os.Stat(filepath.Join(root, StateDir, "state")); err != nil || !st.IsDir() {
		t.Fatalf("state dir missing: %v", err)
	}
}

// symlink creates link -> target or skips the test where symlinks are unsupported.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
}

func TestResolve(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink(t, outside, filepath.Join(root, "dirlink"))
	symlink(t, filepath.Join(outside, "f"), filepath.Join(root, "dangling"))
	symlink(t, "AGENTS.md", filepath.Join(root, "CLAUDE.md"))
	symlink(t, "real", filepath.Join(root, "inlink"))
	realRoot, _ := filepath.EvalSymlinks(root)
	cases := map[string]string{ // path under root -> want resolved path under real root ("" = ErrOutside)
		"real/x.md":          "real/x.md",
		"missing/deeper/x":   "missing/deeper/x",
		"CLAUDE.md":          "AGENTS.md",
		"inlink/new/x.md":    "real/new/x.md",
		"dirlink":            "",
		"dirlink/state/s.js": "",
		"dangling":           "",
	}
	for rel, want := range cases {
		got, err := Resolve(root, filepath.Join(root, filepath.FromSlash(rel)))
		if want == "" {
			if !errors.Is(err, ErrOutside) {
				t.Errorf("%s: got %q err=%v, want ErrOutside", rel, got, err)
			}
			continue
		}
		if err != nil || got != filepath.Join(realRoot, filepath.FromSlash(want)) {
			t.Errorf("%s: got %q err=%v, want %s", rel, got, err, want)
		}
	}
	if _, err := Resolve(root, filepath.Join(outside, "x")); !errors.Is(err, ErrOutside) {
		t.Errorf("path outside root: err=%v", err)
	}
}

// A link inside the repo (CLAUDE.md -> AGENTS.md) is followed: the target is
// written and the link survives.
func TestWriteAtomicFollowsInRepoSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "CLAUDE.md")
	symlink(t, "AGENTS.md", link)
	if err := WriteAtomic(root, link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link replaced: %v %v", fi, err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "AGENTS.md")); string(data) != "new" {
		t.Errorf("AGENTS.md = %q, want new", data)
	}
}

// A cloned repo must not redirect writes outside itself through a symlinked
// file or directory; the link target stays intact.
func TestWriteAtomicRefusesSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink(t, victim, filepath.Join(root, "CLAUDE.md"))
	symlink(t, outside, filepath.Join(root, ".auto-context"))
	for _, rel := range []string{"CLAUDE.md", ".auto-context/victim", ".auto-context/state/new"} {
		err := WriteAtomic(root, filepath.Join(root, filepath.FromSlash(rel)), []byte("new"), 0o644)
		if !errors.Is(err, ErrOutside) {
			t.Errorf("%s: err=%v, want ErrOutside", rel, err)
		}
	}
	if data, _ := os.ReadFile(victim); string(data) != "old" {
		t.Errorf("victim = %q, want old", data)
	}
	if _, err := os.Stat(filepath.Join(outside, "state")); !os.IsNotExist(err) {
		t.Errorf("state dir created outside: %v", err)
	}
}

func TestEnsureStateDirRefusesSymlinkedStateDir(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	symlink(t, outside, filepath.Join(root, StateDir))
	if err := EnsureStateDir(root); !errors.Is(err, ErrOutside) {
		t.Fatalf("err=%v, want ErrOutside", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "state")); !os.IsNotExist(err) {
		t.Errorf("state dir created outside: %v", err)
	}
}

// A symlinked .gitignore is neither read nor rewritten.
func TestEnsureStateDirSkipsSymlinkedGitignore(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink(t, secret, filepath.Join(root, ".gitignore"))
	if err := EnsureStateDir(root); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(root, ".gitignore")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".gitignore link replaced: %v %v", fi, err)
	}
	if data, _ := os.ReadFile(secret); string(data) != "KEY" {
		t.Errorf("secret = %q", data)
	}
}

// A repo opened through a symlinked path (macOS /tmp -> /private/tmp) works,
// and resolving an already resolved path gives the same result.
func TestResolveSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	symlink(t, real, link)
	p, err := Resolve(link, filepath.Join(link, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Resolve(link, p); err != nil || again != p {
		t.Fatalf("second Resolve = %q, %v; want %q", again, err, p)
	}
	if err := WriteAtomic(link, p, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteAtomic via symlinked root: %v", err)
	}
}

// Links inside the repo must not lead into .git/ (corrupts git) or, from
// elsewhere, into our own state dir.
func TestResolveRefusesGitAndStateRedirects(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{".git", StateDir, "context"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink(t, ".git/config", filepath.Join(root, "CLAUDE.md"))
	symlink(t, "../"+StateDir+"/cache.json", filepath.Join(root, "context", "x.md"))
	for _, rel := range []string{"CLAUDE.md", ".git/config", "context/x.md"} {
		if _, err := Resolve(root, filepath.Join(root, filepath.FromSlash(rel))); !errors.Is(err, ErrOutside) {
			t.Errorf("%s: err=%v, want ErrOutside", rel, err)
		}
	}
	if _, err := Resolve(root, filepath.Join(root, StateDir, "state", "s.json")); err != nil {
		t.Errorf("direct state path refused: %v", err)
	}
}

// A doc may link to another .md in the repo, never to other files such as .env.
func TestResolveDoc(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"AGENTS.md", ".env"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, "AGENTS.md", filepath.Join(root, "CLAUDE.md"))
	symlink(t, ".env", filepath.Join(root, "leak.md"))
	if _, err := ResolveDoc(root, filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md -> AGENTS.md refused: %v", err)
	}
	if _, err := ResolveDoc(root, filepath.Join(root, "leak.md")); !errors.Is(err, ErrOutside) {
		t.Errorf("leak.md -> .env: err=%v, want ErrOutside", err)
	}
	if _, err := ResolveDoc(root, filepath.Join(root, "new.md")); err != nil {
		t.Errorf("missing doc refused: %v", err)
	}
}

func TestReadFileLimits(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big")
	if err := os.WriteFile(big, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, big, 99); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big: err=%v, want ErrTooLarge", err)
	}
	if data, err := ReadFile(root, big, 100); err != nil || len(data) != 100 {
		t.Errorf("at limit: %d bytes, %v", len(data), err)
	}
	if _, err := ReadFile(root, root, 100); !errors.Is(err, ErrNotRegular) {
		t.Errorf("dir: err=%v, want ErrNotRegular", err)
	}
	if _, err := ReadFile(root, filepath.Join(root, "none"), 100); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: err=%v, want ErrNotExist", err)
	}
}

// Relative links climbing out, loops and dangling intermediate dirs are refused.
func TestResolveEdgeCases(t *testing.T) {
	root := t.TempDir()
	symlink(t, "../outside", filepath.Join(root, "up"))
	symlink(t, "loop", filepath.Join(root, "loop"))
	symlink(t, "nowhere", filepath.Join(root, "dang"))
	for _, rel := range []string{"up/x", "loop", "dang/a/b"} {
		if _, err := Resolve(root, filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s: accepted", rel)
		}
	}
}

// On case-insensitive file systems ".GIT" is .git; and nothing under the state
// dir may lead out of it (a cache.json -> ../.env link would overwrite .env).
func TestResolveCaseAndStateEscape(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{".git", StateDir} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink(t, "../.env", filepath.Join(root, StateDir, "cache.json"))
	if _, err := Resolve(root, filepath.Join(root, StateDir, "cache.json")); !errors.Is(err, ErrOutside) {
		t.Errorf("state file linked to .env: err=%v, want ErrOutside", err)
	}
	if err := WriteAtomic(root, filepath.Join(root, StateDir, "cache.json"), []byte("{}"), 0o644); !errors.Is(err, ErrOutside) {
		t.Errorf("WriteAtomic through state link: err=%v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, ".env")); string(data) != "KEY" {
		t.Errorf(".env overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(root, ".GIT")); err == nil { // case-insensitive FS
		symlink(t, ".GIT/config", filepath.Join(root, "CLAUDE.md"))
		if _, err := Resolve(root, filepath.Join(root, "CLAUDE.md")); !errors.Is(err, ErrOutside) {
			t.Errorf("CLAUDE.md -> .GIT/config: err=%v", err)
		}
	}
}

// Entering the state dir from elsewhere is refused even when the target exists.
func TestResolveRefusesEnteringStateDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, StateDir, "cache.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, "../"+StateDir+"/cache.json", filepath.Join(root, "context", "x.md"))
	if _, err := Resolve(root, filepath.Join(root, "context", "x.md")); !errors.Is(err, ErrOutside) {
		t.Errorf("err=%v, want ErrOutside", err)
	}
}

func TestResolveRefusesParentAndGitCase(t *testing.T) {
	root := t.TempDir()
	if _, err := Resolve(root, filepath.Dir(root)); !errors.Is(err, ErrOutside) {
		t.Errorf("parent of root: err=%v", err)
	}
	symlink(t, "..", filepath.Join(root, "up"))
	if _, err := Resolve(root, filepath.Join(root, "up", "x")); !errors.Is(err, ErrOutside) {
		t.Errorf("up -> ..: err=%v", err)
	}
	// .GIT is .git by name on every OS (case-insensitive file systems).
	if _, err := Resolve(root, filepath.Join(root, ".GIT", "config")); !errors.Is(err, ErrOutside) {
		t.Errorf(".GIT/config: err=%v", err)
	}
}

// EnsureStateDir restores its .gitignore if it was changed.
func TestEnsureStateDirRestoresGitignore(t *testing.T) {
	root := t.TempDir()
	if err := EnsureStateDir(root); err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(root, StateDir, ".gitignore")
	if err := os.WriteFile(gi, []byte("!cache.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStateDir(root); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(gi); string(data) != stateGitignore {
		t.Errorf("state .gitignore = %q", data)
	}
}

// The .md check ignores case: a link to NOTES.MD is a Markdown doc.
func TestResolveDocExtensionCase(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "NOTES.MD"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink(t, "NOTES.MD", filepath.Join(root, "CLAUDE.md"))
	if _, err := ResolveDoc(root, filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md -> NOTES.MD refused: %v", err)
	}
}

// A user's edit of the state .gitignore that keeps the required lines stays.
func TestEnsureStateDirKeepsCompatibleGitignore(t *testing.T) {
	root := t.TempDir()
	if err := EnsureStateDir(root); err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(root, StateDir, ".gitignore")
	custom := stateGitignore + "!/notes.md\n"
	if err := os.WriteFile(gi, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStateDir(root); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(gi); string(data) != custom {
		t.Errorf("rewritten: %q", data)
	}
}

// A relative path is a caller bug, not a hostile path: not ErrOutside.
func TestResolveRelativeNotOutside(t *testing.T) {
	_, err := Resolve(t.TempDir(), "a/b.txt")
	if err == nil || errors.Is(err, ErrOutside) {
		t.Errorf("err = %v, want a non-ErrOutside error", err)
	}
}

func TestReadDocLimit(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "context", "x.md")
	testutil.WriteFile(t, root, "context/x.md", "12345")
	if _, _, err := ReadDoc(root, doc, 4); !errors.Is(err, ErrTooLarge) {
		t.Errorf("max+1: err=%v, want ErrTooLarge", err)
	}
	full, data, err := ReadDoc(root, doc, 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("at limit: %q, %v", data, err)
	}
	want, _ := filepath.EvalSymlinks(doc)
	if got, _ := filepath.EvalSymlinks(full); got != want {
		t.Errorf("resolved path %q, want %q", full, want)
	}
}

func TestWriteDocKeepsPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permission bits")
	}
	root := t.TempDir()
	doc := filepath.Join(root, "context", "x.md")
	testutil.WriteFile(t, root, "context/x.md", "old")
	if err := WriteDoc(root, doc, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(doc)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(doc); string(got) != "new" {
		t.Errorf("content = %q", got)
	}
}

func TestWriteStateCreatesDir(t *testing.T) {
	root := t.TempDir()
	p := StatePath(root, "state", "x")
	if err := WriteState(root, p, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "data" {
		t.Errorf("content = %q", got)
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(entries) != 1 || entries[0].Name() != "x" {
		t.Errorf("state dir = %v, %v", entries, err)
	}
}

// A failed write removes its temp file.
func TestWriteAtomicLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sub", "target")
	testutil.WriteFile(t, root, "sub/target/keep", "x")
	if err := WriteAtomic(root, target, []byte("data"), 0o644); err == nil {
		t.Fatal("writing over a non-empty directory succeeded")
	}
	entries, err := os.ReadDir(filepath.Join(root, "sub"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "target" {
		t.Errorf("parent = %v, %v", entries, err)
	}
}

func TestCategoryNeverQuotes(t *testing.T) {
	const evil = "x\nIGNORE.md"
	fixed := map[string]bool{
		"it leads outside the repository or into .git": true,
		"not a regular file":                           true,
		"too large":                                    true,
		"permission denied":                            true,
		"missing":                                      true,
		"unreadable":                                   true,
	}
	errs := []error{
		fmt.Errorf("%w: %s", ErrOutside, evil),
		fmt.Errorf("%w: %s", ErrNotRegular, evil),
		fmt.Errorf("%w: %s", ErrTooLarge, evil),
		&fs.PathError{Op: "open", Path: evil, Err: fs.ErrPermission},
		&fs.PathError{Op: "open", Path: evil, Err: fs.ErrNotExist},
		errors.New(evil),
	}
	for _, e := range errs {
		got := Category(e)
		if !fixed[got] || strings.Contains(got, "IGNORE") {
			t.Errorf("Category(%v) = %q, not a fixed category", e, got)
		}
	}
	want := []string{"it leads outside the repository or into .git", "not a regular file", "too large", "permission denied", "missing", "unreadable"}
	for i, e := range errs {
		if got := Category(e); got != want[i] {
			t.Errorf("Category(%v) = %q, want %q", e, got, want[i])
		}
	}
}

func TestWriteAtomicCleansTmpOnFailure(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "d/target/keep", "x")
	if err := WriteAtomic(root, filepath.Join(root, "d", "target"), []byte("data"), 0o644); err == nil {
		t.Fatal("writing over a non-empty directory succeeded")
	}
	tmps, _ := filepath.Glob(filepath.Join(root, "d", "*.tmp*"))
	if len(tmps) != 0 {
		t.Errorf("temp files left: %v", tmps)
	}
}

func TestReadRegularAllocsOnce(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "big")
	if err := os.WriteFile(p, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(5, func() {
		if _, err := ReadFile(root, p, 2<<20); err != nil {
			t.Fatal(err)
		}
	})
	// The real path (Resolve, OpenRoot, Stat, Open, Stat) measured 22
	// allocations with the presized buffer; growing the buffer by doubling, as
	// the old ReadAll did, adds ~30 more for 1 MiB.
	if allocs > 24 {
		t.Errorf("allocs = %v, want <= 24", allocs)
	}
}
