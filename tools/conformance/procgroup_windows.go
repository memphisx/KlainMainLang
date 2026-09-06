//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcGroup: no group-wide signal on Windows, so terminate the whole
// process tree (`taskkill /T /F`) — this reaps the clang -cc1 child along
// with the driver, the case the POSIX group SIGKILL exists for. WaitDelay in
// killableCommand still bounds the pipe wait.
func killProcGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	return cmd.Process.Kill()
}
