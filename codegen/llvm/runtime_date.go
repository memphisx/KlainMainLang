// runtime_date.go — Date and performance clocks: the wall/monotonic reads,
// the UTC calendar decompose/compose, the ISO 8601 parser and the name
// tables. They live in datesrc/date.c (TDD-00240).
package llvm

import _ "embed"

//go:embed datesrc/date.c
var dateSource string

// DateSource is the Date/performance runtime's C source.
func DateSource() string { return dateSource }

// UsesDate reports whether the program links the Date runtime.
func (e *Emitter) UsesDate() bool {
	return e.usedDateNow || e.usedPerformanceNow || e.usedDateDecompose || e.usedDateNameTables ||
		e.usedDaysFromCivil || e.usedDateCompose || e.usedDateParse
}

func (e *Emitter) ensureClockGettime() {
	if e.usedClockGettime {
		return
	}
	e.usedClockGettime = true
	e.emitGlobal("declare i32 @clock_gettime(i32 noundef, ptr noundef)")
}

// ensureDateNow declares __kml_date_now: ms since the Unix epoch.
func (e *Emitter) ensureDateNow() {
	if e.usedDateNow {
		return
	}
	e.usedDateNow = true
	e.emitGlobal("declare i64 @__kml_date_now()")
}

// ensurePerformanceNow declares __kml_performance_now: ms since the
// program's time origin, captured once at process start by date.c's
// __kml_perf_init constructor (ADR-00568).
func (e *Emitter) ensurePerformanceNow() {
	if e.usedPerformanceNow {
		return
	}
	e.usedPerformanceNow = true
	e.emitGlobal(`declare double @__kml_perf_raw_ms()
declare double @__kml_performance_now()`)
}

// ensureNativePerf declares lib/native.d.ts's perfNow and perfTimeOrigin.
func (e *Emitter) ensureNativePerf() {
	if e.fnDecls["__kml_native_perf_now"] {
		return
	}
	e.fnDecls["__kml_native_perf_now"] = true
	e.ensurePerformanceNow()
	e.emitGlobal(`declare double @__kml_native_perf_now()
declare double @__kml_native_perf_time_origin()`)
}

// ensureDateDecompose defines __kml_date_decompose: ms since epoch -> UTC
// calendar fields (year, month[0-11], day, weekday[0=Sun], hour, minute,
// second, millisecond) as an { i64 x 8 } aggregate. An aggregate return of
// that size does not match the C ABI, so this thin wrapper calls the C
// routine that fills a buffer.
func (e *Emitter) ensureDateDecompose() {
	if e.usedDateDecompose {
		return
	}
	e.usedDateDecompose = true
	e.emitGlobal(`declare void @__kml_date_decompose_out(i64, ptr)

define { i64, i64, i64, i64, i64, i64, i64, i64 } @__kml_date_decompose(i64 %ms) {
entry:
  %buf = alloca { i64, i64, i64, i64, i64, i64, i64, i64 }, align 8
  call void @__kml_date_decompose_out(i64 %ms, ptr %buf)
  %r = load { i64, i64, i64, i64, i64, i64, i64, i64 }, ptr %buf, align 8
  ret { i64, i64, i64, i64, i64, i64, i64, i64 } %r
}`)
}

// ensureDateNameTables declares the weekday/month name pointer arrays
// (Date's toDateString indexes them at run time).
func (e *Emitter) ensureDateNameTables() {
	if e.usedDateNameTables {
		return
	}
	e.usedDateNameTables = true
	e.emitGlobal("@__kml_weekday_names = external constant [7 x ptr]")
	e.emitGlobal("@__kml_month_names = external constant [12 x ptr]")
}

// ensureDateCompose declares __kml_date_compose: the inverse of decompose
// (month 1-indexed here), shared by Date.parse and the Date setters.
func (e *Emitter) ensureDateCompose() {
	if e.usedDateCompose {
		return
	}
	e.usedDateCompose = true
	e.emitGlobal("declare i64 @__kml_date_compose(i64, i64, i64, i64, i64, i64, i64)")
}
