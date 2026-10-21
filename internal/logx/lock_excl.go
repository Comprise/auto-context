package logx

import (
	"errors"
	"os"
	"time"
)

// lockStale is the age after which an exclusive lock file is taken to be left
// by a killed hook and removed.
const lockStale = 10 * time.Second

// lockExcl is lock without flock (Windows and others): the lock is the file
// path itself, created with O_EXCL and removed by unlock. It polls for at
// most lockWait (ok=false when the lock stays taken); a lock file older than
// lockStale is removed first.
func lockExcl(path string) (unlock func(), ok bool, err error) {
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false, err
		}
		if st, err := os.Lstat(path); err == nil && time.Since(st.ModTime()) > lockStale {
			_ = os.Remove(path)
		}
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		time.Sleep(lockPoll)
	}
}
