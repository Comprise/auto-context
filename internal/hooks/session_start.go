package hooks

import (
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
	"auto-context/internal/session"
)

// staleSessionAge is how long the session files an ended session leaves are kept.
const staleSessionAge = 7 * 24 * time.Hour

// SessionStart refreshes the root index, cleans up old state and returns the
// stale/missing notice plus every problem the user must fix, or nil. If the
// hook cannot run at all, it says so in a systemMessage instead of going silent.
// It only orchestrates: the problems come from Project.Diagnose (and the
// index and cache operations), their texts from the project's code → text
// table (channel project.Session).
func SessionStart(env Env, in Input) ([]byte, error) {
	if !project.Active(env.Root) {
		if project.ActivationRefused(env.Root) {
			// Nothing is written, but the user should know why the plugin is off.
			pr := project.Problem{Code: project.ProblemActivationRefused}
			return encodeJSON(hookOutput{
				SystemMessage: "auto-context: " + pr.Text(project.Session, project.Render{Command: env.CLI()}),
				Hook:          &hookSpecific{EventName: "SessionStart"},
			})
		}
		return nil, nil
	}
	// Counted before this hook logs anything, and the mark written after:
	// what this hook logs it reports below as problems, not again as errors.
	defer markSessionStart(env)
	p, err := load(env)
	if err != nil {
		return failNotice(env, err)
	}
	r := project.Render{Command: env.CLI(), IndexFile: p.IndexFile()}
	// After load (which creates .auto-context/) and before anything is
	// logged: Diagnose reads the log. With a broken config only the config
	// and the log are reported, so only they are checked.
	var diag []project.Problem
	if p.ConfigBroken() {
		diag = append(p.DiagnoseConfig(), p.LogProblems()...)
	} else {
		diag = p.Diagnose()
	}
	logProblems := logStatus(env.Root, diag, r)
	user := configProblem(env.Root, diag, r)
	if p.ConfigBroken() {
		// The units come from defaults: no statuses, index or cache update, which
		// would act on the wrong units.
		return sessionNotice(prefixed(logProblems), user)
	}
	problems := envProblems(env.Root, diag, r)
	statuses := p.Statuses()
	logTimeUp(env.Root, statuses)
	g := classifyStatuses(statuses)
	problems = append(problems, unitProblems(env, p, g, diag, r)...)
	problems = append(problems, indexProblems(env.Root, p, statuses, diag, r)...)
	problems = append(problems, cacheSaveProblems(env.Root, p, r)...)
	problems = append(problems, logProblems...)
	return sessionAnswer(g, problems, p, env.Agent, user)
}

// findProblems returns the problems of diag with one of codes, in diag's order.
func findProblems(diag []project.Problem, codes ...project.ProblemCode) []project.Problem {
	var out []project.Problem
	for _, pr := range diag {
		if slices.Contains(codes, pr.Code) {
			out = append(out, pr)
		}
	}
	return out
}

// configProblem logs a config that cannot be read or has invalid settings
// and returns the text the user must see, or "".
func configProblem(root string, diag []project.Problem, r project.Render) string {
	for _, pr := range findProblems(diag, project.ProblemConfigUnreadable, project.ProblemConfigInvalid, project.ProblemConfigDegraded) {
		// A degraded config reads as ignored settings too; what it pauses
		// comes with the index (indexProblems).
		logx.Errorf(root, "config: %v", pr.Err)
		return pr.Text(project.Session, r)
	}
	return ""
}

// envProblems reports what surrounds the units: whether the config is
// committed and the hash cache; it logs discovery warnings (counted by
// unitProblems) and cleans up old session files.
func envProblems(root string, diag []project.Problem, r project.Render) []string {
	var problems []string
	for _, pr := range findProblems(diag, project.ProblemConfigIgnoreUnknown, project.ProblemConfigIgnored) {
		if pr.Err != nil {
			logx.Errorf(root, "config check-ignore: %v", pr.Err)
		}
		problems = append(problems, pr.Text(project.Session, r))
	}
	for _, pr := range findProblems(diag, project.ProblemCacheUnusable) {
		logx.Errorf(root, "hash cache: %v", pr.Err)
		problems = append(problems, pr.Text(project.Session, r))
	}
	for _, pr := range findProblems(diag, project.ProblemDiscoverWarning) {
		logx.Errorf(root, "discover: %v", pr.Err)
	}
	// File mtimes are wall-clock time, so is the cutoff.
	if err := session.RemoveStale(root, staleSessionAge, time.Now()); err != nil {
		logx.Errorf(root, "state cleanup: %v", err)
		problems = append(problems, "old session files in .auto-context/state could not be removed; ask the user to check .auto-context/log.")
	}
	return problems
}

// logTimeUp logs the units left unhashed because the hook's budget ran out
// (reported as unreadable sources like any other hash error).
func logTimeUp(root string, statuses []docstate.UnitStatus) {
	n := 0
	for _, s := range statuses {
		if s.Note == docstate.NoteHashError && s.Detail == docstate.TimeUp {
			n++
		}
	}
	if n > 0 {
		logx.Errorf(root, "session start: %s: %d unit(s) not hashed", docstate.TimeUp, n)
	}
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
func unitProblems(env Env, p *project.Project, g statusGroups, diag []project.Problem, r project.Render) []string {
	cli := r.Command
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
	if ws := findProblems(diag, project.ProblemDiscoverWarning); len(ws) > 0 {
		problems = append(problems, ws[0].Text(project.Session, r)) // one count for all
	}
	return problems
}

// indexProblems rewrites the root index from statuses and reports why it
// could not be. A root index Diagnose already found unusable is reported as
// found (Update would refuse it for the same reason) and not rewritten. A
// degraded config pauses the index and auto-ack: said with a fixed text, not
// logged (the config problems already are).
func indexProblems(root string, p *project.Project, statuses []docstate.UnitStatus, diag []project.Problem, r project.Render) []string {
	var pr project.Problem
	if found := findProblems(diag, project.ProblemIndexMarkers, project.ProblemIndexUnusable); len(found) > 0 && !p.Config().DiscoveryReset {
		pr = found[0]
	} else if _, err := p.UpdateIndex(statuses); err != nil {
		pr = project.IndexProblem(err)
	} else {
		return nil
	}
	if pr.Code != project.ProblemIndexPaused {
		logx.Errorf(root, "root index: %v", pr.Err)
	}
	return []string{pr.Text(project.Session, r)}
}

// cacheSaveProblems saves the hash cache and reports a failure.
func cacheSaveProblems(root string, p *project.Project, r project.Render) []string {
	if err := p.SaveCache(); err != nil {
		logx.Errorf(root, "hash cache: %v", err)
		return []string{project.Problem{Code: project.ProblemCacheSave, Err: err}.Text(project.Session, r)}
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
func logStatus(root string, diag []project.Problem, r project.Render) []string {
	var out []string
	for _, pr := range findProblems(diag, project.ProblemLogUnreadable, project.ProblemLogUnwritable, project.ProblemLogErrors) {
		if pr.Code == project.ProblemLogErrors {
			// Diagnose counts every entry; SessionStart the ones since the
			// last session.
			n, _ := errorsSinceLastSession(root)
			if n == 0 {
				continue
			}
			pr.N = n
		}
		out = append(out, pr.Text(project.Session, r))
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
