//go:build !windows

package main

import (
	"syscall"
	"time"
)

// selfCPU is this process's user and system time so far.
func selfCPU() (time.Duration, bool) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}
