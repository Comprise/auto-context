# 4A. Best Practices & Standards (Go 1.27, протокол хуков, сборка)

Коммит af0cae8. Находки фаз 1–3 не переоткрываются (WaitDelay в gitx, `-race`/`-shuffle` в CI, GOGC/лимит памяти, дедлайн только для git как архитектурная проблема и т.п.); где тема пересекается, здесь только угол «идиома/стандарт».

## Счётчики

Critical 0 · High 0 · Medium 4 · Low 12 · Info 6.

## Что запускалось

| Инструмент | Результат |
|---|---|
| `go vet ./...` | чисто |
| `staticcheck -checks all ./...` (latest) | 1: ST1020 `internal/session/session.go:249` |
| `modernize ./...` (x/tools latest) | 13: 2× `errors.AsType`, 2× `WaitGroup.Go`, 9× range-over-int (все в тестах) |
| `nilness` | чисто |
| `shadow` | 11 — все идиоматичные `if err := …; err != nil`, ошибок нет |
| `govulncheck ./...` | No vulnerabilities found |
| `go list -m -u all` | обновлений прямых зависимостей нет; `gopkg.in/check.v1` (транзитивно из тестов yaml.v3) |
| `go mod tidy -diff` | чисто |
| Пробная миграция на `go.yaml.in/yaml/v3 v3.0.5` в копии репо | `go test ./...` зелёный, бинарь +4 КБ, `gopkg.in/check.v1` исчез из go.sum |

Общая оценка: код уже современный — `slices`/`maps`, `strings.SplitSeq`, `min`/`max`, `for range n`, `atomic.Int64`, `errors.Is/As` + `%w` повсеместно, `io.ReadAll`, нет `ioutil`, `sort`, `interface{}`. Устаревших API (SA1019) нет. Основные пробелы — в инфраструктуре (зависимость YAML, контекст отмены, воспроизводимость сборки, линтеры в CI), а не в стиле.

---

## Medium

### BP-M1. `gopkg.in/yaml.v3` архивирован — перейти на `go.yaml.in/yaml/v3`

- **Где:** `go.mod`, `internal/config/config.go:20`, `internal/frontmatter/frontmatter.go:14`.
- **Текущее:** `gopkg.in/yaml.v3 v3.0.1` (2022, репозиторий go-yaml/yaml архивирован в апреле 2025). Исправлений больше не будет; при этом YAML парсится из файлов враждебного репо (`config.yaml`, frontmatter `context/*.md`).
- **Рекомендуемое:** `go.yaml.in/yaml/v3` — официальное продолжение от YAML-организации, API идентичен (включая `yaml.Node`, `Kind`, `Content`, `NewEncoder/Decoder`, `TypeError`), последняя версия v3.0.5. Проверено в копии: замена импорта + `go mod tidy`, все тесты зелёные, бинарь +4 КБ, из go.sum уходит `gopkg.in/check.v1`.
- **Почему не goccy/go-yaml:** у него другой AST (`ast.Node`/`parser.ParseBytes`), `AddExclude` и `indexFileKeys`/`decodeConfig` (`config.go:188-195, 355-410, 468`) пришлось бы переписать; поведение duplicate keys/ошибок типа иное → риск регрессий в коде, на который уже есть поведенческие тесты (Q-M3). `go.yaml.in/yaml/v4` пока rc.6 — не для продакшена.
- **Миграция:**
  ```go
  // config.go, frontmatter.go
  import yaml "go.yaml.in/yaml/v3"
  ```
  ```sh
  go get go.yaml.in/yaml/v3@v3.0.5 && go mod tidy
  ```
  Комментарии `config.go:173-174` («as yaml.v3 defines») оставить — семантика та же.

### BP-M2. Глобальный дедлайн `gitx.SetDeadline` вместо `context.Context`

- **Где:** `internal/gitx/gitx.go:19-34,104-114`, `cmd/auto-context/main.go:169-172`; `var Timeout` — тоже изменяемая глобалка для тестов.
- **Текущее:** бюджет хука хранится в package-level `atomic.Int64`; `Run` создаёт `context.WithTimeout(context.Background(), …)`. Ограничение действует только на git; обход дерева, хэширование, rootindex о дедлайне не знают (это и есть архитектурная сторона P-M6/A-L «дедлайн только для git»). Тесты обязаны сбрасывать глобальное состояние (`defer gitx.SetDeadline(time.Time{})`), параллельные тесты с разными бюджетами невозможны.
- **Рекомендуемое (идиома Go):** контекст передаётся первым параметром сверху вниз; один `context.WithTimeout` на хук; дорогие циклы проверяют `ctx.Err()`.
  ```go
  // main.go
  ctx := context.Background()
  if b, ok := budgetFor(event, agent); ok {
      var cancel context.CancelFunc
      ctx, cancel = context.WithTimeout(ctx, b)
      defer cancel()
  }
  out, err = h(ctx, hooks.Env{Root: root, Now: time.Now, Agent: agent}, in)

  // gitx
  func Run(ctx context.Context, dir string, limit int, args ...string) (Result, error) {
      ctx, cancel := context.WithTimeout(ctx, Timeout) // min(родительский дедлайн, Timeout) — автоматически
      defer cancel()
      cmd := exec.CommandContext(ctx, "git", full...)
      ...
      if ctx.Err() != nil { e.TimedOut = true }
  }

  // hashing / walk: в цикле воркера
  if err := ctx.Err(); err != nil { return err }
  ```
- **Миграция:** поэтапно — сначала `gitx.Run(ctx, …)` + `project.Open(ctx, root)`, `Env.Ctx` (или параметр хендлера); `SetDeadline` оставить как обёртку на один релиз и удалить. CLI-команды передают `context.Background()` (или `signal.NotifyContext` для Ctrl-C). Заодно убирает нужду в «not run: the hook's time is up» — это даёт `ctx.Err()` до старта.

### BP-M3. Toolchain не закреплён — релизные бинари невоспроизводимы

- **Где:** `go.mod` (`go 1.27`, нет `toolchain`), `.github/workflows/release.yml` (`setup-go` с `go-version-file: go.mod`).
- **Текущее:** `setup-go` по `go 1.27` берёт последний доступный 1.27.x на момент сборки. Пересборка того же тега через месяц даст другой бинарь (другой патч компилятора/stdlib) — attestation (`attest-build-provenance`) подтверждает, «где собрано», но независимая проверка «из этого коммита получается этот sha256» невозможна. Флаги в остальном правильные: `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X main.version=…"`, VCS-штамп детерминирован коммитом, кэш модулей в релизе выключен, `go mod verify` есть.
- **Рекомендуемое:**
  ```
  go 1.27.0
  toolchain go1.27.3
  ```
  `setup-go` (v5) читает `toolchain` раньше `go`; локально `GOTOOLCHAIN=auto` скачает ту же версию. Обновлять `toolchain` осознанно (Dependabot `gomod` это умеет). Дополнительно — шаг в release: собрать каждый таргет дважды (или в CI на другом раннере) и сравнить sha256; записать `go version -m` бинарей в лог релиза.
- **Сопутствующее (Low):** логика сборки продублирована в `Makefile:dist` и `release.yml:Build` (одинаковые флаги в двух местах). Сделать `make dist VERSION=$GITHUB_REF_NAME PLATFORMS=$GOOS/$GOARCH` единственным источником, либо вынести флаги в переменную `LDFLAGS`/`GOFLAGS` в Makefile и вызвать из workflow.

### BP-M4. В CI нет статического анализа кроме `go vet`

- **Где:** `.github/workflows/ci.yml` (только `go mod verify`, `go vet`, `go test`). T-M5 покрыл `-race/-shuffle/coverage/fuzz`; линтеры — отдельный пробел.
- **Текущее:** staticcheck/govulncheck/modernize запускаются только вручную; уже накопился дрейф (13 предложений modernize, ST1020). Для проекта с моделью угроз «враждебный репо» govulncheck в CI особенно уместен: о CVE в yaml/stdlib узнаете при следующем ручном прогоне.
- **Рекомендуемое:** закрепить версии инструментов через `tool`-директиву (Go 1.24+) в отдельном модуле, чтобы не тянуть их в граф основного модуля, и вызывать из CI:
  ```
  # tools/go.mod
  module auto-context/tools
  go 1.27.0
  tool (
      honnef.co/go/tools/cmd/staticcheck
      golang.org/x/vuln/cmd/govulncheck
  )
  ```
  ```yaml
  - run: go tool -modfile=tools/go.mod staticcheck ./...
  - run: go tool -modfile=tools/go.mod govulncheck ./...
  - run: go fix -diff ./... # с Go 1.26 `go fix` включает modernize-анализаторы; падать при непустом diff
  ```
  (Если `go fix -diff` в 1.27 недоступен — `modernize` из x/tools с закреплённой версией.) Плюс плановый запуск govulncheck по cron, чтобы ловить новые CVE без коммитов.

---

## Low

### BP-L1. `errors.As` → `errors.AsType` (Go 1.26)
`internal/config/config.go:126-127,180`, `internal/gitx/gitx.go:65-68,77-78,94-95`.
```go
// было
var e *Error
if errors.As(err, &e) { return e.Code }
// стало
if e, ok := errors.AsType[*Error](err); ok { return e.Code }
```
Типобезопасно (нет указателя на указатель, нет паники при неверном target) и короче. Механически применяется `modernize -fix`.

### BP-L2. `wg.Add(1); go func(){ defer wg.Done() … }()` → `wg.Go` (Go 1.25)
`internal/hashing/hashing.go:266-276`, `internal/units/walk.go:276-298`.
```go
for range workers {
    wg.Go(func() {
        buf := make([]byte, 32<<10)
        for rel := range jobs { … }
    })
}
```

### BP-L3. Тесты: range-over-int, `b.Loop`, `slices.Equal`
- 9 циклов `for i := 0; i < n; i++` в тестах (`main_test.go:809`, `config_test.go:67`, `fsutil_test.go:44`, `rootindex_test.go:100,167`, `snapshot_test.go:110,115`, `units_test.go:33,380`) → `for i := range n`.
- `internal/session/snapshot_test.go:123` `for i := 0; i < b.N; i++` → `for b.Loop()` (Go 1.24: таймер исключает setup, нет устранения мёртвого кода).
- `reflect.DeepEqual` на `[]string` (journal_test, config_test) → `slices.Equal` (типобезопасно, быстрее, понятнее при nil vs empty — учтите, что `slices.Equal(nil, []string{})` == true, а DeepEqual — false; где различие важно, оставить DeepEqual).

### BP-L4. `os.Root` вместо самописного `fsutil.Resolve` — применимо только частично
`internal/fsutil/fsutil.go:63-152`.

Что даст `os.Root` (Go 1.24; в 1.25 добавлены `MkdirAll`, `Rename`, `Chmod`, `ReadFile`, `WriteFile`, `Readlink`, `RemoveAll`, `Symlink`, `Lstat` уже был):
- **Гарантию без гонок:** разрешение пути идёт покомпонентно через `openat`-подобные вызовы; подмена каталога/симлинка между проверкой и открытием не уводит за корень. Сейчас `Resolve` — check-then-use: `EvalSymlinks` → затем `os.Open/CreateTemp/Rename` по имени (TOCTOU).
- Симлинки внутри корня следуются, наружу — ошибка; `..` за корень — ошибка.

Чего `os.Root` **не** даст (и что придётся сохранить):
- правила `.git/` и state-dir (`same(…, ".git")`, «из state-dir наружу нельзя и снаружи в него нельзя»), сравнение без учёта регистра и по `SameFile` на macOS/Windows;
- правило `ResolveDoc` «симлинк дока должен вести на `.md`» — `os.Root` не возвращает разрешённое имя, нужен собственный `Readlink`-обход;
- **семантика записи через симлинк:** `Root.Rename(tmp, "CLAUDE.md")` заменит сам симлинк `CLAUDE.md → AGENTS.md` файлом, а сейчас пишется цель. Это поведенческое изменение, на которое опирается документация;
- нет `CreateTemp` — имя временного файла придётся генерировать (`OpenFile(name, O_CREATE|O_EXCL)` с `rand.Text()`);
- защиту от FIFO (`readRegular` делает `Stat` → `Open`; подмена на FIFO между ними блокирует open) — нужен `O_NONBLOCK` + `f.Stat()` после открытия, независимо от `os.Root`;
- поведение с Windows junction/mount point (`ModeIrregular`) — проверить по документации `os.Root` для Windows, прежде чем удалять текущую явную проверку.

Рекомендация: не заменять, а **совместить** — `Resolve` остаётся политикой, а ввод-вывод идёт через один `os.OpenRoot(realRoot)` на процесс с root-relative разрешённым путём:
```go
r, _ := os.OpenRoot(realRoot)          // кэшировать как realRoots
rel, _ := filepath.Rel(realRoot, full) // full из Resolve
f, err := r.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0) // unix; затем f.Stat().Mode().IsRegular()
```
Severity Low: по модели угроз репо статичен (враждебный клон), TOCTOU требует параллельного процесса-атакующего, у которого уже есть исполнение кода. Выигрыш — defence-in-depth, не закрытие подтверждённой атаки.

### BP-L5. `escapes()` → `filepath.IsLocal`
`internal/fsutil/fsutil.go:35-37`. `!filepath.IsLocal(rel)` (Go 1.20) покрывает `..`, абсолютные пути и дополнительно зарезервированные имена Windows (`NUL`, `COM1`) — строже текущего. `IsLocal(".")` == true, как и нужно.

### BP-L6. Версия бинаря только через `-X main.version`
`cmd/auto-context/main.go:27`. При `go install auto-context/cmd/auto-context@v1.x` или `go build` без Makefile печатается `dev`, хотя Go 1.24+ сам штампует версию модуля из VCS-тега (локальная сборка показала `mod auto-context v1.0.0+dirty`).
```go
func versionString() string {
    if version != "dev" { return version }
    if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
        return bi.Main.Version
    }
    return version
}
```

### BP-L7. Мелкие идиомы ошибок
- `cmd/auto-context/main.go:201` `fmt.Errorf("unknown event")` и `project.go:306` без аргументов → `errors.New`.
- `"unknown unit %s"` создаётся в двух пакетах (`docstate.go:144`, `changes.go:59`) — сделать sentinel `units.ErrUnknownUnit` и оборачивать `fmt.Errorf("%w: %s", units.ErrUnknownUnit, u)`, чтобы CLI различал классы ошибок через `errors.Is` (сочетается с A-M3, типизированная модель проблем).
- `staticcheck ST1020`: `internal/session/session.go:249` — doc-комментарий `AddCode` начать с имени.

### BP-L8. Codex: читать `PLUGIN_ROOT`, `CLAUDE_PLUGIN_ROOT` — как запасной
`internal/hooks/agent.go:62-66`, `hooks/codex.json`. Документация Codex (learn.chatgpt.com/docs/hooks): Codex выставляет `PLUGIN_ROOT`/`PLUGIN_DATA`, а `CLAUDE_PLUGIN_ROOT` — «for compatibility». Сейчас работает, но опирается на совместимый алиас.
```go
func pluginRoot(a Agent) string {
    if a == Codex {
        if r := os.Getenv("PLUGIN_ROOT"); r != "" { return r }
    }
    return os.Getenv("CLAUDE_PLUGIN_ROOT")
}
```
В `codex.json` — `"${PLUGIN_ROOT}"/bin/auto-context …` после проверки, что Codex подставляет переменную в `command` (иначе оставить как есть).

### BP-L9. `syscall` и `f.Fd()` в блокировке лога
`internal/logx/lock_flock.go:22`. `f.Fd()` переводит файл в блокирующий режим и отвязывает его от netpoller; идиоматичнее `f.SyscallConn().Control(func(fd uintptr){ err = syscall.Flock(int(fd), …) })`. Пакет `syscall` заморожен; для `Flock`/`Stat_t` это допустимо (зависимость `x/sys` ради двух вызовов не нужна) — Info по выбору пакета, Low по `Fd()`.

### BP-L10. `Input` не сверяет `hook_event_name` с аргументом командной строки
`internal/hooks/hooks.go:14-19`, `main.go:166`. Обработчик выбирается по argv; поле `hook_event_name` (есть в обоих протоколах) игнорируется. Ошибка в `hooks.json` (событие X вызывает `hook y`) сейчас проявится молча неверным ответом. Дешёвая проверка: при несовпадении — запись в лог и пустой ответ.
```go
HookEventName string `json:"hook_event_name"`
...
if in.HookEventName != "" && in.HookEventName != eventNames[event] { return fmt.Errorf("event mismatch: %q", in.HookEventName) }
```

### BP-L11. Makefile
- нет цели `build` (быстрая сборка под хост в `libexec/`) и `lint`; `test` без `-race` (см. T-M5);
- `dist` не очищает `libexec/` (остатки старых таргетов попадут в ручной архив);
- `VERSION ?= dev` — можно `VERSION ?= $(shell git describe --tags --always --dirty)`;
- дублирование с release.yml — см. BP-M3.

### BP-L12. Минимальная версия Claude Code не объявлена
`internal/hooks/stop.go:191-205` полагается на `hookSpecificOutput.additionalContext` в Stop, `hooks/hooks.json` — на поле `"if": "Bash(auto-context *)"`. Оба поддерживаются текущей документацией, но это сравнительно новые возможности; на старых версиях Stop-обратная связь тихо пропадёт (а `if` просто игнорируется — безопасно, фильтр есть и в Go). CI валидирует против `claude-code@2.1.304`. Рекомендация: указать минимальную версию в README (и в манифесте, если формат это поддерживает); при сомнении для Claude можно дублировать `decision:"block"`+`reason` — но это показывается как ошибка хука, поэтому текущий выбор оправдан.

---

## Info

- **I-1. Протокол хуков Claude Code соответствует документации** (code.claude.com/docs/en/hooks, сверено 2026-10-23): PreToolUse — `hookSpecificOutput{hookEventName, permissionDecision:"allow", permissionDecisionReason}` (значения allow/deny/ask/defer; устаревших top-level `decision:"approve"` нет); SessionStart — `systemMessage` (универсальное поле) + `hookSpecificOutput.additionalContext`; Stop — `hookSpecificOutput.additionalContext` (документирован для Stop/SubagentStop как «non-error feedback that continues the conversation»); `if` — валиден на tool-событиях, одна permission-rule; код выхода всегда 0 (exit 2 = блокирующая ошибка, прочие ненулевые — non-blocking) — шим это гарантирует.
- **I-2. Протокол Codex соответствует** (learn.chatgpt.com/docs/hooks): Stop — top-level `decision:"block"`+`reason` (`additionalContext` для Stop в Codex не документирован — поэтому ветка Codex в `stopFeedback` верна); PermissionRequest — `hookSpecificOutput.decision.behavior:"allow"` без `updatedInput` (как требует документация); matcher — regex (`^Bash$` корректен); таймауты в секундах, SessionEnd максимум 3 — `codex.json` ставит 3, бюджет 2 с (`main.go:143`) верен. Stop в Codex требует JSON при exit 0 — пустой вывод допустим.
- **I-3.** Комментарий `hooks.go:21-22` («an array in Codex, say») — по документации Codex `tool_input.command` для Bash строка; толерантный разбор безвреден, комментарий можно уточнить.
- **I-4.** `encoding/json` v1 сопоставляет ключи без учёта регистра и берёт последний дубликат; для входа хуков от агента это приемлемо. При переходе на `encoding/json/v2` (когда выйдет из эксперимента) поведение станет строгим — учесть в тестах `Input`.
- **I-5.** Shadow-предупреждения (`config.go:125,346`, `frontmatter.go:113`, `logx.go:38-142`) — все в форме `if x, err := …; err != nil`, логических ошибок нет; править не нужно.
- **I-6.** Зависимости минимальны и обоснованы: `doublestar/v4 v4.10.3` — последняя, активно поддерживается; лишних зависимостей нет; `go mod tidy` чист; govulncheck чист.
