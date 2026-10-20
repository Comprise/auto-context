package hooks

// Agent is the coding agent that runs the hooks. Its zero value means Claude.
type Agent string

const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

// codexSkillPrefix is how Codex addresses a skill, and a skill-backed command,
// in text.
const codexSkillPrefix = "$"

// ParseAgent maps a --agent value to an Agent; ok is false for anything else.
func ParseAgent(s string) (Agent, bool) {
	switch Agent(s) {
	case Claude:
		return Claude, true
	case Codex:
		return Codex, true
	}
	return "", false
}

// Skill is how texts for this agent refer to the plugin skill name.
func (a Agent) Skill(name string) string {
	if a == Codex {
		return codexSkillPrefix + name
	}
	return "auto-context:" + name
}

// Command is how texts for this agent refer to the plugin command name.
func (a Agent) Command(name string) string {
	if a == Codex {
		return codexSkillPrefix + name
	}
	return "/auto-context:" + name
}
