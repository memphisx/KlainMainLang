package llvm

import (
	"KlainMainLang/ast"
	"fmt"
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
	case targetTy.IsClass:
		return Value{}, fmt.Errorf("%d:%d: JSON.parse into a class instance is not supported", pos.Line, pos.Col)
	case targetTy.IsDynamic:
		return Value{}, fmt.Errorf("%d:%d: JSON.parse into any/unknown is not yet supported", pos.Line, pos.Col)
	case targetTy.IsTuple:
		return e.emitJSONProjectTuple(node, targetTy, pos)
	case targetTy.IsArray:
		return e.emitJSONProjectArray(node, targetTy, pos)
	case targetTy.IsObject:
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
		return Value{Ref: s, Ty: targetTy}, nil
	default: // integer scalars (i64/i32/i16/i8)
		lex := e.emitJSONNumLexeme(node)
		e.ensureAtoll()
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", n, lex))
		return e.coerce(Value{Ref: n, Ty: TypeI64}, targetTy), nil
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, targetTy.StructSize()))

	for i, f := range targetTy.Fields {
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldIR, foundRef, gep, f.Ty.Align()))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(missingL)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldIR, e.jsonDefaultRef(f.Ty), gep, f.Ty.Align()))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
	}
	return Value{Ref: dataReg, Ty: targetTy}, nil
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, targetTy.StructSize()))

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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldIR, foundRef, gep, f.Ty.Align()))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(missingL)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldIR, e.jsonDefaultRef(f.Ty), gep, f.Ty.Align()))
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
		return e.makeNullableScalarAgg(fieldTy, present, payload.Ref), nil
	}
	v, err := e.emitJSONProject(child, fieldTy, pos)
	if err != nil {
		return "", err
	}
	return v.Ref, nil
}

// jsonDefaultRef returns the default value ref for a field whose key is absent
// from the JSON, matching the old extractor's per-type defaults: empty string
// for a string, an absent aggregate for a nullable scalar, an empty {ptr,i64}
// for an array, null for an object/function, and the zero value otherwise.
func (e *Emitter) jsonDefaultRef(ty Type) string {
	switch {
	case isNullableScalar(ty):
		return e.makeNullableScalarAgg(ty, "false", zeroRef(ty.withoutNullable()))
	case ty.IsArray:
		r0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr null, 0", r0))
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", r1, r0))
		return r1
	case isStringTy(ty):
		return e.internString("")
	default:
		return zeroRef(ty)
	}
}

// emitJSONProjectArray projects a JSON array node onto `T[]`: a fresh buffer of
// the node's length with each element projected as T, returned as the standard
// {ptr,i64} aggregate. Object elements (`Item[]`) and nested arrays recurse.
func (e *Emitter) emitJSONProjectArray(node string, targetTy Type, pos ast.Pos) (Value, error) {
	elemTy := *targetTy.ElemType
	e.ensureMalloc()

	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_json_len(ptr %s)", lenReg, node))
	sizeReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", sizeReg, lenReg, elemTy.Align()))
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, sizeReg))

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
	r0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}
