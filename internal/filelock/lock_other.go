//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package filelock

func lock(path string) (unlock func(), ok bool, err error) { return lockExcl(path) }
