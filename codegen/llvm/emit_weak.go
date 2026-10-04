// emit_weak.go — WeakMap<K,V>/WeakSet<T>/WeakRef<T> construction and method
// dispatch (TDD-00112). All three key on object-pointer identity and are
// non-iterable; the runtime backing (strong under -mm=manual, real weak via
// Boehm disappearing links under -mm=gc) lives in runtime_weak.go behind one
// mode-independent set of `__kml_weak_*`/`__kml_weakref_*` symbols, so this
// codegen never branches on the memory mode itself.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitGlobalGC implements the `gc()` global (the Node `--expose-gc` idiom):
// under -mm=gc it forces a full Boehm collection (GC_gcollect), which makes
// weak-reference reclamation observable deterministically; under -mm=manual it
// is a no-op (nothing is ever collected). Returns void.
func (e *Emitter) emitGlobalGC(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: gc() takes no arguments", pos.Line, pos.Col)
	}
	if e.isGCMode() {
		if !e.usedGCGcollect {
			e.usedGCGcollect = true
			e.emitGlobal("declare void @GC_gcollect()")
		}
		e.emitInstr("call void @GC_gcollect()")
		// TDD-00163 Stage 3: Boehm queues finalizers at collection but runs
		// them lazily from later allocations; invoking them here makes
		// FinalizationRegistry firing observable right after a forced gc().
		if e.programUsesFinReg {
			e.ensureGCInvokeFinalizers()
			e.emitInstr(fmt.Sprintf("%s = call i32 @GC_invoke_finalizers()", e.freshReg()))
		}
	}
	return Value{Ty: TypeVoid}, nil
}

// emitNewWeakMapValue builds `new WeakMap<K,V>()`.
func (e *Emitter) emitNewWeakMapValue(init *ast.NewWeakMapExpression) (Value, error) {
	keyTy := TypePtr // default: object keys
	valTy := TypeI64 // default: number values
	if init.KeyType != nil {
		keyTy = e.resolveType(init.KeyType)
	}
	if init.ValType != nil {
		valTy = e.resolveType(init.ValType)
	}
	e.ensureWeakHelpers()
	ptr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_weak_create()", ptr))
	return Value{Ref: ptr, Ty: WeakMapType(keyTy, valTy)}, nil
}

// emitNewWeakSetValue builds `new WeakSet<T>()`.
func (e *Emitter) emitNewWeakSetValue(init *ast.NewWeakSetExpression) (Value, error) {
	elemTy := TypePtr // default: object elements
	if init.ElemType != nil {
		elemTy = e.resolveType(init.ElemType)
	}
	e.ensureWeakHelpers()
	ptr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_weak_create()", ptr))
	return Value{Ref: ptr, Ty: WeakSetType(elemTy)}, nil
}

// weakObjectKey evaluates a WeakMap/WeakSet key/element and enforces it is an
// object reference (a ptr that isn't a string) — a primitive key is meaningless
// for identity-keyed weak storage and a clean compile error.
func (e *Emitter) weakObjectKey(keyExpr ast.Expression, pos ast.Pos) (string, error) {
	return e.weakObjectKeyFor(keyExpr, pos, "Invalid value used as weak map key")
}

// weakObjectKeyFor is weakObjectKey with the TypeError a primitive run-time
// key throws (a WeakSet's differs from a WeakMap's).
func (e *Emitter) weakObjectKeyFor(keyExpr ast.Expression, pos ast.Pos, weakKeyError string) (string, error) {
	kVal, err := e.emitExpr(keyExpr)
	if err != nil {
		return "", err
	}
	if as, ok := keyExpr.(*ast.AsExpression); ok && !kVal.Ty.IsDynamic && as.TypeAnnot != nil && e.resolveType(as.TypeAnnot).IsDynamic {
		// `5 as any`: the static type is any, whatever its representation.
		if kVal, err = e.emitBoxValue(kVal); err != nil {
			return "", err
		}
	}
	// An any/unknown key holds an object at run time or not: a primitive
	// throws Node's TypeError, an object keys by its reference.
	if kVal.Ty.IsDynamic {
		tag, _ := e.emitUnboxTagPayload(kVal)
		// Tags above undefined are the object kinds.
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ugt i8 %s, %d", ok, tag, kmlTagUndefined))
		goodL, badL := e.freshLabel("weak.key.ok"), e.freshLabel("weak.key.bad")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, goodL, badL))
		e.emitLabel(badL)
		e.emitThrowTypeError(weakKeyError)
		e.emitLabel(goodL)
		_, payload := e.emitUnboxTagPayload(kVal)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, payload))
		return r, nil
	}
	if kVal.Ty.IR != "ptr" || isStringTy(kVal.Ty) {
		return "", fmt.Errorf("%d:%d: a WeakMap/WeakSet key must be an object (not a primitive)", pos.Line, pos.Col)
	}
	return kVal.Ref, nil
}

// emitWeakCall dispatches WeakMap (set/get/has/delete) and WeakSet
// (add/has/delete) methods. Iteration methods (keys/values/entries/forEach/
// size) are intentionally absent — weak collections are non-iterable per spec.
func (e *Emitter) emitWeakCall(ty Type, ptr, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	kind := "WeakMap"
	if ty.IsSet {
		kind = "WeakSet"
	}
	e.ensureWeakHelpers()

	switch method {
	case "set":
		if ty.IsSet {
			break
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: WeakMap.set() requires 2 arguments", pos.Line, pos.Col)
		}
		valTy := TypeI64
		if ty.MapVal != nil {
			valTy = *ty.MapVal
		}
		kRef, err := e.weakObjectKey(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		vVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		if vVal.Ty.IsDynamic && !valTy.IsDynamic {
			vVal = e.coerce(vVal, valTy)
		}
		vRef := e.valueToMapVal(vVal, valTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_weak_set(ptr %s, ptr %s, i64 %s)", ptr, kRef, vRef))
		return Value{Ref: ptr, Ty: ty}, nil

	case "add":
		if !ty.IsSet {
			break
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: WeakSet.add() requires 1 argument", pos.Line, pos.Col)
		}
		kRef, err := e.weakObjectKeyFor(args[0], pos, "Invalid value used in weak set")
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_weak_set(ptr %s, ptr %s, i64 0)", ptr, kRef))
		return Value{Ref: ptr, Ty: ty}, nil

	case "get":
		if ty.IsSet {
			break
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: WeakMap.get() requires 1 argument", pos.Line, pos.Col)
		}
		valTy := TypeI64
		if ty.MapVal != nil {
			valTy = *ty.MapVal
		}
		kRef, err := e.weakObjectKey(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		// A scalar value type returns `V | null` (a missing key is a real
		// absence, distinguished from a stored 0/false) — the same nullable-
		// scalar representation Map.get uses (TDD-00064 bug #3).
		if isNullableScalarMapValue(valTy) {
			present := e.freshReg()
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_weak_has(ptr %s, ptr %s)", present, ptr, kRef))
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_weak_get(ptr %s, ptr %s)", raw, ptr, kRef))
			nty := valTy
			nty.Nullable = true
			nty.IsUndefined = true // a miss is undefined, not null (ADR-00833)
			payload := e.mapValFromI64(raw, valTy)
			agg := e.makeNullableScalarAgg(nty, present, payload.Ref)
			return Value{Ref: agg, Ty: nty}, nil
		}
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_weak_get(ptr %s, ptr %s)", raw, ptr, kRef))
		return e.mapValFromI64(raw, valTy), nil

	case "has":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: %s.has() requires 1 argument", pos.Line, pos.Col, kind)
		}
		kRef, err := e.weakObjectKey(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_weak_has(ptr %s, ptr %s)", res, ptr, kRef))
		return Value{Ref: res, Ty: TypeBool}, nil

	case "delete":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: %s.delete() requires 1 argument", pos.Line, pos.Col, kind)
		}
		kRef, err := e.weakObjectKey(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_weak_delete(ptr %s, ptr %s)", res, ptr, kRef))
		return Value{Ref: res, Ty: TypeBool}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown %s method '%s'", pos.Line, pos.Col, kind, method)
}
