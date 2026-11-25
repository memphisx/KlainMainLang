//go:build !windows

package main

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSS is a finished child's peak resident bytes.
func peakRSS(ps *os.ProcessState) (int64, bool) {
	if ps == nil {
		return 0, false
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0, false
	}
	if runtime.GOOS == "darwin" {
		return int64(ru.Maxrss), true // bytes
	}
	return int64(ru.Maxrss) << 10, true // KiB
}
