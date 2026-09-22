//go:build windows

package tests

import (
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// The test binary puts itself in a kill-on-close job at start-up. Cleanups do
// not run when the binary is killed or panics on -timeout, and a started server
// (a clustered one: a whole tree) then lives on for good, holding its port and
// its temp-dir executable. Every process a test starts inherits the job, and
// the job's only handle is this process's: however the binary ends, the kernel
// ends the rest with it. The handle is deliberately never closed.
func init() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	job, _, _ := k32.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		return
	}
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION; only BasicLimitInformation.LimitFlags is set.
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
	info.LimitFlags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	const jobObjectExtendedLimitInformation = 9
	if ok, _, _ := k32.NewProc("SetInformationJobObject").Call(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); ok == 0 {
		return
	}
	self, _ := syscall.GetCurrentProcess()
	k32.NewProc("AssignProcessToJobObject").Call(job, uintptr(self))
}

// setProcGroup: Windows has no fork()/Setpgid. A compiled server that
// clusters re-spawns itself (win32proc.c), so the children are ordinary
// processes; a new process group keeps console Ctrl+C from reaching them
// and killProcGroup terminates the whole tree.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcGroup terminates the started process and every descendant
// (`taskkill /T /F`), the counterpart of the POSIX group-wide SIGKILL: a
// clustered server's spawned workers would otherwise outlive the test and
// keep the temp-dir binary open, failing the TempDir cleanup.
func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
