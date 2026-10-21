package project

import (
	"errors"
	"fmt"
	"io"
	"os"
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
// is a HEAD) and `git status` (untracked and modified files). It runs fixed git invocations itself, so
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

	if _, err := gitx.Run(root, 0, "rev-parse", "--is-inside-work-tree"); err != nil {
		fmt.Fprintf(w, "(git unavailable: %s; read the code against %s)\n", textx.Err(err), ctx)
		return nil
	}
	// `rev-parse --verify -q` exits 1 for a missing HEAD; anything else is a
	// git failure, not an empty repository.
	_, headErr := gitx.Run(root, 0, "rev-parse", "--verify", "-q", "HEAD")
	hasCommits := headErr == nil
	if headErr != nil && gitx.Code(headErr) != 1 {
		fmt.Fprintf(w, "(git failed: %s; read the code against %s)\n", textx.Err(headErr), ctx)
		return nil
	}
	type section struct {
		title string
		args  []string
	}
	var sections []section
	logArgs := []string{"log", "--relative", "--no-ext-diff", "--no-textconv", "--stat", "--date=short", "--format=%h %ad %s", "-n", "50"}
	byCommit := false
	dateNote := "no acked commit recorded"
	if hasCommits && ack.commit != "" {
		// The id is checked hex, so it cannot be read as an option.
		_, err := gitx.Run(root, 0, "merge-base", "--is-ancestor", ack.commit, "HEAD")
		byCommit = err == nil
		dateNote = "the acked commit is not in HEAD's history"
		if err != nil && gitx.Code(err) != 1 {
			// Exit 1 is "not an ancestor"; anything else (an unknown commit, a
			// shallow clone without it, a timeout) means it could not be checked.
			dateNote = "the acked commit is not in this clone or could not be checked"
		}
	}
	switch {
	case !hasCommits:
		fmt.Fprintln(w, "(the repository has no commits yet)")
	case byCommit:
		sections = append(sections, section{"## commits touching " + unit + " since the acked commit " + ack.commit[:12],
			append(logArgs, ack.commit+"..HEAD")})
	case ack.since != "":
		sections = append(sections, section{"## commits touching " + unit + " since " + ack.since + " (docs last acked; " + dateNote +
			", so commits are picked by commit date and older commits merged later are missed; the whole day is included)",
			append(logArgs, "--since="+ack.since)})
	default:
		sections = append(sections, section{"## last commits touching " + unit + " (" + ack.why + ": compare the code with " + ctx + ")", logArgs})
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
			fmt.Fprintf(w, "(git failed: %s)\n", textx.Err(err))
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
		return false, "the git filter config could not be checked (" + textx.Err(err) + ")"
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(res.Out)), "\n") {
		scope, _, _ := strings.Cut(line, "\t")
		if scope != "global" && scope != "command" {
			return false, "the repository's git config defines filter drivers"
		}
	}
	return true, ""
}
