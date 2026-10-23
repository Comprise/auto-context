package project

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"auto-context/internal/docstate"
	"auto-context/internal/frontmatter"
	"auto-context/internal/fsutil"
	"auto-context/internal/gitx"
	"auto-context/internal/textx"
	"auto-context/internal/units"
)

// maxChangesOutput caps each git section WriteChanges prints.
const maxChangesOutput = 64 << 10

// WriteChanges writes what changed in unit since its docs were last acked:
// commits after the frontmatter's `commit` when HEAD descends from it, else
// commits since its `updated` date (the whole ack day included; by commit
// date, so an older commit merged later is missed, and the title says so)
// and, when every git filter is trusted, the uncommitted diff stat (if there
// is a HEAD) and `git status` (untracked and modified files). Git whose work
// tree or git dir lies outside the project (gitx.WorkTreeOK) is not used, and
// git errors are written as gitx.Brief only. It runs fixed git invocations itself, so
// the sync skill needs no git permissions (a pre-approved `git log *` would
// also allow `--output=<any file>`). Paths are relative to the root, which may
// be below the git top level. Git failures are written as notes, not returned.
func (p *Project) WriteChanges(w io.Writer, unit string) error {
	ew := &errWriter{w: w}
	if err := p.writeChanges(ew, unit); err != nil {
		return err
	}
	return ew.err
}

// errWriter remembers the first error of its writer and drops later writes.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(b []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(b)
	e.err = err
	return n, err
}

func (p *Project) writeChanges(w io.Writer, unit string) error {
	if p.ConfigBroken() {
		return ErrConfigBroken // the units would be the defaults
	}
	if !p.tree.IsUnit(unit) {
		return fmt.Errorf("unknown unit %s", unit)
	}
	root := p.root
	spec := "."
	if unit != units.RootUnit {
		spec = ":(literal)" + unit
	}
	ctx := units.ContextPath(unit)
	ack := ackPoint(root, ctx)

	// Git errors are written by gitx.Brief only: git's stderr may quote
	// values from the repo's config.
	switch ok, err := gitx.WorkTreeOK(root); {
	case gitx.NotRun(err):
		fmt.Fprintf(w, "(git unavailable: git could not be run; read the code against %s)\n", ctx)
		return nil
	case gitx.NotRepo(err):
		fmt.Fprintf(w, "(git unavailable: not a git repository; read the code against %s)\n", ctx)
		return nil
	case err != nil:
		fmt.Fprintf(w, "(git failed: %s; read the code against %s)\n", gitx.Brief(err), ctx)
		return nil
	case !ok:
		// core.worktree or a .git file sends git elsewhere: its file names
		// and commits are not this project's.
		fmt.Fprintf(w, "(git unavailable: the work tree or git dir lies outside the project; read the code against %s)\n", ctx)
		return nil
	}
	// `rev-parse --verify -q` exits 1 for a missing HEAD; anything else is a
	// git failure, not an empty repository.
	_, headErr := gitx.Run(root, 0, "rev-parse", "--verify", "-q", "HEAD")
	hasCommits := headErr == nil
	if headErr != nil && gitx.Code(headErr) != 1 {
		fmt.Fprintf(w, "(git failed: %s; read the code against %s)\n", gitx.Brief(headErr), ctx)
		return nil
	}
	var sections []section
	if !hasCommits {
		fmt.Fprintln(w, "(the repository has no commits yet)")
	} else {
		sections = append(sections, logSection(unit, ctx, ack, func() (bool, bool) {
			// The id is checked hex, so it cannot be read as an option. Exit 1
			// is "not an ancestor"; anything else (an unknown commit, a
			// shallow clone without it, a timeout) means it could not be checked.
			_, err := gitx.Run(root, 0, "merge-base", "--is-ancestor", ack.commit, "HEAD")
			return err == nil, err == nil || gitx.Code(err) == 1
		}))
	}
	// Comparing with the work tree runs clean filters, so do it only when every
	// filter git would use comes from a trusted scope (see trustedFilters).
	// Submodules are not entered: their configs are not checked.
	ok, reason := trustedFilters(root)
	if !ok {
		fmt.Fprintf(w, "(%s, so uncommitted changes are not listed; read the code against %s)\n", reason, ctx)
	} else {
		if hasCommits {
			sections = append(sections, section{"## uncommitted changes", []string{"diff", "--relative", "--no-ext-diff", "--no-textconv", "HEAD", "--stat", "--ignore-submodules=all"}})
		}
		sections = append(sections, section{"## untracked or modified files", []string{"status", "--short", "--untracked-files=all", "--ignore-submodules=all"}})
	}
	for _, sec := range sections {
		fmt.Fprintln(w, sec.title)
		res, err := gitx.Run(root, maxChangesOutput, append(sec.args, "--", spec)...)
		switch {
		case err != nil:
			fmt.Fprintf(w, "(git failed: %s)\n", gitx.Brief(err))
			continue
		case len(res.Out) == 0:
			fmt.Fprintln(w, "(none)")
		}
		w.Write(textx.StripControls(res.Out)) // commit subjects may carry escape sequences
		if res.Truncated {
			fmt.Fprintf(w, "(output cut at %d KiB)\n", maxChangesOutput>>10)
		}
	}
	if unit == units.RootUnit {
		fmt.Fprintln(w, "(for _root this also lists changes inside other units; ignore those)")
	}
	return nil
}

// section is one titled git invocation of the changes report.
type section struct {
	title string
	args  []string
}

// logArgs list up to 50 commits, newest first, with their stat.
var logArgs = []string{"log", "--relative", "--no-ext-diff", "--no-textconv", "--stat", "--date=short", "--format=%h %ad %s", "-n", "50"}

// logSection picks the commits section of a repository with commits: after
// the acked commit when it is in HEAD's history, else since the ack date
// (saying why), else the last commits. ancestor is called only with a
// recorded commit and reports whether it is an ancestor of HEAD and whether
// that could be checked.
func logSection(unit, ctx string, a ack, ancestor func() (is, checked bool)) section {
	dateNote := "no acked commit recorded"
	if a.commit != "" {
		is, checked := ancestor()
		if is {
			return section{"## commits touching " + unit + " since the acked commit " + a.commit[:12],
				slices.Concat(logArgs, []string{a.commit + "..HEAD"})}
		}
		dateNote = "the acked commit is not in HEAD's history"
		if !checked {
			dateNote = "the acked commit is not in this clone or could not be checked"
		}
	}
	if a.since != "" {
		return section{"## commits touching " + unit + " since " + a.since + " (docs last acked; " + dateNote +
			", so commits are picked by commit date and older commits merged later are missed; the whole day is included)",
			slices.Concat(logArgs, []string{"--since=" + a.since})}
	}
	return section{"## last commits touching " + unit + " (" + a.why + ": compare the code with " + ctx + ")", slices.Clone(logArgs)}
}

// ack is the last ack of a unit from its frontmatter: the acked commit (""
// if none or malformed), the "YYYY-MM-DD 00:00" of the ack day ("" if none)
// and, without a date, why.
type ack struct {
	commit, since, why string
}

// ackPoint reads the last ack from the context doc. Only a full hex commit id
// and a real date reach git: the frontmatter is repo content.
func ackPoint(root, ctx string) ack {
	meta, _, err := frontmatter.Read(root, fsutil.Abs(root, ctx))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ack{why: "no context doc yet"}
	case errors.Is(err, frontmatter.ErrNoFrontmatter):
		return ack{why: "not acked yet"}
	case errors.Is(err, frontmatter.ErrBroken):
		return ack{why: "broken frontmatter"}
	case errors.Is(err, fsutil.ErrOutside):
		return ack{why: "the context doc " + docstate.OutsideText}
	case err != nil:
		return ack{why: "the context doc cannot be read"}
	}
	var a ack
	if isCommitID(meta.Commit) {
		a.commit = meta.Commit
	}
	d, err := time.Parse("2006-01-02", meta.Updated)
	if err != nil {
		a.why = "no valid ack date"
		return a
	}
	a.since = d.Format("2006-01-02") + " 00:00"
	return a
}

// isCommitID reports whether s is a full lowercase hex object id (SHA-1 or
// SHA-256), as rev-parse prints it.
func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// trustedFilters reports whether every filter driver in git's effective
// config comes from the user's own global config or the command line: those
// are trusted (e.g. Git LFS), while the repo's local or worktree config (and
// what it includes) may have come in an archive. It fails closed: any other
// scope, an error or a git without --show-scope means false, with the reason.
func trustedFilters(root string) (bool, string) {
	res, err := gitx.Run(root, 0, "config", "--show-scope", "--includes", "--get-regexp", `^filter\.`)
	switch gitx.Code(err) {
	case 1:
		return true, "" // no filter anywhere
	case 0:
	default:
		return false, "the git filter config could not be checked (git " + gitx.Brief(err) + ")"
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(res.Out)), "\n") {
		scope, _, _ := strings.Cut(line, "\t")
		if scope != "global" && scope != "command" {
			return false, "the repository's git config defines filter drivers"
		}
	}
	return true, ""
}
