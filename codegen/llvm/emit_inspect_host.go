package llvm

import "fmt"

// emit_inspect_host.go — util.inspect's rendering of the host classes that
// are not plain object layouts: Blob, Headers, ArrayBuffer, TextEncoder,
// TextDecoder and URLSearchParams, as Node prints them. ok is false for any
// other type.
func (e *Emitter) emitInspectHost(v Value, depth int) (s Value, ok bool, err error) {
	t := v.Ty
	if !isInspectHost(t) {
		return Value{}, false, nil
	}
	if t.Nullable || t.IsNull {
		// An absent one prints its keyword.
		present := v
		present.Ty.Nullable, present.Ty.IsNull, present.Ty.IsUndefined = false, false, false
		s, err := e.emitInspectNullablePtr(v, func(b Value) (Value, error) {
			r, _, err := e.emitInspectHost(Value{Ref: b.Ref, Ty: present.Ty}, depth)
			return r, err
		})
		return s, true, err
	}
	switch {
	case t.IsURLSearchParams:
		e.ensureURLSearchParams()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_inspect(ptr %s)", r, v.Ref))
		return Value{Ref: r, Ty: TypePtr}, true, nil
	case t.IsHeaders:
		s, err := e.emitInspectHeaders(v, depth)
		return s, true, err
	case t.IsBlob:
		s, err := e.emitInspectBlob(v, depth)
		return s, true, err
	case t.IsArrayBuffer:
		s, err := e.emitInspectArrayBuffer(v, depth)
		return s, true, err
	case t.IsDataView:
		s, err := e.emitInspectDataView(v, depth)
		return s, true, err
	case t.IsTextEncoder:
		// Node's TextEncoder has no class-named inspect: its custom inspect
		// hands back a plain object.
		return Value{Ref: e.internString("{ encoding: 'utf-8' }"), Ty: TypePtr}, true, nil
	case t.IsPromise:
		// Node's `Promise { value }` form, through the boxed promise.
		b, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, true, err
		}
		s, err := e.emitDynamicInspectAt(b, depth)
		return s, true, err
	case t.IsTextDecoder:
		return Value{Ref: e.internString("TextDecoder { encoding: 'utf-8', fatal: false, ignoreBOM: false }"), Ty: TypePtr}, true, nil
	}
	return Value{}, false, nil
}

func isInspectHost(t Type) bool {
	return t.IR == "ptr" && (t.IsPromise || t.IsURLSearchParams || t.IsHeaders || t.IsBlob || t.IsArrayBuffer || t.IsDataView || t.IsTextEncoder || t.IsTextDecoder)
}

// emitInspectHeaders renders `Headers { x: '1', 'content-type': 'a' }`.
func (e *Emitter) emitInspectHeaders(v Value, depth int) (Value, error) {
	if depth > e.effectiveInspectDepth() {
		return Value{Ref: e.internString("[Headers]"), Ty: TypePtr}, nil
	}
	suffix, _ := mapRuntime(TypePtr)
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(v.Ref, suffix, TypePtr)
	nonempty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", nonempty, e.icmpNe(keysLen, "0")))
	list := e.inspectBegin(depth, nonempty)
	idxSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxSlot))
	condL, bodyL, doneL := e.freshLabel("insphdr.cond"), e.freshLabel("insphdr.body"), e.freshLabel("insphdr.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idx, done := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx, idxSlot))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idx, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
	e.emitLabel(bodyL)
	kGep, k, vGep, val := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kGep, keysPtr, idx))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", k, kGep))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", vGep, valsPtr, idx))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", val, vGep))
	kStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_key(ptr %s)", kStr, k))
	entry, err := e.emitStringConcat(Value{Ref: kStr, Ty: TypePtr}, Value{Ref: e.internString(": "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	vStr, err := e.emitInspectField(Value{Ref: val, Ty: TypePtr}, depth+1)
	if err != nil {
		return Value{}, err
	}
	entry, err = e.emitStringConcat(entry, vStr)
	if err != nil {
		return Value{}, err
	}
	e.inspectPush(list, entry)
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	// Node formats the entries as an object and then prefixes the class name
	// (undici's inspect.custom), so the prefix does not count toward the
	// line-break width.
	body := e.inspectEnd(list, e.internString("{"), e.internString("}"), depth, false, false)
	return e.emitStringConcat(Value{Ref: e.internString("Headers "), Ty: TypePtr}, body)
}

// emitInspectBlob renders `Blob { size: 2, type: ” }`.
func (e *Emitter) emitInspectBlob(v Value, depth int) (Value, error) {
	if depth > e.effectiveInspectDepth() {
		return Value{Ref: e.internString("[Blob]"), Ty: TypePtr}, nil
	}
	size, _ := e.emitBlobSizeData(v.Ref)
	typeGep, typ := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", typeGep, blobStructIR, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", typ, typeGep))
	list := e.inspectBegin(depth, "1")
	sizeStr, err := e.emitValueToString(Value{Ref: size, Ty: TypeI64})
	if err != nil {
		return Value{}, err
	}
	entry, err := e.emitStringConcat(Value{Ref: e.internString("size: "), Ty: TypePtr}, sizeStr)
	if err != nil {
		return Value{}, err
	}
	e.inspectPush(list, entry)
	typeStr, err := e.emitInspectField(Value{Ref: e.emitNullToEmpty(typ), Ty: TypePtr}, depth+1)
	if err != nil {
		return Value{}, err
	}
	entry, err = e.emitStringConcat(Value{Ref: e.internString("type: "), Ty: TypePtr}, typeStr)
	if err != nil {
		return Value{}, err
	}
	e.inspectPush(list, entry)
	return e.inspectEnd(list, e.internString("Blob {"), e.internString("}"), depth, false, false), nil
}

// emitInspectArrayBuffer renders
// `ArrayBuffer { [Uint8Contents]: <00 00>, [byteLength]: 2 }`.
func (e *Emitter) emitInspectArrayBuffer(v Value, depth int) (Value, error) {
	name := "ArrayBuffer"
	if v.Ty.IsSharedArrayBuffer {
		name = "SharedArrayBuffer"
	}
	if depth > e.effectiveInspectDepth() {
		return Value{Ref: e.internString("[" + name + "]"), Ty: TypePtr}, nil
	}
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, v.Ref))
	dataGep, data := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataGep, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, dataGep))
	e.ensureInspectReduce()
	hex := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_hex(ptr %s, i64 %s)", hex, data, n))
	list := e.inspectBegin(depth, "1")
	entry, err := e.emitStringConcat(Value{Ref: e.internString("[Uint8Contents]: "), Ty: TypePtr}, Value{Ref: hex, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.inspectPush(list, entry)
	lenStr, err := e.emitValueToString(Value{Ref: n, Ty: TypeI64})
	if err != nil {
		return Value{}, err
	}
	entry, err = e.emitStringConcat(Value{Ref: e.internString("[byteLength]: "), Ty: TypePtr}, lenStr)
	if err != nil {
		return Value{}, err
	}
	e.inspectPush(list, entry)
	return e.inspectEnd(list, e.internString(name+" {"), e.internString("}"), depth, false, false), nil
}

// emitInspectDataView renders `DataView { [byteLength]: 2, [byteOffset]: 0,
// [buffer]: ArrayBuffer { … } }`.
func (e *Emitter) emitInspectDataView(v Value, depth int) (Value, error) {
	if depth > e.effectiveInspectDepth() {
		return Value{Ref: e.internString("[DataView]"), Ty: TypePtr}, nil
	}
	field := func(i int, ir string) string {
		g, r := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, dataViewStructIR, v.Ref, i))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ir, g))
		return r
	}
	list := e.inspectBegin(depth, "1")
	for _, f := range []struct {
		label string
		idx   int
	}{{"[byteLength]: ", 1}, {"[byteOffset]: ", 2}} {
		n, err := e.emitValueToString(Value{Ref: field(f.idx, "i64"), Ty: TypeI64})
		if err != nil {
			return Value{}, err
		}
		entry, err := e.emitStringConcat(Value{Ref: e.internString(f.label), Ty: TypePtr}, n)
		if err != nil {
			return Value{}, err
		}
		e.inspectPush(list, entry)
	}
	buf, err := e.emitInspectArrayBuffer(Value{Ref: field(3, "ptr"), Ty: ArrayBufferType()}, depth+1)
	if err != nil {
		return Value{}, err
	}
	entry, err := e.emitStringConcat(Value{Ref: e.internString("[buffer]: "), Ty: TypePtr}, buf)
	if err != nil {
		return Value{}, err
	}
	e.inspectPush(list, entry)
	return e.inspectEnd(list, e.internString("DataView {"), e.internString("}"), depth, false, false), nil
}

// emitNullToEmpty is s, or "" for a null pointer.
func (e *Emitter) emitNullToEmpty(s string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, e.ptrIsNull(s), e.internString(""), s))
	return r
}
