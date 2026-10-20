// Package project opens a repository for the hooks and the CLI: config, unit
// tree and hash cache, and the operations both need (activation, status, ack,
// root index, exclude, saving the cache).
package project

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/fsutil"
	"auto-context/internal/gitx"
	"auto-context/internal/hashing"
	"auto-context/internal/rootindex"
	"auto-context/internal/units"
)

// Active reports whether the plugin is enabled in root: it has
// .auto-context/config.yaml or context/_root.md (created by /auto-context:init), as a
// regular file inside the repo. A bare context/ directory is too common a name
// to count. Inactive projects are never written to.
func Active(root string) bool {
	ctx := units.ContextPath(units.RootUnit)
	for _, m := range []string{config.FileName, ctx} {
		p, err := fsutil.Resolve(root, fsutil.Abs(root, m))
		if err != nil {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// ActivationRefused reports a marker (.auto-context/config.yaml or context/_root.md)
// that exists but is not used: it links outside the repo or into .git, is not
// a regular file, or cannot be inspected, so the plugin stays off.
func ActivationRefused(root string) bool {
	ctx := units.ContextPath(units.RootUnit)
	for _, m := range []string{config.FileName, ctx} {
		full := fsutil.Abs(root, m)
		if _, err := os.Lstat(full); err != nil {
			continue
		}
		p, err := fsutil.Resolve(root, full)
		if err != nil {
			return true
		}
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// Project is an opened repository, read-only after Open (but for the lazily
// loaded cache): the tree, cache and config belong together.
type Project struct {
	root string
	cfg  config.Config
	tree *units.Tree
	// cache is loaded on first use: PromptSubmit and most Stops never hash.
	cache *hashing.Cache
	// cfgProblems lists settings that were dropped (the rest applies);
	// cfgErr is a config that could not be read or parsed (defaults apply).
	cfgProblems config.Problems
	cfgErr      error
	// cacheErr explains an unusable hash cache (hashing restarts cold).
	cacheErr error
}

// Root is the repository root the project was opened at.
func (p *Project) Root() string { return p.root }

// Config is the config in effect: the defaults when it is broken.
func (p *Project) Config() config.Config { return p.cfg }

// Tree is the unit tree Discover built.
func (p *Project) Tree() *units.Tree { return p.tree }

// ConfigProblems lists settings of the config that were dropped (nil if none).
func (p *Project) ConfigProblems() config.Problems { return p.cfgProblems }

// ConfigErr is why the config could not be read or parsed (nil if it was).
func (p *Project) ConfigErr() error { return p.cfgErr }

// Open loads the config and discovers units; the hash cache loads on first
// use. Only a failed discovery is an error; config problems are kept.
func Open(root string) (*Project, error) {
	cfg, problems, cfgErr := config.Load(root)
	tree, err := units.Discover(root, cfg)
	if err != nil {
		return nil, err
	}
	return &Project{root: root, cfg: cfg, tree: tree, cfgProblems: problems, cfgErr: cfgErr}, nil
}

// ErrConfigBroken refuses an operation that would act on the default units
// while the config cannot be read or parsed.
var ErrConfigBroken = errors.New(config.FileName + " cannot be read or parsed; fix it first")

// IndexFile is the name of the per-unit index file from the config.
func (p *Project) IndexFile() string { return p.cfg.IndexFile }

// ConfigBroken reports a config that could not be read or parsed at all: the
// defaults apply, so the unit tree may not be the user's.
func (p *Project) ConfigBroken() bool { return p.cfgErr != nil }

// ConfigIgnored reports a config file that git ignores (e.g. the user's
// .gitignore lists .auto-context/), so the settings would not be committed.
// err is a git failure other than "not a repository" or "no git".
func (p *Project) ConfigIgnored() (bool, error) {
	if _, err := os.Lstat(fsutil.Abs(p.root, config.FileName)); err != nil {
		return false, nil
	}
	_, err := gitx.Run(p.root, 0, "check-ignore", "-q", "--", config.FileName)
	switch {
	case err == nil:
		return true, nil
	case gitx.Code(err) == 1, gitx.NotRun(err),
		gitx.Code(err) == 128 && strings.Contains(err.Error(), "not a git repository"):
		return false, nil
	}
	return false, err
}

// StateDirErr reports a .auto-context that leads outside the repository: the
// hooks cannot work there.
func (p *Project) StateDirErr() error {
	if _, err := fsutil.Resolve(p.root, fsutil.Abs(p.root, fsutil.StateDir)); err != nil {
		return fmt.Errorf("%s leads outside the repository, so the hooks are disabled: %w", fsutil.StateDir, err)
	}
	return nil
}

// Warnings lists problems discovery worked around (a skipped .gitignore, a
// git failure).
func (p *Project) Warnings() []string { return p.tree.Warnings() }

// Cache returns the hash cache, loading it on first use.
func (p *Project) Cache() *hashing.Cache {
	if p.cache == nil {
		p.cache, p.cacheErr = hashing.LoadCache(p.root)
		p.cache.UseStats(p.tree.Stat)
	}
	return p.cache
}

// CacheErr explains why the hash cache could not be used (loading it if
// needed); it is rewritten on the next save.
func (p *Project) CacheErr() error {
	p.Cache()
	return p.cacheErr
}

// Orphans lists context docs (context/<x>.md) whose unit no longer exists:
// its directory was removed or no longer qualifies.
func (p *Project) Orphans() []string {
	var out []string
	for _, rel := range p.tree.Tracked() {
		// The prefix test first: Classify may run the .gitignore rules.
		if !strings.HasPrefix(rel, "context/") || !strings.HasSuffix(rel, ".md") {
			continue
		}
		if k, _ := p.tree.Classify(rel); k == units.KindIgnore {
			out = append(out, rel)
		}
	}
	return out
}

// maxShown caps the repo paths ShowNames returns.
const maxShown = 20

// ShowNames splits repo paths into those that may be shown to Claude
// (units.SafeName, at most maxShown) and the number of the others, whose
// names are not echoed: they come from the repo.
func ShowNames(paths []string) (shown []string, hidden int) {
	for _, s := range paths {
		if len(shown) < maxShown && units.SafeName(s) {
			shown = append(shown, s)
		} else {
			hidden++
		}
	}
	return shown, hidden
}

// Prewarm hashes the changed files among files in parallel (see
// hashing.Cache.Prewarm), saving the cache along the way.
func (p *Project) Prewarm(files []string) {
	p.Cache().Prewarm(p.root, files, func() { _ = p.SaveCache() }, cacheSaveEvery)
}

// cacheSaveEvery is how often Statuses and Prewarm save the hash cache while hashing, so
// a hook killed by its timeout keeps the work done so far.
const cacheSaveEvery = 5 * time.Second

// Statuses computes the state of every unit. The cache is updated, and saved
// along the way when hashing takes long.
func (p *Project) Statuses() []docstate.UnitStatus {
	c := p.Cache()
	var files []string
	for _, u := range p.tree.Units() {
		files = append(files, p.tree.Files(u.Path)...)
	}
	// Hash what changed in parallel first; save along the way (best effort,
	// the caller's final SaveCache reports errors).
	c.Prewarm(p.root, files, func() { _ = p.SaveCache() }, cacheSaveEvery)
	var out []docstate.UnitStatus
	for _, u := range p.tree.Units() {
		out = append(out, docstate.StatusOf(p.root, p.tree, c, u.Path))
	}
	return out
}

// UpToDate reports whether unit's docs match its current sources.
func (p *Project) UpToDate(unit string) bool {
	return docstate.StatusOf(p.root, p.tree, p.Cache(), unit).State == docstate.OK
}

// Ack records the current source hash of unit in its context doc.
func (p *Project) Ack(unit string, now time.Time) error {
	if p.ConfigBroken() {
		return ErrConfigBroken
	}
	return docstate.Ack(p.root, p.tree, p.Cache(), unit, now)
}

// AckTouched acks the units among touched that are units with a usable
// context doc, then refreshes the root index if any was touched (a doc edit
// may change a summary even when no hash changes). It returns the units
// acked, per unit why its ack failed, and the index error.
func (p *Project) AckTouched(touched []string, now time.Time) (acked []string, failed map[string]error, indexErr error) {
	failed = map[string]error{}
	for _, u := range touched {
		if kind, _, _ := docstate.Doc(p.root, u); !p.tree.IsUnit(u) || kind != docstate.DocPresent {
			continue
		}
		if err := p.Ack(u, now); err != nil {
			failed[u] = err
			continue
		}
		acked = append(acked, u)
	}
	if len(touched) > 0 {
		indexErr = p.RefreshIndex()
	}
	return acked, failed, indexErr
}

// UpdateIndex rewrites the root index file units index from sts, which must be
// the statuses of every unit (a partial list drops units from the index), and
// reports whether the file changed. An inactive project gets nothing.
func (p *Project) UpdateIndex(sts []docstate.UnitStatus) (bool, error) {
	if p.ConfigBroken() {
		return false, ErrConfigBroken
	}
	if !Active(p.root) {
		return false, nil
	}
	entries := make([]rootindex.Entry, 0, len(sts))
	for _, s := range sts {
		entries = append(entries, rootindex.Entry{Unit: s.Unit, Summary: s.Summary})
	}
	return rootindex.Update(p.root, p.IndexFile(), entries)
}

// RefreshIndex recomputes the statuses and rewrites the root index.
func (p *Project) RefreshIndex() error {
	_, err := p.UpdateIndex(p.Statuses())
	return err
}

// Exclude adds dir (an existing directory, not the root unit) to the config's
// exclude list.
func (p *Project) Exclude(dir string) error {
	if dir == units.RootUnit {
		return fmt.Errorf("the root unit cannot be excluded; create context/_root.md instead")
	}
	full, err := fsutil.Resolve(p.root, fsutil.Abs(p.root, dir))
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no directory %s in %s", dir, p.root)
		}
		return err
	}
	if err := config.ValidatePattern(dir); err != nil {
		return err
	}
	return config.AddExclude(p.root, dir)
}

// SaveCache drops entries of untracked files and writes the hash cache, only
// in an active project whose state dir is usable.
func (p *Project) SaveCache() error {
	if !Active(p.root) {
		return nil
	}
	if err := p.StateDirErr(); err != nil {
		return err
	}
	if p.cache == nil {
		return nil // never loaded: nothing changed
	}
	p.cache.Prune(p.tree.Tracked())
	return p.cache.Save(p.root)
}
