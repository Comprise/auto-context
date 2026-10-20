package units

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"auto-context/internal/gitx"
)

// maxList caps the git file listing (about 2M paths). A variable so tests
// can lower it.
var maxList = 256 << 20

// ErrGitTimeout stops discovery when git times out listing the files.
var ErrGitTimeout = errors.New("git timed out listing the files; the repository is too large or slow for auto-context (add big generated directories to .gitignore)")

// ErrListTooLarge stops discovery when git lists more than maxList: walking
// such a tree instead would take longer than the hook timeouts.
var ErrListTooLarge = errors.New("git ls-files lists more than 256 MiB of paths; the repository is too large for auto-context (add big generated directories to .gitignore)")

// gitFiles lists tracked and untracked, not ignored files under root (slash
// paths relative to root) when root is inside a git work tree. ok is false
// outside git; a warning explains a git failure where git was expected (a
// crash included). err is ErrGitTimeout or ErrListTooLarge.
func gitFiles(root string) (files []string, ok bool, warning string, err error) {
	res, gerr := gitx.Run(root, maxList, "ls-files", "-co", "--exclude-standard", "-z")
	if gerr != nil {
		_, statErr := os.Lstat(filepath.Join(root, ".git"))
		// Quiet only without a .git in root: with one, "not a git repository"
		// means a broken repo (e.g. a gitfile pointing nowhere).
		notRepo := gitx.Code(gerr) == 128 && strings.Contains(gerr.Error(), "not a git repository")
		if statErr != nil && (notRepo || gitx.NotRun(gerr)) {
			return nil, false, "", nil // not a git repo, or no git: walk quietly
		}
		var ge *gitx.Error
		if errors.As(gerr, &ge) && ge.TimedOut {
			// Walking a tree git could not list in time would miss the hook timeout too.
			return nil, false, "", ErrGitTimeout
		}
		return nil, false, "git ls-files failed (" + gitx.Brief(gerr) + "); walked the tree instead, so .git/info/exclude and global git excludes are not applied", nil
	}
	if res.Truncated {
		return nil, false, "", ErrListTooLarge
	}
	// Entry by entry, without a string copy of the whole listing.
	var prev []byte
	for out := res.Out; len(out) > 0; {
		f, rest, _ := bytes.Cut(out, []byte{0})
		out = rest
		if len(f) > 0 && !bytes.Equal(f, prev) { // unmerged entries repeat, next to each other
			files = append(files, string(f))
		}
		prev = f
	}
	if len(files) == 0 {
		// An empty listing can mean the root itself is ignored by the
		// enclosing repository: git then lists nothing under it.
		_, cerr := gitx.Run(root, 0, "check-ignore", "-q", ".")
		var ge *gitx.Error
		switch {
		case cerr == nil:
			return nil, false, "the root is ignored by the enclosing git repository; walked the tree instead", nil
		case gitx.Code(cerr) == 1: // not ignored: the repository is empty
		case errors.As(cerr, &ge) && ge.TimedOut:
			return nil, false, "", ErrGitTimeout
		default:
			// An empty list would drop every unit (and the index with it).
			return nil, false, "git check-ignore failed (" + gitx.Brief(cerr) + ") after an empty file list; walked the tree instead", nil
		}
	}
	return files, true, "", nil
}
