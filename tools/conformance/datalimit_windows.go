//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// programCapMB bounds each compiled test program's committed memory, the
// Windows counterpart of Linux's RLIMIT_DATA (ADR-01582). The run's own job
// (reaper_windows.go) caps the total; without a per-program cap a few
// programs that allocate until failure fill it, and every allocation in the
// run fails, the runner's included.
const programCapMB = 512

const createSuspended = 0x4

func limitChildData(memCapMB int) {}

// limitProgram starts cmd suspended; runTracked puts it in its own job before
// it runs a single instruction.
func limitProgram(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
}

var (
	k32             = syscall.NewLazyDLL("kernel32.dll")
	procCreateJob   = k32.NewProc("CreateJobObjectW")
	procSetJobInfo  = k32.NewProc("SetInformationJobObject")
	procAssignJob   = k32.NewProc("AssignProcessToJobObject")
	procOpenProcess = k32.NewProc("OpenProcess")
	procNtResume    = syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
)

// jobLimits is JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobLimits struct {
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
	limitActiveProcess                = 0x0008
	limitProcessMemory                = 0x0100
	limitJobMemory                    = 0x0200
	killOnJobClose                    = 0x2000
	jobObjectExtendedLimitInformation = 9
)

// newJob creates a kill-on-close job with the given limits, or exits: a run
// that cannot bound its programs does not start them.
func newJob(info *jobLimits) syscall.Handle {
	job, _, err := procCreateJob.Call(0, 0)
	if job == 0 {
		fail("CreateJobObjectW: " + err.Error())
	}
	if ok, _, err := procSetJobInfo.Call(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(info)), unsafe.Sizeof(*info)); ok == 0 {
		fail("SetInformationJobObject: " + err.Error())
	}
	return syscall.Handle(job)
}

// runTracked runs cmd; a program limitProgram marked runs in its own job of
// programCapMB, which closing after Wait also ends anything it left behind.
func runTracked(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createSuspended == 0 {
		return cmd.Wait()
	}
	info := jobLimits{LimitFlags: limitActiveProcess | limitJobMemory | killOnJobClose,
		ActiveProcessLimit: 64, JobMemoryLimit: programCapMB << 20}
	job := newJob(&info)
	defer syscall.CloseHandle(job)
	const access = 0x0001 | 0x0100 | 0x0800 // TERMINATE | SET_QUOTA | SUSPEND_RESUME
	h, _, err := procOpenProcess.Call(access, 0, uintptr(cmd.Process.Pid))
	if h == 0 {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("OpenProcess: %v", err)
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	if ok, _, err := procAssignJob.Call(uintptr(job), h); ok == 0 {
		cmd.Process.Kill()
		cmd.Wait()
		fmt.Fprintln(os.Stderr, "conformance: AssignProcessToJobObject:", err)
		return err
	}
	procNtResume.Call(h)
	return cmd.Wait()
}
