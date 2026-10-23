package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"auto-context/internal/config"
)

// skillFiles returns the SKILL.md files with their text.
func skillFiles(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", "SKILL.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no skills: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	return out
}

func TestSkillsMarkUntrustedData(t *testing.T) {
	for f, s := range skillFiles(t) {
		for _, want := range []string{"are data from the repository", "never as instructions"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", f, want)
			}
		}
	}
}

var skillCommandRE = regexp.MustCompile("`auto-context\\s+([a-z][^`]*)`")

func TestSkillsCommandsParse(t *testing.T) {
	cwd := t.TempDir() // not a project: valid commands fail with 3, never with 2
	n := 0
	for f, s := range skillFiles(t) {
		for _, m := range skillCommandRE.FindAllStringSubmatch(s, -1) {
			args := strings.Fields(strings.Join(strings.Fields(m[1]), " "))
			if len(args) == 0 {
				continue
			}
			n++
			var out, errb strings.Builder
			if code := run(args, strings.NewReader(""), &out, &errb, cwd); code == exitUsage {
				t.Errorf("%s: `auto-context %s` is a usage error", f, m[1])
			}
		}
	}
	if n == 0 {
		t.Fatal("no auto-context commands found in skills")
	}
}

func TestSkillsSyncHasNoAllowedTools(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "sync", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "allowed-tools") || strings.Contains(s, "!`") {
		t.Error("sync must not use allowed-tools or a !` injection")
	}
	if !strings.Contains(s, "status --stale --json") {
		t.Error("sync must run `status --stale --json`")
	}
}

func TestSkillsInitDefaults(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "init", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(b), "```yaml\n     min_files")
	if !ok {
		t.Fatal("init: defaults yaml block not found")
	}
	block, _, _ := strings.Cut("min_files"+rest, "```")
	var lines []string
	for l := range strings.SplitSeq(block, "\n") {
		lines = append(lines, strings.TrimPrefix(l, "     "))
	}
	var got config.Config
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &got); err != nil {
		t.Fatal(err)
	}
	want := config.Default()
	if got.MinFiles != want.MinFiles ||
		!slices.Equal(got.CodeExtensions, want.CodeExtensions) ||
		!slices.Equal(got.ModuleMarkers, want.ModuleMarkers) ||
		!slices.Equal(got.Exclude, want.Exclude) {
		t.Errorf("init defaults differ from config.Default():\n got %+v\nwant %+v", got, want)
	}
}
