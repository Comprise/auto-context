package rootindex

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzUpdate checks that Update is idempotent: once it succeeds, a second
// Update with the same entries changes nothing and Check finds no fault.
func FuzzUpdate(f *testing.F) {
	f.Add("\r", "a", "b")
	f.Add("", "a", "")
	f.Add("# T\r\n\r\ntext\r\n", "svc", "S")
	f.Add("# T\n```\ncode", "a", "b")
	f.Add("\ufeff<!-- auto-context:units -->\n- `old`\n<!-- /auto-context:units -->\n", "a", "<!-- x -->")
	f.Add("```\n<!-- auto-context:units -->\n```\n~~~a\n````\n", "a/b", "c")
	f.Fuzz(func(t *testing.T, content, unit, summary string) {
		root := t.TempDir()
		p := filepath.Join(root, "CLAUDE.md")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		es := []Entry{{unit, summary}, {"x", summary}}
		if _, err := Update(root, "CLAUDE.md", es); err != nil {
			return
		}
		first, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := Update(root, "CLAUDE.md", es)
		if err != nil || ch {
			second, _ := os.ReadFile(p)
			t.Fatalf("second Update: ch=%v err=%v\nfirst  %q\nsecond %q", ch, err, first, second)
		}
		if err := Check(root, "CLAUDE.md"); err != nil {
			t.Fatalf("Check after Update: %v\n%q", err, first)
		}
	})
}
