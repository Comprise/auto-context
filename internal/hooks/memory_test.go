package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"auto-context/internal/testutil"
)

// A Stop on 50 000 files stays well inside the hook's memory: the turn-start
// snapshot and the tree's stats are not copied into extra maps. TotalAlloc
// across the Stop is measured: unlike HeapInuse it does not depend on when
// the GC ran.
func TestStopMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 50 000 files")
	}
	root := t.TempDir()
	testutil.WriteFile(t, root, "CLAUDE.md", "# Project\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n\nRoot.\n")
	for d := range 500 {
		dir := filepath.Join(root, "pkg", fmt.Sprintf("d%03d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range 100 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file_with_a_longer_name_%03d.go", f)), []byte("package d\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	env := testEnv(root)
	in := Input{SessionID: "mem"}
	if _, err := PromptSubmit(env, in); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := Stop(env, in); err != nil {
		t.Fatal(err)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// Measured 58 MiB (about 1.2 KiB a file, discovery included); the
	// bound leaves about half again for platforms and the race detector.
	alloc := ms.TotalAlloc - before.TotalAlloc
	if limit := uint64(90 << 20); alloc >= limit {
		t.Errorf("Stop allocated %d MiB, want < %d MiB", alloc>>20, limit>>20)
	}
	t.Logf("Stop allocated %d MiB", alloc>>20)
}
