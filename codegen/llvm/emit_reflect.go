package llvm

// emit_reflect.go — Reflect.* and Proxy glue (TDD-00155 Stage 7). Reflect
// maps 1:1 onto the __kml_dynobj_* runtime, with the spec's boolean-result
// (non-throwing) forms for set/deleteProperty/setPrototypeOf/
// preventExtensions. Proxy construction builds a special bag header (the
// PROXY flag, target at the proto slot, handler at the props slot) that the
// runtime get/set/has/delete entry points trap on — see runtime_dynobj.go.

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitReflectCall dispatches Reflect.<method>(args).
func (e *Emitter) emitReflectCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureDynObj()
	argBag := func(i int, what string) (string, error) {
		return e.emitDynBagOrThrow(args[i], what, pos)
	}
	need := func(n int) error {
		if len(args) != n {
			return fmt.Errorf("%d:%d: Reflect.%s takes %d argument(s)", pos.Line, pos.Col, method, n)
		}
		return nil
	}
	switch method {
	case "get":
		if len(args) != 2 && len(args) != 3 {
			return Value{}, fmt.Errorf("%d:%d: Reflect.get takes 2 or 3 arguments", pos.Line, pos.Col)
		}
		v, err := e.emitExprWithObjectHint(args[0], TypeAny)
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMemberGet(v, keyRef, pos)
	case "set":
		if len(args) != 3 && len(args) != 4 {
			return Value{}, fmt.Errorf("%d:%d: Reflect.set takes 3 or 4 arguments", pos.Line, pos.Col)
		}
		bag, err := argBag(0, "Reflect.set")
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		val, err := e.emitExprWithObjectHint(args[2], TypeAny)
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(val)
		if err != nil {
			return Value{}, err
		}
		status := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", status, bag, keyRef, boxed.Ref))
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", ok, status))
		return Value{Ref: ok, Ty: TypeBool}, nil
	case "has":
		if err := need(2); err != nil {
			return Value{}, err
		}
		v, err := e.emitExprWithObjectHint(args[0], TypeAny)
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyHas(v, keyRef, false, pos)
	case "deleteProperty":
		if err := need(2); err != nil {
			return Value{}, err
		}
		bag, err := argBag(0, "Reflect.deleteProperty")
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_delete(ptr %s, ptr %s)", r, bag, keyRef))
		return Value{Ref: r, Ty: TypeBool}, nil
	case "ownKeys":
		if err := need(1); err != nil {
			return Value{}, err
		}
		return e.emitObjectGetOwnPropertyNames(args, pos)
	case "getPrototypeOf":
		if err := need(1); err != nil {
			return Value{}, err
		}
		return e.emitObjectGetPrototypeOf(args, pos)
	case "setPrototypeOf":
		if err := need(2); err != nil {
			return Value{}, err
		}
		bag, err := argBag(0, "Reflect.setPrototypeOf")
		if err != nil {
			return Value{}, err
		}
		protoVal, err := e.emitExprWithObjectHint(args[1], TypeAny)
		if err != nil {
			return Value{}, err
		}
		protoReg, okReg, err := e.emitDynProtoOperand(protoVal, true)
		if err != nil {
			return Value{}, err
		}
		_ = okReg
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_set_proto(ptr %s, ptr %s)", r, bag, protoReg))
		return Value{Ref: r, Ty: TypeBool}, nil
	case "isExtensible":
		if err := need(1); err != nil {
			return Value{}, err
		}
		v, err := e.emitExprWithObjectHint(args[0], TypeAny)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynFlagsTest(v, 0)
	case "preventExtensions":
		if err := need(1); err != nil {
			return Value{}, err
		}
		v, err := e.emitExprWithObjectHint(args[0], TypeAny)
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitDynPrevent(v, 0); err != nil {
			return Value{}, err
		}
		return Value{Ref: "true", Ty: TypeBool}, nil
	case "defineProperty":
		// The boolean-result form; a spec violation still throws in V1
		// (disclosed) rather than returning false.
		if _, err := e.emitObjectDefineProperty(args, pos); err != nil {
			return Value{}, err
		}
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: Reflect has no method '%s'", pos.Line, pos.Col, method)
}

// emitNewProxy implements `new Proxy(target, handler)` (TDD-00155 Stage 7):
// a special bag header — PROXY flag, target where a bag keeps its proto,
// handler where it keeps its props, zero count — that the runtime property
// entry points trap on. `typeof` answers "object" (it is a tag-10 value).
func (e *Emitter) emitNewProxy(ex *ast.NewExpression) (Value, error) {
	if len(ex.Args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: new Proxy takes 2 arguments (target, handler)", ex.GetPos().Line, ex.GetPos().Col)
	}
	e.ensureDynObj()
	target, err := e.emitDynBagOrThrow(ex.Args[0], "Proxy target", ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	handler, err := e.emitDynBagOrThrow(ex.Args[1], "Proxy handler", ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	e.ensureCalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 40)", hdr))
	// magic | PROXY (1<<33)
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", 1145867595|int64(1)<<33, hdr))
	tSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", tSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", target, tSlot))
	hSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", hSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", handler, hSlot))
	return e.emitDynObjBox(hdr), nil
}
