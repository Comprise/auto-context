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
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Source         string `json:"source"`
	StopHookActive bool   `json:"stop_hook_active"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// Env carries the resolved project root and the clock.
type Env struct {
	Root string
	Now  func() time.Time
}

// load prepares the state dir and opens the project. It reports nothing
// itself: SessionStart logs and shows config problems, the other hooks do not
// repeat them.
func load(env Env) (*project.Project, error) {
	if err := fsutil.EnsureStateDir(env.Root); err != nil {
		return nil, err
	}
	p, err := project.Open(env.Root)
	if err != nil {
		return nil, err
	}
	return p, nil
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
