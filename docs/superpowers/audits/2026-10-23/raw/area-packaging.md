# Ревью по области: упаковка, релиз, документация (ревьюер из первого запроса)

Проверено: `claude plugin validate .` и `--strict` на собранном дереве проходят; actionlint+shellcheck — только info; hooks.json/codex.json сходятся с hookHandlers/budgetFor; цитаты навыков есть в коде; README.md и README.ru.md синхронны; actions закреплены по SHA, минимальные права, инъекций `${{ }}` нет; логика ветки release корректна.

1. **Low — нет `.gitattributes` с `eol=lf` для шима** (`.gitattributes:1`, `release.yml:93,130`, `ci.yml:65`): при `core.autocrlf=true` `bin/auto-context` с CRLF → `sh` выходит с кодом 2 (блокирующий для Claude Code). ПОДТВЕРЖДЕНО для не-MSYS sh. Исправление: `bin/auto-context text eol=lf`, включить `.gitattributes` в дерево release.
2. **Low — npm-код выполняется в publish-задании с `contents: write` и `id-token: write`** (`release.yml:66-71,106`). ПО КОДУ. Исправление: validate в отдельном read-only задании; GH_TOKEN только на шагах push/release.
3. **Low — `concurrency: release` отменяет промежуточные ожидающие релизы** (`release.yml:10`). ПО КОДУ. Исправление: `release-${{ github.ref }}`.
4. **Low — старый тег, выпущенный позже, становится «Latest»** (`release.yml:122-125,148`). ПРЕДПОЛОЖИТЕЛЬНО. Исправление: `--latest=false`, если ветка не сдвинулась.
5. **Low — неверное имя теста в `context/cmd/auto-context.md:30`** (TestHooksJSONContract не проверяет codex.json; это TestCodexHooksManifest). ПОДТВЕРЖДЕНО.

Общее: область в хорошем состоянии, блокирующих проблем нет.
