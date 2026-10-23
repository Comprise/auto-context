# Ревью по области: cmd, project, config, fsutil, gitx, logx, textx, testutil (ревьюер из первого запроса)

1. **High — локальный `.git/config` выполняет код через `git log` в `auto-context changes`** (`internal/project/changes.go:86`, `internal/gitx/gitx.go:116`): `log.showSignature=true` + `gpg.program=./evil.sh` + подписанный коммит → команда атакующего; `changes` авто-одобряется PreToolUse (`internal/hooks/pre_tool_use.go:14`). ПОДТВЕРЖДЕНО (scratchpad/r1). Исправление: `-c log.showSignature=false`, `--no-show-signature`; плюс `-c core.hooksPath=/dev/null`, `-c diff.external=`; регрессионный тест рядом с TestRunIgnoresFSMonitor.
2. **Medium — таймаут gitx не срабатывает при живом потомке git** (`gitx.go:117-124`): нет `cmd.WaitDelay`, потомок держит pipe. ПОДТВЕРЖДЕНО (Timeout=1s → возврат через 30 с). Исправление: `WaitDelay≈1s`, на Unix `Setpgid` + kill группы.
3. **Medium — CLI создаёт `.auto-context/` без `.gitignore`** (`internal/project/project.go:326-337` SaveCache; `internal/logx/logx.go:41`): `status` в проекте, включённом через `context/_root.md`, оставляет `?? .auto-context/`. ПОДТВЕРЖДЕНО. Исправление: `fsutil.EnsureStateDir` перед записью.
4. **Low/Medium — `ack --exclude` не экранирует glob и регистр** (`project.go:318-321`): `lib[v1]` не исключается, exit 0. ПОДТВЕРЖДЕНО для `[`; регистр ПО КОДУ.
5. **Low — защита FindRoot от $HOME обходится неканоническим HOME** (`internal/config/config.go:447-451`, `bin/auto-context:23`): слеш в конце или симлинк. ПОДТВЕРЖДЕНО.
6. **Low — AddExclude через YAML-якорь меняет другие ключи** (`config.go:389-390`). ПОДТВЕРЖДЕНО. Косметика: теряются пустые строки и `%YAML`.
7. **Low — хуки и CLI выбирают корень по-разному** (`main.go:155-167` vs `main.go:233`): после `cd` во вложенный репо подсказанный `ack` идёт в другой проект. ПО КОДУ.
8. **Low — `StripControls` пропускает bidi/Cf в вывод `changes`** (`internal/textx/textx.go:29-31`, `changes.go:135`). ПО КОДУ.
9. **Low (Windows/без flock) — гонка снятия протухшего lock** (`internal/logx/lock_excl.go:241-243`). ПО КОДУ.
10. Пробелы в тестах: п.1, п.2, п.3, п.4–6, bidi в textx_test.

Проверено и в порядке: fsutil.Resolve (.., абсолютные пути, симлинки, junction, .GIT/8.3/HFS через SameFile, state-dir, ResolveDoc .md), writeAtomic, CLI-аргументы и коды выхода, валидация конфига, аргументы git (`--`, `:(literal)`, hex commit).

Общее: зрелый код; системный пробел — защита от локального .git/config через список известных опасных ключей.
