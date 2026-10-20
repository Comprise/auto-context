package rootindex

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var entries = []Entry{{"src/auth", "JWT."}, {"_root", "skip"}, {"cmd/api", ""}}

func TestRenderSkipsRoot(t *testing.T) {
	want := "<!-- auto-context:units -->\n## Units (details: context/<unit>.md)\n- `src/auth` — JWT.\n- `cmd/api`\n<!-- /auto-context:units -->\n"
	if got := Render(entries); got != want {
		t.Fatalf("got %q", got)
	}
	empty := "<!-- auto-context:units -->\n## Units (details: context/<unit>.md)\n<!-- /auto-context:units -->\n"
	if got := Render(nil); got != empty {
		t.Fatalf("got %q", got)
	}
}

func read(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateCreatesFile(t *testing.T) {
	root := t.TempDir()
	ch, err := Update(root, entries)
	if err != nil || !ch || read(t, root) != Render(entries) {
		t.Fatalf("ch=%v err=%v got=%q", ch, err, read(t, root))
	}
}

func TestUpdateAppendsWhenNoMarkers(t *testing.T) {
	for _, tc := range []struct{ in, sep string }{{"# T\n", "\n"}, {"# T", "\n\n"}, {"# T\n\n", ""}} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(tc.in), 0o644)
		ch, err := Update(root, entries)
		if err != nil || !ch {
			t.Fatal(ch, err)
		}
		if got, want := read(t, root), tc.in+tc.sep+Render(entries); got != want {
			t.Errorf("in %q: got %q want %q", tc.in, got, want)
		}
	}
}

func TestUpdateReplacesSection(t *testing.T) {
	root := t.TempDir()
	before, after := "# T\n\nintro\n\n", "\ntail\n"
	old := "<!-- auto-context:units -->\n- `old` — x\n<!-- /auto-context:units -->\n"
	os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(before+old+after), 0o644)
	ch, err := Update(root, entries)
	if err != nil || !ch {
		t.Fatal(ch, err)
	}
	if got, want := read(t, root), before+Render(entries)+after; got != want {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateNoChange(t *testing.T) {
	root := t.TempDir()
	Update(root, entries)
	ch, err := Update(root, entries)
	if err != nil || ch {
		t.Fatalf("ch=%v err=%v", ch, err)
	}
}

func TestUpdateRefusesBrokenMarkers(t *testing.T) {
	for _, in := range []string{
		"a\n<!-- auto-context:units -->\nb\n",
		"a\n<!-- /auto-context:units -->\nb\n",
		"<!-- /auto-context:units -->\n<!-- auto-context:units -->\n",
	} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(in), 0o644)
		ch, err := Update(root, entries)
		if !errors.Is(err, ErrBrokenMarkers) || ch || read(t, root) != in {
			t.Errorf("in %q: ch=%v err=%v", in, ch, err)
		}
	}
}

// A summary quoting the close marker must not make the file grow on every
// rewrite, and markers count only as whole lines.
func TestUpdateMarkerInSummary(t *testing.T) {
	root := t.TempDir()
	e := []Entry{{"pkg", "sum <!-- /auto-context:units --> tail"}}
	var sizes []int
	for i := 0; i < 3; i++ {
		if _, err := Update(root, e); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(read(t, root)))
	}
	if sizes[0] != sizes[2] {
		t.Errorf("file grows: %v\n%s", sizes, read(t, root))
	}
	if got := strings.Count(read(t, root), closeMarker); got != 1 {
		t.Errorf("close markers = %d:\n%s", got, read(t, root))
	}
}

func TestRenderSanitizesSummary(t *testing.T) {
	got := Render([]Entry{{"pkg", "a <!-- x --> b\x1b[31m\u202e " + strings.Repeat("y", 300)}})
	line := strings.Split(got, "\n")[2]
	if strings.Contains(line, "<!--") || strings.Contains(line, "-->") || strings.ContainsAny(line, "\x1b\u202e") {
		t.Errorf("unsanitized: %q", line)
	}
	if n := len([]rune(line)); n > maxSummary+20 {
		t.Errorf("line has %d runes", n)
	}
}

// The user's own heading inside the section (e.g. in their language) is kept.
func TestUpdateKeepsUserHeader(t *testing.T) {
	root := t.TempDir()
	cur := "# P\n\n<!-- auto-context:units -->\n## Юниты\n- `old`\n<!-- /auto-context:units -->\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(cur), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, entries); err != nil {
		t.Fatal(err)
	}
	want := "# P\n\n<!-- auto-context:units -->\n## Юниты\n- `src/auth` — JWT.\n- `cmd/api`\n<!-- /auto-context:units -->\n"
	if got := read(t, root); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// A second close marker later in the file belongs to the user's text: the
// section ends at the first one, and the rest is kept.
func TestUpdateKeepsTextAfterFirstClose(t *testing.T) {
	root := t.TempDir()
	cur := "<!-- auto-context:units -->\n## Units (details: context/<unit>.md)\n<!-- /auto-context:units -->\nuser text\n<!-- /auto-context:units -->\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(cur), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, entries); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root); !strings.Contains(got, "user text") {
		t.Errorf("user text lost:\n%s", got)
	}
}

// The section runs from the first open marker; markers are whole lines that
// may be indented or carry trailing spaces.
func TestUpdateMarkerEdgeCases(t *testing.T) {
	root := t.TempDir()
	in := "A\n  <!-- auto-context:units -->  \nX\n<!-- auto-context:units -->\nY\n<!-- /auto-context:units -->\t\nB\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, entries); err != nil {
		t.Fatal(err)
	}
	got := read(t, root)
	if !strings.HasPrefix(got, "A\n") || !strings.HasSuffix(got, "B\n") || strings.Contains(got, "X\n") || strings.Contains(got, "Y\n") {
		t.Errorf("got %q", got)
	}
}

// An update keeps the file's permissions.
func TestUpdateKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	root := t.TempDir()
	p := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(p, []byte("# T\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, entries); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestCheck(t *testing.T) {
	root := t.TempDir()
	if err := Check(root); err != nil {
		t.Errorf("no CLAUDE.md: %v", err)
	}
	p := filepath.Join(root, "CLAUDE.md")
	for content, want := range map[string]error{
		"# T\n": nil,
		"<!-- auto-context:units -->\nx\n<!-- /auto-context:units -->\n": nil,
		"<!-- /auto-context:units -->\n":                                 ErrBrokenMarkers,
		"<!-- auto-context:units -->\nx\n":                               ErrBrokenMarkers,
	} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Check(root); !errors.Is(err, want) && err != want {
			t.Errorf("%q: %v, want %v", content, err, want)
		}
	}
}
