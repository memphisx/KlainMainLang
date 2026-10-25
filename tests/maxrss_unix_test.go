//go:build !windows

package tests

import (
	"os"
	"syscall"
)

// maxRSS is a finished process's peak resident set size, as getrusage
// reports it (KiB on Linux, bytes on macOS).
func maxRSS(ps *os.ProcessState) (int64, bool) {
	ru, _ := ps.SysUsage().(*syscall.Rusage)
	if ru == nil {
		return 0, false
	}
	return int64(ru.Maxrss), true
}
