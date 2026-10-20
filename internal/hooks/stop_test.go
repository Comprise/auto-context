package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/docstate"
	"auto-context/internal/fsutil"
	"auto-context/internal/session"
)

const stopSID = "stop-sess"

// turn builds the fixture repo and starts a turn with PromptSubmit.
func turn(t *testing.T) Env {
	t.Helper()
	env := newRepo(t, false)
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	return env
}

func stop(t *testing.T, env Env) []byte {
	t.Helper()
	out, err := Stop(env, Input{SessionID: stopSID})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// stopActive is a Stop following feedback: Claude Code sets stop_hook_active.
func stopActive(t *testing.T, env Env) []byte {
	t.Helper()
	out, err := Stop(env, Input{SessionID: stopSID, StopHookActive: true})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// feedback asserts out is Stop feedback via additionalContext and returns its text.
func feedback(t *testing.T, out []byte) string {
	t.Helper()
	var v struct {
		Decision string `json:"decision"`
		Hook     struct {
			EventName         string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("bad output %q: %v", out, err)
	}
	if v.Decision != "" || v.Hook.EventName != "Stop" || v.Hook.AdditionalContext == "" {
		t.Fatalf("want Stop additionalContext feedback, got %q", out)
	}
	return v.Hook.AdditionalContext
}

func stateFileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func unitState(t *testing.T, env Env, unit string) docstate.State {
	t.Helper()
	p, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	statuses := p.Statuses()
	for _, s := range statuses {
		if s.Unit == unit {
			return s.State
		}
	}
	t.Fatalf("unit %s not found", unit)
	return ""
}

// Claude Code sets stop_hook_active on every Stop after feedback; docs edits
// made in response must still be processed and auto-acked.
func TestStopHookActiveAfterFeedbackAutoAcks(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env))
	write(t, env.Root, "context/svc.md", "# svc\n\nService summary, now longer.\n")
	if out := stopActive(t, env); out != nil {
		t.Fatalf("out=%q, want nil", out)
	}
	if st := unitState(t, env, "svc"); st != docstate.OK {
		t.Errorf("svc state = %s, want ok", st)
	}
}

func TestStopHookActiveFlagsNewChange(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env))
	write(t, env.Root, "tool.go", "package main\n")
	r := feedback(t, stopActive(t, env))
	if !strings.Contains(r, "code changed in _root ") || strings.Contains(r, "svc") {
		t.Errorf("reason = %q, want only _root", r)
	}
}

func TestStopWithoutSnapshot(t *testing.T) {
	env := newRepo(t, false)
	if out := stop(t, env); out != nil {
		t.Fatalf("out=%q, want nil", out)
	}
	snap, err := session.LoadSnapshot(env.Root, stopSID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["svc/main.go"]; !ok {
		t.Errorf("snapshot lacks svc/main.go: %v", snap)
	}
}

// A clean Stop keeps the turn-start snapshot: another Stop hook may make
// Claude continue, and edits made then must still be compared with it.
func TestStopNoChangesAllows(t *testing.T) {
	env := turn(t)
	if out := stop(t, env); out != nil {
		t.Fatalf("out=%q, want nil", out)
	}
	if !stateFileExists(t, session.SnapshotPath(env.Root, stopSID)) {
		t.Error("snapshot removed by a clean Stop")
	}
}

func TestStopCleanThenEditGivesFeedback(t *testing.T) {
	env := turn(t)
	if out := stop(t, env); out != nil {
		t.Fatalf("first stop out=%q", out)
	}
	write(t, env.Root, "svc/main.go", "package main\n// later edit\n")
	if r := feedback(t, stopActive(t, env)); !strings.Contains(r, "code changed in svc ") {
		t.Errorf("reason = %q", r)
	}
}

// setMtime sets rel's mtime to base + d, to order edits deterministically.
func setMtime(t *testing.T, root, rel string, base time.Time, d time.Duration) {
	t.Helper()
	ts := base.Add(d)
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), ts, ts); err != nil {
		t.Fatal(err)
	}
}

// Docs edited before the code edit in the same turn do not cover it.
func TestStopDocsBeforeCodeIsNotDocsUpdate(t *testing.T) {
	env := turn(t)
	base := time.Now().Add(time.Hour)
	write(t, env.Root, "context/svc.md", "# svc\n\nService summary, edited first.\n")
	setMtime(t, env.Root, "context/svc.md", base, 0)
	write(t, env.Root, "svc/main.go", "package main\nfunc New() {}\n")
	setMtime(t, env.Root, "svc/main.go", base, time.Second)
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc ") {
		t.Errorf("reason = %q", r)
	}
	if st := unitState(t, env, "svc"); st != docstate.Stale {
		t.Errorf("svc state = %s, want stale", st)
	}
}

// A failed auto-ack (here: the context doc links to a non-.md file) must not
// silence the feedback.
func TestStopFailedAutoAckGivesFeedback(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	write(t, env.Root, "notes.txt", "# svc\n\nx\n")
	ctx := filepath.Join(env.Root, "context", "svc.md")
	if err := os.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../notes.txt", ctx); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	r := feedback(t, stop(t, env))
	if !strings.Contains(r, "context/svc.md cannot be used") || strings.Contains(r, "New unit svc") {
		t.Errorf("reason = %q", r)
	}
}

// A real auto-ack failure (an unreadable source) is reported as such, not as
// "docs not updated": the docs were updated.
func TestStopAckFailureIsReported(t *testing.T) {
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("file permissions do not deny reads here")
	}
	env := turn(t)
	base := time.Now().Add(time.Hour)
	write(t, env.Root, "svc/extra.go", "package main\n")
	if err := os.Chmod(filepath.Join(env.Root, "svc", "extra.go"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(env.Root, "svc", "extra.go"), 0o644)
	setMtime(t, env.Root, "svc/extra.go", base, 0)
	write(t, env.Root, "context/svc.md", "# svc\n\nUpdated.\n")
	setMtime(t, env.Root, "context/svc.md", base, time.Second)
	r := feedback(t, stop(t, env))
	if !strings.Contains(r, "auto-ack of svc failed") || strings.Contains(r, "docs not updated") {
		t.Errorf("reason = %q", r)
	}
}

// An edit undone within the turn leaves the docs up to date: no feedback.
func TestStopNoFeedbackWhenDocsStillMatch(t *testing.T) {
	env := turn(t)
	orig, err := os.ReadFile(filepath.Join(env.Root, "svc", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, env.Root, "svc/main.go", "package main\n// temporary\n")
	write(t, env.Root, "svc/main.go", string(orig))
	setMtime(t, env.Root, "svc/main.go", time.Now().Add(time.Hour), 0)
	if out := stop(t, env); out != nil {
		t.Errorf("out = %s", out)
	}
}

// Without a turn-start snapshot the turn is not tracked, and the log says so.
func TestStopWithoutSnapshotLogs(t *testing.T) {
	env := newRepo(t, false)
	stop(t, env)
	data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "log"))
	if !strings.Contains(string(data), "no turn-start snapshot") {
		t.Errorf("log = %q", data)
	}
}

func TestStopFeedbackOnCodeEdit(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	out := stop(t, env)
	want := `{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"auto-context: code changed in svc `
	if !strings.HasPrefix(string(out), want) {
		t.Fatalf("out=%q, want prefix %q", out, want)
	}
	if r := feedback(t, out); !strings.Contains(r, "code changed in svc ") {
		t.Errorf("reason lacks svc: %q", r)
	}
	if j, _ := session.LoadJournal(env.Root, stopSID); !slices.Equal(j.AlreadyNotified, []string{"svc"}) {
		t.Errorf("AlreadyNotified = %v, want [svc]", j.AlreadyNotified)
	}
}

func TestStopSecondCallAllowsAfterNotify(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env))
	if out := stopActive(t, env); out != nil {
		t.Fatalf("second Stop out=%q, want nil", out)
	}
}

func TestStopAllowsAfterDocsEdited(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env))
	write(t, env.Root, "context/svc.md", "# svc\n\nService summary, now longer.\n")
	if out := stopActive(t, env); out != nil {
		t.Fatalf("out=%q, want nil", out)
	}
	if st := unitState(t, env, "svc"); st != docstate.OK {
		t.Errorf("svc state = %s, want ok", st)
	}
}

func TestStopBashRename(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, "svc/a.go", "package main\n")
	ack(t, env, "svc")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(env.Root, "svc", "a.go"), filepath.Join(env.Root, "svc", "b.go")); err != nil {
		t.Fatal(err)
	}
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc ") {
		t.Errorf("reason lacks svc: %q", r)
	}
}

func TestStopAckFlow(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env))
	ack(t, env, "svc")
	if out := stopActive(t, env); out != nil {
		t.Fatalf("out=%q, want nil", out)
	}
}

func TestStopNewUnitAndExclude(t *testing.T) {
	env := turn(t)
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		write(t, env.Root, "pkg/new/"+n+".go", "package new\n")
	}
	r := feedback(t, stop(t, env))
	if !strings.Contains(r, "auto-context ack --exclude pkg/new") {
		t.Fatalf("reason lacks exclude hint: %q", r)
	}
	// A new unit changes _root's unit list: _root is reported with it.
	if !strings.Contains(r, "code changed in _root ") {
		t.Errorf("reason lacks _root: %q", r)
	}
	if err := config.AddExclude(env.Root, "pkg/new"); err != nil {
		t.Fatal(err)
	}
	// pkg/new files now belong to _root, which was already notified.
	if out := stopActive(t, env); out != nil {
		t.Errorf("second Stop out = %s", out)
	}
}

func TestStopUnitDirDeleted(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, "svc/auth/go.mod", "module auth\n")
	write(t, env.Root, "svc/auth/auth.go", "package auth\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(env.Root, "svc", "auth")); err != nil {
		t.Fatal(err)
	}
	// svc's own sources did not change, so Stop has nothing to say about svc;
	// the removed unit's docs are reported as orphans at the next SessionStart.
	if out := stop(t, env); out != nil {
		t.Errorf("stop out = %s", out)
	}
}

func TestSessionStartReportsOrphanDocs(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, "context/gone.md", "# gone\n\nOld.\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if c := decodeSession(t, out).Hook.AdditionalContext; !strings.Contains(c, "context/gone.md") {
		t.Errorf("context = %q", c)
	}
}

func TestStopReasonText(t *testing.T) {
	want := "auto-context: code changed in src/auth, cmd/api but docs not updated. If the change affects purpose, public API, structure or key behavior — update context/<unit>.md (and <unit>/CLAUDE.md if its purpose or key files changed) per skill auto-context:auto-context-docs. Otherwise run `auto-context ack src/auth cmd/api`. New unit internal/billing: ask the user, then create its docs or run `auto-context ack --exclude internal/billing`."
	if got := StopReason(Feedback{Changed: []string{"src/auth", "cmd/api"}, New: []string{"internal/billing"}}); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	wantRoot := "auto-context: New unit _root: create context/_root.md (and the root CLAUDE.md purpose line). New unit a: ask the user, then create its docs or run `auto-context ack --exclude a`."
	if got := StopReason(Feedback{New: []string{"_root", "a"}}); got != wantRoot {
		t.Errorf("got  %q\nwant %q", got, wantRoot)
	}
	wantNew := "auto-context: New unit a: ask the user, then create its docs or run `auto-context ack --exclude a`. New unit b: ask the user, then create its docs or run `auto-context ack --exclude b`."
	if got := StopReason(Feedback{New: []string{"a", "b"}}); got != wantNew {
		t.Errorf("got  %q\nwant %q", got, wantNew)
	}
	got := StopReason(Feedback{Failed: map[string]string{"x": "permission denied"}, Refused: []string{"y"}})
	for _, w := range []string{"auto-ack of x failed: permission denied", "context/y.md cannot be used"} {
		if !strings.Contains(got, w) {
			t.Errorf("got %q, lacks %q", got, w)
		}
	}
}

func TestStopOutputNotHTMLEscaped(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	out := stop(t, env)
	if !strings.Contains(string(out), "<unit>/CLAUDE.md") || strings.HasSuffix(string(out), "\n") {
		t.Errorf("unexpected encoding: %q", out)
	}
}

// Spec п. 16 end-to-end: edit code, feedback, edit docs, quiet stop, quiet SessionStart.
func TestStopEndToEnd(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env))
	write(t, env.Root, "context/svc.md", "# svc\n\nService summary, now longer.\n")
	if out := stopActive(t, env); out != nil {
		t.Fatalf("out=%q, want nil", out)
	}
	if st := unitState(t, env, "svc"); st != docstate.OK {
		t.Fatalf("svc state = %s, want ok", st)
	}
	out, err := SessionStart(env, Input{SessionID: stopSID})
	if err != nil || out != nil {
		t.Fatalf("SessionStart out=%q err=%v, want nil", out, err)
	}
}

// Stop's own root index rewrite must not auto-ack a stale _root on the next Stop.
func TestStopIgnoresOwnRootIndexRewrite(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	write(t, env.Root, "context/svc.md", "# svc\n\nA different service summary.\n")
	write(t, env.Root, "tool.go", "package main\n")
	r := feedback(t, stop(t, env))
	if !strings.Contains(r, "code changed in _root ") {
		t.Fatalf("Stop1 reason lacks _root: %q", r)
	}
	if out := stopActive(t, env); out != nil {
		t.Fatalf("Stop2 out=%q, want nil (_root already notified)", out)
	}
	if st := unitState(t, env, "_root"); st != docstate.Stale {
		t.Errorf("_root state after Stop2 = %s, want stale", st)
	}
}

// Deleting context/<u>.md is not a docs update: the unit stays pending as a new unit.
func TestStopDeletedContextIsNotDocsUpdate(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	if err := os.Remove(filepath.Join(env.Root, "context", "svc.md")); err != nil {
		t.Fatal(err)
	}
	r := feedback(t, stop(t, env))
	if !strings.Contains(r, "New unit svc:") {
		t.Errorf("reason = %q, want new unit svc", r)
	}
}

// A mid-turn SessionStart (compact) rewrites the root index; the next Stop must
// not treat that as a _root docs edit.
func TestStopIgnoresSessionStartRootIndexRewrite(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "tool.go", "package main\n")
	write(t, env.Root, "context/svc.md", "# svc\n\nA different service summary.\n")
	if _, err := SessionStart(env, Input{SessionID: stopSID, Source: "compact"}); err != nil {
		t.Fatal(err)
	}
	r := feedback(t, stop(t, env))
	if !strings.Contains(r, "code changed in _root ") {
		t.Errorf("reason lacks _root: %q", r)
	}
	if st := unitState(t, env, "_root"); st != docstate.Stale {
		t.Errorf("_root state = %s, want stale", st)
	}
}

// Editing only <unit>/CLAUDE.md is not a docs update: it must not auto-ack the
// unit or suppress the feedback.
func TestStopClaudeMDOnlyIsNotDocsUpdate(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\nfunc NewPublicAPI() {}\n")
	write(t, env.Root, "svc/CLAUDE.md", "# svc\ntypo fix\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc ") {
		t.Errorf("reason lacks svc: %q", r)
	}
	if st := unitState(t, env, "svc"); st != docstate.Stale {
		t.Errorf("svc state = %s, want stale", st)
	}
}

// Docs saved in the same timestamp tick as the code count as updated.
func TestStopEqualMtimesCount(t *testing.T) {
	env := turn(t)
	base := time.Now().Add(time.Hour)
	write(t, env.Root, "svc/main.go", "package main\nfunc G() {}\n")
	write(t, env.Root, "context/svc.md", "# svc\n\nService summary.\n")
	setMtime(t, env.Root, "svc/main.go", base, 0)
	setMtime(t, env.Root, "context/svc.md", base, 0)
	if out := stop(t, env); out != nil {
		t.Errorf("out = %s", out)
	}
	if st := unitState(t, env, "svc"); st != docstate.OK {
		t.Errorf("svc = %s", st)
	}
}

// Auto-ack refreshes the root index with the new summary line.
func TestStopRefreshesIndexAfterAutoAck(t *testing.T) {
	env := turn(t)
	base := time.Now().Add(time.Hour)
	write(t, env.Root, "svc/main.go", "package main\nfunc H() {}\n")
	setMtime(t, env.Root, "svc/main.go", base, 0)
	write(t, env.Root, "context/svc.md", "# svc\n\nA brand new summary.\n")
	setMtime(t, env.Root, "context/svc.md", base, time.Second)
	stop(t, env)
	data, _ := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md"))
	if !strings.Contains(string(data), "- `svc` — A brand new summary.") {
		t.Errorf("index not refreshed:\n%s", data)
	}
}

func TestStopNamesDocsLanguage(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, ".auto-context/config.yaml", "language: ru\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "in language ru") {
		t.Errorf("reason = %q", r)
	}
}

// A turn that activates the project (init writes context/_root.md) had no
// snapshot to take: that is not a hook error.
func TestStopActivationTurnLogsNothing(t *testing.T) {
	root := t.TempDir()
	env := testEnv(root)
	write(t, root, "svc/go.mod", "module svc\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	write(t, root, "context/_root.md", "# root\n\nRoot.\n")
	stop(t, env)
	if data, err := os.ReadFile(filepath.Join(root, ".auto-context", "log")); err == nil {
		t.Errorf("log = %q", data)
	}
	if !stateFileExists(t, session.SnapshotPath(root, stopSID)) {
		t.Error("no baseline snapshot saved")
	}
}

// With an unparsable config the units come from defaults: Stop acks and
// reports nothing about units, but says once that tracking is paused; the
// turn's changes are checked once the config is fixed.
func TestStopBrokenConfigPausesTracking(t *testing.T) {
	env := turn(t)
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "docs tracking is paused") || strings.Contains(r, "code changed") {
		t.Errorf("reason = %q", r)
	}
	if out := stop(t, env); out != nil {
		t.Errorf("second Stop out = %s", out)
	}
	// The next prompt keeps the snapshot while the config is broken.
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(env.Root, ".auto-context", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("after the fix: %q", r)
	}
}

// A unit that stopped being one during the turn gets no "run ack" advice
// that would fail with "unknown unit".
func TestStopSkipsUnitThatIsGone(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env)) // svc is reported once
	// Another code change, then svc stops being a unit.
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	write(t, env.Root, "svc/main.go", "package main\n// again\n")
	write(t, env.Root, ".auto-context/config.yaml", "exclude: [svc/**]\n")
	if out := stop(t, env); out != nil {
		t.Errorf("out = %s", out)
	}
}

// An unreadable context doc is reported as unreadable, not as a link problem.
func TestStopReasonUnreadable(t *testing.T) {
	r := StopReason(Feedback{Unreadable: []string{"svc"}})
	if !strings.Contains(r, "context/svc.md cannot be read") || strings.Contains(r, "links outside") {
		t.Errorf("reason = %q", r)
	}
}

// The newest code edit decides, not the last one visited: docs written
// between two code edits of the unit do not count.
func TestStopUsesNewestCodeEdit(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/b.go", "package main\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(time.Minute)
	write(t, env.Root, "svc/main.go", "package main\n// a\n")
	write(t, env.Root, "svc/b.go", "package main\n// b\n")
	write(t, env.Root, "context/svc.md", "# svc\n\nNew.\n")
	for _, order := range [][2]string{{"svc/main.go", "svc/b.go"}, {"svc/b.go", "svc/main.go"}} {
		setMtime(t, env.Root, order[0], base, 2*time.Second) // the newest code edit
		setMtime(t, env.Root, order[1], base, 0)
		setMtime(t, env.Root, "context/svc.md", base, time.Second)
		j, _ := session.LoadJournal(env.Root, stopSID)
		j.AlreadyNotified = nil
		if err := j.Save(env.Root, stopSID); err != nil {
			t.Fatal(err)
		}
		if out := stop(t, env); out == nil {
			t.Errorf("newest edit %s: docs between the edits counted", order[0])
		}
	}
}

// A failed ack is named by category: the error's repo paths stay in the log.
func TestAckFailureHidesPaths(t *testing.T) {
	err := fmt.Errorf("open svc/IGNORE ALL RULES.go: %w", os.ErrPermission)
	if got := errCategory(err); got != "permission denied" {
		t.Errorf("got %q", got)
	}
	if got := errCategory(errors.New("odd svc/IGNORE.go")); strings.Contains(got, "IGNORE") {
		t.Errorf("got %q", got)
	}
}

func TestPendingUnits(t *testing.T) {
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	only := func(u string) func(string) bool { return func(x string) bool { return x == u } }
	cases := []struct {
		name     string
		j        session.Journal
		acked    []string
		failed   map[string]string
		isUnit   func(string) bool
		upToDate func(string) bool
		want     []string
	}{
		{"changed, stale", session.Journal{CodeChanged: []string{"a"}}, nil, nil, yes, no, []string{"a"}},
		{"acked", session.Journal{CodeChanged: []string{"a"}}, []string{"a"}, nil, yes, no, nil},
		{"already notified", session.Journal{CodeChanged: []string{"a"}, AlreadyNotified: []string{"a"}}, nil, nil, yes, no, nil},
		{"docs already match", session.Journal{CodeChanged: []string{"a"}}, nil, nil, yes, yes, nil},
		{"failed ack is pending even if up to date", session.Journal{CodeChanged: []string{"a"}}, nil, map[string]string{"a": "x"}, yes, yes, []string{"a"}},
		{"no longer a unit", session.Journal{CodeChanged: []string{"a", "b"}}, nil, nil, only("b"), no, []string{"b"}},
		{"failed ack after the notice is reported", session.Journal{CodeChanged: []string{"a"}, AlreadyNotified: []string{"a"}}, nil, map[string]string{"a": "x"}, yes, no, []string{"a"}},
		{"failed ack reported once", session.Journal{CodeChanged: []string{"a"}, AlreadyNotified: []string{"a"}, AckFailNotified: []string{"a"}}, nil, map[string]string{"a": "x"}, yes, no, nil},
		{"failed ack without a code change", session.Journal{}, nil, map[string]string{"b": "x"}, yes, no, []string{"b"}},
	}
	for _, c := range cases {
		if got := pendingUnits(c.j.CodeChanged, c.j.AlreadyNotified, c.j.AckFailNotified, c.acked, c.failed, c.isUnit, c.upToDate); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBuildFeedback(t *testing.T) {
	kinds := map[string]docstate.DocKind{"new": docstate.DocAbsent, "link": docstate.DocRefused, "perm": docstate.DocUnreadable, "doc": docstate.DocPresent, "fail": docstate.DocPresent}
	f := buildFeedback([]string{"new", "link", "perm", "doc", "fail"}, map[string]string{"fail": "permission denied"},
		func(u string) docstate.DocKind { return kinds[u] }, "ru")
	if !slices.Equal(f.New, []string{"new"}) || !slices.Equal(f.Refused, []string{"link"}) || !slices.Equal(f.Unreadable, []string{"perm"}) ||
		!slices.Equal(f.Changed, []string{"doc"}) || f.Failed["fail"] != "permission denied" || f.Language != "ru" {
		t.Errorf("%+v", f)
	}
}

// recordTurn: code edits go to the journal, docs count only if not older than
// the unit's newest code edit; CLAUDE.md edits never count.
func TestRecordTurn(t *testing.T) {
	env := newRepo(t, true)
	p, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	before := session.Snapshot{
		"svc/main.go": {MtimeNs: 1, Size: 1}, "lib/lib.go": {MtimeNs: 1, Size: 1},
		"context/svc.md": {MtimeNs: 1, Size: 1}, "context/lib.md": {MtimeNs: 1, Size: 1}, "svc/CLAUDE.md": {MtimeNs: 1, Size: 1},
	}
	cur := session.Snapshot{
		"svc/main.go": {MtimeNs: 10, Size: 1}, "lib/lib.go": {MtimeNs: 10, Size: 1},
		"context/svc.md": {MtimeNs: 20, Size: 1}, // after svc's code: counts
		"context/lib.md": {MtimeNs: 5, Size: 1},  // before lib's code: does not
		"svc/CLAUDE.md":  {MtimeNs: 30, Size: 2},
	}
	var j session.Journal
	fresh := recordTurn(p.Tree(), before, cur, &j)
	if !slices.Equal(fresh, []string{"svc"}) || !slices.Equal(j.CodeChanged, []string{"lib", "svc"}) || !slices.Equal(j.DocsTouched, []string{"lib", "svc"}) {
		t.Errorf("fresh=%v journal=%+v", fresh, j)
	}
}

// A context doc that links to a .md elsewhere in the repo counts as updated
// when its target is edited.
func TestStopSymlinkedContextDoc(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	env := newRepo(t, false)
	write(t, env.Root, "docs/svc.md", "# svc\n\nService summary.\n")
	ctx := filepath.Join(env.Root, "context", "svc.md")
	if err := os.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../docs/svc.md", ctx); err != nil {
		t.Fatal(err)
	}
	ack(t, env, "svc")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(time.Minute)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	setMtime(t, env.Root, "svc/main.go", base, 0)
	write(t, env.Root, "docs/svc.md", "# svc\n\nService summary, updated.\n")
	setMtime(t, env.Root, "docs/svc.md", base, time.Second)
	if out := stop(t, env); out != nil {
		t.Errorf("edit of the link target not seen: %s", out)
	}
}

// A config with one invalid setting is not "broken": Stop still works.
func TestStopWorksWithConfigProblems(t *testing.T) {
	env := turn(t)
	write(t, env.Root, ".auto-context/config.yaml", "exlude: [x]\n")
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("reason = %q", r)
	}
}

// Editing CLAUDE.md next to the code is not a docs update.
func TestStopClaudeMDWithCodeIsNotDocsUpdate(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	write(t, env.Root, "svc/CLAUDE.md", "# svc\nNew purpose.\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("reason = %q", r)
	}
}

// A snapshot without the header or with garbage is logged and replaced, so
// later Stops of the turn work.
func TestStopReplacesBadSnapshots(t *testing.T) {
	for name, content := range map[string]string{"old": `{"svc/main.go":{"m":1,"s":2}}`, "corrupt": "garbage"} {
		t.Run(name, func(t *testing.T) {
			env := turn(t)
			if err := os.WriteFile(session.SnapshotPath(env.Root, stopSID), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := Stop(env, Input{SessionID: stopSID}); out != nil || err != nil {
				t.Fatalf("%s, %v", out, err)
			}
			if _, err := session.LoadSnapshot(env.Root, stopSID); err != nil {
				t.Errorf("snapshot not replaced: %v", err)
			}
			data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "log"))
			if !strings.Contains(string(data), "snapshot unreadable") {
				t.Errorf("log = %q", data)
			}
		})
	}
}

func TestAckFailureCategories(t *testing.T) {
	cases := map[error]string{
		fmt.Errorf("x: %w", fsutil.ErrOutside):    "a file links outside the repository",
		fmt.Errorf("x: %w", fsutil.ErrNotRegular): "a source or doc is not a regular file",
		fmt.Errorf("x: %w", fsutil.ErrTooLarge):   "the context doc is too large",
		fmt.Errorf("x: %w", os.ErrPermission):     "permission denied",
		fmt.Errorf("x: %w", os.ErrNotExist):       "a file disappeared",
		errors.New("odd"):                         "see .auto-context/log",
	}
	for err, want := range cases {
		if got := errCategory(err); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}

// Names with spaces are quoted in suggested commands, and long lists are cut.
func TestStopReasonQuotesAndCaps(t *testing.T) {
	if r := StopReason(Feedback{Changed: []string{"my dir"}}); !strings.Contains(r, "`auto-context ack 'my dir'`") {
		t.Errorf("not quoted: %q", r)
	}
	var many []string
	for i := range 30 {
		many = append(many, fmt.Sprintf("u%02d", i))
	}
	r := StopReason(Feedback{Changed: many})
	if !strings.Contains(r, "(+10 more)") || strings.Contains(r, "u25") {
		t.Errorf("not capped: %q", r)
	}
}

// A config fixed between turns keeps the snapshot from before it broke, so
// the changes made meanwhile are reported.
func TestStopConfigFixedBetweenTurns(t *testing.T) {
	env := turn(t)
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	write(t, env.Root, "svc/main.go", "package main\n// changed while broken\n")
	feedback(t, stop(t, env)) // the paused notice
	if err := os.Remove(filepath.Join(env.Root, ".auto-context", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("reason = %q", r)
	}
}

// Broken from the session's first prompt: the turn after the fix is tracked
// and no false "no turn-start snapshot" error is logged.
func TestStopConfigBrokenFromStart(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	feedback(t, stop(t, env)) // paused notice
	if err := os.Remove(filepath.Join(env.Root, ".auto-context", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	write(t, env.Root, "svc/main.go", "package main\n// changed after the fix\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("reason = %q", r)
	}
	if data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "log")); strings.Contains(string(data), "no turn-start snapshot") {
		t.Errorf("false error logged: %s", data)
	}
}

// A unit notified before the pause is reported again for changes made during
// it; the pause notice comes again on a second breakage; after resuming,
// the next turn takes a fresh snapshot.
func TestStopPauseResumeCycle(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// one\n")
	feedback(t, stop(t, env)) // svc notified
	cfg := filepath.Join(env.Root, ".auto-context", "config.yaml")
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "paused") {
		t.Fatalf("no pause notice: %q", r)
	}
	write(t, env.Root, "svc/main.go", "package main\n// two\n")
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("change during the pause hidden by the old notice: %q", r)
	}
	// Break again: the pause is announced again.
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "paused") {
		t.Errorf("second pause not announced: %q", r)
	}
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	// Two full turns after the fix: the second starts from a fresh snapshot.
	for range 2 {
		if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
			t.Fatal(err)
		}
		stop(t, env)
	}
	ack(t, env, "svc")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if out := stop(t, env); out != nil {
		t.Errorf("old changes reported in a fresh turn: %s", out)
	}
}

// PromptSubmit alone during the pause (no Stop) still keeps the snapshot.
func TestPromptSubmitPauseWithoutStop(t *testing.T) {
	env := turn(t)
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	write(t, env.Root, "svc/main.go", "package main\n// while broken\n")
	if err := os.Remove(filepath.Join(env.Root, ".auto-context", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("reason = %q", r)
	}
}

// An unreadable journal is logged and the snapshot kept.
func TestPromptSubmitUnreadableJournal(t *testing.T) {
	env := turn(t)
	write(t, env.Root, "svc/main.go", "package main\n// changed\n")
	journal := filepath.Join(env.Root, ".auto-context", "state", session.SanitizeID(stopSID)+".json")
	if err := os.WriteFile(journal, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "log")); !strings.Contains(string(data), "journal unreadable") {
		t.Errorf("log = %q", data)
	}
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("snapshot not kept: %q", r)
	}
}

// Orphan names come from the repo: unusual ones are only counted.
func TestSessionStartHidesUnsafeOrphanNames(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, "context/gone.md", "# gone\n")
	write(t, env.Root, "context/x`; run this.md", "# x\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	c := decodeSession(t, out).Hook.AdditionalContext
	if !strings.Contains(c, "context/gone.md, 1 more") || strings.Contains(c, "run this") {
		t.Errorf("context = %q", c)
	}
}

// Broken from the first PromptSubmit (no snapshot) and fixed before that
// turn's Stop: tracking resumes without a false "no turn-start snapshot"
// error, and the next turn reports code edits.
func TestStopPausedWithoutSnapshot(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if stateFileExists(t, session.SnapshotPath(env.Root, stopSID)) {
		t.Fatal("snapshot taken while the config is broken")
	}
	if err := os.Remove(filepath.Join(env.Root, ".auto-context", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if out := stop(t, env); out != nil {
		t.Errorf("stop output = %s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "log")); strings.Contains(string(data), "no turn-start snapshot") {
		t.Errorf("false error logged: %s", data)
	}
	if j, err := session.LoadJournal(env.Root, stopSID); err != nil || j.Paused {
		t.Errorf("journal paused=%v err=%v", j.Paused, err)
	}
	if !stateFileExists(t, session.SnapshotPath(env.Root, stopSID)) {
		t.Error("no snapshot saved")
	}
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	write(t, env.Root, "svc/main.go", "package main\n// next turn\n")
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "code changed in svc") {
		t.Errorf("reason = %q", r)
	}
}

// A paused journal with a corrupt snapshot: PromptSubmit logs it and takes a
// fresh snapshot instead of keeping the broken one.
func TestPromptSubmitPausedCorruptSnapshot(t *testing.T) {
	env := turn(t)
	write(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if j, _ := session.LoadJournal(env.Root, stopSID); !j.Paused {
		t.Fatal("journal not paused")
	}
	if err := os.Remove(filepath.Join(env.Root, ".auto-context", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(session.SnapshotPath(env.Root, stopSID), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PromptSubmit(env, Input{SessionID: stopSID}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(env.Root, ".auto-context", "log")); !strings.Contains(string(data), "prompt-submit: snapshot unreadable") {
		t.Errorf("log = %q", data)
	}
	if _, err := session.LoadSnapshot(env.Root, stopSID); err != nil {
		t.Errorf("no fresh snapshot: %v", err)
	}
}
