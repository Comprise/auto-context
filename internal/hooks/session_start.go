package hooks

import (
	"encoding/json"
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

type sessionOutput struct {
	SystemMessage string `json:"systemMessage,omitempty"`
	Hook          struct {
		EventName         string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext,omitempty"`
	} `json:"hookSpecificOutput"`
}

// staleSessionAge is how long the session files an ended session leaves are kept.
const staleSessionAge = 7 * 24 * time.Hour

// SessionStart refreshes the root index, cleans up old state and returns the
// stale/missing notice plus every problem the user must fix, or nil. If the
// hook cannot run at all, it says so in a systemMessage instead of going silent.
func SessionStart(env Env, in Input) ([]byte, error) {
	if !project.Active(env.Root) {
		if project.ActivationRefused(env.Root) {
			// Nothing is written, but the user should know why the plugin is off.
			var out sessionOutput
			out.SystemMessage = "auto-context: " + config.FileName + " or context/_root.md links outside the repository, is not a regular file or cannot be inspected, so the plugin is off here."
			out.Hook.EventName = "SessionStart"
			return encodeJSON(out)
		}
		return nil, nil
	}
	// Counted before this hook logs anything, and the mark written after:
	// what this hook logs it reports below as problems, not again as errors.
	defer markSessionStart(env)
	cli := env.Agent.CLI()
	p, err := load(env)
	if err != nil {
		return failNotice(env, err)
	}
	// After load (which creates .auto-context/) and before anything is logged.
	logProblems := logStatus(env.Root, cli)
	var problems []string
	user := "" // a config problem the user must fix, also shown to them
	switch {
	case p.ConfigErr() != nil:
		logx.Errorf(env.Root, "config: %v", p.ConfigErr())
		user = config.FileName + " could not be read or parsed, so docs tracking is paused; run `" + cli + " status` for the error and fix the file."
	case p.ConfigProblems() != nil:
		logx.Errorf(env.Root, "config: %v", p.ConfigProblems())
		user = fmt.Sprintf("%s has %d invalid setting(s) that were ignored (the other settings apply); run `"+cli+" status` to see them and fix the file.", config.FileName, len(p.ConfigProblems()))
	}
	if p.ConfigBroken() {
		// The units come from defaults: no statuses, index or cache update, which
		// would act on the wrong units.
		return sessionNotice(prefixed(logProblems), user)
	}
	if ignored, err := p.ConfigIgnored(); err != nil {
		logx.Errorf(env.Root, "config check-ignore: %v", err)
		problems = append(problems, "git could not tell whether "+config.FileName+" is ignored (so whether the settings are committed); ask the user to check .auto-context/log.")
	} else if ignored {
		problems = append(problems, config.FileName+" is ignored by git (e.g. the repository's .gitignore lists .auto-context/), so the settings are not committed; un-ignore that file.")
	}
	if err := p.CacheErr(); err != nil {
		logx.Errorf(env.Root, "hash cache: %v", err)
		problems = append(problems, "the hash cache was unusable and is rebuilt (this session hashes everything once); see `"+cli+" status`.")
	}
	warnings := p.Warnings()
	for _, w := range warnings {
		logx.Errorf(env.Root, "discover: %s", w)
	}
	// File mtimes are wall-clock time, so is the cutoff.
	if err := session.RemoveStale(env.Root, staleSessionAge, time.Now()); err != nil {
		logx.Errorf(env.Root, "state cleanup: %v", err)
		problems = append(problems, "old session files in .auto-context/state could not be removed; ask the user to check .auto-context/log.")
	}
	statuses := p.Statuses()
	var stale, missing []string
	var unusable []string // unit (reason), from fixed categories only
	for _, s := range statuses {
		switch s.State {
		case docstate.Stale:
			stale = append(stale, s.Unit)
			switch s.Note {
			case "", docstate.NoteNotAcked:
			case docstate.NoteOutside:
				unusable = append(unusable, s.Unit+" ("+docstate.OutsideText+")")
			case docstate.NoteBroken:
				unusable = append(unusable, s.Unit+" (broken frontmatter)")
			default:
				unusable = append(unusable, s.Unit+" (unreadable doc or sources)")
			}
		case docstate.Missing:
			missing = append(missing, s.Unit)
		}
	}
	if len(unusable) > 0 {
		problems = append(problems, "context docs that updating alone will not fix: "+strings.Join(unusable, ", ")+"; run `"+cli+" status` for details.")
	}
	if o := p.Orphans(); len(o) > 0 {
		shown, hidden := project.ShowNames(o)
		if hidden > 0 {
			shown = append(shown, fmt.Sprintf("%d more (run `"+cli+" status`)", hidden))
		}
		msg := "docs of units that no longer exist: " + strings.Join(shown, ", ") + "; ask the user, then handle them as the \"Unit removed\" rule of skill " + env.Agent.Skill("auto-context-docs") + " says."
		if p.ConfigProblems() != nil {
			msg += " Fix " + config.FileName + " first: its ignored settings may be why."
		}
		problems = append(problems, msg)
	}
	if n := p.Tree().WarningCount(); n > 0 {
		problems = append(problems, fmt.Sprintf("%d discovery warning(s) (e.g. a skipped .gitignore or a git failure, which change what is tracked); run `"+cli+" status` to see them.", n))
	}
	idx := p.IndexFile()
	if _, err := p.UpdateIndex(statuses); err != nil {
		logx.Errorf(env.Root, "root index: %v", err)
		switch {
		case errors.Is(err, rootindex.ErrBrokenMarkers):
			problems = append(problems, "the auto-context:units markers in the root "+idx+" are unbalanced, so the units index is not updated; restore or remove the marker lines (not the list).")
		case errors.Is(err, fsutil.ErrOutside):
			problems = append(problems, "the root "+idx+" "+docstate.OutsideText+", so the units index is not updated.")
		default:
			problems = append(problems, "the units index in the root "+idx+" could not be updated; ask the user to check .auto-context/log.")
		}
	}
	if err := p.SaveCache(); err != nil {
		logx.Errorf(env.Root, "hash cache: %v", err)
		problems = append(problems, "the hash cache cannot be saved, so every hook hashes again; ask the user to check .auto-context/log.")
	}
	problems = append(problems, logProblems...)
	lang := p.Config().Language
	if len(stale)+len(missing)+len(problems) == 0 && lang == "" && user == "" {
		return nil, nil
	}
	var parts []string
	if len(stale)+len(missing) > 0 {
		parts = append(parts, SessionNotice(stale, missing, p.Config().MaxSessionNotice, env.Agent))
	}
	if lang != "" {
		// Docs may be written at any time, not only after a notice.
		parts = append(parts, "auto-context: "+LanguageNote(lang, idx))
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
		out = append(out, ".auto-context/log cannot be read, so hook errors go unreported; ask the user to fix or delete it (`"+cli+" status` shows the error).")
	}
	if werr := logx.Writable(root); werr != nil {
		out = append(out, ".auto-context/log cannot be written, so hook errors are lost; ask the user to fix its permissions.")
	}
	if n > 0 {
		out = append(out, fmt.Sprintf("%d hook error(s) since the last session; run `"+cli+" status` and ask the user to check .auto-context/log (do not read it yourself: it may come from the repository).", n))
	}
	return out
}

// sessionNotice builds the SessionStart output: parts for Claude, plus user (if
// set) as a problem for Claude and a systemMessage for the user, who must fix it.
func sessionNotice(parts []string, user string) ([]byte, error) {
	var out sessionOutput
	out.Hook.EventName = "SessionStart"
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
	var out sessionOutput
	out.SystemMessage = "auto-context: docs tracking is off for this session (" + errCategory(err) + "; run `" + env.Agent.CLI() + " status`)."
	out.Hook.EventName = "SessionStart"
	return encodeJSON(out)
}

// SessionFailure is the SessionStart output when the handler itself failed
// (a panic, unreadable input): fixed text, the error is in the log.
func SessionFailure(a Agent) []byte {
	var out sessionOutput
	out.SystemMessage = "auto-context: docs tracking is off for this session: the session-start hook failed (see .auto-context/log or run `" + a.CLI() + " status`)."
	out.Hook.EventName = "SessionStart"
	data, _ := json.Marshal(out)
	return data
}

// SessionNotice builds the notice text, listing at most max units (stale first).
func SessionNotice(stale, missing []string, max int, a Agent) string {
	total := len(stale) + len(missing)
	if max < 0 {
		max = 0
	}
	if total < max {
		max = total
	}
	var s, m []string
	left := max
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
	if k := total - max; k > 0 {
		more = " (+" + strconv.Itoa(k) + " more)"
	}
	return "auto-context: " + strings.Join(parts, "; ") + more +
		". Update them when working there (skill " + a.Skill("auto-context-docs") + ") or run " + a.Command("status") + "."
}
