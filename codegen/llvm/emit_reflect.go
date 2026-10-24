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

// The Reflect functions: one emitter each, registered as the Reflect.*
// intrinsics with their result types (reflectIntrinsics).
func init() {
	for name, emit := range map[string]func(e *Emitter, method string, args []ast.Expression, pos ast.Pos) (Value, error){
		"apply":                    (*Emitter).emitReflectApply,
		"get":                      (*Emitter).emitReflectGet,
		"set":                      (*Emitter).emitReflectSet,
		"has":                      (*Emitter).emitReflectHas,
		"deleteProperty":           (*Emitter).emitReflectDeleteProperty,
		"construct":                (*Emitter).emitReflectConstruct,
		"getOwnPropertyDescriptor": (*Emitter).emitReflectGetOwnPropertyDescriptor,
		"ownKeys":                  (*Emitter).emitReflectOwnKeysCall,
		"getPrototypeOf":           (*Emitter).emitReflectGetPrototypeOf,
		"setPrototypeOf":           (*Emitter).emitReflectSetPrototypeOf,
		"isExtensible":             (*Emitter).emitReflectIsExtensible,
		"preventExtensions":        (*Emitter).emitReflectPreventExtensions,
		"defineProperty":           (*Emitter).emitReflectDefineProperty,
		"defineMetadata":           (*Emitter).emitReflectDefineMetadata,
		"getMetadata":              (*Emitter).emitReflectGetMetadata,
		"getOwnMetadata":           (*Emitter).emitReflectGetMetadata,
		"hasMetadata":              (*Emitter).emitReflectHasMetadata,
		"hasOwnMetadata":           (*Emitter).emitReflectHasMetadata,
		"metadata":                 (*Emitter).emitReflectMetadata,
	} {
		method, emit, ty := name, emit, reflectIntrinsics[name]
		intrinsics["Reflect."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				e.ensureDynObj()
				return emit(e, method, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return ty },
		}
	}
}

// reflectNeed checks a Reflect function's argument count.
func reflectNeed(method string, args []ast.Expression, pos ast.Pos, n int) error {
	if len(args) != n {
		return fmt.Errorf("%d:%d: Reflect.%s takes %d argument(s)", pos.Line, pos.Col, method, n)
	}
	return nil
}

// reflectArgBag is argument i's dynamic bag, or a TypeError.
func (e *Emitter) reflectArgBag(args []ast.Expression, pos ast.Pos, i int, what string) (string, error) {
	return e.emitDynBagOrThrow(args[i], what, pos)
}

func (e *Emitter) emitReflectApply(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Reflect.apply(f, thisArg, args): f called with thisArg and the
	// elements of args.
	if err := reflectNeed(method, args, pos, 3); err != nil {
		return Value{}, err
	}
	fv, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	fb, err := e.emitBoxValue(fv)
	if err != nil {
		return Value{}, err
	}
	tv, err := e.emitExprWithObjectHint(args[1], TypeAny)
	if err != nil {
		return Value{}, err
	}
	argv, n, err := e.emitDynArgv([]ast.Expression{ast.NewSpreadElement(args[2], args[2].GetPos())}, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFnBoxCallN(fb, tv, argv, n, "Function.prototype.apply was called on a value that is not a function", pos)
}

func (e *Emitter) emitReflectGet(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: Reflect.get takes 2 or 3 arguments", pos.Line, pos.Col)
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !v.Ty.IsDynamic() {
		return Value{}, fmt.Errorf("%d:%d: Reflect.get requires a dynamic (any-typed) object", pos.Line, pos.Col)
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynAnyMemberGet(v, keyRef, pos)
}

func (e *Emitter) emitReflectSet(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 && len(args) != 4 {
		return Value{}, fmt.Errorf("%d:%d: Reflect.set takes 3 or 4 arguments", pos.Line, pos.Col)
	}
	bag, err := e.reflectArgBag(args, pos, 0, "Reflect.set")
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
}

func (e *Emitter) emitReflectHas(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !v.Ty.IsDynamic() {
		return Value{}, fmt.Errorf("%d:%d: Reflect.has requires a dynamic (any-typed) object", pos.Line, pos.Col)
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynAnyHas(v, keyRef, false, pos)
}

func (e *Emitter) emitReflectDeleteProperty(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	bag, err := e.reflectArgBag(args, pos, 0, "Reflect.deleteProperty")
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
}

func (e *Emitter) emitReflectConstruct(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Reflect.construct(C, args): `new C(...args)` through the run-time
	// constructor dispatch.
	if len(args) != 2 && len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: Reflect.construct takes 2 or 3 arguments", pos.Line, pos.Col)
	}
	cv, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.emitBoxValue(cv)
	if err != nil {
		return Value{}, err
	}
	if len(args) == 3 {
		// A newTarget other than the target itself (or undefined) would
		// give the instance another constructor's prototype, which a
		// class instance here cannot take: a TypeError, never a silent
		// instance of the target.
		nv, err := e.emitExprWithObjectHint(args[2], TypeAny)
		if err != nil {
			return Value{}, err
		}
		nb, err := e.emitBoxValue(nv)
		if err != nil {
			return Value{}, err
		}
		e.ensureAnyEq()
		undef, same, ok := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", undef, nb.Ref, nbUndefined))
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_eq(i64 %s, i64 %s)", same, nb.Ref, cb.Ref))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, undef, same))
		okL, badL := e.freshLabel("refl.nt.ok"), e.freshLabel("refl.nt.bad")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
		e.emitLabel(badL)
		e.emitThrowTypeError("Reflect.construct with a newTarget other than the target is not supported")
		e.emitLabel(okL)
	}
	argv, n, err := e.emitDynArgv([]ast.Expression{ast.NewSpreadElement(args[1], args[1].GetPos())}, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureNanBox()
	e.usedDynConstruct = true
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dyn_construct(i64 %s, ptr %s, i64 %s, ptr %s)", r, cb.Ref, argv, n, e.internString("Reflect.construct target is not a constructor")))
	return Value{Ref: r, Ty: TypeAny}, nil
}

func (e *Emitter) emitReflectGetOwnPropertyDescriptor(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	return e.emitObjectGetOwnPropertyDescriptor(args, pos)
}

func (e *Emitter) emitReflectOwnKeysCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
		return Value{}, err
	}
	return e.emitReflectOwnKeys(args[0], pos)
}

func (e *Emitter) emitReflectGetPrototypeOf(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
		return Value{}, err
	}
	return e.emitObjectGetPrototypeOf(args, pos)
}

func (e *Emitter) emitReflectSetPrototypeOf(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	bag, err := e.reflectArgBag(args, pos, 0, "Reflect.setPrototypeOf")
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
}

func (e *Emitter) emitReflectIsExtensible(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
		return Value{}, err
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFlagsTest(v, 0)
}

func (e *Emitter) emitReflectPreventExtensions(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
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
}

func (e *Emitter) emitReflectDefineProperty(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// The boolean-result form; a spec violation still throws in V1
	// (disclosed) rather than returning false.
	if _, err := e.emitObjectDefineProperty(args, pos); err != nil {
		return Value{}, err
	}
	return Value{Ref: "true", Ty: TypeBool}, nil
}

func (e *Emitter) emitReflectDefineMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
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
}

func (e *Emitter) emitReflectGetMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
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
}

func (e *Emitter) emitReflectHasMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
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
}

func (e *Emitter) emitReflectMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Reflect.metadata(key, value) returns a decorator; used manually it
	// hits the same returned-value-call gap factory decorators do. The
	// compiler-emitted design:* metadata does not go through it.
	return Value{}, fmt.Errorf("%d:%d: Reflect.metadata (used as a decorator factory) is not supported yet — define metadata directly with Reflect.defineMetadata, or use -emit-decorator-metadata for design:* metadata", pos.Line, pos.Col)
}

// reflectMetaTargetBag resolves a Reflect metadata `target` argument to the raw
// ptr of its dynamic-object bag, rejecting a non-dynamic target (metadata is
// stored on the dynamic decorator target, TDD-00161 Stage 3).
func (e *Emitter) reflectMetaTargetBag(arg ast.Expression, pos ast.Pos) (string, error) {
	v, err := e.emitExprWithObjectHint(arg, TypeAny)
	if err != nil {
		return "", err
	}
	if !v.Ty.IsDynamic() {
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
	// A Proxy's [[OwnPropertyKeys]]/[[GetOwnProperty]] live in shape.c,
	// which reads an ownKeys result through the array runtimes.
	e.ensureShapeRuntime()
	e.ensureDynJSONC()
	e.ensureDynArr()
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
	e.heapCallocInto(hdr, "1", "40")
	// magic | PROXY (1<<33)
	e.heapStore("i64", fmt.Sprint(1145867595|int64(1)<<33), hdr, 8)
	tSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", tSlot, hdr))
	e.heapStore("ptr", target, tSlot, 8)
	hSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", hSlot, hdr))
	e.heapStore("ptr", handler, hSlot, 8)
	return e.emitDynObjBox(hdr), nil
}
