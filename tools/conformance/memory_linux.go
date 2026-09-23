//go:build linux

package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// availableCommitBytes answers the memory the kernel will still hand out
// without the OOM killer or an ENOMEM: MemAvailable from /proc/meminfo (the
// kernel's own "how much can start without swapping" estimate), plus free
// swap. Inside a container with a cgroup memory limit the limit is the real
// ceiling, so the smaller of the two wins. 0 = unknown.
func availableCommitBytes() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	var avail, swapFree uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemAvailable:":
			avail = kb * 1024
		case "SwapFree:":
			swapFree = kb * 1024
		}
	}
	total := avail + swapFree
	// cgroup v2 / v1 memory limit, when one is set.
	for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if lim, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil && lim > 0 && lim < total {
			total = lim
		}
		break
	}
	return total
}
