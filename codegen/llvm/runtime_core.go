package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed coresrc/core.c
var coreSource string

// CoreSource is the core numeric/string runtime's C source (TDD-00240), behind
// kml_layout.h; its math calls need -lm (LibmLibs).
func CoreSource() string { return layoutHeader() + coreSource }

// UsesCoreC reports whether the program links coresrc/core.c.
func (e *Emitter) UsesCoreC() bool { return e.usedCoreC }

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

// ensureStdoutGlobal declares the libc `FILE *stdout` extern global (symbol per
// platform, stdoutGlobalSymbol) exactly once — shared by the startup setvbuf and
// the per-write fflush (ADR-00867). Never used on Windows.
func (e *Emitter) ensureStdoutGlobal() {
	if !e.usedStdoutGlobal {
		e.emitGlobal(fmt.Sprintf("@%s = external global ptr", e.stdoutGlobalSymbol()))
		e.usedStdoutGlobal = true
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

// ensureMmapDecl declares mmap(2) exactly once — used by the cluster close-flag
// page (runtime_http.go, TDD-00117). Prototyped with the POSIX signature; the
// off_t last arg is i64 on both host targets (arm64 Darwin, x86-64/arm64 Linux).
func (e *Emitter) ensureMmapDecl() {
	if !e.usedMmapDecl {
		e.emitGlobal("declare ptr @mmap(ptr noundef, i64 noundef, i32 noundef, i32 noundef, i32 noundef, i64 noundef)")
		e.usedMmapDecl = true
	}
}

// ensureSetenvDecl declares setenv(3) exactly once — shared by cluster's
// worker-id passing (runtime_cluster.go) and process.env writes (emit_process.go).
func (e *Emitter) ensureSetenvDecl() {
	if !e.usedSetenvDecl {
		e.emitGlobal("declare i32 @setenv(ptr noundef, ptr noundef, i32 noundef)")
		e.usedSetenvDecl = true
	}
}

func (e *Emitter) ensureReadDecl() {
	if !e.usedReadDecl {
		e.emitGlobal("declare i64 @read(i32 noundef, ptr noundef, i64 noundef)")
		e.usedReadDecl = true
	}
}

func (e *Emitter) ensureWriteDecl() {
	if !e.usedWriteDecl {
		e.emitGlobal("declare i64 @write(i32 noundef, ptr noundef, i64 noundef)")
		e.usedWriteDecl = true
	}
}

func (e *Emitter) ensureFcntlDecl() {
	if !e.usedFcntlDecl {
		e.emitGlobal("declare i32 @fcntl(i32 noundef, i32 noundef, ...)")
		e.usedFcntlDecl = true
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

// ensureCurrentRSS declares __kml_current_rss_bytes() -> i64: the process's
// *instantaneous* resident set size in bytes, matching real Node's
// process.memoryUsage().rss (ADR-00570) — not getrusage's ru_maxrss peak.
//
//   - Darwin: task_info(mach_task_self(), MACH_TASK_BASIC_INFO=20) →
//     resident_size (u64 at offset 8 of struct mach_task_basic_info; count = 12
//     natural_t words). Verified on Apple Silicon.
//   - Linux: /proc/self/statm field 2 (resident pages) × 4096. The page size is
//     4 KiB on x86-64 and arm64 Linux; parsing via fscanf("%*ld %ld"). Encoded
//     but verified on Mac only, like the other Linux /proc paths.
//
// On a read failure the result is 0 (the same zeroed fallback the field had
// before).
func (e *Emitter) ensureCurrentRSS() {
	if e.usedCurrentRSS {
		return
	}
	e.usedCurrentRSS = true
	if e.opts.Target.OS() == "windows" {
		// Defined by win32shim.c over GetProcessMemoryInfo (WorkingSetSize).
		e.emitGlobal("declare i64 @__kml_current_rss_bytes()")
		return
	}
	// Darwin: task_info(MACH_TASK_BASIC_INFO); Linux: /proc/self/statm x 4096.
	// Both in coresrc/core.c; 0 on a read failure.
	e.usedCoreC = true
	e.emitGlobal("declare i64 @__kml_current_rss_bytes()")
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

func (e *Emitter) ensureStrchr() {
	if !e.usedStrchr {
		e.emitGlobal("declare ptr @strchr(ptr noundef, i32 noundef)")
		e.usedStrchr = true
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

func (e *Emitter) ensureStrcasecmp() {
	if !e.usedStrcasecmp {
		e.emitGlobal("declare i32 @strcasecmp(ptr noundef, ptr noundef)")
		e.usedStrcasecmp = true
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

// ensureFptosiSat declares the saturating double→i64 intrinsic once. A plain
// `fptosi` of NaN or ±Infinity is poison, and every integer consumer downstream
// (an indexOf fromIndex, a slice bound, a Set hash) then behaves arbitrarily —
// the conformance run-timeouts of ADR-01061 were exactly that. The saturating
// form gives NaN → 0 and ±Infinity → INT64_MIN/MAX, which is JS's
// ToIntegerOrInfinity as seen by a consumer that clamps to a length.
func (e *Emitter) ensureFptosiSat() {
	if e.usedFptosiSat {
		return
	}
	e.usedFptosiSat = true
	e.emitGlobal("declare i64 @llvm.fptosi.sat.i64.f64(double)")
	e.emitGlobal("declare i64 @llvm.fptosi.sat.i64.f32(float)")
}

// floatInfLiteral / floatNegInfLiteral: ±Infinity as an LLVM constant for the
// given float IR type (a `float` constant is spelled as the exactly-representable
// double bit pattern).
func floatInfLiteral(irTy string) string    { return "0x7FF0000000000000" }
func floatNegInfLiteral(irTy string) string { return "0xFFF0000000000000" }

// emitFloatToI64 converts a float register to i64 with JS semantics for the
// non-finite cases (see ensureFptosiSat). `irTy` is "double" or "float".
func (e *Emitter) emitFloatToI64(irTy, ref string) string {
	e.ensureFptosiSat()
	r := e.freshReg()
	suffix := "f64"
	if irTy == "float" {
		suffix = "f32"
	}
	e.emitInstr(fmt.Sprintf("%s = call i64 @llvm.fptosi.sat.i64.%s(%s %s)", r, suffix, irTy, ref))
	return r
}

func (e *Emitter) ensureMathFuncs() {
	if e.usedMathFuncs {
		return
	}
	e.usedMathFuncs = true
	// On Linux these symbols live in libm, linked separately from libc — omitted
	// on macOS too since libSystem folds libm in and -lm is still accepted there
	// as a standard no-op flag, so this doesn't need a runtime.GOOS branch.
	e.requireLink("m")
	e.declareFn("floor", "declare double @floor(double noundef)")
	e.declareFn("ceil", "declare double @ceil(double noundef)")
	e.declareFn("round", "declare double @round(double noundef)")
	e.declareFn("trunc", "declare double @trunc(double noundef)")
	e.declareFn("fabs", "declare double @fabs(double noundef)")
	e.declareFn("sqrt", "declare double @sqrt(double noundef)")
	e.declareFn("pow", "declare double @pow(double noundef, double noundef)")
	e.declareFn("log", "declare double @log(double noundef)")
	e.declareFn("log2", "declare double @log2(double noundef)")
	e.declareFn("log10", "declare double @log10(double noundef)")
	e.declareFn("sin", "declare double @sin(double noundef)")
	e.declareFn("cos", "declare double @cos(double noundef)")
	e.declareFn("tan", "declare double @tan(double noundef)")
	e.declareFn("hypot", "declare double @hypot(double noundef, double noundef)")
	e.declareFn("asin", "declare double @asin(double noundef)")
	e.declareFn("acos", "declare double @acos(double noundef)")
	e.declareFn("atan", "declare double @atan(double noundef)")
	e.declareFn("atan2", "declare double @atan2(double noundef, double noundef)")
	e.declareFn("sinh", "declare double @sinh(double noundef)")
	e.declareFn("cosh", "declare double @cosh(double noundef)")
	e.declareFn("tanh", "declare double @tanh(double noundef)")
	e.declareFn("cbrt", "declare double @cbrt(double noundef)")
	e.declareFn("expm1", "declare double @expm1(double noundef)")
	e.declareFn("log1p", "declare double @log1p(double noundef)")
}

// ensureJsPow defines @__kml_js_pow: libm pow with the one place JS's
// exponentiation deviates from IEEE-754 pow — a base of magnitude exactly 1
// with an infinite exponent is NaN in JS (pow returns 1).
func (e *Emitter) ensureJsPow() {
	if e.usedJsPow {
		return
	}
	e.usedJsPow = true
	e.ensureMathFuncs()
	e.usedCoreC = true
	e.emitGlobal("declare double @__kml_js_pow(double, double)")
}

// ensureFloatMinMaxIntrinsics declares the IEEE-754 minimum/maximum LLVM
// intrinsics used by Math.min/Math.max's float path — unlike a plain
// fcmp/select fold, these propagate a NaN operand and order -0.0 below +0.0,
// which is exactly the JS spec's behavior for both functions.
func (e *Emitter) ensureFloatMinMaxIntrinsics() {
	if e.usedFloatMinMax {
		return
	}
	e.usedFloatMinMax = true
	e.emitGlobal("declare double @llvm.minimum.f64(double, double)")
	e.emitGlobal("declare double @llvm.maximum.f64(double, double)")
}

// ensureIPow defines __kml_ipow, an exact i64 integer exponentiation (base**exp)
// by squaring — used by the `**` operator when both operands are integers, so a
// result like `2 ** 62` stays exact rather than losing precision through a
// double round-trip. A negative exponent returns 0 (the integer-model
// truncation of 1/base^|exp|, matching this compiler's truncating integer `/`);
// exp == 0 returns 1 (including 0 ** 0 === 1, as in JS). i64 overflow wraps like
// every other integer op here.
func (e *Emitter) ensureIPow() {
	if e.usedIPow {
		return
	}
	e.usedIPow = true
	e.usedCoreC = true
	e.emitGlobal("declare i64 @__kml_ipow(i64, i64)")
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

// ensureCbrt emits a correctly-rounded cbrt (the public-domain fdlibm/musl
// algorithm) as @__kml_cbrt, so Math.cbrt is deterministic across platforms.
// Platform libm cbrt is NOT reliably correctly-rounded — glibc's runtime
// cbrt(27) returns 3.0000000000000004 (~1 ULP high) where macOS/BSD, V8, and
// fdlibm all give exactly 3 — which failed a cross-platform E2E test on Linux
// CI. A single Newton/Halley refinement of the libm result is not a fix (it
// merely moves the ULP error to other inputs, e.g. cbrt(0.001)); the fdlibm
// path below is correctly rounded to < 0.667 ULP. Self-contained bit math, no
// libm dependency. Comments key each step to the reference C.
func (e *Emitter) ensureCbrt() {
	if e.usedCbrt {
		return
	}
	e.usedCbrt = true
	e.usedCoreC = true
	e.emitGlobal("declare double @__kml_cbrt(double)")
}

func (e *Emitter) ensureArc4Random() {
	if !e.usedArc4Random {
		e.emitGlobal("declare i32 @arc4random()")
		e.usedArc4Random = true
	}
}

// ensureRandS declares UCRT rand_s (Windows Math.random backend).
func (e *Emitter) ensureRandS() {
	if !e.usedArc4Random {
		e.emitGlobal("declare i32 @rand_s(ptr)")
		e.usedArc4Random = true
	}
}

// ensureRandRandom declares @__klain_math_random (coresrc/core.c), which uses
// C89 rand()/srand()/time() — available on every libc — as the portable
// fallback for Math.random() on non-BSD platforms.
// ensureTime declares libc's time(3) once.
func (e *Emitter) ensureTime() {
	if e.declaredTime {
		return
	}
	e.declaredTime = true
	e.emitGlobal("declare i64 @time(ptr)")
}

func (e *Emitter) ensureRandRandom() {
	if e.usedArc4Random { // reuse flag slot; only one path is ever taken
		return
	}
	e.usedArc4Random = true // mark as emitted so we don't emit it twice

	// Defined in coresrc/core.c (rand/srand/time, seeded once per thread).
	e.usedCoreC = true
	e.emitGlobal("declare double @__klain_math_random()")
}

func (e *Emitter) ensureStrtoll() {
	if !e.usedStrtoll {
		e.emitGlobal("declare i64 @strtoll(ptr noundef, ptr noundef, i32 noundef)")
		e.usedStrtoll = true
	}
}

// ensureParseIntBase defines @__kml_parseint_base(ptr s) -> i32, the radix
// `parseInt` uses when its 2nd argument is omitted: real JS auto-detects base
// 16 only for a `"0x"`/`"0X"` prefix (past leading whitespace and an optional
// sign) and otherwise base 10 — it does NOT auto-detect octal from a leading
// `0` (that ES3 behavior was dropped in ES5), so `"077"` is 77 and `"08"` is
// 8, matching this returning 10 for both. strtoll accepts the `0x` prefix when
// handed base 16, so the caller just passes this result straight through.
func (e *Emitter) ensureParseIntBase() {
	if e.usedParseIntBase {
		return
	}
	e.usedParseIntBase = true
	e.usedCoreC = true
	e.emitGlobal("declare i32 @__kml_parseint_base(ptr)")
}

func (e *Emitter) ensureStrtod() {
	if !e.usedStrtod {
		e.emitGlobal("declare double @strtod(ptr noundef, ptr noundef)")
		e.usedStrtod = true
	}
}

// ensureStrtodJS defines @__kml_strtod_js, a strtod wrapper enforcing JS's
// ToNumber/parseFloat rule that the ONLY accepted string infinity spelling is
// the exact word "Infinity" (optionally signed). C's strtod additionally
// accepts "inf"/"infinity" and any case variant ("INF", "Infinity", …), all of
// which JS rejects as NaN. After strtod, when the result is ±Infinity AND the
// consumed token begins (past leading whitespace and an optional sign) with a
// letter — i.e. it is an infinity *word*, not a digit overflow like `1e999`
// (which JS does render Infinity) — the token must equal "Infinity" byte for
// byte; otherwise the endptr is rewound to the start so every caller treats it
// as "no conversion" exactly as it already does for genuine junk. strtod's
// "nan" word needs no handling: JS renders it NaN either way.
func (e *Emitter) ensureStrtodJS() {
	if e.usedStrtodJS {
		return
	}
	e.usedStrtodJS = true
	e.ensureStrtod()
	e.ensureStrncmp()
	e.usedCoreC = true
	e.emitGlobal("declare double @__kml_strtod_js(ptr, ptr)")
}

// ensureStrtodParseFloat defines @__kml_strtod_parsefloat, the parseFloat-only
// variant of __kml_strtod_js. It differs in one respect: a "0x"/"0X" hex prefix
// is NOT a valid StrDecimalLiteral for parseFloat, so parseFloat("0x10") is 0
// (it reads the leading "0" and stops at the "x"), whereas C's strtod — and
// JS's ToNumber/Number("0x10") — read the whole thing as 16. This wrapper
// detects a hex prefix past optional leading whitespace and an optional sign;
// when found it returns a signed zero and points endptr just past the "0" so
// the caller sees a successful one-digit conversion. Otherwise it delegates to
// __kml_strtod_js unchanged.
func (e *Emitter) ensureStrtodParseFloat() {
	if e.usedStrtodParseFloat {
		return
	}
	e.usedStrtodParseFloat = true
	e.ensureStrtodJS()
	e.usedCoreC = true
	e.emitGlobal("declare double @__kml_strtod_parsefloat(ptr, ptr)")
}

// ensureToNumber defines @__kml_to_number, JS's ToNumber for a string:
// strtod parses (skipping leading whitespace; C99 strtod also handles the
// "0x10" hex form and "Infinity", both as JS); then the tail must be
// whitespace-only for the value to count — "12px" is NaN, not strtod's 12.
// No conversion at all distinguishes "" / whitespace-only (0, as JS) from
// genuine junk (NaN) by scanning whether anything non-whitespace exists.
// Goes through __kml_strtod_js, not bare strtod, so C's extra infinity
// spellings ("inf"/"infinity"/case variants) are rejected as NaN — JS accepts
// only the exact word "Infinity".
func (e *Emitter) ensureToNumber() {
	if e.usedToNumber {
		return
	}
	e.usedToNumber = true
	e.ensureStrtodJS()
	e.ensureStrtoll()
	e.usedCoreC = true
	e.emitGlobal("declare double @__kml_to_number(ptr)")
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
func (e *Emitter) errnoAccessor() string {
	switch e.opts.Target.OS() {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		return "__error"
	case "windows":
		return "_errno"
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
	e.emitGlobal(fmt.Sprintf("declare ptr @%s()", e.errnoAccessor()))
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

// ensureDOMExceptionCode declares __kml_domexc_code (coresrc/core.c): a
// DOMException's legacy code from its name.
func (e *Emitter) ensureDOMExceptionCode() {
	if e.usedDOMExcCode {
		return
	}
	e.usedDOMExcCode = true
	e.usedCoreC = true
	e.emitGlobal("declare double @__kml_domexc_code(ptr)")
}
