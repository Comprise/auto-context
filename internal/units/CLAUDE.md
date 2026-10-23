# internal/units
Обнаружение юнитов документации в репозитории и классификация путей.

- `units.go` — дерево юнитов: полный и индексный вид (`HashFiles`, `HashUnits`), принадлежность файлов, классификация путей, пути доков (`ContextPath`), имя индексного файла, `SafeName`, `ErrUnknownUnit`
- `walk.go` — `Discover` (эвристика юнитов, `view`), обход без git, `classifyFile` и `addFile`
- `ignore.go` — разбор и сопоставление `.gitignore` и шаблонов `exclude`/`track` (`patternSet`, `parseIgnore`, лимиты)
- `gitfiles.go` — список файлов от git с проверкой топологии (`gitFiles`, `maxFiles`, `ErrGitTimeout`, `ErrListTooLarge`)
- `fuzz_test.go`, `testdata/fuzz/` — дифференциальный фазз-тест `.gitignore` против git и его сиды

Подробно: context/internal/units.md
