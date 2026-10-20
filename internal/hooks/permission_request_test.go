package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionRequestAllows(t *testing.T) {
	want := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	env := testEnv(t.TempDir())
	for _, c := range allowedCLICalls {
		out, err := PermissionRequest(env, preInput("Bash", c))
		if err != nil || string(out) != want {
			t.Errorf("%q: got %s, %v\nwant %s", c, out, err, want)
		}
	}
}

func TestPermissionRequestSilent(t *testing.T) {
	env := testEnv(t.TempDir())
	for _, c := range []struct{ tool, cmd string }{
		{"Bash", "auto-context ack --exclude x"},
		{"Bash", "auto-context ack ../x"},
		{"Bash", "auto-context status; rm -rf /"},
		{"apply_patch", "auto-context status"},
	} {
		if out, err := PermissionRequest(env, preInput(c.tool, c.cmd)); err != nil || out != nil {
			t.Errorf("%v: out=%q err=%v, want nothing", c, out, err)
		}
	}
}

func TestPermissionRequestSilentWhenShadowed(t *testing.T) {
	plugin, fake := t.TempDir(), t.TempDir()
	fakeCLI(t, filepath.Join(plugin, "bin"))
	fakeCLI(t, fake)
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	t.Setenv("PATH", fake+string(os.PathListSeparator)+filepath.Join(plugin, "bin"))
	if out, err := PermissionRequest(testEnv(t.TempDir()), preInput("Bash", "auto-context status")); err != nil || out != nil {
		t.Errorf("shadowed binary approved: out=%q err=%v", out, err)
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
