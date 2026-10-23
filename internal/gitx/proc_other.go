//go:build !unix

package gitx

import "os/exec"

// killGroup keeps exec's default cancel (kill git only); WaitDelay still
// bounds the wait for pipes a child holds.
func killGroup(cmd *exec.Cmd) {}
