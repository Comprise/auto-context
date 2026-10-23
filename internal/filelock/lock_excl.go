package filelock

import (
	"errors"
	"os"
	"time"
)

// staleAfter is the age after which an exclusive lock file is taken to be left
// by a killed hook and removed.
const staleAfter = 10 * time.Second

// beforeRemove is a test hook run between the staleness check and the removal.
var beforeRemove func()

// lockExcl is lock without flock (Windows and others): the lock is the file
// path itself, created with O_EXCL and removed by unlock. It polls for at
// most Wait (ok=false when the lock stays taken); a lock file older than
// staleAfter is removed first.
func lockExcl(path string) (unlock func(), ok bool, err error) {
	deadline := time.Now().Add(Wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false, err
		}
		if st, err := os.Lstat(path); err == nil && time.Since(st.ModTime()) > staleAfter {
			// On Windows a FileInfo from Lstat reads its file ID lazily, by
			// path, on the first SameFile: load it now, or it would be the
			// ID of whatever file holds the path at the comparison.
			os.SameFile(st, st)
			if beforeRemove != nil {
				beforeRemove()
			}
			// Remove only the file that was judged stale: a fresh lock
			// that took its place is another holder's.
			if cur, err := os.Lstat(path); err == nil && os.SameFile(st, cur) && time.Since(cur.ModTime()) > staleAfter {
				_ = os.Remove(path)
			}
		}
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		time.Sleep(poll)
	}
}
