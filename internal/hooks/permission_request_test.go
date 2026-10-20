package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"auto-context/internal/testutil"
)

func TestPermissionRequestAllows(t *testing.T) {
	want := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	out, err := PermissionRequest(testEnv(t.TempDir()), preInput("Bash", "auto-context status --stale"))
	if err != nil || string(out) != want {
		t.Fatalf("got %s, %v\nwant %s", out, err, want)
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
	if runtime.GOOS == "windows" {
		t.Skip("LookPath only finds PATHEXT files; the sh shim has no extension")
	}
	plugin, fake := t.TempDir(), t.TempDir()
	testutil.WriteFile(t, plugin, "bin/auto-context", "#!/bin/sh\n")
	testutil.WriteFile(t, fake, "auto-context", "#!/bin/sh\n")
	for _, p := range []string{filepath.Join(plugin, "bin", "auto-context"), filepath.Join(fake, "auto-context")} {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
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
