// emit_headers.go — `new Headers()`/`new Headers(init)` (TDD-00040).
// Headers IS a Map<string,string> under the hood (HeadersType, types.go) —
// get/set/has/delete/forEach/entries/keys/values all come for free from the
// existing Map<string,string> runtime (emit_collections.go) with zero new
// runtime code. Only two things are genuinely Headers-specific: normalizing
// every key to lowercase (matching the real spec's case-insensitive header
// names) and append(), the one method with no Map equivalent.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitNewHeadersExpression implements `new Headers()` (empty) and
// `new Headers(init)` (init: Map<string,string> — copies every entry,
// lowercasing each key so a caller who happens to pass already-mixed-case
// keys still ends up with normalized storage).
func (e *Emitter) emitNewHeadersExpression(ex *ast.NewHeadersExpression) (Value, error) {
	if ex.Init == nil {
		e.ensureMapStrHelpers()
		mapPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))
		return Value{Ref: mapPtr, Ty: HeadersType()}, nil
	}
	initVal, err := e.emitExpr(ex.Init)
	if err != nil {
		return Value{}, err
	}
	if !isHeaderMapType(initVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: Headers' init argument must be a Map<string, string>", ex.GetPos().Line, ex.GetPos().Col)
	}
	return e.emitHeadersFromMapValue(initVal)
}

// isHeaderMapType reports whether ty is a Map<string,string> — the one
// shape both `new Headers(init)` and `new Request(url, init)`'s init.headers
// field accept, whether or not IsHeaders happens to also be set.
func isHeaderMapType(ty Type) bool {
	return ty.IsMap && ty.MapKey != nil && ty.MapVal != nil &&
		isStringTy(*ty.MapKey) && isStringTy(*ty.MapVal)
}

// emitHeadersFromMapValue builds a fresh Headers object by copying every
// entry out of an already-evaluated Map<string,string>-shaped Value (which
// may itself already be a Headers, or a plain Map<string,string> — both
// share the identical underlying representation), lowercasing each key
// during the copy. Shared by emitNewHeadersExpression's init-argument path
// and emitNewRequestExpression's own init.headers extraction
// (emit_fetch_request.go).
func (e *Emitter) emitHeadersFromMapValue(mapVal Value) (Value, error) {
	e.ensureMapStrHelpers()
	e.ensureStringToLower()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))

	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(mapVal.Ref, true)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("headers.copy.cond")
	bodyL := e.freshLabel("headers.copy.body")
	doneL := e.freshLabel("headers.copy.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	isDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", isDone, idxVal, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDone, doneL, bodyL))

	e.emitLabel(bodyL)
	keyGep, keyVal := e.freshReg(), e.freshReg()
	valGep, valVal := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", keyGep, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", keyVal, keyGep))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", valGep, valsPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", valVal, valGep))

	lowered := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", lowered, keyVal))
	valAsI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", valAsI64, valVal))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, lowered, valAsI64))

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ref: mapPtr, Ty: HeadersType()}, nil
}

// emitLoweredHeaderName evaluates nameExpr and lowercases it via the same
// __kml_tolower primitive String.prototype.toLowerCase() uses — shared by
// every Headers method below that takes a header-name argument.
func (e *Emitter) emitLoweredHeaderName(nameExpr ast.Expression) (string, error) {
	nameVal, err := e.emitExpr(nameExpr)
	if err != nil {
		return "", err
	}
	nameVal = e.coerce(nameVal, TypePtr)
	e.ensureStringToLower()
	lowered := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", lowered, nameVal.Ref))
	return lowered, nil
}

// emitHeadersCall dispatches Headers' case-insensitive get/set/has/delete
// (thin lowercased-key wrappers around the exact same __kml_map_str_*
// primitives emitMapCall already calls for a plain Map<string,string>) plus
// append (the one method with no Map equivalent). forEach/entries/keys/
// values are deliberately not handled here — they fall through to the
// generic Map dispatch in emit_call.go unchanged, since Headers IS a
// Map<string,string> and none of those four need case-insensitive key
// handling (they read back whatever was actually stored, already
// lowercased by get/set/has/delete/append/the constructor above).
func (e *Emitter) emitHeadersCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	ty, mapPtr, err := e.resolveMapOrSetForCall(objExpr, pos)
	if err != nil {
		return Value{}, err
	}

	switch method {
	case "get":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: headers.get() requires 1 argument", pos.Line, pos.Col)
		}
		lowered, err := e.emitLoweredHeaderName(args[0])
		if err != nil {
			return Value{}, err
		}
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, mapPtr, lowered))
		return e.mapValFromI64(raw, TypePtr), nil

	case "has":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: headers.has() requires 1 argument", pos.Line, pos.Col)
		}
		lowered, err := e.emitLoweredHeaderName(args[0])
		if err != nil {
			return Value{}, err
		}
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", res, mapPtr, lowered))
		return Value{Ref: res, Ty: TypeBool}, nil

	case "delete":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: headers.delete() requires 1 argument", pos.Line, pos.Col)
		}
		lowered, err := e.emitLoweredHeaderName(args[0])
		if err != nil {
			return Value{}, err
		}
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_delete(ptr %s, ptr %s)", res, mapPtr, lowered))
		return Value{Ref: res, Ty: TypeBool}, nil

	case "set":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: headers.set() requires 2 arguments", pos.Line, pos.Col)
		}
		lowered, err := e.emitLoweredHeaderName(args[0])
		if err != nil {
			return Value{}, err
		}
		valVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		valVal = e.coerce(valVal, TypePtr)
		valAsI64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", valAsI64, valVal.Ref))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, lowered, valAsI64))
		return Value{Ref: mapPtr, Ty: ty}, nil

	case "append":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: headers.append() requires 2 arguments", pos.Line, pos.Col)
		}
		lowered, err := e.emitLoweredHeaderName(args[0])
		if err != nil {
			return Value{}, err
		}
		valVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		valVal = e.coerce(valVal, TypePtr)

		hasRes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasRes, mapPtr, lowered))
		combined, err := e.emitStrBranch(hasRes,
			func() (string, error) {
				existingRaw := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", existingRaw, mapPtr, lowered))
				existing := e.mapValFromI64(existingRaw, TypePtr)
				sep := e.internString(", ")
				joined, err := e.emitStringConcat(existing, Value{Ref: sep, Ty: TypePtr})
				if err != nil {
					return "", err
				}
				final, err := e.emitStringConcat(joined, valVal)
				if err != nil {
					return "", err
				}
				return final.Ref, nil
			},
			func() (string, error) { return valVal.Ref, nil })
		if err != nil {
			return Value{}, err
		}
		combinedAsI64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", combinedAsI64, combined))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, lowered, combinedAsI64))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Headers method '%s'", pos.Line, pos.Col, method)
}
