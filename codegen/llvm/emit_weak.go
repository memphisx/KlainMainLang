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

// emitNewWeakRefValue builds `new WeakRef(obj)` — a one-word box over the
// referent (registered as a disappearing link under -mm=gc).
func (e *Emitter) emitNewWeakRefValue(init *ast.NewWeakRefExpression) (Value, error) {
	obj, err := e.emitExpr(init.Init)
	if err != nil {
		return Value{}, err
	}
	// An any/unknown referent under -compat=js may hold an object at runtime;
	// unbox its payload pointer rather than rejecting statically (a primitive at
	// runtime is a -compat=js divergence, not invalid IR — strict still rejects
	// a statically-primitive referent below).
	if obj.Ty.IsDynamic && e.compatJS() {
		obj = e.coerce(obj, TypePtr)
	}
	if obj.Ty.IR != "ptr" || isStringTy(obj.Ty) {
		pos := init.GetPos()
		return Value{}, fmt.Errorf("%d:%d: new WeakRef requires an object referent (not a primitive)", pos.Line, pos.Col)
	}
	referentTy := obj.Ty
	if init.ElemType != nil {
		referentTy = e.resolveType(init.ElemType)
	}
	e.ensureWeakHelpers()
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_weakref_create(ptr %s)", box, obj.Ref))
	return Value{Ref: box, Ty: WeakRefType(referentTy)}, nil
}

// resolveWeakRefForCall loads a WeakRef receiver's box pointer — the same
// named-variable-vs-arbitrary-expression split resolveMapOrSetForCall uses.
func (e *Emitter) resolveWeakRefForCall(objExpr ast.Expression, pos ast.Pos) (string, error) {
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if !found || !sym.Ty.IsWeakRef {
			return "", fmt.Errorf("%d:%d: '%s' is not a WeakRef", pos.Line, pos.Col, id.Name)
		}
		ptr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))
		return ptr, nil
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return "", err
	}
	if !val.Ty.IsWeakRef {
		return "", fmt.Errorf("%d:%d: value is not a WeakRef", pos.Line, pos.Col)
	}
	return val.Ref, nil
}

// emitWeakRefCall dispatches WeakRef.deref().
func (e *Emitter) emitWeakRefCall(ty Type, box, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if method != "deref" {
		return Value{}, fmt.Errorf("%d:%d: unknown WeakRef method '%s' (only .deref() is supported)", pos.Line, pos.Col, method)
	}
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: WeakRef.deref() takes no arguments", pos.Line, pos.Col)
	}
	referentTy := TypePtr
	if ty.MapKey != nil {
		referentTy = *ty.MapKey
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_weakref_deref(ptr %s)", r, box))
	// A collected referent reads back as a null pointer — the compiler's
	// undefined stand-in for a reference type, same as a Map miss.
	return Value{Ref: r, Ty: referentTy}, nil
}

// weakObjectKey evaluates a WeakMap/WeakSet key/element and enforces it is an
// object reference (a ptr that isn't a string) — a primitive key is meaningless
// for identity-keyed weak storage and a clean compile error.
func (e *Emitter) weakObjectKey(keyExpr ast.Expression, pos ast.Pos) (string, error) {
	kVal, err := e.emitExpr(keyExpr)
	if err != nil {
		return "", err
	}
	// An any/unknown key under -compat=js may hold an object reference at
	// runtime; the object-key requirement can't be decided statically, so unbox
	// the NaN box's payload pointer and proceed. (A primitive value at runtime
	// is a -compat=js divergence, not invalid IR — strict still rejects a
	// statically-primitive key below.)
	if kVal.Ty.IsDynamic && e.compatJS() {
		return e.coerce(kVal, TypePtr).Ref, nil
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
		kRef, err := e.weakObjectKey(args[0], pos)
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
