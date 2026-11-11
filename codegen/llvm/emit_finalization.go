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
	if ty.Nullable() && ty.IR != irPtr {
		return false
	}
	switch ty.IR {
	case "i64", "double", irPtr, "i1", "float", "i8", "i16", "i32":
		return true
	}
	return false
}

// emitNewFinalizationRegistry implements `new FinalizationRegistry<T>(cb)`.
func (e *Emitter) emitNewFinalizationRegistry(ex *ast.NewExpression) (Value, error) {
	pos := ex.GetPos()
	args, err := e.fitArgs(ex.Args, 1, pos)
	if err != nil {
		return Value{}, err
	}
	cbVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !cbVal.Ty.IsFunc() || len(cbVal.Ty.FuncParams) > 1 || len(cbVal.Ty.FuncParams) == 1 && !finregHeldBoxable(cbVal.Ty.FuncParams[0]) {
		// Any other callback (a value held in any, one of more parameters,
		// one whose held value has no box of its own):
		// callable when constructed, else Node's TypeError, then called
		// with the held value through an adapter.
		box, err := e.emitBoxValue(cbVal)
		if err != nil {
			return Value{}, err
		}
		tag, _ := e.emitUnboxTagPayload(box)
		isRef, isFn, ok := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRef, tag, kmlTagFuncRef))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, isRef, isFn))
		badL, okL := e.freshLabel("finreg.cb.bad"), e.freshLabel("finreg.cb.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
		e.emitLabel(badL)
		e.emitThrowTypeError("FinalizationRegistry: cleanup must be callable")
		e.emitLabel(okL)
		cb := e.bindValue(box, pos)
		h := ast.NewIdentifier("__kml_finreg_held", pos)
		adapter := ast.NewArrowFunction([]ast.Param{{Name: "__kml_finreg_held", Type: ast.AnyAnnotation("ts")}}, nil,
			ast.NewCallExpression(cb, []ast.Expression{h}, pos), nil, pos)
		if cbVal, err = e.emitExpr(adapter); err != nil {
			return Value{}, err
		}
	}
	// The held type: the compiled callback's own parameter type is the ground
	// truth (it fixes the invocation ABI); the explicit <T> or a default of
	// number covers a zero-parameter callback.
	heldTy := TypeI64
	if len(ex.TypeArgs) == 1 && ex.TypeArgs[0] != nil {
		heldTy = e.annotationType(ex.TypeArgs[0])
	}
	if len(cbVal.Ty.FuncParams) == 1 {
		heldTy = cbVal.Ty.FuncParams[0]
	}
	if !finregHeldBoxable(heldTy) {
		heldTy = TypeAny // (the callback reads no held value: any holds it)
	}

	n := e.finregCount
	e.finregCount++
	tramp := fmt.Sprintf("@__kml_finreg_tramp_%d", n)
	report := fmt.Sprintf("@__kml_finreg_report_%d", n)
	e.emitFinRegTrampoline(tramp, cbVal.Ty, heldTy)
	e.emitFinRegReportFn(report, heldTy)

	e.ensureFinalizationHelpers()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_finreg_create("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, i64 %d)", r, cbVal.Ref, tramp, report, e.hostHeaderWord(FinalizationRegistryType(heldTy))))
	return Value{Ref: r, Ty: FinalizationRegistryType(heldTy)}, nil
}

// emitFinRegUnbox converts the boxed i64 held back to heldTy (held itself
// when i64 is already the right shape).
func (e *Emitter) emitFinRegUnbox(held string, heldTy Type) string {
	if heldTy.IR == "i64" {
		return held
	}
	h := e.freshReg()
	switch heldTy.IR {
	case "double":
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", h, held))
	case "float":
		// Boxed as double bits (the register path widens float held values).
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", d, held))
		e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", h, d))
	case irPtr:
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, h, held))
	default: // i1, i8, i16, i32
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", h, held, heldTy.IR))
	}
	return h
}

// emitFinRegTrampoline emits the per-site `void(ptr closureHdr, i64 held)`
// wrapper the generic runtime queue invokes: unbox held to its static type,
// call the user callback with its real signature, drop any return value.
func (e *Emitter) emitFinRegTrampoline(name string, cbTy Type, heldTy Type) {
	retIR := "void"
	if cbTy.FuncRetType != nil && cbTy.FuncRetType.IR != "void" {
		retIR = cbTy.FuncRetType.IR
	}
	restore := e.beginDetachedFunc()
	fp, epP, ep := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %%cl, align 8", fp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %%cl, i64 8", epP))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ep, epP))
	callArgs := irPtr + " " + ep
	if len(cbTy.FuncParams) != 0 {
		callArgs += ", " + heldTy.IR + " " + e.emitFinRegUnbox("%held", heldTy)
	}
	if retIR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(%s)", fp, callArgs))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", e.freshReg(), retIR, fp, callArgs))
	}
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.emitGlobal(funcDef("internal void "+name+"("+irPtr+" %cl, i64 %held)", body))
}

// emitFinRegReportFn emits the per-site leak-line printer for
// -finalizers=report: `held=<value>   registered at <line>:<col>`, formatted
// by the held value's static type.
func (e *Emitter) emitFinRegReportFn(name string, heldTy Type) {
	restore := e.beginDetachedFunc()
	var format, operand string
	switch {
	case isStringTy(heldTy):
		format, operand = "%s", irPtr+" "+e.emitFinRegUnbox("%held", TypePtr)
	case heldTy.IR == "double" || heldTy.IR == "float":
		format, operand = "%g", "double "+e.emitFinRegUnbox("%held", TypeF64)
	case heldTy.IR == irPtr:
		format, operand = "%p", irPtr+" "+e.emitFinRegUnbox("%held", TypePtr)
	default: // every integer/boolean shape is boxed as a plain i64
		format, operand = "%lld", "i64 %held"
	}
	fmtStr := e.internString("  held=" + format + "   registered at %lld:%lld\n")
	e.emitInstr(fmt.Sprintf("%s = call i32 ("+irPtr+", ...) @printf("+irPtr+" %s, %s, i64 %%line, i64 %%col)", e.freshReg(), fmtStr, operand))
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.emitGlobal(funcDef("internal void "+name+"(i64 %held, i64 %line, i64 %col)", body))
}

// finregObjectArg evaluates an expression required to be an object reference
// (register's target / unregisterToken) — the same object-or-unboxed-dynamic
// rule weakObjectKey enforces for weak-collection keys.
func (e *Emitter) finregObjectArg(expr ast.Expression, what string, pos ast.Pos) (string, error) {
	v, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}
	if e.compatJS() && !v.Ty.IsDynamic() && (v.Ty.IR != irPtr || isStringTy(v.Ty)) {
		// A primitive is JavaScript's run-time TypeError, as an any's is.
		if v, err = e.emitBoxValue(v); err != nil {
			return "", err
		}
	}
	// An any/unknown holds an object at run time or not: a primitive
	// throws Node's TypeError, an object is held by its reference.
	if v.Ty.IsDynamic() {
		tag, payload := e.emitUnboxTagPayload(v)
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ugt i8 %s, %d", isObj, tag, kmlTagUndefined))
		bad, ok := e.freshLabel("finreg.badobj"), e.freshLabel("finreg.obj")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, ok, bad))
		e.emitLabel(bad)
		if err := e.emitFinregInvalid(v, what, pos); err != nil {
			return "", err
		}
		e.emitLabel(ok)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, r, payload))
		return r, nil
	}
	if v.Ty.IR != irPtr || isStringTy(v.Ty) || ownlessPrimitive(v.Ty) && !v.Ty.IsSymbol() {
		// A primitive (a symbol is held weakly, below): Node's TypeError,
		// when it runs.
		if err := e.emitFinregInvalid(v, what, pos); err != nil {
			return "", err
		}
		e.emitLabel(e.freshLabel("finreg.dead"))
		return "null", nil
	}
	if v.Ty.IsSymbol() {
		// A registered symbol (`Symbol.for`) cannot be held weakly: Node's
		// TypeError.
		e.ensureSymbolRegistry()
		key, isReg := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_symbol_keyfor("+irPtr+" %s)", key, v.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", isReg, key))
		bad, ok := e.freshLabel("finreg.badkey"), e.freshLabel("finreg.key")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isReg, bad, ok))
		e.emitLabel(bad)
		shown, err := e.emitSymbolToString(v)
		if err != nil {
			return "", err
		}
		if err := e.emitFinregInvalid(shown, what, pos); err != nil {
			return "", err
		}
		e.emitLabel(ok)
	}
	return v.Ref, nil
}

// emitFinregInvalid throws Node's TypeError for v, a value register or
// unregister cannot hold weakly: a register target's, or an
// unregisterToken's naming the value.
func (e *Emitter) emitFinregInvalid(v Value, what string, pos ast.Pos) error {
	if what != "unregisterToken" {
		e.emitThrowTypeError("FinalizationRegistry.prototype.register: invalid target")
		return nil
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, v.Ty.IR))
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", v.Ty.IR, v.Ref, slot))
	e.pushScope()
	defer e.popScope()
	e.define("__kml_finreg_val", Symbol{Ptr: slot, Ty: v.Ty})
	msg, err := e.emitExpr(ast.NewBinaryExpression("+", ast.NewBinaryExpression("+", ast.NewStringLiteral("Invalid unregisterToken ('", pos), ast.NewIdentifier("__kml_finreg_val", pos), pos), ast.NewStringLiteral("')", pos), pos))
	if err != nil {
		return err
	}
	e.emitThrowTypeErrorValue(msg.Ref)
	return nil
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

	c := &finregCall{objExpr: objExpr, method: method, args: args, pos: pos, regVal: regVal, heldTy: heldTy}
	if f, ok := finregMethods[method]; ok {
		return f(e, c)
	}
	// No such method (JS): its arguments evaluated, then the call's
	// TypeError.
	for _, a := range args {
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, err
		}
	}
	e.emitThrowTypeError(calleeDisplayName(ast.NewMemberExpression(objExpr, method, pos)) + " is not a function")
	e.emitLabel(e.freshLabel("finreg.nomethod"))
	return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
}

type finregCall struct {
	objExpr ast.Expression
	method  string
	args    []ast.Expression
	pos     ast.Pos
	regVal  Value
	heldTy  Type
}

// finregMethods are a FinalizationRegistry's methods, by name.
var finregMethods map[string]func(e *Emitter, c *finregCall) (Value, error)

func init() {
	finregMethods = map[string]func(e *Emitter, c *finregCall) (Value, error){
		"register":   (*Emitter).finregRegister,
		"unregister": (*Emitter).finregUnregister,
	}
}

// finregRegister is register().
func (e *Emitter) finregRegister(c *finregCall) (Value, error) {
	if len(c.args) == 0 {
		// No target: the run-time TypeError of a non-object target.
		e.emitThrowTypeError("FinalizationRegistry.prototype.register: invalid target")
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	target, err := e.finregObjectArg(c.args[0], "register target", c.pos)
	if err != nil {
		return Value{}, err
	}
	heldVal := Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny} // left out: undefined
	if len(c.args) >= 2 {
		if heldVal, err = e.emitExpr(c.args[1]); err != nil {
			return Value{}, err
		}
	}
	var same string
	switch {
	case heldVal.Ty.IsDynamic():
		tag, payload := e.emitUnboxTagPayload(heldVal)
		isObj, tp, eq := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ugt i8 %s, %d", isObj, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", tp, target))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", eq, payload, tp))
		same = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", same, isObj, eq))
	case heldVal.Ty.IR == irPtr && !isStringTy(heldVal.Ty):
		same = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, %s", same, target, heldVal.Ref))
	}
	if same != "" {
		// The holdings being the target itself is a TypeError.
		badL, okL := e.freshLabel("finreg.same"), e.freshLabel("finreg.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", same, badL, okL))
		e.emitLabel(badL)
		e.emitThrowTypeError("FinalizationRegistry.prototype.register: target and holdings must not be same")
		e.emitLabel(okL)
	}
	for _, extra := range c.args[min(len(c.args), 3):] {
		if _, err := e.emitExpr(extra); err != nil { // evaluated, then ignored
			return Value{}, err
		}
	}
	heldVal = e.convert(heldVal, c.heldTy)
	var heldBits string
	if c.heldTy.IR == "float" {
		// Box float held values as double bits (emitFinRegUnbox mirrors this).
		wide := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", wide, heldVal.Ref))
		heldBits = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", heldBits, wide))
	} else {
		heldBits = e.valueToMapVal(heldVal, c.heldTy)
	}
	token := "null"
	if len(c.args) == 3 {
		token, err = e.finregObjectArg(c.args[2], "unregisterToken", c.pos)
		if err != nil {
			return Value{}, err
		}
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_finreg_register("+irPtr+" %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s, i64 %d, i64 %d)",
		c.regVal.Ref, target, heldBits, token, c.pos.Line, c.pos.Col))
	// Per spec, register() evaluates to `undefined` — return a real undefined
	// value (not a Ref-less void) so a caller that uses the result
	// (`x = fr.register(...)`, `assert(fr.register(...) === undefined)`) has a
	// value to read rather than an empty operand.
	return Value{Ref: "null", Ty: TypeUndefined}, nil
}

// finregUnregister is unregister().
func (e *Emitter) finregUnregister(c *finregCall) (Value, error) {
	args, err := e.fitArgs(c.args, 1, c.pos)
	if err != nil {
		return Value{}, err
	}
	c.args = args
	token, err := e.finregObjectArg(c.args[0], "unregisterToken", c.pos)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_finreg_unregister("+irPtr+" %s, "+irPtr+" %s)", r, c.regVal.Ref, token))
	return Value{Ref: r, Ty: TypeBool}, nil
}
