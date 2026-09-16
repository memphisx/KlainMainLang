//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestReaperKillsOrphanedWorkdirProcess is the regression guard for the
// conformance memory blowup: a process whose command line references the run's
// workdir, orphaned (not a live tracked child), must be reaped. This models the
// re-exec'd cluster worker / detached self-spawn that escapes kill(-pgid) and
// outlives its parent, which once accumulated to ~880 processes / ~42GB.
func TestReaperKillsOrphanedWorkdirProcess(t *testing.T) {
	workDir := t.TempDir()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// A looping script *inside* the workdir, so its command line carries the
	// workdir path the reaper matches on — exactly like a re-exec'd worker.bin.
	script := filepath.Join(absWorkDir, "worker.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile true; do sleep 0.2; done\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Launch it in its own process group and DON'T track it — an orphan.
	cmd := exec.Command("/bin/sh", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	// Wait in the background so that once the reaper kills it, the process is
	// actually reaped rather than lingering as a zombie (a zombie still answers
	// kill(pid,0), which would mask the kill).
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	// Sanity: it's alive.
	if syscall.Kill(pid, 0) != nil {
		t.Fatalf("child %d did not start", pid)
	}

	r := newReaper(absWorkDir, 0)
	// It must appear in the workdir scan and be classified as an orphan.
	procs, _ := r.scanWorkdirProcs()
	found := false
	for _, p := range procs {
		if p.pid == pid {
			found = true
			if !r.orphaned(p) {
				t.Fatalf("untracked workdir process %d should be orphaned", pid)
			}
		}
	}
	if !found {
		t.Fatalf("reaper scan did not find workdir process %d", pid)
	}

	r.sweepOnce()

	// After the sweep the process must exit within a moment.
	select {
	case <-waited:
		return // reaped — success
	case <-time.After(3 * time.Second):
		t.Fatalf("reaper did not kill orphaned workdir process %d", pid)
	}
}

// TestReaperSparesLiveChild ensures a currently-running, registered test binary
// (and thus its still-parented descendants) is NOT killed mid-test.
func TestReaperSparesLiveChild(t *testing.T) {
	workDir := t.TempDir()
	absWorkDir, _ := filepath.Abs(workDir)
	script := filepath.Join(absWorkDir, "live.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	registerChild(pid)
	defer unregisterChild(pid)
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	r := newReaper(absWorkDir, 0)
	r.sweepOnce()

	if syscall.Kill(pid, 0) != nil {
		t.Fatalf("reaper killed live registered child %d", pid)
	}
}
