//go:build unix

package gitx

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// killGroup starts git in a process group of its own and makes the context's
// cancel kill the whole group, so children git started (a transport, a
// helper) die with it instead of holding its pipes.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
