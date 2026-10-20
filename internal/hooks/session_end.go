package hooks

import (
	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/session"
)

// SessionEnd removes the session's snapshot and journal. It does not open the
// project: nothing else is needed, and the session is closing. Files of
// sessions that end without this hook are removed by SessionStart later.
func SessionEnd(env Env, in Input) ([]byte, error) {
	if !project.Active(env.Root) {
		return nil, nil
	}
	if err := session.Clear(env.Root, in.SessionID); err != nil {
		logx.Errorf(env.Root, "session end: %v", err)
	}
	return nil, nil
}
