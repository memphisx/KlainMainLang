package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// streamProp is a property of the WHATWG stream surface (TDD-00097): the
// receivers that own it, its type, and its read from the stream's hidden
// struct. The type is both the emitted value's and inference's, so the two
// cannot disagree.
type streamProp struct {
	owner    func(Type) bool
	writable bool // the receiver resolves to a %kml.wstream
	ty       func() Type
	emit     func(e *Emitter, ptr string) Value
}

// streamProps are the stream properties by name, each owner's entry.
var streamProps map[string][]streamProp

func nullableNumber() Type {
	t := TypeF64
	t.setNullable(true)
	return t
}

func init() {
	// locked: a flag bit in the stream's state word.
	locked := func(structIR string, idx int) func(e *Emitter, ptr string) Value {
		return func(e *Emitter, ptr string) Value {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, ptr, idx))
			f := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", f, gep))
			bit := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i64 %s, 32", bit, f))
			b := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, bit))
			return Value{Ref: b, Ty: TypeBool}
		}
	}
	// desiredSize is `number | null`: `null` once errored (state 2), else
	// the runtime's `hwm − queued` (0 once closed) (TDD-00196/ADR-00826).
	desired := func(fn, structIR string) func(e *Emitter, ptr string) Value {
		return func(e *Emitter, ptr string) Value {
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call double @%s(ptr %s)", d, fn, ptr))
			stGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", stGep, structIR, ptr))
			st := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", st, stGep))
			present := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 2", present, st))
			nty := nullableNumber()
			return Value{Ref: e.makeNullableScalarAgg(nty, present, d), Ty: nty}
		}
	}
	// A Promise<void> the stream holds in a slot (a reader's or writer's
	// closed, a writer's ready).
	promiseAt := func(structIR string, idx int) func(e *Emitter, ptr string) Value {
		return func(e *Emitter, ptr string) Value {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, ptr, idx))
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, gep))
			return Value{Ref: p, Ty: taskPromise(TypeVoid)}
		}
	}
	boolTy := func() Type { return TypeBool }
	voidPromise := func() Type { return taskPromise(TypeVoid) }
	isWriterOrCtl := func(t Type) bool { return t.IsStreamWriter() || t.IsWSController() }
	streamProps = map[string][]streamProp{
		"locked": {
			{owner: Type.IsReadableStream, ty: boolTy, emit: locked(rstreamStructIR, 11)},
			{owner: Type.IsWritableStream, writable: true, ty: boolTy, emit: locked(wstreamStructIR, 12)},
		},
		"desiredSize": {
			{owner: Type.IsRSController, ty: nullableNumber, emit: desired("__kml_rs_desired", rstreamStructIR)},
			{owner: isWriterOrCtl, writable: true, ty: nullableNumber, emit: desired("__kml_ws_desired", wstreamStructIR)},
		},
		"closed": {
			{owner: Type.IsStreamReader, ty: voidPromise, emit: promiseAt(rstreamStructIR, 16)},
			{owner: Type.IsStreamWriter, writable: true, ty: voidPromise, emit: promiseAt(wstreamStructIR, 14)},
		},
		"ready": {
			{owner: Type.IsStreamWriter, writable: true, ty: voidPromise, emit: promiseAt(wstreamStructIR, 13)},
		},
	}
}

// isReadableSide and isWritableSide report a readable or writable stream,
// its reader or writer, or its controller.
func isReadableSide(t Type) bool {
	return t.IsReadableStream() || t.IsStreamReader() || t.IsRSController()
}

func isWritableSide(t Type) bool {
	return t.IsWritableStream() || t.IsStreamWriter() || t.IsWSController()
}

// streamPropFor is the entry for property name on a receiver of type objTy.
func streamPropFor(name string, objTy Type) (streamProp, bool) {
	for _, p := range streamProps[name] {
		if p.owner(objTy) {
			return p, true
		}
	}
	return streamProp{}, false
}

// emitStreamProp reads a stream property of ex's receiver, a readable or
// writable side (isReadableSide, isWritableSide).
func (e *Emitter) emitStreamProp(ex *ast.MemberExpression, writable bool) (Value, error) {
	var ty Type
	var handle, ptr string
	var err error
	if writable {
		ty, handle, ptr, err = e.resolveWStreamHandle(ex.Object, ex.GetPos())
	} else {
		ty, handle, ptr, err = e.resolveStreamHandle(ex.Object, ex.GetPos())
	}
	if err != nil {
		return Value{}, err
	}
	if writable {
		e.ensureWStreamRuntime()
	} else {
		e.ensureStreamRuntime()
	}
	if p, ok := streamPropFor(ex.Property, ty); ok && p.writable == writable {
		if !ty.IsStreamReader() && !ty.IsStreamWriter() {
			return p.emit(e, ptr), nil
		}
		return e.emitHandleProp(ex.Property, p, handle, ptr)
	}
	what := "stream"
	if writable {
		what = "writable stream"
	}
	return Value{}, fmt.Errorf("%d:%d: property '%s' is not available on this %s value", ex.GetPos().Line, ex.GetPos().Col, ex.Property, what)
}

// emitHandleProp reads property name of a reader or writer: its stream's
// while attached; once released, its own closed and ready, and a
// desiredSize that throws as Node's does.
func (e *Emitter) emitHandleProp(name string, p streamProp, handle, ptr string) (Value, error) {
	if name == "desiredSize" {
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, ptr))
		relL, okL := e.freshLabel("writer.released"), e.freshLabel("writer.bound")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, relL, okL))
		e.emitLabel(relL)
		e.excThrow(e.emitStreamStateErrorPtr("Invalid state: Writer is not bound to a WritableStream"))
		e.emitTerminator("unreachable")
		e.emitLabel(okL)
		return p.emit(e, ptr), nil
	}
	slot := handleClosedSlot
	if name == "ready" {
		slot = handleReadySlot
	}
	return e.emitIfReleased(ptr, func() (Value, error) { return p.emit(e, ptr), nil }, func() (Value, error) {
		return Value{Ref: e.handleSlot(handle, slot), Ty: p.ty()}, nil
	})
}
