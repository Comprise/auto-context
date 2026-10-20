# skills/

Skills плагина. Claude Code находит каждый по `skills/<name>/SKILL.md`; этот README не загружается.

| Skill | Вызов | Назначение |
|---|---|---|
| `auto-context-docs` | Claude сам | правила ведения `<unit>/CLAUDE.md` и `context/<unit>.md`: когда обновлять, когда только `ack`, формат, язык |
| `init` | `/auto-context:init` | первичная разметка: юниты, язык доков, создание доков |
| `sync` | `/auto-context:sync` | обновить устаревшие и недостающие доки |
| `status` | `/auto-context:status`, Claude сам | сводка состояния доков |

- `disable-model-invocation: true` у `init` и `sync`: они меняют файлы проекта и запускаются только пользователем.
- `allowed-tools` есть только у `sync` (`Bash(auto-context status --stale)` для `!`-вставки). Другие разрешения skills не выдают: `ack --exclude` правит конфиг и должен спрашивать.
- Skills вызывают `auto-context` по имени: `bin/` плагина в PATH. Git сами не запускают — для истории юнита есть `auto-context changes`.
- Тексты, которые выводят хуки и CLI (`note:`, `warning:`, `stale (…)`), skills перечисляют дословно (`auto-context-docs` — «Status notes», а также `status` и `sync`): при их изменении в Go правьте и skills.
- Вызовы `auto-context` одобряются без запроса, только если пути юнитов состоят из `A-Za-z0-9_./-`; для юнитов с пробелами или не-ASCII символами в имени Claude Code спросит разрешение.
