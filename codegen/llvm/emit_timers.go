// emit_timers.go — setTimeout/clearTimeout/setInterval/clearInterval.
// Bare global functions (like fetch/btoa/parseInt), not a namespace.
//
// Needs no general-purpose (I/O-multiplexing) event loop — just a
// sleep-until-next-due queue, drained once by EmitProgram after the
// program's own top-level code finishes (see runtime.go's
// ensureTimerRuntime for the full design). An active setInterval with
// nothing ever calling clearInterval on it means that drain loop never
// finishes, matching real Node's behavior: the process only exits once
// every timer has fired-and-not-repeated or been cleared.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// timerCallbackPtr evaluates and validates arg as a Memory.free-free zero-
// argument, void-returning closure — the only callback shape this V1
// supports, matching the fixed `call void (ptr) %fp(ptr %ep)` trampoline
// shape __kml_timer_drain uses to call it back later.
func (e *Emitter) timerCallbackPtr(arg ast.Expression, fnName string, pos ast.Pos) (string, error) {
	val, err := e.emitExpr(arg)
	if err != nil {
		return "", err
	}
	if !val.Ty.IsFunc {
		return "", fmt.Errorf("%d:%d: %s's first argument must be a function", pos.Line, pos.Col, fnName)
	}
	if len(val.Ty.FuncParams) != 0 || (val.Ty.FuncRetType != nil && val.Ty.FuncRetType.IR != "void") {
		return "", fmt.Errorf("%d:%d: %s's callback must take no arguments and return nothing (() => void)", pos.Line, pos.Col, fnName)
	}
	return val.Ref, nil
}

// timerDelayArg resolves the optional delayMs argument (0 if omitted).
func (e *Emitter) timerDelayArg(args []ast.Expression, idx int) (string, error) {
	if idx >= len(args) {
		return "0", nil
	}
	val, err := e.emitExpr(args[idx])
	if err != nil {
		return "", err
	}
	val = e.coerce(val, TypeI64)
	return val.Ref, nil
}

func (e *Emitter) emitSetTimeout(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: setTimeout takes 1 or 2 arguments (callback, delayMs?)", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setTimeout", pos)
	if err != nil {
		return Value{}, err
	}
	delayRef, err := e.timerDelayArg(args, 1)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 %s, i64 0)", r, closurePtr, delayRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitSetInterval(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: setInterval takes 1 or 2 arguments (callback, delayMs?)", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setInterval", pos)
	if err != nil {
		return Value{}, err
	}
	delayRef, err := e.timerDelayArg(args, 1)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	// intervalMs == delayMs: the same cadence used for the first fire is
	// reused for every subsequent one, matching real JS's setInterval.
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 %s, i64 %s)", r, closurePtr, delayRef, delayRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitClearTimer(args []ast.Expression, fnName string, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 1 argument (id)", pos.Line, pos.Col, fnName)
	}
	idVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	idVal = e.coerce(idVal, TypeI64)
	e.ensureTimerRuntime()
	e.emitInstr(fmt.Sprintf("call void @__kml_timer_clear(i64 %s)", idVal.Ref))
	return Value{Ty: TypeVoid}, nil
}
