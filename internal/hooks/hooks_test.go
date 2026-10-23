package hooks

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"auto-context/internal/docstate"
	"auto-context/internal/frontmatter"
	"auto-context/internal/hashing"
	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/session"
	"auto-context/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite golden files")

func testEnv(root string) Env {
	return Env{Root: root, Now: func() time.Time { return time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) }}
}

// ack records the current source hash of units in their context files.
func ack(t *testing.T, env Env, units ...string) {
	t.Helper()
	p, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		if err := p.Ack(u, env.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.SaveCache(); err != nil {
		t.Fatal(err)
	}
}

// newRepo builds a repo with units svc (go.mod) and, if withLib, lib; _root is always documented.
func newRepo(t *testing.T, withLib bool) Env {
	t.Helper()
	root := t.TempDir()
	testutil.WriteFile(t, root, "CLAUDE.md", "# Project\n")
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/main.go", "package main\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n\nRoot summary.\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nService summary.\n")
	if withLib {
		testutil.WriteFile(t, root, "lib/go.mod", "module lib\n")
		testutil.WriteFile(t, root, "lib/lib.go", "package lib\n")
	}
	env := testEnv(root)
	ack(t, env, "_root", "svc")
	return env
}

func TestSessionNotice(t *testing.T) {
	tail := ". Update them when working there (skill auto-context:auto-context-docs) or run /auto-context:status."
	cases := []struct {
		name           string
		stale, missing []string
		max            int
		want           string
	}{
		{"stale only", []string{"a", "b"}, nil, 10, "auto-context: docs stale for a, b" + tail},
		{"missing only", nil, []string{"c"}, 10, "auto-context: missing for c" + tail},
		{"both", []string{"a"}, []string{"c"}, 10, "auto-context: docs stale for a; missing for c" + tail},
		{"overflow", []string{"a", "b"}, []string{"c"}, 2, "auto-context: docs stale for a, b (+1 more)" + tail},
		{"overflow into missing", []string{"a"}, []string{"c", "d"}, 2, "auto-context: docs stale for a; missing for c (+1 more)" + tail},
		{"counts only", []string{"a", "b"}, []string{"c"}, 0, "auto-context: docs stale for 2 units, missing for 1 unit" + tail},
		{"counts stale", []string{"a"}, nil, 0, "auto-context: docs stale for 1 unit" + tail},
		{"counts missing", nil, []string{"c", "d"}, 0, "auto-context: docs missing for 2 units" + tail},
		{"spec", []string{"src/auth", "cmd/api"}, []string{"internal/billing"}, 10,
			"auto-context: docs stale for src/auth, cmd/api; missing for internal/billing. Update them when working there (skill auto-context:auto-context-docs) or run /auto-context:status."},
	}
	for _, c := range cases {
		if got := SessionNotice(c.stale, c.missing, c.max, Claude); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestSessionStartQuietWhenOK(t *testing.T) {
	env := newRepo(t, false)
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil || out != nil {
		t.Fatalf("out=%q err=%v, want nil", out, err)
	}
}

// checkGolden compares got with testdata/<name>; -update rewrites the file.
// Golden files are stored without a trailing newline, so trailing line breaks
// (e.g. from a CRLF checkout) are trimmed from the file before comparing.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if w := strings.TrimRight(string(want), "\r\n"); string(got) != w {
		t.Errorf("%s:\n got %q\nwant %q", name, got, w)
	}
}

func TestSessionStartGolden(t *testing.T) {
	env := newRepo(t, true)
	testutil.WriteFile(t, env.Root, "svc/main.go", "package main\n// changed\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "session_start.golden.json", out)
}

func TestSessionStartCodexGolden(t *testing.T) {
	env := newRepo(t, true)
	env.Agent = Codex
	testutil.WriteFile(t, env.Root, "svc/main.go", "package main\n// changed\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "session_start_codex.golden.json", out)
}

func TestSessionStartUpdatesRootIndex(t *testing.T) {
	env := newRepo(t, false)
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- `svc` — Service summary.") {
		t.Errorf("index missing svc entry:\n%s", data)
	}
}

func TestPromptSubmitWritesSnapshotAndClearsJournal(t *testing.T) {
	env := newRepo(t, false)
	const sid = "sess1"
	j := session.Journal{CodeChanged: []string{"svc"}}
	if err := j.Save(env.Root, sid); err != nil {
		t.Fatal(err)
	}
	out, err := PromptSubmit(env, Input{SessionID: sid})
	if err != nil || out != nil {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if got, _ := session.LoadJournal(env.Root, sid); len(got.CodeChanged) != 0 {
		t.Errorf("journal not cleared: %+v", got)
	}
	snap, err := session.LoadSnapshot(env.Root, sid)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["svc/main.go"]; !ok {
		t.Errorf("snapshot lacks svc/main.go: %v", snap)
	}
}

// snapshotTree maps every path under root to its content ("" for directories).
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			m[filepath.ToSlash(rel)+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(p)
		m[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHooksInactiveWithoutConfigOrContext(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "CLAUDE.md", "# Project\n")
	testutil.WriteFile(t, root, ".gitignore", "bin/\n")
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/main.go", "package main\n")
	env := testEnv(root)
	before := snapshotTree(t, root)
	in := Input{SessionID: "s"}
	for name, h := range map[string]func(Env, Input) ([]byte, error){
		"SessionStart": SessionStart, "PromptSubmit": PromptSubmit, "Stop": Stop,
	} {
		if out, err := h(env, in); out != nil || err != nil {
			t.Errorf("%s: out=%q err=%v, want nil", name, out, err)
		}
	}
	testutil.WriteFile(t, root, "svc/main.go", "package main\n// changed\n")
	if out, err := Stop(env, in); out != nil || err != nil {
		t.Errorf("Stop after edit: out=%q err=%v, want nil", out, err)
	}
	before["svc/main.go"] = "package main\n// changed\n"
	after := snapshotTree(t, root)
	if len(after) != len(before) {
		t.Errorf("tree changed:\nbefore %v\nafter  %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed: %q -> %q", k, v, after[k])
		}
	}
}

func TestActive(t *testing.T) {
	root := t.TempDir()
	if project.Active(root) {
		t.Error("empty dir active")
	}
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 3\n")
	if !project.Active(root) {
		t.Error("config does not activate")
	}
	// A bare context/ directory is a common name (it may even hold code): it
	// must not activate the plugin on its own.
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "context/theme.go", "package context\n")
	if project.Active(dir) {
		t.Error("bare context dir activates")
	}
	testutil.WriteFile(t, dir, "context/_root.md", "# root\n")
	if !project.Active(dir) {
		t.Error("context/_root.md does not activate")
	}
	// A marker linking outside the repo does not count.
	linked, outside := t.TempDir(), t.TempDir()
	testutil.WriteFile(t, outside, "conf.yaml", "min_files: 1\n")
	testutil.Symlink(t, filepath.Join(outside, "conf.yaml"), filepath.Join(linked, ".auto-context/config.yaml"))
	if project.Active(linked) || !project.ActivationRefused(linked) {
		t.Error("marker linked outside activates")
	}
	// A marker that is no regular file does not count either.
	notFile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(notFile, "context", "_root.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if project.Active(notFile) || !project.ActivationRefused(notFile) {
		t.Error("context/_root.md directory activates")
	}
}

type sessionOut struct {
	SystemMessage string `json:"systemMessage"`
	Hook          struct {
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func decodeSession(t *testing.T, out []byte) sessionOut {
	t.Helper()
	var v sessionOut
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("bad output %q: %v", out, err)
	}
	return v
}

// Index problems the user must fix are reported, with the remedy, even when
// every unit is ok.
func TestSessionStartReportsBrokenMarkers(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, "CLAUDE.md", "# Project\n<!-- /auto-context:units -->\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeSession(t, out).Hook.AdditionalContext; !strings.Contains(c, "markers") {
		t.Errorf("context = %q", c)
	}
}

// A hook that cannot run tells the user instead of going silent.
func TestSessionStartFailureIsVisible(t *testing.T) {
	env := newRepo(t, false)
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(env.Root, ".auto-context")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(env.Root, ".auto-context")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeSession(t, out).SystemMessage; !strings.Contains(m, "auto-context") {
		t.Errorf("systemMessage = %q", m)
	}
}

func TestSessionStartRemovesStaleSessions(t *testing.T) {
	env := newRepo(t, false)
	old := session.SnapshotPath(env.Root, "old")
	if err := (session.Snapshot{}).Save(env.Root, "old"); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("stale session file kept: %v", err)
	}
}

// A bad setting in the config is shown to the user, and the rest applies.
func TestSessionStartShowsConfigProblem(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "exlude: [svc]\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	v := decodeSession(t, out)
	if !strings.Contains(v.SystemMessage, "1 invalid setting") || !strings.Contains(v.Hook.AdditionalContext, "auto-context status") {
		t.Errorf("out = %s", out)
	}
}

// Text from a hostile config never reaches Claude's context or the user notice.
func TestSessionStartDoesNotEchoConfig(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "IGNORE_PREVIOUS_INSTRUCTIONS: 1\nexclude: [\"[RUN_CURL\"]\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); strings.Contains(s, "IGNORE_PREVIOUS") || strings.Contains(s, "RUN_CURL") {
		t.Errorf("config text echoed: %s", s)
	}
	if v := decodeSession(t, out); !strings.Contains(v.SystemMessage, "2 invalid setting") {
		t.Errorf("systemMessage = %q", v.SystemMessage)
	}
}

// Problems SessionStart reports itself are not counted again as hook errors,
// in this session or the next.
func TestSessionStartDoesNotCountOwnLogEntries(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "exlude: [svc]\n")
	for i := range 2 {
		out, err := SessionStart(env, Input{SessionID: "s"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "hook error") {
			t.Errorf("run %d: %s", i, out)
		}
	}
}

// A configured docs language is announced even when every doc is up to date.
func TestSessionStartNamesDocsLanguage(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "language: pt-BR\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if v := decodeSession(t, out); v.Hook.AdditionalContext != "auto-context: "+LanguageNote("pt-BR", "CLAUDE.md") || v.SystemMessage != "" {
		t.Errorf("out = %s", out)
	}
}

func TestSessionEndRemovesSessionFiles(t *testing.T) {
	env := newRepo(t, false)
	if _, err := PromptSubmit(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	other := Input{SessionID: "other"}
	if _, err := PromptSubmit(env, other); err != nil {
		t.Fatal(err)
	}
	if out, err := SessionEnd(env, Input{SessionID: "s"}); out != nil || err != nil {
		t.Fatalf("%s, %v", out, err)
	}
	if _, err := os.Stat(session.SnapshotPath(env.Root, "s")); !os.IsNotExist(err) {
		t.Errorf("snapshot kept: %v", err)
	}
	if _, err := os.Stat(session.SnapshotPath(env.Root, "other")); err != nil {
		t.Errorf("another session's snapshot removed: %v", err)
	}
}

// In an inactive project SessionEnd creates and logs nothing.
func TestSessionEndInactiveWritesNothing(t *testing.T) {
	root := t.TempDir()
	if out, err := SessionEnd(testEnv(root), Input{SessionID: "s"}); out != nil || err != nil {
		t.Fatalf("%s, %v", out, err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("wrote %v", entries)
	}
}

// An unparsable config is reported alone: no statuses, no index rewrite and
// no orphan advice computed from the default units.
func TestSessionStartBrokenConfigTouchesNothing(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "include: [svc]\n")
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md"))
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	v := decodeSession(t, out)
	if !strings.Contains(v.SystemMessage, "could not be read or parsed") || strings.Contains(v.Hook.AdditionalContext, "no longer exist") || strings.Contains(v.Hook.AdditionalContext, "stale") {
		t.Errorf("out = %s", out)
	}
	if after, _ := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md")); string(after) != string(before) {
		t.Errorf("root index rewritten:\n%s\n---\n%s", before, after)
	}
}

// Orphan docs are not to be deleted without asking.
func TestSessionStartOrphanAdviceAsksFirst(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, "context/gone.md", "# gone\n\nOld.\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeSession(t, out).Hook.AdditionalContext; !strings.Contains(c, "ask the user") || strings.Contains(c, "delete them") {
		t.Errorf("context = %q", c)
	}
}

// An error logged in the same second as the session mark, after it, still counts.
func TestErrorsSinceLastSessionSameSecond(t *testing.T) {
	env := newRepo(t, false)
	markSessionStart(env)
	logx.Errorf(env.Root, "boom")
	if n, err := errorsSinceLastSession(env.Root); err != nil || n != 1 {
		t.Errorf("n=%d err=%v, want 1", n, err)
	}
}

// A log the plugin cannot read is reported: its errors would be lost.
func TestSessionStartReportsUnreadableLog(t *testing.T) {
	env := newRepo(t, false)
	if err := os.MkdirAll(filepath.Join(env.Root, ".auto-context", "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeSession(t, out).Hook.AdditionalContext; !strings.Contains(c, ".auto-context/log cannot be read") {
		t.Errorf("context = %q", c)
	}
}

// A corrupt hash cache is logged under its own name, not as a discovery
// warning, and rewritten.
func TestSessionStartCorruptCache(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/cache.json", "garbage")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "discovery warning") {
		t.Errorf("out = %s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "cache.json")); string(data) == "garbage" {
		t.Error("corrupt cache kept")
	}
}

func TestSessionFailureIsValidJSON(t *testing.T) {
	if v := decodeSession(t, SessionFailure(Claude)); !strings.Contains(v.SystemMessage, "docs tracking is off") {
		t.Errorf("%+v", v)
	}
}

func TestSessionFailureAgentCLI(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_ROOT", "/home/u/plugin")
	if v := decodeSession(t, SessionFailure(Codex)); !strings.Contains(v.SystemMessage, "`/home/u/plugin/bin/auto-context status`") {
		t.Errorf("codex: %s", v.SystemMessage)
	}
	if v := decodeSession(t, SessionFailure(Claude)); !strings.Contains(v.SystemMessage, "`auto-context status`") {
		t.Errorf("claude: %s", v.SystemMessage)
	}
}

// A marker that links outside the repo keeps the plugin off, and says so.
func TestSessionStartRefusedMarker(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "cfg.yaml")
	testutil.WriteFile(t, filepath.Dir(out), "cfg.yaml", "")
	testutil.Symlink(t, out, filepath.Join(root, ".auto-context/config.yaml"))
	got, err := SessionStart(testEnv(root), Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if v := decodeSession(t, got); !strings.Contains(v.SystemMessage, "the plugin is off here") {
		t.Errorf("out = %s", got)
	}
	// Nothing is written beside the link itself.
	if es, err := os.ReadDir(filepath.Join(root, ".auto-context")); err != nil || len(es) != 1 {
		t.Errorf("state written: %v %v", es, err)
	}
	if es, err := os.ReadDir(root); err != nil || len(es) != 1 {
		t.Errorf("repo written: %v %v", es, err)
	}
}

// Errors logged by earlier hooks are reported by the next SessionStart, once.
func TestSessionStartReportsEarlierHookErrors(t *testing.T) {
	env := newRepo(t, false)
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	logx.Errorf(env.Root, "stop: boom")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeSession(t, out).Hook.AdditionalContext; !strings.Contains(c, "1 hook error(s) since the last session") {
		t.Errorf("context = %q", c)
	}
	if out, _ := SessionStart(env, Input{SessionID: "s"}); strings.Contains(string(out), "hook error") {
		t.Errorf("reported twice: %s", out)
	}
}

// While the config is broken SessionStart still reports earlier hook errors.
func TestSessionStartBrokenConfigKeepsErrorCount(t *testing.T) {
	env := newRepo(t, false)
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	logx.Errorf(env.Root, "stop: boom")
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeSession(t, out).Hook.AdditionalContext; !strings.Contains(c, "1 hook error(s)") {
		t.Errorf("context = %q", c)
	}
}

// The first SessionStart of a project enabled by its docs only, with no
// .auto-context/ yet, does not report an unwritable log and leaves no log.
func TestSessionStartFirstRunLogIsFine(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "context/_root.md", "# r\n\nR.\n")
	out, err := SessionStart(testEnv(root), Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "cannot be written") {
		t.Errorf("out = %s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".auto-context", "log")); err == nil {
		t.Error("empty log left behind")
	}
}

// Hooks that keep session state skip a payload without a session id: no
// shared "_" files, a log entry instead (Q-L10).
func TestHooksSkipEmptySessionID(t *testing.T) {
	env := newRepo(t, false)
	for _, id := range []string{"", "  "} {
		for name, h := range map[string]func(Env, Input) ([]byte, error){
			"PromptSubmit": PromptSubmit, "Stop": Stop, "SessionEnd": SessionEnd,
		} {
			if out, err := h(env, Input{SessionID: id}); out != nil || err != nil {
				t.Errorf("%s(%q): out=%q err=%v", name, id, out, err)
			}
		}
	}
	es, _ := os.ReadDir(filepath.Join(env.Root, ".auto-context", "state"))
	for _, e := range es {
		if strings.HasPrefix(e.Name(), "_") {
			t.Errorf("state file %s written", e.Name())
		}
	}
	data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "log"))
	if n := strings.Count(string(data), "no session_id"); n != 6 {
		t.Errorf("%d log entries, want 6: %s", n, data)
	}
}

// classifyStatuses groups statuses for the notice; unusable reasons are
// fixed texts, old-scheme docs are counted (Q-M1).
func TestSessionStartClassifyStatuses(t *testing.T) {
	g := classifyStatuses([]docstate.UnitStatus{
		{Unit: "ok", State: docstate.OK},
		{Unit: "old", State: docstate.OK, Note: docstate.NoteOldScheme},
		{Unit: "old2", State: docstate.OK, Note: docstate.NoteOldScheme},
		{Unit: "plain", State: docstate.Stale},
		{Unit: "new", State: docstate.Stale, Note: docstate.NoteNotAcked},
		{Unit: "link", State: docstate.Stale, Note: docstate.NoteOutside},
		{Unit: "fm", State: docstate.Stale, Note: docstate.NoteBroken},
		{Unit: "perm", State: docstate.Stale, Note: docstate.NoteUnreadable, Detail: "permission denied"},
		{Unit: "hash", State: docstate.Stale, Note: docstate.NoteHashError},
		{Unit: "none", State: docstate.Missing},
	})
	want := statusGroups{
		stale:   []string{"plain", "new", "link", "fm", "perm", "hash"},
		missing: []string{"none"},
		unusable: []string{
			"link (" + docstate.OutsideText + ")", "fm (broken frontmatter)",
			"perm (unreadable doc or sources)", "hash (unreadable doc or sources)",
		},
		oldScheme: 2,
	}
	if !slices.Equal(g.stale, want.stale) || !slices.Equal(g.missing, want.missing) || !slices.Equal(g.unusable, want.unusable) || g.oldScheme != want.oldScheme {
		t.Errorf("got %+v\nwant %+v", g, want)
	}
	if g := classifyStatuses(nil); g.stale != nil || g.missing != nil || g.unusable != nil || g.oldScheme != 0 {
		t.Errorf("empty: %+v", g)
	}
}

// A "%" in the plugin root never reaches a format string (Q-L1).
func TestSessionStartCLIWithPercent(t *testing.T) {
	env := newRepo(t, false)
	env.Agent, env.PluginRoot = Codex, "/opt/100%d/p"
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "language: \"bad lang\"\n")
	testutil.WriteFile(t, env.Root, "context/gone.md", "# gone\n")
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	logx.Errorf(env.Root, "stop: boom")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	v := decodeSession(t, out)
	all := v.SystemMessage + v.Hook.AdditionalContext
	if strings.Contains(all, "%!") || !strings.Contains(all, "invalid setting(s)") || !strings.Contains(all, "1 hook error(s)") {
		t.Errorf("out = %s", out)
	}
	if n := strings.Count(all, "'/opt/100%d/p/bin/auto-context' status"); n < 3 {
		t.Errorf("%d commands with the CLI, want 3+: %s", n, out)
	}
}

// Docs acked in the old hash scheme are counted with the upgrade command.
func TestSessionStartOldSchemeLine(t *testing.T) {
	env := newRepo(t, false)
	p, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hashing.SourceHash(context.Background(), env.Root, p.Tree().Files("svc"), nil, p.Cache())
	if err != nil {
		t.Fatal(err)
	}
	if err := frontmatter.SetHash(env.Root, filepath.Join(env.Root, "context", "svc.md"), "svc", h, "", env.Now()); err != nil {
		t.Fatal(err)
	}
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	c := decodeSession(t, out).Hook.AdditionalContext
	if !strings.Contains(c, "auto-context: 1 unit(s) were acked by an older auto-context; run `auto-context ack --upgrade`.") || strings.Contains(c, "stale") {
		t.Errorf("context = %q", c)
	}
}

// Invalid settings that decide the units pause the index and auto-ack: a
// fixed text says so, the index stays as it was (A-L8).
func TestSessionStartDiscoveryReset(t *testing.T) {
	env := newRepo(t, false)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "min_files: 0\n")
	before, _ := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md"))
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	c := decodeSession(t, out).Hook.AdditionalContext
	if !strings.Contains(c, "config.yaml has invalid settings that decide the units, so the units index and auto-ack are paused until it is fixed; run `auto-context status` to see them.") {
		t.Errorf("context = %q", c)
	}
	if strings.Contains(c, "min_files") || strings.Contains(c, "could not be updated") {
		t.Errorf("config text or generic index error: %q", c)
	}
	if after, _ := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md")); string(after) != string(before) {
		t.Errorf("index rewritten:\n%s", after)
	}
}

// SessionNotice and the orphan list cap the names they show (T-L3).
func TestSessionNoticeCaps(t *testing.T) {
	var stale []string
	for i := range 30 {
		stale = append(stale, fmt.Sprintf("u%02d", i))
	}
	got := SessionNotice(stale, []string{"m"}, 20, Claude)
	if !strings.Contains(got, "u19 (+11 more)") || strings.Contains(got, "u20") || strings.Contains(got, "missing") {
		t.Errorf("notice = %q", got)
	}
	env := newRepo(t, false)
	for i := range 25 {
		testutil.WriteFile(t, env.Root, fmt.Sprintf("context/gone%02d.md", i), "# g\n")
	}
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	c := decodeSession(t, out).Hook.AdditionalContext
	if !strings.Contains(c, "5 more (run `auto-context status`)") || strings.Contains(c, "gone20") {
		t.Errorf("context = %q", c)
	}
}
