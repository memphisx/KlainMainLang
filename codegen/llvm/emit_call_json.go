package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
	"strings"
)

// jsonIndent carries JSON.stringify's pretty-print state (TDD-00077 Track S).
// An empty unit means compact mode — byte-identical to the pre-pretty output —
// so an absent `space` argument leaves every existing call path unchanged.
type jsonIndent struct {
	unit  string // one indentation level ("" ⇒ compact, no whitespace)
	depth int    // current nesting depth
}

func (ji jsonIndent) pretty() bool      { return ji.unit != "" }
func (ji jsonIndent) child() jsonIndent { return jsonIndent{ji.unit, ji.depth + 1} }
func (ji jsonIndent) pad() string       { return strings.Repeat(ji.unit, ji.depth) }
func (ji jsonIndent) childPad() string  { return strings.Repeat(ji.unit, ji.depth+1) }

// colon is the key/value separator: ": " in pretty mode, ":" compact.
func (ji jsonIndent) colon() string {
	if ji.pretty() {
		return ": "
	}
	return ":"
}

// itemPrefix returns the whitespace/comma that precedes element/member number i
// (0-based): compact uses "" then ","; pretty puts each item on its own line
// indented one level deeper than the bracket.
func (ji jsonIndent) itemPrefix(i int) string {
	if !ji.pretty() {
		if i == 0 {
			return ""
		}
		return ","
	}
	if i == 0 {
		return "\n" + ji.childPad()
	}
	return ",\n" + ji.childPad()
}

// closeBracket returns the closing bracket for a container that held nItems:
// pretty puts a non-empty container's close on its own line at the parent
// indent, while an empty container (and all compact output) closes inline.
func (ji jsonIndent) closeBracket(bracket string, nItems int) string {
	if ji.pretty() && nItems > 0 {
		return "\n" + ji.pad() + bracket
	}
	return bracket
}

// jsonAppend concatenates a compile-time-constant string onto acc, skipping the
// concat entirely for the empty string (compact mode's common no-op case).
func (e *Emitter) jsonAppend(acc Value, s string) (Value, error) {
	if s == "" {
		return acc, nil
	}
	return e.emitStringConcat(acc, Value{Ref: e.internString(s), Ty: TypePtr})
}

// emitJSONStringifyArray resolves arrExpr and delegates to
// emitJSONStringifyArrayData — see that function's doc comment.
func (e *Emitter) emitJSONStringifyArray(arrExpr ast.Expression, pos ast.Pos, ind jsonIndent) (Value, error) {
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(arrExpr, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitJSONStringifyArrayData(ptrReg, lenReg, elemTy, ind)
}

// emitJSONStringifyArrayValue is emitJSONStringifyArray's counterpart for an
// array that's already an evaluated {ptr,i64} aggregate Value rather than an
// unevaluated ast.Expression — used by emitJSONStringifyValue's own IsArray
// branch below to recurse into a nested-array element (TDD-00029), which
// loadArrayElem has already unboxed into exactly this shape by the time it
// gets here.
func (e *Emitter) emitJSONStringifyArrayValue(val Value, ind jsonIndent) (Value, error) {
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
	return e.emitJSONStringifyArrayData(ptrReg, lenReg, *val.Ty.ElemType, ind)
}

// emitJSONStringifyArrayData is emitJSONStringifyArray/
// emitJSONStringifyArrayValue's shared core: builds a JSON array
// "[e1,e2,...]" from any element type by looping at runtime and delegating
// each element to emitJSONStringifyValue (which already correctly handles
// numbers, strings, booleans, nested objects, and — recursively, via
// emitJSONStringifyArrayValue — nested arrays) — the same runtime
// accumulator-loop shape emitArrayJoin uses, just bracketed and JSON-
// encoding each element instead of plain-string-joining.
func (e *Emitter) emitJSONStringifyArrayData(ptrReg, lenReg string, elemTy Type, ind jsonIndent) (Value, error) {
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
	elemJSONVal, err := e.emitJSONStringifyValue(inElem, ind.child())
	if err != nil {
		return Value{}, err
	}
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	e.emitLabel(firstL)
	accAtFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accAtFirst, accAlloca))
	firstPre, err := e.jsonAppend(Value{Ref: accAtFirst, Ty: TypePtr}, ind.itemPrefix(0))
	if err != nil {
		return Value{}, err
	}
	firstAcc, err := e.emitStringConcat(firstPre, elemJSONVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", firstAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(restL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accCur, accAlloca))
	withComma, err := e.jsonAppend(Value{Ref: accCur, Ty: TypePtr}, ind.itemPrefix(1))
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
	if ind.pretty() {
		// Whether to put the closing bracket on its own indented line depends on
		// whether the array had any elements — a runtime fact for a dynamic-length
		// array, so select between the two closes on lenReg == 0.
		isEmpty := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, lenReg))
		closeSel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", closeSel, isEmpty,
			e.internString("]"), e.internString("\n"+ind.pad()+"]")))
		return e.emitStringConcat(Value{Ref: preClose, Ty: TypePtr}, Value{Ref: closeSel, Ty: TypePtr})
	}
	return e.emitStringConcat(Value{Ref: preClose, Ty: TypePtr}, Value{Ref: e.internString("]"), Ty: TypePtr})
}

func (e *Emitter) emitJSONStringify(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: JSON.stringify expects at least 1 argument", pos.Line, pos.Col)
	}

	// Optional replacer (arg 2): V1 supports only a null/undefined replacer.
	// A function or array replacer is a separate, far less common feature
	// (TDD-00077 Track S) — reject it cleanly rather than silently ignore it.
	if len(args) >= 2 {
		if _, isNull := args[1].(*ast.NullLiteral); !isNull {
			return Value{}, fmt.Errorf("%d:%d: JSON.stringify replacer argument is not supported (only null)", pos.Line, pos.Col)
		}
	}

	// Optional space (arg 3): a compile-time literal number (N spaces, capped at
	// 10) or string (used literally, first 10 chars), matching JSON.stringify.
	// An empty unit keeps compact mode, byte-identical to the pre-pretty output.
	ind := jsonIndent{}
	if len(args) >= 3 {
		unit, err := e.jsonSpaceUnit(args[2], pos)
		if err != nil {
			return Value{}, err
		}
		ind.unit = unit
	}

	argTy := e.inferExprType(args[0])

	if argTy.IsArray && argTy.ElemType != nil {
		return e.emitJSONStringifyArray(args[0], pos, ind)
	}

	// A map-backed dynamic object (a computed-key literal or a string
	// index-signature dict, TDD-00012/TDD-00130) has no fixed field table to
	// walk; serializing it needs a map-iteration path not yet built. Reject
	// cleanly rather than emit an empty string — iterate `Object.keys(d)` and
	// build the JSON by hand for now.
	if argTy.IsDynamicObject || argTy.IsMap {
		return Value{}, fmt.Errorf("%d:%d: JSON.stringify of an index-signature / dynamic-key object is not yet supported — build it from Object.keys(...) for now", pos.Line, pos.Col)
	}

	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}

	return e.emitJSONStringifyValue(val, ind)
}

// jsonSpaceUnit resolves JSON.stringify's compile-time `space` argument to one
// indentation unit: a numeric literal becomes that many spaces (clamped to
// [0,10] per the spec), a string literal is used literally (first 10 chars),
// null/undefined or a non-positive count means compact (""). A runtime (non-
// literal) space is rejected in V1 — pretty-print units are resolved at compile
// time so the indent strings can be interned as constants.
func (e *Emitter) jsonSpaceUnit(arg ast.Expression, pos ast.Pos) (string, error) {
	switch a := arg.(type) {
	case *ast.NullLiteral:
		return "", nil
	case *ast.NumberLiteral:
		if a.IsBigInt {
			return "", fmt.Errorf("%d:%d: JSON.stringify space argument must be a number or string, not a bigint", pos.Line, pos.Col)
		}
		n := 0
		if iv, err := strconv.ParseInt(a.Value, 0, 64); err == nil {
			n = int(iv)
		} else if fv, err := strconv.ParseFloat(a.Value, 64); err == nil {
			n = int(fv)
		}
		if n < 0 {
			n = 0
		}
		if n > 10 {
			n = 10
		}
		return strings.Repeat(" ", n), nil
	case *ast.StringLiteral:
		s := a.Value
		if len(s) > 10 {
			s = s[:10]
		}
		return s, nil
	default:
		return "", fmt.Errorf("%d:%d: JSON.stringify space argument must be a literal number or string (a runtime value is not yet supported)", pos.Line, pos.Col)
	}
}

// emitJSONStringifyTuple builds [v0,v1,...] for a tuple value (TDD-00066),
// matching real JSON.stringify, which serializes a tuple as a JSON array.
func (e *Emitter) emitJSONStringifyTuple(val Value, ind jsonIndent) (Value, error) {
	acc := Value{Ref: e.internString("["), Ty: TypePtr}
	structIR := val.Ty.StructIR()
	n := len(val.Ty.Fields)
	for i, field := range val.Ty.Fields {
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, val.Ref, i))
		fieldVal := e.loadScalarOrNullableField(gepReg, field.Ty)
		var err error
		if acc, err = e.jsonAppend(acc, ind.itemPrefix(i)); err != nil {
			return Value{}, err
		}
		jsonVal, err := e.emitJSONStringifyValue(fieldVal, ind.child())
		if err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, jsonVal); err != nil {
			return Value{}, err
		}
	}
	return e.jsonAppend(acc, ind.closeBracket("]", n))
}

// emitJSONStringifyObject builds {"k1":v1,"k2":v2,...} inline by walking the
// known fields of a statically-typed object. Handles nested objects recursively.
func (e *Emitter) emitJSONStringifyObject(val Value, ind jsonIndent) (Value, error) {
	acc := Value{Ref: e.internString("{"), Ty: TypePtr}
	fields := val.Ty.VisibleFields()
	for i, field := range fields {
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

		// Key segment: the item prefix (compact comma, or pretty newline+indent),
		// then `"name"` and the colon separator (":" compact, ": " pretty).
		keyStr := ind.itemPrefix(i) + `"` + field.Name + `"` + ind.colon()
		var err error
		acc, err = e.jsonAppend(acc, keyStr)
		if err != nil {
			return Value{}, err
		}

		// JSON-encode the field value.
		jsonVal, err := e.emitJSONStringifyValue(fieldVal, ind.child())
		if err != nil {
			return Value{}, err
		}
		acc, err = e.emitStringConcat(acc, jsonVal)
		if err != nil {
			return Value{}, err
		}
	}
	return e.jsonAppend(acc, ind.closeBracket("}", len(fields)))
}

// emitJSONStringifyValue returns a ptr string with the JSON encoding of val.
// Handles strings (quoted), numbers, booleans, and nested objects recursively.
func (e *Emitter) emitJSONStringifyValue(val Value, ind jsonIndent) (Value, error) {
	// Must be checked before the generic IsObject branch below — Symbol
	// reuses IsObject's struct representation (see IsSymbol's doc comment,
	// types.go), so without this it would silently serialize as
	// {"description":"..."} instead of erroring like real JS does on a bare
	// Symbol argument. See docs/tdd/TDD-00044.md.
	if val.Ty.IsSymbol {
		return Value{}, fmt.Errorf("JSON.stringify does not support symbol values")
	}
	// Real JS throws "Do not know how to serialize a BigInt" — TDD-00074.
	if val.Ty.IsBigInt {
		return Value{}, fmt.Errorf("JSON.stringify does not support BigInt values (TypeError in JS)")
	}
	// A nullable-scalar field/value (TDD-00064 Stage 3) serializes to its
	// value's JSON when present, the literal `null` when absent — matching real
	// JSON.stringify, which emits null for a null-valued property.
	if isNullableScalar(val.Ty) {
		present, payload := e.nullableScalarAggParts(val)
		payloadJSON, err := e.emitJSONStringifyValue(payload, ind)
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, present, payloadJSON.Ref, e.internString("null")))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	// A user-defined class toJSON() is honored: real JSON.stringify calls
	// toJSON() when present and serializes its *result* instead of the object's
	// own fields (TDD-00077 Track S). Mirrors emitValueToString's toString()
	// override dispatch. The jsonToJSONActive guard prevents a toJSON() that
	// returns its own (or a mutually-referencing) type from recursing forever
	// at compile time (cf. ADR-00221) — a re-entry serializes the object's
	// fields directly instead of re-dispatching toJSON.
	if canon := e.canonicalizeClassTy(val.Ty); canon.IsClass {
		if info, ok := e.classes[canon.ClassName]; ok {
			if _, has := info.MethodSigs["toJSON"]; has && !e.jsonToJSONActive[canon.ClassName] {
				// Keep the class marked active for the *whole* serialization of
				// toJSON's result, not just the call: a result of the same type
				// (e.g. `toJSON() { return this }`) must re-enter with the guard
				// still set so it serializes the object's fields instead of
				// re-dispatching toJSON forever (matches JS, which applies toJSON
				// once then serializes the returned value's own properties).
				e.jsonToJSONActive[canon.ClassName] = true
				res, err := e.emitClassCall(canon, Value{Ref: val.Ref, Ty: canon}, "toJSON", nil, ast.Pos{}, false)
				if err != nil {
					delete(e.jsonToJSONActive, canon.ClassName)
					return Value{}, err
				}
				out, err := e.emitJSONStringifyValue(res, ind)
				delete(e.jsonToJSONActive, canon.ClassName)
				return out, err
			}
		}
	}
	if val.Ty.IsTuple {
		// A tuple serializes as a JSON array, not an object — checked before
		// the generic IsObject branch (a tuple is structurally an object).
		return e.emitJSONStringifyTuple(val, ind)
	}
	if val.Ty.IsObject {
		return e.emitJSONStringifyObject(val, ind)
	}
	if val.Ty.IsArray {
		return e.emitJSONStringifyArrayValue(val, ind)
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
	// TDD-00077 Track P: parse into a validated tree (throwing a catchable
	// SyntaxError on malformed input), project it onto the target type, then
	// release it. This type-directed projection replaced the old lenient strstr
	// extraction, and handles nested objects, array/object-array fields, and
	// top-level `T[]` in the one path (P3).
	tree := e.emitJSONParseTree(val)
	result, err := e.emitJSONProject(tree, targetTy, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_json_free(ptr %s)", tree))
	return result, nil
}
