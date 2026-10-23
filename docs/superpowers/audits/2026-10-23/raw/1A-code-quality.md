# 1A. Качество кода (cmd/, internal/) — коммит af0cae8

Метод: прочитан весь непроверочный Go-код (~6000 строк). Цикломатическая сложность посчитана своим AST-скриптом
(if/for/range/case/&&/||; staticcheck, gocyclo не установлены). Каждая находка сверена с исходником; три подтверждены
одноразовыми тестами в копии пакета (файлы удалены, репозиторий не изменён). Находки, исправленные в af0cae8 по
docs/superpowers/audits/2026-10-20/, не повторяются. В конце — пункты прошлого аудита, которые всё ещё открыты.

Сводка: Critical 0 · High 2 · Medium 3 · Low 10 (+3 ещё открыты с прошлого аудита).

Самые сложные функции (cc / строк): `SessionStart` 34/124, `Discover` 30/87, `bracketClass` 30/89, `parseIgnore` 29/73,
`AddExclude` 26/89, `ignoreFile.match` 26/56, `writeChanges` 22/91, `runStatus` 22/77, `walk` 22/59, `addFile` 22/56.
Отмечены только те, где сложность мешает сопровождению. `bracketClass`, `parseIgnore` и `match` сложны из-за самой предметной
области (семантика wildmatch в git), хорошо прокомментированы и покрыты тестами, поэтому находками не считаются.

---

## High

### H1. `rootindex.fenced` работает за квадратичное время на содержимом репозитория: хук зависает до таймаута
`internal/rootindex/rootindex.go:102-124` (вызовы: `markers` :132, `Update` :196 и :203, `Check` :167)

Для каждой строки, открывающей fence, `fenced` просматривает все строки до конца файла в поисках закрывающей. Если
fence ни разу не закрывается, получается O(n²) по числу строк. Корневой CLAUDE.md/AGENTS.md — это содержимое репозитория
(до `MaxSize` = 2 MiB, то есть около 400 тыс. строк). В модели угроз репозиторий может быть враждебным клоном, и такой файл
вредоносен.

Проверено тестом: строки `~~~a` (закрывающая fence должна быть без info, поэтому ни одна не закрывается). 10 000 строк
(50 KB) — 0,68 с, 40 000 строк (200 KB) — 11,1 с. На 2 MiB экстраполяция даёт около 1100 с. `Update` вызывается в
SessionStart, а в Stop — через `AckTouched → RefreshIndex` при любой правке docs. `Check` вызывается в `status`. Хук
убивается по таймауту (60 с), `status` фактически висит. Если в файле нет маркеров, `Update` вызывает `fenced` второй раз
(:203).

Как исправить: один линейный проход со стеком открытой fence, как в CommonMark:
```go
func fenced(lines []string) (in []bool, open string) {
	in = make([]bool, len(lines))
	start, oc, on := -1, byte(0), 0
	for i, l := range lines {
		c, n, info := fence(l)
		switch {
		case start < 0 && c != 0 && !(c == '`' && strings.ContainsRune(info, '`')):
			start, oc, on = i, c, n
		case start >= 0 && c == oc && n >= on && info == "":
			for k := start; k <= i; k++ { in[k] = true }
			start = -1
		}
	}
	if start >= 0 {
		open = strings.Repeat(string(oc), on)
	}
	return in, open
}
```
Внимание: семантика здесь меняется. Сейчас незакрытая fence ничего не скрывает, и следующий за ней opener снова
рассматривается как открывающий. В линейном варианте незакрытая fence скрывает всё до конца файла, что соответствует
CommonMark. Сохранить текущее поведение можно так: заранее одним проходом снизу вверх посчитать для каждого (символ, длина)
индекс ближайшей следующей закрывающей строки. Нужен регрессионный тест на 200 KB, укладывающийся в бюджет времени.

### H2. `rootindex.sanitize` удаляет `<!--`/`-->` в цикле: квадратичное время на одной строке summary
`internal/rootindex/rootindex.go:69-75`

```go
for strings.Contains(s, "<!--") || strings.Contains(s, "-->") {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "<!--", ""), "-->", "")
}
return textx.Cap(strings.TrimSpace(s), maxSummary)
```
Длина обрезается (`Cap`) только после цикла. Summary — первая непустая строка после `# ` в `context/<unit>.md` (до 1 MiB,
это содержимое репозитория). Строка вида `"<"*k + "!--"*k` за один проход теряет одно вхождение, поэтому проходов k, и
каждый стоит O(n). Проверено: k=5000 (20 KB) — 24 мс, k=20000 (80 KB) — 0,36 с. На 1 MiB это около 60 с на один юнит при
каждом `Update` (SessionStart, Stop с auto-ack, `ack`).

Как исправить: обрезать до цикла (summary всё равно ≤ 200 рун) и удалять за один проход:
```go
func sanitize(s string) string {
	s = textx.Cap(textx.Clean(s), 4*maxSummary) // bound the work first
	for {
		t := strings.ReplaceAll(strings.ReplaceAll(s, "<!--", ""), "-->", "")
		if t == s { break }
		s = t
	}
	return textx.Cap(strings.TrimSpace(s), maxSummary)
}
```
Длина s ≤ 800 рун, поэтому цикл дешёвый. Обрезку лучше перенести ещё выше, в `frontmatter.Summary`, чтобы и `Clean`
не обрабатывал мегабайтную строку.

---

## Medium

### M1. `SessionStart` — функция на 124 строки с цикломатической сложностью 34
`internal/hooks/session_start.go:34-157`

Совмещает восемь задач: активацию и отказ, логирование и формулировку проблем конфига, проверку check-ignore, ошибку кеша,
предупреждения discover, очистку state, классификацию статусов (stale/missing/unusable с вложенным `switch` по Note),
orphans, обновление индекса с разбором ошибок, сохранение кеша и сборку ответа. Каждая проблема добавляется в `problems`
тем же шаблоном «залогировать + добавить текст». В прошлом аудите такая же находка была по `Stop` (#8), её исправили, а
`SessionStart` стал самой сложной функцией проекта. Добавить новую проверку и не сломать порядок сообщений трудно, а
юнит-тестировать отдельные ветки нельзя.

Как исправить: выделить чистые шаги, которые возвращают тексты проблем, и оставить в `SessionStart` оркестрацию:
```go
func SessionStart(env Env, in Input) ([]byte, error) {
	// ... activation, load, logProblems, user ...
	if p.ConfigBroken() {
		return sessionNotice(prefixed(logProblems), user)
	}
	var problems []string
	problems = append(problems, envProblems(env, p, cli)...)          // check-ignore, cache, discover, state cleanup
	statuses := p.Statuses()
	stale, missing, unusable := classifyStatuses(statuses)             // pure, testable
	problems = append(problems, unitProblems(env, p, unusable, cli)...) // unusable docs, orphans, warning count
	problems = append(problems, indexProblem(env, p, statuses)...)
	problems = append(problems, cacheSaveProblem(env, p)...)
	return buildSessionAnswer(stale, missing, append(problems, logProblems...), p.Config(), env.Agent, user)
}
```
`classifyStatuses` (сейчас :90-108) можно покрыть табличным тестом без файловой системы.

### M2. `units.Tree` смешивает изменяемое состояние сопоставителя, нужное при построении, с результатом, а «читающие» методы его меняют
`internal/units/units.go:53-72`; изменения в `Classify` (:108) → `untracked` → `gitIgnored` (`ignore.go:75-96`: `globBudget`,
`budgetWarned`, `warn`) и `skipDir` (`walk.go:44-59`: map `skipped`)

В `Tree` 20 полей. Половина нужна только Discover и сопоставителю .gitignore (`ignores`, `rules`, `globRules`, `globBudget`,
`budgetWarned`, `ctxCodeWarned`, `skipped`, `excludes`, `tracks`). При этом `Classify`, `Project.Orphans` и Stop при
классификации diff после Discover продолжают тратить общий бюджет glob, дописывать предупреждения и наполнять кеш
`skipped`. Последствия:
- Комментарий `project.Project` (`project.go:64-65`) говорит «read-only after Open (but for the lazily loaded cache)», но
  это не так: `Warnings()`/`WarningCount()` зависят от того, сколько путей успели классифицировать.
- `Tree` нельзя использовать из нескольких горутин даже для чтения (`units.go:51-52`). Это ограничение мешает, например,
  распараллелить классификацию в Stop.
- Бюджет, исчерпанный в Discover, молча отключает glob-правила для последующих `Classify` в том же процессе.

Как исправить: вынести изменяемое состояние в отдельный `matcher`, который живёт только внутри Discover. `Classify` должен
получать собственный бюджет на вызов или работать с замороженной копией и ничего не писать в `Tree.warnings`:
```go
type matcher struct {
	ignores map[string]ignoreFile
	excludes, tracks patternSet
	budget int
	skipped map[string]bool
	warn func(string)
}
type Tree struct { // immutable after Discover
	root string; cfg config.Config
	units map[string]Unit; files map[string][]string
	tracked []string; stats map[string]Stat
	warnings []string; warnCount int; viaGit bool
	m *matcher // used by Classify with a fresh per-call budget, warnings discarded
}
```
Если переделка слишком большая, нужно хотя бы исправить комментарий `Project` и задокументировать, что `Classify` меняет
бюджет и список предупреждений.

### M3. `config.Load` разбирает YAML до четырёх раз с разной семантикой: дублированный ключ даёт ложное «wrong type» и сбрасывает оба значения
`internal/config/config.go:113-157` (`indexFileKeys` :121/:467, `yaml.Unmarshal` в map :125, `decodeConfig` :143/:177
делает ещё `Unmarshal` в `Config`, `Unmarshal` в `yaml.Node` :189 и `Decode` по каждому ключу :196); `typeProblem` :216

Каждый проход по-своему обрабатывает ошибки: `TypeError` то игнорируется, то превращается в Problem, то вызывает сброс поля.
Отдельная ветка `indexFileKeys` существует только потому, что общий путь не распознаёт дубликаты. yaml.v3 сообщает о
дублированном ключе как о `TypeError` («mapping key "x" already defined»), а `typeProblem` переписывает его в
«a value of the wrong type».

Проверено тестом: `min_files: 3\nmin_files: 7\nexclude: [a/**]\nexclude: [b/**]` →
`MinFiles=5`, `Exclude` = значения по умолчанию, Problems: `line 2: a value of the wrong type; line 4: a value of the wrong type`.
Пользователь видит неверную причину, хотя оба значения корректны, и получает значения по умолчанию вместо любого из своих.

Как исправить: разобрать файл один раз в `yaml.Node`. Дубликаты верхнего уровня определять по Node, в общем виде, как
сейчас сделано для `index_file`, и выдавать отдельное сообщение. Затем декодировать по ключам из того же Node:
```go
var doc yaml.Node
if err := yaml.Unmarshal(data, &doc); err != nil { return Default(), nil, err }
m := topMapping(&doc) // nil for empty/null
seen := map[string]int{}
cfg := Default()
var pr Problems
for i := 0; m != nil && i+1 < len(m.Content); i += 2 {
	k := m.Content[i].Value
	if seen[k]++; seen[k] == 2 {
		if k == "index_file" { return Default(), nil, ErrBadIndexFile }
		pr = append(pr, fmt.Sprintf("line %d: duplicate key %s", m.Content[i].Line, safeKey(k)))
		continue
	}
	if !knownKeys[k] { pr = append(pr, "unknown key "+safeKey(k)); continue }
	one := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: m.Content[i : i+2]}
	if err := one.Decode(&cfg); err != nil {
		resetField(&cfg, ptr(Default()), k)
		pr = append(pr, fmt.Sprintf("line %d: %s has a value of the wrong type", m.Content[i].Line, k))
	}
}
```
Merge-ключи (`<<`) придётся раскрывать явно или оставить на `Decode` целого Node: это решение нужно зафиксировать тестом.

---

## Low

### L1. Нефиксированные строки формата: путь CLI вклеивается в формат `fmt.Sprintf`
`internal/hooks/session_start.go:63, :115, :124, :180`

```go
user = fmt.Sprintf("%s has %d invalid setting(s) ... run `"+cli+" status` ...", config.FileName, len(...))
```
Для Codex `cli` — это `CLAUDE_PLUGIN_ROOT/bin/auto-context`. `codexCLI` (`agent.go:379-393`) пропускает `%` (отбрасываются
только управляющие символы), поэтому каталог вида `…/100%d…` превратит текст в `%!d(MISSING)`. `go vet` такое не ловит,
потому что у вызова есть аргументы. Остальной код собирает тексты конкатенацией.

Как исправить: передавать `cli` аргументом.
```go
user = fmt.Sprintf("%s has %d invalid setting(s) that were ignored (the other settings apply); run `%s status` to see them and fix the file.",
	config.FileName, len(p.ConfigProblems()), cli)
```

### L2. `eolOf` и константа `bom` скопированы побайтно в два пакета
`internal/frontmatter/frontmatter.go:208, :261-267`; `internal/rootindex/rootindex.go:41-47, :78`

Это один и тот же код обработки CRLF и BOM, добавленный в af0cae8. Если правило изменится (например, «доминирующий» перевод
строки вместо первой строки), две копии разойдутся.

Как исправить: перенести в `textx` (пакет уже импортируется rootindex) и использовать в обоих местах:
```go
// textx
const BOM = "﻿"
func EOL(s string) string { if i := strings.IndexByte(s, '\n'); i > 0 && s[i-1] == '\r' { return "\r\n" }; return "\n" }
```

### L3. Три параллельных преобразования «ошибка → фиксированная категория» и дважды продублированное сравнение со stderr git
`internal/hooks/stop.go:414-430` (`errCategory`), `internal/units/walk.go:448-460` (`errKind`), `internal/config/config.go:225-247`
(`Describe`); `internal/project/project.go:132-133` и `internal/units/gitfiles.go:34` (`strings.Contains(err.Error(), "not a git repository")`)

У всех трёх функций одни и те же ветки (`ErrPermission`, `fsutil.ErrOutside`, `ErrNotRegular`, `ErrTooLarge`), но тексты
разные: «a file links outside the repository», «leads outside the repository or into .git», «it leads outside the repository or
into .git». Это инвариант безопасности (путь из репозитория не попадает в контекст), и он размазан по трём местам. Проверка
«not a repo» опирается на текст stderr, хотя gitx уже фиксирует `LC_ALL=C` специально для таких сравнений.

Как исправить: `fsutil.Category(err) string` для общих случаев, а пакеты добавляют только свои (git timeout, yaml).
В gitx добавить предикат:
```go
// gitx
func NotRepo(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == 128 && strings.Contains(e.Stderr, "not a git repository")
}
```
Заодно сравнение пойдёт по `e.Stderr`, а не по всему `Error()`, где есть ещё и аргументы.

### L4. Шаблон «загрузить журнал + залогировать "starting empty"» повторён 4 раза; `LoadJournal` нормализует не все списки
`internal/hooks/stop.go:103-106, :124-127, :212-215`; `internal/hooks/prompt_submit.go:25-28`; `internal/session/session.go:245`

Сообщения отличаются только префиксом хука. `LoadJournal` превращает в множества `CodeChanged`, `DocsTouched` и
`AlreadyNotified`, а `AckFailNotified` нет, хотя `Save` (:260) нормализует все четыре. Сейчас это безвредно (`Minus` и
`AddUnique` сортируют сами), но контракт «списки — отсортированные множества» в комментарии к `LoadJournal` нарушен.

Как исправить:
```go
func loadJournal(root, id, hook string) session.Journal {
	j, err := session.LoadJournal(root, id)
	if err != nil { logx.Errorf(root, "%s: journal unreadable, starting empty: %v", hook, err) }
	return j
}
// session.go:245
j.AckFailNotified = AddUnique(j.AckFailNotified)
```

### L5. `writeChanges` — 91 строка, cc 22: в одной функции и выбор диапазона коммитов, и проверка доверия фильтрам, и исполнение
`internal/project/changes.go:54-144`

Выбор раздела log (по коммиту, по дате или последние) с текстами причин (`dateNote`) переплетён с вызовами git и выводом.
Логику выбора диапазона нельзя протестировать без настоящего репозитория. Есть и мелкий запах: `append(logArgs, …)`
(:105, :109) безопасен только потому, что у литерала `len == cap`.

Как исправить: выделить чистую функцию `logSection(unit, ctx string, ack ack, hasCommits bool, ancestor func() (bool, bool)) (section, bool)`
и создавать аргументы явно: `slices.Concat(logArgs, []string{ack.commit + "..HEAD"})`.

### L6. Разбор `hook` в `run` трижды повторяет одну ветку, а таблицы хендлеров и бюджетов — две параллельные map с особым случаем
`cmd/auto-context/main.go:71-88`, `:114-147`

`logHook(config.FindRoot(cwd), "hook: bad arguments"); return 0` встречается три раза. `hookHandlers` и `hookBudget`
должны иметь одинаковые ключи, но этого ничто не проверяет, кроме тестов. Исключение для Codex SessionEnd вынесено в
отдельную константу и функцию.

Как исправить:
```go
type hookSpec struct {
	h      func(hooks.Env, hooks.Input) ([]byte, error)
	budget map[hooks.Agent]time.Duration // hooks.Claude as the default
}
var hookTable = map[string]hookSpec{ "session-end": {hooks.SessionEnd, map[hooks.Agent]time.Duration{hooks.Claude: 8 * time.Second, hooks.Codex: 2 * time.Second}}, ... }

func parseHookArgs(rest []string) (event string, a hooks.Agent, ok bool) { ... } // one "bad arguments" exit
```

### L7. `Config.fix` выбирает текст проблемы по префиксу текста ошибки
`internal/config/config.go:300-303`

```go
if !strings.HasPrefix(reason, "must") { reason = "invalid pattern" }
```
Это управление потоком через текст сообщения: если переформулировать `ValidatePattern` («must be shorter than…»), в контекст
Claude попадёт `%q` с паттерном из репозитория. Именно этот инвариант (значения не цитируются) комментарий выше и защищает.

Как исправить: сделать проверки, которые возвращают уже безопасную причину, а цитирующий `ValidatePattern` оставить для CLI.
```go
type check func(string) (reason string, ok bool) // reason never quotes the value
{"include", &c.Include, &def.Include, func(p string) (string, bool) { return "invalid pattern", ValidatePattern(p) == nil }},
```

### L8. Сообщение `docstate.Ack` для отсутствующего документа советует то, что для `_root` невозможно
`internal/docstate/docstate.go:149`

`ack _root` без `context/_root.md` выдаёт «create docs or run auto-context ack --exclude _root», а `Exclude` (`project.go:305-307`)
это сразу отвергает. Кроме того, имя юнита в подсказке не экранируется для shell, в отличие от `cmdArg` в Stop.

Как исправить:
```go
case DocAbsent:
	if unit == units.RootUnit {
		return errors.New("no context/_root.md: create it (the root unit cannot be excluded)")
	}
	return fmt.Errorf("no context/%s.md: create docs or run auto-context ack --exclude %s", unit, unit)
```

### L9. `ack --exclude` при сломанном конфиге печатает голую ошибку yaml, не называя файл; путь мимо `ErrConfigBroken`
`cmd/auto-context/main.go:291-295` → `project.Exclude` → `config.AddExclude` (`config.go:337-340`)

Обычный `ack` при сломанном конфиге отвечает `ErrConfigBroken` (`main.go:308-311`), `status` вызывает `config.Describe`.
У `--exclude` вывод получается вида `yaml: line 2: did not find expected key`: без имени файла и без подсказки, что
отслеживание на паузе.

Как исправить: в `runAck` проверять `p.ConfigBroken()` до ветки `exclude`, или в `AddExclude` оборачивать ошибку:
`return fmt.Errorf("%s cannot be used: %s", FileName, Describe(err))`.

### L10. Пустой `session_id` не отвергается: все такие сессии делят файлы `state/_.snapshot` и `_.json`
`internal/session/session.go:60-62`; хуки `Stop`, `PromptSubmit`, `SessionEnd` используют `in.SessionID` без проверки

Если payload пришёл без `session_id` (сбой агента, будущий формат, ручной запуск), параллельные сессии будут сравнивать
дерево со снимками друг друга и сбрасывать журналы друг друга. Ошибки нет, ничего не логируется.

Как исправить: в `runHook` или в хуках, работающих с состоянием, логировать и пропускать вызов:
```go
if in.SessionID == "" { return nil, errors.New("no session_id in the payload") } // logged by runHook, exit 0
```

---

## Всё ещё открыто из аудита 2026-10-20 (quality.md), в счётчики не входит

- #16: `project.Active` и `project.ActivationRefused` (`internal/project/project.go:31-62`) повторяют один и тот же цикл по
  маркерам. Можно сделать одну функцию `markerState(root) (active, refused bool)`.
- #16: четыре почти одинаковые структуры ответа хуков: `stopOutput` (`stop.go:23`), `sessionOutput` (`session_start.go:20`),
  `preToolUseOutput` (`pre_tool_use.go:16`), `permissionRequestOutput` (`permission_request.go:3`).
- #15: `hashing.Cache.Len` (`internal/hashing/hashing.go:104`) используется только тестами.

## Проверено, находок нет

- Обработка ошибок записи: `errWriter` в `WriteChanges`, `writeAtomic` (удаление tmp при ошибке), `Stop` (молчит, если
  журнал не сохранился, чтобы не зациклиться), паники в `runHook` перехватываются и логируются.
- `lfWriter` (CRLF через границы чанков), `limitWriter`, `StripControls`: корректны. `parallelLstat` и `Prewarm`
  синхронизированы правильно, `patternSet.match` чистая и безопасна для параллельного вызова из `interesting`.
- Пункты прошлого аудита #8 (Stop), #9/#10 (разделение walk.go, `classifyFile`), #11-#14, а также основная часть #15-#17
  исправлены.
