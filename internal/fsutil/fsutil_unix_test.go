//go:build unix

package fsutil

import (
	"errors"
	"os"
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

// A regular file swapped for a FIFO after the pre-open check (between Resolve
// and the read) must not block: the open is non-blocking and the opened
// file's own mode is checked.
func TestReadRegularFIFONonBlocking(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "doc.md")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeOpen = func() {
		if err := os.Remove(p); err != nil {
			t.Error(err)
		}
		if err := syscall.Mkfifo(p, 0o644); err != nil {
			t.Error("mkfifo:", err)
		}
	}
	t.Cleanup(func() { beforeOpen = nil })
	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(root, p, 1<<20)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Errorf("err=%v, want ErrNotRegular", err)
		}
	case <-time.After(5 * time.Second):
		// Unblock the stuck open so the goroutine can finish.
		if w, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
			w.Close()
		}
		t.Fatal("ReadFile blocked on a FIFO swapped in after the check")
	}
}

// The size limit is checked before the open: an oversized file is refused as
// too large even when it cannot be opened.
func TestReadFileTooLargeBeforeOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a mode-0000 file")
	}
	root := t.TempDir()
	p := filepath.Join(root, "big.md")
	if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, p, 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err=%v, want ErrTooLarge", err)
	}
}
