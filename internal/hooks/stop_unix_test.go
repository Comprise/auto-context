//go:build unix

package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"auto-context/internal/testutil"
)

// When the journal cannot be saved, Stop says nothing rather than repeat the
// same feedback on every Stop (feedback continues the turn).
func TestStopNoFeedbackLoopWhenJournalUnsaved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	env := turn(t)
	testutil.WriteFile(t, env.Root, "svc/main.go", "package main\n// changed\n")
	state := filepath.Join(env.Root, ".auto-context", "state")
	if err := os.Chmod(state, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(state, 0o755)
	for i := range 2 {
		if out, err := Stop(env, Input{SessionID: stopSID}); out != nil {
			t.Errorf("Stop %d: %s, %v", i, out, err)
		}
	}
}

// The paused notice is not repeated when the journal cannot be saved.
func TestConfigPausedNoLoopWhenJournalUnsaved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	env := turn(t)
	testutil.WriteFile(t, env.Root, ".auto-context/config.yaml", "include: [svc\n")
	state := filepath.Join(env.Root, ".auto-context", "state")
	if err := os.Chmod(state, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(state, 0o755)
	for i := range 2 {
		if out, _ := Stop(env, Input{SessionID: stopSID}); out != nil {
			t.Errorf("Stop %d: %s", i, out)
		}
	}
}

// A failed auto-ack after the first notice is still reported, once.
func TestStopReportsAckFailureAfterNotice(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	env := turn(t)
	testutil.WriteFile(t, env.Root, "svc/main.go", "package main\n// changed\n")
	feedback(t, stop(t, env)) // "code changed in svc"
	testutil.WriteFile(t, env.Root, "context/svc.md", "# svc\n\nUpdated.\n")
	ctx := filepath.Join(env.Root, "context")
	if err := os.Chmod(ctx, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ctx, 0o755)
	if r := feedback(t, stop(t, env)); !strings.Contains(r, "auto-ack of svc failed") {
		t.Errorf("reason = %q", r)
	}
	if out := stop(t, env); out != nil {
		t.Errorf("reported twice: %s", out)
	}
}
