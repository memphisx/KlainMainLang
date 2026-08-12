// emit_exceptions.go — try/catch/throw/new Error emission via setjmp/longjmp.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// errorKinds is the fixed, built-in Error kind enum (TDD-00013 Option A) —
// index into this slice is the runtime kind tag stored in every Error
// object's hidden field 0. "Error" is always kind 0, the base every other
// kind is unconditionally `instanceof` (see emitErrorInstanceOf).
var errorKinds = []string{"Error", "TypeError", "RangeError", "SyntaxError", "EvalError", "URIError", "ReferenceError"}

// errorKindIDs maps a kind name to its errorKinds index, built once at
// package init. Every case in parser_literals.go's parseNew Error-kind
// switch is guaranteed present here — the parser and this table are kept in
// sync by hand, same convention typedArrayElemKinds already uses.
var errorKindIDs = func() map[string]int64 {
	m := make(map[string]int64, len(errorKinds))
	for i, k := range errorKinds {
		m[k] = int64(i)
	}
	return m
}()

// errorObjType is the shared runtime shape of every Error and its built-in
// subtypes (TypeError, RangeError, ...): a hidden i64 kind tag (field 0,
// same ClassTagField-style convention TDD-00009 Stage 2 uses for user
// classes — see VisibleFields), then message, then name. All kinds share
// this one Type; only the stored kind tag and message/name contents differ
// between e.g. a TypeError and a RangeError instance.
var errorObjType = func() Type {
	ty := ObjectType([]Field{
		{Name: "kind", Ty: TypeI64},
		{Name: "message", Ty: TypePtr},
		{Name: "name", Ty: TypePtr},
	})
	ty.IsError = true
	return ty
}()

// buildErrorObj mallocs and fills a new errorObjType instance ({i64 kind,
// ptr message, ptr name}) from already-computed operands, returning the
// instance's ptr register. The one place that knows how to construct an
// errorObjType instance — shared by `new Error(...)`/`new TypeError(...)`
// (emitNewError), a thrown non-object primitive (emitThrow), an internally
// thrown runtime error (emitInternalThrow — array bounds, division by zero,
// fs/fetch/exec failures, ...), and Promise.allSettled's rejection reason
// (emit_promise.go) — deliberately factored out rather than duplicated at
// each of those call sites, since every one of them must agree on the exact
// same 3-field layout; a single point of truth is what makes that safe to
// change later (see ADR-00082's investigation for what happened before this
// existed: some call sites still building the old 1-field shape).
func (e *Emitter) buildErrorObj(kindID int64, msgPtr, namePtr string) string {
	e.ensureExceptionHelpers()

	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, errorObjType.StructSize()))

	kindGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", kindID, kindGep))

	msgGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", msgGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", msgPtr, msgGep))

	nameGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", nameGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", namePtr, nameGep))

	return dataReg
}

// emitInternalThrow throws a base-Error-shaped errorObjType instance (kind
// 0) carrying msgPtr as both message and (via the interned "Error" literal)
// name, then emits `unreachable` — the shared tail every internally
// generated runtime error (array bounds, division by zero, frozen-object
// write, fs/fetch/exec failures, ...) uses after building its own message
// string. Callers are responsible for emitting whatever guard/branch leads
// into this call; this only covers "build the Error object and throw it."
func (e *Emitter) emitInternalThrow(msgPtr string) {
	errReg := e.buildErrorObj(0, msgPtr, e.internString("Error"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
}

// emitNewError emits `new Error(msg)` / `new TypeError(msg)` / etc. —
// allocates the 24-byte {i64, ptr, ptr} errorObjType struct, storing the
// kind tag, message, and name, and returns a ptr Value typed as errorObjType.
func (e *Emitter) emitNewError(ne *ast.NewErrorExpression) (Value, error) {
	e.ensureExceptionHelpers()

	var msgPtr string
	if ne.Message != nil {
		msgVal, err := e.emitExpr(ne.Message)
		if err != nil {
			return Value{}, err
		}
		msgVal = e.coerce(msgVal, TypePtr)
		msgPtr = msgVal.Ref
	} else {
		msgPtr = e.internString(ne.Kind)
	}

	dataReg := e.buildErrorObj(errorKindIDs[ne.Kind], msgPtr, e.internString(ne.Kind))
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
	if val.Ty.IsObject {
		errPtr = val.Ref
	} else {
		// Wrap the value in a base-Error-shaped errorObjType struct with a
		// stringified message, so `.message`/`.name`/`instanceof Error`
		// against the caught value all still work as if `new Error(...)` had
		// been thrown instead of a bare primitive.
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return err
		}
		errPtr = e.buildErrorObj(0, strVal.Ref, e.internString("Error"))
	}

	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr))
	e.emitTerminator("unreachable")
	return nil
}

// emitTry emits a try/catch/finally statement using setjmp/longjmp.
//
// Control flow layout:
//
//	current_block → (setjmp == 0) → try_body
//	              → (setjmp != 0) → catch_block
//	try_body   → (success) → after
//	catch_block            → after
//	after      → finally body (inline)
func (e *Emitter) emitTry(s *ast.TryStatement) error {
	e.ensureExceptionHelpers()

	tryL := e.freshLabel("try.body")
	catchL := e.freshLabel("try.catch")
	afterL := e.freshLabel("try.after")

	// Push a jmpbuf slot and call setjmp.
	jmpbuf := e.freshReg()
	sjRet := e.freshReg()
	threw := e.freshReg()
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
		} else if len(s.Catch.ObjectPattern) > 0 {
			// Destructured catch binding (`catch ({ message, name }) {}`) —
			// every thrown value (including a thrown non-Error primitive,
			// see buildErrorObj's own doc comment) is force-shaped into
			// errorObjType by the time it reaches here, so this can only
			// ever destructure that fixed {kind, message, name} shape, not
			// whatever arbitrary object a `throw` expression's own source
			// literal happened to have.
			errPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", errPtr))
			if err := e.unpackObjectPatternInto(errPtr, errorObjType, s.Catch.ObjectPattern, s.Catch.Pos); err != nil {
				e.popScope()
				return err
			}
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
