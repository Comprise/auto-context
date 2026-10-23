package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"auto-context/internal/frontmatter"
	"auto-context/internal/testutil"
)

// Parallel sessions in one repo: goroutines open the project, ack the same
// units and save the cache over and over. Afterwards every doc reads, the
// cache is valid JSON, the index holds one units section and no temporary
// file is left.
func TestConcurrentAckTouched(t *testing.T) {
	const workers, rounds = 8, 15
	root := t.TempDir()
	us := []string{"svc", "lib", "web"}
	testutil.WriteFile(t, root, ".auto-context/config.yaml", "include: [svc, lib, web]\n")
	testutil.WriteFile(t, root, "context/_root.md", "# root\n\nRoot.\n")
	for _, u := range us {
		testutil.WriteFile(t, root, u+"/a.go", "package "+u+"\n")
		testutil.WriteFile(t, root, "context/"+u+".md", "# "+u+"\n\nThe "+u+".\n")
	}
	var wg sync.WaitGroup
	errs := make(chan error, workers*rounds)
	for w := range workers {
		wg.Go(func() {
			for i := range rounds {
				p, err := Open(context.Background(), root)
				if err != nil {
					errs <- err
					return
				}
				if w%2 == 0 && i%5 == 0 {
					// A code edit in between: the acks rewrite the hash.
					if err := os.WriteFile(filepath.Join(root, "svc", "a.go"), []byte("package svc // "+time.Now().String()+"\n"), 0o644); err != nil {
						errs <- err
					}
				}
				_, failed, indexErr := p.AckTouched(us, time.Now())
				for _, err := range failed {
					errs <- err
				}
				if indexErr != nil {
					errs <- indexErr
				}
				if err := p.SaveCache(); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for _, u := range us {
		if _, _, err := frontmatter.Read(root, filepath.Join(root, "context", u+".md")); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".auto-context", "cache.v5.bin"))
	if err != nil || !strings.HasPrefix(string(data), "acache 5\n") {
		t.Errorf("cache.v5.bin: %v %q", err, data)
	}
	index, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if n := strings.Count(string(index), "<!-- auto-context:units -->"); n != 1 {
		t.Errorf("%d units sections:\n%s", n, index)
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), ".tmp") {
			t.Errorf("leftover %s", path)
		}
		return nil
	})
}
