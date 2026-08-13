package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitJSONStringifyArray resolves arrExpr and delegates to
// emitJSONStringifyArrayData — see that function's doc comment.
func (e *Emitter) emitJSONStringifyArray(arrExpr ast.Expression, pos ast.Pos) (Value, error) {
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(arrExpr, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitJSONStringifyArrayData(ptrReg, lenReg, elemTy)
}

// emitJSONStringifyArrayValue is emitJSONStringifyArray's counterpart for an
// array that's already an evaluated {ptr,i64} aggregate Value rather than an
// unevaluated ast.Expression — used by emitJSONStringifyValue's own IsArray
// branch below to recurse into a nested-array element (TDD-00029), which
// loadArrayElem has already unboxed into exactly this shape by the time it
// gets here.
func (e *Emitter) emitJSONStringifyArrayValue(val Value) (Value, error) {
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
	return e.emitJSONStringifyArrayData(ptrReg, lenReg, *val.Ty.ElemType)
}

// emitJSONStringifyArrayData is emitJSONStringifyArray/
// emitJSONStringifyArrayValue's shared core: builds a JSON array
// "[e1,e2,...]" from any element type by looping at runtime and delegating
// each element to emitJSONStringifyValue (which already correctly handles
// numbers, strings, booleans, nested objects, and — recursively, via
// emitJSONStringifyArrayValue — nested arrays) — the same runtime
// accumulator-loop shape emitArrayJoin uses, just bracketed and JSON-
// encoding each element instead of plain-string-joining.
func (e *Emitter) emitJSONStringifyArrayData(ptrReg, lenReg string, elemTy Type) (Value, error) {
	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("["), accAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("jsonarr.cond")
	bodyL := e.freshLabel("jsonarr.body")
	firstL := e.freshLabel("jsonarr.first")
	restL := e.freshLabel("jsonarr.rest")
	incL := e.freshLabel("jsonarr.inc")
	doneL := e.freshLabel("jsonarr.done")

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
	inElem := e.loadArrayElem(inGep, elemTy)
	elemJSONVal, err := e.emitJSONStringifyValue(inElem)
	if err != nil {
		return Value{}, err
	}
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	e.emitLabel(firstL)
	accAtFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accAtFirst, accAlloca))
	firstAcc, err := e.emitStringConcat(Value{Ref: accAtFirst, Ty: TypePtr}, elemJSONVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", firstAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(restL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accCur, accAlloca))
	withComma, err := e.emitStringConcat(Value{Ref: accCur, Ty: TypePtr}, Value{Ref: e.internString(","), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	newAcc, err := e.emitStringConcat(withComma, elemJSONVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	preClose := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", preClose, accAlloca))
	return e.emitStringConcat(Value{Ref: preClose, Ty: TypePtr}, Value{Ref: e.internString("]"), Ty: TypePtr})
}

func (e *Emitter) emitJSONStringify(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: JSON.stringify expects at least 1 argument", pos.Line, pos.Col)
	}
	argTy := e.inferExprType(args[0])

	if argTy.IsArray && argTy.ElemType != nil {
		return e.emitJSONStringifyArray(args[0], pos)
	}

	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}

	return e.emitJSONStringifyValue(val)
}

// emitJSONStringifyObject builds {"k1":v1,"k2":v2,...} inline by walking the
// known fields of a statically-typed object. Handles nested objects recursively.
func (e *Emitter) emitJSONStringifyObject(val Value) (Value, error) {
	acc := Value{Ref: e.internString("{"), Ty: TypePtr}
	for i, field := range val.Ty.VisibleFields() {
		idx, _, _ := val.Ty.FieldIndex(field.Name)
		// Load the field value via GEP.
		gepReg := e.freshReg()
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
			gepReg, val.Ty.StructIR(), val.Ref, idx))
		// An array-typed field's struct slot is a 16-byte {ptr,i64}
		// aggregate (StructFieldIR, ADR-00061), not field.Ty.IR's plain
		// "ptr" — loading with the wrong width here silently dropped the
		// array's length (found while wiring nested-array JSON support,
		// TDD-00029; pre-existing and independent of nesting — any
		// array-typed object field, e.g. `{ tags: string[] }`, already hit
		// this).
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
			loadReg, StructFieldIR(field.Ty), gepReg, field.Ty.Align()))
		fieldVal := Value{Ref: loadReg, Ty: field.Ty}

		// Key segment: `"name":` with a leading comma after the first field.
		keyStr := `"` + field.Name + `":`
		if i > 0 {
			keyStr = "," + keyStr
		}
		keyPart := Value{Ref: e.internString(keyStr), Ty: TypePtr}
		var err error
		acc, err = e.emitStringConcat(acc, keyPart)
		if err != nil {
			return Value{}, err
		}

		// JSON-encode the field value.
		jsonVal, err := e.emitJSONStringifyValue(fieldVal)
		if err != nil {
			return Value{}, err
		}
		acc, err = e.emitStringConcat(acc, jsonVal)
		if err != nil {
			return Value{}, err
		}
	}
	return e.emitStringConcat(acc, Value{Ref: e.internString("}"), Ty: TypePtr})
}

// emitJSONStringifyValue returns a ptr string with the JSON encoding of val.
// Handles strings (quoted), numbers, booleans, and nested objects recursively.
func (e *Emitter) emitJSONStringifyValue(val Value) (Value, error) {
	// Must be checked before the generic IsObject branch below — Symbol
	// reuses IsObject's struct representation (see IsSymbol's doc comment,
	// types.go), so without this it would silently serialize as
	// {"description":"..."} instead of erroring like real JS does on a bare
	// Symbol argument. See docs/tdd/TDD-00044.md.
	if val.Ty.IsSymbol {
		return Value{}, fmt.Errorf("JSON.stringify does not support symbol values")
	}
	if val.Ty.IsObject {
		return e.emitJSONStringifyObject(val)
	}
	if val.Ty.IsArray {
		return e.emitJSONStringifyArrayValue(val)
	}
	switch val.Ty.IR {
	case "i1":
		trueStr := e.internString("true")
		falseStr := e.internString("false")
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, val.Ref, trueStr, falseStr))
		return Value{Ref: r, Ty: TypePtr}, nil
	case "ptr":
		e.ensureJSONStringifyStr()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_str_str(ptr %s)", r, val.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	default:
		if val.Ty.IsDate {
			// Real JS calls Date.prototype.toJSON() (== toISOString()) during
			// stringification instead of serializing the raw ms timestamp;
			// reuse the existing formatter and JSON-quote its result like any
			// other string.
			iso, err := e.emitDateToISOString(val)
			if err != nil {
				return Value{}, err
			}
			e.ensureJSONStringifyStr()
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_str_str(ptr %s)", r, iso.Ref))
			return Value{Ref: r, Ty: TypePtr}, nil
		}
		if val.Ty.Float {
			// Coercing a float to i64 below would truncate (9.5 -> 9) instead
			// of formatting it; emitValueToString already does correct %g
			// formatting for floats, so reuse it instead of a separate helper.
			return e.emitValueToString(val)
		}
		e.ensureJSONStringifyNum()
		coerced := e.coerce(val, TypeI64)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_str_num(i64 %s)", r, coerced.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
}

func (e *Emitter) emitJSONParse(args []ast.Expression, targetTy Type, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: JSON.parse expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.emitJSONParseValue(val, targetTy, pos)
}

// emitJSONParseValue is emitJSONParse's core, factored out so any already-
// evaluated string Value can be parsed (not just a literal call argument) —
// used directly by Response.json() (emit_fetch.go), which already has the
// buffered response body as a Value with nothing left to re-evaluate.
func (e *Emitter) emitJSONParseValue(val Value, targetTy Type, pos ast.Pos) (Value, error) {
	if val.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: JSON.parse expects a string argument", pos.Line, pos.Col)
	}
	if targetTy.IsObject {
		return e.emitJSONParseObject(val, targetTy, pos)
	}
	if targetTy.IR == TypeI64.IR && !targetTy.IsArray && !targetTy.IsObject {
		e.ensureAtoll()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", r, val.Ref))
		return Value{Ref: r, Ty: TypeI64}, nil
	}
	e.ensureJSONParseStr()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_parse_str(ptr %s)", r, val.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitJSONParseObject parses a flat JSON object text into a heap-allocated
// struct matching targetTy's field layout, known fully at compile time from
// the type annotation. Per field: find "name": in the text (or use a
// zero-value default if the key is missing), parse the value according to
// the field's compile-time type, and GEP+store it — the same "malloc struct,
// then per-field GEP+store" shape emitObjectLiteral/emitJSONStringifyObject
// already use, just sourcing each value from the runtime JSON text instead of
// a literal expression. Nested object fields are not supported (would need
// brace-matched substring isolation to avoid a field-finder incorrectly
// matching a same-named key belonging to a later sibling object) — a clean
// error here instead of silently producing wrong reads for that shape.
func (e *Emitter) emitJSONParseObject(jsonVal Value, targetTy Type, pos ast.Pos) (Value, error) {
	if targetTy.IsClass {
		return Value{}, fmt.Errorf("%d:%d: JSON.parse into a class instance is not supported", pos.Line, pos.Col)
	}
	for _, f := range targetTy.Fields {
		if f.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: JSON.parse into a nested object field ('%s') is not yet supported", pos.Line, pos.Col, f.Name)
		}
		// An array-typed field falls through to the scalar-only per-field parse
		// path below, which would emit a type-mismatched phi (a scalar value
		// where the field's `ptr` array type is expected) and fail at the clang
		// stage. Reject cleanly instead — array-valued fields aren't parsed yet.
		if f.Ty.IsArray {
			return Value{}, fmt.Errorf("%d:%d: JSON.parse into an array-typed field ('%s') is not yet supported", pos.Line, pos.Col, f.Name)
		}
	}

	e.ensureMalloc()
	structIR := targetTy.StructIR()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, targetTy.StructSize()))

	e.ensureJSONFindValue()
	for i, f := range targetTy.Fields {
		pattern := e.internString(`"` + f.Name + `":`)
		valStart := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_find_value(ptr %s, ptr %s)", valStart, jsonVal.Ref, pattern))
		isMissing := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isMissing, valStart))

		foundL := e.freshLabel("jsonobj.found")
		missingL := e.freshLabel("jsonobj.missing")
		mergeL := e.freshLabel("jsonobj.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isMissing, missingL, foundL))

		e.emitLabel(foundL)
		parsedVal, err := e.emitJSONParseFieldValue(valStart, f.Ty)
		if err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(missingL)
		// A missing plain-string field must default to an empty string, not
		// zeroRef's general ptr default of `null` — every other string
		// operation in this compiler (concatenation, .length, console.log,
		// etc.) assumes a `string`-typed value is never null, unlike an
		// object/array/closure field, where null genuinely is the only
		// sensible zero value. Storing `null` here and later printing or
		// concatenating it is undefined behavior (passing NULL to printf's
		// "%s") — confirmed directly: `JSON.parse` into an object whose
		// string field's key is absent from the source text crashed
		// (SIGTRAP/SIGSEGV, depending on how aggressively the optimizer
		// exploited the resulting UB) before this fix.
		defaultRef := zeroRef(f.Ty)
		if f.Ty.IR == "ptr" && !f.Ty.IsObject && !f.Ty.IsArray && !f.Ty.IsFunc {
			defaultRef = e.internString("")
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		fieldReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", fieldReg, f.Ty.IR, parsedVal.Ref, foundL, defaultRef, missingL))

		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", f.Ty.IR, fieldReg, gepReg, f.Ty.Align()))
	}

	return Value{Ref: dataReg, Ty: targetTy}, nil
}

// emitJSONParseFieldValue parses the JSON value text starting at valStart
// (already past whitespace) according to fieldTy: boolean via strncmp
// against "true", float via strtod, integer via atoll (matching the existing
// JSON.parse(s) -> number behavior), string via __kml_json_parse_field_str.
func (e *Emitter) emitJSONParseFieldValue(valStart string, fieldTy Type) (Value, error) {
	switch {
	case fieldTy.IR == "i1":
		e.ensureStrncmp()
		trueStr := e.internString("true")
		cmp := e.freshReg()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strncmp(ptr %s, ptr %s, i64 4)", cmp, valStart, trueStr))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", result, cmp))
		return Value{Ref: result, Ty: TypeBool}, nil
	case fieldTy.Float:
		e.ensureStrtod()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @strtod(ptr %s, ptr null)", result, valStart))
		return Value{Ref: result, Ty: fieldTy}, nil
	case fieldTy.IR == "ptr" && !fieldTy.IsObject && !fieldTy.IsArray && !fieldTy.IsFunc:
		e.ensureJSONParseFieldStr()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_parse_field_str(ptr %s)", result, valStart))
		return Value{Ref: result, Ty: TypePtr}, nil
	default:
		e.ensureAtoll()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", result, valStart))
		return e.coerce(Value{Ref: result, Ty: TypeI64}, fieldTy), nil
	}
}
