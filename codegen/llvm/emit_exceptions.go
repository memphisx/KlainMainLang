// emit_exceptions.go — try/catch/throw/new Error emission via setjmp/longjmp.
package llvm

import (
	"fmt"
	"KlainMainLang/ast"
)

var errorObjType = ObjectType([]Field{{Name: "message", Ty: TypePtr}})

// emitNewError emits `new Error(msg)` — allocates an 8-byte {ptr} struct and
// stores the message pointer, returning a ptr Value typed as an Error object.
func (e *Emitter) emitNewError(ne *ast.NewErrorExpression) (Value, error) {
	e.ensureExceptionHelpers()

	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", dataReg))

	var msgPtr string
	if ne.Message != nil {
		msgVal, err := e.emitExpr(ne.Message)
		if err != nil {
			return Value{}, err
		}
		msgVal = e.coerce(msgVal, TypePtr)
		msgPtr = msgVal.Ref
	} else {
		msgPtr = e.internString("Error")
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", msgPtr, dataReg))
	return Value{Ref: dataReg, Ty: errorObjType}, nil
}

// emitThrow emits a throw statement: calls @__kml_throw then unreachable.
func (e *Emitter) emitThrow(s *ast.ThrowStatement) error {
	e.ensureExceptionHelpers()

	val, err := e.emitExpr(s.Argument)
	if err != nil {
		return err
	}

	var errPtr string
	if val.Ty.IsObject || val.Ty.IR == "ptr" {
		errPtr = val.Ref
	} else {
		// Wrap the value in an Error struct with a stringified message.
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", dataReg))
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", strVal.Ref, dataReg))
		errPtr = dataReg
	}

	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr))
	e.emitTerminator("unreachable")
	return nil
}

// emitTry emits a try/catch/finally statement using setjmp/longjmp.
//
// Control flow layout:
//   current_block → (setjmp == 0) → try_body
//                 → (setjmp != 0) → catch_block
//   try_body   → (success) → after
//   catch_block            → after
//   after      → finally body (inline)
func (e *Emitter) emitTry(s *ast.TryStatement) error {
	e.ensureExceptionHelpers()

	tryL   := e.freshLabel("try.body")
	catchL := e.freshLabel("try.catch")
	afterL := e.freshLabel("try.after")

	// Push a jmpbuf slot and call setjmp.
	jmpbuf := e.freshReg()
	sjRet  := e.freshReg()
	threw  := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = call i32 @setjmp(ptr %s)", sjRet, jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sjRet))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, catchL, tryL))

	// --- try body ---
	e.emitLabel(tryL)
	e.pushScope()
	for _, stmt := range s.Body.Body {
		if err := e.emitStmt(stmt); err != nil {
			e.popScope()
			return err
		}
	}
	e.popScope()
	// Pop jmpbuf only on the success path; __kml_throw pops it on the throw path.
	e.emitInstr("call void @__kml_pop_jmpbuf()")
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	// --- catch block ---
	e.emitLabel(catchL)
	if s.Catch != nil {
		e.pushScope()
		if s.Catch.Param != "" {
			errPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", errPtr))
			varPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", varPtr))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", errPtr, varPtr))
			e.define(s.Catch.Param, Symbol{Ptr: varPtr, Ty: errorObjType})
		}
		for _, stmt := range s.Catch.Body.Body {
			if err := e.emitStmt(stmt); err != nil {
				e.popScope()
				return err
			}
		}
		e.popScope()
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	// --- merge / finally ---
	e.emitLabel(afterL)
	if s.Finally != nil {
		e.pushScope()
		for _, stmt := range s.Finally.Body {
			if err := e.emitStmt(stmt); err != nil {
				e.popScope()
				return err
			}
		}
		e.popScope()
	}
	return nil
}
