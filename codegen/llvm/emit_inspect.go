// emit_inspect.go — Node-style structured rendering of a class instance / object
// literal (`ClassName { field: value, ... }`), used by console.log(obj) in both
// modes and by string coercion under -compat=strict (TDD-00075/ADR-00218). This
// is JS's `util.inspect`, deliberately distinct from the primitive `ToString`
// (`[object Object]`) that string coercion uses under -compat=js. Mirrors
// emit_call_json.go's field-walk, with inspect formatting instead of JSON.
package llvm

import (
	"fmt"
	"strings"
)

// maxInspectDepth bounds the inspector's recursion. Recursion is driven by the
// static type structure, so a self-referential type (`interface Node { next:
// Node }`) would otherwise recurse forever *at compile time*; this cap stops
// that and also mirrors Node's util.inspect, which shows `[Object]`/`[Array]`
// beyond its own depth limit.
const maxInspectDepth = 4

// effectiveInspectDepth returns the recursion cap in force: the per-call
// override console.dir({ depth }) installs, or the default maxInspectDepth.
func (e *Emitter) effectiveInspectDepth() int {
	if e.inspectDepthSet {
		return e.inspectDepthCap
	}
	return maxInspectDepth
}

// inspectClassName strips the resolver's per-file `__kml_mod<N>` mangling suffix
// (resolver.go) so an inspected instance shows `Point`, not `Point__kml_mod0`.
func inspectClassName(mangled string) string {
	if i := strings.Index(mangled, "__kml_mod"); i >= 0 {
		return mangled[:i]
	}
	return mangled
}

// isInspectableObject is the gate for structured rendering: a genuine class
// instance or plain object literal, excluding the many special types that also
// set IsObject (Symbol, URL, Headers, Response, Error, Tuple, …) and have their
// own rendering/dispatch. Conservative by construction — a missed exclusion is
// caught by the console/string tests for that type.
func isInspectableObject(ty Type) bool {
	return ty.IsObject && !ty.IsSymbol && !ty.IsError && !ty.IsTuple &&
		!ty.IsMap && !ty.IsSet && !ty.IsGroupMap &&
		!ty.IsURL && !ty.IsURLSearchParams && !ty.IsHeaders &&
		!ty.IsResponse && !ty.IsRequest && !ty.IsFetchRequest && !ty.IsXHR &&
		!ty.IsEventEmitter && !ty.IsEventSource && !ty.IsWebSocketClient &&
		!ty.IsWSConnection && !ty.IsTextEncoder && !ty.IsTextDecoder &&
		!ty.IsRegExp && !ty.IsTypedArray && !ty.IsArrayBuffer && !ty.IsDataView
}

// emitInspectObject renders an IsObject value as `ClassName { f: v, ... }` (an
// anonymous object literal omits the name), single-quoting nested strings and
// recursing into nested objects. V1 renders array/function fields as Node-style
// `[Array]`/`[Function]` placeholders (this compiler has no array-to-string yet
// — console.log(array) is itself unsupported).
func (e *Emitter) emitInspectObject(val Value, depth int) (Value, error) {
	// A class-typed field carries a name-only placeholder ClassType (no fields);
	// canonicalize to the full field-bearing type so nested instances render
	// their fields, not an empty `Point {}`.
	val.Ty = e.canonicalizeClassTy(val.Ty)
	name := ""
	if val.Ty.IsClass && val.Ty.ClassName != "" && !isSyntheticObjLitClass(val.Ty) {
		// A synthetic object-literal accessor class (TDD-00153) is an anonymous
		// object literal to the user — print it as `{ ... }`, never leaking its
		// internal `__kml_objlit_N` name.
		name = inspectClassName(val.Ty.ClassName) + " "
	}
	fields := val.Ty.VisibleFields()
	if len(fields) == 0 {
		return Value{Ref: e.internString(name + "{}"), Ty: TypePtr}, nil
	}
	acc := Value{Ref: e.internString(name + "{ "), Ty: TypePtr}
	for i, field := range fields {
		if i > 0 {
			var err error
			if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString(", "), Ty: TypePtr}); err != nil {
				return Value{}, err
			}
		}
		var err error
		if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString(field.Name + ": "), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		idx, _, _ := val.Ty.FieldIndex(field.Name)
		gepReg := e.freshReg()
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, val.Ty.StructIR(), val.Ref, idx))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(field.Ty), gepReg, field.Ty.Align()))
		fieldStr, err := e.emitInspectField(Value{Ref: loadReg, Ty: field.Ty}, depth+1)
		if err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, fieldStr); err != nil {
			return Value{}, err
		}
	}
	return e.emitStringConcat(acc, Value{Ref: e.internString(" }"), Ty: TypePtr})
}

// emitInspectArray renders an array as Node's `[ e1, e2, ... ]` (empty: `[]`),
// looping at runtime and formatting each element with emitInspectField (so
// strings quote, nested objects/arrays recurse). This is also what makes
// console.log(array) work at all — previously a hard rejection.
func (e *Emitter) emitInspectArray(val Value, depth int) (Value, error) {
	if val.Ty.ElemType == nil {
		return Value{Ref: e.internString("[Array]"), Ty: TypePtr}, nil
	}
	elemTy := *val.Ty.ElemType
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))

	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("["), accAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("insparr.cond")
	bodyL := e.freshLabel("insparr.body")
	firstL := e.freshLabel("insparr.first")
	restL := e.freshLabel("insparr.rest")
	incL := e.freshLabel("insparr.inc")
	doneL := e.freshLabel("insparr.done")
	emptyL := e.freshLabel("insparr.empty")
	nonEmptyL := e.freshLabel("insparr.nonempty")
	closeL := e.freshLabel("insparr.close")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	elem := e.loadArrayElem(inGep, elemTy)
	// A BigInt64Array/BigUint64Array element is stored raw (i64/u64) but is
	// semantically a bigint — wrap it so it inspects with the `n` suffix.
	if val.Ty.BigIntElem {
		e.ensureBigInt()
		fromFn := "__kml_bigint_from_i64"
		if !elemTy.Signed {
			fromFn = "__kml_bigint_from_u64"
		}
		big := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @%s(i64 %s)", big, fromFn, elem.Ref))
		elem = Value{Ref: big, Ty: BigIntType()}
	}
	elemStr, err := e.emitInspectField(elem, depth+1)
	if err != nil {
		return Value{}, err
	}
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	// First element: "[ " + elem
	e.emitLabel(firstL)
	accF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accF, accAlloca))
	openAcc, err := e.emitStringConcat(Value{Ref: accF, Ty: TypePtr}, Value{Ref: e.internString(" "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	firstAcc, err := e.emitStringConcat(openAcc, elemStr)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", firstAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	// Subsequent: ", " + elem
	e.emitLabel(restL)
	accR := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accR, accAlloca))
	sepAcc, err := e.emitStringConcat(Value{Ref: accR, Ty: TypePtr}, Value{Ref: e.internString(", "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	restAcc, err := e.emitStringConcat(sepAcc, elemStr)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", restAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	// Close: "[]" when empty, "<acc> ]" otherwise.
	e.emitLabel(doneL)
	wasEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", wasEmpty, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", wasEmpty, emptyL, nonEmptyL))

	resAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resAlloca))

	e.emitLabel(emptyL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("[]"), resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", closeL))

	e.emitLabel(nonEmptyL)
	accE := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accE, accAlloca))
	closed, err := e.emitStringConcat(Value{Ref: accE, Ty: TypePtr}, Value{Ref: e.internString(" ]"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", closed.Ref, resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", closeL))

	e.emitLabel(closeL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", res, resAlloca))
	final := Value{Ref: res, Ty: TypePtr}
	// A TypedArray prints with Node's `TypeName(len) ` prefix (a Node Buffer has
	// its own `<Buffer ..>` rendering elsewhere, so it is excluded here).
	if val.Ty.IsTypedArray && !val.Ty.IsBuffer {
		if name := typedArrayConstructorName(val.Ty); name != "" {
			lenStr, err := e.emitValueToString(Value{Ref: lenReg, Ty: TypeI64})
			if err != nil {
				return Value{}, err
			}
			p1, err := e.emitStringConcat(Value{Ref: e.internString(name + "("), Ty: TypePtr}, lenStr)
			if err != nil {
				return Value{}, err
			}
			p2, err := e.emitStringConcat(p1, Value{Ref: e.internString(") "), Ty: TypePtr})
			if err != nil {
				return Value{}, err
			}
			final, err = e.emitStringConcat(p2, final)
			if err != nil {
				return Value{}, err
			}
		}
	}
	return final, nil
}

// typedArrayConstructorName returns the JS constructor name for a TypedArray
// type (for util.inspect's `Name(len)` prefix), or "" if unrecognized.
func typedArrayConstructorName(ty Type) string {
	if ty.ElemType == nil {
		return ""
	}
	el := *ty.ElemType
	switch {
	case ty.BigIntElem && el.Signed:
		return "BigInt64Array"
	case ty.BigIntElem:
		return "BigUint64Array"
	case ty.Clamped:
		return "Uint8ClampedArray"
	case el.Float && el.IR == "float":
		return "Float32Array"
	case el.Float:
		return "Float64Array"
	}
	switch el.IR {
	case "i8":
		if el.Signed {
			return "Int8Array"
		}
		return "Uint8Array"
	case "i16":
		if el.Signed {
			return "Int16Array"
		}
		return "Uint16Array"
	case "i32":
		if el.Signed {
			return "Int32Array"
		}
		return "Uint32Array"
	}
	return ""
}

// emitInspectBuffer renders a Node Buffer as `<Buffer 68 69>` — each byte as a
// two-digit lowercase hex pair, space-separated, wrapped in `<Buffer …>` (an
// empty buffer is `<Buffer >`). This is Node's own console/util.inspect form for
// a Buffer, distinct from the `[ 104, 105 ]` a plain Uint8Array shows.
func (e *Emitter) emitInspectBuffer(val Value) (Value, error) {
	e.ensureSprintf()
	e.ensureMalloc()
	e.ensureStrHeaderRuntime() // __kml_str_from_cstr — sprintf yields a bare C string
	elemTy := TypeU8
	if val.Ty.ElemType != nil {
		elemTy = *val.Ty.ElemType
	}
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))

	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("<Buffer"), accAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("inspbuf.cond")
	bodyL := e.freshLabel("inspbuf.body")
	doneL := e.freshLabel("inspbuf.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	elem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
	byteI32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext %s %s to i32", byteI32, elemTy.IR, elem))
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", buf))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s)", buf, e.internString(" %02x"), byteI32))
	hexStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", hexStr, buf))
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, accAlloca))
	next, err := e.emitStringConcat(Value{Ref: cur, Ty: TypePtr}, Value{Ref: hexStr, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", next.Ref, accAlloca))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	accF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accF, accAlloca))
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, lenReg))
	closeStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", closeStr, isEmpty, e.internString(" >"), e.internString(">")))
	return e.emitStringConcat(Value{Ref: accF, Ty: TypePtr}, Value{Ref: closeStr, Ty: TypePtr})
}

// emitInspectMap renders a Map as `Map(N) { 'a' => 1, ... }` (empty: `Map(0)
// {}`), walking the same parallel keys()/vals() arrays the rest of the Map
// machinery uses. Keys and values are formatted as inspected fields (strings
// single-quoted, nested objects recursed), joined by " => ".
func (e *Emitter) emitInspectMap(val Value, depth int) (Value, error) {
	keyTy := TypePtr
	if val.Ty.MapKey != nil {
		keyTy = *val.Ty.MapKey
	}
	valTy := TypeI64
	if val.Ty.MapVal != nil {
		valTy = *val.Ty.MapVal
	}
	strKey := isStringTy(keyTy)
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(val.Ref, strKey)
	render := func(idxVal string) (Value, error) {
		kGep, kElem := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", kGep, keyTy.IR, keysPtr, idxVal))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", kElem, keyTy.IR, kGep, keyTy.Align()))
		vGep, vElem := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", vGep, valTy.IR, valsPtr, idxVal))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", vElem, valTy.IR, vGep, valTy.Align()))
		kStr, err := e.emitInspectField(Value{Ref: kElem, Ty: keyTy}, depth+1)
		if err != nil {
			return Value{}, err
		}
		arrowStr, err := e.emitStringConcat(kStr, Value{Ref: e.internString(" => "), Ty: TypePtr})
		if err != nil {
			return Value{}, err
		}
		vStr, err := e.emitInspectField(Value{Ref: vElem, Ty: valTy}, depth+1)
		if err != nil {
			return Value{}, err
		}
		return e.emitStringConcat(arrowStr, vStr)
	}
	return e.emitInspectCollection("Map", keysLen, render)
}

// emitInspectSet renders a Set as `Set(N) { 1, 2, ... }` (empty: `Set(0) {}`).
func (e *Emitter) emitInspectSet(val Value, depth int) (Value, error) {
	elemTy := TypePtr
	if val.Ty.MapKey != nil {
		elemTy = *val.Ty.MapKey
	}
	strElem := isStringTy(elemTy)
	keysRes := e.freshReg()
	if strElem {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", keysRes, val.Ref))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_num_keys(ptr %s)", keysRes, val.Ref))
	}
	keysPtr := e.freshReg()
	keysLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", keysPtr, keysRes))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", keysLen, keysRes))
	render := func(idxVal string) (Value, error) {
		gep, elem := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, keysPtr, idxVal))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elem, elemTy.IR, gep, elemTy.Align()))
		return e.emitInspectField(Value{Ref: elem, Ty: elemTy}, depth+1)
	}
	return e.emitInspectCollection("Set", keysLen, render)
}

// emitInspectCollection is the shared body-builder for Map/Set inspection:
// prints `<Name>(<len>) {}` when empty, or `<Name>(<len>) { e0, e1, … }`
// otherwise, where each element string is produced by render(idx). The
// element render closure formats one entry (`k => v` for a Map, the value for
// a Set).
func (e *Emitter) emitInspectCollection(name, lenReg string, render func(idxVal string) (Value, error)) (Value, error) {
	lenStr, err := e.emitValueToString(Value{Ref: lenReg, Ty: TypeI64})
	if err != nil {
		return Value{}, err
	}
	prefix, err := e.emitStringConcat(Value{Ref: e.internString(name + "("), Ty: TypePtr}, lenStr)
	if err != nil {
		return Value{}, err
	}
	prefix, err = e.emitStringConcat(prefix, Value{Ref: e.internString(") "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}

	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("{ "), accAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("inspcoll.cond")
	bodyL := e.freshLabel("inspcoll.body")
	firstL := e.freshLabel("inspcoll.first")
	restL := e.freshLabel("inspcoll.rest")
	incL := e.freshLabel("inspcoll.inc")
	doneL := e.freshLabel("inspcoll.done")
	emptyL := e.freshLabel("inspcoll.empty")
	nonEmptyL := e.freshLabel("inspcoll.nonempty")
	closeL := e.freshLabel("inspcoll.close")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	elemStr, err := render(idxVal)
	if err != nil {
		return Value{}, err
	}
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	e.emitLabel(firstL)
	accF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accF, accAlloca))
	firstAcc, err := e.emitStringConcat(Value{Ref: accF, Ty: TypePtr}, elemStr)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", firstAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(restL)
	accR := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accR, accAlloca))
	sepAcc, err := e.emitStringConcat(Value{Ref: accR, Ty: TypePtr}, Value{Ref: e.internString(", "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	restAcc, err := e.emitStringConcat(sepAcc, elemStr)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", restAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	wasEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", wasEmpty, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", wasEmpty, emptyL, nonEmptyL))

	resAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resAlloca))

	e.emitLabel(emptyL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("{}"), resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", closeL))

	e.emitLabel(nonEmptyL)
	accE := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accE, accAlloca))
	closed, err := e.emitStringConcat(Value{Ref: accE, Ty: TypePtr}, Value{Ref: e.internString(" }"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", closed.Ref, resAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", closeL))

	e.emitLabel(closeL)
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", body, resAlloca))
	return e.emitStringConcat(prefix, Value{Ref: body, Ty: TypePtr})
}

// emitInspectField formats a value as it appears *inside* an inspected object —
// strings single-quoted, bigints with the `n` suffix, nested objects recursed —
// distinct from the top-level bare formatting emitValueToString produces.
func (e *Emitter) emitInspectField(v Value, depth int) (Value, error) {
	switch {
	case v.Ty.IsBigInt:
		return e.emitBigIntToString(v, true) // `10n`, like Node inspect
	case isInspectableObject(v.Ty):
		if depth > e.effectiveInspectDepth() {
			return Value{Ref: e.internString("[Object]"), Ty: TypePtr}, nil
		}
		return e.emitInspectObject(v, depth)
	case v.Ty.IsMap:
		if depth > e.effectiveInspectDepth() {
			return Value{Ref: e.internString("[Map]"), Ty: TypePtr}, nil
		}
		return e.emitInspectMap(v, depth)
	case v.Ty.IsSet:
		if depth > e.effectiveInspectDepth() {
			return Value{Ref: e.internString("[Set]"), Ty: TypePtr}, nil
		}
		return e.emitInspectSet(v, depth)
	case v.Ty.IsBuffer:
		return e.emitInspectBuffer(v)
	case v.Ty.IsObject:
		// A special object type (URL/Headers/…) as a field — a V1 placeholder
		// rather than exposing its internal struct.
		return Value{Ref: e.internString("[Object]"), Ty: TypePtr}, nil
	case v.Ty.IsArray:
		if depth > e.effectiveInspectDepth() {
			return Value{Ref: e.internString("[Array]"), Ty: TypePtr}, nil
		}
		return e.emitInspectArray(v, depth)
	case v.Ty.IsFunc:
		return Value{Ref: e.internString("[Function]"), Ty: TypePtr}, nil
	case v.Ty.IsNull:
		return e.emitValueToString(v) // null / undefined, unquoted
	case isStringTy(v.Ty):
		q := Value{Ref: e.internString("'"), Ty: TypePtr}
		s1, err := e.emitStringConcat(q, v)
		if err != nil {
			return Value{}, err
		}
		return e.emitStringConcat(s1, q)
	default:
		return e.emitValueToString(v) // number / bool / symbol / date
	}
}
