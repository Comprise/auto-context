# skills/

Skills плагина. Claude Code и Codex находят каждый по `skills/<name>/SKILL.md`; этот README не загружается.

| Skill | Вызов (Claude Code) | Вызов (Codex) | Назначение |
|---|---|---|---|
| `auto-context-docs` | Claude сам | агент сам, `$auto-context:auto-context-docs` | правила ведения `<unit>/CLAUDE.md` и `context/<unit>.md`: когда обновлять, когда только `ack`, формат, язык |
| `init` | `/auto-context:init` | `$auto-context:init` (не встроенный `/init` Codex) | первичная разметка: юниты, язык доков, создание доков |
| `sync` | `/auto-context:sync` | `$auto-context:sync` | обновить устаревшие и недостающие доки |
| `status` | `/auto-context:status`, Claude сам | `$auto-context:status` | сводка состояния доков |

- `disable-model-invocation: true` у `init` и `sync`: они меняют файлы проекта и запускаются только пользователем.
- `allowed-tools` есть только у `sync` (`Bash(auto-context status --stale)` для `!`-вставки). Другие разрешения skills не выдают: `ack --exclude` правит конфиг и должен спрашивать.
- Skills вызывают `auto-context` по имени: в Claude Code `bin/` плагина в PATH; в Codex его в PATH нет: команды выполняются тем путём, который дал хук (в кавычках, если он их поставил), а без него навык находит бинарник командой `ls -td …` (абзац в каждом навыке; сверяет `TestSkillsCodexLocate`). Git сами не запускают — для истории юнита есть `auto-context changes`.
- Тексты, которые выводят хуки и CLI (`note:`, `warning:`, `stale (…)`), skills перечисляют дословно (`auto-context-docs` — «Status notes», а также `status` и `sync`): при их изменении в Go правьте и skills.
- Вызовы `auto-context` одобряются без запроса, только если пути юнитов состоят из `A-Za-z0-9_./-`; для юнитов с пробелами или не-ASCII символами в имени Claude Code спросит разрешение.
