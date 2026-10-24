package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/checker"
	"KlainMainLang/sema"
	"fmt"
	"strings"
)

// emitTemplateLiteral builds the concatenated result of a template literal.
func (e *Emitter) emitTemplateLiteral(tl *ast.TemplateLiteral) (Value, error) {
	acc := Value{Ref: e.internString(tl.Quasis[0]), Ty: TypePtr}
	for i, expr := range tl.Exprs {
		val, err := e.emitPreserveNullableOperand(expr)
		if err != nil {
			return Value{}, err
		}
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: %w", tl.GetPos().Line, tl.GetPos().Col, err)
		}
		acc, err = e.emitStringConcat(acc, strVal)
		if err != nil {
			return Value{}, err
		}
		tail := Value{Ref: e.internString(tl.Quasis[i+1]), Ty: TypePtr}
		acc, err = e.emitStringConcat(acc, tail)
		if err != nil {
			return Value{}, err
		}
	}
	return acc, nil
}

// emitArgToString normalizes a builtin argument that JS ToStrings — a RegExp
// subject, parseInt/parseFloat input, padStart/padEnd fill, indexOf search — to
// a plain null-terminated string ptr: an already-string value coerces
// unchanged; undefined / a value-less `void` result becomes "undefined";
// anything else (number, boolean, object) runs through ToString. Faithful to JS
// (`parseInt(true)` parses "true"→NaN; `"abc".padEnd(10, false)` fills with
// "false"; `s.indexOf(123)` searches for "123"). Without it a non-string arg
// left a non-`ptr` word where a `ptr` is required — invalid IR.
func (e *Emitter) emitArgToString(v Value) (Value, error) {
	if t, ok := e.asTriStr(v); ok {
		v = t
	}
	if v.Ty.NullAndUndef() {
		return e.renderThreeState(v, e.emitArgToString)
	}
	if v.Ty.IsNull() && !v.Ty.IsDynamic() {
		// The null or undefined literal's type (TypeNull, TypeUndefined).
		word := "null"
		if v.Ty.IsUndefined() {
			word = "undefined"
		}
		return Value{Ref: e.internString(word), Ty: TypePtr}, nil
	}
	if isStringTy(v.Ty) {
		if !v.Ty.Nullable() {
			return e.convert(v, TypePtr), nil
		}
		s := v // tested raw: converting first would make its null nullRef
		// An absent string (a null or undefined literal, `string | null`)
		// converts to the word, as ToString does.
		word := "null"
		if v.Ty.IsUndefined() {
			word = "undefined"
		}
		isNull, r := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, s.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, isNull, e.internString(word), s.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	if v.Ty.IsUndefined() || v.Ty.IR == "void" {
		return Value{Ref: e.internString("undefined"), Ty: TypePtr}, nil
	}
	return e.emitValueToString(v)
}

// emitValueToString converts any value to a null-terminated string ptr.
// Strings pass through; numbers and bools are formatted via sprintf into a 32-byte scratch buffer.
func (e *Emitter) emitValueToString(v Value) (Value, error) {
	if t, ok := e.asTriStr(v); ok {
		v = t
	}
	if v.Ty.NullAndUndef() {
		return e.renderThreeState(v, e.emitValueToString)
	}
	if v.Ty.IsBuffer() && !v.Ty.Nullable() {
		// Buffer.prototype.toString decodes (utf8), overriding the
		// TypedArray join: `String(buf)`, `buf + ""`.
		p, n := e.freshReg(), e.freshReg()
		p = e.arrayData(v)
		n = e.arrayLen(v)
		return e.emitBufferEncodeString(p, n, "utf8"), nil
	}
	if v.Ty.IsCollIter() {
		// Object.prototype.toString over the iterator's @@toStringTag.
		return Value{Ref: e.internString("[object " + collIterName(v.Ty) + "]"), Ty: TypePtr}, nil
	}
	if v.Ty.IsRegExp() && v.Ty.IsObject() && !v.Ty.Nullable() {
		// RegExp.prototype.toString: `/source/flags`.
		ty := RegExpType()
		load := func(field string) Value {
			idx, fty, _ := ty.FieldIndex(field)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, ty.StructIR(), v.Ref, idx))
			return e.loadScalarOrNullableField(gep, fty)
		}
		acc, err := e.emitStringConcat(Value{Ref: e.internString("/"), Ty: TypePtr}, load("source"))
		if err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString("/"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		return e.emitStringConcat(acc, load("flags"))
	}
	if v.Ty.IsDate() && !v.Ty.IsDynamic() && !v.Ty.Nullable() && !v.Ty.IsClass() {
		// Date.prototype.toString: `String(d)`, `${d}`, `d + ""`.
		return e.emitDateOrInvalid(Value{Ref: e.emitDateTimeValue(v), Ty: TypeI64}, e.emitDateToString)
	}
	if v.Ty.IsCaught() {
		// A caught value (TDD-00202): an Error renders "Name: message", anything
		// else via the dynamic toString.
		return e.emitCaughtToString(v)
	}
	if v.Ty.IsDynamic() {
		return e.emitDynamicToString(v)
	}
	if v.Ty.IsBigInt() {
		// String(10n) / `${10n}` render the bare digits, no `n` suffix (that
		// suffix is console.log-only — see emit_call_console.go).
		return e.emitBigIntToString(v, false)
	}
	if v.Ty.IsSymbol() {
		// V1 deliberately treats template-literal interpolation the same as
		// .toString()/console.log (both format as "Symbol(desc)"); real JS
		// is stricter and throws TypeError here — see docs/tdd/TDD-00044.md.
		return e.emitSymbolToString(v)
	}
	if v.Ty.IsNull() {
		label := "null"
		if v.Ty.IsUndefined() {
			label = "undefined"
		}
		return Value{Ref: e.internString(label), Ty: TypePtr}, nil
	}
	// An Error stringifies as `name: message` (JS Error.prototype.toString), so
	// `String(err)` / `` `${err}` `` render Node's "Error: boom" rather than an
	// object dump.
	if v.Ty.IsError() {
		return e.emitErrorToString(v)
	}
	// A tuple stringifies to its elements joined by commas (TDD-00066),
	// matching real JS's `String([a, b])` / `${tuple}` — checked before the
	// generic ptr/object handling below (a tuple is structurally an object).
	if v.Ty.IsTuple() {
		// An absent tuple (an omitted `p?: [number, string]` field) is a null
		// pointer: render its keyword rather than reading through it.
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(absentWord(v.Ty)), slot))
		presentL := e.freshLabel("tuple.str")
		doneL := e.freshLabel("tuple.str.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(v.Ref), doneL, presentL))
		e.emitLabel(presentL)
		s, err := e.emitTupleToString(v)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
		return Value{Ref: out, Ty: TypePtr}, nil
	}
	// An array stringifies to its elements joined by commas (real JS's
	// Array.prototype.toString / String([a,b]) / `${arr}`) — the same routine
	// arr.join() uses, with the default "," separator. A nested-array element
	// recurses through the join core's per-element emitValueToString, so
	// `String([[1,2],[3]])` renders "1,2,3". v.Ref is the array's header.
	if mayBeArguments(v.Ty) && !v.Ty.Nullable() && v.Ref != "zeroinitializer" {
		// An Arguments object is `[object Arguments]`.
		return e.emitArgumentsBranch(v, func() (Value, error) {
			return Value{Ref: e.internString("[object Arguments]"), Ty: TypePtr}, nil
		}, func() (Value, error) { return e.emitArrayValueToString(v) })
	}
	if v.Ty.IsArray() && v.Ty.ElemType != nil {
		return e.emitArrayValueToString(v)
	}
	// A nullable-scalar aggregate (a T|null field/return value, TDD-00064 Stage
	// 3) stringifies to its value's rendering when present, the literal "null"
	// when absent.
	if isNullableScalar(v.Ty) {
		present, payload := e.nullableScalarAggParts(v)
		payloadStr, err := e.emitValueToString(payload)
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, present, payloadStr.Ref, e.internString(absentLiteral(v.Ty))))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	// An absent object, function or Date reference (a `Date[]` element read
	// is `Date | undefined`) converts to "null" or "undefined"; a present one
	// as its non-null type does.
	if v.Ty.IR == "ptr" && v.Ty.Nullable() && (v.Ty.IsObject() || v.Ty.IsFunc() || v.Ty.IsDate()) && !v.Ty.IsArray() && !v.Ty.IsDynamic() {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(absentLiteral(v.Ty)), slot))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
		presL, doneL := e.freshLabel("tostr.present"), e.freshLabel("tostr.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, presL))
		e.emitLabel(presL)
		str, err := e.emitValueToString(Value{Ref: v.Ref, Ty: v.Ty.withoutNullable()})
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", str.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	// A function converts as a function object does: its own
	// Symbol.toPrimitive / toString first (a function's own properties live
	// with its boxed identity), else Function.prototype.toString.
	if v.Ty.IsFunc() && !v.Ty.Nullable() {
		boxed, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynamicRender(boxed, false)
	}
	if v.Ty.IR == "ptr" && !v.Ty.IsObject() && !v.Ty.IsArray() && !v.Ty.IsFunc() {
		// Nullable string: at runtime select "null" string when ptr is null.
		if v.Ty.Nullable() {
			isNull := e.freshReg()
			result := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s",
				result, isNull, e.internString(absentLiteral(v.Ty)), v.Ref))
			return Value{Ref: result, Ty: TypePtr}, nil
		}
		return v, nil
	}
	if isInspectableObject(v.Ty) {
		// TDD-00201 Stage 2: an object literal declaring a string-hint ToPrimitive
		// method (@@toPrimitive, else own toString) stringifies through that
		// method rather than the debug dump below — `String(obj)`, `` `${obj}` ``,
		// and concatenation all follow. A class instance carries its methods as
		// MethodSigs, not struct fields, so this is a no-op for classes (ok ==
		// false) and the class `toString()` path below still wins. A valueOf-only
		// object falls through (inherited Object.prototype.toString wins the
		// string hint), landing on the "[object Object]"/inspect default below.
		if objectMayToPrimitive(v.Ty) {
			if res, ok, err := e.emitObjectToPrimitive(v, "string"); err != nil {
				return Value{}, err
			} else if ok {
				return e.emitValueToString(res)
			}
		}
		// A user-defined class `toString()` is honored in both modes — the
		// developer chose it (matching real JS's ToString).
		canon := e.canonicalizeClassTy(v.Ty)
		if canon.IsClass() {
			if info, ok := e.classes[canon.ClassName]; ok {
				if _, has := info.MethodSigs["toString"]; has {
					return e.emitClassCall(canon, Value{Ref: v.Ref, Ty: canon}, "toString", nil, ast.Pos{}, false)
				}
			}
		}
		// An error subclass without its own toString() (TDD-00155 Stage 6):
		// JS's Error.prototype.toString — "name: message", or just the name
		// when the message is empty.
		if canon.IsClass() {
			if info, ok := e.classes[canon.ClassName]; ok && info.IsErrorSubclass {
				loadStr := func(field string) string {
					idx, _, _ := info.Ty.FieldIndex(field)
					gep := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.Ty.StructIR(), v.Ref, idx))
					r := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, gep))
					return r
				}
				namePtr := loadStr("name")
				msgPtr := loadStr("message")
				joined, err := e.emitStringConcat(
					Value{Ref: namePtr, Ty: TypePtr},
					Value{Ref: e.internString(": "), Ty: TypePtr})
				if err != nil {
					return Value{}, err
				}
				full, err := e.emitStringConcat(joined, Value{Ref: msgPtr, Ty: TypePtr})
				if err != nil {
					return Value{}, err
				}
				msgLen := e.emitStrLenHeader(msgPtr)
				isEmpty := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, msgLen))
				res := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", res, isEmpty, namePtr, full.Ref))
				return Value{Ref: res, Ty: TypePtr}, nil
			}
		}
		// No user toString() on a class extending RegExp or Date: the host's
		// own (`/a+b/g`, the date string), on its body.
		if info, ok := e.classes[canon.ClassName]; ok && canon.IsClass() && info.HostBase != nil && (info.HostBase.IsRegExp() || info.HostBase.IsDate() || info.HostBase.IsArray()) {
			if info.HostBase.IsArray() {
				return e.emitValueToString(e.convert(v, *info.HostBase))
			}
			body := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, v.Ref))
			return e.emitValueToString(Value{Ref: body, Ty: *info.HostBase})
		}
		// No user toString(): Object.prototype.toString's `[object Tag]`, the
		// tag a class's [Symbol.toStringTag] getter gives, else Object.
		if canon.IsClass() && e.classHasMethodInherited(canon.ClassName, accessorMethodName("get", "@@toStringTag")) {
			tag, err := e.emitClassCall(canon, Value{Ref: v.Ref, Ty: canon}, accessorMethodName("get", "@@toStringTag"), nil, ast.Pos{}, false)
			if err != nil {
				return Value{}, err
			}
			open, err := e.emitStringConcat(Value{Ref: e.internString("[object "), Ty: TypePtr}, e.convert(tag, TypePtr))
			if err != nil {
				return Value{}, err
			}
			return e.emitStringConcat(open, Value{Ref: e.internString("]"), Ty: TypePtr})
		}
		if info, ok := e.classes[canon.ClassName]; ok && canon.IsClass() && info.HostBase != nil {
			// A class extending a host inherits its host's tag.
			return Value{Ref: e.internString("[object " + hostTagName(*info.HostBase) + "]"), Ty: TypePtr}, nil
		}
		return Value{Ref: e.internString("[object Object]"), Ty: TypePtr}, nil
	}
	e.ensureSprintf()
	scratch := e.emitStringScratch(32) // TDD-00120: length-prefixed; finalized below
	switch {
	case v.Ty.IR == "i1":
		truePtr := e.internString("true")
		falsePtr := e.internString("false")
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, v.Ref, truePtr, falsePtr))
		return Value{Ref: r, Ty: TypePtr}, nil
	case v.Ty.Float:
		val := v
		if v.Ty.IR == "float" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, v.Ref))
			val = Value{Ref: r, Ty: TypeF64}
		}
		// Shortest round-trip, JS-faithful formatting (TDD-00080) instead of
		// bare %g's 6-significant-digit truncation.
		e.ensureDtoa()
		e.emitInstr(fmt.Sprintf("call void @__kml_dtoa(ptr %s, double %s)", scratch, val.Ref))
	case v.Ty.IsInteger():
		val := v
		unsigned := !v.Ty.Signed
		if v.Ty.IR != "i64" {
			r := e.freshReg()
			ext := "sext"
			if unsigned {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to i64", r, ext, v.Ty.IR, v.Ref))
			val = Value{Ref: r, Ty: TypeI64}
		}
		// An unsigned 64-bit value with its high bit set (`uint64` above 2^63)
		// must print via `%llu`; `%lld` would render it as a negative number.
		// Narrow unsigned types zext to a non-negative i64, so `%llu` is correct
		// for every unsigned width (TDD-00123 — the integer escape hatch).
		intFmt := "%lld"
		if unsigned {
			intFmt = "%llu"
		}
		fmtPtr := e.internString(intFmt)
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, fmtPtr, val.Ref))
	default:
		return Value{}, fmt.Errorf("cannot convert type %s to string in template literal", v.Ty.IR)
	}
	e.emitStringFinalizeLen(scratch)
	return Value{Ref: scratch, Ty: TypePtr}, nil
}

// declaredArrayType is inferArrayType for a *named binding* whose initializer is
// an array literal: an empty `[]` consults the usage pre-pass
// (inferEmptyArrayElemTypes / TDD-00205 Stage 1) for the element type inferred
// from `name`'s later push/insert/reassign usages, falling back to inferArrayType's
// blind `number[]` default when the pass found nothing conclusive. A non-empty
// literal is unaffected (its own elements decide).
func (e *Emitter) declaredArrayType(name string, lit *ast.ArrayLiteral) Type {
	if elem, ok := e.emptyArrayElems[name]; ok && (len(lit.Elements) == 0 || elem.IsDynamic()) {
		return ArrayOf(elem)
	}
	return e.inferArrayType(lit)
}

// inferArrayType picks an element type by looking at the first element of a literal.
func (e *Emitter) inferArrayType(lit *ast.ArrayLiteral) Type {
	if len(lit.Elements) < 64 || !constLiteralTree(lit) {
		return e.inferArrayTypeOf(lit)
	}
	if ty, ok := e.constArrayTypes[lit]; ok {
		return ty
	}
	ty := e.inferArrayTypeOf(lit)
	if e.constArrayTypes == nil {
		e.constArrayTypes = map[*ast.ArrayLiteral]Type{}
	}
	e.constArrayTypes[lit] = ty
	return ty
}

func (e *Emitter) inferArrayTypeOf(lit *ast.ArrayLiteral) Type {
	if len(lit.Elements) == 0 {
		return ArrayOf(TypeF64) // default: number[] (TDD-00123: number is float64)
	}
	// Spreading an `any`, or holding one, makes the literal `any[]`
	// (TypeScript's type for it), whatever else it holds.
	for _, el := range lit.Elements {
		if sp, ok := el.(*ast.SpreadElement); ok && isUnconstrainedDynamic(e.inferExprType(sp.Arg)) {
			return ArrayOf(TypeAny)
		}
		if _, ok := el.(*ast.SpreadElement); !ok && len(lit.Elements) > 1 && isUnconstrainedDynamic(e.inferExprType(el)) {
			return ArrayOf(TypeAny)
		}
	}
	// Functions of different signatures (`[String, Number]`): one closure
	// element type would call the others through the wrong signature, so
	// each element is a boxed function.
	fnKey := ""
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			continue
		}
		if t := e.inferExprType(el); t.IsFunc() {
			k := reprKey(t.withoutNullable())
			if fnKey != "" && k != fnKey {
				return ArrayOf(TypeAny)
			}
			fnKey = k
		}
	}
	// A literal TypeScript types `any[]` (an element read off an `any`,
	// `[e.message, e.flag]`) is one, whatever the elements' own shapes.
	if c := e.front(); c != nil {
		if t := c.TypeOf(lit); !c.Unanswered(t) && t.Kind == checker.Array && t.Elem != nil && t.Elem.Flags&checker.Any != 0 {
			return ArrayOf(TypeAny)
		}
	}
	// An empty element (`[]`, never[]) beside an array element is that
	// array's kind (tsc's subtype reduction): `[[], ["x"]]` is string[][].
	if rest, ok := withoutEmptyArrayElems(lit); ok {
		for _, el := range rest.Elements {
			if t := e.inferExprType(el); t.IsArray() && !t.IsDynamic() {
				return e.inferArrayType(rest)
			}
		}
	}
	// A spread tuple contributes its elements (tupleCommonElem).
	for _, el := range lit.Elements {
		if sp, ok := el.(*ast.SpreadElement); ok {
			if t := e.inferExprType(sp.Arg); t.IsTuple() && !t.IsDynamic() {
				if ce := tupleCommonElem(t, 0); ce.IsDynamic() {
					return ArrayOf(TypeAny)
				} else if el == lit.Elements[0] {
					return ArrayOf(ce)
				}
			}
		}
	}
	// A spread beside elements of other kinds (`["a", ...nums, true]`):
	// each spread adds its source's element kind, and kinds that differ
	// make the elements boxed, as the union TypeScript infers holds them.
	if hasSpread(lit) {
		kinds := map[string]bool{}
		known := true
		for _, el := range lit.Elements {
			var t Type
			if sp, ok := el.(*ast.SpreadElement); ok {
				st := e.inferExprType(e.collectionSpreadSource(sp.Arg))
				if !st.IsArray() || st.ElemType == nil {
					known = false
					break
				}
				t = *st.ElemType
			} else {
				t = e.inferExprType(el)
			}
			if t.IR == "" || nullishLiteralType(t) {
				continue
			}
			kinds[elemKindKeyBroad(t)] = true
		}
		if known && len(kinds) >= 2 {
			return ArrayOf(TypeAny)
		}
	}
	first := lit.Elements[0]
	if sp, ok := first.(*ast.SpreadElement); ok {
		// Spread of an array — infer from the spread source. Spreading a string
		// (`[..."abc"]`) yields single-character strings.
		ty := e.inferExprType(e.collectionSpreadSource(sp.Arg))
		if ty.IsArray() {
			return ty
		}
		if isStringTy(ty) && !ty.IsClass() && !ty.IsObject() {
			return ArrayOf(TypePtr) // char array: single-character strings
		}
		// An iterable object (a class with `[Symbol.iterator]`): its
		// protocol's values, collected boxed (resolveArrayForHOF).
		if (ty.IsClass() || ty.IsObject()) && e.hasIteratorMember(ty) {
			return ArrayOf(TypeAny)
		}
		return ArrayOf(TypeF64)
	}
	// Under -compat=js a genuinely heterogeneous literal (`[1, "two", true]`)
	// lowers to a boxed-element array (`any[]`, TDD-00200) rather than being
	// rejected at element coercion — consistent with the inferred empty-array
	// Stage 2 path (TDD-00205). The element emission boxes each value (the same
	// path an explicit `any[]` literal already uses). Strict keeps the uniform-
	// only behavior (its existing element-coercion rejection stands).
	if e.compatJS() {
		// Only holes, `undefined` and `null` (`[, , ,]`): TypeScript widens
		// the nullish literals in a JavaScript file, so it is any[].
		nullish := true
		for _, el := range lit.Elements {
			if _, ok := el.(*ast.NullLiteral); !ok {
				nullish = false
				break
			}
		}
		if nullish {
			return ArrayOf(TypeAny)
		}
		kinds := map[string]bool{}
		allConcrete := true
		for _, el := range lit.Elements {
			if _, ok := el.(*ast.SpreadElement); ok {
				allConcrete = false
				break
			}
			kinds[elemKindKeyBroad(e.inferExprType(el))] = true
		}
		if allConcrete && len(kinds) >= 2 {
			return ArrayOf(TypeAny)
		}
	}
	// A mixed literal (`[new P(), 2]`) is an array of the union of its
	// element types, as TypeScript infers it.
	if u, ok := e.inferMixedArrayElem(lit); ok {
		return ArrayOf(u)
	}
	// Other kinds mixed (a string and two object shapes): boxed elements,
	// rather than every element read as the first's type.
	if mixedKindsBoxed(lit, e.inferExprType) {
		return ArrayOf(TypeAny)
	}
	// One kind with null or undefined among it (`[null, { k: 1 }]`): that
	// kind, possibly absent when it is a reference, as TypeScript's
	// `({ k: number } | null)[]` — not the first element's `null`.
	elem := e.inferExprType(first)
	hasNull, hasUndef := false, false
	for _, el := range lit.Elements {
		t := e.inferExprType(el)
		if nullishLiteralType(t) {
			hasNull = hasNull || !t.IsUndefined()
			hasUndef = hasUndef || t.IsUndefined()
			continue
		}
		if nullishLiteralType(elem) {
			elem = t
		}
	}
	nullish := hasNull || hasUndef
	switch {
	case !nullish || nullishLiteralType(elem):
	case elem.IR == "ptr" && !elem.IsArray():
		if hasNull && hasUndef && threeStateEligible(elem) {
			elem = withNullAndUndef(elem)
			break
		}
		elem.setNullable(true)
		elem.setUndefined(hasUndef && !hasNull)
	case nonPtrScalar(elem) && !elem.IsDynamic() && !isNullableScalar(elem):
		// `[1, null, undefined]` is `(number | null | undefined)[]`: each
		// absent element stays absent, not a zero.
		switch {
		case hasNull && hasUndef && threeStateEligible(elem):
			elem = withNullAndUndef(elem)
		case hasUndef:
			elem = undefinedableElem(elem)
		default:
			elem.setNullable(true)
		}
	}
	return ArrayOf(elem)
}

// withoutEmptyArrayElems is lit without its empty array elements, when it
// has one and another element besides.
func withoutEmptyArrayElems(lit *ast.ArrayLiteral) (*ast.ArrayLiteral, bool) {
	var rest []ast.Expression
	empty := false
	for _, el := range lit.Elements {
		if al, ok := el.(*ast.ArrayLiteral); ok && len(al.Elements) == 0 {
			empty = true
			continue
		}
		if _, ok := el.(*ast.SpreadElement); ok {
			return nil, false
		}
		rest = append(rest, el)
	}
	if !empty || len(rest) == 0 {
		return nil, false
	}
	out := *lit
	out.Elements = rest
	return &out, true
}

// nullishLiteralType reports the type of a null or undefined value itself
// (TypeNull, TypeUndefined), not a type that may hold one.
func nullishLiteralType(t Type) bool {
	return t.IsNull() || t.IsUndefined() && t.IR == ""
}

// inferMixedArrayElem is the union element type of a literal whose elements
// are of two or more kinds, each a scalar, an array or an object with a
// header word; ok is false for a uniform literal or one with any other
// element (a spread, a function, a host object).
func (e *Emitter) inferMixedArrayElem(lit *ast.ArrayLiteral) (Type, bool) {
	return mixedArrayElem(lit, e.inferExprType)
}

// mixedArrayElem is inferMixedArrayElem over typeOf, each element's type.
func mixedArrayElem(lit *ast.ArrayLiteral, typeOf func(ast.Expression) Type) (Type, bool) {
	var members []Type
	seen := map[string]bool{}
	nullable, emptyArr := false, false
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			return Type{}, false
		}
		if al, ok := el.(*ast.ArrayLiteral); ok && len(al.Elements) == 0 {
			// `[]` is `never[]`: it fits any array member.
			emptyArr = true
			continue
		}
		t := typeOf(el)
		if t.IsNull() || t.IsUndefined() && t.IR == "" {
			nullable = true
			continue
		}
		var key string
		switch {
		case t.IsPromise() && !t.IsDynamic():
			key = "promise:" + reprKey(t) // a union member, as an annotation holds one
		case t.IsDynamic() || t.IsFunc() || t.IsMap() || t.IsSet() || t.IsReadableStream() || t.IsBigInt():
			return Type{}, false
		case scalarTypeKind(t) != "" && !t.IsObject() && !t.IsClass():
			key = scalarTypeKind(t)
			if t.IsStrLiteral() {
				t = TypePtr
			}
			if key == "number" {
				t = TypeF64
			}
		case t.IsArray():
			key = "array:" + arrayStorageKey(t)
		case t.IsClass() && hasObjHeader(t) && !t.IsError():
			key = "class:" + t.ClassName
		case plainRecordType(t):
			key = "object:" + layoutKey(t)
		default:
			return Type{}, false
		}
		if !seen[key] {
			seen[key] = true
			members = append(members, t.withoutNullable())
		}
	}
	// A promise of never (`Promise.reject(…)`) is a subtype of every other
	// promise: the union keeps only those (TypeScript's subtype reduction).
	promises := 0
	for _, m := range members {
		if m.IsPromise() && (m.PromiseType == nil || !m.PromiseType.IsNever()) {
			promises++
		}
	}
	if promises > 0 {
		kept := members[:0:0]
		for _, m := range members {
			if !(m.IsPromise() && m.PromiseType != nil && m.PromiseType.IsNever()) {
				kept = append(kept, m)
			}
		}
		members = kept
	}
	if emptyArr {
		hasArr := false
		for _, m := range members {
			hasArr = hasArr || m.IsArray()
		}
		if !hasArr {
			members = append(members, ArrayOf(TypeF64))
		}
	}
	if len(members) < 2 {
		return Type{}, false
	}
	objects := 0
	for _, m := range members {
		if m.IsObject() || m.IsClass() {
			objects++
		}
	}
	if objects > 1 {
		return Type{}, false // two object members need a discriminant (BACKLOG 71)
	}
	return Type{IR: TypeAny.IR, Kind: kindDynamic, UnionMembers: members, Absent: absenceOf(nullable, false, false)}, true
}

// inferObjectType determines field types by inspecting the literal's values.
// inferObjectType computes the merged field layout for an object literal.
// A field's position is fixed by its first occurrence (via a spread's source
// fields or an explicit property); a later property or spread with the same
// name overrides its type in place rather than moving it — matching JS's
// object spread semantics, where re-assigning an existing key doesn't change
// its enumeration order.
func (e *Emitter) inferObjectType(lit *ast.ObjectLiteral) Type {
	if lit.HasComputedKey() {
		return e.inferDynamicObjectType(lit)
	}
	if e.hasDynamicSpread(lit) || hasProtoKey(lit) {
		return TypeAny
	}
	if lit.HasAccessors() {
		// TDD-00153: an accessor-bearing literal is a synthetic-class instance;
		// its type must be that class (registered lazily & idempotently so the
		// variable-slot type computed here matches the value the emit site
		// produces). Registration builds only class metadata, never IR.
		return e.classes[e.ensureObjLitClass(lit)].Ty
	}
	var fields []Field
	upsert := func(f Field) {
		for i, existing := range fields {
			if existing.Name == f.Name {
				fields[i] = f
				return
			}
		}
		fields = append(fields, f)
	}
	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			srcTy := e.inferExprType(spread.Arg)
			if _, ok := e.falsyUnionSpreadType(spread.Arg, srcTy); ok {
				// `false | { j }`: the object's fields, each maybe absent,
				// as the checker types the literal.
				if c := e.front(); c != nil {
					if lt := c.TypeOf(lit); !c.Unanswered(lt) {
						if r, rok := e.repr().of(lt); rok && r.IsObject() && !r.IsDynamic() {
							return r
						}
					}
				}
				return TypeAny
			}
			for _, f := range srcTy.SpreadFields() {
				upsert(f)
			}
			continue
		}
		ft := e.inferExprType(prop.Value)
		if fe, ok := prop.Value.(*ast.FunctionExpression); ok && fe.IsGenerator {
			// A generator function is held boxed, as a generator method is; a
			// function that returns a generator (`() => gen()`) is not one.
			ft = TypeAny
		}
		upsert(Field{Name: prop.Key, Ty: ft})
	}
	return UserObjectType(fields)
}

// hasProtoKey reports whether an object literal uses the `__proto__: p`
// special form (a non-computed key): it sets the prototype rather than
// defining a field, so the literal builds as a D1 dynamic object, whose
// literal path implements the form (TDD-00229). The shorthand `{ __proto__ }`
// defines an ordinary property, as in JavaScript.
func hasProtoKey(lit *ast.ObjectLiteral) bool {
	for _, prop := range lit.Properties {
		if prop.KeyExpr == nil && prop.Key == "__proto__" && prop.AccessorKind == "" && !prop.Shorthand {
			return true
		}
	}
	return false
}

// hasDynamicSpread reports whether an object literal spreads a bare any/
// unknown value (`{ ...process.env, X: 1 }`), an array or a function: its
// key set exists only at run time, so the literal builds as a D1 dynamic
// object (ADR-01077).
func (e *Emitter) hasDynamicSpread(lit *ast.ObjectLiteral) bool {
	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" && prop.KeyExpr == nil {
			if t := e.inferExprType(spread.Arg); isUnconstrainedDynamic(t) || t.IsArray() || t.IsFunc() || isSyntheticObjLitClass(t) {
				return true
			}
		}
	}
	return false
}

// inferDynamicObjectType is the type of an object literal with a computed
// key (`{ [expr]: value }`): the object itself, a dictionary of its values'
// one static type when they share it (methods and spreads excepted), else
// `any`.
func (e *Emitter) inferDynamicObjectType(lit *ast.ObjectLiteral) Type {
	var valTy *Type
	for _, prop := range lit.Properties {
		if _, ok := prop.Value.(*ast.SpreadElement); ok || prop.AccessorKind != "" {
			return TypeAny
		}
		t := e.inferExprType(prop.Value)
		if t.IsDynamic() || t.IsFunc() || t.IR == "" {
			return TypeAny
		}
		if valTy == nil {
			valTy = &t
		} else if reprKey(*valTy) != reprKey(t) {
			return TypeAny
		}
	}
	if valTy == nil {
		return TypeAny
	}
	return dictViewType(*valTy)
}

// callbackReturnType returns a HOF callback argument's inferred return
// type, purely (no codegen) — whether it's a literal arrow function, a
// named top-level function reference, or a closure-typed variable, the
// same three shapes resolveCallback (emit_func.go) resolves at emission
// time. Used by .map()/.flatMap()'s own inferExprType case: previously
// only a literal arrow function was recognized, so `arr.map(namedFn)`
// silently fell through to "same type as the receiver" — usually
// harmless by coincidence (a callback that happens to return the same
// element type the receiver already has), but genuinely wrong whenever it
// doesn't (found via `matrix.map(rowSum)`, matrix: number[][], rowSum
// returning a plain number — the result was mistyped as number[][]
// instead of number[]). Not a general callback-type inference utility;
// only handles what .map()/.flatMap() need.
// asyncClosureRetType normalizes an async arrow/function-expression's inferred
// return type into the task-shaped `Promise<T>` a call through the closure yields
// (TDD-00084 Part A): a non-async closure keeps ret; an async one wraps ret's
// inner type in a PromiseTask promise (unwrapping an already-`Promise<T>`
// annotation first), so `await`/`.then` on the result take the task path.
func asyncClosureRetType(isAsync bool, ret Type) Type {
	if !isAsync {
		return ret
	}
	if ret.IsPromise() {
		// An annotated `Promise<T>` (or `Promise<void>`, with no element
		// type) is already the promise: tag it, never wrap it again.
		ret.PromiseTask = true
		return ret
	}
	pt := PromiseOf(ret)
	pt.PromiseTask = true
	return pt
}

// taskTaggedRet tags a method/function signature's return type PromiseTask when
// it's an async `Promise<T>`, so a later `await`/`.then` on the call takes the
// task path — the same treatment the plain-function call path applies (TDD-00087
// follow-up: async methods now emit task-struct promises like async functions).
func taskTaggedRet(sig FuncSig) Type {
	if sig.IsAsync && sig.RetType.IsPromise() {
		rt := sig.RetType
		rt.PromiseTask = true
		return rt
	}
	return sig.RetType
}

// callbackReturnType infers a callback's return type for the array/promise
// method return-type inference. The optional paramHints supply the caller's
// known parameter types (e.g. a HOF receiver's element type) so an unannotated
// param resolves to the same type the actual emission (emitArrowFunctionWithHints)
// binds it to — without them an unannotated param would default to a scalar and
// the inferred type would disagree with the emitted one (TDD-00123: matters now
// that `number` is float64, so a mis-defaulted i64 corrupts the read type).
// hofElemHint returns the element type of an array/HOF receiver expression, to
// hint a callback's first parameter type during return-type inference. Falls
// back to TypeF64 (a bare `number`) when the receiver's element type isn't
// statically known — the correct default now that `number` is a double.
func (e *Emitter) hofElemHint(recv ast.Expression) Type {
	if ty := e.inferExprType(recv); ty.IsArray() && ty.ElemType != nil {
		return *ty.ElemType
	}
	return TypeF64
}

func (e *Emitter) callbackReturnType(arg ast.Expression, paramHints ...Type) (Type, bool) {
	hintFor := func(i int, pt *ast.TypeAnnotation) Type {
		if pt != nil {
			return e.resolveType(pt)
		}
		if i < len(paramHints) {
			return paramHints[i]
		}
		return TypeF64
	}
	switch cb := arg.(type) {
	case *ast.ArrowFunction:
		if cb.RetType != nil {
			return e.resolveType(cb.RetType), true
		}
		if cb.Body != nil {
			e.pushScope()
			for i, p := range cb.Params {
				e.definePatternParamForInference(p, hintFor(i, p.Type), i)
			}
			rt := e.inferExprType(cb.Body)
			e.popScope()
			return rt, true
		}
		if cb.Block != nil {
			paramTypes := make([]Type, len(cb.Params))
			for i, p := range cb.Params {
				paramTypes[i] = hintFor(i, p.Type)
			}
			if inferred, ok := e.inferUnannotatedReturnTypeParams(cb.Block, cb.Params, paramTypes); ok {
				return inferred, true
			}
		}
		return TypeF64, true
	case *ast.FunctionExpression:
		if cb.RetType != nil {
			return e.resolveType(cb.RetType), true
		}
		paramTypes := make([]Type, len(cb.Params))
		for i, p := range cb.Params {
			paramTypes[i] = hintFor(i, p.Type)
		}
		if inferred, ok := e.inferUnannotatedReturnTypeParams(cb.Body, cb.Params, paramTypes); ok {
			return inferred, true
		}
		return TypeVoid, true
	case *ast.Identifier:
		if _, sig, found := e.resolveFuncRef(cb.Name); found {
			return sig.RetType, true
		}
		if sym, found := e.lookup(cb.Name); found && sym.Ty.IsFunc() && sym.Ty.FuncRetType != nil {
			return *sym.Ty.FuncRetType, true
		}
	}
	// Any other function value: a builtin (`Number`, `Math.sqrt`), a bound
	// function, a function-typed field.
	if t := e.inferExprType(arg); t.IsFunc() && t.FuncRetType != nil {
		return *t.FuncRetType, true
	}
	return Type{}, false
}

// inferBuiltPreamble is the answer for a node emission dispatches before its
// kind: a boxed field's call, a built-in function value, a chain past a
// `?.`, a `globalThis.X` read or call.
func (e *Emitter) inferBuiltPreamble(expr ast.Expression) (Type, bool) {
	// A call of a fixed-layout object's boxed field (a generator method) is
	// a dynamic call: a box (emitCall).
	if call, ok := expr.(*ast.CallExpression); ok {
		if mem, ok := call.Callee.(*ast.MemberExpression); ok && !mem.Optional {
			if ot := e.inferExprType(mem.Object); ot.IsObject() && !ot.IsDynamic() && !ot.IsClass() {
				for _, f := range ot.Fields {
					if f.Name == mem.Property && isUnconstrainedDynamic(f.Ty) {
						return TypeAny, true
					}
				}
			}
		}
	}
	if b, ok := e.builtinRef(expr); ok {
		if _, sig, err := e.ensureBuiltinFunc(b); err == nil {
			return funcTypeFromSig(sig), true
		}
	}
	// A chain that continues past a `?.` — mirror emitOptionalChain.
	if ty, ok := e.inferOptionalChain(expr); ok {
		return ty, true
	}
	// `globalThis.X` (and its call form) infers as the bare global X — mirror
	// the same alias-peeling emitMember/emitCall apply, so member-type-driven
	// dispatch agrees with codegen.
	if mem, ok := expr.(*ast.MemberExpression); ok {
		if unwrapped := e.unwrapGlobalThis(mem); unwrapped != ast.Expression(mem) {
			return e.inferExprType(unwrapped), true
		}
	}
	if call, ok := expr.(*ast.CallExpression); ok {
		if unwrapped := e.unwrapGlobalThis(call.Callee); unwrapped != call.Callee {
			if l, ok := e.loweredCall(call); ok {
				if t := l.resultType(); t.IR != "" || l.intrinsic == "" {
					return t, true
				}
			}
			rewritten := ast.NewCallExpression(unwrapped, call.Args, call.GetPos())
			rewritten.TypeArgs = call.TypeArgs
			return e.inferExprType(rewritten), true
		}
	}
	return Type{}, false
}

func (e *Emitter) inferExprTypeUncached(expr ast.Expression) Type {
	if t, ok := e.inferBuiltPreamble(expr); ok {
		return t
	}
	switch ex := expr.(type) {
	case *ast.NewTargetExpression:
		// What emitNewTarget emits: the constructor read from the receiver
		// in a class constructor, else undefined or the function (any).
		pos := ex.GetPos()
		switch ex.Ctx {
		case ast.NewTargetConstructor:
			if _, ok := e.lookup("this"); ok {
				return e.inferExprType(ast.NewMemberExpression(ast.NewThisExpression(pos), "constructor", pos))
			}
		case ast.NewTargetFunction:
			if fn := e.newTargetFunc(); fn != "" && ex.FnID != 0 && ex.FnID == e.newTargetDecl.NewTargetID {
				if _, ok := e.lookup("this"); ok {
					return TypeAny
				}
			}
		}
		return e.inferExprType(ast.NewNullLiteral(true, pos))
	case *ast.YieldExpression:
		// A `yield` expression is the value `.next(v)` sends: TNext, `any`
		// (emitYieldExpression). `yield*` is the inner's return value.
		if !ex.Delegate {
			return TypeAny
		}
		// Delegating to anything but a generator (an array's iterator, an
		// object's protocol): the value its done result carries, any.
		if ex.Argument != nil && !e.inferExprType(ex.Argument).IsGenerator() {
			return TypeAny
		}
	case *ast.AwaitExpression:
		// Unwrap Promise<T> → T.
		argTy := e.inferExprType(ex.Argument)
		if argTy.IsPromise() {
			if argTy.PromiseType != nil {
				return *argTy.PromiseType
			}
			return TypeVoid
		}
		// A thenable class instance: what its `then` fulfills with.
		if sig, ok := e.thenableClass(argTy); ok {
			return thenableValueType(sig)
		}
		// `await` of a non-thenable is identity — its type is the argument's
		// own (e.g. Response.text() → string), matching emitAwait's pass-through.
		return argTy
	case *ast.IndexExpression:
		return e.structuralIndexType(ex)
	case *ast.BinaryExpression:
		// Infer each operand's type exactly once, up front, and reuse it in
		// every branch below. Re-calling inferExprType(ex.Left/Right) separately
		// in the bigint check AND again in each operator branch made inference
		// cost O(2^depth) on a left-associative chain like `a + b + c + ...`
		// (each node re-inferred both children, which re-inferred theirs …) —
		// enough to peg a core for minutes on a deep expression, found as an
		// in-process hang while running the Test262 corpus. inferExprType does
		// no codegen and has no side effects, so inferring both operands eagerly
		// — even for `&&`/`||`/`??`, which may not need the right one — is free;
		// the bigint check already forced both anyway.
		lt := e.inferExprType(ex.Left)
		rt := e.inferExprType(ex.Right)
		// A dynamic operand makes an arithmetic result dynamic — the runtime
		// dispatch (TDD-00076 A2) yields a NaN-boxed word (`+` may
		// concatenate, others produce a boxed number); a comparison stays a
		// bool. TypeScript allows every operator on `any`.
		if isUnconstrainedDynamic(lt) || isUnconstrainedDynamic(rt) {
			switch ex.Op {
			case "===", "!==", "==", "!=", "<", ">", "<=", ">=":
				return TypeBool
			case "+":
				// With a string side it is concatenation: a string, as
				// emitBinary's ToString path produces.
				if isStringTy(lt) || isStringTy(rt) {
					return TypePtr
				}
				return TypeAny
			case "-", "*", "/", "%", "**":
				if anyArithIsNumber(ex.Op, lt, rt) {
					return TypeF64
				}
				return TypeAny
			}
		}
		// A mixed concrete scalar pair (`n * "4"`, `true + 1`) also routes
		// through the runtime dispatch under `-compat=js` — its result is a
		// boxed dynamic value too.
		if e.compatJS() {
			lk, rk := scalarTypeKind(lt), scalarTypeKind(rt)
			if lk != "" && rk != "" && lk != rk {
				switch ex.Op {
				case "<", ">", "<=", ">=":
					return TypeBool
				case "+":
					// `+` with a string side is always concatenation → string
					// (the pre-existing concat path emits it; only a
					// string-free mixed pair reaches the dynamic dispatch).
					if lk == "string" || rk == "string" {
						return TypePtr
					}
					return TypeAny
				case "-", "*", "/", "%", "**":
					return TypeAny
				}
			}
		}
		// Bare null/undefined in arithmetic/relational routes through the runtime
		// dispatch under -compat=js (null→0, undefined→NaN) — see emitBinary
		// (ADR-00901). Infer TypeAny / bool so the slot type agrees with the boxed
		// value emitBinary stores (otherwise `var x = null + undefined` infers a
		// string/ptr slot and the boxed result is mis-stored). A real-string side
		// keeps the concat result (string); object/array operands keep their paths.
		if e.compatJS() {
			lNullish := (lt.IsNull() || lt.IsUndefined()) && !lt.UncheckedIndex
			rNullish := (rt.IsNull() || rt.IsUndefined()) && !rt.UncheckedIndex
			lRealStr := isStringTy(lt) && !lNullish
			rRealStr := isStringTy(rt) && !rNullish
			if (lNullish || rNullish) && !lRealStr && !rRealStr &&
				!lt.IsObject() && !rt.IsObject() && !lt.IsArray() && !rt.IsArray() {
				switch ex.Op {
				case "<", ">", "<=", ">=":
					return TypeBool
				case "+", "-", "*", "/", "%", "**":
					return TypeAny
				}
			}
		}
		// A bigint operand makes an arithmetic/bitwise result a bigint (a
		// comparison stays a bool) — so `const x = 2n ** 53n + 1n` types as
		// bigint, not the i64/string the generic cases below would infer.
		if lt.IsBigInt() || rt.IsBigInt() {
			switch ex.Op {
			case "===", "!==", "==", "!=", "<", ">", "<=", ">=":
				return TypeBool
			default:
				return BigIntType()
			}
		}
		switch ex.Op {
		case "===", "!==", "==", "!=", "<", ">", "<=", ">=", "instanceof", "in":
			return TypeBool
		case "+":
			if isStringTy(lt) || isStringTy(rt) {
				return TypePtr
			}
			// A Date operand concatenates its date string (emitBinary).
			if lt.IsDate() || rt.IsDate() {
				return TypePtr
			}
			if lt.Float || rt.Float {
				// Mixed int/float promotes to double — must match
				// emitBinary's numeric-promotion rule.
				return TypeF64
			}
			return TypeI64
		case "-":
			// A Date operand is its time value: a number (emitBinary).
			if lt.IsDate() || rt.IsDate() {
				return TypeF64
			}
			if lt.Float || rt.Float {
				return TypeF64
			}
			return TypeI64
		case "*", "/", "%":
			// Same promotion rule as emitBinary: any float operand makes the
			// result a double; all-integer stays exact i64.
			if lt.Float || rt.Float {
				return TypeF64
			}
			return TypeI64
		case "**":
			// Exponentiation promotes to float if either operand is float,
			// otherwise stays this compiler's default i64 `number` — matching
			// emitBinary's own `**` result type.
			if lt.Float || rt.Float {
				return TypeF64
			}
			return TypeI64
		case "&&", "||":
			// Mirrors emitShortCircuit exactly: the value-preserving type when
			// there is one, else the bool the emitter falls back to.
			if ty, ok := andAbsentScalar(ex.Op, lt, rt); ok {
				return ty
			}
			if ty, ok := shortCircuitValueType(ex.Op, lt, rt); ok {
				return ty
			}
			return TypeBool
		case "??":
			return nullCoalesceType(lt, rt)
		case "&", "|", "^", "<<", ">>", ">>>":
			// JS bitwise/shift ops compute in the 32-bit integer domain but
			// return a Number (double) — TDD-00123 Stage 2. Must mirror
			// emitBinary/emitBitShift's sitofp-to-double result.
			return TypeF64
		}
	case *ast.SuperExpression:
		// `super` is bound in scope typed as the base class (emitClassMember), so
		// `super.method()` resolves against the base's method table — mirror that
		// here so a method returning `super.method()` infers the base method's
		// return type instead of the i64 default (ADR-00906).
		if sym, ok := e.lookup("super"); ok {
			if this, ok := e.lookup("this"); ok && !sym.Ty.IsClass() {
				// A class extending Array: its super's methods are Array's,
				// run on the instance's body (emitSuperMethodCall).
				if info, ok := e.classes[this.Ty.ClassName]; ok && info.HostBase != nil && info.HostBase.IsArray() {
					return *info.HostBase
				}
			}
			return sym.Ty
		}
	case *ast.NewExpression:
		if e.isGlobalObjectCtor(ex.ClassName, ex.Qualified) {
			return TypeAny // `new Object(…)`: a dynamic object
		}
		if e.isPrimWrapperCtor(ex.ClassName, ex.Qualified) {
			return TypeAny // a wrapper object is held boxed
		}
		if target, ok := e.constClassAlias(ex); ok {
			alias := *ex
			alias.ClassName = target
			return e.inferExprType(&alias) // `const K = C; new K(…)`
		}
		if instTy, ok := e.dynNewInstanceType(ex); ok {
			return instTy
		}
		// new Promise<T>(executor) → task Promise<T> (TDD-00087).
		if ex.ClassName == "Promise" {
			valTy := e.newPromiseValueType(ex)
			pt := PromiseOf(valTy)
			pt.PromiseTask = true
			return pt
		}
		// new DynamicLibrary(path) → the node:ffi handle (TDD-00164).
		if _, user := e.classes[ex.ClassName]; ex.ClassName == "Response" && !user && !ex.Qualified {
			return ResponseType()
		}
		// new FinalizationRegistry<T>(cb) → the registry handle (TDD-00163).
		if ex.ClassName == "FinalizationRegistry" {
			held := TypeI64
			if len(ex.TypeArgs) == 1 && ex.TypeArgs[0] != nil {
				held = e.resolveType(ex.TypeArgs[0])
			}
			if len(ex.Args) == 1 {
				if cbTy := e.inferExprType(ex.Args[0]); cbTy.IsFunc() && len(cbTy.FuncParams) == 1 {
					held = cbTy.FuncParams[0]
				}
			}
			return FinalizationRegistryType(held)
		}
		if info, ok := e.classes[ex.ClassName]; ok && info.Ty.IsObject() {
			return info.Ty
		}
		// During class registration, a class's own unannotated-field inference
		// (`p = new C()`) can reference a sibling class whose ClassInfo already
		// exists in e.classes but whose `.Ty` is not finalized until a later pass
		// (a zero Type with no fields) — and, for a sibling not yet reached in the
		// topological pass, is absent entirely. Either way the nominal class type
		// published into e.interfaces (registerClassNamePlaceholders, then the
		// provisional in registerClasses) is a real `IsClass` object type that
		// canonicalizeClassTy re-resolves to its full field shape on demand at
		// each access. Returning it gives the field C's real object shape instead
		// of falling through to the zero-Type default, which produced an empty
		// struct-field IR and a "field access on non-object" on `obj.p.x`.
		if it, ok := e.interfaces[ex.ClassName]; ok && it.IsClass() {
			return it
		}
		// A vanilla-JS prototype-constructor instance is a dynamic object
		// (TDD-00155 Stage 4, `-compat=js`); a Proxy is one too (Stage 7).
		if ex.ClassName == "Proxy" {
			return TypeAny
		}
		if e.jsProtoCtor[ex.ClassName] {
			return TypeAny
		}
		// A generic class (TDD-00010 V1) is never itself in e.classes — ask
		// genericClassInstanceType for the shape its `new
		// ClassName<T>(...)` instantiation would have, purely (no IR
		// emission, no e.classes registration): inferExprType must never
		// trigger real emission as a side effect of merely asking "what
		// type is this."
		if genDecl, ok := e.genericClasses[ex.ClassName]; ok {
			if targs, ok := e.newTypeArgs(genDecl, ex); ok {
				subs := e.buildTypeArgSubs(genDecl.TypeParams, targs)
				if ty, err := e.genericClassInstanceType(genDecl, subs); err == nil {
					return ty
				}
			}
		}
	case *ast.TaggedTemplateExpression:
		// The desugaring emitExpr's own case uses: a tagged template's type is
		// its tag function's return type (TDD-00059).
		return e.inferExprType(e.desugarTaggedTemplate(ex))
	case *ast.NonNullExpression:
		// `expr!` strips null/undefined from the operand's type (TDD-00187).
		t := e.inferExprType(ex.Arg)
		t.setNullable(false)
		t.setUndefined(false)
		return t
	case *ast.AsExpression:
		if t, ok := e.nullAssertedType(ex); ok {
			return t // mirrors emitAsExpression
		}
		// `expr as T` (ADR-00929): a narrowing from a dynamic operand adopts the
		// asserted concrete type (`x as number` where `x: any` → number, so the
		// binding/operand it flows into is typed concretely); every other
		// assertion stays erased — the operand keeps its own type (ADR-00371).
		if lit, isLit := ex.Expr.(*ast.ArrayLiteral); isLit && ex.TypeAnnot != nil {
			if t := e.resolveType(ex.TypeAnnot); t.IsArray() && t.ElemType != nil && (t.ElemType.IsDynamic() || len(lit.Elements) == 0) {
				return t // mirrors emitAsExpression
			}
		}
		if lit, isLit := ex.Expr.(*ast.ObjectLiteral); isLit && ex.TypeAnnot != nil {
			if t := e.resolveType(ex.TypeAnnot); t.IsObject() && !t.IsDynamic() && !t.IsClass() && len(t.Fields) > 0 && literalKeysAreFields(lit, t) {
				return t // mirrors emitAsExpression
			}
		}
		if t, ok := e.tupleAsArray(ex); ok {
			return t
		}
		inner := e.inferExprType(ex.Expr)
		if !inner.IsDynamic() && inner.IR != "void" && ex.TypeAnnot != nil {
			t := e.resolveType(ex.TypeAnnot)
			if isUnconstrainedDynamic(t) {
				return t // the value boxed (mirrors emitAsExpression)
			}
			if e.nativeIfaceMembers[t.RefName] != nil && t.IR == inner.IR && t.StructIR() == inner.StructIR() {
				t.setNullable(inner.Nullable())
				return t // an interface over its native layout (mirrors emitAsExpression)
			}
		}
		if inner.IsDynamic() && ex.TypeAnnot != nil && e.keepsBoxedTypedArray(ex) {
			return inner // mirrors emitAsExpression
		}
		if inner.IsDynamic() && ex.TypeAnnot != nil {
			// A box (any, or a union) asserted to a concrete type unboxes to it
			// (mirrors emitAsExpression).
			if isUnconstrainedDynamic(inner) {
				return e.resolveType(ex.TypeAnnot)
			}
			if t := e.resolveType(ex.TypeAnnot); !t.IsDynamic() || isUnconstrainedDynamic(t) {
				return t // a member, or `any`: the same box, retyped
			}
		}
		return inner
	case *ast.UnaryExpression:
		if ex.Op == "delete" {
			return TypeBool // whether the key is gone (emitUnary)
		}
		switch ex.Op {
		case "typeof":
			return TypePtr
		case "!":
			return TypeBool
		case "-":
			// Unary - preserves the operand's numeric type (i64 or float); any
			// other operand is ToNumber'd to a double (emitUnary).
			at := e.inferExprType(ex.Arg)
			if at.IsBigInt() || at.IsDynamic() || at.Float || numericIntRepr(at) {
				return at
			}
			if isNullableScalar(at) {
				return e.inferExprType(&ast.UnaryExpression{Op: "+", Arg: ex.Arg})
			}
			return TypeF64
		case "+":
			// Unary + is ToNumber: a numeric operand keeps its type, anything
			// else (string, boolean, null/undefined, any, object) becomes a
			// double (mirrors emitUnaryPlus).
			at := e.inferExprType(ex.Arg)
			if at.Float || (at.IsInteger() && at.IR != "i1" && !at.IsDynamic() && !isNullableScalar(at)) {
				return at
			}
			return TypeF64
		case "~":
			// ToInt32(x) ^ -1 as a Number (emitUnary); a bigint stays one.
			if at := e.inferExprType(ex.Arg); at.IsBigInt() {
				return at
			}
			return TypeF64
		}
	case *ast.ConditionalExpression:
		// A test narrowing a union local (mirrors emitNarrowedConditional).
		if cons, alt, ok := e.narrowedCondBranchTypes(ex); ok {
			return narrowedCondResult(cons, alt)
		}
		// `cond ? scalar : null` is `T | null` (ADR-00538) — mirror
		// emitConditional so an unannotated `const x = b ? 3 : null` gets
		// nullable-scalar storage rather than a collapsed bare scalar.
		if nty, ok := ternaryNullableScalarType(e.inferExprType(ex.Consequent), e.inferExprType(ex.Alternate)); ok {
			return nty
		}
		// An array-valued ternary (mirrors emitConditionalArray).
		if aty, ok := e.conditionalArrayType(ex); ok {
			return aty
		}
		// A dynamic branch makes the whole ternary `any` (mirrors
		// emitConditionalAny).
		if e.inferExprType(ex.Consequent).IsDynamic() || e.inferExprType(ex.Alternate).IsDynamic() {
			return TypeAny
		}
		// Scalar branches of different kinds are their union (mirrors
		// emitConditional).
		if uTy, ok := ternaryUnion(e.inferExprType(ex.Consequent), e.inferExprType(ex.Alternate)); ok {
			return uTy
		}
		if nty, ok := ternaryNullablePointerType(e.inferExprType(ex.Consequent), e.inferExprType(ex.Alternate)); ok {
			return nty
		}
		if ternaryMixed(e.inferExprType(ex.Consequent), e.inferExprType(ex.Alternate)) {
			return TypeAny // mirrors emitConditional's boxed union
		}
		return e.inferExprType(ex.Consequent)
	case *ast.SequenceExpression:
		// The comma operator's value (and type) is its last operand's.
		if len(ex.Exprs) > 0 {
			return e.inferExprType(ex.Exprs[len(ex.Exprs)-1])
		}
		return TypeI64
	case *ast.NewCompressionStreamExpression:
		return compressionStreamType(ex.Decompress)
	case *ast.NewMapExpression:
		// Mirrors emitNewMapValue's K/V resolution (mapKVTypes): explicit
		// `<K, V>` wins, else infer from an initial `[K, V][]` entries array,
		// else the string-key/number-value defaults.
		keyTy, valTy := e.mapKVTypes(ex.KeyType, ex.ValType, ex.Init)
		return MapType(keyTy, valTy)
	case *ast.NewSetExpression:
		// Mirrors emitNewSetValue's shape without emitting the initializer.
		// Without type arguments the element is the checker's (an iterator
		// over the set reads it), else the string default.
		elemTy := TypePtr
		if ex.ElemType != nil {
			elemTy = e.resolveType(ex.ElemType)
		} else if c := e.front(); c != nil && ex.Init != nil {
			if ct := c.TypeOf(ex); !c.Unanswered(ct) {
				if rt, ok := e.repr().of(ct); ok && rt.IsSet() && rt.MapKey != nil {
					elemTy = *rt.MapKey
				}
			}
		}
		return SetType(elemTy)
	case *ast.NewWeakMapExpression:
		keyTy, valTy := e.weakMapKV(ex, nil)
		return WeakMapType(keyTy, valTy)
	case *ast.NewWeakSetExpression:
		return WeakSetType(e.weakSetElem(ex))
	case *ast.NewErrorExpression:
		return errorObjType
	case *ast.ImportCallExpression:
		// A dynamic import yields Promise<{ ...exports }>, a task promise.
		pt := PromiseOf(e.importCallResultObjectType(ex))
		pt.PromiseTask = true
		return pt
	case *ast.NewArrayBufferExpression:
		ty := ArrayBufferType()
		if ex.Shared {
			ty = SharedArrayBufferType()
		}
		ty.BufferGrowable = ex.MaxByteLength != nil
		return ty
	case *ast.NewXMLHttpRequestExpression:
		return XMLHttpRequestType()
	case *ast.ObjectLiteral:
		// Bare literals are dynamic under `-compat=js` (matching emitExpr's
		// routing); hint-typed contexts never consult this default.
		if e.compatJS() {
			return TypeAny
		}
		return e.inferObjectType(ex)
	case *ast.ArrayLiteral:
		// TDD-00028: inferExprType previously had no case for a bare array
		// literal at all (falling through to the final TypeI64 default) —
		// harmless while array literals could only ever appear in
		// var-decl position (var-decl's own inference went through
		// inferArrayType directly, never through here), but a real gap now
		// that a literal can appear as a general sub-expression: without
		// this, inferArrayType's own first-element inference couldn't tell
		// a nested array-literal element apart from any other unresolvable
		// expression, silently mistyping it as i64 instead of correctly
		// recognizing (and then, since real array-of-arrays storage isn't
		// supported yet, cleanly rejecting) it as array-typed. See
		// emitArrayLiteralAggregate's nested-array guard.
		return e.inferArrayType(ex)
	case *ast.ArrowFunction:
		params := make([]Type, len(ex.Params))
		for i, p := range ex.Params {
			if p.Rest && p.Type == nil {
				// Same default rest-element type emitArrowFunctionWithHints
				// (emit_func.go) and buildFunctionSig (emitter.go) already
				// give an unannotated rest param — this duplicate
				// computation (see the comment on the return-type mirror
				// below) needs to agree, not fall into the bare-scalar
				// unannotated-param default just below.
				params[i] = untypedRestType()
			} else if dt, ok := e.paramDefaultType(p); ok {
				params[i] = dt // mirrors emitArrowFunctionWithHints
			} else if ct, ok := e.checkerContextualParam(ex, i); p.Type == nil && ok {
				params[i] = ct
			} else if st, ok := e.jsSiteParam(ex, i); p.Type == nil && ok {
				params[i] = st // its call sites' type (-compat=js)
			} else if p.Type == nil && e.front() != nil {
				params[i] = TypeAny // an implicit any (mirrors emitArrowFunctionWithHints)
			} else if p.Type == nil {
				params[i] = TypeI64
				params[i].Inferred = true // no annotation — see docs/adr/00/ADR-00042.md
			} else {
				params[i] = e.resolveType(p.Type)
			}
			// Optional `x?: T` → `T | undefined`, matching the closure's actual
			// emitted parameter ABI (emit_func.go) so a `const f = …` symbol's
			// type agrees with the body — the same reason FuncHasRest is mirrored
			// here (TDD-00187).
			params[i] = optionalParamType(p, params[i])
		}
		var ret Type
		if ex.RetType != nil {
			ret = e.resolveType(ex.RetType)
		} else if ex.Body != nil {
			// The body is typed with its parameters in scope: `(s: string) =>
			// s.length` / `=> s[0]` are a number / a string only once `s` is
			// known — unresolved, they fell to the i64 default while the closure
			// itself was emitted returning double / ptr.
			e.pushScope()
			for i, p := range ex.Params {
				e.definePatternParamForInference(p, params[i], i)
			}
			ret = e.inferExprType(ex.Body)
			e.popScope()
		} else if hasReturn(ex.Block) {
			// Same best-effort inference emitArrowFunctionWithHints uses when
			// actually emitting this closure — this duplicate exists because
			// inferExprType has to answer "what type is this arrow function"
			// before any closure value exists yet (e.g. right when a `const`
			// binding to it is being declared). The two computations used to
			// disagree (this one unconditionally defaulted to TypeI64
			// regardless of what was returned), which silently mistyped the
			// variable itself even though the actual closure body was
			// correctly built to return an object/array/Date — a real bug,
			// not just a missed optimization, since callers trust this type.
			paramNames := make([]string, len(ex.Params))
			for i, p := range ex.Params {
				paramNames[i] = p.Name
			}
			if inferred, ok := e.inferUnannotatedReturnType(ex.Block, paramNames, e.bodyParamTypes(ex.Params, params)); ok {
				ret = inferred
			} else if firstReturnExpr(ex.Block) == nil {
				ret = TypeVoid // every return is bare (mirrors emitArrowFunctionWithHints)
			} else {
				ret = TypeF64
			}
		} else if blockAlwaysDiverges(ex.Block) {
			ret = TypeNever // block-body arrow that only throws (ADR-00869)
		} else {
			ret = TypeVoid
		}
		fty := FuncType(params, asyncClosureRetType(ex.IsAsync, ret))
		if len(ex.Params) > 0 && ex.Params[len(ex.Params)-1].Rest {
			fty.FuncHasRest = true
		}
		setFuncParamDefaults(&fty, ex.Params)
		return fty
	case *ast.FunctionExpression:
		// A generator expression is a boxed constructor closure (mirrors
		// emitGeneratorExpressionValue).
		if ex.IsGenerator {
			return TypeAny
		}
		// Same type-inference as ArrowFunction above — a function expression
		// is a block-only closure whose type is determined by param/return
		// types and captures. Params use the same resolver; unlike arrows,
		// function expressions never have expression bodies (Body is always
		// a BlockStatement, never an Expression).
		// Mirror emitFunctionExpression's implicit `arguments` rest (TDD-00210)
		// so the inferred type's arity matches the emitted function's.
		ex.Params = maybeAddArgumentsRestParams(ex.Params, ex.Body.Body)
		thisTaking := e.maybeAddThisParamExpr(ex)
		if !thisTaking && e.compatJS() && !ex.IsAsync && usesOwnThis(ex.Body.Body) && hasArgumentsRestParamSlice(ex.Params) {
			return TypeAny // stays dynamic (mirrors emitFunctionExpression)
		}
		params := make([]Type, len(ex.Params))
		for i, p := range ex.Params {
			if p.Rest && p.Name == argumentsRestParam {
				params[i] = ArrayOf(TypeAny) // implicit `arguments` rest (TDD-00210)
			} else if p.Rest && p.Type == nil {
				params[i] = untypedRestType()
			} else if dt, ok := e.paramDefaultType(p); ok && p.Type == nil {
				params[i] = dt
			} else if ct, ok := e.checkerContextualParam(ex, i); p.Type == nil && ok {
				params[i] = ct // mirrors emitFunctionExpression
			} else if st, ok := e.jsSiteParam(ex, i); p.Type == nil && ok {
				params[i] = st // its call sites' type (-compat=js)
			} else if p.Type == nil && e.front() != nil {
				params[i] = TypeAny // an implicit any (mirrors emitFunctionExpression)
			} else if p.Type == nil {
				params[i] = TypeI64
				params[i].Inferred = true
			} else {
				params[i] = e.resolveType(p.Type)
			}
			// Optional `x?: T` → `T | undefined` — mirror of the arrow case above.
			params[i] = optionalParamType(p, params[i])
		}
		var ret Type
		if ex.RetType == nil && hasReturn(ex.Body) {
			defer e.bindSelfForInference(ex, params)()
		}
		if ex.RetType != nil {
			ret = e.resolveType(ex.RetType)
		} else if hasReturn(ex.Body) {
			paramNames := make([]string, len(ex.Params))
			for i, p := range ex.Params {
				paramNames[i] = p.Name
			}
			if inferred, ok := e.inferUnannotatedReturnType(ex.Body, paramNames, e.bodyParamTypes(ex.Params, params)); ok {
				ret = inferred
			} else if firstReturnExpr(ex.Body) == nil {
				ret = TypeVoid // every return is bare (mirrors emitFunctionExpression)
			} else {
				ret = TypeF64
			}
		} else if blockAlwaysDiverges(ex.Body) {
			ret = TypeNever // function expression that only throws (ADR-00869)
		} else {
			ret = TypeVoid
		}
		fty := FuncType(params, asyncClosureRetType(ex.IsAsync, ret))
		if len(ex.Params) > 0 && ex.Params[len(ex.Params)-1].Rest {
			fty.FuncHasRest = true
		}
		fty.FuncThis = thisTaking
		setFuncParamDefaults(&fty, ex.Params)
		// Reading `arguments`, a call stores its count (emitFuncValue).
		fty.FuncCountsArgs = fty.FuncHasRest && ex.Params[len(ex.Params)-1].Name == argumentsRestParam
		return fty
	case *ast.UpdateExpression:
		// `x++`/`--x` yield the stepped number (or bigint); a dynamic or
		// nullable-scalar operand goes through ToNumeric first, so the result
		// is a plain `number` (ADR-01061).
		t := e.inferExprType(ex.Arg)
		if t.IsBigInt() || (!t.IsDynamic() && !t.Nullable() && (t.Float || t.IsInteger())) {
			return t
		}
		return TypeF64
	case *ast.AssignmentExpression:
		// A destructuring assignment's value is its right-hand side; the
		// object destructuring path holds a dynamic or computed-key source
		// boxed (emitDynDestructAssign).
		if ol, ok := ex.Left.(*ast.ObjectLiteral); ok && ex.Op == "=" {
			rt := e.inferExprType(ex.Right)
			if isUnconstrainedDynamic(rt) || ol.HasComputedKey() {
				return TypeAny
			}
			return rt
		}
		// An assignment's value is the value stored: the target's type
		// (`b += c` on a string `b` is a string).
		return e.inferExprType(ex.Left)
	}
	return TypeI64
}

// isPlainStringTy reports whether ty is a genuine string — not an object,
// array, closure, or any other ptr-backed builtin marker type (Map/Set/
// ArrayBuffer/TextEncoder/TextDecoder/Promise all share
// string's bare IR=="ptr" shape with none of the other flags isStringTy
// already excludes, so isStringTy alone isn't precise enough here). Used
// only by toBool: a string is the one JS primitive whose truthiness
// depends on its content (empty string is falsy) rather than "is this
// value present at all," and treating e.g. a Map as string-shaped would
// wrongly base its truthiness on the first byte of its internal
// representation instead of always being truthy like any other object.
func isPlainStringTy(ty Type) bool {
	// A host handle (a stream, a reader, a Blob, …) is a `ptr` but never a
	// string: its truthiness is its presence.
	return isStringTy(ty) && !ty.IsMap() && !ty.IsSet() &&
		!ty.IsArrayBuffer() && !ty.IsPromise() && !hasHandleFlag(ty)
}

// toBool converts a Value to i1 (truthiness).
//
// A bare integer 0 literal is invalid LLVM syntax against a ptr-typed
// operand (LLVM requires the `null` keyword) — found as a real, pre-
// existing bug while wiring RegExp.exec()'s `T[] | null` return: any bare
// ptr-typed truthiness check (`if (someObj)`, ...) previously emitted
// unparseable IR, a hard clang-stage failure, not just a wrong runtime
// answer. See ADR-00116.
//
// An array value is its header pointer. Only a Nullable array's truthiness
// depends on that pointer (null header = absent — see emitRegexExec); a
// non-Nullable array is always truthy regardless of its
// pointer, matching real JS ("any array, even an empty one, is truthy")
// and avoiding a false "falsy" result from libc's malloc(0) sometimes
// returning NULL for an ordinary empty array.
//
// A genuine string also needs its own path, found the same way: falsy for
// a real null (a `string | null` null value) OR an empty string (""),
// truthy for anything else — content-dependent, unlike every other
// ptr-backed value here, which is truthy whenever merely non-null. A bare
// "is the pointer null" check alone (what every other ptr type uses) would
// leave `if ("")` truthy, since an empty string is still a real, non-null
// 1-byte buffer, not a null pointer.
func (e *Emitter) toBool(v Value) Value {
	if v.Ty.NullAndUndef() {
		v = e.fromThreeState(v) // null and undefined are both falsy
	}
	// A nullable-scalar aggregate ({ i1 present, T }, TDD-00064) — e.g. the
	// value of an optional chain `obj?.a` (T | undefined) used directly in a
	// boolean context (`while (obj?.a)`, `if (obj?.b)`). Its truthiness is
	// `present && ToBoolean(payload)`: an absent value is `undefined`, which is
	// falsy. Checked before the `IR == "i1"` fast path below, since a nullable
	// bool reports payload IR "i1" and would otherwise be returned as the raw
	// { i1, i1 } aggregate where an `i1` is required — invalid IR
	// (`'…' defined with type '{ i1, i1 }' but expected 'i1'`).
	if isNullableScalar(v.Ty) {
		present, payload := e.nullableScalarAggParts(v)
		payloadBool := e.toBool(payload)
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", reg, present, payloadBool.Ref))
		return Value{Ref: reg, Ty: TypeBool}
	}
	if v.Ty.IR == "i1" {
		return v
	}
	// A caught value (the unpacked catch-value record, TDD-00202) is a { i8, i64 }
	// aggregate; its truthiness is that of the underlying value — reuse the
	// dynamic ToBoolean by widening the record to a NaN-boxed `any` first, rather
	// than emitting `icmp ne { i8, i64 }, 0` on the aggregate (invalid IR). Fixes
	// `Boolean(e)` / `if (e)` in a catch block.
	if v.Ty.IsCaught() {
		return e.toBool(e.emitCaughtToAny(v))
	}
	// A value-less or literally-nullish value is falsy (JS
	// ToBoolean(undefined)===false, ToBoolean(null)===false). This reaches here
	// from a void-returning predicate used by filter/some/every/find — a callback
	// with no `return` statement — where the result carries no value; producing
	// `false` avoids emitting an `icmp`/`br` against a `void` operand (invalid
	// IR). ADR-00687. A bare `null`/`undefined` literal (IsNull/IsUndefined but
	// NOT Nullable, Ref "null") also folds here.
	//
	// A *possibly*-undefined/null value — a `T | undefined` / `T | null` UNION
	// (Nullable set) — carries a real runtime representation (a `Map<K,string>`
	// miss is a null string ptr, a present key a real one), so it must NOT fold:
	// it falls through to the string/pointer runtime test below, where a null
	// pointer reads falsy and a present value truthy. Folding it to a constant
	// `false` was a pre-existing bug — `if (map.get(presentKey))` on a
	// `string`/object-valued map was wrongly false (ADR-00950). Nullable scalars
	// (`number | undefined`, the `{ i1, T }` presence path) are already handled
	// above and never reach here.
	if v.Ty.IR == "void" || v.Ty.IR == "" || ((v.Ty.IsUndefined() || v.Ty.IsNull()) && !v.Ty.Nullable()) {
		return Value{Ref: zeroRef(TypeBool), Ty: TypeBool}
	}
	// A dynamic value's truthiness is the real JS ToBoolean over the
	// NaN-boxed word (TDD-00076): false/null/undefined/±0/NaN/"" are false,
	// everything else true. Runs in both compat modes — truthiness has one
	// correct answer, unlike the js-gated operator dispatch.
	if v.Ty.IsDynamic() {
		return e.emitAnyTruthy(v)
	}
	if v.Ty.IsBigInt() {
		// 0n is falsy, every other bigint truthy — a bigint handle is never
		// null, so the generic ptr!=null path below would wrongly call 0n truthy.
		e.ensureBigInt()
		zero := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 0)", zero))
		cmpReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp(ptr %s, ptr %s)", cmpReg, v.Ref, zero))
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", reg, cmpReg))
		return Value{Ref: reg, Ty: TypeBool}
	}
	if v.Ty.IsArray() {
		if !v.Ty.Nullable() {
			return Value{Ref: "1", Ty: TypeBool}
		}
		// A nullable array is falsy when absent. Absence is the null header:
		// a `Map<K,T[]>` miss, a null `T[] | null` field, a RegExp.exec miss.
		// A present array, even an empty one, has a header, so it is truthy.
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", reg, e.emitArrayIsAbsent(v)))
		return Value{Ref: reg, Ty: TypeBool}
	}
	if isPlainStringTy(v.Ty) {
		return e.emitStringTruthiness(v)
	}
	reg := e.freshReg()
	switch {
	case v.Ty.Float:
		// "one" (ordered-and-not-equal) is NaN-safe: a NaN comparison is
		// "unordered," so this correctly evaluates false for NaN, matching
		// real JS's Boolean(NaN) === false.
		e.emitInstr(fmt.Sprintf("%s = fcmp one %s %s, 0.0", reg, v.Ty.IR, v.Ref))
	case v.Ty.IR == "ptr":
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", reg, v.Ref))
	default:
		e.emitInstr(fmt.Sprintf("%s = icmp ne %s %s, 0", reg, v.Ty.IR, v.Ref))
	}
	return Value{Ref: reg, Ty: TypeBool}
}

// emitStringTruthiness implements real JS string truthiness: falsy for a
// real null (a `string | null` null value — checked first, since loading a
// byte through a genuinely null pointer would be undefined behavior) or an
// empty string, truthy for anything else.
func (e *Emitter) emitStringTruthiness(v Value) Value {
	isNullReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNullReg, v.Ref))

	nullL := e.freshLabel("strbool.null")
	checkL := e.freshLabel("strbool.check")
	mergeL := e.freshLabel("strbool.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullReg, nullL, checkL))

	resultSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultSlot))

	e.emitLabel(nullL)
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(checkL)
	firstByte := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", firstByte, v.Ref))
	nonEmptyReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", nonEmptyReg, firstByte))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", nonEmptyReg, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resultSlot))
	return Value{Ref: result, Ty: TypeBool}
}

// inferCollectionCallType is the result type of a method call on a Map, Set,
// WeakRef or URLSearchParams receiver, shared by inferExprType and the
// collection intrinsics' declarations.
func (e *Emitter) inferCollectionCallType(ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	objTy, haveObjTy := Type{}, false
	if id, ok2 := mem.Object.(*ast.Identifier); ok2 {
		if sym, found := e.lookup(id.Name); found {
			objTy, haveObjTy = sym.Ty, true
		}
	}
	if !haveObjTy {
		objTy = e.inferExprType(mem.Object)
	}
	var table map[string]collTypeFn
	switch {
	case objTy.IsMap():
		table = mapMethodTypes
	case objTy.IsSet():
		table = setMethodTypes
	}
	if f, ok := table[mem.Property]; ok {
		return f(ex, mem.Property, objTy)
	}
	return Type{}, false
}

// objectStaticType is the type of an Object static's call, the Object.*
// intrinsics' answer.
func (e *Emitter) objectStaticType(prop string, ex *ast.CallExpression) (Type, bool) {
	if f, ok := objectStaticTypes[prop]; ok {
		return f(e, ex)
	}
	return Type{}, false
}

type callTypeFn func(e *Emitter, ex *ast.CallExpression) (Type, bool)

func fixedCallType(t Type) callTypeFn {
	return func(*Emitter, *ast.CallExpression) (Type, bool) { return t, true }
}

// objectStaticTypes are the Object statics' result types, by name. The
// prototype and descriptor statics operate on and return dynamic values
// (TDD-00155 Stages 3 and 5).
var objectStaticTypes map[string]callTypeFn

func init() {
	firstArg := func(e *Emitter, ex *ast.CallExpression) (Type, bool) {
		if len(ex.Args) >= 1 {
			return e.inferExprType(ex.Args[0]), true
		}
		return Type{}, false
	}
	objectStaticTypes = map[string]callTypeFn{
		"groupBy":                  fixedCallType(TypeAny),
		"create":                   fixedCallType(TypeAny),
		"getPrototypeOf":           fixedCallType(TypeAny),
		"setPrototypeOf":           fixedCallType(TypeAny),
		"defineProperty":           fixedCallType(TypeAny),
		"getOwnPropertyDescriptor": fixedCallType(TypeAny),
		"getOwnPropertyNames":      fixedCallType(ArrayOf(TypePtr)),
		"getOwnPropertySymbols":    fixedCallType(ArrayOf(SymbolType())),
		"isExtensible":             fixedCallType(TypeBool),
		"isSealed":                 fixedCallType(TypeBool),
		"isFrozen":                 fixedCallType(TypeBool),
		"hasOwn":                   fixedCallType(TypeBool),
		"keys":                     objectEnumType(objKeys),
		"values":                   objectEnumType(objValues),
		"entries":                  objectEnumType(objEntries),
		// A dynamic object (a Map<string,V>-backed value, TDD-00012),
		// V from the [string, V][] entries' tuple field 1.
		"fromEntries": func(e *Emitter, ex *ast.CallExpression) (Type, bool) {
			valTy := TypeI64
			if len(ex.Args) >= 1 {
				if e.fromEntriesKeyed(ex.Args[0]) {
					return TypeAny, true // mirrors emitObjectFromEntries
				}
				valTy, _ = e.fromEntriesTypes(ex.Args[0])
			}
			keyTy := TypePtr
			return Type{IR: "ptr", Host: hostDynamicObject, MapKey: &keyTy, MapVal: &valTy}, true
		},
		"assign": func(e *Emitter, ex *ast.CallExpression) (Type, bool) {
			if e.dynamicAssign(ex.Args) {
				return TypeAny, true // mirrors emitObjectAssign
			}
			if lit, ok := assignAsSpread(ex.Args); ok {
				return e.inferExprType(lit), true // mirrors emitObjectAssign
			}
			return firstArg(e, ex)
		},
		"freeze":            firstArg,
		"seal":              firstArg,
		"preventExtensions": firstArg,
	}
}

type objEnum int

const (
	objKeys objEnum = iota
	objValues
	objEntries
)

// objectEnumType is Object.keys/values/entries' result. A dynamic object
// (or any string-keyed Map<string,V>) delegates to the Map's own methods
// (TDD-00012) and so returns its typed keys and values; a bare any's
// values and entries are dynamic arrays (emitDynAnyEntries), its keys the
// string[] of emitDynAnyKeys; a homogeneous fixed-shape object keeps its
// values' type (ADR-00492).
func objectEnumType(which objEnum) callTypeFn {
	return func(e *Emitter, ex *ast.CallExpression) (Type, bool) {
		if len(ex.Args) >= 1 {
			argTy := e.inferExprType(ex.Args[0])
			if isUnconstrainedDynamic(argTy) && which != objKeys {
				return TypeAny, true
			}
			if argTy.IsMap() && argTy.MapKey != nil {
				keyTy := *argTy.MapKey
				valTy := TypeI64
				if argTy.MapVal != nil {
					valTy = *argTy.MapVal
				}
				switch which {
				case objKeys:
					return ArrayOf(keyTy), true
				case objValues:
					return ArrayOf(valTy), true
				}
				return ArrayOf(TupleType([]Type{keyTy, valTy})), true
			}
		}
		if which == objKeys {
			return ArrayOf(TypePtr), true
		}
		valTy, homogeneous := TypeAny, false
		if len(ex.Args) >= 1 {
			if argTy := e.inferExprType(ex.Args[0]); argTy.IsObject() && !isRecordView(argTy) {
				if vt, ok := homogeneousFieldType(presentFieldTypes(argTy.VisibleFields())); ok {
					valTy, homogeneous = vt, true
				}
			}
		}
		if which == objValues {
			if homogeneous {
				return ArrayOf(valTy), true
			}
			return ArrayOf(TypeAny), true
		}
		return ArrayOf(TupleType([]Type{TypePtr, valTy})), true
	}
}

// promiseStaticType is the type of a Promise static's call: a task promise.
// Promise.resolve(v) settles to v's type (an already-promise argument passes
// through); Promise.reject(e)'s value type is never observed (await
// re-throws); the combinators return a pending task promise
// (emit_promise_combinators.go, ADR-01193).
func (e *Emitter) promiseStaticType(prop string, ex *ast.CallExpression) (Type, bool) {
	if f, ok := promiseStaticTypes[prop]; ok {
		return f(e, prop, ex)
	}
	return Type{}, false
}

// staticTypeFn is a static method's result type, given its name and call.
type staticTypeFn func(e *Emitter, prop string, ex *ast.CallExpression) (Type, bool)

// taskPromise is a task Promise<t>.
func taskPromise(t Type) Type {
	pt := PromiseOf(t)
	pt.PromiseTask = true
	return pt
}

var promiseStaticTypes map[string]staticTypeFn

func init() {
	combinator := func(e *Emitter, prop string, ex *ast.CallExpression) (Type, bool) {
		if len(ex.Args) == 1 {
			if innerTy, err := e.promiseArrayElemType(ex.Args[0], prop, ex.GetPos()); err == nil {
				return taskPromise(e.combinatorResultType(prop, innerTy)), true
			}
		}
		return Type{}, false
	}
	promiseStaticTypes = map[string]staticTypeFn{
		"resolve": func(e *Emitter, _ string, ex *ast.CallExpression) (Type, bool) {
			if len(ex.Args) == 0 {
				return taskPromise(TypeVoid), true
			}
			argTy := e.inferExprType(ex.Args[0])
			if argTy.IsPromise() {
				return argTy, true
			}
			if sig, ok := e.thenableClass(argTy); ok {
				return taskPromise(thenableValueType(sig)), true
			}
			return taskPromise(argTy), true
		},
		"reject": func(*Emitter, string, *ast.CallExpression) (Type, bool) {
			return taskPromise(TypeNever), true
		},
		"all":        combinator,
		"race":       combinator,
		"allSettled": combinator,
		"any":        combinator,
	}
}

// arrayStaticType is the type of an Array static's call (Array.of,
// Array.from; Array.isArray is a boolean).
func (e *Emitter) arrayStaticType(prop string, ex *ast.CallExpression) (Type, bool) {
	if f, ok := arrayStaticTypes[prop]; ok {
		return f(e, prop, ex)
	}
	return Type{}, false
}

var arrayStaticTypes map[string]staticTypeFn

func init() {
	arrayStaticTypes = map[string]staticTypeFn{
		"isArray": func(*Emitter, string, *ast.CallExpression) (Type, bool) { return TypeBool, true },
		"of": func(e *Emitter, _ string, ex *ast.CallExpression) (Type, bool) {
			// The array literal of its arguments (emitArrayOf); none: unknown[].
			if len(ex.Args) > 0 {
				return e.inferExprType(ast.NewArrayLiteral(ex.Args, ex.GetPos())), true
			}
			return ArrayOf(TypeAny), true
		},
		"from": func(e *Emitter, _ string, ex *ast.CallExpression) (Type, bool) {
			// 2-arg mapFn form (ADR-00491): infer through the same
			// .map() desugar the emitter uses, so the closure's
			// parameters are typed contextually against the source
			// element type (inferring the bare closure here would
			// bind its `number` params to the standalone default
			// instead).
			if len(ex.Args) == 2 {
				fromCall := arrayFromCall(ex.GetPos(), ex.Args[:1])
				mapCall := ast.NewCallExpression(
					ast.NewMemberExpression(fromCall, "map", ex.GetPos()),
					ex.Args[1:2], ex.GetPos())
				return e.inferExprType(mapCall), true
			}
			if len(ex.Args) == 1 {
				argTy := e.inferExprType(ex.Args[0])
				if argTy.IsArray() {
					return ArrayOf(*argTy.ElemType), true
				}
				if argTy.IsCollIter() {
					return ArrayOf(collIterElemType(argTy)), true
				}
				// A generator's values (emitArrayFrom unboxes to its
				// element type); `any` or a static iterable → any[].
				if argTy.IsGenerator() && !argTy.GeneratorIsAsync {
					if argTy.GeneratorElemType != nil && !argTy.GeneratorElemType.IsDynamic() {
						return ArrayOf(*argTy.GeneratorElemType), true
					}
					return ArrayOf(TypeAny), true
				}
				if isUnconstrainedDynamic(argTy) || e.hasIteratorMember(argTy) {
					return ArrayOf(TypeAny), true
				}
				// Map → entries tuple array; Set → element array;
				// string → string[] (ADR-00482, matching emitArrayFrom).
				if argTy.IsMap() && !argTy.IsSet() {
					keyTy, valTy := TypePtr, TypePtr
					if argTy.MapKey != nil {
						keyTy = *argTy.MapKey
					}
					if argTy.MapVal != nil {
						valTy = *argTy.MapVal
					}
					return ArrayOf(TupleType([]Type{keyTy, valTy})), true
				}
				if argTy.IsSet() {
					elemTy := TypePtr
					if argTy.MapKey != nil {
						elemTy = *argTy.MapKey
					}
					return ArrayOf(elemTy), true
				}
				if isStringTy(argTy) && !argTy.IsClass() && !argTy.IsObject() {
					return ArrayOf(TypePtr), true
				}
				if argTy.IsClass() {
					if info, ok3 := e.classes[argTy.ClassName]; ok3 && info.HostBase == nil && !e.classHasMethodInherited(argTy.ClassName, syncIteratorMethodName) {
						if _, _, hasLen := argTy.FieldIndex("length"); !hasLen {
							return ArrayOf(TypeAny), true // an empty array-like read (emitArrayFrom)
						}
					}
				}
				// Array-like `{ length: n }` → an `any[]` of `undefined`
				// (ADR-00957), matching emitArrayFrom's undefined-fill path.
				if argTy.IsObject() && !argTy.IsArray() && !argTy.IsClass() && !argTy.IsTuple() {
					return ArrayOf(TypeAny), true
				}
			}
			return ArrayOf(TypeI64), true
		},
	}
}

// streamCallType is the result type of a stream, reader, writer or
// controller method call (emitStreamMethodCall, emitWStreamMethodCall).
func (e *Emitter) streamCallType(ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	objTy := e.inferExprType(mem.Object)
	if !(objTy.IsReadableStream() || objTy.IsStreamReader() || objTy.IsRSController() || objTy.IsWritableStream() || objTy.IsStreamWriter() || objTy.IsWSController()) {
		return Type{}, false
	}
	if objTy.IsRSController() || objTy.IsWSController() {
		return TypeVoid, true // close, enqueue, error
	}
	return e.intrinsicMethodType(streamMethodTypes, ex)
}

// promiseThenType is the type of `p.then/catch/finally(…)` on a promise.
func (e *Emitter) promiseThenType(ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	// .then/.catch/.finally on a promise return a task Promise<U> where U
	// is the value the returned promise settles to (TDD-00083 Stage 3 +
	// value-chaining, ADR-00248): the callback's return type for then/catch,
	// the source's own inner type for finally (pass-through).
	if mem.Property == "then" || mem.Property == "catch" || mem.Property == "finally" {
		if srcTy := e.inferExprType(mem.Object); srcTy.IsPromise() {
			retTy := TypeVoid
			switch mem.Property {
			case "then", "catch":
				// The fulfilled path settles to the fulfilment callback's
				// result (the source value when there is none) and the
				// rejected path to the rejection callback's (parameter: the
				// caught reason); Q holds their union boxed when they differ,
				// as emitPromiseThen builds it.
				fArg, rArg := -1, 1
				if mem.Property == "catch" {
					fArg, rArg = -1, 0
				} else if len(ex.Args) >= 1 && !e.isNonCallableThenArg(ex.Args[0]) {
					fArg = 0
				}
				if fArg < 0 {
					if srcTy.PromiseType != nil {
						retTy = *srcTy.PromiseType
					}
				} else {
					var hints []Type
					if srcTy.PromiseType != nil {
						hints = []Type{*srcTy.PromiseType}
					}
					if t, ok := e.callbackReturnType(ex.Args[0], hints...); ok {
						retTy = t
					}
				}
				if len(ex.Args) > rArg && !e.isNonCallableThenArg(ex.Args[rArg]) {
					if rTy, ok := e.callbackReturnType(ex.Args[rArg], TypeCaught); ok {
						if thenPathsDiffer(retTy, rTy) {
							retTy = TypeAny
						} else if mem.Property == "catch" {
							retTy = rTy
						}
					}
				}
				// A callback returning a thenable class instance resolves the
				// chain through its `then` (emitPromiseThen's thenableCallback).
				if sig, ok := e.thenableClass(retTy); ok {
					retTy = thenableValueType(sig)
				}
				// A callback that returns a promise resolves the chain with it
				// (flattening): the result is Promise<U>, never Promise<Promise<U>>
				// — this must agree with what emitPromiseThen builds.
				if retTy.IsPromise() {
					if retTy.PromiseType != nil {
						retTy = *retTy.PromiseType
					} else {
						retTy = TypeVoid
					}
				}
			case "finally":
				if srcTy.PromiseType != nil {
					retTy = *srcTy.PromiseType
				}
			}
			vt := PromiseOf(retTy)
			vt.PromiseTask = true
			return vt, true
		}
	}
	return Type{}, false
}

// anyArithIsNumber reports whether an arithmetic operator between an `any`
// and a number (`after * 1`) yields a number: a bigint on the `any` side
// cannot mix with the number, so the result is never a bigint, and it is
// held unboxed.
func anyArithIsNumber(op string, lt, rt Type) bool {
	switch op {
	case "-", "*", "/", "%", "**":
	default:
		return false
	}
	num := func(t Type) bool {
		return !t.IsDynamic() && !t.IsBigInt() && !t.Nullable() && (t.Float || t.IsInteger() && t.IR != "i1")
	}
	return isUnconstrainedDynamic(lt) && num(rt) || isUnconstrainedDynamic(rt) && num(lt)
}

// presentCopy is t as a method's fresh copy of a receiver of type t: never
// absent.
func presentCopy(t Type) Type {
	t = t.withoutNullable()
	t.setUndefined(false)
	t.setNullAndUndef(false)
	t.UncheckedIndex = false
	return t
}

// isNamespaceMember reports whether ex reads a member a namespace of its
// receiver's name declares (`N.h` of `function N` merged with `namespace
// N`), as emitMember resolves it.
func (e *Emitter) isNamespaceMember(ex *ast.MemberExpression) bool {
	id, ok := ex.Object.(*ast.Identifier)
	if !ok || e.isShadowedByLocal(id.Name) {
		return false
	}
	members, _ := e.namespaceMembers(id.Name)
	return members != nil && members[ex.Property]
}

// boundFuncValueType is the value `f.bind(…)` with nargs arguments produces
// for a function value of type objTy, as emitFunctionBind builds it: a box
// for a dynamically bound function, else objTy without the bound leading
// parameters. A body-filled-default closure (TDD-00206 Stage 2) keeps its
// presence-mask ABI after binding, with defaults/names shifted down by the
// bound-parameter count.
func (e *Emitter) boundFuncValueType(objTy Type, nargs int) (Type, bool) {
	if !objTy.IsFunc() {
		return Type{}, false
	}
	if e.bindsDynamically(objTy) {
		return TypeAny, true
	}
	boundCount := bindBoundCount(objTy, nargs)
	if boundCount > len(objTy.FuncParams) {
		return Type{}, false
	}
	ret := TypeVoid
	if objTy.FuncRetType != nil {
		ret = *objTy.FuncRetType
	}
	reduced := FuncType(objTy.FuncParams[boundCount:], ret)
	if objTy.FuncHasDefaultMask {
		reduced.FuncHasDefaultMask = true
		reduced.FuncParamNames = shiftExprOrNameSlice(objTy.FuncParamNames, boundCount)
		reduced.FuncParamDefaults = shiftDefaultSlice(objTy.FuncParamDefaults, boundCount)
		reduced.FuncBodyDefaults = shiftDefaultSlice(objTy.FuncBodyDefaults, boundCount)
	}
	return reduced, true
}

// builtinGlobalValueType is a name with no program binding read as a value
// (must match emitIdent): a function codegen resolves (a builtin like
// `Number` or `parseInt`, or a program function), a namespace object, or a
// builtin constructor, boxed.
func (e *Emitter) builtinGlobalValueType(id *ast.Identifier) (Type, bool) {
	// `process` as a value: its emitter object, boxed (emitIdent).
	if e.processIsValue(id) {
		return TypeAny, true
	}
	if b, ok := e.builtinRef(id); ok {
		if _, sig, err := e.ensureBuiltinFunc(b); err == nil {
			return funcTypeFromSig(sig), true
		}
	}
	if _, isGen := e.lookupGenerator(id.Name); isGen {
		// A nested generator is registered as a function too, with no
		// callable signature: its value is its constructor closure
		// (generatorValueType, as emitIdent emits it).
		return Type{}, false
	}
	if _, sig, ok := e.resolveFuncRef(id.Name); ok {
		// Carry the resolved signature's parameter and return types onto the
		// funcref Type, so `f.call()` / `f.apply()` / `f.bind()` (inferred
		// above) see the real return type instead of defaulting to void — a
		// named function used as `f.apply()` in value position otherwise
		// inferred void while emit produced the real typed call, mismatching
		// the enclosing function's signature (invalid IR).
		return funcTypeFromSig(sig), true
	}
	if key, ns := namespaceValueExport(id.Name); ns && (e.isGlobalName(id) || id.Name == "globalThis" && !e.isShadowedByLocal(id.Name)) && (id.Name != "crypto" || e.libExports[key] != "") {
		return TypeAny, true // a namespace object as a value (must match emitIdent)
	}
	if (id.Name == "Object" || id.Name == "Function") && e.isGlobalName(id) {
		return TypeAny, true
	}
	if _, isClass := e.classes[id.Name]; isClass || isErrorKindName(id.Name) || sema.IsBuiltinConstructor(id.Name) || id.Name == "Promise" {
		// Built-in constructor in value position — a boxed funcref
		// (must match emitIdent's own emission).
		return TypeAny, true
	}
	if e.isGlobalName(id) && e.isLibConstructorValue(id) {
		return TypeAny, true // a library constructor (`Buffer`), as emitIdent boxes it
	}
	if e.isGlobalName(id) && strings.HasPrefix(id.Name, "__kml_") {
		// A builtin module's native namespace (`__kml_native`): only its
		// members are read, each by its declaration.
		return TypeAny, true
	}
	return Type{}, false
}

// unboundBuiltinValueType is builtinGlobalValueType for a name with no
// codegen binding; a bound one (a library module's export) reads as its
// storage.
func (e *Emitter) unboundBuiltinValueType(id *ast.Identifier) (Type, bool) {
	if _, bound := e.lookup(id.Name); bound {
		return Type{}, false
	}
	return e.builtinGlobalValueType(id)
}

// identStorageType is the type an identifier reads as from its binding:
// the storage its declaration gives it, as emitIdent reads it, for a name
// the checker-side mapping leaves unanswered.
// enumName reports x naming an enum no binding shadows: its members are
// the enum's constants (emitMember), not reads off its boxed object.
func (e *Emitter) enumName(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	if !ok {
		return false
	}
	if _, isEnum := e.enums[id.Name]; !isEnum {
		return false
	}
	_, bound := e.lookup(id.Name)
	return !bound
}

func (e *Emitter) identStorageType(id *ast.Identifier) Type {
	if call, ok := e.tsSubtleAlias(id); ok {
		return e.inferExprType(call)
	}
	if _, isEnum := e.enumObjects[id.Name]; isEnum {
		if _, ok := e.lookup(id.Name); !ok {
			return TypeAny // the enum as a value (emitIdent)
		}
	}
	if m, _ := e.namespaces[id.Name]; m != nil {
		if _, ok := e.lookup(id.Name); !ok {
			return TypeAny // a namespace: an object, held boxed
		}
	}
	if t, isAlias := e.nsAliases[id.Name]; isAlias && e.namespaces[t] != nil {
		if _, ok := e.lookup(id.Name); !ok {
			return TypeAny // an import-equals alias of one
		}
	}
	if sym, ok := e.lookup(id.Name); ok {
		// A union local flow-narrowed in this region reads as its narrowed
		// type (TDD-00114), matching emitIdent's own unboxing.
		if sym.NarrowedTo != nil {
			return e.flowNarrowedTy(id, sym)
		}
		if info, ok := e.checkerHostSubclass(id, sym.Ty); ok {
			return info.Ty // emitIdent's instance read
		}
		if t, ok := e.checkerNarrowed(id, sym.Ty); ok && !sym.Ty.IsArray() && !sym.isNullableScalarLocal() {
			return t
		}
		if isNullableScalar(sym.Ty) && (sym.NarrowedNonNull && sym.isNullableScalarLocal() || e.checkerPresent(id, sym.Ty)) {
			return sym.Ty.withoutNullable() // the payload emitIdent reads
		}
		if e.checkerPresentRef(id, sym.Ty) || e.assignedString(id, sym.Ty) {
			return sym.Ty.withoutNullable()
		}
		return sym.Ty
	}
	if _, ok := globalNumberConsts[id.Name]; ok {
		return TypeF64
	}
	if id.Name == "isMainThread" {
		return TypeBool
	}
	if t, ok := e.builtinGlobalValueType(id); ok {
		return t
	}
	// A generator function in value position is a first-class constructor
	// closure (must match emitGeneratorCtorClosure's emission).
	if info, found := e.lookupGenerator(id.Name); found {
		return generatorValueType(info)
	}
	// A generic function or class read as a value: its erased
	// instantiation (emitIdent).
	if decl, ok := e.genericFuncs[id.Name]; ok {
		return e.erasedGenericValueType(decl)
	}
	if _, ok := e.genericClasses[id.Name]; ok {
		return TypeAny // its constructor reference
	}
	// A capturing nested function declaration referenced before its
	// declaration statement (TDD-00129 Stage 2, letrec hoisting): its type
	// is the closure the on-demand emission will produce — mirror
	// letrecEmitCapturingNested without emitting.
	if len(e.nestedFuncScopes) > 0 {
		for fd := range e.nestedFuncScopes[len(e.nestedFuncScopes)-1].capturing {
			if fd.Name == id.Name {
				nsig := e.buildFunctionSig(fd)
				return funcTypeFromSig(nsig)
			}
		}
	}
	// A top-level binding referenced from a function body while its
	// signature is being inferred (Pass 1) — before registerModuleGlobals
	// (Pass 1.7) has made it a module global — types from its own
	// declaration (annotation, else initializer), exactly as a `typeof x`
	// query does. Without this `const s = "x"; function f() { return s }`
	// inferred f's return as the number default and emitted `ret ptr` for
	// an i64 — invalid IR (ADR-01060).
	if e.topLevelNames[id.Name] {
		if ty, ok := e.resolveValueType(unmangleTopLevelName(id.Name)); ok && ty.IR != "" {
			return ty
		}
	}
	// A name a pass reads before its declaration is emitted (a `var` in
	// a `for` header, read by a whole-program pre-pass): the checker's
	// type for it, not the number default. A catch clause's binding is
	// the caught-value record emitCatchBody binds.
	if e.catchBinding(id) {
		return TypeCaught
	}
	// -compat=js: a binding an untyped object literal declares is a
	// dynamic object in an `any` (emitVarDecl), read before its
	// declaration is emitted.
	if sym, _ := e.resolveIdent(id); sym != nil && e.compatJS() && len(sym.Declarations) == 1 {
		if vd, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && vd.TypeAnnot == nil {
			if _, isLit := vd.Init.(*ast.ObjectLiteral); isLit {
				return TypeAny
			}
			// … and a top-level one declared without a value is the
			// `any` global registerModuleGlobals makes it.
			if vd.Init == nil && !vd.Ambient && e.topLevelNames[id.Name] {
				return TypeAny
			}
		}
	}
	if c := e.front(); c != nil {
		if t := c.StorageTypeOf(id); !c.Unanswered(t) {
			// Classes as codegen registered them; object types keep the
			// old inference (reprCtx.objects).
			r := reprCtx{class: e.classRepr}
			if ty, ok := r.of(t); ok && ty.IR != "" && ty.IR != "void" {
				// `const b = a[i]` holds the element read's absence, as
				// its declaration binds it.
				if arr, typed := (frontOracle{b: e.frontBinding, c: c, r: r, storesBusy: map[*ast.Identifier]bool{}, litBusy: map[*ast.ObjectLiteral]bool{}, forOfBusy: map[*ast.ForOfStatement]bool{}}).elementRead(id); arr.IsArray() && !typed {
					ty = indexReadType(ty)
				}
				if isNullableScalar(ty) && e.checkerPresent(id, ty) || e.checkerPresentRef(id, ty) {
					ty = ty.withoutNullable() // read where it is proven present
				}
				return ty
			}
		}
	}
	return TypeI64
}

// codegenCallRepr is the representation of a call whose value codegen lays
// out itself, which no checker type names: a generator function's instance
// (GenTy, per function), a generator's next/throw/return result, and an
// Array, Map or Set iterator's own methods (emitCollIterCall).
// generatorValueRepr is the value of a generator function named by id
// (inferExprType's generator branch).
func (e *Emitter) generatorValueRepr(id *ast.Identifier) (Type, bool) {
	if info, found := e.lookupGenerator(id.Name); found {
		return generatorValueType(info), true
	}
	return Type{}, false
}

func (e *Emitter) codegenCallRepr(ex *ast.CallExpression) (Type, bool) {
	if ex.Optional {
		return Type{}, false
	}
	switch callee := ex.Callee.(type) {
	case *ast.Identifier:
		if info, found := e.lookupGenerator(callee.Name); found {
			return info.GenTy, true
		}
		// A function whose emitted result is boxed (an overloaded lib
		// function returning `string | Buffer`, held as any) returns the
		// box, whichever overload the checker picked: the value is read the
		// way it is stored (`fs.readFileSync(p).toString()`, the import
		// resolved to the module's function).
		if fn, ok := e.funcs[callee.Name]; ok && isUnconstrainedDynamic(fn.RetType) && fn.RetType.DynPropTy == nil {
			// An overload the checker resolves to a primitive is unboxed to
			// it (emitExpr's call result).
			if pt, ok := e.checkerPrimitive(ex); ok {
				return pt, true
			}
			return fn.RetType, true
		}
	case *ast.MemberExpression:
		if callee.Optional {
			return Type{}, false
		}
		// A method of a value held boxed runs through the dynamic dispatch:
		// its result is what that dispatch returns, whatever the checker
		// knows of the value (a lib iterator helper's `toArray()`).
		if rt := e.inferExprType(callee.Object); isUnconstrainedDynamic(rt) && rt.DynPropTy == nil {
			return e.structuralCallType(ex), true
		}
		objTy := e.inferExprType(callee.Object)
		var f func(objTy Type) Type
		switch {
		case objTy.IsGenerator():
			f = generatorMethodTypes[callee.Property]
		case objTy.IsCollIter():
			f = collIterMethodTypes[callee.Property]
		}
		if f != nil {
			return f(objTy), true
		}
		// A program class's method whose emitted result is boxed (a method
		// returning a method value) returns the box, as a function's does.
		if objTy.IsClass() && !objTy.Nullable() {
			if sig, ok := e.classMethodSig(objTy.ClassName, callee.Property); ok && isUnconstrainedDynamic(sig.RetType) && sig.RetType.DynPropTy == nil {
				if pt, ok := e.checkerPrimitive(ex); ok {
					return pt, true
				}
				return sig.RetType, true
			}
		}
		// keys(), values() and entries() of an array, a Map or a Set are
		// its lazy iterator (arrayMethodTypes, collIter).
		if kind, ok := collIterMethod(callee.Property); ok && len(ex.Args) == 0 && (arrayIterable(objTy) || mapIterable(objTy)) {
			return CollIterType(objTy, kind), true
		}
		// A string's matchAll is its lazy match iterator.
		if callee.Property == "matchAll" && isStringTy(objTy) {
			return stringMethodTypes["matchAll"](e, ex, callee)
		}
	case *ast.IndexExpression:
		// `x[Symbol.iterator]()`: the method call it is.
		if r, ok := e.builtinIteratorCall(ex); ok {
			if t, ok := e.codegenCallRepr(r); ok {
				return t, true
			}
		}
	}
	return Type{}, false
}

// builtinNumericConst reports a read of a builtin's numeric constant
// (`Math.E`, `Number.EPSILON`) its name is not shadowed for.
func (e *Emitter) builtinNumericConst(ex *ast.MemberExpression) bool {
	id, ok := ex.Object.(*ast.Identifier)
	return ok && numericBuiltinConst(ex) && !e.isShadowedByLocal(id.Name)
}

// generatorMethodTypes are a generator's next/throw/return results: the
// {value, done} result, a promise of it for an async generator
// (TDD-00085/00086).
var generatorMethodTypes map[string]func(objTy Type) Type

// collIterMethodTypes are a builtin collection iterator's next and the lib
// helpers' results compiled here.
var collIterMethodTypes = map[string]func(objTy Type) Type{
	"next":    collIterResultType,
	"toArray": func(objTy Type) Type { return ArrayOf(collIterElemType(objTy)) },
	"reduce":  func(Type) Type { return TypeAny }, // the lib's Iterator.prototype.reduce
}

func init() {
	step := func(objTy Type) Type {
		if !objTy.GeneratorIsAsync {
			return genUserResultType(*objTy.GeneratorElemType)
		}
		pt := PromiseOf(asyncGenResultType(*objTy.GeneratorElemType))
		pt.PromiseTask = true
		return pt
	}
	generatorMethodTypes = map[string]func(objTy Type) Type{"next": step, "throw": step, "return": step}
}

// emitArrayValueToString is an array's elements joined by commas (String(a),
// `${a}`), v.Ref its header.
func (e *Emitter) emitArrayValueToString(v Value) (Value, error) {
	ptrReg, lenReg := e.arrayData(v), e.arrayLen(v)
	sepVal := Value{Ref: e.internString(","), Ty: TypePtr}
	if !v.Ty.IsTypedArray() && !v.Ty.IsFlatArray() && !v.Ty.ExecArray && v.Ref != "zeroinitializer" && e.holesUsed() {
		e.joinHoles = e.absentSafeArrayHeader(v.Ref)
	}
	joined, err := e.emitArrayJoinCore(ptrReg, lenReg, *v.Ty.ElemType, sepVal)
	if err != nil {
		return Value{}, err
	}
	// A `T[] | undefined` miss (a nested-array element absence, TDD-00221) is a
	// null header — render "undefined", not the empty join. `absentLiteral`
	// picks "undefined" (IsUndefined) over "null".
	if v.Ty.Nullable() {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s",
			r, e.emitArrayIsAbsent(v), e.internString(absentLiteral(v.Ty)), joined.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	return joined, nil
}

// structuralIndexType is an index read's type from the receiver's
// representation and the dispatch emitIndex runs.
func (e *Emitter) structuralIndexType(ex *ast.IndexExpression) Type {
	// A namespace's enum indexed — must match emitIndexUnguarded.
	if bare := e.stripNSTypeQualifier(ex.Object); bare != nil {
		plain := *ex
		plain.Object = bare
		return e.structuralIndexType(&plain)
	}
	if m, ok := e.builtinLiteralMember(ex); ok {
		return e.inferExprType(m) // emitIndex reads it as the member
	}
	// `a?.[k]` is `ElemType | undefined` behind a pointer receiver — mirror
	// emitOptionalIndex.
	if ex.Optional {
		plain := *ex
		plain.Optional = false
		inner := e.inferExprType(&plain)
		if !optionalIndexGuards(e.inferExprType(ex.Object)) {
			return inner
		}
		return optionalCallResultType(optCalleeValue, inner)
	}
	// process.env[key] (mirrors emitIndex).
	if r, ok := e.processEnvIndexRewrite(ex); ok {
		return e.inferExprType(r)
	}
	if e.classDynIndex(ex) {
		if pt, ok := e.checkerPrimitive(ex); ok {
			return pt
		}
		return TypeAny
	}
	objTy := e.inferExprType(ex.Object)
	// A bracket read off a bare any/unknown base is itself dynamic
	// (TDD-00155): the runtime tag dispatch yields another box — unless
	// the base is an index-signature view (DynPropTy).
	if isUnconstrainedDynamic(objTy) {
		if objTy.DynPropTy != nil {
			return dynPropReadType(*objTy.DynPropTy)
		}
		return TypeAny
	}
	// `/** @value */` flat array: an index read is the element (a view).
	if objTy.IsFlatArray() && objTy.ElemType != nil {
		return indexReadType(*objTy.ElemType)
	}
	// String-keyed Map bracket access yields the value type (TDD-00139).
	if objTy.IsMap() && objTy.MapKey != nil && isPlainStringType(*objTy.MapKey) {
		if ft, ok := dictFieldType(objTy, ex.Index); ok {
			return ft
		}
		if objTy.MapVal != nil {
			v := *objTy.MapVal
			if v.IsArray() {
				v.setNullable(true)
				v.setUndefined(true) // a missing key's array is undefined (mirrors the read)
			} else if dictMissReadsUndefined(v) {
				v = indexReadType(v) // a missing key is undefined (mirrors the read)
			}
			return v
		}
		return TypePtr
	}
	if isStringTy(objTy) {
		return TypePtr
	}
	// Fixed-object constant key (mirrors emitIndexUnguarded): the field's
	// type, a getter's result, or — a key naming no field — the dynamic
	// read's any.
	if objTy.IsObject() && !objTy.IsArray() && !objTy.IsTuple() && !objTy.IsMap() {
		if key, ok := constObjectKey(ex.Index); ok {
			if _, ft, found := objTy.FieldIndex(key); found {
				return ft
			}
			if objTy.IsClass() {
				if getter, _, ok := e.classAccessorSigs(objTy.ClassName, key); ok && getter != nil {
					return getter.RetType
				}
			}
			return TypeAny
		}
	}
	// Tuple constant-index (TDD-00066): `t[i]` has the type of field "i".
	if objTy.IsTuple() {
		if idx, ok := tupleConstIndex(ex.Index); ok && idx < int64(len(objTy.Fields)) {
			return objTy.Fields[idx].Ty
		} else if !ok {
			return TypeAny // emitTupleIndexDynamic's boxed element
		}
	}
	// TDD-00101: BigInt64Array/BigUint64Array elements surface as bigint.
	// An element read is `T | undefined` — out of range reads `undefined`
	// (mirrors emitIndexRead).
	if objTy.BigIntElem {
		return indexReadType(BigIntType())
	}
	if objTy.IsArray() && objTy.ElemType != nil {
		return indexReadType(*objTy.ElemType)
	}
	return TypeI64
}

// nullCoalesceType is `left ?? right`'s type from its operands'
// representations (emitNullCoalesce and its scalar, box, pointer and
// array forms).
func nullCoalesceType(lt, rt Type) Type {
	// Operands of different scalar kinds yield their union — mirror
	// emitNullCoalesce / emitNullCoalesceScalar.
	if !lt.IsNull() && !lt.IsDynamic() && (isNullableScalar(lt) || (lt.IR == "ptr" && !lt.IsArray())) {
		if uTy, ok := nullCoalesceUnion(lt.withoutNullable(), rt); ok {
			return uTy
		}
	}
	// A dynamic left operand mirrors emitNullCoalesceDynamic: a constrained
	// nullable box resolves against a scalar right operand, anything else
	// stays `any`.
	if lt.IsDynamic() {
		if resTy, ok := nullCoalesceBoxResult(lt, rt); ok {
			return resTy
		}
		return TypeAny
	}
	if nullCoalesceMixed(lt, rt) {
		return TypeAny // mirrors emitNullCoalesce's boxed union
	}
	if lt.IR == "ptr" && !lt.IsArray() && !lt.IsNull() {
		return nullCoalescePtrResult(lt, rt)
	}
	if lt.IsArray() {
		return nullCoalesceArrayResult(lt, rt) // mirrors emitNullCoalesceArray
	}
	if lt.IR == "ptr" {
		return rt
	}
	// A nullable-scalar left operand unwraps: `??`'s result is the
	// bare payload (emitNullCoalesceScalar returns exactly that).
	if isNullableScalar(lt) {
		if isNullableScalar(rt) {
			return rt
		}
		if resTy, ok := nullCoalesceNullishRight(lt.withoutNullable(), rt); ok {
			return resTy
		}
		return lt.withoutNullable()
	}
	return lt
}

// mixedKindsBoxed reports a literal of elements of two or more kinds, or of
// plain objects of different layouts, over typeOf: boxed elements, rather
// than every element read as the first's type (inferArrayTypeOf).
func mixedKindsBoxed(lit *ast.ArrayLiteral, typeOf func(ast.Expression) Type) bool {
	if len(lit.Elements) <= 1 {
		return false
	}
	kinds := map[string]bool{}
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			return false
		}
		t := typeOf(el)
		if nullishLiteralType(t) {
			continue // a null or undefined literal, not a value that may be one
		}
		kinds[elemKindKeyBroad(t)] = true
	}
	if len(kinds) >= 2 {
		return true
	}
	// Plain objects of different layouts (`[{ then(r) {…} }, { then(r,
	// j) {…} }]`) are a union, each its own shape: boxed elements, rather
	// than every element read through the first's layout (its methods
	// adapted to the first's arity).
	if kinds["object"] {
		var shape string
		for _, el := range lit.Elements {
			t := typeOf(el)
			if t.IsNull() || t.IsUndefined() || !t.IsObject() || t.IsClass() {
				continue
			}
			k := layoutKey(t.withoutNullable())
			if shape == "" {
				shape = k
			} else if k != shape {
				return true
			}
		}
	}
	return false
}

// hasSpread reports whether an array literal spreads anything.
func hasSpread(lit *ast.ArrayLiteral) bool {
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}
