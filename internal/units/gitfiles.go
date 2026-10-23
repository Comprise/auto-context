package units

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"auto-context/internal/gitx"
)

// maxList caps the git file listing in bytes. A variable so tests can lower it.
var maxList = 256 << 20

// maxFiles caps the files one Discover lists (git) or walks: more would not
// fit in the hook timeouts or memory. A variable so tests can lower it.
var maxFiles = 800_000

// ErrGitTimeout stops discovery when git times out listing the files.
var ErrGitTimeout = errors.New("git timed out listing the files: the repository is too large or slow, or a .gitignore/exclude file is not a regular file (add big generated directories to .gitignore; replace a FIFO or device .gitignore with a regular file)")

// ErrTimeUp stops discovery when the caller's context ends (a hook's
// budget, an interrupted CLI) before the git listing or during the walk.
var ErrTimeUp = errors.New("listing the files stopped: the hook's time is up or the command was interrupted (add big generated directories to .gitignore)")

// ErrListTooLarge stops discovery when the listing holds more than maxFiles
// files or maxList bytes: walking such a tree instead would take longer than
// the hook timeouts.
var ErrListTooLarge = errors.New("the repository lists more than 800000 files (or 256 MiB of paths); it is too large for auto-context (add big generated directories to .gitignore)")

// gitFiles lists tracked and untracked, not ignored files under root (slash
// paths relative to root) when root is inside a git work tree of its own
// (gitx.WorkTreeOK). other[i] tells that files[i] is untracked (not in the
// index). ok is false outside git; a warning explains why git was not used
// where it was expected (a crash included). err is ErrGitTimeout (also when
// ctx ends while git runs) or ErrListTooLarge.
func gitFiles(ctx context.Context, root string) (files []string, other []bool, ok bool, warning string, err error) {
	wtOK, gerr := gitx.WorkTreeOK(ctx, root)
	if gerr != nil {
		w, err := gitFailed(root, gerr, "git rev-parse")
		return nil, nil, false, w, err
	}
	if !wtOK {
		return nil, nil, false, "git's work tree or git dir lies outside the project (core.worktree or a .git file); walked the tree instead", nil
	}
	// core.untrackedCache only lets git use the index extension if present.
	res, gerr := gitx.Run(ctx, root, maxList, "-c", "core.untrackedCache=true", "ls-files", "-co", "--exclude-standard", "-t", "-z")
	if gerr != nil {
		w, err := gitFailed(root, gerr, "git ls-files")
		return nil, nil, false, w, err
	}
	if res.Truncated {
		return nil, nil, false, "", ErrListTooLarge
	}
	// Entry by entry ("<tag> <path>"), without a string copy of the listing.
	var prev []byte
	for out := res.Out; len(out) > 0; {
		e, rest, _ := bytes.Cut(out, []byte{0})
		out = rest
		if len(e) < 3 || e[1] != ' ' {
			continue
		}
		f := e[2:]
		if !bytes.Equal(f, prev) { // unmerged entries repeat, next to each other
			if len(files) == maxFiles {
				return nil, nil, false, "", ErrListTooLarge
			}
			files = append(files, string(f))
			other = append(other, e[0] == '?')
		}
		prev = f
	}
	if len(files) == 0 {
		// An empty listing can mean the root itself is ignored by the
		// enclosing repository: git then lists nothing under it.
		_, cerr := gitx.Run(ctx, root, 0, "check-ignore", "-q", ".")
		switch {
		case cerr == nil:
			return nil, nil, false, "the root is ignored by the enclosing git repository; walked the tree instead", nil
		case gitx.Code(cerr) == 1: // not ignored: the repository is empty
		case timedOut(cerr):
			return nil, nil, false, "", ErrGitTimeout
		default:
			// An empty list would drop every unit (and the index with it).
			return nil, nil, false, "git check-ignore failed (" + gitx.Brief(cerr) + ") after an empty file list; walked the tree instead", nil
		}
	}
	return files, other, true, "", nil
}

// gitFailed turns a failed git call (what names it) into gitFiles' answer:
// quiet walk without a repo or without git, ErrGitTimeout on a timeout, else
// a walk with a warning.
func gitFailed(root string, gerr error, what string) (warning string, err error) {
	_, statErr := os.Lstat(filepath.Join(root, ".git"))
	// Quiet only without a .git in root: with one, "not a git repository"
	// means a broken repo (e.g. a gitfile pointing nowhere).
	if statErr != nil && (gitx.NotRepo(gerr) || gitx.NotRun(gerr)) {
		return "", nil
	}
	if timedOut(gerr) {
		// Walking a tree git could not list in time would miss the hook timeout too.
		return "", ErrGitTimeout
	}
	return what + " failed (" + gitx.Brief(gerr) + "); walked the tree instead, so .git/info/exclude and global git excludes are not applied", nil
}

func timedOut(err error) bool {
	var ge *gitx.Error
	return errors.As(err, &ge) && ge.TimedOut
}
