package docstate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"auto-context/internal/config"
	"auto-context/internal/frontmatter"
	"auto-context/internal/hashing"
	"auto-context/internal/testutil"
	"auto-context/internal/units"
)

func setup(t *testing.T) (string, *units.Tree) {
	t.Helper()
	root := t.TempDir()
	for _, u := range []string{"ok", "stale", "svc", "bad"} {
		testutil.WriteFile(t, root, u+"/a.go", "package x\n")
	}
	cfg := config.Default()
	cfg.Include = []string{"ok", "stale", "svc", "bad"}
	tr, err := units.Discover(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return root, tr
}

func TestComputeStates(t *testing.T) {
	root, tr := setup(t)
	c := cache(root)
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, u := range []string{"ok", "stale"} {
		testutil.WriteFile(t, root, "context/"+u+".md", "# "+u+"\n\nSummary of "+u+".\n")
		if err := Ack(root, tr, c, u, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	testutil.WriteFile(t, root, "context/bad.md", "---\nunit: [\n---\n# bad\n\nText.\n")
	testutil.WriteFile(t, root, "stale/a.go", "package x\n// changed\n")

	st := computeAll(root, tr, c)
	got := map[string]UnitStatus{}
	for _, s := range st {
		got[s.Unit] = s
	}
	if got["ok"].State != OK || got["ok"].Summary != "Summary of ok." || got["ok"].ContextPath != "context/ok.md" {
		t.Errorf("ok = %+v", got["ok"])
	}
	if got["stale"].State != Stale || got["stale"].Note != "" || got["stale"].Summary != "Summary of stale." {
		t.Errorf("stale = %+v", got["stale"])
	}
	if got["svc"].State != Missing {
		t.Errorf("svc = %+v", got["svc"])
	}
	if got["bad"].State != Stale || got["bad"].Note != "broken frontmatter" {
		t.Errorf("bad = %+v", got["bad"])
	}
	if len(st) != len(tr.Units()) || st[0].Unit != tr.Units()[0].Path {
		t.Errorf("order: %+v", st)
	}
}

func TestComputeNoFrontmatterIsStale(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nHi.\n")
	st := computeAll(root, tr, cache(root))
	for _, s := range st {
		if s.Unit == "svc" && (s.State != Stale || s.Note != NoteNotAcked) {
			t.Errorf("svc = %+v", s)
		}
	}
}

func TestAckMissingDoc(t *testing.T) {
	root, tr := setup(t)
	err := Ack(root, tr, cache(root), "svc", time.Now(), nil)
	if err == nil || !strings.Contains(err.Error(), "ack --exclude svc") {
		t.Fatalf("err = %v", err)
	}
}

func TestAckUnknownUnit(t *testing.T) {
	root, tr := setup(t)
	err := Ack(root, tr, cache(root), "nope", time.Now(), nil)
	if err == nil || err.Error() != "unknown unit nope" {
		t.Fatalf("err = %v", err)
	}
}

func statusOf(t *testing.T, root string, tr *units.Tree, unit string) UnitStatus {
	t.Helper()
	sts := computeAll(root, tr, cache(root))
	for _, s := range sts {
		if s.Unit == unit {
			return s
		}
	}
	t.Fatalf("unit %s not found", unit)
	return UnitStatus{}
}

// A context doc linking outside the repo or to a non-.md file is never read.
func TestComputeRefusesLinkedDocs(t *testing.T) {
	root, tr := setup(t)
	outside := filepath.Join(t.TempDir(), "o.md")
	testutil.WriteFile(t, filepath.Dir(outside), "o.md", "# o\n\nSECRET outside\n")
	testutil.WriteFile(t, root, ".env", "# Settings\nAPI_KEY=secret\n")
	if err := os.MkdirAll(filepath.Join(root, "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "context", "ok.md")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := os.Symlink("../.env", filepath.Join(root, "context", "svc.md")); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"ok", "svc"} {
		s := statusOf(t, root, tr, u)
		if s.State != Stale || s.Note != "unusable symlink" || s.Summary != "" {
			t.Errorf("%s: %+v, want stale (unusable symlink) with no summary", u, s)
		}
	}
}

// One unreadable source file marks its own unit, not the whole repo.
func TestComputeUnreadableFileIsPerUnit(t *testing.T) {
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("file permissions do not deny reads here")
	}
	root, tr := setup(t)
	c := cache(root)
	for _, u := range []string{"ok", "svc"} {
		testutil.WriteFile(t, root, "context/"+u+".md", "# "+u+"\n\nS.\n")
		if err := Ack(root, tr, c, u, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "svc", "a.go"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "svc", "a.go"), 0o644)
	if s := statusOf(t, root, tr, "svc"); s.State != Stale || s.Note != NoteHashError || !strings.Contains(s.Detail, "permission denied") {
		t.Errorf("svc: %+v, want stale with the read error", s)
	}
	if s := statusOf(t, root, tr, "ok"); s.State != OK {
		t.Errorf("ok: %+v, want ok", s)
	}
}

func cache(root string) *hashing.Cache {
	c, _ := hashing.LoadCache(root)
	return c
}

// Adding or removing a unit makes the root docs stale, even with no files.
func TestRootHashCoversUnitList(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/_root.md", "# r\n\nR.\n")
	c := cache(root)
	if err := Ack(root, tr, c, units.RootUnit, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if s := StatusOf(root, tr, c, units.RootUnit); s.State != OK {
		t.Fatalf("after ack: %+v", s)
	}
	// Drop the "bad" unit with exclude "bad/**": its files leave the tree
	// entirely, so only the unit list can change _root's hash.
	cfg := config.Default()
	cfg.Include = []string{"ok", "stale", "svc", "bad"}
	cfg.Exclude = append(cfg.Exclude, "bad/**")
	tr2, err := units.Discover(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s := StatusOf(root, tr2, c, units.RootUnit); s.State != Stale {
		t.Errorf("unit removed, _root still %s", s.State)
	}
}

func TestNotAckedNote(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nS.\n")
	if s := StatusOf(root, tr, cache(root), "svc"); s.State != Stale || s.Note != NoteNotAcked {
		t.Errorf("%+v", s)
	}
}

// Acking an up-to-date doc does not rewrite it (a parallel session's or a
// repeated auto-ack must not look like a docs edit).
func TestAckIsIdempotent(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	c := cache(root)
	if err := Ack(root, tr, c, "ok", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "context", "ok.md")
	before, _ := os.ReadFile(p)
	if err := Ack(root, tr, c, "ok", time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Errorf("rewritten:\n%s\n---\n%s", before, after)
	}
}

func TestDocKinds(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "context/a.md", "# a\n")
	if k, p, err := Doc(root, "a"); k != DocPresent || p == "" || err != nil {
		t.Errorf("present: %v %q %v", k, p, err)
	}
	if k, _, _ := Doc(root, "none"); k != DocAbsent {
		t.Errorf("absent: %v", k)
	}
	if err := os.MkdirAll(filepath.Join(root, "context", "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if k, _, _ := Doc(root, "dir"); k != DocRefused {
		t.Errorf("directory: %v", k)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(t.TempDir(), "x.md"), filepath.Join(root, "context", "out.md")); err != nil {
			t.Fatal(err)
		}
		if k, _, _ := Doc(root, "out"); k != DocRefused {
			t.Errorf("link outside: %v", k)
		}
	}
}

func TestNoteText(t *testing.T) {
	if s := (UnitStatus{Note: NoteHashError, Detail: "boom"}).NoteText(); s != "cannot hash sources: boom" {
		t.Errorf("%q", s)
	}
	if s := (UnitStatus{Note: NoteBroken}).NoteText(); s != "broken frontmatter" {
		t.Errorf("%q", s)
	}
}

// An ack with the same hash but another unit name rewrites the frontmatter.
func TestAckRewritesForeignUnit(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	c := cache(root)
	if err := Ack(root, tr, c, "ok", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "context", "ok.md")
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), "unit: ok", "unit: other", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ack(root, tr, c, "ok", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); !strings.Contains(string(data), "unit: ok") {
		t.Errorf("not rewritten:\n%s", data)
	}
}

// computeAll is the status of every unit, in t.Units() order.
func computeAll(root string, t *units.Tree, c *hashing.Cache) []UnitStatus {
	var out []UnitStatus
	for _, u := range t.Units() {
		out = append(out, StatusOf(root, t, c, u.Path))
	}
	return out
}

// Ack records the commit head returns, and asks for it only when it rewrites
// the doc.
func TestAckRecordsCommit(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	c := cache(root)
	sha := strings.Repeat("1f", 20)
	calls := 0
	head := func() string { calls++; return sha }
	if err := Ack(root, tr, c, "ok", time.Now(), head); err != nil {
		t.Fatal(err)
	}
	if err := Ack(root, tr, c, "ok", time.Now(), head); err != nil {
		t.Fatal(err)
	}
	m, _, err := frontmatter.Read(root, filepath.Join(root, "context", "ok.md"))
	if err != nil || m.Commit != sha {
		t.Fatalf("m=%+v err=%v", m, err)
	}
	if calls != 1 {
		t.Errorf("head called %d times, want 1", calls)
	}
}
