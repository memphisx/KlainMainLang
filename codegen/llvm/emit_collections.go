// emit_collections.go — Map<K,V> and Set<T> variable declarations and method dispatch.
package llvm

import (
	"fmt"
	"KlainMainLang/ast"
)

// emitMapVarDecl handles `const m = new Map<K, V>()`.
func (e *Emitter) emitMapVarDecl(v *ast.VarDeclaration, init *ast.NewMapExpression) error {
	keyTy := TypePtr // default: string keys
	valTy := TypeI64 // default: number values
	if init.KeyType != nil {
		keyTy = e.resolveType(init.KeyType)
	}
	if init.ValType != nil {
		valTy = e.resolveType(init.ValType)
	}
	ty := MapType(keyTy, valTy)

	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})

	mapPtr := e.freshReg()
	if isStringTy(keyTy) {
		e.ensureMapStrHelpers()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))
	} else {
		e.ensureMapNumHelpers()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_num_create()", mapPtr))
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", mapPtr, ptrName))
	return nil
}

// emitSetVarDecl handles `const s = new Set<T>()`.
func (e *Emitter) emitSetVarDecl(v *ast.VarDeclaration, init *ast.NewSetExpression) error {
	elemTy := TypePtr // default: string elements
	if init.ElemType != nil {
		elemTy = e.resolveType(init.ElemType)
	}
	ty := SetType(elemTy)

	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})

	setPtr := e.freshReg()
	if isStringTy(elemTy) {
		e.ensureMapStrHelpers()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", setPtr))
	} else {
		e.ensureMapNumHelpers()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_num_create()", setPtr))
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", setPtr, ptrName))
	return nil
}

// resolveMapOrSetForCall resolves a Map/Set method call's receiver expression
// to its type and already-loaded heap pointer — the same "named variable vs.
// arbitrary expression" split resolveArrayForHOF already uses for arrays.
// A plain identifier loads the pointer from its alloca (the named-variable
// case, e.g. `m.get(...)`); anything else (a field access, an array index, a
// function call's result) is evaluated directly, since object-field GEP+load
// and friends already yield the map/set's heap pointer with no separate
// alloca indirection to unwrap — e.g. `c.scores.get(...)` where `scores` is
// a Map-typed interface field.
func (e *Emitter) resolveMapOrSetForCall(objExpr ast.Expression, pos ast.Pos) (Type, string, error) {
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if !found || !(sym.Ty.IsMap || sym.Ty.IsSet) {
			return Type{}, "", fmt.Errorf("%d:%d: '%s' is not a Map or Set", pos.Line, pos.Col, id.Name)
		}
		ptr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))
		return sym.Ty, ptr, nil
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return Type{}, "", err
	}
	if !val.Ty.IsMap && !val.Ty.IsSet {
		return Type{}, "", fmt.Errorf("%d:%d: value is not a Map or Set", pos.Line, pos.Col)
	}
	return val.Ty, val.Ref, nil
}

// emitMapCall dispatches Map method calls: .set .get .has .delete .keys .values
// mapPtr is the map's already-resolved heap pointer — the caller (see
// resolveMapOrSetForCall) is responsible for getting there, whether that
// means loading it from a named variable's alloca or evaluating an arbitrary
// expression (a field access, an array index, another call's result) that
// itself produces the pointer directly.
func (e *Emitter) emitMapCall(ty Type, mapPtr string, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	keyTy := TypePtr
	valTy := TypeI64
	if ty.MapKey != nil {
		keyTy = *ty.MapKey
	}
	if ty.MapVal != nil {
		valTy = *ty.MapVal
	}
	strKey := isStringTy(keyTy)

	switch method {
	case "set":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: map.set() requires 2 arguments", pos.Line, pos.Col)
		}
		kVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		vVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		kRef := e.valueToMapKey(kVal, keyTy)
		vRef := e.valueToMapVal(vVal, valTy)
		if strKey {
			e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, kRef, vRef))
		} else {
			e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 %s)", mapPtr, kRef, vRef))
		}
		return Value{Ref: mapPtr, Ty: ty}, nil

	case "get":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: map.get() requires 1 argument", pos.Line, pos.Col)
		}
		kVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		kRef := e.valueToMapKey(kVal, keyTy)
		raw := e.freshReg()
		if strKey {
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, mapPtr, kRef))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_num_get(ptr %s, i64 %s)", raw, mapPtr, kRef))
		}
		return e.mapValFromI64(raw, valTy), nil

	case "has":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: map.has() requires 1 argument", pos.Line, pos.Col)
		}
		kVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		kRef := e.valueToMapKey(kVal, keyTy)
		res := e.freshReg()
		if strKey {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", res, mapPtr, kRef))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_num_has(ptr %s, i64 %s)", res, mapPtr, kRef))
		}
		return Value{Ref: res, Ty: TypeBool}, nil

	case "delete":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: map.delete() requires 1 argument", pos.Line, pos.Col)
		}
		kVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		kRef := e.valueToMapKey(kVal, keyTy)
		res := e.freshReg()
		if strKey {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_delete(ptr %s, ptr %s)", res, mapPtr, kRef))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_num_delete(ptr %s, i64 %s)", res, mapPtr, kRef))
		}
		return Value{Ref: res, Ty: TypeBool}, nil

	case "keys":
		res := e.freshReg()
		if strKey {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", res, mapPtr))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_keys(ptr %s)", res, mapPtr))
		}
		return Value{Ref: res, Ty: ArrayOf(keyTy)}, nil

	case "values":
		res := e.freshReg()
		if strKey {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_vals(ptr %s)", res, mapPtr))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_vals(ptr %s)", res, mapPtr))
		}
		return Value{Ref: res, Ty: ArrayOf(valTy)}, nil

	case "entries":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: map.entries() takes no arguments", pos.Line, pos.Col)
		}
		return e.emitMapEntries(mapPtr, strKey, keyTy, valTy)

	case "forEach":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: map.forEach() requires 1 argument", pos.Line, pos.Col)
		}
		cb, err := e.resolveCallbackWithHints(args[0], []Type{valTy, keyTy})
		if err != nil {
			return Value{}, err
		}
		return e.emitMapForEach(mapPtr, strKey, keyTy, valTy, cb)

	case "clear":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: map.clear() takes no arguments", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", mapPtr))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Map method '%s'", pos.Line, pos.Col, method)
}

// emitSetCall dispatches Set method calls: .add .has .delete .values
// setPtr is the set's already-resolved heap pointer — see emitMapCall's own
// doc comment for why the caller resolves this rather than emitSetCall
// itself (resolveMapOrSetForCall handles both the named-variable and
// arbitrary-expression cases uniformly).
func (e *Emitter) emitSetCall(ty Type, setPtr string, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	elemTy := TypePtr
	if ty.MapKey != nil {
		elemTy = *ty.MapKey
	}
	strElem := isStringTy(elemTy)

	switch method {
	case "add":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: set.add() requires 1 argument", pos.Line, pos.Col)
		}
		eVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eRef := e.valueToMapKey(eVal, elemTy)
		if strElem {
			e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 0)", setPtr, eRef))
		} else {
			e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 0)", setPtr, eRef))
		}
		return Value{Ref: setPtr, Ty: ty}, nil

	case "has":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: set.has() requires 1 argument", pos.Line, pos.Col)
		}
		eVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eRef := e.valueToMapKey(eVal, elemTy)
		res := e.freshReg()
		if strElem {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", res, setPtr, eRef))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_num_has(ptr %s, i64 %s)", res, setPtr, eRef))
		}
		return Value{Ref: res, Ty: TypeBool}, nil

	case "delete":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: set.delete() requires 1 argument", pos.Line, pos.Col)
		}
		eVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eRef := e.valueToMapKey(eVal, elemTy)
		res := e.freshReg()
		if strElem {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_delete(ptr %s, ptr %s)", res, setPtr, eRef))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_num_delete(ptr %s, i64 %s)", res, setPtr, eRef))
		}
		return Value{Ref: res, Ty: TypeBool}, nil

	case "values":
		// Set elements are stored as keys; return the keys array.
		res := e.freshReg()
		if strElem {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", res, setPtr))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_keys(ptr %s)", res, setPtr))
		}
		return Value{Ref: res, Ty: ArrayOf(elemTy)}, nil

	case "forEach":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: set.forEach() requires 1 argument", pos.Line, pos.Col)
		}
		// Real JS calls back(value, value, set) for a Set — the same value
		// twice, kept only for Map/Set callback-shape parity. Mirrored here
		// as (element, element) when the callback declares a 2nd parameter.
		cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, elemTy})
		if err != nil {
			return Value{}, err
		}
		return e.emitSetForEach(setPtr, strElem, elemTy, cb)

	case "clear":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: set.clear() takes no arguments", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", setPtr))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Set method '%s'", pos.Line, pos.Col, method)
}

// mapOrSetValuesArray resolves a Map or Set's already-loaded heap pointer to
// the {ptr, i64} array aggregate `for...of` should iterate: a Set's own
// elements (same array .values() already returns, since Set elements are
// stored as map keys), or a Map's values (not [key,value] entries — this
// compiler has no destructuring-in-for-of support, so a bare
// `for (const x of map)` iterates values, same shape as Set, rather than
// matching real JS's entry-pair default; `for (const k of map.keys())`
// remains the way to get keys). ptr is resolved by the caller — see
// resolveMapOrSetForCall's doc comment for why (named variable vs. an
// arbitrary expression like a Map/Set-typed field access).
func (e *Emitter) mapOrSetValuesArray(ty Type, ptr string) (Value, error) {
	if ty.IsSet {
		elemTy := TypePtr
		if ty.MapKey != nil {
			elemTy = *ty.MapKey
		}
		res := e.freshReg()
		if isStringTy(elemTy) {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", res, ptr))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_keys(ptr %s)", res, ptr))
		}
		return Value{Ref: res, Ty: ArrayOf(elemTy)}, nil
	}

	keyTy := TypePtr
	if ty.MapKey != nil {
		keyTy = *ty.MapKey
	}
	valTy := TypeI64
	if ty.MapVal != nil {
		valTy = *ty.MapVal
	}
	res := e.freshReg()
	if isStringTy(keyTy) {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_vals(ptr %s)", res, ptr))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_vals(ptr %s)", res, ptr))
	}
	return Value{Ref: res, Ty: ArrayOf(valTy)}, nil
}

// valueToMapKey converts a value to the appropriate key representation for
// the map helpers (ptr for string keys, i64 for number keys).
func (e *Emitter) valueToMapKey(v Value, keyTy Type) string {
	if isStringTy(keyTy) {
		// Ensure we have a ptr (string values already are ptr).
		if v.Ty.IR == "ptr" {
			return v.Ref
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, v.Ref))
		return r
	}
	// Number key: coerce to i64.
	v = e.coerce(v, TypeI64)
	return v.Ref
}

// valueToMapVal converts any scalar value to i64 for uniform map storage.
func (e *Emitter) valueToMapVal(v Value, valTy Type) string {
	switch v.Ty.IR {
	case "i64":
		return v.Ref
	case "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, v.Ref))
		return r
	case "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", r, v.Ref))
		return r
	case "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", r, v.Ref))
		return r
	default:
		if v.Ty.IsInteger() {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sext %s %s to i64", r, v.Ty.IR, v.Ref))
			return r
		}
		return v.Ref
	}
}

// mapKeysAndVals calls the appropriate keys()/vals() runtime helper pair for
// a map ptr already loaded from its alloca, returning the extracted
// {dataPtr, len} pieces of each — shared by emitMapEntries and
// emitMapForEach, both of which need to walk the same two parallel arrays.
func (e *Emitter) mapKeysAndVals(mapPtr string, strKey bool) (keysPtr, keysLen, valsPtr string) {
	keysRes := e.freshReg()
	valsRes := e.freshReg()
	if strKey {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", keysRes, mapPtr))
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_vals(ptr %s)", valsRes, mapPtr))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_keys(ptr %s)", keysRes, mapPtr))
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_vals(ptr %s)", valsRes, mapPtr))
	}
	keysPtr = e.freshReg()
	keysLen = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", keysPtr, keysRes))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", keysLen, keysRes))
	valsPtr = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", valsPtr, valsRes))
	return keysPtr, keysLen, valsPtr
}

// emitMapEntries implements map.entries() → {key: K, value: V}[], the same
// heap-allocated-entry-object convention Object.entries(obj) already uses
// (emit_objects.go's emitObjectEntries) — this compiler has no tuple type,
// so a real JS [key, value] pair isn't representable; iterate with
// `for (const e of m.entries())` then read `e.key` / `e.value`. Unlike
// Object.entries (a compile-time loop over a known field list), a Map's
// size is only known at runtime, so this walks the same {ptr, i64} arrays
// keys()/vals() already return via a genuine IR loop.
func (e *Emitter) emitMapEntries(mapPtr string, strKey bool, keyTy, valTy Type) (Value, error) {
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(mapPtr, strKey)

	entryTy := ObjectType([]Field{{Name: "key", Ty: keyTy}, {Name: "value", Ty: valTy}})
	entrySize := entryTy.StructSize()

	e.ensureMalloc()
	outBytes := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", outBytes, keysLen))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", outPtr, outBytes))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("mapentries.cond")
	bodyL := e.freshLabel("mapentries.body")
	doneL := e.freshLabel("mapentries.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	kGep, kElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", kGep, keyTy.IR, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", kElem, keyTy.IR, kGep, keyTy.Align()))
	vGep, vElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", vGep, valTy.IR, valsPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", vElem, valTy.IR, vGep, valTy.Align()))

	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entryReg, entrySize))
	keySlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", keySlot, entryTy.StructIR(), entryReg))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", keyTy.IR, kElem, keySlot, keyTy.Align()))
	valSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", valTy.IR, vElem, valSlot, valTy.Align()))

	slotReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotReg, outPtr, idxVal))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", entryReg, slotReg))

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, keysLen))
	return Value{Ref: r1, Ty: ArrayOf(entryTy)}, nil
}

// emitMapForEach implements map.forEach(fn): calls fn(value, key?) for each
// entry, matching real JS's (value, key, map) callback order minus the
// dropped third argument — the same "drop the trailing, rarely-used
// argument" convention Array.forEach already uses for its own (elem, index)
// callback.
func (e *Emitter) emitMapForEach(mapPtr string, strKey bool, keyTy, valTy Type, cb Callback) (Value, error) {
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(mapPtr, strKey)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("mapforeach.cond")
	bodyL := e.freshLabel("mapforeach.body")
	doneL := e.freshLabel("mapforeach.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	kGep, kElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", kGep, keyTy.IR, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", kElem, keyTy.IR, kGep, keyTy.Align()))
	vGep, vElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", vGep, valTy.IR, valsPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", vElem, valTy.IR, vGep, valTy.Align()))

	cbArgs := []Value{{Ref: vElem, Ty: valTy}}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: kElem, Ty: keyTy})
	}
	if _, err := e.emitCBCall(cb, cbArgs); err != nil {
		return Value{}, err
	}

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// emitSetForEach implements set.forEach(fn): calls fn(element, element?) for
// each element — the second argument (when the callback declares one)
// mirrors real JS's own quirky Set.prototype.forEach(value, value, set)
// shape, where the "key" is just the value again.
func (e *Emitter) emitSetForEach(setPtr string, strElem bool, elemTy Type, cb Callback) (Value, error) {
	keysRes := e.freshReg()
	if strElem {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", keysRes, setPtr))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_keys(ptr %s)", keysRes, setPtr))
	}
	keysPtr := e.freshReg()
	keysLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", keysPtr, keysRes))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", keysLen, keysRes))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("setforeach.cond")
	bodyL := e.freshLabel("setforeach.body")
	doneL := e.freshLabel("setforeach.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	eGep, eElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", eGep, elemTy.IR, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", eElem, elemTy.IR, eGep, elemTy.Align()))

	cbArgs := []Value{{Ref: eElem, Ty: elemTy}}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: eElem, Ty: elemTy})
	}
	if _, err := e.emitCBCall(cb, cbArgs); err != nil {
		return Value{}, err
	}

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// mapValFromI64 converts a raw i64 retrieved from the map back to the target value type.
func (e *Emitter) mapValFromI64(rawReg string, valTy Type) Value {
	switch valTy.IR {
	case "i64":
		return Value{Ref: rawReg, Ty: valTy}
	case "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, rawReg))
		return Value{Ref: r, Ty: valTy}
	case "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i1", r, rawReg))
		return Value{Ref: r, Ty: valTy}
	case "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", r, rawReg))
		return Value{Ref: r, Ty: valTy}
	default:
		return Value{Ref: rawReg, Ty: TypeI64}
	}
}
