//go:build unix && !linux

package gitx

import "syscall"

// procAttr puts git in its own process group. These systems have no
// Pdeathsig: a git outliving a killed hook is bounded only by Timeout.
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
