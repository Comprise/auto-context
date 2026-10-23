// Package hooks implements the Claude Code and Codex hook handlers.
package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"auto-context/internal/fsutil"
	"auto-context/internal/project"
)

// Input is the JSON payload the agent (Claude Code or Codex) passes to a hook on stdin.
type Input struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// Command is the Bash command of the tool call, or "" when tool_input has no
// string under exactly the key "command". Both agents document it as a
// string; any other shape is tolerated as "" rather than failing the parse.
// encoding/json matches struct fields case-insensitively, so a "Command" key
// beside (or instead of) "command" could make the hook judge another command
// than the agent runs: any such case variant makes the command unknown.
func (in Input) Command() string {
	var m map[string]json.RawMessage
	if json.Unmarshal(in.ToolInput, &m) != nil {
		return ""
	}
	for k := range m {
		if k != "command" && strings.EqualFold(k, "command") {
			return ""
		}
	}
	var cmd string
	if json.Unmarshal(m["command"], &cmd) != nil {
		return ""
	}
	return cmd
}

// eventNames maps a hook subcommand to the hook_event_name the agent sends.
var eventNames = map[string]string{
	"session-start":      "SessionStart",
	"prompt-submit":      "UserPromptSubmit",
	"stop":               "Stop",
	"pre-tool-use":       "PreToolUse",
	"session-end":        "SessionEnd",
	"permission-request": "PermissionRequest",
}

// errWrongEvent is returned by CheckEvent for a payload of another event.
var errWrongEvent = errors.New("hook_event_name does not match the hook")

// CheckEvent reports an error when the payload names an event other than the
// one the hook subcommand event handles. An absent hook_event_name passes.
func CheckEvent(event string, in Input) error {
	if in.HookEventName == "" {
		return nil
	}
	if want, ok := eventNames[event]; !ok || in.HookEventName != want {
		return errWrongEvent
	}
	return nil
}

// Env carries the resolved project root, the clock and the agent.
type Env struct {
	Root  string
	Now   func() time.Time
	Agent Agent // the zero value means Claude
	// PluginRoot is the plugin's install directory; when empty, CLI reads it
	// from the environment.
	PluginRoot string
}

// load prepares the state dir and opens the project. It reports nothing
// itself: SessionStart logs and shows config problems, the other hooks do not
// repeat them.
func load(env Env) (*project.Project, error) {
	if err := fsutil.EnsureStateDir(env.Root); err != nil {
		return nil, err
	}
	return project.Open(env.Root)
}

// hookOutput is the answer of every hook; unset fields are left out, so each
// hook writes only its own keys.
type hookOutput struct {
	SystemMessage string        `json:"systemMessage,omitempty"`
	Decision      string        `json:"decision,omitempty"` // Codex Stop: "block"
	Reason        string        `json:"reason,omitempty"`   // Codex Stop
	Hook          *hookSpecific `json:"hookSpecificOutput,omitempty"`
}

// hookSpecific is hookSpecificOutput.
type hookSpecific struct {
	EventName                string `json:"hookEventName"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	// Decision is PermissionRequest's {"behavior": ...}.
	Decision *permissionDecision `json:"decision,omitempty"`
}

type permissionDecision struct {
	Behavior string `json:"behavior"`
}

// encodeJSON marshals v without HTML escaping and without a trailing newline.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
