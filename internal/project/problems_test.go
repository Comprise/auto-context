package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"auto-context/internal/fsutil"
	"auto-context/internal/units"
)

// Discovery stopped by time, a slow git or an interrupt gets its own fixed
// CLI text, not the generic "unreadable".
func TestCauseTextsCLI(t *testing.T) {
	cases := map[error]string{
		fmt.Errorf("discover: %w", units.ErrTimeUp):       causeTexts[CauseTimeUp][0],
		fmt.Errorf("discover: %w", units.ErrGitTimeout):   "git is too slow or lists too many files; add big generated directories to .gitignore",
		fmt.Errorf("discover: %w", units.ErrListTooLarge): "git is too slow or lists too many files; add big generated directories to .gitignore",
		fmt.Errorf("x: %w", fsutil.ErrOutside):            "it leads outside the repository or into .git",
		fmt.Errorf("x: %w", os.ErrNotExist):               "missing",
		errors.New("odd a/b.go"):                          "unreadable",
	}
	for err, want := range cases {
		if got := CauseOf(err).Text(CLI); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
	if got := CauseOf(units.ErrTimeUp).Text(CLI); got == "unreadable" || got == CauseOf(units.ErrTimeUp).Text(Stop) {
		t.Errorf("time up in the CLI: %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := CauseOfCtx(ctx, units.ErrTimeUp).Text(CLI); got != "interrupted" {
		t.Errorf("interrupted: %q", got)
	}
	if got := CauseOfCtx(context.Background(), units.ErrTimeUp); got != CauseTimeUp {
		t.Errorf("not interrupted: %v", got)
	}
}

// Each channel's text is fixed (no error text leaks).
func TestProblemTextsNeverQuoteErrors(t *testing.T) {
	err := fmt.Errorf("open svc/SECRET.go: %w", os.ErrPermission)
	for code, spec := range problemTable {
		for ch := range spec.texts {
			if code == ProblemDiscoverWarning || code == ProblemConfigInvalid || code == ProblemConfigDegraded {
				continue // their Err is fixed text by contract
			}
			pr := Problem{Code: code, Err: err, N: 2}
			if got := pr.Text(ch, Render{Command: "ac", IndexFile: "CLAUDE.md"}); got == "" || strings.Contains(got, "SECRET") {
				t.Errorf("code %d channel %d: %q", code, ch, got)
			}
		}
	}
}

// Every code has a row with a severity, and the problems Diagnose builds
// carry it.
func TestProblemTableComplete(t *testing.T) {
	for c := ProblemNotActive; c <= ProblemLogErrors; c++ {
		spec, ok := problemTable[c]
		if !ok || (spec.severity != SeverityWarning && spec.severity != SeverityNote) {
			t.Errorf("code %d: %+v", c, spec)
		}
		if newProblem(c, nil, 0).Severity() != spec.severity || (Problem{Code: c}).Severity() != spec.severity {
			t.Errorf("code %d: severity not from the table", c)
		}
	}
	// A paused index is said by the hooks only; the CLI says the ack's
	// ErrConfigDegraded.
	if got := IndexProblem(ErrConfigDegraded); got.Code != ProblemIndexPaused || got.Text(CLI, Render{}) != "" || got.Text(Session, Render{}) == "" {
		t.Errorf("paused index: %+v", got)
	}
}

// The CLI's file categories are fsutil.Category's.
func TestCauseTextsFollowFsutil(t *testing.T) {
	for c, sentinel := range map[Cause]error{
		CauseOther: errors.New("x"), CauseOutside: fsutil.ErrOutside, CauseNotRegular: fsutil.ErrNotRegular,
		CauseTooLarge: fsutil.ErrTooLarge, CausePermission: os.ErrPermission, CauseMissing: os.ErrNotExist,
	} {
		if got, want := c.Text(CLI), fsutil.Category(sentinel); got != want {
			t.Errorf("cause %d: %q, want %q", c, got, want)
		}
	}
}

// ErrText says "interrupted" once the command's context is canceled.
func TestErrTextInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := (Render{Ctx: ctx}).ErrText(os.ErrPermission, "ack"); got != "interrupted" {
		t.Errorf("canceled: %q", got)
	}
	if got := (Render{}).ErrText(os.ErrPermission, "ack"); got != "permission denied" {
		t.Errorf("no ctx: %q", got)
	}
}
