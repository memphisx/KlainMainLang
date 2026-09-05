// emit_finalization.go — FinalizationRegistry<T> construction and method
// dispatch (TDD-00163). The runtime (registration list, death signals,
// pending-callback queue, exit flush) lives in runtime_finalization.go; this
// file owns the per-construction-site pieces — the trampoline that unboxes
// the i64-boxed held value back to its static type before invoking the user
// callback, and the leak-report line printer -finalizers=report uses.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// finregHeldBoxable reports whether T can ride the i64 held-value box (the
// same representation trick WeakMap values use): any scalar — number, int
// sizes, float sizes, boolean — or a single-pointer value (string, object,
// class instance). Aggregates (arrays, nullable scalars) are out for V1.
func finregHeldBoxable(ty Type) bool {
	if ty.Nullable && ty.IR != "ptr" {
		return false
	}
	switch ty.IR {
	case "i64", "double", "ptr", "i1", "float", "i8", "i16", "i32":
		return true
	}
	return false
}

// emitNewFinalizationRegistry implements `new FinalizationRegistry<T>(cb)`.
func (e *Emitter) emitNewFinalizationRegistry(ex *ast.NewExpression) (Value, error) {
	pos := ex.GetPos()
	if len(ex.Args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: new FinalizationRegistry(cleanupCallback) takes exactly 1 argument", pos.Line, pos.Col)
	}
	cbVal, err := e.emitExpr(ex.Args[0])
	if err != nil {
		return Value{}, err
	}
	if !cbVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: new FinalizationRegistry's argument must be a function (the cleanup callback)", pos.Line, pos.Col)
	}
	if len(cbVal.Ty.FuncParams) > 1 {
		return Value{}, fmt.Errorf("%d:%d: the FinalizationRegistry cleanup callback takes at most one parameter (the held value)", pos.Line, pos.Col)
	}
	// The held type: the compiled callback's own parameter type is the ground
	// truth (it fixes the invocation ABI); the explicit <T> or a default of
	// number covers a zero-parameter callback.
	heldTy := TypeI64
	if len(ex.TypeArgs) == 1 && ex.TypeArgs[0] != nil {
		heldTy = e.resolveType(ex.TypeArgs[0])
	}
	if len(cbVal.Ty.FuncParams) == 1 {
		heldTy = cbVal.Ty.FuncParams[0]
	}
	if !finregHeldBoxable(heldTy) {
		return Value{}, fmt.Errorf("%d:%d: this held-value type is not supported by FinalizationRegistry yet — use a scalar (number/boolean), a string, or an object", pos.Line, pos.Col)
	}

	n := e.finregCount
	e.finregCount++
	tramp := fmt.Sprintf("@__kml_finreg_tramp_%d", n)
	report := fmt.Sprintf("@__kml_finreg_report_%d", n)
	e.emitFinRegTrampoline(tramp, cbVal.Ty, heldTy)
	e.emitFinRegReportFn(report, heldTy)

	e.ensureFinalizationHelpers()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_finreg_create(ptr %s, ptr %s, ptr %s)", r, cbVal.Ref, tramp, report))
	return Value{Ref: r, Ty: FinalizationRegistryType(heldTy)}, nil
}

// finregUnboxIR returns the instructions converting the boxed `%held` i64
// back to heldTy in register %h (empty when i64 is already the right shape,
// in which case the returned operand is %held itself).
func finregUnboxIR(heldTy Type) (conv, operand string) {
	switch heldTy.IR {
	case "i64":
		return "", "%held"
	case "double":
		return "  %h = bitcast i64 %held to double\n", "%h"
	case "float":
		// Boxed as double bits (the register path widens float held values).
		return "  %hd = bitcast i64 %held to double\n  %h = fptrunc double %hd to float\n", "%h"
	case "ptr":
		return "  %h = inttoptr i64 %held to ptr\n", "%h"
	default: // i1, i8, i16, i32
		return fmt.Sprintf("  %%h = trunc i64 %%held to %s\n", heldTy.IR), "%h"
	}
}

// emitFinRegTrampoline emits the per-site `void(ptr closureHdr, i64 held)`
// wrapper the generic runtime queue invokes: unbox held to its static type,
// call the user callback with its real signature, drop any return value.
func (e *Emitter) emitFinRegTrampoline(name string, cbTy Type, heldTy Type) {
	retIR := "void"
	if cbTy.FuncRetType != nil && cbTy.FuncRetType.IR != "void" {
		retIR = cbTy.FuncRetType.IR
	}
	conv, operand := finregUnboxIR(heldTy)
	callArgs := fmt.Sprintf("ptr %%ep, %s %s", heldTy.IR, operand)
	if len(cbTy.FuncParams) == 0 {
		callArgs = "ptr %ep"
		conv = ""
	}
	call := fmt.Sprintf("  call %s %%fp(%s)\n", retIR, callArgs)
	if retIR != "void" {
		call = fmt.Sprintf("  %%ign = call %s %%fp(%s)\n", retIR, callArgs)
	}
	e.emitGlobal(fmt.Sprintf(`
define internal void %s(ptr %%cl, i64 %%held) {
entry:
  %%fp = load ptr, ptr %%cl, align 8
  %%ep_p = getelementptr i8, ptr %%cl, i64 8
  %%ep = load ptr, ptr %%ep_p, align 8
%s%s  ret void
}`, name, conv, call))
}

// emitFinRegReportFn emits the per-site leak-line printer for
// -finalizers=report: `held=<value>   registered at <line>:<col>`, formatted
// by the held value's static type.
func (e *Emitter) emitFinRegReportFn(name string, heldTy Type) {
	e.ensurePrintf()
	var format, conv, operand string
	switch {
	case isStringTy(heldTy):
		format, conv, operand = "%s", "  %h = inttoptr i64 %held to ptr\n", "ptr %h"
	case heldTy.IR == "double" || heldTy.IR == "float":
		format, conv, operand = "%g", "  %h = bitcast i64 %held to double\n", "double %h"
	case heldTy.IR == "ptr":
		format, conv, operand = "%p", "  %h = inttoptr i64 %held to ptr\n", "ptr %h"
	default: // every integer/boolean shape is boxed as a plain i64
		format, conv, operand = "%lld", "", "i64 %held"
	}
	fmtStr := e.internString("  held=" + format + "   registered at %lld:%lld\n")
	e.emitGlobal(fmt.Sprintf(`
define internal void %s(i64 %%held, i64 %%line, i64 %%col) {
entry:
%s  %%rc = call i32 (ptr, ...) @printf(ptr %s, %s, i64 %%line, i64 %%col)
  ret void
}`, name, conv, fmtStr, operand))
}

// finregObjectArg evaluates an expression required to be an object reference
// (register's target / unregisterToken) — the same object-or-unboxed-dynamic
// rule weakObjectKey enforces for weak-collection keys.
func (e *Emitter) finregObjectArg(expr ast.Expression, what string, pos ast.Pos) (string, error) {
	v, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}
	if v.Ty.IsDynamic && e.compatJS() {
		return e.coerce(v, TypePtr).Ref, nil
	}
	if v.Ty.IR != "ptr" || isStringTy(v.Ty) {
		return "", fmt.Errorf("%d:%d: FinalizationRegistry's %s must be an object (not a primitive)", pos.Line, pos.Col, what)
	}
	return v.Ref, nil
}

// emitFinalizationRegistryMethod dispatches register/unregister.
func (e *Emitter) emitFinalizationRegistryMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	regVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	heldTy := TypeI64
	if regVal.Ty.ElemType != nil {
		heldTy = *regVal.Ty.ElemType
	}
	e.ensureFinalizationHelpers()

	switch method {
	case "register":
		if len(args) < 2 || len(args) > 3 {
			return Value{}, fmt.Errorf("%d:%d: FinalizationRegistry.register(target, heldValue, unregisterToken?) takes 2 or 3 arguments", pos.Line, pos.Col)
		}
		// held === target keeps the target reachable from the registry under
		// gc, so it could never fire — reject the statically-obvious case.
		if tid, ok := args[0].(*ast.Identifier); ok {
			if hid, ok2 := args[1].(*ast.Identifier); ok2 && tid.Name == hid.Name {
				return Value{}, fmt.Errorf("%d:%d: FinalizationRegistry.register's held value must not be the target itself (it would keep the target alive and the callback could never fire)", pos.Line, pos.Col)
			}
		}
		target, err := e.finregObjectArg(args[0], "register target", pos)
		if err != nil {
			return Value{}, err
		}
		heldVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		heldVal = e.coerce(heldVal, heldTy)
		var heldBits string
		if heldTy.IR == "float" {
			// Box float held values as double bits (finregUnboxIR mirrors this).
			wide := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", wide, heldVal.Ref))
			heldBits = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", heldBits, wide))
		} else {
			heldBits = e.valueToMapVal(heldVal, heldTy)
		}
		token := "null"
		if len(args) == 3 {
			token, err = e.finregObjectArg(args[2], "unregisterToken", pos)
			if err != nil {
				return Value{}, err
			}
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_finreg_register(ptr %s, ptr %s, i64 %s, ptr %s, i64 %d, i64 %d)",
			regVal.Ref, target, heldBits, token, pos.Line, pos.Col))
		return Value{Ty: TypeVoid}, nil

	case "unregister":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: FinalizationRegistry.unregister(unregisterToken) takes exactly 1 argument", pos.Line, pos.Col)
		}
		token, err := e.finregObjectArg(args[0], "unregisterToken", pos)
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_finreg_unregister(ptr %s, ptr %s)", r, regVal.Ref, token))
		return Value{Ref: r, Ty: TypeBool}, nil

	case "cleanupSome":
		return Value{}, fmt.Errorf("%d:%d: FinalizationRegistry.cleanupSome is not supported (non-standard); pending cleanup callbacks run on the event loop and at exit", pos.Line, pos.Col)
	}
	return Value{}, fmt.Errorf("%d:%d: FinalizationRegistry has no method '%s' (register, unregister)", pos.Line, pos.Col, method)
}
