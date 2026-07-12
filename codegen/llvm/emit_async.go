// emit_async.go — async function prologue/epilogue and await expression emission.
// Strategy: an async function mallocs a slot for the return value on the heap and
// returns its pointer.  await reads the value from the slot and frees it.
// No LLVM coroutine intrinsics are used.
package llvm

import (
	"fmt"
	"KlainMainLang/ast"
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

	// Load the promised value then free the slot.
	resultReg := e.freshReg()
	align := promiseTy.Align()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
		resultReg, promiseTy.IR, hdlVal.Ref, align))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", hdlVal.Ref))

	return Value{Ref: resultReg, Ty: promiseTy}, nil
}
