// emit_async.go — async function prologue/epilogue and await expression emission.
// Strategy: an async function mallocs a slot for the return value on the heap and
// returns its pointer.  await reads the value from the slot and frees it.
// No LLVM coroutine intrinsics are used.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// ── free declaration ──────────────────────────────────────────────────────────

func (e *Emitter) ensureFree() {
	if e.usedFree {
		return
	}
	e.usedFree = true
	e.emitGlobal("declare void @free(ptr)")
}

// ── Async function prologue / epilogue ────────────────────────────────────────

// emitAsyncPrologue mallocs the promise slot in the entry block (e.allocas) and
// stores its pointer in e.coroHdl.  Called before the function body is emitted.
func (e *Emitter) emitAsyncPrologue() {
	e.ensureMalloc()
	size := int64(8) // default: enough for i64 / ptr
	if e.currentPromiseTy.IR != "void" && e.currentPromiseTy.IR != "" {
		a := int64(e.currentPromiseTy.Align())
		if a > size {
			size = a
		}
	} else {
		size = 1 // Promise<void>: allocate one byte (never written)
	}
	frameReg := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", frameReg, size))
	e.coroHdl = frameReg
}

// emitAsyncEpilogue emits an implicit branch to the async-return label and the
// return block itself.  Must be called after all body instructions.
func (e *Emitter) emitAsyncEpilogue() {
	e.emitTerminator(fmt.Sprintf("br label %%%s", e.coroRetLabel))
	e.body.WriteString(fmt.Sprintf("\n%s:\n", e.coroRetLabel))
	e.body.WriteString(fmt.Sprintf("  ret ptr %s\n", e.coroHdl))
}

// ── await expression ──────────────────────────────────────────────────────────

// emitAwait evaluates the Promise (a ptr to the heap slot), loads the resolved
// value, frees the slot, and returns the inner value.
func (e *Emitter) emitAwait(ex *ast.AwaitExpression) (Value, error) {
	hdlVal, err := e.emitExpr(ex.Argument)
	if err != nil {
		return Value{}, err
	}
	if hdlVal.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: await requires a Promise value",
			ex.GetPos().Line, ex.GetPos().Col)
	}

	// Determine the unwrapped type (T in Promise<T>).
	var promiseTy Type
	if hdlVal.Ty.IsPromise && hdlVal.Ty.PromiseType != nil {
		promiseTy = *hdlVal.Ty.PromiseType
	} else {
		argTy := e.inferExprType(ex.Argument)
		if argTy.IsPromise && argTy.PromiseType != nil {
			promiseTy = *argTy.PromiseType
		} else {
			promiseTy = TypeVoid
		}
	}

	e.ensureFree()

	if promiseTy.IR == "void" || promiseTy.IR == "" {
		// Promise<void>: just free the 1-byte slot.
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}

	if promiseTy.IsResponse {
		// fetch()'s Promise<Response>: the slot holds a still-pending fetch
		// handle (see emit_fetch.go's emitFetch), not a Response yet.
		// __kml_await_fetch does the actual wait (yielding if on a
		// connection fiber, busy-spinning otherwise — see
		// ensureFetchAsync's doc comment) and returns the final
		// status/body once the transfer completes, throwing on a
		// transfer-level failure exactly like the old blocking fetch did.
		e.ensureFetchAsync()
		pendingPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pendingPtr, hdlVal.Ref))
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call { i64, ptr } @__kml_await_fetch(ptr %s)", raw, pendingPtr))
		status := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 0", status, raw))
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 1", body, raw))

		ok := e.freshReg()
		okHigh := e.freshReg()
		okLow := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 200", okLow, status))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 300", okHigh, status))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", ok, okLow, okHigh))

		respTy := promiseTy
		e.ensureMalloc()
		respReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", respReg, respTy.StructSize()))
		structIR := respTy.StructIR()
		storeField := func(name, ir, ref string, align int) {
			idx, _, _ := respTy.FieldIndex(name)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, respReg, idx))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, ref, gep, align))
		}
		storeField("status", "i64", status, 8)
		storeField("ok", "i1", ok, 1)
		storeField("body", "ptr", body, 8)

		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))
		return Value{Ref: respReg, Ty: respTy}, nil
	}

	// Load the promised value then free the slot.
	resultReg := e.freshReg()
	align := promiseTy.Align()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
		resultReg, promiseTy.IR, hdlVal.Ref, align))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))

	return Value{Ref: resultReg, Ty: promiseTy}, nil
}
