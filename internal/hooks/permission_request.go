package hooks

type permissionRequestOutput struct {
	Hook struct {
		EventName string `json:"hookEventName"`
		Decision  struct {
			Behavior string `json:"behavior"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

// PermissionRequest is Codex's counterpart of PreToolUse: it approves the same
// plugin CLI calls when Codex is about to ask for permission. Anything else
// yields no output, leaving the normal approval prompt.
func PermissionRequest(env Env, in Input) ([]byte, error) {
	if !approvable(env, in) {
		return nil, nil
	}
	var out permissionRequestOutput
	out.Hook.EventName = "PermissionRequest"
	out.Hook.Decision.Behavior = "allow"
	return encodeJSON(out)
}
