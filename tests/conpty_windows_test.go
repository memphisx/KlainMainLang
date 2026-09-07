package tests

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// conpty_windows_test.go — a pseudo-console harness (ConPTY, Windows 10 1809+)
// so the console-mode branches of klain:tty / klain:tui can be exercised by
// tests rather than only by hand at a real console (ADR-00727). The child is
// attached to a real console (GetConsoleMode succeeds, GetConsoleScreenBufferInfo
// reports the requested size, ReadFile on STD_INPUT_HANDLE delivers key
// events); the harness writes VT input to it and collects the VT output the
// console renders. This is the Windows analogue of the manual pty runs
// recorded for macOS in ADR-00518.
//
// Everything here is syscall-level (no golang.org/x/sys): the pseudo-console
// API is five kernel32 exports plus CreateProcessW with a STARTUPINFOEX.

var (
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	procCreatePseudoConsole          = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole          = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole           = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttrList = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute    = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttrList     = kernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW               = kernel32.NewProc("CreateProcessW")
	procSetStdHandle                 = kernel32.NewProc("SetStdHandle")
	procSetConsoleCtrlHandler        = kernel32.NewProc("SetConsoleCtrlHandler")
)

const (
	procThreadAttributePseudoConsole = 0x00020016
	extendedStartupInfoPresent       = 0x00080000
)

type startupInfoEx struct {
	syscall.StartupInfo
	attrList uintptr
}

// conptyResult is what a pseudo-console run produced.
type conptyResult struct {
	Output   string // everything the console rendered, VT sequences included
	ExitCode uint32
}

// runInConPTY runs bin attached to a cols×rows pseudo-console. feed, if set,
// runs on its own goroutine with a writer to the console's input (VT bytes:
// "q", "\x1b[A" …) — write after a delay so the program has reached its read.
// The run is bounded by timeout; the child is killed past it.
func runInConPTY(t *testing.T, bin string, cols, rows int, timeout time.Duration, feed func(write func(string))) conptyResult {
	t.Helper()
	return runInConPTYArgs(t, []string{bin}, cols, rows, timeout, feed)
}

// runInConPTYArgs is runInConPTY for a command line (argv[0] plus arguments,
// each quoted as CreateProcessW expects).
func runInConPTYArgs(t *testing.T, argv []string, cols, rows int, timeout time.Duration, feed func(write func(string))) conptyResult {
	t.Helper()
	skipIfNoConPTY(t)
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	bin := strings.Join(quoted, " ")

	// The console's input pipe (we write, conhost reads) and output pipe
	// (conhost writes, we read).
	var inR, inW, outR, outW syscall.Handle
	if err := syscall.CreatePipe(&inR, &inW, nil, 0); err != nil {
		t.Fatalf("CreatePipe(in): %v", err)
	}
	if err := syscall.CreatePipe(&outR, &outW, nil, 0); err != nil {
		t.Fatalf("CreatePipe(out): %v", err)
	}

	var hpc uintptr
	size := uintptr(uint16(cols)) | uintptr(uint16(rows))<<16 // COORD by value
	if hr, _, _ := procCreatePseudoConsole.Call(size, uintptr(inR), uintptr(outW), 0, uintptr(unsafe.Pointer(&hpc))); hr != 0 {
		t.Fatalf("CreatePseudoConsole: HRESULT 0x%x", hr)
	}
	// conhost holds its own references now.
	syscall.CloseHandle(inR)
	syscall.CloseHandle(outW)

	// STARTUPINFOEX with the pseudo-console attribute.
	var attrSize uintptr
	procInitializeProcThreadAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&attrSize)))
	attrBuf := make([]byte, attrSize)
	attrList := uintptr(unsafe.Pointer(&attrBuf[0]))
	if ok, _, err := procInitializeProcThreadAttrList.Call(attrList, 1, 0, uintptr(unsafe.Pointer(&attrSize))); ok == 0 {
		t.Fatalf("InitializeProcThreadAttributeList: %v", err)
	}
	defer procDeleteProcThreadAttrList.Call(attrList)
	if ok, _, err := procUpdateProcThreadAttribute.Call(attrList, 0, procThreadAttributePseudoConsole, hpc, unsafe.Sizeof(hpc), 0, 0); ok == 0 {
		t.Fatalf("UpdateProcThreadAttribute: %v", err)
	}

	var si startupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.attrList = attrList
	var pi syscall.ProcessInformation
	cmdline, _ := syscall.UTF16PtrFromString(bin)
	// CreateProcess hands a child copies of the parent's standard handles
	// even with bInheritHandles=FALSE when they are not console handles (the
	// test binary's are pipes), and those would win over the pseudo-console's.
	// A terminal emulator has no standard handles at all, which is what makes
	// its shell pick up the console; mimic that for the duration of the spawn.
	// Go's os.Stdout keeps the handle it captured at start-up, so the test
	// process's own output is unaffected.
	var saved [3]syscall.Handle
	for i, which := range []int{syscall.STD_INPUT_HANDLE, syscall.STD_OUTPUT_HANDLE, syscall.STD_ERROR_HANDLE} {
		saved[i], _ = syscall.GetStdHandle(which)
		procSetStdHandle.Call(uintptr(uint32(which)), 0)
	}
	// A process started in a new process group (as `go test` and MSYS2's
	// bash start theirs) has Ctrl+C *disabled*, and that attribute is
	// inherited by its children — the pseudo-console would then generate
	// CTRL_C_EVENT and the child would ignore it. Restore normal Ctrl+C
	// processing in this process before spawning so the child inherits it;
	// a user's terminal never starts a program with it disabled.
	procSetConsoleCtrlHandler.Call(0, 0)
	ok, _, err := procCreateProcessW.Call(0, uintptr(unsafe.Pointer(cmdline)), 0, 0, 0, extendedStartupInfoPresent, 0, 0,
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)))
	for i, which := range []int{syscall.STD_INPUT_HANDLE, syscall.STD_OUTPUT_HANDLE, syscall.STD_ERROR_HANDLE} {
		procSetStdHandle.Call(uintptr(uint32(which)), uintptr(saved[i]))
	}
	if ok == 0 {
		procClosePseudoConsole.Call(hpc)
		t.Fatalf("CreateProcessW: %v", err)
	}
	syscall.CloseHandle(pi.Thread)

	// Drain the console's output until conhost closes the pipe.
	var out bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			var n uint32
			if err := syscall.ReadFile(outR, buf, &n, nil); err != nil || n == 0 {
				return
			}
			out.Write(buf[:n])
		}
	}()
	write := func(s string) {
		b := []byte(s)
		var n uint32
		_ = syscall.WriteFile(inW, b, &n, nil)
	}
	conptyChildPID.Store(pi.ProcessId)
	conptyResizeFn.Store(func(cols, rows int) {
		procResizePseudoConsole.Call(hpc, uintptr(uint16(cols))|uintptr(uint16(rows))<<16)
	})
	if feed != nil {
		go feed(write)
	}

	ev, _ := syscall.WaitForSingleObject(pi.Process, uint32(timeout/time.Millisecond))
	var code uint32
	if ev == syscall.WAIT_TIMEOUT {
		syscall.TerminateProcess(pi.Process, 124)
		syscall.WaitForSingleObject(pi.Process, 5000)
	}
	syscall.GetExitCodeProcess(pi.Process, &code)
	syscall.CloseHandle(pi.Process)
	// Closing the pseudo-console ends conhost, which closes our output pipe.
	procClosePseudoConsole.Call(hpc)
	syscall.CloseHandle(inW)
	wg.Wait()
	syscall.CloseHandle(outR)
	if ev == syscall.WAIT_TIMEOUT {
		t.Fatalf("program did not exit within %v under the pseudo-console; output so far:\n%s", timeout, out.String())
	}
	return conptyResult{Output: out.String(), ExitCode: code}
}

// conptyChildPID is the pid of the most recently spawned pseudo-console
// child, for pressCtrlC. The harness runs one child at a time per test
// binary (the E2E suite is serial), so a single slot suffices.
var conptyChildPID atomic.Uint32

// conptyResizeFn resizes the current pseudo-console (ResizePseudoConsole),
// set per run for a feed callback to trigger a SIGWINCH. Serial suite → one
// slot. Holds a func(cols, rows int).
var conptyResizeFn atomic.Value

// conptyResize resizes the running pseudo-console to cols×rows.
func conptyResize(cols, rows int) {
	if f, ok := conptyResizeFn.Load().(func(int, int)); ok && f != nil {
		f(cols, rows)
	}
}

// win32InputCtrlC is Ctrl+C in the console's "win32-input-mode" encoding
// (`ESC [ Vk ; Sc ; Uc ; Kd ; Cs ; Rc _`: virtual key 'C', scan code 46,
// char 0x03, key down then up, LEFT_CTRL_PRESSED) — the form Windows Terminal
// sends once the pseudo-console has asked for that mode (the `?9001h` it
// emits at start-up). A bare 0x03 byte reaches the input queue as a plain
// character under that mode and raises no CTRL_C_EVENT; this does, and the
// event is what Node maps to process.on('SIGINT') on Windows.
const win32InputCtrlC = "\x1b[67;46;3;1;8;1_\x1b[67;46;3;0;8;1_"

// pressCtrlC presses Ctrl+C on the pseudo-console through its input.
func pressCtrlC(write func(string)) { write(win32InputCtrlC) }

// skipIfNoConPTY skips where the pseudo-console API is missing (pre-1809
// Windows) or disabled by the environment.
func skipIfNoConPTY(t *testing.T) {
	t.Helper()
	if os.Getenv("KML_NO_CONPTY") == "1" {
		t.Skip("KML_NO_CONPTY=1")
	}
	if err := procCreatePseudoConsole.Find(); err != nil {
		t.Skipf("no CreatePseudoConsole on this Windows: %v", err)
	}
}
