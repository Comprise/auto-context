# hooks/

[English](README.md) · **Русский**

Два файла регистрации хуков: `hooks.json` (Claude Code) и `codex.json` (Codex; на него указывает `.codex-plugin/plugin.json`, команды с `--agent codex`). Каждый хук вызывает `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context hook <event>`; логика — в `internal/hooks`.

Claude Code (`hooks.json`):

| Событие | Подкоманда | Тайм-аут | Что делает |
|---|---|---|---|
| `SessionStart` | `session-start` | 60 с | статусы доков, оглавление в корневом `CLAUDE.md`, уведомление о stale/missing и проблемах |
| `UserPromptSubmit` | `prompt-submit` | 30 с | снимок файлов на начало хода |
| `Stop` | `stop` | 60 с | сравнение со снимком, авто-ack, напоминание обновить доки |
| `PreToolUse` (`Bash`, `if: Bash(auto-context *)`) | `pre-tool-use` | 10 с | авто-одобрение безопасных вызовов `auto-context` |
| `SessionEnd` | `session-end` | 10 с | удаление снимка и журнала сессии |

Codex (`codex.json`):

| Событие | Подкоманда | Тайм-аут | Что делает |
|---|---|---|---|
| `SessionStart` | `session-start` | 60 с | как у Claude Code |
| `UserPromptSubmit` | `prompt-submit` | 30 с | снимок файлов на начало хода |
| `Stop` | `stop` | 60 с | как у Claude Code; напоминание — `{"decision":"block","reason":…}` |
| `PermissionRequest` (`matcher: ^Bash$`) | `permission-request` | 10 с | авто-одобрение тех же вызовов CLI, что `pre-tool-use`, только по абсолютному пути плагина (голое `auto-context` — никогда) |
| `SessionEnd` | `session-end` | 3 с | удаление снимка и журнала (бюджет git 2 с); при выходе из TUI Codex его не вызывает |

- Хук никогда не блокирует сессию: шим всегда выходит с 0, ошибки идут в `.auto-context/log`.
- `TestHooksJSONContract` (`hooks.json`) и `TestCodexHooksManifest` (`codex.json`) сверяют события, подкоманды, таймауты и бюджет git с `hookTable`/`budgetFor` в `cmd/auto-context`: новое событие добавляйте в оба файла и в код.
- `if` у `PreToolUse` — фильтр Claude Code, чтобы бинарь не запускался на каждый Bash-вызов; само решение принимает бинарь.
- У Codex нет `if`: фильтр по `matcher: ^Bash$`, а решение принимает бинарь.
