package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// NotRepo reports whether err from Run means dir is not inside a git
// repository (exit 128 with git's "not a git repository").
func NotRepo(err error) bool {
	e, ok := errors.AsType[*Error](err)
	return ok && e.Code == 128 && strings.Contains(e.Stderr, "not a git repository")
}

// WorkTreeOK reports whether git at root works on root's own tree: the real
// root is the work tree's top level or lies under it, and the git dir is
// either (a) under the top level, (b) a linked worktree's dir whose `gitdir`
// file points back at <top level>/.git, or (c) a submodule's dir: under
// <A>/.git/modules/ or <A>/.git/worktrees/<n>/modules/ for a strict ancestor
// A of root (for the latter, A may also be the main repository of a linked
// worktree that is a strict ancestor of root). When the top level is above
// root, the git dir must not lie under root (core.worktree pointing up).
// core.worktree or a `.git` file sending git to another tree or repository
// gives false and a nil error; an error means git did not answer (NotRepo
// tells "no repo").
func WorkTreeOK(ctx context.Context, root string) (bool, error) {
	res, err := Run(ctx, root, 0, "rev-parse", "--show-toplevel", "--absolute-git-dir")
	if err != nil {
		return false, err
	}
	lines := strings.Split(strings.TrimRight(string(res.Out), "\n"), "\n")
	if len(lines) != 2 || lines[0] == "" || lines[1] == "" {
		// A bare repository or an unexpected answer: no work tree to trust.
		return false, nil
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return false, err
	}
	top, gitDir := resolve(lines[0]), resolve(lines[1])
	if !within(real, top) {
		return false, nil
	}
	if top != real && within(gitDir, real) {
		return false, nil
	}
	if within(gitDir, top) {
		return true, nil
	}
	if pointsAt(gitDir, filepath.Join(top, ".git")) {
		return true, nil
	}
	return submoduleDir(gitDir, real), nil
}

// pointsAt reports whether the `gitdir` file in dir names want.
func pointsAt(dir, want string) bool {
	back, ok := backPointer(dir)
	return ok && back == want
}

// backPointer reads the `gitdir` file of a linked worktree's admin dir: the
// path of the worktree's `.git` file. With worktree.useRelativePaths (git
// 2.48+) it is relative to dir itself, not to the process cwd.
func backPointer(dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "gitdir"))
	if err != nil {
		return "", false
	}
	p := filepath.FromSlash(strings.TrimSpace(string(b)))
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return resolve(p), true
}

// submoduleDir reports whether gitDir is a submodule's git dir of a
// superproject around root: <A>/.git/modules/<...>, or
// <A>/.git/worktrees/<n>/modules/<...> where A, or the linked worktree <n>
// itself, is a strict ancestor of root.
func submoduleDir(gitDir, root string) bool {
	for a := filepath.Dir(root); ; a = filepath.Dir(a) {
		common := filepath.Join(a, ".git")
		if rel, err := filepath.Rel(common, gitDir); err == nil && within(gitDir, common) {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) >= 2 && parts[0] == "modules" {
				return true
			}
			if len(parts) >= 4 && parts[0] == "worktrees" && parts[2] == "modules" {
				return true
			}
		}
		if filepath.Dir(a) == a {
			break
		}
	}
	// A linked worktree beside its main repository: the submodule's git dir
	// sits in the main repository, the worktree itself contains root.
	parts := strings.Split(gitDir, string(filepath.Separator))
	for i := len(parts) - 3; i >= 1; i-- {
		if parts[i] != "worktrees" || parts[i+2] != "modules" || parts[i-1] != ".git" || i+3 >= len(parts) {
			continue
		}
		wt := strings.Join(parts[:i+2], string(filepath.Separator))
		back, ok := backPointer(wt)
		if !ok {
			continue
		}
		if filepath.Base(back) == ".git" {
			if w := filepath.Dir(back); w != root && within(root, w) {
				return true
			}
		}
	}
	return false
}

// resolve cleans p and follows symlinks where it can: git and the caller may
// spell the same directory differently (macOS /var vs /private/var).
func resolve(p string) string {
	p = filepath.Clean(filepath.FromSlash(p))
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// within reports whether p is dir or lies under it (both clean, absolute).
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
