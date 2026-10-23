package project

import (
	"os"
	"path/filepath"

	"auto-context/internal/config"
)

// FindRoot walks up from cwd to a directory holding .git or config.FileName
// owned by the current user. It never climbs into $HOME from below it, so a
// stray ~/.auto-context/config.yaml (or a dotfiles ~/.git) does not make the
// whole home the project; without a marker it returns cwd. $HOME and each
// directory are compared cleaned and with symlinks resolved.
func FindRoot(cwd string) string {
	h, _ := os.UserHomeDir()
	home := canonical(h)
	dir := cwd
	for {
		if home != "" && dir != cwd && canonical(dir) == home {
			return cwd
		}
		for _, m := range []string{".git", config.FileName} {
			if fi, err := os.Stat(filepath.Join(dir, m)); err == nil && ownedByUser(fi) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

// canonical is p cleaned, with symlinks resolved when that works.
func canonical(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
