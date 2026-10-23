package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"auto-context/internal/filestat"
)

func TestJournalRoundTrip(t *testing.T) {
	root := t.TempDir()
	j := Journal{Version: 1, CodeChanged: []string{"a.go"}, DocsTouched: []string{"x"}, AlreadyNotified: []string{"y"}}
	if err := j.Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadJournal(root, "s"); err != nil || !reflect.DeepEqual(got, j) {
		t.Fatalf("got %+v", got)
	}
}

func TestJournalEmptySerializesArrays(t *testing.T) {
	root := t.TempDir()
	if err := (Journal{}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".auto-context", "state", "s.json"))
	for _, k := range []string{`"code_changed":[]`, `"docs_touched":[]`, `"already_notified":[]`} {
		if !strings.Contains(strings.ReplaceAll(string(data), " ", ""), k) {
			t.Fatalf("missing %s in %s", k, data)
		}
	}
}

func TestJournalBrokenIsEmpty(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".auto-context", "state", "s.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("{not json"), 0o644)
	if got, err := LoadJournal(root, "s"); err == nil || got.CodeChanged != nil || got.DocsTouched != nil || got.AlreadyNotified != nil {
		t.Fatalf("got %+v err %v, want empty and an error", got, err)
	}
	if got, err := LoadJournal(root, "absent"); err != nil || len(got.CodeChanged) != 0 {
		t.Fatal("absent")
	}
}

func TestJournalsIsolatedBySession(t *testing.T) {
	root := t.TempDir()
	Journal{CodeChanged: []string{"1"}}.Save(root, "s1")
	Journal{CodeChanged: []string{"2"}}.Save(root, "s2")
	Snapshot{{"a", filestat.Stat{MtimeNs: 1, Size: 1}}}.Save(root, "s1")
	Snapshot{{"b", filestat.Stat{MtimeNs: 1, Size: 1}}}.Save(root, "s2")
	if err := Clear(root, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := Clear(root, "s1"); err != nil {
		t.Fatalf("second clear: %v", err)
	}
	if j, _ := LoadJournal(root, "s1"); len(j.CodeChanged) != 0 {
		t.Fatal("s1 not cleared")
	}
	if _, err := LoadSnapshot(root, "s1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("s1 snapshot: %v", err)
	}
	if j, _ := LoadJournal(root, "s2"); !reflect.DeepEqual(j.CodeChanged, []string{"2"}) {
		t.Fatal("s2 touched")
	}
	if _, err := LoadSnapshot(root, "s2"); err != nil {
		t.Fatalf("s2 snapshot: %v", err)
	}
}

func TestSessionIDSanitized(t *testing.T) {
	root := t.TempDir()
	if err := (Journal{CodeChanged: []string{"a"}}).Save(root, "../x"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".auto-context", "state"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	if n := entries[0].Name(); !strings.HasPrefix(n, "___x-") || !strings.HasSuffix(n, ".json") {
		t.Fatalf("name %s", entries[0].Name())
	}
	if _, err := os.Stat(filepath.Join(root, "x.json")); err == nil {
		t.Fatal("escaped")
	}
}

func TestMinusAndAddUnique(t *testing.T) {
	got := AddUnique([]string{"b", "a"}, "c", "a", "b")
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatal(got)
	}
	got = Minus([]string{"a", "b", "c", "d"}, []string{"b"}, []string{"d", "z"})
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatal(got)
	}
	if len(Minus(nil, []string{"a"})) != 0 || len(AddUnique(nil)) != 0 {
		t.Fatal("empty")
	}
}

// Load normalizes the lists, and the methods keep them sorted sets.
func TestJournalSets(t *testing.T) {
	root := t.TempDir()
	if err := (Journal{CodeChanged: []string{"b", "a", "b"}}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	j, err := LoadJournal(root, "s")
	if err != nil || !reflect.DeepEqual(j.CodeChanged, []string{"a", "b"}) {
		t.Fatalf("%v %v", j.CodeChanged, err)
	}
	j.AddCode("c", "a")
	j.AddDocs("x")
	j.Notify("y", "y")
	if !reflect.DeepEqual(j.CodeChanged, []string{"a", "b", "c"}) || !reflect.DeepEqual(j.AlreadyNotified, []string{"y"}) {
		t.Errorf("%+v", j)
	}
}

// Clear never deletes through a state dir that links outside the repo.
func TestClearRefusesStateDirOutside(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	victim := filepath.Join(out, SanitizeID("s")+".json")
	if err := os.WriteFile(victim, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".auto-context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(root, ".auto-context", "state")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := Clear(root, "s"); err == nil {
		t.Error("Clear through an outside link succeeded")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("outside file removed: %v", err)
	}
}

// Resume forgets reported ack failures, so a failure after pause and resume
// is reported again.
func TestResumeClearsAckFailNotified(t *testing.T) {
	var j Journal
	j.NotifyAckFailed("svc")
	j.Pause()
	j.Resume()
	if j.Paused || len(j.AckFailNotified) != 0 {
		t.Errorf("journal = %+v", j)
	}
}

func TestMarkConfigNotifiedOnce(t *testing.T) {
	var j Journal
	if !j.MarkConfigNotified() || !j.Paused {
		t.Fatalf("first call: %+v", j)
	}
	if j.MarkConfigNotified() || !j.Paused {
		t.Fatalf("second call: %+v", j)
	}
	j.Resume()
	if !j.MarkConfigNotified() {
		t.Error("not reported again after Resume")
	}
}

func TestLoadJournalNormalizesAckFail(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".auto-context", "state", "s.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"version":1,"ack_fail_notified":["b","a","b"]}`), 0o644)
	j, err := LoadJournal(root, "s")
	if err != nil || !reflect.DeepEqual(j.AckFailNotified, []string{"a", "b"}) {
		t.Fatalf("got %+v %v", j, err)
	}
}

func TestJournalForeignVersion(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".auto-context", "state", "s.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	for _, body := range []string{`{"version":99,"code_changed":["a"]}`, `{"code_changed":["a"]}`} {
		os.WriteFile(p, []byte(body), 0o644)
		j, err := LoadJournal(root, "s")
		if err != nil || len(j.CodeChanged) != 0 {
			t.Fatalf("%s: got %+v %v", body, j, err)
		}
	}
	if err := (Journal{CodeChanged: []string{"a"}}).Save(root, "s"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `"version":1`) {
		t.Fatalf("version not written: %s", data)
	}
	if j, _ := LoadJournal(root, "s"); !reflect.DeepEqual(j.CodeChanged, []string{"a"}) {
		t.Fatalf("got %+v", j)
	}
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{"abc": true, "a b": true, "": false, "  \t\n": false} {
		if ValidID(id) != want {
			t.Errorf("ValidID(%q) != %v", id, want)
		}
	}
}
