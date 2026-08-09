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
