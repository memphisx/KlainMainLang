//go:build windows

package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// The test binary puts itself in a kill-on-close, bounded job at start-up. Cleanups do
// not run when the binary is killed or panics on -timeout, and a started server
// (a clustered one: a whole tree) then lives on for good, holding its port and
// its temp-dir executable. Every process a test starts inherits the job, and
// the job's only handle is this process's: however the binary ends, the kernel
// ends the rest with it. The handle is deliberately never closed.
func init() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	// Without the job the run is unbounded, so its absence is fatal.
	job, _, err := k32.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		panic("tests: CreateJobObjectW: " + err.Error())
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
	// The job also bounds the run, the role the POSIX lanes' rlimits and the
	// conformance reaper play there: a program that respawns itself (a
	// self-spawn its guard misses) or allocates without end hits the kernel's
	// limit and fails, instead of exhausting the machine. The limits are
	// enforced at creation and allocation, so no runaway can outpace them.
	// Each process is capped like a conformance test program (-mem-cap-mb,
	// ADR-01582): a program, or a clang, that allocates past it fails there.
	const (
		killOnJobClose     = 0x2000
		limitActiveProcess = 0x0008
		limitProcessMemory = 0x0100
		limitJobMemory     = 0x0200
	)
	info.LimitFlags = killOnJobClose | limitActiveProcess | limitProcessMemory | limitJobMemory
	info.ActiveProcessLimit = 512
	info.ProcessMemoryLimit = 4 << 30 // committed memory of any one process
	info.JobMemoryLimit = 8 << 30     // committed memory of every process in the job
	const jobObjectExtendedLimitInformation = 9
	if ok, _, err := k32.NewProc("SetInformationJobObject").Call(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); ok == 0 {
		panic("tests: SetInformationJobObject: " + err.Error())
	}
	self, _ := syscall.GetCurrentProcess()
	if ok, _, err := k32.NewProc("AssignProcessToJobObject").Call(job, uintptr(self)); ok == 0 {
		panic("tests: AssignProcessToJobObject: " + err.Error())
	}
	go reapStuckPrograms(k32, job)
}

// stuckProgramAge bounds a compiled test program's run: one still running
// after it is stuck (a hung IPC exchange, a loop that never drains), and the
// helpers that run it wait without a deadline, so it would hold the whole run
// until -timeout.
const stuckProgramAge = 4 * time.Minute

// reapStuckPrograms ends every program in the job, under the temp directory
// tests build into, older than stuckProgramAge; the test that ran it then
// fails on its exit status. Compilers and the test binary itself are left
// alone.
func reapStuckPrograms(k32 *syscall.LazyDLL, job uintptr) {
	query := k32.NewProc("QueryInformationJobObject")
	imageName := k32.NewProc("QueryFullProcessImageNameW")
	// Matched without its volume: a RAM drive with no DOS device mapping
	// reports its images as \Device\<disk>\..., not under its drive letter.
	tmp := strings.ToLower(filepath.Clean(os.TempDir()))
	tmp = tmp[len(filepath.VolumeName(tmp)):] + `\`
	self := uint32(os.Getpid())
	for range time.Tick(10 * time.Second) {
		// JOBOBJECT_BASIC_PROCESS_ID_LIST (class 3): two counts, then the ids.
		var list struct {
			Assigned, Listed uint32
			IDs              [1024]uintptr
		}
		if ok, _, _ := query.Call(job, 3, uintptr(unsafe.Pointer(&list)), unsafe.Sizeof(list), 0); ok == 0 {
			continue
		}
		for _, id := range list.IDs[:list.Listed] {
			if uint32(id) == self {
				continue
			}
			const queryLimited, terminate = 0x1000, 0x0001
			h, err := syscall.OpenProcess(queryLimited|terminate, false, uint32(id))
			if err != nil {
				continue
			}
			var buf [512]uint16
			n := uint32(len(buf))
			var created, exited, kernel, user syscall.Filetime
			if ok, _, _ := imageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); ok != 0 &&
				syscall.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
				image := syscall.UTF16ToString(buf[:n])
				age := time.Since(time.Unix(0, created.Nanoseconds()))
				if strings.Contains(strings.ToLower(image), tmp) && age > stuckProgramAge {
					fmt.Fprintf(os.Stderr, "tests: ending %s (pid %d), still running after %s\n", image, id, age.Round(time.Second))
					syscall.TerminateProcess(h, 1)
				}
			}
			syscall.CloseHandle(h)
		}
	}
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
