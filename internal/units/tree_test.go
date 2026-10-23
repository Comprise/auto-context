package units

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"auto-context/internal/config"
)

// Classify after Discover neither spends a shared glob budget nor adds
// warnings: the tree is the same after any number of calls.
func TestClassifyDoesNotMutate(t *testing.T) {
	old := maxGlobWork
	maxGlobWork = 1000 // Discover spends 36; 10 000 shared calls would not fit
	defer func() { maxGlobWork = old }()
	root := t.TempDir()
	mkfiles(t, root, "keep.go", "axb.go", "d1/x.go")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("a*b.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := discover(t, root, config.Default())
	warnings, count := tr.Warnings(), tr.WarningCount()
	for range 10_000 {
		if k, _ := tr.Classify("axb.go"); k != KindIgnore {
			t.Fatalf("axb.go classified %v: the glob budget ran out", k)
		}
	}
	if k, u := tr.Classify("keep.go"); k != KindCode || u != RootUnit {
		t.Errorf("keep.go = %v %q", k, u)
	}
	if got := tr.Warnings(); !slices.Equal(got, warnings) || tr.WarningCount() != count {
		t.Errorf("warnings changed: %v (%d) -> %v (%d)", warnings, count, got, tr.WarningCount())
	}
}

// A discovered tree may be read from several goroutines (run with -race).
func TestTreeConcurrentReads(t *testing.T) {
	root := t.TempDir()
	var files []string
	for i := range 4 {
		files = append(files, numbered(fmt.Sprintf("pkg/p%d", i), 3)...)
	}
	mkfiles(t, root, files...)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("gen*/\n*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := discover(t, root, config.Default())
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				rel := fmt.Sprintf("x%d/y%d/z.go", g, i)
				if k, _ := tr.Classify(rel); k != KindCode {
					t.Errorf("%s = %v", rel, k)
					return
				}
				if k, _ := tr.Classify(fmt.Sprintf("gen%d/a.go", i)); k != KindIgnore {
					t.Errorf("gen%d/a.go = %v", i, k)
					return
				}
				_ = tr.UnitOf(rel)
				_ = tr.Units()
				_ = tr.Files("pkg/p0")
				_ = tr.HashFiles("pkg/p0")
				_ = tr.Tracked()
				_ = tr.Warnings()
				_, _, _ = tr.Stat("pkg/p0/fa.go")
			}
		})
	}
	wg.Wait()
}

// One Classifier spends one glob budget over all its calls (a Stop or an
// Orphans pass stays bounded), and never touches the tree.
func TestClassifierSharesBudget(t *testing.T) {
	old := maxGlobWork
	maxGlobWork = 1000 // about 27 matches of "a*b.go" against "axb.go"
	defer func() { maxGlobWork = old }()
	root := t.TempDir()
	mkfiles(t, root, "keep.go", "axb.go", "d1/x.go")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("a*b.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := discover(t, root, config.Default())
	warnings, count := tr.Warnings(), tr.WarningCount()
	c := tr.Classifier()
	if k, _ := c.Classify("axb.go"); k != KindIgnore {
		t.Fatalf("first call: axb.go = %v", k)
	}
	spent := false
	for range 100 {
		if k, _ := c.Classify("axb.go"); k == KindCode {
			spent = true // the shared budget ran out: glob rules skipped
			break
		}
	}
	if !spent {
		t.Error("100 calls through one Classifier never exhausted its budget")
	}
	if k, u := c.Classify("d1/x.go"); k != KindCode || u != RootUnit {
		t.Errorf("d1/x.go = %v %q", k, u)
	}
	if k, _ := tr.Classifier().Classify("axb.go"); k != KindIgnore {
		t.Errorf("a fresh Classifier: axb.go = %v", k)
	}
	if k, _ := tr.Classify("axb.go"); k != KindIgnore {
		t.Errorf("Tree.Classify: axb.go = %v", k)
	}
	if got := tr.Warnings(); !slices.Equal(got, warnings) || tr.WarningCount() != count {
		t.Errorf("warnings changed: %v (%d) -> %v (%d)", warnings, count, got, tr.WarningCount())
	}
}
