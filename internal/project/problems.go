package project

import (
	"context"
	"errors"
	"fmt"
	"os"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/fsutil"
	"auto-context/internal/gitx"
	"auto-context/internal/logx"
	"auto-context/internal/rootindex"
	"auto-context/internal/units"
)

// ProblemCode names one kind of project health problem. Every channel that
// reports problems (status text and JSON, SessionStart, Stop) renders it by
// its code through Problem.Text, never by the error's text: errors quote
// repository paths, which must not reach Claude's context.
type ProblemCode int

const (
	// ProblemNotActive: no marker, the plugin is off (a note).
	ProblemNotActive ProblemCode = iota + 1
	// ProblemActivationRefused: a marker exists but is not used.
	ProblemActivationRefused
	// ProblemStateDirOutside: .auto-context leads outside the repository.
	ProblemStateDirOutside
	// ProblemConfigUnreadable: the config cannot be read or parsed (Err).
	ProblemConfigUnreadable
	// ProblemConfigInvalid: settings were dropped (Err: config.Problems, N: their number).
	ProblemConfigInvalid
	// ProblemConfigDegraded: as ProblemConfigInvalid, and a dropped or
	// reset setting decides the units (config.DiscoveryReset).
	ProblemConfigDegraded
	// ProblemConfigIgnoreUnknown: git failed telling whether the config is ignored (Err).
	ProblemConfigIgnoreUnknown
	// ProblemConfigIgnored: git ignores the config file.
	ProblemConfigIgnored
	// ProblemDiscoverWarning: one discovery warning (Err: its fixed text,
	// N: the number of warnings, including those past the cap).
	ProblemDiscoverWarning
	// ProblemCacheUnusable: the hash cache could not be used (Err).
	ProblemCacheUnusable
	// ProblemCacheSave: the hash cache could not be saved (Err); an
	// operation's result, not found by Diagnose.
	ProblemCacheSave
	// ProblemIndexMarkers: the root index's markers are unbalanced or repeated.
	ProblemIndexMarkers
	// ProblemIndexUnusable: the root index file cannot be used (Err).
	ProblemIndexUnusable
	// ProblemIndexPaused: the index (and auto-ack) is not written because
	// of a degraded config (Err: ErrConfigDegraded); an operation's result,
	// not found by Diagnose, which reports ProblemConfigDegraded instead.
	ProblemIndexPaused
	// ProblemLogUnreadable: .auto-context/log cannot be read (Err; a note).
	ProblemLogUnreadable
	// ProblemLogUnwritable: .auto-context/log cannot be written (Err).
	ProblemLogUnwritable
	// ProblemLogErrors: the log holds N hook errors (a note).
	ProblemLogErrors
)

// Severity is how a problem is shown: a warning (something does not work
// as configured) or a note.
type Severity int

const (
	// SeverityWarning: something does not work as configured (status
	// `warning:`).
	SeverityWarning Severity = iota + 1
	// SeverityNote: information (status `note:`).
	SeverityNote
)

// Severity is the severity of the problems of code c (from the code table).
func (c ProblemCode) Severity() Severity { return problemTable[c].severity }

// newProblem builds a problem of code with its severity from the code table.
func newProblem(code ProblemCode, err error, n int) Problem {
	return Problem{Code: code, Severity: code.Severity(), Err: err, N: n}
}

// Problem is one finding of Diagnose (or of an operation, such as saving
// the cache or writing the index). Severity is the code's (Diagnose and
// IndexProblem fill it from the code table); Err is the cause, for the log
// only; Unit is the unit concerned, for per-unit problems (none of
// Diagnose's checks is per unit); N is a count the text shows.
type Problem struct {
	Code     ProblemCode
	Severity Severity
	Unit     string
	Err      error
	N        int
}

// Diagnose runs every health check of the project: activation, the state
// dir, the config (unreadable, invalid or unit-deciding settings dropped,
// ignored by git), discovery warnings, the hash cache, the root index and
// the log. While the config is broken the cache and the index are not
// checked: the units would be the defaults. It writes nothing and loads the
// hash cache.
func (p *Project) Diagnose() []Problem {
	out := p.DiagnoseOpen()
	if !p.ConfigBroken() {
		if err := p.CacheErr(); err != nil {
			out = append(out, newProblem(ProblemCacheUnusable, err, 0))
		}
		if err := rootindex.Check(p.root, p.IndexFile()); err != nil {
			out = append(out, IndexProblem(err))
		}
	}
	return append(out, p.LogProblems()...)
}

// DiagnoseOpen is the part of Diagnose a command needs after opening the
// project: activation, the state dir, the config and discovery warnings. It
// neither loads the hash cache nor reads the index or the log.
func (p *Project) DiagnoseOpen() []Problem {
	var out []Problem
	switch {
	case ActivationRefused(p.root):
		out = append(out, newProblem(ProblemActivationRefused, nil, 0))
	case !Active(p.root):
		out = append(out, newProblem(ProblemNotActive, nil, 0))
	}
	if err := p.StateDirErr(); err != nil {
		out = append(out, newProblem(ProblemStateDirOutside, err, 0))
	}
	out = append(out, p.DiagnoseConfig()...)
	if ignored, err := p.ConfigIgnored(); err != nil {
		out = append(out, newProblem(ProblemConfigIgnoreUnknown, err, 0))
	} else if ignored {
		out = append(out, newProblem(ProblemConfigIgnored, nil, 0))
	}
	n := p.WarningCount()
	for _, w := range p.Warnings() {
		out = append(out, newProblem(ProblemDiscoverWarning, errors.New(w), n))
	}
	return out
}

// DiagnoseConfig is the config part of Diagnose, without git: unreadable,
// or settings dropped (ProblemConfigDegraded when one decides the units).
// SessionStart needs only it and the log while the config is broken.
func (p *Project) DiagnoseConfig() []Problem {
	switch {
	case p.cfgErr != nil:
		return []Problem{newProblem(ProblemConfigUnreadable, p.cfgErr, 0)}
	case p.cfgProblems != nil:
		code := ProblemConfigInvalid
		if p.cfg.DiscoveryReset {
			code = ProblemConfigDegraded
		}
		return []Problem{newProblem(code, p.cfgProblems, len(p.cfgProblems))}
	}
	return nil
}

// LogProblems is the log part of Diagnose: unreadable, unwritable (only in
// an active project: an inactive one is never written to) and the errors it
// holds. status checks it again after its own writes to the log.
func (p *Project) LogProblems() []Problem {
	var out []Problem
	entries, err := logx.Entries(p.root)
	if err != nil {
		out = append(out, newProblem(ProblemLogUnreadable, err, 0))
	}
	if Active(p.root) {
		if werr := logx.Writable(p.root); werr != nil {
			out = append(out, newProblem(ProblemLogUnwritable, werr, 0))
		}
	}
	if len(entries) > 0 {
		out = append(out, newProblem(ProblemLogErrors, nil, len(entries)))
	}
	return out
}

// IndexProblem is the problem of a root index that could not be checked or
// written (rootindex.Check, UpdateIndex): its markers, the config that
// pauses it (ProblemIndexPaused), or the file.
func IndexProblem(err error) Problem {
	switch {
	case errors.Is(err, rootindex.ErrBrokenMarkers):
		return newProblem(ProblemIndexMarkers, err, 0)
	case errors.Is(err, ErrConfigDegraded):
		return newProblem(ProblemIndexPaused, err, 0)
	}
	return newProblem(ProblemIndexUnusable, err, 0)
}

// Channel is where a problem or an error category is shown.
type Channel int

const (
	// CLI is the output of the commands: status (text and JSON) and the
	// warnings of the others.
	CLI Channel = iota + 1
	// Session is SessionStart's additionalContext (and systemMessage).
	Session
	// Stop is Stop's feedback.
	Stop
)

// Render is what the texts need besides the problem.
type Render struct {
	// Command is the command line the hooks suggest (hooks.Env.CLI).
	Command string
	// IndexFile is the name of the root index file.
	IndexFile string
	// Detail, for the CLI, logs err (what names the operation) and returns
	// the suffix that points to the log; nil adds nothing.
	Detail func(err error, what string) string
	// Ctx, for the CLI, is the command's context: once it is canceled an
	// error reads "interrupted" (CauseOfCtx); nil: CauseOf.
	Ctx context.Context
}

// ErrText is an error as the CLI shows it: its category, plus the pointer
// to the log where the full text went.
func (r Render) ErrText(err error, what string) string {
	c := CauseOf(err)
	if r.Ctx != nil {
		c = CauseOfCtx(r.Ctx, err)
	}
	s := c.Text(CLI)
	if r.Detail != nil && !c.fixed() {
		s += r.Detail(err, what)
	}
	return s
}

// Text is pr as channel ch shows it, or "" when ch does not show this code
// (it is reported another way there). The texts are fixed: only categories
// and counts, never the error's text.
func (pr Problem) Text(ch Channel, r Render) string {
	fn := problemTable[pr.Code].texts[ch]
	if fn == nil {
		return ""
	}
	return fn(pr, r)
}

type textFunc func(pr Problem, r Render) string

// fixedText is a textFunc of a constant text.
func fixedText(s string) textFunc { return func(Problem, Render) string { return s } }

// problemSpec is a code's row of the table: its severity and its text per
// channel.
type problemSpec struct {
	severity Severity
	texts    map[Channel]textFunc
}

// problemTable is the code → severity and text table. A missing channel
// does not show the code: SessionStart learns of a linked state dir from
// load failing (failNotice), of an inactive project from Active; the CLI
// says a paused index through the ack's ErrConfigDegraded.
var problemTable = map[ProblemCode]problemSpec{
	ProblemNotActive: {SeverityNote, map[Channel]textFunc{
		CLI: fixedText("auto-context is not enabled here (no .auto-context/config.yaml or context/_root.md); run /auto-context:init to enable it"),
	}},
	ProblemActivationRefused: {SeverityNote, map[Channel]textFunc{
		CLI:     fixedText("auto-context is not enabled here: .auto-context/config.yaml or context/_root.md exists but is not used (it links outside the repository, is not a regular file, or cannot be inspected)"),
		Session: fixedText(config.FileName + " or context/_root.md links outside the repository, is not a regular file or cannot be inspected, so the plugin is off here."),
	}},
	ProblemStateDirOutside: {SeverityWarning, map[Channel]textFunc{
		CLI: fixedText(fsutil.StateDir + " leads outside the repository, so the hooks are disabled"),
	}},
	ProblemConfigUnreadable: {SeverityWarning, map[Channel]textFunc{
		CLI: func(pr Problem, _ Render) string {
			return fmt.Sprintf("config: %s cannot be used: %s (using defaults; docs tracking is paused until it is fixed)", config.FileName, config.Describe(pr.Err))
		},
		Session: func(_ Problem, r Render) string {
			return fmt.Sprintf("%s could not be read or parsed, so docs tracking is paused; run `%s status` for the error and fix the file.", config.FileName, r.Command)
		},
		Stop: func(_ Problem, r Render) string {
			return config.FileName + " cannot be read or parsed, so docs tracking is paused until it is fixed; run `" + r.Command + " status` for the error. Changes made meanwhile are checked once it is fixed."
		},
	}},
	ProblemConfigInvalid: {SeverityWarning, map[Channel]textFunc{
		CLI:     configInvalidCLI,
		Session: configInvalidSession,
	}},
	// Told as ignored settings, like ProblemConfigInvalid; SessionStart adds
	// what it pauses with ProblemIndexPaused, from the index update.
	ProblemConfigDegraded: {SeverityWarning, map[Channel]textFunc{
		CLI:     configInvalidCLI,
		Session: configInvalidSession,
	}},
	ProblemConfigIgnoreUnknown: {SeverityWarning, map[Channel]textFunc{
		CLI: func(pr Problem, _ Render) string {
			return fmt.Sprintf("git could not tell whether %s is ignored (%s)", config.FileName, gitx.Brief(pr.Err))
		},
		Session: fixedText("git could not tell whether " + config.FileName + " is ignored (so whether the settings are committed); ask the user to check .auto-context/log."),
	}},
	ProblemConfigIgnored: {SeverityWarning, map[Channel]textFunc{
		CLI:     fixedText(config.FileName + " is ignored by git, so the settings are not committed"),
		Session: fixedText(config.FileName + " is ignored by git (e.g. the repository's .gitignore lists .auto-context/), so the settings are not committed; un-ignore that file."),
	}},
	ProblemDiscoverWarning: {SeverityWarning, map[Channel]textFunc{
		// Discovery warnings are fixed texts (units never quotes a path in them).
		CLI: func(pr Problem, _ Render) string { return pr.Err.Error() },
		Session: func(pr Problem, r Render) string {
			return fmt.Sprintf("%d discovery warning(s) (e.g. a skipped .gitignore or a git failure, which change what is tracked); run `%s status` to see them.", pr.N, r.Command)
		},
	}},
	ProblemCacheUnusable: {SeverityWarning, map[Channel]textFunc{
		CLI: func(pr Problem, r Render) string {
			return "the hash cache cannot be used, so the files are hashed again: " + r.ErrText(pr.Err, "hash cache")
		},
		Session: func(_ Problem, r Render) string {
			return fmt.Sprintf("the hash cache was unusable and is rebuilt (this session hashes everything once); see `%s status`.", r.Command)
		},
	}},
	ProblemCacheSave: {SeverityWarning, map[Channel]textFunc{
		CLI: func(pr Problem, r Render) string {
			return "the hash cache cannot be saved: " + r.ErrText(pr.Err, "saving the hash cache")
		},
		Session: fixedText("the hash cache cannot be saved, so every hook hashes again; ask the user to check .auto-context/log."),
	}},
	ProblemIndexMarkers: {SeverityWarning, map[Channel]textFunc{
		CLI: func(_ Problem, r Render) string {
			return fmt.Sprintf("the auto-context:units markers in the root %s are unbalanced or repeated, so the units index is not updated", r.IndexFile)
		},
		Session: func(_ Problem, r Render) string {
			return "the auto-context:units markers in the root " + r.IndexFile + " are unbalanced or repeated, so the units index is not updated; restore or remove the marker lines (not the list)."
		},
	}},
	ProblemIndexUnusable: {SeverityWarning, map[Channel]textFunc{
		CLI: func(pr Problem, r Render) string {
			return fmt.Sprintf("the root %s cannot be used for the units index: %s", r.IndexFile, r.ErrText(pr.Err, "root index"))
		},
		Session: func(pr Problem, r Render) string {
			if errors.Is(pr.Err, fsutil.ErrOutside) {
				return "the root " + r.IndexFile + " " + docstate.OutsideText + ", so the units index is not updated."
			}
			return "the units index in the root " + r.IndexFile + " could not be updated; ask the user to check .auto-context/log."
		},
	}},
	ProblemIndexPaused: {SeverityWarning, map[Channel]textFunc{
		Session: func(_ Problem, r Render) string {
			return fmt.Sprintf("%s has invalid settings that decide the units, so the units index and auto-ack are paused until it is fixed; run `%s status` to see them.", config.FileName, r.Command)
		},
	}},
	ProblemLogUnreadable: {SeverityNote, map[Channel]textFunc{
		CLI: func(pr Problem, _ Render) string {
			return ".auto-context/log could not be read: " + CauseOf(pr.Err).Text(CLI)
		},
		Session: func(_ Problem, r Render) string {
			return fmt.Sprintf(".auto-context/log cannot be read, so hook errors go unreported; ask the user to fix or delete it (`%s status` shows the error).", r.Command)
		},
	}},
	ProblemLogUnwritable: {SeverityWarning, map[Channel]textFunc{
		CLI: func(pr Problem, _ Render) string {
			return ".auto-context/log cannot be written, so hook errors are lost: " + CauseOf(pr.Err).Text(CLI)
		},
		Session: fixedText(".auto-context/log cannot be written, so hook errors are lost; ask the user to fix its permissions."),
	}},
	ProblemLogErrors: {SeverityNote, map[Channel]textFunc{
		CLI: func(pr Problem, _ Render) string {
			return fmt.Sprintf("%d error(s) in .auto-context/log (ask the user to check it; do not read it yourself; delete it and log.1 once dealt with)", pr.N)
		},
		Session: func(pr Problem, r Render) string {
			return fmt.Sprintf("%d hook error(s) since the last session; run `%s status` and ask the user to check .auto-context/log (do not read it yourself: it may come from the repository).", pr.N, r.Command)
		},
	}},
}

func configInvalidCLI(pr Problem, _ Render) string {
	return fmt.Sprintf("%v (the other settings apply)", pr.Err)
}

func configInvalidSession(pr Problem, r Render) string {
	return fmt.Sprintf("%s has %d invalid setting(s) that were ignored (the other settings apply); run `%s status` to see them and fix the file.", config.FileName, pr.N, r.Command)
}

// Cause is the fixed category of an error, the only form in which an error
// reaches the user or Claude: the error's text holds repository paths. It is
// the error → category mapping of the hooks and the CLI (units and config
// keep their own for discovery warnings and config errors).
type Cause int

const (
	// CauseOther: anything else; the text points to the log.
	CauseOther Cause = iota
	// CauseOutside: a path leads outside the repository or into .git (fsutil.ErrOutside).
	CauseOutside
	// CauseNotRegular: not a regular file (fsutil.ErrNotRegular).
	CauseNotRegular
	// CauseTooLarge: a file over its read limit (fsutil.ErrTooLarge).
	CauseTooLarge
	// CausePermission: permission denied (os.ErrPermission).
	CausePermission
	// CauseMissing: a file does not exist (os.ErrNotExist).
	CauseMissing
	// CauseConfigBroken: ErrConfigBroken.
	CauseConfigBroken
	// CauseConfigDegraded: ErrConfigDegraded.
	CauseConfigDegraded
	// CauseIndexMarkers: rootindex.ErrBrokenMarkers.
	CauseIndexMarkers
	// CauseTimeUp: discovery stopped by its context (units.ErrTimeUp).
	CauseTimeUp
	// CauseGitSlow: git timed out or listed too much (units.ErrGitTimeout,
	// units.ErrListTooLarge).
	CauseGitSlow
	// CauseInterrupted is a CLI command interrupted (Ctrl-C): CauseOf cannot
	// tell it from the errors discovery returns, the caller sets it from its
	// context.
	CauseInterrupted
)

// CauseOf classifies err.
func CauseOf(err error) Cause {
	switch {
	case errors.Is(err, ErrConfigBroken):
		return CauseConfigBroken
	case errors.Is(err, ErrConfigDegraded):
		return CauseConfigDegraded
	case errors.Is(err, rootindex.ErrBrokenMarkers):
		return CauseIndexMarkers
	case errors.Is(err, units.ErrTimeUp):
		return CauseTimeUp
	case errors.Is(err, units.ErrGitTimeout), errors.Is(err, units.ErrListTooLarge):
		return CauseGitSlow
	case errors.Is(err, fsutil.ErrOutside):
		return CauseOutside
	case errors.Is(err, fsutil.ErrNotRegular):
		return CauseNotRegular
	case errors.Is(err, fsutil.ErrTooLarge):
		return CauseTooLarge
	case errors.Is(err, os.ErrPermission):
		return CausePermission
	case errors.Is(err, os.ErrNotExist):
		return CauseMissing
	}
	return CauseOther
}

// CauseOfCtx is CauseOf, but CauseInterrupted when ctx was canceled (a CLI
// interrupted while discovery or git ran).
func CauseOfCtx(ctx context.Context, err error) Cause {
	if errors.Is(ctx.Err(), context.Canceled) {
		return CauseInterrupted
	}
	return CauseOf(err)
}

// fixed reports a cause whose text says all: the CLI logs no detail for it.
func (c Cause) fixed() bool {
	return c == CauseConfigBroken || c == CauseConfigDegraded || c == CauseIndexMarkers
}

// causeTexts is the cause → text table: the CLI's terse categories (for
// the file errors fsutil.Category's own texts, taken from it), and the
// hooks' (SessionStart and Stop) sentences for Claude.
var causeTexts = map[Cause][2]string{
	CauseOther:          {fsutil.Category(nil), "see .auto-context/log"},
	CauseOutside:        {fsutil.Category(fsutil.ErrOutside), "a file links outside the repository"},
	CauseNotRegular:     {fsutil.Category(fsutil.ErrNotRegular), "a source or doc is not a regular file"},
	CauseTooLarge:       {fsutil.Category(fsutil.ErrTooLarge), "the context doc is too large"},
	CausePermission:     {fsutil.Category(os.ErrPermission), "permission denied"},
	CauseMissing:        {fsutil.Category(os.ErrNotExist), "a file disappeared"},
	CauseConfigBroken:   {ErrConfigBroken.Error(), ErrConfigBroken.Error()},
	CauseConfigDegraded: {ErrConfigDegraded.Error(), ErrConfigDegraded.Error()},
	CauseIndexMarkers:   {rootindex.ErrBrokenMarkers.Error(), "see .auto-context/log"},
	CauseTimeUp:         {"listing the files stopped before it finished; the repository may be too large or slow (add big generated directories to .gitignore)", "the hook's time ran out; the repository may be too large or slow"},
	CauseGitSlow:        {"git is too slow or lists too many files; add big generated directories to .gitignore", "git is too slow or lists too many files; add big generated directories to .gitignore"},
	CauseInterrupted:    {"interrupted", "interrupted"},
}

// Text is c as channel ch shows it.
func (c Cause) Text(ch Channel) string {
	t := causeTexts[c]
	if ch == CLI {
		return t[0]
	}
	return t[1]
}
