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
		if !v.Ty.IsDynamic {
			return Value{}, fmt.Errorf("%d:%d: Reflect.get requires a dynamic (any-typed) object", pos.Line, pos.Col)
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
		if !v.Ty.IsDynamic {
			return Value{}, fmt.Errorf("%d:%d: Reflect.has requires a dynamic (any-typed) object", pos.Line, pos.Col)
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
	case "defineMetadata":
		// Reflect.defineMetadata(metadataKey, metadataValue, target[, propertyKey])
		if len(args) != 3 && len(args) != 4 {
			return Value{}, fmt.Errorf("%d:%d: Reflect.defineMetadata takes 3 or 4 arguments", pos.Line, pos.Col)
		}
		metaKeyRef, err := e.dynAnyKeyRef(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		val, err := e.emitExprWithObjectHint(args[1], TypeAny)
		if err != nil {
			return Value{}, err
		}
		valBox, err := e.emitBoxValue(val)
		if err != nil {
			return Value{}, err
		}
		targetBag, err := e.reflectMetaTargetBag(args[2], pos)
		if err != nil {
			return Value{}, err
		}
		propKeyRef, err := e.reflectMetaPropKey(args, 3, pos)
		if err != nil {
			return Value{}, err
		}
		e.emitDefineMetadata(targetBag, propKeyRef, metaKeyRef, valBox.Ref)
		return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, nil
	case "getMetadata", "getOwnMetadata":
		// V1 does not walk the prototype chain, so getMetadata == getOwnMetadata.
		if len(args) != 2 && len(args) != 3 {
			return Value{}, fmt.Errorf("%d:%d: Reflect.%s takes 2 or 3 arguments", pos.Line, pos.Col, method)
		}
		metaKeyRef, err := e.dynAnyKeyRef(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		targetBag, err := e.reflectMetaTargetBag(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		propKeyRef, err := e.reflectMetaPropKey(args, 2, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitGetMetadata(targetBag, propKeyRef, metaKeyRef), nil
	case "hasMetadata", "hasOwnMetadata":
		if len(args) != 2 && len(args) != 3 {
			return Value{}, fmt.Errorf("%d:%d: Reflect.%s takes 2 or 3 arguments", pos.Line, pos.Col, method)
		}
		metaKeyRef, err := e.dynAnyKeyRef(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		targetBag, err := e.reflectMetaTargetBag(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		propKeyRef, err := e.reflectMetaPropKey(args, 2, pos)
		if err != nil {
			return Value{}, err
		}
		got := e.emitGetMetadata(targetBag, propKeyRef, metaKeyRef)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", r, got.Ref, nbUndefined))
		return Value{Ref: r, Ty: TypeBool}, nil
	case "metadata":
		// Reflect.metadata(key, value) returns a decorator; used manually it
		// hits the same returned-value-call gap factory decorators do. The
		// compiler-emitted design:* metadata does not go through it.
		return Value{}, fmt.Errorf("%d:%d: Reflect.metadata (used as a decorator factory) is not supported yet — define metadata directly with Reflect.defineMetadata, or use -emit-decorator-metadata for design:* metadata", pos.Line, pos.Col)
	}
	return Value{}, fmt.Errorf("%d:%d: Reflect has no method '%s'", pos.Line, pos.Col, method)
}

// reflectMetaTargetBag resolves a Reflect metadata `target` argument to the raw
// ptr of its dynamic-object bag, rejecting a non-dynamic target (metadata is
// stored on the dynamic decorator target, TDD-00161 Stage 3).
func (e *Emitter) reflectMetaTargetBag(arg ast.Expression, pos ast.Pos) (string, error) {
	v, err := e.emitExprWithObjectHint(arg, TypeAny)
	if err != nil {
		return "", err
	}
	if !v.Ty.IsDynamic {
		return "", fmt.Errorf("%d:%d: a Reflect metadata target must be a dynamic (any-typed) object", pos.Line, pos.Col)
	}
	// The metadata store is a D1 bag operation — the runtime value must be an
	// actual tag-10 dynamic object, not a boxed static class instance (tag 6),
	// whose pointer these bag functions would corrupt. Throw a clear TypeError
	// rather than segfault on misuse.
	tag, pay := e.emitUnboxTagPayload(v)
	okL, badL := e.emitTagCheck(tag, kmlTagDynObject, "meta.tgt")
	contL := e.freshLabel("meta.tgt.cont")
	e.emitLabel(okL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
	e.emitLabel(badL)
	e.emitThrowTypeError("Reflect metadata target must be a plain object")
	e.emitLabel(contL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, pay))
	return bag, nil
}

// reflectMetaPropKey returns the interned key ref for a metadata property key
// argument at index i, or the constructor sentinel when the argument is absent.
func (e *Emitter) reflectMetaPropKey(args []ast.Expression, i int, pos ast.Pos) (string, error) {
	if i < len(args) {
		return e.dynAnyKeyRef(args[i], pos)
	}
	return e.internString(metaCtorKey), nil
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
