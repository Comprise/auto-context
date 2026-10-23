//go:build unix

package fsutil

import "syscall"

// readFlags opens files for reading without blocking on a FIFO that has no
// writer and without making a terminal device our controlling terminal; the
// opened file's mode is checked afterwards. Reads of a regular file ignore
// both flags.
const readFlags = syscall.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOCTTY
