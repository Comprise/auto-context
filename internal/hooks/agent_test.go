package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRefs(t *testing.T) {
	if got := Claude.Skill("auto-context-docs"); got != "auto-context:auto-context-docs" {
		t.Errorf("Claude.Skill = %q", got)
	}
	if got := Codex.Skill("auto-context-docs"); got != "$auto-context:auto-context-docs" {
		t.Errorf("Codex.Skill = %q", got)
	}
	if got := Claude.Command("status"); got != "/auto-context:status" {
		t.Errorf("Claude.Command = %q", got)
	}
	if got := Codex.Command("status"); got != "$auto-context:status" {
		t.Errorf("Codex.Command = %q", got)
	}
	if a, ok := ParseAgent("codex"); a != Codex || !ok {
		t.Errorf("ParseAgent(codex) = %q, %v", a, ok)
	}
	for _, s := range []string{"", "Codex"} {
		if a, ok := ParseAgent(s); a != "" || ok {
			t.Errorf("ParseAgent(%q) = %q, %v", s, a, ok)
		}
	}
}

func TestStopReasonCodex(t *testing.T) {
	r := StopReason(Feedback{Agent: Codex, IndexFile: "AGENTS.md", Changed: []string{"src/auth"}})
	if !strings.Contains(r, "(and <unit>/AGENTS.md if its purpose or key files changed) per skill $auto-context:auto-context-docs.") || strings.Contains(r, "CLAUDE.md") {
		t.Errorf("reason = %q", r)
	}
	r = StopReason(Feedback{Agent: Codex, IndexFile: "AGENTS.md", New: []string{"_root"}})
	if !strings.Contains(r, "create context/_root.md (and the root AGENTS.md purpose line).") {
		t.Errorf("reason = %q", r)
	}
}

func TestLanguageNoteIndex(t *testing.T) {
	want := "Write unit docs (AGENTS.md, context/) in language ru (`language` in .auto-context/config.yaml)."
	if got := LanguageNote("ru", "AGENTS.md"); got != want {
		t.Errorf("got %q", got)
	}
}

func TestSessionNoticeCodex(t *testing.T) {
	got := SessionNotice([]string{"a"}, nil, 10, Codex)
	if !strings.HasSuffix(got, "(skill $auto-context:auto-context-docs) or run $auto-context:status.") {
		t.Errorf("got %q", got)
	}
}

func TestStopFeedbackCodex(t *testing.T) {
	out, err := stopFeedback("x", Codex)
	if err != nil || string(out) != `{"decision":"block","reason":"x"}` {
		t.Errorf("codex = %s, %v", out, err)
	}
	out, err = stopFeedback("x", Claude)
	if err != nil || string(out) != `{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"x"}}` {
		t.Errorf("claude = %s, %v", out, err)
	}
}

func TestSessionStartRootIndexAgents(t *testing.T) {
	env := newRepo(t, false)
	write(t, env.Root, ".auto-context/config.yaml", "index_file: AGENTS.md\n")
	write(t, env.Root, "AGENTS.md", "# Project\n<!-- /auto-context:units -->\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	c := decodeSession(t, out).Hook.AdditionalContext
	if !strings.Contains(c, "root AGENTS.md") || strings.Contains(c, "CLAUDE.md") {
		t.Errorf("context = %q", c)
	}
}

func TestBrokenIndexFileNoClaudeMD(t *testing.T) {
	env := newRepo(t, false)
	if err := os.Remove(filepath.Join(env.Root, "CLAUDE.md")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	write(t, env.Root, ".auto-context/config.yaml", "index_file: agents.md\n")
	out, err := SessionStart(env, Input{SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if v := decodeSession(t, out); !strings.Contains(v.SystemMessage, "auto-context") || !strings.Contains(v.SystemMessage, "config.yaml") {
		t.Errorf("out = %s", out)
	}
	if _, err := os.Stat(filepath.Join(env.Root, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md was created")
	}
}

func TestNonStringIndexFileNoClaudeMD(t *testing.T) {
	env := newRepo(t, false)
	env.Agent = Codex
	if err := os.Remove(filepath.Join(env.Root, "CLAUDE.md")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	write(t, env.Root, ".auto-context/config.yaml", "index_file: [AGENTS.md]\n")
	if _, err := SessionStart(env, Input{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.Root, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md was created")
	}
}

func TestCodexCLIPath(t *testing.T) {
	const root = "/home/u/.codex/plugins/cache/m/auto-context/1.0.0"
	f := Feedback{Agent: Codex, IndexFile: "AGENTS.md", Changed: []string{"src/auth"}}

	t.Setenv("CLAUDE_PLUGIN_ROOT", root)
	if got := StopReason(f); !strings.Contains(got, "`"+root+"/bin/auto-context ack src/auth`") {
		t.Errorf("absolute path missing: %s", got)
	}
	if got := Claude.CLI(); got != "auto-context" {
		t.Errorf("Claude.CLI = %q", got)
	}
	f.Agent = Claude
	if got := StopReason(f); !strings.Contains(got, "`auto-context ack src/auth`") {
		t.Errorf("claude text changed: %s", got)
	}

	f.Agent = Codex
	for _, bad := range []string{"", "relative/path", "/x\n"} {
		t.Setenv("CLAUDE_PLUGIN_ROOT", bad)
		if got := StopReason(f); !strings.Contains(got, "`auto-context ack src/auth`") {
			t.Errorf("root %q: want the bare name: %s", bad, got)
		}
	}
}

func TestCodexCLI(t *testing.T) {
	for _, c := range []struct{ root, want string }{
		{"", "auto-context"},
		{"rel/p", "auto-context"},
		{`C:\Users\u\p`, "auto-context"},
		{"/home/u/p", "/home/u/p/bin/auto-context"},
		{"/c/Users/u/p", "/c/Users/u/p/bin/auto-context"},
		{"/home/my user/p", "'/home/my user/p/bin/auto-context'"},
		{"/home/o'neil/p", `'/home/o'\''neil/p/bin/auto-context'`},
		{"/home/ü/p", "'/home/ü/p/bin/auto-context'"},
		{"/home/u/p\n", "auto-context"},
		{"/home/u\x7f", "auto-context"},
		{"/home/\xff", "auto-context"},
	} {
		if got := codexCLI(c.root); got != c.want {
			t.Errorf("codexCLI(%q) = %q, want %q", c.root, got, c.want)
		}
	}
}
