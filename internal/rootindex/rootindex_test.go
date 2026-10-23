package rootindex

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	ch, err := Update(root, "CLAUDE.md", entries)
	if err != nil || !ch || read(t, root) != Render(entries) {
		t.Fatalf("ch=%v err=%v got=%q", ch, err, read(t, root))
	}
}

func TestUpdateAppendsWhenNoMarkers(t *testing.T) {
	for _, tc := range []struct{ in, sep string }{{"# T\n", "\n"}, {"# T", "\n\n"}, {"# T\n\n", ""}} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(tc.in), 0o644)
		ch, err := Update(root, "CLAUDE.md", entries)
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
	ch, err := Update(root, "CLAUDE.md", entries)
	if err != nil || !ch {
		t.Fatal(ch, err)
	}
	if got, want := read(t, root), before+Render(entries)+after; got != want {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateNoChange(t *testing.T) {
	root := t.TempDir()
	Update(root, "CLAUDE.md", entries)
	ch, err := Update(root, "CLAUDE.md", entries)
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
		ch, err := Update(root, "CLAUDE.md", entries)
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
		if _, err := Update(root, "CLAUDE.md", e); err != nil {
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
	if _, err := Update(root, "CLAUDE.md", entries); err != nil {
		t.Fatal(err)
	}
	want := "# P\n\n<!-- auto-context:units -->\n## Юниты\n- `src/auth` — JWT.\n- `cmd/api`\n<!-- /auto-context:units -->\n"
	if got := read(t, root); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// A second open or close marker (a duplicate pair, or a stray one after the
// section) is refused, not silently left stale: Update keeps the file as is
// and Check reports it.
func TestUpdateRefusesDuplicateMarkers(t *testing.T) {
	sec := "<!-- auto-context:units -->\n- `old`\n<!-- /auto-context:units -->\n"
	for _, in := range []string{
		sec + "user text\n<!-- /auto-context:units -->\n",
		sec + "text\n" + sec,
		"<!-- auto-context:units -->\nX\n" + sec,
	} {
		root := writeIndex(t, in)
		ch, err := Update(root, "CLAUDE.md", entries)
		if !errors.Is(err, ErrBrokenMarkers) || ch || read(t, root) != in {
			t.Errorf("in %q: ch=%v err=%v", in, ch, err)
		}
		if err := Check(root, "CLAUDE.md"); !errors.Is(err, ErrBrokenMarkers) {
			t.Errorf("Check %q: %v", in, err)
		}
	}
}

// A section appended after an unclosed fence closes that fence first: the
// section is found again on the next run, and a fenced block the user adds
// after it later does not swallow it.
func TestUpdateUnclosedFenceStable(t *testing.T) {
	root := writeIndex(t, "# T\n```\ncode")
	for i := 0; i < 2; i++ {
		if _, err := Update(root, "CLAUDE.md", entries); err != nil {
			t.Fatal(err)
		}
	}
	want := "# T\n```\ncode\n```\n\n" + Render(entries)
	if got := read(t, root); got != want {
		t.Errorf("got %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(want+"\n```\nfoo\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, "CLAUDE.md", nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root); got != "# T\n```\ncode\n```\n\n"+Render(nil)+"\n```\nfoo\n```\n" {
		t.Errorf("after a later fence: got %q", got)
	}
}

// Check reads a file starting with a BOM the way Update does.
func TestCheckBOMFence(t *testing.T) {
	sec := "<!-- auto-context:units -->\n- `old`\n<!-- /auto-context:units -->\n"
	for in, broken := range map[string]bool{
		"\ufeff```\n<!-- auto-context:units -->\n```\n": false,
		"\ufeff```\n" + sec + "```\n" + sec:             false,
		"\ufeff" + sec + sec:                            true,
	} {
		root := writeIndex(t, in)
		err := Check(root, "CLAUDE.md")
		_, uerr := Update(root, "CLAUDE.md", entries)
		if errors.Is(err, ErrBrokenMarkers) != broken || errors.Is(uerr, ErrBrokenMarkers) != broken {
			t.Errorf("in %q: Check %v, Update %v", in, err, uerr)
		}
	}
}

// Markers are whole lines that may be indented or carry trailing spaces.
func TestUpdateMarkerEdgeCases(t *testing.T) {
	root := t.TempDir()
	in := "A\n  <!-- auto-context:units -->  \nY\n<!-- /auto-context:units -->\t\nB\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, "CLAUDE.md", entries); err != nil {
		t.Fatal(err)
	}
	got := read(t, root)
	if !strings.HasPrefix(got, "A\n") || !strings.HasSuffix(got, "B\n") || strings.Contains(got, "Y\n") {
		t.Errorf("got %q", got)
	}
}

// A UTF-8 BOM before the open marker on line 1 does not hide it, and is kept.
func TestUpdateBOM(t *testing.T) {
	in := "\ufeff<!-- auto-context:units -->\n- `old`\n<!-- /auto-context:units -->\n"
	root := writeIndex(t, in)
	if err := Check(root, "CLAUDE.md"); err != nil {
		t.Errorf("Check: %v", err)
	}
	if _, err := Update(root, "CLAUDE.md", entries); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root); got != "\ufeff"+Render(entries) {
		t.Errorf("got %q", got)
	}
	root = writeIndex(t, "\ufeff# T\n")
	if _, err := Update(root, "CLAUDE.md", nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root); got != "\ufeff# T\n\n"+Render(nil) {
		t.Errorf("got %q", got)
	}
}

// Markers inside a fenced code block are an example, not the section: they
// are neither rewritten nor counted.
func TestUpdateSkipsFencedMarkers(t *testing.T) {
	sec := "<!-- auto-context:units -->\n- `old`\n<!-- /auto-context:units -->\n"
	for _, fenced := range []string{
		"```\n" + sec + "```\n",
		"   ~~~~ md\n" + sec + "~~~\n````\n~~~~~\n",
		"````\n" + sec + "```\n````\n",
		"```go\n<!-- /auto-context:units -->\n``` \n",
		"- ex:\n\n    ```\n    <!-- auto-context:units -->\n    <!-- /auto-context:units -->\n    ```\n",
	} {
		for _, in := range []string{fenced, sec + fenced} {
			root := writeIndex(t, in)
			if err := Check(root, "CLAUDE.md"); err != nil {
				t.Errorf("Check %q: %v", in, err)
			}
			if _, err := Update(root, "CLAUDE.md", entries); err != nil {
				t.Errorf("in %q: %v", in, err)
				continue
			}
			if got := read(t, root); !strings.Contains(got, fenced) || !strings.Contains(got, Render(entries)) {
				t.Errorf("in %q: got %q", in, got)
			}
		}
	}
	// A backtick in a backtick fence's info string or no closing fence make no
	// fence: the markers are live.
	for _, in := range []string{"    ```\n" + sec, "``` a`b\n" + sec, "```\n" + sec} {
		root := writeIndex(t, in)
		if _, err := Update(root, "CLAUDE.md", entries); err != nil || strings.Contains(read(t, root), "old") {
			t.Errorf("in %q: err=%v got %q", in, err, read(t, root))
		}
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
	if _, err := Update(root, "CLAUDE.md", entries); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestCheck(t *testing.T) {
	root := t.TempDir()
	if err := Check(root, "CLAUDE.md"); err != nil {
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
		if err := Check(root, "CLAUDE.md"); !errors.Is(err, want) && err != want {
			t.Errorf("%q: %v, want %v", content, err, want)
		}
	}
}

func TestUpdateAgents(t *testing.T) {
	root := t.TempDir()
	ch, err := Update(root, "AGENTS.md", entries)
	if err != nil || !ch {
		t.Fatalf("ch=%v err=%v", ch, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || string(b) != Render(entries) {
		t.Fatalf("AGENTS.md = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md was created")
	}
	if err := Check(root, "AGENTS.md"); err != nil {
		t.Errorf("Check: %v", err)
	}
}

func TestUpdateThroughSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# T\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("CLAUDE.md", filepath.Join(root, "AGENTS.md")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if _, err := Update(root, "AGENTS.md", entries); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(root, "AGENTS.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("AGENTS.md link replaced: %v %v", fi, err)
	}
	if got := read(t, root); !strings.Contains(got, Render(entries)) {
		t.Errorf("CLAUDE.md lacks the section: %q", got)
	}
}

func writeIndex(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func noBareLF(s string) bool {
	return !strings.Contains(strings.ReplaceAll(s, "\r\n", ""), "\n")
}

func TestUpdateKeepsCRLF(t *testing.T) {
	root := writeIndex(t, "# P\r\n\r\ntext\r\n")
	es := []Entry{{"svc", "S"}}
	if _, err := Update(root, "CLAUDE.md", es); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root); !noBareLF(got) || !strings.Contains(got, "- `svc` — S\r\n") {
		t.Fatalf("got %q", got)
	}
	if changed, err := Update(root, "CLAUDE.md", es); err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

func TestUpdateCRLFExistingSection(t *testing.T) {
	root := writeIndex(t, "# P\r\n\r\n<!-- auto-context:units -->\r\n## My units\r\n- `old`\r\n<!-- /auto-context:units -->\r\ntail\r\n")
	if _, err := Update(root, "CLAUDE.md", []Entry{{"svc", "S"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, root)
	if !noBareLF(got) || !strings.Contains(got, "## My units\r\n- `svc` — S\r\n") || strings.Contains(got, "old") {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateCRLFNoFinalNewline(t *testing.T) {
	root := writeIndex(t, "# P\r\ntext")
	if _, err := Update(root, "CLAUDE.md", nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root); !strings.HasPrefix(got, "# P\r\ntext\r\n\r\n<!-- auto-context:units -->\r\n") || !noBareLF(got) {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateLFSingleLine(t *testing.T) {
	root := writeIndex(t, "text")
	if _, err := Update(root, "CLAUDE.md", nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root); !strings.HasPrefix(got, "text\n\n<!-- auto-context:units -->\n") || strings.Contains(got, "\r") {
		t.Fatalf("got %q", got)
	}
}

// limit is a wall-clock limit for a test, ten times longer under -race.
func limit(d time.Duration) time.Duration {
	if raceOn {
		return 10 * d
	}
	return d
}

// Many unclosed fence openers stay linear: no opener scans the rest of the
// file for its closer (Q-H1).
func TestUpdateManyUnclosedFencesFast(t *testing.T) {
	for _, l := range []string{"~~~a\n", "```a\n"} {
		root := writeIndex(t, strings.Repeat(l, 50000))
		start := time.Now()
		if _, err := Update(root, "CLAUDE.md", entries); err != nil {
			t.Fatal(err)
		}
		if err := Check(root, "CLAUDE.md"); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > limit(200*time.Millisecond) {
			t.Errorf("%q x50000: Update+Check took %v", l, d)
		}
	}
}

// The fence rules are unchanged: an opener is closed by the nearest later
// fence of the same character, not shorter, with no info string; an opener
// with no such closer hides nothing, and the scan goes on after it.
func TestFencedSemanticsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		in, in2, open string
	}{
		{"```\nx\n```\n", "111", ""},
		{"   ~~~~ md\nx\n~~~\n````\n~~~~~\n", "11111", ""},
		{"````\nx\n```\n````\n", "1111", ""},
		{"```go\nx\n``` \n", "111", ""},
		{"``` a`b\nx\n```\n", "000", "```"},
		{"```\nx\n", "00", "```"},
		{"```a\n```b\n```\n", "111", ""},
		{"~~~a\n```\nx\n```\n", "0111", "~~~"},
		{"```\n~~~\n```\n~~~\n", "1110", "~~~"},
		{"````\n```\n```\n", "011", "````"},
		{"```x\n````\n```\n````\n", "1111", ""},
		{"`` \n~~\nx\n", "000", ""},
	} {
		lines := strings.SplitAfter(strings.TrimSuffix(tc.in, "\n"), "\n")
		in, open := fenced(lines)
		var got strings.Builder
		for _, b := range in {
			if b {
				got.WriteByte('1')
			} else {
				got.WriteByte('0')
			}
		}
		if got.String() != tc.in2 || open != tc.open {
			t.Errorf("%q: in=%s open=%q, want in=%s open=%q", tc.in, got.String(), open, tc.in2, tc.open)
		}
	}
	// An unclosed fence before the markers hides nothing: the markers are live.
	sec := "<!-- auto-context:units -->\n- `old`\n<!-- /auto-context:units -->\n"
	for _, pre := range []string{"```\n", "~~~a\n```b\n", "````\n```\n"} {
		root := writeIndex(t, pre+sec)
		if err := Check(root, "CLAUDE.md"); err != nil {
			t.Errorf("Check %q: %v", pre, err)
		}
		if _, err := Update(root, "CLAUDE.md", entries); err != nil {
			t.Fatal(err)
		}
		if got := read(t, root); got != pre+Render(entries) {
			t.Errorf("pre %q: got %q", pre, got)
		}
	}
}

// A huge summary built to make comment-delimiter removal quadratic is cut
// before the removal loop (Q-H2).
func TestRenderLongSummary(t *testing.T) {
	k := (1 << 20) / 4
	sum := strings.Repeat("<", k) + strings.Repeat("!--", k)
	start := time.Now()
	got := Render([]Entry{{"pkg", sum}})
	if d := time.Since(start); d > limit(50*time.Millisecond) {
		t.Errorf("Render took %v", d)
	}
	line := strings.Split(got, "\n")[2]
	_, s, ok := strings.Cut(line, " — ")
	if !ok {
		t.Fatalf("no summary in %q", line)
	}
	if n := len([]rune(strings.TrimSuffix(s, "…"))); n > maxSummary {
		t.Errorf("summary has %d runes", n)
	}
	if strings.Contains(s, "<!--") || strings.Contains(s, "-->") {
		t.Errorf("unsanitized: %q", s)
	}
}

// A file of a lone "\r" (no "\n") gets a section that the next Update keeps
// as is (T-L1).
func TestUpdateLoneCR(t *testing.T) {
	for _, in := range []string{"\r", "text\r", "```\r"} {
		root := writeIndex(t, in)
		if _, err := Update(root, "CLAUDE.md", entries); err != nil {
			t.Fatal(err)
		}
		if ch, err := Update(root, "CLAUDE.md", entries); err != nil || ch {
			t.Errorf("in %q: second Update ch=%v err=%v: %q", in, ch, err, read(t, root))
		}
		if !strings.HasPrefix(read(t, root), in) {
			t.Errorf("in %q: content changed: %q", in, read(t, root))
		}
	}
}

func BenchmarkUpdateUnclosedFences(b *testing.B) {
	root := b.TempDir()
	p := filepath.Join(root, "CLAUDE.md")
	content := []byte(strings.Repeat("~~~a\n", 50000))
	for b.Loop() {
		if err := os.WriteFile(p, content, 0o644); err != nil {
			b.Fatal(err)
		}
		if _, err := Update(root, "CLAUDE.md", entries); err != nil {
			b.Fatal(err)
		}
	}
}
