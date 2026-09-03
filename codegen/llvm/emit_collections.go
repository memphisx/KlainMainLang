// emit_collections.go — Map<K,V> and Set<T> variable declarations and method dispatch.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitMapOrSetCreate emits the shared string-keyed-vs-numeric-keyed heap
// pointer creation `new Map<K,V>()`/`new Set<T>()` both reduce to (Set
// reuses the same Map<K,ptr-unused-V> runtime, keyed on keyTy alone) —
// factored out of emitMapVarDecl/emitSetVarDecl so the general-expression
// producers below (TDD-00028) share it instead of duplicating the
// isStringTy branch.
func (e *Emitter) emitMapOrSetCreate(keyTy Type) string {
	ptr := e.freshReg()
	if isStringTy(keyTy) {
		e.ensureMapStrHelpers()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", ptr))
	} else {
		e.ensureMapNumHelpers()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_num_create()", ptr))
	}
	return ptr
}

// emitMapVarDecl handles `const m = new Map<K, V>()`.
func (e *Emitter) emitMapVarDecl(v *ast.VarDeclaration, init *ast.NewMapExpression) error {
	keyTy, valTy := e.mapKVTypes(init.KeyType, init.ValType, init.Init)
	// A bare `new Map()` assigned to an annotated `Map<K,V>`/`Record<string,V>`
	// takes its K/V from the annotation — the `new`-expression carries no type
	// args here. Latent before TDD-00123 (the value default i64 happened to equal
	// `number`); now `number` is float64, so the annotation must win to keep the
	// stored value type and the read-back type in agreement.
	if init.KeyType == nil && init.ValType == nil && v.TypeAnnot != nil {
		if annTy := e.resolveType(v.TypeAnnot); annTy.IsMap && annTy.MapKey != nil && annTy.MapVal != nil {
			keyTy, valTy = *annTy.MapKey, *annTy.MapVal
		}
	}
	val, err := e.emitNewMapValueTyped(init, keyTy, valTy)
	if err != nil {
		return err
	}
	ptrName := e.moduleGlobalPtrOrLocal(v, val.Ty)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
	return nil
}

// emitNewMapValue builds `new Map<K,V>()` as a plain ptr Value (TDD-00028) —
// usable as a general expression (a call argument, a return value, an
// object-literal field, etc.), not just a var-decl initializer. Defaults
// (string keys, number values) match emitMapVarDecl's own when K/V aren't
// given explicitly, since there's no var-decl annotation to infer from in
// a general expression position.
func (e *Emitter) emitNewMapValue(init *ast.NewMapExpression) (Value, error) {
	keyTy, valTy := e.mapKVTypes(init.KeyType, init.ValType, init.Init)
	return e.emitNewMapValueTyped(init, keyTy, valTy)
}

// emitNewMapValueTyped builds the map with explicit key/value types (so a
// var-decl can override them from its annotation — emitMapVarDecl).
func (e *Emitter) emitNewMapValueTyped(init *ast.NewMapExpression, keyTy, valTy Type) (Value, error) {
	mapPtr := e.emitMapOrSetCreate(keyTy)
	if init.Init != nil {
		if err := e.emitMapSeedFromEntries(mapPtr, init.Init, keyTy, valTy, init.GetPos()); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: mapPtr, Ty: MapType(keyTy, valTy)}, nil
}

// mapKVTypes resolves a `new Map(...)`'s key/value types: an explicit
// `<K, V>` wins; otherwise, when initial entries are given, K/V are inferred
// from the source's `[K, V][]` element type — a bare array literal of pairs
// (`[[k, v], ...]`) is read positionally, since it carries no tuple type of
// its own without a contextual hint; otherwise the bare-form
// string-key/number-value defaults stand.
func (e *Emitter) mapKVTypes(keyAnn, valAnn *ast.TypeAnnotation, entries ast.Expression) (keyTy, valTy Type) {
	keyTy, valTy = TypePtr, TypeI64 // defaults: string keys, number values
	if keyAnn == nil && valAnn == nil && entries != nil {
		if lit, ok := entries.(*ast.ArrayLiteral); ok && len(lit.Elements) > 0 {
			if pair, ok := lit.Elements[0].(*ast.ArrayLiteral); ok && len(pair.Elements) == 2 {
				keyTy = e.inferExprType(pair.Elements[0])
				valTy = e.inferExprType(pair.Elements[1])
			}
		} else if elemTy := e.inferExprType(entries); elemTy.IsArray && elemTy.ElemType != nil &&
			elemTy.ElemType.IsTuple && len(elemTy.ElemType.Fields) == 2 {
			keyTy = elemTy.ElemType.Fields[0].Ty
			valTy = elemTy.ElemType.Fields[1].Ty
		}
	}
	if keyAnn != nil {
		keyTy = e.resolveType(keyAnn)
	}
	if valAnn != nil {
		valTy = e.resolveType(valAnn)
	}
	return keyTy, valTy
}

// resolveMapEntriesArray normalizes the entries source to (ptr, len, elemTy),
// like resolveArrayForHOF, but supplies the `[keyTy, valTy]` tuple hint a bare
// array-literal source (`[[k, v], ...]`) needs — its inner `[k, v]` arrays
// carry no tuple type of their own without that contextual push (the same
// hinted-array-literal path an annotated `[K, V][]` var decl already uses). A
// non-literal source (an annotated variable) already carries its element type
// and goes through resolveArrayForHOF unchanged.
func (e *Emitter) resolveMapEntriesArray(entries ast.Expression, keyTy, valTy Type, pos ast.Pos) (ptrReg, lenReg string, elemTy Type, err error) {
	if lit, ok := entries.(*ast.ArrayLiteral); ok {
		// A bare array literal carries no tuple type of its own, so validate its
		// shape here before forcing the `[K, V]` element hint: every element must
		// be a 2-element array literal, else the hinted store would miscompile a
		// scalar as a tuple pointer.
		for _, el := range lit.Elements {
			pair, isArr := el.(*ast.ArrayLiteral)
			if !isArr || len(pair.Elements) != 2 {
				return "", "", Type{}, fmt.Errorf("%d:%d: new Map(...) expects a [key, value][] array of 2-element pairs", pos.Line, pos.Col)
			}
			// keyTy/valTy were inferred from the first pair (mapKVTypes); a later
			// pair whose key/value type differs (`new Map([['a','b'],[1,1]])` — a
			// mixed-type map that would need any-typed keys/values) is rejected
			// cleanly here, rather than being hinted to the first pair's tuple
			// shape and storing the mismatched scalar raw into a ptr field.
			if kt := e.inferExprType(pair.Elements[0]); kt.IR != keyTy.IR && !keyTy.IsArray && !keyTy.IsDynamic && !isNullableScalar(keyTy) {
				return "", "", Type{}, fmt.Errorf("%d:%d: new Map(...) entries must share one key type — a heterogeneous-key map is not supported", pair.Elements[0].GetPos().Line, pair.Elements[0].GetPos().Col)
			}
			if vt := e.inferExprType(pair.Elements[1]); vt.IR != valTy.IR && !valTy.IsArray && !valTy.IsDynamic && !isNullableScalar(valTy) {
				return "", "", Type{}, fmt.Errorf("%d:%d: new Map(...) entries must share one value type — a heterogeneous-value map is not supported", pair.Elements[1].GetPos().Line, pair.Elements[1].GetPos().Col)
			}
		}
		tupleTy := TupleType([]Type{keyTy, valTy})
		var val Value
		val, err = e.emitArrayLiteralAggregate(lit, &tupleTy)
		if err != nil {
			return
		}
		elemTy = tupleTy
		ptrReg = e.freshReg()
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
		return
	}
	return e.resolveArrayForHOF(entries, pos)
}

// emitMapSeedFromEntries populates an already-created map from a `[K, V][]`
// entries array (`new Map([[k, v], ...])`, TDD-00066): it walks the source
// array, and for each 2-tuple element GEPs out field 0 (key) and field 1
// (value), converting each to the map's stored representation and calling the
// same str/num set helper `map.set()` uses. The source's element type must be
// a 2-tuple whose fields match the map's K/V; anything else is a clean
// compile error.
func (e *Emitter) emitMapSeedFromEntries(mapPtr string, entries ast.Expression, keyTy, valTy Type, pos ast.Pos) error {
	srcPtr, srcLen, elemTy, err := e.resolveMapEntriesArray(entries, keyTy, valTy, pos)
	if err != nil {
		return err
	}
	if !elemTy.IsTuple || len(elemTy.Fields) != 2 {
		return fmt.Errorf("%d:%d: new Map(...) expects a [key, value][] array of 2-tuples", pos.Line, pos.Col)
	}
	strKey := isStringTy(keyTy)
	tupleIR := elemTy.StructIR()

	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))
	condL := e.freshLabel("map.init.cond")
	bodyL := e.freshLabel("map.init.body")
	endL := e.freshLabel("map.init.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxReg, idxPtr))
	condReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", condReg, idxReg, srcLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, bodyL, endL))

	e.emitLabel(bodyL)
	// The source array holds one ptr per tuple (a tuple's IR is "ptr" — a heap
	// struct), so load the tuple pointer, then GEP its two fields.
	slotGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slotGep, elemTy.IR, srcPtr, idxReg))
	tuplePtr := e.loadArrayElem(slotGep, elemTy)

	kGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kGep, tupleIR, tuplePtr.Ref))
	kVal := e.loadScalarOrNullableField(kGep, keyTy)
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", vGep, tupleIR, tuplePtr.Ref))
	vVal := e.loadScalarOrNullableField(vGep, valTy)

	kRef := e.valueToMapKey(kVal, keyTy)
	vRef := e.valueToMapVal(vVal, valTy)
	if strKey {
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapPtr, kRef, vRef))
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 %s)", mapPtr, kRef, vRef))
	}

	nextIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextIdx, idxReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nextIdx, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// emitSetVarDecl handles `const s = new Set<T>()`.
func (e *Emitter) emitSetVarDecl(v *ast.VarDeclaration, init *ast.NewSetExpression) error {
	val, err := e.emitNewSetValue(init)
	if err != nil {
		return err
	}
	ptrName := e.moduleGlobalPtrOrLocal(v, val.Ty)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
	return nil
}

// emitNewSetValue is emitNewMapValue's Set<T> sibling (TDD-00028), extended
// (ADR-00159) to accept an optional initial-elements array argument
// (`new Set([1, 2, 3])`) — real spec takes any iterable, narrowed here to
// an array expression, the only iterable concept a general expression has
// in this compiler. elemTy is the explicit `<T>` type argument if given,
// else inferred from the initializer array's own element type, else the
// pre-existing string-element default for the bare no-argument form.
func (e *Emitter) emitNewSetValue(init *ast.NewSetExpression) (Value, error) {
	elemTy := TypePtr // default: string elements
	var srcPtr, srcLen string
	haveSrc := false
	if init.Init != nil {
		ptrReg, lenReg, srcElemTy, err := e.resolveArrayForHOF(init.Init, init.GetPos())
		if err != nil {
			return Value{}, err
		}
		srcPtr, srcLen, elemTy, haveSrc = ptrReg, lenReg, srcElemTy, true
	}
	if init.ElemType != nil {
		elemTy = e.resolveType(init.ElemType)
	}

	setPtr := e.emitMapOrSetCreate(elemTy)

	if haveSrc {
		strElem := isStringTy(elemTy)
		idxPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))
		condL := e.freshLabel("set.init.cond")
		bodyL := e.freshLabel("set.init.body")
		endL := e.freshLabel("set.init.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

		e.emitLabel(condL)
		idxReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxReg, idxPtr))
		condReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", condReg, idxReg, srcLen))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, bodyL, endL))

		e.emitLabel(bodyL)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, srcPtr, idxReg))
		elemVal := e.loadArrayElem(gepReg, elemTy)
		eRef := e.valueToMapKey(elemVal, elemTy)
		if strElem {
			e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 0)", setPtr, eRef))
		} else {
			e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 0)", setPtr, eRef))
		}
		nextIdx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextIdx, idxReg))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nextIdx, idxPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

		e.emitLabel(endL)
	}

	return Value{Ref: setPtr, Ty: SetType(elemTy)}, nil
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
		// A function-typed value contextually types an untyped arrow /
		// function-expression argument's parameters from the value signature
		// (ADR-00632); otherwise they self-infer to the numeric default.
		var vVal Value
		if valTy.IsFunc {
			vVal, err = e.emitExprWithObjectHint(args[1], valTy)
		} else {
			vVal, err = e.emitExpr(args[1])
		}
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
		// A non-pointer scalar value type returns `V | null` (TDD-00064 Stage
		// 3, bug #3): a missing key is genuinely absent — a presence-flagged
		// { i1, V } aggregate whose bit comes from has() — rather than the
		// value 0 the raw i64 get returns for a miss. A pointer value type
		// keeps its null-pointer miss (no scalar collision to disambiguate).
		if isNullableScalarMapValue(valTy) {
			return e.emitMapGetNullable(mapPtr, kRef, strKey, valTy), nil
		}
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
		mapTy := MapType(keyTy, valTy)
		cb, err := e.resolveCallbackWithHints(args[0], []Type{valTy, keyTy, mapTy})
		if err != nil {
			return Value{}, err
		}
		return e.emitMapForEach(mapPtr, strKey, keyTy, valTy, mapTy, cb)

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
		// twice (kept only for Map/Set callback-shape parity), then the set
		// itself. Mirrored here per the callback's declared arity (ADR-00573).
		setTy := SetType(elemTy)
		cb, err := e.resolveCallbackWithHints(args[0], []Type{elemTy, elemTy, setTy})
		if err != nil {
			return Value{}, err
		}
		return e.emitSetForEach(setPtr, strElem, elemTy, setTy, cb)

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
		// A non-ptr value inttoptr's to a ptr — coerce to i64 first so a float
		// `number` (whose Ref is an LLVM hex-double `0x…`, not a valid i64
		// operand) becomes a real i64 before the inttoptr (TDD-00123). This only
		// arises when a number is used where a string key is expected — a
		// type-confusion the compiler doesn't reject, kept consistent so add and
		// has/get agree.
		v = e.coerce(v, TypeI64)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, v.Ref))
		return r
	}
	// Number key. A `number` (float64) key bitcasts to i64 so the full double
	// bit-pattern is the opaque map key — `1.5` and `1` stay distinct and the
	// value round-trips exactly (TDD-00123). An explicit integer-typed key
	// (`int32`, …) sign/zero-extends to i64 as before.
	if keyTy.Float {
		v = e.coerce(v, TypeF64)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", r, v.Ref))
		return r
	}
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

	// Each entry is a real [K, V] tuple (TDD-00066) — field 0 is the key, field
	// 1 the value, the same struct layout the previous {key,value} object used,
	// now positionally destructurable as `for (const [k, v] of m.entries())`.
	entryTy := TupleType([]Type{keyTy, valTy})
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

// emitMapForEach implements map.forEach(fn): calls fn(value, key?, map?) for
// each entry, matching real JS's (value, key, map) callback order (ADR-00573).
// The 3rd `map` argument is the same map object being iterated.
func (e *Emitter) emitMapForEach(mapPtr string, strKey bool, keyTy, valTy, mapTy Type, cb Callback) (Value, error) {
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
	if cb.arity() >= 3 {
		cbArgs = append(cbArgs, Value{Ref: mapPtr, Ty: mapTy})
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
func (e *Emitter) emitSetForEach(setPtr string, strElem bool, elemTy, setTy Type, cb Callback) (Value, error) {
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
	if cb.arity() >= 3 {
		cbArgs = append(cbArgs, Value{Ref: setPtr, Ty: setTy})
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
// isNullableScalarMapValue reports whether a Map's value type is a non-pointer
// scalar, i.e. one whose `.get()` miss must be represented as a real absent
// value rather than a raw 0 (TDD-00064 bug #3). A pointer value type is
// excluded — its miss is already a distinguishable null pointer.
func isNullableScalarMapValue(valTy Type) bool {
	return valTy.IR != "ptr" && valTy.IR != "void" && !valTy.IsDynamic
}

// emitMapGetNullable returns a scalar Map value as a `V | null` aggregate: the
// presence bit comes from has(), the payload from the raw get(). See bug #3.
func (e *Emitter) emitMapGetNullable(mapPtr, kRef string, strKey bool, valTy Type) Value {
	present := e.freshReg()
	raw := e.freshReg()
	if strKey {
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", present, mapPtr, kRef))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, mapPtr, kRef))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_num_has(ptr %s, i64 %s)", present, mapPtr, kRef))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_num_get(ptr %s, i64 %s)", raw, mapPtr, kRef))
	}
	nty := valTy
	nty.Nullable = true
	payload := e.mapValFromI64(raw, valTy)
	agg := e.makeNullableScalarAgg(nty, present, payload.Ref)
	return Value{Ref: agg, Ty: nty}
}

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
