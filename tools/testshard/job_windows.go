//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// boundRun puts the runner in a kill-on-close job that bounds the whole run.
// Each test process puts itself in its own bounded job (the tests package's
// init); Windows nests jobs, so those sit inside this one and every limit on
// the way up applies. Without it, -j processes would each get the per-binary
// total, and the run as a whole none.
func boundRun() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	job, _, err := k32.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		fatal("CreateJobObjectW: " + err.Error())
	}
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
	var info struct {
		PerProcessUserTimeLimit, PerJobUserTimeLimit int64
		LimitFlags                                   uint32
		MinimumWorkingSetSize, MaximumWorkingSetSize uintptr
		ActiveProcessLimit                           uint32
		Affinity                                     uintptr
		PriorityClass, SchedulingClass               uint32
		IoInfo                                       [6]uint64
		ProcessMemoryLimit, JobMemoryLimit           uintptr
		PeakProcessMemoryUsed, PeakJobMemoryUsed     uintptr
	}
	const (
		killOnJobClose     = 0x2000
		limitActiveProcess = 0x0008
		limitJobMemory     = 0x0200
	)
	info.LimitFlags = killOnJobClose | limitActiveProcess | limitJobMemory
	info.ActiveProcessLimit = 1024
	info.JobMemoryLimit = 8 << 30 // committed memory of the whole run
	const jobObjectExtendedLimitInformation = 9
	if ok, _, err := k32.NewProc("SetInformationJobObject").Call(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); ok == 0 {
		fatal("SetInformationJobObject: " + err.Error())
	}
	self, _ := syscall.GetCurrentProcess()
	if ok, _, err := k32.NewProc("AssignProcessToJobObject").Call(job, uintptr(self)); ok == 0 {
		fatal("AssignProcessToJobObject: " + err.Error())
	}
}
