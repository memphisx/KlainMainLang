//go:build windows

// Windows stub for the child-process reaper. The runaway-child leak this
// guards against on POSIX is the setsid()-detached / re-exec'd cluster worker
// escaping kill(-pgid) (see reaper.go); the Windows spawn path uses
// DETACHED_PROCESS and a taskkill /T group teardown (procgroup_windows.go)
// instead, and Windows conformance runs are not the exposure. These keep the
// shared call sites (runTracked / startReaper) compiling on Windows.

package main

import "os/exec"

// runTracked runs the command; Start()+Wait() matches Run() while leaving room
// for the POSIX build to register the PID with its reaper.
func runTracked(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

// startReaper is a no-op on Windows; returns a no-op cleanup.
func startReaper(absWorkDir string, memCapMB int) func() { return func() {} }
