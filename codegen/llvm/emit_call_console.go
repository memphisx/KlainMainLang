package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
	"strings"
)

// emitConsolePrint is the shared core for all console.* output methods.
// fd=1 writes to stdout via printf; fd=2 writes to stderr via dprintf.
// prefix, if non-empty, is printed before the first argument on the same line.
//
// Arguments are joined by a single space on one line, matching real
// console.log — so console.group()'s indent is applied once at the very
// start (before the prefix, or before the first argument if there's no
// prefix), and each argument's own print ends with a space except the last,
// which ends the line with "\n".
func (e *Emitter) emitConsolePrint(args []ast.Expression, fd int, prefix string) (Value, error) {
	// A spread argument (console.log(...arr), console.log(a, ...arr)) expands to
	// a runtime number of tokens, so it can't use the static, index-based
	// last-token detection below — it takes a dedicated runtime-separator path
	// (TDD-00106 V2).
	if anySpread(args) {
		return e.emitConsolePrintSpread(args, fd, prefix)
	}
	if fd == 2 {
		e.ensureDprintf()
	} else {
		e.ensurePrintf()
	}
	e.emitConsoleGroupIndent(fd)
	if prefix != "" {
		pfxPtr := e.internString(prefix)
		fmtStr := e.internString("%s")
		if fd == 2 {
			e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, pfxPtr))
		} else {
			e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", fmtStr, pfxPtr))
		}
	}
	if len(args) == 0 {
		// console.log() with no arguments prints a bare newline, as real JS.
		e.emitConsolePrintVal(Value{Ref: e.internString(""), Ty: TypePtr}, e.internString("%s\n"), fd)
		return Value{Ty: TypeVoid}, nil
	}
	for i, arg := range args {
		term := "\n"
		if i < len(args)-1 {
			term = " "
		}
		if err := e.emitConsolePrintArgToken(arg, fd, term); err != nil {
			return Value{}, err
		}
	}
	return Value{Ty: TypeVoid}, nil
}

// emitConsolePrintArgToken prints one console.* argument followed by term (a
// separator " " or the line-ending "\n"). Split out of emitConsolePrint's loop
// so the runtime-separator spread path (emitConsolePrintSpread) can reuse the
// exact same per-argument type dispatch.
func (e *Emitter) emitConsolePrintArgToken(arg ast.Expression, fd int, term string) error {
	// An un-narrowed nullable-scalar local prints its value or the literal
	// `null` (TDD-00064 Stage 2), rather than the payload 0 the bare
	// representation used to surface for a null. A narrowed local is known
	// present and falls through to the ordinary path below.
	if sym, ok := e.nullableScalarLValue(arg); ok && !sym.NarrowedNonNull {
		return e.emitConsoleNullableScalar(sym, fd, term)
	}
	val, err := e.emitExpr(arg)
	if err != nil {
		return err
	}
	return e.emitConsolePrintValueToken(val, fd, term)
}

// emitConsolePrintValueToken prints one already-evaluated value followed by
// term — the type-dispatch half shared by a direct argument
// (emitConsolePrintArgToken) and a spread array's elements
// (emitConsolePrintSpread).
func (e *Emitter) emitConsolePrintValueToken(val Value, fd int, term string) error {
	// A nullable-scalar aggregate value (a T|null return/field) prints
	// null-aware, same as a boxed local (TDD-00064 Stage 3).
	if isNullableScalar(val.Ty) {
		return e.emitConsoleNullableScalarAgg(val, fd, term)
	}
	// A tuple prints as its comma-joined elements (TDD-00066) — checked
	// before the array rejection, since a tuple is a fixed-shape value with
	// a well-defined rendering, unlike a general homogeneous array.
	if val.Ty.IsTuple {
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return err
		}
		e.emitConsolePrintVal(strVal, e.internString("%s"+term), fd)
		return nil
	}
	// console.log(array) → Node-style `[ 1, 2, 3 ]` (util.inspect). Previously
	// a hard rejection; now rendered via the inspector (TDD-00075/ADR-00218).
	if val.Ty.IsArray {
		strVal, err := e.emitInspectArray(val, 0)
		if err != nil {
			return err
		}
		e.emitConsolePrintVal(strVal, e.internString("%s"+term), fd)
		return nil
	}
	// A class instance / object literal prints Node-style: `Foo { x: 1 }`
	// (util.inspect), in both -compat modes. See TDD-00075/emit_inspect.go.
	if isInspectableObject(val.Ty) {
		strVal, err := e.emitInspectObject(val, 0)
		if err != nil {
			return err
		}
		e.emitConsolePrintVal(strVal, e.internString("%s"+term), fd)
		return nil
	}
	if val.Ty.IsDynamic {
		strVal, err := e.emitDynamicToString(val)
		if err != nil {
			return err
		}
		e.emitConsolePrintVal(strVal, e.internString("%s"+term), fd)
		return nil
	}
	if val.Ty.IsBigInt {
		// console.log(10n) shows the trailing `n` (String(10n) does not).
		strVal, err := e.emitBigIntToString(val, true)
		if err != nil {
			return err
		}
		e.emitConsolePrintVal(strVal, e.internString("%s"+term), fd)
		return nil
	}
	if val.Ty.IsSymbol {
		strVal, err := e.emitSymbolToString(val)
		if err != nil {
			return err
		}
		e.emitConsolePrintVal(strVal, e.internString("%s"+term), fd)
		return nil
	}
	// A boolean prints true/false, and a float prints via the JS-faithful
	// shortest-round-trip formatter (TDD-00080) — both live in
	// emitValueToString, so route through it rather than PrintfFmt's raw
	// i1/%g. (%g truncated to 6 significant digits.) See ADR-00183.
	if val.Ty.IR == "i1" || val.Ty.Float {
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return err
		}
		if val.Ty.Float {
			// console.log(-0) displays `-0` (Node's util.inspect), even
			// though String(-0) — what emitValueToString computes — is
			// "0". Detected by exact bit pattern (only -0.0 has just the
			// sign bit set), so no other value pays for the check.
			f64 := e.coerce(val, TypeF64)
			bits := e.freshReg()
			isNegZero := e.freshReg()
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", bits, f64.Ref))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, -9223372036854775808", isNegZero, bits))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isNegZero, e.internString("-0"), strVal.Ref))
			strVal = Value{Ref: sel, Ty: TypePtr}
		}
		e.emitConsolePrintVal(strVal, e.internString("%s"+term), fd)
		return nil
	}
	e.emitConsolePrintVal(val, e.internString(val.Ty.PrintfFmt()+term), fd)
	return nil
}

// emitConsolePrintSpread renders console.* output when the argument list holds
// one or more spread arguments (console.log(...arr), console.log(a, ...xs, b) —
// TDD-00106 V2). Because a spread expands to a runtime number of tokens, the
// last-token detection the static loop uses (index vs len) doesn't apply: each
// token is printed with no terminator, a single separator space is emitted
// *before* every token except the first (tracked by a runtime flag, so an empty
// spread contributes nothing), and one trailing newline closes the line — so
// the output is exactly `a b c\n` for any positional/spread mix.
func (e *Emitter) emitConsolePrintSpread(args []ast.Expression, fd int, prefix string) (Value, error) {
	if fd == 2 {
		e.ensureDprintf()
	} else {
		e.ensurePrintf()
	}
	e.emitConsoleGroupIndent(fd)
	if prefix != "" {
		pfxPtr := e.internString(prefix)
		fmtStr := e.internString("%s")
		if fd == 2 {
			e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, pfxPtr))
		} else {
			e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", fmtStr, pfxPtr))
		}
	}
	startedPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", startedPtr))
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", startedPtr))
	// emitSep prints one space before a token when one has already been printed.
	emitSep := func() {
		started := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", started, startedPtr))
		sepL := e.freshLabel("log.sep")
		contL := e.freshLabel("log.cont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", started, sepL, contL))
		e.emitLabel(sepL)
		e.emitConsolePrintVal(Value{Ref: e.internString(" "), Ty: TypePtr}, e.internString("%s"), fd)
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
		e.emitInstr(fmt.Sprintf("store i1 1, ptr %s, align 1", startedPtr))
	}
	for _, arg := range args {
		if sp, ok := arg.(*ast.SpreadElement); ok {
			ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(sp.Arg, sp.Arg.GetPos())
			if err != nil {
				return Value{}, err
			}
			if elemTy.IsArray || elemTy.IsObject {
				return Value{}, fmt.Errorf("%d:%d: console spread of an array of arrays or objects is not supported", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col)
			}
			idxAlloca := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
			condL := e.freshLabel("log.cond")
			bodyL := e.freshLabel("log.body")
			doneL := e.freshLabel("log.done")
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(condL)
			idxVal := e.freshReg()
			done := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
			e.emitLabel(bodyL)
			emitSep()
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
			elem := e.loadArrayElem(gep, elemTy)
			if err := e.emitConsolePrintValueToken(elem, fd, ""); err != nil {
				return Value{}, err
			}
			idxNext := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(doneL)
			continue
		}
		emitSep()
		if err := e.emitConsolePrintArgToken(arg, fd, ""); err != nil {
			return Value{}, err
		}
	}
	// Trailing newline — a bare newline even if nothing printed, matching
	// console.log() and console.log(...[]).
	e.emitConsolePrintVal(Value{Ref: e.internString(""), Ty: TypePtr}, e.internString("%s\n"), fd)
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleGroupIndent prints "  " (two spaces) once per current
// console.group() nesting level, with no trailing newline — called right
// before the start of every line this compiler's console.* output ever
// prints. Uses fd's own already-established printf-vs-dprintf convention
// (never dprintf on fd 1: mixing a raw fd write with stdio's own buffered
// printf on the same descriptor risks interleaving output out of order).
//
// Bails out immediately if the current block is already dead (past a
// terminator, e.g. unreachable code after return/process.exit/throw).
// emitLabel below unconditionally starts a fresh (reachable-looking) block
// regardless of blockDone, so without this guard a dead console.log call
// would "come back to life" partway through this helper — the depth value
// loaded before the loop's labels gets silently dropped (correctly, since
// it's genuinely dead), but the loop body after emitLabel would still
// reference it, and LLVM's verifier rejects that as a use of an undefined
// value. Every other value this function touches lives in an alloca
// (unconditionally emitted regardless of blockDone) except this one, so
// bailing out early here is the correct, minimal fix rather than reworking
// the loop to avoid the cross-block dependency.
func (e *Emitter) emitConsoleGroupIndent(fd int) {
	if e.blockDone {
		return
	}
	e.ensureConsoleGroupDepth()
	if fd == 2 {
		e.ensureDprintf()
	} else {
		e.ensurePrintf()
	}
	depthReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_console_group_depth, align 8", depthReg))

	counterPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", counterPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", counterPtr))

	loopL := e.freshLabel("group.indent.loop")
	bodyL := e.freshLabel("group.indent.body")
	doneL := e.freshLabel("group.indent.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(loopL)
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, counterPtr))
	cond := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cond, cur, depthReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, bodyL, doneL))

	e.emitLabel(bodyL)
	indentStr := e.internString("  ")
	fmtStr := e.internString("%s")
	if fd == 2 {
		e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, indentStr))
	} else {
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", fmtStr, indentStr))
	}
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, counterPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(doneL)
}

// emitConsoleGroup implements console.group(label?): prints label (if
// given) at the current indent depth, then increases the depth by one so
// every subsequent console.* call (until the matching groupEnd) is indented
// one level further.
func (e *Emitter) emitConsoleGroup(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: console.group takes 0 or 1 arguments (label?)", pos.Line, pos.Col)
	}
	if len(args) == 1 {
		if _, err := e.emitConsolePrint(args, 1, ""); err != nil {
			return Value{}, err
		}
	}
	e.ensureConsoleGroupDepth()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_console_group_depth, align 8", cur))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_console_group_depth, align 8", next))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleGroupEnd implements console.groupEnd(): decreases the indent
// depth by one, floored at 0 (an extra, unbalanced groupEnd() call is
// harmless rather than underflowing into a negative depth).
func (e *Emitter) emitConsoleGroupEnd(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: console.groupEnd takes no arguments", pos.Line, pos.Col)
	}
	e.ensureConsoleGroupDepth()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_console_group_depth, align 8", cur))
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %s, 0", isZero, cur))
	dec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", dec, cur))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", next, isZero, dec))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_console_group_depth, align 8", next))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleDir implements console.dir(obj, options?): prints obj exactly
// like a single-argument console.log. The `depth` option (real Node's nesting
// limit, `null` for unlimited) is honored by overriding the inspector's
// recursion cap for this one call; `colors` is accepted syntactically but
// ignored (this compiler emits no ANSI in inspected output).
func (e *Emitter) emitConsoleDir(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: console.dir takes 1 or 2 arguments (obj, options?)", pos.Line, pos.Col)
	}
	// Node's console.dir defaults to depth 2 (util.inspect's default), unlike a
	// bare console.log; honor that here, overridable via { depth }.
	e.inspectDepthCap = 2
	e.inspectDepthSet = true
	if len(args) == 2 {
		ol, ok := args[1].(*ast.ObjectLiteral)
		if !ok {
			e.inspectDepthSet = false
			return Value{}, fmt.Errorf("%d:%d: console.dir's options argument must be an object literal", pos.Line, pos.Col)
		}
		for _, prop := range ol.Properties {
			switch prop.Key {
			case "depth":
				// A literal number caps nesting; `null` means unlimited (a large
				// cap, since the walk is compile-time-bounded by the type anyway).
				if _, isNull := prop.Value.(*ast.NullLiteral); isNull {
					e.inspectDepthCap = 1 << 20
				} else if nl, ok := prop.Value.(*ast.NumberLiteral); ok && !nl.IsBigInt {
					n, convErr := strconv.Atoi(nl.Value)
					if convErr != nil || n < 0 {
						e.inspectDepthSet = false
						return Value{}, fmt.Errorf("%d:%d: console.dir's depth option must be a non-negative integer literal or null", pos.Line, pos.Col)
					}
					e.inspectDepthCap = n
				} else {
					e.inspectDepthSet = false
					return Value{}, fmt.Errorf("%d:%d: console.dir's depth option must be a literal number or null", pos.Line, pos.Col)
				}
			case "colors":
				// Accepted, ignored — inspected output carries no ANSI here.
			default:
				e.inspectDepthSet = false
				return Value{}, fmt.Errorf("%d:%d: console.dir options support only { depth, colors }", pos.Line, pos.Col)
			}
		}
	}
	res, err := e.emitConsolePrint(args[:1], 1, "")
	e.inspectDepthSet = false // restore the default for subsequent inspects
	return res, err
}

// consoleLabelArg resolves an optional single string-label argument (0 or 1
// args), defaulting to "default" when omitted — matching real Node's own
// default label for time/timeEnd/count/countReset.
func (e *Emitter) consoleLabelArg(args []ast.Expression, name string, pos ast.Pos) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("%d:%d: console.%s takes 0 or 1 arguments (label?)", pos.Line, pos.Col, name)
	}
	if len(args) == 0 {
		return e.internString("default"), nil
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return "", err
	}
	val = e.coerce(val, TypePtr)
	return val.Ref, nil
}

// emitConsoleTimeMapEnsure returns a register holding the lazily-created
// console.time() backing map, creating it on first use — same shape as
// emitConsoleCountMapEnsure below.
func (e *Emitter) emitConsoleTimeMapEnsure() string {
	e.ensureConsoleTimer()
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_console_time_map, align 8", cur))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cur, resPtr))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, cur))
	createL := e.freshLabel("consoletime.create")
	doneL := e.freshLabel("consoletime.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, createL, doneL))

	e.emitLabel(createL)
	newMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_console_time_map, align 8", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newMap, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return result
}

// emitConsoleTime implements console.time(label?): stores the current
// monotonic time under the given label (default "default") in the per-label
// backing map — see ensureConsoleTimer.
func (e *Emitter) emitConsoleTime(args []ast.Expression, pos ast.Pos) (Value, error) {
	labelPtr, err := e.consoleLabelArg(args, "time", pos)
	if err != nil {
		return Value{}, err
	}
	mapReg := e.emitConsoleTimeMapEnsure()
	nowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", nowReg))
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", bits, nowReg))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapReg, labelPtr, bits))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleTimeEnd implements console.timeEnd(label?): prints "<label>:
// <elapsed>ms" using the elapsed time since the matching console.time()
// call (no validation that time() was actually called first — the single-
// slot V1 scope means there's no separate label to check existence of).
func (e *Emitter) emitConsoleTimeEnd(args []ast.Expression, pos ast.Pos) (Value, error) {
	labelPtr, err := e.consoleLabelArg(args, "timeEnd", pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureSprintf()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensurePrintf()

	mapReg := e.emitConsoleTimeMapEnsure()
	nowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", nowReg))
	startBits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", startBits, mapReg, labelPtr))
	startReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", startReg, startBits))
	elapsed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", elapsed, nowReg, startReg))

	labelLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", labelLen, labelPtr))
	bufSize := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 48", bufSize, labelLen))
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, bufSize))
	msgFmt := e.internString("%s: %gms")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, double %s)", buf, msgFmt, labelPtr, elapsed))

	e.emitConsoleGroupIndent(1)
	nlFmt := e.internString("%s\n")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", nlFmt, buf))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleCountMapEnsure returns a register holding the lazily-created
// console.count() backing map, creating it on first use. Uses the
// alloca+store-in-each-branch+load-after-merge shape emitOptionalMember
// already established for "branch, then merge a value back" — simpler and
// safer than hand-tracking phi predecessor labels.
func (e *Emitter) emitConsoleCountMapEnsure() string {
	e.ensureConsoleCountMap()
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_console_count_map, align 8", cur))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cur, resPtr))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, cur))
	createL := e.freshLabel("consolecount.create")
	doneL := e.freshLabel("consolecount.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, createL, doneL))

	e.emitLabel(createL)
	newMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_console_count_map, align 8", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newMap, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return result
}

// emitConsoleCount implements console.count(label?): increments and prints
// a per-label counter (default label "default"), backed by a real
// Map<string, number> — matches real Node's multi-label semantics exactly,
// unlike console.time's single-slot V1 narrowing above.
func (e *Emitter) emitConsoleCount(args []ast.Expression, pos ast.Pos) (Value, error) {
	labelPtr, err := e.consoleLabelArg(args, "count", pos)
	if err != nil {
		return Value{}, err
	}
	mapReg := e.emitConsoleCountMapEnsure()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", cur, mapReg, labelPtr))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapReg, labelPtr, next))

	e.ensureSprintf()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensurePrintf()
	labelLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", labelLen, labelPtr))
	bufSize := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 32", bufSize, labelLen))
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, bufSize))
	msgFmt := e.internString("%s: %lld")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, i64 %s)", buf, msgFmt, labelPtr, next))

	e.emitConsoleGroupIndent(1)
	nlFmt := e.internString("%s\n")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", nlFmt, buf))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleCountReset implements console.countReset(label?): resets the
// given label's counter back to 0 (does not remove the label).
func (e *Emitter) emitConsoleCountReset(args []ast.Expression, pos ast.Pos) (Value, error) {
	labelPtr, err := e.consoleLabelArg(args, "countReset", pos)
	if err != nil {
		return Value{}, err
	}
	mapReg := e.emitConsoleCountMapEnsure()
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 0)", mapReg, labelPtr))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitConsolePrintVal(val Value, fmtPtr string, fd int) {
	call := func(extra ...string) {
		parts := append([]string{fmtPtr}, extra...)
		joined := strings.Join(parts, ", ")
		if fd == 2 {
			e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s)", joined))
		} else {
			e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s)", joined))
		}
	}
	switch val.Ty.IR {
	case "i8", "i16", "i32", "i64":
		pv := val
		if val.Ty.IR != "i64" {
			r := e.freshReg()
			ext := "sext"
			if !val.Ty.Signed {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to i64", r, ext, val.Ty.IR, val.Ref))
			pv = Value{Ref: r, Ty: TypeI64}
		}
		call("i64 " + pv.Ref)
	case "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i32", r, val.Ref))
		call("i32 " + r)
	case "float":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, val.Ref))
		call("double " + r)
	case "double":
		call("double " + val.Ref)
	case "ptr":
		// A ptr value being printed can genuinely be null at runtime even
		// when its static type doesn't say Nullable — e.g. Map<K,V>.get()
		// on a missing key (emit_collections.go) returns a bare null V,
		// with no type-level marker at all that it might be absent (real
		// JS would return `undefined` there; this compiler doesn't
		// distinguish that from `null` at the value level). Passing a NULL
		// pointer to printf's "%s" is undefined behavior — glibc happens to
		// print "(null)", but Darwin's libSystem printf has no such
		// special case and segfaults outright. Found via `new
		// URLSearchParams(...).get()` on a missing key crashing
		// console.log — confirmed to reproduce identically with a plain
		// `Map<string,string>.get()` miss, so this guard belongs here
		// (every ptr value console.log ever prints), not in the
		// URLSearchParams/Map-specific call sites.
		safe := e.freshReg()
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", safe, isNull, e.internString("null"), val.Ref))
		call("ptr " + safe)
	}
}

// emitConsoleAssert emits console.assert(condition, ...message).
// If condition is falsy, it prints "Assertion failed: <message>" to stderr and continues.
func (e *Emitter) emitConsoleAssert(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("%d:%d: console.assert requires at least one argument", pos.Line, pos.Col)
	}
	e.ensureDprintf()

	cond, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)

	failL := e.freshLabel("assert.fail")
	passL := e.freshLabel("assert.pass")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, passL, failL))

	e.emitLabel(failL)
	if len(args) > 1 {
		pfxPtr := e.internString("Assertion failed: ")
		fmtStr := e.internString("%s")
		e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, pfxPtr))
		msgArgs := args[1:]
		if _, err := e.emitConsolePrint(msgArgs, 2, ""); err != nil {
			return Value{}, err
		}
	} else {
		msgPtr := e.internString("Assertion failed\n")
		fmtStr := e.internString("%s")
		e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, msgPtr))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", passL))

	e.emitLabel(passL)
	return Value{Ty: TypeVoid}, nil
}
