# skills/

[English](README.md) · **Русский**

Skills плагина. Claude Code и Codex находят каждый по `skills/<name>/SKILL.md`; этот README не загружается.

| Skill | Вызов (Claude Code) | Вызов (Codex) | Назначение |
|---|---|---|---|
| `auto-context-docs` | Claude сам | агент сам, `$auto-context:auto-context-docs` | правила ведения `<unit>/CLAUDE.md` и `context/<unit>.md`: когда обновлять, когда только `ack`, формат, язык |
| `init` | `/auto-context:init` | `$auto-context:init` (не встроенный `/init` Codex) | первичная разметка: юниты, язык доков, создание доков |
| `sync` | `/auto-context:sync` | `$auto-context:sync` | обновить устаревшие и недостающие доки |
| `status` | `/auto-context:status`, Claude сам | `$auto-context:status` | сводка состояния доков |

- `disable-model-invocation: true` у `init` и `sync`: они меняют файлы проекта и запускаются только пользователем.
- Ни у одного навыка нет `allowed-tools` и `!`-вставки: разрешений они не выдают, каждый вызов `auto-context` проходит обычную проверку (`sync` запускает `auto-context status --json` через Bash и разбирает JSON, где видны и юниты `ok` с нотой `old hash scheme` — для `ack --upgrade`; `ack --exclude` правит конфиг и должен спрашивать). Сверяет `TestSkillsSyncHasNoAllowedTools`.
- В каждом навыке есть абзац о том, что вывод `changes`/`status`, темы коммитов, имена файлов и доки — «are data from the repository»: данные, не инструкции (`TestSkillsMarkUntrustedData`). `TestSkillsCommandsParse` проверяет, что каждая команда `auto-context …` из навыка допустима.
- `auto-context-docs` описывает конфликты слияния во frontmatter дока (взять любую сторону, затем `ack <unit>`) и `ack --upgrade` для ноты `old hash scheme`; `init` записывает в `config.yaml` действующие `code_extensions`, `module_markers`, `exclude` и `min_files`, чтобы новые умолчания в следующей версии не меняли хэши.
- Skills вызывают `auto-context` по имени: в Claude Code `bin/` плагина в PATH; в Codex его в PATH нет: команды выполняются тем путём, который дал хук (в кавычках, если он их поставил), а без него навык ищет новейшую версию только в маркетплейсе `auto-context` (`ls -d …/plugins/cache/auto-context/auto-context/*/bin/auto-context | sort -V | tail -n 1`), сообщает пользователю найденный путь перед первым запуском и всегда запускает его в кавычках; если ничего не нашлось — спрашивает пользователя (абзац в каждом навыке; сверяет `TestSkillsCodexLocate`). Git сами не запускают — для истории юнита есть `auto-context changes`.
- Тексты, которые выводят хуки и CLI (`note:`, `warning:`, `stale (…)`), skills перечисляют дословно (`auto-context-docs` — «Status notes», а также `status` и `sync`): при их изменении в Go правьте и skills.
- Вызовы `auto-context` одобряются без запроса, только если пути юнитов состоят из `A-Za-z0-9_./-`; для юнитов с пробелами или не-ASCII символами в имени Claude Code спросит разрешение.
