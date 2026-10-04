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
	"strings"

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
	// The UTC getters are exact aliases here: this compiler's Date stores UTC
	// milliseconds and every field getter already reports UTC (the Date ADR's
	// deterministic-output divergence for the local `get*` forms). So `getUTC*`
	// is fully faithful to Node — same decomposed field as its `get*` sibling.
	"getUTCFullYear":     0,
	"getUTCMonth":        1,
	"getUTCDate":         2,
	"getUTCDay":          3,
	"getUTCHours":        4,
	"getUTCMinutes":      5,
	"getUTCSeconds":      6,
	"getUTCMilliseconds": 7,
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
	// UTC setters alias the field setters — the stored timestamp is UTC (as with
	// the getters above).
	"setUTCFullYear":     0,
	"setUTCMonth":        1,
	"setUTCDate":         2,
	"setUTCHours":        4,
	"setUTCMinutes":      5,
	"setUTCSeconds":      6,
	"setUTCMilliseconds": 7,
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
		return Value{Ref: e.emitTimeValueToDate(parsed.Ref), Ty: TypeDate}, nil
	}
	if val.Ty.Float && !val.Ty.IsDynamic {
		// A double time value: NaN or one past the range is Invalid Date.
		return Value{Ref: e.emitTimeValueToDate(e.coerce(val, TypeF64).Ref), Ty: TypeDate}, nil
	}
	if isUnconstrainedDynamic(val.Ty) {
		return e.emitNewDateFromAny(val)
	}
	return Value{Ref: e.coerce(val, TypeI64).Ref, Ty: TypeDate}, nil
}

// emitNewDateFromAny is `new Date(value)` with value known only at run time
// (ECMA-262 §21.4.2.1): a Date gives its time value, a string is parsed,
// anything else is ToNumber'd into one.
func (e *Emitter) emitNewDateFromAny(val Value) (Value, error) {
	e.ensureHostBoxHooks()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	tag, payload := e.emitUnboxTagPayload(val)
	strL, notStrL, dateL, numL, doneL := e.freshLabel("ndate.str"), e.freshLabel("ndate.nstr"), e.freshLabel("ndate.date"), e.freshLabel("ndate.num"), e.freshLabel("ndate.done")
	isStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStr, strL, notStrL))
	e.emitLabel(strL)
	parsed, err := e.emitDateParseValue(Value{Ref: e.emitIntToPtr(payload), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitTimeValueToDate(parsed.Ref), slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(notStrL)
	isDate := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_host_is(i64 %s, ptr %s)", isDate, val.Ref, e.internString("Date")))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDate, dateL, numL))
	e.emitLabel(dateL)
	d := e.coerce(val, TypeDate)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", d.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(numL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitTimeValueToDate(e.emitAnyToNum(val)), slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypeDate}, nil
}

// emitNewDateMulti implements the multi-argument calendar form
// new Date(year, month, day?, hours?, minutes?, seconds?, ms?). Real JS
// defaults an omitted day to 1 and every other omitted trailing field to 0;
// month is 0-indexed here (matching real JS/getMonth()), but
// __kml_date_compose expects a 1-indexed month (matching ISO date strings),
// so 1 is added before the call — the same adjustment emitDateToISOString
// already makes in the other direction. A year of 0–99 is 1900+year
// (MakeFullYear, ECMA-262 §21.4.2.1), as in real JS.
func (e *Emitter) emitNewDateMulti(args []ast.Expression) (Value, error) {
	v, err := e.emitDateFields(args)
	if err != nil {
		return Value{}, err
	}
	// The constructor reads its fields as local time.
	return Value{Ref: e.emitLocalToUTC(v.Ref), Ty: TypeDate}, nil
}

// emitDateFields composes Date's year, month, … arguments into a time value,
// reading them as UTC (Date.UTC; the constructor converts from local time).
func (e *Emitter) emitDateFields(args []ast.Expression) (Value, error) {
	defaults := [7]int64{0, 0, 1, 0, 0, 0, 0} // year, month, day, hour, min, sec, ms
	vals := make([]string, 7)
	for i := range vals {
		if i < len(args) {
			v, err := e.emitExpr(args[i])
			if err != nil {
				return Value{}, err
			}
			cv, err := e.coerceChecked(v, TypeI64, args[i].GetPos(), "Date component")
			if err != nil {
				return Value{}, err
			}
			vals[i] = cv.Ref
		} else {
			vals[i] = fmt.Sprintf("%d", defaults[i])
		}
	}
	inRange, lifted, year := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, 100", inRange, vals[0]))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1900", lifted, vals[0]))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", year, inRange, lifted, vals[0]))
	month := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", month, vals[1]))
	e.ensureDateCompose()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_compose(i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s)",
		r, year, month, vals[2], vals[3], vals[4], vals[5], vals[6]))
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
	// A `Date | undefined` slot (a `var` hoisted out of a loop) holds its
	// { present, time } pair; the receiver's absent case was already
	// guarded, so the time value is its payload.
	if isNullableScalar(dateVal.Ty) {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue %s %s, 1", p, nullableScalarStorageIR(dateVal.Ty), dateVal.Ref))
		dateVal = Value{Ref: p, Ty: dateVal.Ty.withoutNullable()}
	}
	switch method {
	case "getTime", "valueOf":
		// An Invalid Date's time value is NaN.
		f, r := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", f, dateVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0x7FF8000000000000, double %s", r, e.emitDateIsInvalid(dateVal.Ref), f))
		return Value{Ref: r, Ty: TypeF64}, nil
	case "toISOString":
		e.emitDateThrowIfInvalid(dateVal.Ref)
		return e.emitDateToISOString(dateVal)
	case "toJSON":
		// An Invalid Date's toJSON is null (its time value is not finite).
		iso, err := e.emitDateOrInvalid(dateVal, e.emitDateToISOString)
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", r, e.emitDateIsInvalid(dateVal.Ref), iso.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	case "toDateString":
		return e.emitDateOrInvalid(dateVal, e.emitDateToDateString)
	case "toTimeString":
		return e.emitDateOrInvalid(dateVal, e.emitDateToTimeString)
	case "toLocaleTimeString":
		return e.emitDateOrInvalid(dateVal, e.emitDateToLocaleTimeString)
	case "toLocaleString":
		return e.emitDateOrInvalid(dateVal, func(v Value) (Value, error) {
			d, err := e.emitDateToLocaleDateString(v)
			if err != nil {
				return Value{}, err
			}
			t, err := e.emitDateToLocaleTimeString(v)
			if err != nil {
				return Value{}, err
			}
			ds, err := e.emitStringConcat(d, Value{Ref: e.internString(", "), Ty: TypePtr})
			if err != nil {
				return Value{}, err
			}
			return e.emitStringConcat(ds, t)
		})
	case "toString":
		return e.emitDateOrInvalid(dateVal, e.emitDateToString)
	case "toUTCString", "toGMTString":
		return e.emitDateToUTCString(dateVal)
	case "toLocaleDateString":
		return e.emitDateToLocaleDateString(dateVal)
	case "getTimezoneOffset":
		// Minutes local time is behind UTC.
		off, q, neg := e.emitTZOffset(dateVal.Ref), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 60000", q, off))
		e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", neg, q))
		return Value{Ref: neg, Ty: TypeI64}, nil
	}
	if idx, ok := dateDecomposeFieldIndex[method]; ok {
		invalid := e.emitDateIsInvalid(dateVal.Ref)
		if !strings.HasPrefix(method, "getUTC") {
			dateVal = e.emitDateLocal(dateVal)
		}
		decomposed := e.emitDateDecompose(dateVal)
		result, f, r := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", result, decomposed, idx))
		// An Invalid Date's every field is NaN.
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", f, result))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0x7FF8000000000000, double %s", r, invalid, f))
		return Value{Ref: r, Ty: TypeF64}, nil
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
		// UTC setters mirror their local siblings' arities (all UTC here).
		"setUTCFullYear": 3, "setUTCMonth": 2, "setUTCDate": 1,
		"setUTCHours": 4, "setUTCMinutes": 3, "setUTCSeconds": 2,
		"setUTCMilliseconds": 1,
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

	// A `Date | undefined` slot (a `var` hoisted out of a loop) holds its
	// { present, time } pair: read and written through its payload.
	nullable := isNullableScalar(sym.Ty)
	store := func(t string) {
		if nullable {
			agg := e.makeNullableScalarAgg(sym.Ty, "true", t)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", nullableScalarStorageIR(sym.Ty), agg, sym.Ptr))
			return
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", t, sym.Ptr))
	}

	if method == "setTime" {
		// TimeClip(ToNumber(time)): NaN, an infinity or a value past the
		// range is Invalid Date; the result is the new time value.
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		n, err := e.coerceChecked(v, TypeF64, args[0].GetPos(), "Date setter argument")
		if err != nil {
			return Value{}, err
		}
		t := e.emitTimeValueToDate(n.Ref)
		store(t)
		return e.emitDateCall(Value{Ref: t, Ty: TypeDate}, "getTime", pos)
	}

	argVals := make([]string, len(args))
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		cv, err := e.coerceChecked(v, TypeI64, a.GetPos(), "Date setter argument")
		if err != nil {
			return Value{}, err
		}
		argVals[i] = cv.Ref
	}

	curReg := e.freshReg()
	if nullable {
		agg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", agg, nullableScalarStorageIR(sym.Ty), sym.Ptr))
		e.emitInstr(fmt.Sprintf("%s = extractvalue %s %s, 1", curReg, nullableScalarStorageIR(sym.Ty), agg))
	} else {
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curReg, sym.Ptr))
	}
	local := !strings.HasPrefix(method, "setUTC")
	wasInvalid := e.emitDateIsInvalid(curReg)
	cur := Value{Ref: curReg, Ty: TypeDate}
	if local {
		cur = e.emitDateLocal(cur)
	}
	fullYear := method == "setFullYear" || method == "setUTCFullYear"
	if fullYear {
		// setFullYear on an Invalid Date starts from +0 (not a local time).
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", z, wasInvalid, cur.Ref))
		cur = Value{Ref: z, Ty: TypeDate}
	}
	decomposed := e.emitDateDecompose(cur)
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
		"setUTCFullYear": 0, "setUTCMonth": 1, "setUTCDate": 2,
		"setUTCHours": 3, "setUTCMinutes": 4, "setUTCSeconds": 5, "setUTCMilliseconds": 6,
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
	if local {
		newMs = e.emitLocalToUTC(newMs)
	}

	if !fullYear {
		// Any other setter leaves an Invalid Date invalid.
		kept := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", kept, wasInvalid, dateInvalid, newMs))
		newMs = kept
	}
	store(newMs)
	// The result is the new time value: NaN for Invalid Date.
	return e.emitDateCall(Value{Ref: newMs, Ty: TypeDate}, "getTime", pos)
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
	// Date.parse takes ToString of its argument (a box, a number, an object).
	if strVal, err = e.coerceStringArg(strVal); err != nil {
		return Value{}, err
	}
	return e.emitDateParseValue(strVal)
}

// emitDateParseValue is emitDateParse's core, factored out so an
// already-evaluated string Value can be parsed directly — used by
// emitNewDate for the new Date(aStringLiteral) constructor form, which
// already has the argument evaluated and nothing left to re-evaluate.
func (e *Emitter) emitDateParseValue(strVal Value) (Value, error) {
	e.ensureDateLocal()
	if !e.declaredDateParseStr {
		e.declaredDateParseStr = true
		e.emitGlobal("declare double @__kml_date_parse_str(ptr)")
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_date_parse_str(ptr %s)", r, strVal.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
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

	// A year outside 0..9999 is the expanded form: a sign and six digits
	// (`-000001-01-01…`, `+275760-09-13…`), as in Node.
	sign, absYear := e.emitDateYearSign(year, true)
	isExt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ugt i64 %s, 9999", isExt, year)) // negative is huge unsigned
	width := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i32 6, i32 4", width, isExt))

	e.ensureSprintf()
	buf := e.emitStringScratch(32) // TDD-00120
	fmtPtr := e.internString("%s%0*lld-%02lld-%02lldT%02lld:%02lld:%02lld.%03lldZ")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, i32 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s, i64 %s)",
		buf, fmtPtr, sign, width, absYear, month, day, hour, minute, sec, millis))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitDateYearSign splits a decomposed year into its printed sign ("-" when
// negative; "+" above 9999 when plusAbove9999, the ISO expanded form; else
// "") and its absolute value.
func (e *Emitter) emitDateYearSign(year string, plusAbove9999 bool) (sign, abs string) {
	neg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, year))
	negY := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", negY, year))
	abs = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", abs, neg, negY, year))
	pos := e.internString("")
	if plusAbove9999 {
		big := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 9999", big, year))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", p, big, e.internString("+"), pos))
		pos = p
	}
	sign = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sign, neg, e.internString("-"), pos))
	return sign, abs
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
	decomposed := e.emitDateDecompose(e.emitDateLocal(dateVal))
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

	// A negative year keeps four digits after its sign (`-0001`), as in Node.
	sign, absYear := e.emitDateYearSign(year, false)
	e.ensureSprintf()
	buf := e.emitStringScratch(32) // TDD-00120
	fmtPtr := e.internString("%s %s %02lld %s%04lld")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, ptr %s, i64 %s, ptr %s, i64 %s)",
		buf, fmtPtr, wdayName, monthName, day, sign, absYear))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitDateToString is Date.prototype.toString: "Thu Jan 01 1970 00:00:00
// GMT+0000 (Coordinated Universal Time)" — in UTC, the zone every Date
// accessor here reads.
func (e *Emitter) emitDateToString(dateVal Value) (Value, error) {
	decomposed := e.emitDateDecompose(e.emitDateLocal(dateVal))
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	year, month0, day, wday := extract(0), extract(1), extract(2), extract(3)
	hour, min, sec := extract(4), extract(5), extract(6)
	e.ensureDateNameTables()
	wdayGep, wdayName := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [7 x ptr], ptr @__kml_weekday_names, i64 0, i64 %s", wdayGep, wday))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", wdayName, wdayGep))
	monthGep, monthName := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [12 x ptr], ptr @__kml_month_names, i64 0, i64 %s", monthGep, month0))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", monthName, monthGep))
	sign, absYear := e.emitDateYearSign(year, false)
	e.ensureSprintf()
	// The zone suffix (" GMT+0300 (Eastern European Summer Time)").
	e.ensureDateLocal()
	suffix := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [80 x i8], align 1", suffix))
	e.emitInstr(fmt.Sprintf("call void @__kml_tz_suffix(i64 %s, ptr %s)", dateVal.Ref, suffix))
	buf := e.emitStringScratch(160)
	fmtPtr := e.internString("%s %s %02lld %s%04lld %02lld:%02lld:%02lld%s")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, ptr %s, i64 %s, ptr %s, i64 %s, i64 %s, i64 %s, i64 %s, ptr %s)",
		buf, fmtPtr, wdayName, monthName, day, sign, absYear, hour, min, sec, suffix))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitDateToTimeString is Date.prototype.toTimeString: "00:00:00 GMT+0000
// (Coordinated Universal Time)", toString's time and zone.
func (e *Emitter) emitDateToTimeString(dateVal Value) (Value, error) {
	decomposed := e.emitDateDecompose(e.emitDateLocal(dateVal))
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	hour, min, sec := extract(4), extract(5), extract(6)
	e.ensureSprintf()
	e.ensureDateLocal()
	suffix := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [80 x i8], align 1", suffix))
	e.emitInstr(fmt.Sprintf("call void @__kml_tz_suffix(i64 %s, ptr %s)", dateVal.Ref, suffix))
	buf := e.emitStringScratch(112)
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s, ptr %s)",
		buf, e.internString("%02lld:%02lld:%02lld%s"), hour, min, sec, suffix))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitDateToLocaleTimeString formats en-US's "1:04:09 PM", the shape
// toLocaleDateString's default locale gives the time.
func (e *Emitter) emitDateToLocaleTimeString(dateVal Value) (Value, error) {
	decomposed := e.emitDateDecompose(e.emitDateLocal(dateVal))
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	hour, min, sec := extract(4), extract(5), extract(6)
	pm, h12, zero, h := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 12", pm, hour))
	e.emitInstr(fmt.Sprintf("%s = srem i64 %s, 12", h12, hour))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", zero, h12))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 12, i64 %s", h, zero, h12))
	ampm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", ampm, pm, e.internString("PM"), e.internString("AM")))
	e.ensureSprintf()
	buf := e.emitStringScratch(32)
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s, ptr %s)",
		buf, e.internString("%lld:%02lld:%02lld %s"), h, min, sec, ampm))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitDateToUTCString formats RFC 7231's IMF-fixdate, as Node does:
// "Thu, 01 Jan 1970 00:00:00 GMT".
func (e *Emitter) emitDateToUTCString(dateVal Value) (Value, error) {
	decomposed := e.emitDateDecompose(dateVal)
	extract := func(idx int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %s, %d", r, decomposed, idx))
		return r
	}
	year, month0, day, wday := extract(0), extract(1), extract(2), extract(3)
	hour, min, sec := extract(4), extract(5), extract(6)
	e.ensureDateNameTables()
	wdayGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [7 x ptr], ptr @__kml_weekday_names, i64 0, i64 %s", wdayGep, wday))
	wdayName := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", wdayName, wdayGep))
	monthGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [12 x ptr], ptr @__kml_month_names, i64 0, i64 %s", monthGep, month0))
	monthName := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", monthName, monthGep))
	sign, absYear := e.emitDateYearSign(year, false)
	e.ensureSprintf()
	buf := e.emitStringScratch(48)
	fmtPtr := e.internString("%s, %02lld %s %s%04lld %02lld:%02lld:%02lld GMT")
	e.emitInstr(fmt.Sprintf(
		"call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, i64 %s, ptr %s, ptr %s, i64 %s, i64 %s, i64 %s, i64 %s)",
		buf, fmtPtr, wdayName, day, monthName, sign, absYear, hour, min, sec))
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
	decomposed := e.emitDateDecompose(e.emitDateLocal(dateVal))
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

// ensureDateLocal declares the local-time helpers (stringsrc/string.c).
func (e *Emitter) ensureDateLocal() {
	if e.usedDateLocal {
		return
	}
	e.usedDateLocal = true
	e.ensureStringC()
	e.emitGlobal("declare i64 @__kml_tz_offset_ms(i64)")
	e.emitGlobal("declare i64 @__kml_local_to_utc_ms(i64)")
	e.emitGlobal("declare void @__kml_tz_suffix(i64, ptr)")
}

// emitTZOffset is the local zone's offset from UTC, in milliseconds, at the
// UTC time value t.
func (e *Emitter) emitTZOffset(t string) string {
	e.ensureDateLocal()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_tz_offset_ms(i64 %s)", r, t))
	return r
}

// emitDateLocal is a Date's local wall-clock time, as a time value whose UTC
// fields are the local ones.
func (e *Emitter) emitDateLocal(v Value) Value {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", r, v.Ref, e.emitTZOffset(v.Ref)))
	return Value{Ref: r, Ty: v.Ty}
}

// emitLocalToUTC is the UTC time value of a local wall-clock time value.
func (e *Emitter) emitLocalToUTC(local string) string {
	e.ensureDateLocal()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_local_to_utc_ms(i64 %s)", r, local))
	return r
}

// dateInvalid is an Invalid Date's stored time value: the i64 no valid time
// value (within ±8.64e15 ms) can be.
const dateInvalid = "-9223372036854775808"

// emitDateIsInvalid tests a Date's time value for Invalid Date.
func (e *Emitter) emitDateIsInvalid(t string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", r, t, dateInvalid))
	return r
}

// emitTimeValueToDate stores a double time value as a Date's: NaN (or any
// value past the time value range) is Invalid Date.
func (e *Emitter) emitTimeValueToDate(d string) string {
	ok, lo, hi, in, t, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp ord double %s, 0.0", ok, d))
	e.emitInstr(fmt.Sprintf("%s = fcmp oge double %s, -8.64e15", lo, d))
	e.emitInstr(fmt.Sprintf("%s = fcmp ole double %s, 8.64e15", hi, d))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", in, lo, hi))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double 0.0", t, in, d))
	ti := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", ti, t))
	both := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, ok, in))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, both, ti, dateInvalid))
	return r
}

// emitDateThrowIfInvalid throws toISOString's RangeError for Invalid Date.
func (e *Emitter) emitDateThrowIfInvalid(t string) {
	e.ensureRangeErrorThrow()
	badL, okL := e.freshLabel("date.invalid"), e.freshLabel("date.valid")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitDateIsInvalid(t), badL, okL))
	e.emitLabel(badL)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_range_error(ptr %s)", e.internString("Invalid time value")))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// emitDateOrInvalid renders a Date through render, or "Invalid Date".
func (e *Emitter) emitDateOrInvalid(v Value, render func(Value) (Value, error)) (Value, error) {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("Invalid Date"), slot))
	okL, doneL := e.freshLabel("date.str"), e.freshLabel("date.strdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitDateIsInvalid(v.Ref), doneL, okL))
	e.emitLabel(okL)
	s, err := render(v)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypePtr}, nil
}
