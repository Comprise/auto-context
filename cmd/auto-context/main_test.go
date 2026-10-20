package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/gitx"
	"auto-context/internal/hooks"

	"gopkg.in/yaml.v3"
)

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

func repo(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "# Project\n")
	write(t, root, "svc/go.mod", "module svc\n")
	write(t, root, "svc/main.go", "package main\n")
	write(t, root, "context/_root.md", "# root\n\nRoot summary.\n")
	return root
}

func do(cwd, stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb, cwd)
	return code, out.String(), errb.String()
}

func TestHookPanicIsSilent(t *testing.T) {
	root := repo(t)
	code, out, _ := do(root, "not json", "hook", "stop")
	if code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	data, err := os.ReadFile(filepath.Join(root, ".auto-context", "log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != 1 {
		t.Fatalf("log lines = %d: %q", n, data)
	}
}

func TestHookUnknownEvent(t *testing.T) {
	root := repo(t)
	code, out, _ := do(root, `{}`, "hook", "bogus")
	if code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHookStopEndToEnd(t *testing.T) {
	root := repo(t)
	if c, _, e := do(root, "", "ack", "_root", "svc"); c != 1 {
		t.Fatalf("ack svc without doc should fail, code=%d %s", c, e)
	}
	if c, _, e := do(root, "", "ack", "."); c != 0 {
		t.Fatalf("ack . code=%d %s", c, e)
	}
	in := `{"session_id":"s1","cwd":` + quote(root) + `}`
	if c, out, _ := do(t.TempDir(), in, "hook", "prompt-submit"); c != 0 || out != "" {
		t.Fatalf("prompt-submit code=%d out=%q", c, out)
	}
	write(t, root, "svc/main.go", "package main\n\nfunc main() {}\n")
	code, out, _ := do(t.TempDir(), in, "hook", "stop")
	if code != 0 || !strings.HasPrefix(out, `{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"auto-context: New unit svc`) || !strings.HasSuffix(out, "\n") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	// Claude writes the docs; the follow-up Stop carries stop_hook_active and auto-acks.
	write(t, root, "context/svc.md", "# svc\n\nService summary.\n")
	active := `{"session_id":"s1","stop_hook_active":true,"cwd":` + quote(root) + `}`
	if c, out, _ := do(t.TempDir(), active, "hook", "stop"); c != 0 || out != "" {
		t.Fatalf("post-feedback stop code=%d out=%q", c, out)
	}
	if _, out, _ := do(root, "", "status", "--stale"); out != "all docs up to date\n" {
		t.Fatalf("status after auto-ack: %q", out)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func TestAckUnknownUnitExit1(t *testing.T) {
	root := repo(t)
	code, _, errs := do(root, "", "ack", "nope", ".")
	if code != 1 || !strings.Contains(errs, "unknown unit nope") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	// the remaining unit is still processed
	if _, out, _ := do(root, "", "status"); !strings.Contains(out, "ok") {
		t.Fatalf("_root not acked: %q", out)
	}
}

func TestAckUsage(t *testing.T) {
	if code, _, _ := do(repo(t), "", "ack"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestAckExcludeWritesConfig(t *testing.T) {
	root := repo(t)
	code, out, _ := do(root, "", "ack", "--exclude", "svc/")
	if code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	data, err := os.ReadFile(filepath.Join(root, ".auto-context/config.yaml"))
	if err != nil || !strings.Contains(string(data), "svc") {
		t.Fatalf("err=%v data=%q", err, data)
	}
}

func TestStatusStaleFilter(t *testing.T) {
	root := repo(t)
	do(root, "", "ack", ".")
	code, out, _ := do(root, "", "status", "--stale")
	if code != 0 || !strings.Contains(out, "missing") || strings.Contains(out, "ok") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if !strings.HasPrefix(out, "UNIT") || !strings.Contains(out, "context/svc.md") {
		t.Fatalf("out=%q", out)
	}
}

func TestStatusAllUpToDate(t *testing.T) {
	root := repo(t)
	write(t, root, "context/svc.md", "# svc\n\nSummary.\n")
	do(root, "", "ack", ".", "svc")
	_, out, _ := do(root, "", "status", "--stale")
	if out != "all docs up to date\n" {
		t.Fatalf("out=%q", out)
	}
}

func TestUnitsOutput(t *testing.T) {
	_, out, _ := do(repo(t), "", "units")
	if !strings.Contains(out, "svc\tmodule\t2\n") || !strings.HasPrefix(out, "_root\troot\t") {
		t.Fatalf("out=%q", out)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := do(t.TempDir(), "", "version")
	if code != 0 || out != "dev\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errs := do(t.TempDir(), "", "bogus")
	if code != 2 || !strings.Contains(errs, "usage") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestHookWrongArgCountExits0(t *testing.T) {
	root := repo(t)
	for _, args := range [][]string{{"hook"}, {"hook", "stop", "extra"}} {
		if code, out, _ := do(root, `{}`, args...); code != 0 || out != "" {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}
}

func TestHookGetwdFailureExits0(t *testing.T) {
	fail := func() (string, error) { return "", errors.New("no cwd") }
	var out, errb bytes.Buffer
	if code := start([]string{"hook", "stop"}, strings.NewReader("{}"), &out, &errb, fail); code != 0 || out.Len()+errb.Len() != 0 {
		t.Fatalf("hook: code=%d out=%q err=%q", code, out.String(), errb.String())
	}
	if code := start([]string{"status"}, strings.NewReader(""), &out, &errb, fail); code != 1 {
		t.Fatalf("status: code=%d", code)
	}
}

func TestHookInactiveRepoWritesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "svc/main.go", "package main\n")
	if code, out, _ := do(root, "not json", "hook", "stop"); code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".auto-context")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".auto-context created: %v", err)
	}
}

func TestNormUnit(t *testing.T) {
	cases := map[string]string{
		"./svc": "svc", "svc//": "svc", "svc/": "svc", "svc": "svc",
		".": "_root", "./": "_root", "a/./b/": "a/b", "_root": "_root",
	}
	for in, want := range cases {
		if got := normUnit(in); got != want {
			t.Errorf("normUnit(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAckExcludeRootRefused(t *testing.T) {
	root := repo(t)
	for _, u := range []string{"_root", ".", "./"} {
		code, _, errs := do(root, "", "ack", "--exclude", u)
		if code != 1 || !strings.Contains(errs, "the root unit cannot be excluded; create context/_root.md instead") {
			t.Errorf("%q: code=%d err=%q", u, code, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".auto-context/config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config written: %v", err)
	}
}

func TestHookPreToolUse(t *testing.T) {
	dir := t.TempDir()
	allow := `{"tool_name":"Bash","tool_input":{"command":"auto-context status --stale"},"cwd":` + quote(dir) + `}`
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"auto-context CLI"}}` + "\n"
	if c, out, _ := do(dir, allow, "hook", "pre-tool-use"); c != 0 || out != want {
		t.Fatalf("allow: code=%d out=%q", c, out)
	}
	for _, in := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"auto-context ack svc; rm -rf /"}}`,
		`not json`,
		``,
	} {
		if c, out, _ := do(dir, in, "hook", "pre-tool-use"); c != 0 || out != "" {
			t.Errorf("%q: code=%d out=%q", in, c, out)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("pre-tool-use wrote files: %v", entries)
	}
}

func TestAckRefreshesRootIndex(t *testing.T) {
	root := repo(t)
	write(t, root, "context/svc.md", "# svc\n\nService summary.\n")
	if code, _, errs := do(root, "", "ack", "_root", "svc"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- `svc` — Service summary.") {
		t.Errorf("root CLAUDE.md lacks svc index entry:\n%s", data)
	}
}

func TestStatusReportsLoggedErrors(t *testing.T) {
	root := repo(t)
	write(t, root, ".auto-context/log", "2026-10-19T00:00:00Z hook stop: first\n2026-10-20T00:00:00Z hook stop: last one\n")
	_, out, _ := do(root, "", "status")
	if !strings.Contains(out, "note: 2 error(s) in .auto-context/log") {
		t.Errorf("status lacks log note: %q", out)
	}
	// The log may come from the repo itself: its text is never echoed.
	if strings.Contains(out, "last one") {
		t.Errorf("status echoes log text: %q", out)
	}
}

// --exit-code fails on missing docs too, and only when asked.
func TestStatusExitCodeCases(t *testing.T) {
	root := repo(t) // svc has no context doc: missing
	write(t, root, "context/_root.md", "# root\n\nRoot summary.\n")
	do(root, "", "ack", "_root")
	if code, out, _ := do(root, "", "status", "--exit-code"); code != 1 || !strings.Contains(out, "UNIT") {
		t.Errorf("missing only: code=%d out=%q", code, out)
	}
	if code, _, _ := do(root, "", "status"); code != 0 {
		t.Errorf("without --exit-code: code=%d", code)
	}
	write(t, root, "context/svc.md", "# svc\n\nS.\n")
	do(root, "", "ack", "svc")
	if code, out, _ := do(root, "", "status", "--exit-code"); code != 0 || !strings.Contains(out, "svc") {
		t.Errorf("fresh, full table: code=%d out=%q", code, out)
	}
}

func TestStatusExitCode(t *testing.T) {
	root := repo(t)
	if code, _, _ := do(root, "", "status", "--exit-code"); code != 1 {
		t.Errorf("stale repo: code=%d, want 1", code)
	}
	write(t, root, "context/svc.md", "# svc\n\nSummary.\n")
	do(root, "", "ack", "_root", "svc")
	if code, out, _ := do(root, "", "status", "--stale", "--exit-code"); code != 0 {
		t.Errorf("fresh repo: code=%d out=%q, want 0", code, out)
	}
}

// hooks/hooks.json must only call hook events the binary handles, each with a timeout.
func TestHooksJSONContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				If      string `json:"if"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SessionStart": "session-start", "UserPromptSubmit": "prompt-submit",
		"Stop": "stop", "PreToolUse": "pre-tool-use", "SessionEnd": "session-end",
	}
	if len(cfg.Hooks) != len(want) {
		t.Errorf("events = %d, want %d", len(cfg.Hooks), len(want))
	}
	for event, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				const prefix = `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context hook `
				sub, ok := strings.CutPrefix(h.Command, prefix)
				if !ok || sub != want[event] {
					t.Errorf("%s: command %q, want %q", event, h.Command, prefix+want[event])
				}
				if _, ok := hookHandlers[sub]; !ok {
					t.Errorf("%s: binary has no handler for %q", event, sub)
				}
				if b, ok := budgetFor(sub, hooks.Claude); !ok || b >= time.Duration(h.Timeout)*time.Second {
					t.Errorf("%s: git budget %v not below the timeout %ds", event, b, h.Timeout)
				}
			}
			if len(g.Hooks) == 0 {
				t.Errorf("%s: empty hooks list", event)
				continue
			}
			if event == "PreToolUse" && (g.Matcher != "Bash" || g.Hooks[0].If != "Bash(auto-context *)") {
				t.Errorf("PreToolUse matcher=%q if=%q", g.Matcher, g.Hooks[0].If)
			}
		}
	}
}

// A cloned repo with symlinks inside it must not make the hooks or the CLI read
// or write anything outside the repo.
func TestSymlinkAttacksStayInsideRepo(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "rc", "RC\n")
	write(t, outside, "ctx/_root.md", "# r\n\nsecret summary\n")
	snapshotOutside := func() map[string]string {
		m := map[string]string{}
		filepath.WalkDir(outside, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := os.ReadFile(p)
				m[p] = string(data)
			}
			return nil
		})
		return m
	}
	before := snapshotOutside()
	link := func(t *testing.T, target, l string) {
		if err := os.Symlink(target, l); err != nil {
			t.Skip("symlinks unsupported:", err)
		}
	}
	setups := map[string]func(root string){
		"CLAUDE.md -> file": func(root string) {
			write(t, root, "context/_root.md", "# r\n\nx\n")
			link(t, filepath.Join(outside, "rc"), filepath.Join(root, "CLAUDE.md"))
		},
		".auto-context -> dir": func(root string) {
			// The config would be outside too: the project is enabled by its docs.
			write(t, root, "context/_root.md", "# r\n\nx\n")
			link(t, outside, filepath.Join(root, ".auto-context"))
		},
		".auto-context/log -> file": func(root string) {
			write(t, root, ".auto-context/config.yaml", "min_files: \"$(sh a)\"\n")
			if err := os.MkdirAll(filepath.Join(root, ".auto-context"), 0o755); err != nil {
				t.Fatal(err)
			}
			link(t, filepath.Join(outside, "rc"), filepath.Join(root, ".auto-context", "log"))
		},
		"context -> dir": func(root string) {
			write(t, root, ".auto-context/config.yaml", "min_files: 1\n")
			link(t, filepath.Join(outside, "ctx"), filepath.Join(root, "context"))
		},
		".gitignore -> file": func(root string) {
			write(t, root, ".auto-context/config.yaml", "min_files: 1\n")
			link(t, filepath.Join(outside, "rc"), filepath.Join(root, ".gitignore"))
		},
		"CLAUDE.md -> .git/config": func(root string) {
			write(t, root, ".auto-context/config.yaml", "min_files: 1\n")
			write(t, root, ".git/config", "[core]\n")
			link(t, ".git/config", filepath.Join(root, "CLAUDE.md"))
		},
		"context doc -> .env": func(root string) {
			write(t, root, ".auto-context/config.yaml", "include: [svc]\n")
			write(t, root, "svc/a.go", "package svc\n")
			write(t, root, ".env", "# Settings\nAPI_KEY=secret\n")
			if err := os.MkdirAll(filepath.Join(root, "context"), 0o755); err != nil {
				t.Fatal(err)
			}
			link(t, "../.env", filepath.Join(root, "context", "svc.md"))
		},
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, ".git/HEAD", "ref: refs/heads/main\n")
			write(t, root, "main.go", "package main\n")
			setup(root)
			in := `{"session_id":"s","cwd":` + quote(root) + `}`
			for _, ev := range []string{"session-start", "prompt-submit", "stop"} {
				do(root, in, "hook", ev)
			}
			write(t, root, "main.go", "package main\n// edit\n")
			do(root, in, "hook", "stop")
			do(root, "", "ack", "_root")
			do(root, "", "ack", "--exclude", "x")
			do(root, "", "status")
			after := snapshotOutside()
			for p, v := range before {
				if after[p] != v {
					t.Errorf("%s changed: %q -> %q", p, v, after[p])
				}
			}
			if len(after) != len(before) {
				t.Errorf("files created outside: %v", after)
			}
			claude := filepath.Join(root, "CLAUDE.md")
			if fi, err := os.Lstat(claude); err == nil && fi.Mode().IsRegular() {
				if data, _ := os.ReadFile(claude); strings.Contains(string(data), "RC") || strings.Contains(string(data), "secret") {
					t.Errorf("outside content copied into repo CLAUDE.md: %q", data)
				}
			}
			if data, err := os.ReadFile(filepath.Join(root, ".git", "config")); err == nil && string(data) != "[core]\n" {
				t.Errorf(".git/config changed: %q", data)
			}
			if _, out, _ := do(root, "", "status"); strings.Contains(out, "secret") || strings.Contains(out, "RC") {
				t.Errorf("status shows outside content: %q", out)
			}
		})
	}
}

// Older logs hold multi-line entries; the note counts entries, not lines.
func TestStatusLogNoteCountsEntries(t *testing.T) {
	root := repo(t)
	write(t, root, ".auto-context/log", "2026-10-19T00:00:00Z config: yaml: unmarshal errors:\n  line 1: cannot unmarshal\ngoroutine 1 [running]:\n2026-10-20T00:00:00+03:00 hook stop: last one\n")
	_, out, _ := do(root, "", "status")
	if !strings.Contains(out, "note: 2 error(s) in .auto-context/log") {
		t.Errorf("status note = %q", out)
	}
}

// A common setup: CLAUDE.md links to AGENTS.md inside the repo. The index must
// land in AGENTS.md and the link must survive.
func TestInRepoClaudeMDSymlink(t *testing.T) {
	root := repo(t)
	if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "AGENTS.md", "# Project\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	write(t, root, "context/svc.md", "# svc\n\nService summary.\n")
	in := `{"session_id":"s","cwd":` + quote(root) + `}`
	do(root, in, "hook", "session-start")
	if code, _, errs := do(root, "", "ack", "_root", "svc"); code != 0 || errs != "" {
		t.Fatalf("ack: code=%d stderr=%q", code, errs)
	}
	if fi, err := os.Lstat(filepath.Join(root, "CLAUDE.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("CLAUDE.md link replaced: %v %v", fi, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(data), "- `svc` — Service summary.") {
		t.Errorf("AGENTS.md lacks index:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(root, ".auto-context", "log")); err == nil {
		log, _ := os.ReadFile(filepath.Join(root, ".auto-context", "log"))
		t.Errorf("unexpected hook errors: %s", log)
	}
}

// status and units must not write into a project the plugin is not active in.
func TestStatusInactiveWritesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	write(t, root, "p/a.go", "package p\n")
	do(root, "", "status")
	do(root, "", "units")
	if _, err := os.Stat(filepath.Join(root, ".auto-context")); !os.IsNotExist(err) {
		t.Errorf(".auto-context created in an inactive repo: %v", err)
	}
}

func TestStatusWarnsAboutLinkedStateDir(t *testing.T) {
	root := repo(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".auto-context")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	// status prints its warnings on stdout, where the sync skill sees them.
	_, out, _ := do(root, "", "status")
	if !strings.Contains(out, "hooks are disabled") {
		t.Errorf("stdout = %q", out)
	}
}

// --exclude takes an existing directory, so a typo is not written silently.
func TestAckExcludeRequiresDir(t *testing.T) {
	root := repo(t)
	if code, _, errs := do(root, "", "ack", "--exclude", "svcc"); code != 1 || !strings.Contains(errs, "no directory") {
		t.Errorf("code=%d stderr=%q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(root, ".auto-context/config.yaml")); !os.IsNotExist(err) {
		t.Errorf("config written for a typo: %v", err)
	}
	if code, _, errs := do(root, "", "ack", "--exclude", "svc"); code != 0 {
		t.Errorf("valid exclude: code=%d stderr=%q", code, errs)
	}
}

// Hooks use the project dir Claude Code passes, not a cwd that drifted into a
// nested repository or submodule.
func TestHookRootFromProjectDir(t *testing.T) {
	root := repo(t)
	write(t, root, "context/svc.md", "# svc\n\nS.\n")
	do(root, "", "ack", "_root", "svc")
	write(t, root, "sub/.git", "gitdir: x\n")
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	start := `{"session_id":"s","cwd":` + quote(root) + `}`
	drift := `{"session_id":"s","cwd":` + quote(filepath.Join(root, "sub")) + `}`
	do(root, start, "hook", "prompt-submit")
	write(t, root, "svc/main.go", "package main\n// edit\n")
	if _, out, _ := do(root, drift, "hook", "stop"); !strings.Contains(out, "code changed in svc") {
		t.Errorf("stop with drifted cwd: %q", out)
	}
}

// FindRoot does not climb past $HOME: a stray ~/.auto-context/config.yaml must not
// make the whole home directory the project.
func TestFindRootStopsAtHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	write(t, home, ".auto-context/config.yaml", "min_files: 1\n")
	proj := filepath.Join(home, "proj", "src")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := config.FindRoot(proj); got != proj {
		t.Errorf("FindRoot = %q, want %q", got, proj)
	}
}

func TestChangesListsUnitHistory(t *testing.T) {
	// A user-wide filter (Git LFS installs filter.lfs.* globally) is trusted
	// and must not hide the uncommitted changes.
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := repo(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	write(t, root, "context/svc.md", "# svc\n\nS.\n")
	do(root, "", "ack", "_root", "svc")
	git("add", "-A")
	git("commit", "-qm", "initial")
	write(t, root, "svc/main.go", "package main\nfunc New() {}\n")
	git("commit", "-qam", "add New to svc")
	write(t, root, "svc/extra.go", "package main\n")
	code, out, errs := do(filepath.Join(root, "context"), "", "changes", "svc")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	for _, want := range []string{"add New to svc", "svc/main.go", "svc/extra.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if code, _, _ := do(root, "", "changes", "nope"); code != 1 {
		t.Errorf("unknown unit: code=%d", code)
	}
}

// `changes` must not run a filter driver from the repo's git config (e.g. a
// .git/config that came in an archive): comparing with the work tree would.
func TestChangesDoesNotRunGitFilters(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := repo(t)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	write(t, root, "context/svc.md", "# svc\n\nS.\n")
	git("add", "-A")
	git("commit", "-qm", "initial")
	write(t, root, ".gitattributes", "*.go filter=evil\n")
	write(t, root, "svc/main.go", "package main\n// edit\n")
	// The filter may sit in .git/config or in the worktree config, which
	// `git config --local` does not show.
	for name, setup := range map[string][]string{
		"local":    {"config", "filter.evil.clean"},
		"worktree": {"config", "--worktree", "filter.evil.clean"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, scope := range []string{"--local", "--worktree"} {
				exec.Command("git", "-C", root, "config", scope, "--unset-all", "filter.evil.clean").Run() // absent is fine
			}
			if name == "worktree" {
				git("config", "extensions.worktreeConfig", "true")
			}
			pwned := filepath.Join(t.TempDir(), "pwned")
			git(append(setup, "touch "+pwned+"; cat")...)
			if code, out, errs := do(root, "", "changes", "svc"); code != 0 || !strings.Contains(out, "defines filter drivers") {
				t.Errorf("code=%d out=%q stderr=%q", code, out, errs)
			}
			if _, err := os.Stat(pwned); err == nil {
				t.Error("git filter driver was executed")
			}
		})
	}
	// A filter in a file the repo config includes is the repo's too.
	t.Run("include", func(t *testing.T) {
		for _, scope := range []string{"--local", "--worktree"} {
			exec.Command("git", "-C", root, "config", scope, "--unset-all", "filter.evil.clean").Run()
		}
		pwned := filepath.Join(t.TempDir(), "pwned")
		// Quoted, with forward slashes: git config reads backslashes as escapes
		// and an unquoted ";" as a comment.
		write(t, root, ".git/evil.inc", "[filter \"evil\"]\n\tclean = \"touch '"+filepath.ToSlash(pwned)+"'; cat\"\n")
		git("config", "include.path", "evil.inc")
		if code, out, _ := do(root, "", "changes", "svc"); code != 0 || !strings.Contains(out, "defines filter drivers") {
			t.Errorf("code=%d out=%q", code, out)
		}
		if _, err := os.Stat(pwned); err == nil {
			t.Error("included filter driver was executed")
		}
	})
}

// A filter defined only in a submodule's own config must not run either:
// status and diff would recurse into the submodule.
func TestChangesDoesNotRunSubmoduleFilters(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "protocol.file.allow=always"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	sub := t.TempDir()
	run(sub, "init", "-q")
	write(t, sub, "lib.go", "package lib\n")
	run(sub, "add", "-A")
	run(sub, "commit", "-qm", "sub")

	root := repo(t)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	run(root, "init", "-q")
	write(t, root, "context/svc.md", "# svc\n\nS.\n")
	run(root, "add", "-A")
	run(root, "commit", "-qm", "initial")
	run(root, "submodule", "add", "-q", sub, "svc/vendored")
	run(root, "commit", "-qm", "add submodule")

	pwned := filepath.Join(t.TempDir(), "pwned")
	subDir := filepath.Join(root, "svc", "vendored")
	run(subDir, "config", "filter.evil.clean", "touch "+pwned+"; cat")
	write(t, subDir, ".gitattributes", "*.go filter=evil\n")
	// Same size as the committed file: git must read it through the filter.
	write(t, subDir, "lib.go", "package xyz\n")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(subDir, "lib.go"), future, future); err != nil {
		t.Fatal(err)
	}

	if code, _, errs := do(root, "", "changes", "svc"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Error("submodule filter driver was executed")
	}
}

// hook-exit (called by the shim after a crash) writes a log entry.
func TestHookExitIsLogged(t *testing.T) {
	root := repo(t)
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	do(root, "", "hook-exit", "stop", "2")
	data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "log"))
	if !strings.Contains(string(data), "hook stop: binary exited with status 2") {
		t.Errorf("log = %q", data)
	}
}

// changes works in a repo without commits and lists the new files.
func TestChangesWithoutCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := repo(t)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	code, out, errs := do(root, "", "changes", "svc")
	if code != 0 || !strings.Contains(out, "no commits yet") || !strings.Contains(out, "svc/main.go") {
		t.Errorf("code=%d out=%q stderr=%q", code, out, errs)
	}
	// A missing HEAD is not a git failure: no section reports one.
	if strings.Contains(out, "(git failed") {
		t.Errorf("git failure reported without commits: %q", out)
	}
}

// For a plugin root below the git top level, changes looks only inside it.
func TestChangesInSubdirectoryRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	top := t.TempDir()
	g := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", top, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g("init", "-q")
	for i := 0; i < 5; i++ {
		write(t, top, fmt.Sprintf("pkg/f%d.go", i), "package p\n")
		write(t, top, fmt.Sprintf("sub/pkg/f%d.go", i), "package p\n")
	}
	write(t, top, "sub/.auto-context/config.yaml", "min_files: 1\n")
	g("add", "-A")
	g("commit", "-qm", "init")
	write(t, top, "pkg/f0.go", "package p // outer\n")
	g("commit", "-qam", "outer only")
	_, out, _ := do(filepath.Join(top, "sub"), "", "changes", "pkg")
	if strings.Contains(out, "outer only") {
		t.Errorf("outer commit listed for sub/pkg:\n%s", out)
	}
	if !strings.Contains(out, "init") {
		t.Errorf("own history missing:\n%s", out)
	}
	// Paths are relative to the root in every section, as status prints them.
	if strings.Contains(out, "sub/pkg/") || !strings.Contains(out, "pkg/f0.go") {
		t.Errorf("paths not root-relative:\n%s", out)
	}
}

func TestStatusNamesDocsLanguage(t *testing.T) {
	root := repo(t)
	write(t, root, ".auto-context/config.yaml", "language: ru\n")
	if _, out, _ := do(root, "", "status"); !strings.Contains(out, "note: write unit docs in language ru") {
		t.Errorf("out=%q", out)
	}
}

// Every skill's frontmatter is strict YAML with a name matching its directory
// and a description: `claude plugin validate` accepts some invalid YAML.
func TestSkillsFrontmatter(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", "SKILL.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no skills: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.SplitN(strings.ReplaceAll(string(data), "\r\n", "\n"), "---\n", 3)
		if len(parts) != 3 || parts[0] != "" {
			t.Errorf("%s: no frontmatter", f)
			continue
		}
		var fm struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if dir := filepath.Base(filepath.Dir(f)); fm.Name != dir || fm.Description == "" {
			t.Errorf("%s: name %q (want %q), description %q", f, fm.Name, dir, fm.Description)
		}
	}
}

// ack, units and changes refuse while the config cannot be parsed: the units come from defaults.
func TestAckRefusesBrokenConfig(t *testing.T) {
	root := repo(t)
	write(t, root, "context/svc.md", "# svc\n\nS.\n")
	write(t, root, ".auto-context/config.yaml", "include: [svc\n")
	for _, args := range [][]string{{"ack", "svc"}, {"units"}, {"changes", "svc"}} {
		if code, out, errs := do(root, "", args...); code != 1 || !strings.Contains(errs, "fix it first") || out != "" {
			t.Errorf("%v: code=%d out=%q stderr=%q", args, code, out, errs)
		}
	}
}

// changes reports a failed HEAD lookup as a git failure, not an empty repo.
func TestChangesHeadFailure(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs git and a POSIX sh")
	}
	root := repo(t)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	bin := t.TempDir()
	write(t, bin, "git", "#!/bin/sh\ncase \"$*\" in *'--verify -q HEAD'*) echo 'fatal: detected dubious ownership' >&2; exit 128;; esac\nexec '"+real+"' \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, out, _ := do(root, "", "changes", "svc"); !strings.Contains(out, "git failed") || strings.Contains(out, "no commits yet") {
		t.Errorf("out=%q", out)
	}
}

// status shows no table for the default units while the config is broken;
// only --exit-code fails, as the docs state is unknown.
func TestStatusBrokenConfig(t *testing.T) {
	root := repo(t)
	write(t, root, ".auto-context/config.yaml", "include: [svc\n")
	code, out, _ := do(root, "", "status")
	if code != 0 || strings.Contains(out, "UNIT") || !strings.Contains(out, "no table is printed") {
		t.Errorf("code=%d out=%q", code, out)
	}
	if code, _, _ := do(root, "", "status", "--exit-code"); code != 1 {
		t.Errorf("--exit-code: code=%d", code)
	}
}

// An orphan doc with an unusual name is counted, not echoed into status.
func TestStatusHidesUnusualOrphanName(t *testing.T) {
	root := repo(t)
	write(t, root, "context/x`; run this.md", "# x\n")
	_, out, _ := do(root, "", "status")
	if strings.Contains(out, "run this") || !strings.Contains(out, "1 more file(s)") {
		t.Errorf("out=%q", out)
	}
}

// With a broken config, status still counts logged errors, prints no table
// and names the YAML error without quoting yaml's own text.
func TestStatusBrokenConfigLogNote(t *testing.T) {
	root := repo(t)
	write(t, root, ".auto-context/config.yaml", "include: [svc\n")
	write(t, root, ".auto-context/log", "2026-10-19T00:00:00Z hook stop: x\n")
	_, out, _ := do(root, "", "status")
	for _, want := range []string{"1 error(s) in .auto-context/log", "no table is printed", "YAML syntax error"} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q: %q", want, out)
		}
	}
	if strings.Contains(out, "did not find") || strings.Contains(out, "yaml: ") {
		t.Errorf("out quotes the yaml error: %q", out)
	}
}

// A read-only log in an active repo is reported; an inactive directory gets
// no such warning and no .auto-context.
func TestStatusLogNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	root := repo(t)
	write(t, root, ".auto-context/log", "")
	if err := os.Chmod(filepath.Join(root, ".auto-context", "log"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := do(root, "", "status"); !strings.Contains(out, ".auto-context/log cannot be written") {
		t.Errorf("out=%q", out)
	}
	inactive := t.TempDir()
	write(t, inactive, "p/a.go", "package p\n")
	if _, out, _ := do(inactive, "", "status"); strings.Contains(out, "cannot be written") {
		t.Errorf("inactive out=%q", out)
	}
	if _, err := os.Stat(filepath.Join(inactive, ".auto-context")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".auto-context created: %v", err)
	}
}

// runHook resets the gitx deadline it set, even when the hook's budget ran
// out: later in-process git calls are not refused.
func TestHookResetsGitDeadline(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	old := hookBudget["pre-tool-use"]
	hookBudget["pre-tool-use"] = time.Nanosecond
	t.Cleanup(func() { hookBudget["pre-tool-use"] = old })
	root := repo(t)
	do(root, `{"session_id":"s","cwd":`+quote(root)+`}`, "hook", "pre-tool-use")
	if _, err := gitx.Run(t.TempDir(), 0, "version"); err != nil {
		t.Errorf("git after the hook: %v", err)
	}
}

// A git failure while checking whether config.yaml is ignored is a warning
// naming the failure only by its kind, without git's stderr.
func TestStatusConfigIgnoredGitFailure(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs git and a POSIX sh")
	}
	root := repo(t)
	write(t, root, ".auto-context/config.yaml", "min_files: 5\n")
	bin := t.TempDir()
	write(t, bin, "git", "#!/bin/sh\ncase \"$*\" in *'check-ignore -q -- .auto-context'*) echo 'fatal: SECRET STDERR' >&2; exit 128;; esac\nexec '"+real+"' \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, out, _ := do(root, "", "status")
	if !strings.Contains(out, "git could not tell whether .auto-context/config.yaml is ignored (exit 128)") || strings.Contains(out, "SECRET") {
		t.Errorf("out=%q", out)
	}
}

// With a corrupt index (git check-ignore exits 128), status warns that git
// could not tell, without git's own message.
func TestStatusConfigIgnoredCorruptIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := repo(t)
	write(t, root, ".auto-context/config.yaml", "min_files: 5\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(t, root, ".git/index", "garbage")
	_, out, _ := do(root, "", "status")
	if !strings.Contains(out, "git could not tell whether") || strings.Contains(out, "index file") || strings.Contains(out, "fatal") {
		t.Errorf("out=%q", out)
	}
}

func TestStatusBrokenMarkersAgents(t *testing.T) {
	root := repo(t)
	write(t, root, ".auto-context/config.yaml", "index_file: AGENTS.md\n")
	write(t, root, "AGENTS.md", "<!-- auto-context:units -->\nx\n")
	_, out, _ := do(root, "", "status")
	want := "warning: the auto-context:units markers in the root AGENTS.md are unbalanced, so the units index is not updated"
	if !strings.Contains(out, want) {
		t.Errorf("status output lacks %q:\n%s", want, out)
	}
}

func TestHookAgentFlag(t *testing.T) {
	root := repo(t)
	write(t, root, "context/svc.md", "# svc\n\nService summary.\n")
	if c, _, e := do(root, "", "ack", "_root", "svc"); c != 0 {
		t.Fatalf("ack . code=%d %s", c, e)
	}
	in := `{"session_id":"s1","cwd":` + quote(root) + `}`
	if c, out, _ := do(t.TempDir(), in, "hook", "prompt-submit", "--agent", "codex"); c != 0 || out != "" {
		t.Fatalf("prompt-submit code=%d out=%q", c, out)
	}
	write(t, root, "svc/main.go", "package main\n\nfunc main() {}\n")
	code, out, _ := do(t.TempDir(), in, "hook", "stop", "--agent", "codex")
	if code != 0 || !strings.Contains(out, "$auto-context-docs") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHookAgentFlagBad(t *testing.T) {
	for _, args := range [][]string{
		{"hook", "stop", "--agent"},
		{"hook", "stop", "--agent", "gpt"},
		{"hook", "stop", "--x", "codex"},
	} {
		root := repo(t)
		write(t, root, ".auto-context/config.yaml", "min_files: 1\n")
		code, out, _ := do(root, `{}`, args...)
		if code != 0 || out != "" {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
		data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "log"))
		if !strings.Contains(string(data), "hook: bad arguments") {
			t.Errorf("%v: log = %q", args, data)
		}
	}
}

// The fixture follows the Codex documentation: PermissionRequest was not
// observed live (Codex runs the plugin binary by absolute path without asking).
func TestHookPermissionRequest(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "hooks", "testdata", "permission_request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	payload["cwd"] = dir
	in, _ := json.Marshal(payload)
	want := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}` + "\n"
	if c, out, _ := do(dir, string(in), "hook", "permission-request", "--agent", "codex"); c != 0 || out != want {
		t.Fatalf("code=%d out=%q", c, out)
	}
}

func TestHookToolInputArray(t *testing.T) {
	root := repo(t)
	write(t, root, ".auto-context/config.yaml", "min_files: 1\n")
	in := `{"tool_name":"Bash","tool_input":{"command":["auto-context","status"]},"cwd":` + quote(root) + `}`
	if c, out, _ := do(root, in, "hook", "permission-request", "--agent", "codex"); c != 0 || out != "" {
		t.Fatalf("code=%d out=%q", c, out)
	}
	if data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "log")); len(data) != 0 {
		t.Errorf("log = %q", data)
	}
}

func TestBudgetFor(t *testing.T) {
	for _, c := range []struct {
		event string
		agent hooks.Agent
		want  time.Duration
	}{
		{"session-end", hooks.Codex, 2 * time.Second},
		{"session-end", hooks.Claude, 8 * time.Second},
		{"permission-request", hooks.Codex, 8 * time.Second},
	} {
		if got, ok := budgetFor(c.event, c.agent); !ok || got != c.want {
			t.Errorf("budgetFor(%q, %q) = %v, %v; want %v", c.event, c.agent, got, ok, c.want)
		}
	}
}

// hooks/codex.json and .codex-plugin/plugin.json must match what the binary handles.
func TestCodexHooksManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "hooks", "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		sub     string
		timeout int
	}{
		"SessionStart": {"session-start", 60}, "UserPromptSubmit": {"prompt-submit", 30},
		"Stop": {"stop", 60}, "PermissionRequest": {"permission-request", 10},
		"SessionEnd": {"session-end", 3},
	}
	if len(cfg.Hooks) != len(want) {
		t.Errorf("events = %d, want %d", len(cfg.Hooks), len(want))
	}
	for event, groups := range cfg.Hooks {
		w, known := want[event]
		if !known {
			t.Errorf("unexpected event %s", event)
			continue
		}
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Errorf("%s: want one group with one hook", event)
			continue
		}
		g, h := groups[0], groups[0].Hooks[0]
		wantCmd := `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context hook ` + w.sub + " --agent codex"
		if h.Type != "command" || h.Command != wantCmd {
			t.Errorf("%s: command %q, want %q", event, h.Command, wantCmd)
		}
		if _, ok := hookHandlers[w.sub]; !ok {
			t.Errorf("%s: binary has no handler for %q", event, w.sub)
		}
		if h.Timeout != w.timeout {
			t.Errorf("%s: timeout %d, want %d", event, h.Timeout, w.timeout)
		}
		if b, ok := budgetFor(w.sub, hooks.Codex); !ok || b >= time.Duration(h.Timeout)*time.Second {
			t.Errorf("%s: git budget %v not below the timeout %ds", event, b, h.Timeout)
		}
		wantMatcher := ""
		if event == "PermissionRequest" {
			wantMatcher = "^Bash$"
		}
		if g.Matcher != wantMatcher {
			t.Errorf("%s: matcher %q, want %q", event, g.Matcher, wantMatcher)
		}
	}

	pdata, err := os.ReadFile(filepath.Join("..", "..", ".codex-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plugin map[string]any
	if err := json.Unmarshal(pdata, &plugin); err != nil {
		t.Fatal(err)
	}
	if plugin["name"] != "auto-context" || plugin["hooks"] != "./hooks/codex.json" {
		t.Errorf("plugin.json name=%v hooks=%v", plugin["name"], plugin["hooks"])
	}
	if _, has := plugin["version"]; has {
		t.Error("plugin.json on main must not carry a version")
	}
}

func TestCodexMarketplace(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".agents", "plugins", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Plugins) != 1 || m.Plugins[0].Name != "auto-context" || m.Plugins[0].Source.Source != "local" || m.Plugins[0].Source.Path != "./" {
		t.Errorf("plugins = %+v", m.Plugins)
	}
}
