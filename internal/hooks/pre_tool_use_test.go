package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// allowedCLICalls are the commands PreToolUse approves for Claude; for Codex
// both hooks approve them only with the bare name replaced by Codex.CLI().
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
	t.Setenv("PATH", t.TempDir()) // absolute entries only: a relative one blocks approval
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

// writeStub writes an executable file name into dir and returns its path.
func writeStub(t *testing.T, dir, name, body string) string {
	t.Helper()
	testutil.WriteFile(t, dir, name, body)
	p := filepath.Join(dir, name)
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// pluginShim writes the plugin's own bin/auto-context, as shipped: an
// extensionless sh shim on every OS. On Windows exec.LookPath does not find
// an extensionless file, so shadowed() sees nothing on PATH from the plugin.
func pluginShim(t *testing.T, pluginRoot string) string {
	t.Helper()
	return writeStub(t, filepath.Join(pluginRoot, "bin"), "auto-context", "#!/bin/sh\n")
}

// fakeCLI writes a foreign auto-context into dir that LookPath finds on this
// OS: an executable auto-context, or auto-context.bat on Windows (PATHEXT).
func fakeCLI(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return writeStub(t, dir, "auto-context.bat", "@echo off\r\n")
	}
	return writeStub(t, dir, "auto-context", "#!/bin/sh\n")
}

// With only the plugin's own bin on PATH nothing shadows it (on Windows
// LookPath ignores the extensionless shim, so there is nothing to compare).
func TestShadowedOnWindowsPluginShimOnly(t *testing.T) {
	plugin := t.TempDir()
	pluginShim(t, plugin)
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	t.Setenv("PATH", filepath.Join(plugin, "bin"))
	if shadowed() {
		t.Error("shadowed() = true with only the plugin shim on PATH")
	}
}

// An auto-context earlier on PATH than the plugin's own is not auto-approved.
func TestPreToolUseRefusesShadowedBinary(t *testing.T) {
	plugin, fake := t.TempDir(), t.TempDir()
	pluginShim(t, plugin)
	fakeCLI(t, fake)
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	env := testEnv(t.TempDir())

	t.Setenv("PATH", fake+string(os.PathListSeparator)+filepath.Join(plugin, "bin"))
	if out, _ := PreToolUse(env, preInput("Bash", "auto-context status")); out != nil {
		t.Errorf("shadowed binary approved: %s", out)
	}
	// On Windows LookPath ignores the extensionless plugin shim and would
	// still find the fake .bat, so "plugin first" cannot be told apart there;
	// TestShadowedOnWindowsPluginShimOnly covers approval on Windows.
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
	pluginShim(t, plugin)
	fakeCLI(t, cwd)
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	t.Setenv("PATH", "."+string(os.PathListSeparator)+filepath.Join(plugin, "bin"))
	t.Chdir(cwd)
	if out, _ := PreToolUse(testEnv(t.TempDir()), preInput("Bash", "auto-context status")); out != nil {
		t.Errorf("approved via relative PATH: %s", out)
	}
}

// When the plugin's own shim cannot be resolved (while another auto-context
// is on PATH), nothing is approved.
func TestPreToolUseRefusesWhenPluginShimMissing(t *testing.T) {
	fake := t.TempDir()
	fakeCLI(t, fake)
	t.Setenv("CLAUDE_PLUGIN_ROOT", t.TempDir()) // no bin/auto-context
	t.Setenv("PATH", fake)
	if out, _ := PreToolUse(testEnv(t.TempDir()), preInput("Bash", "auto-context status")); out != nil {
		t.Errorf("approved: %s", out)
	}
}

// An empty or relative PATH entry anywhere in PATH counts as shadowing: the
// shell may resolve it from another cwd than the hook's, where a foreign
// auto-context can sit. Nothing named auto-context is reachable from the
// hook's cwd here, so only the entry itself can refuse.
func TestShadowedByAnyRelativeEntry(t *testing.T) {
	plugin := t.TempDir()
	pluginShim(t, plugin)
	t.Chdir(t.TempDir())
	bin, sep := filepath.Join(plugin, "bin"), string(os.PathListSeparator)
	for _, withRoot := range []bool{true, false} {
		if withRoot {
			t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
		} else {
			t.Setenv("CLAUDE_PLUGIN_ROOT", "")
		}
		for _, p := range []string{
			"bin" + sep + bin,
			bin + sep + "bin",
			bin + sep + filepath.Join("node_modules", ".bin"),
			sep + bin,
			bin + sep,
			bin + sep + sep + bin,
			"." + sep + bin,
		} {
			t.Setenv("PATH", p)
			if !shadowed() {
				t.Errorf("root=%v PATH=%q: shadowed() = false", withRoot, p)
			}
			if out, _ := PreToolUse(testEnv(t.TempDir()), preInput("Bash", "auto-context status")); out != nil {
				t.Errorf("root=%v PATH=%q: approved: %s", withRoot, p, out)
			}
		}
	}
	// Absolute entries only: the plugin's own bin first still approves (on
	// Windows LookPath ignores the extensionless shim; nothing to compare).
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	t.Setenv("PATH", bin+sep+t.TempDir())
	if shadowed() {
		t.Error("absolute-only PATH with the plugin bin: shadowed() = true")
	}
}

// Codex's shell never has the plugin's bin/ on PATH, so the bare name is
// never approved for it, whatever PATH the hook sees.
func TestCodexNeverApprovesBareName(t *testing.T) {
	plugin := t.TempDir()
	pluginShim(t, plugin)
	codex := testEnv(t.TempDir())
	codex.Agent = Codex
	for _, root := range []string{"", plugin, "/home/u/.codex/plugins/cache/auto-context/auto-context/1.0.0"} {
		t.Setenv("CLAUDE_PLUGIN_ROOT", root)
		t.Setenv("PATH", filepath.Join(plugin, "bin"))
		for _, c := range allowedCLICalls {
			if out, err := PreToolUse(codex, preInput("Bash", c)); err != nil || out != nil {
				t.Errorf("root=%q PreToolUse %q: out=%q err=%v", root, c, out, err)
			}
			if out, err := PermissionRequest(codex, preInput("Bash", c)); err != nil || out != nil {
				t.Errorf("root=%q PermissionRequest %q: out=%q err=%v", root, c, out, err)
			}
		}
	}
}

// Only the exact key "command" counts: a case variant next to it (or alone)
// may be what the agent runs, so the command is treated as unknown (HS-5).
func TestCommandCaseVariantKeys(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	env := testEnv(t.TempDir())
	for _, raw := range []string{
		`{"command":"rm -rf ~","Command":"auto-context status"}`,
		`{"Command":"auto-context status","command":"auto-context status"}`,
		`{"COMMAND":"auto-context status"}`,
		`{"Command":"auto-context status"}`,
	} {
		in := Input{ToolName: "Bash", ToolInput: []byte(raw)}
		if got := in.Command(); got != "" {
			t.Errorf("%s: Command() = %q, want empty", raw, got)
		}
		if out, err := PreToolUse(env, in); err != nil || out != nil {
			t.Errorf("%s: out=%q err=%v, want nothing", raw, out, err)
		}
	}
	in := Input{ToolName: "Bash", ToolInput: []byte(`{"command":"auto-context status","timeout":5}`)}
	if got := in.Command(); got != "auto-context status" {
		t.Errorf("exact key: Command() = %q", got)
	}
}

// A non-string command, or a tool_input that is not an object, yields "".
func TestCommandArrayStillEmpty(t *testing.T) {
	for _, raw := range []string{
		`{"command":["auto-context","status"]}`,
		`{"command":null}`,
		`{"command":1}`,
		`["auto-context status"]`,
		`"auto-context status"`,
		``,
		`{`,
	} {
		if got := (Input{ToolInput: []byte(raw)}).Command(); got != "" {
			t.Errorf("%s: Command() = %q, want empty", raw, got)
		}
	}
}

func TestCheckEvent(t *testing.T) {
	names := map[string]string{
		"session-start":      "SessionStart",
		"prompt-submit":      "UserPromptSubmit",
		"stop":               "Stop",
		"pre-tool-use":       "PreToolUse",
		"session-end":        "SessionEnd",
		"permission-request": "PermissionRequest",
	}
	for event, name := range names {
		if err := CheckEvent(event, Input{}); err != nil {
			t.Errorf("%s with no name: %v", event, err)
		}
		if err := CheckEvent(event, Input{HookEventName: name}); err != nil {
			t.Errorf("%s with %s: %v", event, name, err)
		}
		for _, other := range names {
			if other != name && CheckEvent(event, Input{HookEventName: other}) == nil {
				t.Errorf("%s accepted %s", event, other)
			}
		}
		if CheckEvent(event, Input{HookEventName: strings.ToLower(name)}) == nil {
			t.Errorf("%s accepted the lower-case name", event)
		}
	}
	if CheckEvent("bogus", Input{HookEventName: "Stop"}) == nil {
		t.Error("unknown event accepted a name")
	}
	var in Input
	if err := json.Unmarshal([]byte(`{"hook_event_name":"Stop"}`), &in); err != nil || in.HookEventName != "Stop" {
		t.Errorf("hook_event_name not decoded: %q, %v", in.HookEventName, err)
	}
}
