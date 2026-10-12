// emit_date.go — Date: new Date()/new Date(ms), Date.now(), and instance
// methods (getFullYear, getMonth, getDate, getDay, getHours, getMinutes,
// getSeconds, getMilliseconds, getTime, valueOf, toISOString, toDateString,
// toLocaleDateString).
//
// A Date is a heap object: a pointer to a one-word body holding its time
// value (milliseconds since the Unix epoch; dateInvalid for Invalid Date),
// with its host header word at body - 8 (TDD-00247). The formatting and
// calendar helpers below work on the time value itself (an i64).
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

// emitNewDate implements `new Date()` (current time), `new Date(value)` and
// the calendar form `new Date(year, month, …)`.
func (e *Emitter) emitNewDate(n *ast.NewDateExpression) (Value, error) {
	t, err := e.emitNewDateTime(n)
	if err != nil {
		return Value{}, err
	}
	return e.emitDateObject(t), nil
}

// emitHostDateBody sets the body of an instance of a class extending Date
// from its construction's arguments, as `new Date(…args)` would.
func (e *Emitter) emitHostDateBody(body string, args []ast.Expression, pos ast.Pos) error {
	var n *ast.NewDateExpression
	switch len(args) {
	case 0:
		n = ast.NewNewDateExpression(nil, pos)
	case 1:
		n = ast.NewNewDateExpression(args[0], pos)
	default:
		if len(args) > 7 {
			return fmt.Errorf("%d:%d: Date takes at most 7 arguments", pos.Line, pos.Col)
		}
		n = ast.NewNewDateExpressionMulti(args, pos)
	}
	t, err := e.emitNewDateTime(n)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", t, body))
	return nil
}

// emitNewDateTime is the time value `new Date(…)` starts with.
func (e *Emitter) emitNewDateTime(n *ast.NewDateExpression) (string, error) {
	if n.Args != nil {
		v, err := e.emitDateFields(n.Args)
		if err != nil {
			return "", err
		}
		// The constructor reads its fields as local time: an invalid one
		// stays Invalid Date.
		local := e.emitTimeValueToDate(v.Ref)
		utc := e.emitLocalToUTC(local)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, e.emitDateIsInvalid(local), dateInvalid, utc))
		return r, nil
	}
	if n.Millis == nil {
		e.ensureDateNow()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_now()", r))
		return r, nil
	}
	val, err := e.emitExpr(n.Millis)
	if err != nil {
		return "", err
	}
	if val.Ty.IR == "ptr" && !val.Ty.IsDate && !e.isDateClass(val.Ty) {
		// A string argument is parsed (an absent `string | undefined`,
		// undefined, is Invalid Date: its parse fails).
		parsed, err := e.emitDateParseValue(e.coerce(val, TypePtr))
		if err != nil {
			return "", err
		}
		return e.emitTimeValueToDate(parsed.Ref), nil
	}
	return e.emitDateTimeArg(val)
}

// emitDateTimeArg is the time value `new Date(value)` takes from a
// non-string argument: a Date's own time value, or the argument as a number
// (NaN, or one past the range, being Invalid Date).
func (e *Emitter) emitDateTimeArg(val Value) (string, error) {
	switch {
	case val.Ty.IsDate || e.isDateClass(val.Ty):
		return e.emitDateTimeValue(val), nil
	case isUnconstrainedDynamic(val.Ty):
		return e.emitDateTimeFromAny(val)
	case val.Ty.Float:
		return e.emitTimeValueToDate(e.coerce(val, TypeF64).Ref), nil
	}
	return e.coerce(val, TypeI64).Ref, nil
}

// emitDateTimeFromAny is the time value of `new Date(value)` with value
// known only at run time (ECMA-262 §21.4.2.1): a Date gives its time value,
// a string is parsed, anything else is ToNumber'd into one.
func (e *Emitter) emitDateTimeFromAny(val Value) (string, error) {
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
		return "", err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitTimeValueToDate(parsed.Ref), slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(notStrL)
	isDate := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_host_is(i64 %s, ptr %s)", isDate, val.Ref, e.internString("Date")))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDate, dateL, numL))
	e.emitLabel(dateL)
	d := e.emitDateTimeValue(e.coerce(val, TypeDate))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", d, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(numL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitTimeValueToDate(e.emitAnyToNum(val)), slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
	return r, nil
}

// emitDateFields composes Date's year, month, … arguments into a time value
// (a double, NaN for an invalid one), reading them as UTC (Date.UTC; the
// constructor converts from local time). An absent field takes its default;
// each present one is ToNumber'd.
func (e *Emitter) emitDateFields(args []ast.Expression) (Value, error) {
	// year (none: NaN), month, day, hour, min, sec, ms
	defaults := [7]string{"0x7FF8000000000000", "0.0", "1.0", "0.0", "0.0", "0.0", "0.0"}
	vals := make([]string, 7)
	for i := range vals {
		if i < len(args) {
			v, err := e.emitExpr(args[i])
			if err != nil {
				return Value{}, err
			}
			cv, err := e.coerceChecked(v, TypeF64, args[i].GetPos(), "Date component")
			if err != nil {
				return Value{}, err
			}
			vals[i] = cv.Ref
		} else {
			vals[i] = defaults[i]
		}
	}
	e.ensureDateFields()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_date_fields(double %s, double %s, double %s, double %s, double %s, double %s, double %s)",
		r, vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6]))
	return Value{Ref: r, Ty: TypeF64}, nil
}

// emitDateNow implements the static Date.now(): a number.
func (e *Emitter) emitDateNow() (Value, error) {
	e.ensureDateNow()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_date_now()", r))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// emitDateObject allocates a Date holding the time value t.
func (e *Emitter) emitDateObject(t string) Value {
	e.ensureMalloc()
	base, body := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", base))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", e.hostHeaderWord(TypeDate), base))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, base))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", t, body))
	return Value{Ref: body, Ty: TypeDate}
}

// isDateClass reports an instance of a class extending Date.
func (e *Emitter) isDateClass(t Type) bool {
	if !t.IsClass || t.IsDynamic {
		return false
	}
	info, ok := e.classes[t.ClassName]
	return ok && info.HostBase != nil && info.HostBase.IsDate
}

// emitDateBody is the body pointer of a Date-valued v: the Date itself, an
// instance of a class extending Date at +8, or a boxed Date unboxed.
func (e *Emitter) emitDateBody(v Value) string {
	switch {
	case e.isDateClass(v.Ty):
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, v.Ref))
		return body
	case v.Ty.IsDynamic:
		return e.coerce(v, TypeDate).Ref
	}
	return v.Ref
}

// emitDateTimeValue loads a Date's time value.
func (e *Emitter) emitDateTimeValue(v Value) string {
	body, r := e.emitDateBody(v), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, body))
	return r
}

// emitDateNumber is a Date's time value as a number (ToPrimitive with the
// number hint): NaN for Invalid Date.
func (e *Emitter) emitDateNumber(v Value) Value {
	if !v.Ty.Nullable && !v.Ty.IsUndefined {
		return e.emitDateTimeNumber(e.emitDateTimeValue(v))
	}
	// An absent `Date | undefined` is NaN (null would be 0, but a Date
	// slot's absent value is undefined).
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca double, align 8", slot))
	e.emitInstr(fmt.Sprintf("store double 0x7FF8000000000000, ptr %s, align 8", slot))
	okL, doneL := e.freshLabel("date.num"), e.freshLabel("date.numdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(v.Ref), doneL, okL))
	e.emitLabel(okL)
	n := e.emitDateTimeNumber(e.emitDateTimeValue(Value{Ref: v.Ref, Ty: v.Ty.withoutNullable()}))
	e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", n.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load double, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypeF64}
}

// emitDateTimeNumber is the time value t as a number: NaN for Invalid Date.
func (e *Emitter) emitDateTimeNumber(t string) Value {
	f, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", f, t))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0x7FF8000000000000, double %s", r, e.emitDateIsInvalid(t), f))
	return Value{Ref: r, Ty: TypeF64}
}

// emitDateCall dispatches a Date instance method call on the Date dateVal.
func (e *Emitter) emitDateCall(dateVal Value, method string, pos ast.Pos) (Value, error) {
	return e.emitDateTimeCall(Value{Ref: e.emitDateTimeValue(dateVal), Ty: TypeI64}, method, pos)
}

// emitDateTimeCall is a Date method that reads the time value dateVal (an
// i64) and returns a fresh value.
func (e *Emitter) emitDateTimeCall(dateVal Value, method string, pos ast.Pos) (Value, error) {
	switch method {
	case "getTime", "valueOf":
		// An Invalid Date's time value is NaN.
		return e.emitDateTimeNumber(dateVal.Ref), nil
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
// setDate, setHours, setMinutes, setSeconds, setMilliseconds, their UTC
// forms, setTime): it writes the new time value into the receiver's body,
// so any Date-valued receiver works, and returns the new time value.
func (e *Emitter) emitDateSetterCall(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Multi-argument overloads (ADR-00488): each extra argument cascades
	// into the following field, per JS — setFullYear(y, m?, d?),
	// setMonth(m, d?), setHours(h, m?, s?, ms?), setMinutes(m, s?, ms?),
	// setSeconds(s, ms?). setDate/setMilliseconds/setTime stay one-arg.
	maxArgs := map[string]int{
		"setFullYear": 3, "setMonth": 2, "setDate": 1,
		"setHours": 4, "setMinutes": 3, "setSeconds": 2,
		"setMilliseconds": 1, "setTime": 1,
		"setUTCFullYear": 3, "setUTCMonth": 2, "setUTCDate": 1,
		"setUTCHours": 4, "setUTCMinutes": 3, "setUTCSeconds": 2,
		"setUTCMilliseconds": 1,
	}[method]
	if len(args) < 1 || len(args) > maxArgs {
		return Value{}, fmt.Errorf("%d:%d: Date.%s takes 1 to %d arguments", pos.Line, pos.Col, method, maxArgs)
	}
	recv, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !recv.Ty.IsDate && !e.isDateClass(recv.Ty) && !recv.Ty.IsDynamic {
		return Value{}, fmt.Errorf("%d:%d: '%s' is not a Date", pos.Line, pos.Col, method)
	}
	body := e.emitDateBody(recv)
	store := func(t string) {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", t, body))
	}
	// The time value is read before the arguments are converted
	// (ECMA-262 §21.4.4.20 and its siblings).
	curReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curReg, body))

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
		return e.emitDateTimeNumber(t), nil
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

	local := !strings.HasPrefix(method, "setUTC")
	wasInvalid := e.emitDateIsInvalid(curReg)
	cur := Value{Ref: curReg, Ty: TypeI64}
	if local {
		cur = e.emitDateLocal(cur)
	}
	fullYear := method == "setFullYear" || method == "setUTCFullYear"
	if fullYear {
		// setFullYear on an Invalid Date starts from +0 (not a local time).
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", z, wasInvalid, cur.Ref))
		cur = Value{Ref: z, Ty: TypeI64}
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
	return e.emitDateTimeNumber(newMs), nil
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

// emitDateInspect is util.inspect's form of the Date v: its ISO string,
// Invalid Date as such, and `null`/`undefined` for an absent one.
func (e *Emitter) emitDateInspect(v Value) (Value, error) {
	render := func(v Value) (Value, error) {
		return e.emitDateOrInvalid(Value{Ref: e.emitDateTimeValue(v), Ty: TypeI64}, e.emitDateToISOString)
	}
	if !v.Ty.Nullable && !v.Ty.IsUndefined {
		return render(v)
	}
	absent := "null"
	if v.Ty.IsUndefined {
		absent = "undefined"
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(absent), slot))
	okL, doneL := e.freshLabel("date.insp"), e.freshLabel("date.inspdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(v.Ref), doneL, okL))
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

// emitDateBodyOrNull is a possibly absent Date's identity: its body
// pointer (an instance of a class extending Date compares by the body it
// is viewed as a Date through), null when absent.
func (e *Emitter) emitDateBodyOrNull(v Value) string {
	if e.isDateClass(v.Ty) {
		return e.emitDateBody(v)
	}
	return v.Ref
}

// emitDeepCloneDate is structuredClone of the Date v: a new Date with its
// time value (an absent one stays absent).
func (e *Emitter) emitDeepCloneDate(v Value) Value {
	if !v.Ty.Nullable && !v.Ty.IsUndefined {
		return e.emitDateObject(e.emitDateTimeValue(v))
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	okL, doneL := e.freshLabel("date.clone"), e.freshLabel("date.clonedone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(v.Ref), doneL, okL))
	e.emitLabel(okL)
	c := e.emitDateObject(e.emitDateTimeValue(Value{Ref: v.Ref, Ty: v.Ty.withoutNullable()}))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", c.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: v.Ty}
}

// emitDateUTCFromRest is Date.UTC over a run-time argument list (its value
// called): the fields it holds, each ToNumber'd, the rest their defaults.
func (e *Emitter) emitDateUTCFromRest(rest ast.Expression) (Value, error) {
	ptr, n, elemTy, err := e.resolveArrayForHOF(rest, rest.GetPos())
	if err != nil {
		return Value{}, err
	}
	defaults := [7]string{"0x7FF8000000000000", "0.0", "1.0", "0.0", "0.0", "0.0", "0.0"}
	vals := make([]string, 7)
	for i := range vals {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca double, align 8", slot))
		e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", defaults[i], slot))
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %d", present, n, i))
		has, done := e.freshLabel("utc.has"), e.freshLabel("utc.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, has, done))
		e.emitLabel(has)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gep, elemTy.IR, ptr, i))
		v := e.loadArrayElem(gep, elemTy)
		e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", e.coerce(v, TypeF64).Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", done))
		e.emitLabel(done)
		vals[i] = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load double, ptr %s, align 8", vals[i], slot))
	}
	e.ensureDateFields()
	t, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_date_fields(double %s, double %s, double %s, double %s, double %s, double %s, double %s)",
		t, vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6]))
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_date_timeclip(double %s)", r, t))
	return Value{Ref: r, Ty: TypeF64}, nil
}
