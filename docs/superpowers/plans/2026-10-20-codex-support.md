# Поддержка Codex CLI — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** плагин auto-context работает в Codex CLI с `<unit>/AGENTS.md`, не меняя поведения в Claude Code.

**Architecture:** имя индексного файла — новый ключ конфига `index_file`, который протаскивается в
`units`, `rootindex`, тексты хуков и CLI. Агент — флаг `--agent codex` после события в команде хука
(`hooks.Env.Agent`); он выбирает синтаксис ссылок на навыки и добавляет обработчик
`permission-request`. Упаковка Codex — `.codex-plugin/plugin.json` + `hooks/codex.json`.

**Tech Stack:** Go (модуль `auto-context`), `gopkg.in/yaml.v3`, POSIX sh-шим, GitHub Actions, Codex CLI 0.163.

**Spec:** `docs/superpowers/specs/2026-10-20-codex-support-design.md`

## Global Constraints

- Допустимые `index_file`: ровно `CLAUDE.md` (по умолчанию) и `AGENTS.md`.
- Без `--agent` агент — `claude`; вывод для Claude Code и `hooks/hooks.json` не меняются. В существующих тестах правятся только вызовы изменённых сигнатур (`LanguageNote`, `SessionNotice`, `rootindex.Check/Update`), не ожидаемые значения.
- Флаг идёт после события: `auto-context hook <event> --agent codex` (шим читает событие из `$2`).
- Хуки всегда выходят с 0; ошибки — в `.auto-context/log` через `logHook`.
- Таймауты `hooks/codex.json`: session-start 60, prompt-submit 30, stop 60, permission-request 10, session-end 3 (с).
- Бюджет git меньше таймаута: `budgetFor(event, agent)`; permission-request 8 с, session-end для codex 2 с, остальное как в `hookBudget`.
- Тексты хуков, перечисленные в навыках дословно, меняются вместе с навыками (`skills/README.md`).
- Доки юнитов — на русском (`language: ru`); после правок кода обновить `context/<unit>.md` по навыку `auto-context:auto-context-docs` или `auto-context ack`.

## Review Focus

1. `index_file: AGENTS.md` при оставшихся в репо `CLAUDE.md` — они не доки и не отслеживаются (тест в Task 2).
2. Опечатка `index_file: agents.md` — конфиг сломан, трекинг на паузе, корневой `CLAUDE.md` не создаётся (тест в Task 1 и Task 3).
3. Корневой `AGENTS.md` — симлинк на `CLAUDE.md` внутри репо: оглавление пишется через симлинк, как сейчас (тест в Task 2).
4. `--agent` без значения, с неизвестным значением или лишние аргументы — запись в лог, выход 0, вывода нет (тест в Task 4).
5. `PermissionRequest` для `auto-context ack --exclude x`, команд с `..`, затенённого PATH или `tool_input.command` не строкой — пустой вывод, в логе нет ошибки (тест в Task 4).

---

### Task 0: Проверка Codex вживую (одноразово)

Результат — раздел «Поправки по шагу 0» в конце спецификации; код проверки не коммитится.

**Files:**
- Modify: `docs/superpowers/specs/2026-10-20-codex-support-design.md` (раздел в конце)

- [ ] **Step 1: Собрать пробный плагин** во временном каталоге вне репо (`mktemp -d`): `.codex-plugin/plugin.json` (`name: "ac-probe"`, `hooks: "./hooks/codex.json"`), `hooks/codex.json` с `SessionStart`, `PermissionRequest` (`matcher: "^Bash$"`), `Stop`, `SessionEnd`, каждый — `sh -c 'cat > "$PLUGIN_DATA/$0.json"; env > "$PLUGIN_DATA/$0.env"' <event>`; `Stop` отвечает один раз (если нет файла-флага) `{"decision":"block","reason":"probe: say PROBE"}`; `hooks/hooks.json` с `PreToolUse`-хуком, пишущим `$PLUGIN_DATA/claude-hooks-ran` (не должен сработать); `bin/ac-probe` (печатает `probe ok`); `skills/probe/SKILL.md`; `.agents/plugins/marketplace.json` с источником `local` `./`.
- [ ] **Step 2: Установить и доверить:** `codex plugin marketplace add <dir>`, затем `codex` → `/plugins` → установить `ac-probe`, `/hooks` → доверить. Ожидается: хуки видны в `/hooks` — значит, поле `hooks` в `.codex-plugin/plugin.json` читается.
- [ ] **Step 3: Прогнать ход:** в тестовом git-репо попросить Codex «выполни `ac-probe`» и «выполни `git status`». Записать: нашёл ли shell `ac-probe` в PATH и что печатает `echo "$PLUGIN_ROOT"` в shell; stdin `PermissionRequest` (поля, тип `tool_input.command` — строка или массив); в каком approval-режиме (`codex --ask-for-approval …`) `PermissionRequest` приходит, и выполняется ли после `allow` ровно запрошенная команда; ответил ли агент `PROBE` после `Stop` (формат `decision:block` работает); нет ли файла `claude-hooks-ran`.
- [ ] **Step 4: Имя навыка:** в Codex набрать `$` и посмотреть, как показан `probe` (`$probe` или с префиксом `ac-probe`). Проверить, что незнакомые поля frontmatter (`disable-model-invocation`, `allowed-tools`) не ломают загрузку.
- [ ] **Step 5: Маркетплейс `github`:** `codex plugin marketplace add Comprise/auto-context --ref release` — принимает ли Codex `.claude-plugin/marketplace.json` с `"source": "github"`.
- [ ] **Step 6: Записать поправки и закоммитить.** Для каждого пункта «Не подтверждено» из §2 спеки — факт и следствие: (а) `bin/` в PATH → иначе Task 3 и 4 используют абсолютный путь `<PLUGIN_ROOT>/bin/auto-context`, который хук подставляет в тексты из своего окружения, а `allowedCommand` сверяет литерально; (б) имя навыка → значение `codexSkillPrefix` в Task 3; (в) нужен ли `.agents/plugins/marketplace.json` в Task 5; (г) реальный JSON `PermissionRequest` → `Input` в Task 4 и фикстура `testdata/permission_request.json`; (д) approval-режим для Task 7; (е) если `hooks/hooks.json` всё же грузится — в `.codex-plugin/plugin.json` нужен явный `hooks`, а при неудаче — остановиться и пересмотреть спеку.

```bash
git add docs/superpowers/specs/2026-10-20-codex-support-design.md
git commit -m "docs: Codex probe findings"
```

### Task 1: Ключ конфига `index_file`

**Files:**
- Modify: `internal/config/config.go` (`Config`, `Default`, `knownKeys`, `fix`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.IndexFile string` (yaml `index_file`), `Default().IndexFile == "CLAUDE.md"`, константы `config.IndexClaude = "CLAUDE.md"`, `config.IndexAgents = "AGENTS.md"`.

- [ ] **Step 1: Failing test** `TestIndexFile`, таблица по содержимому config.yaml:
  - нет ключа → `IndexFile == "CLAUDE.md"`, проблем нет;
  - `index_file: AGENTS.md` → `"AGENTS.md"`, проблем нет;
  - `index_file: ""` и `index_file: null` → `"CLAUDE.md"`, проблем нет;
  - `index_file: README.md` → `Load` возвращает ошибку `index_file must be CLAUDE.md or AGENTS.md` (конфиг сломан, как при ошибке разбора);
  - `index_file: agents.md` → то же (регистр значим);
  - `Validate()` на литерале `Config{}` не выдаёт проблему `index_file`.
- [ ] **Step 2:** `go test ./internal/config -run TestIndexFile` → FAIL (нет поля).
- [ ] **Step 3: Реализовать:** поле с комментарием в стиле `Language`, ключ в `knownKeys`; пустое значение → умолчание в `fix()`; недопустимое — ошибка из `Load` (тот же путь, что ошибка разбора, → `Project.ConfigBroken()`).
- [ ] **Step 4:** `go test ./internal/config` → PASS.
- [ ] **Step 5: Commit** `feat(config): index_file key`.

### Task 2: Индексный файл из конфига в `units`, `rootindex`, `project`, CLI

**Files:**
- Modify: `internal/units/units.go` (`Classify`, `DocPaths`), `internal/units/walk.go:528,596`
- Modify: `internal/rootindex/rootindex.go` (`Check`, `Update`), `internal/project/project.go:27,44,257-275`, `internal/project/changes.go:41`, `internal/docstate/docstate.go:61,116`, `internal/hooks/stop.go:219,340,348`, `cmd/auto-context/main.go:350-355`
- Test: `internal/units/units_test.go`, `internal/rootindex/rootindex_test.go`, `internal/project/project_test.go`, `cmd/auto-context/main_test.go`

**Interfaces:**
- Consumes: `config.Config.IndexFile` (Task 1).
- Produces:
  - `units.ContextPath(unit string) string` — заменяет `DocPaths` (первое значение нигде не используется); все вызовы `_, ctx := units.DocPaths(u)` → `ctx := units.ContextPath(u)`.
  - `(*units.Tree).IndexFile() string` — имя из `cfg`, которым `Classify`, `interesting`, `addFile` заменяют литерал `"CLAUDE.md"`.
  - `rootindex.Check(root, name string) error`, `rootindex.Update(root, name string, entries []Entry) (bool, error)`.
  - `(*project.Project).IndexFile() string`.

- [ ] **Step 1: Failing tests:**
  - `units`: `TestClassifyIndexFile` — при `IndexFile: "AGENTS.md"`: `AGENTS.md` → `(KindIndexDoc, "_root")`, `src/a/AGENTS.md` → `(KindIndexDoc, "src/a")` для юнита, `src/a/CLAUDE.md` → `(KindIgnore, "")`; при умолчании — наоборот.
  - `units`: `TestDiscoverTracksIndexFile` — при `AGENTS.md` в `Tracked()` есть `src/a/AGENTS.md` и нет `src/a/CLAUDE.md`; при умолчании — наоборот (Review Focus 1). Набор юнитов от имени не зависит.
  - `units`: `TestContextPath` — `"_root"` → `"context/_root.md"`, `"src/a"` → `"context/src/a.md"`.
  - `rootindex`: `TestUpdateAgents` — `Update(root, "AGENTS.md", entries)` пишет секцию в `AGENTS.md`, `CLAUDE.md` не создаёт; `TestUpdateThroughSymlink` (unix-файл) — `AGENTS.md -> CLAUDE.md`, секция оказывается в `CLAUDE.md` (Review Focus 3).
  - `cmd`: `TestStatusBrokenMarkersAgents` — битые маркеры в `AGENTS.md` при `index_file: AGENTS.md` → вывод `warning: the auto-context:units markers in the root AGENTS.md are unbalanced, so the units index is not updated`.
- [ ] **Step 2:** `go test ./internal/units ./internal/rootindex ./internal/project ./cmd/...` → FAIL (компиляция).
- [ ] **Step 3: Реализовать** по Interfaces; в комментариях `CLAUDE.md` → «индексный файл» там, где речь о любом имени. В `main.go` имя берётся из `p.IndexFile()`; при `p.ConfigBroken()` `status` корневой индекс не проверяет. `rootindex.ErrBrokenMarkers` — текст без имени файла (`unbalanced auto-context:units markers in the root index file`).
- [ ] **Step 4:** `make test` → PASS.
- [ ] **Step 5: Commit** `feat: index file name from config`.

### Task 3: Агент и тексты хуков

**Files:**
- Create: `internal/hooks/agent.go`
- Modify: `internal/hooks/hooks.go` (`Env`), `internal/hooks/stop.go` (`Feedback`, `StopReason`, `LanguageNote`, `stopFeedback`, `configPaused`), `internal/hooks/session_start.go:117,130-134,152,249-286`
- Test: `internal/hooks/agent_test.go`, `internal/hooks/stop_test.go`, `internal/hooks/hooks_test.go`

**Interfaces:**
- Consumes: `(*project.Project).IndexFile()` (Task 2).
- Produces:
  - `type Agent string`; `const Claude Agent = "claude"`, `Codex Agent = "codex"`; `ParseAgent(s string) (Agent, bool)`.
  - `(a Agent) Skill(name string) string` — `Claude`: `"auto-context:"+name`; `Codex`: `codexSkillPrefix+name`, `const codexSkillPrefix = "$"` (значение по шагу 0).
  - `(a Agent) Command(name string) string` — `Claude`: `"/auto-context:"+name`; `Codex`: `codexSkillPrefix+name`.
  - `Env.Agent Agent` (нулевое значение трактуется как `Claude`).
  - `Feedback.Agent Agent`, `Feedback.IndexFile string` — заполняются в `Stop` после `buildFeedback` (сигнатура `buildFeedback` не меняется); `LanguageNote(lang, indexFile string) string`; `SessionNotice(stale, missing []string, max int, a Agent) string`.
  - `stopFeedback(text string, a Agent) ([]byte, error)` — `Claude`: как сейчас (`hookSpecificOutput.additionalContext`); `Codex`: ровно `{"decision":"block","reason":<text>}`. `configPaused` получает агента.

- [ ] **Step 1: Failing tests:**
  - `TestAgentRefs`: `Claude.Skill("auto-context-docs") == "auto-context:auto-context-docs"`, `Codex.Skill("auto-context-docs") == "$auto-context-docs"`, `Claude.Command("status") == "/auto-context:status"`, `Codex.Command("status") == "$status"`; `ParseAgent("codex") == (Codex, true)`, `ParseAgent("") == ("", false)`, `ParseAgent("Codex") == ("", false)`.
  - `TestStopReasonCodex`: `StopReason(Feedback{Agent: Codex, IndexFile: "AGENTS.md", Changed: []string{"src/auth"}})` содержит `(and <unit>/AGENTS.md if its purpose or key files changed) per skill $auto-context-docs.` и не содержит `CLAUDE.md`; `New: []string{"_root"}` → `create context/_root.md (and the root AGENTS.md purpose line).`
  - Существующие golden-строки `StopReason` для Claude не меняются: в них добавляется только `IndexFile: "CLAUDE.md"` (или пустое значение трактуется как `CLAUDE.md`).
  - `TestLanguageNoteIndex`: `LanguageNote("ru", "AGENTS.md") == "Write unit docs (AGENTS.md, context/) in language ru (`language` in .auto-context/config.yaml)."`
  - `TestSessionNoticeCodex`: оканчивается на `(skill $auto-context-docs) or run $status.`; `session_start.golden.json` для Claude не меняется.
  - `TestStopFeedbackCodex`: `stopFeedback("x", Codex)` == `{"decision":"block","reason":"x"}`; для `Claude` — прежний JSON.
  - `TestSessionStartRootIndexAgents`: при `index_file: AGENTS.md` и битых маркерах в корневом `AGENTS.md` сообщение SessionStart говорит `root AGENTS.md` и не содержит `CLAUDE.md`.
  - `TestBrokenIndexFileNoClaudeMD` (Review Focus 2): `index_file: agents.md` → SessionStart сообщает о сломанном конфиге, корневой `CLAUDE.md` не создан.
- [ ] **Step 2:** `go test ./internal/hooks` → FAIL.
- [ ] **Step 3: Реализовать** `agent.go`; `Stop` и `SessionStart` берут агент из `env.Agent`, имя — из `p.IndexFile()`; сообщение «Unit removed» в `session_start.go:117` — через `Skill`.
- [ ] **Step 4:** `go test ./internal/hooks` → PASS.
- [ ] **Step 5: Commit** `feat(hooks): agent-specific references`.

### Task 4: `--agent` и обработчик `permission-request`

**Files:**
- Create: `internal/hooks/permission_request.go`
- Modify: `internal/hooks/pre_tool_use.go` (общая проверка), `cmd/auto-context/main.go` (`run`, `runHook`, `hookHandlers`, `hookBudget`, `usage`)
- Test: `internal/hooks/permission_request_test.go`, `cmd/auto-context/main_test.go`

**Interfaces:**
- Consumes: `hooks.ParseAgent`, `Env.Agent` (Task 3).
- Produces:
  - `hooks.PermissionRequest(env Env, in Input) ([]byte, error)`; ответ для одобряемой команды ровно `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`.
  - `func approvable(in Input) bool` — `in.ToolName == "Bash" && allowedCommand(...) && !shadowed()`, используют оба обработчика.
  - `runHook(event string, agent hooks.Agent, stdin io.Reader, stdout io.Writer, cwd string) int`.
  - `hookHandlers["permission-request"]`, `hookBudget["permission-request"] = 8s`; `budgetFor(event string, a hooks.Agent) (time.Duration, bool)` — `hookBudget[event]`, кроме `Codex`+`session-end` = 2 с; `runHook` использует только её.
  - `Input.ToolInput` разбирается терпимо: `ToolInput json.RawMessage` + метод `(Input) Command() string` (строка `command`, иначе `""`); `pre-tool-use` переходит на `Command()`.

- [ ] **Step 1: Failing tests:**
  - `TestPermissionRequestAllows`: `ToolName "Bash"`, `Command "auto-context status --stale"` → точный JSON выше.
  - `TestPermissionRequestSilent` (таблица, Review Focus 5): `auto-context ack --exclude x`, `auto-context ack ../x`, `auto-context status; rm -rf /`, `ToolName "apply_patch"`, затенённый PATH (как в `pre_tool_use_test.go`) → `nil`.
  - `TestHookAgentFlag`: `run([]string{"hook","stop","--agent","codex"}, …)` в активном репо с правкой кода → вывод содержит `$auto-context-docs`.
  - `TestHookAgentFlagBad` (Review Focus 4): `{"hook","stop","--agent"}`, `{"hook","stop","--agent","gpt"}`, `{"hook","stop","--x","codex"}` → код 0, пустой stdout, в `.auto-context/log` строка `hook: bad arguments`.
  - `TestHookPermissionRequest`: `run({"hook","permission-request","--agent","codex"})` со stdin из `internal/hooks/testdata/permission_request.json` (снят в шаге 0) → JSON одобрения.
  - `TestHookToolInputArray`: `tool_input.command` — массив → код 0, пустой stdout, лог пуст.
  - `TestBudgetFor`: `budgetFor("session-end", Codex) == 2s`, `budgetFor("session-end", Claude) == 8s`, `budgetFor("permission-request", Codex) == 8s`.
- [ ] **Step 2:** `go test ./internal/hooks ./cmd/...` → FAIL.
- [ ] **Step 3: Реализовать:** в `run` для `hook` принимать `rest` длины 1 или 3 (`<event> --agent <name>`), иначе `logHook(..., "hook: bad arguments")`, 0. Если шаг 0 показал, что `bin/` не в PATH, `allowedCommand` при `Env.Agent == Codex` принимает вместо `auto-context` литеральный абсолютный путь `filepath.Join(CLAUDE_PLUGIN_ROOT, "bin", "auto-context")` без кавычек (только если путь из `A-Za-z0-9_./-`), а тексты хуков (Task 3) подставляют его же; добавить кейсы в `TestPermissionRequestAllows`.
- [ ] **Step 4:** `make test` → PASS.
- [ ] **Step 5: Commit** `feat: --agent flag and permission-request hook`.

### Task 5: Упаковка Codex, CI и релиз

**Files:**
- Create: `.codex-plugin/plugin.json`, `hooks/codex.json`, при необходимости (шаг 0) `.agents/plugins/marketplace.json`
- Modify: `.github/workflows/ci.yml` (job `plugin`), `.github/workflows/release.yml:85-125`
- Test: `cmd/auto-context/main_test.go`

**Interfaces:**
- Consumes: `hookHandlers`, `hookBudget` (Task 4).

- [ ] **Step 1: Failing test** `TestCodexHooksManifest`: читает `../../hooks/codex.json`; для каждой команды — вид `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context hook <event> --agent codex`, `<event>` есть в `hookHandlers`, `timeout` > `budgetFor(event, Codex)`; события ровно `SessionStart`, `UserPromptSubmit`, `Stop`, `PermissionRequest`, `SessionEnd`; таймауты из Global Constraints; у `PermissionRequest` `matcher == "^Bash$"`. Читает `../../.codex-plugin/plugin.json`: `name == "auto-context"`, `hooks == "./hooks/codex.json"`, поля `version` нет.
- [ ] **Step 2:** `go test ./cmd/... -run TestCodexHooksManifest` → FAIL.
- [ ] **Step 3: Создать файлы.** `plugin.json` — поля как в `.claude-plugin/plugin.json` плюс `hooks`; описание без слова CLAUDE.md: `"Keeps per-directory CLAUDE.md/AGENTS.md indexes and a context/ knowledge library in sync with code"`.
- [ ] **Step 4:** `go test ./cmd/...` → PASS; существующий тест на `hooks/hooks.json` (если есть) сверяется через `budgetFor(event, Claude)`.
- [ ] **Step 5: CI/релиз:** в `release.yml` штамп версии и для `.codex-plugin/plugin.json`, `.codex-plugin` (и `.agents`, если создан) в `tar` и `git add -f`; в `ci.yml` — `jq -e . .codex-plugin/plugin.json hooks/codex.json` и копирование `.codex-plugin` в дерево strict-проверки. Проверка: `jq`-команды локально выходят с 0, `claude plugin validate --strict` на дереве из `ci.yml` проходит (`npx --yes @anthropic-ai/claude-code@2.1.304 plugin validate --strict <tree>`).
- [ ] **Step 6: Commit** `feat: Codex plugin packaging`.

### Task 6: Навыки, README, доки юнитов

**Files:**
- Modify: `skills/init/SKILL.md`, `skills/sync/SKILL.md`, `skills/status/SKILL.md`, `skills/auto-context-docs/SKILL.md`, `skills/README.md`, `README.md`
- Modify: `context/_root.md`, `context/internal/hooks.md`, `context/internal/config.md`, `context/internal/units.md`, `context/internal/rootindex.md`, `context/internal/project.md`, `context/internal/docstate.md`, `context/cmd/auto-context.md` (по навыку `auto-context:auto-context-docs`)

- [ ] **Step 1: Навыки** (§6 спеки): «индексный файл юнита (`index_file`, по умолчанию `CLAUDE.md`)» вместо `CLAUDE.md`; в `init` — первым шагом, до создания любых доков: «в Codex запиши `index_file: AGENTS.md` в `.auto-context/config.yaml`»; `description` навыков без `/auto-context:…` как единственного триггера (добавить «or $init / $sync / $status in Codex»); в `sync` — «если выше нет вывода `auto-context status --stale` (Codex), выполни его»; дословные тексты хуков в «Status notes» обновить под Task 3. В `skills/README.md` — колонка вызова для Codex (`$init`, `$sync`, `$status`).
- [ ] **Step 2: README:** раздел «Codex»: `codex plugin marketplace add Comprise/auto-context --ref release` (или команда по шагу 0), `/plugins` → установить, `/hooks` → доверить хукам (после каждого обновления плагина — снова), `$init` (не встроенный `/init` Codex); в каком approval-режиме `ack` идёт без запроса (по шагу 0).
- [ ] **Step 3: Доки юнитов:** `auto-context status --stale`, обновить перечисленные `context/*.md` (новые `index_file`, `Agent`, `permission-request`, `hooks/codex.json`), затем `auto-context status` — все `ok`.
- [ ] **Step 4: Commit** `docs: Codex support in skills, README and unit docs`.

### Task 7: Ручной прогон в Codex

- [ ] **Step 1:** `make dist`, собрать дерево плагина как в `ci.yml` (с `libexec/`) во временном каталоге, добавить его маркетплейсом (`local`), установить, доверить хукам.
- [ ] **Step 2:** в тестовом git-репо `$init` → появились `.auto-context/config.yaml` с `index_file: AGENTS.md`, `AGENTS.md` с секцией `auto-context:units`, `context/_root.md`; `CLAUDE.md` не создан.
- [ ] **Step 3:** в approval-режиме из шага 0 попросить Codex поправить функцию в юните → в конце хода приходит продолжение с `code changed in … per skill $auto-context-docs`; Codex выполняет `auto-context ack …` без запроса разрешения.
- [ ] **Step 4:** выйти из Codex → `.auto-context/state` без файлов этой сессии; `.auto-context/log` пуст.
- [ ] **Step 5:** результаты (и найденные расхождения) — в раздел «Поправки» спеки; commit `docs: Codex end-to-end check`.
