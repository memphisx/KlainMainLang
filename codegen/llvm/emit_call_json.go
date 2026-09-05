package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
	"strings"
)

// callAssertedTargetTy resolves the `as T` a call carries
// (CallExpression.AssertedType, attached by the parser on JSON.parse /
// `.json()` shapes only) into the projection target type the call should
// deserialize into — the assertion's real static effect in TypeScript
// (narrowing JSON.parse's `any` to T). Honored under `-compat=strict` only:
// `-compat=js`'s whole identity is the dynamic-tree path, and its
// declarations are any-backed anyway. A target the projection cannot
// deserialize into (class, union/any, function) is declined here — the call
// keeps its default dynamic-tree behavior rather than erroring, matching
// erased-assertion semantics for the shapes the carve-out doesn't cover.
func (e *Emitter) callAssertedTargetTy(ce *ast.CallExpression) (Type, bool) {
	if ce.AssertedType == nil || e.compatJS() {
		return Type{}, false
	}
	ty := e.resolveType(ce.AssertedType)
	if ty.IsDynamic || ty.IsClass || ty.IsFunc || ty.IR == "void" {
		return Type{}, false
	}
	return ty, true
}

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
// acc is always an owned accumulator (see jsonSeed) and is freed by the
// concat; the interned fragment is not.
func (e *Emitter) jsonAppend(acc Value, s string) (Value, error) {
	if s == "" {
		return acc, nil
	}
	return e.jsonConcatFree(acc, Value{Ref: e.internString(s), Ty: TypePtr}, true, false)
}

// jsonSeed returns a fresh owned heap copy of a constant seed string ("[",
// "{", …) so every stringify accumulator is uniformly owned — each append
// (jsonConcatFree) can then free the previous accumulator unconditionally.
func (e *Emitter) jsonSeed(s string) string {
	e.ensureStrHeaderRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", r, e.internString(s)))
	return r
}

// jsonConcatFree concatenates via __kml_json_concat2, freeing the operands
// the caller owns. Fragment ownership is compile-time-known per call site.
func (e *Emitter) jsonConcatFree(a, b Value, freeA, freeB bool) (Value, error) {
	e.ensureJSONConcat2()
	fa, fb := "false", "false"
	if freeA {
		fa = "true"
	}
	if freeB {
		fb = "true"
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_concat2(ptr %s, ptr %s, i1 %s, i1 %s)", r, a.Ref, b.Ref, fa, fb))
	return Value{Ref: r, Ty: TypePtr}, nil
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
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.jsonSeed("["), accAlloca))

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
	firstAcc, err := e.jsonConcatFree(firstPre, elemJSONVal, true, true)
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
	newAcc, err := e.jsonConcatFree(withComma, elemJSONVal, true, true)
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
		return e.jsonConcatFree(Value{Ref: preClose, Ty: TypePtr}, Value{Ref: closeSel, Ty: TypePtr}, true, false)
	}
	return e.jsonConcatFree(Value{Ref: preClose, Ty: TypePtr}, Value{Ref: e.internString("]"), Ty: TypePtr}, true, false)
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

	// A bare any/unknown value serializes through the dynamic walker
	// (TDD-00155 Stage 2): tag-10 bags and tag-11 dynamic arrays recurse,
	// scalars render per JSON, undefined/function values are skipped (object)
	// or null (array), cycles throw TypeError. Returns `any` — a string box,
	// or undefined for a top-level undefined/function, matching JS.
	if isUnconstrainedDynamic(argTy) {
		val, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitJSONStringifyDynamic(val, ind, pos)
	}

	if argTy.IsArray && argTy.ElemType != nil {
		return e.emitJSONStringifyArray(args[0], pos, ind)
	}

	// A map-backed dynamic object (a computed-key literal or a string
	// index-signature dict, TDD-00012/TDD-00130) and a string-keyed Map both
	// serialize by iterating the runtime key list (ADR-00482, clearing the
	// TDD-00130 deferral). A number-keyed Map keeps the rejection (JSON
	// object keys are strings; real JS stringifies a Map to "{}" — matching
	// that would silently drop data).
	if argTy.IsDynamicObject || argTy.IsMap {
		if argTy.MapKey != nil && !isStringTy(*argTy.MapKey) {
			return Value{}, fmt.Errorf("%d:%d: JSON.stringify of a number-keyed Map is not supported — JSON object keys are strings", pos.Line, pos.Col)
		}
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitJSONStringifyMapDict(v, ind)
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
	acc := Value{Ref: e.jsonSeed("["), Ty: TypePtr}
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
		if acc, err = e.jsonConcatFree(acc, jsonVal, true, true); err != nil {
			return Value{}, err
		}
	}
	return e.jsonAppend(acc, ind.closeBracket("]", n))
}

// emitJSONStringifyObject builds {"k1":v1,"k2":v2,...} inline by walking the
// known fields of a statically-typed object. Handles nested objects recursively.
func (e *Emitter) emitJSONStringifyObject(val Value, ind jsonIndent) (Value, error) {
	// A Promise.allSettled() settlement object is serialized specially: real JS
	// gives a fulfilled entry exactly `{status, value}` and a rejected entry
	// exactly `{status, reason}` — never both keys. The static struct carries
	// both slots (this compiler has no optional fields), so the applicable key is
	// chosen at runtime from the `status` discriminant rather than emitting both.
	if isSettlementType(val.Ty) {
		return e.emitJSONStringifySettlement(val, ind)
	}
	acc := Value{Ref: e.jsonSeed("{"), Ty: TypePtr}
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
		acc, err = e.jsonConcatFree(acc, jsonVal, true, true)
		if err != nil {
			return Value{}, err
		}
	}
	return e.jsonAppend(acc, ind.closeBracket("}", len(fields)))
}

// isSettlementType reports whether ty is a Promise.allSettled() settlement
// object — an object whose visible fields are exactly status, value, reason (in
// that order), the shape SettlementType builds. Detected structurally so the
// JSON path needs no extra Type flag.
func isSettlementType(ty Type) bool {
	if !ty.IsObject || ty.IsClass {
		return false
	}
	vf := ty.VisibleFields()
	if len(vf) != 3 {
		return false
	}
	return vf[0].Name == "status" && vf[1].Name == "value" && vf[2].Name == "reason"
}

// emitJSONStringifySettlement serializes one Promise.allSettled() settlement
// object, emitting only the key that applies to its runtime status: `value` for
// a fulfilled entry, `reason` for a rejected one (never both), matching real JS.
// The `status` string pointer is compared against the interned "fulfilled"
// constant (internString dedups, so a fulfilled entry's slot holds that exact
// pointer) to pick the branch without a string compare.
func (e *Emitter) emitJSONStringifySettlement(val Value, ind jsonIndent) (Value, error) {
	structIR := val.Ty.StructIR()
	acc := Value{Ref: e.jsonSeed("{"), Ty: TypePtr}

	// status key + quoted value (member 0).
	var err error
	if acc, err = e.jsonAppend(acc, ind.itemPrefix(0)+`"status"`+ind.colon()); err != nil {
		return Value{}, err
	}
	sIdx, _, _ := val.Ty.FieldIndex("status")
	sGep := e.freshReg()
	statusPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", sGep, structIR, val.Ref, sIdx))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", statusPtr, sGep))
	statusJSON, err := e.emitJSONStringifyValue(Value{Ref: statusPtr, Ty: TypePtr}, ind.child())
	if err != nil {
		return Value{}, err
	}
	if acc, err = e.jsonConcatFree(acc, statusJSON, true, true); err != nil {
		return Value{}, err
	}

	// Runtime discriminant: fulfilled entries carry the interned "fulfilled" ptr.
	isFul := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", isFul, statusPtr, e.internString("fulfilled")))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	fulL := e.freshLabel("json.settle.ful")
	rejL := e.freshLabel("json.settle.rej")
	mergeL := e.freshLabel("json.settle.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFul, fulL, rejL))

	// Fulfilled: append "value": <value> only.
	e.emitLabel(fulL)
	accF, err := e.jsonAppend(acc, ind.itemPrefix(1)+`"value"`+ind.colon())
	if err != nil {
		return Value{}, err
	}
	vIdx, vTy, _ := val.Ty.FieldIndex("value")
	vGep := e.freshReg()
	vReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, structIR, val.Ref, vIdx))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", vReg, StructFieldIR(vTy), vGep, vTy.Align()))
	vJSON, err := e.emitJSONStringifyValue(Value{Ref: vReg, Ty: vTy}, ind.child())
	if err != nil {
		return Value{}, err
	}
	if accF, err = e.jsonConcatFree(accF, vJSON, true, true); err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", accF.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// Rejected: append "reason": <reason> only. The reason slot holds the raw
	// rejection value's string form (emit_promise.go), so serialize it as a
	// string rather than as the errorObjType the slot is nominally typed as.
	e.emitLabel(rejL)
	accR, err := e.jsonAppend(acc, ind.itemPrefix(1)+`"reason"`+ind.colon())
	if err != nil {
		return Value{}, err
	}
	rIdx, _, _ := val.Ty.FieldIndex("reason")
	rGep := e.freshReg()
	rReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", rGep, structIR, val.Ref, rIdx))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rReg, rGep))
	rJSON, err := e.emitJSONStringifyValue(Value{Ref: rReg, Ty: TypePtr}, ind.child())
	if err != nil {
		return Value{}, err
	}
	if accR, err = e.jsonConcatFree(accR, rJSON, true, true); err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", accR.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
	return e.jsonAppend(Value{Ref: out, Ty: TypePtr}, ind.closeBracket("}", 2))
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
		// Fragments are uniformly owned (the accumulator concats free them),
		// so the absent branch materializes a heap "null" instead of the
		// interned literal — branchy rather than a select, so the copy only
		// allocates when actually taken. The present-path payloadJSON is
		// computed unconditionally either way (as before).
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		pL := e.freshLabel("json.nsc.present")
		aL := e.freshLabel("json.nsc.absent")
		mL := e.freshLabel("json.nsc.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, pL, aL))
		e.emitLabel(pL)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", payloadJSON.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mL))
		e.emitLabel(aL)
		e.emitInstr(fmt.Sprintf("call void @__kml_str_free(ptr %s)", payloadJSON.Ref))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.jsonSeed("null"), slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mL))
		e.emitLabel(mL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
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
		// A null object pointer (an absent/null object-typed field — e.g. an
		// allSettled settlement's unset `reason`/`value` slot, ADR-00683) must
		// serialize as JSON `null` rather than dereferencing null in the field
		// walk (which segfaulted).
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		isN := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isN, val.Ref))
		nL := e.freshLabel("json.objnull")
		nnL := e.freshLabel("json.objnn")
		mL := e.freshLabel("json.objmerge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isN, nL, nnL))
		e.emitLabel(nL)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.jsonSeed("null"), slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mL))
		e.emitLabel(nnL)
		ov, err := e.emitJSONStringifyObject(val, ind)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ov.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mL))
		e.emitLabel(mL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
		return Value{Ref: out, Ty: TypePtr}, nil
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
		// Uniform fragment ownership: hand back a heap copy so the
		// accumulator concat can free every fragment unconditionally.
		e.ensureStrHeaderRuntime()
		r2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", r2, r))
		return Value{Ref: r2, Ty: TypePtr}, nil
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
			// JS serializes a non-finite number (NaN / ±Infinity) as `null`
			// (ADR-00685); a finite float formats via emitValueToString (%g).
			// Finiteness test: `x - x == 0` is true only for a finite x
			// (Inf-Inf and NaN-NaN are both NaN, so != 0).
			ir := val.Ty.IR
			diff := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fsub %s %s, %s", diff, ir, val.Ref, val.Ref))
			fin := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fcmp oeq %s %s, 0.0", fin, ir, diff))
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
			finL := e.freshLabel("json.fin")
			nfL := e.freshLabel("json.nonfin")
			mL := e.freshLabel("json.finmerge")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fin, finL, nfL))
			e.emitLabel(finL)
			sv, err := e.emitValueToString(val)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sv.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mL))
			e.emitLabel(nfL)
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.jsonSeed("null"), slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mL))
			e.emitLabel(mL)
			out := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
			return Value{Ref: out, Ty: TypePtr}, nil
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
	// A dynamic argument goes through ToString first, matching JS's own
	// coercion (a boxed string round-trips unchanged; JSON.parse(undefined)
	// then throws the SyntaxError the parser raises for "undefined").
	if val.Ty.IsDynamic {
		s, err := e.emitDynamicToString(val)
		if err != nil {
			return Value{}, err
		}
		val = s
	}
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


// emitJSONStringifyMapDict serializes a map-backed dynamic object / string-
// keyed Map to a JSON object by iterating its runtime key list (ADR-00482):
// the same accumulator-loop shape emitJSONStringifyArrayData uses, with each
// entry rendered as `"key":<value>` — the key through the escaping
// __kml_json_str_str, the value re-read via __kml_map_str_get and delegated
// to emitJSONStringifyValue under the map's declared value type.
func (e *Emitter) emitJSONStringifyMapDict(mapVal Value, ind jsonIndent) (Value, error) {
	e.ensureJSONStringifyStr()
	e.ensureMapStrHelpers()
	valTy := TypePtr
	if mapVal.Ty.MapVal != nil {
		valTy = *mapVal.Ty.MapVal
	}
	keys := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", keys, mapVal.Ref))
	kPtr, kLen := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", kPtr, keys))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", kLen, keys))

	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.jsonSeed("{"), accAlloca))
	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))

	condL := e.freshLabel("jsonmap.cond")
	bodyL := e.freshLabel("jsonmap.body")
	doneL := e.freshLabel("jsonmap.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i0, c := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i0, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", c, i0, kLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, bodyL, doneL))

	e.emitLabel(bodyL)
	i1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i1, idxPtr))
	kg, key := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kg, kPtr, i1))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", key, kg))
	acc0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", acc0, accAlloca))
	// Separator: "," for every entry after the first.
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, i1))
	sep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sep, isFirst, e.internString(""), e.internString(",")))
	acc1, err := e.jsonConcatFree(Value{Ref: acc0, Ty: TypePtr}, Value{Ref: sep, Ty: TypePtr}, true, false)
	if err != nil {
		return Value{}, err
	}
	keyJSON := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_str_str(ptr %s)", keyJSON, key))
	acc2, err := e.jsonConcatFree(acc1, Value{Ref: keyJSON, Ty: TypePtr}, true, true)
	if err != nil {
		return Value{}, err
	}
	acc3, err := e.jsonConcatFree(acc2, Value{Ref: e.internString(":"), Ty: TypePtr}, true, false)
	if err != nil {
		return Value{}, err
	}
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, mapVal.Ref, key))
	var vVal Value
	switch {
	case valTy.IR == "ptr":
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, raw))
		vVal = Value{Ref: p, Ty: valTy}
	case valTy.IR == "double":
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", d, raw))
		vVal = Value{Ref: d, Ty: valTy}
	case valTy.IR == "i1":
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i1", b, raw))
		vVal = Value{Ref: b, Ty: valTy}
	default:
		vVal = Value{Ref: raw, Ty: valTy}
	}
	vJSON, err := e.emitJSONStringifyValue(vVal, ind)
	if err != nil {
		return Value{}, err
	}
	acc4, err := e.jsonConcatFree(acc3, vJSON, true, true)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", acc4.Ref, accAlloca))
	i2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i2, i1))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i2, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	preClose := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", preClose, accAlloca))
	return e.jsonConcatFree(Value{Ref: preClose, Ty: TypePtr}, Value{Ref: e.internString("}"), Ty: TypePtr}, true, false)
}
