package turn

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"auto-context/internal/config"
	"auto-context/internal/filestat"
	"auto-context/internal/session"
	"auto-context/internal/testutil"
	"auto-context/internal/units"
)

func TestMain(m *testing.M) { testutil.Main(m) }

// newTree builds the hooks' fixture repo (units _root and svc, and lib with
// withLib) and discovers its unit tree.
func newTree(t *testing.T, withLib bool) *units.Tree {
	t.Helper()
	root := t.TempDir()
	testutil.WriteFile(t, root, "CLAUDE.md", "# Project\n")
	testutil.WriteFile(t, root, "svc/go.mod", "module svc\n")
	testutil.WriteFile(t, root, "svc/main.go", "package main\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n\nRoot summary.\n")
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nService summary.\n")
	if withLib {
		testutil.WriteFile(t, root, "lib/go.mod", "module lib\n")
		testutil.WriteFile(t, root, "lib/lib.go", "package lib\n")
	}
	tree, err := units.DiscoverContext(context.Background(), root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestPendingUnits(t *testing.T) {
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	only := func(u string) func(string) bool { return func(x string) bool { return x == u } }
	cases := []struct {
		name     string
		j        session.Journal
		acked    []string
		failed   map[string]string
		isUnit   func(string) bool
		upToDate func(string) bool
		want     []string
	}{
		{"changed, stale", session.Journal{CodeChanged: []string{"a"}}, nil, nil, yes, no, []string{"a"}},
		{"acked", session.Journal{CodeChanged: []string{"a"}}, []string{"a"}, nil, yes, no, nil},
		{"already notified", session.Journal{CodeChanged: []string{"a"}, AlreadyNotified: []string{"a"}}, nil, nil, yes, no, nil},
		{"docs already match", session.Journal{CodeChanged: []string{"a"}}, nil, nil, yes, yes, nil},
		{"failed ack is pending even if up to date", session.Journal{CodeChanged: []string{"a"}}, nil, map[string]string{"a": "x"}, yes, yes, []string{"a"}},
		{"no longer a unit", session.Journal{CodeChanged: []string{"a", "b"}}, nil, nil, only("b"), no, []string{"b"}},
		{"failed ack after the notice is reported", session.Journal{CodeChanged: []string{"a"}, AlreadyNotified: []string{"a"}}, nil, map[string]string{"a": "x"}, yes, no, []string{"a"}},
		{"failed ack reported once", session.Journal{CodeChanged: []string{"a"}, AlreadyNotified: []string{"a"}, AckFailNotified: []string{"a"}}, nil, map[string]string{"a": "x"}, yes, no, nil},
		{"failed ack without a code change", session.Journal{}, nil, map[string]string{"b": "x"}, yes, no, []string{"b"}},
	}
	for _, c := range cases {
		if got := pendingUnits(c.j.CodeChanged, pendingQuery{notified: c.j.AlreadyNotified, failNotified: c.j.AckFailNotified, acked: c.acked, failed: c.failed, isUnit: c.isUnit, upToDate: c.upToDate}); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// recordTurn: code edits go to the journal, docs count only if not older than
// the unit's newest code edit; CLAUDE.md edits never count.
func TestRecordTurn(t *testing.T) {
	tree := newTree(t, true)
	before := session.FromMap(map[string]filestat.Stat{
		"svc/main.go": {MtimeNs: 1, Size: 1}, "lib/lib.go": {MtimeNs: 1, Size: 1},
		"context/svc.md": {MtimeNs: 1, Size: 1}, "context/lib.md": {MtimeNs: 1, Size: 1}, "svc/CLAUDE.md": {MtimeNs: 1, Size: 1},
	})
	cur := session.FromMap(map[string]filestat.Stat{
		"svc/main.go": {MtimeNs: 10, Size: 1}, "lib/lib.go": {MtimeNs: 10, Size: 1},
		"context/svc.md": {MtimeNs: 20, Size: 1}, // after svc's code: counts
		"context/lib.md": {MtimeNs: 5, Size: 1},  // before lib's code: does not
		"svc/CLAUDE.md":  {MtimeNs: 30, Size: 2},
	})
	var j session.Journal
	fresh := recordTurn(tree, before, cur, turnEnv{0, noDirs, nil, nil}, nil, &j).fresh
	if !slices.Equal(fresh, []string{"svc"}) || !slices.Equal(j.CodeChanged, []string{"lib", "svc"}) || !slices.Equal(j.DocsTouched, []string{"lib", "svc"}) {
		t.Errorf("fresh=%v journal=%+v", fresh, j)
	}
}

// recordTurn stops when the hook's time is up: the changes not looked at
// are not recorded (the snapshot stays, so the next Stop sees them), and the
// result says so.
func TestRecordTurnStopsWhenTimeUp(t *testing.T) {
	tree := newTree(t, true)
	before := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 1, Size: 1}, "lib/lib.go": {MtimeNs: 1, Size: 1}})
	cur := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 10, Size: 1}, "lib/lib.go": {MtimeNs: 10, Size: 1}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var j session.Journal
	tc := recordTurn(tree, before, cur, turnEnv{stat: noDirs, ctx: ctx}, nil, &j)
	if !tc.timeUp || len(j.CodeChanged) != 0 {
		t.Errorf("timeUp=%v journal=%+v", tc.timeUp, j)
	}
}

// errAfter is a context whose Err is nil for the first n calls, then
// DeadlineExceeded: the hook's time ends in the middle of a loop.
type errAfter struct {
	context.Context
	n int
}

func (c *errAfter) Err() error {
	if c.n--; c.n < 0 {
		return context.DeadlineExceeded
	}
	return nil
}

// A diff cut short by the hook's time gives no auto-ack candidates: the
// sorted diff reaches context/svc.md before svc/main.go, whose newer edit
// would bar the ack, and marking the doc seen would hide the feedback on the
// next Stop.
func TestRecordTurnTimeUpMidDiffNoFresh(t *testing.T) {
	tree := newTree(t, false)
	b := map[string]filestat.Stat{"svc/main.go": {MtimeNs: 1, Size: 1}, "context/svc.md": {MtimeNs: 1, Size: 1}}
	c := map[string]filestat.Stat{"svc/main.go": {MtimeNs: 100, Size: 1}, "context/svc.md": {MtimeNs: 20, Size: 1}}
	for i := range 63 { // diff entries 0..62; context/svc.md is 63, svc/main.go 64
		c[fmt.Sprintf("aaa/x%02d.go", i)] = filestat.Stat{MtimeNs: 5, Size: 1}
	}
	var j session.Journal
	te := turnEnv{stat: noDirs, ctx: &errAfter{Context: context.Background(), n: 1}}
	tc := recordTurn(tree, session.FromMap(b), session.FromMap(c), te, nil, &j)
	if !tc.timeUp || len(tc.fresh) != 0 {
		t.Errorf("timeUp=%v fresh=%v, want time up and no fresh", tc.timeUp, tc.fresh)
	}
}

func TestRecordTurnOldMtime(t *testing.T) {
	tree := newTree(t, true)
	before := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 1, Size: 1}, "context/svc.md": {MtimeNs: 1, Size: 1}})
	cur := session.FromMap(map[string]filestat.Stat{
		"svc/main.go": {MtimeNs: 1, Size: 2}, "svc/moved.go": {MtimeNs: 5, Size: 1},
		"context/svc.md": {MtimeNs: 20, Size: 2},
	})
	var j session.Journal
	if fresh := recordTurn(tree, before, cur, turnEnv{10, noDirs, nil, nil}, nil, &j).fresh; len(fresh) != 0 ||
		!slices.Equal(j.CodeChanged, []string{"svc"}) || !slices.Equal(j.DocsTouched, []string{"svc"}) {
		t.Errorf("fresh=%v journal=%+v", fresh, j)
	}
	var j0 session.Journal
	if fresh := recordTurn(tree, before, cur, turnEnv{0, noDirs, nil, nil}, nil, &j0).fresh; !slices.Equal(fresh, []string{"svc"}) {
		t.Errorf("rule off: fresh=%v", fresh)
	}
	// A modified (not new) file with an old mtime bars the ack too.
	cur = session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 5, Size: 2}, "context/svc.md": {MtimeNs: 20, Size: 2}})
	var j1 session.Journal
	if fresh := recordTurn(tree, before, cur, turnEnv{10, noDirs, nil, nil}, nil, &j1).fresh; len(fresh) != 0 {
		t.Errorf("modified old file: fresh=%v", fresh)
	}
}

// noDirs is a DirStat for recordTurn tests that delete nothing.
func noDirs(string) (int64, bool, error) { return 0, false, errors.New("unexpected dir stat") }

// A deleted code file is ordered by the mtime of its nearest surviving
// directory; a directory that cannot order it bars the auto-ack.
func TestRecordTurnDeletedFile(t *testing.T) {
	tree := newTree(t, true)
	before := session.FromMap(map[string]filestat.Stat{
		"svc/main.go": {MtimeNs: 1, Size: 1}, "svc/a/b/x.go": {MtimeNs: 1, Size: 1}, "svc/a/b/y.go": {MtimeNs: 1, Size: 1},
		"context/svc.md": {MtimeNs: 1, Size: 1},
	})
	cur := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 1, Size: 1}, "context/svc.md": {MtimeNs: 20, Size: 2}})
	cases := []struct {
		name  string
		dirs  map[string]int64 // surviving directories; missing ones are climbed past
		err   error
		fresh bool
	}{
		{"deleted before the docs edit", map[string]int64{"svc/a/b": 15}, nil, true},
		{"deleted after the docs edit", map[string]int64{"svc/a/b": 25}, nil, false},
		{"directory gone, parent orders", map[string]int64{"svc": 15}, nil, true},
		{"directory gone, parent after the docs", map[string]int64{"svc": 25}, nil, false},
		{"only the root survives", map[string]int64{".": 15}, nil, true},
		{"directory older than the turn", map[string]int64{"svc/a/b": 5}, nil, false},
		{"stat fails", nil, errors.New("boom"), false},
		{"nothing survives", map[string]int64{}, nil, false},
	}
	for _, c := range cases {
		calls := map[string]int{}
		stat := func(dir string) (int64, bool, error) {
			calls[dir]++
			if c.err != nil {
				return 0, false, c.err
			}
			m, ok := c.dirs[dir]
			return m, ok, nil
		}
		var j session.Journal
		fresh := recordTurn(tree, before, cur, turnEnv{10, stat, nil, nil}, nil, &j).fresh
		if got := len(fresh) == 1; got != c.fresh || !slices.Equal(j.CodeChanged, []string{"svc"}) {
			t.Errorf("%s: fresh=%v journal=%+v", c.name, fresh, j)
		}
		for dir, n := range calls {
			if n > 1 {
				t.Errorf("%s: %s statted %d times", c.name, dir, n)
			}
		}
	}
}

// A unit is new only if no rule could have made it one by the turn-start
// snapshot, whatever rule makes it one now: no marker then, fewer than
// min_files source files directly in it then (deleted ones count), and, for
// an include unit, no file then. A context doc in the snapshot means it is
// not new (HS-4).
func TestTurnNew(t *testing.T) {
	cfg := config.Default()
	cfg.MinFiles = 3
	files := map[string][]string{
		"mod/old":   {"mod/old/go.mod", "mod/old/a.go"},
		"mod/new":   {"mod/new/go.mod", "mod/new/a.go"},
		"mod/fresh": {"mod/fresh/go.mod"},
		"cnt/old":   {"cnt/old/a.go", "cnt/old/b.go", "cnt/old/c.go", "cnt/old/d.go"},
		"cnt/grown": {"cnt/grown/a.go", "cnt/grown/b.go", "cnt/grown/c.go"},
		"cnt/sub":   {"cnt/sub/a.go", "cnt/sub/b.go", "cnt/sub/x/c.go"},
		"inc/old":   {"inc/old/a.go", "inc/old/new.go"},
		"inc/new":   {"inc/new/a.go"},
		"docced":    {"docced/go.mod"},
		// A marker added to a unit that already had min_files files.
		"cnt/marked": {"cnt/marked/go.mod", "cnt/marked/a.go", "cnt/marked/b.go", "cnt/marked/c.go"},
		// A marker removed from a unit that still has enough files.
		"mod/unmarked": {"mod/unmarked/a.go", "mod/unmarked/b.go", "mod/unmarked/c.go"},
		// Files deleted this turn count for then: it had 3 files.
		"cnt/shuffled": {"cnt/shuffled/a.go", "cnt/shuffled/new1.go", "cnt/shuffled/new2.go"},
	}
	before := session.FromMap(map[string]filestat.Stat{
		"mod/old/go.mod": {}, "mod/new/a.go": {},
		"cnt/old/a.go": {}, "cnt/old/b.go": {}, "cnt/old/c.go": {}, // d.go added: was a unit already
		"cnt/grown/a.go": {}, "cnt/grown/b.go": {}, // c.go added: becomes a unit
		"cnt/sub/a.go": {}, "cnt/sub/b.go": {}, "cnt/sub/x/c.go": {}, // only 2 of its own files
		"inc/old/a.go":      {},
		"context/docced.md": {},
		"cnt/marked/a.go":   {}, "cnt/marked/b.go": {}, "cnt/marked/c.go": {},
		"mod/unmarked/go.mod": {}, "mod/unmarked/a.go": {},
		"cnt/shuffled/a.go": {}, "cnt/shuffled/gone1.go": {}, "cnt/shuffled/gone2.go": {},
		"cnt/shuffled/CLAUDE.md": {}, // the index file is no source
	})
	of := func(u string) []string { return files[u] }
	for _, c := range []struct {
		u      string
		reason units.Reason
		want   bool
	}{
		{"mod/old", units.ReasonModule, false},
		{"mod/new", units.ReasonModule, true},
		{"mod/fresh", units.ReasonModule, true},
		{"cnt/old", units.ReasonFiles, false},
		{"cnt/grown", units.ReasonFiles, true},
		{"cnt/sub", units.ReasonFiles, true},
		{"inc/old", units.ReasonInclude, false},
		{"inc/new", units.ReasonInclude, true},
		{"docced", units.ReasonModule, false},
		{"_root", units.ReasonRoot, false},
		{"cnt/marked", units.ReasonModule, false},
		{"mod/unmarked", units.ReasonFiles, false},
		{"cnt/shuffled", units.ReasonFiles, false},
	} {
		if got := turnNew(c.u, c.reason, before, directSources(before, "CLAUDE.md"), of, cfg); got != c.want {
			t.Errorf("turnNew(%s) = %v, want %v", c.u, got, c.want)
		}
	}
}

// Evaluate runs the effects in order (record, ack, prewarm), reports a stale
// changed unit as pending and marks it notified in the returned journal.
func TestEvaluatePendingAndNotify(t *testing.T) {
	tree := newTree(t, true)
	// lib/go.mod at the turn start: lib is no new unit.
	before := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 1, Size: 1}, "lib/go.mod": {MtimeNs: 1, Size: 1}, "lib/lib.go": {MtimeNs: 1, Size: 1}, "context/svc.md": {MtimeNs: 1, Size: 1}})
	cur := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 10, Size: 1}, "lib/go.mod": {MtimeNs: 1, Size: 1}, "lib/lib.go": {MtimeNs: 10, Size: 1}, "context/svc.md": {MtimeNs: 20, Size: 2}})
	var calls []string
	out := Evaluate(Input{
		Tree: tree, Config: config.Default(), Before: before, Cur: cur,
		Journal:  session.Journal{Paused: true},
		DirStat:  noDirs,
		UpToDate: func(string) bool { return false },
		Recorded: func(j session.Journal, timeUp bool) {
			calls = append(calls, "recorded")
			if j.Paused || timeUp || !slices.Equal(j.CodeChanged, []string{"lib", "svc"}) {
				t.Errorf("recorded journal %+v, timeUp %v", j, timeUp)
			}
		},
		Ack: func(c []string) ([]string, map[string]string) {
			calls = append(calls, "ack")
			if !slices.Equal(c, []string{"svc"}) {
				t.Errorf("ack candidates %v", c)
			}
			return []string{"svc"}, map[string]string{}
		},
		Prewarm: func(us []string) { calls = append(calls, "prewarm") },
	})
	if !slices.Equal(calls, []string{"recorded", "ack", "prewarm"}) {
		t.Errorf("calls %v", calls)
	}
	if !slices.Equal(out.AutoAck, []string{"svc"}) || !slices.Equal(out.Acked, []string{"svc"}) || !slices.Equal(out.Pending, []string{"lib"}) || len(out.ListChanged) != 0 || out.Quiet() {
		t.Errorf("outcome %+v", out)
	}
	if !slices.Equal(out.Journal.AlreadyNotified, []string{"lib"}) {
		t.Errorf("notified %v", out.Journal.AlreadyNotified)
	}
}

// A failed auto-ack is pending and recorded as reported; nothing to report
// leaves the journal without notifications.
func TestEvaluateFailedAckAndQuiet(t *testing.T) {
	tree := newTree(t, false)
	before := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 1, Size: 1}, "context/svc.md": {MtimeNs: 1, Size: 1}})
	cur := session.FromMap(map[string]filestat.Stat{"svc/main.go": {MtimeNs: 10, Size: 1}, "context/svc.md": {MtimeNs: 20, Size: 2}})
	in := Input{Tree: tree, Config: config.Default(), Before: before, Cur: cur, DirStat: noDirs,
		UpToDate: func(string) bool { return true },
		Ack: func([]string) ([]string, map[string]string) {
			return nil, map[string]string{"svc": "permission denied"}
		},
	}
	out := Evaluate(in)
	if !slices.Equal(out.Pending, []string{"svc"}) || out.Failed["svc"] != "permission denied" ||
		!slices.Equal(out.Journal.AckFailNotified, []string{"svc"}) || !slices.Equal(out.Journal.AlreadyNotified, []string{"svc"}) {
		t.Errorf("outcome %+v", out)
	}
	in.Ack = func(c []string) ([]string, map[string]string) { return c, map[string]string{} }
	if out := Evaluate(in); !out.Quiet() || len(out.Journal.AlreadyNotified) != 0 {
		t.Errorf("want quiet: %+v", out)
	}
}
