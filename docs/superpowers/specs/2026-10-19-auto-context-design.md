# auto-context — спецификация плагина Claude Code

Дата: 2026-10-19
Статус: реализовано; при расхождении верен код и `context/`

## 1. Цель

Плагин для Claude Code (бинарник на Go), который поддерживает документацию проекта
синхронной с кодом и экономит токены:

- в каталогах-юнитах лежат короткие `CLAUDE.md` — только оглавление и навигация;
- подробные описания живут в `context/` в корне проекта (библиотека знаний, зеркалит дерево кода);
- после изменений кода плагин указывает Claude, какие документы, возможно, устарели;
  Claude решает, существенна ли правка, и либо обновляет документы, либо подтверждает (`ack`).

Экономия достигается тем, что Claude Code подгружает вложенный `CLAUDE.md` лениво — только при
работе с файлами этого каталога, а `context/<unit>.md` читается Claude явно и только при необходимости.

### Критерии успеха

- `CLAUDE.md` юнита ≤ 15 строк, корневой — компактный, без подробностей.
- Любое изменение кода юнита за ход Claude — через Edit/Write, Bash (`sed`, `mv`, `rm`, кодогенераторы)
  или параллельно в редакторе — в том же ходе приводит либо к обновлению документов, либо к явному `ack`.
- Расхождения, возникшие между сессиями (ручные правки, `git pull`), обнаруживаются при старте сессии
  или по `/auto-context:sync`.
- Если документация актуальна, плагин не добавляет в контекст ни одного токена.
- Плагин никогда не ломает сессию: ошибки не всплывают в Claude.

## 2. Принятые решения

| Вопрос | Решение |
|---|---|
| Кто пишет текст документов | Текущая сессия Claude по сигналу хука (без внешнего API) |
| Структура `context/` | Зеркало дерева кода: `src/auth/` → `context/src/auth.md` |
| Когда каталогу нужны документы | Эвристика (граница модуля / ≥N файлов кода) + `include`/`exclude` в `.auto-context/config.yaml` |
| Обнаружение устаревания | Снимок `stat` файлов на `UserPromptSubmit` и сравнение на `Stop` (ловит любые правки за ход) + `source_hash` во frontmatter, сверка на `SessionStart` и `/auto-context:sync` |
| Поставка бинарника | Предсобранные бинарники в `bin/` плагина + shim, выбирающий платформу |

## 3. Термины

- **Юнит** — каталог, которому положена своя документация. Корень репозитория — всегда юнит `_root`.
- **Документы юнита** — `<unit>/CLAUDE.md` (оглавление) и `context/<unit>.md` (подробно).
  Для `_root`: `CLAUDE.md` и `context/_root.md`.
- **Файлы юнита** — файлы кода внутри каталога юнита, исключая вложенные юниты, `exclude` и `.gitignore`.
- **`source_hash`** — отпечаток файлов юнита на момент последней синхронизации документов.

## 4. Структура репозитория пользователя

```
repo/
├── CLAUDE.md                 # корень: правила проекта + генерируемое оглавление юнитов
├── .auto-context/config.yaml            # необязательный конфиг
├── .auto-context/                # служебное состояние (скрыто собственным .auto-context/.gitignore)
│   ├── state/<session_id>.json       # журнал хода
│   ├── state/<session_id>.snapshot   # снимок stat файлов на начало хода
│   ├── cache.json
│   └── log
├── context/
│   ├── _root.md
│   ├── src/auth.md
│   └── cmd/api.md
├── src/auth/CLAUDE.md
└── cmd/api/CLAUDE.md
```

## 5. Структура плагина

```
auto-context/
├── .claude-plugin/plugin.json
├── hooks/hooks.json
├── skills/
│   ├── auto-context-docs/SKILL.md   # навык форматов (вызывается моделью)
│   ├── init/SKILL.md                # /auto-context:init
│   ├── sync/SKILL.md                # /auto-context:sync
│   └── status/SKILL.md              # /auto-context:status (вызывается и моделью)
├── bin/
│   └── auto-context          # shim (POSIX sh): выбирает бинарник по uname -s / uname -m; bin/ попадает в PATH Bash-инструмента
├── libexec/
│   ├── auto-context-linux-amd64
│   ├── auto-context-linux-arm64
│   ├── auto-context-darwin-amd64
│   ├── auto-context-darwin-arm64
│   ├── auto-context-windows-amd64.exe
│   └── auto-context-windows-arm64.exe
├── .github/workflows/
│   ├── ci.yml                    # тесты на ubuntu / macos / windows
│   └── release.yml               # по тегу v*: сборка 6 бинарников, GitHub Release, ветка release
├── cmd/auto-context/             # main
├── internal/...
└── Makefile                      # make dist — кросс-сборка в libexec/ (локально)
```

`hooks/hooks.json`:

```json
{
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}\"/bin/auto-context hook session-start" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}\"/bin/auto-context hook prompt-submit" }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}\"/bin/auto-context hook stop" }] }],
    "PreToolUse": [{ "matcher": "Bash", "hooks": [{ "type": "command", "if": "Bash(auto-context *)", "command": "\"${CLAUDE_PLUGIN_ROOT}\"/bin/auto-context hook pre-tool-use" }] }]
  }
}
```

**Windows.** Claude Code на Windows выполняет хуки и Bash-инструмент через Git Bash, поэтому тот же
sh-shim работает: `uname -s` вида `MINGW64_NT-*` / `MSYS_NT-*` / `CYGWIN_NT-*` → `windows`, к имени добавляется `.exe`.
Внутри бинарника все пути приводятся к `/` (`filepath.ToSlash`) до сравнения и записи.
Поэтому на Windows обязателен Git for Windows: без Git Bash Claude Code запускает хуки через PowerShell, shim не
выполняется и плагин молча бездействует (путь задаётся через `CLAUDE_CODE_GIT_BASH_PATH` в `env` settings.json).

**Релизы.** Бинарники в `libexec/` в основной ветке не хранятся. Workflow `release.yml` по тегу `v*`:
прогоняет тесты, собирает 6 бинарников (linux/darwin/windows × amd64/arm64, `CGO_ENABLED=0`, `-trimpath`,
`-ldflags "-s -w -X main.version=<tag>"`), публикует GitHub Release с архивом плагина и `checksums.txt`,
и коммитит содержимое тега вместе с `libexec/` в ветку `release`. Маркетплейс указывает на ветку `release`,
поэтому установленный плагин содержит готовые бинарники.

**Версия плагина.** В `.claude-plugin/plugin.json` основной ветки поля `version` нет, в записи маркетплейса —
тоже: Claude Code не доставляет обновления, пока `version` не меняется. Workflow `release.yml` проставляет
`version` из тега (`v1.2.3` → `1.2.3`, через `jq`) перед сборкой архива и перед коммитом в ветку `release`.
Вручную версию не редактировать.

Каталог `bin/` плагина Claude Code добавляет в `PATH` Bash-инструмента, поэтому Claude вызывает
`auto-context ack …` и `auto-context status` без пути. Команды оформлены как навыки (`skills/<name>/SKILL.md`),
каталог `commands/` не используется.

Корень репозитория определяется как `cwd` из входа хука, поднимаясь вверх до каталога с `.git`
или `.auto-context/config.yaml`; если не найден — используется `cwd`.

## 6. Определение юнитов

Каталог — юнит, если выполняется хотя бы одно условие и он не попадает под `exclude`/`.gitignore`:

1. содержит файл из `module_markers` (граница модуля);
2. непосредственно содержит ≥ `min_files` файлов с расширением из `code_extensions`;
3. совпадает с glob из `include`.

Корень — всегда юнит `_root`. Каталоги `context/` и `.auto-context/` никогда не бывают юнитами
(это зашито в код, а не в `exclude`).

**Семантика `exclude`** (glob, синтаксис doublestar, пути относительно корня, разделитель `/`):
- файл не отслеживается, если его путь совпадает с любым шаблоном;
- каталог не может быть юнитом, если его путь совпадает с любым шаблоном
  (`ack --exclude internal/billing` добавляет ровно `internal/billing`: каталог перестаёт быть юнитом,
  его файлы относятся к родительскому юниту и продолжают отслеживаться);
- шаблон вида `X/**` дополнительно отключает обход каталога `X` целиком.

**Файл → юнит:** ближайший предок-юнит (включая собственный каталог файла); иначе `_root`.

**Отслеживаемые файлы** — файлы с расширением из `code_extensions`, все `CLAUDE.md` и всё внутри `context/`,
не попадающие под `exclude`/`.gitignore`.

**Классификация изменённого пути:**
- `<unit>/CLAUDE.md` или `context/<unit>.md` → правка документов юнита (`docs_touched`);
- файлы внутри `context/` и `.auto-context/`, прочие `.md` → игнорируются;
- файл с расширением из `code_extensions` → правка кода юнита (`code_changed`);
- остальное → игнорируется.

## 7. Хэширование

`source_hash = "sha256:" + hex(sha256(конкатенация по отсортированным путям: relpath + "\0" + sha256(content) + "\n"))`
по файлам юнита (п. 3). Кэш `.auto-context/cache.json`: `path → {mtime, size, sha256}`; файл перечитывается,
только если изменились mtime или size.

## 8. Хуки

Общие правила: вход — JSON на stdin (`session_id`, `cwd`, `hook_event_name`, …), выход — stdout.
**Код возврата всегда 0** (в том числе при неверном числе аргументов `hook` и ошибке определения
текущего каталога). Любая внутренняя ошибка пишется в `.auto-context/log` (ротация по 1 МБ),
вывод при этом пустой.

**Активация.** Плагин устанавливается глобально, поэтому хуки работают только в проектах, где он включён:
в корне есть `.auto-context/config.yaml` или каталог `context/`. Иначе `SessionStart`, `UserPromptSubmit` и `Stop` сразу завершаются с пустым выводом
и ничего не пишут (ни `.auto-context/`, ни `.gitignore`, ни корневой `CLAUDE.md`, ни лог). Проверка одна —
`hooks.Active(root)` в начале каждого из этих обработчиков. `PreToolUse` (п. 8.4) и команды CLI не ограничиваются; `/auto-context:init`
создаёт `context/` и тем самым включает хуки.

### 8.1 SessionStart (`source`: startup | resume | clear | compact)

1. Найти юниты, посчитать `source_hash` каждого.
2. Классифицировать: `ok` (хэш совпадает), `stale` (не совпадает / битый frontmatter),
   `missing` (нет `context/<unit>.md`).
3. Регенерировать оглавление в корневом `CLAUDE.md` (п. 10), если оно изменилось. Если файл переписан и у сессии
   есть снимок хода (SessionStart с `source: compact` посреди хода), обновить в снимке запись `CLAUDE.md`,
   чтобы `Stop` не принял собственную перезапись оглавления за правку документов `_root`.
4. Если есть `stale` или `missing`, вывести (не более `max_session_notice` юнитов, остальные — «и ещё K»):

```json
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"auto-context: docs stale for src/auth, cmd/api; missing for internal/billing. Update them when working there (skill auto-context:auto-context-docs) or run /auto-context:status."}}
```

Иначе — пустой вывод.

### 8.2 UserPromptSubmit — начало хода

1. Обойти дерево и сделать снимок отслеживаемых файлов (п. 6): `relpath → {mtime_ns, size}`. Только `stat`,
   содержимое не читается. Записать в `.auto-context/state/<session_id>.snapshot`.
2. Если журнал хода не пуст (предыдущий ход завершился без `Stop`, например прерыванием) — очистить его.
3. Вывод пустой.

Формат журнала `.auto-context/state/<session_id>.json`:

```json
{ "code_changed": ["src/auth"], "docs_touched": [], "already_notified": [] }
```

Снимок и журнал пишутся атомарно (временный файл + rename).

### 8.3 Stop

1. `stop_hook_active` **не учитывается**: алгоритм всегда выполняется целиком. Claude Code ставит этот флаг на
   каждом `Stop`, следующем за обратной связью хука, а это и есть основной путь (feedback → Claude правит
   `context/<unit>.md` → `Stop`): ранний выход не дал бы записать правку документов и сделать auto-ack.
   Зацикливания нет: `already_notified` гарантирует не более одного сообщения на юнит за ход
   (плюс собственные защиты Claude Code: `stop_hook_active` и лимит продолжений подряд).
2. **Сравнение со снимком.** Если снимка нет (плагин включён посреди сессии) — сделать его и разрешить остановку.
   Иначе снова обойти дерево (только `stat`) и сравнить со снимком начала хода:
   - `mtime_ns` или `size` отличается → изменён;
   - есть в снимке, нет на диске → удалён (в т.ч. переименован);
   - есть на диске, нет в снимке → добавлен.

   Классифицировать изменённые пути (п. 6) и добавить юниты в `code_changed` / `docs_touched` журнала.
   Конфиг перечитывается на каждом `Stop`, поэтому `ack --exclude` действует сразу.
   Сравнение всегда идёт со снимком **начала хода**, поэтому правки, сделанные Claude после обратной связи
   (п. 5), попадают в тот же журнал.
3. **Auto-ack:** для каждого юнита из `docs_touched` пересчитать `source_hash` и записать во frontmatter
   `context/<unit>.md` (обновить `updated`). Если `docs_touched` не пуст — регенерировать оглавление корня.
4. `pending = code_changed − docs_touched − already_notified`, где из `docs_touched` вычитаются только юниты,
   у которых `context/<unit>.md` сейчас существует (удаление документа — не обновление); дополнительно
   `new_units` — юниты из `pending`, у которых нет `context/<unit>.md`.
5. `pending` не пуст → вывести и перенести `pending` в `already_notified`:

```json
{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"auto-context: code changed in src/auth, cmd/api but docs not updated. If the change affects purpose, public API, structure or key behavior — update <unit>/CLAUDE.md and context/<unit>.md per skill auto-context:auto-context-docs. Otherwise run `auto-context ack src/auth cmd/api`. New unit internal/billing: create its docs or run `auto-context ack --exclude internal/billing`."}}
```

   Используется `hookSpecificOutput.additionalContext`, а не `decision: "block"`: разговор так же продолжается,
   и Claude может выполнить подсказку, но в транскрипте это показывается как обратная связь хука, а не как
   ошибка хука. Защиты от зацикливания у Claude Code те же (`stop_hook_active`, лимит продолжений).

   Для нового юнита `_root` вместо подсказки `ack --exclude` выводится
   `New unit _root: create context/_root.md (and the root CLAUDE.md purpose line).` — корневой юнит исключить нельзя.

6. `pending` пуст → очистить журнал и удалить снимок; разрешить остановку. Новый снимок сделает
   следующий `UserPromptSubmit`.

Порядок шагов важен: auto-ack (шаг 3) сам пишет во frontmatter `context/<unit>.md`, но это правка документа,
а не кода, и на `pending` не влияет. Если auto-ack переписал оглавление корневого `CLAUDE.md`, запись `CLAUDE.md`
в снимке обновляется, чтобы следующий `Stop` не засчитал её как правку документов `_root`.

### 8.4 PreToolUse — авто-разрешение собственного CLI

Хук зарегистрирован с `matcher: "Bash"` и `if: "Bash(auto-context *)"` (п. 5), поэтому процесс запускается
только для Bash-вызовов, похожих на `auto-context …`. Фильтр `if` — best-effort, поэтому решение принимает бинарник.

1. Разрешать, только если `tool_name` — `Bash`, а вся команда `tool_input.command` как есть (без обрезки пробелов:
   bash не делит слова по Unicode-пробелам вроде NBSP) — один простой вызов, целиком совпадающий с
   `^auto-context (ack|status|units|version)( [A-Za-z0-9_./-]+)*$` (подходят `ack --exclude <unit>` и
   `status --stale`), и ни один аргумент не содержит `..`. Любые другие символы (`;`, `&`, `|`, `$`, обратные
   кавычки, `>`, `<`, кавычки, переводы строк, табы, двойные пробелы), префиксы (`sudo`, `VAR=…`, путь к
   бинарнику) и подкоманды (`hook`) — пустой вывод, обычный поток разрешений.
2. Вывод при разрешении:

```json
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"auto-context CLI"}}
```

   `allow` лишь снимает запрос подтверждения; правила `deny`/`ask` пользователя Claude Code проверяет всё равно.
3. Активацией не ограничивается (разрешать собственный CLI безвредно), на диск ничего не пишет.
   Неверный вход — пустой вывод, код 0.

## 9. CLI

| Команда | Действие |
|---|---|
| `auto-context hook <session-start\|prompt-submit\|stop\|pre-tool-use>` | обработчики хуков |
| `auto-context ack <unit…>` | правка несущественна: записать текущий `source_hash` во frontmatter `context/<unit>.md` (если в файле нет frontmatter — добавить его; если самого файла нет — ошибка с подсказкой создать документы или `ack --exclude`) |
| `auto-context ack --exclude <unit>` | добавить юнит в `exclude` в `.auto-context/config.yaml` (создать файл при отсутствии); для `_root` (или `.`) — ошибка `the root unit cannot be excluded; create context/_root.md instead`, код 1, конфиг не меняется |
| `auto-context status [--stale]` | таблица юнитов: unit / state (ok, stale, missing) / путь к документам; `--stale` — только stale и missing |
| `auto-context units` | список юнитов по эвристике (для `/auto-context:init`) |

Аргумент `<unit>` нормализуется: `/` вместо разделителя ОС, `path.Clean` (`./svc`, `svc/`, `svc//` → `svc`),
`.` → `_root`.

Отдельная связь `ack` с журналом не нужна: `ack` меняет `context/<unit>.md`, и `Stop` видит это через
сравнение со снимком как `docs_touched`; `ack --exclude` меняет конфиг, и юнит перестаёт быть юнитом.

## 10. Форматы документов

### 10.1 `<unit>/CLAUDE.md` (≤ 15 строк)

```markdown
# src/auth
JWT-аутентификация и сессии HTTP API.

- `jwt.go` — выпуск/проверка токенов
- `middleware.go` — HTTP middleware, кладёт User в context
- `store/` — хранилище refresh-токенов (Redis)

Подробно: context/src/auth.md
```

Запрещено: `@`-импорты, блоки кода, описание внутренней логики.

### 10.2 `context/<unit>.md` (≤ ~150 строк)

```markdown
---
unit: src/auth
source_hash: sha256:3f9a…
updated: 2026-10-19
---
# src/auth
<одна строка — она же попадает в оглавление корня>

## Назначение
## Публичный API / точки входа
## Устройство и потоки данных
## Инварианты и подводные камни
## Связи (зависит от / используется в)
```

Frontmatter пишет только бинарник. Пустые разделы опускаются. Писать то, что не видно из кода быстро:
зачем, инварианты, подводные камни; не пересказывать функции.

### 10.3 Оглавление в корневом `CLAUDE.md`

Бинарник поддерживает секцию между маркерами; остальной текст корня не трогает. Если маркеров нет —
секция добавляется в конец файла (файл создаётся при отсутствии).

```markdown
<!-- auto-context:units -->
## Юниты (подробно — context/<unit>.md)
- `src/auth` — JWT-аутентификация и сессии HTTP API.
- `cmd/api` — HTTP-сервер, роутинг.
<!-- /auto-context:units -->
```

Строка описания берётся из первой непустой строки после заголовка `# ` в `context/<unit>.md`;
при отсутствии — пусто.

## 11. Навык `skills/auto-context-docs/SKILL.md`

40–60 строк, загружается Claude только при работе с документацией. Содержит:

- шаблоны 10.1 и 10.2 и лимиты строк;
- **критерий существенности**: менялись назначение, публичный API, структура файлов, ключевое поведение
  или инварианты → обновить документы; переименования локальных сущностей, рефакторинг без смены
  поведения, тесты, форматирование → `auto-context ack`;
- в `CLAUDE.md` юнита обновлять только строку назначения и список ключевых файлов;
- не трогать frontmatter и секцию `auto-context:units`;
- запрет `@`-импортов.

## 12. Команды

Все четыре навыка объявляют `allowed-tools: Bash(auto-context *)` — вызовы CLI в ходе навыка не требуют
подтверждения. Полный текст навыка подгружается только при вызове, поэтому `init` и `sync` первым шагом
явно вызывают навык форматов `auto-context:auto-context-docs` (имя навыка плагина — `<plugin>:<skill>`).
`init` и `sync` помечены `disable-model-invocation: true` (только по команде пользователя); `status` вызывается
и моделью — на него ссылается уведомление `SessionStart`.

- `/auto-context:init` — мастер первичной разметки:
  1. вызвать `auto-context units` и показать пользователю найденные юниты (путь, причина: модуль /
     N файлов / include, число файлов);
  2. спросить подтверждение; юниты, которые пользователь отклонил, добавить через `auto-context ack --exclude`,
     дополнительные — в `include` в `.auto-context/config.yaml`;
  3. только после подтверждения для каждого юнита без документов прочитать код и создать оба документа
     по навыку. Единственная дорогая операция, выполняется однократно.
- `/auto-context:sync` — проверка по требованию, не дожидаясь новой сессии: вывод
  `auto-context status --stale` подставляется в текст навыка при вызове (`` !`auto-context status --stale` ``; при обычных состояниях
  команда завершается с кодом 0, при настоящей ошибке вызов навыка прерывается с её stderr); для каждого stale/missing юнита решить по критерию существенности
  (п. 11) — обновить документы или `auto-context ack`. Покрывает правки между сессиями (`git pull`,
  переключение веток, ручные правки).
- `/auto-context:status` — вызвать `auto-context status` и кратко пересказать.

## 13. Конфиг `.auto-context/config.yaml`

Необязателен; значения по умолчанию:

```yaml
min_files: 5
code_extensions: [.go, .ts, .tsx, .js, .jsx, .py, .rs, .java, .kt, .rb, .php, .cs, .cpp, .c, .h]
module_markers: [go.mod, package.json, Cargo.toml, pyproject.toml, pom.xml, build.gradle]
include: []
exclude: [vendor/**, node_modules/**, "**/testdata/**", dist/**, build/**]
max_session_notice: 10
```

Битый YAML → используются значения по умолчанию, ошибка в лог.

## 14. Обработка ошибок

- Хуки: код возврата 0 всегда, ошибки → `.auto-context/log`.
- Битый frontmatter → юнит `stale`; `status` помечает это явно.
- Отсутствующий/битый журнал сессии → считается пустым.
- Атомарная запись всех служебных файлов и правок frontmatter/корневого `CLAUDE.md`.
- Если `.auto-context/` отсутствует — создаётся вместе с `.auto-context/.gitignore` (всё, кроме
  `config.yaml`, вне git); `.gitignore` пользователя плагин не меняет.

## 15. Устройство Go-кода

Зависимости: стандартная библиотека, `gopkg.in/yaml.v3`, `github.com/bmatcuk/doublestar/v4` (glob).
Актуальное устройство пакетов — в `context/_root.md` и `context/internal/*.md`; таблица ниже совпадает
с ним.

| Пакет | Ответственность |
|---|---|
| `cmd/auto-context` | разбор аргументов, вызов подкоманд |
| `internal/config` | загрузка и проверка `.auto-context/config.yaml`, поиск корня |
| `internal/units` | список файлов (git или обход со своим разбором `.gitignore`), эвристика, `файл → юнит`, классификация путей |
| `internal/gitx` | единый запуск git с фиксированной защитой |
| `internal/hashing` | `source_hash`, кэш по mtime/size |
| `internal/frontmatter` | чтение/запись YAML-frontmatter без изменения тела |
| `internal/docstate` | ok/stale/missing и ack |
| `internal/session` | снимок `stat` на начало хода и журнал Stop |
| `internal/rootindex` | генерация секции `auto-context:units` |
| `internal/project` | сервисный слой для хуков и CLI |
| `internal/fsutil` | политика доверия к путям, чтение с лимитом, атомарная запись |
| `internal/logx` | лог ошибок хуков |
| `internal/textx` | очистка текста из репозитория |
| `internal/hooks` | обработчики событий: `(input JSON, FS) → output JSON`, без глобального состояния |

## 16. Тестирование

- Табличные тесты: эвристика юнитов, классификация путей, хэширование, frontmatter — на `t.TempDir()`.
- Golden-тесты обработчиков хуков: фикстура репозитория + входной JSON → ожидаемый stdout.
- Табличные тесты `snapshot`: изменение содержимого, изменение только размера, удаление, переименование,
  новый файл, файл под `exclude`/`.gitignore`.
- Сквозной сценарий: `UserPromptSubmit` → правка `src/auth/jwt.go` → `Stop` (feedback) → правка
  `context/src/auth.md` → `Stop` (allow, `source_hash` обновлён) → `SessionStart` (пустой вывод).
- Сценарий Bash-правки: `UserPromptSubmit` → `os.Rename` / перезапись файла в обход Edit → `Stop` (feedback).
- Сценарий `ack`: `UserPromptSubmit` → правка кода → `Stop` (feedback) → `auto-context ack` → `Stop` (allow).
- Сценарий `ack --exclude`: новый юнит → `Stop` (feedback) → `ack --exclude` → `Stop` (allow).
- Сценарий без снимка: `Stop` без предшествующего `UserPromptSubmit` → allow, снимок создан.
- Бенчмарк обхода дерева на синтетическом репозитории из 50 000 файлов: цель — < 200 мс на `stat`-обход.
- Ручная проверка: `claude --plugin-dir .` на тестовом репозитории.

## 17. Вне рамок v1

- Фоновая генерация документов через API / `claude -p`.
- Тематические сквозные документы в `context/`.
- Монорепозитории с несколькими независимыми корнями.

## Поправки к разделам выше

Где разделы выше расходятся с этими пунктами, действуют пункты:

- Активация: `.auto-context/config.yaml` или файл `context/_root.md` (голый каталог `context/` не включает плагин).
- Обновлением доков юнита считается только правка `context/<unit>.md`, не старше последней правки кода
  юнита в ходе; правка `CLAUDE.md` не засчитывается, точечного обновления снимка (§8.1, §8.3) нет.
  Чистый Stop не удаляет снимок хода.
- Список файлов в git-репозитории берётся из `git ls-files -co --exclude-standard`; обход дерева —
  запасной путь. Значения `code_extensions` по умолчанию — как в README.
- Заголовок оглавления по умолчанию — `## Units (details: context/<unit>.md)`; существующий
  заголовок пользователя внутри маркеров сохраняется (§10.3).
- Политика путей (`fsutil.Resolve`): ничего вне репозитория и в `.git/`, симлинк документа — только
  на `.md`, чтения ограничены по размеру. `.auto-context/` скрывается собственным `.gitignore`.
- Конфиг валидируется; плохие настройки отбрасываются с сообщением, остальные применяются.
- PreToolUse не одобряет `ack --exclude` и подменённый бинарь; skill `sync` использует
  `auto-context changes` вместо git.
