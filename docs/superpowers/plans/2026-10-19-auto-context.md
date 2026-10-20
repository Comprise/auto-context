# auto-context Implementation Plan

> Исторический документ: план первой реализации. Текущее устройство — в `context/_root.md`.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Плагин Claude Code на Go, который после каждого хода указывает Claude на устаревшие `CLAUDE.md` / `context/<unit>.md` и поддерживает `source_hash` и оглавление корня.

**Architecture:** Один бинарник `auto-context` с подкомандами `hook <event>`, `ack`, `status`, `units`. Хуки `SessionStart` / `UserPromptSubmit` / `Stop` — чистые обработчики `(Env, Input) → []byte` поверх небольших пакетов: config, units (обход + эвристика), hashing, frontmatter, docstate, rootindex, snapshot, state. Плагин — shim в `bin/`, бинарники в `libexec/`, навыки в `skills/`.

**Tech Stack:** Go 1.27, `gopkg.in/yaml.v3`, `github.com/bmatcuk/doublestar/v4`, `github.com/sabhiram/go-gitignore`.

**Spec:** `docs/superpowers/specs/2026-10-19-auto-context-design.md` — читать вместе с планом; номера «п. N» ниже ссылаются на разделы спецификации.

## Global Constraints

- Модуль Go: `auto-context` (go.mod: `module auto-context`, `go 1.27`).
- Зависимости только: stdlib, `gopkg.in/yaml.v3`, `github.com/bmatcuk/doublestar/v4`, `github.com/sabhiram/go-gitignore`.
- Хуки всегда завершаются с кодом 0; ошибки — только в `.auto-context/log` (ротация: при размере > 1 МБ `log` → `log.1`).
- Все записи служебных файлов, frontmatter и корневого `CLAUDE.md` — через `fsutil.WriteAtomic`.
- Пути юнитов и файлов — относительные к корню, разделитель `/`; корневой юнит — строка `_root`.
- Значения по умолчанию конфига — ровно из п. 13 спецификации.
- Тексты сообщений хуков — английские, ровно по шаблонам п. 8.1 и п. 8.3.
- Обход дерева не следует по симлинкам на каталоги.
- Платформы: linux, darwin, windows × amd64, arm64. Любой путь из ОС приводится к `/` через `filepath.ToSlash` до сравнения, хранения и вывода; на диск — через `filepath.FromSlash`. Тесты проходят на всех трёх ОС (CI, Task 12).
- Версия бинарника: `var version = "dev"` в `cmd/auto-context`, задаётся `-ldflags "-X main.version=<tag>"`.
- Тесты — `go test ./...`, файловая система — `t.TempDir()`.

## Review Focus

- **Два Claude-сеанса в одном репозитории одновременно** — журналы и снимки раздельны по `session_id`; общий `cache.json` пишется атомарно, побеждает последний. Тест в Task 7 (`TestJournalsIsolatedBySession`).
- **Корневой `CLAUDE.md` с только одним маркером** (пользователь удалил закрывающий) — файл не изменять, вернуть ошибку (в хуке — в лог). Тест в Task 6 (`TestUpdateRefusesBrokenMarkers`).
- **Каталог юнита удалён за ход** — изменения его файлов (удаления) относятся к ближайшему существующему предку-юниту по «старому» дереву; `Stop` не падает и сообщает о родителе. Тест в Task 9 (`TestStopUnitDirDeleted`).
- **Симлинк-цикл в репозитории** — обход завершается, симлинк на каталог пропускается. Тест в Task 3 (`TestDiscoverSkipsSymlinkDirs`).
- **`Stop` без `UserPromptSubmit`** (плагин включён посреди сессии или ход прерван) — снимок создаётся, остановка разрешена. Тест в Task 9 (`TestStopWithoutSnapshot`).

---

### Task 1: Каркас модуля и config

**Files:**
- Create: `go.mod`, `Makefile`, `.gitignore`, `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Config struct { MinFiles int \`yaml:"min_files"\`; CodeExtensions []string \`yaml:"code_extensions"\`; ModuleMarkers []string \`yaml:"module_markers"\`; Include []string \`yaml:"include"\`; Exclude []string \`yaml:"exclude"\`; MaxSessionNotice int \`yaml:"max_session_notice"\` }`
  - `func Default() Config`
  - `func Load(root string) (Config, error)` — нет файла → `Default(), nil`; битый YAML → `Default(), err`; отсутствующие поля заполняются из `Default()`.
  - `func AddExclude(root, pattern string) error` — дописывает шаблон в `exclude` (без дублей), создаёт `.auto-context.yaml` при отсутствии.
  - `func FindRoot(cwd string) string` — вверх до каталога с `.git` или `.auto-context.yaml`; не найден → `cwd`.
  - `const FileName = ".auto-context.yaml"`

- [ ] **Step 1:** `go mod init auto-context`; `.gitignore` с `/dist/`; `Makefile` с целями `test` (`go test ./...`) и `dist` (кросс-сборка `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o libexec/auto-context-$(GOOS)-$(GOARCH)$(EXT) ./cmd/auto-context` для linux/darwin/windows × amd64/arm64, `EXT=.exe` для windows; `VERSION ?= dev`).
- [ ] **Step 2: Write failing tests** в `config_test.go`:
  - `TestLoadMissingReturnsDefault`: `Load(t.TempDir())` == `Default()`, err nil; `Default().MinFiles == 5`, `MaxSessionNotice == 10`, `Exclude` == `[vendor/** node_modules/** **/testdata/** dist/** build/**]`.
  - `TestLoadPartialFillsDefaults`: файл `min_files: 3` → `MinFiles==3`, `CodeExtensions` == default.
  - `TestLoadBrokenYAML`: `"min_files: ["` → err != nil и результат == `Default()`.
  - `TestAddExcludeCreatesAndDedups`: два вызова `AddExclude(root,"internal/billing")` → в `Load(root).Exclude` ровно одно вхождение, default-элементы сохранены.
  - `TestFindRoot`: `root/.git` + cwd `root/a/b` → `root`; без маркеров → cwd.
- [ ] **Step 3:** Run `go test ./internal/config/` — FAIL (не определено).
- [ ] **Step 4:** Реализовать (`yaml.v3`; `AddExclude` пишет полный конфиг через `os.WriteFile` — `fsutil` появится в Task 2, после него заменить на `WriteAtomic`).
- [ ] **Step 5:** Run `go test ./internal/config/` — PASS.
- [ ] **Step 6:** Commit `feat(config): defaults, load, add-exclude, find-root`.

### Task 2: fsutil и logx

**Files:**
- Create: `internal/fsutil/fsutil.go`, `internal/fsutil/fsutil_test.go`, `internal/logx/logx.go`, `internal/logx/logx_test.go`
- Modify: `internal/config/config.go` (AddExclude → WriteAtomic)

**Interfaces:**
- Produces:
  - `func fsutil.WriteAtomic(path string, data []byte, perm os.FileMode) error` — создаёт родительские каталоги, пишет во временный файл в том же каталоге, `rename`.
  - `func fsutil.EnsureStateDir(root string) error` — создаёт `<root>/.auto-context/state`; если `<root>/.gitignore` существует и в нём нет строки `.auto-context/` — дописывает её (с переводом строки перед, если файл не заканчивается на `\n`).
  - `const fsutil.StateDir = ".auto-context"`
  - `func logx.Errorf(root, format string, args ...any)` — дописывает `RFC3339 message\n` в `<root>/.auto-context/log`; если файл > 1<<20 байт — сначала `rename` в `log.1`. Собственные ошибки игнорирует.

- [ ] **Step 1: Write failing tests:**
  - `TestWriteAtomicCreatesDirs`: путь `a/b/c.txt` в TempDir → содержимое совпадает, временных файлов в каталоге не осталось.
  - `TestEnsureStateDirAppendsGitignore`: `.gitignore` = `"bin"` → после вызова `"bin\n.auto-context/\n"`; повторный вызов не дублирует.
  - `TestEnsureStateDirNoGitignore`: `.gitignore` не создаётся.
  - `TestLogRotates`: записать `log` размером 1<<20+1, вызвать `Errorf` → существует `log.1`, `log` содержит одну строку.
- [ ] **Step 2:** Run `go test ./internal/fsutil/ ./internal/logx/` — FAIL.
- [ ] **Step 3:** Реализовать; перевести `config.AddExclude` на `WriteAtomic`.
- [ ] **Step 4:** Run `go test ./...` — PASS.
- [ ] **Step 5:** Commit `feat: atomic writes, state dir, error log`.

### Task 3: units — обход, эвристика, классификация

**Files:**
- Create: `internal/units/units.go`, `internal/units/walk.go`, `internal/units/units_test.go`

**Interfaces:**
- Consumes: `config.Config`.
- Produces:
  - `type Unit struct { Path string; Reason string; FileCount int }` — `Reason` ∈ `"root"`, `"module"`, `"files"`, `"include"`.
  - `type Kind int; const ( KindIgnore Kind = iota; KindCode; KindDocs )`
  - `type Tree struct { … }` — неэкспортируемые поля.
  - `func Discover(root string, cfg config.Config) (*Tree, error)` — один обход дерева.
  - `func (t *Tree) Units() []Unit` — отсортированы по `Path`, `_root` первым.
  - `func (t *Tree) IsUnit(path string) bool`
  - `func (t *Tree) UnitOf(rel string) string` — ближайший предок-юнит каталога файла (включая сам каталог), иначе `_root`. Работает и для несуществующих путей (чисто по строке).
  - `func (t *Tree) Classify(rel string) (Kind, string)` — правила п. 6: `<unit>/CLAUDE.md` или `CLAUDE.md` в корне → `(KindDocs, unit)`; `context/<unit>.md` / `context/_root.md` при `IsUnit` → `(KindDocs, unit)`; прочее в `context/`, `.auto-context/`, прочие `.md` → `KindIgnore`; расширение из `CodeExtensions` и не под exclude → `(KindCode, UnitOf(rel))`; иначе `KindIgnore`.
  - `func (t *Tree) Files(unit string) []string` — кодовые файлы юнита без вложенных юнитов, отсортированы.
  - `func (t *Tree) Tracked() []string` — все отслеживаемые файлы (п. 6: кодовые + все `CLAUDE.md` + всё в `context/`), отсортированы.
  - `func DocPaths(unit string) (claudeMD, contextMD string)` — `_root` → `("CLAUDE.md","context/_root.md")`; иначе `(unit+"/CLAUDE.md", "context/"+unit+".md")`.

Правила обхода (п. 6, семантика `exclude`): пропускать `.git`, `context`, `.auto-context` как кандидатов в юниты; пропускать каталог целиком, если шаблон `X/**` и `doublestar.Match(X, dir)`; файл не отслеживается, если совпал любой шаблон `exclude` или корневой `.gitignore`; каталог не юнит, если его путь совпал с любым шаблоном; симлинки на каталоги не обходить.

- [ ] **Step 1: Write failing tests** (фикстуры строятся хелпером `mkfiles(t, root, paths...)`):
  - `TestDiscoverModuleMarker`: `svc/go.mod`, `svc/main.go` → юниты `_root`, `svc` (Reason `module`).
  - `TestDiscoverMinFiles`: 5 файлов `.go` в `pkg/a` → юнит; 4 в `pkg/b` → не юнит.
  - `TestDiscoverInclude`: `Include: ["tools/x"]`, 1 файл → юнит с Reason `include`.
  - `TestExcludeDirNotUnitButTracked`: `Exclude: ["pkg/a"]` при 5 файлах → `pkg/a` не юнит, `UnitOf("pkg/a/f.go") == "_root"`, файл в `Tracked()`.
  - `TestExcludeGlobSkipsTree`: `vendor/x/*.go` (6 файлов) → не юнит, не в `Tracked()`.
  - `TestGitignoreRespected`: `.gitignore` = `gen/` → `gen/*.go` не в `Tracked()`.
  - `TestUnitOfNested`: юниты `svc` и `svc/auth` → `UnitOf("svc/auth/jwt/x.go")=="svc/auth"`, `UnitOf("svc/y.go")=="svc"`, `UnitOf("README")=="_root"`.
  - `TestClassify`: таблица — `svc/main.go`→Code/svc; `svc/CLAUDE.md`→Docs/svc; `context/svc.md`→Docs/svc; `context/notes.md`→Ignore; `CLAUDE.md`→Docs/_root; `docs/x.md`→Ignore; `svc/data.json`→Ignore.
  - `TestFilesExcludesNestedUnits`: `Files("svc")` не содержит файлов `svc/auth/…`.
  - `TestDiscoverSkipsSymlinkDirs`: `a/loop -> ..` симлинк → `Discover` завершается без ошибки.
- [ ] **Step 2:** Run `go test ./internal/units/` — FAIL.
- [ ] **Step 3:** Реализовать (`filepath.WalkDir`, `doublestar.Match`, `ignore.CompileIgnoreFile`).
- [ ] **Step 4:** Run `go test ./internal/units/` — PASS.
- [ ] **Step 5:** Commit `feat(units): discovery heuristics and path classification`.

### Task 4: hashing

**Files:**
- Create: `internal/hashing/hashing.go`, `internal/hashing/hashing_test.go`

**Interfaces:**
- Produces:
  - `type Entry struct { MtimeNs int64 \`json:"mtime_ns"\`; Size int64 \`json:"size"\`; Sum string \`json:"sum"\` }`
  - `type Cache struct { Entries map[string]Entry \`json:"entries"\` }`
  - `func LoadCache(root string) *Cache` — нет/битый файл → пустой кэш.
  - `func (c *Cache) Save(root string) error` — `.auto-context/cache.json` через `WriteAtomic`.
  - `func SourceHash(root string, files []string, c *Cache) (string, error)` — формула п. 7: `"sha256:" + hex(sha256(Σ relpath + "\x00" + hex(sha256(content)) + "\n"))` по отсортированным `files`; содержимое читается потоково и только при несовпадении mtime/size с кэшем; кэш обновляется.

- [ ] **Step 1: Write failing tests:**
  - `TestSourceHashStable`: одинаковые файлы в двух TempDir → одинаковый хэш с префиксом `sha256:`; порядок `files` на входе не влияет.
  - `TestSourceHashChangesOnContent`, `TestSourceHashChangesOnRename` (тот же контент, другой путь).
  - `TestCacheAvoidsRead`: заполнить кэш, затем подменить `Entry.Sum` на `"x"` при совпадающих mtime/size → хэш вычисляется из `"x"` (доказывает, что файл не перечитан).
  - `TestCacheRoundTrip`: `Save` → `LoadCache` равны.
- [ ] **Step 2:** Run `go test ./internal/hashing/` — FAIL.
- [ ] **Step 3:** Реализовать.
- [ ] **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat(hashing): source hash with mtime cache`.

### Task 5: frontmatter

**Files:**
- Create: `internal/frontmatter/frontmatter.go`, `internal/frontmatter/frontmatter_test.go`

**Interfaces:**
- Produces:
  - `type Meta struct { Unit string \`yaml:"unit"\`; SourceHash string \`yaml:"source_hash"\`; Updated string \`yaml:"updated"\` }`
  - `var ErrNoFrontmatter = errors.New("no frontmatter")`
  - `func Read(path string) (Meta, string, error)` — возвращает meta и тело (после закрывающего `---\n`); нет блока → `ErrNoFrontmatter` и всё содержимое как тело; битый YAML → ошибка.
  - `func SetHash(path, unit, hash string, today time.Time) error` — записывает `unit`, `source_hash`, `updated` (`2006-01-02`); тело байт-в-байт сохраняется; блок добавляется, если отсутствовал; файл отсутствует → `os.ErrNotExist`.
  - `func Summary(body string) string` — первая непустая строка после первой строки, начинающейся с `# `; иначе `""`.

- [ ] **Step 1: Write failing tests:** `TestReadRoundTrip`, `TestReadNoFrontmatter`, `TestSetHashPreservesBody` (тело с `---` внутри текста не повреждается), `TestSetHashAddsBlock`, `TestSetHashMissingFile` (`errors.Is(err, os.ErrNotExist)`), `TestSummary` (таблица: обычный случай; пустые строки между заголовком и текстом; нет заголовка → `""`).
- [ ] **Step 2:** Run `go test ./internal/frontmatter/` — FAIL.
- [ ] **Step 3:** Реализовать (запись через `WriteAtomic`).
- [ ] **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat(frontmatter): read/write meta preserving body`.

### Task 6: docstate и rootindex

**Files:**
- Create: `internal/docstate/docstate.go`, `internal/docstate/docstate_test.go`, `internal/rootindex/rootindex.go`, `internal/rootindex/rootindex_test.go`

**Interfaces:**
- Consumes: `units.Tree`, `units.DocPaths`, `hashing.SourceHash`, `hashing.Cache`, `frontmatter.Read/SetHash/Summary`.
- Produces:
  - `type State string; const ( OK State = "ok"; Stale State = "stale"; Missing State = "missing" )`
  - `type UnitStatus struct { Unit string; State State; ContextPath string; Summary string; Note string }` — `Note` = `"broken frontmatter"` при битом блоке.
  - `func docstate.Compute(root string, t *units.Tree, c *hashing.Cache) ([]UnitStatus, error)` — в порядке `t.Units()`.
  - `func docstate.Ack(root string, t *units.Tree, c *hashing.Cache, unit string, now time.Time) error` — `!t.IsUnit(unit)` → ошибка `unknown unit`; нет `context/<unit>.md` → ошибка с текстом `no context/<unit>.md: create docs or run auto-context ack --exclude <unit>`; иначе `SetHash` с текущим хэшем.
  - `type rootindex.Entry struct { Unit, Summary string }`
  - `func rootindex.Render(entries []Entry) string` — секция п. 10.3 с маркерами, `_root` не включается, строка `` - `<unit>` — <summary> `` (без ` — ` при пустом summary).
  - `var rootindex.ErrBrokenMarkers`
  - `func rootindex.Update(root string, entries []Entry) (bool, error)` — заменяет секцию между маркерами; нет маркеров → дописывает в конец (создаёт файл); ровно один маркер → `ErrBrokenMarkers`, файл не меняется; возвращает `changed`.

- [ ] **Step 1: Write failing tests:**
  - `TestComputeStates`: три юнита — с актуальным хэшем (после `Ack`) → ok; после правки кода → stale; без `context/*.md` → missing; с битым YAML-блоком → stale + Note.
  - `TestAckMissingDoc`: текст ошибки содержит `ack --exclude svc`.
  - `TestRenderSkipsRoot`, `TestUpdateAppendsWhenNoMarkers`, `TestUpdateReplacesSection` (текст вне маркеров не изменён), `TestUpdateNoChange` (`changed == false` при той же секции), `TestUpdateRefusesBrokenMarkers`.
- [ ] **Step 2:** Run `go test ./internal/docstate/ ./internal/rootindex/` — FAIL.
- [ ] **Step 3:** Реализовать.
- [ ] **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat: doc state computation, ack, root index`.

### Task 7: snapshot и state

**Files:**
- Create: `internal/snapshot/snapshot.go`, `internal/snapshot/snapshot_test.go`, `internal/state/state.go`, `internal/state/state_test.go`

**Interfaces:**
- Produces:
  - `type snapshot.FileStat struct { MtimeNs int64 \`json:"m"\`; Size int64 \`json:"s"\` }`
  - `type snapshot.Snapshot map[string]FileStat`
  - `func snapshot.Take(root string, tracked []string) (Snapshot, error)` — только `os.Lstat`; исчезнувший между обходом и stat файл пропускается.
  - `func snapshot.Diff(before, after Snapshot) []string` — изменённые (mtime или size), удалённые, добавленные; отсортированы, без дублей.
  - `func snapshot.Path(root, sessionID string) string` — `.auto-context/state/<session_id>.snapshot`
  - `func snapshot.Load(path string) (Snapshot, error)` — нет файла → `nil, os.ErrNotExist`; `func (s Snapshot) Save(path string) error`.
  - `type state.Journal struct { CodeChanged []string \`json:"code_changed"\`; DocsTouched []string \`json:"docs_touched"\`; AlreadyNotified []string \`json:"already_notified"\` }`
  - `func state.Load(root, sessionID string) Journal` — нет/битый → пустой.
  - `func (j Journal) Save(root, sessionID string) error`; `func state.Clear(root, sessionID string) error` — удаляет журнал и снимок.
  - `func state.AddUnique(list []string, items ...string) []string` — отсортированное множество; `func state.Minus(a []string, bs ...[]string) []string`.
  - `session_id` санитизируется: символы вне `[A-Za-z0-9_-]` → `_`.

- [ ] **Step 1: Write failing tests:**
  - `TestDiffDetectsAll`: изменение контента с той же длиной (выставить mtime через `os.Chtimes` +1с), изменение только размера, удаление, rename (= удалён + добавлен), новый файл → все пути в результате; неизменный файл отсутствует.
  - `TestSnapshotRoundTrip`.
  - `TestJournalRoundTrip`, `TestJournalBrokenIsEmpty`.
  - `TestJournalsIsolatedBySession`: журналы `s1` и `s2` не влияют друг на друга; `Clear("s1")` не трогает `s2`.
  - `TestSessionIDSanitized`: `"../x"` → файл внутри `.auto-context/state/`.
  - `TestMinusAndAddUnique`.
- [ ] **Step 2:** Run `go test ./internal/snapshot/ ./internal/state/` — FAIL.
- [ ] **Step 3:** Реализовать (JSON, `WriteAtomic`).
- [ ] **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat: stat snapshots and per-session journal`.

### Task 8: hooks — SessionStart и PromptSubmit

**Files:**
- Create: `internal/hooks/hooks.go`, `internal/hooks/session_start.go`, `internal/hooks/prompt_submit.go`, `internal/hooks/hooks_test.go`, `internal/hooks/testdata/` (golden)

**Interfaces:**
- Consumes: всё выше.
- Produces:
  - `type Input struct { SessionID string \`json:"session_id"\`; Cwd string \`json:"cwd"\`; HookEventName string \`json:"hook_event_name"\`; Source string \`json:"source"\`; StopHookActive bool \`json:"stop_hook_active"\` }`
  - `type Env struct { Root string; Now func() time.Time }`
  - `func SessionStart(env Env, in Input) ([]byte, error)` — п. 8.1; `nil` = пустой вывод.
  - `func PromptSubmit(env Env, in Input) ([]byte, error)` — п. 8.2; всегда `nil` вывод.
  - `func SessionNotice(stale, missing []string, max int) string` — экспортирована для тестов.
  - внутренний хелпер `load(env) (config.Config, *units.Tree, *hashing.Cache, error)` — общий для всех хуков; вызывает `fsutil.EnsureStateDir`.

`SessionNotice`: берёт сначала stale, потом missing, всего не больше `max`; формат
`auto-context: docs stale for A, B; missing for C. Update them when working there (skill auto-context-docs) or run /auto-context:status.`
— части `docs stale for …` / `missing for …` опускаются, если пусты (при одной части без `; `); при отбрасывании K юнитов перед `. Update` вставляется ` (+K more)`.
Вывод SessionStart: `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":<notice>}}`.

- [ ] **Step 1: Write failing tests:**
  - `TestSessionNotice`: таблица — только stale; только missing; оба; переполнение при `max=2` с `(+1 more)`; ровно строка из спецификации для `[src/auth cmd/api]`, `[internal/billing]`.
  - `TestSessionStartQuietWhenOK`: все юниты ok → `nil`.
  - `TestSessionStartGolden`: фикстура со stale и missing → stdout совпадает с `testdata/session_start.golden.json`.
  - `TestSessionStartUpdatesRootIndex`: после вызова корневой `CLAUDE.md` содержит секцию с summary из `context/svc.md`.
  - `TestPromptSubmitWritesSnapshotAndClearsJournal`: непустой журнал до вызова → после пустой; файл снимка существует и содержит `svc/main.go`.
- [ ] **Step 2:** Run `go test ./internal/hooks/` — FAIL.
- [ ] **Step 3:** Реализовать.
- [ ] **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat(hooks): session-start notice and prompt-submit snapshot`.

### Task 9: hooks — Stop

**Files:**
- Create: `internal/hooks/stop.go`, `internal/hooks/stop_test.go`

**Interfaces:**
- Consumes: Task 8 (`Input`, `Env`, `load`), `snapshot`, `state`, `docstate.Ack`, `rootindex.Update`.
- Produces:
  - `func Stop(env Env, in Input) ([]byte, error)` — п. 8.3, шаги 1–6.
  - `func StopReason(changed, newUnits []string) string` — экспортирована для тестов.

Детали, которые фиксирует план:
- Классификация удалённых путей идёт по дереву, построенному **сейчас**; `UnitOf` работает по строке, поэтому удалённый каталог-юнит даёт ближайшего существующего предка.
- Auto-ack (шаг 3) — `docstate.Ack` для каждого юнита из `DocsTouched`, у которого есть `context/<unit>.md`; ошибки — в лог, не прерывают.
- `newUnits` — юниты из `pending` без `context/<unit>.md`; `changed = pending − newUnits`.
- `StopReason`: если `changed` не пуст — `auto-context: code changed in <changed, ", "> but docs not updated. If the change affects purpose, public API, structure or key behavior — update <unit>/CLAUDE.md and context/<unit>.md per skill auto-context-docs. Otherwise run \`auto-context ack <changed, " ">\`.`; если `newUnits` не пуст — дописать (через пробел) `New unit <u>: create its docs or run \`auto-context ack --exclude <u>\`.` для каждого. Если `changed` пуст — первая фраза начинается с `auto-context:` перед первым `New unit`.
- Вывод: `{"decision":"block","reason":<StopReason>}`.

- [ ] **Step 1: Write failing tests** (хелпер `turn(t)`: фикстура + `PromptSubmit`):
  - `TestStopHookActiveAllows`: `StopHookActive: true` → `nil`, журнал не изменён.
  - `TestStopWithoutSnapshot`: → `nil`, снимок создан.
  - `TestStopNoChangesAllows`: → `nil`, журнал и снимок удалены.
  - `TestStopBlocksOnCodeEdit`: правка `svc/main.go` → JSON с `"decision":"block"` и `svc` в reason; `AlreadyNotified == [svc]`.
  - `TestStopSecondCallAllowsAfterNotify`: повторный `Stop` без новых правок → `nil`.
  - `TestStopAllowsAfterDocsEdited`: правка кода → block → правка `context/svc.md` → `Stop` → `nil`, а `docstate.Compute` даёт `svc` = ok.
  - `TestStopBashRename`: `os.Rename("svc/a.go","svc/b.go")` после `PromptSubmit` → block с `svc`.
  - `TestStopAckFlow`: block → `docstate.Ack(svc)` → `Stop` → `nil`.
  - `TestStopNewUnitAndExclude`: создать 5 файлов в `pkg/new` → block с `ack --exclude pkg/new` → `config.AddExclude(root,"pkg/new")` → `Stop` → reason упоминает `_root` (файлы теперь в корневом юните) либо `nil`, если `_root` уже в `AlreadyNotified`; проверить, что `pkg/new` в reason отсутствует.
  - `TestStopUnitDirDeleted`: юниты `svc`, `svc/auth`; удалить каталог `svc/auth` → block с `svc`, без паники.
  - `TestStopReasonText`: точная строка для `changed=[src/auth cmd/api]`, `newUnits=[internal/billing]`.
- [ ] **Step 2:** Run `go test ./internal/hooks/ -run Stop` — FAIL.
- [ ] **Step 3:** Реализовать.
- [ ] **Step 4:** Run `go test ./...` — PASS.
- [ ] **Step 5:** Commit `feat(hooks): stop hook with snapshot diff and auto-ack`.

### Task 10: CLI

**Files:**
- Create: `cmd/auto-context/main.go`, `cmd/auto-context/main_test.go`

**Interfaces:**
- Consumes: `hooks.*`, `docstate.Compute/Ack`, `config.AddExclude/FindRoot`, `units.Discover`, `logx.Errorf`.
- Produces: `func run(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) int`; `main` вызывает `os.Exit(run(...))`.

Поведение:
- `hook <session-start|prompt-submit|stop>`: читает Input из stdin; корень = `FindRoot(in.Cwd)` (пустой → `cwd`); при **любой** ошибке или панике (`recover`) — `logx.Errorf`, пустой stdout, код 0.
- `ack <unit…>`: корень от `cwd`; для каждого `docstate.Ack`; ошибки → stderr, код 1, остальные юниты всё равно обрабатываются.
- `ack --exclude <unit>`: `config.AddExclude(root, unit)`.
- `status [--stale]`: таблица `UNIT  STATE  DOC` через `text/tabwriter`, `Note` в скобках после state; `--stale` — только stale/missing; пустая выборка → `all docs up to date`.
- `units`: строки `<path>\t<reason>\t<file_count>`.
- `version`: печатает `version` и `\n`.
- Неизвестная команда → usage в stderr, код 2.

- [ ] **Step 1: Write failing tests:**
  - `TestHookPanicIsSilent`: stdin = `"not json"` → код 0, stdout пуст, в `.auto-context/log` одна строка.
  - `TestHookStopEndToEnd`: через `run` — `prompt-submit`, правка файла, `stop` → stdout содержит `"decision":"block"`.
  - `TestAckUnknownUnitExit1`, `TestAckExcludeWritesConfig`.
  - `TestStatusStaleFilter`: в выводе есть `missing`, нет строк со `ok`.
  - `TestUnitsOutput`: строка `svc\tmodule\t1`.
  - `TestVersion`: вывод `dev\n`, код 0.
- [ ] **Step 2:** Run `go test ./cmd/...` — FAIL.
- [ ] **Step 3:** Реализовать (без сторонних CLI-библиотек).
- [ ] **Step 4:** Run `go test ./...` — PASS.
- [ ] **Step 5:** Commit `feat(cli): hook, ack, status, units commands`.

### Task 11: Упаковка плагина, навыки, ручная проверка

**Files:**
- Create: `.claude-plugin/plugin.json`, `hooks/hooks.json`, `bin/auto-context`, `skills/auto-context-docs/SKILL.md`, `skills/init/SKILL.md`, `skills/sync/SKILL.md`, `skills/status/SKILL.md`, `README.md`
- Modify: `Makefile` (цель `dist` → `libexec/`), `.gitignore`

**Interfaces:**
- Consumes: бинарник из Task 10.

- [ ] **Step 1:** `plugin.json`: `{"name":"auto-context","version":"0.1.0","description":"Keeps per-directory CLAUDE.md indexes and a context/ knowledge library in sync with code","author":{"name":"Elia Samoylov"},"license":"MIT"}`. `hooks/hooks.json` — ровно п. 5 спецификации.
- [ ] **Step 2:** `bin/auto-context` (POSIX sh, `chmod +x`): `os=$(uname -s | tr A-Z a-z)`; маппинг `mingw*|msys*|cygwin*→windows` с `ext=.exe`, иначе `ext=`; `arch=$(uname -m)` с маппингом `x86_64|amd64→amd64`, `aarch64|arm64→arm64`; `exec "$(dirname "$0")/../libexec/auto-context-$os-$arch$ext" "$@"`; если бинарника нет — для `hook *` молча `exit 0`, иначе сообщение в stderr и `exit 1`. Проверка: `sh -n bin/auto-context`.
- [ ] **Step 3:** `skills/auto-context-docs/SKILL.md` — по п. 11 (frontmatter `name: auto-context-docs`, `description`, срабатывающий на «update/create CLAUDE.md or context docs for a unit»), 40–60 строк, оба шаблона п. 10.1/10.2.
- [ ] **Step 4:** `skills/init|sync|status/SKILL.md` — по п. 12, frontmatter `disable-model-invocation: true`; шаги ссылаются на команды `auto-context units`, `auto-context status --stale`, `auto-context ack [--exclude]` без пути (каталог `bin/` в PATH).
- [ ] **Step 5:** Run `make test && make dist && claude plugin validate .` — Expected: тесты PASS, шесть файлов в `libexec/`, `Validation passed`.
- [ ] **Step 6: Ручная проверка** на тестовом репозитории (`/tmp`-копия любого Go-проекта с ≥2 пакетами): `claude --plugin-dir <path-to-plugin>`;
  1. `/auto-context:init` → показан список юнитов, после подтверждения созданы `CLAUDE.md` и `context/*.md`, в корневом `CLAUDE.md` секция `auto-context:units`;
  2. попросить изменить сигнатуру публичной функции → в конце хода Claude получает block и обновляет документы; повторного block нет;
  3. попросить переименовать файл через `git mv` в Bash → block;
  4. выйти, вручную изменить файл, запустить снова → в контексте сообщение `docs stale for …`;
  5. `/auto-context:sync` → stale-юнит обновлён или ack.
  Записать результат каждого пункта в `README.md` (раздел «Проверено»).
- [ ] **Step 7:** Добавить `/libexec/` в `.gitignore` (бинарники попадают только в ветку `release`, Task 12). Commit `feat: plugin packaging and skills`.

### Task 12: GitHub Actions — CI и релизы

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.claude-plugin/marketplace.json`
- Modify: `README.md` (разделы «Установка», «Релиз»)

**Interfaces:**
- Consumes: цель `make dist` и `VERSION` из Task 1/11; shim из Task 11.

`ci.yml` — триггеры `push` и `pull_request`; job `test` с `strategy.matrix.os: [ubuntu-latest, macos-latest, windows-latest]`; шаги: `actions/checkout@v4`, `actions/setup-go@v5` (`go-version-file: go.mod`), `go vet ./...`, `go test ./...`, `sh -n bin/auto-context` (на windows — `shell: bash`).

`release.yml` — триггер `push.tags: ['v*']`, `permissions: contents: write`:
1. job `test` — как в `ci.yml` (тот же matrix; переиспользовать через `workflow_call` из `ci.yml`).
2. job `build` (`needs: test`, `ubuntu-latest`) — matrix `goos: [linux, darwin, windows]` × `goarch: [amd64, arm64]`; `make dist`-эквивалент для одной пары: `CGO_ENABLED=0 GOOS=… GOARCH=… go build -trimpath -ldflags "-s -w -X main.version=${GITHUB_REF_NAME}" -o libexec/auto-context-$GOOS-$GOARCH$EXT ./cmd/auto-context`; `actions/upload-artifact@v4` с именем `bin-$GOOS-$GOARCH`.
3. job `publish` (`needs: build`) — `actions/download-artifact@v4` (`merge-multiple: true`, в `libexec/`); `chmod +x libexec/* bin/auto-context`; `sha256sum libexec/* > checksums.txt`; архив `auto-context-${GITHUB_REF_NAME}.tar.gz` из `.claude-plugin hooks skills bin libexec README.md`; `gh release create "$GITHUB_REF_NAME" auto-context-*.tar.gz checksums.txt --generate-notes`.
4. В том же job — обновить ветку `release`: `git checkout -B release`, `git add -f libexec/`, commit `release ${GITHUB_REF_NAME}`, `git push -f origin release` (от `github-actions[bot]`).

`marketplace.json` — один плагин `auto-context` с `source` на этот репозиторий, ref `release`.

- [ ] **Step 1:** Написать `ci.yml`. Проверка: `actionlint .github/workflows/ci.yml` (если установлен; иначе `go run github.com/rhysd/actionlint/cmd/actionlint@latest`) — без ошибок.
- [ ] **Step 2:** Написать `release.yml` и `marketplace.json`. Проверка: `actionlint` без ошибок; `claude plugin validate .` — `Validation passed`.
- [ ] **Step 3:** Локальная имитация шага build: `make dist VERSION=v0.0.0-test` → в `libexec/` шесть файлов, `file libexec/*` показывает ELF/Mach-O/PE для соответствующих ОС; `./libexec/auto-context-linux-amd64 version` → `v0.0.0-test`.
- [ ] **Step 4:** README: установка (`/plugin marketplace add <owner>/auto-context`, `/plugin install auto-context@auto-context`), выпуск релиза (`git tag v0.1.0 && git push --tags`).
- [ ] **Step 5:** Commit `ci: test matrix and tagged multi-platform releases`.
- [ ] **Step 6 (после публикации репозитория на GitHub):** запушить тег `v0.1.0-rc1` → workflow зелёный, в Release шесть бинарников в архиве и `checksums.txt`, ветка `release` содержит `libexec/`.
