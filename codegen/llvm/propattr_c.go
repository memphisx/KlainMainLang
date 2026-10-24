package llvm

import (
	_ "embed"
	"fmt"

	"KlainMainLang/ast"
)

// propattr_c.go — the writable/enumerable/configurable attributes
// Object.defineProperty gives a static object's own property
// (propattrsrc/propattr.c). Only a program that defines a property pays:
// the write, delete and enumeration sites consult the attributes when
// propAttrsUsed is set.

//go:embed propattrsrc/propattr.c
var propAttrSource string

// Attribute bits of __kml_pattr_attrs.
const (
	pattrNotWritable     = 1
	pattrNotEnumerable   = 2
	pattrNotConfigurable = 4
)

func (e *Emitter) ensurePropAttr() {
	if e.usedPropAttr {
		return
	}
	e.usedPropAttr = true
	e.usedCollections = true
	e.ensureMapNumHelpers()
	e.emitGlobal("declare i64 @__kml_pattr_attrs(ptr, ptr)")
	e.emitGlobal("declare i64 @__kml_pattr_define(ptr, ptr, i64, i64, i64, i64)")
	e.emitGlobal("declare void @__kml_pattr_forget(ptr, ptr)")
}

// propAttrsUsed reports whether the program defines a property anywhere
// (Object.defineProperty / defineProperties, Reflect.defineProperty): only
// then can a static object's property have an attribute other than the
// default.
func (e *Emitter) propAttrsUsed() bool {
	if e.programIndependent() {
		return false
	}
	if e.propAttrScan == 0 {
		e.propAttrScan = 1
		if e.prog != nil {
			ast.Inspect(e.prog, func(n ast.Node) bool {
				if m, ok := n.(*ast.MemberExpression); ok && (m.Property == "defineProperty" || m.Property == "defineProperties") {
					e.propAttrScan = 2
				}
				// Object.create(proto, props) defines props by descriptor.
				if c, ok := n.(*ast.CallExpression); ok && len(c.Args) == 2 {
					if m, ok := c.Callee.(*ast.MemberExpression); ok && m.Property == "create" {
						if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "Object" {
							e.propAttrScan = 2
						}
					}
				}
				return e.propAttrScan != 2
			})
		}
	}
	return e.propAttrScan == 2
}

// emitPropAttrs is the attribute bits of obj's property name.
func (e *Emitter) emitPropAttrs(obj, name string) string {
	e.ensurePropAttr()
	a := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_pattr_attrs(ptr %s, ptr %s)", a, obj, e.internString(name)))
	return a
}

// emitPropAttrTest is an i1: obj's property name has attribute bit set.
func (e *Emitter) emitPropAttrTest(obj, name string, bit int) string {
	a := e.emitPropAttrs(obj, name)
	m, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", m, a, bit))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, m))
	return r
}

// emitPropEnumerable is an i1: obj's property name is enumerable (constant
// true when the program defines no property).
func (e *Emitter) emitPropEnumerable(obj, name string) string {
	if !e.propAttrsUsed() || obj == "null" {
		return "true"
	}
	ne := e.emitPropAttrTest(obj, name, pattrNotEnumerable)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", r, ne))
	return r
}

// emitReadOnlyCheck throws the strict-mode TypeError for a write to obj's
// property name when it is not writable.
func (e *Emitter) emitReadOnlyCheck(obj string, objTy Type, name string) {
	if !e.propAttrsUsed() {
		return
	}
	ro := e.emitPropAttrTest(obj, name, pattrNotWritable)
	badL, okL := e.freshLabel("pattr.ro"), e.freshLabel("pattr.rw")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ro, badL, okL))
	e.emitLabel(badL)
	if objTy.IsError() {
		// V8 names an Error by its string: 'Error: x'.
		if s, err := e.emitErrorToString(Value{Ref: obj, Ty: objTy}); err == nil {
			pre := Value{Ref: e.internString(fmt.Sprintf("Cannot assign to read only property '%s' of object '", name)), Ty: TypePtr}
			if m, err := e.emitStringConcat(pre, s); err == nil {
				if m2, err := e.emitStringConcat(m, Value{Ref: e.internString("'"), Ty: TypePtr}); err == nil {
					e.emitThrowTypeErrorValue(m2.Ref)
					e.emitLabel(okL)
					return
				}
			}
		}
	}
	e.emitThrowTypeError(fmt.Sprintf("Cannot assign to read only property '%s' of object '%s'", name, objectTagFor(objTy)))
	e.emitLabel(okL)
}

// emitDeleteConfigurableCheck throws the strict-mode TypeError for deleting
// obj's property name when it is not configurable, and otherwise forgets
// its attributes (a key created again is a plain property).
func (e *Emitter) emitDeleteConfigurableCheck(obj string, objTy Type, name string) {
	if !e.propAttrsUsed() {
		return
	}
	nc := e.emitPropAttrTest(obj, name, pattrNotConfigurable)
	badL, okL := e.freshLabel("pattr.nc"), e.freshLabel("pattr.del")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nc, badL, okL))
	e.emitLabel(badL)
	e.emitThrowTypeError(fmt.Sprintf("Cannot delete property '%s' of %s", name, objectTagFor(objTy)))
	e.emitLabel(okL)
	e.emitInstr(fmt.Sprintf("call void @__kml_pattr_forget(ptr %s, ptr %s)", obj, e.internString(name)))
}

// objectTagFor is V8's `#<Name>` for an object in an error message.
func objectTagFor(t Type) string {
	if t.IsClass() && t.ClassName != "" {
		return "#<" + t.ClassName + ">"
	}
	return "#<Object>"
}

// emitStaticDefineProperty is Object.defineProperty(obj, name, desc) on a
// static object's declared field: the descriptor's attributes go into the
// object's record (validated as ValidateAndApplyPropertyDescriptor does), its
// value into the field, and the key exists afterwards.
func (e *Emitter) emitStaticDefineProperty(obj Value, f Field, descExpr ast.Expression, pos ast.Pos) (Value, error) {
	if lit, ok := descExpr.(*ast.ObjectLiteral); ok {
		for _, p := range lit.Properties {
			if p.Key == "get" || p.Key == "set" {
				return Value{}, fmt.Errorf("%d:%d: Object.defineProperty: an accessor on a declared field of a typed object is not supported; type the object as any", pos.Line, pos.Col)
			}
		}
	}
	e.ensurePropAttr()
	e.ensureObjectIs()
	descBag, err := e.emitDynBagOrThrow(descExpr, "Object.defineProperty (descriptor)", pos)
	if err != nil {
		return Value{}, err
	}
	wP, wV := e.descFieldBool(descBag, "writable")
	eP, eV := e.descFieldBool(descBag, "enumerable")
	cP, cV := e.descFieldBool(descBag, "configurable")
	bits := func(w, en, c string) string {
		acc := "0"
		for i, b := range []string{w, en, c} {
			z, sh, o := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, b))
			e.emitInstr(fmt.Sprintf("%s = shl i64 %s, %d", sh, z, i))
			e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", o, acc, sh))
			acc = o
		}
		return acc
	}
	has := bits(wP, eP, cP)
	val := bits(wV, eV, cV)
	valBox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", valBox, descBag, e.internString("value")))
	vP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", vP, valBox, nbUndefined))
	present, cur := e.emitFieldPresent(obj.Ref, obj.Ty, f)
	curBox, err := e.emitBoxValue(cur)
	if err != nil {
		return Value{}, err
	}
	same, sameAny, notV := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_Object_is(i64 %s, i64 %s)", same, curBox.Ref, valBox))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notV, vP))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", sameAny, same, notV))
	existed, sameW := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", existed, present))
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", sameW, sameAny))
	rej, bad := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_pattr_define(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s, i64 %s)", rej, obj.Ref, e.internString(f.Name), has, val, existed, sameW))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", bad, rej))
	badL, okL := e.freshLabel("defprop.redef"), e.freshLabel("defprop.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitThrowTypeError("Cannot redefine property: " + f.Name)
	e.emitLabel(okL)
	storeL, doneL := e.freshLabel("defprop.value"), e.freshLabel("defprop.valuedone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", vP, storeL, doneL))
	e.emitLabel(storeL)
	idx, _, _ := obj.Ty.FieldIndex(f.Name)
	gep := e.emitRecordGep(obj.Ref, obj.Ty, idx, f.Ty, f.Name)
	cv := e.convert(Value{Ref: valBox, Ty: TypeAny}, f.Ty)
	if f.Ty.IsArray() {
		e.storeArrayFieldHeader(gep, cv)
	} else {
		e.heapStore(StructFieldIR(f.Ty), cv.Ref, gep, f.Ty.Align())
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	e.emitSetPresence(obj.Ref, obj.Ty, f.Name, true) // the key exists
	return obj, nil
}

// andEnumerable is present narrowed to obj's enumerable property name: a
// key-listing reader's test (a non-enumerable key is own but not listed).
func (e *Emitter) andEnumerable(present, obj, name string) string {
	en := e.emitPropEnumerable(obj, name)
	if en == "true" {
		return present
	}
	if present == "true" {
		return en
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, present, en))
	return r
}

// emitErrorDefineAttrs validates and records Object.defineProperty's
// attributes for an Error's own key keyRef (propattr.c) before its value v
// (boxed, undefined when the descriptor gives none) is stored: a rejected
// redefinition throws the TypeError and stores nothing.
func (e *Emitter) emitErrorDefineAttrs(errPtr, keyRef, descBag, v string) {
	e.ensurePropAttr()
	e.ensureObjectIs()
	e.declareFn("__kml_error_own_desc", "declare i64 @__kml_error_own_desc(ptr, ptr)")
	wP, wV := e.descFieldBool(descBag, "writable")
	eP, eV := e.descFieldBool(descBag, "enumerable")
	cP, cV := e.descFieldBool(descBag, "configurable")
	bits := func(w, en, c string) string {
		acc := "0"
		for i, b := range []string{w, en, c} {
			z, sh, o := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, b))
			e.emitInstr(fmt.Sprintf("%s = shl i64 %s, %d", sh, z, i))
			e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", o, acc, sh))
			acc = o
		}
		return acc
	}
	has, val := bits(wP, eP, cP), bits(wV, eV, cV)
	// The key's current descriptor: whether it exists, and its value.
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_error_own_desc(ptr %s, ptr %s)", cur, errPtr, keyRef))
	ex, existed := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", ex, cur, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", existed, ex))
	_, curPay := e.emitUnboxTagPayload(Value{Ref: cur, Ty: TypeAny})
	curBag, old := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", curBag, ex, e.emitIntToPtr(curPay)))
	oldSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", oldSlot))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, oldSlot))
	rdL, rdDone := e.freshLabel("errdef.old"), e.freshLabel("errdef.olddone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ex, rdL, rdDone))
	e.emitLabel(rdL)
	ov := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", ov, curBag, e.internString("value")))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ov, oldSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", rdDone))
	e.emitLabel(rdDone)
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", old, oldSlot))
	vP, notV, same, sameAny, sameW := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", vP, v, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notV, vP))
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_Object_is(i64 %s, i64 %s)", same, old, v))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", sameAny, same, notV))
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", sameW, sameAny))
	rej, bad := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_pattr_define(ptr %s, ptr %s, i64 %s, i64 %s, i64 %s, i64 %s)", rej, errPtr, keyRef, has, val, existed, sameW))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", bad, rej))
	badL, okL := e.freshLabel("errdef.redef"), e.freshLabel("errdef.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitThrowRedefine(keyRef)
	e.emitLabel(okL)
}

// emitDynDeleteConfigurableCheck is emitDeleteConfigurableCheck for a boxed
// static object or Error and a run-time key: a property Object.defineProperty
// made non-configurable throws the strict-mode TypeError, any other forgets
// its attributes.
func (e *Emitter) emitDynDeleteConfigurableCheck(tag, payload, keyRef string) {
	e.ensurePropAttr()
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	objL, doneL := e.freshLabel("dyndel.attrs"), e.freshLabel("dyndel.attrsdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, doneL))
	e.emitLabel(objL)
	obj := e.emitIntToPtr(payload)
	a, m, nc := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_pattr_attrs(ptr %s, ptr %s)", a, obj, keyRef))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", m, a, pattrNotConfigurable))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nc, m))
	badL, okL := e.freshLabel("dyndel.nc"), e.freshLabel("dyndel.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nc, badL, okL))
	e.emitLabel(badL)
	// V8: `Cannot delete property 'k' of #<Object>`, an Error named by its
	// string.
	errPtr, isErr := e.emitBoxedErrorProbe(payload)
	name := e.freshReg()
	errName := e.internString("#<Object>")
	if s, err := e.emitErrorToString(Value{Ref: errPtr, Ty: errorObjType}); err == nil {
		errName = s.Ref
	}
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", name, isErr, errName, e.internString("#<Object>")))
	msg := Value{Ref: e.internString("Cannot delete property '"), Ty: TypePtr}
	for _, part := range []string{keyRef, e.internString("' of "), name} {
		if m2, err := e.emitStringConcat(msg, Value{Ref: part, Ty: TypePtr}); err == nil {
			msg = m2
		}
	}
	e.emitThrowTypeErrorValue(msg.Ref)
	e.emitLabel(okL)
	e.emitInstr(fmt.Sprintf("call void @__kml_pattr_forget(ptr %s, ptr %s)", obj, keyRef))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}
