//go:build linux

package gitx

import "syscall"

// procAttr puts git in its own process group and has the kernel SIGKILL it
// when the hook dies (killed by the agent on its timeout), since the group
// kill of the hook no longer reaches git. Pdeathsig follows the thread that
// started git; Go only ends a thread when a goroutine exits while locked to
// it, which Run's callers do not do.
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
