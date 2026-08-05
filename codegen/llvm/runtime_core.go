package llvm

import (
	"fmt"
	"runtime"
)

func (e *Emitter) ensureFree() {
	if e.usedFree {
		return
	}
	e.usedFree = true
	e.emitGlobal("declare void @free(ptr)")
}

func (e *Emitter) ensurePrintf() {
	if !e.usedPrintf {
		e.emitGlobal("declare i32 @printf(ptr noundef, ...)")
		e.usedPrintf = true
	}
}

func (e *Emitter) ensureDprintf() {
	if !e.usedDprintf {
		e.emitGlobal("declare i32 @dprintf(i32 noundef, ptr noundef, ...)")
		e.usedDprintf = true
	}
}

func (e *Emitter) ensureMalloc() {
	if !e.usedMalloc {
		e.emitGlobal("declare ptr @malloc(i64 noundef)")
		e.usedMalloc = true
	}
}

// ensureForkDecl/ensureCloseDecl/ensureReadDecl exist as their own
// dedicated ensure*() helpers (rather than being inlined into whichever
// ensure*() happened to need them first) because both
// ensureExecFileSync (runtime_process.go) and ensureHTTPRuntime
// (runtime_http.go, including this feature's __kml_http_cluster_fork)
// independently need fork()/close()/read() — inlining a `declare` in both
// places produces two identical `declare`s in the same module when a
// program uses both features, which LLVM rejects outright ("invalid
// redefinition of function"), confirmed directly against clang. The
// ensure*() pattern's whole point (the project's own instructions: "declared exactly once") is
// what this fixes.
func (e *Emitter) ensureForkDecl() {
	if !e.usedForkDecl {
		e.emitGlobal("declare i32 @fork()")
		e.usedForkDecl = true
	}
}

func (e *Emitter) ensureCloseDecl() {
	if !e.usedCloseDecl {
		e.emitGlobal("declare i32 @close(i32 noundef)")
		e.usedCloseDecl = true
	}
}

func (e *Emitter) ensureReadDecl() {
	if !e.usedReadDecl {
		e.emitGlobal("declare i64 @read(i32 noundef, ptr noundef, i64 noundef)")
		e.usedReadDecl = true
	}
}

func (e *Emitter) ensureFflushDecl() {
	if !e.usedFflushDecl {
		e.emitGlobal("declare i32 @fflush(ptr noundef)")
		e.usedFflushDecl = true
	}
}

func (e *Emitter) ensureExit() {
	if !e.usedExit {
		e.emitGlobal("declare void @exit(i32) noreturn")
		e.usedExit = true
	}
}

func (e *Emitter) ensureGetenv() {
	if !e.usedGetenv {
		e.emitGlobal("declare ptr @getenv(ptr noundef)")
		e.usedGetenv = true
	}
}

func (e *Emitter) ensureCalloc() {
	if !e.usedCalloc {
		e.emitGlobal("declare ptr @calloc(i64 noundef, i64 noundef)")
		e.usedCalloc = true
	}
}

func (e *Emitter) ensureRealloc() {
	if !e.usedRealloc {
		e.emitGlobal("declare ptr @realloc(ptr noundef, i64 noundef)")
		e.usedRealloc = true
	}
}

func (e *Emitter) ensureMemmove() {
	if !e.usedMemmove {
		e.emitGlobal("declare ptr @memmove(ptr noundef, ptr noundef, i64 noundef)")
		e.usedMemmove = true
	}
}

func (e *Emitter) ensureStrlen() {
	if !e.usedStrlen {
		e.emitGlobal("declare i64 @strlen(ptr noundef)")
		e.usedStrlen = true
	}
}

func (e *Emitter) ensureMemcpy() {
	if !e.usedMemcpy {
		e.emitGlobal("declare ptr @memcpy(ptr noundef, ptr noundef, i64 noundef)")
		e.usedMemcpy = true
	}
}

func (e *Emitter) ensureMemset() {
	if !e.usedMemset {
		e.emitGlobal("declare ptr @memset(ptr noundef, i32 noundef, i64 noundef)")
		e.usedMemset = true
	}
}

func (e *Emitter) ensureStrcmp() {
	if !e.usedStrcmp {
		e.emitGlobal("declare i32 @strcmp(ptr noundef, ptr noundef)")
		e.usedStrcmp = true
	}
}

func (e *Emitter) ensureSprintf() {
	if !e.usedSprintf {
		e.emitGlobal("declare i32 @sprintf(ptr noundef, ptr noundef, ...)")
		e.usedSprintf = true
	}
}

func (e *Emitter) ensureStrstr() {
	if !e.usedStrstr {
		e.emitGlobal("declare ptr @strstr(ptr noundef, ptr noundef)")
		e.usedStrstr = true
	}
}

func (e *Emitter) ensureStrncmp() {
	if !e.usedStrncmp {
		e.emitGlobal("declare i32 @strncmp(ptr noundef, ptr noundef, i64 noundef)")
		e.usedStrncmp = true
	}
}

func (e *Emitter) ensureAtoll() {
	if !e.usedAtoll {
		e.emitGlobal("declare i64 @atoll(ptr noundef)")
		e.usedAtoll = true
	}
}

func (e *Emitter) ensureSscanf() {
	if e.usedSscanf {
		return
	}
	e.usedSscanf = true
	e.emitGlobal("declare i32 @sscanf(ptr noundef, ptr noundef, ...)")
}

// ensureDaysFromCivil declares __kml_days_from_civil: days since the Unix
// epoch (1970-01-01) for a given proleptic-Gregorian (year, month[1-12],
// day[1-31]), via Howard Hinnant's days_from_civil algorithm
// (http://howardhinnant.github.io/date_algorithms.html). Chosen over calling
// libc's timegm() specifically to avoid needing a caller-allocated
// struct-tm-sized buffer whose exact byte layout/size varies by platform
// (glibc appends tm_gmtoff/tm_zone; so does Darwin, but not necessarily at
// the same offsets) — this is pure integer arithmetic, so it's portable by
// construction and works for any year, including pre-1970 (negative
// timestamps).

func (e *Emitter) ensureMathFuncs() {
	if e.usedMathFuncs {
		return
	}
	e.usedMathFuncs = true
	// On Linux these symbols live in libm, linked separately from libc — omitted
	// on macOS too since libSystem folds libm in and -lm is still accepted there
	// as a standard no-op flag, so this doesn't need a runtime.GOOS branch.
	e.requireLink("m")
	e.emitGlobal("declare double @floor(double noundef)")
	e.emitGlobal("declare double @ceil(double noundef)")
	e.emitGlobal("declare double @round(double noundef)")
	e.emitGlobal("declare double @trunc(double noundef)")
	e.emitGlobal("declare double @fabs(double noundef)")
	e.emitGlobal("declare double @sqrt(double noundef)")
	e.emitGlobal("declare double @pow(double noundef, double noundef)")
	e.emitGlobal("declare double @log(double noundef)")
	e.emitGlobal("declare double @log2(double noundef)")
	e.emitGlobal("declare double @log10(double noundef)")
	e.emitGlobal("declare double @sin(double noundef)")
	e.emitGlobal("declare double @cos(double noundef)")
	e.emitGlobal("declare double @tan(double noundef)")
	e.emitGlobal("declare double @hypot(double noundef, double noundef)")
	e.emitGlobal("declare double @asin(double noundef)")
	e.emitGlobal("declare double @acos(double noundef)")
	e.emitGlobal("declare double @atan(double noundef)")
	e.emitGlobal("declare double @atan2(double noundef, double noundef)")
	e.emitGlobal("declare double @sinh(double noundef)")
	e.emitGlobal("declare double @cosh(double noundef)")
	e.emitGlobal("declare double @tanh(double noundef)")
	e.emitGlobal("declare double @cbrt(double noundef)")
	e.emitGlobal("declare double @expm1(double noundef)")
	e.emitGlobal("declare double @log1p(double noundef)")
}

// ensureCtlz32 declares LLVM's own count-leading-zeros intrinsic for Math.clz32
// — a standard IR intrinsic (not a libc/libm call), so unlike ensureMathFuncs
// this needs no -lm link requirement.
func (e *Emitter) ensureCtlz32() {
	if e.usedCtlz32 {
		return
	}
	e.usedCtlz32 = true
	e.emitGlobal("declare i32 @llvm.ctlz.i32(i32, i1)")
}

func (e *Emitter) ensureArc4Random() {
	if !e.usedArc4Random {
		e.emitGlobal("declare i32 @arc4random()")
		e.usedArc4Random = true
	}
}

// ensureRandRandom emits a self-contained @__klain_math_random helper in LLVM IR
// that uses C89 rand()/srand()/time() — available on every libc — as the portable
// fallback for Math.random() on non-BSD platforms.
func (e *Emitter) ensureRandRandom() {
	if e.usedArc4Random { // reuse flag slot; only one path is ever taken
		return
	}
	e.usedArc4Random = true // mark as emitted so we don't emit it twice

	// C89 declarations needed by the helper.
	e.emitGlobal("declare i32  @rand()")
	e.emitGlobal("declare void @srand(i32 noundef)")
	e.emitGlobal("declare i64  @time(ptr)")

	// One-time seeded flag (thread-unsafe but fine for single-threaded scripts).
	e.emitGlobal("@__klain_rand_seeded = private global i1 false, align 1")

	// The helper function itself — defined fully in IR, no external symbols beyond the above.
	e.emitGlobal(`define private double @__klain_math_random() {
entry:
  %seeded = load i1, ptr @__klain_rand_seeded, align 1
  br i1 %seeded, label %gen, label %do_seed
do_seed:
  %t = call i64 @time(ptr null)
  %t32 = trunc i64 %t to i32
  call void @srand(i32 %t32)
  store i1 true, ptr @__klain_rand_seeded, align 1
  br label %gen
gen:
  %r = call i32 @rand()
  %rf = sitofp i32 %r to double
  %result = fdiv double %rf, 2147483647.0
  ret double %result
}`)
}

func (e *Emitter) ensureStrtoll() {
	if !e.usedStrtoll {
		e.emitGlobal("declare i64 @strtoll(ptr noundef, ptr noundef, i32 noundef)")
		e.usedStrtoll = true
	}
}

func (e *Emitter) ensureStrtod() {
	if !e.usedStrtod {
		e.emitGlobal("declare double @strtod(ptr noundef, ptr noundef)")
		e.usedStrtod = true
	}
}

func (e *Emitter) ensureQsort() {
	if !e.usedQsort {
		e.emitGlobal("declare void @qsort(ptr, i64, i64, ptr)")
		e.usedQsort = true
	}
}

// errnoAccessor returns the C symbol that exposes the current thread's
// errno as an `int*` on the host this compiler itself is running on (and
// will therefore also be clang'ing on — this project doesn't cross-compile
// today). glibc (Linux) and Darwin/BSD (macOS) use different symbol names
// for the same thing, since `errno` is a macro, not a portable global
// symbol — the same class of platform check emitMathRandom already makes
// for arc4random vs a portable fallback.
func errnoAccessor() string {
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		return "__error"
	default:
		return "__errno_location"
	}
}

// ensureErrnoAccessor declares the errnoAccessor() symbol exactly once.
// Extracted as its own singleton after ensureFsThrow and ensureProcessKill
// both independently declared it and collided ("invalid redefinition of
// function '__error'") the first time a program used both fs and
// process.kill — the same class of bug ADR-00023 already found and fixed
// once for fopen/fclose/fwrite; fixed the same way here.
func (e *Emitter) ensureErrnoAccessor() {
	if e.usedErrnoAccessor {
		return
	}
	e.usedErrnoAccessor = true
	e.emitGlobal(fmt.Sprintf("declare ptr @%s()", errnoAccessor()))
}

// ensureStrerror declares C strerror() exactly once — same singleton-sharing
// reasoning as ensureErrnoAccessor above.
func (e *Emitter) ensureStrerror() {
	if e.usedStrerror {
		return
	}
	e.usedStrerror = true
	e.emitGlobal("declare ptr @strerror(i32 noundef)")
}
