//go:build windows

package fsutil

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// renameWait bounds the retries of a rename over a file another process has
// open: Windows refuses to replace it until the reader closes it, which for
// a parallel hook or an editor reading the doc is a matter of milliseconds.
const renameWait = 500 * time.Millisecond

// errSharingViolation is ERROR_SHARING_VIOLATION, missing from syscall.
const errSharingViolation syscall.Errno = 32

// rename is r.Rename retried while the target is in use by another process.
func rename(r *os.Root, from, to string) error {
	deadline := time.Now().Add(renameWait)
	for d := time.Millisecond; ; d = min(2*d, 50*time.Millisecond) {
		err := r.Rename(from, to)
		var errno syscall.Errno
		if err == nil || !errors.As(err, &errno) ||
			(errno != syscall.ERROR_ACCESS_DENIED && errno != errSharingViolation) ||
			time.Now().After(deadline) {
			return err
		}
		time.Sleep(d)
	}
}
