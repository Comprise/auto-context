package units

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"auto-context/internal/testutil"
)

// fuzzAlphabet is what FuzzIgnoreMatchesGit builds rules and paths from:
// the glob syntax plus two letters, "." and "/".
const fuzzAlphabet = `ab*?[]!-\/.^`

func onlyAlphabet(s string, max int) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < max; i++ {
		if strings.IndexByte(fuzzAlphabet, s[i]) >= 0 {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// fuzzPath makes a clean relative path of s: no empty, "." or ".." segment.
func fuzzPath(s string) string {
	var segs []string
	for seg := range strings.SplitSeq(onlyAlphabet(s, 48), "/") {
		if seg != "" && seg != "." && seg != ".." {
			segs = append(segs, seg)
		}
	}
	return strings.Join(segs, "/")
}

// ignoredByWalk tells whether the walk skips rel (a file) with content as
// the only .gitignore, at the root: an ignored ancestor directory or rel
// itself.
func ignoredByWalk(content, rel string) bool {
	p := &patterns{ignores: map[string]ignoreFile{".": parseIgnore(strings.SplitSeq(content, "\n"))}}
	return newMatcher(p, true, func(string) {}).untracked(rel)
}

// The walk's .gitignore matcher agrees with git on random rules and paths.
func FuzzIgnoreMatchesGit(f *testing.F) {
	if runtime.GOOS == "windows" {
		f.Skip("backslash is a path separator on Windows")
	}
	if _, err := exec.LookPath("git"); err != nil {
		f.Skip("git not installed")
	}
	repo := f.TempDir()
	testutil.Git(f, repo, "init", "-q")
	for _, s := range [][3]string{
		{"*.b", "", "a/x.b"},
		{"a/", "!a/b", "a/b"},
		{"a/**", "!a/b", "a/b"},
		{"**/b", "", "a/a/b"},
		{"/**/b", "", "b"},
		{"***/b", "", "a/b"},
		{"a***b", "", "a/b"},
		{"a[!b]", "", "a/"},
		{"a[]b]", "", "a]"},
		{"a[b-a]", "", "ab"},
		{"a[\\]]", "", "a]"},
		{"a[^b]", "", "aa"},
		{"[a-]b", "", "-b"},
		{"a\\*", "", "a*"},
		{"a?b", "", "a/b"},
		{"a/*", "!a/b", "a/b/a"},
		{"!a", "a", "a"},
		{"a\\", "", "a"},
		{"[!", "", "a"},
		{"a[", "", "a["},
		{"b/a/**/", "", "b/a/a/b"},
	} {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, rule1, rule2, p string) {
		rule1, rule2, p = onlyAlphabet(rule1, 32), onlyAlphabet(rule2, 32), fuzzPath(p)
		if p == "" {
			return
		}
		content := rule1 + "\n" + rule2 + "\n"
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "-C", repo, "check-ignore", "--no-index", "-q", "--", p)
		out, err := cmd.CombinedOutput()
		var git bool
		var ee *exec.ExitError
		switch {
		case err == nil:
			git = true
		case errors.As(err, &ee) && ee.ExitCode() == 1:
		default:
			t.Fatalf("git check-ignore %q: %v\n%s", p, err, out)
		}
		if walk := ignoredByWalk(content, p); walk != git {
			t.Errorf(".gitignore %q, path %q: walk ignored = %v, git = %v", content, p, walk, git)
		}
	})
}
