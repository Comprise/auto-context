package hooks

import (
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
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

// pluginRootSafe matches a path of plain characters, which needs no shell
// quoting when put into a suggested command.
var pluginRootSafe = regexp.MustCompile(`^/[A-Za-z0-9_./-]*$`)

// CLI is how texts for this agent name the plugin binary, with the plugin
// root taken from the environment (see Env.CLI).
func (a Agent) CLI() string {
	return Env{Agent: a}.CLI()
}

// CLI is how texts for env's agent name the plugin binary. Codex's shell tool
// has no plugin bin/ on PATH, so it gets the absolute path under the plugin
// root (see codexCLI): env.PluginRoot, else PLUGIN_ROOT (set by Codex), else
// CLAUDE_PLUGIN_ROOT. An unset or unusable root falls back to the bare name.
func (e Env) CLI() string {
	if e.Agent != Codex {
		return "auto-context"
	}
	root := e.PluginRoot
	if root == "" {
		root = os.Getenv("PLUGIN_ROOT")
	}
	if root == "" {
		root = os.Getenv("CLAUDE_PLUGIN_ROOT")
	}
	return codexCLI(root)
}

// codexCLI builds the shell text that runs the plugin binary under root. The
// root must be a POSIX absolute path of valid UTF-8 without control characters,
// else the bare name is returned. A path of plain characters (pluginRootSafe)
// is used as is; any other is wrapped in single quotes. The path is joined
// with "/" on every OS: the text goes to a POSIX shell, not to the filesystem
// API. The function reads no environment.
func codexCLI(root string) string {
	if !strings.HasPrefix(root, "/") || !utf8.ValidString(root) {
		return "auto-context"
	}
	for _, r := range root {
		if r < 0x20 || r == 0x7f {
			return "auto-context"
		}
	}
	p := path.Join(root, "bin", "auto-context")
	if pluginRootSafe.MatchString(p) {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
