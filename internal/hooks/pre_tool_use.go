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
	if !approvable(env, in) {
		return nil, nil
	}
	var out preToolUseOutput
	out.Hook.EventName = "PreToolUse"
	out.Hook.Decision = "allow"
	out.Hook.Reason = "auto-context CLI"
	return encodeJSON(out)
}

// approvable reports whether the tool call is a Bash run of the plugin's own
// CLI that both agents' hooks may approve. Codex's shell never has the plugin's
// bin/ on PATH, so for Codex only the literal Codex.CLI() prefix (quoted as it
// suggests, if needed) is accepted, never the bare name; that path points at
// the plugin's binary itself, so PATH shadowing does not matter for it.
func approvable(env Env, in Input) bool {
	if in.ToolName != "Bash" {
		return false
	}
	cmd := in.Command()
	if env.Agent == Codex {
		abs := Codex.CLI()
		if abs == "auto-context" {
			return false // no usable plugin root: nothing to anchor the path to
		}
		rest, ok := strings.CutPrefix(cmd, abs+" ")
		return ok && allowedCommand("auto-context "+rest)
	}
	return allowedCommand(cmd) && !shadowed()
}

// allowedCommand matches the raw command exactly: no trimming, since bash does
// not split on the Unicode spaces strings.TrimSpace would strip. `ack --exclude`
// edits the committed config, so it keeps the normal permission prompt.
func allowedCommand(cmd string) bool {
	if !ownCLI.MatchString(cmd) {
		return false
	}
	for arg := range strings.SplitSeq(cmd, " ") {
		if strings.Contains(arg, "..") || arg == "--exclude" {
			return false
		}
	}
	return true
}

// shadowed reports whether "auto-context" on PATH may not be the plugin's own
// binary, so the approval could run someone else's program. Any empty or
// relative PATH entry counts: the shell resolves it against its own cwd, which
// may differ from the hook's. It is a best effort: the hook sees Claude Code's
// PATH, which is close to the Bash tool's.
func shadowed() bool {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			return true
		}
	}
	pluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT")
	if pluginRoot == "" {
		return false
	}
	found, err := exec.LookPath("auto-context")
	if errors.Is(err, exec.ErrDot) {
		return true // found in the cwd (Windows searches it before PATH)
	}
	if err != nil {
		return false // not on our PATH; nothing to compare
	}
	got, err1 := filepath.EvalSymlinks(found)
	want, err2 := filepath.EvalSymlinks(filepath.Join(pluginRoot, "bin", "auto-context"))
	return err1 != nil || err2 != nil || got != want
}
