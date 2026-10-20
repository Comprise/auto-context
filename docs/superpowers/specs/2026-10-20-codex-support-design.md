# auto-context — поддержка OpenAI Codex CLI

Дата: 2026-10-20
Статус: проект; дополняет `2026-10-19-auto-context-design.md`

## 1. Цель

Плагин работает и в Claude Code, и в Codex CLI. Репозиторий ведётся одним агентом: под Claude
Code — с `<unit>/CLAUDE.md`, под Codex — с `<unit>/AGENTS.md`. Ядро (юниты, хэши, состояние
доков, `context/`) общее; различаются имя индексного файла, протокол хуков и тексты.

### Критерии успеха

- Для Claude Code поведение, упаковка и тексты хуков не меняются, кроме нейтральных формулировок
  (§6); все существующие тесты проходят.
- В Codex 0.163+ после установки плагина и доверия хукам (`/hooks`) ход с правкой кода юнита
  заканчивается тем же напоминанием `Stop`, что в Claude Code, а `auto-context ack`/`status`
  выполняются без запроса разрешения.
- В репозитории с `index_file: AGENTS.md` плагин нигде не читает, не пишет и не упоминает
  `CLAUDE.md`.

### Вне рамок

- Оба агента в одном репозитории одновременно (`CLAUDE.md` и `AGENTS.md` сразу).
- Codex desktop и облачный Codex.
- Миграция существующих `CLAUDE.md` в `AGENTS.md`.

## 2. Факты о Codex (документация, проверено 2026-10-20)

- Хуки: `hooks/hooks.json` плагина или путь из поля `hooks` манифеста; схема та же
  (событие → группы с `matcher` → `{type: command, command, timeout}`), `matcher` — regex.
  Поля `if` нет. Хуки включены по умолчанию (`[features] hooks`), но запускаются только после
  ручного доверия пользователем (`/hooks`); доверие привязано к хэшу хука.
- События: `SessionStart` (`source`: startup|resume|clear|compact|fork), `UserPromptSubmit`,
  `PreToolUse`, `PermissionRequest`, `Stop`, `SessionEnd` и др. Общие поля stdin: `session_id`,
  `cwd`, `hook_event_name`, `transcript_path`, `model`; у `Stop` есть `stop_hook_active`.
  Shell-инструмент — `tool_name: "Bash"`, команда в `tool_input.command`.
- Ответы: `SessionStart`/`UserPromptSubmit` — `hookSpecificOutput.additionalContext`,
  `systemMessage`; `Stop` — `decision: "block"` + `reason` (продолжает ход); `PreToolUse` с
  `permissionDecision: "allow"` **переписывает** команду и требует `updatedInput` — для
  одобрения служит `PermissionRequest` с `hookSpecificOutput.decision.behavior: "allow"`.
  Выход 2 блокирует. `SessionEnd`: таймаут по умолчанию 1 с, максимум 3 с.
- Окружение хука: `PLUGIN_ROOT`, `PLUGIN_DATA` и для совместимости `CLAUDE_PLUGIN_ROOT`,
  `CLAUDE_PLUGIN_DATA`; рабочий каталог — `cwd` сессии. `CLAUDE_PROJECT_DIR` не выставляется.
- Плагин: манифест `plugin.json` в корне или `.codex-plugin/plugin.json`; навыки в
  `skills/<name>/SKILL.md` (frontmatter `name`, `description`), вызов — `$name`. Маркетплейс:
  `.agents/plugins/marketplace.json` или совместимый `.claude-plugin/marketplace.json`;
  источники `local`, `url`, `git-subdir`, `npm`. Установка: `codex plugin marketplace add
  owner/repo [--ref …]`, затем `/plugins`.
- Codex читает `AGENTS.md` по цепочке каталогов от корня проекта до `cwd`.

Не подтверждено документацией (проверяется в шаге 0 плана): читает ли Codex поле `hooks` в
`.codex-plugin/plugin.json`; принимает ли источник `github` из нашего `marketplace.json`;
попадает ли `bin/` плагина в PATH shell-инструмента; точный stdin `PermissionRequest`; под
каким именем виден навык плагина (`$auto-context:sync`; подтверждено шагом 0) — от этого зависят тексты §6.

## 3. Принятые решения

- Один бинарник, явный флаг агента после события: `auto-context hook <event> --agent codex`
  (шим читает событие из `$2`, поэтому флаг идёт последним). Без флага — `claude`. Угадывание по stdin отвергнуто: общий `hooks/hooks.json` в Codex
  запускал бы `pre-tool-use` на каждый Bash и «одобрял» бы переписыванием.
- Имя индексного файла — настройка репозитория (`index_file` в конфиге), а не свойство
  агента: CLI вне хука агента не знает, и результат не должен зависеть от того, кто запустил.
- Отдельный файл хуков `hooks/codex.json`, на который указывает `.codex-plugin/plugin.json`;
  `hooks/hooks.json` остаётся для Claude Code без изменений.

## 4. Конфиг: `index_file`

- Ключ `index_file`, значения `CLAUDE.md` (по умолчанию; также при пустом/`null`) или `AGENTS.md`.
  Другое значение делает конфиг сломанным (как ошибка разбора: трекинг на паузе, сообщение в
  SessionStart и `status`), а не откатывается на умолчание — иначе опечатка в Codex-репо создала бы
  корневой `CLAUDE.md` (`rootindex.Update` создаёт файл). При сломанном конфиге `status` не
  проверяет корневой индекс.
- Хуки активны по-прежнему при `.auto-context/config.yaml` или `context/_root.md`; без конфига
  `index_file` = `CLAUDE.md`, поэтому Codex-репозиторию конфиг нужен (его создаёт `$auto-context:init`).
- Имя берётся из конфига везде, где сейчас зашит `CLAUDE.md`:
  - `units`: `Classify` (`KindIndexDoc`), `interesting`/`addFile` (индексный файл отслеживается;
    юнитом каталог он не делает — ни сейчас, ни после), `DocPaths` заменяется на `ContextPath`;
    файл с другим именем — не док (`KindIgnore`).
  - `rootindex.Check`/`Update` получают имя файла; секция `auto-context:units` пишется в
    корневой `AGENTS.md`.
  - `fsutil.ResolveDoc` не меняется (симлинки внутри репо уже поддержаны).
  - Тексты хуков (`stop.go`, `session_start.go`, включая сообщения о корневом индексе) и CLI
    (`main.go`) подставляют имя.
- Правило §8.3 основной спецификации («правка индексного файла не считается обновлением доков»)
  сохраняется для выбранного имени.

## 5. Хуки Codex

`hooks/codex.json` (команды через `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context`, шим без изменений):

| Событие | Команда | Таймаут | Отличия от Claude Code |
|---|---|---|---|
| SessionStart | `hook session-start --agent codex` | 60 | нет (`source` хуки не читают) |
| UserPromptSubmit | `hook prompt-submit --agent codex` | 30 | нет |
| Stop | `hook stop --agent codex` | 60 | ответ `{"decision":"block","reason":…}` вместо `hookSpecificOutput.additionalContext` |
| PermissionRequest (`matcher: ^Bash$`) | `hook permission-request --agent codex` | 10 | новый обработчик |
| SessionEnd | `hook session-end --agent codex` | 3 | таймаут ≤ 3 с |

- `permission-request` использует ту же проверку, что `PreToolUse` (`allowedCommand`,
  `shadowed`), и отвечает `{"hookSpecificOutput":{"hookEventName":"PermissionRequest",
  "decision":{"behavior":"allow"}}}`; для чужих команд — пустой вывод. `PreToolUse` в Codex не
  регистрируется. `shadowed` сравнивает с `CLAUDE_PLUGIN_ROOT`, который Codex выставляет.
- Бюджет git зависит от события и агента (`session-end` в Codex — 2 с при таймауте 3 с).
- `tool_input` разбирается терпимо: команда берётся, только если это строка; иной формат —
  не ошибка разбора, а «не наша команда».
- `hooks/hooks.json` Codex загружать не должен (у него нет `if`, а `allow` в PreToolUse
  переписывает команду) — проверяется в шаге 0.
- Шим: при отсутствии `CLAUDE_PROJECT_DIR` уже берёт `.` — это `cwd` сессии Codex. JSON-ответы
  шима (`systemMessage` + `additionalContext`) Codex понимает.
- Код: `hooks.Env` получает поле `Agent` (`claude` | `codex`); `main.go` разбирает `--agent`
  после имени события, неизвестный агент — ошибка в лог, выход 0, как прочие ошибки хука.

## 6. Тексты под агента

- Профиль агента — маленькая таблица в `internal/hooks`: ссылка на навык
  (`auto-context:auto-context-docs` → `$auto-context-docs`), команда синка
  (`/auto-context:sync` → `$sync`).
- Строки хуков и CLI слова «Claude» не содержат (оно только в комментариях); меняются лишь
  имя индексного файла и ссылки на навыки/команды.
- Навыки: frontmatter Claude-only (`disable-model-invocation`, `allowed-tools`) остаётся —
  Codex незнакомые поля игнорирует (проверяется в шаге 0). В `init`, `sync`, `status`,
  `auto-context-docs` — короткие указания для Codex:
  - `!`-вставка в `sync` в Codex не работает: «если вывода выше нет — выполни
    `auto-context status --stale`»;
  - `init` в Codex записывает `index_file: AGENTS.md` и пишет `AGENTS.md` вместо `CLAUDE.md`;
  - навыки ссылаются на «индексный файл юнита (`index_file`, по умолчанию `CLAUDE.md`)».
- Если шаг 0 покажет, что `bin/` не в PATH, навыки и тексты хуков для Codex предлагают
  `"$PLUGIN_ROOT/bin/auto-context"`; тогда `allowedCommand` для Codex принимает и эту форму
  (строго: литерал префикса, остальные правила те же).

## 7. Упаковка и релиз

- `.codex-plugin/plugin.json`: `name`, `description`, `author`, `license`, `homepage`,
  `repository`, `keywords`, `hooks: "./hooks/codex.json"`; версию проставляет `release.yml`,
  как для `.claude-plugin/plugin.json`.
- Маркетплейс: если Codex принимает `.claude-plugin/marketplace.json` с источником `github` —
  ничего нового; иначе `.agents/plugins/marketplace.json` в ветке `release` с источником
  `local`/`./` (маркетплейс и плагин в одном корне).
- `release.yml`: строгая проверка дерева плагина знает новые файлы. `ci.yml`: проверка JSON
  `.codex-plugin/plugin.json` и `hooks/codex.json` (схема, что каждая команда существует в
  `hookHandlers`).
- README: раздел «Codex» — установка, доверие хукам в `/hooks`, `index_file: AGENTS.md`.

## 8. Ошибки

Одобрение в `PermissionRequest` может означать запуск вне sandbox; одобряется только та же строгая
форма команды, что в Claude Code. В режимах, где sandbox и так разрешает команду, запроса нет —
это нормально.

В остальном без изменений: хуки всегда выходят с 0, ошибки — в `.auto-context/log`, проблемы конфига
(включая невалидный `index_file`) — в SessionStart. Модель угроз та же; `--agent` приходит из
манифеста плагина, не из репозитория.

## 9. Тестирование

- `config`: `index_file` — значение по умолчанию, оба допустимых, невалидное.
- `units`: таблицы `Classify`/`Discover`/`ContextPath` для обоих имён; `CLAUDE.md` в
  AGENTS-репозитории — не док и не отслеживается.
- `rootindex`: `Update`/`Check` на `AGENTS.md`.
- `hooks`: `permission-request` (одобряет те же команды, что `pre-tool-use`, молчит на
  остальные); тексты `stop`/`session-start` для обоих агентов и обоих имён (golden-строки);
  разбор `--agent` в `main.go`.
- `make test` на Linux/macOS/Windows в CI.
- Ручной прогон в Codex 0.163: установка из маркетплейса, доверие хукам, `$auto-context:init`, ход с правкой
  кода → напоминание `Stop`, `auto-context ack` без запроса, `SessionEnd` чистит файлы сессии.

## Поправки по шагу 0 (2026-10-20)

Живая проверка в Codex CLI 0.163:

- Хуки из `.codex-plugin/plugin.json` → `hooks: "./hooks/codex.json"` загружаются; `hooks/hooks.json`
  Codex не читает. Доверие (`/hooks`) хранится в `~/.codex/config.toml`
  (`[hooks.state."<plugin>@<marketplace>:hooks/codex.json:<event>:0:0"] trusted_hash`); хуки
  работают только в сессии, начатой после доверия.
- `Stop` с `{"decision":"block","reason":…}` продолжает ход; при продолжении приходит
  `stop_hook_active: true`.
- stdin совпадает с документацией: общие `session_id`, `transcript_path`, `cwd`, `hook_event_name`,
  `model`, `permission_mode`; `turn_id` у UserPromptSubmit/Stop; `source: "startup"` у SessionStart.
- Окружение хука: `PLUGIN_ROOT`, `PLUGIN_DATA`, `CLAUDE_PLUGIN_ROOT`, `CLAUDE_PLUGIN_DATA`; рабочий
  каталог — cwd сессии.
- Shell-инструмент Codex: `bin/` плагина не в PATH, `PLUGIN_ROOT`/`CLAUDE_PLUGIN_ROOT` там пусты.
  Бинарник плагина по абсолютному пути в режиме по умолчанию запускается без запроса; `PermissionRequest`
  при этом не приходит.
- Вложенный `<unit>/AGENTS.md` Codex подгружает при работе с файлами юнита.
- Навык виден как `probe (ac-probe)`, вызывается `$probe`; Claude-only поля frontmatter не мешают.
- Ручной прогон 0.163: навыки плагина показываются и вызываются с префиксом плагина (`$auto-context:init`;
  внутреннее имя `auto-context:init`).
- `SessionEnd` при выходе из TUI не срабатывает (сессию держит фоновый app-server): ни в пробном
  плагине, ни в auto-context записи нет; файлы сессии в `.auto-context/state` остаются.

Следствия:

- Для Codex хуки предлагают команды с абсолютным путём `$CLAUDE_PLUGIN_ROOT/bin/auto-context`
  (`Agent.CLI()`); при пустой переменной или небезопасном пути остаётся `auto-context`.
- `allowedCommand`/`approvable` для Codex принимают и эту абсолютную форму (только путь из
  `CLAUDE_PLUGIN_ROOT`, прочие правила прежние); `shadowed()` для неё не нужен.
- `PermissionRequest` обычно не нужен; оставлен на режимы, где Codex спрашивает. Фикстура — по документации.
- Вложенный `AGENTS.md` работает как рассчитано; вызов `$name` подтверждён.
- Полные имена навыков: `$auto-context:init`, `$auto-context:sync`, `$auto-context:status`,
  `$auto-context:auto-context-docs` (`codexSkillPrefix = "$auto-context:"`).
- `SessionEnd` в Codex не надёжен: уборка старых файлов сессии держится на SessionStart; хук
  SessionEnd оставлен на случай, если Codex начнёт его вызывать.
- Маркетплейс: Codex принимает `.claude-plugin/marketplace.json`, но молча игнорирует запись плагина
  с `"source": "github"`. Поэтому в ветке `release` рядом лежит `.agents/plugins/marketplace.json`
  с источником `local`, `./`. Установка: `codex plugin marketplace add Comprise/auto-context --ref release`,
  идентификатор `auto-context@auto-context`. Проверяется.
