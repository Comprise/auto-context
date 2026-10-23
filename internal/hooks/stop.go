package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/filestat"
	"auto-context/internal/fsutil"
	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/session"
	rules "auto-context/internal/turn" // "turn" is a test helper of this package
	"auto-context/internal/units"
)

// Stop compares the tree with the turn-start snapshot and returns feedback when
// code changed without matching docs updates.
func Stop(env Env, in Input) ([]byte, error) {
	// stop_hook_active is deliberately ignored: Claude Code sets it on every Stop
	// after feedback, and that Stop must still record docs edits and auto-ack.
	// already_notified keeps it to one feedback per unit per turn.
	if !project.Active(env.Root) {
		return nil, nil
	}
	root, id := env.Root, in.SessionID
	if !validSession(root, "stop", id) {
		return nil, nil
	}
	// No state dir yet means the project was activated during this turn (e.g. by
	// /auto-context:init), so PromptSubmit had nothing to snapshot.
	_, serr := os.Lstat(fsutil.StatePath(root, "state"))
	wasActive := serr == nil
	p, err := load(env)
	if err != nil {
		return nil, err
	}
	if p.ConfigBroken() {
		return configPaused(env, id)
	}
	cur := session.FromTree(p.Stats())
	before, err := session.LoadSnapshot(root, id)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return stopWithoutSnapshot(root, id, wasActive, cur)
	case err != nil:
		// A corrupt snapshot is replaced, or every Stop of the turn would fail.
		logx.Errorf(root, "stop: turn-start snapshot unreadable, this turn's changes are not tracked: %v", err)
		return nil, cur.Save(root, id)
	}

	seen := loadSeenDocs(root, id)
	j, _ := loadJournal(root, id, "stop")
	// The snapshot was just read: a failed stat is a race, and 0 only turns
	// the old-mtime rule off.
	turnStart, err := session.SnapshotTime(root, id)
	if err != nil {
		turnStart = 0
	}
	out := rules.Evaluate(rules.Input{
		Tree: p, Config: p.Config(), Before: before, Cur: cur, Journal: j, Seen: seen,
		TurnStart: turnStart, Ctx: env.ctx(), DirStat: repoDirStat(root), Gone: repoGone(root),
		UpToDate: p.UpToDate,
		Recorded: func(j session.Journal, timeUp bool) {
			if timeUp {
				logx.Errorf(root, "stop: the hook's time is up; only part of this turn's changes was recorded, the next Stop checks the rest")
			}
			if err := j.Save(root, id); err != nil {
				// Feedback is still worth giving; it may repeat on the next Stop.
				logx.Errorf(root, "stop: saving the journal: %v", err)
			}
		},
		Ack: func(candidates []string) ([]string, map[string]string) {
			acked, failed := autoAck(env, p, candidates)
			saveSeenDocs(root, id, seen, candidates)
			return acked, failed
		},
		// Hash the changed units in parallel first; UpToDate then hits the cache.
		Prewarm: func(changed []string) {
			var files []string
			for _, u := range changed {
				files = append(files, p.Files(u)...)
			}
			p.Prewarm(files)
		},
	})
	if err := p.SaveCache(); err != nil {
		logx.Errorf(root, "hash cache: %v", err)
	}
	if out.Quiet() {
		// Keep the turn-start snapshot and the journal: another Stop hook may make
		// Claude continue this turn. PromptSubmit resets both.
		return nil, nil
	}
	docKind := func(u string) docstate.DocKind {
		kind, _, _ := docstate.Doc(root, u)
		return kind
	}
	f := buildFeedback(out.Pending, out.Failed, docKind, p.Config().Language)
	f.ListChanged, f.Removed = out.ListChanged, out.Removed
	// out.Journal marks this feedback as given.
	if err := out.Journal.Save(root, id); err != nil {
		// Without the notified mark the same feedback would come back on every
		// Stop (it continues the turn): say nothing rather than loop.
		logx.Errorf(root, "stop: saving the journal, feedback dropped: %v", err)
		return nil, nil
	}
	f.Agent, f.IndexFile, f.CLI = env.Agent, p.IndexFile(), env.CLI()
	return stopFeedback(StopReason(f), env.Agent)
}

// validSession reports whether id can name the session's state files; a
// blank one is logged and the hook does nothing: sessions without an id
// would share (and reset) one snapshot and journal.
func validSession(root, hook, id string) bool {
	if session.ValidID(id) {
		return true
	}
	logx.Errorf(root, "%s: no session_id in the payload, hook skipped", hook)
	return false
}

// loadJournal returns the session journal; an unreadable one is logged, and
// an empty journal is returned with the error.
func loadJournal(root, id, hook string) (session.Journal, error) {
	j, err := session.LoadJournal(root, id)
	if err != nil {
		logx.Errorf(root, "%s: journal unreadable, starting empty: %v", hook, err)
	}
	return j, err
}

// stopWithoutSnapshot handles a Stop with no turn-start snapshot: the project
// was activated or the config fixed during this turn, or PromptSubmit failed.
// It saves the current snapshot so tracking starts from now.
func stopWithoutSnapshot(root, id string, wasActive bool, cur session.Snapshot) ([]byte, error) {
	j, _ := loadJournal(root, id, "stop")
	switch {
	case j.Paused:
		// Broken since the session started, fixed during this turn: there
		// was nothing to snapshot. Tracking resumes from now.
		j.Resume()
		if err := j.Save(root, id); err != nil {
			logx.Errorf(root, "stop: saving the journal: %v", err)
		}
	case wasActive:
		logx.Errorf(root, "stop: no turn-start snapshot (prompt-submit did not run or failed); this turn's changes are not tracked")
	}
	return nil, cur.Save(root, id)
}

// maxFeedbackUnits caps the units each Stop message names: a mass change must
// not flood Claude's context.
const maxFeedbackUnits = 20

// capList returns at most maxFeedbackUnits of list and " (+N more)" for the rest.
func capList(list []string) ([]string, string) {
	if len(list) <= maxFeedbackUnits {
		return list, ""
	}
	return list[:maxFeedbackUnits], fmt.Sprintf(" (+%d more)", len(list)-maxFeedbackUnits)
}

// cmdArg makes a unit name an argument of a suggested shell command. Unit
// names pass units.SafeName (no spaces, quotes or leading "-"); should that
// ever be relaxed, a leading "-" gets "./" so it cannot read as an option,
// and a space gets the name quoted.
func cmdArg(u string) string {
	if strings.HasPrefix(u, "-") {
		u = "./" + u
	}
	if strings.Contains(u, " ") {
		return "'" + u + "'"
	}
	return u
}

func cmdArgs(us []string) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = cmdArg(u)
	}
	return out
}

// stopFeedback is Stop's answer. For Claude, additionalContext continues the
// conversation like decision:block but is shown as hook feedback rather than a
// hook error. Codex only continues the turn on decision:block.
func stopFeedback(text string, a Agent) ([]byte, error) {
	if a == Codex {
		return encodeJSON(hookOutput{Decision: "block", Reason: text})
	}
	return encodeJSON(hookOutput{Hook: &hookSpecific{EventName: "Stop", AdditionalContext: text}})
}

// configPaused tells Claude once that tracking is paused while the config
// cannot be parsed (the units would be the defaults). The journal remembers
// the notice; without a saved journal nothing is said, since the notice
// continues the turn and would repeat on every Stop.
func configPaused(env Env, id string) ([]byte, error) {
	root := env.Root
	j, _ := loadJournal(root, id, "stop")
	first := j.MarkConfigNotified()
	if err := j.Save(root, id); err != nil {
		logx.Errorf(root, "stop: saving the journal: %v", err)
		return nil, nil
	}
	if !first {
		return nil, nil
	}
	return stopFeedback("auto-context: "+config.FileName+" cannot be read or parsed, so docs tracking is paused until it is fixed; run `"+env.CLI()+" status` for the error. Changes made meanwhile are checked once it is fixed.", env.Agent)
}

// repoDirStat is the rules.DirStat of the repository at root. Lstat: a
// symlink is not a directory of the repo, except the root itself (a repo
// opened through a link).
func repoDirStat(root string) rules.DirStat {
	return func(dir string) (int64, bool, error) {
		stat := os.Lstat
		if dir == "." {
			stat = os.Stat
		}
		fi, err := stat(filepath.Join(root, filepath.FromSlash(dir)))
		switch {
		case errors.Is(err, os.ErrNotExist):
			return 0, false, nil
		case err != nil:
			return 0, false, err
		}
		return fi.ModTime().UnixNano(), fi.IsDir(), nil
	}
}

// repoGone reports whether the repo file rel is missing from disk (Lstat).
func repoGone(root string) func(rel string) bool {
	return func(rel string) bool {
		_, err := os.Lstat(fsutil.Abs(root, rel))
		return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
	}
}

// buildFeedback groups the pending units by what Claude should be told.
func buildFeedback(pending []string, failed map[string]string, doc func(string) docstate.DocKind, lang string) Feedback {
	f := Feedback{Failed: map[string]string{}, Language: lang}
	for _, u := range pending {
		if failed[u] != "" {
			f.Failed[u] = failed[u]
			continue
		}
		switch doc(u) {
		case docstate.DocAbsent:
			f.New = append(f.New, u)
		case docstate.DocRefused:
			f.Refused = append(f.Refused, u)
		case docstate.DocUnreadable:
			f.Unreadable = append(f.Unreadable, u)
		default:
			f.Changed = append(f.Changed, u)
		}
	}
	return f
}

// ackTouched is Project.AckTouched; tests count its calls.
var ackTouched = (*project.Project).AckTouched

// autoAck acks the documented touched units (Project.AckTouched), logging
// failures, and returns the units acked and, per unit, why an ack failed.
// Nothing touched, nothing done: the root index is not rebuilt either.
func autoAck(env Env, p *project.Project, touched []string) (acked []string, failed map[string]string) {
	failed = map[string]string{}
	if len(touched) == 0 {
		return nil, failed
	}
	acked, errs, indexErr := ackTouched(p, touched, env.Now())
	// A degraded config refuses the index: SessionStart reports it.
	if indexErr != nil && !errors.Is(indexErr, project.ErrConfigDegraded) {
		logx.Errorf(env.Root, "root index: %v", indexErr)
	}
	for u, err := range errs {
		logx.Errorf(env.Root, "auto-ack %s: %v", u, err)
		failed[u] = errCategory(err)
	}
	return acked, failed
}

// errCategory names why an operation failed with a fixed category: the error
// text holds repo paths, which must not reach Claude's context. The full
// error is in the log.
func errCategory(err error) string {
	switch {
	case errors.Is(err, fsutil.ErrOutside):
		return "a file links outside the repository"
	case errors.Is(err, fsutil.ErrNotRegular):
		return "a source or doc is not a regular file"
	case errors.Is(err, fsutil.ErrTooLarge):
		return "the context doc is too large"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "a file disappeared"
	case errors.Is(err, project.ErrConfigBroken):
		return config.FileName + " cannot be read or parsed; fix it first"
	case errors.Is(err, project.ErrConfigDegraded):
		return config.FileName + " has invalid settings that decide the units; fix it first"
	case errors.Is(err, units.ErrTimeUp):
		return "the hook's time ran out; the repository may be too large or slow"
	case errors.Is(err, units.ErrGitTimeout), errors.Is(err, units.ErrListTooLarge):
		return "git is too slow or lists too many files; add big generated directories to .gitignore"
	}
	return "see .auto-context/log"
}

// Feedback groups the units Stop reports.
type Feedback struct {
	Changed    []string          // code changed, docs not updated
	New        []string          // no context doc yet
	Refused    []string          // context doc links outside the repo, into .git, to a non-.md file, or nowhere
	Unreadable []string          // context doc exists but cannot be inspected
	Failed     map[string]string // unit -> why its auto-ack failed
	// ListChanged: parents (and _root) of units added or removed this turn.
	ListChanged []string
	// Removed: units removed this turn whose context doc is still there.
	Removed []string
	// Language is the configured docs language code, or "".
	Language string
	// Agent selects how skills are named; the zero value is Claude.
	Agent Agent
	// IndexFile is the per-unit index file name; "" means CLAUDE.md.
	IndexFile string
	// CLI is how suggested commands name the binary (Env.CLI); "" means the
	// agent's default from the environment.
	CLI string
}

// contextPaths returns the context doc path of each unit.
func contextPaths(us []string) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = units.ContextPath(u)
	}
	return out
}

// StopReason builds the feedback text.
func StopReason(f Feedback) string {
	cli := f.CLI
	if cli == "" {
		cli = Env{Agent: f.Agent}.CLI()
	}
	idx := f.IndexFile
	if idx == "" {
		idx = config.IndexClaude
	}
	var parts []string
	if len(f.Changed) > 0 {
		shown, more := capList(f.Changed)
		parts = append(parts, "code changed in "+strings.Join(shown, ", ")+more+
			" but docs not updated. If the change affects purpose, public API, structure or key behavior"+
			" — update context/<unit>.md (and <unit>/"+idx+" if its purpose or key files changed) per skill "+f.Agent.Skill("auto-context-docs")+"."+
			" Otherwise run `"+cli+" ack "+strings.Join(cmdArgs(shown), " ")+"`.")
	}
	failed, more := capList(slices.Sorted(maps.Keys(f.Failed)))
	for _, u := range failed {
		parts = append(parts, "auto-ack of "+u+" failed: "+f.Failed[u]+". The docs edit was seen, but the hash could not be recorded; fix the cause, then run `"+cli+" ack "+cmdArg(u)+"`.")
	}
	if more != "" {
		parts = append(parts, "More failed auto-acks"+more+"; run `"+cli+" status`.")
	}
	if refused, more := capList(f.Refused); len(refused) > 0 {
		parts = append(parts, strings.Join(contextPaths(refused), ", ")+more+" cannot be used: each "+docstate.OutsideText+". Tell the user; replace it with a regular Markdown file if they agree.")
	}
	if unreadable, more := capList(f.Unreadable); len(unreadable) > 0 {
		parts = append(parts, strings.Join(contextPaths(unreadable), ", ")+more+" cannot be read (e.g. permissions); fix it, then run `"+cli+" status`.")
	}
	newUnits, moreNew := capList(f.New)
	for _, u := range newUnits {
		if u == units.RootUnit {
			// The root unit cannot be excluded.
			parts = append(parts, "New unit _root: create context/_root.md (and the root "+idx+" purpose line).")
			continue
		}
		parts = append(parts, "New unit "+u+": ask the user, then create its docs or run `"+cli+" ack --exclude "+cmdArg(u)+"`.")
	}
	if moreNew != "" {
		parts = append(parts, "More new units"+moreNew+"; run `"+cli+" status`.")
	}
	listChanged, moreList := capList(f.ListChanged)
	for _, u := range listChanged {
		parts = append(parts, "Unit list changed under "+u+": check "+units.ContextPath(u)+".")
	}
	if moreList != "" {
		parts = append(parts, "More units whose unit list changed"+moreList+"; run `"+cli+" status`.")
	}
	var removed []string
	for _, u := range f.Removed {
		if units.SafeName(u) {
			removed = append(removed, u)
		}
	}
	if shown, more := capList(removed); len(shown) > 0 {
		parts = append(parts, "docs of units removed this turn: "+strings.Join(contextPaths(shown), ", ")+more+"; ask the user, then handle them as the \"Unit removed\" rule of skill "+f.Agent.Skill("auto-context-docs")+" says.")
	}
	if f.Language != "" && len(f.Changed)+len(f.New) > 0 {
		parts = append(parts, LanguageNote(f.Language, idx))
	}
	return "auto-context: " + strings.Join(parts, " ")
}

// LanguageNote tells the agent the configured docs language (a validated code).
func LanguageNote(lang, indexFile string) string {
	return "Write unit docs (" + indexFile + ", context/) in language " + lang + " (`language` in " + config.FileName + ")."
}

// seenDocsPath is the file in which Stop keeps the stat of the context docs it
// auto-acked this turn, after the ack: the next Stop of the turn skips a doc
// still the same (P-M2). PromptSubmit and SessionEnd remove it.
func seenDocsPath(root, id string) string {
	return fsutil.StatePath(root, "state", session.SanitizeID(id)+".docs")
}

// maxSeenDocs caps the size of the seen-docs file read back.
const maxSeenDocs = 16 << 20

// loadSeenDocs returns the docs the previous Stops of this turn left (doc path
// -> stat). A missing file is empty; an unreadable one is logged and treated
// as empty, which only re-acks.
func loadSeenDocs(root, id string) map[string]filestat.Stat {
	seen := map[string]filestat.Stat{}
	data, err := fsutil.ReadFile(root, seenDocsPath(root, id), maxSeenDocs)
	if errors.Is(err, os.ErrNotExist) {
		return seen
	}
	var raw map[string][2]int64
	if err == nil {
		err = json.Unmarshal(data, &raw)
	}
	if err != nil {
		logx.Errorf(root, "stop: seen docs unreadable, starting empty: %v", err)
		return seen
	}
	for p, st := range raw {
		seen[p] = filestat.Stat{MtimeNs: st[0], Size: st[1]}
	}
	return seen
}

// saveSeenDocs adds to seen the current stat (as Discover takes it: a linked
// doc's target) of the context docs of us, the units this Stop tried to
// auto-ack, and saves it. A failed ack is not retried until the doc changes:
// it was reported, with the command to run.
func saveSeenDocs(root, id string, seen map[string]filestat.Stat, us []string) {
	if len(us) == 0 {
		return
	}
	for _, u := range us {
		ctx := units.ContextPath(u)
		if st, ok := docStat(root, ctx); ok {
			seen[ctx] = st
		} else {
			delete(seen, ctx)
		}
	}
	raw := make(map[string][2]int64, len(seen))
	for p, st := range seen {
		raw[p] = [2]int64{st.MtimeNs, st.Size}
	}
	data, err := json.Marshal(raw)
	if err == nil {
		err = fsutil.WriteState(root, seenDocsPath(root, id), data)
	}
	if err != nil {
		logx.Errorf(root, "stop: saving the seen docs: %v", err)
	}
}

// docStat stats the context doc ctx as units.DiscoverContext does: a symlink by its
// target inside the repo. ok is false for anything but a regular file.
func docStat(root, ctx string) (filestat.Stat, bool) {
	full := fsutil.Abs(root, ctx)
	fi, err := os.Lstat(full)
	if err != nil {
		return filestat.Stat{}, false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		p, err := fsutil.ResolveDoc(root, full)
		if err != nil {
			return filestat.Stat{}, false
		}
		if fi, err = os.Stat(p); err != nil {
			return filestat.Stat{}, false
		}
	}
	if !fi.Mode().IsRegular() {
		return filestat.Stat{}, false
	}
	return filestat.Of(fi), true
}

// removeSeenDocs deletes the session's seen-docs file; a missing one is fine.
func removeSeenDocs(root, id string) error {
	p, err := fsutil.Resolve(root, seenDocsPath(root, id))
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
