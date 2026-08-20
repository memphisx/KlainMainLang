// emit_task_emit.go — emit-side wiring for may-suspend async functions
// (TDD-00083 Stage 2): binding params from a heap args bundle, marshalling a
// value into / out of a task promise's v0/v1 fields, and the call-site spawn.

package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// bindTaskParamsFromBundle allocas each parameter of a may-suspend function and
// loads its value from the incoming args bundle (%__taskargs), mirroring the
// normal LLVM-param binding but sourced from the heap struct emitSpawnCall built.
func (e *Emitter) bindTaskParamsFromBundle(decl *ast.FunctionDeclaration, sig FuncSig) error {
	fields, paramStart := taskBundleFields(sig.ParamTypes)
	bundleIR := taskBundleIR(fields)
	for i, p := range decl.Params {
		pty := sig.ParamTypes[i]
		fi := paramStart[i]
		if p.Rest {
			return fmt.Errorf("%d:%d: rest parameters in a suspending async function are not yet supported (TDD-00083 Stage 2)", decl.GetPos().Line, decl.GetPos().Col)
		}
		switch {
		case pty.IsArray:
			ptrAlloca := "%v_" + p.Name + "_ptr"
			lenAlloca := "%v_" + p.Name + "_len"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
			fp := e.freshReg()
			pv := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%__taskargs, i32 0, i32 %d", fp, bundleIR, fi))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pv, fp))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pv, ptrAlloca))
			fl := e.freshReg()
			lv := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%__taskargs, i32 0, i32 %d", fl, bundleIR, fi+1))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lv, fl))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lv, lenAlloca))
			// A destructured array parameter unpacks straight from the (ptr, len)
			// pair, mirroring the non-suspending binding (emit_func.go). lv is the
			// i64 length value unpackArrayPatternInto wants (not the alloca ptr).
			if p.ArrayPattern != nil {
				dataPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, ptrAlloca))
				if err := e.unpackArrayPatternInto(dataPtr, lv, *pty.ElemType, p.ArrayPattern); err != nil {
					return err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: pty})
		case isNullableScalar(pty):
			// Reassemble the { i1, T } aggregate from the presence + payload slots
			// (TDD-00084 Part C), then bind it exactly like a non-suspending
			// nullable-scalar parameter.
			base := pty.withoutNullable()
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, nullableScalarStorageIR(pty), storageAlign(pty)))
			fp := e.freshReg()
			presI64 := e.freshReg()
			pres1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%__taskargs, i32 0, i32 %d", fp, bundleIR, fi))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", presI64, fp))
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i1", pres1, presI64))
			fl := e.freshReg()
			payBits := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%__taskargs, i32 0, i32 %d", fl, bundleIR, fi+1))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", payBits, fl))
			payload := e.promiseValFromBits(payBits, base)
			e.storeNullableScalarFields(ptrName, pty, pres1, payload.Ref)
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty, NullableBoxed: true})
		default:
			alloca := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", alloca, pty.IR, pty.Align()))
			fp := e.freshReg()
			v := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%__taskargs, i32 0, i32 %d", fp, bundleIR, fi))
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", v, pty.IR, fp, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", pty.IR, v, alloca, pty.Align()))
			// A destructured object parameter unpacks straight from the object ptr.
			if p.ObjectPattern != nil {
				objPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objPtr, alloca))
				if err := e.unpackObjectPatternInto(objPtr, pty, p.ObjectPattern, decl.GetPos()); err != nil {
					return err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: alloca, Ty: pty})
		}
	}
	return nil
}

// emitTaskEpilogue emits the may-suspend async function's return block: marshal
// the body's return value (held in the coroHdl slot, exactly as the synchronous
// async path) into the running task's promise, then `ret void` back to the
// trampoline, which resolves the promise (or, if the body threw, rejects it).
func (e *Emitter) emitTaskEpilogue() {
	e.emitTerminator("br label %" + e.coroRetLabel)
	e.emitLabel(e.coroRetLabel)
	pty := e.currentPromiseTy
	if pty.IR != "void" && pty.IR != "" {
		ct := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_current_task, align 8", ct))
		psp := e.freshReg()
		ps := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", psp, taskStructIR, ct, taskPromiseSlot))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ps, psp))
		valReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, StructFieldIR(pty), e.coroHdl, pty.Align()))
		e.storePromiseValue(ps, Value{Ref: valReg, Ty: pty})
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", e.coroHdl))
	}
	e.emitTerminator("ret void")
}

// taskBundleFields returns the LLVM field-IR list of the args bundle for a
// may-suspend function's parameters (an array param is two fields: ptr, i64),
// plus each parameter's starting field index. The call site and the body share
// this layout.
func taskBundleFields(paramTypes []Type) (fields []string, paramStart []int) {
	for _, pty := range paramTypes {
		paramStart = append(paramStart, len(fields))
		switch {
		case pty.IsArray:
			fields = append(fields, "ptr", "i64")
		case isNullableScalar(pty):
			// A nullable-scalar { i1, T } travels as two i64 slots (presence bit,
			// payload bits) so every bundle field stays 8 bytes — TDD-00084 Part C.
			fields = append(fields, "i64", "i64")
		default:
			fields = append(fields, pty.IR)
		}
	}
	return
}

func taskBundleIR(fields []string) string {
	if len(fields) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

// promiseBitsOf converts a scalar/ptr Value to the i64 bit pattern stored in a
// task promise's v0 field.
func (e *Emitter) promiseBitsOf(val Value) string {
	switch val.Ty.IR {
	case "i64":
		return val.Ref
	case "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, val.Ref))
		return r
	case "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", r, val.Ref))
		return r
	default: // i1/i8/i16/i32
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext %s %s to i64", r, val.Ty.IR, val.Ref))
		return r
	}
}

// promiseValFromBits converts an i64 bit pattern back to a Value of type ty.
func (e *Emitter) promiseValFromBits(bits string, ty Type) Value {
	switch ty.IR {
	case "i64":
		return Value{Ref: bits, Ty: ty}
	case "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, bits))
		return Value{Ref: r, Ty: ty}
	case "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", r, bits))
		return Value{Ref: r, Ty: ty}
	default:
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", r, bits, ty.IR))
		return Value{Ref: r, Ty: ty}
	}
}

// emitQueueMicrotask emits queueMicrotask(cb) — enqueue the callback closure on
// the microtask FIFO (TDD-00083 Stage 3), drained at the reachable checkpoints.
func (e *Emitter) emitQueueMicrotask(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: queueMicrotask expects 1 argument", pos.Line, pos.Col)
	}
	cb, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if cb.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: queueMicrotask expects a function", pos.Line, pos.Col)
	}
	e.ensureMicrotasks()
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", cb.Ref))
	return Value{Ty: TypeVoid}, nil
}

// emitTaskRethrowIfRejected emits: if the (already-awaited) task promise is
// rejected (resolved == 2), re-throw its stored error object at the current
// point; otherwise fall through. Shared by emitAwait and the combinators.
func (e *Emitter) emitTaskRethrowIfRejected(promiseReg string) {
	e.ensureExceptionHelpers()
	resP := e.freshReg()
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, promiseReg))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", res, resP))
	rej := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", rej, res))
	rejL := e.freshLabel("task.reject")
	okL := e.freshLabel("task.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rej, rejL, okL))
	e.emitLabel(rejL)
	v0P := e.freshReg()
	v0 := e.freshReg()
	errReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0P, promiseStructIR, promiseReg))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", v0, v0P))
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errReg, v0))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// storePromiseValue marshals val into promiseReg's v0 (index 2) and, for
// array-shaped {ptr,i64} values, v1 (index 3).
func (e *Emitter) storePromiseValue(promiseReg string, val Value) {
	v0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0, promiseStructIR, promiseReg))
	if val.Ty.IsArray {
		p := e.freshReg()
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", p, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", l, val.Ref))
		pi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", pi, p))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pi, v0))
		v1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", v1, promiseStructIR, promiseReg))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, v1))
		return
	}
	bits := e.promiseBitsOf(val)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bits, v0))
}

// loadPromiseValue reads a value of type ty from promiseReg's v0/v1 fields.
func (e *Emitter) loadPromiseValue(promiseReg string, ty Type) Value {
	v0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0, promiseStructIR, promiseReg))
	if ty.IsArray {
		lo := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lo, v0))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, lo))
		v1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", v1, promiseStructIR, promiseReg))
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, v1))
		agg0 := e.freshReg()
		agg1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", agg0, p))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", agg1, agg0, l))
		return Value{Ref: agg1, Ty: ty}
	}
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", bits, v0))
	return e.promiseValFromBits(bits, ty)
}

// emitMaySuspendCall evaluates the arguments to a may-suspend async function and
// spawns it as a task (TDD-00083 Stage 2).
func (e *Emitter) emitMaySuspendCall(name string, sig FuncSig, args []ast.Expression, pos ast.Pos) (Value, error) {
	if sig.HasRest {
		return Value{}, fmt.Errorf("%d:%d: rest parameters in a suspending async function are not yet supported (TDD-00083 Stage 2)", pos.Line, pos.Col)
	}
	argVals := make([]Value, len(sig.ParamTypes))
	for i := range sig.ParamTypes {
		if i >= len(args) {
			return Value{}, fmt.Errorf("%d:%d: missing argument to '%s'", pos.Line, pos.Col, name)
		}
		v, err := e.emitExprWithObjectHint(args[i], sig.ParamTypes[i])
		if err != nil {
			return Value{}, err
		}
		argVals[i] = v
	}
	return e.emitSpawnCall(name, sig, argVals)
}

// emitSpawnCall emits a call to a may-suspend async function `name` as a task
// spawn: it bundles the already-evaluated argument Values into a heap struct,
// allocates a task promise, spawns the task (running the body up to its first
// suspend), and returns the pending promise (typed Promise<T> with PromiseTask).
func (e *Emitter) emitSpawnCall(name string, sig FuncSig, argVals []Value) (Value, error) {
	e.ensureTaskRuntime()
	fields, paramStart := taskBundleFields(sig.ParamTypes)
	bundleIR := taskBundleIR(fields)

	var bundleReg string
	if len(fields) == 0 {
		bundleReg = "null"
	} else {
		bundleReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", bundleReg, 8*len(fields)))
		for i := range sig.ParamTypes {
			pty := sig.ParamTypes[i]
			av := e.coerce(argVals[i], pty)
			fi := paramStart[i]
			if pty.IsArray {
				p := e.freshReg()
				l := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", p, av.Ref))
				e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", l, av.Ref))
				fp := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fp, bundleIR, bundleReg, fi))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, fp))
				fl := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fl, bundleIR, bundleReg, fi+1))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, fl))
			} else if isNullableScalar(pty) {
				// { i1, T } aggregate → presence (zext to i64) + payload bits. Box
				// the raw arg (coerce doesn't box a bare value toward nullable).
				aggRef := e.boxNullableScalarFromValue(argVals[i], pty)
				present, payload := e.nullableScalarAggParts(Value{Ref: aggRef, Ty: pty})
				presI64 := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", presI64, present))
				fp := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fp, bundleIR, bundleReg, fi))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", presI64, fp))
				bits := e.promiseBitsOf(payload)
				fl := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fl, bundleIR, bundleReg, fi+1))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bits, fl))
			} else {
				fp := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fp, bundleIR, bundleReg, fi))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", pty.IR, av.Ref, fp, pty.Align()))
			}
		}
	}

	promise := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", promise))
	e.emitInstr(fmt.Sprintf("call ptr @__kml_spawn_task(ptr @%s, ptr %s, ptr %s)", name, bundleReg, promise))

	inner := sig.RetType
	if inner.IsPromise && inner.PromiseType != nil {
		inner = *inner.PromiseType
	} else {
		inner = TypeVoid
	}
	pt := PromiseOf(inner)
	pt.PromiseTask = true
	return Value{Ref: promise, Ty: pt}, nil
}
