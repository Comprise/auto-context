# internal/turn
Правила хода без ввода-вывода: auto-ack, напоминания, новые и исчезнувшие юниты.

- `turn.go` — `Evaluate(Input) Outcome`, `View`, `DirStat`; `recordTurn`, `deletionTimes`, `pendingUnits`, `turnNew`, `listChangedParents`
- `turn_test.go` — табличные тесты правил на чистых входах

Подробно: context/internal/turn.md
