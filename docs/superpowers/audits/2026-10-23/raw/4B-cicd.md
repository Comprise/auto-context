# 4B. CI/CD и эксплуатационные практики

Коммит af0cae8. Проверено: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.github/scripts/check-manifests.sh`, `Makefile`, `go.mod`, `bin/auto-context`, `internal/logx`, README (установка, Troubleshooting, Security, Development), а также настройки репозитория через `gh api` (только чтение), ветка `origin/release` и список GitHub Releases.
actionlint, shellcheck и govulncheck локально не установлены, поэтому не запускались.

Ранее найденное не повторяю: `.gitattributes eol=lf`, npm-код в publish-задании с правами на запись, `concurrency: release` без ref, старый тег становится Latest (area-packaging), а также T-M5 (CI без -race/-shuffle/покрытия/fuzz) и D-M5 (нет CHANGELOG).

Что уже сделано хорошо: actions закреплены по SHA. У workflow минимальные права (`contents: read`), повышенные права есть только у publish. Используется `persist-credentials: false`. Сборка `-trimpath`, `CGO_ENABLED=0`, в build-задании `cache: false`. Перед сборкой выполняется `go mod verify`. Guard проверяет «тег по semver на коммите из main». Ветка release движется только вперёд, без force-push. Для бинарников есть provenance-аттестация. Шим не блокирует сессию при сбое бинарника. Лог ротируется при 1 MiB (`log` → `log.1`). Включены secret scanning и push protection. По умолчанию у workflow права read.

## Счётчики

| Severity | Кол-во |
|---|---|
| Critical | 0 |
| High | 1 |
| Medium | 7 |
| Low | 9 |

---

## High

### CD-H1. Ничто не защищает канал доставки: у main, release и тегов `v*` нет правил, у publish нет environment
**Факты (gh api, ПОДТВЕРЖДЕНО):**
- `branches/main/protection` и `branches/release/protection` возвращают 404 «Branch not protected».
- `rulesets` возвращает `[]`: правил для тегов нет.
- `environments` возвращает `total_count: 0`.
- `actions/permissions` возвращает `sha_pinning_required: false` и `allowed_actions: all`.
- Ветка release уже переписывалась вручную. В её истории `release v0.0.1…v0.0.3 → v1.0.0`, а GitHub Releases выпущены как v0.1.0-rc1…v0.3.0 (см. CD-M6). Сам workflow такой последовательности создать не может: он не пускает ветку на версию ниже текущей.

**Операционный риск.** Пользователи ставят плагин клонированием ветки `release`. Бинарники из `libexec/` запускаются автоматически на каждом хуке: SessionStart, UserPromptSubmit, Stop. Поэтому прямой push в `release` (скомпрометированный SSH-ключ или PAT владельца, ошибочный `git push -f`) доставит произвольный код всем пользователям с автообновлением. Такой push обходит CI, guard и provenance.
Guard «тег на коммите из main» тоже ничего не гарантирует: main не защищена, и в неё можно запушить что угодно напрямую. Тег `v*` может создать и удалить любой, у кого есть запись. У publish нет ручного подтверждения.

**Рекомендации.**
1. Ruleset на `refs/heads/release`:
   - запретить удаление и force-push;
   - разрешить обновление только GitHub Actions (bypass list: `github-actions`/app) или через environment.
2. Ruleset на `refs/heads/main`:
   - PR обязателен;
   - required status checks `test (ubuntu/macos/windows)` и `plugin`;
   - запретить force-push.
3. Ruleset на теги `refs/tags/v*`: запретить удаление и обновление (неизменяемые теги). Создавать теги могут только maintainers.
4. Перевести `publish` в `environment: release` с required reviewers (хотя бы сам владелец). Тогда подтверждать придётся вручную, и токен с `contents: write` не выдаётся без апрува.
5. Включить в настройках Actions «Require actions to be pinned to a full-length commit SHA». Ограничить `allowed_actions` списком GitHub-owned/verified.
6. Включить Immutable Releases (GitHub): опубликованные ассеты и тег нельзя будет подменить.
7. Добавить `CODEOWNERS` для `.github/`, `bin/`, `hooks/`, манифестов.

---

## Medium

### CD-M1. Нет сканирования безопасности и обновления зависимостей
**Факты.**
- Нет `.github/dependabot.yml`, renovate, CodeQL, govulncheck.
- В настройках репозитория `dependabot_security_updates: disabled`.
- Нет `schedule:`-запуска CI.

**Риск.**
- Бинарник статически включает stdlib Go. Он разбирает недоверенные данные: YAML, git-вывод, пути. Если CVE появится в stdlib 1.27.x или в `yaml.v3`/`doublestar`, об этом никто не узнает, и уязвимые бинарники останутся в ветке release.
- Закреплённые SHA actions (checkout v4.4.0, setup-go v5.6.0, attest v3.2.0, upload/download-artifact v4) и `@anthropic-ai/claude-code@2.1.304` устаревают без сигнала. Закрепление без бота обновлений означает отказ от исправлений.

**Рекомендации.**
- `dependabot.yml` с экосистемами `gomod` и `github-actions`, weekly. Сгруппировать actions в один PR.
- Задание `govulncheck ./...` (golang/govulncheck-action по SHA) в ci.yml и в `schedule: cron` еженедельно. Еженедельный запуск ловит новые CVE в уже выпущенной версии.
- Опционально CodeQL для Go (`github/codeql-action`, по SHA).
- Включить Dependabot alerts и security updates в настройках.

### CD-M2. Нет пути отката: ветка release движется только вперёд, коммиты release не помечены тегами, runbook нет
**Факты.**
- `release.yml:118-124` пускает ветку только на большую версию.
- На коммитах `release` нет тегов: тег `v1.0.0` указывает на коммит main `af0cae8`, а не на `59f8fea` в release.
- README не описывает ни откат, ни установку конкретной версии.

**Риск.** Плохой релиз (например, хук падает с кодом 2 или ломает формат `cache.json`) можно исправить только «вперёд»: revert в main, новый тег `vX.Y.Z+1` и полный прогон CI на трёх ОС. Это десятки минут, и всё это время у пользователей с автообновлением сломано.
Пользователь не может закрепиться на предыдущей версии. Codex принимает `--ref`, но указать там нечего: на коммиты release нет ссылок по именам. «Отозвать» релиз в GitHub UI бессмысленно, потому что установка идёт не из Releases.

**Рекомендации.**
- В publish ставить на коммит release тег-«дистрибутив» (например, `dist/vX.Y.Z`). В README описать закрепление: `codex plugin marketplace add Comprise/auto-context --ref dist/v1.0.0`; для Claude Code указать аналог `#ref`, если маркетплейс его поддерживает.
- Добавить `docs/RELEASING.md` с runbook:
  1. как выпустить hotfix (revert → `vX.Y.Z+1`);
  2. как пометить плохой GitHub Release (`gh release edit --prerelease` или удалить ассеты, добавить в notes «yanked»);
  3. как поступить, если откат нужен немедленно. Ручной `workflow_dispatch` «rollback» коммитит дерево предыдущего `dist/`-тега как новый коммит с версией `X.Y.Z+1`. Так ветка остаётся fast-forward, а у пользователей возвращается старый код.

### CD-M3. Собранные бинарники не запускаются ни разу до доставки, пре-релизы не проходят реальным путём установки
**Факты.**
- `go test` гоняет код из исходников только на хостовой архитектуре. Шесть кросс-собранных `libexec/*` до публикации только проверяются на количество (`test … -eq 6`) и проходят `plugin validate`. Ни один не исполняется, в том числе через шим.
- Пре-релизы (`-rc`) в ветку не попадают (`release.yml:117`), поэтому установку из маркетплейса до выхода в stable проверить негде.

**Риск.** Ошибка `-ldflags`, не та архитектура, отсутствующий `+x` после `git add`, поломка шима в дереве release: всё это обнаружат пользователи, а не CI. В сочетании с CD-M2 восстановление получается медленным.

**Рекомендации.**
- Smoke-задание после build на `ubuntu-latest`, `ubuntu-24.04-arm`, `macos-latest` (arm64), `macos-13` (amd64) и `windows-latest`. Оно распаковывает собранный tar.gz и проверяет:
  1. `bin/auto-context version` возвращает тег;
  2. `echo '{}' | bin/auto-context hook session-start` завершается с кодом 0 и выдаёт валидный JSON на фикстуре с `context/_root.md`.
- Канал пре-релизов: ветка `release-next`, которую двигают `-rc`-теги. Её можно поставить через `--ref release-next` для проверки.

### CD-M4. Provenance нельзя проверить там, откуда пользователь реально берёт код
**Факты.**
- `attest-build-provenance` аттестует только `libexec/*` (`release.yml:82-85`). `auto-context-vX.tar.gz` и `checksums.txt` не аттестованы и не подписаны.
- Пользователь получает бинарники из коммита ветки release (`git add -f libexec`). Коммит не подписан, а README не упоминает `gh attestation verify` и `sha256sum -c`.

**Риск.** Аттестация есть, но цепочка «установленный файл → workflow run» для пользователя не замкнута. Подмену в ветке release (CD-H1) снаружи не обнаружить.

**Рекомендации.**
- Добавить в `subject-path` tar.gz и `checksums.txt`.
- В README (оба языка, см. MEMORY) добавить раздел «Verify». Пример: `gh attestation verify ~/.claude/plugins/cache/auto-context/auto-context/<ver>/libexec/auto-context-linux-amd64 -R Comprise/auto-context`.
- Класть `checksums.txt` в дерево release, чтобы его можно было сверить офлайн.
- По желанию подписывать коммиты release (`commit-tree -S` с ключом бота) или проверять, что дерево release побайтно совпадает с аттестованным архивом.

### CD-M5. Нет SECURITY.md, отключён private vulnerability reporting
**Факты.**
- В репозитории нет `SECURITY.md`, нет `.github/ISSUE_TEMPLATE`.
- `private-vulnerability-reporting` возвращает `{"enabled": false}`. Репозиторий публичный.
- На этом фоне фазы 2–3 нашли High (S-1: git исполняет команды из `.git/config` враждебного архива; D-H1: раздел Security в README обещает больше, чем код).

**Риск.** Исследователю некуда сообщить об уязвимости приватно, и отчёт придёт публичным issue. Нет обещанных сроков реакции и списка поддерживаемых версий. Нет процедуры GHSA/CVE и уведомления пользователей, поставивших уязвимую версию.

**Рекомендации.**
- Включить Private vulnerability reporting.
- Добавить `SECURITY.md`:
  - поддерживаемая версия: последний stable из ветки release;
  - канал: GitHub advisory;
  - сроки: подтверждение за N дней;
  - модель угроз (ссылка на README Security после исправления D-H1);
  - процедура: advisory → hotfix по runbook CD-M2 → GHSA.
- Шаблон issue «bug» с полями `auto-context version`, ОС/архитектура, агент (Claude Code/Codex и версия), хвост `.auto-context/log` (см. CD-L6).

### CD-M6. История релизов рассогласована: висячие GitHub Releases без тегов, коммиты release без релизов
**Факты (ПОДТВЕРЖДЕНО через `gh release list`/`git ls-remote`).**
- На remote есть только тег `v1.0.0`.
- При этом GitHub Releases `v0.1.0-rc1`, `v0.1.0`, `v0.2.0`, `v0.3.0` опубликованы (не draft), но их тегов на remote нет: `commits/v0.3.0` возвращает 422.
- Ветка release содержит коммиты `release v0.0.1…v0.0.3` с `source:` на коммиты, у которых нет ни тегов на remote, ни Releases.
- Значит, теги удалялись и пересоздавались, а ветка release переписывалась.

**Риск.**
- Ассеты висячих Releases (по 2 файла, старый формат) нельзя связать с исходным коммитом, и provenance для них бесполезна.
- Пользователь, поставивший 0.3.0 до сброса, получит 1.0.0 как обновление. Если бы новая серия начиналась ниже 0.3.0, «вперёд» означало бы тихий даунгрейд.
- `--generate-notes` строит заметки от предыдущего тега, а его нет, поэтому notes неверны.

**Рекомендации.**
- Удалить висячие Releases v0.1.0-rc1…v0.3.0 или пометить их «withdrawn».
- Зафиксировать политику «теги и релизы неизменяемы» (ruleset из CD-H1 и Immutable Releases).
- В `--generate-notes` передавать `--notes-start-tag` = предыдущий stable-тег.

### CD-M7. Ветка release накапливает бинарники в истории: каждый релиз добавляет около 24 МБ, клон растёт линейно (ПРЕДПОЛОЖИТЕЛЬНО)
**Факты.** Каждый коммит release заменяет все шесть бинарников, это около 24 МБ несжатых, а Go-бинарники почти не дельта-сжимаются (см. `git show --stat 59f8fea`). Коммиты связаны через `-p origin/release` (`release.yml:132-136`), поэтому история хранит все версии.

**Риск.** Если маркетплейс клонирует ветку не shallow (для Claude Code и Codex это не проверено), установка и обновление тянут все прошлые бинарники. Через десятки релизов это сотни МБ, медленная установка и место на диске у каждого пользователя.

**Рекомендации.**
- Проверить, клонируют ли Claude Code и Codex с `--depth 1`.
- Если нет, есть два варианта:
  - (а) коммиты release без родителя. Тогда нужен force-push, а он конфликтует с CD-H1: разрешить его только боту через bypass;
  - (б) не хранить бинарники в git: шим при первом запуске скачивает `libexec` из Release и проверяет хеш из `checksums.txt`, закоммиченного в дерево. Это сложнее и требует сети.
- Минимум: замерить размер клона и записать порог в RELEASING.md.

---

## Low

### CD-L1. Версия Go не закреплена, сборка невоспроизводима
`go.mod` содержит `go 1.27` без `toolchain`. Поэтому `setup-go` с `go-version-file` берёт последнюю 1.27.x на момент запуска. Повторный запуск release (`release.yml:144-148` это допускает) или локальный `make dist` дадут другие байты, хотя версия та же. Проверки воспроизводимости нет. Риск: хеш `checksums.txt` и аттестация не совпадут с повторной сборкой. Кроме того, патч-версия Go в бинарнике определяется случайно, моментом тега.
**Рекомендация:** `toolchain go1.27.N` в go.mod, обновлять через dependabot. Добавить в release задание, которое собирает linux-amd64 дважды и сравнивает sha256. Явно задать `-buildvcs`.

### CD-L2. `paths-ignore` и required checks: docs-only PR зависнет; список непоследователен
Если сделать `test`/`plugin` обязательными (CD-H1), PR, меняющий только `docs/**` или `context/**`, никогда не получит статус и не смёржится: так ведёт себя GitHub, когда workflow пропущен через paths-ignore.
Кроме того, в списке есть `README.md`, но нет `README.ru.md`. При этом `**/CLAUDE.md` исключён, хотя `TestHooksJSONContract`/skills-тесты могут читать README (проверить).
**Рекомендация:** убрать paths-ignore и сделать дешёвый ранний выход (dorny/paths-filter → задания с `if:`) либо задание-«агрегатор», которое всегда сообщает статус. Синхронизировать список README.

### CD-L3. Нет линт-гейтов
В CI нет `gofmt -l`, `staticcheck`/`golangci-lint`, `actionlint`, `shellcheck` (для `bin/auto-context` есть только `sh -n` и `check-manifests.sh`). Шим исполняется на каждом хуке у каждого пользователя, и `sh -n` ловит только синтаксис.
**Рекомендация:** одно задание `lint` с закреплёнными версиями инструментов (`go run honnef.co/go/tools/cmd/staticcheck@vX`, rhysd/actionlint по SHA, shellcheck `-s sh`). Отдельно проверить шим под `dash` и `busybox sh`.

### CD-L4. `npx --yes @anthropic-ai/claude-code@2.1.304` без lock-файла
Сама версия закреплена, а транзитивные зависимости нет: npx резолвит их при каждом запуске (`ci.yml:58,69`, `release.yml:110`). Это вход для supply chain и в CI, и в publish (права publish уже разобраны в area-packaging #2). Версию никто не обновляет (CD-M1).
**Рекомендация:** `package.json` и `package-lock.json` в `.github/tools/`, `npm ci --ignore-scripts`, обновлять через dependabot (`npm`).

### CD-L5. Кэш setup-go в тестовом задании релиза
`release.yml:test` вызывает `ci.yml`, где `setup-go` с кэшем по умолчанию. Запуск по тегу читает кэш модулей, созданный запусками main/PR. Сборка идёт с `cache: false`, поэтому на бинарники это не влияет, но гейт «тесты прошли» опирается на кэш из менее доверенного контекста.
**Рекомендация:** параметр `workflow_call` `inputs.cache` (false для release) или `cache: ${{ github.event_name != 'push' || !startsWith(github.ref,'refs/tags/') }}`.

### CD-L6. Мало диагностики для отчёта об ошибке
- `auto-context version` печатает только тег (`main.go:105`). Коммит, версия Go, GOOS/GOARCH и путь установки не выводятся.
- Нет команды, которая собирает отчёт: версия, `status`, конфиг, хвост `.auto-context/log` с `textx.Clean`.
- Сбои хуков, кроме SessionStart, только пишутся в лог, и пользователь о них не узнаёт, пока не заглянет туда. Счётчика «N ошибок с прошлой сессии» в SessionStart нет.

**Рекомендация:**
- `version -v` через `debug.ReadBuildInfo()` (vcs.revision, GoVersion);
- `auto-context doctor` или `status --report`, готовый для вставки в issue;
- в SessionStart одной строкой systemMessage сообщать, что лог вырос с прошлого раза.

### CD-L7. `make dist` и release-сборка дублируют команду и расходятся
Makefile (`VERSION ?= dev`, без `go mod verify`) и `release.yml:104-106` содержат одну и ту же строку `go build`, скопированную вручную. Изменение флагов в одном месте не попадёт в другое. Локально собранный «релиз» неотличим от `dev`.
**Рекомендация:** release вызывает `make dist VERSION=$GITHUB_REF_NAME PLATFORMS=$goos/$goarch` (единый источник). В Makefile добавить цели `lint` и `verify` (go mod verify, check-manifests), совпадающие с CI.

### CD-L8. Публикация не атомарна: ветка обновляется до создания Release
`release.yml:115-136` сначала двигает ветку, и только потом `gh release create`. Если второй шаг упадёт (rate limit, сеть), пользователи получат версию без Release, checksums и notes. Re-run это исправит, но только если кто-то заметит.
**Рекомендация:** создавать Release черновиком (`--draft`) до push ветки и публиковать (`gh release edit --draft=false`) после него. Добавить уведомление о провале workflow; по умолчанию письмо получает только инициатор.

### CD-L9. Настройки Actions репозитория разрешают всё
`allowed_actions: all`, `sha_pinning_required: false`. Сейчас все actions закреплены вручную, но новый workflow (или PR со сторонним action) это ничем не ограничено. Пересекается с CD-H1 п.5, вынесено отдельно как дешёвое исправление в настройках.

---

## Наблюдаемость и инциденты: итог по пунктам задания

- **Что видит пользователь при сбое.** SessionStart показывает systemMessage, если бинарника нет или он упал (шим). Остальные хуки пишут ошибку только в `.auto-context/log`, ротация 1 MiB + `.1`, это достаточно. Пробел: нет агрегированного сигнала и отчёта (CD-L6).
- **Диагностика.** Есть `status` (предупреждения конфига, .gitignore, git) и `version` (только тег); Troubleshooting в README. Нет: doctor, шаблона issue, SECURITY.md (CD-M5, CD-L6).
- **Откат и восстановление ветки release.** Не задокументированы, технически возможен только roll-forward (CD-M2). Восстановить ветку после порчи можно: `source:` в сообщении коммита плюс аттестованные бинарники. Но процедуры нет, и защиты от порчи тоже нет (CD-H1).
- **Окружения и секреты.** Используется только `github.token`; dev/prod разделены как main/release, пре-релизы до пользователей не доходят. Нет GitHub environment с апрувом (CD-H1), нет канала пре-релизов (CD-M3).
- **Согласованность версий.** Go: go.mod ↔ setup-go через `go-version-file` согласованы, но патч не закреплён (CD-L1). claude CLI закреплён на 2.1.304, но не обновляется (CD-M1/CD-L4). actionlint в CI нет (CD-L3). Версии бинарника и дерева плагина совпадают by construction: тег проставляется в plugin.json и `-X main.version`, а check-manifests это проверяет.
