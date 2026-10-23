package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexEnv is a Codex env whose plugin root is a plain absolute path; it
// returns the env and the binary path env.CLI() gives for it.
func codexEnv(t *testing.T) (Env, string) {
	t.Helper()
	const root = "/home/u/.codex/plugins/cache/auto-context/auto-context/1.0.0"
	t.Setenv("CLAUDE_PLUGIN_ROOT", root)
	env := testEnv(t.TempDir())
	env.Agent = Codex
	return env, root + "/bin/auto-context"
}

func TestPermissionRequestAllows(t *testing.T) {
	want := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	env, abs := codexEnv(t)
	for _, c := range allowedCLICalls {
		c = abs + strings.TrimPrefix(c, "auto-context")
		out, err := PermissionRequest(env, preInput("Bash", c))
		if err != nil || string(out) != want {
			t.Errorf("%q: got %s, %v\nwant %s", c, out, err, want)
		}
	}
}

func TestPermissionRequestSilent(t *testing.T) {
	env, abs := codexEnv(t)
	for _, c := range []struct{ tool, cmd string }{
		{"Bash", abs + " ack --exclude x"},
		{"Bash", abs + " ack ../x"},
		{"Bash", abs + " status; rm -rf /"},
		{"Bash", "auto-context status"},
		{"apply_patch", abs + " status"},
	} {
		if out, err := PermissionRequest(env, preInput(c.tool, c.cmd)); err != nil || out != nil {
			t.Errorf("%v: out=%q err=%v, want nothing", c, out, err)
		}
	}
}

// For Codex, PATH does not matter: the absolute form is approved even when a
// foreign auto-context or a relative entry is on PATH, the bare name never.
func TestPermissionRequestIgnoresPATH(t *testing.T) {
	plugin, fake := t.TempDir(), t.TempDir()
	pluginShim(t, plugin)
	fakeCLI(t, fake)
	env, abs := codexEnv(t)
	allow := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	sep := string(os.PathListSeparator)
	for _, p := range []string{fake + sep + filepath.Join(plugin, "bin"), "bin" + sep + fake} {
		t.Setenv("PATH", p)
		if out, err := PermissionRequest(env, preInput("Bash", abs+" status")); err != nil || string(out) != allow {
			t.Errorf("PATH=%q: absolute form: out=%q err=%v", p, out, err)
		}
		if out, err := PermissionRequest(env, preInput("Bash", "auto-context status")); err != nil || out != nil {
			t.Errorf("PATH=%q: bare name approved: out=%q err=%v", p, out, err)
		}
	}
}

func TestPermissionRequestAbsolutePath(t *testing.T) {
	const root = "/home/u/.codex/plugins/cache/m/auto-context/1.0.0"
	t.Setenv("CLAUDE_PLUGIN_ROOT", root)
	abs := root + "/bin/auto-context"
	allow := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`

	codex := testEnv(t.TempDir())
	codex.Agent = Codex
	if out, err := PermissionRequest(codex, preInput("Bash", abs+" ack src/auth")); err != nil || string(out) != allow {
		t.Errorf("own absolute path: got %s, %v", out, err)
	}
	for _, cmd := range []string{
		"/tmp/evil/bin/auto-context ack src/auth",
		abs + " ack ../x",
		abs + " ack --exclude x",
		abs + " ack x; rm -rf /",
		abs + "/../auto-context status",
	} {
		if out, err := PermissionRequest(codex, preInput("Bash", cmd)); err != nil || out != nil {
			t.Errorf("%q: out=%q err=%v, want nothing", cmd, out, err)
		}
	}

	if out, err := PermissionRequest(testEnv(t.TempDir()), preInput("Bash", abs+" ack src/auth")); err != nil || out != nil {
		t.Errorf("claude approved the absolute form: out=%q err=%v", out, err)
	}
}

func TestPermissionRequestQuotedRoot(t *testing.T) {
	root := "/home/o'neil/.codex/plugins/cache/m/auto-context/1.0.0"
	t.Setenv("CLAUDE_PLUGIN_ROOT", root)
	cli := codexCLI(root)
	allow := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	codex := testEnv(t.TempDir())
	codex.Agent = Codex
	if out, err := PermissionRequest(codex, preInput("Bash", cli+" ack src/auth")); err != nil || string(out) != allow {
		t.Errorf("quoted path: got %s, %v", out, err)
	}
	for _, cmd := range []string{
		cli + " ack x; rm -rf /",
		cli + " ack --exclude x",
		root + "/bin/auto-context ack x",
		`"` + root + `/bin/auto-context" ack x`,
		cli + "  status",
	} {
		if out, err := PermissionRequest(codex, preInput("Bash", cmd)); err != nil || out != nil {
			t.Errorf("%q: out=%q err=%v, want nothing", cmd, out, err)
		}
	}
}
