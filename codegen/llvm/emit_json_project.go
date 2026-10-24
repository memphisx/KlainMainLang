package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emit_json_project.go — P3 of the JSON parse rewrite (TDD-00077 Track P).
// Projects a parsed JSON tree (built by json_parse.c, P1) onto a compile-time
// target type: a type-directed walk of (node, targetTy) that allocates fresh
// storage outliving the tree. This replaces the old lenient strstr per-field
// extraction and, in one path, handles nested objects, array-typed fields,
// object-array fields, and top-level `T[]` — the cluster of caveats the old
// extractor rejected.

// emitJSONProject projects the JSON node (a ptr-to-KmlJsonNode register,
// assumed non-null) onto targetTy, returning a StructFieldIR(targetTy)-typed
// Value. Strings are copied out of the tree so they survive __kml_json_free.
func (e *Emitter) emitJSONProject(node string, targetTy Type, pos ast.Pos) (Value, error) {
	switch {
	case targetTy.IsClass():
		return Value{}, fmt.Errorf("%d:%d: JSON.parse into a class instance is not supported", pos.Line, pos.Col)
	case targetTy.IsDynamic() && targetTy.UnionMembers != nil:
		return Value{}, fmt.Errorf("%d:%d: JSON.parse into a union type is not supported — use any/unknown or a concrete shape", pos.Line, pos.Col)
	case targetTy.IsDynamic():
		// Untyped parse (TDD-00155 Stage 2 / TDD-00077 P4): convert the whole
		// tree into dynamic values — objects become tag-10 bags, arrays tag-11
		// dynamic arrays, scalars their box tags. Strings/keys are copied out,
		// so the caller's __kml_json_free stays correct.
		e.ensureDynJSONFromNode()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynjson_from_node(ptr %s)", r, node))
		return Value{Ref: r, Ty: TypeAny}, nil
	case targetTy.IsTuple():
		return e.emitJSONProjectTuple(node, targetTy, pos)
	case targetTy.IsArray():
		return e.emitJSONProjectArray(node, targetTy, pos)
	case targetTy.IsObject():
		return e.emitJSONProjectObject(node, targetTy, pos)
	case targetTy.IR == "i1":
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_json_bool(ptr %s)", b, node))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", r, b))
		return Value{Ref: r, Ty: TypeBool}, nil
	case targetTy.Float:
		lex := e.emitJSONNumLexeme(node)
		e.ensureStrtod()
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @strtod(ptr %s, ptr null)", d, lex))
		if targetTy.IR == "float" {
			f := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", f, d))
			return Value{Ref: f, Ty: targetTy}, nil
		}
		return Value{Ref: d, Ty: targetTy}, nil
	case isStringTy(targetTy):
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_string_dup(ptr %s)", s, node))
		if triStr(targetTy) {
			// A JSON null is null: nullRef (TDD-00257).
			kind, isNull, r := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_json_kind(ptr %s)", kind, node))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isNull, kind)) // KJSON_NULL == 0
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, isNull, e.nullRef(), s))
			s = r
		}
		return Value{Ref: s, Ty: targetTy}, nil
	case targetTy.IR == "ptr" || targetTy.IsFunc():
		// A member JSON cannot spell (a function, a host value): the value
		// the text holds, converted as from any.
		e.ensureDynJSONFromNode()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynjson_from_node(ptr %s)", r, node))
		return e.convert(Value{Ref: r, Ty: TypeAny}, targetTy), nil
	default: // integer scalars (i64/i32/i16/i8)
		lex := e.emitJSONNumLexeme(node)
		e.ensureAtoll()
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", n, lex))
		return e.convert(Value{Ref: n, Ty: TypeI64}, targetTy), nil
	}
}

// emitJSONNumLexeme returns a register holding the node's raw numeric token
// (transient — atoll/strtod consume it before the tree is freed).
func (e *Emitter) emitJSONNumLexeme(node string) string {
	lex := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_num_lexeme(ptr %s)", lex, node))
	return lex
}

// emitJSONProjectObject projects a JSON object node onto an object/interface
// type: a fresh malloc'd struct whose every field is projected from the node's
// value for that key (or a type-appropriate default when the key is absent).
// Nested object and array fields recurse through emitJSONProject.
func (e *Emitter) emitJSONProjectObject(node string, targetTy Type, pos ast.Pos) (Value, error) {
	e.ensureMalloc()
	structIR := targetTy.StructIR()
	dataReg := e.freshReg()
	e.emitObjMallocInto(dataReg, targetTy)

	for i, f := range targetTy.Fields {
		if isHiddenField(f.Name) {
			continue
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, dataReg, i))
		fieldIR := StructFieldIR(f.Ty)

		child := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_get(ptr %s, ptr %s)", child, node, e.internString(f.Name)))
		isMissing := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isMissing, child))

		foundL := e.freshLabel("jsonp.found")
		missingL := e.freshLabel("jsonp.missing")
		mergeL := e.freshLabel("jsonp.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isMissing, missingL, foundL))

		// Store the projected value directly into the field slot in each branch
		// (rather than a phi), since a nested object/array projection emits its
		// own control flow and would break a phi's single-predecessor assumption.
		e.emitLabel(foundL)
		foundRef, err := e.emitJSONFieldFound(child, f.Ty, pos)
		if err != nil {
			return Value{}, err
		}
		// An array field holds a header pointer (TDD-00213 Stage 2); store the
		// projected array's header.
		if f.Ty.IsArray() {
			e.storeArrayFieldHeader(gep, Value{Ref: foundRef, Ty: f.Ty})
		} else {
			e.heapStore(fieldIR, foundRef, gep, f.Ty.Align())
		}
		e.emitPresenceInit(dataReg, targetTy, f.Name) // the input has the key
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(missingL)
		if f.Ty.IsArray() {
			e.storeArrayFieldHeader(gep, Value{Ref: e.jsonDefaultRef(f.Ty), Ty: f.Ty})
		} else {
			e.heapStore(fieldIR, e.jsonDefaultRef(f.Ty), gep, f.Ty.Align())
		}
		e.emitPresenceAbsentInit(dataReg, targetTy, f.Name) // the input lacks the key
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
	}
	e.emitKeyOrderJSON(dataReg, targetTy, node) // the input's key order
	e.emitJSONExpandoRest(dataReg, node, targetTy)
	return Value{Ref: dataReg, Ty: targetTy}, nil
}

// emitJSONExpandoRest keeps the node's keys the target shape does not
// declare as the projected object's own expando properties.
func (e *Emitter) emitJSONExpandoRest(obj, node string, targetTy Type) {
	e.ensureDynJSONFromNode()
	e.ensureShapeRuntime()
	e.usedObjExtensible = true // inspect, keys and stringify read the extras
	if !e.usedJSONExpandoRest {
		e.usedJSONExpandoRest = true
		e.emitGlobal("declare void @__kml_json_expando_rest(ptr, ptr, ptr, i64)")
	}
	var refs []string
	for _, f := range targetTy.UserFields() {
		refs = append(refs, "ptr "+e.internString(f.Name))
	}
	tab := "null"
	if len(refs) > 0 {
		tab = fmt.Sprintf("@__kml_json_known_%d", e.jsonKnownTabs)
		e.jsonKnownTabs++
		e.emitGlobal(fmt.Sprintf("%s = private constant [%d x ptr] [%s]", tab, len(refs), strings.Join(refs, ", ")))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_json_expando_rest(ptr %s, ptr %s, ptr %s, i64 %d)", obj, node, tab, len(refs)))
}

// emitJSONProjectTuple projects a JSON array node onto a fixed-arity tuple
// struct: a fresh malloc'd struct whose field i is projected from the array's
// i-th element (or a type-appropriate default when the array has fewer than i+1
// elements). Structurally identical to emitJSONProjectObject — same malloc /
// StructIR / per-field found-missing-merge branch / emitJSONFieldFound /
// jsonDefaultRef — except the child node is fetched positionally via
// __kml_json_item(node, i) rather than by key. Nested object/array/tuple
// elements recurse through emitJSONFieldFound → emitJSONProject.
func (e *Emitter) emitJSONProjectTuple(node string, targetTy Type, pos ast.Pos) (Value, error) {
	e.ensureMalloc()
	structIR := targetTy.StructIR()
	dataReg := e.freshReg()
	e.emitObjMallocInto(dataReg, targetTy)

	for i, f := range targetTy.Fields {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, dataReg, i))
		fieldIR := StructFieldIR(f.Ty)

		child := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_item(ptr %s, i64 %d)", child, node, i))
		isMissing := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isMissing, child))

		foundL := e.freshLabel("jsonp.tfound")
		missingL := e.freshLabel("jsonp.tmissing")
		mergeL := e.freshLabel("jsonp.tmerge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isMissing, missingL, foundL))

		e.emitLabel(foundL)
		foundRef, err := e.emitJSONFieldFound(child, f.Ty, pos)
		if err != nil {
			return Value{}, err
		}
		// An array field holds a header pointer (TDD-00213 Stage 2); store the
		// projected array's header.
		if f.Ty.IsArray() {
			e.storeArrayFieldHeader(gep, Value{Ref: foundRef, Ty: f.Ty})
		} else {
			e.heapStore(fieldIR, foundRef, gep, f.Ty.Align())
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(missingL)
		if f.Ty.IsArray() {
			e.storeArrayFieldHeader(gep, Value{Ref: e.jsonDefaultRef(f.Ty), Ty: f.Ty})
		} else {
			e.heapStore(fieldIR, e.jsonDefaultRef(f.Ty), gep, f.Ty.Align())
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
	}
	return Value{Ref: dataReg, Ty: targetTy}, nil
}

// emitJSONFieldFound projects a present field value (child is non-null),
// handling a nullable-scalar field's null-vs-value boxing on top of the plain
// projection. Returns a StructFieldIR(fieldTy)-typed value ref.
func (e *Emitter) emitJSONFieldFound(child string, fieldTy Type, pos ast.Pos) (string, error) {
	if isNullableScalar(fieldTy) {
		// A literal `null` value boxes as absent; any other value as present.
		kind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_json_kind(ptr %s)", kind, child))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isNull, kind)) // KJSON_NULL == 0
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", present, isNull))
		payload, err := e.emitJSONProject(child, fieldTy.withoutNullable(), pos)
		if err != nil {
			return "", err
		}
		pl := payload.Ref
		if fieldTy.NullAndUndef() {
			// A JSON null in a three-state field is null, not a missing key.
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", r, isNull, fieldTy.IR, triMarker(fieldTy), fieldTy.IR, pl))
			pl = r
		}
		return e.makeNullableScalarAgg(fieldTy, present, pl), nil
	}
	if fieldTy.Nullable() && fieldTy.IR == "ptr" && !fieldTy.IsArray() && !fieldTy.IsDynamic() {
		// A JSON null in a `T | null` (or optional) string or object field
		// is the absent pointer, not an empty string or object.
		kind, isNull := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_json_kind(ptr %s)", kind, child))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isNull, kind)) // KJSON_NULL == 0
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		nullRef := "null"
		if fieldTy.NullAndUndef() || triStr(fieldTy) {
			nullRef = e.nullRef() // null, distinct from a missing key's undefined
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nullRef, slot))
		valL, doneL := e.freshLabel("jsonp.nonnull"), e.freshLabel("jsonp.nulldone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, valL))
		e.emitLabel(valL)
		v, err := e.emitJSONProject(child, fieldTy.withoutNullable(), pos)
		if err != nil {
			return "", err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		return r, nil
	}
	v, err := e.emitJSONProject(child, fieldTy, pos)
	if err != nil {
		return "", err
	}
	return v.Ref, nil
}

// jsonDefaultRef returns the default value ref for a field whose key is absent
// from the JSON, matching the old extractor's per-type defaults: empty string
// for a string, an absent aggregate for a nullable scalar, an empty array
// for an array, null for an object/function, and the zero value otherwise.
func (e *Emitter) jsonDefaultRef(ty Type) string {
	switch {
	case isNullableScalar(ty):
		return e.makeNullableScalarAgg(ty, "false", zeroRef(ty.withoutNullable()))
	case ty.IsArray():
		r1 := e.newArrayHeader("null", "0")
		return r1
	case isStringTy(ty) && ty.Nullable() || triStr(ty):
		// A missing field is undefined, the null pointer (TDD-00257).
		return "null"
	case isF64Slot(ty):
		// A missing number is undefined, the double's sentinel (TDD-00241).
		u := int64(undefF64)
		return fmt.Sprintf("0x%016X", uint64(u))
	case isStringTy(ty):
		// Under -mm=auto the projected tree may be deep-freed (TDD-00175
		// Stage 1), and __kml_str_free on an interned global corrupts the
		// heap — so an absent string field defaults to a fresh heap copy of
		// "" there. Every present string is already a heap dup
		// (__kml_json_string_dup), making the whole tree uniformly owned.
		if e.isAutoMode() {
			e.ensureStrHeaderRuntime()
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", r, e.internString("")))
			return r
		}
		return e.internString("")
	default:
		return zeroRef(ty)
	}
}

// emitJSONProjectArray projects a JSON array node onto `T[]`: a fresh buffer of
// the node's length with each element projected as T, returned as the standard
// array value. Object elements (`Item[]`) and nested arrays recurse.
func (e *Emitter) emitJSONProjectArray(node string, targetTy Type, pos ast.Pos) (Value, error) {
	elemTy := *targetTy.ElemType
	e.ensureMalloc()

	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_json_len(ptr %s)", lenReg, node))
	sizeReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", sizeReg, lenReg, elemTy.Align()))
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, sizeReg)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("jsonparr.cond")
	bodyL := e.freshLabel("jsonparr.body")
	incL := e.freshLabel("jsonparr.inc")
	doneL := e.freshLabel("jsonparr.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	item := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_item(ptr %s, i64 %s)", item, node, idxVal))
	elemVal, err := e.emitJSONProject(item, elemTy, pos)
	if err != nil {
		return Value{}, err
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, dataReg, idxVal))
	e.storeArrayElem(gep, elemTy, elemVal)
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r1 := e.newArrayHeader(dataReg, lenReg)
	// A JSON null is null's header, nullRef, not an empty array.
	kind, isNull, r := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_json_kind(ptr %s)", kind, node))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isNull, kind)) // KJSON_NULL == 0
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, isNull, e.nullRef(), r1))
	return Value{Ref: r, Ty: ArrayOf(elemTy)}, nil
}
