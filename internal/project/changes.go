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
// commits since the frontmatter's `updated` date (the whole ack day included)
// and, when every git filter is trusted, the uncommitted diff stat (if there
// is a HEAD) and `git status` (untracked and modified files). It runs fixed git invocations itself, so
// the sync skill needs no git permissions (a pre-approved `git log *` would
// also allow `--output=<any file>`). Paths are relative to the root, which may
// be below the git top level. Git failures are written as notes, not returned.
func (p *Project) WriteChanges(w io.Writer, unit string) error {
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
	since, why := ackDate(root, ctx)

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
	switch {
	case !hasCommits:
		fmt.Fprintln(w, "(the repository has no commits yet)")
	case since != "":
		sections = append(sections, section{"## commits touching " + unit + " since " + since + " (docs last acked; the whole day is included)",
			[]string{"log", "--relative", "--no-ext-diff", "--no-textconv", "--stat", "--date=short", "--format=%h %ad %s", "-n", "50", "--since=" + since}})
	default:
		sections = append(sections, section{"## last commits touching " + unit + " (" + why + ": compare the code with " + ctx + ")",
			[]string{"log", "--relative", "--no-ext-diff", "--no-textconv", "--stat", "--date=short", "--format=%h %ad %s", "-n", "50"}})
	}
	// Comparing with the work tree runs clean filters, so do it only when every
	// filter git would use comes from a trusted scope (see trustedFilters).
	// Submodules are not entered: their configs are not checked.
	ok, reason := trustedFilters(root)
	switch {
	case !ok:
		fmt.Fprintf(w, "(%s, so uncommitted changes are not listed; read the code against %s)\n", reason, ctx)
	default:
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
		w.Write(res.Out)
		if res.Truncated {
			fmt.Fprintf(w, "(output cut at %d KiB)\n", maxChangesOutput>>10)
		}
	}
	if unit == units.RootUnit {
		fmt.Fprintln(w, "(for _root this also lists changes inside other units; ignore those)")
	}
	return nil
}

// ackDate returns the "YYYY-MM-DD 00:00" of the last ack from the frontmatter,
// or "" and why there is none. Only a real date reaches git: the frontmatter
// is repo content.
func ackDate(root, ctx string) (since, why string) {
	meta, _, err := frontmatter.Read(root, fsutil.Abs(root, ctx))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", "no context doc yet"
	case errors.Is(err, frontmatter.ErrNoFrontmatter):
		return "", "not acked yet"
	case errors.Is(err, frontmatter.ErrBroken):
		return "", "broken frontmatter"
	case errors.Is(err, fsutil.ErrOutside):
		return "", "the context doc " + docstate.OutsideText
	case err != nil:
		return "", "the context doc cannot be read"
	}
	d, err := time.Parse("2006-01-02", meta.Updated)
	if err != nil {
		return "", "no valid ack date"
	}
	return d.Format("2006-01-02") + " 00:00", ""
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
	for _, line := range strings.Split(strings.TrimSpace(string(res.Out)), "\n") {
		scope, _, _ := strings.Cut(line, "\t")
		if scope != "global" && scope != "command" {
			return false, "the repository's git config defines filter drivers"
		}
	}
	return true, ""
}
