package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emitConsolePrint is the shared core for all console.* output methods.
// fd=1 writes to stdout via printf; fd=2 writes to stderr via dprintf.
// prefix, if non-empty, is printed before the first argument on the same line.
//
// Each argument is printed on its own line (this compiler's own long-
// standing convention, not real console.log's single-space-joined-line
// behavior) — so console.group()'s indent is applied once at the very start
// (before prefix, or before the first argument if there's no prefix) and
// again before every argument after the first, since each of those starts a
// fresh line of its own.
func (e *Emitter) emitConsolePrint(args []ast.Expression, fd int, prefix string) (Value, error) {
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
	for i, arg := range args {
		if i > 0 {
			e.emitConsoleGroupIndent(fd)
		}
		// An un-narrowed nullable-scalar local prints its value or the literal
		// `null` (TDD-00064 Stage 2), rather than the payload 0 the bare
		// representation used to surface for a null. A narrowed local is known
		// present and falls through to the ordinary path below.
		if sym, ok := e.nullableScalarLValue(arg); ok && !sym.NarrowedNonNull {
			if err := e.emitConsoleNullableScalar(sym, fd); err != nil {
				return Value{}, err
			}
			continue
		}
		val, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		// A nullable-scalar aggregate value (a T|null return/field) prints
		// null-aware, same as a boxed local (TDD-00064 Stage 3).
		if isNullableScalar(val.Ty) {
			if err := e.emitConsoleNullableScalarAgg(val, fd); err != nil {
				return Value{}, err
			}
			continue
		}
		// A tuple prints as its comma-joined elements (TDD-00066) — checked
		// before the array rejection, since a tuple is a fixed-shape value with
		// a well-defined rendering, unlike a general homogeneous array.
		if val.Ty.IsTuple {
			strVal, err := e.emitValueToString(val)
			if err != nil {
				return Value{}, err
			}
			e.emitConsolePrintVal(strVal, e.internString("%s\n"), fd)
			continue
		}
		if val.Ty.IsArray {
			return Value{}, fmt.Errorf("%d:%d: console output does not support arrays; iterate and print each element", arg.GetPos().Line, arg.GetPos().Col)
		}
		if val.Ty.IsDynamic {
			strVal, err := e.emitDynamicToString(val)
			if err != nil {
				return Value{}, err
			}
			fmtPtr := e.internString("%s\n")
			e.emitConsolePrintVal(strVal, fmtPtr, fd)
			continue
		}
		if val.Ty.IsSymbol {
			strVal, err := e.emitSymbolToString(val)
			if err != nil {
				return Value{}, err
			}
			fmtPtr := e.internString("%s\n")
			e.emitConsolePrintVal(strVal, fmtPtr, fd)
			continue
		}
		// A boolean prints as `true`/`false`, matching real JS/TS — not the
		// raw i1's 0/1. emitValueToString already does exactly this conversion
		// (template-literal interpolation of a bool has always printed
		// true/false); console.log had simply never routed through it, using
		// the numeric PrintfFmt directly instead. See ADR-00183.
		if val.Ty.IR == "i1" {
			strVal, err := e.emitValueToString(val)
			if err != nil {
				return Value{}, err
			}
			fmtPtr := e.internString("%s\n")
			e.emitConsolePrintVal(strVal, fmtPtr, fd)
			continue
		}
		fmtPtr := e.internString(val.Ty.PrintfFmt() + "\n")
		e.emitConsolePrintVal(val, fmtPtr, fd)
	}
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
// like a single-argument console.log — options (real Node's depth/color
// controls) is accepted syntactically but ignored, a documented V1 scope
// narrowing.
func (e *Emitter) emitConsoleDir(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: console.dir takes 1 or 2 arguments (obj, options?)", pos.Line, pos.Col)
	}
	return e.emitConsolePrint(args[:1], 1, "")
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

// emitConsoleTime implements console.time(label?): stores the current
// monotonic time. V1 scope: a single global slot, not a per-label map — see
// ensureConsoleTimer.
func (e *Emitter) emitConsoleTime(args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, err := e.consoleLabelArg(args, "time", pos); err != nil {
		return Value{}, err
	}
	e.ensureConsoleTimer()
	nowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", nowReg))
	e.emitInstr(fmt.Sprintf("store double %s, ptr @__kml_console_time_start, align 8", nowReg))
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
	e.ensureConsoleTimer()
	e.ensureSprintf()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensurePrintf()

	nowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", nowReg))
	startReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load double, ptr @__kml_console_time_start, align 8", startReg))
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
