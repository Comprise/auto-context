//go:build unix

package fsutil

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO (or device) in the repo must be refused without opening it: opening
// a FIFO with no writer blocks forever.
func TestReadFileRefusesFIFO(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, ".gitignore")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip("mkfifo unsupported:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(root, fifo, 1<<20)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Errorf("err=%v, want ErrNotRegular", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFile blocked on a FIFO")
	}
}
