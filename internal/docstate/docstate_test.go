package docstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	tr, err := units.DiscoverContext(context.Background(), root, cfg)
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
		if _, err := AckIfNeeded(context.Background(), root, tr, c, u, now, nil); err != nil {
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
	_, err := AckIfNeeded(context.Background(), root, tr, cache(root), "svc", time.Now(), nil)
	if err == nil || !strings.Contains(err.Error(), "ack --exclude svc") {
		t.Fatalf("err = %v", err)
	}
}

func TestAckUnknownUnit(t *testing.T) {
	root, tr := setup(t)
	_, err := AckIfNeeded(context.Background(), root, tr, cache(root), "nope", time.Now(), nil)
	if err == nil || err.Error() != "unknown unit nope" || !errors.Is(err, units.ErrUnknownUnit) {
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
		if _, err := AckIfNeeded(context.Background(), root, tr, c, u, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "svc", "a.go"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "svc", "a.go"), 0o644)
	if s := statusOf(t, root, tr, "svc"); s.State != Stale || s.Note != NoteHashError || s.Detail != "permission denied" {
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
	if _, err := AckIfNeeded(context.Background(), root, tr, c, units.RootUnit, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if s := StatusOf(context.Background(), root, tr, c, units.RootUnit); s.State != OK {
		t.Fatalf("after ack: %+v", s)
	}
	// Drop the "bad" unit with exclude "bad/**": its files leave the tree
	// entirely, so only the unit list can change _root's hash.
	cfg := config.Default()
	cfg.Include = []string{"ok", "stale", "svc", "bad"}
	cfg.Exclude = append(cfg.Exclude, "bad/**")
	tr2, err := units.DiscoverContext(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s := StatusOf(context.Background(), root, tr2, c, units.RootUnit); s.State != Stale {
		t.Errorf("unit removed, _root still %s", s.State)
	}
}

func TestNotAckedNote(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/svc.md", "# svc\n\nS.\n")
	if s := StatusOf(context.Background(), root, tr, cache(root), "svc"); s.State != Stale || s.Note != NoteNotAcked {
		t.Errorf("%+v", s)
	}
}

// Acking an up-to-date doc does not rewrite it (a parallel session's or a
// repeated auto-ack must not look like a docs edit).
func TestAckIsIdempotent(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	c := cache(root)
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "context", "ok.md")
	before, _ := os.ReadFile(p)
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), nil); err != nil {
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
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "context", "ok.md")
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), "unit: ok", "unit: other", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), nil); err != nil {
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
		out = append(out, StatusOf(context.Background(), root, t, c, u.Path))
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
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), head); err != nil {
		t.Fatal(err)
	}
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), head); err != nil {
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

// AckIfNeeded tells whether it wrote the doc, and always writes scheme v2.
func TestAckIfNeededWritten(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	c := cache(root)
	w, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), nil)
	if err != nil || !w {
		t.Fatalf("first: written=%v err=%v", w, err)
	}
	m, _, err := frontmatter.Read(root, filepath.Join(root, "context", "ok.md"))
	if err != nil || !strings.HasPrefix(m.SourceHash, HashPrefix+"sha256:") {
		t.Fatalf("hash %q err %v, want the v2 prefix", m.SourceHash, err)
	}
	if w, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), nil); err != nil || w {
		t.Errorf("second: written=%v err=%v", w, err)
	}
	testutil.WriteFile(t, root, "ok/a.go", "package x\n// changed\n")
	if w, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), nil); err != nil || !w {
		t.Errorf("after a change: written=%v err=%v", w, err)
	}
}

// writeLegacy records the hash of the old scheme (all files, all units, no
// prefix) in unit's doc.
func writeLegacy(t *testing.T, root string, tr *units.Tree, unit string) {
	t.Helper()
	var extra []string
	if unit == units.RootUnit {
		for _, u := range tr.Units() {
			if u.Path != units.RootUnit {
				extra = append(extra, u.Path)
			}
		}
	}
	h, err := hashing.SourceHash(context.Background(), root, tr.Files(unit), extra, cache(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := frontmatter.SetHash(root, filepath.Join(root, "context", unit+".md"), unit, h, "", time.Now()); err != nil {
		t.Fatal(err)
	}
}

// A doc acked by v1.0.0 (old scheme) is ok with a note while the code did
// not change; ack moves it to v2.
func TestLegacyHashIsOKWithNote(t *testing.T) {
	root, tr := setup(t)
	for _, u := range []string{"ok", units.RootUnit} {
		testutil.WriteFile(t, root, "context/"+u+".md", "# "+u+"\n\nS.\n")
		writeLegacy(t, root, tr, u)
		s := StatusOf(context.Background(), root, tr, cache(root), u)
		if s.State != OK || s.Note != NoteOldScheme || !OldScheme(s) || s.Summary != "S." {
			t.Errorf("%s: %+v, want ok with %q", u, s, NoteOldScheme)
		}
	}
	if w, err := AckIfNeeded(context.Background(), root, tr, cache(root), "ok", time.Now(), nil); err != nil || !w {
		t.Fatalf("upgrade: written=%v err=%v", w, err)
	}
	if s := StatusOf(context.Background(), root, tr, cache(root), "ok"); s.State != OK || s.Note != "" || OldScheme(s) {
		t.Errorf("after ack: %+v", s)
	}
}

func TestLegacyMismatchIsStale(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nS.\n")
	writeLegacy(t, root, tr, "ok")
	testutil.WriteFile(t, root, "ok/a.go", "package x\n// changed\n")
	if s := StatusOf(context.Background(), root, tr, cache(root), "ok"); s.State != Stale || s.Note != "" || OldScheme(s) {
		t.Errorf("%+v, want plain stale", s)
	}
}

// Q-L8: _root cannot be excluded, so the hint does not suggest it.
func TestAckRootMissingMessage(t *testing.T) {
	root, tr := setup(t)
	_, err := AckIfNeeded(context.Background(), root, tr, cache(root), units.RootUnit, time.Now(), nil)
	if err == nil || err.Error() != "no context/_root.md: create it (the root unit cannot be excluded)" {
		t.Errorf("err = %v", err)
	}
}

func TestSummaryOf(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nSummary of ok.\n")
	if s := SummaryOf(root, "ok"); s != "Summary of ok." {
		t.Errorf("not acked: %q", s)
	}
	if _, err := AckIfNeeded(context.Background(), root, tr, cache(root), "ok", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if s := SummaryOf(root, "ok"); s != "Summary of ok." {
		t.Errorf("acked: %q", s)
	}
	if s := SummaryOf(root, "svc"); s != "" {
		t.Errorf("missing: %q", s)
	}
	if err := os.MkdirAll(filepath.Join(root, "context", "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s := SummaryOf(root, "dir"); s != "" {
		t.Errorf("directory: %q", s)
	}
}

// A-H1: untracked files do not enter source_hash, so a doc acked with an
// untracked file in the unit is ok in a clean clone.
func TestSourceHashIgnoresUntracked(t *testing.T) {
	testutil.RequireGit(t)
	src := t.TempDir()
	testutil.WriteFile(t, src, "svc/a.go", "package x\n")
	testutil.WriteFile(t, src, "context/svc.md", "# svc\n\nS.\n")
	testutil.WriteFile(t, src, "context/_root.md", "# r\n\nR.\n")
	testutil.Git(t, src, "init", "-q")
	testutil.Git(t, src, "add", "-A")
	testutil.Git(t, src, "commit", "-q", "-m", "init")
	testutil.WriteFile(t, src, "svc/scratch.go", "package x\n// local\n")
	testutil.WriteFile(t, src, "tmp/b.go", "package y\n") // an untracked would-be unit
	cfg := config.Default()
	cfg.Include = []string{"svc", "tmp"}
	discover := func(root string) *units.Tree {
		tr, err := units.DiscoverContext(context.Background(), root, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	tr := discover(src)
	for _, u := range []string{"svc", units.RootUnit} {
		if _, err := AckIfNeeded(context.Background(), src, tr, cache(src), u, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, src, "add", "context")
	testutil.Git(t, src, "commit", "-q", "-m", "ack")
	dst := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, src, "clone", "-q", src, dst)
	ctr := discover(dst)
	for _, u := range []string{"svc", units.RootUnit} {
		if s := StatusOf(context.Background(), dst, ctr, cache(dst), u); s.State != OK || s.Note != "" {
			t.Errorf("clone %s: %+v, want ok", u, s)
		}
		if s := StatusOf(context.Background(), src, tr, cache(src), u); s.State != OK || s.Note != "" {
			t.Errorf("work tree %s: %+v, want ok", u, s)
		}
	}
}

// Golden old-scheme hashes of the tree TestLegacyGoldenGitMode builds: the
// full view, the untracked svc/scratch.go and the untracked-only unit tmp
// included. Hardcoded so the test pins v1.0.0's rules, not legacyHash's.
const (
	goldenLegacySvc  = "sha256:a4040191816104c65bbbbe3aa8724cb0336d23d890ec082bdf65aec9d629ca44"
	goldenLegacyRoot = "sha256:55327ffdc1ef8cab18dba1a865ff1c251c3f087ae69f862411f77dedd50ee79c"
)

// In git mode the old scheme covers untracked files and units; a doc acked by
// v1.0.0 over such a tree is ok with NoteOldScheme.
func TestLegacyGoldenGitMode(t *testing.T) {
	testutil.RequireGit(t)
	root := t.TempDir()
	testutil.WriteFile(t, root, "svc/a.go", "package x\n")
	testutil.Git(t, root, "init", "-q")
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-q", "-m", "init")
	testutil.WriteFile(t, root, "svc/scratch.go", "package x\n// local\n")
	testutil.WriteFile(t, root, "tmp/b.go", "package y\n")
	doc := func(unit, hash string) string {
		return "---\nunit: " + unit + "\nsource_hash: " + hash + "\n---\n# " + unit + "\n\nS.\n"
	}
	testutil.WriteFile(t, root, "context/svc.md", doc("svc", goldenLegacySvc))
	testutil.WriteFile(t, root, "context/_root.md", doc(units.RootUnit, goldenLegacyRoot))
	cfg := config.Default()
	cfg.Include = []string{"svc", "tmp"}
	tr, err := units.DiscoverContext(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.IsUnit("tmp") || slices.Contains(tr.HashUnits(), "tmp") || slices.Contains(tr.HashFiles("svc"), "svc/scratch.go") {
		t.Fatalf("fixture is not in git mode: units %v hash units %v", tr.Units(), tr.HashUnits())
	}
	for _, u := range []string{"svc", units.RootUnit} {
		if s := StatusOf(context.Background(), root, tr, cache(root), u); !OldScheme(s) {
			l, _ := legacyHash(context.Background(), root, tr, cache(root), u)
			t.Errorf("%s: %+v, want ok with %q (legacyHash now %s)", u, s, NoteOldScheme, l)
		}
	}
}

// A unit whose sources are hashed after the hook's time is up is stale with
// a fixed detail saying so.
func TestStatusOfTimeUp(t *testing.T) {
	root, tr := setup(t)
	c := cache(root)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	s := StatusOf(ctx, root, tr, cache(root), "ok")
	if s.State != Stale || s.Note != NoteHashError || s.Detail != TimeUp {
		t.Errorf("%+v, want stale with %q", s, TimeUp)
	}
	if TimeUp != "the hook's time is up" {
		t.Errorf("TimeUp = %q", TimeUp)
	}
}

// StatusOfDoc decides by the doc it is given, not by the file: a doc read
// before an edit gives the status of that read.
func TestStatusOfDocUsesGivenDoc(t *testing.T) {
	root, tr := setup(t)
	c := cache(root)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	if _, err := AckIfNeeded(context.Background(), root, tr, c, "ok", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	acked := ReadDoc(root, "ok")
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nRewritten.\n") // no frontmatter now
	if s := StatusOfDoc(context.Background(), root, tr, c, "ok", acked); s.State != OK || s.Summary != "Ok." {
		t.Errorf("with the acked doc: %+v", s)
	}
	if s := StatusOf(context.Background(), root, tr, c, "ok"); s.State != Stale || s.Note != NoteNotAcked {
		t.Errorf("from the file: %+v", s)
	}
}

// An interrupted command (a cancelled context) says so in Detail.
func TestStatusOfInterrupted(t *testing.T) {
	root, tr := setup(t)
	testutil.WriteFile(t, root, "context/ok.md", "# ok\n\nOk.\n")
	if _, err := AckIfNeeded(context.Background(), root, tr, cache(root), "ok", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s := StatusOf(ctx, root, tr, cache(root), "ok"); s.State != Stale || s.Note != NoteHashError || s.Detail != Interrupted {
		t.Errorf("%+v, want stale with %q", s, Interrupted)
	}
	if Interrupted != "interrupted" {
		t.Errorf("Interrupted = %q", Interrupted)
	}
}
