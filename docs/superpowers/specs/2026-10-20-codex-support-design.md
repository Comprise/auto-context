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
каким именем виден навык плагина (`$sync` или с префиксом плагина) — от этого зависят тексты §6.

## 3. Принятые решения

- Один бинарник, явный флаг агента в команде хука: `auto-context hook --agent codex <event>`.
  Без флага — `claude`. Угадывание по stdin отвергнуто: общий `hooks/hooks.json` в Codex
  запускал бы `pre-tool-use` на каждый Bash и «одобрял» бы переписыванием.
- Имя индексного файла — настройка репозитория (`index_file` в конфиге), а не свойство
  агента: CLI вне хука агента не знает, и результат не должен зависеть от того, кто запустил.
- Отдельный файл хуков `hooks/codex.json`, на который указывает `.codex-plugin/plugin.json`;
  `hooks/hooks.json` остаётся для Claude Code без изменений.

## 4. Конфиг: `index_file`

- Ключ `index_file`, значения `CLAUDE.md` (по умолчанию) или `AGENTS.md`; другое значение —
  проблема конфига (как прочие невалидные ключи: игнорируется с сообщением в SessionStart).
- Хуки активны по-прежнему при `.auto-context/config.yaml` или `context/_root.md`; без конфига
  `index_file` = `CLAUDE.md`, поэтому Codex-репозиторию конфиг нужен (его создаёт `$init`).
- Имя берётся из конфига везде, где сейчас зашит `CLAUDE.md`:
  - `units`: `Classify` (`KindIndexDoc`), `interesting`/`addFile` (индексный файл делает каталог
    юнитом, как сейчас `CLAUDE.md`), `DocPaths` становится методом `Tree` (или получает имя);
    файл с другим именем — обычный файл, не док.
  - `rootindex.Check`/`Update` получают имя файла; секция `auto-context:units` пишется в
    корневой `AGENTS.md`.
  - `fsutil.ResolveDoc` не меняется (симлинки внутри репо уже поддержаны).
  - Тексты хуков (`stop.go`, `session_start.go`) и CLI (`main.go`) подставляют имя.
- Правило §8.3 основной спецификации («правка индексного файла не считается обновлением доков»)
  сохраняется для выбранного имени.

## 5. Хуки Codex

`hooks/codex.json` (команды через `"${CLAUDE_PLUGIN_ROOT}"/bin/auto-context`, шим без изменений):

| Событие | Команда | Таймаут | Отличия от Claude Code |
|---|---|---|---|
| SessionStart | `hook --agent codex session-start` | 60 | нет; `source: fork` трактуется как `startup` |
| UserPromptSubmit | `hook --agent codex prompt-submit` | 30 | нет |
| Stop | `hook --agent codex stop` | 60 | нет: `decision: block` + `reason` |
| PermissionRequest (`matcher: ^Bash$`) | `hook --agent codex permission-request` | 10 | новый обработчик |
| SessionEnd | `hook --agent codex session-end` | 3 | таймаут ≤ 3 с |

- `permission-request` использует ту же проверку, что `PreToolUse` (`allowedCommand`,
  `shadowed`), и отвечает `{"hookSpecificOutput":{"hookEventName":"PermissionRequest",
  "decision":{"behavior":"allow"}}}`; для чужих команд — пустой вывод. `PreToolUse` в Codex не
  регистрируется. `shadowed` сравнивает с `CLAUDE_PLUGIN_ROOT`, который Codex выставляет.
- Бюджет git (`hookBudget`) для `session-end` в Codex укладывается в 3 с.
- Шим: при отсутствии `CLAUDE_PROJECT_DIR` уже берёт `.` — это `cwd` сессии Codex. JSON-ответы
  шима (`systemMessage` + `additionalContext`) Codex понимает.
- Код: `hooks.Env` получает поле `Agent` (`claude` | `codex`); `main.go` разбирает `--agent`
  до имени события, неизвестный агент — ошибка в лог, выход 0, как прочие ошибки хука.

## 6. Тексты под агента

- Профиль агента — маленькая таблица в `internal/hooks`: ссылка на навык
  (`auto-context:auto-context-docs` → `$auto-context-docs`), команда синка
  (`/auto-context:sync` → `$sync`).
- Слово «Claude» в текстах хуков, которые видит модель или пользователь, заменяется на
  нейтральное («the agent»/без субъекта) для обоих агентов — так меньше ветвлений.
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

Без изменений: хуки всегда выходят с 0, ошибки — в `.auto-context/log`, проблемы конфига
(включая невалидный `index_file`) — в SessionStart. Модель угроз та же; `--agent` приходит из
манифеста плагина, не из репозитория.

## 9. Тестирование

- `config`: `index_file` — значение по умолчанию, оба допустимых, невалидное.
- `units`: таблицы `Classify`/`Discover`/`DocPaths` для обоих имён; `CLAUDE.md` в
  AGENTS-репозитории — не док и не делает юнит.
- `rootindex`: `Update`/`Check` на `AGENTS.md`.
- `hooks`: `permission-request` (одобряет те же команды, что `pre-tool-use`, молчит на
  остальные); тексты `stop`/`session-start` для обоих агентов и обоих имён (golden-строки);
  разбор `--agent` в `main.go`.
- `make test` на Linux/macOS/Windows в CI.
- Ручной прогон в Codex 0.163: установка из маркетплейса, доверие хукам, `$init`, ход с правкой
  кода → напоминание `Stop`, `auto-context ack` без запроса, `SessionEnd` чистит файлы сессии.
