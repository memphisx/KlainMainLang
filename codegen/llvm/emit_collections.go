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

// emitMapCall dispatches Map method calls: .set .get .has .delete .keys .values
func (e *Emitter) emitMapCall(mapSym Symbol, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	ty := mapSym.Ty
	keyTy := TypePtr
	valTy := TypeI64
	if ty.MapKey != nil {
		keyTy = *ty.MapKey
	}
	if ty.MapVal != nil {
		valTy = *ty.MapVal
	}
	strKey := isStringTy(keyTy)

	// Load the map ptr from the alloca.
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, mapSym.Ptr))

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
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Map method '%s'", pos.Line, pos.Col, method)
}

// emitSetCall dispatches Set method calls: .add .has .delete .values
func (e *Emitter) emitSetCall(setSym Symbol, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	ty := setSym.Ty
	elemTy := TypePtr
	if ty.MapKey != nil {
		elemTy = *ty.MapKey
	}
	strElem := isStringTy(elemTy)

	setPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", setPtr, setSym.Ptr))

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
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Set method '%s'", pos.Line, pos.Col, method)
}

// mapOrSetValuesArray resolves a Map or Set symbol to the {ptr, i64} array
// aggregate `for...of` should iterate: a Set's own elements (same array
// .values() already returns, since Set elements are stored as map keys), or
// a Map's values (not [key,value] entries — this compiler has no
// destructuring-in-for-of support, so a bare `for (const x of map)` iterates
// values, same shape as Set, rather than matching real JS's entry-pair
// default; `for (const k of map.keys())` remains the way to get keys).
func (e *Emitter) mapOrSetValuesArray(sym Symbol) (Value, error) {
	ty := sym.Ty
	ptr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))

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
