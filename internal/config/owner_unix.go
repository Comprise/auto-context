//go:build unix

package config

import (
	"os"
	"syscall"
)

// ownedByUser reports whether fi belongs to the current user, so a marker
// planted by someone else (e.g. in a shared /tmp) is not trusted.
func ownedByUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == os.Getuid()
}
