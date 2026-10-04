package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// What a Map or Set iteration yields per entry.
type mapIterKind int

const (
	mapIterKeys mapIterKind = iota
	mapIterValues
	mapIterEntries
)

// liveMapIterable recognizes a for-of over a Map or Set — the collection
// itself, or its keys(), values() or entries() — and returns the collection
// expression, its type and what each step yields. A Map iterates its entries
// and a Set its values, as their [Symbol.iterator] does.
func (e *Emitter) liveMapIterable(expr ast.Expression) (coll ast.Expression, ty Type, kind mapIterKind, ok bool) {
	if call, isCall := expr.(*ast.CallExpression); isCall && len(call.Args) == 0 {
		if me, isMember := call.Callee.(*ast.MemberExpression); isMember {
			if t := e.inferExprType(me.Object); mapIterable(t) {
				if k, isIter := collIterMethod(me.Property); isIter {
					return me.Object, t, k, true
				}
			}
		}
	}
	t := e.inferExprType(expr)
	switch {
	case t.IsCollIter:
		return expr, t, t.IterKind, true
	case mapIterable(t) && t.IsSet:
		return expr, t, mapIterValues, true
	case mapIterable(t):
		return expr, t, mapIterEntries, true
	}
	return nil, Type{}, 0, false
}

// mapIterable reports whether t is a Map or Set with the ordered, iterable
// runtime (not a WeakMap or dictionary).
func mapIterable(t Type) bool {
	return (t.IsMap || t.IsSet) && !t.Weak && !t.IsDynamicObject && !t.IsDynamic
}

// collIterMethod maps an iterator-returning method name to what it yields.
func collIterMethod(name string) (mapIterKind, bool) {
	switch name {
	case "keys":
		return mapIterKeys, true
	case "values":
		return mapIterValues, true
	case "entries":
		return mapIterEntries, true
	}
	return 0, false
}

// collIterElemType is what one step of an iterator of type t yields.
func collIterElemType(t Type) Type {
	if src := *t.IterSrc; src.IsArray {
		switch t.IterKind {
		case mapIterKeys:
			return TypeI64
		case mapIterValues:
			return *src.ElemType
		}
		return TupleType([]Type{TypeI64, *src.ElemType})
	}
	keyTy, valTy := mapIterTypes(*t.IterSrc)
	switch t.IterKind {
	case mapIterKeys:
		return keyTy
	case mapIterValues:
		return valTy
	}
	return TupleType([]Type{keyTy, valTy})
}

// collIterResultType is next()'s {value, done}. value is undefined once the
// iterator is done, so a scalar value is `T | undefined`.
func collIterResultType(t Type) Type {
	elem := collIterElemType(t)
	if isNullableScalarMapValue(elem) {
		elem.Nullable = true
		elem.IsUndefined = true
	}
	return genNextResultType(elem)
}

// emitMapIterOpen is a Map or Set iterator: a node on the map's list of open
// iterators, whose position the map's removals adjust.
func (e *Emitter) emitMapIterOpen(ty Type, mapPtr string, kind mapIterKind) Value {
	e.ensureMapIters()
	node := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_iter_open(ptr %s)", node, mapPtr))
	return Value{Ref: node, Ty: CollIterType(ty, kind)}
}

// emitCollIterElemRaw is the element at pos as a field holds it (an array
// element is its header pointer).
func (e *Emitter) emitCollIterElemRaw(mapPtr, pos string, srcTy Type, kind mapIterKind) Value {
	if srcTy.IsArray {
		return e.emitArrayIterElemRaw(mapPtr, pos, srcTy, kind)
	}
	keyTy, valTy := mapIterTypes(srcTy)
	switch {
	case kind == mapIterKeys || kind == mapIterValues && srcTy.IsSet:
		return e.emitMapKeyRaw(mapPtr, pos, keyTy)
	case kind == mapIterValues && valTy.IsArray:
		return e.emitMapSlotHeader(mapPtr, 24, pos, valTy)
	case kind == mapIterValues:
		return e.emitMapValAt(mapPtr, pos, valTy)
	}
	return e.emitMapEntryAt(mapPtr, pos, keyTy, valTy, srcTy.IsSet)
}

// emitCollIterStep advances the iterator node one entry. It returns whether
// an entry was there and, if so, its raw element; at the end it closes the
// node, so the iterator stays done even if the map grows again. The element
// register is only defined on the "more" path: the caller branches on more
// and reads elem only there (through the phi-free slot it returns).
func (e *Emitter) emitCollIterStep(node string, srcTy Type, kind mapIterKind, elemTy Type) (more string, elemSlot string) {
	elemSlot = e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", elemSlot, StructFieldIR(elemTy)))
	moreSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", moreSlot))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", moreSlot))
	mp, mapPtr, isNull := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", mp, node))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, mp))
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, mapPtr))
	chkL, hasL, endL, doneL := e.freshLabel("iter.chk"), e.freshLabel("iter.has"), e.freshLabel("iter.end"), e.freshLabel("iter.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, chkL))
	e.emitLabel(chkL)
	pp, pos, size, lt := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", pp, node))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", pos, pp))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", size, e.collIterSizeSlot(mapPtr, srcTy)))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", lt, pos, size))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", lt, hasL, endL))
	e.emitLabel(hasL)
	raw := e.emitCollIterElemRaw(mapPtr, pos, srcTy, kind)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", StructFieldIR(elemTy), raw.Ref, elemSlot))
	nx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, pos))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, pp))
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", moreSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(endL)
	e.emitCollIterClose(node, srcTy)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	more = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", more, moreSlot))
	return more, elemSlot
}

// emitCollIterNext is it.next(): {value, done}, value undefined when done.
func (e *Emitter) emitCollIterNext(it Value) Value {
	elemTy := collIterElemType(it.Ty)
	resTy := collIterResultType(it.Ty)
	more, slot := e.emitCollIterStep(it.Ref, *it.Ty.IterSrc, it.Ty.IterKind, elemTy)
	res := e.freshReg()
	e.emitObjMallocInto(res, resTy)
	vIdx, vTy, _ := resTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resTy.StructIR(), res, vIdx))
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", raw, StructFieldIR(elemTy), slot))
	switch {
	case isNullableScalar(vTy):
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", StructFieldIR(vTy), e.makeNullableScalarAgg(vTy, more, raw), vGep))
	default:
		// A pointer value (string, object, array header) is null when done,
		// a boxed one the undefined word, any other its zero.
		ir, absent := StructFieldIR(elemTy), "null"
		switch {
		case elemTy.IsDynamic:
			absent = fmt.Sprintf("%d", nbUndefined)
		case ir != "ptr":
			absent = zeroRef(elemTy)
		}
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", sel, more, ir, raw, ir, absent))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, sel, vGep))
	}
	dIdx, _, _ := resTy.FieldIndex("done")
	dGep, done := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resTy.StructIR(), res, dIdx))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", done, more))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", done, dGep))
	return Value{Ref: res, Ty: resTy}
}

// emitCollIterDrain runs the iterator to its end, collecting the rest of its
// elements into an array (spread, Array.from, toArray()).
func (e *Emitter) emitCollIterDrain(it Value) Value {
	elemTy := collIterElemType(it.Ty)
	e.ensureRealloc()
	buf, n, capS := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", buf))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", n))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", capS))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", buf))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", n))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", capS))
	loopL, growL, putL, endL := e.freshLabel("drain.loop"), e.freshLabel("drain.grow"), e.freshLabel("drain.put"), e.freshLabel("drain.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(loopL)
	more, slot := e.emitCollIterStep(it.Ref, *it.Ty.IterSrc, it.Ty.IterKind, elemTy)
	bodyL := e.freshLabel("drain.body")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
	e.emitLabel(bodyL)
	cur, cp, full := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, n))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cp, capS))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", full, cur, cp))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", full, growL, putL))
	e.emitLabel(growL)
	dbl, nc, bytes, ob, nb := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 2", dbl, cp))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, cp))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 8, i64 %s", nc, isZero, dbl))
	// 16 bytes a slot covers every element layout, a { i1, T } one included.
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 16", bytes, nc))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ob, buf))
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", nb, ob, bytes))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nb, buf))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nc, capS))
	e.emitTerminator(fmt.Sprintf("br label %%%s", putL))
	e.emitLabel(putL)
	b, i, raw := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", b, buf))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, n))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", raw, StructFieldIR(elemTy), slot))
	e.storeArrayElement(b, i, raw, elemTy)
	i1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i1, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i1, n))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(endL)
	fb, fl, r0, r1 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fb, buf))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fl, n))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, fb))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, fl))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}
}

// mapIterTypes is a Map's (or Set's) key and value types; a Set's value is
// its element, which it stores as the key.
func mapIterTypes(t Type) (keyTy, valTy Type) {
	keyTy = TypePtr
	if t.MapKey != nil {
		keyTy = *t.MapKey
	}
	if t.IsSet {
		return keyTy, keyTy
	}
	valTy = TypeI64
	if t.MapVal != nil {
		valTy = *t.MapVal
	}
	return keyTy, valTy
}

// emitMapKeyRaw loads entry pos's key from the map's dense key array as a
// field holds it: an array key is its header pointer.
func (e *Emitter) emitMapKeyRaw(mapPtr, pos string, keyTy Type) Value {
	kp, ka, gep := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", kp, mapPtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ka, kp))
	if isReferenceKeyTy(keyTy) || keyTy.IsBigInt {
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", gep, ka, pos))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", box, gep))
		return Value{Ref: e.mapKeyFromSlot(box, keyTy), Ty: keyTy}
	}
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, keyTy.IR, ka, pos))
	return e.loadArrayElem(gep, keyTy)
}

// emitMapValAt loads entry pos's value from the map's dense value array.
func (e *Emitter) emitMapValAt(mapPtr, pos string, valTy Type) Value {
	vp, va, gep := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 24", vp, mapPtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", va, vp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, valTy.IR, va, pos))
	return e.loadArrayElem(gep, valTy)
}

// emitMapEntryAt builds entry pos as a fresh [key, value] tuple, the layout
// emitMapEntries gives each entry. A Set's entry is [value, value].
func (e *Emitter) emitMapEntryAt(mapPtr, pos string, keyTy, valTy Type, isSet bool) Value {
	k := e.emitMapKeyRaw(mapPtr, pos, keyTy)
	v := k
	if !isSet {
		v = e.emitMapValAt(mapPtr, pos, valTy)
		if valTy.IsArray {
			// The tuple field holds the value's own header, as the map slot
			// does, so the entry aliases the stored array.
			v = e.emitMapSlotHeader(mapPtr, 24, pos, valTy)
		}
	}
	entryTy := TupleType([]Type{keyTy, valTy})
	e.ensureMalloc()
	entry := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entry, entryTy.StructSize()))
	for i, f := range []Value{k, v} {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, entryTy.StructIR(), entry, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(f.Ty), f.Ref, slot, f.Ty.Align()))
	}
	return Value{Ref: entry, Ty: entryTy}
}

// emitForOfMapLive walks a Map or Set live, as its iterator does in
// JavaScript: an entry added during the loop is visited, a deleted one is
// not, and clear() ends the walk. The loop steps an iterator node on the
// map's list (ensureMapIters), whose position the map's removals adjust; a
// step advances the position before the body runs, so deleting the current
// entry does not skip the next. A loop over the collection or a fresh
// keys()/values()/entries() owns its node and closes it however the loop is
// left; a loop over an iterator value leaves it open on break, as a Map
// iterator has no return().
func (e *Emitter) emitForOfMapLive(s *ast.ForOfStatement, coll ast.Expression, collTy Type, kind mapIterKind, condL, bodyL, incL, endL string) error {
	collVal, err := e.emitExpr(coll)
	if err != nil {
		return err
	}
	it := collVal
	if !collTy.IsCollIter {
		it = e.emitMapIterOpen(collTy, collVal.Ref, kind)
		closeNode := func() {
			e.emitInstr(fmt.Sprintf("call void @__kml_map_iter_close(ptr %s)", it.Ref))
		}
		e.pendingFinallys = append(e.pendingFinallys, pendingExit{native: closeNode})
		defer func() { e.pendingFinallys = e.pendingFinallys[:len(e.pendingFinallys)-1] }()
		defer closeNode()
	}
	elemTy := collIterElemType(it.Ty)
	varPtr, bindTy := e.defineForOfVar(s, elemTy, Type{})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	e.emitSafepoint()
	more, slot := e.emitCollIterStep(it.Ref, *it.Ty.IterSrc, it.Ty.IterKind, elemTy)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))

	e.emitLabel(bodyL)
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", raw, StructFieldIR(elemTy), slot))
	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	switch {
	case elemTy.IsArray && !isPattern:
		// The loop variable aliases the stored array: its slot takes the
		// header the map holds.
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", raw, varPtr))
	default:
		elem := Value{Ref: raw, Ty: elemTy}
		if elemTy.IsArray {
			elem = e.unboxArrayValue(raw, elemTy)
		}
		if err := e.bindForOfElem(s, elem, elemTy, bindTy, varPtr, Type{}); err != nil {
			return err
		}
	}
	if err := e.emitForOfBody(s); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))
	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return nil
}

// emitMapSlotHeader is the array header pointer an array-typed map slot holds
// (field at off: 16 the keys, 24 the values).
func (e *Emitter) emitMapSlotHeader(mapPtr string, off int, pos string, ty Type) Value {
	fp, fa, gep, h := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", fp, mapPtr, off))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fa, fp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", gep, fa, pos))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, gep))
	return Value{Ref: h, Ty: ty}
}

// emitCollIterCall is a method call on an Array, Map or Set iterator: next(),
// toArray() (ES2025's iterator helper) and [Symbol.iterator]'s self.
func (e *Emitter) emitCollIterCall(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, helper := iteratorHelperArity[mem.Property]; helper {
		// An Iterator.prototype helper reached without its declaration (a
		// host adapter's synthesized call): its TypeScript function, the
		// receiver first.
		if fn, ok := e.globalLinks["__kml_Iterator_"+mem.Property]; ok {
			return e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(fn, pos), append([]ast.Expression{mem.Object}, args...), pos))
		}
	}
	it, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	switch mem.Property {
	case "next":
		// A Map iterator's next ignores its argument; evaluate it for effects.
		for _, a := range args {
			if _, err := e.emitExpr(a); err != nil {
				return Value{}, err
			}
		}
		return e.emitCollIterNext(it), nil
	case "toArray":
		return e.emitCollIterDrain(it), nil
	}
	return Value{}, fmt.Errorf("%d:%d: %s has no method '%s'", pos.Line, pos.Col, collIterName(it.Ty), mem.Property)
}

// collIterName is the iterator's class as console.log names it.
func collIterName(t Type) string {
	src := *t.IterSrc
	switch {
	case t.IterRegExp:
		return "RegExp String Iterator"
	case src.IsSet:
		return "Set Iterator"
	case src.IsMap:
		return "Map Iterator"
	}
	return "Array Iterator"
}

// collIterInspectName is the iterator's tag in console.log: an entries
// iterator of a Map or Set shows as "Map Entries" or "Set Entries".
func collIterInspectName(t Type) string {
	src := *t.IterSrc
	if t.IterKind == mapIterEntries && (src.IsMap || src.IsSet) {
		if src.IsSet {
			return "Set Entries"
		}
		return "Map Entries"
	}
	return collIterName(t)
}

// emitInspectCollIter renders an iterator as Node does — `[Map Iterator] {
// 'a', 'b' }`, `[Map Iterator] {  }` once done — listing the entries it has
// yet to yield without consuming them.
func (e *Emitter) emitInspectCollIter(v Value, depth int) (Value, error) {
	name := collIterInspectName(v.Ty)
	if depth > e.effectiveInspectDepth() {
		return Value{Ref: e.internString("[Object]"), Ty: TypePtr}, nil
	}
	if v.Ty.IterSrc.IsArray {
		// An Array Iterator shows no entries.
		return Value{Ref: e.internString("Object [Array Iterator] {}"), Ty: TypePtr}, nil
	}
	srcTy, kind := *v.Ty.IterSrc, v.Ty.IterKind
	mp, mapPtr, isNull := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", mp, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, mp))
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, mapPtr))
	countSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countSlot))
	liveL, doneL := e.freshLabel("inspiter.live"), e.freshLabel("inspiter.count")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, liveL))
	e.emitLabel(liveL)
	pp, pos, size, rem := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", pp, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", pos, pp))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", size, mapPtr))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rem, size, pos))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", rem, countSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	count, spp, start := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", count, countSlot))
	// When the iterator is closed the count is 0 and render never runs.
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", spp, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", start, spp))
	elemTy := collIterElemType(v.Ty)
	render := func(idx string) (Value, error) {
		at := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", at, start, idx))
		raw := e.emitCollIterElemRaw(mapPtr, at, srcTy, kind)
		elem := Value{Ref: raw.Ref, Ty: elemTy}
		if elemTy.IsArray {
			elem = e.unboxArrayValue(raw.Ref, elemTy)
		}
		return e.emitInspectField(elem, depth+1)
	}
	open := Value{Ref: e.internString("[" + name + "] {"), Ty: TypePtr}
	body, err := e.emitInspectListBody(open, count, depth, render)
	if err != nil {
		return Value{}, err
	}
	empty, res := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, count))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", res, empty, e.internString("["+name+"] {  }"), body.Ref))
	return Value{Ref: res, Ty: TypePtr}, nil
}

// collIterSizeSlot is the address of the source's live length: a Map's size
// word, or an array header's length field.
func (e *Emitter) collIterSizeSlot(src string, srcTy Type) string {
	if !srcTy.IsArray {
		return src
	}
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", lp, src))
	return lp
}

// emitCollIterClose ends an iterator: a Map or Set one leaves its map's list,
// an array one drops its array.
func (e *Emitter) emitCollIterClose(node string, srcTy Type) {
	if !srcTy.IsArray {
		e.emitInstr(fmt.Sprintf("call void @__kml_map_iter_close(ptr %s)", node))
		return
	}
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", sp, node))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", sp))
}

// arrayIterable reports whether t is an ordinary array, whose keys(),
// values() and entries() give an Array Iterator over its header.
func arrayIterable(t Type) bool {
	return t.IsArray && t.ElemType != nil && !t.IsTypedArray && !t.IsFlatArray && !t.IsDynamic && !t.IsTuple
}

// emitArrayIterOpen is an Array Iterator: an unregistered {next, pos,
// header} node over the array's own header, so it sees the array's later
// pushes and truncations, as a JavaScript array iterator does.
func (e *Emitter) emitArrayIterOpen(arr ast.Expression, kind mapIterKind) (Value, error) {
	v, err := e.emitExpr(arr)
	if err != nil {
		return Value{}, err
	}
	return e.emitArrayIterOpenValue(v, kind, false), nil
}

// emitArrayIterOpenValue is an iterator over the array v; regexp marks
// matchAll's RegExp String Iterator, which walks its steps the same way.
func (e *Emitter) emitArrayIterOpenValue(v Value, kind mapIterKind, regexp bool) Value {
	header := e.arrayReturnHeader(v)
	e.ensureMalloc()
	node := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", node))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", node))
	pp, sp := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", pp, node))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", pp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", sp, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, sp))
	ty := CollIterType(v.Ty, kind)
	ty.IterRegExp = regexp
	return Value{Ref: node, Ty: ty}
}

// emitArrayIterElemRaw is an Array Iterator's element at pos, as a field
// holds it.
func (e *Emitter) emitArrayIterElemRaw(header, pos string, srcTy Type, kind mapIterKind) Value {
	if kind == mapIterKeys {
		return Value{Ref: pos, Ty: TypeI64}
	}
	elemTy := *srcTy.ElemType
	data, gep := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, header))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, data, pos))
	var elem Value
	if elemTy.IsArray {
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, gep))
		elem = Value{Ref: h, Ty: elemTy}
	} else {
		elem = e.loadArrayElem(gep, elemTy)
	}
	if kind == mapIterValues {
		return elem
	}
	entryTy := TupleType([]Type{TypeI64, elemTy})
	e.ensureMalloc()
	entry := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entry, entryTy.StructSize()))
	for i, f := range []Value{{Ref: pos, Ty: TypeI64}, elem} {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, entryTy.StructIR(), entry, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(f.Ty), f.Ref, slot, f.Ty.Align()))
	}
	return Value{Ref: entry, Ty: entryTy}
}
