package hooks

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/fsutil"
	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/rootindex"
	"auto-context/internal/session"
)

// staleSessionAge is how long the session files an ended session leaves are kept.
const staleSessionAge = 7 * 24 * time.Hour

// SessionStart refreshes the root index, cleans up old state and returns the
// stale/missing notice plus every problem the user must fix, or nil. If the
// hook cannot run at all, it says so in a systemMessage instead of going silent.
// It only orchestrates; each step returns its problem texts.
func SessionStart(env Env, in Input) ([]byte, error) {
	if !project.Active(env.Root) {
		if project.ActivationRefused(env.Root) {
			// Nothing is written, but the user should know why the plugin is off.
			return encodeJSON(hookOutput{
				SystemMessage: "auto-context: " + config.FileName + " or context/_root.md links outside the repository, is not a regular file or cannot be inspected, so the plugin is off here.",
				Hook:          &hookSpecific{EventName: "SessionStart"},
			})
		}
		return nil, nil
	}
	// Counted before this hook logs anything, and the mark written after:
	// what this hook logs it reports below as problems, not again as errors.
	defer markSessionStart(env)
	cli := env.CLI()
	p, err := load(env)
	if err != nil {
		return failNotice(env, err)
	}
	// After load (which creates .auto-context/) and before anything is logged.
	logProblems := logStatus(env.Root, cli)
	user := configProblem(env.Root, p, cli)
	if p.ConfigBroken() {
		// The units come from defaults: no statuses, index or cache update, which
		// would act on the wrong units.
		return sessionNotice(prefixed(logProblems), user)
	}
	problems := envProblems(env.Root, p, cli)
	statuses := p.Statuses()
	g := classifyStatuses(statuses)
	problems = append(problems, unitProblems(env, p, g, cli)...)
	problems = append(problems, indexProblems(env.Root, p, statuses, cli)...)
	problems = append(problems, cacheSaveProblems(env.Root, p)...)
	problems = append(problems, logProblems...)
	return sessionAnswer(g, problems, p, env.Agent, user)
}

// configProblem logs a config that cannot be read or has invalid settings
// and returns the text the user must see, or "".
func configProblem(root string, p *project.Project, cli string) string {
	switch {
	case p.ConfigErr() != nil:
		logx.Errorf(root, "config: %v", p.ConfigErr())
		return fmt.Sprintf("%s could not be read or parsed, so docs tracking is paused; run `%s status` for the error and fix the file.", config.FileName, cli)
	case p.ConfigProblems() != nil:
		logx.Errorf(root, "config: %v", p.ConfigProblems())
		return fmt.Sprintf("%s has %d invalid setting(s) that were ignored (the other settings apply); run `%s status` to see them and fix the file.", config.FileName, len(p.ConfigProblems()), cli)
	}
	return ""
}

// envProblems checks what surrounds the units: whether the config is
// committed, the hash cache, discovery warnings (logged) and the cleanup of
// old session files.
func envProblems(root string, p *project.Project, cli string) []string {
	var problems []string
	if ignored, err := p.ConfigIgnored(); err != nil {
		logx.Errorf(root, "config check-ignore: %v", err)
		problems = append(problems, "git could not tell whether "+config.FileName+" is ignored (so whether the settings are committed); ask the user to check .auto-context/log.")
	} else if ignored {
		problems = append(problems, config.FileName+" is ignored by git (e.g. the repository's .gitignore lists .auto-context/), so the settings are not committed; un-ignore that file.")
	}
	if err := p.CacheErr(); err != nil {
		logx.Errorf(root, "hash cache: %v", err)
		problems = append(problems, fmt.Sprintf("the hash cache was unusable and is rebuilt (this session hashes everything once); see `%s status`.", cli))
	}
	for _, w := range p.Warnings() {
		logx.Errorf(root, "discover: %s", w)
	}
	// File mtimes are wall-clock time, so is the cutoff.
	if err := session.RemoveStale(root, staleSessionAge, time.Now()); err != nil {
		logx.Errorf(root, "state cleanup: %v", err)
		problems = append(problems, "old session files in .auto-context/state could not be removed; ask the user to check .auto-context/log.")
	}
	return problems
}

// statusGroups is what SessionStart reports of the unit statuses.
type statusGroups struct {
	stale, missing []string
	unusable       []string // "unit (reason)", from fixed categories only
	oldScheme      int      // up to date, acked in the old hash scheme
}

// classifyStatuses groups the statuses for the notice.
func classifyStatuses(statuses []docstate.UnitStatus) statusGroups {
	var g statusGroups
	for _, s := range statuses {
		switch s.State {
		case docstate.Stale:
			g.stale = append(g.stale, s.Unit)
			switch s.Note {
			case "", docstate.NoteNotAcked:
			case docstate.NoteOutside:
				g.unusable = append(g.unusable, s.Unit+" ("+docstate.OutsideText+")")
			case docstate.NoteBroken:
				g.unusable = append(g.unusable, s.Unit+" (broken frontmatter)")
			default:
				g.unusable = append(g.unusable, s.Unit+" (unreadable doc or sources)")
			}
		case docstate.Missing:
			g.missing = append(g.missing, s.Unit)
		case docstate.OK:
			if docstate.OldScheme(s) {
				g.oldScheme++
			}
		}
	}
	return g
}

// unitProblems reports unusable docs, docs acked in the old hash scheme,
// orphan docs and the number of discovery warnings.
func unitProblems(env Env, p *project.Project, g statusGroups, cli string) []string {
	var problems []string
	if len(g.unusable) > 0 {
		problems = append(problems, fmt.Sprintf("context docs that updating alone will not fix: %s; run `%s status` for details.", strings.Join(g.unusable, ", "), cli))
	}
	if g.oldScheme > 0 {
		problems = append(problems, fmt.Sprintf("%d unit(s) were acked by an older auto-context; run `%s ack --upgrade`.", g.oldScheme, cli))
	}
	if o := p.Orphans(); len(o) > 0 {
		shown, hidden := project.ShowNames(o)
		if hidden > 0 {
			shown = append(shown, fmt.Sprintf("%d more (run `%s status`)", hidden, cli))
		}
		msg := "docs of units that no longer exist: " + strings.Join(shown, ", ") + "; ask the user, then handle them as the \"Unit removed\" rule of skill " + env.Agent.Skill("auto-context-docs") + " says."
		if p.ConfigProblems() != nil {
			msg += " Fix " + config.FileName + " first: its ignored settings may be why."
		}
		problems = append(problems, msg)
	}
	if n := p.Tree().WarningCount(); n > 0 {
		problems = append(problems, fmt.Sprintf("%d discovery warning(s) (e.g. a skipped .gitignore or a git failure, which change what is tracked); run `%s status` to see them.", n, cli))
	}
	return problems
}

// indexProblems rewrites the root index from statuses and reports why it
// could not be. A degraded config pauses the index and auto-ack: said with a
// fixed text, not logged (the config problems already are).
func indexProblems(root string, p *project.Project, statuses []docstate.UnitStatus, cli string) []string {
	_, err := p.UpdateIndex(statuses)
	if err == nil {
		return nil
	}
	idx := p.IndexFile()
	if errors.Is(err, project.ErrConfigDegraded) {
		return []string{fmt.Sprintf("%s has invalid settings that decide the units, so the units index and auto-ack are paused until it is fixed; run `%s status` to see them.", config.FileName, cli)}
	}
	logx.Errorf(root, "root index: %v", err)
	switch {
	case errors.Is(err, rootindex.ErrBrokenMarkers):
		return []string{"the auto-context:units markers in the root " + idx + " are unbalanced or repeated, so the units index is not updated; restore or remove the marker lines (not the list)."}
	case errors.Is(err, fsutil.ErrOutside):
		return []string{"the root " + idx + " " + docstate.OutsideText + ", so the units index is not updated."}
	}
	return []string{"the units index in the root " + idx + " could not be updated; ask the user to check .auto-context/log."}
}

// cacheSaveProblems saves the hash cache and reports a failure.
func cacheSaveProblems(root string, p *project.Project) []string {
	if err := p.SaveCache(); err != nil {
		logx.Errorf(root, "hash cache: %v", err)
		return []string{"the hash cache cannot be saved, so every hook hashes again; ask the user to check .auto-context/log."}
	}
	return nil
}

// sessionAnswer builds SessionStart's output from the status groups and the
// problems, or nil when there is nothing to say.
func sessionAnswer(g statusGroups, problems []string, p *project.Project, a Agent, user string) ([]byte, error) {
	lang := p.Config().Language
	if len(g.stale)+len(g.missing)+len(problems) == 0 && lang == "" && user == "" {
		return nil, nil
	}
	var parts []string
	if len(g.stale)+len(g.missing) > 0 {
		parts = append(parts, SessionNotice(g.stale, g.missing, p.Config().MaxSessionNotice, a))
	}
	if lang != "" {
		// Docs may be written at any time, not only after a notice.
		parts = append(parts, "auto-context: "+LanguageNote(lang, p.IndexFile()))
	}
	parts = append(parts, prefixed(problems)...)
	return sessionNotice(parts, user)
}

func prefixed(problems []string) []string {
	out := make([]string, len(problems))
	for i, pr := range problems {
		out[i] = "auto-context: " + pr
	}
	return out
}

// logStatus reports what the log says about earlier hooks: errors since the
// last SessionStart, and a log that cannot be read or written (its errors
// would be lost). The log's text is never quoted: it may come from the repo.
func logStatus(root, cli string) []string {
	var out []string
	n, err := errorsSinceLastSession(root)
	if err != nil {
		out = append(out, fmt.Sprintf(".auto-context/log cannot be read, so hook errors go unreported; ask the user to fix or delete it (`%s status` shows the error).", cli))
	}
	if werr := logx.Writable(root); werr != nil {
		out = append(out, ".auto-context/log cannot be written, so hook errors are lost; ask the user to fix its permissions.")
	}
	if n > 0 {
		out = append(out, fmt.Sprintf("%d hook error(s) since the last session; run `%s status` and ask the user to check .auto-context/log (do not read it yourself: it may come from the repository).", n, cli))
	}
	return out
}

// sessionNotice builds the SessionStart output: parts for Claude, plus user (if
// set) as a problem for Claude and a systemMessage for the user, who must fix it.
func sessionNotice(parts []string, user string) ([]byte, error) {
	out := hookOutput{Hook: &hookSpecific{EventName: "SessionStart"}}
	if user != "" {
		if !slices.Contains(parts, "auto-context: "+user) {
			parts = append(parts, "auto-context: "+user)
		}
		out.SystemMessage = "auto-context: " + user
	}
	out.Hook.AdditionalContext = strings.Join(parts, " ")
	return encodeJSON(out)
}

func sessionMark(root string) string {
	return fsutil.StatePath(root, "state", "last-session-start")
}

// errorsSinceLastSession counts log entries written after the previous
// SessionStart finished (all of them the first time).
// An unreadable log is returned as the error: its own entries would be lost.
func errorsSinceLastSession(root string) (int, error) {
	var since time.Time
	if data, err := fsutil.ReadFile(root, sessionMark(root), 64); err == nil {
		since, _ = time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	}
	entries, err := logx.Entries(root)
	n := 0
	for _, t := range entries {
		if t.After(since) {
			n++
		}
	}
	return n, err
}

// markSessionStart records when this SessionStart finished. It uses the wall
// clock, not Env.Now: it is compared with log timestamps.
func markSessionStart(env Env) {
	if err := fsutil.WriteState(env.Root, sessionMark(env.Root), []byte(time.Now().Format(time.RFC3339Nano))); err != nil {
		logx.Errorf(env.Root, "session mark: %v", err)
	}
}

// failNotice logs err and tells the user that docs tracking is off for now.
func failNotice(env Env, err error) ([]byte, error) {
	logx.Errorf(env.Root, "session start: %v", err)
	return encodeJSON(hookOutput{
		SystemMessage: "auto-context: docs tracking is off for this session (" + errCategory(err) + "; run `" + env.CLI() + " status`).",
		Hook:          &hookSpecific{EventName: "SessionStart"},
	})
}

// SessionFailure is the SessionStart output when the handler itself failed
// (a panic, unreadable input): fixed text, the error is in the log.
func SessionFailure(a Agent) []byte {
	// Encoding a struct of strings cannot fail.
	data, _ := encodeJSON(hookOutput{
		SystemMessage: "auto-context: docs tracking is off for this session: the session-start hook failed (see .auto-context/log or run `" + Env{Agent: a}.CLI() + " status`).",
		Hook:          &hookSpecific{EventName: "SessionStart"},
	})
	return data
}

// SessionNotice builds the notice text, listing at most limit units (stale
// first); with limit 0 it gives only the counts.
func SessionNotice(stale, missing []string, limit int, a Agent) string {
	total := len(stale) + len(missing)
	if limit < 0 {
		limit = 0
	}
	tail := ". Update them when working there (skill " + a.Skill("auto-context-docs") + ") or run " + a.Command("status") + "."
	if limit == 0 {
		// No names to list: counts, so the notice still says what is wrong.
		var parts []string
		if len(stale) > 0 {
			parts = append(parts, "stale for "+unitCount(len(stale)))
		}
		if len(missing) > 0 {
			parts = append(parts, "missing for "+unitCount(len(missing)))
		}
		return "auto-context: docs " + strings.Join(parts, ", ") + tail
	}
	if total < limit {
		limit = total
	}
	var s, m []string
	left := limit
	for _, u := range stale {
		if left == 0 {
			break
		}
		s = append(s, u)
		left--
	}
	for _, u := range missing {
		if left == 0 {
			break
		}
		m = append(m, u)
		left--
	}
	var parts []string
	if len(s) > 0 {
		parts = append(parts, "docs stale for "+strings.Join(s, ", "))
	}
	if len(m) > 0 {
		parts = append(parts, "missing for "+strings.Join(m, ", "))
	}
	more := ""
	if k := total - limit; k > 0 {
		more = " (+" + strconv.Itoa(k) + " more)"
	}
	return "auto-context: " + strings.Join(parts, "; ") + more + tail
}

// unitCount is "1 unit" or "N units".
func unitCount(n int) string {
	if n == 1 {
		return "1 unit"
	}
	return strconv.Itoa(n) + " units"
}
