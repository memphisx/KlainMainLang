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
	case t.IsArrayBuffer:
		s, err := e.emitInspectArrayBuffer(v, depth)
		return s, true, err
	case t.IsPromise:
		// Node's `Promise { value }` form, through the boxed promise.
		b, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, true, err
		}
		s, err := e.emitDynamicInspectAt(b, depth)
		return s, true, err
	}
	return Value{}, false, nil
}

func isInspectHost(t Type) bool {
	return t.IR == "ptr" && (t.IsPromise || t.IsArrayBuffer)
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

// emitNullToEmpty is s, or "" for a null pointer.
func (e *Emitter) emitNullToEmpty(s string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, e.ptrIsNull(s), e.internString(""), s))
	return r
}
