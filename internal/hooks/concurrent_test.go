package hooks

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"auto-context/internal/docstate"
	"auto-context/internal/frontmatter"
	"auto-context/internal/testutil"
	"auto-context/internal/units"
)

// Environment of TestHelperStop when it runs as a child process.
const (
	helperRootEnv = "AUTO_CONTEXT_HELPER_ROOT"
	helperSIDEnv  = "AUTO_CONTEXT_HELPER_SID"
)

// concurrentUnits are the units every helper session documents.
var concurrentUnits = []string{"svc", "lib"}

// Parallel sessions in one repo are normal: several processes run a turn at
// once, each editing the same docs and auto-acking them. Afterwards every doc
// parses and is up to date, the cache is valid JSON, the root index has one
// marker pair and no temporary file is left (T-M4).
func TestConcurrentStopHooks(t *testing.T) {
	env := newRepo(t, true)
	testutil.WriteFile(t, env.Root, "context/lib.md", "# lib\n\nLib summary.\n")
	ack(t, env, "lib")
	// Code changed before the turns: each turn's docs edit acks it.
	testutil.WriteFile(t, env.Root, "svc/main.go", "package main\n// changed\n")
	testutil.WriteFile(t, env.Root, "lib/lib.go", "package lib\n// changed\n")

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	outs := make([][]byte, n)
	for i := range n {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestHelperStop$", "-test.count=1")
			cmd.Env = append(os.Environ(), helperRootEnv+"="+env.Root, fmt.Sprintf("%s=sess-%d", helperSIDEnv, i))
			outs[i], errs[i] = cmd.CombinedOutput()
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i])
		}
	}

	for _, u := range append([]string{units.RootUnit}, concurrentUnits...) {
		if _, _, err := frontmatter.Read(env.Root, filepath.Join(env.Root, "context", u+".md")); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range concurrentUnits {
		// The helpers ran: the last edit of some session is there.
		if data, _ := os.ReadFile(filepath.Join(env.Root, "context", u+".md")); !strings.Contains(string(data), "Edited by sess-") {
			t.Errorf("%s not edited by a helper:\n%s", u, data)
		}
	}
	cache, err := os.ReadFile(filepath.Join(env.Root, ".auto-context", "cache.v5.bin"))
	if err != nil || !strings.HasPrefix(string(cache), "acache 5\n") {
		t.Errorf("cache.v5.bin: %v %q", err, cache)
	}
	idx, err := os.ReadFile(filepath.Join(env.Root, "CLAUDE.md"))
	if err != nil || strings.Count(string(idx), "<!-- auto-context:units -->") != 1 || strings.Count(string(idx), "<!-- /auto-context:units -->") != 1 {
		t.Errorf("root index markers:\n%s", idx)
	}
	filepath.WalkDir(env.Root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), ".tmp") {
			t.Errorf("temporary file left: %s", p)
		}
		return nil
	})
	p, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Statuses() {
		if s.State != docstate.OK {
			t.Errorf("%s: %s %s", s.Unit, s.State, s.Note)
		}
	}
}

// TestHelperStop is one session of TestConcurrentStopHooks, run as a child
// process; on its own it does nothing.
func TestHelperStop(t *testing.T) {
	root, sid := os.Getenv(helperRootEnv), os.Getenv(helperSIDEnv)
	if root == "" {
		return
	}
	env := testEnv(root)
	if _, err := PromptSubmit(env, Input{SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	for _, u := range concurrentUnits {
		// Written atomically, as editors do: another session's ack must never
		// read a half-written doc.
		doc := filepath.Join(root, "context", u+".md")
		tmp := doc + "." + sid
		if err := os.WriteFile(tmp, []byte("# "+u+"\n\nEdited by "+sid+".\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, doc); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := Stop(env, Input{SessionID: sid}); err != nil || out != nil {
		t.Fatalf("Stop: out=%s err=%v", out, err)
	}
}

// BenchmarkStopNoChange is a Stop with nothing changed since the turn start
// in a repo of 200 documented units of 10 files each.
func BenchmarkStopNoChange(b *testing.B) {
	root := b.TempDir()
	testutil.WriteFile(b, root, "CLAUDE.md", "# Project\n")
	testutil.WriteFile(b, root, "context/_root.md", "# root\n\nRoot.\n")
	var us []string
	for i := range 200 {
		u := fmt.Sprintf("pkg/u%03d", i)
		us = append(us, u)
		testutil.WriteFile(b, root, u+"/go.mod", "module u\n")
		for k := range 9 {
			testutil.WriteFile(b, root, fmt.Sprintf("%s/f%d.go", u, k), "package u\n")
		}
		testutil.WriteFile(b, root, "context/"+u+".md", "# u\n\nU.\n")
	}
	env := testEnv(root)
	p, err := load(env)
	if err != nil {
		b.Fatal(err)
	}
	for _, u := range append(us, units.RootUnit) {
		if err := p.Ack(u, env.Now()); err != nil {
			b.Fatal(err)
		}
	}
	if err := p.SaveCache(); err != nil {
		b.Fatal(err)
	}
	in := Input{SessionID: "bench"}
	if _, err := PromptSubmit(env, in); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if out, err := Stop(env, in); err != nil || out != nil {
			b.Fatalf("out=%s err=%v", out, err)
		}
	}
}
