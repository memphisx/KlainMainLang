//go:build !windows

package main

// boundRun bounds the run with a job object on Windows (job_windows.go);
// elsewhere it does nothing.
func boundRun() {}
