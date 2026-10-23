// Package filelock takes short exclusive locks on files shared by parallel
// hook processes (the log rotation, the hash cache).
package filelock

import (
	"fmt"
	"os"
	"time"

	"auto-context/internal/fsutil"
)

// Wait bounds the wait for a lock: a hook never hangs on one. poll is the
// retry interval.
const (
	Wait = 500 * time.Millisecond
	poll = 5 * time.Millisecond
)

// Lock takes an exclusive lock on path, creating the file, polling for at
// most Wait (ok=false when the lock stays taken). unlock releases it.
//
// path is the lexical path the caller checked with fsutil.Resolve, not the
// resolved one: a symlink there (committed by a hostile repo) or another
// non-regular file is refused with fsutil.ErrNotRegular, so the lock never
// creates, locks or removes a link's target (or blocks on a FIFO). The
// unix open also uses O_NOFOLLOW against a link appearing after the check.
func Lock(path string) (unlock func(), ok bool, err error) {
	if st, err := os.Lstat(path); err == nil && !st.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s: %w", path, fsutil.ErrNotRegular)
	}
	return lock(path)
}
