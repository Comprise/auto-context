package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"auto-context/internal/testutil"
)

// manifestCommands returns event -> command of a hooks manifest.
func manifestCommands(t *testing.T, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cmds := map[string]string{}
	for event, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				cmds[event] = h.Command
			}
		}
	}
	if len(cmds) == 0 {
		t.Fatalf("%s lists no commands", name)
	}
	return cmds
}

// buildPlugin lays out a plugin dir whose path has a space: the real shim and
// the binary built from this package under the libexec name the shim picks.
func buildPlugin(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the shim needs a POSIX sh")
	}
	if testing.Short() {
		t.Skip("builds the binary")
	}
	plug := filepath.Join(t.TempDir(), "plugin dir")
	shimText, err := os.ReadFile(filepath.Join("..", "..", "bin", "auto-context"))
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, plug, "bin/auto-context", string(shimText))
	if err := os.Chmod(filepath.Join(plug, "bin", "auto-context"), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(plug, "libexec", hostBinary()), ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return plug
}

// runManifest runs a manifest command as the agent does: through sh -c, with
// the plugin root in the environment.
func runManifest(t *testing.T, command, plug, project string, stdin []byte) (int, string) {
	t.Helper()
	c := exec.Command("sh", "-c", command)
	c.Dir = project
	c.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+plug, "PLUGIN_ROOT="+plug, "CLAUDE_PROJECT_DIR="+project)
	c.Stdin = bytes.NewReader(stdin)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String()
}

// T-M6: every command of hooks/hooks.json and hooks/codex.json runs through
// the real shim and binary from a plugin path with a space, exits 0 and
// answers nothing or one JSON object; an oversized payload is logged.
func TestE2EHooksThroughShim(t *testing.T) {
	plug := buildPlugin(t)
	for _, manifest := range []string{"hooks.json", "codex.json"} {
		for event, command := range manifestCommands(t, manifest) {
			project := repo(t)
			in := []byte(`{"session_id":"e2e","cwd":` + quote(project) + `,"tool_name":"Bash","tool_input":{"command":"auto-context status"}}`)
			code, out := runManifest(t, command, plug, project, in)
			if code != 0 {
				t.Errorf("%s %s: exit %d", manifest, event, code)
			}
			if out = strings.TrimSpace(out); out != "" {
				var v map[string]any
				if err := json.Unmarshal([]byte(out), &v); err != nil {
					t.Errorf("%s %s: not JSON: %q", manifest, event, out)
				}
			}
			if data, err := os.ReadFile(filepath.Join(project, ".auto-context", "log")); err == nil {
				t.Errorf("%s %s: hook errors logged: %q", manifest, event, data)
			}
		}
	}

	project := repo(t)
	big := append([]byte(`{"session_id":"e2e","pad":"`), bytes.Repeat([]byte("x"), 2<<20)...)
	big = append(big, `"}`...)
	command := manifestCommands(t, "hooks.json")["Stop"]
	if code, out := runManifest(t, command, plug, project, big); code != 0 || out != "" {
		t.Errorf("2 MiB payload: exit %d out=%q", code, out)
	}
	data, _ := os.ReadFile(filepath.Join(project, ".auto-context", "log"))
	if !strings.Contains(string(data), "input larger than") {
		t.Errorf("log = %q", data)
	}
}
