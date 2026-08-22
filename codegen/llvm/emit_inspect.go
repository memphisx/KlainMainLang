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
	if val.Ty.IsClass && val.Ty.ClassName != "" {
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
	elemStr, err := e.emitInspectField(e.loadArrayElem(inGep, elemTy), depth+1)
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
	return Value{Ref: res, Ty: TypePtr}, nil
}

// emitInspectField formats a value as it appears *inside* an inspected object —
// strings single-quoted, bigints with the `n` suffix, nested objects recursed —
// distinct from the top-level bare formatting emitValueToString produces.
func (e *Emitter) emitInspectField(v Value, depth int) (Value, error) {
	switch {
	case v.Ty.IsBigInt:
		return e.emitBigIntToString(v, true) // `10n`, like Node inspect
	case isInspectableObject(v.Ty):
		if depth > maxInspectDepth {
			return Value{Ref: e.internString("[Object]"), Ty: TypePtr}, nil
		}
		return e.emitInspectObject(v, depth)
	case v.Ty.IsObject:
		// A special object type (URL/Headers/…) as a field — a V1 placeholder
		// rather than exposing its internal struct.
		return Value{Ref: e.internString("[Object]"), Ty: TypePtr}, nil
	case v.Ty.IsArray:
		if depth > maxInspectDepth {
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
