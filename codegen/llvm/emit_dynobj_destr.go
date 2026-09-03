package llvm

import (
	"fmt"
	"strconv"

	"KlainMainLang/ast"
)

// unpackDynObjectPatternInto destructures an object pattern (`{ x, y: z, k: [a] }`)
// against a D1 *dynamic* source value (a bare any / dynamic object, `-compat=js`).
// It is the dynamic-object counterpart of unpackObjectPatternInto: where the
// static path GEPs a struct field, this reads each property through the runtime
// dynamic-get (emitDynAnyMemberGet), so an element with no fixed static shape
// (the common `-compat=js` case, e.g. `for (const { x } of [{ x: 23 }])`) binds
// correctly instead of failing the static FieldIndex lookup.
//
// Each leaf binding is an `any` local (the got value is a NaN-box). A `= default`
// fires on the JS rule — the got value is `undefined` (a missing key). Nested
// sub-patterns recurse through the dynamic path too. A `{ ...rest }` element is
// not yet supported here (collecting the residual keys of a runtime bag into a
// fresh dynamic object is a separate follow-up) and is rejected cleanly.
func (e *Emitter) unpackDynObjectPatternInto(objVal Value, props []ast.DestructProp, pos ast.Pos) error {
	for _, prop := range props {
		if prop.Rest {
			return fmt.Errorf("%d:%d: object rest ({ ...rest }) in a destructuring pattern over a dynamic (-compat=js) object is not yet supported", pos.Line, pos.Col)
		}
		keyRef := e.internString(prop.Key)
		val, err := e.emitDynAnyMemberGetNamed(objVal, keyRef, prop.Key, pos)
		if err != nil {
			return err
		}
		if err := e.bindDynPatternValue(val, prop.Local, prop.Default, prop.SubArray, prop.SubObject, pos); err != nil {
			return err
		}
	}
	return nil
}

// unpackDynArrayPatternInto destructures an array pattern (`[a, b, ...]`) against
// a D1 *dynamic* array source value (a bare any holding a dynamic array). Element
// i is read by its numeric string key ("0", "1", …) through the same runtime get
// used for a dynamic object; an out-of-range read yields `undefined`, which is
// also the signal a `[a = default]` element uses (JS fires the default when the
// element value is undefined). A `...rest` element is a follow-up, rejected here.
func (e *Emitter) unpackDynArrayPatternInto(arrVal Value, elems []ast.ArrayPatternElem, pos ast.Pos) error {
	for i, elem := range elems {
		if elem.Rest {
			return fmt.Errorf("%d:%d: array rest ([...rest]) in a destructuring pattern over a dynamic (-compat=js) value is not yet supported", pos.Line, pos.Col)
		}
		// A hole (no name, no sub-pattern, no default) skips the position.
		if elem.Name == "" && elem.SubArray == nil && elem.SubObject == nil && elem.Default == nil {
			continue
		}
		keyRef := e.internString(strconv.Itoa(i))
		val, err := e.emitDynAnyMemberGet(arrVal, keyRef, pos)
		if err != nil {
			return err
		}
		if err := e.bindDynPatternValue(val, elem.Name, elem.Default, elem.SubArray, elem.SubObject, pos); err != nil {
			return err
		}
	}
	return nil
}

// bindDynPatternValue binds one destructuring leaf/sub-pattern against a value
// already read from a dynamic source (an any NaN-box). Shared by the object- and
// array-pattern dynamic paths. Handles a `= default` (fires when val is
// undefined), a nested array/object sub-pattern (recurses through the dynamic
// path), or a plain leaf binding to an `any` local.
func (e *Emitter) bindDynPatternValue(val Value, local string, def ast.Expression, subArray []ast.ArrayPatternElem, subObject []ast.DestructProp, pos ast.Pos) error {
	if def != nil {
		defaulted, err := e.applyDynDefault(val, def, pos)
		if err != nil {
			return err
		}
		val = defaulted
	}
	if subArray != nil {
		return e.unpackDynArrayPatternInto(val, subArray, pos)
	}
	if subObject != nil {
		return e.unpackDynObjectPatternInto(val, subObject, pos)
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", val.Ref, slot))
	e.define(local, Symbol{Ptr: slot, Ty: TypeAny})
	return nil
}

// applyDynDefault returns val, or the (boxed) default expression's value when val
// is `undefined` — the JS destructuring-default rule for a dynamic source. The
// select is a real control-flow branch (not an llvm `select`) so the default
// expression is only evaluated in the absent case, matching JS's lazy default.
func (e *Emitter) applyDynDefault(val Value, def ast.Expression, pos ast.Pos) (Value, error) {
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	isUndef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, val.Ref, nbUndefined))
	absentL := e.freshLabel("dyndestr.absent")
	presentL := e.freshLabel("dyndestr.present")
	afterL := e.freshLabel("dyndestr.after")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUndef, absentL, presentL))

	e.emitLabel(absentL)
	defVal, err := e.emitExpr(def)
	if err != nil {
		return Value{}, err
	}
	boxed, err := e.emitBoxValue(defVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(presentL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", val.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(afterL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}
