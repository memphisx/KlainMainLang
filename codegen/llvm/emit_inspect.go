// emit_inspect.go — Node-style structured rendering of a class instance / object
// literal (`ClassName { field: value, ... }`), used by console.log(obj) in both
// modes and by string coercion under -compat=strict (TDD-00075/ADR-00218). This
// is JS's `util.inspect`, deliberately distinct from the primitive `ToString`
// (`[object Object]`) that string coercion uses under -compat=js. Mirrors
// emit_call_json.go's field-walk, with inspect formatting instead of JSON.
package llvm

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// maxInspectDepth bounds the inspector's recursion. Recursion is driven by the
// static type structure, so a self-referential type (`interface Node { next:
// Node }`) would otherwise recurse forever *at compile time*; this cap stops
// that and also mirrors Node's util.inspect, which shows `[Object]`/`[Array]`
// beyond its own depth limit — `depth: 2` by default (ADR-01067).
const maxInspectDepth = 2

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
	mangled = ast.Unmangle(mangled)
	if i := strings.Index(mangled, "__kml_cx"); i >= 0 {
		mangled = mangled[:i] // a hoisted class expression (emit_classexpr.go)
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
		!ty.IsMap && !ty.IsSet &&
		!ty.IsResponse && !ty.IsRequest && !ty.IsFetchRequest && !ty.IsXHR &&
		!ty.IsRegExp && !ty.IsTypedArray && !ty.IsArrayBuffer
}

// emitInspectObject renders an IsObject value as `ClassName { f: v, ... }` (an
// anonymous object literal omits the name), single-quoting nested strings and
// recursing into nested objects. V1 renders array/function fields as Node-style
// `[Array]`/`[Function]` placeholders (this compiler has no array-to-string yet
// — console.log(array) is itself unsupported).
func (e *Emitter) emitInspectObject(val Value, depth int) (Value, error) {
	// An Error subclass's instance renders as an error does: Node's
	// `Name: message` header, then its own properties.
	if info, ok := e.classes[val.Ty.ClassName]; ok && val.Ty.IsClass && info.IsErrorSubclass {
		box, err := e.emitBoxValue(val)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynamicInspectAt(box, depth)
	}
	// An object of another layout behind a structural type prints as the
	// object it is (TDD-00233).
	if isRecordView(val.Ty) && !val.Ty.Nullable {
		return e.emitRecordSplit(val, TypePtr,
			func(v Value) (Value, error) { return e.emitInspectObjectOwn(v, depth) },
			func(box Value) (Value, error) { return e.emitDynamicInspectAt(box, depth) })
	}
	if extraCandidate(val.Ty) {
		return e.emitExtraSplit(val, TypePtr,
			func(v Value) (Value, error) { return e.emitInspectObjectOwn(v, depth) },
			func(obj string) string {
				return fmt.Sprintf("call ptr @__kml_obj_inspect_dyn(ptr %s, i64 %d)", obj, depth)
			})
	}
	return e.emitInspectObjectOwn(val, depth)
}

// emitInspectObjectOwn is emitInspectObject reading val at its static
// type's layout.
func (e *Emitter) emitInspectObjectOwn(val Value, depth int) (Value, error) {
	// A class-typed field carries a name-only placeholder ClassType (no fields);
	// canonicalize to the full field-bearing type so nested instances render
	// their fields, not an empty `Point {}`.
	val.Ty = e.canonicalizeClassTy(val.Ty)
	if s, ok, err := e.emitInspectCustom(val, depth); ok || err != nil {
		return s, err
	}
	name := ""
	if val.Ty.IsClass && val.Ty.ClassName != "" && !isSyntheticObjLitClass(val.Ty) {
		// A synthetic object-literal accessor class (TDD-00153) is an anonymous
		// object literal to the user — print it as `{ ... }`, never leaking its
		// internal `__kml_objlit_N` name.
		name = e.classDisplayName(val.Ty.ClassName) + " "
	}
	if val.Ty.IsNullProtoObject {
		name = "[Object: null prototype] "
	}
	// `Name [Tag] {` when a Symbol.toStringTag getter answers a tag other
	// than the class name (util.inspect).
	opener := func(rest string) string {
		if name == "" || !val.Ty.IsClass || !e.classHasMethodInherited(val.Ty.ClassName, accessorMethodName("get", "@@toStringTag")) {
			return e.internString(name + rest)
		}
		e.ensureInspectReduce()
		tag, r := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_obj_tostring_tag(ptr %s)", tag, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_tagged(ptr %s, ptr %s, ptr %s)",
			r, e.internString(strings.TrimSuffix(name, " ")), tag, e.internString(rest)))
		return r
	}
	fields := esOrderedFields(val.Ty.VisibleFields()) // ES key order (Node)
	if len(fields) == 0 {
		return Value{Ref: opener("{}"), Ty: TypePtr}, nil
	}
	// An optional (`x?: T`, i.e. `T | undefined`) field that is absent has no
	// key at all in Node's rendering (`{ name: 'a' }`, not `{ name: 'a', age:
	// undefined }`), so — as JSON.stringify already does — the separator and
	// the closing brace are decided at runtime from an "emitted anything yet"
	// flag whenever such a field exists (ADR-01063).
	// Entries are rendered one by one into a runtime list and laid out by
	// __kml_inspect_end (single line when they fit 80 columns and the value
	// nests fewer than three levels below, else one per line — ADR-01067). An
	// optional (`x?: T`) field that is absent has no entry (ADR-01063).
	list := e.inspectBegin(depth, "1")
	appendField := func(field Field, fieldStr Value) error {
		entry, err := e.emitStringConcat(Value{Ref: e.internString(field.Name + ": "), Ty: TypePtr}, fieldStr)
		if err != nil {
			return err
		}
		e.inspectPush(list, entry)
		return nil
	}
	for _, field := range fields {
		present, fieldForInspect := e.emitFieldPresent(val.Ref, val.Ty, field)
		if present == "true" {
			fieldStr, err := e.emitInspectField(fieldForInspect, depth+1)
			if err != nil {
				return Value{}, err
			}
			if err := appendField(field, fieldStr); err != nil {
				return Value{}, err
			}
			continue
		}
		// Skippable `T | undefined`: render only when present.
		doL := e.freshLabel("inspect.opt.emit")
		contL := e.freshLabel("inspect.opt.cont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, doL, contL))
		e.emitLabel(doL)
		fieldStr, err := e.emitInspectField(fieldForInspect, depth+1)
		if err != nil {
			return Value{}, err
		}
		if err := appendField(field, fieldStr); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
	}
	return e.inspectEnd(list, opener("{"), e.internString("}"), depth, false, false), nil
}

// emitInspectTuple renders a heap tuple as Node does an array: `[ 1, 'a' ]`
// (ADR-01063). A tuple is a fixed-shape object whose fields are "0", "1", …
func (e *Emitter) emitInspectTuple(val Value, depth int) (Value, error) {
	fields := val.Ty.Fields
	if len(fields) == 0 {
		return Value{Ref: e.internString("[]"), Ty: TypePtr}, nil
	}
	list := e.inspectBegin(depth, "1")
	numeric := true
	for i, field := range fields {
		if !(field.Ty.Float || field.Ty.IsInteger()) || field.Ty.IR == "i1" || field.Ty.IsDynamic {
			numeric = false
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, val.Ty.StructIR(), val.Ref, i))
		var elem Value
		if field.Ty.IsArray {
			elem = e.loadArrayFieldValue(gepReg, field.Ty)
		} else {
			loadReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(field.Ty), gepReg, field.Ty.Align()))
			elem = Value{Ref: loadReg, Ty: field.Ty}
		}
		elemStr, err := e.emitInspectField(elem, depth+1)
		if err != nil {
			return Value{}, err
		}
		e.inspectPush(list, elemStr)
	}
	return e.inspectEnd(list, e.internString("["), e.internString("]"), depth, true, numeric), nil
}

// emitInspectNullablePtr renders a pointer-shaped value that may be absent: a
// null pointer prints its keyword (`undefined`/`null` by the static type),
// anything else runs `render`. Inspecting through a null pointer is a
// segfault, so this is a real branch, not a select (ADR-01063).
func (e *Emitter) emitInspectNullablePtr(v Value, render func(Value) (Value, error)) (Value, error) {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(absentLiteral(v.Ty)), slot))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	nullL := e.freshLabel("inspect.ptr.null")
	valL := e.freshLabel("inspect.ptr.val")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, valL))
	e.emitLabel(valL)
	base := v
	base.Ty.Nullable, base.Ty.IsUndefined, base.Ty.IsNull = false, false, false
	s, err := render(base)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", nullL))
	e.emitLabel(nullL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
	return Value{Ref: out, Ty: TypePtr}, nil
}

// emitInspectArray renders an array as Node's `[ e1, e2, ... ]` (empty: `[]`),
// looping at runtime and formatting each element with emitInspectField (so
// strings quote, nested objects/arrays recurse). This is also what makes
// console.log(array) work at all — previously a hard rejection.
// inspectMaxArrayLength is util.inspect's default maxArrayLength: elements past
// it collapse into one `... n more items` entry.
const inspectMaxArrayLength = 100

func (e *Emitter) emitInspectArray(val Value, depth int) (Value, error) {
	if val.Ty.ElemType == nil {
		return Value{Ref: e.internString("[Array]"), Ty: TypePtr}, nil
	}
	elemTy := *val.Ty.ElemType
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))

	nonempty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", nonempty, e.icmpNe(lenReg, "0")))
	list := e.inspectBegin(depth, nonempty)
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	// Node's maxArrayLength: at most 100 elements are rendered, then one
	// `... n more items` entry (__kml_inspect_push_more, after the loop).
	over := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ugt i64 %s, %d", over, lenReg, inspectMaxArrayLength))
	shown := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", shown, over, inspectMaxArrayLength, lenReg))

	condL := e.freshLabel("insparr.cond")
	bodyL := e.freshLabel("insparr.body")
	doneL := e.freshLabel("insparr.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, shown))
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
	e.inspectPush(list, elemStr)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	moreL := e.freshLabel("insparr.more")
	endL := e.freshLabel("insparr.end")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", over, moreL, endL))
	e.emitLabel(moreL)
	rest := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", rest, lenReg, inspectMaxArrayLength))
	e.emitInstr(fmt.Sprintf("call void @__kml_inspect_push_more(ptr %s, i64 %s)", list, rest))
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	e.emitLabel(endL)
	// An exec() result lists its own index, input and groups after its
	// elements: `[ 'a', index: 0, input: 'ab', groups: undefined ]`.
	if val.Ty.ExecArray && val.ArrayHeader != "" {
		plainL, extrasL := "", ""
		if val.Ty.ExecMaybePlain {
			// A global match's plain array (index -1) lists no extras.
			idx := e.execMemberRead(val.ArrayHeader, "index", false)
			has := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", has, idx.Ref))
			extrasL, plainL = e.freshLabel("inspexec.extras"), e.freshLabel("inspexec.plain")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, extrasL, plainL))
			e.emitLabel(extrasL)
		}
		for _, prop := range []string{"index", "input", "groups"} {
			ty, off, _ := execArrayMemberType(prop)
			gep, r := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, val.ArrayHeader, off))
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ty.IR, gep))
			var fv Value
			var err error
			if prop == "groups" {
				// undefined when the pattern names no group.
				fv, err = e.emitInspectNullablePtr(Value{Ref: r, Ty: ty}, func(g Value) (Value, error) { return e.emitInspectField(g, depth+1) })
			} else {
				fv, err = e.emitInspectField(Value{Ref: r, Ty: ty}, depth+1)
			}
			if err != nil {
				return Value{}, err
			}
			entry, err := e.emitStringConcat(Value{Ref: e.internString(prop + ": "), Ty: TypePtr}, fv)
			if err != nil {
				return Value{}, err
			}
			e.inspectPush(list, entry)
		}
		if plainL != "" {
			e.emitTerminator(fmt.Sprintf("br label %%%s", plainL))
			e.emitLabel(plainL)
		}
	}
	// Node right-aligns the columns of a number/bigint array, left-aligns others.
	numeric := (elemTy.Float || elemTy.IsInteger()) && elemTy.IR != "i1" && !elemTy.IsDynamic || val.Ty.BigIntElem || elemTy.IsBigInt
	res := e.inspectEnd(list, e.internString("["), e.internString("]"), depth, true, numeric).Ref
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
// two-digit lowercase hex pair, space-separated, at most 50 of them (Node's
// INSPECT_MAX_BYTES) then ` ... N more bytes`. This is Node's own
// console/util.inspect form for a Buffer, distinct from the `[ 104, 105 ]` a
// plain Uint8Array shows; a Buffer boxed into `any` renders through the same
// C function (dynjson.c).
func (e *Emitter) emitInspectBuffer(val Value) (Value, error) {
	e.ensureDynJSONC()
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buffer_inspect(ptr %s, i64 %s)", res, ptrReg, lenReg))
	return Value{Ref: res, Ty: TypePtr}, nil
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
	suffix, _ := mapRuntime(keyTy)
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(val.Ref, suffix, keyTy)
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
	return e.emitInspectCollection("Map", keysLen, depth, render)
}

// emitInspectSet renders a Set as `Set(N) { 1, 2, ... }` (empty: `Set(0) {}`).
func (e *Emitter) emitInspectSet(val Value, depth int) (Value, error) {
	elemTy := TypePtr
	if val.Ty.MapKey != nil {
		elemTy = *val.Ty.MapKey
	}
	suffix, _ := mapRuntime(elemTy)
	keysRes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_%s_keys(ptr %s)", keysRes, suffix, val.Ref))
	keysRes = e.materializeKeysAggregate(keysRes, elemTy)
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
	return e.emitInspectCollection("Set", keysLen, depth, render)
}

// emitInspectCollection is the shared body-builder for Map/Set inspection:
// prints `<Name>(<len>) {}` when empty, or `<Name>(<len>) { e0, e1, … }`
// otherwise, where each element string is produced by render(idx). The
// element render closure formats one entry (`k => v` for a Map, the value for
// a Set).
func (e *Emitter) emitInspectCollection(name, lenReg string, depth int, render func(idxVal string) (Value, error)) (Value, error) {
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

	open, err := e.emitStringConcat(prefix, Value{Ref: e.internString("{"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	return e.emitInspectListBody(open, lenReg, depth, render)
}

// emitInspectListBody renders lenReg elements after open, as `open e0, e1 }`
// (or `open}` when there are none).
func (e *Emitter) emitInspectListBody(open Value, lenReg string, depth int, render func(idxVal string) (Value, error)) (Value, error) {
	nonempty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", nonempty, e.icmpNe(lenReg, "0")))
	list := e.inspectBegin(depth, nonempty)
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("inspcoll.cond")
	bodyL := e.freshLabel("inspcoll.body")
	doneL := e.freshLabel("inspcoll.done")

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
	e.inspectPush(list, elemStr)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return e.inspectEnd(list, open.Ref, e.internString("}"), depth, false, false), nil
}

// emitInspectField formats a value as it appears *inside* an inspected object —
// strings single-quoted, bigints with the `n` suffix, nested objects recursed —
// distinct from the top-level bare formatting emitValueToString produces.
func (e *Emitter) emitInspectField(v Value, depth int) (Value, error) {
	if v.Ty.NullAndUndef {
		return e.renderThreeState(v, func(p Value) (Value, error) { return e.emitInspectField(p, depth) })
	}
	switch {
	case isSelfDescribingBox(v.Ty) || inspectsByTag(v.Ty):
		// A boxed element (`any[]`, `(number | string)[]`) renders by its run-time
		// tag: a string is quoted like any other nested string, the rest inspect.
		s, err := e.emitDynamicInspectAt(v, depth)
		if err != nil {
			return Value{}, err
		}
		q := Value{Ref: e.internString("'"), Ty: TypePtr}
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s.Ref, slot))
		tag, _ := e.emitUnboxTagPayload(v)
		strL, doneL := e.emitTagCheck(tag, kmlTagString, "inspect.box.str")
		e.emitLabel(strL)
		s1, err := e.emitStringConcat(q, s)
		if err != nil {
			return Value{}, err
		}
		quoted, err := e.emitStringConcat(s1, q)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", quoted.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
		return Value{Ref: out, Ty: TypePtr}, nil
	case v.Ty.IsBigInt:
		return e.emitBigIntToString(v, true) // `10n`, like Node inspect
	case v.Ty.IsRegExp && v.Ty.IsObject && !v.Ty.Nullable && !v.Ty.IsDynamic:
		// util.inspect shows a RegExp as its toString, `/a+/g`.
		return e.emitValueToString(v)
	case isInspectableObject(v.Ty):
		// Past the depth cap Node prints `[Object]` — except an empty object,
		// whose `{}` early return precedes its depth check (ADR-01067).
		if depth > e.effectiveInspectDepth() && len(e.canonicalizeClassTy(v.Ty).VisibleFields()) > 0 {
			if !v.Ty.IsClass || v.Ty.ClassName == "" || isSyntheticObjLitClass(v.Ty) || v.Ty.IsNullProtoObject {
				return Value{Ref: e.internString("[Object]"), Ty: TypePtr}, nil
			}
			// A class instance is `[Name]`, `[Name [Tag]]` with a toStringTag.
			name := e.classDisplayName(v.Ty.ClassName)
			if !e.classHasMethodInherited(v.Ty.ClassName, accessorMethodName("get", "@@toStringTag")) {
				return Value{Ref: e.internString("[" + name + "]"), Ty: TypePtr}, nil
			}
			render := func(b Value) (Value, error) {
				e.ensureInspectReduce()
				tag, r := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_obj_tostring_tag(ptr %s)", tag, b.Ref))
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_tagged(ptr %s, ptr %s, ptr null)", r, e.internString(name), tag))
				return Value{Ref: r, Ty: TypePtr}, nil
			}
			if v.Ty.Nullable || v.Ty.IsNull {
				return e.emitInspectNullablePtr(v, render)
			}
			return render(v)
		}
		if v.Ty.Nullable || v.Ty.IsNull {
			return e.emitInspectNullablePtr(v, func(b Value) (Value, error) { return e.emitInspectObject(b, depth) })
		}
		return e.emitInspectObject(v, depth)
	case v.Ty.IsTuple && !v.Ty.TupleByVal:
		if depth > e.effectiveInspectDepth() && len(v.Ty.Fields) > 0 {
			return Value{Ref: e.internString("[Array]"), Ty: TypePtr}, nil
		}
		if v.Ty.Nullable || v.Ty.IsNull {
			return e.emitInspectNullablePtr(v, func(b Value) (Value, error) { return e.emitInspectTuple(b, depth) })
		}
		return e.emitInspectTuple(v, depth)
	case isInspectHost(v.Ty):
		s, _, err := e.emitInspectHost(v, depth)
		return s, err
	case v.Ty.IsDynamicObject:
		// An index-signature dictionary is a plain object.
		bag, err := e.emitDictToBag(v)
		if err != nil {
			return Value{}, err
		}
		return e.emitInspectField(bag, depth)
	case v.Ty.Weak && (v.Ty.IsMap || v.Ty.IsSet):
		// A weak collection's entries are not observable.
		name := hostClassName(v.Ty)
		if depth > e.effectiveInspectDepth() {
			return Value{Ref: e.internString("[" + name + "]"), Ty: TypePtr}, nil
		}
		return Value{Ref: e.internString(name + " { <items unknown> }"), Ty: TypePtr}, nil
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
	case v.Ty.IsCollIter:
		return e.emitInspectCollIter(v, depth)
	case v.Ty.IsBuffer:
		return e.emitInspectBuffer(v)
	case v.Ty.IsError && !v.Ty.IsDynamic:
		// A builtin Error nested in an array or object renders as one does at
		// the top level.
		render := func(b Value) (Value, error) {
			b.Ty.Nullable, b.Ty.IsUndefined = false, false
			box, err := e.emitBoxValue(b)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynamicInspectAt(box, depth)
		}
		if v.Ty.Nullable || v.Ty.IsNull {
			return e.emitInspectNullablePtr(v, render)
		}
		return render(v)
	case v.Ty.IsSymbol && !v.Ty.Nullable && !v.Ty.IsDynamic:
		return e.emitSymbolToString(v) // `Symbol(x)`, at any depth
	case v.Ty.IsObject:
		// A special object type (URL/Headers/…) as a field — a V1 placeholder
		// rather than exposing its internal struct.
		return Value{Ref: e.internString("[Object]"), Ty: TypePtr}, nil
	case v.Ty.IsArray:
		if depth > e.effectiveInspectDepth() {
			// `[]` for an empty array even past the cap (Node's early return);
			// `[Array]` otherwise — a runtime length test. An absent
			// `T[] | undefined` is still its keyword.
			past := e.emitInspectArrayPastDepth(v)
			if !v.Ty.Nullable {
				return past, nil
			}
			isAbsent := e.emitArrayIsAbsent(v)
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isAbsent, e.internString(absentLiteral(v.Ty)), past.Ref))
			return Value{Ref: sel, Ty: TypePtr}, nil
		}
		// A `T[] | undefined` element (a nested-array element absence, TDD-00221)
		// renders as its keyword on a miss (null data-ptr), not `[]`. Inspecting a
		// null-data-ptr array is itself safe (len 0 → "[]"), so a select suffices.
		if v.Ty.Nullable {
			isAbsent := e.emitArrayIsAbsent(v)
			base := v.Ty
			base.Nullable, base.IsUndefined = false, false
			arr, err := e.emitInspectArray(Value{Ref: v.Ref, Ty: base, ArrayHeader: v.ArrayHeader}, depth)
			if err != nil {
				return Value{}, err
			}
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s",
				sel, isAbsent, e.internString(absentLiteral(v.Ty)), arr.Ref))
			return Value{Ref: sel, Ty: TypePtr}, nil
		}
		return e.emitInspectArray(v, depth)
	case v.Ty.IsFunc:
		return e.emitInspectFunc(v), nil
	case v.Ty.IsNull:
		return e.emitValueToString(v) // null / undefined, unquoted
	case isStringTy(v.Ty):
		// Node's strEscape: single quotes unless the string holds one (then
		// double, then backticks), control characters escaped (ADR-01067).
		e.ensureInspectReduce()
		quotedReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_quote(ptr %s)", quotedReg, v.Ref))
		quoted := Value{Ref: quotedReg, Ty: TypePtr}
		// An absent string (a null pointer: an out-of-range element stored on, a
		// `string | null` field) renders as its bare keyword, never quoted. A
		// plain `string` slot can only be null by absence — `undefined`.
		word := "undefined"
		if v.Ty.Nullable && !v.Ty.IsUndefined {
			word = "null"
		}
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, e.ptrIsNull(v.Ref), e.internString(word), quoted.Ref))
		return Value{Ref: sel, Ty: TypePtr}, nil
	case v.Ty.IsDate && v.Ty.IR == "i64" && !v.Ty.Nullable:
		// util.inspect shows a Date as its toISOString, Invalid Date as such.
		return e.emitDateOrInvalid(v, e.emitDateToISOString)
	default:
		s, err := e.emitValueToString(v) // number / bool / symbol
		if err != nil {
			return Value{}, err
		}
		if v.Ty.Float {
			s = e.inspectNegZero(v, s) // `[ -0 ]`, as util.inspect shows it
		}
		return s, nil
	}
}

// emitInspectArrayPastDepth renders an array beyond the inspection depth cap:
// Node's `[Array]` placeholder, or `[]` when it is empty (the empty early
// return precedes the depth check) — an absent `T[] | undefined` counts as
// empty here since its data pointer is null with length 0 (ADR-01067).
func (e *Emitter) emitInspectArrayPastDepth(v Value) Value {
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, v.Ref))
	full, empty := "[Array]", "[]"
	if v.Ty.IsTypedArray && !v.Ty.IsBuffer {
		// A typed array names its class: `[Uint8Array]`, `Uint8Array(0) []`.
		name := arrayTypeName(v.Ty)
		full, empty = "["+name+"]", name+"(0) []"
	}
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, e.icmpNe(lenReg, "0"), e.internString(full), e.internString(empty)))
	return Value{Ref: sel, Ty: TypePtr}
}

// inspectsByTag reports a union whose every member renders from its box: a
// scalar, an array, or an object with a header word (its layout row names
// its fields, TDD-00230 phase 5).
func inspectsByTag(ty Type) bool {
	if !ty.IsDynamic || len(ty.UnionMembers) == 0 {
		return false
	}
	for _, m := range ty.UnionMembers {
		if scalarTypeKind(m) != "" || m.IsArray || m.IsNull || m.IsUndefined {
			continue
		}
		if m.IsObject && hasObjHeader(m) && !m.IsError && !isInspectHost(m) {
			continue
		}
		return false
	}
	return true
}

// urlInspectOrder is the order Node's URL [util.inspect.custom] lists its
// properties in.
var urlInspectOrder = []string{"href", "origin", "protocol", "username", "password", "host", "hostname",
	"port", "pathname", "search", "searchParams", "hash"}

// declareInspectOptDepth declares util.inspect's run-time `depth` option
// (inspect_reduce.c) exactly once.
func (e *Emitter) declareInspectOptDepth() {
	if e.fnDecls["@__kml_inspect_opt_depth"] {
		return
	}
	e.fnDecls["@__kml_inspect_opt_depth"] = true
	e.emitGlobal("@__kml_inspect_opt_depth = external global i64")
}

// isErrorValue reports a statically typed Error: a builtin one or an Error
// subclass's instance.
func (e *Emitter) isErrorValue(t Type) bool {
	if t.IsDynamic {
		return false
	}
	if t.IsClass {
		info, ok := e.classes[t.ClassName]
		return ok && info.IsErrorSubclass
	}
	return t.IsError
}

// markErrorCauseOwn records that error obj's `cause` was assigned on it,
// making it an own enumerable property (boxsrc/errkeys.c).
func (e *Emitter) markErrorCauseOwn(obj Value) {
	e.emitErrorOwnKeysHooks()
	e.declareFn("__kml_error_own_cause", "declare void @__kml_error_own_cause(ptr)")
	e.emitInstr(fmt.Sprintf("call void @__kml_error_own_cause(ptr %s)", obj.Ref))
}

// markErrorNameOwn records that error obj's `name` was assigned on it, and
// where it falls among its own keys: JavaScript defines a class's fields
// when its constructor's super() returns, so a `name` assigned in class K's
// constructor follows the fields of K and its bases; one assigned anywhere
// else follows them all.
func (e *Emitter) markErrorNameOwn(obj Value) {
	idx, _, ok := obj.Ty.FieldIndex(errorNameOwnField)
	if !ok {
		return
	}
	pos := int64(1) << 40
	if info, ok := e.classes[e.currentCtorClass]; ok && info.IsErrorSubclass {
		pos = int64(len(errorOwnLayoutKeys(info.Ty)))
	}
	// The first assignment places the property; a later one keeps it there.
	gep, cur, unset, v := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, obj.Ty.StructIR(), obj.Ref, idx))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, gep))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", unset, cur))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", v, unset, pos+1, cur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, gep))
}

// inspectCustomMethod is the protocol name a class's `[inspect.custom]`
// method desugars to (the parser's wellKnownSymbolMemberName).
const inspectCustomMethod = "@@inspectCustom"

// emitInspectCustom renders a class instance through its own
// `[inspect.custom](depth, options)` when it has one, as util.inspect does:
// a string result is the rendering, any other value is inspected in its
// place. ok is false for a class without the method.
func (e *Emitter) emitInspectCustom(val Value, depth int) (Value, bool, error) {
	return e.emitInspectCustomAt(val, fmt.Sprint(depth), depth)
}

// emitInspectCustomAt is emitInspectCustom at the depth in register
// depthReg (an i64); a non-string result is inspected at that run-time
// depth, or at static depth when it is known statically (static >= 0).
func (e *Emitter) emitInspectCustomAt(val Value, depthReg string, static int) (Value, bool, error) {
	info, isClass := e.classes[val.Ty.ClassName]
	if !val.Ty.IsClass || !isClass {
		return Value{}, false, nil
	}
	sig, has := info.MethodSigs[inspectCustomMethod]
	if !has {
		return Value{}, false, nil
	}
	// depth: the levels left below this one; options: { depth }, the
	// configured depth, as Node's carries it; the third argument, inspect
	// itself, is not passed.
	// The configured depth: the compile-time one for a console.log site, the
	// run-time option (util.inspect's `depth`) for the shared hook. An
	// unlimited depth is Infinity levels left and `options.depth: null`.
	optDepth := fmt.Sprint(e.effectiveInspectDepth())
	if static < 0 {
		e.ensureInspectReduce()
		e.declareInspectOptDepth()
		optDepth = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_inspect_opt_depth, align 8", optDepth))
	}
	left := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", left, optDepth, depthReg))
	leftF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", leftF, left))
	var fullDepth ast.Expression = ast.NewNumberLiteral(fmt.Sprint(e.effectiveInspectDepth()), ast.Pos{})
	if static < 0 {
		inf, optF, boxed, depthBox := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %d", inf, optDepth, int64(1)<<62))
		capped := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0x7FF0000000000000, double %s", capped, inf, leftF))
		leftF = capped
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", optF, optDepth))
		b, err := e.emitBoxValue(Value{Ref: optF, Ty: TypeF64})
		if err != nil {
			return Value{}, true, err
		}
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", boxed, inf, nbNull, b.Ref))
		depthName := fmt.Sprintf("__kml_insp_optdepth_%d", e.dynFnCtr)
		e.dynFnCtr++
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", depthBox))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed, depthBox))
		e.define(depthName, Symbol{Ptr: depthBox, Ty: TypeAny})
		fullDepth = ast.NewIdentifier(depthName, ast.Pos{})
	}
	leftName := fmt.Sprintf("__kml_insp_depth_%d", e.dynFnCtr)
	e.dynFnCtr++
	leftSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca double, align 8", leftSlot))
	e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", leftF, leftSlot))
	e.define(leftName, Symbol{Ptr: leftSlot, Ty: TypeF64})
	leftID := ast.NewIdentifier(leftName, ast.Pos{})
	// options.depth is the inspection's configured depth (Node's
	// getUserOptions), not the levels left.
	opts := ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "depth", Value: fullDepth}}, ast.Pos{})
	all := []ast.Expression{leftID, opts}
	if n := len(sig.ParamTypes); n < len(all) {
		all = all[:n]
	}
	self := fmt.Sprintf("__kml_insp_self_%d", e.dynFnCtr)
	e.dynFnCtr++
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, slot))
	e.define(self, Symbol{Ptr: slot, Ty: val.Ty})
	r, err := e.emitClassMethodCall(val.Ty, ast.NewIdentifier(self, ast.Pos{}), inspectCustomMethod, all, ast.Pos{})
	if err != nil {
		return Value{}, true, err
	}
	if isStringTy(r.Ty) && !r.Ty.IsDynamic {
		// A string is spliced in indented to its nesting level.
		e.ensureInspectReduce()
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_reindent(ptr %s, i64 %s)", out, r.Ref, depthReg))
		return Value{Ref: out, Ty: r.Ty}, true, nil
	}
	if static < 0 {
		box, err := e.emitBoxValue(r)
		if err != nil {
			return Value{}, true, err
		}
		r = box
	}
	if r.Ty.IsDynamic {
		// A string held in any is the rendering itself; anything else is
		// inspected.
		var s Value
		if static >= 0 {
			var err error
			if s, err = e.emitDynamicInspectAt(r, static); err != nil {
				return Value{}, true, err
			}
		} else {
			e.ensureDynJSONC()
			e.declareFn("__kml_any_inspect_at", "declare ptr @__kml_any_inspect_at(i64, i64)")
			s = Value{Ref: e.freshReg(), Ty: TypePtr}
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_any_inspect_at(i64 %s, i64 %s)", s.Ref, r.Ref, depthReg))
		}
		tag, pay := e.emitUnboxTagPayload(r)
		isStr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
		e.ensureInspectReduce()
		str := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_reindent(ptr %s, i64 %s)", str, e.emitIntToPtr(pay), depthReg))
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", out, isStr, str, s.Ref))
		return Value{Ref: out, Ty: TypePtr}, true, nil
	}
	s, err := e.emitInspectField(r, static)
	return s, true, err
}

// emitToStringTagHook defines `ptr __kml_obj_tostring_tag(ptr o)`: the tag
// a class instance's [Symbol.toStringTag] getter gives, or null
// (Object.prototype.toString's `[object Tag]`). Each class with the getter
// registers a row naming its own thunk (unitreg.go).
func (e *Emitter) emitToStringTagHook() {
	getter := accessorMethodName("get", "@@toStringTag")
	for _, c := range e.sortedClassNames(func(name string, info ClassInfo) bool {
		return info.Ty.IsObject && e.classHasMethodInherited(name, getter)
	}) {
		info := e.classes[c]
		id := info.TagID & kmlHdrIDMask
		thunk := fmt.Sprintf("__kml_tostring_tag_%d", id)
		restore := e.beginThunkEmit()
		if t, err := e.emitClassCall(info.Ty, Value{Ref: "%o", Ty: info.Ty}, getter, nil, ast.Pos{}, false); err == nil && !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret ptr %s", e.coerce(t, TypePtr).Ref))
		}
		if !e.blockDone {
			e.emitTerminator("ret ptr null")
		}
		body := e.allocas.String() + e.body.String()
		restore()
		e.emitGlobal(fmt.Sprintf("define internal ptr @%s(ptr %%o) {\nentry:\n%s}", thunk, body))
		e.addUnitRow(unitKindToStringTag, "{ i64, ptr }", id, "ptr @"+thunk)
	}
	e.emitUnitDispatch("__kml_obj_tostring_tag", unitKindToStringTag, "", "")
}

// emitInspectCustomHook defines `ptr __kml_obj_inspect_custom(ptr o, i64
// depth)`: a class instance's own `[inspect.custom]` rendering, or null.
// Each class with the method registers a row naming its own thunk.
func (e *Emitter) emitInspectCustomHook() {
	for _, c := range e.sortedClassNames(func(_ string, info ClassInfo) bool {
		_, has := info.MethodSigs[inspectCustomMethod]
		return has && info.Ty.IsObject
	}) {
		info := e.classes[c]
		id := info.TagID & kmlHdrIDMask
		thunk := fmt.Sprintf("__kml_inspect_custom_%d", id)
		restore := e.beginThunkEmit()
		if s, ok, err := e.emitInspectCustomAt(Value{Ref: "%o", Ty: info.Ty}, "%depth", -1); ok && err == nil && !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret ptr %s", s.Ref))
		}
		if !e.blockDone {
			e.emitTerminator("ret ptr null")
		}
		body := e.allocas.String() + e.body.String()
		restore()
		e.emitGlobal(fmt.Sprintf("define internal ptr @%s(ptr %%o, i64 %%depth) {\nentry:\n%s}", thunk, body))
		e.addUnitRow(unitKindInspectCustom, "{ i64, ptr }", id, "ptr @"+thunk)
	}
	e.emitUnitDispatch("__kml_obj_inspect_custom", unitKindInspectCustom, ", i64 %depth", ", i64 %depth")
}

// sortedClassNames lists the classes keep accepts, in name order.
func (e *Emitter) sortedClassNames(keep func(string, ClassInfo) bool) []string {
	var out []string
	for name, info := range e.classes {
		if keep(name, info) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
