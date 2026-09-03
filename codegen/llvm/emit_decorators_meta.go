package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// demangleModuleName strips the resolver's per-file `__kml_mod<N>` suffix so a
// class's design-type descriptor carries its source name (`Dep`, not
// `Dep__kml_mod0`).
func demangleModuleName(name string) string {
	if i := strings.Index(name, "__kml_mod"); i >= 0 {
		return name[:i]
	}
	return name
}

// emit_decorators_meta.go — TypeScript's emitDecoratorMetadata (TDD-00161
// Stage 3). With the `-emit-decorator-metadata` flag, a decorated member gets
// `design:type` / `design:paramtypes` / `design:returntype` reflection
// metadata, stored on the decorator `target` object and readable through
// Reflect.getMetadata / getOwnMetadata / hasMetadata.
//
// Storage model (reuses D1, no new runtime): the target dynamic object carries
// a reserved `\x01meta` bag; that bag maps a property key (the member name, or
// a constructor sentinel) to a per-property bag; the per-property bag maps a
// metadata key to its value. Get/define/has all navigate this nesting.
//
// Design-type values are name-carrying descriptor objects (`{ name: "Number" }`
// etc.); for a class-typed position the name is the class name. These are not
// the real runtime constructors (this compiler has no first-class class
// constructor value), so identity checks against a global `Number`/class fail —
// consumers that read `.name` work; that limit is documented.

const (
	metaRootKey = "\x01meta"      // reserved property on target holding the metadata root bag
	metaCtorKey = "\x01ctor"      // per-property key standing in for the constructor (no propertyKey)
)

// designTypeName maps a static type to TypeScript's design-type constructor
// name, or "" for a position whose design type is `undefined` (void/never).
func designTypeName(ty Type) string {
	switch {
	case ty.IR == "void" || ty.IR == "" || ty.IsNever || ty.IsUndefined || ty.IsNull:
		return ""
	case ty.Float:
		return "Number"
	case ty.IR == "i1":
		return "Boolean"
	case ty.IsBigInt:
		return "BigInt"
	case ty.IsDate:
		return "Date"
	case ty.IsPromise:
		return "Promise"
	case ty.IsArray:
		return "Array"
	case ty.IsFunc:
		return "Function"
	case ty.ClassName != "":
		return demangleModuleName(ty.ClassName)
	case isStringTy(ty):
		return "String"
	case ty.IR == "i8" || ty.IR == "i16" || ty.IR == "i32" || ty.IR == "i64":
		return "Number"
	default:
		return "Object" // any/unknown/object/union/interface
	}
}

// emitDesignTypeDescriptor builds the boxed design-type value for a type: a
// `{ name }` dynamic object, or boxed `undefined` for a void/never position.
func (e *Emitter) emitDesignTypeDescriptor(ty Type) Value {
	name := designTypeName(ty)
	if name == "" {
		return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	}
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	nameBox := e.emitNbTagPtr(e.internString(name), kmlTagString)
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, bag, e.internString("name"), nameBox))
	return Value{Ref: e.emitNbTagPtr(bag, kmlTagDynObject), Ty: TypeAny}
}

// emitDesignParamtypes builds the boxed `design:paramtypes` value: a dynamic
// array of one design-type descriptor per parameter.
func (e *Emitter) emitDesignParamtypes(paramTypes []Type) Value {
	e.ensureDynObj()
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 0)", arr))
	for _, pty := range paramTypes {
		d := e.emitDesignTypeDescriptor(pty)
		e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", arr, d.Ref))
	}
	return Value{Ref: e.emitNbTagPtr(arr, kmlTagDynArray), Ty: TypeAny}
}

// emitDynGetOrCreateBag returns the ptr of the child dynamic object at
// parentBag[keyRef], creating (and storing) a fresh one if the slot is absent or
// not a dynamic object. Used to lazily grow the metadata nesting.
func (e *Emitter) emitDynGetOrCreateBag(parentBag, keyRef string) string {
	e.ensureDynObj()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", cur, parentBag, keyRef))
	tag, pay := e.emitUnboxTagPayload(Value{Ref: cur, Ty: TypeAny})
	isBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
	haveL := e.freshLabel("meta.have")
	makeL := e.freshLabel("meta.make")
	doneL := e.freshLabel("meta.done")
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, haveL, makeL))
	e.emitLabel(haveL)
	existing := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", existing, pay))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", existing, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(makeL)
	fresh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", fresh))
	freshBox := e.emitNbTagPtr(fresh, kmlTagDynObject)
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, parentBag, keyRef, freshBox))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fresh, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, resPtr))
	return out
}

// metaPerPropBag navigates target → metaRoot → per-property bag, creating each
// level. Returns the per-property bag ptr, or "" (and false) if target is not a
// dynamic object at runtime is not distinguished here — the caller guarantees a
// dynamic target (the decorator target is always a dynamic object).
func (e *Emitter) metaPerPropBag(targetBag string, propKeyRef string) string {
	root := e.emitDynGetOrCreateBag(targetBag, e.internString(metaRootKey))
	return e.emitDynGetOrCreateBag(root, propKeyRef)
}

// emitDefineMetadata stores value under (target, propKey, metaKey).
func (e *Emitter) emitDefineMetadata(targetBagPtr, propKeyRef, metaKeyRef, valueBox string) {
	perProp := e.metaPerPropBag(targetBagPtr, propKeyRef)
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, perProp, metaKeyRef, valueBox))
}

// emitGetMetadata reads (target, propKey, metaKey), returning the stored boxed
// value or boxed undefined.
func (e *Emitter) emitGetMetadata(targetBagPtr, propKeyRef, metaKeyRef string) Value {
	perProp := e.metaPerPropBag(targetBagPtr, propKeyRef)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", r, perProp, metaKeyRef))
	return Value{Ref: r, Ty: TypeAny}
}

// emitClassMemberMetadata emits the compiler-generated design:* metadata for a
// decorated member, before its user decorators run (so a user decorator can
// read it). targetBagPtr is the raw ptr of the per-class decorator target.
func (e *Emitter) emitClassMemberDesignMetadata(targetBagPtr string, cd *ast.ClassDeclaration, pos ast.Pos) {
	if !e.emitDecoratorMetadata {
		return
	}
	// A decorated class emits design:paramtypes for its constructor (TS), stored
	// under the constructor sentinel key.
	if len(cd.Decorators) > 0 {
		var ctorParams []Type
		if cd.Constructor != nil {
			ctorParams = e.classes[cd.Name].CtorSig.ParamTypes
		}
		e.emitDefineMetadata(targetBagPtr, e.internString(metaCtorKey), e.internString("design:paramtypes"), e.emitDesignParamtypes(ctorParams).Ref)
	}
	for _, f := range cd.Fields {
		if len(f.Decorators) == 0 {
			continue
		}
		ty := e.classes[cd.Name].Ty // fallback; refine to the field type below
		if ft := e.fieldDesignType(cd.Name, f.Name); ft != nil {
			ty = *ft
		}
		e.emitDefineMetadata(targetBagPtr, e.internString(f.Name), e.internString("design:type"), e.emitDesignTypeDescriptor(ty).Ref)
	}
	for _, m := range cd.Methods {
		if len(m.Decorators) == 0 || !methodDecoratorSupported(cd, m) {
			continue
		}
		sig := e.classes[cd.Name].MethodSigs[m.Name]
		// A method's own design:type is always Function.
		fnDesc := e.emitDesignTypeDescriptor(Type{IR: "ptr", IsFunc: true})
		e.emitDefineMetadata(targetBagPtr, e.internString(m.Name), e.internString("design:type"), fnDesc.Ref)
		e.emitDefineMetadata(targetBagPtr, e.internString(m.Name), e.internString("design:paramtypes"), e.emitDesignParamtypes(sig.ParamTypes).Ref)
		e.emitDefineMetadata(targetBagPtr, e.internString(m.Name), e.internString("design:returntype"), e.emitDesignTypeDescriptor(sig.RetType).Ref)
	}
}

// fieldDesignType resolves a class field's declared type for design:type, or
// nil to fall back. Uses the registered class field layout.
func (e *Emitter) fieldDesignType(className, fieldName string) *Type {
	for _, f := range e.classes[className].Ty.Fields {
		if f.Name == fieldName {
			ft := f.Ty
			return &ft
		}
	}
	return nil
}
