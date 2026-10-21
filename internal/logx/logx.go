// Package logx appends hook errors to <root>/.auto-context/log; it never fails loudly.
package logx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"auto-context/internal/fsutil"
	"auto-context/internal/textx"
)

const maxSize = 1 << 20

// Stderr receives entries that cannot be written to the log.
var Stderr io.Writer = os.Stderr

// Errorf appends "RFC3339Nano message" as one line to the log, rotating log to
// log.1 above 1 MiB. Nothing is written outside the repo (fsutil.Resolve);
// when the log cannot be written the entry goes to Stderr. Newlines, control
// and format characters (ANSI escapes, bidi overrides) become spaces.
func Errorf(root, format string, args ...any) {
	msg := textx.Clean(fmt.Sprintf(format, args...))
	if err := write(root, msg); err != nil {
		fmt.Fprintf(Stderr, "auto-context: %s (log unavailable: %v)\n", msg, err)
	}
}

func write(root, msg string) error {
	path, err := fsutil.Resolve(root, fsutil.StatePath(root, "log"))
	if err != nil {
		return err
	}
	if _, err := fsutil.Resolve(root, path+".1"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if st, err := os.Lstat(path); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", path, fsutil.ErrNotRegular) // a FIFO would block the open
	}
	var rotErr error
	if st, err := os.Stat(path); err == nil && st.Size() > maxSize {
		rotErr = rotate(root, path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	now := time.Now().Format(time.RFC3339Nano)
	if rotErr != nil && !recentlyNoted(f, rotationFailed) {
		// In the log itself, so it counts as an error: the log keeps growing.
		// Not on every write: that would double the error count.
		fmt.Fprintf(f, "%s %s: %s\n", now, rotationFailed, textx.Clean(rotErr.Error()))
	}
	_, err = fmt.Fprintf(f, "%s %s\n", now, msg)
	return err
}

const rotationFailed = "log rotation failed"

// lockWait bounds the wait for the rotation lock: a hook never hangs on it.
// lockPoll is the retry interval.
const (
	lockWait = 500 * time.Millisecond
	lockPoll = 5 * time.Millisecond
)

// rotate moves the oversized log to log.1 under the lock file log.lock and
// re-checks the size there: of parallel hooks that all saw the log oversized,
// the first rotates and the others find the fresh log and only append (an
// unguarded second rotation would replace the rotated log with the fresh
// one). A lock not taken within lockWait skips this rotation; a later write
// rotates.
func rotate(root, path string) error {
	lockPath, err := fsutil.Resolve(root, path+".lock")
	if err != nil {
		return err
	}
	if st, err := os.Lstat(lockPath); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", lockPath, fsutil.ErrNotRegular)
	}
	unlock, ok, err := lock(lockPath)
	if err != nil || !ok {
		return err
	}
	defer unlock()
	if st, err := os.Stat(path); err != nil || st.Size() <= maxSize {
		return nil
	}
	if os.Rename(path, path+".1") == nil {
		return nil
	}
	// Rename replaces log.1 on POSIX; elsewhere it may have to go first.
	_ = os.Remove(path + ".1")
	return os.Rename(path, path+".1")
}

// recentlyNoted reports whether the last 64 KiB of the log mention msg.
func recentlyNoted(f *os.File, msg string) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	off := max(st.Size()-64<<10, 0)
	buf := make([]byte, st.Size()-off)
	n, _ := f.ReadAt(buf, off)
	return strings.Contains(string(buf[:n]), msg)
}

// Writable reports whether Errorf could append to the log, writing nothing:
// an existing log is opened for appending; for a missing one a probe file is
// created next to it and removed (never the log: another hook may be
// creating it). A missing state directory is not checked (nil).
func Writable(root string) error {
	path, err := fsutil.Resolve(root, fsutil.StatePath(root, "log"))
	if err != nil {
		return err
	}
	if _, err := fsutil.Resolve(root, path+".1"); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	switch {
	case err == nil && !st.Mode().IsRegular():
		return fmt.Errorf("%s: %w", path, fsutil.ErrNotRegular)
	case err == nil:
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		return f.Close()
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	if _, err := os.Stat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), "log.probe-*")
	if err != nil {
		return err
	}
	f.Close()
	os.Remove(f.Name()) // a leftover probe is harmless: names never repeat
	return nil
}

// maxRead caps each log file read by Entries.
const maxRead = 4 << 20

// Entries returns the timestamps of the entries in log.1 and log, oldest
// first. A missing log is not an error.
func Entries(root string) ([]time.Time, error) {
	var out []time.Time
	for _, name := range []string{"log.1", "log"} {
		data, err := fsutil.ReadFile(root, fsutil.StatePath(root, name), maxRead)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			ts, _, _ := strings.Cut(line, " ")
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				out = append(out, t)
			}
		}
	}
	return out, nil
}
