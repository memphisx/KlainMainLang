// emit_nullderef.go — the TypeError a property access on a run-time
// `undefined`/`null` base throws.
//
// `s.length`, `r.name`, `p.hi()`, `xs[0]`, `p.x = 5` on an absent base are
// `TypeError: Cannot read properties of undefined (reading 'length')` /
// `Cannot set properties of null (setting 'x')` in JS — catchable, and
// `instanceof TypeError`. A statically-typed base is a bare pointer (or a
// { i1, T } optional, or a nullable array's {null,0} aggregate), so an absent
// one used to be dereferenced: a segfault. The guard is a null test in front of
// the access that calls one shared cold, noreturn thrower.
//
// The access sites are spread over the whole call/member/index/assign
// dispatch, and most of them evaluate the base with a plain emitExpr. Rather
// than touch each one, the dispatch entry points *register* the base node
// (guardBase) and emitExpr applies the guard to whatever that node evaluates
// to — once, with no second evaluation of the base.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// derefGuard is the pending guard registered for one base expression node.
// key is set for a bracket access whose key is not a literal: Node names the
// run-time key in the message, so the throw path evaluates and renders it.
type derefGuard struct {
	prop    string
	key     ast.Expression
	setting bool
}

// ensureNullDerefThrow defines @__kml_throw_nullderef(ptr msg): build a
// TypeError around the (compile-time interned) message and throw it. Out of
// line so a guarded access costs one compare-and-branch at the site.
func (e *Emitter) ensureNullDerefThrow() {
	if e.usedNullDerefThrow {
		return
	}
	e.usedNullDerefThrow = true
	e.ensureExceptionHelpers()
	e.ensureMalloc()
	e.ensureMemset()
	sir := errorObjType.StructIR()
	kindIdx, _, _ := errorObjType.FieldIndex("kind")
	msgIdx, _, _ := errorObjType.FieldIndex("message")
	nameIdx, _, _ := errorObjType.FieldIndex("name")
	causeIdx, _, _ := errorObjType.FieldIndex("cause")
	name := e.internString("TypeError")
	var b strings.Builder
	b.WriteString("define void @__kml_throw_nullderef(ptr %msg) cold noreturn {\nentry:\n")
	// malloc+memset rather than the zeroing allocator: -optimize-memory's tests
	// count that allocator's call sites to prove an instance left the heap.
	fmt.Fprintf(&b, "  %%o = call ptr @malloc(i64 %d)\n", errorObjType.StructSize())
	fmt.Fprintf(&b, "  call ptr @memset(ptr %%o, i32 0, i64 %d)\n", errorObjType.StructSize())
	fmt.Fprintf(&b, "  %%k = getelementptr %s, ptr %%o, i32 0, i32 %d\n", sir, kindIdx)
	fmt.Fprintf(&b, "  store i64 %d, ptr %%k, align 8\n", errorTypeIDStored(errorKindIDs["TypeError"]))
	fmt.Fprintf(&b, "  %%m = getelementptr %s, ptr %%o, i32 0, i32 %d\n", sir, msgIdx)
	b.WriteString("  store ptr %msg, ptr %m, align 8\n")
	fmt.Fprintf(&b, "  %%n = getelementptr %s, ptr %%o, i32 0, i32 %d\n", sir, nameIdx)
	fmt.Fprintf(&b, "  store ptr %s, ptr %%n, align 8\n", name)
	fmt.Fprintf(&b, "  %%c = getelementptr %s, ptr %%o, i32 0, i32 %d\n", sir, causeIdx)
	fmt.Fprintf(&b, "  store i64 %d, ptr %%c, align 8\n", nbUndefined)
	b.WriteString("  call void @__kml_throw(ptr %o)\n  unreachable\n}")
	e.emitGlobal(b.String())
}

// derefGuardable reports whether an absent value of type ty is observable as a
// null pointer / cleared presence flag that a member access would trip over.
// A declared-nullable type always is: null *is* its absent state. A
// non-nullable one is guarded only when it is a struct pointer (an object or
// class instance — an unassigned field, say). Every other bare `ptr` is left
// alone: the type flags cannot tell a string from a runtime handle, and some
// handles are legitimately null (a TextDecoder holds no storage at all).
func derefGuardable(ty Type) bool {
	if ty.IsDynamic || ty.IsNull || ty.IsFunc {
		return false
	}
	if ty.Nullable {
		return ty.IR != "void" && ty.IR != ""
	}
	return ty.IR == "ptr" && ty.IsObject && !ty.IsArray && !ty.IsFlatArray && !ty.IsTuple
}

// absentWord is how JS names the absent base in the message: a `T | null`
// reads "null"; everything else (`T | undefined`, an optional parameter, a
// missing element) is "undefined" — the same static discriminator the
// string/typeof/JSON coercions use (TDD-00187).
func absentWord(ty Type) string {
	if ty.Nullable && !ty.IsUndefined {
		return "null"
	}
	return "undefined"
}

// emitNullDerefGuard emits the absent-base test for v in front of a read (or
// write, when g.setting) of the property g names.
func (e *Emitter) emitNullDerefGuard(v Value, g derefGuard) error {
	if e.blockDone || !derefGuardable(v.Ty) || strings.HasPrefix(v.Ref, "@") {
		return nil
	}
	absent := e.freshReg()
	switch {
	case isNullableScalar(v.Ty):
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue %s %s, 0", present, nullableScalarStorageIR(v.Ty), v.Ref))
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", absent, present))
	case v.Ty.IsArray || v.Ty.IsFlatArray:
		// An absent array is a null *header*. The {null,0} aggregate alone cannot
		// say so — a present, empty array has the same data pointer — so a value
		// with no header to consult is left to read as the empty array it may be.
		if v.ArrayHeader == "" {
			return nil
		}
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", absent, v.ArrayHeader))
	case v.Ty.IR == "ptr":
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", absent, v.Ref))
	default:
		return nil
	}
	verb, gerund := "read", "reading"
	if g.setting {
		verb, gerund = "set", "setting"
	}
	head := fmt.Sprintf("Cannot %s properties of %s (%s '", verb, absentWord(v.Ty), gerund)
	e.ensureNullDerefThrow()
	throwL := e.freshLabel("nullderef.throw")
	okL := e.freshLabel("nullderef.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, throwL, okL))
	e.emitLabel(throwL)
	msg := e.internString(head + g.prop + "')")
	if g.key != nil {
		// JS evaluates the key before the access throws, and names it.
		keyRef, err := e.dynAnyKeyRef(g.key, g.key.GetPos())
		if err != nil {
			return err
		}
		withKey, err := e.emitStringConcat(Value{Ref: e.internString(head), Ty: TypePtr}, Value{Ref: keyRef, Ty: TypePtr})
		if err != nil {
			return err
		}
		full, err := e.emitStringConcat(withKey, Value{Ref: e.internString("')"), Ty: TypePtr})
		if err != nil {
			return err
		}
		msg = full.Ref
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_throw_nullderef(ptr %s)", msg))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	return nil
}

// guardBase registers base as the receiver of a `.prop` access (guardIndexBase:
// a `[key]` one) so emitExpr guards its value. It returns the undo the caller
// defers: a dispatch path that never evaluates the base through emitExpr (a
// compile-time namespace) must not leave the entry behind for an unrelated
// later evaluation of the same node.
//
// A named binding is guarded on the spot instead: many dispatch paths read a
// symbol straight from its slot (`.length`, `.size`, array indexing) and never
// reach emitExpr for it, and a second load of a local is free.
func (e *Emitter) guardBase(base ast.Expression, prop string, setting bool) (func(), error) {
	return e.registerDerefGuard(base, derefGuard{prop: prop, setting: setting})
}

func (e *Emitter) guardIndexBase(base, index ast.Expression, setting bool) (func(), error) {
	g := derefGuard{setting: setting}
	if name, ok := indexPropName(index); ok {
		g.prop = name
	} else {
		g.key = index
	}
	return e.registerDerefGuard(base, g)
}

func (e *Emitter) registerDerefGuard(base ast.Expression, g derefGuard) (func(), error) {
	noop := func() {}
	if base == nil {
		return noop, nil
	}
	// `x!.p` asserts away the static nullability, not the run-time value: guard
	// the operand, whose type still says whether absent means null or undefined.
	for {
		nn, ok := base.(*ast.NonNullExpression)
		if !ok {
			break
		}
		base = nn.Arg
	}
	if id, ok := base.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && derefGuardable(sym.Ty) {
			// A nullable-scalar local reads as its bare payload; keep the
			// presence bit the guard tests.
			v, err := e.emitPreserveNullableOperand(id)
			if err != nil {
				return noop, nil // the access itself reports the problem
			}
			return noop, e.emitNullDerefGuard(v, g)
		}
		return noop, nil
	}
	if e.pendingDeref == nil {
		e.pendingDeref = map[ast.Expression]derefGuard{}
	}
	if _, dup := e.pendingDeref[base]; dup {
		return noop, nil
	}
	e.pendingDeref[base] = g
	return func() { delete(e.pendingDeref, base) }, nil
}

// applyPendingDeref is emitExpr's hook: guard v if expr was registered.
func (e *Emitter) applyPendingDeref(expr ast.Expression, v Value) error {
	g, ok := e.pendingDeref[expr]
	if !ok {
		return nil
	}
	delete(e.pendingDeref, expr)
	return e.emitNullDerefGuard(v, g)
}

// indexPropName renders a bracket key for the message the way Node does: the
// literal's text for a constant key (`xs[0]` → '0', `o["k"]` → 'k').
func indexPropName(index ast.Expression) (string, bool) {
	switch k := index.(type) {
	case *ast.NumberLiteral:
		return k.Value, k.Value != "" && !k.IsBigInt
	case *ast.StringLiteral:
		return k.Value, true
	}
	return "", false
}
