package llvm

// emit_toprimitive_dyn.go — runtime ToPrimitive over a NaN-boxed dynamic object
// (TDD-00201 Stage 4, `-compat=js`). Under compat=js a plain object literal is a
// D1 dynamic bag (tag 10), so the static ladder in emit_toprimitive.go cannot see
// its methods — they are looked up and invoked at runtime here instead. This is
// what makes the faithful lane faithful: `{valueOf(){return 5}} * 2` is 10 (not
// NaN) and `String({toString(){return "hi"}})` is "hi" (not "[object Object]").
//
// @__kml_toprimitive(box, strHint) returns the RAW primitive box a method
// produced: Symbol.toPrimitive (@@toPrimitive) first, then valueOf/toString in
// hint order (valueOf→toString for number/default, toString→valueOf for string);
// the first callable whose result is a primitive (nb_tag < 6) wins, else the
// literal "[object Object]". A non-object box passes through unchanged, so the
// helper is safe to apply to every dynamic operand.

import (
	_ "embed"
	"fmt"
)

//go:embed boxsrc/toprim.c
var toPrimitiveSource string

//go:embed boxsrc/loose_eq.c
var looseEqSource string

// ToPrimitiveSource is the runtime ToPrimitive ladder's C source
// (boxsrc/toprim.c).
func ToPrimitiveSource() string { return toPrimitiveSource }

// LooseEqSource is the Abstract Equality C source (boxsrc/loose_eq.c),
// behind the boxed-bigint constants.
func LooseEqSource() string { return bigintBoxDefines() + looseEqSource }

// UsesToPrimitiveC reports whether the program links the ToPrimitive ladder.
func (e *Emitter) UsesToPrimitiveC() bool { return e.usedAnyToPrimitive }

// UsesLooseEqC reports whether the program links the Abstract Equality
// comparison.
func (e *Emitter) UsesLooseEqC() bool { return e.usedAnyLooseEq }

func (e *Emitter) ensureAnyToPrimitive() {
	if e.usedAnyToPrimitive {
		return
	}
	e.usedAnyToPrimitive = true
	e.ensureNanBox()
	e.ensureDynObj()
	e.ensureFnMeta()         // __kml_fn_props_dyn: a function's own properties
	e.ensureDynJSONC()       // an array's toString: the array joins
	e.ensureNullDerefThrow() // "Cannot convert object to primitive value"
	// The ladder, its invoke shims and key lookup are boxsrc/toprim.c.
	e.emitGlobal("declare i64 @__kml_toprimitive(i64, i8)")
}

// ensureAnyLooseEq declares @__kml_any_loose_eq (boxsrc/loose_eq.c): JS's
// Abstract Equality (`==`) over two NaN-boxed values (TDD-00201 Stage 4). Distinct from @__kml_any_eq
// (Strict Equality, `===`): loose `==` coerces. Algorithm: both heap objects →
// reference compare (`{}=={}` is false); exactly one object → ToPrimitive it;
// null/undefined loosely equal only each other; same primitive type → strict
// compare; mixed primitives (number/string/boolean) → ToNumber both and compare
// (a NaN result makes the comparison false, as in JS).
func (e *Emitter) ensureAnyLooseEq() {
	if e.usedAnyLooseEq {
		return
	}
	e.usedAnyLooseEq = true
	e.ensureNanBox()
	e.ensureAnyEq()
	e.ensureAnyOps() // __kml_any_tonum
	e.ensureAnyToPrimitive()
	e.ensureBoxedBigIntHooks()
	e.emitGlobal("declare zeroext i1 @__kml_any_loose_eq(i64, i64)") // boxsrc/loose_eq.c
}

// emitAnyLooseEquals implements `==`/`!=` when either operand is dynamic
// (`-compat=js`): the JS Abstract Equality Comparison, with object coercion.
func (e *Emitter) emitAnyLooseEquals(a, b Value, negate bool) (Value, error) {
	boxedA, err := e.emitBoxValue(a)
	if err != nil {
		return Value{}, err
	}
	boxedB, err := e.emitBoxValue(b)
	if err != nil {
		return Value{}, err
	}
	e.ensureAnyLooseEq()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_loose_eq(i64 %s, i64 %s)", r, boxedA.Ref, boxedB.Ref))
	if negate {
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", neg, r))
		return Value{Ref: neg, Ty: TypeBool}, nil
	}
	return Value{Ref: r, Ty: TypeBool}, nil
}

// emitAnyToPrimitive runs the runtime ToPrimitive ladder on a boxed value,
// returning a boxed primitive (a non-object box passes through). strHint selects
// the string-hint method order.
func (e *Emitter) emitAnyToPrimitive(boxRef string, strHint bool) string {
	if strHint {
		return e.emitAnyToPrimitiveHint(boxRef, hintString)
	}
	return e.emitAnyToPrimitiveHint(boxRef, hintNumber)
}

// ToPrimitive's hints: the i8 @__kml_toprimitive takes.
const (
	hintNumber  = 0
	hintString  = 1
	hintDefault = 2
)

// emitAnyToPrimitiveHint is emitAnyToPrimitive with an explicit hint
// ("default" for `+` and `==`, where a Date converts to its string).
func (e *Emitter) emitAnyToPrimitiveHint(boxRef string, hint int) string {
	e.ensureAnyToPrimitive()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_toprimitive(i64 %s, i8 %d)", r, boxRef, hint))
	return r
}

// emitHostToPrimFinalize defines @__kml_host_toprim(box, hint) as a stub
// when the program boxed no host value; otherwise it is boxsrc/hostbox.c: a
// Date host box's primitive (its time value for the number hint, its
// toString otherwise), from its registered host row; any other box as
// itself.
func (e *Emitter) emitHostToPrimFinalize() {
	if !e.usedAnyToPrimitive || e.usedHostBox {
		return
	}
	e.functions.WriteString("\ndefine i64 @__kml_host_toprim(i64 %v, i8 %hint) {\nentry:\n  ret i64 %v\n}\n")
}

// hostToPrimFn generates h's primitive conversion (a Date's), or "" for a
// host class without one.
func (e *Emitter) hostToPrimFn(h hostLayout) string {
	if h.class != "Date" {
		return ""
	}
	restore := e.beginDetachedFunc()
	t := e.emitHostCellLoad("%cell", h.ty)
	isNum := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %%hint, %d", isNum, hintNumber))
	numL, strL := e.freshLabel("date.prim.num"), e.freshLabel("date.prim.str")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNum, numL, strL))
	e.emitLabel(numL)
	d := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", d, t))
	e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbEncodeDouble(d)))
	e.emitLabel(strL)
	s, err := e.emitDateOrInvalid(Value{Ref: t, Ty: h.ty}, e.emitDateToString)
	if err != nil {
		e.emitTerminator("ret i64 %v")
	} else {
		bx, _ := e.emitBoxValue(s)
		e.emitTerminator(fmt.Sprintf("ret i64 %s", bx.Ref))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	fn := fmt.Sprintf("@__kml_host_toprim_%d", h.id&kmlHdrIDMask)
	fmt.Fprintf(&e.functions, "\ndefine internal i64 %s(ptr %%cell, i64 %%v, i8 %%hint) {\nentry:\n%s}\n", fn, body)
	return fn
}
