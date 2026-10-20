---
unit: _root
source_hash: sha256:f70b00f96abbcde2a8a632cc47ee2d50dffb9924ae6a38af3a8c0324060779bd
updated: "2026-10-20"
---
# _root
Плагин Claude Code и Codex CLI на Go, который держит индексные файлы юнитов и библиотеку context/ в синхроне с кодом.

## Назначение
Документация проекта живёт в двух слоях: короткий индексный файл `<unit>/CLAUDE.md` (в Codex —
`AGENTS.md`, ключ `index_file`; оглавление, агент подгружает его лениво при работе с файлами каталога) и подробный `context/<unit>.md` (читается явно).
Плагин после каждого хода Claude замечает, что код юнита изменился, а документы нет, и просит
обновить их или подтвердить несущественность правки (`auto-context ack`). Текст документов пишет
сама сессия Claude по навыку `auto-context:auto-context-docs` (в Codex `$auto-context:auto-context-docs`); бинарник только следит и ведёт хэши.

## Устройство
- `cmd/auto-context` — единственный бинарник: подкоманды `hook <event> [--agent codex]`, `ack`, `status`, `changes`, `units`, `version`.
- `internal/hooks` — обработчики событий Claude Code и Codex: SessionStart (уведомления, оглавление корня,
  уборка state), UserPromptSubmit (снимок stat), Stop (diff снимка, auto-ack, feedback),
  PreToolUse в Claude и PermissionRequest в Codex (авто-разрешение простых вызовов `auto-context`), SessionEnd (удаление файлов сессии).
- `internal/project` — сервисный слой: открывает репозиторий (конфиг, дерево юнитов, кэш) и даёт
  хукам и CLI общие операции (статусы, ack, оглавление, сохранение кэша).
- Пакеты-кирпичи: `units` (список файлов через `git ls-files` или обход со своим разбором
  `.gitignore`, эвристика юнитов, классификация путей), `hashing` (`source_hash` с кэшем по
  mtime/size), `frontmatter`, `docstate` (ok/stale/missing, состояние context-дока, ack),
  `rootindex` (секция `auto-context:units` в корневом индексном файле), `session` (снимок и журнал хода),
  `config`, `gitx` (запуск git), `fsutil` (политика путей, чтение с лимитом, атомарная запись),
  `logx` (лог ошибок), `textx` (очистка текста из репозитория).
- Параллельные сессии в одном дереве делят `cache.json`: побеждает последняя запись, кэш
  восстановим. Файлы хода у каждой сессии свои.
- Плагин: `.claude-plugin/plugin.json` и `hooks/hooks.json` (Claude Code), `.codex-plugin/plugin.json` и `hooks/codex.json` (Codex; команды с `--agent codex`), `.agents/plugins/marketplace.json` (маркетплейс Codex: источник `local`, `./`, потому что Codex молча игнорирует github-источник из `.claude-plugin/marketplace.json`), общие навыки в `skills/`, POSIX sh-shim
  `bin/auto-context`, выбирающий бинарник `libexec/auto-context-<os>-<arch>` (в main бинарников нет).
- `.github/workflows/`: `ci.yml` (тесты на Linux/macOS/Windows, `claude plugin validate`),
  `release.yml`; обе проверяют манифесты Claude и Codex скриптом `.github/scripts/check-manifests.sh` (в релизе — на собранном дереве, с версией тега), и релиз кладёт `.codex-plugin`, `.agents` и `README.ru.md` в дерево. По тегу `vX.Y.Z[-pre]` с коммита из main: 6 бинарников, provenance, строгая
  проверка дерева плагина, ветка `release` с файлами плагина — новый коммит поверх прежнего, без
  истории main и без force-push, только к более высокой версии; затем GitHub Release; теги с `-`
  ветку не трогают). Релиз прикладывает шесть бинарников и `checksums.txt` с плоскими именами (`sha256sum -c` работает в каталоге скачанных файлов).

## Инварианты и подводные камни
- Хуки всегда завершаются с кодом 0 (шим гасит даже фатальный выход Go с кодом 2, который Claude Code
  считает блокирующим); ошибки — в `.auto-context/log`, а то, что пользователь должен исправить
  (конфиг, маркеры, сломанный `.auto-context`), — в `systemMessage`/`additionalContext` SessionStart.
- Хуки активны лишь при `.auto-context/config.yaml` или `context/_root.md` в корне; иначе ничего не пишут.
- Все вызовы git одного хука укладываются в его таймаут из `hooks/hooks.json` (`hooks/codex.json`) (`gitx.SetDeadline`).
- `.auto-context/` — каталог плагина: коммитится только `config.yaml` (и `.gitignore`, который плагин
  ведёт сам), остальное — локальное состояние.
- Модель угроз: проект может быть клонированным враждебным репозиторием, а хуки срабатывают сами.
  Поэтому служебные и документные пути идут через `fsutil.Resolve` (ничего вне репозитория и в
  `.git/`), чтения ограничены по размеру, git — через `gitx`, имена юнитов и summary очищаются,
  конфиг валидируется, текст из репозитория в контекст Claude не цитируется.
- Служебные файлы и правки доков пишутся атомарно (`fsutil.WriteAtomic`), кроме лога — он дописывается; пути внутри — относительные, через `/`.
- Правка индексного файла не считается обновлением доков; правка `context/<unit>.md` засчитывается, только
  если она не старше последней правки кода юнита в ходе; юнит, чьи доки и так совпадают с кодом,
  напоминания не получает. Файл кода с mtime старше начала хода (перенесённый `mv`/`cp -p`) auto-ack
  юнита в этом ходе запрещает.
- Версия плагина не хранится в main: её проставляет `release.yml` из тега. Иначе пользователи
  не получают обновлений.
- Спецификация и план: `docs/superpowers/specs/`, `docs/superpowers/plans/` — документы проектирования;
  где они расходятся с кодом, прав код и эти доки.

## Связи
Модуль Go `auto-context`; зависимости: `gopkg.in/yaml.v3`, `github.com/bmatcuk/doublestar/v4`; во
время работы — `git` (необязательно). Сборка — `Makefile`
(`make test` — `go vet ./...` и `go test ./...`; `make dist`).
