// emit_exceptions.go — try/catch/throw/new Error emission via setjmp/longjmp.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// errorTypeIDFlag is the high marker bit set in field 0 of every Error-shaped
// object — a built-in errorObjType (where field 0 is the error kind) and an
// error-subclass class instance (where field 0 is the class TagID). It lets a
// general boxed-object site tell an Error apart from a plain class instance or
// a dynobj bag, whose field-0 tag is a small integer (class TagIDs are far
// below this bit by construction). The low bits keep the original kind/TagID,
// so nothing that reads the value masks — every kind read is an equality
// compare, which flags the compared constant via errorTypeIDStored instead
// (TDD-00222).
//
// Bit 48 specifically: a render/member/JSON site reads field 0 of an ARBITRARY
// boxed object (which may be a plain malloc'd struct whose field 0 is a
// pointer, e.g. a boxed allSettled settlement). Valid user-space heap pointers
// on both targets (arm64 macOS, x86-64 Linux) are < 2^47, so bit 48 is always
// clear for a pointer or a small tag — only a real Error ever has it set, so
// the field-0 probe can never mistake a pointer slot for an Error.
const errorTypeIDFlag = 1 << 48

// errorTypeIDStored returns the field-0 value stored for an Error whose logical
// kind/TagID is id: the marker bit OR'd onto id. Every write of an Error's
// field 0 uses this, and every compare against an Error's field 0 flags its
// constant with it, so the two sides stay in lockstep.
func errorTypeIDStored(id int64) int64 { return errorTypeIDFlag | id }

// errorSubclassTagBase is the TagID floor for a `class X extends Error`
// instance (TDD-00155 Stage 6). It sits above every builtin error kind (0–8)
// so a caught subclass instance never satisfies `instanceof TypeError`, and it
// lets a boxed-object render/member site tell a built-in errorObjType (whose
// message/name fields line up) from a subclass class instance (a different
// struct layout): a flagged field-0 whose low bits are < this base is a
// built-in error; >= this base is a subclass instance (TDD-00222).
const errorSubclassTagBase = 1000

// emitBoxedErrorProbe reads field 0 of a boxed object whose i64 payload
// (a ptrtoint of the object pointer) is payloadReg, and returns objPtr (the
// pointer) plus an i1 that is true iff the object is Error-shaped — the Error
// type-id flag is set. That covers both a built-in errorObjType AND an
// error-subclass class instance: a `class X extends Error` layout is
// prefix-compatible with errorObjType by construction (the tag slot occupies
// the kind slot, message/name follow at the same offsets, and an error
// subclass can never carry a vtable — dynamic dispatch requires subclassing,
// which is rejected for error subclasses), so render/member sites read the
// real fields the same way for both (TDD-00222; the subclass arm closed the
// former "renders [object Object]" caveat on allSettled/AbortSignal reasons).
func (e *Emitter) emitBoxedErrorProbe(payloadReg string) (objPtr, isErr string) {
	objPtr = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", objPtr, payloadReg))
	f0Gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", f0Gep, errorObjType.StructIR(), objPtr))
	f0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", f0, f0Gep))
	flagged := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", flagged, f0, errorTypeIDFlag))
	isErr = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isErr, flagged))
	return objPtr, isErr
}

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
// (TDD-00083). Only an AggregateError is allocated at this size; every other
// error stays the plain errorObjType, and `.errors` access (see
// emitErrorErrorsAccess) is kind-guarded so those trailing fields are never read
// on a non-aggregate object. The allocation is sized to the full errorObjType
// (not just the 5 IR fields here) so that a bounds-safe (if non-meaningful)
// read of any errorObjType field — code/errcode/errstr and now syscall/path/dest —
// through errorObjType.StructIR() on an AggregateError stays inside the buffer.
// Kept >= errorObjType.StructSize() (fourteen 8-byte fields = 112); bump this
// in lockstep whenever a field is appended to errorObjType.
const (
	aggregateErrorStructIR   = "{ i64, ptr, ptr, ptr, i64 }"
	aggregateErrorStructSize = 112
)

// errorKindIDs maps a kind name to its errorKinds index, built once at
// package init. Every case in parser_literals.go's parseNew Error-kind
// switch is guaranteed present here — the parser and this table are kept in
// sync by hand, same convention typedArrayElemKinds already uses.
// isErrorKindName reports whether name is one of the built-in error
// constructors — used by emitIdent's value-position funcref boxing.
func isErrorKindName(name string) bool {
	_, ok := errorKindIDs[name]
	return ok
}

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
// Fields 0–2 (kind/message/name) keep byte-identical offsets so the
// AggregateError prefix (aggregateErrorStructIR) and every .message/.name read
// stay valid. Fields 3–5 (code/errcode/errstr) carry the Node error-code trio
// (ERR_* strings + numeric SQLite result code); they default to null/0 for
// ordinary errors and are set for node:sqlite failures (ADR-00540). Reading
// them on an AggregateError object is not meaningful (that struct's trailing
// slots mean something else) but is bounds-safe.
var errorObjType = func() Type {
	ty := ObjectType([]Field{
		{Name: "kind", Ty: TypeI64},
		{Name: "message", Ty: TypePtr},
		{Name: "name", Ty: TypePtr},
		{Name: "code", Ty: TypePtr},
		{Name: "errcode", Ty: TypeF64},
		{Name: "errstr", Ty: TypePtr},
		// Node fs-error extras: `err.syscall` (the bare syscall name — "open",
		// "stat", "scandir", …) and `err.path` (the offending path). Default
		// null for non-fs errors; set by __kml_fs_throw (ADR-00768).
		{Name: "syscall", Ty: TypePtr},
		{Name: "path", Ty: TypePtr},
		// `err.errno` — the negative libuv-style errno (Node fs/net/child_process
		// convention: ENOENT → -2 on POSIX). Distinct from `errcode` (idx 4),
		// which is node:sqlite's positive result code. Default 0; set by
		// __kml_fs_throw (ADR-00770).
		{Name: "errno", Ty: TypeF64},
		// `err.dest` — the destination path of a two-path fs op (rename/copyFile).
		// Node sets it alongside `err.path` (the source); null for every other
		// error. Set only by __kml_fs_throw2 (ADR-01000).
		{Name: "dest", Ty: TypePtr},
		// `err.cause` — the error-options bag's cause (`new Error(m, { cause })`),
		// a NaN-boxed any (nbUndefined when absent), matching Node's untyped slot.
		{Name: "cause", Ty: TypeAny},
		// Node net-error extras: `err.address` (the remote IP) and `err.port` (the
		// remote port) of a failed connection — set only by the async net.connect
		// failure path (__kml_net_connect_errobj, ADR-01021); null/0 for every
		// other error. Every errorObjType allocation is calloc'd, so these default
		// cleanly on errors that don't set them.
		{Name: "address", Ty: TypePtr},
		{Name: "port", Ty: TypeF64},
		// `extra` — a D1 dynamic-object bag of further own properties an error
		// carries beyond the fixed fields above (child_process's `status`/
		// `signal`/`stdout`/`stderr`/`pid`/`output` on an execSync failure,
		// ADR-01080); null for every other error. A caught value's unknown
		// property read consults it (emitDynAnyMemberGetNamed's error arm).
		{Name: "extra", Ty: TypePtr},
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
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", errorTypeIDStored(kindID), kindGep))

	msgGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", msgGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", msgPtr, msgGep))

	nameGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", nameGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", namePtr, nameGep))

	// Default the Node error-code trio (code/errcode/errstr) — set only for
	// errors that carry one (node:sqlite). buildErrorObjWithCode overwrites them.
	codeGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", codeGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", codeGep))
	ecGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", ecGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store double 0.0, ptr %s, align 8", ecGep))
	esGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 5", esGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", esGep))
	// Default the fs-error extras (syscall/path) — set only by __kml_fs_throw.
	scGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 6", scGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", scGep))
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 7", pGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", pGep))
	enGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 8", enGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store double 0.0, ptr %s, align 8", enGep))
	destGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 9", destGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", destGep))
	causeGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 10", causeGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, causeGep))
	// The net-error extras address (11) / port (12) default null/0 on every error
	// that does not set them (only the async net.connect failure path does).
	addrGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 11", addrGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", addrGep))
	portGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 12", portGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store double 0.0, ptr %s, align 8", portGep))
	extraGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 13", extraGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", extraGep))

	return dataReg
}

// emitErrorToString renders an Error the way `err.toString()` / `String(err)` /
// “ `${err}` “ do in JS: `name` when the message is empty, otherwise
// `name + ": " + message` (name is the kind name, always set). errVal is the
// error object pointer.
func (e *Emitter) emitErrorToString(errVal Value) (Value, error) {
	e.ensureStrlen()
	nameGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", nameGep, errorObjType.StructIR(), errVal.Ref))
	namePtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", namePtr, nameGep))
	msgGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", msgGep, errorObjType.StructIR(), errVal.Ref))
	msgPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", msgPtr, msgGep))

	nameVal := Value{Ref: namePtr, Ty: TypePtr}
	// name + ": " + message
	withSep, err := e.emitStringConcat(nameVal, Value{Ref: e.internString(": "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	full, err := e.emitStringConcat(withSep, Value{Ref: msgPtr, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	// Empty message → just the name (JS's Error.prototype.toString rule).
	msgLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", msgLen, msgPtr))
	empty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, msgLen))
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", res, empty, namePtr, full.Ref))
	return Value{Ref: res, Ty: TypePtr}, nil
}

// buildErrorObjWithCode builds an errorObjType instance carrying the Node
// error-code trio (code string, numeric errcode, errstr string) — used by
// node:sqlite failures so `err.code === 'ERR_SQLITE_ERROR'` matches Node.
func (e *Emitter) buildErrorObjWithCode(kindID int64, msgPtr, namePtr, codePtr, errcodeD, errstrPtr string) string {
	dataReg := e.buildErrorObj(kindID, msgPtr, namePtr)
	codeGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", codeGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", codePtr, codeGep))
	ecGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", ecGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", errcodeD, ecGep))
	esGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 5", esGep, errorObjType.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", errstrPtr, esGep))
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
	// Allocate and default-fill through buildErrorObj (full errorObjType size,
	// so a stray shared-field read — code/cause/… — is bounds-safe and
	// well-defined), then overlay the aggregate's trailing { data, len } pair.
	data := e.buildErrorObj(kindID, msgPtr, namePtr)
	store := func(idx int, ty, ref string) {
		gp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gp, aggregateErrorStructIR, data, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ty, ref, gp))
	}
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

// emitInternalThrowKind throws a runtime-detected error of a specific built-in
// kind (e.g. a frozen-object write is a TypeError in strict-mode JS), so a
// `catch (e) { e instanceof TypeError }` narrows correctly — the plain-Error
// emitInternalThrow would tag it kmlTagError with a generic "Error" name.
func (e *Emitter) emitInternalThrowKind(kind, msgPtr string) {
	errReg := e.buildErrorObj(errorKindIDs[kind], msgPtr, e.internString(kind))
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
	if ne.Cause != nil {
		causeVal, err := e.emitExpr(ne.Cause)
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(causeVal)
		if err != nil {
			return Value{}, err
		}
		causeGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 10", causeGep, errorObjType.StructIR(), dataReg))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, causeGep))
	}
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
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isAgg, k, errorTypeIDStored(aggID)))
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

// errorPtrFromValue turns an evaluated throw / `.throw()` argument into an error
// object pointer: an object value is used directly; a primitive is wrapped in a
// base-Error-shaped errorObjType struct with a stringified message, so
// `.message`/`.name`/`instanceof Error` against the caught value work as if
// `new Error(...)` had been thrown.
func (e *Emitter) errorPtrFromValue(val Value) (string, error) {
	if val.Ty.IsObject {
		return val.Ref, nil
	}
	strVal, err := e.emitValueToString(val)
	if err != nil {
		return "", err
	}
	return e.buildErrorObj(0, strVal.Ref, e.internString("Error")), nil
}

// emitThrow emits a throw statement: calls @__kml_throw then unreachable.
func (e *Emitter) emitThrow(s *ast.ThrowStatement) error {
	e.ensureExceptionHelpers()

	val, err := e.emitExpr(s.Argument)
	if err != nil {
		return err
	}
	// TDD-00202: the thrown value keeps its real type. A caught value is
	// re-thrown verbatim (record pass-through); an Error goes through the
	// tag-13 ptr shim; everything else boxes to a NaN-box value whose (tag,
	// payload) the catch reconstructs — so `throw "x"` is caught as the string
	// "x", not wrapped in an Error.
	if val.Ty.IsCaught {
		tag, pay := e.caughtParts(val)
		e.emitInstr(fmt.Sprintf("call void @__kml_throw_any(i8 %s, i64 %s)", tag, pay))
		e.emitTerminator("unreachable")
		return nil
	}
	// An Error, or a `class X extends Error` instance (errorObjType-compatible
	// layout with the subclass TagID in the kind slot), records as kmlTagError
	// so the catch reads its message and resolves `instanceof` off the kind slot.
	isErrSubclass := false
	if val.Ty.ClassName != "" {
		if info, ok := e.classes[val.Ty.ClassName]; ok && info.IsErrorSubclass {
			isErrSubclass = true
		}
	}
	if val.Ty.IsError || isErrSubclass {
		errPtr := e.coerce(val, TypePtr)
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr.Ref))
		e.emitTerminator("unreachable")
		return nil
	}
	boxed, err := e.emitBoxValue(val)
	if err != nil {
		return err
	}
	tag, pay := e.emitUnboxTagPayload(boxed)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_any(i8 %s, i64 %s)", tag, pay))
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
	e.emitInstr(fmt.Sprintf("%s = %s", sjRet, setjmpCall(jmpbuf)))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sjRet))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, catchL, tryL))

	// A `return`/`break`/`continue` inside the try or catch must run this
	// finally before it exits — push it so emitPendingFinallys can splice it in
	// (popped just before the normal-path inline finally at afterL below).
	if s.Finally != nil {
		e.pendingFinallys = append(e.pendingFinallys, s.Finally.Body)
	}
	// Locals touched anywhere in the statement must survive the longjmp
	// (pinSlotAcrossSetjmp, ADR-01057).
	e.tryDepth++
	defer func() { e.tryDepth-- }()

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
			// TDD-00202: bind the catch variable as the unpacked thrown-value
			// record (TypeCaught ≈ TypeScript `unknown`) — a { i8 tag, i64
			// payload } aggregate reconstructed from the throw. Narrowing
			// (typeof/instanceof/===) and Error member access resolve off the tag.
			tagR := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tagR))
			payR := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_get_thrown_pay()", payR))
			agg := e.emitCaughtAggregate(tagR, payR)
			varPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca { i8, i64 }, align 8", varPtr))
			e.emitInstr(fmt.Sprintf("store { i8, i64 } %s, ptr %s, align 8", agg.Ref, varPtr))
			e.define(s.Catch.Param, Symbol{Ptr: varPtr, Ty: TypeCaught})
		} else if len(s.Catch.ObjectPattern) > 0 {
			// Destructured catch binding (`catch ({ message, name }) {}`). A
			// caught Error is destructured by its errorObjType fields; a
			// non-Error thrown value has no such fields, so the record's payload
			// is not a valid errorObjType pointer — synthesize an empty Error so
			// the destructured names read as empty rather than dereferencing a
			// bad pointer (the bare `{ kind, message, name }` shape, TDD-00202).
			tagR := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tagR))
			isErr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isErr, tagR, kmlTagError))
			realPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", realPtr))
			emptyErr := e.buildErrorObj(0, e.internString(""), e.internString("Error"))
			errPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", errPtr, isErr, realPtr, emptyErr))
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
