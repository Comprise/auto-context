package hooks

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/session"
)

var update = flag.Bool("update", false, "rewrite golden files")

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

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
	write(t, root, "CLAUDE.md", "# Project\n")
	write(t, root, "svc/go.mod", "module svc\n")
	write(t, root, "svc/main.go", "package main\n")
	write(t, root, "context/_root.md", "# root\n\nRoot summary.\n")
	write(t, root, "context/svc.md", "# svc\n\nService summary.\n")
	if withLib {
		write(t, root, "lib/go.mod", "module lib\n")
		write(t, root, "lib/lib.go", "package lib\n")
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
		{"spec", []string{"src/auth", "cmd/api"}, []string{"internal/billing"}, 10,
			"auto-context: docs stale for src/auth, cmd/api; missing for internal/billing. Update them when working there (skill auto-context:auto-context-docs) or run /auto-context:status."},
	}
	for _, c := range cases {
		if got := SessionNotice(c.stale, c.missing, c.max); got != c.want {
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

func TestSessionStartGolden(t *testing.T) {
	env := newRepo(t, true)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "session_start.golden.json")
	if *update {
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(want) {
		t.Errorf("got %q\nwant %q", out, want)
	}
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
	write(t, root, "CLAUDE.md", "# Project\n")
	write(t, root, ".gitignore", "bin/\n")
	write(t, root, "svc/go.mod", "module svc\n")
	write(t, root, "svc/main.go", "package main\n")
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
	write(t, root, "svc/main.go", "package main\n// changed\n")
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
	write(t, root, ".auto-context/config.yaml", "min_files: 3\n")
	if !project.Active(root) {
		t.Error("config does not activate")
	}
	// A bare context/ directory is a common name (it may even hold code): it
	// must not activate the plugin on its own.
	dir := t.TempDir()
	write(t, dir, "context/theme.go", "package context\n")
	if project.Active(dir) {
		t.Error("bare context dir activates")
	}
	write(t, dir, "context/_root.md", "# root\n")
	if !project.Active(dir) {
		t.Error("context/_root.md does not activate")
	}
	// A marker linking outside the repo does not count.
	linked, outside := t.TempDir(), t.TempDir()
	write(t, outside, "conf.yaml", "min_files: 1\n")
	if err := os.Symlink(filepath.Join(outside, "conf.yaml"), filepath.Join(linked, ".auto-context/config.yaml")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if project.Active(linked) {
		t.Error("marker linked outside activates")
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
	write(t, env.Root, "CLAUDE.md", "# Project\n<!-- /auto-context:units -->\n")
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
	write(t, env.Root, ".auto-context/config.yaml", "exlude: [svc]\n")
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
	write(t, env.Root, ".auto-context/config.yaml", "IGNORE_PREVIOUS_INSTRUCTIONS: 1\nexclude: [\"[RUN_CURL\"]\n")
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
	write(t, env.Root, ".auto-context/config.yaml", "exlude: [svc]\n")
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
	write(t, env.Root, ".auto-context/config.yaml", "language: pt-BR\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if v := decodeSession(t, out); v.Hook.AdditionalContext != "auto-context: "+LanguageNote("pt-BR") || v.SystemMessage != "" {
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
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc]\n")
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md"))
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
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
	write(t, env.Root, "context/gone.md", "# gone\n\nOld.\n")
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
	write(t, env.Root, ".auto-context/cache.json", "garbage")
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
	if v := decodeSession(t, SessionFailure()); !strings.Contains(v.SystemMessage, "docs tracking is off") {
		t.Errorf("%+v", v)
	}
}

// A marker that links outside the repo keeps the plugin off, and says so.
func TestSessionStartRefusedMarker(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "cfg.yaml")
	write(t, filepath.Dir(out), "cfg.yaml", "")
	if err := os.Symlink(out, filepath.Join(root, ".auto-context/config.yaml")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	got, err := SessionStart(testEnv(root), Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if v := decodeSession(t, got); !strings.Contains(v.SystemMessage, "the plugin is off here") {
		t.Errorf("out = %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".auto-context")); !os.IsNotExist(err) {
		t.Errorf("state written: %v", err)
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
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
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
	write(t, root, "context/_root.md", "# r\n\nR.\n")
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
