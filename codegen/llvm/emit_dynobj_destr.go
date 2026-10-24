package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"KlainMainLang/sema"
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
	src := e.destrSource
	e.destrSource = nil
	if (len(props) == 0 || props[0].Rest) && objVal.Ty.IsDynamic() {
		// `{}` and `{ ...r }` read no property first, but still require an
		// object-coercible value.
		e.emitRequireDestructurable(objVal, destrSourceText(src))
	}
	var named []ast.Expression // the keys read so far, which a rest leaves out
	var srcID *ast.Identifier
	for _, prop := range props {
		if prop.Rest {
			// The value's remaining own enumerable properties, in a new object.
			rest, err := e.emitAnyObjRestExcluding(objVal, named, pos)
			if err != nil {
				return err
			}
			if err := e.bindDynPatternValue(rest, prop.Local, nil, nil, nil, pos); err != nil {
				return err
			}
			continue
		}
		var val Value
		var err error
		if prop.KeyExpr != nil {
			// A runtime key, evaluated once here, in pattern order.
			if srcID == nil {
				srcID = e.anyTemp(objVal.Ref, pos)
			}
			kid, kerr := e.emitPropertyKeyTemp(prop.KeyExpr, pos)
			if kerr != nil {
				return kerr
			}
			named = append(named, kid)
			if val, err = e.emitExpr(ast.NewIndexExpression(srcID, kid, pos)); err == nil {
				val, err = e.emitBoxValue(val)
			}
		} else {
			named = append(named, ast.NewStringLiteral(prop.Key, pos))
			val, err = e.emitDynAnyMemberGetNamed(objVal, e.internString(prop.Key), prop.Key, pos)
		}
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
	// The iteration protocol, stepped only as far as the pattern reads
	// (and the iterator closed when it is left unfinished).
	if !arrVal.Ty.IsDynamic() {
		b, err := e.emitBoxValue(arrVal)
		if err != nil {
			return err
		}
		arrVal = b
	}
	dataPtr, lenVal := e.emitAnyArrayElemsTake(arrVal, nil, patternTake(elems))
	for i, elem := range elems {
		if elem.Rest {
			// The remaining values, as a new array.
			n, pos0, from, nn := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", n, lenVal, i))
			e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", pos0, n))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", nn, pos0, n))
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", from, dataPtr, i))
			data := e.emitArrayCopy(from, nn, TypeAny)
			rest, err := e.emitBoxValue(Value{Ref: e.newArrayHeader(data, nn), Ty: ArrayOf(TypeAny)})
			if err != nil {
				return err
			}
			return e.bindDynPatternValue(rest, elem.Name, nil, elem.SubArray, elem.SubObject, pos)
		}
		// A hole (no name, no sub-pattern, no default) skips the position.
		if elem.Name == "" && elem.SubArray == nil && elem.SubObject == nil && elem.Default == nil {
			continue
		}
		in, idx := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", in, i, lenVal))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 0", idx, in, i))
		gep, w, val := e.freshReg(), e.freshReg(), e.freshReg()
		// A past-the-end read loads slot 0 only to discard it: guard an
		// empty view by loading through a select on the pointer too.
		safe := e.freshReg()
		e.declareFn("__kml_undef_word", fmt.Sprintf("@__kml_undef_word = private constant i64 %d, align 8", nbUndefined))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr @__kml_undef_word", safe, in, dataPtr))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", gep, safe, idx))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, gep))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", val, in, w, nbUndefined))
		if err := e.bindDynPatternValue(Value{Ref: val, Ty: TypeAny}, elem.Name, elem.Default, elem.SubArray, elem.SubObject, pos); err != nil {
			return err
		}
	}
	return nil
}

// patternTake is how many values an array pattern reads from an iterator:
// one per position, or every value (-1) with a rest element.
func patternTake(elems []ast.ArrayPatternElem) int {
	for _, el := range elems {
		if el.Rest {
			return -1
		}
	}
	return len(elems)
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
	e.definePatternLocal(local, Symbol{Ptr: slot, Ty: TypeAny})
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

// emitRequireDestructurable throws Node's TypeError when box v is null or
// undefined (RequireObjectCoercible of an object pattern). The message names
// the source as written (srcText), else as V8 shows a value without one.
func (e *Emitter) emitRequireDestructurable(v Value, srcText string) {
	bad, isN := e.emitIsNullish(v), e.freshReg()
	thr, nul, und, ok := e.freshLabel("dc.thr"), e.freshLabel("dc.null"), e.freshLabel("dc.undef"), e.freshLabel("dc.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, thr, ok))
	e.emitLabel(thr)
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isN, v.Ref, nbNull))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isN, nul, und))
	e.emitLabel(nul)
	nullText, undefText := "object null", "undefined"
	if srcText != "" {
		nullText, undefText = srcText, srcText
	}
	e.emitThrowTypeError("Cannot destructure '" + nullText + "' as it is null.")
	e.emitLabel(und)
	e.emitThrowTypeError("Cannot destructure '" + undefText + "' as it is undefined.")
	e.emitLabel(ok)
}

// emitAnyObjRestExcluding is an object rest element's value: a new object
// holding box v's own enumerable properties (string keys, then symbols)
// other than the excluded keys (strings or symbols, each evaluated once by
// the caller).
func (e *Emitter) emitAnyObjRestExcluding(v Value, keys []ast.Expression, pos ast.Pos) (Value, error) {
	excl, err := e.emitExprWithObjectHint(ast.NewArrayLiteral(keys, pos), TypeAny)
	if err != nil {
		return Value{}, err
	}
	if excl, err = e.emitBoxValue(excl); err != nil {
		return Value{}, err
	}
	e.ensureAnyObjRest()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_obj_rest(i64 %s, i64 %s)", r, v.Ref, excl.Ref))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// anyObjRestSrc is __kml_any_obj_rest's loops: CopyDataProperties of its
// source's own enumerable keys (__kml_rest_keys, then __kml_rest_syms),
// less the excluded ones, into a new object.
const anyObjRestSrc = `for (let __kml_rest_i = 0; __kml_rest_i < __kml_rest_keys.length; __kml_rest_i++) {
  const __kml_rest_k: any = __kml_rest_keys[__kml_rest_i];
  if (__kml_rest_excl.indexOf(__kml_rest_k) < 0) __kml_rest_out[__kml_rest_k] = __kml_rest_o[__kml_rest_k];
}
for (let __kml_rest_j = 0; __kml_rest_j < __kml_rest_syms.length; __kml_rest_j++) {
  const __kml_rest_s: any = __kml_rest_syms[__kml_rest_j];
  if (__kml_rest_excl.indexOf(__kml_rest_s) < 0 && Object.prototype.propertyIsEnumerable.call(__kml_rest_o, __kml_rest_s)) __kml_rest_out[__kml_rest_s] = __kml_rest_o[__kml_rest_s];
}`

// ensureAnyObjRest defines @__kml_any_obj_rest(i64 src, i64 excl) once,
// compiled from anyObjRestSrc.
func (e *Emitter) ensureAnyObjRest() {
	if e.usedAnyObjRest {
		return
	}
	e.usedAnyObjRest = true
	e.ensureDynObj()
	restore := e.beginDetachedFunc()
	defer restore()
	e.pushScope()
	defer e.popScope()
	bind := func(name, init string) string {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", init, slot))
		e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
		return slot
	}
	// A Proxy source copies through its traps (shape.c).
	e.declareFn("__kml_proxy_obj_rest", "declare i64 @__kml_proxy_obj_rest(i64, i64)")
	pr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_proxy_obj_rest(i64 %%p_src, i64 %%p_excl)", pr))
	isPx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isPx, pr))
	pxL, ordL := e.freshLabel("rest.proxy"), e.freshLabel("rest.ord")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPx, pxL, ordL))
	e.emitLabel(pxL)
	e.emitTerminator(fmt.Sprintf("ret i64 %s", pr))
	e.emitLabel(ordL)
	bind("__kml_rest_src", "%p_src")
	bind("__kml_rest_excl", "%p_excl")
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	outSlot := bind("__kml_rest_out", e.emitDynObjBox(bag).Ref)
	pos := ast.Pos{}
	obj := func(m string, arg ast.Expression) ast.Expression {
		return ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), m, pos), []ast.Expression{arg}, pos)
	}
	// Its own keys, through the Object statics directly (this body is not
	// resolved like a program); they and the reads apply ToObject.
	bind("__kml_rest_o", "%p_src")
	var err error
	for _, kv := range [][2]string{{"keys", "__kml_rest_keys"}, {"getOwnPropertySymbols", "__kml_rest_syms"}} {
		if err != nil {
			break
		}
		var v Value
		if v, err = e.emitObjectStatic(kv[0], obj(kv[0], ast.NewIdentifier("__kml_rest_o", pos)).(*ast.CallExpression)); err == nil {
			if v, err = e.emitBoxValue(v); err == nil {
				bind(kv[1], v.Ref)
			}
		}
	}
	var prog *ast.Program
	if err == nil {
		prog, err = parser.Parse(anyObjRestSrc)
	}
	if err == nil {
		err = sema.Prepare(prog)
	}
	for i := 0; err == nil && i < len(prog.Body); i++ {
		err = e.emitStmt(prog.Body[i])
	}
	if err != nil {
		panic(fmt.Sprintf("__kml_any_obj_rest: %v", err))
	}
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, outSlot))
	e.emitTerminator(fmt.Sprintf("ret i64 %s", out))
	e.writeFunc("i64 @__kml_any_obj_rest(i64 %p_src, i64 %p_excl)", e.allocas.String()+e.body.String())
}

// destrSourceText is a destructuring source as V8's TypeError names it: a
// name or a null/undefined literal as written, else "".
func destrSourceText(src ast.Expression) string {
	switch x := src.(type) {
	case *ast.Identifier:
		return unmangleTopLevelName(x.Name)
	case *ast.NullLiteral:
		if x.IsUndefined {
			return "undefined"
		}
		return "null"
	}
	return ""
}
