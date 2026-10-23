// Package turn holds the rules of a turn: from the turn-start snapshot, the
// tree now and the session journal it decides which units are auto-ack
// candidates, which need feedback, whose unit list changed and which were
// removed. It does no I/O of its own: directory stats, hashing, the auto-ack
// and saving the journal come in through Input, so the Stop hook only reads
// its input, calls Evaluate and renders the outcome.
package turn

import (
	"context"
	"maps"
	"math"
	"path"
	"slices"
	"strings"

	"auto-context/internal/config"
	"auto-context/internal/filestat"
	"auto-context/internal/session"
	"auto-context/internal/units"
)

// View is what the rules read of the unit tree now (*units.Tree, or a
// project's narrow view of it).
type View interface {
	Units() []units.Unit
	IsUnit(dir string) bool
	UnitOf(rel string) string
	Files(unit string) []string
	Classifier() *units.Classifier
}

// DirStat reports the mtime (ns) of the repo directory dir (slash path, "."
// is the root); found is false when dir is missing or not a real directory.
type DirStat func(dir string) (mtimeNs int64, found bool, err error)

// Input is one Stop of a turn: the snapshots, the journal and the lookups
// the rules need, plus the effects Evaluate calls on the way (each nil one
// is skipped), in this order: Recorded, Ack, Prewarm.
type Input struct {
	Tree View
	// Config gives module_markers, min_files and the index file name.
	Config config.Config
	// Before is the turn-start snapshot, Cur the tree now.
	Before, Cur session.Snapshot
	// Journal is the session journal as loaded; a paused one resumes.
	Journal session.Journal
	// Seen is the stat of the docs the previous Stops of the turn auto-acked
	// (doc path -> stat, after the ack): a doc still the same is no candidate.
	Seen map[string]filestat.Stat
	// TurnStart is the snapshot's time (ns); 0 turns the old-mtime rule off.
	TurnStart int64
	// Ctx is the hook's budget; nil: none.
	Ctx context.Context
	// DirStat orders deleted files; Gone reports a repo file missing from disk.
	DirStat DirStat
	Gone    func(rel string) bool
	// UpToDate reports whether a unit's docs match its sources (hashing).
	UpToDate func(unit string) bool

	// Recorded gets the journal with this Stop's changes folded in, before
	// any auto-ack; timeUp: the hook's time ran out and only part of the
	// changes was recorded.
	Recorded func(j session.Journal, timeUp bool)
	// Ack auto-acks the candidates and returns the units acked and, per
	// unit, why its ack failed (a fixed category).
	Ack func(candidates []string) (acked []string, failed map[string]string)
	// Prewarm gets the units whose code changed this turn before UpToDate is
	// asked about them.
	Prewarm func(changed []string)
}

// Outcome is what one Stop found.
type Outcome struct {
	// Journal is the recorded journal with this Stop's feedback marked as
	// given; it differs from the one Recorded got only when !Quiet().
	Journal session.Journal
	// AutoAck: the auto-ack candidates passed to Ack.
	AutoAck []string
	// Acked and Failed are what Ack returned.
	Acked  []string
	Failed map[string]string
	// Pending: units whose code changed without a docs update, or whose
	// auto-ack failed, not reported yet.
	Pending []string
	// ListChanged: parents (and _root) of units added or removed this turn.
	ListChanged []string
	// Removed: units removed this turn whose context doc is still there;
	// RemovedGone: those removed with their doc (not reported).
	Removed, RemovedGone []string
	// TimeUp: the hook's time ran out while recording (see Recorded).
	TimeUp bool
}

// Quiet reports that the Stop has nothing to say.
func (o Outcome) Quiet() bool { return len(o.Pending)+len(o.ListChanged)+len(o.Removed) == 0 }

// Evaluate applies the rules of a turn to one Stop.
func Evaluate(in Input) Outcome {
	j := in.Journal
	if j.Paused {
		// Tracking resumes after a broken config: the snapshot is older than
		// this turn, so earlier notifications must not hide its changes.
		j.Resume()
	}
	tc := recordTurn(in.Tree, in.Before, in.Cur, turnEnv{in.TurnStart, in.DirStat, in.Gone, in.Ctx}, in.Seen, &j)
	if in.Recorded != nil {
		in.Recorded(j, tc.timeUp)
	}
	out := Outcome{AutoAck: tc.fresh, TimeUp: tc.timeUp, Failed: map[string]string{}}
	if in.Ack != nil {
		out.Acked, out.Failed = in.Ack(tc.fresh)
	}
	if in.Prewarm != nil {
		in.Prewarm(j.CodeChanged)
	}
	upToDate := in.UpToDate
	if upToDate == nil {
		upToDate = func(string) bool { return false }
	}
	q := pendingQuery{notified: j.AlreadyNotified, failNotified: j.AckFailNotified, acked: out.Acked, failed: out.Failed, isUnit: in.Tree.IsUnit, upToDate: upToDate}
	out.Pending = pendingUnits(j.CodeChanged, q)
	var reasons map[string]units.Reason // built on the first isNew call
	var direct map[string]int
	isNew := func(u string) bool {
		if reasons == nil {
			reasons = map[string]units.Reason{}
			for _, un := range in.Tree.Units() {
				reasons[un.Path] = un.Reason
			}
			direct = directSources(in.Before, in.Config.IndexFile)
		}
		return turnNew(u, reasons[u], in.Before, direct, in.Tree.Files, in.Config)
	}
	// A removed unit whose doc is gone too is not named (its doc was handled),
	// but its parents still are while their docs are stale.
	for _, u := range session.Minus(tc.removed, j.AlreadyNotified) {
		if in.Cur.Has(units.ContextPath(u)) {
			out.Removed = append(out.Removed, u)
		} else {
			out.RemovedGone = append(out.RemovedGone, u)
		}
	}
	out.ListChanged = listChangedParents(in.Tree, session.AddUnique(out.Pending, out.Acked...), out.Removed, out.RemovedGone, isNew, q, out.Pending)
	if !out.Quiet() {
		j.Notify(out.Pending...)
		j.Notify(out.ListChanged...)
		j.Notify(out.Removed...)
		j.Notify(out.RemovedGone...)
		var failed []string // the failures this feedback reports
		for _, u := range out.Pending {
			if out.Failed[u] != "" {
				failed = append(failed, u)
			}
		}
		j.NotifyAckFailed(failed...)
	}
	out.Journal = j
	return out
}

// turnNew reports whether unit u is new this turn: no rule could have made
// it a unit by the turn-start snapshot, whatever rule (reason) makes it one
// now. It was one then if it had a module marker, or at least min_files
// source files directly in it (direct, from directSources: files deleted
// since count), or, for an include unit, any file. A unit with a context doc
// then, and _root, are not new. So a unit that existed at the turn start
// without docs is not new, whatever was added to it or removed: its
// parent's file list did not change (HS-4).
func turnNew(u string, reason units.Reason, before session.Snapshot, direct map[string]int, files func(string) []string, cfg config.Config) bool {
	if u == units.RootUnit {
		return false
	}
	if before.Has(units.ContextPath(u)) {
		return false
	}
	for _, m := range cfg.ModuleMarkers {
		if before.Has(path.Join(u, m)) {
			return false
		}
	}
	if direct[u] >= cfg.MinFiles {
		return false
	}
	if reason == units.ReasonInclude {
		if direct[u] > 0 {
			return false
		}
		if slices.ContainsFunc(files(u), before.Has) {
			return false
		}
	}
	return true
}

// directSources counts, per directory, the source files of the snapshot
// directly in it. A snapshot holds sources and docs only: the context docs
// and the index files are left out.
func directSources(before session.Snapshot, indexFile string) map[string]int {
	n := map[string]int{}
	for _, e := range before {
		if strings.HasPrefix(e.Path, "context/") || path.Base(e.Path) == indexFile {
			continue
		}
		n[path.Dir(e.Path)]++
	}
	return n
}

// listChangedParents returns the parent units and _root of every unit of us
// new this turn and of every removed unit: a new unit takes files from its
// parent and a removed one gives them back, and both change _root's unit
// list, so their docs may be wrong without a code edit or a hash change.
// The parents of a unit removed with its doc (removedGone: the removal was
// handled) are named only while their docs are stale. Units already in
// pending, notified, acked or no units are left out.
func listChangedParents(tree View, us, removed, removedGone []string, isNew func(string) bool, q pendingQuery, pending []string) []string {
	var parents, ifStale []string
	for _, u := range us {
		if isNew(u) {
			parents = append(parents, tree.UnitOf(u), units.RootUnit)
		}
	}
	for _, u := range removed {
		parents = append(parents, tree.UnitOf(u), units.RootUnit)
	}
	for _, u := range removedGone {
		ifStale = append(ifStale, tree.UnitOf(u), units.RootUnit)
	}
	var out []string
	for _, u := range session.Minus(session.AddUnique(parents, ifStale...), q.notified, q.acked, pending) {
		if !q.isUnit(u) || !slices.Contains(parents, u) && q.upToDate(u) {
			continue
		}
		out = append(out, u)
	}
	return out
}

// turnChanges is what recordTurn finds in this Stop's diff.
type turnChanges struct {
	// fresh: units whose context doc changed since the turn start and since
	// the previous Stop of the turn (seen), and is not older than their
	// newest code edit: the candidates for auto-ack.
	fresh []string
	// removed: directories with a context doc at the turn start that are no
	// units now and had a file deleted from disk this turn (not one excluded
	// or untracked: that is the user's choice, or no removal).
	removed []string
	// timeUp: the hook's time ran out before every changed path was looked
	// at; the result is partial and fresh is empty.
	timeUp bool
}

// turnCheckEvery is how many changed paths recordTurn looks at between
// checks of the hook's context.
const turnCheckEvery = 64

// turnEnv is what recordTurn reads besides the snapshots: the turn start
// (ns; 0 turns the old-mtime rule off), the mtime of repo directories,
// whether a repo file is gone from disk, and the hook's context.
type turnEnv struct {
	turnStart int64
	stat      DirStat
	gone      func(rel string) bool
	ctx       context.Context // the hook's budget; nil: none
}

// recordTurn folds the changes between the turn-start snapshot and now into
// the journal and returns the auto-ack candidates and the removed units.
// A doc whose stat equals seen (what the previous Stop of the turn left, after
// its acks) is no candidate: re-acking it would only rebuild the root index.
// A changed code file whose mtime predates the turn start (turnStart, ns; 0
// turns the rule off) was moved in with its old time (mv, cp -p) and cannot
// be ordered against the docs edit: it bars the unit's auto-ack.
// A deleted code file has no mtime: its deletion time is the mtime of its
// nearest surviving directory (stat; never above the root), which removing
// an entry updates. Later changes in that directory only make it newer,
// which errs towards no auto-ack; a directory older than the turn start or a
// failed stat cannot order the deletion and bars the auto-ack.
func recordTurn(tree View, before, cur session.Snapshot, te turnEnv, seen map[string]filestat.Stat, j *session.Journal) turnChanges {
	turnStart := te.turnStart
	// newestCode is the latest mtime (or deletion time) of a changed code file
	// per unit; a docs edit counts for the unit only if it is not older.
	newestCode := map[string]int64{}
	docs := map[string]bool{}
	deletedAt := deletionTimes(te.stat, turnStart)
	removed := map[string]bool{}
	checked := map[string]bool{} // directories looked at for removed units
	// One classifier for the whole diff: one glob budget, one skipDir memo.
	cl := tree.Classifier()
	var tc turnChanges
	for i, p := range session.Diff(before, cur) {
		if i%turnCheckEvery == 0 && te.ctx != nil && te.ctx.Err() != nil {
			// Partial: the paths left are not recorded. The snapshot stays,
			// so the next Stop of the turn sees them.
			tc.timeUp = true
			break
		}
		if !cur.Has(p) && !strings.HasPrefix(p, "context/") {
			removedUnits(tree, before, p, te.gone, checked, removed)
		}
		switch kind, unit := cl.Classify(p); kind {
		case units.KindCode:
			var m int64
			if fs, ok := cur.Get(p); ok {
				m = fs.MtimeNs
				if m < turnStart {
					m = math.MaxInt64
				}
			} else {
				m = deletedAt(path.Dir(p))
			}
			newestCode[unit] = max(newestCode[unit], m)
		case units.KindContextDoc:
			// Only context/<unit>.md counts as a docs update: an index file touch
			// (a typo fix, or our own root index rewrite) must not ack the unit.
			docs[unit] = true
		}
	}
	// One set update each: a mass change must not cost a sort per path.
	j.AddCode(slices.Collect(maps.Keys(newestCode))...)
	j.AddDocs(slices.Collect(maps.Keys(docs))...)
	tc.removed = slices.Sorted(maps.Keys(removed))
	if tc.timeUp {
		// A partial diff may have reached a unit's doc but not its newer
		// code (context/ sorts before most units): no auto-ack from it, and
		// so no doc is marked seen, which would hide the next Stop's feedback.
		return tc
	}
	for _, u := range slices.Sorted(maps.Keys(docs)) {
		ctx := units.ContextPath(u)
		fs, ok := cur.Get(ctx)
		if s, done := seen[ctx]; !ok || done && s == fs {
			continue
		}
		if fs.MtimeNs >= newestCode[u] {
			tc.fresh = append(tc.fresh, u)
		}
	}
	return tc
}

// removedUnits adds to removed each directory above rel, a path missing from
// the tree now, that had a context doc at the turn start and is no unit now,
// if rel is gone from disk. checked caches the directories looked at; one
// file gone is enough for a directory.
func removedUnits(tree View, before session.Snapshot, rel string, gone func(string) bool, checked, removed map[string]bool) {
	for dir := path.Dir(rel); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if checked[dir] {
			continue
		}
		if !before.Has(units.ContextPath(dir)) || tree.IsUnit(dir) {
			checked[dir] = true
			continue
		}
		if gone(rel) {
			checked[dir] = true
			removed[dir] = true
		}
	}
}

// deletionTimes returns the deletion time of an entry of dir: the mtime of
// dir or, if it is gone too, of its nearest surviving ancestor up to the
// root; math.MaxInt64 when that cannot order the deletion (see recordTurn).
// Results are cached per directory: a mass deletion stats each one once.
func deletionTimes(stat DirStat, turnStart int64) func(dir string) int64 {
	cache := map[string]int64{}
	var at func(dir string) int64
	at = func(dir string) int64 {
		if m, ok := cache[dir]; ok {
			return m
		}
		m, found, err := stat(dir)
		switch {
		case err != nil || found && m < turnStart:
			m = math.MaxInt64
		case !found && dir == ".":
			m = math.MaxInt64 // the root itself is gone or not a directory
		case !found:
			m = at(path.Dir(dir))
		}
		cache[dir] = m
		return m
	}
	return at
}

// pendingQuery is what pendingUnits needs to know besides the changed units.
type pendingQuery struct {
	notified, failNotified, acked []string
	failed                        map[string]string // unit -> why its auto-ack failed
	isUnit, upToDate              func(string) bool
}

// pendingUnits returns the units that need feedback: units whose code
// changed this turn, neither acked nor notified yet, still units, and whose
// docs do not already match the code — plus units whose auto-ack failed and
// was not reported yet (even if notified before: the failure is new). A unit
// counts as updated when it was acked or its docs already match.
func pendingUnits(changed []string, q pendingQuery) []string {
	var pending []string
	for _, u := range session.Minus(changed, q.acked, q.notified) {
		// A unit that stopped being one (excluded, too few files) cannot be
		// acked; docs that already match the code (an edit undone, a checkout of
		// acked code, a formatter that rewrote nothing) need no feedback.
		if !q.isUnit(u) || (q.failed[u] == "" && q.upToDate(u)) {
			continue
		}
		pending = append(pending, u)
	}
	for _, u := range session.Minus(slices.Collect(maps.Keys(q.failed)), q.failNotified, pending) {
		if q.isUnit(u) {
			pending = append(pending, u)
		}
	}
	return session.AddUnique(nil, pending...)
}
