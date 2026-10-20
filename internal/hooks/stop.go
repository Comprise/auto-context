package hooks

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/fsutil"
	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/session"
	"auto-context/internal/units"
)

type stopOutput struct {
	Hook struct {
		EventName         string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// Stop compares the tree with the turn-start snapshot and returns feedback when
// code changed without matching docs updates.
func Stop(env Env, in Input) ([]byte, error) {
	// stop_hook_active is deliberately ignored: Claude Code sets it on every Stop
	// after feedback, and that Stop must still record docs edits and auto-ack.
	// already_notified keeps it to one feedback per unit per turn.
	if !project.Active(env.Root) {
		return nil, nil
	}
	// No state dir yet means the project was activated during this turn (e.g. by
	// /auto-context:init), so PromptSubmit had nothing to snapshot.
	_, serr := os.Lstat(fsutil.StatePath(env.Root, "state"))
	wasActive := serr == nil
	p, err := load(env)
	if err != nil {
		return nil, err
	}
	root, id := env.Root, in.SessionID
	if p.ConfigBroken() {
		return configPaused(root, id, env.Agent)
	}
	tree := p.Tree()
	cur := session.FromTree(tree.Stats())
	before, err := session.LoadSnapshot(root, id)
	switch {
	case errors.Is(err, os.ErrNotExist):
		j, jerr := session.LoadJournal(root, id)
		if jerr != nil {
			logx.Errorf(root, "stop: journal unreadable, starting empty: %v", jerr)
		}
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
	case err != nil:
		// A corrupt snapshot is replaced, or every Stop of the turn would fail.
		logx.Errorf(root, "stop: turn-start snapshot unreadable, this turn's changes are not tracked: %v", err)
		return nil, cur.Save(root, id)
	}

	j, err := session.LoadJournal(root, id)
	if err != nil {
		logx.Errorf(root, "stop: journal unreadable, starting empty: %v", err)
	}
	if j.Paused {
		// Tracking resumes after a broken config: the snapshot is older than
		// this turn, so earlier notifications must not hide its changes.
		j.Resume()
	}
	fresh := recordTurn(tree, before, cur, &j)
	if err := j.Save(root, id); err != nil {
		// Feedback is still worth giving; it may repeat on the next Stop.
		logx.Errorf(root, "stop: saving the journal: %v", err)
	}
	acked, failed := autoAck(env, p, fresh)
	// Hash the changed units in parallel first; UpToDate then hits the cache.
	var files []string
	for _, u := range j.CodeChanged {
		files = append(files, tree.Files(u)...)
	}
	p.Prewarm(files)
	pending := pendingUnits(j.CodeChanged, j.AlreadyNotified, j.AckFailNotified, acked, failed, tree.IsUnit, p.UpToDate)
	// A new unit takes files from its parent unit and changes _root's unit
	// list: both may be stale now without a code edit of their own.
	docKind := func(u string) docstate.DocKind {
		kind, _, _ := docstate.Doc(root, u)
		return kind
	}
	var parents []string
	for _, u := range turnNewUnits(session.AddUnique(pending, acked...), before, docKind) {
		parents = session.AddUnique(parents, tree.UnitOf(u), units.RootUnit)
	}
	if len(parents) > 0 {
		pending = session.AddUnique(pending, pendingUnits(parents, session.AddUnique(j.AlreadyNotified, pending...), nil, acked, nil, tree.IsUnit, p.UpToDate)...)
	}
	if err := p.SaveCache(); err != nil {
		logx.Errorf(root, "hash cache: %v", err)
	}
	if len(pending) == 0 {
		// Keep the turn-start snapshot and the journal: another Stop hook may make
		// Claude continue this turn. PromptSubmit resets both.
		return nil, nil
	}
	f := buildFeedback(pending, failed, docKind, p.Config().Language)
	j.Notify(pending...)
	j.NotifyAckFailed(slices.Collect(maps.Keys(f.Failed))...)
	if err := j.Save(root, id); err != nil {
		// Without the notified mark the same feedback would come back on every
		// Stop (it continues the turn): say nothing rather than loop.
		logx.Errorf(root, "stop: saving the journal, feedback dropped: %v", err)
		return nil, nil
	}
	f.Agent, f.IndexFile = env.Agent, p.IndexFile()
	return stopFeedback(StopReason(f), env.Agent)
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

// cmdArg quotes a unit name for a suggested shell command: names may contain
// spaces (but never quotes, see units.SafeName).
func cmdArg(u string) string {
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
		return encodeJSON(struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}{"block", text})
	}
	var out stopOutput
	out.Hook.EventName = "Stop"
	out.Hook.AdditionalContext = text
	return encodeJSON(out)
}

// configPaused tells Claude once that tracking is paused while the config
// cannot be parsed (the units would be the defaults). The journal remembers
// the notice; without a saved journal nothing is said, since the notice
// continues the turn and would repeat on every Stop.
func configPaused(root, id string, a Agent) ([]byte, error) {
	j, err := session.LoadJournal(root, id)
	if err != nil {
		logx.Errorf(root, "stop: journal unreadable, starting empty: %v", err)
	}
	first := j.MarkConfigNotified()
	if !first {
		if err := j.Save(root, id); err != nil {
			logx.Errorf(root, "stop: saving the journal: %v", err)
		}
		return nil, nil
	}
	if err := j.Save(root, id); err != nil {
		logx.Errorf(root, "stop: saving the journal: %v", err)
		return nil, nil
	}
	return stopFeedback("auto-context: "+config.FileName+" cannot be read or parsed, so docs tracking is paused until it is fixed; run `"+a.CLI()+" status` for the error. Changes made meanwhile are checked once it is fixed.", a)
}

// recordTurn folds the changes between the turn-start snapshot and now into
// the journal and returns the units whose context doc was touched this turn
// and is not older than their newest code edit: the candidates for auto-ack.
func recordTurn(tree *units.Tree, before, cur session.Snapshot, j *session.Journal) (fresh []string) {
	// newestCode is the latest mtime of a changed code file per unit; a docs
	// edit counts for the unit only if it is not older (deleted files have no
	// mtime and do not order).
	newestCode := map[string]int64{}
	docs := map[string]bool{}
	for _, path := range session.Diff(before, cur) {
		switch kind, unit := tree.Classify(path); kind {
		case units.KindCode:
			mt := newestCode[unit] // a deleted file changes the unit, with no mtime to order by
			if fs, ok := cur[path]; ok {
				mt = max(mt, fs.MtimeNs)
			}
			newestCode[unit] = mt
		case units.KindContextDoc:
			// Only context/<unit>.md counts as a docs update: an index file touch
			// (a typo fix, or our own root index rewrite) must not ack the unit.
			docs[unit] = true
		}
	}
	// One set update each: a mass change must not cost a sort per path.
	j.AddCode(slices.Collect(maps.Keys(newestCode))...)
	j.AddDocs(slices.Collect(maps.Keys(docs))...)
	for _, u := range j.DocsTouched {
		ctx := units.ContextPath(u)
		if fs, ok := cur[ctx]; ok && fs.MtimeNs >= newestCode[u] {
			fresh = append(fresh, u)
		}
	}
	return fresh
}

// turnNewUnits returns the sorted unique units of us (except _root) that are
// new this turn: their doc is absent, or context/<u>.md did not exist at the
// turn start. Their parent unit and _root may be stale even if the new unit
// was documented (or auto-acked) in the same turn.
func turnNewUnits(us []string, before session.Snapshot, doc func(string) docstate.DocKind) []string {
	var out []string
	for _, u := range us {
		if u == units.RootUnit {
			continue
		}
		if _, ok := before[units.ContextPath(u)]; !ok || doc(u) == docstate.DocAbsent {
			out = append(out, u)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// pendingUnits returns the units that need feedback: units whose code
// changed this turn, neither acked nor notified yet, still units, and whose
// docs do not already match the code — plus units whose auto-ack failed and
// was not reported yet (even if notified before: the failure is new). A unit
// counts as updated when it was acked or its docs already match.
func pendingUnits(changed, notified, failNotified, acked []string, failed map[string]string, isUnit, upToDate func(string) bool) []string {
	var pending []string
	for _, u := range session.Minus(changed, acked, notified) {
		// A unit that stopped being one (excluded, too few files) cannot be
		// acked; docs that already match the code (an edit undone, a checkout of
		// acked code, a formatter that rewrote nothing) need no feedback.
		if !isUnit(u) || (failed[u] == "" && upToDate(u)) {
			continue
		}
		pending = append(pending, u)
	}
	for _, u := range session.Minus(slices.Collect(maps.Keys(failed)), failNotified, pending) {
		if isUnit(u) {
			pending = append(pending, u)
		}
	}
	return session.AddUnique(nil, pending...)
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

// autoAck acks the documented touched units (Project.AckTouched), logging
// failures, and returns the units acked and, per unit, why an ack failed.
func autoAck(env Env, p *project.Project, touched []string) (acked []string, failed map[string]string) {
	acked, errs, indexErr := p.AckTouched(touched, env.Now())
	if indexErr != nil {
		logx.Errorf(env.Root, "root index: %v", indexErr)
	}
	failed = map[string]string{}
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
	// Language is the configured docs language code, or "".
	Language string
	// Agent selects how skills are named; the zero value is Claude.
	Agent Agent
	// IndexFile is the per-unit index file name; "" means CLAUDE.md.
	IndexFile string
}

// StopReason builds the feedback text.
func StopReason(f Feedback) string {
	cli := f.Agent.CLI()
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
		var ctxs []string
		for _, u := range refused {
			ctx := units.ContextPath(u)
			ctxs = append(ctxs, ctx)
		}
		parts = append(parts, strings.Join(ctxs, ", ")+more+" cannot be used: each "+docstate.OutsideText+". Tell the user; replace it with a regular Markdown file if they agree.")
	}
	if unreadable, more := capList(f.Unreadable); len(unreadable) > 0 {
		var ctxs []string
		for _, u := range unreadable {
			ctx := units.ContextPath(u)
			ctxs = append(ctxs, ctx)
		}
		parts = append(parts, strings.Join(ctxs, ", ")+more+" cannot be read (e.g. permissions); fix it, then run `"+cli+" status`.")
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
	if f.Language != "" && len(f.Changed)+len(f.New) > 0 {
		parts = append(parts, LanguageNote(f.Language, idx))
	}
	return "auto-context: " + strings.Join(parts, " ")
}

// LanguageNote tells the agent the configured docs language (a validated code).
func LanguageNote(lang, indexFile string) string {
	return "Write unit docs (" + indexFile + ", context/) in language " + lang + " (`language` in " + config.FileName + ")."
}
