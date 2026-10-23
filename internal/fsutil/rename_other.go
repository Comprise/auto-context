//go:build !windows

package fsutil

import "os"

// rename is r.Rename: POSIX replaces a file that others have open.
func rename(r *os.Root, from, to string) error { return r.Rename(from, to) }
