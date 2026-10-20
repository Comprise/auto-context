// Package hooks implements the Claude Code hook handlers.
package hooks

import (
	"bytes"
	"encoding/json"
	"time"

	"auto-context/internal/fsutil"
	"auto-context/internal/project"
)

// Input is the JSON payload Claude Code passes to a hook on stdin.
type Input struct {
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// Command is the Bash command of the tool call, or "" when tool_input has no
// string command (an array in Codex, say): that must not fail the whole parse.
func (in Input) Command() string {
	var v struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(in.ToolInput, &v) != nil {
		return ""
	}
	return v.Command
}

// Env carries the resolved project root and the clock.
type Env struct {
	Root  string
	Now   func() time.Time
	Agent Agent // the zero value means Claude
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
