//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package logx

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// lock takes an exclusive flock on path, polling for at most lockWait
// (ok=false when it stays taken). The kernel drops the lock when the holder
// exits, so a killed hook leaves none behind.
func lock(path string) (unlock func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	rc, err := f.SyscallConn() // unlike Fd(), leaves the file in blocking mode
	if err != nil {
		f.Close()
		return nil, false, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		var ferr error
		err := rc.Control(func(fd uintptr) {
			ferr = syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB)
		})
		if err == nil {
			err = ferr
		}
		if err == nil {
			return func() { f.Close() }, true, nil // closing releases the lock
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, false, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, false, nil
		}
		time.Sleep(lockPoll)
	}
}
