package llvm

import (
	"fmt"
	"runtime"
	"strings"
)

func (e *Emitter) ensureClockGettime() {
	if e.usedClockGettime {
		return
	}
	e.usedClockGettime = true
	e.emitGlobal("declare i32 @clock_gettime(i32 noundef, ptr noundef)")
}

// monotonicClockID returns the CLOCK_MONOTONIC numeric value for whatever
// OS is running this compiler right now (and will therefore also run clang
// moments later — this project doesn't cross-compile). Verified directly
// against the system header rather than trusted from memory: Darwin's is 6
// (confirmed in <_time.h>); glibc's is the well-known, decades-stable
// kernel UAPI value 1. The same class of platform check as errnoAccessor.
func monotonicClockID() string {
	if runtime.GOOS == "darwin" {
		return "6"
	}
	return "1"
}

// ensureDateNow declares __kml_date_now: the current time in milliseconds
// since the Unix epoch, via clock_gettime(CLOCK_REALTIME, ...). Uses
// clock_gettime/struct timespec rather than gettimeofday/struct timeval
// specifically because struct timespec's two fields (time_t tv_sec, long
// tv_nsec) are BOTH 64-bit on every LP64 target this compiler supports
// (macOS ARM64, Linux x86-64/aarch64) — struct timeval's tv_usec is a
// 32-bit suseconds_t on macOS/BSD but 64-bit on Linux, so hardcoding a
// {i64,i64} GEP layout for it would silently misread on macOS.
// CLOCK_REALTIME is defined as 0 on both platforms, so it's safe to inline.
func (e *Emitter) ensureDateNow() {
	if e.usedDateNow {
		return
	}
	e.usedDateNow = true
	e.ensureClockGettime()
	e.emitGlobal(`
define i64 @__kml_date_now() {
entry:
  %ts = alloca { i64, i64 }, align 8
  %r = call i32 @clock_gettime(i32 0, ptr %ts)
  %sec_p = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 0
  %nsec_p = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 1
  %sec = load i64, ptr %sec_p, align 8
  %nsec = load i64, ptr %nsec_p, align 8
  %sec_ms = mul i64 %sec, 1000
  %nsec_ms = sdiv i64 %nsec, 1000000
  %total = add i64 %sec_ms, %nsec_ms
  ret i64 %total
}`)
}

// ensurePerformanceNow declares __kml_performance_now: a CLOCK_MONOTONIC
// timestamp in milliseconds, as a double with sub-millisecond precision
// (real performance.now() is spec'd relative to a "time origin," typically
// process/page start — this compiler has no such fixed origin concept, so
// it returns the raw monotonic clock reading instead; fine for the common
// use of subtracting two calls to measure elapsed time, a documented
// simplification for anything expecting an absolute origin-relative value).
func (e *Emitter) ensurePerformanceNow() {
	if e.usedPerformanceNow {
		return
	}
	e.usedPerformanceNow = true
	e.ensureClockGettime()
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_performance_now() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  %%r = call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%nsec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%sec = load i64, ptr %%sec_p, align 8
  %%nsec = load i64, ptr %%nsec_p, align 8
  %%sec_f = sitofp i64 %%sec to double
  %%nsec_f = sitofp i64 %%nsec to double
  %%sec_ms = fmul double %%sec_f, 1000.0
  %%nsec_ms = fdiv double %%nsec_f, 1000000.0
  %%total = fadd double %%sec_ms, %%nsec_ms
  ret double %%total
}`, monotonicClockID()))
}

// ensurePerformanceMarkMap declares the hidden global backing
// performance.mark()/.measure() — a lazily-created Map<string, number>,
// reusing the exact same __kml_map_str_* helpers console.count() already
// uses (ensureMapStrHelpers), just never exposed as a KML-level value. The
// map's i64 value slot holds a double mark timestamp's raw bit pattern
// (bitcast, not a lossy numeric conversion — the same 64-bit width means no
// precision is lost). Marking the same name twice overwrites the previous
// timestamp (last-write-wins, V1 scope: this compiler tracks one timestamp
// per name, not real performance's full ordered entries-by-name list).
func (e *Emitter) ensurePerformanceMarkMap() {
	if e.usedPerformanceMarkMap {
		return
	}
	e.usedPerformanceMarkMap = true
	e.ensureMapStrHelpers()
	e.emitGlobal("@__kml_performance_mark_map = internal thread_local global ptr null, align 8")
}

// ensureDateDecompose declares __kml_date_decompose: converts a milliseconds-
// since-epoch i64 into its UTC calendar fields (year, month[0-11], day,
// weekday[0=Sun..6=Sat], hour, minute, second, millisecond) via gmtime(),
// returned as an { i64 x 8 } aggregate in that order. Deliberately UTC (not
// local time) so output is deterministic across machines/CI regardless of
// timezone — see docs/adr for the Date ADR. struct tm's first 7 fields
// (tm_sec, tm_min, tm_hour, tm_mday, tm_mon, tm_year, tm_wday) are `int`
// (i32) in that exact order on both glibc and Darwin/BSD, the standard
// POSIX layout — reading only those (not the platform-varying tail fields
// like tm_gmtoff) keeps this portable across this compiler's targets.
func (e *Emitter) ensureDateDecompose() {
	if e.usedDateDecompose {
		return
	}
	e.usedDateDecompose = true
	e.emitGlobal("declare ptr @gmtime(ptr noundef)")
	e.emitGlobal(`
define { i64, i64, i64, i64, i64, i64, i64, i64 } @__kml_date_decompose(i64 %ms) {
entry:
  %secs = sdiv i64 %ms, 1000
  %millis_raw = srem i64 %ms, 1000
  %millis_neg = icmp slt i64 %millis_raw, 0
  %millis_adj = add i64 %millis_raw, 1000
  %millis = select i1 %millis_neg, i64 %millis_adj, i64 %millis_raw
  %secs_adj = select i1 %millis_neg, i64 -1, i64 0
  %secs_final = add i64 %secs, %secs_adj
  %tbuf = alloca i64, align 8
  store i64 %secs_final, ptr %tbuf, align 8
  %tmptr = call ptr @gmtime(ptr %tbuf)
  %sec_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 0
  %min_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 1
  %hour_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 2
  %mday_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 3
  %mon_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 4
  %year_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 5
  %wday_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 6
  %sec_i32 = load i32, ptr %sec_p, align 4
  %min_i32 = load i32, ptr %min_p, align 4
  %hour_i32 = load i32, ptr %hour_p, align 4
  %mday_i32 = load i32, ptr %mday_p, align 4
  %mon_i32 = load i32, ptr %mon_p, align 4
  %year_i32 = load i32, ptr %year_p, align 4
  %wday_i32 = load i32, ptr %wday_p, align 4
  %sec64 = sext i32 %sec_i32 to i64
  %min64 = sext i32 %min_i32 to i64
  %hour64 = sext i32 %hour_i32 to i64
  %mday64 = sext i32 %mday_i32 to i64
  %mon64 = sext i32 %mon_i32 to i64
  %year64_raw = sext i32 %year_i32 to i64
  %year64 = add i64 %year64_raw, 1900
  %wday64 = sext i32 %wday_i32 to i64
  %r0 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } undef, i64 %year64, 0
  %r1 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r0, i64 %mon64, 1
  %r2 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r1, i64 %mday64, 2
  %r3 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r2, i64 %wday64, 3
  %r4 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r3, i64 %hour64, 4
  %r5 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r4, i64 %min64, 5
  %r6 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r5, i64 %sec64, 6
  %r7 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r6, i64 %millis, 7
  ret { i64, i64, i64, i64, i64, i64, i64, i64 } %r7
}`)
}

// ensureDateNameTables declares two global arrays of string pointers,
// indexed by the weekday[0-6]/month[0-11] fields __kml_date_decompose
// returns — a runtime lookup, since the index is only known at run time
// (not a Go-side compile-time switch), used by Date's toDateString.
func (e *Emitter) ensureDateNameTables() {
	if e.usedDateNameTables {
		return
	}
	e.usedDateNameTables = true
	wdayInit := make([]string, len(weekdayAbbrevs))
	for i, name := range weekdayAbbrevs {
		wdayInit[i] = "ptr " + e.internString(name)
	}
	monthInit := make([]string, len(monthAbbrevs))
	for i, name := range monthAbbrevs {
		monthInit[i] = "ptr " + e.internString(name)
	}
	e.emitGlobal(fmt.Sprintf("@__kml_weekday_names = private unnamed_addr constant [7 x ptr] [%s]", strings.Join(wdayInit, ", ")))
	e.emitGlobal(fmt.Sprintf("@__kml_month_names = private unnamed_addr constant [12 x ptr] [%s]", strings.Join(monthInit, ", ")))
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
func (e *Emitter) ensureDaysFromCivil() {
	if e.usedDaysFromCivil {
		return
	}
	e.usedDaysFromCivil = true
	e.emitGlobal(`
define i64 @__kml_days_from_civil(i64 %y0, i64 %m, i64 %d) {
entry:
  %mle2 = icmp sle i64 %m, 2
  %madj = select i1 %mle2, i64 1, i64 0
  %y = sub i64 %y0, %madj
  %yneg = icmp slt i64 %y, 0
  %yminus399 = sub i64 %y, 399
  %era_base = select i1 %yneg, i64 %yminus399, i64 %y
  %era = sdiv i64 %era_base, 400
  %era400 = mul i64 %era, 400
  %yoe = sub i64 %y, %era400
  %mgt2 = icmp sgt i64 %m, 2
  %madj2 = select i1 %mgt2, i64 -3, i64 9
  %mplus = add i64 %m, %madj2
  %mul153 = mul i64 153, %mplus
  %plus2 = add i64 %mul153, 2
  %div5 = sdiv i64 %plus2, 5
  %dm1 = sub i64 %d, 1
  %doy = add i64 %div5, %dm1
  %yoe365 = mul i64 %yoe, 365
  %yoediv4 = sdiv i64 %yoe, 4
  %yoediv100 = sdiv i64 %yoe, 100
  %t1 = add i64 %yoe365, %yoediv4
  %t2 = sub i64 %t1, %yoediv100
  %doe = add i64 %t2, %doy
  %era146097 = mul i64 %era, 146097
  %sum = add i64 %era146097, %doe
  %result = sub i64 %sum, 719468
  ret i64 %result
}`)
}

// ensureDateParse declares __kml_date_parse: parses an ISO 8601 UTC date
// string into milliseconds since epoch, trying (in order) the full
// "YYYY-MM-DDTHH:mm:ss.sssZ" shape (exactly what toISOString produces), the
// same shape without milliseconds, and a bare "YYYY-MM-DD" date (UTC
// midnight, matching real JS's date-only parsing rule). Anything else
// returns -1 — real JS's Date.parse returns NaN for unparseable input, but
// this compiler's Date is a plain i64 with no NaN representation, so -1 is
// used as the documented sentinel instead.
// ensureDateCompose declares __kml_date_compose: the inverse of
// __kml_date_decompose — takes calendar fields (year, month[1-12, note:
// 1-indexed here, unlike the 0-indexed month __kml_date_decompose returns],
// day, hour, min, sec, millis) and returns milliseconds since epoch. Shared
// by both Date.parse (ADR-00015) and the Date setters (setFullYear, etc.,
// ADR-00016) so the calendar-to-timestamp math exists in exactly one place.
func (e *Emitter) ensureDateCompose() {
	if e.usedDateCompose {
		return
	}
	e.usedDateCompose = true
	e.ensureDaysFromCivil()
	e.emitGlobal(`
define i64 @__kml_date_compose(i64 %year, i64 %month, i64 %day, i64 %hour, i64 %min, i64 %sec, i64 %msec) {
entry:
  %days = call i64 @__kml_days_from_civil(i64 %year, i64 %month, i64 %day)
  %daysecs = mul i64 %days, 86400
  %hoursecs = mul i64 %hour, 3600
  %minsecs = mul i64 %min, 60
  %t1 = add i64 %daysecs, %hoursecs
  %t2 = add i64 %t1, %minsecs
  %totalsecs = add i64 %t2, %sec
  %totalms1 = mul i64 %totalsecs, 1000
  %totalms = add i64 %totalms1, %msec
  ret i64 %totalms
}`)
}

// ensureDateParse declares __kml_date_parse. Tries, in order (most specific
// first): full ISO with milliseconds and a "+HH:MM"/"-HH:MM" offset; the
// same without milliseconds; the plain "...Z" (UTC) forms with and without
// milliseconds (ADR-00015); and a bare "YYYY-MM-DD" date. The offset
// patterns MUST be tried before the "Z" patterns: sscanf's return value only
// counts successfully assigned %-conversions, not whether trailing literal
// characters (like "Z") matched, so an offset string like
// "...20.000+02:00" fed to the "Z" pattern would still report all 7 numeric
// fields as matched even though the literal "Z" never matched the "+" — a
// real bug found while implementing this (confirmed: the pre-offset-support
// parser silently returned the wrong value for such input, treating the
// local time as if it were already UTC). Trying the higher-specificity
// (higher expected field count) offset patterns first, and requiring an
// exact field-count match, avoids that ambiguity entirely — a genuine "Z"
// string can never satisfy an offset pattern's field count (matching stops
// at the literal '+'/'-', which isn't present), so it correctly falls
// through.
//
// The offset sign is baked into which of the four offset patterns matched
// (a literal '+' or '-' in the format string) rather than relying on
// sscanf's %d parsing a signed hour value — a "-00:30" offset (zero hour,
// negative sign) would otherwise silently lose its sign, since -0 and 0 are
// the same integer. Structural per-pattern sign tracking sidesteps that.
func (e *Emitter) ensureDateParse() {
	if e.usedDateParse {
		return
	}
	e.usedDateParse = true
	e.ensureSscanf()
	e.ensureDateCompose()
	fmtPlusMillis := e.internString("%d-%d-%dT%d:%d:%d.%d+%d:%d")
	fmtMinusMillis := e.internString("%d-%d-%dT%d:%d:%d.%d-%d:%d")
	fmtPlusNoMillis := e.internString("%d-%d-%dT%d:%d:%d+%d:%d")
	fmtMinusNoMillis := e.internString("%d-%d-%dT%d:%d:%d-%d:%d")
	fmtFull := e.internString("%d-%d-%dT%d:%d:%d.%dZ")
	fmtNoMillis := e.internString("%d-%d-%dT%d:%d:%dZ")
	fmtDateOnly := e.internString("%d-%d-%d")
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_date_parse(ptr %%str) {
entry:
  %%year_a = alloca i32, align 4
  %%month_a = alloca i32, align 4
  %%day_a = alloca i32, align 4
  %%hour_a = alloca i32, align 4
  %%min_a = alloca i32, align 4
  %%sec_a = alloca i32, align 4
  %%msec_a = alloca i32, align 4
  %%offh_a = alloca i32, align 4
  %%offm_a = alloca i32, align 4
  %%offset_ms_a = alloca i64, align 8
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  store i64 0, ptr %%offset_ms_a, align 8

  %%noff1 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%msec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok1 = icmp eq i32 %%noff1, 9
  br i1 %%offok1, label %%off_plus_ms, label %%try_off_minus_ms

off_plus_ms:
  %%offh_ld1 = load i32, ptr %%offh_a, align 4
  %%offm_ld1 = load i32, ptr %%offm_a, align 4
  %%offh64_1 = sext i32 %%offh_ld1 to i64
  %%offm64_1 = sext i32 %%offm_ld1 to i64
  %%offmin_1 = mul i64 %%offh64_1, 60
  %%offmintot_1 = add i64 %%offmin_1, %%offm64_1
  %%offsec_1 = mul i64 %%offmintot_1, 60
  %%offms_1 = mul i64 %%offsec_1, 1000
  store i64 %%offms_1, ptr %%offset_ms_a, align 8
  br label %%compute

try_off_minus_ms:
  %%noff2 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%msec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok2 = icmp eq i32 %%noff2, 9
  br i1 %%offok2, label %%off_minus_ms, label %%try_off_plus_s

off_minus_ms:
  %%offh_ld2 = load i32, ptr %%offh_a, align 4
  %%offm_ld2 = load i32, ptr %%offm_a, align 4
  %%offh64_2 = sext i32 %%offh_ld2 to i64
  %%offm64_2 = sext i32 %%offm_ld2 to i64
  %%offmin_2 = mul i64 %%offh64_2, 60
  %%offmintot_2 = add i64 %%offmin_2, %%offm64_2
  %%offsec_2 = mul i64 %%offmintot_2, 60
  %%offms_2 = mul i64 %%offsec_2, 1000
  %%negoffms_2 = sub i64 0, %%offms_2
  store i64 %%negoffms_2, ptr %%offset_ms_a, align 8
  br label %%compute

try_off_plus_s:
  store i32 0, ptr %%msec_a, align 4
  %%noff3 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok3 = icmp eq i32 %%noff3, 8
  br i1 %%offok3, label %%off_plus_s, label %%try_off_minus_s

off_plus_s:
  %%offh_ld3 = load i32, ptr %%offh_a, align 4
  %%offm_ld3 = load i32, ptr %%offm_a, align 4
  %%offh64_3 = sext i32 %%offh_ld3 to i64
  %%offm64_3 = sext i32 %%offm_ld3 to i64
  %%offmin_3 = mul i64 %%offh64_3, 60
  %%offmintot_3 = add i64 %%offmin_3, %%offm64_3
  %%offsec_3 = mul i64 %%offmintot_3, 60
  %%offms_3 = mul i64 %%offsec_3, 1000
  store i64 %%offms_3, ptr %%offset_ms_a, align 8
  br label %%compute

try_off_minus_s:
  store i32 0, ptr %%msec_a, align 4
  %%noff4 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok4 = icmp eq i32 %%noff4, 8
  br i1 %%offok4, label %%off_minus_s, label %%try_z_ms

off_minus_s:
  %%offh_ld4 = load i32, ptr %%offh_a, align 4
  %%offm_ld4 = load i32, ptr %%offm_a, align 4
  %%offh64_4 = sext i32 %%offh_ld4 to i64
  %%offm64_4 = sext i32 %%offm_ld4 to i64
  %%offmin_4 = mul i64 %%offh64_4, 60
  %%offmintot_4 = add i64 %%offmin_4, %%offm64_4
  %%offsec_4 = mul i64 %%offmintot_4, 60
  %%offms_4 = mul i64 %%offsec_4, 1000
  %%negoffms_4 = sub i64 0, %%offms_4
  store i64 %%negoffms_4, ptr %%offset_ms_a, align 8
  br label %%compute

try_z_ms:
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  %%n1 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%msec_a)
  %%ok1 = icmp eq i32 %%n1, 7
  br i1 %%ok1, label %%compute, label %%try_z_s

try_z_s:
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  %%n2 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a)
  %%ok2 = icmp eq i32 %%n2, 6
  br i1 %%ok2, label %%compute, label %%try_date

try_date:
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  %%n3 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a)
  %%ok3 = icmp eq i32 %%n3, 3
  br i1 %%ok3, label %%compute, label %%invalid

invalid:
  ret i64 -1

compute:
  %%year32 = load i32, ptr %%year_a, align 4
  %%month32 = load i32, ptr %%month_a, align 4
  %%day32 = load i32, ptr %%day_a, align 4
  %%hour32 = load i32, ptr %%hour_a, align 4
  %%min32 = load i32, ptr %%min_a, align 4
  %%sec32 = load i32, ptr %%sec_a, align 4
  %%msec32 = load i32, ptr %%msec_a, align 4
  %%year = sext i32 %%year32 to i64
  %%month = sext i32 %%month32 to i64
  %%day = sext i32 %%day32 to i64
  %%hour = sext i32 %%hour32 to i64
  %%min = sext i32 %%min32 to i64
  %%sec = sext i32 %%sec32 to i64
  %%msec = sext i32 %%msec32 to i64
  %%localms = call i64 @__kml_date_compose(i64 %%year, i64 %%month, i64 %%day, i64 %%hour, i64 %%min, i64 %%sec, i64 %%msec)
  %%offset_ms = load i64, ptr %%offset_ms_a, align 8
  %%totalms = sub i64 %%localms, %%offset_ms
  ret i64 %%totalms
}`, fmtPlusMillis, fmtMinusMillis, fmtPlusNoMillis, fmtMinusNoMillis, fmtFull, fmtNoMillis, fmtDateOnly))
}
