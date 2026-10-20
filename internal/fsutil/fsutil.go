// Package fsutil holds the path-trust policy (Resolve), bounded reads,
// atomic writes and state-dir setup.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// StateDir is the per-project directory for service files.
const StateDir = ".auto-context"

// StatePath joins root, StateDir and elem: every service file lives there.
func StatePath(root string, elem ...string) string {
	return filepath.Join(append([]string{root, StateDir}, elem...)...)
}

// ErrOutside means a path leads outside the repository, into .git/, or into
// our state dir from elsewhere (through a symlink, possibly dangling, or a
// Windows junction). A cloned repo could use such a link to redirect our reads
// or writes, so we refuse.
var ErrOutside = errors.New("refusing a path that leads outside the repository or into .git")

// ErrNotRegular and ErrTooLarge are returned by ReadFile and ReadDoc.
var (
	ErrNotRegular = errors.New("not a regular file")
	ErrTooLarge   = errors.New("file too large")
)

func escapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

// within returns path relative to base, if it is inside base.
func within(base, path string) (string, bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || escapes(rel) {
		return "", false
	}
	return rel, true
}

func first(rel string) string {
	f, _, _ := strings.Cut(rel, string(filepath.Separator))
	return f
}

// Resolve returns path with its symlinks resolved, so callers read and write
// the real file: a link inside the repo (e.g. CLAUDE.md -> AGENTS.md) keeps
// working and survives our writes. Components that do not exist yet are kept
// as they are. path may be under root as given or under its resolved form, so
// resolving twice gives the same result. It fails with ErrOutside for a result
// outside root, inside .git/, or inside the state dir when path was not.
func Resolve(root, path string) (string, error) {
	full, _, err := resolve(root, path)
	return full, err
}

// ResolveDoc is Resolve for docs whose text we copy around (CLAUDE.md, context
// files): a symlinked doc must point at another .md file, so a link to .env or
// a key file is refused.
func ResolveDoc(root, path string) (string, error) {
	full, lexical, err := resolve(root, path)
	if err != nil {
		return "", err
	}
	if full != lexical && !strings.EqualFold(filepath.Ext(full), ".md") {
		return "", fmt.Errorf("%w: %s links to a non-.md file", ErrOutside, path)
	}
	return full, nil
}

// resolve returns the resolved path and the same path joined lexically to the
// resolved root (they differ when a symlink was followed).
func resolve(root, path string) (full, lexical string, err error) {
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("fsutil: %s is not an absolute path", path) // a caller bug, not a hostile path
	}
	realRoot, err := realPath(root)
	if err != nil {
		return "", "", err
	}
	rel, ok := within(root, path)
	if !ok {
		if rel, ok = within(realRoot, path); !ok {
			return "", "", fmt.Errorf("%w: %s", ErrOutside, path)
		}
	}
	lexical = filepath.Join(realRoot, rel)
	// Refuse irregular components: Windows junctions and mount points, which
	// EvalSymlinks does not resolve, show up as ModeIrregular. Without a
	// symlink among the existing components the path is already resolved.
	cur, linked := realRoot, false
	if rel != "." {
		for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			if err != nil {
				return "", "", err
			}
			if fi.Mode()&os.ModeIrregular != 0 {
				return "", "", fmt.Errorf("%w: %s", ErrOutside, cur)
			}
			linked = linked || fi.Mode()&os.ModeSymlink != 0
		}
	}
	full = lexical
	cur, rest := lexical, ""
	for linked {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			full = filepath.Join(real, rest)
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		if _, err := os.Lstat(cur); err == nil {
			// cur exists but does not resolve: a dangling symlink, target unknown.
			return "", "", fmt.Errorf("%w: %s", ErrOutside, cur)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = filepath.Dir(cur)
	}
	frel, ok := within(realRoot, full)
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrOutside, path)
	}
	// Compare top-level names case-insensitively and by identity: on macOS and
	// Windows ".GIT" is .git. A path under the state dir must stay in it (a
	// cache.json -> ../.env link would overwrite .env), and no other path may
	// lead into it.
	top, ltop := first(frel), first(rel)
	if same(realRoot, top, ".git") || same(realRoot, top, StateDir) != same(realRoot, ltop, StateDir) {
		return "", "", fmt.Errorf("%w: %s", ErrOutside, path)
	}
	return full, lexical, nil
}

// same reports whether the top-level entry name under root is the entry want.
// Results are memoized per process: every doc read asks about the same few
// top-level names.
func same(root, name, want string) bool {
	if name == "" || name == "." {
		return false
	}
	if strings.EqualFold(name, want) {
		return true
	}
	key := root + "\x00" + name + "\x00" + want
	if v, ok := sameCache.Load(key); ok {
		return v.(bool)
	}
	a, err1 := os.Stat(filepath.Join(root, name))
	b, err2 := os.Stat(filepath.Join(root, want))
	v := err1 == nil && err2 == nil && os.SameFile(a, b)
	sameCache.Store(key, v)
	return v
}

var sameCache sync.Map

// Abs joins a slash-separated root-relative path to root.
func Abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// realRoots memoizes EvalSymlinks of roots: one process resolves the same
// root for every path it touches.
var realRoots sync.Map

func realPath(root string) (string, error) {
	if v, ok := realRoots.Load(root); ok {
		return v.(string), nil
	}
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realRoots.Store(root, r)
	return r, nil
}

// ReadFile reads path, which must be inside root (see Resolve) and a regular
// file of at most max bytes. A FIFO or device is refused before it is opened.
func ReadFile(root, path string, max int64) ([]byte, error) {
	full, err := Resolve(root, path)
	if err != nil {
		return nil, err
	}
	return readRegular(full, max)
}

// ReadDoc is ReadFile with ResolveDoc; it also returns the resolved path.
func ReadDoc(root, path string, max int64) (string, []byte, error) {
	full, err := ResolveDoc(root, path)
	if err != nil {
		return "", nil, err
	}
	data, err := readRegular(full, max)
	return full, data, err
}

func readRegular(path string, max int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, path)
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("%w: %s (%d bytes, limit %d)", ErrTooLarge, path, fi.Size(), max)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, path)
	}
	return data, nil
}

// WriteAtomic writes data via a temp file in the same dir and renames it into
// place, with fsync. path must be inside root; symlinks are resolved first
// (see Resolve).
func WriteAtomic(root, path string, data []byte, perm os.FileMode) error {
	full, err := Resolve(root, path)
	if err != nil {
		return err
	}
	return writeAtomic(full, data, perm, true)
}

// WriteDoc is WriteAtomic under the document rule of ResolveDoc: a symlinked
// doc is written only if it points at another .md file in the repo.
func WriteDoc(root, path string, data []byte, perm os.FileMode) error {
	full, err := ResolveDoc(root, path)
	if err != nil {
		return err
	}
	return writeAtomic(full, data, perm, true)
}

// WriteState is WriteAtomic without fsync, for state that can be rebuilt
// (hash cache, snapshots, journals): an fsync per hook costs more than a lost
// cache entry after a crash.
func WriteState(root, path string, data []byte) error {
	full, err := Resolve(root, path)
	if err != nil {
		return err
	}
	return writeAtomic(full, data, 0o644, false)
}

func writeAtomic(path string, data []byte, perm os.FileMode, sync bool) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if sync {
		if err = f.Sync(); err != nil {
			return err
		}
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// stateGitignore keeps everything in StateDir out of git except the committed
// config (config.yaml) and this file itself.
const stateGitignore = "# auto-context: config.yaml is committed, the rest is local state\n/*\n!/.gitignore\n!/config.yaml\n"

// hasLines reports whether data holds every line in want (whitespace trimmed).
func hasLines(data []byte, want ...string) bool {
	have := map[string]bool{}
	for l := range strings.SplitSeq(string(data), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

// EnsureStateDir creates <root>/.auto-context/state and the
// .auto-context/.gitignore above (rewritten unless it still has "/*" and
// "!/config.yaml", e.g. a bare "*"), so local
// state stays out of git without editing the user's .gitignore. A state dir
// leading outside the repo is an error.
func EnsureStateDir(root string) error {
	state, err := Resolve(root, StatePath(root, "state"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return err
	}
	gi := StatePath(root, ".gitignore")
	if data, err := ReadFile(root, gi, 64<<10); err == nil && hasLines(data, "/*", "!/config.yaml") {
		return nil // ours, or the user's edit that still ignores the state and keeps the config
	}
	return WriteAtomic(root, gi, []byte(stateGitignore), 0o644)
}
