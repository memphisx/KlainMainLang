//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcGroup: negative pid ⇒ signal the entire process group, reaping
// the clang -cc1 child along with the driver.
func killProcGroup(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
