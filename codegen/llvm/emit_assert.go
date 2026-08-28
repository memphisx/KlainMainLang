// emit_assert.go — Node's `assert` core module: a lightweight namespace
// (like path/os/querystring — see emit_call.go's dispatcher) wrapping this
// compiler's existing throw and binary-comparison machinery rather than a
// new subsystem. Every check funnels into emitAssertCheck, which throws an
// AssertionError — an ordinary base-kind Error (errorObjType,
// emit_exceptions.go) whose name field is overridden to "AssertionError".
// This compiler's fixed errorKinds enum has no dedicated AssertionError
// kind, and adding one would ripple into every instanceof/kind-tag site
// for a single name string, so the base "Error" kind (0) is reused —
// `e instanceof Error` still holds for a caught AssertionError, matching
// real Node (AssertionError extends Error there too).
//
// Deliberately out of scope: assert.deepEqual/deepStrictEqual (this
// compiler has no generic recursive object-equality helper to build on —
// see docs/status/OBJECT-COLLECTIONS.md) and assert.throws matching against
// an expected error type/message (no matcher-object/RegExp-testing
// machinery exists here to drive it) — assert.throws only checks that
// *something* was thrown.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitAssertModuleCall dispatches assert.<method>(...).
func (e *Emitter) emitAssertModuleCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "ok":
		if len(args) < 1 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: assert.ok takes 1-2 arguments", pos.Line, pos.Col)
		}
		cond, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitAssertCheck(e.toBool(cond), args, 1, "the expression evaluated to a falsy value", pos)
	case "equal", "strictEqual":
		// This compiler's `==` is already strict (no implicit coercion
		// between differing types — see TYPE-SYSTEM.md), so equal and
		// strictEqual are deliberately aliases, unlike real Node.
		return e.emitAssertBinary(property, args, "==", "values are not equal", pos)
	case "notEqual", "notStrictEqual":
		return e.emitAssertBinary(property, args, "!=", "values are equal", pos)
	case "deepEqual", "deepStrictEqual":
		return e.emitAssertDeepEqual(args, false, "values are not deeply equal", pos)
	case "notDeepEqual", "notDeepStrictEqual":
		return e.emitAssertDeepEqual(args, true, "values are deeply equal", pos)
	case "fail":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: assert.fail takes 0-1 arguments", pos.Line, pos.Col)
		}
		e.ensureExceptionHelpers()
		msgPtr := e.internString("failed")
		if len(args) == 1 {
			var err error
			msgPtr, err = e.emitAssertMessage(args[0])
			if err != nil {
				return Value{}, err
			}
		}
		e.emitAssertThrowFail(msgPtr)
		return Value{Ty: TypeVoid}, nil
	case "throws":
		return e.emitAssertThrows(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: unsupported assert.%s", pos.Line, pos.Col, property)
}

// emitAssertDeepEqual implements assert.deepEqual/deepStrictEqual (and the
// negated forms). Since this compiler's `==` is already strict, the two names
// are aliases. Structural recursive equality over the shared static type of
// the two operands: scalars/strings/booleans compare directly; arrays compare
// length then element-wise; objects compare each visible field. Deliberately
// out of V1 scope (clean error): Map/Set/class instances, nullable-scalar
// members, and TypedArrays.
func (e *Emitter) emitAssertDeepEqual(args []ast.Expression, negate bool, defaultMsg string, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: assert.deepStrictEqual takes (actual, expected, message?)", pos.Line, pos.Col)
	}
	av, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	bv, err := e.emitExprWithObjectHint(args[1], av.Ty)
	if err != nil {
		return Value{}, err
	}
	if !coerciblePure(bv.Ty, av.Ty) && !coerciblePure(av.Ty, bv.Ty) {
		return Value{}, fmt.Errorf("%d:%d: assert.deepStrictEqual's two arguments have unrelated types", pos.Line, pos.Col)
	}
	eq, err := e.emitDeepEqual(av, bv, av.Ty, pos)
	if err != nil {
		return Value{}, err
	}
	if negate {
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", n, eq.Ref))
		eq = Value{Ref: n, Ty: TypeBool}
	}
	return e.emitAssertCheck(eq, args, 2, defaultMsg, pos)
}

// emitDeepEqual recursively compares two values of type ty, returning an i1.
func (e *Emitter) emitDeepEqual(a, b Value, ty Type, pos ast.Pos) (Value, error) {
	switch {
	case ty.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i1 %s, %s", r, a.Ref, b.Ref))
		return Value{Ref: r, Ty: TypeBool}, nil
	case isStringTy(ty):
		e.ensureStrcmp()
		cmp := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmp, a.Ref, b.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", r, cmp))
		return Value{Ref: r, Ty: TypeBool}, nil
	case ty.IR == "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", r, a.Ref, b.Ref))
		return Value{Ref: r, Ty: TypeBool}, nil
	case ty.IsArray:
		return e.emitDeepEqualArray(a, b, ty, pos)
	case ty.IsObject && !ty.IsMap && !ty.IsSet && !ty.IsClass && !ty.IsTypedArray && !ty.IsDate && !ty.IsError:
		return e.emitDeepEqualObject(a, b, ty, pos)
	case isNumberTy(ty) && ty.IR != "ptr":
		// Any remaining integer width (i8/i16/i32/i64).
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", r, ty.IR, a.Ref, b.Ref))
		return Value{Ref: r, Ty: TypeBool}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: assert.deepStrictEqual does not yet support values of this type (Map/Set/class/TypedArray/nullable are out of V1 scope)", pos.Line, pos.Col)
}

// emitDeepEqualObject ANDs the field-wise deep-equality of two struct values.
func (e *Emitter) emitDeepEqualObject(a, b Value, ty Type, pos ast.Pos) (Value, error) {
	structIR := ty.StructIR()
	acc := "" // empty until the first field; a fieldless object is deeply equal
	for _, f := range ty.VisibleFields() {
		idx, fieldTy, ok := ty.FieldIndex(f.Name)
		if !ok {
			continue
		}
		load := func(src string) string {
			g := e.freshReg()
			v := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, src, idx))
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", v, StructFieldIR(fieldTy), g, fieldTy.Align()))
			return v
		}
		feq, err := e.emitDeepEqual(Value{Ref: load(a.Ref), Ty: fieldTy}, Value{Ref: load(b.Ref), Ty: fieldTy}, fieldTy, pos)
		if err != nil {
			return Value{}, err
		}
		if acc == "" {
			acc = feq.Ref
		} else {
			n := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", n, acc, feq.Ref))
			acc = n
		}
	}
	if acc == "" {
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	return Value{Ref: acc, Ty: TypeBool}, nil
}

// emitDeepEqualArray compares two array aggregates: equal length, then a loop
// AND-ing the element-wise deep-equality (short-circuiting to false once any
// element differs).
func (e *Emitter) emitDeepEqualArray(a, b Value, ty Type, pos ast.Pos) (Value, error) {
	elemTy := TypeF64
	if ty.ElemType != nil {
		elemTy = *ty.ElemType
	}
	elemIR := StructFieldIR(elemTy)
	ap, al := e.freshReg(), e.freshReg()
	bp, bl := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ap, a.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", al, a.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", bp, b.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", bl, b.Ref))
	lenEq := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", lenEq, al, bl))
	res := e.freshReg()
	iptr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", res))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iptr))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", lenEq, res))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iptr))
	condL := e.freshLabel("deq.cond")
	bodyL := e.freshLabel("deq.body")
	doneL := e.freshLabel("deq.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", lenEq, condL, doneL))
	e.emitLabel(condL)
	iv := e.freshReg()
	rv := e.freshReg()
	inRange := e.freshReg()
	goReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", iv, iptr))
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", rv, res))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", inRange, iv, al))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", goReg, inRange, rv))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", goReg, bodyL, doneL))
	e.emitLabel(bodyL)
	agp, aelem := e.freshReg(), e.freshReg()
	bgp, belem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", agp, elemIR, ap, iv))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", aelem, elemIR, agp, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", bgp, elemIR, bp, iv))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", belem, elemIR, bgp, elemTy.Align()))
	eeq, err := e.emitDeepEqual(Value{Ref: aelem, Ty: elemTy}, Value{Ref: belem, Ty: elemTy}, elemTy, pos)
	if err != nil {
		return Value{}, err
	}
	rv2, newr, inext := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", rv2, res))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", newr, rv2, eeq.Ref))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", newr, res))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", inext, iv))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", inext, iptr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, res))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitAssertMessage evaluates a message argument and stringifies it via
// emitValueToString rather than coerce — a message isn't necessarily
// string-typed at the call site (real assert stringifies whatever it's
// given), and coerce's numeric/pointer reinterpretation would be unsafe
// applied to, say, a plain number passed as a message.
func (e *Emitter) emitAssertMessage(arg ast.Expression) (string, error) {
	msgVal, err := e.emitExpr(arg)
	if err != nil {
		return "", err
	}
	strVal, err := e.emitValueToString(msgVal)
	if err != nil {
		return "", err
	}
	return strVal.Ref, nil
}

// emitAssertThrowFail builds and throws an AssertionError carrying msgPtr,
// then emits `unreachable` — the shared tail for every failed check below.
func (e *Emitter) emitAssertThrowFail(msgPtr string) {
	errReg := e.buildErrorObj(0, msgPtr, e.internString("AssertionError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
}

// emitAssertCheck branches on cond (already-evaluated i1), throwing an
// AssertionError built from args[msgArgIndex] (if present) or defaultMsg
// otherwise. Mirrors emitConsoleAssert's (emit_call_console.go) branch
// shape, which predates this module and prints instead of throwing.
func (e *Emitter) emitAssertCheck(cond Value, args []ast.Expression, msgArgIndex int, defaultMsg string, pos ast.Pos) (Value, error) {
	e.ensureExceptionHelpers()

	failL := e.freshLabel("assert.fail")
	passL := e.freshLabel("assert.pass")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, passL, failL))

	e.emitLabel(failL)
	msgPtr := e.internString(defaultMsg)
	if len(args) > msgArgIndex {
		var err error
		msgPtr, err = e.emitAssertMessage(args[msgArgIndex])
		if err != nil {
			return Value{}, err
		}
	}
	e.emitAssertThrowFail(msgPtr)

	e.emitLabel(passL)
	return Value{Ty: TypeVoid}, nil
}

// emitAssertBinary implements assert.equal/strictEqual/notEqual/
// notStrictEqual: synthesizes `args[0] <op> args[1]` and hands it to
// emitBinary — the exact same string/number/boolean/any comparison
// machinery `==`/`!=` already use (emit_exprs_operators.go), so an
// unsupported comparison (e.g. two arrays) is rejected with the same error
// a plain `a == b` would give, rather than a second implementation to keep
// in sync.
func (e *Emitter) emitAssertBinary(name string, args []ast.Expression, op, defaultMsg string, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: assert.%s takes 2-3 arguments", pos.Line, pos.Col, name)
	}
	cmp, err := e.emitBinary(ast.NewBinaryExpression(op, args[0], args[1], pos))
	if err != nil {
		return Value{}, err
	}
	return e.emitAssertCheck(cmp, args, 2, defaultMsg, pos)
}

// emitAssertThrows implements assert.throws(fn, message?): calls fn (a
// zero-arg closure) inside the same setjmp/longjmp try frame emitTry
// (emit_exceptions.go) itself uses; if fn returns normally (no throw),
// that's the failure case — an AssertionError is thrown reporting the
// *missing* exception. The caught error's own type/message is never
// inspected (see this file's header comment).
func (e *Emitter) emitAssertThrows(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: assert.throws takes 1-2 arguments", pos.Line, pos.Col)
	}
	if fnTy := e.inferExprType(args[0]); !fnTy.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: assert.throws's first argument must be a function", pos.Line, pos.Col)
	}
	fnVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}

	e.ensureExceptionHelpers()

	tryL := e.freshLabel("assert.throws.try")
	caughtL := e.freshLabel("assert.throws.caught")
	doneL := e.freshLabel("assert.throws.done")

	jmpbuf := e.freshReg()
	sjRet := e.freshReg()
	threw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = call i32 @setjmp(ptr %s)", sjRet, jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sjRet))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, caughtL, tryL))

	e.emitLabel(tryL)
	if _, err := e.emitClosureCallByPtr(fnVal.Ref, fnVal.Ty, nil, pos); err != nil {
		return Value{}, err
	}
	e.emitInstr("call void @__kml_pop_jmpbuf()")
	msgPtr := e.internString("missing expected exception")
	if len(args) == 2 {
		msgPtr, err = e.emitAssertMessage(args[1])
		if err != nil {
			return Value{}, err
		}
	}
	e.emitAssertThrowFail(msgPtr)

	e.emitLabel(caughtL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}
