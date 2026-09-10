package llvm

// emit_toprimitive.go — the ToPrimitive abstract operation for objects used in a
// primitive context (TDD-00201). Stage 1: the "number" hint over a statically-
// typed object literal, hooked into coerce. Because objects are fixed-shape, the
// ladder is ordinary typed method calls — Symbol.toPrimitive (@@toPrimitive) →
// valueOf → toString — resolved at compile time from the object's Fields, with
// no dynamic-property-bag dependency. Faithful in both -compat lanes.

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitObjectToPrimitive runs the ToPrimitive ladder on a statically-typed object
// literal value `v` for the given hint ("number", "string", or "default") and
// returns the RAW primitive value a method produced — the caller applies the
// surrounding ToNumber/ToString. The spec order is Symbol.toPrimitive first,
// then valueOf/toString in hint-dependent order; a candidate is taken only when
// it exists as a callable method field AND its declared return type is usable
// for the hint (numeric for "number", so a string-returning method in numeric
// context does not silently misuse a string pointer — that edge, needing
// ToNumber(string), is deferred). Returns (result, true, nil) on success,
// (Value{}, false, nil) when no applicable method field exists (caller keeps its
// existing behavior), or an error from the method call.
func (e *Emitter) emitObjectToPrimitive(v Value, hint string) (Value, bool, error) {
	var order []string
	if hint == "string" {
		// valueOf is intentionally omitted: in real JS the inherited
		// Object.prototype.toString always intercepts the string hint before
		// valueOf is reached, so `String({valueOf(){return 5}})` is
		// "[object Object]", not "5". A plain object with no own toString falls
		// through to the caller's Object.prototype.toString default.
		order = []string{"@@toPrimitive", "toString"}
	} else { // "number" and "default"
		order = []string{"@@toPrimitive", "valueOf", "toString"}
	}
	for _, name := range order {
		idx, fieldTy, ok := v.Ty.FieldIndex(name)
		if !ok || !fieldTy.IsFunc {
			continue
		}
		retTy := TypeF64
		if fieldTy.FuncRetType != nil {
			retTy = *fieldTy.FuncRetType
		}
		// The result must be usable for the hint to end the ladder; otherwise try
		// the next candidate (mirrors the spec's "if result is an Object,
		// continue"). @@toPrimitive is trusted to return the right kind.
		if name != "@@toPrimitive" && !resultUsableForHint(retTy, hint) {
			continue
		}
		// Load the method closure from the object struct, then call it. A
		// @@toPrimitive method receives the hint string; valueOf/toString take
		// no argument.
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, v.Ty.StructIR(), v.Ref, idx))
		closure := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", closure, StructFieldIR(fieldTy), gep, fieldTy.Align()))
		var args []ast.Expression
		if name == "@@toPrimitive" {
			args = []ast.Expression{ast.NewStringLiteral(hint, ast.Pos{})}
		}
		res, err := e.emitClosureCallByPtr(closure, fieldTy, args, ast.Pos{})
		if err != nil {
			return Value{}, false, err
		}
		return res, true, nil
	}
	return Value{}, false, nil
}

// coerceStringArg applies ToString to a value flowing into a string-typed
// position (e.g. a string method's search argument): an object is run through
// the string-hint ToPrimitive ladder / Object.prototype.toString via
// emitValueToString, matching real JS's ToString(argument). A value that is
// already a string is returned unchanged. Only objects are converted here (the
// TDD-00201 scope); a non-string, non-object argument keeps its current handling.
func (e *Emitter) coerceStringArg(v Value) (Value, error) {
	if objectMayToPrimitive(v.Ty) {
		return e.emitValueToString(v)
	}
	return v, nil
}

// disjointEqConstResult names the constant result of comparing an object with a
// disjoint scalar (`false` for ==/===, `true` for !=/!==) — used in the strict
// rejection message so the developer sees what -compat=js would evaluate it to.
func disjointEqConstResult(op string) string {
	if op == "!=" || op == "!==" {
		return "true"
	}
	return "false"
}

// isLooseEqPrimitive reports whether a type is a number/string/bigint — the
// operand kinds that make `obj == x` trigger ToPrimitive on the object. null/
// undefined (loosely equal only each other), booleans, and objects are excluded:
// `obj == null` is false and `obj == obj` is reference equality, neither coercing.
func isLooseEqPrimitive(t Type) bool {
	if t.IsNull || t.IsUndefined || t.IsObject || t.IsArray || t.IsFunc || t.IsDynamic {
		return false
	}
	return t.Float || t.IsBigInt || isPlainStringType(t) ||
		t.IR == "i8" || t.IR == "i16" || t.IR == "i32" || t.IR == "i64"
}

// resultUsableForHint reports whether a ToPrimitive candidate's declared return
// type can serve the given hint without a further object-valued ladder step. For
// "number" the result must be numerically coercible (a string result would need
// ToNumber(string), deferred); "string"/"default" accept any primitive.
func resultUsableForHint(retTy Type, hint string) bool {
	if !isPrimitiveResultTy(retTy) {
		return false
	}
	if hint == "number" {
		return retTy.Float || retTy.IsBigInt || retTy.IR == "i1" ||
			retTy.IR == "i8" || retTy.IR == "i16" || retTy.IR == "i32" || retTy.IR == "i64"
	}
	return true
}

// objectMayToPrimitive reports whether a value's type is a plain statically-
// typed object that the ToPrimitive ladder should be tried on. The real guard is
// the FieldIndex lookup inside emitObjectToPrimitive (Date/Map/Set/Symbol carry
// their methods as builtins, not struct fields, so they never match); this just
// screens out arrays and already-dynamic values cheaply.
func objectMayToPrimitive(t Type) bool {
	return t.IsObject && !t.IsArray && !t.IsDynamic
}

// isPrimitiveResultTy reports whether a method's declared return type is a JS
// primitive (so the ToPrimitive ladder can stop on it). Deliberately excludes
// composite/boxed types: Type.IsInteger() is true for aggregate boxes too, so
// the machine-integer IRs are listed explicitly.
func isPrimitiveResultTy(t Type) bool {
	if t.IsDynamic || t.IsObject || t.IsArray || t.IsMap || t.IsSet || t.IsFunc {
		return false
	}
	if t.Float || t.IsBigInt || isPlainStringType(t) {
		return true
	}
	switch t.IR {
	case "i1", "i8", "i16", "i32", "i64":
		return true
	}
	return false
}
