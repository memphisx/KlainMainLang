//go:build !windows

package tests

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts cmd (and every process it forks) in its own process
// group so killProcGroup can reach forked http.listen workers too — see
// startHTTPClusterServer.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcGroup SIGKILLs the whole group rooted at the started cmd.
func killProcGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
