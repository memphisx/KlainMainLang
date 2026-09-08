// emit_os.go — os module (TDD-00024): platform/EOL/homedir/tmpdir/hostname/
// totalmem/freemem/cpus. Platform selection (Linux vs. Darwin) is a Go-side
// runtime.GOOS branch, not a runtime IR branch — this compiler doesn't
// cross-compile (clang always targets the host it runs on), so which C API
// to call is a decision made once at codegen time, the same mechanism
// nodePlatformName() (process.platform) and ucontextLayout() (the fiber
// scheduler) already use.
package llvm

import (
	"fmt"
	"runtime"

	"KlainMainLang/ast"
)

// emitOSHomedir implements os.homedir(). On Windows it reads USERPROFILE and
// throws a catchable Error if unset (TDD-00177 Stage 1). On POSIX it mirrors
// libuv's uv_os_homedir: a *set* HOME wins (an empty one included — Node
// returns "" there, it is not treated as unset); only an unset HOME falls
// back to the passwd database (getpwuid(getuid())->pw_dir via the
// struct-layout-safe __kml_os_homedir_pw shim), and it throws only if that
// fails too — so a program run under a login without HOME (a bare cron/systemd
// unit, a container) still gets a home directory, matching real Node instead
// of throwing.
func (e *Emitter) emitOSHomedir(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.homedir() takes no arguments", pos.Line, pos.Col)
	}
	if runtime.GOOS == "windows" {
		val := e.emitGetenvCall(e.internString("USERPROFILE"))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, val.Ref))
		failL := e.freshLabel("os.homedir.fail")
		okL := e.freshLabel("os.homedir.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, failL, okL))
		e.emitLabel(failL)
		e.emitInternalThrow(e.internString("could not determine home directory"))
		e.emitLabel(okL)
		return Value{Ref: val.Ref, Ty: TypePtr}, nil
	}

	// POSIX. emitGetenvCall wraps HOME into a header string ("" stays "",
	// unset stays null); only the null (unset) case takes the passwd fallback.
	e.ensureStrHeaderRuntime()
	e.usedOSHomedirPw = true
	e.emitGlobal("declare ptr @__kml_os_homedir_pw()")

	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr", slot))

	home := e.emitGetenvCall(e.internString("HOME"))
	homeNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", homeNull, home.Ref))
	passwdL := e.freshLabel("os.homedir.passwd")
	useHomeL := e.freshLabel("os.homedir.usehome")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", homeNull, passwdL, useHomeL))

	doneL := e.freshLabel("os.homedir.done")

	e.emitLabel(useHomeL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s", home.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(passwdL)
	pw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_os_homedir_pw()", pw))
	pwNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", pwNull, pw))
	failL := e.freshLabel("os.homedir.fail")
	usePwL := e.freshLabel("os.homedir.usepw")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", pwNull, failL, usePwL))

	e.emitLabel(usePwL)
	pwStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", pwStr, pw))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s", pwStr, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("could not determine home directory"))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s", result, slot))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// UsesOSHomedirPw reports whether the program's os.homedir() needed the POSIX
// passwd-database fallback shim (getpwuid), so its C source gets linked in.
func (e *Emitter) UsesOSHomedirPw() bool { return e.usedOSHomedirPw }

// OSHomedirPwSource is the struct-layout-safe passwd-lookup shim behind the
// POSIX os.homedir() fallback: it returns getpwuid(getuid())->pw_dir (a
// pointer into a static buffer libc owns, so no free), or NULL if the current
// uid has no passwd entry. Compiled against the platform's own <pwd.h> so the
// struct passwd layout (which differs across Darwin/glibc) never has to be
// reproduced in IR.
func OSHomedirPwSource() string {
	return `#include <pwd.h>
#include <unistd.h>
#include <stddef.h>

const char *__kml_os_homedir_pw(void) {
  struct passwd *pw = getpwuid(getuid());
  return pw ? pw->pw_dir : NULL;
}
`
}

// emitOSTmpdir implements os.tmpdir(): getenv("TMPDIR"), falling back to
// the literal "/tmp" when unset — never throws, matching real Node.
func (e *Emitter) emitOSTmpdir(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.tmpdir() takes no arguments", pos.Line, pos.Col)
	}
	if runtime.GOOS == "windows" {
		// Node on Windows: TEMP, then TMP, then <SystemRoot|windir>\temp,
		// with one trailing backslash stripped unless it names a drive root
		// — the shim helper implements lib/os.js's algorithm exactly
		// (ADR-00739; supersedes the TDD-00177 Stage 1 literal fallback).
		if !e.usedOSTmpdirWin {
			e.usedOSTmpdirWin = true
			e.emitGlobal("declare ptr @__kml_os_tmpdir()")
		}
		e.ensureStrHeaderRuntime()
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_os_tmpdir()", raw))
		// Raw C string from the shim — wrap into a length-prefixed header
		// string so .length/concat/indexOf work.
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", result, raw))
		return Value{Ref: result, Ty: TypePtr}, nil
	}
	// POSIX Node: `process.env.TMPDIR || '/tmp'`, then strip a single trailing
	// '/' unless the path is just "/" (lib/os.js's tmpdir()). The `||` makes an
	// unset *or empty* TMPDIR fall back to /tmp; the trailing-slash strip means
	// `TMPDIR=/foo/` yields `/foo`, matching real Node.
	e.ensureStrHeaderRuntime()
	e.ensureMalloc()
	e.ensureMemcpy()
	val := e.emitGetenvCall(e.internString("TMPDIR"))
	tmpFallback := e.internString("/tmp")

	fallbackL := e.freshLabel("tmpdir.fallback")
	checkEmptyL := e.freshLabel("tmpdir.checkempty")
	haveL := e.freshLabel("tmpdir.have")
	doneL := e.freshLabel("tmpdir.done")

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, val.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, fallbackL, checkEmptyL))

	e.emitLabel(checkEmptyL)
	slen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", slen, val.Ref))
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, slen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, fallbackL, haveL))

	e.emitLabel(haveL)
	lastIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", lastIdx, slen))
	lastPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", lastPtr, val.Ref, lastIdx))
	lastCh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", lastCh, lastPtr))
	isSlash := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 47", isSlash, lastCh)) // '/'
	gt1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 1", gt1, slen))
	strip := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", strip, isSlash, gt1))
	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", newLen, strip, lastIdx, slen))
	stripped := e.emitStringExtract(val.Ref, "0", newLen)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(fallbackL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", result, stripped.Ref, haveL, tmpFallback, fallbackL))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitOSHostname implements os.hostname() via POSIX gethostname() into a
// 256-byte malloc'd buffer, throwing a catchable Error on failure. 256
// bytes is generous headroom over POSIX's own HOST_NAME_MAX (typically 64
// or 255 depending on platform).
func (e *Emitter) emitOSHostname(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.hostname() takes no arguments", pos.Line, pos.Col)
	}
	e.ensureGethostname()
	buf := e.emitStringScratch(256) // TDD-00120: length-prefixed, finalized below
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @gethostname(ptr %s, i64 256)", rc, buf))
	failed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", failed, rc))
	failL := e.freshLabel("os.hostname.fail")
	okL := e.freshLabel("os.hostname.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", failed, failL, okL))
	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("could not determine hostname"))
	e.emitLabel(okL)
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitOSTotalmem implements os.totalmem() — bytes of total system memory.
// Linux: sysconf(_SC_PHYS_PAGES) * sysconf(_SC_PAGESIZE), both constants
// (85, 30) verified by compiling and running a real probe on this
// project's own Linux dev sandbox, not recalled from memory. Darwin:
// sysctlbyname("hw.memsize", ...) — deliberately not sysconf at all, since
// Darwin's _SC_* numbering is a different, unverified-here enum; a
// string-keyed sysctl MIB name needs no numeric constant to get wrong (see
// docs/tdd/TDD-00024.md).
func (e *Emitter) emitOSTotalmem(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.totalmem() takes no arguments", pos.Line, pos.Col)
	}
	if runtime.GOOS == "darwin" {
		return e.emitOSSysctlbynameU64("hw.memsize"), nil
	}
	e.ensureSysconf()
	pages := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @sysconf(i32 85)", pages)) // _SC_PHYS_PAGES
	pagesize := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @sysconf(i32 30)", pagesize)) // _SC_PAGESIZE
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", result, pages, pagesize))
	return Value{Ref: result, Ty: TypeI64}, nil
}

// emitOSFreemem implements os.freemem() — bytes of currently-available
// system memory. Linux: sysconf(_SC_AVPHYS_PAGES) * sysconf(_SC_PAGESIZE),
// same verified-constant approach as totalmem. Darwin: UNVERIFIED on real
// hardware (mach_host_self/host_statistics/vm_statistics_data_t — see
// docs/tdd/TDD-00024.md and docs/adr/ADR-00090.md for the full design and
// risk discussion).
func (e *Emitter) emitOSFreemem(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.freemem() takes no arguments", pos.Line, pos.Col)
	}
	if runtime.GOOS == "darwin" {
		e.ensureMachVM()
		host := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @mach_host_self()", host))
		pagesize32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @getpagesize()", pagesize32))
		pagesize := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", pagesize, pagesize32))
		vmstat := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca [15 x i32], align 4", vmstat)) // vm_statistics_data_t: 15 x natural_t
		countPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", countPtr))
		e.emitInstr(fmt.Sprintf("store i32 15, ptr %s, align 4", countPtr))
		hs := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @host_statistics(i32 %s, i32 2, ptr %s, ptr %s)", hs, host, vmstat, countPtr)) // HOST_VM_INFO=2
		freeCount32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", freeCount32, vmstat)) // free_count is field 0
		freeCount := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", freeCount, freeCount32))
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", result, freeCount, pagesize))
		return Value{Ref: result, Ty: TypeI64}, nil
	}
	e.ensureSysconf()
	pages := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @sysconf(i32 86)", pages)) // _SC_AVPHYS_PAGES
	pagesize := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @sysconf(i32 30)", pagesize)) // _SC_PAGESIZE
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", result, pages, pagesize))
	return Value{Ref: result, Ty: TypeI64}, nil
}

// emitOSSysctlbynameU64 evaluates a Darwin sysctlbyname(name, ...) MIB
// lookup expecting a uint64_t result, returning 0 if the sysctl fails or
// is absent (e.g. "hw.cpufrequency" on Apple Silicon, which has no fixed
// clock-speed model — see docs/tdd/TDD-00024.md).
func (e *Emitter) emitOSSysctlbynameU64(name string) Value {
	e.ensureSysctlbyname()
	e.ensureMalloc()
	keyPtr := e.internString(name)
	valPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", valPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", valPtr))
	sizePtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sizePtr))
	e.emitInstr(fmt.Sprintf("store i64 8, ptr %s, align 8", sizePtr))
	e.emitInstr(fmt.Sprintf("call i32 @sysctlbyname(ptr %s, ptr %s, ptr %s, ptr null, i64 0)", keyPtr, valPtr, sizePtr))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, valPtr))
	return Value{Ref: result, Ty: TypeI64}
}

// emitOSCpus implements os.cpus() — see runtime_os.go's ensureOSCpusLinux/
// ensureOSCpusDarwin for the real per-platform implementations. Darwin is
// UNVERIFIED on real hardware (docs/tdd/TDD-00024.md).
func (e *Emitter) emitOSCpus(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.cpus() takes no arguments", pos.Line, pos.Col)
	}
	result := e.freshReg()
	if runtime.GOOS == "windows" {
		e.ensureOSCpusWin()
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_os_cpus_win()", result))
	} else if runtime.GOOS == "darwin" {
		e.ensureOSCpusDarwin()
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_os_cpus_darwin()", result))
	} else {
		e.ensureOSCpusLinux()
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_os_cpus_linux()", result))
	}
	return Value{Ref: result, Ty: ArrayOf(CPUInfoType())}, nil
}
