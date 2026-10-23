package hooks

// PermissionRequest is Codex's counterpart of PreToolUse: it approves the same
// plugin CLI calls when Codex is about to ask for permission. Anything else
// yields no output, leaving the normal approval prompt.
func PermissionRequest(env Env, in Input) ([]byte, error) {
	if !approvable(env, in) {
		return nil, nil
	}
	return encodeJSON(hookOutput{Hook: &hookSpecific{EventName: "PermissionRequest", Decision: &permissionDecision{Behavior: "allow"}}})
}
