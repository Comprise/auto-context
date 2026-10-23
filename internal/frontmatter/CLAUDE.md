# internal/frontmatter
Чтение и запись YAML-frontmatter контекстных файлов (unit, source_hash, commit, updated).

- `frontmatter.go` — чтение и перезапись блока меты, summary для оглавления
- `fuzz_test.go`, `testdata/fuzz/` — fuzz круговой записи `SetHash` → `Read`

Подробно: context/internal/frontmatter.md
