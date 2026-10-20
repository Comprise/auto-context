# Исправления по аудиту 2026-10-20 — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** закрыть все находки аудита 2026-10-20 (ядро, интеграция, тесты, качество): исправить ошибки, дописать тесты, привести код и доки в порядок, не меняя вывод хуков Claude Code и не ослабляя модель угроз.

**Architecture:** сначала ошибки поведения (каждая — с падающим тестом): Stop (новые юниты, порядок правок по mtime), CLI `ack`, обход без git, концы строк, путь CLI для Codex. Затем упаковка и релиз, затем задачи только с тестами (общий `internal/testutil`, прямые тесты, golden-файлы), затем рефакторинги без изменения поведения (`stop.go`, раскол `walk.go`, идиомы, затенение, мёртвый код) и в конце доки.

**Tech Stack:** Go 1.27 (модуль `auto-context`), `gopkg.in/yaml.v3`, `doublestar/v4`, POSIX sh-шим, GitHub Actions, `jq`, Claude Code 2.1.304, Codex CLI 0.163.

**Spec:** `docs/superpowers/audits/2026-10-20/` (`README.md`, `core.md`, `integration.md`, `tests.md`, `quality.md`); фон — `context/_root.md`, `docs/superpowers/specs/2026-10-19-auto-context-design.md`, `docs/superpowers/specs/2026-10-20-codex-support-design.md` (раздел «Поправки по шагу 0»).

## Покрытие находок

Ниже `T<n>` — номер задачи. «Ограничение» — документированное ограничение; текст для доков дан в разделе «Документированные ограничения» и вносится задачей, указанной в скобках. В `quality.md` 17 находок (не 15): №16 и №17 тоже покрыты.

| Находка | Куда | Находка | Куда |
|---|---|---|---|
| core#1 | T1 | tests#1 | T10 |
| core#2 | T3 | tests#2 | T10 |
| core#3 | T2 | tests#3 | T10 (частично уже есть, см. задачу) |
| core#4 | Ограничение (T16) | tests#4 | T10 |
| core#5 | T4 | tests#5 | Rejected |
| core#6 | T4 | tests#6 | T11 |
| core#7 | T3 | tests#7 | T6 (чистая функция пути) + T11 (PATHEXT) |
| core#8 | T5 | tests#8 | T10 |
| integration#1 | T7 | tests#9 | T9 |
| integration#2 | T6 | tests#10 | Rejected |
| integration#3 | Ограничение (T7) | tests#11 | T9 |
| integration#4 | Rejected | tests#12 | T10 (только проверка ошибки `Chtimes`; см. Rejected-часть) |
| integration#5 | T16 | tests#13 | T10 |
| integration#6 | T16 (= quality#2) | tests#14 | T10 |
| integration#7 | T7 | tests#15 | T11 |
| integration#8 | Ограничение (T16) | quality#1 | T16 |
| integration#9 | T8 | quality#2 | T16 (= integration#6) |
| integration#10 | T8 | quality#3 | T16 |
| integration#11 | T8 | quality#4 | T8 |
| | | quality#5 | T6 |
| | | quality#6 | T16 |
| | | quality#7 | T3 |
| | | quality#8 | T12 |
| | | quality#9 | T13 |
| | | quality#10 | T13 |
| | | quality#11 | T12 |
| | | quality#12 | T12 |
| | | quality#13 | T14 |
| | | quality#14 | T15 |
| | | quality#15 | T15 |
| | | quality#16 | T14 |
| | | quality#17 | T16 |

### Rejected findings

| Находка | Почему отклонена | Доказательство |
|---|---|---|
| integration#4 (Stop у Claude через `additionalContext`) | Выбор осознанный и записан в спецификации; в репозитории нет ни одного свидетельства, что Claude Code его отвергает. Менять поведение Claude по догадке запрещено Global Constraints. | `docs/superpowers/specs/2026-10-19-auto-context-design.md` §8.3, п. 5: «Используется `hookSpecificOutput.additionalContext`, а не `decision: "block"` … Защиты от зацикливания у Claude Code те же»; `stopFeedback` (`internal/hooks/stop.go:161-175`) и тест `feedback()` (`internal/hooks/stop_test.go:53-68`) пинуют эту форму. T11 добавляет golden-файл Stop, чтобы форма не поменялась случайно. |
| tests#5 (фикстура `permission_request.json` не используется) | Ложная. | `cmd/auto-context/main_test.go:1049-1066` (`TestHookPermissionRequest`) читает `internal/hooks/testdata/permission_request.json`; отмечено и в `audits/2026-10-20/README.md`. |
| tests#10 (git без `LookPath`-пропуска, коммиты без identity) | Неверно по фактам. | `LookPath`-пропуски: `cmd/auto-context/main_test.go:586,625,689,748,771,857,946,962,983`; `internal/units/git_test.go:18-27` (`gitInit`); identity передаётся в каждом коммите: `main_test.go:592,634,694,777,862,988` (`-c user.name=t -c user.email=t@t`). Сведение в общий `RequireGit`/`Git` — часть T9 (tests#11), а identity через `GIT_AUTHOR_*` T9 даёт заодно. |
| tests#12, часть «тест прошёл бы при безусловной записи» | Ложная: при безусловном `Save` mtime стал бы «сейчас», а тест требует не позже `past+1m` — упал бы. | `internal/hashing/hashing_test.go:199-208`. Остаётся проигнорированная ошибка `os.Chtimes` в строке 200 — это та же строка, что в tests#8; чинится в T10. |

Частичные отклонения внутри задач (остальная часть находки исправляется): `quality#15` — `hashing.Cache.Len` остаётся (его используют тесты двух пакетов, `hashing` и `project/ops_test.go:113`; `export_test.go` второму пакету не поможет); `quality#16` — три структуры вывода хуков не сливаются (R16); `quality#12` — повтор `capList`+`more` остаётся (явные пять строк читаются легче обёртки с колбэком); `integration#11` — права `libexec` в `make dist` не трогаются (`go build` создаёт файлы с битом исполнения, релиз делает `chmod +x`, `release.yml:92`); `tests#7` — пропуски на Windows в `docstate_test.go:141`, `logx_test.go:156,240`, `session_problems_test.go:21,26` остаются: они про права и симлинки, у которых на Windows другая семантика, а не про поиск по PATH; `tests#14` — методы-заглушки `statInfo` (`walk.go:544-549`) не тестируются: это обязательная часть `fs.FileInfo`, `Name`/`Sys` никто не вызывает.

### Документированные ограничения

| Находка | Почему код не исправить | Точный текст (куда) |
|---|---|---|
| core#4 (параллельные сессии в одном дереве) | Stop по замыслу сравнивает всё дерево со снимком начала хода, чтобы видеть правки через shell (`mv`, `sed`, генераторы; `TestStopBashRename`). Отделить «свои» правки по stat нельзя; пересечение с путями из PostToolUse Edit/Write потеряло бы правки через shell и не работает в Codex, где таких хуков нет. Это нарушило бы инвариант «правка кода юнита в ходе замечается». | `context/_root.md`, сразу после пункта про `cache.json`: «- Параллельные сессии (и правки пользователя в редакторе) в одном дереве не различаются: Stop каждой сессии сравнивает всё дерево со своим снимком начала хода, поэтому правка, сделанная во время чужого хода, попадает в напоминание и auto-ack обеих. Отделить свои правки по stat нельзя; для параллельной работы нужны отдельные worktree.» README, раздел «Если что-то не работает»: «- Две сессии агента в одном каталоге видят правки друг друга: напоминание или auto-ack может прийти не той сессии. Для параллельной работы используйте отдельные `git worktree`.» (T16) |
| integration#3 (Codex на Windows) | Проверить нечем: шаг 0 спецификации Codex прогонялся только на Linux; хуки Codex — команды POSIX sh (`"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context …`), и как Codex исполняет их в Windows без WSL, неизвестно. Обещать непроверенное нельзя. | README, раздел «Требования», после пункта про Windows: «- **Codex:** только Linux и macOS (в Windows — внутри WSL). Хуки Codex — команды POSIX sh; как Codex запускает их в Windows без WSL, не проверено.» README, раздел «Codex», первым абзацем после заголовка: «Codex поддерживается в Linux и macOS (в Windows — через WSL).» (T7) |
| integration#8 (шим берёт `.` вместо `cwd` из stdin) | stdin хука читает бинарь; шим его не трогает (иначе бинарь получит пустой ввод), а разбирать JSON в POSIX sh без `jq` ненадёжно. По «Поправкам по шагу 0» рабочий каталог хука Codex — cwd сессии, то есть тот же каталог, что `cwd` в stdin; расхождение возможно только в краевых случаях. | `bin/README.md`, новый пункт после пункта про «Включённый проект»: «- Шим не читает stdin (его читает бинарь), поэтому включённость проверяет от `CLAUDE_PROJECT_DIR`, а без него — от рабочего каталога процесса (в Codex это cwd сессии), а не от `cwd` из JSON. Его уведомление о пропавшем или упавшем бинаре — best effort.» (T16) |

### Rulings

- **R1 (core#1)** Юнит считается новым в этом ходе, если его `context/<u>.md` нет в снимке начала хода или дока нет сейчас (`DocAbsent`); проверяются `pending ∪ acked`, а не только `pending`. Для новых юнитов, как и сейчас, проверяются родитель и `_root` через `pendingUnits` (лишняя проверка безвредна: актуальный по хэшу юнит отфильтровывается). — почему: это ловит и «юнит + доки в одном ходе», и «подкаталог перешёл `min_files` и получил доки», без хранения списка юнитов рядом со снимком (новый формат снимка). — цена ошибки: лишний хэш родителя для юнита, который был без доков до хода (кэш делает его дешёвым), либо пропуск случая «юнит появился, а его док уже лежал в `context/` до хода» — тот же пропуск, что сейчас.
- **R2 (core#2)** Охрана `Active` — внутри `Project.UpdateIndex` (как у `SaveCache`), а `runAck` вызывает `RefreshIndex` только если хоть один `Ack` удался. — почему: SessionStart и Stop уже активны, а CLI так не может забыть проверку; провал всех ack ничего не должен трогать. — цена ошибки: `ack` в неактивном проекте больше не пишет оглавление (раньше писал); в `init` к шагу 4 `context/_root.md` уже есть, так что проект активен.
- **R3 (core#3)** Изменённый в ходе файл кода, чей mtime старше mtime файла снимка начала хода, имеет неизвестное время правки: его юнит в этом ходе не авто-ackается (`newestCode = math.MaxInt64`), напоминание приходит, нужен явный `ack`. Время снимка — mtime файла `.auto-context/state/<id>.snapshot` (та же ФС, тот же источник часов, что у файлов кода); при ошибке stat правило выключено (0). — почему: использовать «время Stop» для всех добавленных файлов сломало бы обычный сценарий «создал файл, потом написал доки», а `ctime` нет в снимке и на Windows. — цена ошибки: лишнее напоминание после `mv`/`cp -p`/`tar x` даже когда доки писались после переноса, и при сильном расхождении часов ФС (снимок «из будущего») — напоминания вместо auto-ack.
- **R4 (core#4)** Документированное ограничение (см. таблицу). — почему: исправление нарушило бы замысел diff всего дерева. — цена ошибки: пользователи с параллельными сессиями в одном каталоге продолжают получать чужие напоминания.
- **R5 (core#7)** После успешного `ack --exclude` проект открывается заново (`project.Open`: дерево с новым конфигом) и вызывается `RefreshIndex`; `Exclude` создаёт `config.yaml`, поэтому проект после него активен и оглавление пишется даже в первом шаге `init`. — почему: иначе исключённый юнит висит в оглавлении до следующего SessionStart. — цена ошибки: `init` создаёт корневой индексный файл чуть раньше (на шаге 2, а не 4); имя берётся из `index_file`, который Codex-`init` пишет на шаге 0.
- **R6 (core#8)** Конец строки для вставляемых строк определяет первая строка существующего файла: `\r\n` — пишем CRLF, иначе LF; пустой файл или файл без перевода строки — LF. Для frontmatter: если наш блок уже был, решает его первая строка (`---\r\n`), иначе первая строка тела. — почему: просто, детерминированно, совпадает с тем, что видит пользователь в начале файла. — цена ошибки: в файле со смешанными концами строк вставка пойдёт по первой строке, а не по большинству — шум в diff остаётся только в таком и без того смешанном файле.
- **R7 (integration#1)** Путь бинарника Codex находит навык, а не SessionStart: во всех четырёх навыках — одинаковый абзац с командой `ls -td "${CODEX_HOME:-$HOME/.codex}"/plugins/cache/*/auto-context/*/bin/auto-context | head -n 1` на случай, если ни одно сообщение хука пути не дало. Неактивный проект по-прежнему ничего не выводит. — почему: путь нужен и в активном проекте, где SessionStart молчит при актуальных доках, и до доверия хукам (`/hooks`), когда хуки не работают вовсе; вывод в каждой Codex-сессии каждого чужого репозитория — шум и нарушение «неактивный проект — тишина». `ls -t` (последняя установленная версия) вместо `sort -V`: POSIX, есть на macOS. — цена ошибки: если Codex сменит раскладку кэша, команда ничего не найдёт и агент спросит пользователя; правится одной строкой в навыках и тесте.
- **R8 (integration#2)** `Agent.CLI()` для Codex принимает любой абсолютный POSIX-корень (начинается с `/`, валидный UTF-8, без управляющих символов `< 0x20` и `0x7f`) и, если в пути есть символ вне `[A-Za-z0-9_./-]`, берёт весь путь в одинарные кавычки (`'` внутри → `'\''`). `approvable` по-прежнему сравнивает литеральный префикс `CLI()+" "`, так что одобряется только ровно эта запись. Корень вне правил (пустой, относительный, `C:\…`, с переводом строки) — по-прежнему `auto-context`. — почему: пробел, `@`, `+`, `~`, не-ASCII в домашнем каталоге — обычное дело, а голое `auto-context` в Codex не работает; кавычки — одно shell-слово, указывающее ровно на бинарник плагина, строгость одобрения та же. — цена ошибки: агент перепишет кавычки по-своему (`\ ` вместо `'…'`) — тогда будет обычный запрос разрешения, не авто-одобрение.
- **R9 (integration#3)** Документировать «Codex — Linux/macOS, Windows через WSL» (см. таблицу ограничений); `CLI()` для `C:\…` остаётся `auto-context`. — почему: проверить на Windows нечем. — цена ошибки: пользователи Codex на Windows, у которых всё работало бы, читают «не поддерживается».
- **R10 (integration#4)** Поведение Claude не меняется (Rejected). — почему: спецификация §8.3 фиксирует выбор, репозиторий его не опровергает. — цена ошибки: если новая версия Claude Code перестанет показывать `additionalContext` у Stop, напоминания пропадут молча; golden-файл Stop из T11 сделает переход на `decision:block` одной точечной правкой.
- **R11 (integration#8)** Документированное ограничение шима (см. таблицу). — почему: шим не может читать stdin. — цена ошибки: уведомление шима в редком случае приходит не для того проекта.
- **R12 (integration#9)** `checksums.txt` содержит плоские имена (`auto-context-linux-amd64`, …, `auto-context-vX.Y.Z.tar.gz`), и все перечисленные файлы прикладываются к GitHub Release (шесть бинарников — это и есть субъекты attestation). — почему: `sha256sum -c checksums.txt` в каталоге загрузок работает, а `gh attestation verify` применим к скачанному бинарнику. — цена ошибки: шесть лишних файлов в релизе; проверка бинарника внутри распакованного архива — из `libexec/` с `--ignore-missing`.
- **R13 (quality#5)** `hook permission-request` без `--agent codex` (или с `--agent claude`) — ошибка аргументов: запись `hook: bad arguments` в лог, выход 0, пустой вывод. — почему: так написано в usage и в доке, а в `hooks/hooks.json` этого события нет; одобрение не должно включаться для агента, у которого нет такого протокола. — цена ошибки: нет (Claude этот хук не регистрирует).
- **R14 (quality#7)** `docstate.NoteOutside` = `"unusable symlink"`; навык `auto-context-docs` («Status notes») меняется в той же задаче. — почему: прежний текст врёт для ссылок в `.git`, на не-`.md` и висячих. — цена ошибки: чьи-то скрипты, грепающие `symlink outside repo` в `status`, перестанут срабатывать.
- **R15 (quality#15)** Удалить `Input.HookEventName`, `Input.Source`, `Input.StopHookActive` (код их не читает; тест на «Stop игнорирует `stop_hook_active`» без поля бессмыслен — Stop его не видит по построению); `Cache.Len` оставить. `Input.Command` разбирает `command` в `string`. — почему: мёртвые поля вводят в заблуждение. — цена ошибки: при будущей потребности поле вернуть — одна строка.
- **R16 (quality#16)** `stopOutput`, `sessionOutput`, `preToolUseOutput`, `permissionRequestOutput` не сливаются в общий тип. — почему: это разные формы протокола, совпадает только `hookEventName`; общий тип с `omitempty` рискует изменить байты вывода Claude. — цена ошибки: пара лишних строк определения типов.
- **R17 (quality#3)** Устаревшие места спецификации 2026-10-19 правятся на месте (её шапка уже говорит «при расхождении верен код»), а у Codex-спецификации статус становится «реализовано». — почему: пометка «исторический раздел» хуже короткой точной правки. — цена ошибки: спецификация снова отстанет при следующих изменениях — как и сейчас.
- **R18 (quality#9)** `walk.go` делится на `ignore.go` (сопоставление `.gitignore` и шаблонов), `gitfiles.go` (`gitFiles` и её ошибки) и `walk.go` (`Discover`, обход, `addFile`); `SafeName` переезжает в `units.go`. — почему: три независимые части с отдельными тестами. — цена ошибки: потеря `git blame` по перемещённым строкам (смягчается `git log --follow -M`).
- **R19 (tests#9)** `TestMain` через `testutil.Main` в пакетах, которые запускают git или читают переменные агента (`cmd/auto-context`, `internal/hooks`, `internal/units`, `internal/gitx`, `internal/project`): снимает `GIT_*`, `CLAUDE_PROJECT_DIR`, `CLAUDE_PLUGIN_ROOT`, `CLAUDE_PLUGIN_DATA`, `PLUGIN_ROOT`, `PLUGIN_DATA`, `CODEX_HOME`; ставит `GIT_CONFIG_GLOBAL=os.DevNull`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_AUTHOR_NAME/EMAIL`, `GIT_COMMITTER_NAME/EMAIL` = `t`/`t@t`. `HOME` не трогается (от него зависит `FindRoot` и шим, тесты ставят его сами). — почему: тесты, запущенные из сессии Claude Code, сейчас наследуют `CLAUDE_PROJECT_DIR` и берут корень хука из него; глобальный `commit.gpgsign` разработчика ломает коммиты в тестах. — цена ошибки: тест, который неявно полагался на окружение разработчика, упадёт — и это правильно.

## Global Constraints

- Вывод хуков Claude Code (`hook <event>` без `--agent`) байт-в-байт прежний, кроме мест, которые явно чинит задача (в этом плане таких нет: T1 и T2 меняют лишь, *когда* приходит то же напоминание). Существующие golden и строковые ожидания тестов Claude не меняются; T11 добавляет golden Stop с текущим текстом.
- `hooks/hooks.json` не меняется.
- Хуки всегда завершаются с кодом 0; ошибки — в `.auto-context/log` (`logHook`/`logx.Errorf`).
- Модель угроз `context/_root.md` не ослабляется: служебные и документные пути — через `fsutil.Resolve`/`ResolveDoc`, чтения с лимитом, git — через `gitx`, текст из репозитория в контекст агента не цитируется, неактивный проект ничего не получает на диск. Авто-одобрение остаётся таким же строгим: только `ownCLI`-форма, без `--exclude` и `..`, абсолютный путь — только литерал из `CLAUDE_PLUGIN_ROOT`.
- Никаких `filepath.Join` в тексте, который уходит в shell (подсказки команд, JSON хуков): только `/` (`path.Join`), иначе ломается Windows CI.
- Тесты зелёные после каждой задачи; рефакторинги (T12–T15) не меняют поведения и существующих ожиданий тестов (меняются только вызовы изменённых внутренних сигнатур).
- Доки — на русском (`language: ru`). Навыки и тексты для агента — на английском, как сейчас.
- После правок кода юнита: обновить `context/<unit>.md` (и `<unit>/CLAUDE.md`, если меняются назначение или ключевые файлы) по правилам `skills/auto-context-docs/SKILL.md` (только текущее состояние, без истории), затем `go run ./cmd/auto-context ack <unit…>`; если правка не задевает назначение/API/устройство/инварианты — только `ack`. Правки `skills/`, `hooks/`, `bin/`, `.github/`, README → юнит `_root`.
- Перед каждым коммитом: `make test`, `go vet ./...`, `gofmt -l .` (пусто), `go run ./cmd/auto-context status --stale` → `all docs up to date`.
- Тексты, которые навыки цитируют дословно (`skills/README.md`), меняются вместе с навыками.

## Review Focus

1. Снимок, оставленный с прошлого хода (пауза из-за сломанного конфига), старше всех правок: после R3 правки кода в ходе новее снимка, и auto-ack должен работать как раньше (тест в T2).
2. CRLF-файл, последняя строка которого без перевода строки, и однострочный файл без перевода строки: вставка не склеивает строки, концы строк — по R6 (тест в T5).
3. Корень плагина с одинарной кавычкой (`/home/o'neil/…`): подсказка `'/home/o'\''neil/…/bin/auto-context' ack svc` одобряется, а та же команда с хвостом `; rm -rf /` или в другой записи — нет (тест в T6).
4. `ack --exclude` в Codex-репозитории (`index_file: AGENTS.md`) на шаге 2 `init`: оглавление пишется в `AGENTS.md`, `CLAUDE.md` не создаётся (тест в T3).
5. Обход без git: каталог только с `.gitignore` или только с не-кодовыми файлами — каталог есть в обоих режимах, пустой или целиком игнорируемый — ни в одном (тест в T4).

---

### Task 1: Stop проверяет родителя и `_root` для юнита, задокументированного в том же ходе

Находка: core#1.

**Files:**
- Modify: `internal/hooks/stop.go:96-107` (`Stop`)
- Test: `internal/hooks/stop_test.go`
- Docs: `context/internal/hooks.md`

**Interfaces:**
- Produces: `func turnNewUnits(us []string, before session.Snapshot, doc func(string) docstate.DocKind) []string` — отсортированный уникальный список юнитов из `us`, кроме `units.RootUnit`, у которых `doc(u) == docstate.DocAbsent` или `units.ContextPath(u)` нет в `before`. T12 использует его в `withParents`.

- [ ] **Step 1: Failing tests** в `stop_test.go`:
  - `TestTurnNewUnits`: `before = {"context/svc.md": …}`; `us = ["_root","pkg","svc"]`, `doc` → всегда `DocPresent` ⇒ `["pkg"]`; `doc` → `DocAbsent` для `svc` ⇒ `["pkg","svc"]`; `_root` не попадает ни при каком `doc`.
  - `TestStopNewUnitWithDocsFlagsRoot`: `env := turn(t)`; `base := time.Now().Add(time.Hour)`; написать `pkg/go.mod`, `pkg/a.go` (`setMtime(…, base, 0)`), затем `context/pkg.md` = `"# pkg\n\nPkg summary.\n"` (`setMtime(…, base, time.Second)`); `r := feedback(t, stop(t, env))` содержит `"code changed in _root "` и не содержит `"New unit pkg"`; `unitState(t, env, "pkg") == docstate.OK`.
  - `TestStopSubdirBecomesUnitFlagsParent`: `env := newRepo(t, false)`; `svc/sub/{a,b,c,d}.go`; `ack(t, env, "svc", "_root")`; `PromptSubmit`; написать `svc/sub/e.go` (base) и `context/svc/sub.md` (base+1s); `feedback` содержит `"code changed in _root, svc "`; `unitState(t, env, "svc/sub") == docstate.OK`.
- [ ] **Step 2:** `go test ./internal/hooks -run 'TestTurnNewUnits|TestStopNewUnitWithDocsFlagsRoot|TestStopSubdirBecomesUnitFlagsParent' -count=1` → FAIL (`turnNewUnits` не определена; после заглушки — пустой вывод Stop).
- [ ] **Step 3: Реализовать** `turnNewUnits` в `stop.go`; в `Stop` цикл по `pending` заменить на `for _, u := range turnNewUnits(session.AddUnique(pending, acked...), before, docKind)` → `parents = AddUnique(parents, tree.UnitOf(u), units.RootUnit)`; вызов `pendingUnits` для родителей не меняется. `docKind` — та же функция, что передаётся в `buildFeedback` (вынести в локальную переменную, вызывать один раз на юнит).
- [ ] **Step 4:** `go test ./internal/hooks -count=1` → PASS (в т.ч. `TestStopNewUnitAndExclude`).
- [ ] **Step 5: Доки:** в `context/internal/hooks.md` в описании шагов Stop: родитель и `_root` проверяются для каждого юнита, чей `context/<u>.md` появился в ходе или отсутствует (в том числе авто-ackнутого). `go run ./cmd/auto-context ack internal/hooks`.
- [ ] **Step 6: Commit** `fix(hooks): flag parent and _root for a unit documented in the same turn`.

### Task 2: auto-ack не верит mtime файлов старше начала хода

Находка: core#3.

**Files:**
- Modify: `internal/session/session.go` (новая функция), `internal/hooks/stop.go:84,203-233` (`Stop`, `recordTurn`)
- Test: `internal/session/snapshot_test.go`, `internal/hooks/stop_test.go`
- Docs: `context/internal/session.md`, `context/internal/hooks.md`, `context/_root.md` (инвариант про правку `context/<unit>.md`)

**Interfaces:**
- Produces:
  - `func SnapshotTime(root, sessionID string) (int64, error)` в `internal/session` — mtime (ns) файла снимка через `fsutil.Resolve` + `os.Stat`; нет файла → ошибка с `os.ErrNotExist`.
  - `func recordTurn(tree *units.Tree, before, cur session.Snapshot, turnStart int64, j *session.Journal) []string` — `turnStart` в ns; `0` выключает правило.

- [ ] **Step 1: Failing tests:**
  - `session`: `TestSnapshotTime` — после `Snapshot{}.Save(root, "s")` и `os.Chtimes(SnapshotPath(root,"s"), t0, t0)` → `SnapshotTime == t0.UnixNano()`; для `"none"` → `errors.Is(err, os.ErrNotExist)`.
  - `hooks`: `TestRecordTurnOldMtime` — `before` без `svc/moved.go`, `cur` с `svc/moved.go {MtimeNs: 5}` и `context/svc.md {MtimeNs: 20}` (изменён), `turnStart = 10` ⇒ `fresh` пуст, `j.CodeChanged == ["svc"]`, `j.DocsTouched == ["svc"]`; тот же набор с `turnStart = 0` ⇒ `fresh == ["svc"]`; изменённый (не новый) `svc/main.go` с `MtimeNs: 5` при `turnStart = 10` ⇒ `fresh` пуст.
  - `hooks`: `TestStopMovedInFileNotAutoAcked` — вне репо `old := filepath.Join(t.TempDir(), "moved.go")` с mtime `2020-01-01`; `env := turn(t)`; правка `context/svc.md`; `os.Rename(old, <root>/svc/moved.go)`; `feedback` содержит `"code changed in svc "`; `unitState(svc) == docstate.Stale`.
  - `hooks` (Review Focus 1): `TestStopAutoAcksWithOldSnapshot` — `turn(t)`; `os.Chtimes(session.SnapshotPath(root, stopSID), now-1h, now-1h)`; правка `svc/main.go` (base), `context/svc.md` (base+1s) ⇒ `stop` возвращает `nil`, `unitState(svc) == docstate.OK`.
  - `TestRecordTurn` — вызов получает `turnStart = 0`, ожидания прежние.
- [ ] **Step 2:** `go test ./internal/session ./internal/hooks -run 'TestSnapshotTime|TestRecordTurn|TestStopMovedInFileNotAutoAcked|TestStopAutoAcksWithOldSnapshot' -count=1` → FAIL.
- [ ] **Step 3: Реализовать:** `SnapshotTime` рядом с `LoadSnapshot`. В `recordTurn` для `KindCode` с файлом в `cur`: `m := fs.MtimeNs; if m < turnStart { m = math.MaxInt64 }`, затем `max`. В `Stop` после успешного `LoadSnapshot`: `start, err := session.SnapshotTime(root, id)`; при ошибке `start = 0` без записи в лог (снимок только что прочитан, ошибка — гонка). Обновить комментарий `recordTurn`: файл с mtime старше снимка (перенесён с сохранением времени) упорядочить нельзя.
- [ ] **Step 4:** `make test` → PASS.
- [ ] **Step 5: Доки:** `context/internal/session.md` — `SnapshotTime`; `context/internal/hooks.md` — правило R3 одной строкой в «Инварианты»; `context/_root.md`, инвариант «правка `context/<unit>.md` засчитывается…»: дополнить «файл кода с mtime старше начала хода (перенесённый `mv`/`cp -p`) auto-ack юнита в этом ходе запрещает». `go run ./cmd/auto-context ack internal/session internal/hooks _root`.
- [ ] **Step 6: Commit** `fix(hooks): no auto-ack when a changed file predates the turn`.

### Task 3: `ack` пишет оглавление только в активном проекте и после `--exclude`; текст заметки о ссылке

Находки: core#2, core#7, quality#7.

**Files:**
- Modify: `internal/project/project.go:257-269` (`UpdateIndex`), `cmd/auto-context/main.go:272-315` (`runAck`), `internal/docstate/docstate.go:43`, `skills/auto-context-docs/SKILL.md` («Status notes»)
- Test: `internal/project/project_test.go`, `cmd/auto-context/main_test.go`, `internal/docstate/docstate_test.go:133-134`
- Docs: `context/internal/project.md`, `context/cmd/auto-context.md`, `context/internal/docstate.md`

**Interfaces:**
- Produces: `(*Project).UpdateIndex` возвращает `(false, nil)`, ничего не читая и не создавая, если `!Active(p.root)`. `docstate.NoteOutside Note = "unusable symlink"`.

- [ ] **Step 1: Failing tests:**
  - `project`: `TestUpdateIndexInactiveWritesNothing` — неактивный корень с `pkg/f1..f5.go`; `Open` + `RefreshIndex()` → `nil`; файла `CLAUDE.md` нет.
  - `cmd`: `TestAckTypoInactiveWritesNothing` — `t.TempDir()` с `pkg/f1.go … pkg/f5.go` (без конфига и `context/`), `do(root, "", "ack", "typo")` → код 1, stderr содержит `typo`; ни `CLAUDE.md`, ни `.auto-context` не созданы.
  - `cmd`: `TestAckAllFailKeepsIndex` — активный `repo(t)`; запомнить байты `CLAUDE.md` (или его отсутствие); `ack typo` → код 1, файл байт-в-байт тот же.
  - `cmd`: `TestAckExcludeRefreshesIndex` — активный репо с юнитом `newu` (`newu/a..e.go`) и `context/_root.md`; `ack _root` → `CLAUDE.md` содержит `` - `newu` ``; `ack --exclude newu` → код 0, `CLAUDE.md` не содержит `` `newu` ``.
  - `cmd` (Review Focus 4): `TestAckExcludeAgentsIndex` — `config.yaml` = `index_file: AGENTS.md\n`, юниты `keep` и `newu` (по 5 файлов), без `CLAUDE.md`; `ack --exclude newu` → `AGENTS.md` содержит `` `keep` `` и не содержит `` `newu` ``; `CLAUDE.md` не создан.
  - `docstate`: в `docstate_test.go:133-134` ожидание `"unusable symlink"` и текст ошибки `want stale (unusable symlink)`.
- [ ] **Step 2:** `go test ./internal/project ./internal/docstate ./cmd/... -count=1` → FAIL.
- [ ] **Step 3: Реализовать:** охрана в `UpdateIndex` (после проверки `ConfigBroken`). В `runAck`: счётчик успешных `Ack`, `RefreshIndex` только при `> 0`. В ветке `--exclude`: после успешного `Exclude` — `q, err := project.Open(p.Root())` (ошибка → `warning: root index: …` в stderr, код 0), `q.RefreshIndex()` с тем же предупреждением, `saveCache(q, stderr)`. `NoteOutside` = `"unusable symlink"`; в навыке строка «Status notes» → `` `stale (unusable symlink)` — the doc is a symlink that leads outside the repository, into `.git`, to a non-`.md` file, or nowhere: … `` (дальше без изменений).
- [ ] **Step 4:** `make test` → PASS (в т.ч. `TestAckRefreshesRootIndex`, `TestAckExcludeWritesConfig`).
- [ ] **Step 5: Доки:** `context/internal/project.md` — `UpdateIndex` в неактивном проекте ничего не делает; `context/cmd/auto-context.md` — `ack` обновляет оглавление, только если хоть один ack удался; `ack --exclude` перечитывает проект и обновляет оглавление; `context/internal/docstate.md` — текст заметки. `go run ./cmd/auto-context ack internal/project cmd/auto-context internal/docstate _root`.
- [ ] **Step 6: Commit** `fix: ack writes the root index only in active projects, also after --exclude`.

### Task 4: обход без git совпадает с git — каталоги и правила `*<суффикс>` со скобками

Находки: core#5, core#6.

**Files:**
- Modify: `internal/units/walk.go:178-233` (`ignoreRule`, `parseIgnore`), `:293-297` (`match`), `:692-709` (`walk`)
- Test: `internal/units/git_test.go`
- Docs: `context/internal/units.md`

**Interfaces:**
- Produces: поле `ignoreRule.suffix string` — неэкранированный текст после `*` для правил из корзины `suffix`; `match` сравнивает с ним.

- [ ] **Step 1: Failing tests** в `git_test.go` (оба режима, как `TestDiscoverGitMatchesWalk`):
  - `TestDiscoverEmptyDirsMatchGit`: `include: ["packages/*"]`; `packages/a/x.go`, `packages/old/out.log` (корневой `.gitignore` = `*.log\n`), пустой `packages/empty`, `packages/txt/readme.txt` (Review Focus 5), `packages/gi/.gitignore` = `x\n` (Review Focus 5) ⇒ `paths(walk.Units()) == paths(git.Units())` и равно `["_root", "packages/a", "packages/gi", "packages/txt"]`.
  - `TestGitignoreBraceSuffixMatchesGit`: `.gitignore` = `*{a}.go\n*}.go\n`, файлы `x{a}.go`, `y.go`, `z}.go` ⇒ `Tracked()` в обоих режимах равен `["y.go"]` (`.gitignore` не отслеживается: он не код).
- [ ] **Step 2:** `go test ./internal/units -run 'TestDiscoverEmptyDirsMatchGit|TestGitignoreBraceSuffixMatchesGit' -count=1` → FAIL.
- [ ] **Step 3: Реализовать:**
  - `parseIgnore`: в ветке `suffix` записать `r.suffix = l[1:]` (до замены скобок); `match` — `strings.HasSuffix(base, f.rules[i].suffix)`.
  - `walk`: убрать создание `dirInfo` для каждого каталога (строки 700-702). Для каждого обычного файла (не симлинка), который не `t.excluded(rel)`: если `dirs[path.Dir(rel)] == nil` и файл не `t.gitIgnored(rel, false)`, — добавить `dirInfo` для его каталога и предков вверх до уже известного, пропуская `isContext`. Проверка `.gitignore` идёт только для первого файла нового каталога (если каталог есть, предки тоже есть), так что стоимость обхода не растёт. Затем `addFile` как сейчас.
- [ ] **Step 4:** `go test ./internal/units -count=1` → PASS (в т.ч. `TestDiscoverGitMatchesWalk`, `TestIncludeDirWithoutDirectFiles`, `TestGitignoreMoreFormsMatchGit`).
- [ ] **Step 5: Доки:** `context/internal/units.md` — каталог в обоих режимах существует, только если в нём (или ниже) есть неигнорируемый файл. `go run ./cmd/auto-context ack internal/units`.
- [ ] **Step 6: Commit** `fix(units): walk mode makes the same dirs and brace suffix rules as git`.

### Task 5: CRLF-файлы получают CRLF-строки

Находка: core#8.

**Files:**
- Modify: `internal/rootindex/rootindex.go:38-53,128-150` (`render`, `Update`), `internal/frontmatter/frontmatter.go:91-107` (`SetHash`)
- Test: `internal/rootindex/rootindex_test.go`, `internal/frontmatter/frontmatter_test.go`
- Docs: `context/internal/rootindex.md`, `context/internal/frontmatter.md`

**Interfaces:**
- Produces: `func render(head string, entries []Entry, eol string) string` (`Render(entries)` = `render(header, entries, "\n")`, публичный API не меняется); в обоих пакетах свой неэкспортируемый `eolOf(s string) string` — `"\r\n"`, если первая строка `s` кончается на `\r\n`, иначе `"\n"`.

- [ ] **Step 1: Failing tests:**
  - `rootindex`: `TestUpdateKeepsCRLF` — `CLAUDE.md` = `"# P\r\n\r\ntext\r\n"`; `Update(root, "CLAUDE.md", entries{{"svc","S"}})` ⇒ в файле нет `\n` без предшествующего `\r`; повторный `Update` с теми же entries → `changed == false`.
  - `rootindex`: `TestUpdateCRLFExistingSection` — CRLF-файл с секцией и пользовательским заголовком ⇒ после `Update` заголовок сохранён, все строки CRLF.
  - `rootindex` (Review Focus 2): `TestUpdateCRLFNoFinalNewline` — `"# P\r\ntext"` ⇒ результат начинается с `"# P\r\ntext\r\n\r\n<!-- auto-context:units -->\r\n"`; `TestUpdateLFSingleLine` — `"text"` ⇒ `"text\n\n<!-- auto-context:units -->\n"…`.
  - `frontmatter`: `TestSetHashKeepsCRLF` — `"---\r\nunit: old\r\n---\r\n# T\r\n"` и `"# T\r\ntext\r\n"` (без блока) ⇒ после `SetHash` в файле нет одиночного `\n`; `Read` возвращает те же meta; `TestSetHashAddsBlock` (LF) не меняется.
- [ ] **Step 2:** `go test ./internal/rootindex ./internal/frontmatter -count=1` → FAIL.
- [ ] **Step 3: Реализовать** по R6: в `Update` `eol := eolOf(cur)`, им же — разделители перед новой секцией; в `SetHash` — `eolOf` от исходных данных (наш блок, если был, иначе тело), строки YAML от `yaml.Marshal` переводятся заменой `"\n"` → `eol`.
- [ ] **Step 4:** `make test` → PASS.
- [ ] **Step 5: Доки:** `context/internal/rootindex.md`, `context/internal/frontmatter.md` — правило концов строк (одна строка). `go run ./cmd/auto-context ack internal/rootindex internal/frontmatter`.
- [ ] **Step 6: Commit** `fix: keep CRLF line endings in the root index and frontmatter`.

### Task 6: путь CLI Codex в кавычках; `permission-request` только для Codex

Находки: integration#2, quality#5, tests#7 (чистая функция пути).

**Files:**
- Modify: `internal/hooks/agent.go:48-64`, `cmd/auto-context/main.go:70-85` (`run`)
- Test: `internal/hooks/agent_test.go`, `internal/hooks/permission_request_test.go`, `internal/hooks/stop_test.go`, `cmd/auto-context/main_test.go`
- Docs: `context/internal/hooks.md`, `context/cmd/auto-context.md`

**Interfaces:**
- Produces: `func codexCLI(root string) string` — чистая функция по R8 (не читает окружение, не зависит от `GOOS`); `(Agent).CLI()` для Codex = `codexCLI(os.Getenv("CLAUDE_PLUGIN_ROOT"))`. `pluginRootSafe` остаётся как критерий «кавычки не нужны».

- [ ] **Step 1: Failing tests:**
  - `TestCodexCLI` (таблица, без `t.Setenv`): `""` → `auto-context`; `"rel/p"` → `auto-context`; `` `C:\Users\u\p` `` → `auto-context`; `"/home/u/p"` → `/home/u/p/bin/auto-context`; `"/c/Users/u/p"` → `/c/Users/u/p/bin/auto-context`; `"/home/my user/p"` → `'/home/my user/p/bin/auto-context'`; `"/home/o'neil/p"` → `'/home/o'\''neil/p/bin/auto-context'`; `"/home/ü/p"` → `'/home/ü/p/bin/auto-context'`; `"/home/u/p\n"`, `"/home/u\x7f"`, `"/home/\xff"` → `auto-context`.
  - `TestPermissionRequestQuotedRoot` (Review Focus 3): `CLAUDE_PLUGIN_ROOT` = `/home/o'neil/.codex/plugins/cache/m/auto-context/1.0.0`; `cli := codexCLI(root)`; Codex-env: `cli+" ack src/auth"` → JSON одобрения; `nil` для: `cli+" ack x; rm -rf /"`, `cli+" ack --exclude x"`, тот же путь без кавычек, путь в двойных кавычках, `cli+"  status"` (два пробела).
  - `TestStopReasonCodexQuotedCLI`: `t.Setenv("CLAUDE_PLUGIN_ROOT", "/home/my user/p")`; `StopReason(Feedback{Agent: Codex, Changed: []string{"svc"}})` содержит ``"`'/home/my user/p/bin/auto-context' ack svc`"``.
  - `cmd`: `TestHookPermissionRequestNeedsCodex` — активный `repo(t)`, stdin `{"tool_name":"Bash","tool_input":{"command":"auto-context status"}}`; аргументы `{"hook","permission-request"}` и `{"hook","permission-request","--agent","claude"}` → код 0, пустой stdout, `.auto-context/log` содержит `hook: bad arguments`; с `--agent codex` — JSON одобрения (как `TestHookPermissionRequest`).
- [ ] **Step 2:** `go test ./internal/hooks ./cmd/... -run 'TestCodexCLI|TestPermissionRequestQuotedRoot|TestStopReasonCodexQuotedCLI|TestHookPermissionRequestNeedsCodex' -count=1` → FAIL.
- [ ] **Step 3: Реализовать:** `codexCLI`: проверка `strings.HasPrefix(root, "/")`, `utf8.ValidString`, нет рун `< 0x20` и `0x7f`; `p := path.Join(root, "bin", "auto-context")`; `pluginRootSafe.MatchString(p)` → `p`, иначе `"'" + strings.ReplaceAll(p, "'", `'\''`) + "'"`. В `run` для `hook`: если `rest[0] == "permission-request"` и агент не `Codex` — `logHook(…, "hook: bad arguments")`, 0. Комментарий `CLI()` и `approvable` обновить (литеральный префикс, в том числе в кавычках).
- [ ] **Step 4:** `make test` → PASS (в т.ч. `TestPermissionRequestAbsolutePath`, `TestSessionFailureAgentCLI`, `TestHooksJSONContract`).
- [ ] **Step 5: Доки:** `context/internal/hooks.md` — `Agent.CLI` для Codex: путь из `CLAUDE_PLUGIN_ROOT`, при необычных символах в одинарных кавычках, иначе `auto-context`; `context/cmd/auto-context.md` — `permission-request` без `--agent codex` — ошибка аргументов. `go run ./cmd/auto-context ack internal/hooks cmd/auto-context`.
- [ ] **Step 6: Commit** `fix(hooks): quote the Codex CLI path; permission-request only for Codex`.

### Task 7: навыки Codex находят бинарник; README о Codex

Находки: integration#1, integration#3 (ограничение), integration#7.

**Files:**
- Modify: `skills/init/SKILL.md:7`, `skills/sync/SKILL.md:9`, `skills/status/SKILL.md:7`, `skills/auto-context-docs/SKILL.md:7`, `skills/README.md` (пункт про вызов `auto-context`), `README.md` («Требования», «Codex», «Если что-то не работает»)
- Test: `cmd/auto-context/main_test.go`
- Docs: `context/_root.md`

**Interfaces:**
- Produces: константа теста `codexLocateNote` — ровно абзац ниже; T16 на неё не опирается.

Абзац (заменяет нынешнюю строку «In Codex, `auto-context` is not on PATH: …» во всех четырёх навыках):

```
In Codex, `auto-context` is not on PATH. Run it exactly as a hook message wrote it (an absolute path, single-quoted if it has unusual characters). If no hook message gave the path, find it with `ls -td "${CODEX_HOME:-$HOME/.codex}"/plugins/cache/*/auto-context/*/bin/auto-context | head -n 1` and run that path; if nothing is found, ask the user.
```

- [ ] **Step 1: Failing test** `TestSkillsCodexLocate`: для каждого из `init`, `sync`, `status`, `auto-context-docs` файл `../../skills/<name>/SKILL.md` содержит `codexLocateNote` ровно один раз и не содержит `"by the path the hook gave"`.
- [ ] **Step 2:** `go test ./cmd/... -run TestSkillsCodexLocate -count=1` → FAIL.
- [ ] **Step 3: Правки навыков** — абзац выше. `skills/README.md`: «в Codex его в PATH нет: команды выполняются тем путём, который дал хук (в кавычках, если он их поставил), а без него навык находит бинарник командой `ls -td …` (абзац в каждом навыке; сверяет `TestSkillsCodexLocate`)».
- [ ] **Step 4: README:**
  - «Требования» и начало «Codex» — тексты ограничения integration#3 из таблицы.
  - «Codex», абзац про абсолютный путь: «В Codex `auto-context` не в PATH: хуки предлагают команды с абсолютным путём (`~/.codex/plugins/cache/<marketplace>/auto-context/<version>/bin/auto-context ack …`; путь с пробелами или другими необычными символами — в одинарных кавычках), и навыки выполняют их так же; если хук пути не дал, навык находит бинарник в кэше плагинов Codex сам.» (остаток абзаца без изменений).
  - «Если что-то не работает», после пункта про `/hooks`: «- В Codex хук `SessionEnd` при выходе из TUI не срабатывает (сессию держит фоновый app-server), поэтому файлы сессии в `.auto-context/state` остаются; SessionStart удаляет те, что старше 7 дней.»
- [ ] **Step 5:** `make test` → PASS; `npx --yes @anthropic-ai/claude-code@2.1.304 plugin validate .` → без ошибок.
- [ ] **Step 6: Доки:** `context/_root.md` — без изменений по содержанию, если в нём нет утверждений о пути в Codex; иначе привести к R7. `go run ./cmd/auto-context ack _root`.
- [ ] **Step 7: Commit** `fix(skills): find the Codex binary without a hook hint; README Codex notes`.

### Task 8: релиз, CI, `make test`, отслеживание упаковки Codex

Находки: integration#9, integration#10, integration#11, quality#4.

**Files:**
- Create: `.github/scripts/check-manifests.sh`
- Modify: `.github/workflows/release.yml:90-102,144`, `.github/workflows/ci.yml:55-68`, `Makefile`, `.auto-context/config.yaml` (`track`)
- Docs: `context/_root.md` (`.github/`, `Makefile`)

**Interfaces:**
- Produces: `sh .github/scripts/check-manifests.sh <dir> [version]` — выход 0, если в `<dir>`: `.claude-plugin/plugin.json` и `.codex-plugin/plugin.json` имеют `name == "auto-context"`, а `version` равна аргументу (без аргумента — поля `version` нет); `.codex-plugin/plugin.json` `.hooks == "./hooks/codex.json"`; `.agents/plugins/marketplace.json` `.plugins[0].name == "auto-context"`, `.plugins[0].source == {"source":"local","path":"./"}`; `hooks/codex.json` `.hooks | keys == ["PermissionRequest","SessionEnd","SessionStart","Stop","UserPromptSubmit"]`; `bin/auto-context` исполняемый. Иначе — выход 1 с именем проверки в stderr. Только `sh` и `jq -e`.

- [ ] **Step 1: Скрипт и его проверка:** написать скрипт; `sh .github/scripts/check-manifests.sh .` → 0; во временной копии дерева (`cp -r .claude-plugin .codex-plugin .agents hooks bin "$tmp/"`) подменить `.hooks` в `.codex-plugin/plugin.json` на `./hooks/hooks.json` → 1; с `jq '.version="1.2.3"'` в обоих манифестах `check-manifests.sh "$tmp" 1.2.3` → 0, а `… "$tmp"` без версии → 1.
- [ ] **Step 2: `ci.yml`:** шаг «Validate Codex manifests» → `sh .github/scripts/check-manifests.sh .`; в «Validate the shipped tree strictly» перед `validate` — `sh .github/scripts/check-manifests.sh "$tree" 0.0.0-ci` (скопировать `.github/scripts` не нужно: скрипт запускается из checkout).
- [ ] **Step 3: `release.yml`:**
  - «Archive and checksums»: `(cd libexec && sha256sum auto-context-*) > checksums.txt` и `sha256sum "auto-context-${GITHUB_REF_NAME}.tar.gz" >> checksums.txt`.
  - «Validate the tree users install»: после распаковки — `sh .github/scripts/check-manifests.sh "$RUNNER_TEMP/tree" "${GITHUB_REF_NAME#v}"` и `test "$(ls "$RUNNER_TEMP/tree/libexec" | wc -l)" -eq 6`.
  - «GitHub Release»: `gh release create … "auto-context-${GITHUB_REF_NAME}.tar.gz" checksums.txt libexec/* --generate-notes $prerelease`.
  - Проверка локально: во временном каталоге — шесть фиктивных `libexec/auto-context-*` и архив, те же две команды; скопировать все файлы плоско в другой каталог → `sha256sum -c checksums.txt` → все `OK`.
- [ ] **Step 4: `Makefile`:** цель `test` — `go vet ./...`, затем `go test ./...`. `make test` → PASS.
- [ ] **Step 5: `track`:** добавить `.codex-plugin/*`, `.agents/plugins/*`, `.github/scripts/*`. `go run ./cmd/auto-context status` → `_root stale` (хэш учёл новые файлы).
- [ ] **Step 6: Доки:** `context/_root.md`, пункт про `.github/workflows/`: обе workflow проверяют манифесты `.github/scripts/check-manifests.sh`; релиз прикладывает шесть бинарников и `checksums.txt` с плоскими именами; `make test` включает `go vet`. `go run ./cmd/auto-context ack _root`.
- [ ] **Step 7: Commit** `ci: manifest checks on the shipped tree, flat checksums, vet in make test`.

### Task 9: `internal/testutil` и чистое окружение тестов

Находки: tests#9, tests#11.

**Files:**
- Create: `internal/testutil/testutil.go`, `internal/testutil/testutil_test.go`, `internal/testutil/CLAUDE.md`, `context/internal/testutil.md`
- Create: `testmain_test.go` с `TestMain` в `cmd/auto-context`, `internal/hooks`, `internal/units`, `internal/gitx`, `internal/project`
- Modify: локальные `write` в `internal/{hashing,docstate,hooks,project,session}/*_test.go`, `cmd/auto-context/main_test.go:23`; git-обёртки в `cmd/auto-context/main_test.go` (строки ≈586-600, 625-640, 689-700, 748-760, 771-780, 857-866, 983-992), `internal/units/git_test.go:18-27`, `internal/project/ops_test.go:51`, `internal/hooks/session_problems_test.go:36-41,165-175`

**Interfaces:**
- Produces (пакет `testutil`, импортируется только из `_test.go`):
  - `func ScrubEnv()` — по R19.
  - `func Main(m *testing.M)` — `ScrubEnv(); os.Exit(m.Run())`.
  - `func WriteFile(t testing.TB, root, rel, content string)` — создаёт каталоги, `rel` через `/`.
  - `func RequireGit(t testing.TB)` — `t.Skip("git not installed")` без git.
  - `func Git(t testing.TB, dir string, args ...string) string` — `RequireGit`, `git -C dir args…`, при ошибке `t.Fatalf` с выводом; возвращает stdout.

- [ ] **Step 1: Failing test** `TestScrubEnv`: `t.Setenv("CLAUDE_PROJECT_DIR", "/x")`, `t.Setenv("GIT_DIR", "/nonexistent")`, `t.Setenv("CODEX_HOME", "/y")`; `ScrubEnv()` ⇒ `os.LookupEnv` для трёх — не найдено; `GIT_CONFIG_GLOBAL == os.DevNull`; `GIT_AUTHOR_NAME == "t"`. `TestGitCommits`: `Git(t, d, "init", "-q")`, `WriteFile`, `Git(…, "add", "-A")`, `Git(…, "commit", "-qm", "x")` без `-c user.*` — успешно.
- [ ] **Step 2:** `go test ./internal/testutil -count=1` → FAIL (пакета нет). Заодно зафиксировать проблему: `GIT_DIR=/nonexistent go test ./internal/units -count=1` → FAIL.
- [ ] **Step 3: Реализовать** пакет; `TestMain(m) { testutil.Main(m) }` в пяти пакетах; локальные `write` заменить на `testutil.WriteFile` (вариант `frontmatter_test.go:13` другой формы — остаётся); git-обёртки — на `testutil.Git` (дополнительные `-c protocol.file.allow=always` остаются аргументами вызова), `-c user.name=t -c user.email=t@t` убрать. Тела тестов и ожидания не меняются.
- [ ] **Step 4:** `make test` → PASS; `CLAUDE_PROJECT_DIR=/tmp GIT_DIR=/nonexistent make test` → PASS.
- [ ] **Step 5: Доки:** `internal/testutil/CLAUDE.md` (назначение: общие хелперы тестов и чистое окружение; файл `testutil.go`; `Details:`), `context/internal/testutil.md` (назначение, API, инвариант: только для `_test.go`; не трогает `HOME`). `go run ./cmd/auto-context ack internal/testutil _root cmd/auto-context internal/hooks internal/units internal/gitx internal/project internal/hashing internal/docstate internal/session` (новый юнит меняет оглавление; остальные — правки тестов).
- [ ] **Step 6: Commit** `test: shared testutil helpers and a scrubbed test environment`.

### Task 10: прямые тесты непокрытых путей

Находки: tests#1, #2, #3, #4, #8, #12, #13, #14. Только тесты; код не меняется.

**Files:**
- Test: `internal/project/project_test.go`, `cmd/auto-context/main_test.go`, `internal/fsutil/fsutil_test.go`, `internal/hashing/hashing_test.go:200`, `internal/hooks/permission_request_test.go`, `internal/hooks/pre_tool_use_test.go`, `internal/session/journal_test.go`, `internal/config/config_test.go`, `internal/units/units_test.go`, `internal/gitx/gitx_test.go`

**Interfaces:**
- Consumes: `testutil.WriteFile` (T9).
- Produces: `var allowedCLICalls = []string{…}` в `pre_tool_use_test.go` — список из `TestPreToolUseAllowsOwnCLI`, общий для двух тестов.

- [ ] **Step 1: `project` (tests#1):**
  - `TestOrphans`: юнит `svc` (`go.mod`), `context/svc.md`, `context/gone.md`, `context/_root.md` ⇒ `Orphans() == ["context/gone.md"]`.
  - `TestAckTouchedMixed`: `touched = ["svc","lib","nounit","big"]`: `svc` с доком; `lib` — юнит без дока; `nounit` — не юнит; `big` — юнит, `context/big.md` размером `frontmatter.MaxSize+1` ⇒ `acked == ["svc"]`, `failed` ровно с ключом `big` и `errors.Is(failed["big"], fsutil.ErrTooLarge)`, `indexErr == nil`, `CLAUDE.md` содержит `` `svc` ``.
  - `TestAckTouchedEmptyKeepsIndex`: `AckTouched(nil, now)` → корневой индексный файл не создан.
  - `TestUpToDateAfterSourceEdit`: после `Ack("svc")` `UpToDate("svc")`; правка `svc/main.go` (новый размер), `Open` заново ⇒ `!UpToDate("svc")`.
  - `TestWarnings`: `.gitignore` со строкой длиннее 512 символов ⇒ `Warnings()` непуст; чистый репо ⇒ `nil`.
- [ ] **Step 2: `cmd` (tests#2, tests#3):**
  - `TestChangesExitCodes`: `{"changes"}` и `{"changes","a","b"}` → 2 и `usage:` в stderr; `{"changes","nope"}` → 1, stderr непуст; `run([]string{"changes","svc"}, …, stdout=failWriter, …)` (writer всегда возвращает ошибку) в репо с коммитом → 1.
  - `TestHookGetwdFailureExits0` дополнить: явный `t.Setenv("CLAUDE_PROJECT_DIR", "")` для нынешних проверок; подтест с `CLAUDE_PROJECT_DIR` = активный `repo(t)`: `start({"hook","stop"}, stdin {"session_id":"g"}, …, fail)` → 0, stdout пуст, `session.SnapshotPath(root, "g")` существует (хук отработал в этом корне).
- [ ] **Step 3: `fsutil` (tests#4):** `TestReadDocLimit` (файл `max+1` → `ErrTooLarge`; ровно `max` → данные и разрешённый путь); `TestWriteDocKeepsPerm` (не Windows: `0o600` сохраняется; в файле новые данные); `TestWriteStateCreatesDir` (`StatePath(root,"state","x")` записан, в каталоге только `x`); `TestWriteAtomicLeavesNoTmp` (цель — непустой каталог: ошибка, а в родителе ничего, кроме этого каталога).
- [ ] **Step 4: `hashing` (tests#8, #12):** `hashing_test.go:200` — `if err := os.Chtimes(p, past, past); err != nil { t.Fatal(err) }`.
- [ ] **Step 5: `hooks` (tests#13):** вынести `allowedCLICalls`; `TestPermissionRequestAllows` проходит весь список ⇒ для каждого — ровно JSON одобрения `PermissionRequest`.
- [ ] **Step 6: мелкие хелперы (tests#14):** `TestMarkConfigNotifiedOnce` (первый вызов `true`, второй `false`, `Paused == true`; после `Resume` снова `true`); `TestTypeProblem` (`"line 3: cannot unmarshal !!str `x` into int"` → `"line 3: a value of the wrong type"`; `"cannot unmarshal"` → `"a value of the wrong type"`) и `TestDescribe` для `ErrBadIndexFile` и `os.ErrPermission`-ошибки (ожидаемые строки — из `Describe`); `TestWarningCountAndStat` (21+ предупреждение → `len(Warnings()) == 21` с `"more warnings omitted"`, `WarningCount()` = их полное число; `Stat` — `ok` для отслеживаемого, `!ok` для неизвестного); `TestBrief` (`errors.New("x")` → `failed`, `&Error{TimedOut: true}` → `timed out`, `&Error{NotRun: true}` → `could not be run`, `&Error{Code: 3}` → `exit 3`, `&Error{Code: -1}` → `killed`).
- [ ] **Step 7:** `make test` → PASS; `go test -cover ./internal/project` — выше нынешних 61.8%.
- [ ] **Step 8: Доки:** `_test.go` входят в хэш юнита (расширение `.go`), а назначение и API не менялись — только `go run ./cmd/auto-context ack internal/project cmd/auto-context internal/fsutil internal/hashing internal/hooks internal/session internal/config internal/units internal/gitx`.
- [ ] **Step 9: Commit** `test: direct tests for project, changes, fsutil writes and small helpers`.

### Task 11: golden-файлы, PATH на Windows, платформы шима

Находки: tests#6, tests#7 (PATHEXT), tests#15. Только тесты.

**Files:**
- Create: `internal/hooks/testdata/stop.golden.json`, `internal/hooks/testdata/stop_codex.golden.json`, `internal/hooks/testdata/session_start_codex.golden.json`
- Test: `internal/hooks/hooks_test.go`, `internal/hooks/stop_test.go`, `internal/hooks/pre_tool_use_test.go:129-191`, `internal/hooks/permission_request_test.go:32-49`, `cmd/auto-context/shim_test.go`

**Interfaces:**
- Produces: `func checkGolden(t *testing.T, name string, got []byte)` в `hooks_test.go` — при `-update` пишет файл; сравнивает с содержимым, у которого отрезаны конечные `\r\n` (golden пишутся без перевода строки). `TestSessionStartGolden` переходит на него. `func fakeCLI(t *testing.T, dir string) string` в `pre_tool_use_test.go` — пишет исполняемый `auto-context` (не Windows) или `auto-context.bat` (Windows), возвращает путь.

Golden-файлы (точные байты, без перевода строки в конце):
- `stop.golden.json`: `{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"auto-context: code changed in svc but docs not updated. If the change affects purpose, public API, structure or key behavior — update context/<unit>.md (and <unit>/CLAUDE.md if its purpose or key files changed) per skill auto-context:auto-context-docs. Otherwise run `auto-context ack svc`."}}`
- `stop_codex.golden.json` (`CLAUDE_PLUGIN_ROOT=/opt/p`): `{"decision":"block","reason":"auto-context: code changed in svc but docs not updated. If the change affects purpose, public API, structure or key behavior — update context/<unit>.md (and <unit>/CLAUDE.md if its purpose or key files changed) per skill $auto-context:auto-context-docs. Otherwise run `/opt/p/bin/auto-context ack svc`."}`
- `session_start_codex.golden.json`: `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"auto-context: docs stale for svc; missing for lib. Update them when working there (skill $auto-context:auto-context-docs) or run $auto-context:status."}}`

- [ ] **Step 1: Тесты-goldens:** `TestStopGolden` (`turn(t)`, правка `svc/main.go`, `stop`), `TestStopCodexGolden` (то же, `env.Agent = Codex`, `t.Setenv("CLAUDE_PLUGIN_ROOT", "/opt/p")`), `TestSessionStartCodexGolden` (сценарий `TestSessionStartGolden`, агент Codex). Файлы создать руками с текстом выше (не через `-update`), затем `go test ./internal/hooks -run Golden -count=1` → PASS. Если не совпадает — это регрессия одной из T1–T6, а не повод обновлять golden.
- [ ] **Step 2: PATHEXT:** в `TestPreToolUseRefusesShadowedBinary`, `TestPreToolUseRefusesRelativePath`, `TestPreToolUseRefusesWhenPluginShimMissing`, `TestPermissionRequestSilentWhenShadowed` фальшивый бинарь пишет `fakeCLI`; пропуск на Windows убрать. В `TestPreToolUseRefusesShadowedBinary` проверку «плагин первым в PATH — одобряется» на Windows пропустить внутри теста (`LookPath` не видит шим без расширения, это и есть поведение на Windows) с комментарием. На Linux: `go test ./internal/hooks -count=1` → PASS; Windows проверяет CI.
- [ ] **Step 3: Шим (tests#15):** `TestShimPlatformMapping` — каталог с фальшивым `uname` (`-s` → `$FAKE_S`, `-m` → `$FAKE_M`) первым в `PATH`; плагин с фальшивым бинарём под ожидаемым именем, печатающим `ran`; `shim(…, "version")`: (`Linux`,`x86_64`) → `libexec/auto-context-linux-amd64`; (`Linux`,`aarch64`) → `linux-arm64`; (`Darwin`,`arm64`) → `darwin-arm64`; (`MINGW64_NT-10.0`,`x86_64`) → `windows-amd64.exe` — во всех stdout `ran`; (`Linux`,`riscv64`) без бинаря → код 1, stderr содержит `binary for linux/unknown was not found`. Хелпер `plugin` получает параметр имени бинаря; пропуск по `GOARCH` для этого теста не нужен (остаётся требование POSIX sh: linux/darwin).
- [ ] **Step 4:** `make test` → PASS.
- [ ] **Step 5: Доки:** назначение и API не менялись — `go run ./cmd/auto-context ack internal/hooks cmd/auto-context`.
- [ ] **Step 6: Commit** `test: Stop and Codex goldens, PATHEXT fakes on Windows, shim platform table`.

### Task 12: рефакторинг `Stop` (без изменения поведения)

Находки: quality#8, quality#11, quality#12. Поведение и вывод не меняются; тесты не меняются, кроме вызовов `pendingUnits`.

**Files:**
- Modify: `internal/hooks/stop.go`
- Test: `internal/hooks/stop_test.go` (`TestPendingUnits` — новая сигнатура)
- Docs: `context/internal/hooks.md` (если упоминает внутренние функции)

**Interfaces:**
- Consumes: `turnNewUnits` (T1), `recordTurn(…, turnStart, …)` (T2).
- Produces:
  - `type pendingQuery struct { notified, failNotified, acked []string; failed map[string]string; isUnit, upToDate func(string) bool }`; `func pendingUnits(changed []string, q pendingQuery) []string`.
  - `func stopWithoutSnapshot(root, id string, wasActive bool, cur session.Snapshot) ([]byte, error)` — ветка `os.ErrNotExist` (нынешние строки 52-68).
  - `func withParents(tree *units.Tree, pending []string, before session.Snapshot, doc func(string) docstate.DocKind, q pendingQuery) []string` — нынешний блок родителей.
  - `func contextPaths(us []string) []string` — для `Refused`/`Unreadable` в `StopReason`.
  - `configPaused`: один `j.Save`; при ошибке — лог и `nil`; при `!first` — `nil`.

- [ ] **Step 1:** `go test ./internal/hooks -count=1 -v 2>&1 | grep -c '^--- PASS'` — записать число.
- [ ] **Step 2: Рефакторинг** по Interfaces; `Stop` после него — не длиннее ~50 строк.
- [ ] **Step 3:** `make test` → PASS; число `--- PASS` в `internal/hooks` то же; golden-тесты T11 проходят без `-update`.
- [ ] **Step 4: Доки:** если `context/internal/hooks.md` называет внутренние функции Stop — привести имена; иначе только `go run ./cmd/auto-context ack internal/hooks`.
- [ ] **Step 5: Commit** `refactor(hooks): split Stop into small steps`.

### Task 13: раскол `walk.go` и общая классификация файла (без изменения поведения)

Находки: quality#9, quality#10.

**Files:**
- Create: `internal/units/ignore.go`, `internal/units/gitfiles.go`
- Modify: `internal/units/walk.go`, `internal/units/units.go` (`SafeName`), `internal/units/CLAUDE.md`
- Docs: `context/internal/units.md`

**Interfaces:**
- Produces: `type fileClass struct{ ctxDoc, indexDoc, marker, source bool }`, `func (t *Tree) classifyFile(rel string) fileClass`, `func (c fileClass) any() bool`; `interesting(rel)` = `t.classifyFile(rel).any()`, `addFile` берёт флаги из `classifyFile` (`marker` и `source` уже исключают `ctxDoc`, `source` — и `indexDoc`, как сейчас). `compilePatterns`: `literal` — через `globMeta` (тот же набор символов).

- [ ] **Step 1:** записать число `--- PASS` в `go test ./internal/units -count=1 -v`.
- [ ] **Step 2: Перенос** по R18: в `ignore.go` — `patternSet`, `compilePatterns`, `match`, `gitIgnored`, `ignoreRule`, `ignoreFile`, `maxRuleLen`, `globMeta`, `parseIgnore`, `longestLiteral`, `(*ignoreFile).match`, лимиты `.gitignore`, `loadGitignore`; в `gitfiles.go` — `maxList`, `ErrGitTimeout`, `ErrListTooLarge`, `gitFiles`; `SafeName` — в `units.go`; остальное — в `walk.go`. Затем `classifyFile` и `globMeta` в `literal`; исправить опечатку «a index file» в комментарии `interesting`.
- [ ] **Step 3:** `make test` → PASS; число `--- PASS` то же; `go vet ./internal/units` чист.
- [ ] **Step 4: Доки:** `internal/units/CLAUDE.md` — ключевые файлы: `units.go`, `walk.go` (`Discover`, обход, `addFile`), `ignore.go` (разбор и сопоставление `.gitignore` и шаблонов конфига), `gitfiles.go` (список файлов от git); `context/internal/units.md` — раздел «Устройство» по файлам. `go run ./cmd/auto-context ack internal/units`.
- [ ] **Step 5: Commit** `refactor(units): split walk.go; one file classification`.

### Task 14: идиомы и лишняя логика (без изменения поведения)

Находки: quality#13, quality#16 (кроме R16).

**Files:**
- Modify: `internal/hooks/pre_tool_use.go:64`, `internal/logx/logx.go:133`, `internal/fsutil/fsutil.go:100,308`, `internal/frontmatter/frontmatter.go:114`, `internal/project/changes.go:75-83,143`, `internal/units/ignore.go` (`parseIgnore`, после T13), `internal/hashing/hashing.go:137,147`, `internal/session/session.go:99-103,116-125,272`, `internal/config/config.go:118-130,338`, `internal/project/project.go:22-59,206-220`, `internal/hooks/hooks.go:47-56`, `cmd/auto-context/main.go:87,272-315`, `internal/hooks/session_start.go:242-247`
- Test: `internal/units/units_test.go:532` (вызов `parseIgnore`)

**Interfaces:**
- Produces: `func parseIgnore(lines iter.Seq[string]) ignoreFile` (тест вызывает `parseIgnore(slices.Values([]string{…}))`); `func runAck(args []string, stderr io.Writer, cwd string) int`; `func markerPaths() []string` в `project` — `[config.FileName, units.ContextPath(units.RootUnit)]`, общий для `Active` и `ActivationRefused`.

- [ ] **Step 1:** записать число `--- PASS` в `go test ./... -count=1 -v`.
- [ ] **Step 2: Идиомы:** `strings.Split` в `range` → `strings.SplitSeq` во всех местах списка (`frontmatter.Summary` — с ранним выходом, как сейчас); `sort.Strings` → `slices.Sort`; разбор записи снимка — два `bytes.Cut` по `' '`.
- [ ] **Step 3: Лишняя логика:** в `config.Load` проверка `indexFileKeys(data) > 1` — одна, до `yaml.Unmarshal` (результат тот же для всех веток); скобки в `config.go:338`; в `LoadSnapshot` убрать ветку, переупаковывающую `ErrNotExist` (вызывающие используют `errors.Is`); `os.IsNotExist` → `errors.Is(err, os.ErrNotExist)` в `session.go:272`; `switch` в `changes.go:75-83` → `if/else`; `Statuses` вызывает `p.Prewarm(files)`; `Active`/`ActivationRefused` — через `markerPaths`; `load` возвращает `project.Open(env.Root)` напрямую; `runAck` без `stdout`, один `loadRepo` перед развилкой `--exclude`; `SessionFailure` — через `encodeJSON` (для Claude байты те же: в тексте нет `<`, `>`, `&`; ошибка кодирования невозможна, `data, _ :=` с комментарием).
- [ ] **Step 4:** `make test` → PASS; число `--- PASS` то же; `TestSessionStartGolden`, `TestSessionFailureIsValidJSON` и goldens T11 проходят.
- [ ] **Step 5: Доки:** изменения внутренние — `go run ./cmd/auto-context ack <затронутые юниты>` (`internal/hooks internal/logx internal/fsutil internal/frontmatter internal/project internal/units internal/hashing internal/session internal/config cmd/auto-context`).
- [ ] **Step 6: Commit** `refactor: modern Go idioms and simpler control flow`.

### Task 15: затенение и мёртвый код (без изменения поведения)

Находки: quality#14, quality#15 (кроме `Cache.Len`, R15).

**Files:**
- Modify: `internal/fsutil/fsutil.go:192-268`, `internal/textx/textx.go:22,31`, `internal/hooks/session_start.go:251`, `internal/session/session.go:219,244-246`, `internal/hooks/hooks.go:13-35`
- Test: `internal/hooks/stop_test.go:41-50,98-120` (`stopActive`)

- [ ] **Step 1: Затенение:** параметры `max` → `limit` (`ReadFile`, `ReadDoc`, `readRegular`, `Cap`, `Line`, `SessionNotice`); `writeAtomic(…, sync bool)` → `doSync`; вариадики `units` → `us` (`NotifyAckFailed`, `AddCode`, `AddDocs`, `Notify`).
- [ ] **Step 2: Мёртвый код:** удалить `Input.HookEventName`, `Input.Source`, `Input.StopHookActive`; `stopActive` в тестах заменить на `stop` (комментарий: Claude Code шлёт `stop_hook_active`, Stop его не читает); `Input.Command()` разбирает `command` в `string` (не строка → ошибка разбора → `""`, как сейчас; `TestHookToolInputArray` это пинует).
- [ ] **Step 3:** `make test` → PASS; `go vet ./...`; `grep -rn 'StopHookActive\|HookEventName' --include=*.go .` → пусто.
- [ ] **Step 4: Доки:** `context/internal/hooks.md` — если перечисляет поля `Input`, убрать удалённые. `go run ./cmd/auto-context ack internal/fsutil internal/textx internal/hooks internal/session`.
- [ ] **Step 5: Commit** `refactor: no shadowed builtins or packages; drop unused hook input fields`.

### Task 16: doc-комментарии и точность доков

Находки: quality#17, quality#1, quality#2 / integration#6, quality#3, quality#6, integration#5, ограничения core#4 и integration#8.

**Files:**
- Modify: `internal/hooks/hooks.go:1,13`, `internal/docstate/docstate.go:16,24`, `internal/units/units.go:26,32,83`, `internal/config/config.go:34,52`, `internal/rootindex/rootindex.go:22,26`
- Modify: `context/internal/textx.md:20`, `context/internal/units.md:38`, `hooks/README.md`, `bin/README.md:7`, `README.md` (таблица команд, «Если что-то не работает»), `context/_root.md`, `docs/superpowers/specs/2026-10-19-auto-context-design.md`, `docs/superpowers/specs/2026-10-20-codex-support-design.md:4`

- [ ] **Step 1: Doc-комментарии:** пакет `hooks` — «implements the Claude Code and Codex hook handlers»; `Input` — «the JSON payload the agent (Claude Code or Codex) passes to a hook on stdin»; комментарии к `State`, `UnitStatus`, `Unit`, `Kind`, `IsUnit`, `Config`, `Default`, `Entry`, `ErrBrokenMarkers` (одна строка каждый, что это, а не как). `go vet ./...` и `make test` → PASS.
- [ ] **Step 2: Связи юнитов:** `context/internal/textx.md` «Используется в:» — `cmd/auto-context`, `internal/logx`, `internal/project`, `internal/rootindex`; `context/internal/units.md` «Зависит от:» — `internal/config`, `internal/fsutil`, `internal/gitx`; внешняя `doublestar/v4`. Проверка: `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... ./cmd/...` совпадает со всеми «Зависит от» в `context/`.
- [ ] **Step 3: `hooks/README.md`:** первая строка — оба файла: `hooks.json` (Claude Code) и `codex.json` (Codex, на него указывает `.codex-plugin/plugin.json`; команды с `--agent codex`); после таблицы Claude — таблица Codex: `SessionStart` 60 с, `UserPromptSubmit` 30 с, `Stop` 60 с (`{"decision":"block","reason":…}`), `PermissionRequest` (`matcher: ^Bash$`) 10 с — авто-одобрение тех же вызовов CLI, `SessionEnd` 3 с (бюджет git 2 с; при выходе из TUI Codex его не вызывает). Пункты: у Codex нет `if`; `TestHooksJSONContract` сверяет оба файла с `hookHandlers`, `TestCodexHooksManifest` — `codex.json`.
- [ ] **Step 4: `bin/README.md`:** пункт про «Включённый проект» → текст integration#5: «- «Включённый проект» шим определяет, как `config.FindRoot` (без проверки владельца): от `CLAUDE_PROJECT_DIR` (или `.`) вверх до каталога с `.git` или `.auto-context/config.yaml`, не выше `$HOME`; проект включён, если там есть `.auto-context/config.yaml` или `context/_root.md`.»; следом — текст ограничения integration#8 из таблицы.
- [ ] **Step 5: README:** таблица команд CLI — строки `auto-context status --stale` («только устаревшие и недостающие») и `auto-context status --exit-code` («код 1, если есть stale/missing — для CI»); «Если что-то не работает» — текст ограничения core#4 из таблицы.
- [ ] **Step 6: `context/_root.md`:** текст ограничения core#4 после пункта про `cache.json`.
- [ ] **Step 7: Спецификации (R17):** в `2026-10-19-auto-context-design.md`: проверка активации — `project.Active(root)` и маркер `context/_root.md` (а не каталог `context/`); §8.3 п. 6 — «`pending` пуст → разрешить остановку; снимок и журнал остаются (другой Stop-хук может продолжить ход), их сбрасывает следующий `UserPromptSubmit`»; §8.4 — regex `^auto-context (ack|status|units|version|changes)( [A-Za-z0-9_./-]+)*$`, `ack --exclude` всегда спрашивает; §9 — строки `hook <session-start|prompt-submit|stop|pre-tool-use|session-end> [--agent codex]`, `hook permission-request --agent codex`, `status [--stale] [--exit-code]`, `changes <unit>`, `version`, `hook-exit <event> <code>` (внутренняя); значения по умолчанию `code_extensions` — ссылка «см. `config.Default()` и README, «Настройка»» вместо устаревшего списка. В `2026-10-20-codex-support-design.md:4` — «Статус: реализовано; дополняет `2026-10-19-auto-context-design.md`».
- [ ] **Step 8: Итог:** `go run ./cmd/auto-context ack internal/hooks internal/docstate internal/units internal/config internal/rootindex internal/textx _root`; `go run ./cmd/auto-context status` — все `ok`; `make test`, `go vet ./...`, `gofmt -l .` чисты.
- [ ] **Step 9: Commit** `docs: doc comments, Codex hooks README, accurate unit relations and specs`.
