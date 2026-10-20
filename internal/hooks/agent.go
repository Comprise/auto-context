package hooks

import (
	"os"
	"path"
	"regexp"
)

// Agent is the coding agent that runs the hooks. Its zero value means Claude.
type Agent string

const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

// codexSkillPrefix is how Codex addresses a skill, and a skill-backed command,
// in text.
const codexSkillPrefix = "$auto-context:"

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

// pluginRootSafe limits the plugin root put into suggested commands: the text
// is pasted into a shell, so nothing but plain path characters may appear.
var pluginRootSafe = regexp.MustCompile(`^/[A-Za-z0-9_./-]*$`)

// CLI is how texts for this agent name the plugin binary. Codex's shell tool
// has no plugin bin/ on PATH, so it gets the absolute path under
// CLAUDE_PLUGIN_ROOT; an unset or unsafe root falls back to the bare name.
// The root is a POSIX path (pluginRootSafe), so it is joined with "/" on
// every OS: the text goes to a POSIX shell, not to the filesystem API.
func (a Agent) CLI() string {
	if a == Codex {
		if root := os.Getenv("CLAUDE_PLUGIN_ROOT"); pluginRootSafe.MatchString(root) {
			return path.Join(root, "bin", "auto-context")
		}
	}
	return "auto-context"
}
