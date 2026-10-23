package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/testutil"
)

// problemMarks are, per problem code, a phrase of its text in `status
// --json` (warnings and notes) and in SessionStart's output: the test reads
// which problems each channel shows from the outputs themselves. The phrases
// come from the code's own text, not from the fixtures; "" is a code the
// channel cannot tell apart (see shownCodes).
var problemMarks = map[project.ProblemCode][2]string{
	project.ProblemActivationRefused:   {"exists but is not used", "so the plugin is off here"},
	project.ProblemConfigUnreadable:    {"(using defaults; docs tracking is paused until it is fixed)", "could not be read or parsed, so docs tracking is paused"},
	project.ProblemConfigInvalid:       {"(the other settings apply)", "invalid setting(s) that were ignored"},
	project.ProblemConfigDegraded:      {"", "so the units index and auto-ack are paused"},
	project.ProblemConfigIgnoreUnknown: {"git could not tell whether", "(so whether the settings are committed)"},
	project.ProblemConfigIgnored:       {"is ignored by git, so the settings are not committed", "un-ignore that file"},
	// status lists the warnings themselves: matched by Diagnose's texts
	// (discoverShown).
	project.ProblemDiscoverWarning: {"", "discovery warning(s)"},
	project.ProblemCacheUnusable:   {"the hash cache cannot be used", "the hash cache was unusable"},
	project.ProblemIndexMarkers:    {"are unbalanced or repeated, so the units index is not updated", "restore or remove the marker lines"},
	project.ProblemIndexUnusable:   {"cannot be used for the units index", ", so the units index is not updated."},
	project.ProblemLogUnreadable:   {".auto-context/log could not be read", ".auto-context/log cannot be read"},
	project.ProblemLogUnwritable:   {".auto-context/log cannot be written", ".auto-context/log cannot be written"},
	project.ProblemLogErrors:       {"error(s) in .auto-context/log", "hook error(s) since the last session"},
}

// shownCodes lists the problem codes whose phrase (column col of
// problemMarks) is in text.
func shownCodes(text string, col int) []project.ProblemCode {
	var out []project.ProblemCode
	for c, m := range problemMarks {
		if m[col] != "" && strings.Contains(text, m[col]) {
			out = append(out, c)
		}
	}
	if slices.Contains(out, project.ProblemConfigDegraded) {
		// A degraded config is also told as ignored settings (SessionStart's
		// systemMessage): one problem, not two.
		out = slices.DeleteFunc(out, func(c project.ProblemCode) bool { return c == project.ProblemConfigInvalid })
	}
	slices.Sort(out)
	return out
}

// discoverShown reports whether status lists every discovery warning of
// diag (their texts are units' fixed ones), and there is one.
func discoverShown(statusText string, diag []project.Problem) bool {
	n := 0
	for _, pr := range diag {
		if pr.Code == project.ProblemDiscoverWarning {
			if !strings.Contains(statusText, pr.Err.Error()) {
				return false
			}
			n++
		}
	}
	return n > 0
}

// statusView is what status can show of the codes Diagnose found: a
// degraded config reads exactly as invalid settings (the CLI text is one),
// and of a log that cannot be read status says only that, while SessionStart
// also says it cannot be written (the behaviour before the problem model).
func statusView(want []project.ProblemCode) []project.ProblemCode {
	out := slices.Clone(want)
	for i, c := range out {
		if c == project.ProblemConfigDegraded {
			out[i] = project.ProblemConfigInvalid
		}
	}
	if slices.Contains(out, project.ProblemLogUnreadable) {
		out = slices.DeleteFunc(out, func(c project.ProblemCode) bool {
			return c == project.ProblemLogUnwritable || c == project.ProblemLogErrors
		})
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// For each broken fixture, the problems Diagnose finds are exactly those
// `status --json` and SessionStart show: both render the one problem model.
func TestDiagnoseMatchesStatusAndSessionStart(t *testing.T) {
	noRoot := func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("file permissions do not deny writes here")
		}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"config unreadable", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: [\n")
		}},
		{"config invalid", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".auto-context/config.yaml", "max_session_notice: -1\n")
		}},
		{"config degraded", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 0\n")
		}},
		{"activation refused", func(t *testing.T, root string) {
			if runtime.GOOS == "windows" {
				t.Skip("symlinks need privileges on Windows")
			}
			outside := t.TempDir()
			testutil.WriteFile(t, outside, "r.md", "# r\n")
			if err := os.Remove(filepath.Join(root, "context", "_root.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "r.md"), filepath.Join(root, "context", "_root.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"config check-ignore fails", func(t *testing.T, root string) {
			if _, err := exec.LookPath("git"); err != nil {
				t.Skip("git not installed")
			}
			testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 1\n")
			for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"}} {
				testutil.Git(t, root, args...)
			}
			testutil.WriteFile(t, root, ".git/index", "garbage")
		}},
		{"config ignored", func(t *testing.T, root string) {
			if _, err := exec.LookPath("git"); err != nil {
				t.Skip("git not installed")
			}
			testutil.Git(t, root, "init", "-q")
			testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 1\n")
			testutil.WriteFile(t, root, ".gitignore", ".auto-context/\n")
		}},
		{"discovery warning", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".gitignore", "[a\n")
		}},
		{"hash cache unusable", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, ".auto-context/cache.v5.bin", "garbage")
		}},
		{"index markers", func(t *testing.T, root string) {
			testutil.WriteFile(t, root, "CLAUDE.md", "# P\n\n<!-- auto-context:units -->\n")
		}},
		{"index outside", func(t *testing.T, root string) {
			if runtime.GOOS == "windows" {
				t.Skip("symlinks need privileges on Windows")
			}
			outside := t.TempDir()
			testutil.WriteFile(t, outside, "o.md", "# o\n")
			if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "o.md"), filepath.Join(root, "CLAUDE.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"log errors", func(t *testing.T, root string) {
			logx.Errorf(root, "earlier hook failed")
		}},
		{"log unreadable", func(t *testing.T, root string) {
			noRoot(t)
			testutil.WriteFile(t, root, ".auto-context/log", "")
			if err := os.Chmod(filepath.Join(root, ".auto-context", "log"), 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"log unwritable", func(t *testing.T, root string) {
			noRoot(t)
			testutil.WriteFile(t, root, ".auto-context/log", "")
			if err := os.Chmod(filepath.Join(root, ".auto-context", "log"), 0o444); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each channel gets its own copy: both log, and what one logs
			// the other would report.
			statusRoot, sessionRoot := repo(t), repo(t)
			tc.setup(t, statusRoot)
			tc.setup(t, sessionRoot)

			p, err := project.Open(context.Background(), statusRoot)
			if err != nil {
				t.Fatal(err)
			}
			var want []project.ProblemCode
			diag := p.Diagnose()
			for _, pr := range diag {
				if _, ok := problemMarks[pr.Code]; ok && !slices.Contains(want, pr.Code) {
					want = append(want, pr.Code)
				}
			}
			slices.Sort(want)
			if len(want) == 0 {
				t.Fatalf("Diagnose found no problem: %+v", p.Diagnose())
			}

			_, out, _ := do(statusRoot, "", "status", "--json")
			var r statusJSON
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatalf("status --json: %v\n%s", err, out)
			}
			statusText := strings.Join(slices.Concat(r.Warnings, r.Notes), "\n")
			got := shownCodes(statusText, 0)
			if discoverShown(statusText, diag) {
				got = append(got, project.ProblemDiscoverWarning)
				slices.Sort(got)
			}
			if strings.Contains(statusText, "(details in .auto-context/log)") && !slices.Contains(want, project.ProblemLogErrors) {
				// status logs an error's detail before it counts the log:
				// the count includes what it has just written.
				got = slices.DeleteFunc(got, func(c project.ProblemCode) bool { return c == project.ProblemLogErrors })
			}
			if sv := statusView(want); !slices.Equal(got, sv) {
				t.Errorf("status shows %v, Diagnose %v (as status can show them: %v):\n%s", got, want, sv, statusText)
			}

			_, sout, _ := do(sessionRoot, `{"session_id":"s","hook_event_name":"SessionStart"}`, "hook", "session-start")
			var h struct {
				SystemMessage string `json:"systemMessage"`
				Hook          struct {
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(sout), &h); err != nil {
				t.Fatalf("session-start: %v\n%s", err, sout)
			}
			sessionText := h.SystemMessage + "\n" + h.Hook.AdditionalContext
			if got := shownCodes(sessionText, 1); !slices.Equal(got, want) {
				t.Errorf("SessionStart shows %v, Diagnose %v:\n%s", got, want, sessionText)
			}
		})
	}
}

// An interrupt during discovery is said as such, not as "unreadable".
func TestOpenInterruptedText(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb strings.Builder
	if code := runStatus(ctx, nil, &out, &errb, repo(t)); code != exitError {
		t.Fatalf("code=%d", code)
	}
	if got := out.String(); !strings.Contains(got, "cannot be opened: interrupted") {
		t.Errorf("stdout = %q", got)
	}
}
