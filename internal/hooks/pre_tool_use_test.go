package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"auto-context/internal/testutil"
)

func preInput(tool, cmd string) Input {
	var in Input
	in.SessionID = "s"
	in.ToolName = tool
	in.ToolInput, _ = json.Marshal(map[string]string{"command": cmd})
	return in
}

// allowedCLICalls are the commands both PreToolUse and PermissionRequest approve.
var allowedCLICalls = []string{
	"auto-context ack svc",
	"auto-context ack _root src/auth cmd/api",
	"auto-context ack .",
	"auto-context status",
	"auto-context changes svc",
	"auto-context status --stale",
	"auto-context units",
	"auto-context version",
}

func TestPreToolUseAllowsOwnCLI(t *testing.T) {
	root := t.TempDir()
	env := testEnv(root)
	for _, c := range allowedCLICalls {
		out, err := PreToolUse(env, preInput("Bash", c))
		if err != nil {
			t.Fatalf("%q: %v", c, err)
		}
		var v struct {
			Hook struct {
				EventName string `json:"hookEventName"`
				Decision  string `json:"permissionDecision"`
				Reason    string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatalf("%q: bad output %q: %v", c, out, err)
		}
		if v.Hook.EventName != "PreToolUse" || v.Hook.Decision != "allow" || v.Hook.Reason != "auto-context CLI" {
			t.Errorf("%q: got %s", c, out)
		}
	}
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"auto-context CLI"}}`
	if out, _ := PreToolUse(env, preInput("Bash", "auto-context units")); string(out) != want {
		t.Errorf("got %s\nwant %s", out, want)
	}
}

func TestPreToolUseRejectsEverythingElse(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "min_files: 3\n")
	env := testEnv(root)
	before := snapshotTree(t, root)
	rejected := []string{
		"auto-context ack svc; rm -rf /",
		"auto-context ack svc;rm -rf /",
		"auto-context ack $(x)",
		"auto-context ack `x`",
		"auto-context ack ../x",
		"auto-context ack svc/../../etc",
		"auto-context ack ..",
		"auto-context ack svc && ls",
		"auto-context ack svc || ls",
		"auto-context ack svc | sh",
		"auto-context ack svc & ls",
		"auto-context ack svc\nrm -rf /",
		"auto-context ack svc\rrm -rf /",
		"auto-context status > /etc/passwd",
		"auto-context status < x",
		"auto-context ack 'svc'",
		`auto-context ack "svc"`,
		"auto-context ack $HOME",
		"auto-context ack ~",
		"auto-context ack svc*",
		"auto-context  ack svc",
		"auto-context ack --exclude pkg/new",
		"auto-context ack svc --exclude x",
		"auto-context\tack svc",
		"auto-contextx ack",
		"auto-context ackx",
		"auto-context hook stop",
		"auto-context",
		"sudo auto-context ack",
		"FOO=1 auto-context ack svc",
		"/usr/bin/auto-context ack svc",
		"echo auto-context ack svc",
		"",
		// No trimming: bash does not split on Unicode spaces, so a padded word is another program.
		"\u00a0auto-context ack x",
		"auto-context ack x\u00a0",
		"\u0085auto-context ack x",
		"auto-context ack x\u0085",
		"\fauto-context ack x",
		"auto-context ack x\f",
		"\vauto-context ack x",
		"auto-context ack x\v",
		"\u3000auto-context ack x",
		"auto-context ack x\u3000",
		" auto-context ack x",
		"auto-context ack x ",
		"auto-context ack x\n",
		"auto-context ack x\r\n",
	}
	for _, c := range rejected {
		out, err := PreToolUse(env, preInput("Bash", c))
		if err != nil || out != nil {
			t.Errorf("%q: out=%q err=%v, want nothing", c, out, err)
		}
	}
	for _, tool := range []string{"", "PowerShell", "Edit"} {
		if out, err := PreToolUse(env, preInput(tool, "auto-context status")); err != nil || out != nil {
			t.Errorf("tool %q: out=%q err=%v, want nothing", tool, out, err)
		}
	}
	after := snapshotTree(t, root)
	if len(after) != len(before) {
		t.Errorf("PreToolUse wrote to disk:\nbefore %v\nafter  %v", before, after)
	}
}

// fakeCLI writes an executable stub auto-context into dir (auto-context.bat
// on Windows, where LookPath only finds PATHEXT files) and returns its path.
func fakeCLI(t *testing.T, dir string) string {
	t.Helper()
	name, body := "auto-context", "#!/bin/sh\n"
	if runtime.GOOS == "windows" {
		name, body = "auto-context.bat", "@echo off\r\n"
	}
	testutil.WriteFile(t, dir, name, body)
	p := filepath.Join(dir, name)
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Known fail-safe behaviour: on Windows shadowed() does not try PATHEXT
// extensions, so even when PATH holds only the plugin's own bin it reports
// shadowed and nothing is auto-approved.
func TestShadowedFailSafeOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only behaviour")
	}
	plugin := t.TempDir()
	fakeCLI(t, filepath.Join(plugin, "bin"))
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	t.Setenv("PATH", filepath.Join(plugin, "bin"))
	if !shadowed() {
		t.Error("shadowed() = false; the extensionless shim path cannot match on Windows")
	}
}

// An auto-context earlier on PATH than the plugin's own is not auto-approved.
func TestPreToolUseRefusesShadowedBinary(t *testing.T) {
	plugin, fake := t.TempDir(), t.TempDir()
	fakeCLI(t, filepath.Join(plugin, "bin"))
	fakeCLI(t, fake)
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	env := testEnv(t.TempDir())

	t.Setenv("PATH", fake+string(os.PathListSeparator)+filepath.Join(plugin, "bin"))
	if out, _ := PreToolUse(env, preInput("Bash", "auto-context status")); out != nil {
		t.Errorf("shadowed binary approved: %s", out)
	}
	// On Windows shadowed() resolves <plugin>/bin/auto-context without trying
	// PATHEXT extensions, so it cannot match the plugin's .bat and always
	// refuses (see TestShadowedFailSafeOnWindows); the refusals above hold
	// for that reason there, not because of the shadowing.
	if runtime.GOOS == "windows" {
		return
	}
	t.Setenv("PATH", filepath.Join(plugin, "bin")+string(os.PathListSeparator)+fake)
	if out, _ := PreToolUse(env, preInput("Bash", "auto-context status")); out == nil {
		t.Error("plugin binary first on PATH not approved")
	}
}

// An auto-context found through a relative PATH entry (".") is not approved.
func TestPreToolUseRefusesRelativePath(t *testing.T) {
	plugin, cwd := t.TempDir(), t.TempDir()
	fakeCLI(t, filepath.Join(plugin, "bin"))
	fakeCLI(t, cwd)
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	t.Setenv("PATH", "."+string(os.PathListSeparator)+filepath.Join(plugin, "bin"))
	t.Chdir(cwd)
	if out, _ := PreToolUse(testEnv(t.TempDir()), preInput("Bash", "auto-context status")); out != nil {
		t.Errorf("approved via relative PATH: %s", out)
	}
}

// When the plugin's own shim cannot be resolved, nothing is approved.
func TestPreToolUseRefusesWhenPluginShimMissing(t *testing.T) {
	fake := t.TempDir()
	fakeCLI(t, fake)
	t.Setenv("CLAUDE_PLUGIN_ROOT", t.TempDir()) // no bin/auto-context
	t.Setenv("PATH", fake)
	if out, _ := PreToolUse(testEnv(t.TempDir()), preInput("Bash", "auto-context status")); out != nil {
		t.Errorf("approved: %s", out)
	}
}
