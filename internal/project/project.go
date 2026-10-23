// Package project opens a repository for the hooks and the CLI: config, unit
// tree and hash cache, and the operations both need (activation, status, ack,
// root index, exclude, saving the cache).
package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/filestat"
	"auto-context/internal/fsutil"
	"auto-context/internal/gitx"
	"auto-context/internal/hashing"
	"auto-context/internal/rootindex"
	"auto-context/internal/units"
)

// markerPaths are the files whose presence turns the plugin on.
func markerPaths() []string {
	return []string{config.FileName, units.ContextPath(units.RootUnit)}
}

// Active reports whether the plugin is enabled in root: it has
// .auto-context/config.yaml or context/_root.md (created by /auto-context:init), as a
// regular file inside the repo. A bare context/ directory is too common a name
// to count. Inactive projects are never written to.
func Active(root string) bool {
	active, _ := markerState(root)
	return active
}

// ActivationRefused reports a marker (.auto-context/config.yaml or context/_root.md)
// that exists but is not used: it links outside the repo or into .git, is not
// a regular file, or cannot be inspected, so the plugin stays off.
func ActivationRefused(root string) bool {
	_, refused := markerState(root)
	return refused
}

// markerState inspects the markers once: active when one is a regular file
// inside the repo, refused when one exists but is not usable.
func markerState(root string) (active, refused bool) {
	for _, m := range markerPaths() {
		full := fsutil.Abs(root, m)
		if _, err := os.Lstat(full); err != nil {
			continue
		}
		p, err := fsutil.Resolve(root, full)
		if err != nil {
			refused = true
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			active = true
		} else {
			refused = true
		}
	}
	return active, refused
}

// Project is an opened repository, read-only after Open (but for the lazily
// loaded cache, and Exclude, which reloads config and tree in place): the
// tree, cache and config belong together. The tree itself is immutable:
// classifying paths (Orphans, Stop) changes neither its warnings nor a
// shared glob budget.
type Project struct {
	root string
	// ctx bounds the project's git calls and hashing (a hook's budget, an
	// interrupted CLI): Open's context, kept because a Project lives for one
	// hook or command.
	ctx  context.Context
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
	// head is HEAD's commit for acks, looked up once on the first ack that
	// writes (headDone); "" without git or commits.
	head     string
	headDone bool
}

// Root is the repository root the project was opened at.
func (p *Project) Root() string { return p.root }

// Config is the config in effect: the defaults when it is broken.
func (p *Project) Config() config.Config { return p.cfg }

// Tree is the unit tree Discover built. Only cmd/auto-context's changes.go
// still uses it (outside T28's files); the narrow queries below replace it.
func (p *Project) Tree() *units.Tree { return p.tree }

// Units returns all units: _root first, the rest sorted by path.
func (p *Project) Units() []units.Unit { return p.tree.Units() }

// IsUnit reports whether dir is a unit.
func (p *Project) IsUnit(dir string) bool { return p.tree.IsUnit(dir) }

// UnitOf returns the nearest ancestor unit of rel's directory, else _root.
func (p *Project) UnitOf(rel string) string { return p.tree.UnitOf(rel) }

// Classify says what the repo path rel is to the plugin and its unit
// (units.Tree.Classify).
func (p *Project) Classify(rel string) (units.Kind, string) { return p.tree.Classify(rel) }

// Classifier returns a classifier for many paths (one glob budget and memo).
func (p *Project) Classifier() *units.Classifier { return p.tree.Classifier() }

// Files lists the files of unit (a copy).
func (p *Project) Files(unit string) []string { return p.tree.Files(unit) }

// Stats returns the tracked files and their stats as discovery took them,
// the input of session.FromTree. The map is shared: read only.
func (p *Project) Stats() (tracked []string, stats map[string]filestat.Stat) {
	return p.tree.Tracked(), p.tree.StatsView()
}

// WarningCount is the number of discovery warnings, including those
// dropped past the cap of Warnings.
func (p *Project) WarningCount() int { return p.tree.WarningCount() }

// ConfigProblems lists settings of the config that were dropped (nil if none).
func (p *Project) ConfigProblems() config.Problems { return p.cfgProblems }

// ConfigErr is why the config could not be read or parsed (nil if it was).
func (p *Project) ConfigErr() error { return p.cfgErr }

// Open loads the config and discovers units; the hash cache loads on first
// use. Only a failed discovery is an error; config problems are kept. ctx
// bounds every git call and hashing of the project: once it ends, git is not
// run and hashing stops (statuses then say docstate.TimeUp on a deadline).
func Open(ctx context.Context, root string) (*Project, error) {
	cfg, problems, cfgErr := config.Load(root)
	tree, err := units.DiscoverContext(ctx, root, cfg)
	if err != nil {
		return nil, err
	}
	return &Project{root: root, ctx: ctx, cfg: cfg, tree: tree, cfgProblems: problems, cfgErr: cfgErr}, nil
}

// ErrConfigBroken refuses an operation that would act on the default units
// while the config cannot be read or parsed.
var ErrConfigBroken = errors.New(config.FileName + " cannot be read or parsed; fix it first")

// ErrConfigDegraded refuses an ack while a setting that decides the units
// (include, exclude, track, min_files, code_extensions, module_markers) was
// dropped or reset: the hash would be over units the user did not mean.
var ErrConfigDegraded = errors.New(config.FileName + " has invalid settings that decide the units; fix it first")

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
	_, err := gitx.Run(p.ctx, p.root, 0, "check-ignore", "-q", "--", config.FileName)
	switch {
	case err == nil:
		return true, nil
	case gitx.Code(err) == 1, gitx.NotRun(err), gitx.NotRepo(err):
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
		// Only context/*.md: Classify answers those from the unit list,
		// without the .gitignore rules.
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
	if len(files) == 0 {
		return // nothing to hash: do not load the cache
	}
	p.Cache().Prewarm(p.ctx, p.root, files, func() { _ = p.SaveCache() }, cacheSaveEvery)
}

// cacheSaveEvery is how often Statuses and Prewarm save the hash cache while hashing, so
// a hook killed by its timeout keeps the work done so far.
const cacheSaveEvery = 5 * time.Second

// Statuses computes the state of every unit. The cache is updated, and saved
// along the way when hashing takes long.
func (p *Project) Statuses() []docstate.UnitStatus {
	// Hash what changed in parallel first (the caller's final SaveCache
	// reports save errors), only for the units whose docs record a hash: the
	// others are missing or not acked and need none.
	// Each doc is read once: the statuses reuse what was read here.
	us := p.tree.Units()
	docs := make([]docstate.DocText, len(us))
	var files []string
	for i, u := range us {
		d := docstate.ReadDoc(p.root, u.Path)
		docs[i] = d
		switch {
		case d.Err != nil || d.Meta.SourceHash == "":
		case strings.HasPrefix(d.Meta.SourceHash, docstate.HashPrefix):
			files = append(files, p.tree.HashFiles(u.Path)...)
		default:
			files = append(files, p.tree.Files(u.Path)...) // the old scheme
		}
	}
	p.Prewarm(files)
	c := p.Cache()
	out := make([]docstate.UnitStatus, 0, len(us))
	for i, u := range us {
		out = append(out, docstate.StatusOfDoc(p.ctx, p.root, p.tree, c, u.Path, docs[i]))
	}
	return out
}

// UpToDate reports whether unit's docs match its current sources.
func (p *Project) UpToDate(unit string) bool {
	return docstate.StatusOf(p.ctx, p.root, p.tree, p.Cache(), unit).State == docstate.OK
}

// Ack records the current source hash of unit in its context doc.
func (p *Project) Ack(unit string, now time.Time) error {
	_, err := p.ackIfNeeded(unit, now)
	return err
}

// ackIfNeeded acks unit unless the config is broken or degraded, and tells
// whether the doc was rewritten.
func (p *Project) ackIfNeeded(unit string, now time.Time) (bool, error) {
	switch {
	case p.ConfigBroken():
		return false, ErrConfigBroken
	case p.cfg.DiscoveryReset:
		return false, ErrConfigDegraded
	}
	return docstate.AckIfNeeded(p.ctx, p.root, p.tree, p.Cache(), unit, now, p.headCommit)
}

// Upgrade acks the units whose docs are up to date by the old hash scheme
// (docstate.NoteOldScheme), rewriting their hash in scheme v2. It returns the
// units acked and per unit why its ack failed.
func (p *Project) Upgrade(now time.Time) (acked []string, failed map[string]error) {
	failed = map[string]error{}
	for _, st := range p.Statuses() {
		if !docstate.OldScheme(st) {
			continue
		}
		if _, err := p.ackIfNeeded(st.Unit, now); err != nil {
			failed[st.Unit] = err
			continue
		}
		acked = append(acked, st.Unit)
	}
	return acked, failed
}

// headCommit returns HEAD's full commit id, or "" when git fails or there are
// no commits: an ack never fails because of git. One git call per Project,
// within the project's context (a hook's budget).
func (p *Project) headCommit() string {
	if !p.headDone {
		p.headDone = true
		if res, err := gitx.Run(p.ctx, p.root, 0, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
			if id := strings.TrimSpace(string(res.Out)); isCommitID(id) {
				p.head = id
			}
		}
	}
	return p.head
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
		if _, err := p.ackIfNeeded(u, now); err != nil {
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
// reports whether the file changed. An inactive project gets nothing; a
// broken or degraded config is refused (ErrConfigBroken, ErrConfigDegraded).
func (p *Project) UpdateIndex(sts []docstate.UnitStatus) (bool, error) {
	entries := make([]rootindex.Entry, 0, len(sts))
	for _, s := range sts {
		entries = append(entries, rootindex.Entry{Unit: s.Unit, Summary: s.Summary})
	}
	return p.writeIndex(entries)
}

// writeIndex writes entries into the root index file, unless the config is
// broken or degraded (the committed index would list units the user did not
// mean) or the project inactive.
func (p *Project) writeIndex(entries []rootindex.Entry) (bool, error) {
	switch {
	case p.ConfigBroken():
		return false, ErrConfigBroken
	case p.cfg.DiscoveryReset:
		return false, ErrConfigDegraded
	}
	if !Active(p.root) {
		return false, nil
	}
	return rootindex.Update(p.root, p.IndexFile(), entries)
}

// RefreshIndex rewrites the root index from the summaries of the docs
// (docstate.SummaryOf), without hashing any sources.
func (p *Project) RefreshIndex() error {
	us := p.tree.Units()
	entries := make([]rootindex.Entry, 0, len(us))
	for _, u := range us {
		entries = append(entries, rootindex.Entry{Unit: u.Path, Summary: docstate.SummaryOf(p.root, u.Path)})
	}
	_, err := p.writeIndex(entries)
	return err
}

// Exclude adds dir (an existing directory, not the root unit, named exactly
// as on disk) to the config's exclude list as a literal pattern, then reloads
// the config and the unit tree in place.
func (p *Project) Exclude(dir string) error {
	if dir == units.RootUnit {
		return fmt.Errorf("the root unit cannot be excluded; create context/_root.md instead")
	}
	full, err := fsutil.Resolve(p.root, fsutil.Abs(p.root, dir))
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	if err := p.exactName(dir); err != nil {
		return err
	}
	if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no directory %s in %s", dir, p.root)
		}
		return err
	}
	pattern := config.EscapePattern(dir)
	if err := config.ValidatePattern(pattern); err != nil {
		return err
	}
	if err := config.AddExclude(p.root, pattern); err != nil {
		return err
	}
	return p.reload()
}

// exactName checks that every segment of dir is spelled as its parent
// directory lists it: on a case-insensitive file system a wrong case would
// still stat, but the pattern would never match.
func (p *Project) exactName(dir string) error {
	cur := p.root
	for seg := range strings.SplitSeq(dir, "/") {
		entries, err := os.ReadDir(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				return fmt.Errorf("no directory %s in %s", dir, p.root)
			}
			return err
		}
		exact, folded := false, false
		for _, e := range entries {
			exact = exact || e.Name() == seg
			folded = folded || strings.EqualFold(e.Name(), seg)
		}
		switch {
		case exact:
		case folded:
			return fmt.Errorf("no directory %s (names are case-sensitive)", dir)
		default:
			return fmt.Errorf("no directory %s in %s", dir, p.root)
		}
		cur = filepath.Join(cur, seg)
	}
	return nil
}

// reload rereads the config and the unit tree after Exclude changed the
// config; a loaded cache is kept and reads the new tree's stats.
func (p *Project) reload() error {
	q, err := Open(p.ctx, p.root)
	if err != nil {
		return err
	}
	p.cfg, p.tree, p.cfgProblems, p.cfgErr = q.cfg, q.tree, q.cfgProblems, q.cfgErr
	if p.cache != nil {
		p.cache.UseStats(p.tree.Stat)
	}
	return nil
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
	// A project enabled by context/_root.md alone, or one from v1.0.0, may
	// lack the state dir or its .gitignore: create them, so the cache never
	// shows as untracked.
	if err := fsutil.EnsureStateDir(p.root); err != nil {
		return err
	}
	if !p.cache.Dirty() {
		if _, err := os.Lstat(hashing.CachePath(p.root)); err == nil {
			return nil // unchanged: skip the O(files) prune
		}
	}
	p.cache.Prune(p.tree.Tracked())
	return p.cache.Save(p.root)
}
