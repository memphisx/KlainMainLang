package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_inspect_host.go — util.inspect's rendering of the host classes that
// are not plain object layouts: Blob, Headers, ArrayBuffer, TextEncoder,
// TextDecoder and URLSearchParams, as Node prints them. ok is false for any
// other type.
func (e *Emitter) emitInspectHost(v Value, depth int) (s Value, ok bool, err error) {
	t := v.Ty
	if !isInspectHost(t) {
		return Value{}, false, nil
	}
	if t.Nullable() || t.IsNull() {
		// An absent one prints its keyword.
		present := v
		present.Ty.setNullable(false)
		present.Ty.setUndefined(false)
		present.Ty.setKind(kindNull, false)
		s, err := e.emitInspectNullablePtr(v, func(b Value) (Value, error) {
			r, _, err := e.emitInspectHost(Value{Ref: b.Ref, Ty: present.Ty}, depth)
			return r, err
		})
		return s, true, err
	}
	switch {
	case isInspectStream(t):
		s, err := e.emitInspectStream(v, depth)
		return s, true, err
	case t.IsArrayBuffer():
		s, err := e.emitInspectArrayBuffer(v, depth)
		return s, true, err
	case t.IsPromise():
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
	return t.IR == "ptr" && (t.IsPromise() || t.IsArrayBuffer() || isInspectStream(t))
}

// isInspectStream reports a ReadableStream, WritableStream or
// TransformStream value.
func isInspectStream(t Type) bool {
	return !t.IsDynamic() && !t.IsClass() && (t.IsReadableStream() || t.IsWritableStream() || t.IsTransformStream() ||
		t.IsStreamReader() || t.IsStreamWriter() || t.IsRSController() || t.IsWSController())
}

// emitInspectStream renders a stream as Node does:
// `ReadableStream { locked: false, state: 'readable', supportsBYOB: false }`,
// `WritableStream { locked: false, state: 'writable' }`, and a
// TransformStream's two sides with its backpressure. Past the depth limit
// Node's custom inspect yields undefined.
func (e *Emitter) emitInspectStream(v Value, depth int) (Value, error) {
	return e.emitInspectStreamAt(v, depth, e.effectiveInspectDepth())
}

// emitInspectStreamAt is emitInspectStream at nesting level `level` of an
// inspect call whose depth limit is lim, laid out as Node's custom inspect
// does: a fresh inspect (limit lim-1) of the `{ … }` data object, the class
// name before it, its lines re-indented by level.
func (e *Emitter) emitInspectStreamAt(v Value, level, lim int) (Value, error) {
	if level > lim {
		return Value{Ref: e.internString("undefined"), Ty: TypePtr}, nil
	}
	t := v.Ty
	// The data object is level 0 of the fresh call; its entries level 1.
	depth := 0
	inner := lim - 1
	// emitInspectField's level for an entry value: level 1 of the fresh call,
	// shifted so its depth limit (effectiveInspectDepth) is the call's.
	entryDepth := 1 + e.effectiveInspectDepth() - inner
	list := e.inspectBegin(depth, "1")
	push := func(key string, val Value) error {
		entry, err := e.emitStringConcat(Value{Ref: e.internString(key + ": "), Ty: TypePtr}, val)
		if err != nil {
			return err
		}
		e.inspectPush(list, entry)
		return nil
	}
	pick := func(cond string, yes, no string) Value {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, cond, e.internString(yes), e.internString(no)))
		return Value{Ref: r, Ty: TypePtr}
	}
	chunk := TypeI64
	if t.StreamChunk != nil {
		chunk = *t.StreamChunk
	}
	switch {
	case t.IsRSController():
		return e.streamInspectEnd(list, "ReadableStreamDefaultController", depth, level), nil
	case t.IsWSController():
		// Its value is its stream.
		str, err := e.emitInspectStreamAt(Value{Ref: v.Ref, Ty: WritableStreamType(chunk)}, 1, inner)
		if err != nil {
			return Value{}, err
		}
		if err := push("stream", str); err != nil {
			return Value{}, err
		}
		return e.streamInspectEnd(list, "WritableStreamDefaultController", depth, level), nil
	case t.IsStreamReader() || t.IsStreamWriter():
		return e.emitInspectStreamHandle(v, list, push, chunk, level, inner, entryDepth)
	}
	if t.IsTransformStream() {
		const tmp = "__kml_insp_ts"
		e.pushScope()
		defer e.popScope()
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
		e.define(tmp, Symbol{Ptr: slot, Ty: t})
		var readable string
		for _, side := range []string{"readable", "writable"} {
			sv, err := e.emitExpr(ast.NewMemberExpression(ast.NewIdentifier(tmp, ast.Pos{}), side, ast.Pos{}))
			if err != nil {
				return Value{}, err
			}
			if side == "readable" {
				readable = sv.Ref
			}
			str, err := e.emitInspectStreamAt(sv, 1, inner)
			if err != nil {
				return Value{}, err
			}
			if err := push(side, str); err != nil {
				return Value{}, err
			}
		}
		e.ensureStreamRuntime()
		d, bp := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_rs_desired(ptr %s)", d, readable))
		e.emitInstr(fmt.Sprintf("%s = fcmp ole double %s, 0.0", bp, d))
		if err := push("backpressure", pick(bp, "true", "false")); err != nil {
			return Value{}, err
		}
		return e.streamInspectEnd(list, "TransformStream", depth, level), nil
	}
	readable := t.IsReadableStream()
	bits := e.freshReg()
	if readable {
		e.ensureStreamRuntime()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_inspect_bits(ptr %s)", bits, v.Ref))
	} else {
		e.ensureWStreamRuntime()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ws_inspect_bits(ptr %s)", bits, v.Ref))
	}
	lk, locked, st := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, 4", lk, bits))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", locked, lk))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, 3", st, bits))
	if err := push("locked", pick(locked, "true", "false")); err != nil {
		return Value{}, err
	}
	name := "WritableStream"
	names := []string{"'writable'", "'closed'", "'errored'"}
	if readable {
		name = "ReadableStream"
		names[0] = "'readable'"
	}
	isClosed, isErr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isClosed, st))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", isErr, st))
	mid := pick(isClosed, names[1], names[0])
	state := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", state, isErr, e.internString(names[2]), mid.Ref))
	if err := push("state", Value{Ref: state, Ty: TypePtr}); err != nil {
		return Value{}, err
	}
	if readable {
		if err := push("supportsBYOB", Value{Ref: e.internString("false"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
	}
	return e.streamInspectEnd(list, name, depth, level), nil
}

// streamInspectEnd lays out a stream's data object at indentation 0 and
// prefixes the class name, re-indented by level.
func (e *Emitter) streamInspectEnd(list, name string, depth, level int) Value {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_end(ptr %s, ptr %s, ptr %s, i64 0, i64 %d, i64 0, i64 0)",
		r, list, e.internString("{"), e.internString("}"), depth))
	named := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_tagged(ptr %s, ptr null, ptr %s)", named, e.internString(name), r))
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_reindent(ptr %s, i64 %d)", out, named, level))
	return Value{Ref: out, Ty: TypePtr}
}

// emitInspectArrayBuffer renders
// `ArrayBuffer { [Uint8Contents]: <00 00>, [byteLength]: 2 }`.
func (e *Emitter) emitInspectArrayBuffer(v Value, depth int) (Value, error) {
	name := "ArrayBuffer"
	if v.Ty.IsSharedArrayBuffer() {
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

// emitInspectStreamHandle renders a reader
// (`ReadableStreamDefaultReader { stream, readRequests, close }`) or a writer
// (`WritableStreamDefaultWriter { stream, close, ready, desiredSize }`); a
// released one's stream is undefined, and a released writer's desiredSize
// throws, as Node's inspect does.
func (e *Emitter) emitInspectStreamHandle(v Value, list string, push func(string, Value) error, chunk Type, level, inner, entryDepth int) (Value, error) {
	const depth = 0
	t := v.Ty
	reader := t.IsStreamReader()
	stream := e.streamOfHandle(t, v.Ref)
	streamTy := ReadableStreamType(chunk)
	if !reader {
		streamTy = WritableStreamType(chunk)
	}
	// The stream (undefined once released), and a reader's pending reads.
	strSlot, nSlot := e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", strSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", nSlot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("undefined"), strSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", nSlot))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, stream))
	boundL, doneL := e.freshLabel("insp.handle.bound"), e.freshLabel("insp.handle.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, boundL))
	e.emitLabel(boundL)
	str, err := e.emitInspectStreamAt(Value{Ref: stream, Ty: streamTy}, 1, inner)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", str.Ref, strSlot))
	if reader {
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_read_requests(ptr %s)", n, stream))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, nSlot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	sv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sv, strSlot))
	if err := push("stream", Value{Ref: sv, Ty: TypePtr}); err != nil {
		return Value{}, err
	}
	field := func(name, key string) error {
		p, ok := streamPropFor(name, t)
		if !ok {
			return fmt.Errorf("internal: no stream property %s", name)
		}
		pv, err := e.emitHandleProp(name, p, v.Ref, stream)
		if err != nil {
			return err
		}
		ps, err := e.emitInspectField(pv, entryDepth)
		if err != nil {
			return err
		}
		return push(key, ps)
	}
	if reader {
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, nSlot))
		ns, err := e.emitInspectField(Value{Ref: n, Ty: TypeI64}, entryDepth)
		if err != nil {
			return Value{}, err
		}
		if err := push("readRequests", ns); err != nil {
			return Value{}, err
		}
		if err := field("closed", "close"); err != nil {
			return Value{}, err
		}
		return e.streamInspectEnd(list, "ReadableStreamDefaultReader", depth, level), nil
	}
	for _, f := range [][2]string{{"closed", "close"}, {"ready", "ready"}, {"desiredSize", "desiredSize"}} {
		if err := field(f[0], f[1]); err != nil {
			return Value{}, err
		}
	}
	return e.streamInspectEnd(list, "WritableStreamDefaultWriter", depth, level), nil
}
