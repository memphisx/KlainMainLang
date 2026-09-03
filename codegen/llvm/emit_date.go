// emit_date.go — Date: new Date()/new Date(ms), Date.now(), and instance
// methods (getFullYear, getMonth, getDate, getDay, getHours, getMinutes,
// getSeconds, getMilliseconds, getTime, valueOf, toISOString, toDateString,
// toLocaleDateString).
//
// Represented as a plain i64 (milliseconds since the Unix epoch), same
// storage as number — no heap allocation, unlike Map/Set/objects. All
// calendar-field getters report UTC, not local time, for deterministic
// output regardless of the machine/CI timezone (see the Date ADR).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// dateDecomposeFieldIndex maps a getter method name to its position in the
// { year, month, day, weekday, hour, min, sec, millis } aggregate that
// __kml_date_decompose returns.
var dateDecomposeFieldIndex = map[string]int{
	"getFullYear":     0,
	"getMonth":        1,
	"getDate":         2,
	"getDay":          3,
	"getHours":        4,
	"getMinutes":      5,
	"getSeconds":      6,
	"getMilliseconds": 7,
}

// isDateMethodName reports whether name is one of Date's instance methods —
// used as a cheap pre-check (alongside the side-effect-free inferExprType
// guard at the call site) before committing to evaluate the receiver
// expression, so a same-named method on some other type is never mistakenly
// double-evaluated or misrouted.
func isDateMethodName(name string) bool {
	if _, ok := dateDecomposeFieldIndex[name]; ok {
		return true
	}
	switch name {
	case "getTime", "valueOf", "toISOString", "toDateString", "toLocaleDateString":
		return true
	}
	return false
}

// dateSetterFieldIndex maps a setter method name to the position it
// overrides in the same { year, month, day, weekday, hour, min, sec, millis }
// shape dateDecomposeFieldIndex uses (weekday, index 3, is never a setter
// target — it's derived from the other fields, not independently settable).
// setTime is handled separately since it replaces the whole timestamp
// directly rather than one decomposed field.
var dateSetterFieldIndex = map[string]int{
	"setFullYear":     0,
	"setMonth":        1,
	"setDate":         2,
	"setHours":        4,
	"setMinutes":      5,
	"setSeconds":      6,
	"setMilliseconds": 7,
}

// isDateSetterName reports whether name is one of Date's mutating setter
// methods.
func isDateSetterName(name string) bool {
	if _, ok := dateSetterFieldIndex[name]; ok {
		return true
	}
	return name == "setTime"
}

// emitNewDate implements `new Date()` (current time) and `new Date(ms)`
// (from an explicit milliseconds-since-epoch timestamp).
func (e *Emitter) emitNewDate(n *ast.NewDateExpression) (Value, error) {
	if n.Args != nil {
		return e.emitNewDateMulti(n.Args)
	}
	if n.Millis == nil {
		e.ensureDateNow()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_now()", r))
		return Value{Ref: r, Ty: TypeDate}, nil
	}
	val, err := e.emitExpr(n.Millis)
	if err != nil {
		return Value{}, err
	}
	if val.Ty.IR == "ptr" {
		// A string argument (e.g. new Date("2023-11-14T00:00:00.000Z")) needs
		// actual parsing, like real JS's constructor does for a string —
		// coerce() has no ptr->i64 conversion and previously returned the raw
		// string pointer unchanged, silently mistyped as a Date's i64, which
		// produced invalid IR (a global string reference used where an i64
		// was expected) and crashed at the clang stage instead of failing (or
		// working) cleanly.
		parsed, err := e.emitDateParseValue(val)
		if err != nil {
			return Value{}, err
		}
		return Value{Ref: parsed.Ref, Ty: TypeDate}, nil
	}
	return Value{Ref: e.coerce(val, TypeI64).Ref, Ty: TypeDate}, nil
}

// emitNewDateMulti implements the multi-argument calendar form
// new Date(year, month, day?, hours?, minutes?, seconds?, ms?). Real JS
// defaults an omitted day to 1 and every other omitted trailing field to 0;
// month is 0-indexed here (matching real JS/getMonth()), but
// __kml_date_compose expects a 1-indexed month (matching ISO date strings),
// so 1 is added before the call — the same adjustment emitDateToISOString
// already makes in the other direction. Deliberately does not replicate real
// JS's "two-digit year (0-99) means 1900+year" historical quirk — not called
// out anywhere this bug was tracked, and a surprising special case not worth
// adding speculatively.
func (e *Emitter) emitNewDateMulti(args []ast.Expression) (Value, error) {
	defaults := [7]int64{0, 0, 1, 0, 0, 0, 0} // year, month, day, hour, min, sec, ms
	vals := make([]string, 7)
	for i := range vals {
		if i < len(args) {
			v, err := e.emitExpr(args[i])
			if err != nil {
				return Value{}, err
			}
			vals[i] = e.coerce(v, TypeI64).Ref
		} else {
			vals[i] = fmt.Sprintf("%d", defaults[i])
		}
	}
	month := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", month, vals[1]))
	e.ensureDateCompose()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_compose(i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s)",
		r, vals[0], month, vals[2], vals[3], vals[4], vals[5], vals[6]))
	return Value{Ref: r, Ty: TypeDate}, nil
}

// emitDateNow implements the static Date.now().
func (e *Emitter) emitDateNow() (Value, error) {
	e.ensureDateNow()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_now()", r))
	return Value{Ref: r, Ty: TypeDate}, nil
}

// emitDateCall dispatches a Date instance method call. dateVal is any i64
// Value with Ty.IsDate — not restricted to a named variable, since Date
// needs no Symbol/alloca resolution (it's just a plain i64), unlike Map/Set.
func (e *Emitter) emitDateCall(dateVal Value, method string, pos ast.Pos) (Value, error) {
	switch method {
	case "getTime", "valueOf":
		return Value{Ref: dateVal.Ref, Ty: TypeI64}, nil
	case "toISOString":
		return e.emitDateToISOString(dateVal)
	case "toDateString":
		return e.emitDateToDateString(dateVal)
	case "toLocaleDateString":
		return e.emitDateToLocaleDateString(dateVal)
	}
	if idx, ok := dateDecomposeFieldIndex[method]; ok {
		decomposed := e.emitDateDecompose(dateVal)
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", result, decomposed, idx))
		return Value{Ref: result, Ty: TypeI64}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Date method '%s'", pos.Line, pos.Col, method)
}

// emitDateSetterCall implements a Date setter (setFullYear, setMonth,
// setDate, setHours, setMinutes, setSeconds, setMilliseconds, setTime).
// Unlike the read-only getters (emitDateCall, which operates on any i64
// Value regardless of where it came from), a setter must mutate the Date
// variable in place — real JS Dates are reference objects, but this
// compiler's Date is a plain i64 value with no heap identity, so "mutate in
// place" only makes sense for a named variable's own alloca. Mirrors
// emitPush's identical restriction for array push (emit_arrays.go) — the
// receiver must be a plain identifier bound to a Date-typed variable, or
// this fails with a clean compile-time error rather than silently mutating
// nothing (e.g. a Date read from an object field or returned from a call
// has nowhere to write back to). Scope: only the single-argument form of
// each setter (real JS also allows e.g. setFullYear(y, m, d) and
// setHours(h, m, s, ms) — not supported here). Returns the new timestamp,
// matching real JS's setter return value.
func (e *Emitter) emitDateSetterCall(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Multi-argument overloads (ADR-00488): each extra argument cascades
	// into the following field, per JS — setFullYear(y, m?, d?),
	// setMonth(m, d?), setHours(h, m?, s?, ms?), setMinutes(m, s?, ms?),
	// setSeconds(s, ms?). setDate/setMilliseconds/setTime stay one-arg.
	maxArgs := map[string]int{
		"setFullYear": 3, "setMonth": 2, "setDate": 1,
		"setHours": 4, "setMinutes": 3, "setSeconds": 2,
		"setMilliseconds": 1, "setTime": 1,
	}[method]
	if len(args) < 1 || len(args) > maxArgs {
		return Value{}, fmt.Errorf("%d:%d: Date.%s takes 1 to %d arguments", pos.Line, pos.Col, method, maxArgs)
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: Date setters require a named variable receiver, e.g. 'd.%s(...)', not a field access or expression", pos.Line, pos.Col, method)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsDate {
		return Value{}, fmt.Errorf("%d:%d: '%s' is not a Date", pos.Line, pos.Col, id.Name)
	}

	argVals := make([]string, len(args))
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		argVals[i] = e.coerce(v, TypeI64).Ref
	}

	if method == "setTime" {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", argVals[0], sym.Ptr))
		return Value{Ref: argVals[0], Ty: TypeI64}, nil
	}

	curReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curReg, sym.Ptr))
	decomposed := e.emitDateDecompose(Value{Ref: curReg, Ty: TypeDate})
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	year := extract(0)
	month0 := extract(1)
	day := extract(2)
	hour := extract(4)
	min := extract(5)
	sec := extract(6)
	millis := extract(7)

	// The settable-field chain in cascade order (weekday is derived, never
	// set); the setter picks its start position and extra args continue.
	slots := []*string{&year, &month0, &day, &hour, &min, &sec, &millis}
	start := map[string]int{
		"setFullYear": 0, "setMonth": 1, "setDate": 2,
		"setHours": 3, "setMinutes": 4, "setSeconds": 5, "setMilliseconds": 6,
	}[method]
	for i, av := range argVals {
		*slots[start+i] = av
	}

	month1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", month1, month0))

	e.ensureDateCompose()
	newMs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_compose(i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s)",
		newMs, year, month1, day, hour, min, sec, millis))

	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newMs, sym.Ptr))
	return Value{Ref: newMs, Ty: TypeI64}, nil
}

// emitDateParse implements the static Date.parse(dateString), returning a
// plain number (milliseconds since epoch), not a Date — matching real JS,
// where Date.parse's result is typically fed straight into `new Date(...)`.
// Scope: ISO 8601 UTC strings only (the exact shape toISOString produces,
// optionally without milliseconds, or a bare date). Unparseable input
// returns -1: real JS returns NaN, but this compiler's Date is a plain i64
// with no NaN representation, so -1 is the documented sentinel instead.
func (e *Emitter) emitDateParse(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Date.parse takes exactly 1 argument", pos.Line, pos.Col)
	}
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.emitDateParseValue(strVal)
}

// emitDateParseValue is emitDateParse's core, factored out so an
// already-evaluated string Value can be parsed directly — used by
// emitNewDate for the new Date(aStringLiteral) constructor form, which
// already has the argument evaluated and nothing left to re-evaluate.
func (e *Emitter) emitDateParseValue(strVal Value) (Value, error) {
	e.ensureDateParse()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_parse(ptr %s)", r, strVal.Ref))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// emitDateDecompose calls __kml_date_decompose and returns the raw aggregate
// register (year, month, day, weekday, hour, min, sec, millis).
func (e *Emitter) emitDateDecompose(dateVal Value) string {
	e.ensureDateDecompose()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i64, i64, i64, i64, i64, i64, i64, i64 } @__kml_date_decompose(i64 %s)", r, dateVal.Ref))
	return r
}

// emitDateToISOString formats "YYYY-MM-DDTHH:mm:ss.sssZ" (always UTC, hence
// the literal "Z" suffix). ISO months are 1-based, unlike getMonth()'s 0-based
// JS convention, so 1 is added to the decomposed month field here.
func (e *Emitter) emitDateToISOString(dateVal Value) (Value, error) {
	decomposed := e.emitDateDecompose(dateVal)
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	year := extract(0)
	month0 := extract(1)
	day := extract(2)
	hour := extract(4)
	minute := extract(5)
	sec := extract(6)
	millis := extract(7)

	month := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", month, month0))

	e.ensureSprintf()
	buf := e.emitStringScratch(32) // TDD-00120
	fmtPtr := e.internString("%04lld-%02lld-%02lldT%02lld:%02lld:%02lld.%03lldZ")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s)",
		buf, fmtPtr, year, month, day, hour, minute, sec, millis))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// weekdayAbbrevs / monthAbbrevs back a runtime lookup table (ensureDateNameTables,
// runtime.go) indexed by the weekday[0-6]/month[0-11] fields
// __kml_date_decompose returns, used by toDateString.
var weekdayAbbrevs = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
var monthAbbrevs = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// emitDateToDateString formats "Www Mon DD YYYY" (e.g. "Thu Jan 01 1970" —
// day zero-padded to 2 digits), matching real JS's toDateString shape — but
// always UTC, like every other Date method here, not local time.
func (e *Emitter) emitDateToDateString(dateVal Value) (Value, error) {
	decomposed := e.emitDateDecompose(dateVal)
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	year := extract(0)
	month0 := extract(1)
	day := extract(2)
	wday := extract(3)

	e.ensureDateNameTables()
	wdayGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [7 x ptr], ptr @__kml_weekday_names, i64 0, i64 %s", wdayGep, wday))
	wdayName := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", wdayName, wdayGep))

	monthGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [12 x ptr], ptr @__kml_month_names, i64 0, i64 %s", monthGep, month0))
	monthName := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", monthName, monthGep))

	e.ensureSprintf()
	buf := e.emitStringScratch(32) // TDD-00120
	fmtPtr := e.internString("%s %s %02lld %04lld")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, ptr %s, i64 %s, i64 %s)",
		buf, fmtPtr, wdayName, monthName, day, year))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitDateToLocaleDateString formats "M/D/YYYY" (e.g. "1/1/1970"), the
// default en-US-shaped format real JS's toLocaleDateString() produces
// without an explicit locale. Scoped to exactly this one fixed format — full
// Intl.DateTimeFormat-style locale support is out of scope (would require
// bundling locale/calendar data this compiler has no other use for); no
// locale argument is accepted. Deterministic and UTC, like every other Date
// method here, rather than depending on the host's locale/timezone.
func (e *Emitter) emitDateToLocaleDateString(dateVal Value) (Value, error) {
	decomposed := e.emitDateDecompose(dateVal)
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	year := extract(0)
	month0 := extract(1)
	day := extract(2)

	month := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", month, month0))

	e.ensureSprintf()
	buf := e.emitStringScratch(32) // TDD-00120
	fmtPtr := e.internString("%lld/%lld/%lld")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s)",
		buf, fmtPtr, month, day, year))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitPerformanceMarkMapEnsure returns a register holding the lazily-
// created performance.mark() backing map, creating it on first use — the
// same alloca+store-in-each-branch+load-after-merge shape
// emitConsoleCountMapEnsure already established.
func (e *Emitter) emitPerformanceMarkMapEnsure() string {
	e.ensurePerformanceMarkMap()
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_performance_mark_map, align 8", cur))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cur, resPtr))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, cur))
	createL := e.freshLabel("perfmark.create")
	doneL := e.freshLabel("perfmark.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, createL, doneL))

	e.emitLabel(createL)
	newMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_performance_mark_map, align 8", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newMap, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return result
}

// emitPerformanceMarkLookup loads the mark map, calls __kml_map_str_get,
// and bitcasts the returned i64 bit pattern back to a double timestamp.
func (e *Emitter) emitPerformanceMarkLookup(mapReg, namePtr string) string {
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", bits, mapReg, namePtr))
	ts := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", ts, bits))
	return ts
}

// emitPerformanceMark implements performance.mark(name): records the
// current performance.now() timestamp under name in a lazily-created
// Map<string, number> (see ensurePerformanceMarkMap). V1 scope: returns
// void — real performance.mark() returns a PerformanceMark object, not
// modeled here since there's no getEntriesByName/PerformanceObserver
// machinery for it to usefully belong to.
func (e *Emitter) emitPerformanceMark(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: performance.mark() takes exactly 1 argument (name), got %d", pos.Line, pos.Col, len(args))
	}
	nameVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	nameVal = e.coerce(nameVal, TypePtr)

	e.ensurePerformanceNow()
	mapReg := e.emitPerformanceMarkMapEnsure()
	nowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", nowReg))
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", bits, nowReg))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapReg, nameVal.Ref, bits))
	// TDD-00166: notify any PerformanceObserver watching 'mark' (duration 0).
	e.emitPerfDispatch(nameVal.Ref, "mark", nowReg, "0.0", perfMaskMark)
	return Value{Ty: TypeVoid}, nil
}

// emitPerformanceMeasure implements performance.measure(name, startMark,
// endMark?): returns the elapsed milliseconds (as a plain number, not a
// PerformanceMeasure object — same V1 narrowing as emitPerformanceMark
// above) between two previously-recorded marks. name itself is evaluated
// (matching real JS's own evaluation-order guarantee) but not stored
// anywhere, since there's no entries list for it to identify — a
// documented scope narrowing, not an oversight. endMark defaults to the
// current performance.now() reading when omitted, matching real
// performance.measure()'s own "no endMark means measure through now"
// default. Throws (via the existing exception machinery) if startMark or
// an explicit endMark was never marked — real performance.measure() throws
// a SyntaxError for exactly this case, so this isn't a new error shape,
// just reusing the generic internal-throw path rather than modeling a
// distinct SyntaxError-vs-other-kind subtype for it.
func (e *Emitter) emitPerformanceMeasure(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: performance.measure() takes 2 or 3 arguments (name, startMark, endMark?), got %d", pos.Line, pos.Col, len(args))
	}
	nameVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	nameStr := e.coerce(nameVal, TypePtr) // evaluated for ordering; also the entry name for observers (TDD-00166)

	startVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	startVal = e.coerce(startVal, TypePtr)

	e.ensurePerformanceNow()
	mapReg := e.emitPerformanceMarkMapEnsure()

	e.ensureMalloc()
	startHas := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", startHas, mapReg, startVal.Ref))
	e.emitMissingMarkGuard(startHas, startVal.Ref)
	startTs := e.emitPerformanceMarkLookup(mapReg, startVal.Ref)

	var endTs string
	if len(args) == 3 {
		endVal, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		endVal = e.coerce(endVal, TypePtr)
		endHas := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", endHas, mapReg, endVal.Ref))
		e.emitMissingMarkGuard(endHas, endVal.Ref)
		endTs = e.emitPerformanceMarkLookup(mapReg, endVal.Ref)
	} else {
		endReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", endReg))
		endTs = endReg
	}

	dur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", dur, endTs, startTs))
	// TDD-00166: notify any PerformanceObserver watching 'measure'.
	e.emitPerfDispatch(nameStr.Ref, "measure", startTs, dur, perfMaskMeasure)
	return Value{Ref: dur, Ty: TypeF64}, nil
}

// emitMissingMarkGuard throws "performance.measure: no mark named '<name>'"
// when has is false — the same sprintf-a-message-then-emitInternalThrow
// shape emitDivZeroGuard's own static-message throw builds on, just with a
// dynamic name interpolated in since the mark name is only known at
// runtime.
func (e *Emitter) emitMissingMarkGuard(has, namePtr string) {
	okL := e.freshLabel("perfmeasure.ok")
	missL := e.freshLabel("perfmeasure.missing")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, okL, missL))

	e.emitLabel(missL)
	e.ensureSprintf()
	e.ensureStrlen()
	nameLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", nameLen, namePtr))
	bufSize := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 48", bufSize, nameLen))
	buf := e.emitStringScratchReg(bufSize) // TDD-00120
	msgFmt := e.internString("performance.measure: no mark named '%s'")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s)", buf, msgFmt, namePtr))
	e.emitStringFinalizeLen(buf)
	e.emitInternalThrow(buf) // ends with `unreachable`, so missL needs no branch of its own

	e.emitLabel(okL)
}
