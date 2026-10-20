package hooks

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ownCLI matches a single plain invocation of the plugin's own CLI: no shell
// metacharacters, quotes, variables or extra whitespace can appear.
var ownCLI = regexp.MustCompile(`^auto-context (ack|status|units|version|changes)( [A-Za-z0-9_./-]+)*$`)

type preToolUseOutput struct {
	Hook struct {
		EventName string `json:"hookEventName"`
		Decision  string `json:"permissionDecision"`
		Reason    string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// PreToolUse auto-approves Bash calls of the auto-context CLI.
// Anything else yields no output, leaving the normal permission flow. It never
// writes (it only resolves PATH) and is not gated by activation.
func PreToolUse(env Env, in Input) ([]byte, error) {
	if in.ToolName != "Bash" || !allowedCommand(in.ToolInput.Command) || shadowed() {
		return nil, nil
	}
	var out preToolUseOutput
	out.Hook.EventName = "PreToolUse"
	out.Hook.Decision = "allow"
	out.Hook.Reason = "auto-context CLI"
	return encodeJSON(out)
}

// allowedCommand matches the raw command exactly: no trimming, since bash does
// not split on the Unicode spaces strings.TrimSpace would strip. `ack --exclude`
// edits the committed config, so it keeps the normal permission prompt.
func allowedCommand(cmd string) bool {
	if !ownCLI.MatchString(cmd) {
		return false
	}
	for _, arg := range strings.Split(cmd, " ") {
		if strings.Contains(arg, "..") || arg == "--exclude" {
			return false
		}
	}
	return true
}

// shadowed reports whether "auto-context" on PATH is not the plugin's own
// binary, so the approval would run someone else's program. It is a best
// effort: the hook sees Claude Code's PATH, which is close to the Bash tool's.
func shadowed() bool {
	pluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT")
	if pluginRoot == "" {
		return false
	}
	found, err := exec.LookPath("auto-context")
	if errors.Is(err, exec.ErrDot) {
		return true // found through a relative PATH entry such as "."
	}
	if err != nil {
		return false // not on our PATH; nothing to compare
	}
	got, err1 := filepath.EvalSymlinks(found)
	want, err2 := filepath.EvalSymlinks(filepath.Join(pluginRoot, "bin", "auto-context"))
	return err1 != nil || err2 != nil || got != want
}
