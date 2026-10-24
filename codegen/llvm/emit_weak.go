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
		e.ensureGCInterface()
		e.emitInstr("call void @kml_gc_collect()")
		// TDD-00163 Stage 3: Boehm queues finalizers at collection but runs
		// them lazily from later allocations; invoking them here makes
		// FinalizationRegistry firing observable right after a forced gc().
		if e.programUsesFinReg {
			e.emitInstr(fmt.Sprintf("%s = call i32 @kml_gc_run_finalizers()", e.freshReg()))
		}
	}
	return Value{Ty: TypeVoid}, nil
}

// emitNewWeakMapValue builds `new WeakMap<K,V>()`.
func (e *Emitter) emitNewWeakMapValue(init *ast.NewWeakMapExpression) (Value, error) {
	return e.emitNewWeakMapValueAnnotated(init, nil)
}

// weakMapKV is a `new WeakMap()`'s key and value types: its type arguments,
// else a `WeakMap<K, V>` annotation's on its declaration (annot, may be
// nil), else object keys and number values.
func (e *Emitter) weakMapKV(init *ast.NewWeakMapExpression, annot *ast.TypeAnnotation) (Type, Type) {
	keyTy := TypePtr
	valTy := TypeI64
	if init.KeyType == nil && init.ValType == nil && annot != nil {
		if at := e.resolveType(annot); at.IsMap() && at.Weak && at.MapKey != nil && at.MapVal != nil {
			return *at.MapKey, *at.MapVal
		}
	}
	if init.KeyType != nil {
		keyTy = e.resolveType(init.KeyType)
	}
	if init.ValType != nil {
		valTy = e.resolveType(init.ValType)
	} else if init.KeyType == nil && init.Init != nil {
		// Inferred from the entries, as TypeScript infers K and V.
		if k, v, ok := e.literalPairTypes(init.Init); ok {
			keyTy, valTy = k, v
		} else if k, v, ok := iterablePairTypes(e.inferExprType(init.Init)); ok {
			keyTy, valTy = k, v
		}
	}
	return keyTy, valTy
}

// weakSetElem is a `new WeakSet()`'s element type: its type argument, else
// the values iterable's element, else an object.
func (e *Emitter) weakSetElem(init *ast.NewWeakSetExpression) Type {
	if init.ElemType != nil {
		return e.resolveType(init.ElemType)
	}
	if init.Init != nil {
		if t := e.inferExprType(init.Init); (t.IsArray() || t.IsSet()) && t.ElemType != nil && !t.ElemType.IsDynamic() {
			return *t.ElemType
		}
	}
	return TypePtr
}

// literalPairTypes is the key and value types of an array literal of
// `[key, value]` literals: the first pair's, a value type the pairs
// disagree on widening to any.
func (e *Emitter) literalPairTypes(x ast.Expression) (Type, Type, bool) {
	lit, ok := x.(*ast.ArrayLiteral)
	if !ok || len(lit.Elements) == 0 {
		return Type{}, Type{}, false
	}
	var k, v Type
	for i, el := range lit.Elements {
		pair, ok := el.(*ast.ArrayLiteral)
		if !ok || len(pair.Elements) != 2 {
			return Type{}, Type{}, false
		}
		pk, pv := e.inferExprType(pair.Elements[0]), e.inferExprType(pair.Elements[1])
		if i == 0 {
			k, v = pk, pv
			continue
		}
		if sameReprKey(pv) != sameReprKey(v) {
			v = TypeAny
		}
		if sameReprKey(pk) != sameReprKey(k) {
			k = TypeAny
		}
	}
	return k, v, true
}

// iterablePairTypes is the key and value types of an entries iterable: an
// array of [K, V] tuples, or a Map.
func iterablePairTypes(t Type) (Type, Type, bool) {
	switch {
	case t.IsMap() && t.MapKey != nil && t.MapVal != nil:
		return *t.MapKey, *t.MapVal, true
	case t.IsArray() && t.ElemType != nil && t.ElemType.IsTuple() && len(t.ElemType.Fields) == 2:
		return t.ElemType.Fields[0].Ty, t.ElemType.Fields[1].Ty, true
	}
	return Type{}, Type{}, false
}

// emitWeakSeed adds a `new WeakMap(entries)`/`new WeakSet(values)`'s
// initial contents: the loop the constructor runs, through set/add (a
// primitive key throws their TypeError). A null or undefined source adds
// nothing.
func (e *Emitter) emitWeakSeed(coll Value, init ast.Expression, isMap bool, pos ast.Pos) error {
	if init == nil {
		return nil
	}
	if nl, ok := init.(*ast.NullLiteral); (ok && !nl.Void) || isUndefinedLiteral(e, init) {
		return nil
	}
	e.weakSeedCtr++
	src := fmt.Sprintf("__kml_weak_src_%d", e.weakSeedCtr)
	item := fmt.Sprintf("__kml_weak_item_%d", e.weakSeedCtr)
	if err := e.emitStmt(ast.NewVarDeclaration("const", src, nil, init, pos)); err != nil {
		return err
	}
	target := e.bindValue(coll, pos)
	var call ast.Expression
	if isMap {
		call = ast.NewCallExpression(ast.NewMemberExpression(target, "set", pos), []ast.Expression{
			ast.NewIndexExpression(ast.NewIdentifier(item, pos), ast.NewNumberLiteral("0", pos), pos),
			ast.NewIndexExpression(ast.NewIdentifier(item, pos), ast.NewNumberLiteral("1", pos), pos),
		}, pos)
	} else {
		call = ast.NewCallExpression(ast.NewMemberExpression(target, "add", pos), []ast.Expression{ast.NewIdentifier(item, pos)}, pos)
	}
	loop := ast.NewForOfStatement("const", item, ast.NewIdentifier(src, pos), ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(call, pos)}, pos), pos)
	return e.emitStmt(loop)
}

// emitNewWeakMapValueAnnotated builds `new WeakMap<K, V>()`, typed by
// weakMapKV under its declaration's annotation.
func (e *Emitter) emitNewWeakMapValueAnnotated(init *ast.NewWeakMapExpression, annot *ast.TypeAnnotation) (Value, error) {
	keyTy, valTy := e.weakMapKV(init, annot)
	e.ensureWeakHelpers()
	ptr := e.freshReg()
	ty := WeakMapType(keyTy, valTy)
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_weak_create(i64 %d)", ptr, e.hostHeaderWord(ty)))
	v := Value{Ref: ptr, Ty: ty}
	return v, e.emitWeakSeed(v, init.Init, true, init.GetPos())
}

// emitNewWeakSetValue builds `new WeakSet<T>(values?)`.
func (e *Emitter) emitNewWeakSetValue(init *ast.NewWeakSetExpression) (Value, error) {
	elemTy := e.weakSetElem(init)
	e.ensureWeakHelpers()
	ptr := e.freshReg()
	ty := WeakSetType(elemTy)
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_weak_create(i64 %d)", ptr, e.hostHeaderWord(ty)))
	v := Value{Ref: ptr, Ty: ty}
	return v, e.emitWeakSeed(v, init.Init, false, init.GetPos())
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
	if as, ok := keyExpr.(*ast.AsExpression); ok && !kVal.Ty.IsDynamic() && as.TypeAnnot != nil && e.resolveType(as.TypeAnnot).IsDynamic() {
		// `5 as any`: the static type is any, whatever its representation.
		if kVal, err = e.emitBoxValue(kVal); err != nil {
			return "", err
		}
	}
	// An any/unknown key holds an object at run time or not: a primitive
	// throws Node's TypeError, an object keys by its reference.
	if kVal.Ty.IsDynamic() {
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

// weakUnknownMethod reports a method the weak collection lacks.
func weakUnknownMethod(kind, method string, pos ast.Pos) error {
	return fmt.Errorf("%d:%d: unknown %s method '%s'", pos.Line, pos.Col, kind, method)
}

// emitWeakCall dispatches WeakMap (set/get/has/delete) and WeakSet
// (add/has/delete) methods. Iteration methods (keys/values/entries/forEach/
// size) are intentionally absent — weak collections are non-iterable per spec.
func (e *Emitter) emitWeakCall(ty Type, ptr, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	kind := "WeakMap"
	if ty.IsSet() {
		kind = "WeakSet"
	}
	e.ensureWeakHelpers()

	c := &weakCall{ty: ty, ptr: ptr, method: method, args: args, pos: pos, kind: kind}
	if f, ok := weakMethods[method]; ok {
		return f(e, c)
	}
	return Value{}, weakUnknownMethod(kind, method, pos)
}

// weakCall is one WeakMap or WeakSet method call.
type weakCall struct {
	ty     Type
	ptr    string
	method string
	args   []ast.Expression
	pos    ast.Pos
	kind   string
}

// weakMethods are the WeakMap and WeakSet methods' emitters, by name.
var weakMethods map[string]func(e *Emitter, c *weakCall) (Value, error)

func init() {
	weakMethods = map[string]func(e *Emitter, c *weakCall) (Value, error){
		"set":    (*Emitter).emitWeakSet,
		"add":    (*Emitter).emitWeakAdd,
		"get":    (*Emitter).emitWeakGet,
		"has":    (*Emitter).emitWeakHas,
		"delete": (*Emitter).emitWeakDelete,
	}
}

// emitWeakSet is a WeakMap's or WeakSet's set().
func (e *Emitter) emitWeakSet(c *weakCall) (Value, error) {
	if c.ty.IsSet() {
		return Value{}, weakUnknownMethod(c.kind, c.method, c.pos)
	}
	if len(c.args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: WeakMap.set() requires 2 arguments", c.pos.Line, c.pos.Col)
	}
	valTy := TypeI64
	if c.ty.MapVal != nil {
		valTy = *c.ty.MapVal
	}
	kRef, err := e.weakObjectKey(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	vVal, err := e.emitExpr(c.args[1])
	if err != nil {
		return Value{}, err
	}
	if vVal.Ty.IsDynamic() && !valTy.IsDynamic() {
		vVal = e.convert(vVal, valTy)
	}
	vRef := e.valueToMapVal(vVal, valTy)
	e.emitInstr(fmt.Sprintf("call void @__kml_weak_set(ptr %s, ptr %s, i64 %s)", c.ptr, kRef, vRef))
	return Value{Ref: c.ptr, Ty: c.ty}, nil
}

// emitWeakAdd is a WeakMap's or WeakSet's add().
func (e *Emitter) emitWeakAdd(c *weakCall) (Value, error) {
	if !c.ty.IsSet() {
		return Value{}, weakUnknownMethod(c.kind, c.method, c.pos)
	}
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: WeakSet.add() requires 1 argument", c.pos.Line, c.pos.Col)
	}
	kRef, err := e.weakObjectKeyFor(c.args[0], c.pos, "Invalid value used in weak set")
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_weak_set(ptr %s, ptr %s, i64 0)", c.ptr, kRef))
	return Value{Ref: c.ptr, Ty: c.ty}, nil
}

// emitWeakGet is a WeakMap's or WeakSet's get().
func (e *Emitter) emitWeakGet(c *weakCall) (Value, error) {
	if c.ty.IsSet() {
		return Value{}, weakUnknownMethod(c.kind, c.method, c.pos)
	}
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: WeakMap.get() requires 1 argument", c.pos.Line, c.pos.Col)
	}
	valTy := TypeI64
	if c.ty.MapVal != nil {
		valTy = *c.ty.MapVal
	}
	kRef, err := e.weakObjectKey(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	// A scalar value type returns `V | null` (a missing key is a real
	// absence, distinguished from a stored 0/false) — the same nullable-
	// scalar representation Map.get uses (TDD-00064 bug #3).
	if isNullableScalarMapValue(valTy) {
		present := e.freshReg()
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_weak_has(ptr %s, ptr %s)", present, c.ptr, kRef))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_weak_get(ptr %s, ptr %s)", raw, c.ptr, kRef))
		nty := valTy
		nty.setNullable(true)
		nty.setUndefined(true) // a miss is undefined, not null (ADR-00833)
		payload := e.mapValFromI64(raw, valTy)
		agg := e.makeNullableScalarAgg(nty, present, payload.Ref)
		return Value{Ref: agg, Ty: nty}, nil
	}
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_weak_get(ptr %s, ptr %s)", raw, c.ptr, kRef))
	return e.mapValFromI64(raw, valTy), nil
}

// emitWeakHas is a WeakMap's or WeakSet's has().
func (e *Emitter) emitWeakHas(c *weakCall) (Value, error) {
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s.has() requires 1 argument", c.pos.Line, c.pos.Col, c.kind)
	}
	kRef, err := e.weakObjectKey(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_weak_has(ptr %s, ptr %s)", res, c.ptr, kRef))
	return Value{Ref: res, Ty: TypeBool}, nil
}

// emitWeakDelete is a WeakMap's or WeakSet's delete().
func (e *Emitter) emitWeakDelete(c *weakCall) (Value, error) {
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s.delete() requires 1 argument", c.pos.Line, c.pos.Col, c.kind)
	}
	kRef, err := e.weakObjectKey(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_weak_delete(ptr %s, ptr %s)", res, c.ptr, kRef))
	return Value{Ref: res, Ty: TypeBool}, nil
}
