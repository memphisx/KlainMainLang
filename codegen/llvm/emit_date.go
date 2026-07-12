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
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Date.%s takes exactly 1 argument (multi-argument overloads are not supported)", pos.Line, pos.Col, method)
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

	argVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	argVal = e.coerce(argVal, TypeI64)

	if method == "setTime" {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", argVal.Ref, sym.Ptr))
		return Value{Ref: argVal.Ref, Ty: TypeI64}, nil
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

	switch method {
	case "setFullYear":
		year = argVal.Ref
	case "setMonth":
		month0 = argVal.Ref
	case "setDate":
		day = argVal.Ref
	case "setHours":
		hour = argVal.Ref
	case "setMinutes":
		min = argVal.Ref
	case "setSeconds":
		sec = argVal.Ref
	case "setMilliseconds":
		millis = argVal.Ref
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
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", buf))
	fmtPtr := e.internString("%04lld-%02lld-%02lldT%02lld:%02lld:%02lld.%03lldZ")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s)",
		buf, fmtPtr, year, month, day, hour, minute, sec, millis))
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
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", buf))
	fmtPtr := e.internString("%s %s %02lld %04lld")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, ptr %s, i64 %s, i64 %s)",
		buf, fmtPtr, wdayName, monthName, day, year))
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
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", buf))
	fmtPtr := e.internString("%lld/%lld/%lld")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s)",
		buf, fmtPtr, month, day, year))
	return Value{Ref: buf, Ty: TypePtr}, nil
}
