// Command auto-context wires the hooks and maintenance commands.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"text/tabwriter"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/gitx"
	"auto-context/internal/hooks"
	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/rootindex"
	"auto-context/internal/textx"
	"auto-context/internal/units"
)

var version = "dev"

const usage = `usage: auto-context <command>

commands:
  hook <session-start|prompt-submit|stop|pre-tool-use|session-end>
  ack <unit>...
  ack --exclude <unit>
  status [--stale] [--exit-code]
  changes <unit>
  units
  version
`

func main() {
	os.Exit(start(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getwd))
}

func start(args []string, stdin io.Reader, stdout, stderr io.Writer, getwd func() (string, error)) int {
	cwd, err := getwd()
	if err != nil {
		if len(args) > 0 && args[0] == "hook" {
			// Hooks always exit 0. The project dir, if Claude Code passed it,
			// still gives a root to work in and to log to.
			if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" {
				return run(args, stdin, stdout, stderr, d)
			}
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	return run(args, stdin, stdout, stderr, cwd)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "hook":
		if len(rest) != 1 {
			logHook(config.FindRoot(cwd), "hook: want 1 argument, got %d", len(rest))
			return 0
		}
		return runHook(rest[0], stdin, stdout, cwd)
	case "ack":
		return runAck(rest, stdout, stderr, cwd)
	case "status":
		return runStatus(rest, stdout, stderr, cwd)
	case "units":
		return runUnits(stdout, stderr, cwd)
	case "changes":
		return runChanges(rest, stdout, stderr, cwd)
	case "hook-exit":
		// Internal, called by the shim: the binary died without recovering
		// (a Go fatal error, a kill). Record it so it is not lost.
		if len(rest) == 2 {
			logHook(hookRoot(hooks.Input{}, cwd), "hook %s: binary exited with status %s", rest[0], rest[1])
		}
		return 0
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	}
	fmt.Fprint(stderr, usage)
	return 2
}

// hookHandlers maps `auto-context hook <event>` to its handler.
var hookHandlers = map[string]func(hooks.Env, hooks.Input) ([]byte, error){
	"session-start": hooks.SessionStart,
	"prompt-submit": hooks.PromptSubmit,
	"stop":          hooks.Stop,
	"pre-tool-use":  hooks.PreToolUse,
	"session-end":   hooks.SessionEnd,
}

// hookBudget is how long each hook may run git in total: a few seconds less
// than its timeout in hooks/hooks.json, so it still answers in time.
var hookBudget = map[string]time.Duration{
	"session-start": 55 * time.Second,
	"prompt-submit": 25 * time.Second,
	"stop":          55 * time.Second,
	"pre-tool-use":  8 * time.Second,
	"session-end":   8 * time.Second,
}

// maxHookInput caps the hook payload read from stdin.
const maxHookInput = 1 << 20

// hookRoot picks the project root for a hook: the project dir Claude Code
// passes (stable for the whole session), else the payload's cwd, which can
// drift into a nested repo after a `cd`, else the process cwd.
func hookRoot(in hooks.Input, cwd string) string {
	dir := cwd
	if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" {
		dir = d
	} else if in.Cwd != "" {
		dir = in.Cwd
	}
	// fsutil refuses relative paths; Claude Code passes absolute ones.
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return config.FindRoot(dir)
}

// runHook never fails: errors and panics go to the log, the exit code is 0.
func runHook(event string, stdin io.Reader, stdout io.Writer, cwd string) int {
	// A hook is short-lived: trade some memory for less GC time, within a
	// bound for very large repositories.
	debug.SetGCPercent(400)
	debug.SetMemoryLimit(512 << 20)
	if b, ok := hookBudget[event]; ok {
		gitx.SetDeadline(time.Now().Add(b))
		defer gitx.SetDeadline(time.Time{}) // in-process callers (tests) go on without it
	}
	var in hooks.Input
	data, readErr := io.ReadAll(io.LimitReader(stdin, maxHookInput+1))
	parseErr := readErr
	if parseErr == nil && len(data) > maxHookInput {
		parseErr = fmt.Errorf("input larger than %d bytes", maxHookInput)
	}
	if parseErr == nil {
		parseErr = json.Unmarshal(data, &in)
	}
	root := hookRoot(in, cwd)
	var out []byte
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
			}
		}()
		if parseErr != nil {
			return fmt.Errorf("bad input: %w", parseErr)
		}
		h, ok := hookHandlers[event]
		if !ok {
			return fmt.Errorf("unknown event")
		}
		out, err = h(hooks.Env{Root: root, Now: time.Now}, in)
		return err
	}()
	if err != nil {
		logHook(root, "hook %s: %v", event, err)
		if event == "session-start" && project.Active(root) {
			// Say that tracking is off rather than go silent.
			out = hooks.SessionFailure()
		} else {
			return 0
		}
	}
	if len(out) > 0 {
		if _, err := stdout.Write(append(out, '\n')); err != nil {
			logHook(root, "hook %s: writing the answer: %v", event, err)
		}
	}
	return 0
}

// logHook logs a hook error unless the project is inactive (inactive projects are never written to).
func logHook(root, format string, args ...any) {
	if project.Active(root) {
		logx.Errorf(root, format, args...)
	}
}

// loadRepo finds the root and opens the project for CLI commands, printing
// config problems and warnings to w (stderr, or stdout for status).
func loadRepo(w io.Writer, cwd string) (*project.Project, error) {
	p, err := project.Open(config.FindRoot(cwd))
	if err != nil {
		return nil, err
	}
	if err := p.StateDirErr(); err != nil {
		fmt.Fprintf(w, "warning: %v\n", err)
	}
	switch {
	case p.ConfigErr() != nil:
		fmt.Fprintf(w, "warning: config: %s cannot be used: %s (using defaults; docs tracking is paused until it is fixed)\n", config.FileName, config.Describe(p.ConfigErr()))
	case p.ConfigProblems() != nil:
		fmt.Fprintf(w, "warning: %v (the other settings apply)\n", p.ConfigProblems())
	}
	if err := p.CacheErr(); err != nil {
		fmt.Fprintf(w, "warning: %v\n", err)
	}
	if ignored, err := p.ConfigIgnored(); err != nil {
		fmt.Fprintf(w, "warning: git could not tell whether %s is ignored (%s)\n", config.FileName, gitx.Brief(err))
	} else if ignored {
		fmt.Fprintf(w, "warning: %s is ignored by git, so the settings are not committed\n", config.FileName)
	}
	for _, msg := range p.Warnings() {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
	return p, nil
}

// saveCache writes the hash cache (only in an active project) and reports a
// failure on stderr.
func saveCache(p *project.Project, stderr io.Writer) {
	if err := p.SaveCache(); err != nil {
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
}

func normUnit(arg string) string {
	u := path.Clean(filepath.ToSlash(arg))
	if u == "." {
		return units.RootUnit
	}
	return u
}

func runAck(args []string, stdout, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if args[0] == "--exclude" {
		if len(args) != 2 {
			fmt.Fprint(stderr, usage)
			return 2
		}
		p, err := loadRepo(stderr, cwd)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := p.Exclude(normUnit(args[1])); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	p, err := loadRepo(stderr, cwd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if p.ConfigBroken() {
		// The units come from defaults: a hash over them would be wrong.
		fmt.Fprintln(stderr, project.ErrConfigBroken)
		return 1
	}
	code := 0
	for _, a := range args {
		if err := p.Ack(normUnit(a), time.Now()); err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
		}
	}
	if err := p.RefreshIndex(); err != nil {
		fmt.Fprintf(stderr, "warning: root index: %v\n", err)
	}
	saveCache(p, stderr)
	return code
}

func runStatus(args []string, stdout, stderr io.Writer, cwd string) int {
	stale, exitCode := false, false
	for _, a := range args {
		switch a {
		case "--stale":
			stale = true
		case "--exit-code":
			exitCode = true
		default:
			fmt.Fprint(stderr, usage)
			return 2
		}
	}
	// Warnings go to stdout: status is a report, and the sync skill's `!`
	// injection may not show stderr.
	p, err := loadRepo(stdout, cwd)
	if err != nil {
		fmt.Fprintln(stdout, "error:", err)
		return 1
	}
	root := p.Root()
	if p.ConfigBroken() {
		// The units would be the defaults: no table. With --exit-code it
		// fails: the docs state is unknown.
		fmt.Fprintf(stdout, "docs tracking is paused until %s is fixed (see the warning above); no table is printed\n", config.FileName)
		printLogNote(stdout, root)
		if exitCode {
			return 1
		}
		return 0
	}
	sts := p.Statuses()
	saveCache(p, stdout)
	tw := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "UNIT\tSTATE\tDOC")
	n, outdated := 0, false
	for _, s := range sts {
		bad := s.State == docstate.Stale || s.State == docstate.Missing
		outdated = outdated || bad
		if stale && !bad {
			continue
		}
		state := string(s.State)
		if s.Note != "" {
			state += " (" + s.NoteText() + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Unit, state, s.ContextPath)
		n++
	}
	if n == 0 {
		fmt.Fprintln(stdout, "all docs up to date")
	} else {
		tw.Flush()
	}
	shown, hidden := project.ShowNames(p.Orphans())
	for _, o := range shown {
		fmt.Fprintf(stdout, "note: %s belongs to no unit (its directory was removed or no longer qualifies)\n", o)
	}
	if hidden > 0 {
		fmt.Fprintf(stdout, "note: %d more file(s) under context/ belong to no unit (names not shown: too many or unusual characters)\n", hidden)
	}
	switch err := rootindex.Check(root); {
	case errors.Is(err, rootindex.ErrBrokenMarkers):
		fmt.Fprintln(stdout, "warning: the auto-context:units markers in the root CLAUDE.md are unbalanced, so the units index is not updated")
	case err != nil:
		fmt.Fprintf(stdout, "warning: the root CLAUDE.md cannot be used for the units index: %s\n", textx.Err(err))
	}
	if lang := p.Config().Language; lang != "" {
		fmt.Fprintf(stdout, "note: write unit docs in language %s (language in %s)\n", lang, config.FileName)
	}
	printLogNote(stdout, root)
	if exitCode && outdated {
		return 1
	}
	return 0
}

// printLogNote surfaces hook errors, which are otherwise only in the log
// (log and log.1). It prints their number only: the file may come from the
// repo itself, so its text is never echoed into Claude's context.
func printLogNote(stdout io.Writer, root string) {
	entries, err := logx.Entries(root)
	if err != nil {
		fmt.Fprintf(stdout, "note: .auto-context/log could not be read: %s\n", textx.Err(err))
		return
	}
	if n := len(entries); n > 0 {
		fmt.Fprintf(stdout, "note: %d error(s) in .auto-context/log (ask the user to check it; do not read it yourself; delete it and log.1 once dealt with)\n", n)
	}
	if !project.Active(root) {
		return
	}
	if err := logx.Writable(root); err != nil {
		fmt.Fprintf(stdout, "warning: .auto-context/log cannot be written, so hook errors are lost: %s\n", textx.Err(err))
	}
}

func runUnits(stdout, stderr io.Writer, cwd string) int {
	p, err := loadRepo(stderr, cwd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if p.ConfigBroken() {
		fmt.Fprintln(stderr, project.ErrConfigBroken) // the units would be the defaults
		return 1
	}
	for _, u := range p.Tree().Units() {
		fmt.Fprintf(stdout, "%s\t%s\t%d\n", u.Path, u.Reason, u.FileCount)
	}
	return 0
}
