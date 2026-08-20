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
// DOMException is included so `x instanceof DOMException` is decidable and the
// abort/timeout errors fetch throws carry a real DOMException kind tag. Per the
// current WebIDL spec DOMException inherits from Error, so `instanceof Error`
// stays true for it too (emitErrorInstanceOf treats "Error" as the base every
// kind matches). Its runtime `.name` (unlike the other kinds) is not fixed to
// the kind name — it is "AbortError"/"TimeoutError"/etc. per construction site.
var errorKinds = []string{"Error", "TypeError", "RangeError", "SyntaxError", "EvalError", "URIError", "ReferenceError", "DOMException", "AggregateError"}

// aggregateErrorStructIR is AggregateError's extended runtime layout: the shared
// 3-field errorObjType prefix (kind/message/name, byte-identical offsets so
// .message/.name/kind reads work through errorObjType.StructIR() unchanged) plus
// a trailing `{ errData ptr, errLen i64 }` carrying the aggregated errors array
// (TDD-00083). Only an AggregateError is allocated at this 40-byte size; every
// other error stays the 24-byte errorObjType, and `.errors` access (see
// emitErrorErrorsAccess) is kind-guarded so those trailing fields are never read
// on a non-aggregate object.
const (
	aggregateErrorStructIR   = "{ i64, ptr, ptr, ptr, i64 }"
	aggregateErrorStructSize = 40
)

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

// buildAggregateErrorObj mallocs and fills an AggregateError (the extended
// 40-byte aggregateErrorStructIR): kind tag, message, name ("AggregateError"),
// and the aggregated errors as a { ptr data, i64 len } array in the trailing two
// fields. Returned as a plain ptr; callers type it errorObjType (the shared
// error type) — `.errors` reads the trailing fields via a kind guard.
func (e *Emitter) buildAggregateErrorObj(msgPtr, namePtr, dataPtr, lenRef string) string {
	e.ensureExceptionHelpers()
	e.ensureMalloc()
	kindID := errorKindIDs["AggregateError"]
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", data, aggregateErrorStructSize))
	store := func(idx int, ty, ref string) {
		gp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gp, aggregateErrorStructIR, data, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ty, ref, gp))
	}
	store(0, "i64", fmt.Sprintf("%d", kindID))
	store(1, "ptr", msgPtr)
	store(2, "ptr", namePtr)
	store(3, "ptr", dataPtr)
	store(4, "i64", lenRef)
	return data
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

	// AggregateError(errors, message?) carries the aggregated errors array in its
	// extended layout; message defaults to the empty string (real JS), name is
	// the fixed "AggregateError".
	if ne.Kind == "AggregateError" {
		return e.emitNewAggregateError(ne)
	}

	var msgPtr string
	if ne.Message != nil {
		msgVal, err := e.emitExpr(ne.Message)
		if err != nil {
			return Value{}, err
		}
		msgVal = e.coerce(msgVal, TypePtr)
		msgPtr = msgVal.Ref
	} else if ne.Kind == "DOMException" {
		// `new DOMException()` defaults message to the empty string, not the
		// kind name (the other kinds default to their own name as the message).
		msgPtr = e.internString("")
	} else {
		msgPtr = e.internString(ne.Kind)
	}

	// DOMException's `.name` is the second constructor argument (default
	// "Error"), unlike the fixed-name kinds whose name is the kind itself.
	namePtr := e.internString(ne.Kind)
	if ne.Kind == "DOMException" {
		namePtr = e.internString("Error")
		if ne.Name != nil {
			nameVal, err := e.emitExpr(ne.Name)
			if err != nil {
				return Value{}, err
			}
			nameVal = e.coerce(nameVal, TypePtr)
			namePtr = nameVal.Ref
		}
	}

	dataReg := e.buildErrorObj(errorKindIDs[ne.Kind], msgPtr, namePtr)
	return Value{Ref: dataReg, Ty: errorObjType}, nil
}

// emitNewAggregateError emits `new AggregateError(errors, message?)`. The errors
// array resolves to a { data ptr, len } pair stored in the extended layout; the
// value is typed errorObjType (the shared error type) so it flows through
// catch/throw and `instanceof` exactly like every other error kind, with
// `.errors` reading the trailing fields (emitErrorErrorsAccess).
func (e *Emitter) emitNewAggregateError(ne *ast.NewErrorExpression) (Value, error) {
	dataPtr, lenReg := "null", "0"
	if ne.Errors != nil {
		p, l, _, err := e.resolveArrayForHOF(ne.Errors, ne.GetPos())
		if err != nil {
			return Value{}, err
		}
		dataPtr, lenReg = p, l
	}
	msgPtr := e.internString("")
	if ne.Message != nil {
		msgVal, err := e.emitExpr(ne.Message)
		if err != nil {
			return Value{}, err
		}
		msgVal = e.coerce(msgVal, TypePtr)
		msgPtr = msgVal.Ref
	}
	namePtr := e.internString("AggregateError")
	data := e.buildAggregateErrorObj(msgPtr, namePtr, dataPtr, lenReg)
	return Value{Ref: data, Ty: errorObjType}, nil
}

// emitErrorErrorsAccess reads `err.errors` (AggregateError). Kind-guarded: an
// actual AggregateError yields its stored { data, len } array; any other error
// kind yields an empty array — a non-aggregate is only 24 bytes, so its trailing
// fields are never read. Returns an `errorObjType[]` array value (the aggregated
// errors are error objects, so `err.errors[i].message` works).
func (e *Emitter) emitErrorErrorsAccess(errPtr string) Value {
	aggID := errorKindIDs["AggregateError"]
	kp := e.freshReg()
	k := e.freshReg()
	isAgg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kp, errorObjType.StructIR(), errPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", k, kp))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isAgg, k, aggID))
	aggL := e.freshLabel("aggerr.errors")
	emptyL := e.freshLabel("aggerr.empty")
	mergeL := e.freshLabel("aggerr.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAgg, aggL, emptyL))

	e.emitLabel(aggL)
	dp := e.freshReg()
	d := e.freshReg()
	lp := e.freshReg()
	l := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", dp, aggregateErrorStructIR, errPtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", d, dp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", lp, aggregateErrorStructIR, errPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lp))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(emptyL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	data := e.freshReg()
	length := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ null, %%%s ]", data, d, aggL, emptyL))
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ 0, %%%s ]", length, l, aggL, emptyL))
	a0 := e.freshReg()
	a1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", a0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", a1, a0, length))
	return Value{Ref: a1, Ty: ArrayOf(errorObjType)}
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

// emitPendingFinallys emits every enclosing `finally` block inline, innermost
// first, so a `return` that leaves a try/catch still runs its finally cleanup
// before the `ret`. It unwinds the whole stack (down to the function boundary);
// break/continue instead use emitFinallysToDepth to stop at their loop. While
// emitting the i-th finally the active pending stack is narrowed to the
// finallys outside it, so a control-flow exit *inside* a finally runs only
// those outer ones — and that exit's own terminator then supersedes the
// original pending one (via blockDone), matching JS's "an abrupt completion in
// finally wins" rule.
func (e *Emitter) emitPendingFinallys() error {
	return e.emitFinallysToDepth(0)
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

	// A `return`/`break`/`continue` inside the try or catch must run this
	// finally before it exits — push it so emitPendingFinallys can splice it in
	// (popped just before the normal-path inline finally at afterL below).
	if s.Finally != nil {
		e.pendingFinallys = append(e.pendingFinallys, s.Finally.Body)
	}

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
	// Pop the pending finally: from here on it runs via the normal fall-through
	// path below, not via an early-exit splice.
	if s.Finally != nil {
		e.pendingFinallys = e.pendingFinallys[:len(e.pendingFinallys)-1]
	}
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
