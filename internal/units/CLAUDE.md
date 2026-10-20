# internal/units
Обнаружение юнитов документации в репозитории и классификация путей.

- `units.go` — дерево юнитов: принадлежность файлов, классификация путей, пути доков (`ContextPath`), имя индексного файла, `SafeName`
- `walk.go` — `Discover` (эвристика юнитов), обход без git, `classifyFile` и `addFile`
- `ignore.go` — разбор и сопоставление `.gitignore` и шаблонов `exclude`/`track` (`patternSet`, `parseIgnore`, лимиты)
- `gitfiles.go` — список файлов от git (`gitFiles`, `ErrGitTimeout`, `ErrListTooLarge`)

Подробно: context/internal/units.md
