//go:build windows

package tests

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setProcGroup: Windows has no fork()/Setpgid. A compiled server that
// clusters re-spawns itself (win32proc.c), so the children are ordinary
// processes; a new process group keeps console Ctrl+C from reaching them
// and killProcGroup terminates the whole tree.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcGroup terminates the started process and every descendant
// (`taskkill /T /F`), the counterpart of the POSIX group-wide SIGKILL: a
// clustered server's spawned workers would otherwise outlive the test and
// keep the temp-dir binary open, failing the TempDir cleanup.
func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
