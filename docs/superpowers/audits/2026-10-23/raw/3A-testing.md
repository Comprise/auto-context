# 3A. Тестирование: стратегия и покрытие

Коммит af0cae8. Прошлый аудит тестов (`docs/superpowers/audits/2026-10-20/tests.md`) в основном закрыт: `testutil` с `ScrubEnv`/`RequireGit`/`Git`, golden для Stop/Codex, `TestHookGetwdFailureExits0`, `TestChangesExitCodes`, `TestMain` в пакетах с git. Ниже только новое или оставшееся.

## Счётчики

Critical 0 · High 2 · Medium 7 · Low 7. Плюс 8 тестов для подтверждённых находок фазы 2 (не переоткрыты, даны примеры).

## Прогоны (всё в scratchpad, файлы проекта не менялись)

| Прогон | Результат |
|---|---|
| `go test -coverprofile ./...` | всё зелёное, всего **91,2%** (по пакету) |
| `go test -coverpkg=./... ./...` | **93,6%** (с межпакетными вызовами) |
| `go test -count=3 -race ./...` | зелёное, 6,4 с |
| `go test -count=2 -shuffle=on ./...` | зелёное |
| `umask 077`, без `HOME`, `LC_ALL=ru_RU.UTF-8` | зелёное |
| `TMPDIR` внутри git-репозитория | **35 падений в 6 пакетах** (M-1) |
| fuzz `rootindex.Update` (идемпотентность), 2,5 мин | 1 расхождение (L-1) |
| fuzz `frontmatter.SetHash`→`Read` (обратимость + идемпотентность), 1 мин | чисто |
| fuzz `lfWriter` (CRLF на границе чанков), 45 с | чисто |
| дифференциальный fuzz `parseIgnore`/`match` против `git check-ignore --no-index` | чисто, если не считать `[[:alpha:]]` (известное ограничение, задокументировано в context/internal/units.md) |
| стресс: 8 горутин × 15 `Open`+`AckTouched`+`SaveCache` на одном репо, `-race -count=10` | чисто: доки читаются, cache.json валиден, ровно одна секция индекса, `.tmp` нет |

### Покрытие по пакетам (по пакету / с `-coverpkg`)

| Пакет | % |
|---|---|
| cmd/auto-context | 88,2 |
| internal/config | 93,3 |
| internal/docstate | 92,6 |
| internal/frontmatter | 98,4 |
| internal/fsutil | 86,9 |
| internal/gitx | 94,3 |
| internal/hashing | 89,1 |
| internal/hooks | 94,0 |
| internal/logx | 85,3 |
| internal/project | 78,0 (было 61,8) |
| internal/rootindex | 94,3 |
| internal/session | 91,9 |
| internal/textx | 100 |
| internal/units | 95,1 |
| **итого** | **91,2 / 93,6** |

Блоки, которые не выполняет ни один тест (объединение всех тестовых бинарей, 144 блока): `/tmp/claude-1000/-home-elia-Projects-auto-context/b403d57d-6fe7-40d7-9c97-ee52030c3f1f/scratchpad/uncovered.txt`. Самые важные из них разобраны ниже.

## Пирамида и качество

- Unit-тестов много, и они проверяют поведение: хуки вызываются как функции с реальной ФС и git во временном каталоге. Интеграционный слой — это `cmd/auto-context/main_test.go` (`run()` в том же процессе) и `shim_test.go` (настоящий шим, но с фейковым бинарём на sh). Настоящий собранный бинарь не запускается ни разу, поэтому `main()` покрыт на 0%: `os.Exit`, реальный stdin и строки команд из hooks.json через `sh -c` не проверяются (M-6).
- Ассерты в основном точные. Есть golden-файлы для SessionStart/Stop (Claude и Codex). В stop_test/main_test 98 проверок через `strings.Contains`, это оправдано, если рядом есть точные тексты (`TestStopReasonText`).
- Изоляция: `testutil.Main` очищает `GIT_*`/`CLAUDE_*` и задаёт `GIT_CONFIG_GLOBAL=/dev/null`, но только в 5 пакетах. В docstate и session `units.Discover` (а значит и git) запускается без `TestMain`. `GIT_CEILING_DIRECTORIES` не задаётся (M-1).
- `t.Parallel` не используется нигде. Глобальные переменные (`gitx.Timeout`, `units.maxList`, `gitx.SetDeadline`, `fsutil.realRoots/sameCache`, `debug.SetMemoryLimit` в `runHook`) меняются тестами, и с `-shuffle` пока проблем нет.
- Флейков не найдено: `time.Sleep` нет, mtime задаются явно через `Chtimes`, ошибки `Chtimes` проверяются (пункт 8 прошлого аудита закрыт).

---

## High

### H-1. Два теста безопасности активации всегда пропускаются (на всех ОС)
`internal/hooks/hooks_test.go:508-526` (`TestSessionStartRefusedMarker`) и хвост `TestActive` (`:245-255`). Симлинк создаётся в `root/.auto-context/config.yaml`, но каталога `.auto-context` нет. `os.Symlink` падает с ENOENT, а тест принимает это за «symlinks unsupported» и вызывает `t.Skip`. Проверено `go test -v`: оба `--- SKIP` на Linux. Поэтому:
- ветки `project.ActivationRefused` (`project.go:53-59`), которые возвращают true, и системное сообщение «the plugin is off here» (`session_start.go:38-42`) не выполняет ни один тест (0% в объединённом профиле);
- инвариант «маркер-симлинк за корень не включает плагин» в `TestActive` тоже не проверяется.

В копии в scratchpad после `MkdirAll` оба теста проходят: поведение верное, но регрессию никто не поймает.

Рекомендация: создавать родительский каталог и пропускать тест только на настоящее «не умею симлинки».
```go
// testutil
func Symlink(t testing.TB, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
			t.Skip("symlinks need developer mode:", err)
		}
		t.Fatal(err)
	}
}
```
И в тесте: `es, _ := os.ReadDir(root+"/.auto-context"); len(es) == 1` (кроме самой ссылки ничего не записано), плюс сценарий `context/_root.md` как FIFO или каталог (`!IsRegular`). Чтобы такие пропуски больше не прятались, в CI на ubuntu стоит падать на неожиданных SKIP: `go test -json ./... | jq -r 'select(.Action=="skip" and .Test) | .Test'` сравнивать со списком разрешённых пропусков.

### H-2. `.gitignore`-FIFO в git-режиме: каждый хук висит 20 с, и это не покрыто
`TestGitignoreFIFO` (`internal/units/units_unix_test.go:20`) проверяет только режим обхода без git. В git-режиме `git ls-files --exclude-standard` сам читает FIFO и блокируется. Воспроизведено бинарём: `hook session-start` в репо с `mkfifo .gitignore` работает **20,0 с** (`gitx.Timeout`) и выдаёт сообщение «git is too slow or lists too many files». Причина указана неверно: FIFO, а не размер. То же ждёт каждый `prompt-submit` и `stop`. Ветка `gitfiles.go:41`, где таймаут возникает именно в `ls-files`, тоже не покрыта: тест есть только на таймаут `check-ignore`. Модель угроз: архив с `.git/` или файл, созданный локально (git не коммитит FIFO).

Рекомендация: тест и на ветку, и на сценарий.
```go
func TestDiscoverGitFIFOGitignore(t *testing.T) {
	root := t.TempDir()
	mkfiles(t, root, "a.go")
	gitInit(t, root)
	if err := syscall.Mkfifo(filepath.Join(root, ".gitignore"), 0o644); err != nil {
		t.Skip(err)
	}
	old := gitx.Timeout; gitx.Timeout = 300 * time.Millisecond
	t.Cleanup(func() { gitx.Timeout = old })
	start := time.Now()
	_, err := Discover(root, config.Default())
	// желаемое поведение: заранее найти не-regular .gitignore и предупредить (или
	// пойти обходом); минимум — уложиться в Timeout и не винить размер репо
	if time.Since(start) > 2*time.Second || errors.Is(err, ErrGitTimeout) {
		t.Errorf("err=%v after %v", err, time.Since(start))
	}
}
func TestDiscoverLsFilesTimeout(t *testing.T) {
	fakeGit(t, `case "$*" in *ls-files*) exec sleep 5;; esac`)
	// ... gitx.Timeout = 200ms; errors.Is(err, ErrGitTimeout)
}
```
Ремонт в коде (вне этой фазы): перед `ls-files` сделать lstat всех `.gitignore` по пути и в корне, либо передавать `-c core.excludesFile=` и проверять `info/exclude`.

---

## Medium

### M-1. Тесты зависят от того, что `TMPDIR` не лежит внутри git-репозитория
`testutil.ScrubEnv` не задаёт `GIT_CEILING_DIRECTORIES`. С `TMPDIR=<git-репо>/t` падает 35 тестов в cmd/auto-context, config (`TestFindRoot`), gitx (`TestRunForcesEnglish`), hooks, project и units (`TestGitignoreRespected`, `TestGitignoreFIFO` по таймауту 5 с и другие): git находит внешний репозиторий и переключается в git-режим. Так бывает на машинах, где `$HOME` — dotfiles-репозиторий и `TMPDIR` лежит под ним, и в контейнерах CI, где temp находится в рабочем дереве. Хуже того, тесты, которые рассчитывают на режим обхода, могут пройти не в том режиме.

Рекомендация:
```go
// testutil.ScrubEnv
os.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(os.TempDir()))
```
(gitx пропускает переменную, `repoVars` её не режет.) `TestMain` нужен во всех пакетах, где тесты вызывают `units.Discover`: docstate, session, hashing. Проверка: запустить в CI один раз с `TMPDIR=$GITHUB_WORKSPACE/.tmp`.

### M-2. `changes`: обрезка вывода, падение git в секции и причины `ackPoint` не покрыты
`internal/project/changes.go:130-142` (`(git failed: …)`, `(output cut at N KiB)`) и `:163-167` (`broken frontmatter`, doc за корнем, нечитаемый doc). `changes` одобряется автоматически, а его вывод уходит в контекст модели. Лимит `maxChangesOutput` — единственная граница против огромных тем коммитов во враждебном репо, и ни один тест её не проверяет.
```go
func TestChangesOutputCapped(t *testing.T) {
	root := gitRepoWithUnit(t, "svc")
	msg := strings.Repeat("x", maxChangesOutput)
	testutil.Git(t, root, "commit", "--allow-empty", "-qm", msg)
	var b bytes.Buffer
	if err := p.WriteChanges(&b, "svc"); err != nil { t.Fatal(err) }
	if !strings.Contains(b.String(), "(output cut at") || b.Len() > 2*maxChangesOutput { ... }
}
// таблица: frontmatter "---\nunit: [\n---" → "broken frontmatter"; doc-симлинк наружу → OutsideText;
// fakeGit с exit 3 в "log" → "(git failed: exit 3)" и без stderr git
```

### M-3. `Project.Exclude` (запись в коммитный конфиг): ветки ошибок не покрыты
`project.go:310-320`: каталог-симлинк за корень (`Resolve` → ErrOutside), ошибка stat, отличная от ENOENT, отказ `ValidatePattern`. Это единственный путь, по которому CLI правит отслеживаемый `.auto-context/config.yaml`.
```go
func TestExcludeRefuses(t *testing.T) {
	for name, setup := range map[string]func(root string) string{
		"link outside": func(r string) string { testutil.Symlink(t, t.TempDir(), r+"/ext"); return "ext" },
		"file":         func(r string) string { testutil.WriteFile(t, r, "f", ""); return "f" },
		"bad pattern":  func(r string) string { os.MkdirAll(r+"/a[", 0o755); return "a[" },
	} {
		t.Run(name, func(t *testing.T) {
			root := activeRepo(t); dir := setup(root)
			before, _ := os.ReadFile(root + "/.auto-context/config.yaml")
			if err := open(t, root).Exclude(dir); err == nil { t.Fatal("accepted") }
			after, _ := os.ReadFile(root + "/.auto-context/config.yaml")
			if !bytes.Equal(before, after) { t.Error("config changed") }
		})
	}
}
```

### M-4. Нет конкурентных тестов между процессами и сессиями
Параллельные сессии Claude/Codex в одном репо — штатный сценарий. Последнее слово в `cache.json` остаётся за последним записавшим (P-L6). Документы, индекс и журналы из разных процессов пишутся через read-modify-write. Единственный конкурентный тест — `logx` (горутины). Мой стресс-тест в scratchpad (8 горутин, `-race`, 10 прогонов) чистый, поэтому стоит закрепить его в репо, а лучше вариант с процессами:
```go
func TestConcurrentStopHooks(t *testing.T) {
	// N раз exec os.Args[0] -test.run=TestHelperStop с разными session_id
	// (паттерн helper-process), все правят и подтверждают одни юниты;
	// после: frontmatter.Read ok у всех доков, json.Valid(cache.json),
	// ровно одна пара маркеров в CLAUDE.md, нет *.tmp*, status --stale пуст
}
```

### M-5. CI запускает только `go test ./...`
`.github/workflows/ci.yml`: нет `-race` (а в коде есть горутины: `hashing.Prewarm`, `units/walk.go:275`, `sync.Map` в fsutil), нет `-shuffle=on`, нет порога покрытия, нет контроля неожиданных SKIP (H-1), fuzz-корпусов тоже нет. Рекомендация: добавить на ubuntu шаг `go test -race -shuffle=on -count=1 ./...` (6 с) и шаг `go test -json` с проверкой SKIP. Короткий `-fuzztime=20s` по 4 фаззерам из M-7 — в nightly-задачу.

### M-6. Нет сквозного теста «настоящий бинарь + шим + hooks.json»
`TestHooksJSONContract` проверяет строки команд статически, `shim_test` подменяет бинарь sh-скриптом. Регрессии упаковки (раскладка `libexec/`, имя по `uname`, кавычки вокруг `${CLAUDE_PLUGIN_ROOT}` с пробелом, `os.Exit` в `main`, чтение stdin > 1 МиБ — `main.go:183` не покрыт) ловит только ручная проверка.
```go
func TestE2EHooksThroughShim(t *testing.T) {
	if runtime.GOOS == "windows" { t.Skip("POSIX sh") }
	plug := filepath.Join(t.TempDir(), "plugin dir") // с пробелом
	copyTree(t, "../../bin", plug+"/bin")
	out := plug + "/libexec/" + hostBinary()
	if b, err := exec.Command("go", "build", "-o", out, ".").CombinedOutput(); err != nil { t.Fatal(err, string(b)) }
	for event, cmd := range hooksJSONCommands(t) { // из hooks/hooks.json
		c := exec.Command("sh", "-c", cmd)
		c.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+plug, "CLAUDE_PROJECT_DIR="+repo(t))
		c.Stdin = strings.NewReader(`{"session_id":"e2e"}`)
		// exit 0; stdout пуст или валидный JSON с hookEventName == event
	}
	// + stdin 2 МиБ → exit 0, строка "input larger than" в .auto-context/log
}
```

### M-7. Нет fuzz-тестов, бенчмарк один (`BenchmarkTake`)
Разборщики враждебного текста — хорошие кандидаты для fuzz. Проверенные мной в scratchpad свойства, которые стоит закрепить:
```go
// rootindex: после успешного Update — Check()==nil и повторный Update ничего не меняет;
// после ошибки Update — Check()!=nil
func FuzzUpdate(f *testing.F) { f.Fuzz(func(t *testing.T, content, unit, sum string) {...}) }
// frontmatter: SetHash → Read возвращает ровно те же Meta; повторный SetHash ничего не меняет
func FuzzSetHashRoundTrip(f *testing.F)
// hashing: lfWriter при любом размере чанка == strings.ReplaceAll(s, "\r\n", "\n")
// (ветка hashing.go:311, одиночный CR на границе чанка, сейчас не покрыта)
func FuzzLFWriter(f *testing.F)
// units: дифференциально против `git --literal-pathspecs check-ignore --no-index`
// (алфавит "ab*?[]!-\\/.^", без "[:"); пропуск, если git недоступен
func FuzzIgnoreMatchesGit(f *testing.F)
// hooks: allowedCommand(cmd)==true ⇒ strings.Fields(cmd) совпадает со Split(cmd," "),
// нет "..", "--exclude", байтов вне [A-Za-z0-9_./ -]
func FuzzAllowedCommand(f *testing.F)
```
Бенчмарков для горячих путей нет, хотя фаза 2 нашла выход за лимит памяти 512 МиБ (P-M5) и квадратичный `rootindex`. Предлагаю `BenchmarkDiscover`, `BenchmarkStopNoChange` и `BenchmarkStopAfterTouchAll` на синтетическом дереве (20k файлов, 500 юнитов, `b.ReportAllocs()`) и `BenchmarkRootindexUnclosedFences` (п. 6 фазы 2). Плюс тест-предохранитель: `runtime.ReadMemStats` после Stop на 50k файлах < 150 МиБ.

---

## Low

- **L-1. `rootindex.Update` не идемпотентен на файле с одиночным `\r` без `\n`** (найдено fuzz). Вход `"\r"`: первый Update пишет секцию с LF (`eolOf` не видит `\r\n`), второй видит первую строку `"\r\n"` и переписывает секцию в CRLF. Файл перезаписывается дважды, затем стабилизируется. Встречается редко (файлы в формате классического Mac), но регрессионный seed нужен: `f.Add("\r", "a", "b")` в `FuzzUpdate`.
- **L-2. `fsutil.writeAtomic`: не покрыты ветки ошибок** (`fsutil.go:285-297`: Write/Sync/Close/Chmod/Rename) и удаление `.tmp` после сбоя. Это остаток пункта 4 прошлого аудита: ReadDoc/WriteDoc/WriteState теперь покрыты, а уборка временных файлов — нет. Тест: каталог-цель только для чтения после `CreateTemp` нельзя устроить переносимо, поэтому Rename в каталог на месте файла: `path` указывает на существующий непустой каталог → ошибка, и `ReadDir(dir)` не содержит `*.tmp*`.
- **L-3. Обрезка списков в фидбэке Stop не покрыта**: `stop.go:476` («More failed auto-acks») и `:494` («More new units»), а также `SessionNotice` при числе stale больше limit (`session_start.go:276`). Эти лимиты ограничивают объём `additionalContext` во враждебном репо с тысячами юнитов. Тест: 30 новых юнитов → в причине ровно `capList`-лимит имён и хвост «More new units».
- **L-4. Повреждённый журнал сессии не покрыт** (`stop.go:105,126,214`, `prompt_submit.go:31,55`): что происходит с уведомлениями, если `<sid>.journal` — мусор или каталог. Свойство: Stop отвечает ровно один раз, а не на каждом Stop, и не падает.
- **L-5. Глобальные мемо-кэши `fsutil.realRoots`/`sameCache` живут весь процесс тестов.** Тест, который перенаправляет симлинк корня после первого разрешения, получит устаревший ответ. Хуки однопроцессные, так что это ловушка для будущих тестов. Нужен `resetCaches()` только для тестов (export_test.go) и вызов в `t.Cleanup` у тестов с симлинками корня.
- **L-6. Ошибки сохранения кэша в Stop и CLI не покрыты** (`stop.go:79`, `main.go:264`, `hashing.go:130-135`). Достаточно одного теста: `.auto-context/cache.json` — каталог → Stop логирует «hash cache:» и отвечает как обычно, `status` выводит `warning:`.
- **L-7. Версия git в CI не зафиксирована и не варьируется.** Код сопоставляет английские сообщения git (`"not a git repository"`) и зависит от поведения `ls-files`/`check-ignore`. Тесты идут только на git раннера. Предлагаю nightly-задачу с минимальной поддерживаемой версией git (контейнер `debian:oldstable`) или хотя бы `git --version` в логе CI.

---

## Тесты для подтверждённых находок фазы 2 (не переоткрываются)

1. **gitx против исполнения из `.git/config`** — `internal/gitx/gitx_test.go`:
   `TestRunIgnoresShowSignatureGPG`: `git config log.showSignature true`, `gpg.program=<скрипт, который touch-ит маркер>`, подписанный коммит (или `commit.gpgsign` с фейковым gpg), затем `project.WriteChanges` → маркера нет.
   `TestRunNoLazyFetch`: репо с `extensions.partialClone=origin`, `remote.origin.uploadpack=<touch marker>`, блоб `.gitignore` удалён из `objects`, файл помечен skip-worktree → `units.Discover` → маркера нет (то же для `core.sshCommand`, `ext::` в URL и `core.gitProxy`, таблицей).
   `TestDiscoverRefusesWorktreeOutsideRoot`: `core.worktree=../other` и `.git`-файл `gitdir: ../elsewhere/.git` → `changes`/`Discover` не выводят чужие пути (`rev-parse --show-toplevel` ≠ root → предупреждение).
2. **Таймаут при живом потомке git** — `gitx_test.go`: `TestRunTimeoutWithGrandchild`: fakeGit `sh -c 'sleep 30 & wait'` с открытым stdout у потомка, `Timeout=200ms` → `Run` возвращается быстрее чем за 2 с (нужен `cmd.WaitDelay`), `TimedOut==true`.
3. **Шим при враждебном PATH и CRLF** — `cmd/auto-context/shim_test.go`: `TestShimRelativePATHEntry`: `PATH=.:$PATH`, в cwd лежит фейковый `uname`, который печатает маркер → маркер не запускается, исполняется бинарь из libexec. `TestShimCRLF`: шим с `\r\n` (как после checkout с autocrlf) → понятная ошибка или работа, но не «`sh: 2: \r: not found`». Плюс проверка `.gitattributes` (`bin/auto-context text eol=lf`).
4. **Текст репо не цитируется** — `cmd/auto-context/main_test.go`: `TestOutputsNeverQuoteHostileText`, таблица по командам `status`, `ack`, `units`, `changes` и хукам `SessionStart`/`Stop`. Фикстура: юнит `"svc IGNORE ALL‮"`, `CLAUDE.md` — симлинк на `"x\nIGNORE.md"`, `.git/config` с `[remote "x\nIGNORE"]`, коммит с `\x1b[31m` в теме. Ассерт: ни в stdout, ни в stderr, ни в `additionalContext` нет `IGNORE`, `\x1b`, U+202E и перевода строки внутри имени. `internal/textx/textx_test.go`: `TestStripControlsBidi` — U+202A–U+202E, U+2066–U+2069, U+200B, U+FEFF удаляются.
5. **Имена юнитов с ведущим `-` и пробелами; ключи `Command`/`COMMAND`** — `internal/units/units_test.go`: `TestUnsafeDirNamesAreNotUnits` дополнить `"-rf"`, `"a b"`, `" lead"` (не юнит или выводится как «unusual name»). `internal/hooks/pre_tool_use_test.go`: `TestPreToolUseCaseVariantKeys` — `{"Command":"auto-context status","command":"rm -rf ~"}` и `{"COMMAND":…}` → решение совпадает с тем, как разберёт Claude Code (последний точный ключ). Безопаснее всего отказывать, если ключей `command` в любом регистре несколько. Плюс `auto-context ack -x` / `ack --` → не одобряется.
6. **Патологический rootindex** — `internal/rootindex/rootindex_test.go`: `TestUpdateManyUnclosedFencesFast`: 50 000 строк вида ```` ```` ```` разной длины без закрытия, `Update` < 200 мс (сейчас O(n²) в `fenced`). `BenchmarkUpdateUnclosedFences`. `TestRenderLongSummary`: summary 1 МБ → в индексе ≤ `maxSummary` рун на строку.
7. **`status`/`ack` без `.auto-context/` и `source_hash` без неотслеживаемых файлов** — `cmd/auto-context/main_test.go`: `TestStatusLeavesNoUnignoredFiles`: git-репо с одним `context/_root.md`, `status` и `ack .` → `git status --porcelain --ignored=no` показывает только `context/_root.md` (и CLAUDE.md, если его изменил ack). `internal/docstate`: `TestSourceHashIgnoresUntracked` — добавить неотслеживаемый (но не игнорируемый) файл в юнит: ожидаемое поведение по A-H1 — хэш тот же, что и в чистом клоне. Тест сравнивает хэш в рабочей копии и в `git clone`.
8. **Удаление юнита поднимает `_root`** — `internal/hooks/stop_test.go`: переписать `TestStopUnitDirDeleted`. Сейчас он закрепляет `out == nil`. Нужно ожидать, что Stop назовёт `_root` (или родителя) с изменёнными доками, а `context/svc/auth.md` попадёт в orphans уже на этом ходу, а не только на следующем SessionStart.

---

## Что в порядке

- Изоляция окружения (`testutil.ScrubEnv`, идентичность git, `GIT_CONFIG_GLOBAL`), `t.TempDir()` везде, `time.Sleep` нет.
- Сравнение с git (`*MatchGit`, `TestDiscoverGitMatchesWalk`) — сильная сторона. Дифференциальный fuzz расхождений не нашёл.
- Тесты на враждебные имена и пути в предупреждениях (`checkHostileWarnings`), на фильтры и fsmonitor в git, на симлинк-атаки (`TestSymlinkAttacksStayInsideRepo`).
- Под `-race`, `-shuffle`, `umask 077`, без `HOME` и в чужой локали всё зелёное.

Артефакты в scratchpad (`/tmp/claude-1000/-home-elia-Projects-auto-context/b403d57d-6fe7-40d7-9c97-ee52030c3f1f/scratchpad/`): `cover.out`, `coverall.out`, `funcall.txt`, `uncovered.txt`, копия репо с fuzz- и стресс-тестами в `fz/` (`internal/{rootindex,frontmatter,hashing,units}/fuzz_test.go`, `internal/units/rand_ignore_test.go`, `internal/project/conc_test.go`, исправленный `internal/hooks/hooks_test.go`).
