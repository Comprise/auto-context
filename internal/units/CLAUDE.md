# internal/units
Обнаружение юнитов документации в репозитории и классификация путей.

- `units.go` — неизменяемое дерево юнитов: полный и индексный вид (`HashFiles`, `HashUnits`), принадлежность файлов, классификация путей (`Classify`, `Classifier`), `StatsView`, пути доков (`ContextPath`), имя индексного файла, `SafeName`, `ErrUnknownUnit`
- `walk.go` — `DiscoverContext` и состояние обнаружения `discovery` (эвристика юнитов, `view`), обход без git, `classifyFile` и `addFile`
- `ignore.go` — разбор и сопоставление `.gitignore` и шаблонов `exclude`/`track` (`patternSet`, `patterns`, `matcher` с бюджетом glob, `parseIgnore`, лимиты)
- `gitfiles.go` — список файлов от git с проверкой топологии (`gitFiles`, `maxFiles`, `ErrGitTimeout`, `ErrListTooLarge`)
- `fuzz_test.go`, `testdata/fuzz/` — дифференциальный фазз-тест `.gitignore` против git и его сиды

Подробно: context/internal/units.md
