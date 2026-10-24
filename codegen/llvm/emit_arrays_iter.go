package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayKeys(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: keys takes no arguments", pos.Line, pos.Col)
	}
	if arrayIterable(e.inferExprType(mem.Object)) {
		return e.emitArrayIterOpen(mem.Object, mapIterKeys)
	}
	_, lenReg, _, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureMalloc()
	byteCount := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCount, lenReg))
	e.heapMallocInto(newPtr, byteCount)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("keys.cond")
	bodyL := e.freshLabel("keys.body")
	doneL := e.freshLabel("keys.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", slot, newPtr, idxVal))
	e.heapStore("i64", idxVal, slot, 8)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r1 := e.newArrayHeader(newPtr, lenReg)
	return Value{Ref: r1, Ty: ArrayOf(TypeI64)}, nil
}

// emitArrayValues implements arr.values(): returns a fresh copy of arr's
// elements — same materialized-array convention as emitArrayKeys.
func (e *Emitter) emitArrayValues(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: values takes no arguments", pos.Line, pos.Col)
	}
	if arrayIterable(e.inferExprType(mem.Object)) {
		return e.emitArrayIterOpen(mem.Object, mapIterValues)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	newPtr := e.emitArrayCopy(ptrReg, lenReg, elemTy)
	r1 := e.newArrayHeader(newPtr, lenReg)
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayEntries implements arr.entries() → {index: number, value: T}[] —
// this compiler has no tuple type, so a real JS [index, value] pair isn't
// representable; the same heap-allocated-entry-object convention
// Map.entries()/Object.entries() already use. Iterate with
// `for (const e of arr.entries())` then read `e.index`/`e.value`.
func (e *Emitter) emitArrayEntries(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: entries takes no arguments", pos.Line, pos.Col)
	}
	if arrayIterable(e.inferExprType(mem.Object)) {
		return e.emitArrayIterOpen(mem.Object, mapIterEntries)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	// Each entry is a real [number, T] tuple (TDD-00066) — field 0 the index,
	// field 1 the value — positionally destructurable as
	// `for (const [i, v] of arr.entries())`.
	entryTy := TupleType([]Type{TypeI64, elemTy})
	entrySize := entryTy.StructSize()

	e.ensureMalloc()
	outBytes := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", outBytes, lenReg))
	e.heapMallocInto(outPtr, outBytes)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("entries.cond")
	bodyL := e.freshLabel("entries.body")
	doneL := e.freshLabel("entries.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	elemGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", elemGep, elemTy.IR, ptrReg, idxVal))
	elemVal := e.loadArrayElem(elemGep, elemTy)

	entryReg := e.freshReg()
	e.heapMallocInto(entryReg, fmt.Sprint(entrySize))
	idxSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", idxSlot, entryTy.StructIR(), entryReg))
	e.heapStore("i64", idxVal, idxSlot, 8)
	valSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
	// The entry object's "value" field is a struct field. An array-typed value
	// field holds a header pointer (TDD-00213 Stage 2), so store the unboxed
	// array's header.
	if elemTy.IsArray() {
		e.storeArrayFieldHeader(valSlot, elemVal)
	} else {
		e.heapStore(StructFieldIR(elemTy), elemVal.Ref, valSlot, elemTy.Align())
	}

	slotReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotReg, outPtr, idxVal))
	e.heapStore("ptr", entryReg, slotReg, 8)

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r1 := e.newArrayHeader(outPtr, lenReg)
	return Value{Ref: r1, Ty: ArrayOf(entryTy)}, nil
}

// emitArrayOf implements Array.of(...items): builds a fresh array directly
// from the given argument expressions — unlike an array literal `[...]`
// (which currently can only appear in variable-declaration position),
// Array.of is a plain call expression usable anywhere. Element type is
// inferred from the first argument, mirroring inferArrayType's own
// first-element rule for `[...]` literals; an empty call defaults to
// number[], same as an empty literal.
func (e *Emitter) emitArrayOf(args []ast.Expression, pos ast.Pos) (Value, error) {
	// `Array.of(a, b, …)` (and Array.of used as a value, which forwards its
	// rest parameter) is the array literal `[a, b, …]`; with no arguments,
	// an empty `unknown[]`, held boxed.
	if len(args) > 0 {
		return e.emitExpr(ast.NewArrayLiteral(args, pos))
	}
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, "8")
	r1 := e.newArrayHeader(dataReg, "0")
	return Value{Ref: r1, Ty: ArrayOf(TypeAny)}, nil
}

// emitStringToCharArray materializes a string Value into a `string[]` whose
// elements are its code points, as a string's iterator yields them (each a
// length-prefixed string of that code point's UTF-8 bytes, via
// emitStringExtract). Shared by Array.from(string), `[...str]` and
// `for (const ch of str)` (ADR-00535, ADR-01188).
func (e *Emitter) emitStringToCharArray(v Value) Value {
	e.ensureStrlen()
	e.ensureMalloc()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, v.Ref))
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, sLen))
	buf := e.freshReg()
	e.heapMallocInto(buf, bytes)
	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))
	outPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", outPtr))
	condL := e.freshLabel("strfrom.cond")
	bodyL := e.freshLabel("strfrom.body")
	endL := e.freshLabel("strfrom.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i1r, c := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i1r, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", c, i1r, sLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, bodyL, endL))
	e.emitLabel(bodyL)
	i2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i2, idxPtr))
	n := e.emitUTF8SeqLen(v.Ref, i2, sLen)
	ch := e.emitStringExtract(v.Ref, i2, n)
	j := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", j, outPtr))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slot, buf, j))
	e.heapStore("ptr", ch.Ref, slot, 8)
	j2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", j2, j))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", j2, outPtr))
	i3 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", i3, i2, n))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i3, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", count, outPtr))
	r1 := e.newArrayHeader(buf, count)
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}
}

// emitUTF8SeqLen is the byte length of the UTF-8 sequence starting at byte
// i of s (length sLen): 1 for ASCII, 2–4 from the lead byte, 1 for a stray
// continuation byte, never past the end — one code point, the unit a
// string's iterator yields.
func (e *Emitter) emitUTF8SeqLen(s, i, sLen string) string {
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", p, s, i))
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", b, p))
	b32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", b32, b))
	m2, m3, m4 := e.freshReg(), e.freshReg(), e.freshReg()
	t2, t3, t4 := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, 224", m2, b32))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 192", t2, m2))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, 240", m3, b32))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 224", t3, m3))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, 248", m4, b32))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 240", t4, m4))
	s4, s3, s2 := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 4, i64 1", s4, t4))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 3, i64 %s", s3, t3, s4))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 2, i64 %s", s2, t2, s3))
	rem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rem, sLen, i))
	over := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", over, s2, rem))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", n, over, rem, s2))
	return n
}

// emitArrayFrom implements Array.from(iterable): the array-like overload
// only (a plain array or an array-like) — real JS's
// second mapFn/thisArg arguments and iterating a string/Map/Set are not
// built here, see docs/status/ARRAY-METHODS.md.
//
// A plain array is a straight copy into a freshly malloc'd buffer of the
// same, already-known length (mirroring `.slice()`'s own no-arg shape).
func (e *Emitter) emitArrayFrom(args []ast.Expression, pos ast.Pos) (Value, error) {
	// Array.from(iterable, mapFn) desugars to Array.from(iterable).map(mapFn)
	// (ADR-00491) — semantically exact here, since the one observable
	// difference in real JS (mapFn seeing the source's holes/live length)
	// can't arise for this compiler's dense, materialized arrays. A thisArg
	// is mapFn's receiver: `.map((v, i) => mapFn.call(thisArg, v, i))`, each
	// argument evaluated once, in order.
	if len(args) > 0 {
		if t := e.inferExprType(args[0]); t.IsTuple() && !t.IsDynamic() {
			// A tuple is an array of its elements (tupleAsArray).
			args = append([]ast.Expression{ast.NewAsExpression(args[0], ast.AnyArrayAnnotation(""), pos)}, args[1:]...)
		}
	}
	if len(args) == 3 {
		if u, ok := args[2].(*ast.Identifier); ok && u.Name == "undefined" && e.isGlobalName(u) {
			return e.emitArrayFrom(args[:2], pos)
		}
		var temps []ast.Expression
		for _, a := range args {
			t, err := e.receiverTemp(a)
			if err != nil {
				return Value{}, err
			}
			temps = append(temps, t)
		}
		call := ast.NewCallExpression(ast.NewMemberExpression(temps[1], "call", pos),
			[]ast.Expression{temps[2], ast.NewIdentifier("__kml_fv", pos), ast.NewIdentifier("__kml_fi", pos)}, pos)
		mapFn := ast.NewArrowFunction([]ast.Param{{Name: "__kml_fv"}, {Name: "__kml_fi"}}, nil, call, nil, pos)
		return e.emitArrayFromMaybeMapped(temps[0], temps[1], ast.Expression(mapFn), pos)
	}
	if len(args) == 2 && e.inferExprType(args[1]).IsDynamic() {
		// An `any` mapFn may be undefined: then there is no mapping.
		temps := make([]ast.Expression, 2)
		for i, a := range args {
			t, err := e.receiverTemp(a)
			if err != nil {
				return Value{}, err
			}
			temps[i] = t
		}
		// An `any` mapper is called through its box.
		call := ast.NewCallExpression(temps[1], []ast.Expression{ast.NewIdentifier("__kml_fv", pos), ast.NewIdentifier("__kml_fi", pos)}, pos)
		mapper := ast.NewArrowFunction([]ast.Param{{Name: "__kml_fv"}, {Name: "__kml_fi"}}, nil, call, nil, pos)
		return e.emitArrayFromMaybeMapped(temps[0], temps[1], mapper, pos)
	}
	if len(args) == 2 {
		fromCall := arrayFromCall(pos, args[:1])
		mapCall := ast.NewCallExpression(
			ast.NewMemberExpression(fromCall, "map", pos),
			args[1:2], pos)
		return e.emitExpr(mapCall)
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Array.from takes 1 to 3 arguments (iterable, mapFn, thisArg)", pos.Line, pos.Col)
	}
	if hv := e.hostView(args[0]); hv != nil {
		// A class extending Map or Set: its host's default iterator.
		return e.emitArrayFrom([]ast.Expression{hv}, pos)
	}
	srcTy := e.inferExprType(args[0])

	if srcTy.IsArray() {
		e.iterUse = true // its iteration (array_iter_patch.go)
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureMalloc()
		e.ensureMemcpy()
		bytesReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, lenReg, elemTy.Align()))
		newPtr := e.freshReg()
		e.heapMallocInto(newPtr, bytesReg)
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, ptrReg, bytesReg))
		r1 := e.newArrayHeader(newPtr, lenReg)
		return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
	}

	// An Array, Map or Set iterator: the rest of its elements.
	if srcTy.IsCollIter() {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitCollIterDrain(v), nil
	}

	// A value held in `any`, or a static object or class instance with a
	// [Symbol.iterator] member: its elements at run time.
	if isUnconstrainedDynamic(srcTy) || e.hasIteratorMember(srcTy) || (srcTy.IsGenerator() && !srcTy.GeneratorIsAsync) {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		arr, err := e.emitAnyArrayFrom(v)
		if err != nil {
			return Value{}, err
		}
		// A generator's values have its declared element type.
		if srcTy.IsGenerator() && srcTy.GeneratorElemType != nil && !srcTy.GeneratorElemType.IsDynamic() {
			return e.emitUnboxAnyArray(arr, *srcTy.GeneratorElemType)
		}
		return arr, nil
	}

	// Map → entries ([K, V][] tuple array), Set → elements, string → its
	// characters (per byte, this compiler's string model) — ADR-00482.
	if srcTy.IsMap() && !srcTy.IsSet() {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitMapMethod(srcTy, v.Ref, "entries", nil, pos)
	}
	if srcTy.IsSet() {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.mapOrSetValuesArray(srcTy, v.Ref)
	}
	if isStringTy(srcTy) && !srcTy.IsClass() && !srcTy.IsObject() && srcTy.Host == 0 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitStringToCharArray(v), nil
	}

	// Array-like protocol (ADR-00957): `Array.from({ length: n })` reads the
	// object's `length`, coerces it to a non-negative integer, and produces an
	// array of that many `undefined` elements — the ubiquitous
	// `Array.from({ length: n }, (_, i) => …)` idiom (via the 2-arg desugar
	// above, whose map callback sees each `undefined` element and its index).
	// An object with other properties (`{ length: 2, 0: 'a' }`) is read
	// through its indexes at run time; a bare `{ length }` fills with the
	// boxed `undefined`, as a real hole read gives.
	if srcTy.IsObject() && !srcTy.IsArray() && !srcTy.IsClass() && !srcTy.IsTuple() {
		idx, lenFieldTy, ok := srcTy.FieldIndex("length")
		if !ok {
			// No `length`: ToLength(undefined) is 0, an empty array.
			if _, err := e.emitExpr(args[0]); err != nil {
				return Value{}, err
			}
			return e.emitEmptyAnyArray(), nil
		}
		// An array-like with properties besides `length` (`{ length: 2, 0:
		// 'a' }`): its indexed reads, at run time.
		if len(srcTy.Fields) > 1 || lenFieldTy.IsDynamic() {
			v, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			return e.emitAnyArrayFrom(v)
		}
		if lenFieldTy.IsArray() || lenFieldTy.IR == "ptr" {
			return Value{}, fmt.Errorf("%d:%d: Array.from array-like 'length' must be a number", pos.Line, pos.Col)
		}
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		lenGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", lenGep, srcTy.StructIR(), v.Ref, idx))
		lenRaw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", lenRaw, lenFieldTy.IR, lenGep, lenFieldTy.Align()))
		// Coerce the length to i64 (a float length truncates toward zero, per
		// ToIntegerOrInfinity), then clamp a negative length to 0.
		lenI64 := e.convert(Value{Ref: lenRaw, Ty: lenFieldTy}, TypeI64).Ref
		lenPos := e.freshReg()
		isNeg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, lenI64))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", lenPos, isNeg, lenI64))

		e.ensureMalloc()
		bytes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, lenPos, TypeAny.Align()))
		data := e.freshReg()
		e.heapMallocInto(data, bytes)

		iSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iSlot))
		condL := e.freshLabel("arrlike.cond")
		bodyL := e.freshLabel("arrlike.body")
		endL := e.freshLabel("arrlike.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		iCur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", iCur, iSlot))
		more := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, iCur, lenPos))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
		e.emitLabel(bodyL)
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", slot, data, iCur))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, slot))
		iNext := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", iNext, iCur))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", iNext, iSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(endL)
		r1 := e.newArrayHeader(data, lenPos)
		return Value{Ref: r1, Ty: ArrayOf(TypeAny)}, nil
	}

	if srcTy.IsClass() {
		if info, ok := e.classes[srcTy.ClassName]; ok && info.HostBase == nil && !e.classHasMethodInherited(srcTy.ClassName, syncIteratorMethodName) {
			if _, _, hasLen := srcTy.FieldIndex("length"); !hasLen {
				// Not iterable, so read as an array-like: with no `length`,
				// ToLength(undefined) is 0 (a `next()` does not iterate).
				if _, err := e.emitExpr(args[0]); err != nil {
					return Value{}, err
				}
				return e.emitEmptyAnyArray(), nil
			}
		}
	}

	// An ArrayBuffer is neither iterable nor has a `length`: ToLength
	// (undefined) is 0.
	if (srcTy.IsArrayBuffer() || srcTy.IsSharedArrayBuffer()) && !srcTy.Nullable() {
		if _, err := e.emitExpr(args[0]); err != nil {
			return Value{}, err
		}
		return e.emitEmptyAnyArray(), nil
	}

	return Value{}, fmt.Errorf("%d:%d: Array.from argument is not iterable", pos.Line, pos.Col)
}

// emitArrayCopyWithin implements arr.copyWithin(target, start?, end?):
// copies the sequence [start, end) to position target, in place, within the
// same backing buffer — arr's length never changes. Negative indices count
// from the end (same normalization as .at()/.slice()). Source and
// destination ranges can overlap (e.g. copyWithin(0, 2) on a 5-element
// array), so this uses memmove, not memcpy — the same overlap-safety
// already relied on by shift()/unshift()/splice()'s own tail shifts.
func (e *Emitter) emitArrayCopyWithin(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: copyWithin takes 1–3 arguments", pos.Line, pos.Col)
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitArrayGuardFor(mem.Object, arrOpReorder, nil)

	targetRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	targetIdx, err := e.arrayIndexToI64(targetRaw, args[0].GetPos())
	if err != nil {
		return Value{}, err
	}
	targetN := e.emitNormalizeSliceIdx(targetIdx.Ref, lenReg)

	startN := "0"
	if len(args) >= 2 {
		startRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		startIdx, err := e.arrayIndexToI64(startRaw, args[1].GetPos())
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(startIdx.Ref, lenReg)
	}

	endN := lenReg
	if len(args) == 3 {
		if endN, err = e.emitEndIndex(args[2], lenReg); err != nil {
			return Value{}, err
		}
	}

	// count = min(max(end - start, 0), len - target) — never reads past
	// [start, end) and never writes past the array's own end.
	rawCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawCount, endN, startN))
	negCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", negCount, rawCount))
	clampedCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", clampedCount, negCount, rawCount))
	avail := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", avail, lenReg, targetN))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, clampedCount, avail))
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", count, tooBig, avail, clampedCount))

	srcGep := e.freshReg()
	dstGep := e.freshReg()
	byteCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, elemTy.IR, ptrReg, startN))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstGep, elemTy.IR, ptrReg, targetN))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, count, elemTy.Align()))
	e.ensureMemmove()
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", dstGep, srcGep, byteCount))
	e.emitHolesCall("__kml_holes_move", e.hofHolesHeader(mem.Object, hdr), targetN, startN, count)

	r1 := e.receiverArrayHeader(hdr, ptrReg, lenReg)
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayFromMaybeMapped is Array.from(src) mapped by mapper, or unmapped
// when fn, an evaluated mapFn, is undefined, as Array.from treats a missing
// mapFn. A typed fn is never undefined.
func (e *Emitter) emitArrayFromMaybeMapped(src, fn, mapper ast.Expression, pos ast.Pos) (Value, error) {
	if !e.inferExprType(fn).IsDynamic() {
		return e.emitArrayFrom([]ast.Expression{src, mapper}, pos)
	}
	isUndef := ast.NewBinaryExpression("===", fn, ast.NewNullLiteral(true, pos), pos)
	plain := arrayFromCall(pos, []ast.Expression{src})
	mapped := ast.NewCallExpression(ast.NewMemberExpression(arrayFromCall(pos, []ast.Expression{src}), "map", pos), []ast.Expression{mapper}, pos)
	return e.emitExpr(ast.NewConditionalExpression(isUndef, plain, mapped, pos))
}
