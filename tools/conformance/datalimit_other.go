//go:build !linux

package main

import "os/exec"

// The data-segment limit is Linux-only: macOS does not apply RLIMIT_DATA to
// mmap'd memory, and Windows has no rlimits. The reaper's RSS ceiling still
// applies.
func limitChildData(memCapMB int) {}

func limitProgram(cmd *exec.Cmd) {}
