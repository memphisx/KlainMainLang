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

// ensureMmapDecl declares mmap(2) exactly once — used by the cluster close-flag
// page (runtime_http.go, TDD-00117). Prototyped with the POSIX signature; the
// off_t last arg is i64 on both host targets (arm64 Darwin, x86-64/arm64 Linux).
func (e *Emitter) ensureMmapDecl() {
	if !e.usedMmapDecl {
		e.emitGlobal("declare ptr @mmap(ptr noundef, i64 noundef, i32 noundef, i32 noundef, i32 noundef, i64 noundef)")
		e.usedMmapDecl = true
	}
}

// ensureWaitpidDecl declares waitpid(2) exactly once — shared by execFileSync
// (runtime_process.go), child_process (runtime_childprocess.go), and cluster
// (runtime_cluster.go), any two of which can co-occur in one program.
func (e *Emitter) ensureWaitpidDecl() {
	if !e.usedWaitpidDecl {
		e.emitGlobal("declare i32 @waitpid(i32 noundef, ptr noundef, i32 noundef)")
		e.usedWaitpidDecl = true
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

func (e *Emitter) ensureGetrusage() {
	if !e.usedGetrusage {
		e.emitGlobal("declare i32 @getrusage(i32 noundef, ptr noundef)")
		e.usedGetrusage = true
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

// ensureJsPow defines @__kml_js_pow: libm pow with the one place JS's
// exponentiation deviates from IEEE-754 pow — a base of magnitude exactly 1
// with an infinite exponent is NaN in JS (pow returns 1).
func (e *Emitter) ensureJsPow() {
	if e.usedJsPow {
		return
	}
	e.usedJsPow = true
	e.ensureMathFuncs()
	e.emitGlobal(`
define double @__kml_js_pow(double %b, double %x) {
entry:
  %r = call double @pow(double %b, double %x)
  %ab = call double @fabs(double %b)
  %is1 = fcmp oeq double %ab, 1.0
  %ax = call double @fabs(double %x)
  %isinf = fcmp oeq double %ax, 0x7FF0000000000000
  %nanify = and i1 %is1, %isinf
  %res = select i1 %nanify, double 0x7FF8000000000000, double %r
  ret double %res
}`)
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
	e.emitGlobal(`
define i64 @__kml_ipow(i64 %base, i64 %exp) {
entry:
  %neg = icmp slt i64 %exp, 0
  br i1 %neg, label %retzero, label %header
retzero:
  ret i64 0
header:
  %result = phi i64 [ 1, %entry ], [ %result_next, %body ]
  %b = phi i64 [ %base, %entry ], [ %b_sq, %body ]
  %e = phi i64 [ %exp, %entry ], [ %e_half, %body ]
  %more = icmp ne i64 %e, 0
  br i1 %more, label %body, label %exit
body:
  %bit = and i64 %e, 1
  %is_odd = icmp ne i64 %bit, 0
  %rmul = mul i64 %result, %b
  %result_next = select i1 %is_odd, i64 %rmul, i64 %result
  %b_sq = mul i64 %b, %b
  %e_half = lshr i64 %e, 1
  br label %header
exit:
  ret i64 %result
}`)
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
	// Constants: B1/B2 magic biases; P0..P4 the degree-4 1/cbrt approximation.
	// Doubles are given as LLVM's exact-bit hex form (the fdlibm hex comments).
	e.emitGlobal(`define double @__kml_cbrt(double %x) {
entry:
  %xi = bitcast double %x to i64
  %hxsh = lshr i64 %xi, 32
  %hxand = and i64 %hxsh, 2147483647
  %hx = trunc i64 %hxand to i32
  ; cbrt(NaN,Inf) is itself: hx >= 0x7ff00000
  %isnaninf = icmp uge i32 %hx, 2146435072
  br i1 %isnaninf, label %naninf, label %chksub
naninf:
  %sum = fadd double %x, %x
  ret double %sum
chksub:
  ; zero or subnormal: hx < 0x00100000
  %issub = icmp ult i32 %hx, 1048576
  br i1 %issub, label %subn, label %normal
subn:
  ; scale by 2^54, re-extract hx; hx==0 means x is (signed) zero -> return x
  %xs = fmul double %x, 0x4350000000000000
  %xsi = bitcast double %xs to i64
  %hxssh = lshr i64 %xsi, 32
  %hxsand = and i64 %hxssh, 2147483647
  %hxs = trunc i64 %hxsand to i32
  %iszero = icmp eq i32 %hxs, 0
  br i1 %iszero, label %retx, label %subcont
retx:
  ret double %x
subcont:
  %hxsdiv = udiv i32 %hxs, 3
  %hxsb = add i32 %hxsdiv, 696219795
  br label %recon
normal:
  %hxdiv = udiv i32 %hx, 3
  %hxb = add i32 %hxdiv, 715094163
  br label %recon
recon:
  %newhx = phi i32 [ %hxsb, %subcont ], [ %hxb, %normal ]
  %sign = and i64 %xi, -9223372036854775808
  %newhx64 = zext i32 %newhx to i64
  %newhxsh = shl i64 %newhx64, 32
  %t0i = or i64 %sign, %newhxsh
  %t0 = bitcast i64 %t0i to double
  ; r = (t*t)*(t/x)
  %tt = fmul double %t0, %t0
  %tx = fdiv double %t0, %x
  %r = fmul double %tt, %tx
  ; t = t*((P0 + r*(P1 + r*P2)) + ((r*r)*r)*(P3 + r*P4))
  %rP2 = fmul double %r, 0x3FF9F1604A49D6C2
  %P1p = fadd double 0xBFFE28E092F02420, %rP2
  %rP1p = fmul double %r, %P1p
  %poly1 = fadd double 0x3FFE03E60F61E692, %rP1p
  %rr = fmul double %r, %r
  %rrr = fmul double %rr, %r
  %rP4 = fmul double %r, 0x3FC2B000D4E4EDD7
  %P3p = fadd double 0xBFE844CBBEE751D9, %rP4
  %term2 = fmul double %rrr, %P3p
  %polysum = fadd double %poly1, %term2
  %t1 = fmul double %t0, %polysum
  ; round t to 23 bits: u.i = (u.i + 0x80000000) & 0xffffffffc0000000
  %t1i = bitcast double %t1 to i64
  %t1add = add i64 %t1i, 2147483648
  %t1msk = and i64 %t1add, -1073741824
  %t2 = bitcast i64 %t1msk to double
  ; one Newton step to 53 bits: s=t*t; r=x/s; w=t+t; r=(r-t)/(w+r); t=t+t*r
  %s = fmul double %t2, %t2
  %rn = fdiv double %x, %s
  %w = fadd double %t2, %t2
  %rmt = fsub double %rn, %t2
  %wpr = fadd double %w, %rn
  %rfin = fdiv double %rmt, %wpr
  %ttr = fmul double %t2, %rfin
  %res = fadd double %t2, %ttr
  ret double %res
}`)
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
	e.emitGlobal("@__klain_rand_seeded = private thread_local global i1 false, align 1")

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
	e.emitGlobal(`
define i32 @__kml_parseint_base(ptr %s) {
entry:
  br label %wsloop
wsloop:
  %p = phi ptr [ %s, %entry ], [ %pnext, %wsadv ]
  %c = load i8, ptr %p, align 1
  %c_sp = icmp eq i8 %c, 32
  %c_ge9 = icmp uge i8 %c, 9
  %c_le13 = icmp ule i8 %c, 13
  %c_ctl = and i1 %c_ge9, %c_le13
  %c_ws = or i1 %c_sp, %c_ctl
  br i1 %c_ws, label %wsadv, label %afterws
wsadv:
  %pnext = getelementptr i8, ptr %p, i64 1
  br label %wsloop
afterws:
  %is_plus = icmp eq i8 %c, 43
  %is_minus = icmp eq i8 %c, 45
  %issign = or i1 %is_plus, %is_minus
  %psign = getelementptr i8, ptr %p, i64 1
  %p0 = select i1 %issign, ptr %psign, ptr %p
  %c0 = load i8, ptr %p0, align 1
  %is0 = icmp eq i8 %c0, 48
  br i1 %is0, label %checkx, label %ret10
checkx:
  %p1 = getelementptr i8, ptr %p0, i64 1
  %c1 = load i8, ptr %p1, align 1
  %is_x = icmp eq i8 %c1, 120
  %is_X = icmp eq i8 %c1, 88
  %ishex = or i1 %is_x, %is_X
  br i1 %ishex, label %ret16, label %ret10
ret16:
  ret i32 16
ret10:
  ret i32 10
}`)
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
	infPtr := e.internString("Infinity")
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_strtod_js(ptr %%s, ptr %%endpp) {
entry:
  %%v = call double @strtod(ptr %%s, ptr %%endpp)
  %%pinf = fcmp oeq double %%v, 0x7FF0000000000000
  %%ninf = fcmp oeq double %%v, 0xFFF0000000000000
  %%isinf = or i1 %%pinf, %%ninf
  br i1 %%isinf, label %%wsloop, label %%keep
wsloop:
  %%p = phi ptr [ %%s, %%entry ], [ %%pnext, %%wsadv ]
  %%c = load i8, ptr %%p, align 1
  %%c_sp = icmp eq i8 %%c, 32
  %%c_ge9 = icmp uge i8 %%c, 9
  %%c_le13 = icmp ule i8 %%c, 13
  %%c_ctl = and i1 %%c_ge9, %%c_le13
  %%c_ws = or i1 %%c_sp, %%c_ctl
  br i1 %%c_ws, label %%wsadv, label %%afterws
wsadv:
  %%pnext = getelementptr i8, ptr %%p, i64 1
  br label %%wsloop
afterws:
  %%is_plus = icmp eq i8 %%c, 43
  %%is_minus = icmp eq i8 %%c, 45
  %%issign = or i1 %%is_plus, %%is_minus
  %%psign = getelementptr i8, ptr %%p, i64 1
  %%psig = select i1 %%issign, ptr %%psign, ptr %%p
  %%csig = load i8, ptr %%psig, align 1
  %%upper = and i8 %%csig, 223
  %%ge_A = icmp uge i8 %%upper, 65
  %%le_Z = icmp ule i8 %%upper, 90
  %%isalpha = and i1 %%ge_A, %%le_Z
  br i1 %%isalpha, label %%validate, label %%keep
validate:
  %%cmp = call i32 @strncmp(ptr %%psig, ptr %s, i64 8)
  %%eq = icmp eq i32 %%cmp, 0
  br i1 %%eq, label %%keep, label %%invalid
invalid:
  store ptr %%s, ptr %%endpp, align 8
  br label %%keep
keep:
  ret double %%v
}`, infPtr))
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
	e.emitGlobal(`
define double @__kml_to_number(ptr %s) {
entry:
  %endp = alloca ptr, align 8
  %v = call double @__kml_strtod_js(ptr %s, ptr %endp)
  %end = load ptr, ptr %endp, align 8
  %noconv = icmp eq ptr %end, %s
  br i1 %noconv, label %scan_all, label %scan_tail
scan_tail:                       ; converted: tail must be whitespace-only
  br label %tail_loop
tail_loop:
  %tp = phi ptr [ %end, %scan_tail ], [ %tp_next, %tail_ws ]
  %tc = load i8, ptr %tp, align 1
  %t_nul = icmp eq i8 %tc, 0
  br i1 %t_nul, label %ret_val, label %tail_check
tail_check:
  %t_sp = icmp eq i8 %tc, 32
  %t_ge_tab = icmp uge i8 %tc, 9
  %t_le_cr = icmp ule i8 %tc, 13
  %t_ctlws = and i1 %t_ge_tab, %t_le_cr
  %t_ws = or i1 %t_sp, %t_ctlws
  br i1 %t_ws, label %tail_ws, label %ret_nan
tail_ws:
  %tp_next = getelementptr i8, ptr %tp, i64 1
  br label %tail_loop
scan_all:                        ; no conversion: whitespace-only → 0, else NaN
  br label %all_loop
all_loop:
  %ap = phi ptr [ %s, %scan_all ], [ %ap_next, %all_ws ]
  %ac = load i8, ptr %ap, align 1
  %a_nul = icmp eq i8 %ac, 0
  br i1 %a_nul, label %ret_zero, label %all_check
all_check:
  %a_sp = icmp eq i8 %ac, 32
  %a_ge_tab = icmp uge i8 %ac, 9
  %a_le_cr = icmp ule i8 %ac, 13
  %a_ctlws = and i1 %a_ge_tab, %a_le_cr
  %a_ws = or i1 %a_sp, %a_ctlws
  br i1 %a_ws, label %all_ws, label %ret_nan
all_ws:
  %ap_next = getelementptr i8, ptr %ap, i64 1
  br label %all_loop
ret_val:
  ret double %v
ret_zero:
  ret double 0.0
ret_nan:
  ret double 0x7FF8000000000000
}`)
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

// ensureExecvDecl / ensureExitRawDecl / ensureExecvpDecl: the exec-family +
// _exit POSIX decls, shared so runtimes that co-occur (cluster + fork IPC +
// child_process + process.execFileSync) never emit a duplicate declaration.
func (e *Emitter) ensureExecvDecl() {
	if !e.usedExecvDecl {
		e.emitGlobal("declare i32 @execv(ptr noundef, ptr noundef)")
		e.usedExecvDecl = true
	}
}

func (e *Emitter) ensureExecvpDecl() {
	if !e.usedExecvpDecl {
		e.emitGlobal("declare i32 @execvp(ptr noundef, ptr noundef)")
		e.usedExecvpDecl = true
	}
}

func (e *Emitter) ensureExitRawDecl() {
	if !e.usedExitRawDecl {
		e.emitGlobal("declare void @_exit(i32 noundef) noreturn")
		e.usedExitRawDecl = true
	}
}

// ensureChdirDecl declares POSIX chdir once — shared by process.chdir and
// child_process spawn's cwd option.
func (e *Emitter) ensureChdirDecl() {
	if !e.usedChdirDecl {
		e.emitGlobal("declare i32 @chdir(ptr noundef)")
		e.usedChdirDecl = true
	}
}
