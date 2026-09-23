//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// availableCommitBytes answers how much more memory this process tree may
// commit before Windows refuses (`ERROR_COMMITMENT_LIMIT`, 0x5af) — the
// MEMORYSTATUSEX.ullAvailPageFile figure. On a box with a large RAM drive the
// commit limit, not physical RAM, is what 8 parallel `clang -O2` runs hit
// (ADR-01060: the "clang" bucket of the 2026-09-22 run was clang itself
// failing a VirtualProtect for lack of commit). 0 = unknown.
func availableCommitBytes() uint64 {
	type memoryStatusEx struct {
		Length               uint32
		MemoryLoad           uint32
		TotalPhys            uint64
		AvailPhys            uint64
		TotalPageFile        uint64
		AvailPageFile        uint64
		TotalVirtual         uint64
		AvailVirtual         uint64
		AvailExtendedVirtual uint64
	}
	k32 := syscall.NewLazyDLL("kernel32.dll")
	proc := k32.NewProc("GlobalMemoryStatusEx")
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&st)))
	if r == 0 {
		return 0
	}
	return st.AvailPageFile
}
