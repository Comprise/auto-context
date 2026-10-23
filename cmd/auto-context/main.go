// Command auto-context wires the hooks and maintenance commands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/fsutil"
	"auto-context/internal/gitx"
	"auto-context/internal/hooks"
	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/rootindex"
	"auto-context/internal/units"
)

var version = "dev"

const usage = `usage: auto-context <command>

commands:
  hook <session-start|prompt-submit|stop|pre-tool-use|session-end> [--agent codex]
  hook permission-request --agent codex
  ack <unit>...
  ack --exclude <unit>
  ack --upgrade
  status [--stale] [--exit-code] [--json]
  changes <unit>
  units [--json]
  version [-v]

exit codes: 0 success, 1 stale or missing docs (status --exit-code), 2 usage, 3 error
`

// Exit codes of the CLI; hooks always exit 0.
const (
	exitOK    = 0
	exitStale = 1 // status --exit-code only
	exitUsage = 2
	exitError = 3
)

// formatVersion is the version of the --json output formats.
const formatVersion = 1

// On-disk formats named by `version -v`: copies of the packages' constants
// (hashing.cacheVersion, the session snapshot, session.journalVersion,
// docstate.HashPrefix), kept in step by TestVersionVerbose.
const formatsText = "cache 4\nsnapshot 1\njournal 1\nhash v2\n"

func main() {
	os.Exit(start(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getwd))
}

func start(args []string, stdin io.Reader, stdout, stderr io.Writer, getwd func() (string, error)) int {
	cwd, err := getwd()
	if err != nil {
		if len(args) > 0 && args[0] == "hook" {
			// Hooks always exit 0. The payload's cwd or, for Claude, the
			// project dir still gives a root to work in and to log to.
			dir := ""
			if _, a, ok := parseHookArgs(args[1:]); ok && a != hooks.Codex {
				dir = os.Getenv("CLAUDE_PROJECT_DIR")
			}
			return run(args, stdin, stdout, stderr, dir)
		}
		fmt.Fprintln(stderr, "error: the current directory cannot be read")
		return exitError
	}
	return run(args, stdin, stdout, stderr, cwd)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	rest := args[1:]
	switch args[0] {
	case "hook":
		event, agent, ok := parseHookArgs(rest)
		if !ok {
			logHook(hookRoot(hooks.Input{}, cwd, agent), "hook: bad arguments")
			return 0
		}
		return runHook(event, agent, stdin, stdout, cwd)
	case "ack", "status", "units", "changes":
		// An interrupt ends the command's git calls and hashing; a second
		// one, after stop, kills the process as usual.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		cmd := map[string]func(context.Context, []string, io.Writer, io.Writer, string) int{
			"ack": runAck, "status": runStatus, "units": runUnits, "changes": runChanges,
		}[args[0]]
		return cmd(ctx, rest, stdout, stderr, cwd)
	case "hook-exit":
		// Internal, called by the shim only (not in the usage): the binary
		// died without recovering (a Go fatal error, a kill). Record it so it
		// is not lost.
		if len(rest) == 2 {
			logHook(hookRoot(hooks.Input{}, cwd, hooks.Claude), "hook %s: binary exited with status %s", rest[0], rest[1])
		}
		return 0
	case "version":
		switch {
		case len(rest) == 0:
			fmt.Fprintln(stdout, versionString())
			return exitOK
		case len(rest) == 1 && rest[0] == "-v":
			fmt.Fprint(stdout, versionVerbose())
			return exitOK
		}
	}
	fmt.Fprint(stderr, usage)
	return exitUsage
}

// versionString is the version stamped by the build (-X main.version), else
// the module version Go records for `go install ...@vX`, else "dev".
func versionString() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

// versionVerbose adds the commit, the toolchain, the platform and the
// on-disk formats, for bug reports.
func versionVerbose() string {
	rev, modified := "unknown", false
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	if modified {
		rev += " (modified)"
	}
	return fmt.Sprintf("auto-context %s\nrevision %s\ngo %s\nplatform %s/%s\n%s",
		versionString(), rev, runtime.Version(), runtime.GOOS, runtime.GOARCH, formatsText)
}

// hookSpec is one `auto-context hook <event>`: its handler and how long it
// may run git and hash in total (Env.Ctx), a few seconds less than its
// timeout in hooks/hooks.json and hooks/codex.json, so it still answers in
// time.
type hookSpec struct {
	handler     func(hooks.Env, hooks.Input) ([]byte, error)
	budget      time.Duration
	codexBudget time.Duration // Codex's budget when shorter (0: budget)
	codexOnly   bool
}

var hookTable = map[string]hookSpec{
	"session-start":      {handler: hooks.SessionStart, budget: 55 * time.Second},
	"prompt-submit":      {handler: hooks.PromptSubmit, budget: 25 * time.Second},
	"stop":               {handler: hooks.Stop, budget: 55 * time.Second},
	"pre-tool-use":       {handler: hooks.PreToolUse, budget: 8 * time.Second},
	"session-end":        {handler: hooks.SessionEnd, budget: 8 * time.Second, codexBudget: 2 * time.Second},
	"permission-request": {handler: hooks.PermissionRequest, budget: 8 * time.Second, codexOnly: true},
}

// budgetFor is the budget (git and hashing) of an event for an agent.
func budgetFor(event string, a hooks.Agent) (time.Duration, bool) {
	s, ok := hookTable[event]
	if !ok {
		return 0, false
	}
	if a == hooks.Codex && s.codexBudget != 0 {
		return s.codexBudget, true
	}
	return s.budget, true
}

// parseHookArgs parses `hook <event> [--agent <agent>]`: a known event, an
// optional known agent, and permission-request only for Codex. On failure the
// agent is still returned when it was valid, so the error is logged in the
// right root.
func parseHookArgs(rest []string) (event string, a hooks.Agent, ok bool) {
	a = hooks.Claude
	switch {
	case len(rest) == 1:
	case len(rest) == 3 && rest[1] == "--agent":
		if a, ok = hooks.ParseAgent(rest[2]); !ok {
			return "", hooks.Claude, false
		}
	default:
		return "", a, false
	}
	s, known := hookTable[rest[0]]
	if !known || (s.codexOnly && a != hooks.Codex) {
		return "", a, false
	}
	return rest[0], a, true
}

// maxHookInput caps the hook payload read from stdin.
const maxHookInput = 1 << 20

// hookRoot picks the project root for a hook. For Claude it is the project
// dir Claude Code passes (stable for the whole session); Codex does not set
// it, and one inherited from a Claude session names another project. Else the
// payload's cwd, which can drift into a nested repo after a `cd`, else the
// process cwd. "" when none is an absolute path.
func hookRoot(in hooks.Input, cwd string, a hooks.Agent) string {
	dir := cwd
	if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" && a != hooks.Codex {
		dir = d
	} else if in.Cwd != "" {
		dir = in.Cwd
	}
	if dir == "" {
		return ""
	}
	// fsutil refuses relative paths; the agents pass absolute ones.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return project.FindRoot(abs)
}

// pluginRoot is the plugin's install directory the agent passes: Codex sets
// PLUGIN_ROOT (and CLAUDE_PLUGIN_ROOT for compatibility), Claude Code
// CLAUDE_PLUGIN_ROOT.
func pluginRoot(a hooks.Agent) string {
	if a == hooks.Codex {
		if r := os.Getenv("PLUGIN_ROOT"); r != "" {
			return r
		}
	}
	return os.Getenv("CLAUDE_PLUGIN_ROOT")
}

// runHook never fails: errors and panics go to the log, the exit code is 0.
func runHook(event string, agent hooks.Agent, stdin io.Reader, stdout io.Writer, cwd string) int {
	spec := hookTable[event]
	// A hook is short-lived: trade some memory for less GC time, within a
	// bound for very large repositories.
	debug.SetGCPercent(400)
	debug.SetMemoryLimit(512 << 20)
	ctx := context.Background()
	if b, ok := budgetFor(event, agent); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b)
		defer cancel()
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
	root := hookRoot(in, cwd, agent)
	if root == "" {
		return 0 // nowhere to work or to log
	}
	var out []byte
	mismatch := false
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
			}
		}()
		if parseErr != nil {
			return fmt.Errorf("bad input: %w", parseErr)
		}
		if err := hooks.CheckEvent(event, in); err != nil {
			// A manifest that calls the wrong subcommand: answer nothing
			// rather than the wrong thing.
			mismatch = true
			return err
		}
		env := hooks.Env{Root: root, Now: time.Now, Agent: agent, PluginRoot: pluginRoot(agent), Ctx: ctx}
		out, err = spec.handler(env, in)
		return err
	}()
	if err != nil {
		logHook(root, "hook %s: %v", event, err)
		if event == "session-start" && !mismatch && project.Active(root) {
			// Say that tracking is off rather than go silent.
			out = hooks.SessionFailure(agent)
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

// logHook logs an error unless the project is inactive (inactive projects
// are never written to) or there is no root.
func logHook(root, format string, args ...any) {
	if root != "" && project.Active(root) {
		logx.Errorf(root, format, args...)
	}
}

// cliRoot is the project root of a CLI command: the project dir Claude Code
// passes when cwd is inside it, so a command run after a `cd` into a nested
// repository acts on the project the hooks use; else the root found from cwd.
func cliRoot(cwd string) string {
	if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" && filepath.IsAbs(d) && inside(d, cwd) {
		return project.FindRoot(filepath.Clean(d))
	}
	return project.FindRoot(cwd)
}

// inside reports whether p is dir or below it, comparing the paths as given
// and with symlinks resolved.
func inside(dir, p string) bool {
	below := func(a, b string) bool {
		rel, err := filepath.Rel(a, b)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	if below(filepath.Clean(dir), filepath.Clean(p)) {
		return true
	}
	rd, err1 := filepath.EvalSymlinks(dir)
	rp, err2 := filepath.EvalSymlinks(p)
	return err1 == nil && err2 == nil && below(rd, rp)
}

// detail logs err's full text (it may quote repository paths) and returns
// the suffix that points to it; the log is only written in active projects.
func detail(root string, err error, what string) string {
	if !project.Active(root) {
		return ""
	}
	logx.Errorf(root, "%s: %v", what, err)
	return " (details in .auto-context/log)"
}

// errText is how an error of a CLI operation is shown: the fixed texts of
// the project sentinels as they are, anything else as a fixed category, with
// the full text in the log.
func errText(root string, err error, what string) string {
	switch {
	case errors.Is(err, project.ErrConfigBroken):
		return project.ErrConfigBroken.Error()
	case errors.Is(err, project.ErrConfigDegraded):
		return project.ErrConfigDegraded.Error()
	case errors.Is(err, rootindex.ErrBrokenMarkers):
		return rootindex.ErrBrokenMarkers.Error()
	}
	return fsutil.Category(err) + detail(root, err, what)
}

// shownName is a name from the command line or the repo as it may be echoed.
func shownName(name string) string {
	if units.SafeName(name) {
		return name
	}
	return "(name not shown)"
}

// ackErrText explains why unit could not be acked, without the error's text.
func ackErrText(root, unit string, err error) string {
	switch {
	case errors.Is(err, project.ErrConfigBroken), errors.Is(err, project.ErrConfigDegraded):
		return errText(root, err, "ack")
	case errors.Is(err, units.ErrUnknownUnit):
		return "unknown unit " + shownName(unit)
	}
	name := shownName(unit)
	if kind, _, _ := docstate.Doc(root, unit); kind == docstate.DocAbsent && units.SafeName(unit) {
		if unit == units.RootUnit {
			return "no context/_root.md: create it (the root unit cannot be excluded)"
		}
		return fmt.Sprintf("no context/%s.md: create docs or run auto-context ack --exclude %s", unit, unit)
	}
	return name + ": " + errText(root, err, "ack "+unit)
}

// loadRepo finds the root and opens the project for CLI commands, returning
// the warnings about it; none quotes repository text. The hash cache is not
// loaded: only status and ack need it (see cacheWarning).
func loadRepo(ctx context.Context, cwd string) (*project.Project, []string, error) {
	p, err := project.Open(ctx, cliRoot(cwd))
	if err != nil {
		return nil, nil, err
	}
	var warns []string
	if p.StateDirErr() != nil {
		warns = append(warns, fsutil.StateDir+" leads outside the repository, so the hooks are disabled")
	}
	switch {
	case p.ConfigErr() != nil:
		warns = append(warns, fmt.Sprintf("config: %s cannot be used: %s (using defaults; docs tracking is paused until it is fixed)", config.FileName, config.Describe(p.ConfigErr())))
	case p.ConfigProblems() != nil:
		warns = append(warns, fmt.Sprintf("%v (the other settings apply)", p.ConfigProblems()))
	}
	if ignored, err := p.ConfigIgnored(); err != nil {
		warns = append(warns, fmt.Sprintf("git could not tell whether %s is ignored (%s)", config.FileName, gitx.Brief(err)))
	} else if ignored {
		warns = append(warns, config.FileName+" is ignored by git, so the settings are not committed")
	}
	return p, append(warns, p.Warnings()...), nil
}

// openErr reports a project that could not be opened.
func openErr(w io.Writer, cwd string, err error) int {
	root := cliRoot(cwd)
	fmt.Fprintf(w, "error: the project cannot be opened: %s\n", fsutil.Category(err)+detail(root, err, "open"))
	return exitError
}

func printWarnings(w io.Writer, warns []string) {
	for _, msg := range warns {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

// cacheWarning explains a hash cache that could not be used (loading it).
func cacheWarning(p *project.Project) []string {
	if err := p.CacheErr(); err != nil {
		return []string{"the hash cache cannot be used, so the files are hashed again: " + errText(p.Root(), err, "hash cache")}
	}
	return nil
}

// saveCache writes the hash cache (only in an active project) and returns a
// warning on failure.
func saveCache(p *project.Project) []string {
	if err := p.SaveCache(); err != nil {
		return []string{"the hash cache cannot be saved: " + errText(p.Root(), err, "saving the hash cache")}
	}
	return nil
}

// indexWarning explains a root index that could not be updated.
func indexWarning(p *project.Project, err error) []string {
	if err == nil {
		return nil
	}
	return []string{"root index: " + errText(p.Root(), err, "root index")}
}

func normUnit(arg string) string {
	u := path.Clean(filepath.ToSlash(arg))
	if u == "." {
		return units.RootUnit
	}
	return u
}

func runAck(ctx context.Context, args []string, stdout, stderr io.Writer, cwd string) int {
	mode := ""
	if len(args) > 0 && (args[0] == "--exclude" || args[0] == "--upgrade") {
		mode, args = args[0], args[1:]
	}
	switch {
	case mode == "" && len(args) == 0,
		mode == "--exclude" && len(args) != 1,
		mode == "--upgrade" && len(args) != 0,
		slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-") }):
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	p, warns, err := loadRepo(ctx, cwd)
	if err != nil {
		return openErr(stderr, cwd, err)
	}
	printWarnings(stderr, warns)
	if p.ConfigBroken() {
		// The units come from defaults: a hash or an exclude over them would
		// be wrong.
		fmt.Fprintln(stderr, project.ErrConfigBroken)
		return exitError
	}
	switch mode {
	case "--exclude":
		return runExclude(p, normUnit(args[0]), stderr)
	case "--upgrade":
		return runUpgrade(p, stdout, stderr)
	}
	code, acked := exitOK, 0
	for _, a := range args {
		u := normUnit(a)
		if err := p.Ack(u, time.Now()); err != nil {
			fmt.Fprintln(stderr, ackErrText(p.Root(), u, err))
			code = exitError
		} else {
			acked++
		}
	}
	var after []string
	if acked > 0 {
		after = indexWarning(p, p.RefreshIndex())
	}
	after = append(after, cacheWarning(p)...)
	printWarnings(stderr, append(after, saveCache(p)...))
	return code
}

// runExclude excludes dir; Exclude reloads the project in place, so the
// index is rebuilt from the new units. It needs no hash cache.
func runExclude(p *project.Project, dir string, stderr io.Writer) int {
	if dir == units.RootUnit {
		fmt.Fprintln(stderr, "the root unit cannot be excluded; create context/_root.md instead")
		return exitError
	}
	if fi, err := os.Stat(filepath.Join(p.Root(), filepath.FromSlash(dir))); err != nil || !fi.IsDir() {
		fmt.Fprintf(stderr, "no directory %s in the project\n", shownName(dir))
		return exitError
	}
	if err := p.Exclude(dir); err != nil {
		if strings.Contains(err.Error(), "names are case-sensitive") {
			fmt.Fprintf(stderr, "no directory %s (names are case-sensitive)\n", shownName(dir))
		} else {
			fmt.Fprintf(stderr, "%s cannot be excluded: %s\n", shownName(dir), errText(p.Root(), err, "ack --exclude"))
		}
		return exitError
	}
	printWarnings(stderr, indexWarning(p, p.RefreshIndex()))
	return exitOK
}

// runUpgrade rewrites the hashes of the docs up to date by the old scheme in
// scheme v2.
func runUpgrade(p *project.Project, stdout, stderr io.Writer) int {
	acked, failed := p.Upgrade(time.Now())
	fmt.Fprintf(stdout, "upgraded %d unit(s) to hash scheme v2\n", len(acked))
	code := exitOK
	for _, u := range slices.Sorted(maps.Keys(failed)) {
		fmt.Fprintln(stderr, ackErrText(p.Root(), u, failed[u]))
		code = exitError
	}
	printWarnings(stderr, append(cacheWarning(p), saveCache(p)...))
	return code
}

// statusJSON is `status --json`, format_version 1.
type statusJSON struct {
	FormatVersion int              `json:"format_version"`
	Paused        bool             `json:"paused"`
	Units         []statusUnitJSON `json:"units"`
	Orphans       orphansJSON      `json:"orphans"`
	Warnings      []string         `json:"warnings"`
	Notes         []string         `json:"notes"`
}

type statusUnitJSON struct {
	Unit   string `json:"unit"`
	State  string `json:"state"`
	Doc    string `json:"doc"`
	Note   string `json:"note"`
	Detail string `json:"detail"`
}

type orphansJSON struct {
	Shown  []string `json:"shown"`
	Hidden int      `json:"hidden"`
}

func runStatus(ctx context.Context, args []string, stdout, stderr io.Writer, cwd string) int {
	stale, exitCode, asJSON := false, false, false
	for _, a := range args {
		switch a {
		case "--stale":
			stale = true
		case "--exit-code":
			exitCode = true
		case "--json":
			asJSON = true
		default:
			fmt.Fprint(stderr, usage)
			return exitUsage
		}
	}
	// Text goes to stdout, warnings included: status is a report, and the
	// sync skill's `!` injection may not show stderr. JSON output is only
	// the document, so an open error goes to stderr then.
	p, warns, err := loadRepo(ctx, cwd)
	if err != nil {
		if asJSON {
			return openErr(stderr, cwd, err)
		}
		return openErr(stdout, cwd, err)
	}
	root := p.Root()
	r := statusJSON{FormatVersion: formatVersion, Paused: p.ConfigBroken(), Units: []statusUnitJSON{}, Warnings: warns}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	var notes []string
	switch {
	case project.ActivationRefused(root):
		notes = append(notes, "auto-context is not enabled here: .auto-context/config.yaml or context/_root.md exists but is not used (it links outside the repository, is not a regular file, or cannot be inspected)")
	case !project.Active(root):
		notes = append(notes, "auto-context is not enabled here (no .auto-context/config.yaml or context/_root.md); run /auto-context:init to enable it")
	}
	outdated := false
	if !r.Paused {
		for _, s := range p.Statuses() {
			bad := s.State == docstate.Stale || s.State == docstate.Missing
			outdated = outdated || bad
			if stale && !bad {
				continue
			}
			r.Units = append(r.Units, statusUnitJSON{Unit: s.Unit, State: string(s.State), Doc: s.ContextPath, Note: string(s.Note), Detail: s.Detail})
		}
		r.Warnings = append(r.Warnings, cacheWarning(p)...)
		r.Warnings = append(r.Warnings, saveCache(p)...)
		shown, hidden := project.ShowNames(p.Orphans())
		r.Orphans = orphansJSON{Shown: append([]string{}, shown...), Hidden: hidden}
		name := p.IndexFile()
		switch err := rootindex.Check(root, name); {
		case errors.Is(err, rootindex.ErrBrokenMarkers):
			r.Warnings = append(r.Warnings, fmt.Sprintf("the auto-context:units markers in the root %s are unbalanced or repeated, so the units index is not updated", name))
		case err != nil:
			r.Warnings = append(r.Warnings, fmt.Sprintf("the root %s cannot be used for the units index: %s", name, errText(root, err, "root index")))
		}
		if lang := p.Config().Language; lang != "" {
			notes = append(notes, fmt.Sprintf("write unit docs in language %s (language in %s)", lang, config.FileName))
		}
	} else {
		r.Orphans.Shown = []string{}
	}
	logNotes, logWarns := logState(root)
	r.Notes = append(append([]string{}, notes...), logNotes...)
	r.Warnings = append(r.Warnings, logWarns...)

	if asJSON {
		data, _ := json.Marshal(r)
		fmt.Fprintf(stdout, "%s\n", data)
	} else {
		printStatus(stdout, r)
	}
	switch {
	case !exitCode:
		return exitOK
	case r.Paused || !project.Active(root):
		// The docs state is unknown: the units would be the defaults, or
		// the project does not use the plugin.
		return exitError
	case outdated:
		return exitStale
	}
	return exitOK
}

// printStatus renders the status report as text.
func printStatus(w io.Writer, r statusJSON) {
	printWarnings(w, r.Warnings)
	if r.Paused {
		fmt.Fprintf(w, "docs tracking is paused until %s is fixed (see the warning above); no table is printed\n", config.FileName)
	} else if len(r.Units) == 0 {
		fmt.Fprintln(w, "all docs up to date")
	} else {
		tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "UNIT\tSTATE\tDOC")
		for _, u := range r.Units {
			state := u.State
			if u.Note != "" {
				st := docstate.UnitStatus{Note: docstate.Note(u.Note), Detail: u.Detail}
				state += " (" + st.NoteText() + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", u.Unit, state, u.Doc)
		}
		tw.Flush()
	}
	for _, o := range r.Orphans.Shown {
		fmt.Fprintf(w, "note: %s belongs to no unit (its directory was removed or no longer qualifies)\n", o)
	}
	if r.Orphans.Hidden > 0 {
		fmt.Fprintf(w, "note: %d more file(s) under context/ belong to no unit (names not shown: too many or unusual characters)\n", r.Orphans.Hidden)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
}

// logState surfaces hook errors, which are otherwise only in the log (log
// and log.1). It gives their number only: the file may come from the repo
// itself, so its text is never echoed into Claude's context.
func logState(root string) (notes, warns []string) {
	entries, err := logx.Entries(root)
	if err != nil {
		return []string{".auto-context/log could not be read: " + fsutil.Category(err)}, nil
	}
	if n := len(entries); n > 0 {
		notes = append(notes, fmt.Sprintf("%d error(s) in .auto-context/log (ask the user to check it; do not read it yourself; delete it and log.1 once dealt with)", n))
	}
	if !project.Active(root) {
		return notes, nil
	}
	if err := logx.Writable(root); err != nil {
		warns = append(warns, ".auto-context/log cannot be written, so hook errors are lost: "+fsutil.Category(err))
	}
	return notes, warns
}

// unitsJSON is `units --json`, format_version 1.
type unitsJSON struct {
	FormatVersion int            `json:"format_version"`
	Units         []unitJSONItem `json:"units"`
}

type unitJSONItem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Files  int    `json:"files"`
}

func runUnits(ctx context.Context, args []string, stdout, stderr io.Writer, cwd string) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 0 && !asJSON {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	p, warns, err := loadRepo(ctx, cwd)
	if err != nil {
		return openErr(stderr, cwd, err)
	}
	printWarnings(stderr, warns)
	if p.ConfigBroken() {
		fmt.Fprintln(stderr, project.ErrConfigBroken) // the units would be the defaults
		return exitError
	}
	us := p.Tree().Units()
	if asJSON {
		r := unitsJSON{FormatVersion: formatVersion, Units: make([]unitJSONItem, 0, len(us))}
		for _, u := range us {
			r.Units = append(r.Units, unitJSONItem{Path: u.Path, Reason: string(u.Reason), Files: u.FileCount})
		}
		data, _ := json.Marshal(r)
		fmt.Fprintf(stdout, "%s\n", data)
		return exitOK
	}
	for _, u := range us {
		fmt.Fprintf(stdout, "%s\t%s\t%d\n", u.Path, u.Reason, u.FileCount)
	}
	return exitOK
}
