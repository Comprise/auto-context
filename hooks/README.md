# hooks/

`hooks.json` — регистрация хуков. Каждый вызывает `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context hook <event>`; логика — в `internal/hooks`.

| Событие | Подкоманда | Тайм-аут | Что делает |
|---|---|---|---|
| `SessionStart` | `session-start` | 60 с | статусы доков, оглавление в корневом `CLAUDE.md`, уведомление о stale/missing и проблемах |
| `UserPromptSubmit` | `prompt-submit` | 30 с | снимок файлов на начало хода |
| `Stop` | `stop` | 60 с | сравнение со снимком, авто-ack, напоминание обновить доки |
| `PreToolUse` (`Bash`, `if: Bash(auto-context *)`) | `pre-tool-use` | 10 с | авто-одобрение безопасных вызовов `auto-context` |
| `SessionEnd` | `session-end` | 10 с | удаление снимка и журнала сессии |

- Хук никогда не блокирует сессию: шим всегда выходит с 0, ошибки идут в `.auto-context/log`.
- Список событий сверяется с `hookHandlers` в `cmd/auto-context` тестом `TestHooksJSONContract`: новое событие добавляйте в оба места.
- `if` у `PreToolUse` — фильтр Claude Code, чтобы бинарь не запускался на каждый Bash-вызов; само решение принимает бинарь.
