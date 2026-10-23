// Package fsutil holds the path-trust policy (Resolve), bounded reads,
// atomic writes and state-dir setup.
package fsutil

import (
	"bytes"
	"crypto/rand"
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
	return !filepath.IsLocal(rel)
}

// Category maps a path or read error to a fixed phrase. Error texts embed
// repository-controlled paths, so output meant for the terminal or for Claude
// uses this; the full error goes to the log. The result never contains err's text.
func Category(err error) string {
	switch {
	case errors.Is(err, ErrOutside):
		return "it leads outside the repository or into .git"
	case errors.Is(err, ErrNotRegular):
		return "not a regular file"
	case errors.Is(err, ErrTooLarge):
		return "too large"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "missing"
	}
	return "unreadable"
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

// openRoot opens the resolved root for one operation (the caller closes it)
// and returns it with that root's path and full (a Resolve result) relative to
// it. Reads and writes go through it with root-relative names, so a directory
// or symlink swapped in after Resolve cannot lead them outside the repository.
// The handle is not cached: a cached one would go stale if the root directory
// were recreated and would keep it busy on Windows.
func openRoot(root, full string) (r *os.Root, realRoot, rel string, err error) {
	if realRoot, err = realPath(root); err != nil {
		return nil, "", "", err
	}
	rel, ok := within(realRoot, full)
	if !ok {
		return nil, "", "", fmt.Errorf("%w: %s", ErrOutside, full)
	}
	if r, err = os.OpenRoot(realRoot); err != nil {
		return nil, "", "", err
	}
	return r, realRoot, rel, nil
}

// absErr joins base to the root-relative names in an error from an os.Root
// operation, so the log names files by their full path as before.
func absErr(err error, base string) error {
	var pe *os.PathError
	if errors.As(err, &pe) && !filepath.IsAbs(pe.Path) {
		pe.Path = filepath.Join(base, pe.Path)
	}
	if le, ok := errors.AsType[*os.LinkError](err); ok {
		le.Old, le.New = filepath.Join(base, le.Old), filepath.Join(base, le.New)
	}
	return err
}

// ReadFile reads path, which must be inside root (see Resolve) and a regular
// file of at most limit bytes. A FIFO or device is refused before it is
// opened, and one swapped in after that check does not block (see readOpened).
func ReadFile(root, path string, limit int64) ([]byte, error) {
	full, err := Resolve(root, path)
	if err != nil {
		return nil, err
	}
	return readResolved(root, full, limit)
}

// ReadDoc is ReadFile with ResolveDoc; it also returns the resolved path.
func ReadDoc(root, path string, limit int64) (string, []byte, error) {
	full, err := ResolveDoc(root, path)
	if err != nil {
		return "", nil, err
	}
	data, err := readResolved(root, full, limit)
	return full, data, err
}

func readResolved(root, full string, limit int64) ([]byte, error) {
	r, base, rel, err := openRoot(root, full)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	// A FIFO, device or oversized file already there is refused without
	// opening it; readOpened checks both again on the opened file.
	fi, err := r.Stat(rel)
	if err != nil {
		return nil, absErr(err, base)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, full)
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%w: %s (%d bytes, limit %d)", ErrTooLarge, full, fi.Size(), limit)
	}
	if beforeOpen != nil {
		beforeOpen()
	}
	f, err := r.OpenFile(rel, readFlags, 0)
	if err != nil {
		return nil, absErr(err, base)
	}
	defer f.Close()
	return readOpened(f, full, limit)
}

// beforeOpen, if set by a test, runs between the pre-open check and the open.
var beforeOpen func()

// readOpened reads f, opened with readFlags; full names it in errors. The
// open did not block on a FIFO, and the mode of the opened file is checked
// again, so a FIFO or device swapped in after the pre-open check is refused
// without a race.
func readOpened(f *os.File, full string, limit int64) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, full)
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%w: %s (%d bytes, limit %d)", ErrTooLarge, full, fi.Size(), limit)
	}
	// Size the buffer from the stat so the read does not grow it by doubling;
	// the slack lets ReadFrom see EOF without a reallocation.
	buf := bytes.NewBuffer(make([]byte, 0, fi.Size()+512))
	if _, err := buf.ReadFrom(io.LimitReader(f, limit+1)); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, full)
	}
	return data, nil
}

// WriteAtomic writes data via a temp file in the same dir and renames it into
// place, with fsync. path must be inside root; symlinks are resolved first
// (see Resolve), so a link inside the repo keeps pointing at the new content.
func WriteAtomic(root, path string, data []byte, perm os.FileMode) error {
	full, err := Resolve(root, path)
	if err != nil {
		return err
	}
	return writeResolved(root, full, data, perm, true)
}

// WriteDoc is WriteAtomic under the document rule of ResolveDoc: a symlinked
// doc is written only if it points at another .md file in the repo.
func WriteDoc(root, path string, data []byte, perm os.FileMode) error {
	full, err := ResolveDoc(root, path)
	if err != nil {
		return err
	}
	return writeResolved(root, full, data, perm, true)
}

// WriteState is WriteAtomic without fsync, for state that can be rebuilt
// (hash cache, snapshots, journals): an fsync per hook costs more than a lost
// cache entry after a crash.
func WriteState(root, path string, data []byte) error {
	full, err := Resolve(root, path)
	if err != nil {
		return err
	}
	return writeResolved(root, full, data, 0o644, false)
}

// writeResolved writes full, a Resolve result, through the open root. The
// rename replaces the resolved name, never a symlink in front of it.
func writeResolved(root, full string, data []byte, perm os.FileMode, doSync bool) error {
	r, base, rel, err := openRoot(root, full)
	if err != nil {
		return err
	}
	defer r.Close()
	return absErr(writeAtomic(r, rel, data, perm, doSync), base)
}

// createTemp is os.CreateTemp for an os.Root, which has none: an exclusive
// create under a random name next to name.
func createTemp(r *os.Root, name string) (*os.File, string, error) {
	for range 10 {
		tmp := name + ".tmp" + rand.Text()[:12] // 60 random bits, short like CreateTemp
		f, err := r.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return f, tmp, err
	}
	return nil, "", fmt.Errorf("fsutil: no free temp name for %s", name)
}

func writeAtomic(r *os.Root, name string, data []byte, perm os.FileMode, doSync bool) (err error) {
	if err = r.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	f, tmp, err := createTemp(r, name)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			r.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if doSync {
		if err = f.Sync(); err != nil {
			return err
		}
	}
	if err = f.Chmod(perm); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return r.Rename(tmp, name)
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
// .auto-context/.gitignore above, so local state stays out of git without
// editing the user's .gitignore. A file that lacks either line "/*" or
// "!/config.yaml" (e.g. a bare "*") is rewritten. A state dir
// leading outside the repo is an error.
func EnsureStateDir(root string) error {
	state, err := Resolve(root, StatePath(root, "state"))
	if err != nil {
		return err
	}
	r, base, rel, err := openRoot(root, state)
	if err != nil {
		return err
	}
	err = r.MkdirAll(rel, 0o755)
	r.Close()
	if err != nil {
		return absErr(err, base)
	}
	gi := StatePath(root, ".gitignore")
	if data, err := ReadFile(root, gi, 64<<10); err == nil && hasLines(data, "/*", "!/config.yaml") {
		return nil // ours, or the user's edit that still ignores the state and keeps the config
	}
	return WriteAtomic(root, gi, []byte(stateGitignore), 0o644)
}
