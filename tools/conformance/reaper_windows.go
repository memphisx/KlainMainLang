//go:build windows

// Windows bound for a conformance run. POSIX polls the workdir's processes
// and caps each program's data segment (reaper.go, datalimit_linux.go);
// Windows has job objects instead, so the runner puts itself in one before
// the first test is compiled. Every compiler, clang and test program it
// starts inherits the job: the run's committed memory is capped at
// -mem-cap-mb, and closing the runner (normal exit, Ctrl-C or a crash) ends
// everything still in it. Each test program also gets its own job of
// programCapMB (datalimit_windows.go).

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// startReaper bounds the run with a kill-on-close job object. memCapMB 0
// leaves the run unbounded, as it disables the POSIX reaper.
func startReaper(absWorkDir string, memCapMB int) func() {
	if memCapMB <= 0 {
		return func() {}
	}
	info := jobLimits{LimitFlags: limitActiveProcess | limitJobMemory | killOnJobClose,
		ActiveProcessLimit: 512, JobMemoryLimit: uintptr(memCapMB) << 20}
	job := newJob(&info)
	self, _ := syscall.GetCurrentProcess()
	if ok, _, err := procAssignJob.Call(uintptr(job), uintptr(self)); ok == 0 {
		fail("AssignProcessToJobObject: " + err.Error())
	}
	return func() {
		// The peak tells whether -workers fits under the cap. Windows counts a
		// refused commit in it too: a test asking for 32 GiB shows as 32 GiB
		// although the request failed and nothing was used.
		if ok, _, _ := k32.NewProc("QueryInformationJobObject").Call(uintptr(job), jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0); ok != 0 {
			fmt.Fprintf(os.Stderr, "conformance: job peak %d MB (cap %d MB; counts refused requests), largest process %d MB\n",
				info.PeakJobMemoryUsed>>20, memCapMB, info.PeakProcessMemoryUsed>>20)
		}
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "conformance: cannot bound the run: "+msg)
	os.Exit(2)
}
