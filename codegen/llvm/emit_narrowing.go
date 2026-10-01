// emit_narrowing.go — flow-based narrowing of union types (TDD-00114).
//
// Generalizes the nullable-scalar branch-narrowing seam (emit_nullable_scalar.go,
// TDD-00064 Stage 2) from a NarrowedNonNull bool to a NarrowedTo target type: an
// `if` guard over a union-typed local (`typeof x === "string"`, truthiness) is
// recognized, and in the branch it proves, the binding is shadowed into that
// branch's scope carrying the concrete member type. Every use site (emitIdent)
// then unboxes the union's { i8, i64 } value to that concrete type, so the value
// is usable as a real string/number/boolean. popScope discards the shadow.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// unionLocal returns the effective union type of a union-typed local — the
// already-narrowed type when a prior guard narrowed it to a smaller (still
// multi-member) union, else its declared type. This makes nested guards compose:
// `else if` inside a `typeof` else refines the *remaining* members, not the
// original set. ok=false when name isn't a union local (or has been narrowed to
// a single concrete member, which no further typeof guard refines).
func (e *Emitter) unionLocal(name string) (Type, bool) {
	sym, found := e.lookup(name)
	if !found {
		return Type{}, false
	}
	u := sym.Ty
	if sym.NarrowedTo != nil {
		u = *sym.NarrowedTo
	}
	if !u.IsDynamic || len(u.UnionMembers) == 0 {
		return Type{}, false
	}
	return u, true
}

// unionMemberForTypeof returns the union member matching a `typeof` result
// string ("string"/"number"/"boolean"), or ok=false if the union has no such
// member.
func unionMemberForTypeof(u Type, typ string) (Type, bool) {
	for _, m := range u.UnionMembers {
		if unionMemberTag(m) == typ {
			return m, true
		}
	}
	return Type{}, false
}

// unionMemberTag classifies a scalar union member as its `typeof` string.
// Boolean (`i1`) must be checked before number, since isNumberTy also reports
// true for i1 (it only excludes pointers/dates).
func unionMemberTag(m Type) string {
	switch {
	case m.IsSymbol:
		return "symbol"
	case m.IsBigInt:
		return "bigint"
	case m.IsArray:
		return "array"
	case m.IsFunc:
		return "function"
	case isUnionObjectMember(m):
		return "object"
	case isStringTy(m):
		return "string"
	case m.IR == "i1":
		return "boolean"
	case isNumberTy(m):
		return "number"
	}
	return ""
}

// unionComplement returns the union `u` with member `m` removed. If exactly one
// member remains (and the union isn't nullable), that member's concrete type is
// returned; otherwise a smaller union type (ok=false when nothing meaningful
// remains).
func unionComplement(u Type, m Type) (Type, bool) {
	var rest []Type
	for _, mem := range u.UnionMembers {
		if !sameUnionMember(mem, m) {
			rest = append(rest, mem)
		}
	}
	if len(rest) == 0 {
		return Type{}, false
	}
	if len(rest) == 1 && !u.Nullable {
		return rest[0], true
	}
	nu := u
	nu.UnionMembers = rest
	return nu, true
}

// sameUnionMember reports whether two scalar union members are the same tag.
func sameUnionMember(a, b Type) bool {
	return unionMemberTag(a) == unionMemberTag(b) && unionMemberTag(a) != ""
}

// unionNarrowingFromCondition recognizes a narrowing guard over a union-typed
// local. For a `typeof x === "T"` guard it returns the matched member and the
// complement (both branches narrow); for bare truthiness `if (x)` it narrows the
// true branch only. matchWhenTrue is the branch in which `matchTy` applies;
// `complTy`/`hasCompl` describe the opposite branch.
func (e *Emitter) unionNarrowingFromCondition(test ast.Expression) (name string, matchTy Type, matchWhenTrue bool, complTy Type, hasCompl bool, ok bool) {
	switch t := test.(type) {
	case *ast.BinaryExpression:
		if t.Op == "instanceof" {
			return e.instanceofUnionNarrowing(t)
		}
		if t.Op == "in" {
			return e.inUnionNarrowing(t)
		}
		switch t.Op {
		case "===", "==", "!==", "!=":
		default:
			return "", Type{}, false, Type{}, false, false
		}
		// Find the `typeof <ident>` side and the string-literal side, in either order.
		var arg ast.Expression
		var lit string
		found := false
		for _, pair := range [][2]ast.Expression{{t.Left, t.Right}, {t.Right, t.Left}} {
			u, isU := pair[0].(*ast.UnaryExpression)
			s, isS := pair[1].(*ast.StringLiteral)
			if isU && u.Op == "typeof" && isS {
				arg, lit, found = u.Arg, s.Value, true
				break
			}
		}
		if !found {
			// Not a typeof guard — try a discriminant-equality guard
			// (`x.kind === "circle"`) over a discriminated-union local (TDD-00116).
			return e.discriminantNarrowing(t)
		}
		id, isID := arg.(*ast.Identifier)
		if !isID {
			return "", Type{}, false, Type{}, false, false
		}
		u, isUnion := e.unionLocal(id.Name)
		if !isUnion {
			return "", Type{}, false, Type{}, false, false
		}
		m, has := unionMemberForTypeof(u, lit)
		if !has {
			return "", Type{}, false, Type{}, false, false
		}
		positive := t.Op == "===" || t.Op == "=="
		compl, okc := unionComplement(u, m)
		return id.Name, m, positive, compl, okc, true

	case *ast.CallExpression:
		return e.guardUnionNarrowing(t)

	case *ast.Identifier:
		// Truthiness `if (x)` on a nullable union narrows out null/undefined in
		// the true branch. If one member remains it becomes concrete; otherwise
		// it stays a union with Nullable cleared. No complement narrowing (the
		// false branch is nullish, not a useful concrete type).
		u, isUnion := e.unionLocal(t.Name)
		if !isUnion || !u.Nullable {
			return "", Type{}, false, Type{}, false, false
		}
		if len(u.UnionMembers) == 1 {
			return t.Name, u.UnionMembers[0], true, Type{}, false, true
		}
		nn := u
		nn.Nullable = false
		return t.Name, nn, true, Type{}, false, true
	}
	return "", Type{}, false, Type{}, false, false
}

// instanceofUnionNarrowing recognizes `x instanceof C` over a union-typed
// local: the true branch holds the members that are C or derive from it (a
// class, or the host class of that name), the false branch the rest.
func (e *Emitter) instanceofUnionNarrowing(t *ast.BinaryExpression) (name string, matchTy Type, matchWhenTrue bool, complTy Type, hasCompl bool, ok bool) {
	id, isID := t.Left.(*ast.Identifier)
	cls, isCls := t.Right.(*ast.Identifier)
	if !isID || !isCls {
		return "", Type{}, false, Type{}, false, false
	}
	u, isUnion := e.unionLocal(id.Name)
	if !isUnion {
		return "", Type{}, false, Type{}, false, false
	}
	var in, out []Type
	for _, m := range u.UnionMembers {
		switch {
		case m.IsClass && (m.ClassName == cls.Name || e.classExtends(m.ClassName, cls.Name)):
			in = append(in, m)
		case isHostHandle(m) && hostClassName(m) == cls.Name:
			in = append(in, m)
		case m.IsObject && !m.IsClass && !hasObjHeader(m) && hostClassName(m) == cls.Name:
			in = append(in, m) // a headerless host object (URL)
		default:
			out = append(out, m)
		}
	}
	if len(in) == 0 {
		return "", Type{}, false, Type{}, false, false
	}
	pick := func(ms []Type, nullable bool) Type {
		if len(ms) == 1 && !nullable {
			return ms[0]
		}
		nu := u
		nu.UnionMembers = ms
		nu.Nullable = nullable
		return nu
	}
	match := pick(in, false)
	if len(out) == 0 && !u.Nullable {
		return id.Name, match, true, Type{}, false, true
	}
	return id.Name, match, true, pick(out, u.Nullable), true, true
}

// guardUnionNarrowing recognizes a call to a type guard, `g(x)` where g is
// declared `(…, p: …) => p is T`, over a union-typed local passed as p: the
// true branch holds T's members, the false branch the rest.
func (e *Emitter) guardUnionNarrowing(call *ast.CallExpression) (name string, matchTy Type, matchWhenTrue bool, complTy Type, hasCompl bool, ok bool) {
	callee, isID := call.Callee.(*ast.Identifier)
	if !isID {
		return "", Type{}, false, Type{}, false, false
	}
	decl, found := e.topFuncDecls[callee.Name]
	if !found || decl.ReturnType == nil {
		return "", Type{}, false, Type{}, false, false
	}
	pred, isPred := decl.ReturnType.TypeNode().(*ast.TypePredicate)
	if !isPred || pred.Asserts || pred.This || pred.Type == nil {
		return "", Type{}, false, Type{}, false, false
	}
	idx := -1
	for i, p := range decl.Params {
		if p.Name == pred.ParameterName {
			idx = i
		}
	}
	if idx < 0 || idx >= len(call.Args) {
		return "", Type{}, false, Type{}, false, false
	}
	id, isArg := call.Args[idx].(*ast.Identifier)
	if !isArg {
		return "", Type{}, false, Type{}, false, false
	}
	u, isUnion := e.unionLocal(id.Name)
	if !isUnion {
		return "", Type{}, false, Type{}, false, false
	}
	ta, err := ast.TypeAnnotationOf(pred.Type, "")
	if err != nil {
		return "", Type{}, false, Type{}, false, false
	}
	target := e.resolveType(ta)
	var in, out []Type
	for _, m := range u.UnionMembers {
		if unionMemberMatches(m, target) {
			in = append(in, m)
		} else {
			out = append(out, m)
		}
	}
	if len(in) == 0 {
		return "", Type{}, false, Type{}, false, false
	}
	pick := func(ms []Type, nullable bool) Type {
		if len(ms) == 1 && !nullable {
			return ms[0]
		}
		nu := u
		nu.UnionMembers = ms
		nu.Nullable = nullable
		return nu
	}
	if len(out) == 0 && !u.Nullable {
		return id.Name, pick(in, false), true, Type{}, false, true
	}
	return id.Name, pick(in, false), true, pick(out, u.Nullable), true, true
}

// unionMemberMatches reports whether union member m is (a member of) the
// guard's type t: the same class, host class, object type or primitive kind.
func unionMemberMatches(m, t Type) bool {
	if len(t.UnionMembers) > 0 {
		for _, tm := range t.UnionMembers {
			if unionMemberMatches(m, tm) {
				return true
			}
		}
		return false
	}
	switch {
	case m.IsClass || t.IsClass:
		return m.IsClass && t.IsClass && m.ClassName == t.ClassName
	case isUnionObjectMember(m) && isUnionObjectMember(t):
		if !hasObjHeader(m) || !hasObjHeader(t) {
			return hostClassName(m) == hostClassName(t) && !hasObjHeader(m) && !hasObjHeader(t)
		}
		return layoutKey(m) == layoutKey(t) || (m.RefName != "" && m.RefName == t.RefName)
	case isHostHandle(m) && isHostHandle(t):
		return hostClassName(m) == hostClassName(t)
	}
	return unionMemberTag(m) != "" && unionMemberTag(m) == unionMemberTag(t)
}

// inUnionNarrowing recognizes `"k" in x` over a union-typed local: the true
// branch holds the object members declaring k, the false branch the others
// (TypeScript's `in` narrowing).
func (e *Emitter) inUnionNarrowing(t *ast.BinaryExpression) (name string, matchTy Type, matchWhenTrue bool, complTy Type, hasCompl bool, ok bool) {
	key, isKey := t.Left.(*ast.StringLiteral)
	id, isID := t.Right.(*ast.Identifier)
	if !isKey || !isID {
		return "", Type{}, false, Type{}, false, false
	}
	u, isUnion := e.unionLocal(id.Name)
	if !isUnion {
		return "", Type{}, false, Type{}, false, false
	}
	var in, out []Type
	for _, m := range u.UnionMembers {
		if !isUnionObjectMember(m) {
			continue // a primitive: `in` on it throws, so neither branch
		}
		if _, _, has := e.canonicalizeClassTy(m).FieldIndex(key.Value); has {
			in = append(in, m)
		} else if m.IsClass && e.classHasMethod(m.ClassName, key.Value) {
			in = append(in, m)
		} else {
			out = append(out, m)
		}
	}
	if len(in) == 0 {
		return "", Type{}, false, Type{}, false, false
	}
	pick := func(ms []Type) Type {
		if len(ms) == 1 {
			return ms[0]
		}
		nu := u
		nu.UnionMembers = ms
		nu.Nullable = false
		return nu
	}
	if len(out) == 0 {
		return id.Name, pick(in), true, Type{}, false, true
	}
	return id.Name, pick(in), true, pick(out), true, true
}

// classHasMethod reports a method (or accessor) name of class cls.
func (e *Emitter) classHasMethod(cls, name string) bool {
	info, ok := e.classes[cls]
	if !ok {
		return false
	}
	if _, ok := info.MethodSigs[name]; ok {
		return true
	}
	_, g := info.MethodSigs["__kml_get_"+name]
	return g
}

// discriminantNarrowing recognizes `x.tag === "lit"` over a discriminated-union
// local (TDD-00116) and narrows to the member whose discriminant value is "lit"
// (the complement — the other members — narrows the opposite branch).
func (e *Emitter) discriminantNarrowing(t *ast.BinaryExpression) (name string, matchTy Type, matchWhenTrue bool, complTy Type, hasCompl bool, ok bool) {
	var mem *ast.MemberExpression
	var lit string
	found := false
	for _, pair := range [][2]ast.Expression{{t.Left, t.Right}, {t.Right, t.Left}} {
		me, isMe := pair[0].(*ast.MemberExpression)
		s, isS := pair[1].(*ast.StringLiteral)
		if isMe && isS {
			mem, lit, found = me, s.Value, true
			break
		}
	}
	if !found {
		return "", Type{}, false, Type{}, false, false
	}
	id, isID := mem.Object.(*ast.Identifier)
	if !isID {
		return "", Type{}, false, Type{}, false, false
	}
	u, isUnion := e.unionLocal(id.Name)
	if !isUnion {
		return "", Type{}, false, Type{}, false, false
	}
	dname, _, okd := unionDiscriminantField(u)
	if !okd || mem.Property != dname {
		return "", Type{}, false, Type{}, false, false
	}
	var match Type
	var rest []Type
	matched := false
	for _, m := range u.UnionMembers {
		if isUnionObjectMember(m) && len(m.UserFields()) > 0 && m.UserFields()[0].Ty.LitValue == lit {
			match, matched = m, true
		} else {
			rest = append(rest, m)
		}
	}
	if !matched {
		return "", Type{}, false, Type{}, false, false
	}
	positive := t.Op == "===" || t.Op == "=="
	// Complement (opposite branch): the remaining members — concrete if exactly
	// one non-nullable member is left, else a smaller union.
	if len(rest) == 0 {
		return id.Name, match, positive, Type{}, false, true
	}
	if len(rest) == 1 && !u.Nullable {
		return id.Name, match, positive, rest[0], true, true
	}
	cu := u
	cu.UnionMembers = rest
	return id.Name, match, positive, cu, true, true
}

// applyUnionBranchNarrowing narrows a guarded union-typed local inside whichever
// of an `if`'s branches proves it. Call after pushing the branch's own scope so
// the narrowing is discarded on exit (mirrors applyBranchNarrowing).
func (e *Emitter) applyUnionBranchNarrowing(test ast.Expression, branchIsTrue bool) {
	// `e instanceof HttpError` on a caught error (TDD-00155 Stage 6): in the
	// true branch, the binding narrows to the Error-subclass type, so its
	// extra fields/methods become readable. The pointer is the same either
	// way (subclass instances are prefix-compatible with the error struct).
	if branchIsTrue {
		if bin, isBin := test.(*ast.BinaryExpression); isBin && bin.Op == "instanceof" {
			if id, isID := bin.Left.(*ast.Identifier); isID {
				if rid, isRID := bin.Right.(*ast.Identifier); isRID {
					if sym, found := e.lookup(id.Name); found {
						if info, isClass := e.classes[rid.Name]; isClass && info.IsErrorSubclass {
							// A caught value (TDD-00202) narrowed by `e instanceof
							// HttpError`: the caught record's payload is the thrown
							// Error-subclass instance pointer, so re-bind the branch's
							// `e` to that pointer typed as the subclass — its own
							// fields/methods (`e.status`) then read through the static
							// class path instead of the dynamic member fallback (which
							// throws). Without this the record's built-in Error fields
							// still read, but a subclass-declared field did not.
							if sym.Ty.IsCaught {
								agg := e.freshReg()
								e.emitInstr(fmt.Sprintf("%s = load { i8, i64 }, ptr %s, align 8", agg, sym.Ptr))
								_, pay := e.caughtParts(Value{Ref: agg, Ty: TypeCaught})
								objPtr := e.freshReg()
								e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", objPtr, pay))
								// A class binding's Ptr is a slot holding the object
								// pointer (like any object local), so back the narrowed
								// `e` with a fresh slot pointing at the unpacked payload.
								slot := e.freshReg()
								e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
								e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objPtr, slot))
								e.define(id.Name, Symbol{Ptr: slot, Ty: info.Ty})
								return
							}
							// The legacy errorObjType-typed catch binding: the symbol
							// already holds the object pointer, so narrowing is a pure
							// retype (subclass instances are prefix-compatible).
							if sym.Ty.IsError {
								nt := info.Ty
								sym.NarrowedTo = &nt
								e.define(id.Name, sym)
								return
							}
						}
					}
				}
			}
		}
	}
	name, matchTy, matchWhenTrue, complTy, hasCompl, ok := e.unionNarrowingFromCondition(test)
	if !ok {
		return
	}
	var narrowed Type
	if branchIsTrue == matchWhenTrue {
		narrowed = matchTy
	} else if hasCompl {
		narrowed = complTy
	} else {
		return
	}
	sym, found := e.lookup(name)
	if !found {
		return
	}
	nt := narrowed
	sym.NarrowedTo = &nt
	e.define(name, sym)
}

// narrowUnionLocal applies a union-local narrowing guard's effect for one
// branch in the current scope, without emitting anything (the union-local
// half of applyUnionBranchNarrowing). ok reports a guard over a union local.
func (e *Emitter) narrowUnionLocal(test ast.Expression, branchIsTrue bool) bool {
	name, matchTy, matchWhenTrue, complTy, hasCompl, ok := e.unionNarrowingFromCondition(test)
	if !ok {
		return false
	}
	var narrowed Type
	if branchIsTrue == matchWhenTrue {
		narrowed = matchTy
	} else if hasCompl {
		narrowed = complTy
	} else {
		return true
	}
	sym, found := e.lookup(name)
	if !found {
		return false
	}
	nt := narrowed
	sym.NarrowedTo = &nt
	e.define(name, sym)
	return true
}

// narrowedCondBranchTypes is a conditional's branch types, each inferred in
// the scope its test narrows a union local in; ok is false when the test
// narrows none.
func (e *Emitter) narrowedCondBranchTypes(ex *ast.ConditionalExpression) (cons, alt Type, ok bool) {
	// The object guards (`instanceof`, `in`, a type guard); a `typeof` or
	// truthiness test narrows through the checker on the general path.
	switch t := ex.Test.(type) {
	case *ast.BinaryExpression:
		if t.Op != "instanceof" && t.Op != "in" {
			return Type{}, Type{}, false
		}
	case *ast.CallExpression:
	default:
		return Type{}, Type{}, false
	}
	if _, _, _, _, _, isGuard := e.unionNarrowingFromCondition(ex.Test); !isGuard {
		return Type{}, Type{}, false
	}
	e.pushScope()
	e.narrowUnionLocal(ex.Test, true)
	cons = e.inferExprType(ex.Consequent)
	e.popScope()
	e.pushScope()
	e.narrowUnionLocal(ex.Test, false)
	alt = e.inferExprType(ex.Alternate)
	e.popScope()
	return cons, alt, true
}

// narrowedCondResult is the type of a conditional whose branches are cons
// and alt: that type when both share one representation, else a box.
func narrowedCondResult(cons, alt Type) Type {
	if !cons.IsArray && !alt.IsArray && !cons.IsDynamic && !alt.IsDynamic &&
		cons.IR != "" && cons.IR != "void" && StructFieldIR(cons) == StructFieldIR(alt) &&
		isStringTy(cons) == isStringTy(alt) && cons.IsObject == alt.IsObject &&
		cons.IsClass == alt.IsClass && cons.ClassName == alt.ClassName {
		return cons
	}
	return TypeAny
}

// emitNarrowedConditional emits `test ? a : b` whose test narrows a union
// local: each branch in its narrowed scope.
func (e *Emitter) emitNarrowedConditional(ex *ast.ConditionalExpression, cons, alt Type) (Value, error) {
	resTy := narrowedCondResult(cons, alt)
	cond, err := e.emitExpr(ex.Test)
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)
	slotIR := StructFieldIR(resTy)
	if resTy.IsDynamic {
		slotIR = "i64"
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, slotIR))
	thenL, elseL, doneL := e.freshLabel("ncond.then"), e.freshLabel("ncond.else"), e.freshLabel("ncond.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, thenL, elseL))
	branch := func(label string, expr ast.Expression, isTrue bool) error {
		e.emitLabel(label)
		e.pushScope()
		e.narrowUnionLocal(ex.Test, isTrue)
		v, err := e.emitExpr(expr)
		if err != nil {
			e.popScope()
			return err
		}
		if resTy.IsDynamic {
			if v, err = e.emitBoxValue(v); err != nil {
				e.popScope()
				return err
			}
		} else {
			v = e.coerce(v, resTy)
		}
		e.popScope()
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", slotIR, v.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		return nil
	}
	if err := branch(thenL, ex.Consequent, true); err != nil {
		return Value{}, err
	}
	if err := branch(elseL, ex.Alternate, false); err != nil {
		return Value{}, err
	}
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, slotIR, slot))
	return Value{Ref: r, Ty: resTy}, nil
}

// emitUnboxBoxToType unboxes a union's { i8, i64 } value (already loaded into
// boxRef) to the concrete narrowed type — the payload reinterpreted per the
// target's IR (inttoptr for a string/pointer, bitcast for a double, trunc for a
// bool, the raw i64 for an integer). A target that is still a (multi-member)
// union keeps the box as-is.
func (e *Emitter) emitUnboxBoxToType(boxRef string, target Type) Value {
	if target.IsDynamic {
		return Value{Ref: boxRef, Ty: target}
	}
	tagReg, payload := e.emitUnboxTagPayload(Value{Ref: boxRef})
	switch {
	case target.IsCaught:
		// A TypeCaught param is the { i8, i64 } tag+payload aggregate (TDD-00202),
		// the same (tag, payload) a NaN-box already carries — rebuild the struct
		// rather than pass the bare i64 payload where a { i8, i64 } is expected
		// (e.g. a boxed `reject` closure whose reason parameter is TypeCaught).
		return e.emitCaughtAggregate(tagReg, payload)
	case target.IsArray:
		// The array payload is the any-array box cell (anyArrayBoxTy): field 0
		// is the live {data,len} header pointer (TDD-00212 Stage 3). Deref the box
		// to reach the header, then load the real {data,len} aggregate back out. A
		// zero payload (a box holding null/undefined, or a zeroed slot) yields the
		// {null, 0} empty-array sentinel instead of dereferencing null. (Unboxing
		// produces a value-typed aggregate snapshot; a later mutation of it does
		// not write back through the box — matching how every any→array read
		// materializes a plain array Value.)
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, box))
		loadL := e.freshLabel("unbarr.load")
		mergeL := e.freshLabel("unbarr.merge")
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca {ptr, i64}, align 8", slot))
		e.emitInstr(fmt.Sprintf("store {ptr, i64} {ptr null, i64 0}, ptr %s, align 8", slot))
		hdrSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", hdrSlot))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", hdrSlot))
		// A dynamic array (an untyped `[]`, JSON.parse's arrays): its
		// elements, each converted to the target's element type, in a new
		// static array.
		if el := target.ElemType; el != nil && !target.IsTypedArray && (!el.IsArray || el.IsBuffer) && !el.IsObject && !el.IsTuple && !isNullableScalar(*el) && el.IR != "" && el.IR != "void" {
			isDyn := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tagReg, kmlTagDynArray))
			dynL, staticL := e.freshLabel("unbarr.dyn"), e.freshLabel("unbarr.static")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDyn, dynL, staticL))
			e.emitLabel(dynL)
			e.ensureDynArr()
			e.ensureMalloc()
			elemTy := *target.ElemType
			n := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_len(ptr %s)", n, box))
			size := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", size, n, elemTy.Align()))
			data := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, size))
			idx := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
			condL, bodyL, endL := e.freshLabel("unbarr.dcond"), e.freshLabel("unbarr.dbody"), e.freshLabel("unbarr.dend")
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(condL)
			i, more := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
			e.emitLabel(bodyL)
			ev := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_at(ptr %s, i64 %s)", ev, box, i))
			gep := e.freshReg()
			if elemTy.IsArray {
				// A Buffer element: its header in the slot.
				e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", gep, data, i))
				e.storeArrayElem(gep, elemTy, Value{Ref: ev, Ty: TypeAny})
			} else {
				cv := e.coerce(Value{Ref: ev, Ty: TypeAny}, elemTy)
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, data, i))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, cv.Ref, gep, elemTy.Align()))
			}
			nx := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(endL)
			a0, a1 := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", a0, data))
			e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", a1, a0, n))
			e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", a1, slot))
			// Its own {data,len} header, as every array value carries.
			cell := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", cell))
			e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", a1, cell))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cell, hdrSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			e.emitLabel(staticL)
		}
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, mergeL, loadL))
		e.emitLabel(loadL)
		// An `any[]` (each element a NaN-boxed word) read as a typed array:
		// its elements, each converted, in a new array.
		if el := target.ElemType; el != nil && !el.IsDynamic && !target.IsTypedArray && !isNullableScalar(*el) && el.IR != "" && el.IR != "void" {
			kindP := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", kindP, anyArrayBoxTy, box))
			kind := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", kind, kindP))
			isAnyEl := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 12", isAnyEl, kind))
			convL, plainL := e.freshLabel("unbarr.anyel"), e.freshLabel("unbarr.plain")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAnyEl, convL, plainL))
			e.emitLabel(convL)
			e.ensureMalloc()
			elemTy := *el
			src := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", src, box))
			srcAgg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", srcAgg, src))
			sdata, n := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", sdata, srcAgg))
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", n, srcAgg))
			size := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", size, n))
			data := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, size))
			idx := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
			condL, bodyL, endL := e.freshLabel("unbarr.acond"), e.freshLabel("unbarr.abody"), e.freshLabel("unbarr.aend")
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(condL)
			i, more := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
			e.emitLabel(bodyL)
			sg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", sg, sdata, i))
			ev := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", ev, sg))
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", gep, data, i))
			if elemTy.IsArray {
				e.storeArrayElem(gep, elemTy, Value{Ref: ev, Ty: TypeAny})
			} else {
				cv := e.coerce(Value{Ref: ev, Ty: TypeAny}, elemTy)
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(elemTy), cv.Ref, gep, elemTy.Align()))
			}
			nx := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(endL)
			a0, a1 := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", a0, data))
			e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", a1, a0, n))
			e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", a1, slot))
			cell := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", cell))
			e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", a1, cell))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cell, hdrSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			e.emitLabel(plainL)
		}
		hdr := e.freshReg()
		if el := target.ElemType; el != nil && el.IsDynamic && !target.IsTypedArray {
			// An `any[]` reads a static array's elements as NaN-boxed words
			// (a `Buffer[]`'s header pointers would not be).
			e.ensureDynJSONC()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_words(ptr %s)", hdr, box))
		} else {
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, box))
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, hdrSlot))
		agg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, hdr))
		e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", agg, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(mergeL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", out, slot))
		// The header rides along (null for an empty box), so a store of
		// the unboxed array aliases the boxed one.
		outHdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", outHdr, hdrSlot))
		return Value{Ref: out, Ty: target, ArrayHeader: outHdr}
	case isNullableScalar(target):
		// Unbox into a { i1, T } nullable scalar (ADR-00478): a null/
		// undefined tag is the absent aggregate; anything else unboxes the
		// payload as the bare scalar and wraps it present.
		tag, _ := e.emitUnboxTagPayload(Value{Ref: boxRef})
		isN := e.freshReg()
		isU := e.freshReg()
		absent := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isN, tag, kmlTagNull))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isU, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, isN, isU))
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", present, absent))
		bare := e.emitUnboxBoxToType(boxRef, target.withoutNullable())
		return Value{Ref: e.makeNullableScalarAgg(target, present, bare.Ref), Ty: target}
	case target.IsBigInt:
		// A boxed bigint cell (emit_bigint_box.go) yields its bigint; any
		// other box reads as null (TDD-00229).
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tagReg, kmlTagObject))
		probeL := e.freshLabel("unbig.probe")
		loadL := e.freshLabel("unbig.load")
		mergeL := e.freshLabel("unbig.merge")
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, probeL, mergeL))
		e.emitLabel(probeL)
		cell, isBig := e.emitBoxedBigIntProbe(payload)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBig, loadL, mergeL))
		e.emitLabel(loadL)
		b := e.emitBoxedBigIntLoad(cell)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", b.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(mergeL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: target}
	case target.IsFunc:
		// A boxed function is a dynamic function record: a closure of the
		// target's type calls through it.
		if v, ok := e.emitAnyToClosure(Value{Ref: boxRef, Ty: TypeAny}, target); ok {
			return v
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, payload))
		return Value{Ref: r, Ty: target}
	case isHostHandle(target):
		return e.emitUnboxHost(Value{Ref: boxRef, Ty: TypeAny}, target)
	case isRecordView(target) && hasObjHeader(target):
		// A plain object type: a static object as itself (read through the
		// view), a dynamic object (a bag) converted field by field.
		return Value{Ref: e.emitAnyToLayout(Value{Ref: boxRef, Ty: TypeAny}, target), Ty: target}
	case isStringDict(target):
		// A dictionary as itself; a static object as a dictionary of its keys.
		return e.emitBoxedObjToDict(Value{Ref: boxRef, Ty: TypeAny}, target)
	case target.IsTuple && !target.IsArray:
		return e.emitUnboxTuple(tagReg, payload, target)
	case target.IR == "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, payload))
		return Value{Ref: r, Ty: target}
	case target.IR == "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", r, payload))
		return Value{Ref: r, Ty: target}
	case target.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i1", r, payload))
		return Value{Ref: r, Ty: target}
	default:
		// An integer target. Every JS number is boxed as a double (TDD-00156),
		// so the payload is the double's bits, not the integer: convert with
		// ToNumber (booleans/null included) and the ordinary number→integer
		// coercion — reading the bits raw handed a closure called through
		// `any` 4607182418800017408 for 1 (TDD-00229). A Date is an i64
		// epoch boxed the same way.
		d := e.emitAnyToNum(Value{Ref: boxRef, Ty: TypeAny})
		return e.coerce(Value{Ref: d, Ty: TypeF64}, target)
	}
}

// emitUnboxTuple reads a tuple out of a box: an array (a tuple boxed into
// `any` is one, boxTupleAsArray) gives its elements, converted to the
// fields' types, `undefined` past its end; any other box is taken as the
// tuple's own struct.
func (e *Emitter) emitUnboxTuple(tagReg, payload string, target Type) Value {
	e.ensureDynJSONC()
	e.ensureDynArr()
	e.ensureMalloc()
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", box, slot))
	isArr, isDyn := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tagReg, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tagReg, kmlTagDynArray))
	arrL, notArrL, dynL, mergeL := e.freshLabel("untup.arr"), e.freshLabel("untup.notarr"), e.freshLabel("untup.dyn"), e.freshLabel("untup.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, notArrL))
	e.emitLabel(notArrL)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDyn, dynL, mergeL))
	build := func(elemAt func(i int) string) {
		out := e.freshReg()
		e.emitObjMallocInto(out, target)
		structIR := target.StructIR()
		for i, f := range target.Fields {
			w := elemAt(i)
			cv := e.coerce(Value{Ref: w, Ty: TypeAny}, f.Ty)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, out, i))
			e.storeArrayElem(gep, f.Ty, cv)
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", out, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	// A static array box: its elements as words.
	e.emitLabel(arrL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_words(ptr %s)", hdr, box))
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, hdr))
	data, n := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", data, agg))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", n, agg))
	build(func(i int) string {
		in := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %d, %s", in, i, n))
		idx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 0", idx, in, i))
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", gep, data, idx))
		// Index 0 of an empty array is not loaded: the select below keeps
		// undefined, but the load must not read past a null data pointer.
		safe := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr @__kml_untup_undef", safe, in, gep))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, safe))
		return w
	})
	// A dynamic array (JSON.parse's).
	e.emitLabel(dynL)
	dn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_len(ptr %s)", dn, box))
	build(func(i int) string {
		in := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %d, %s", in, i, dn))
		idx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 0", idx, in, i))
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_at(ptr %s, i64 %s)", raw, box, idx))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", w, in, raw, nbUndefined))
		return w
	})
	e.emitLabel(mergeL)
	if !e.fnDecls["__kml_untup_undef"] {
		e.fnDecls["__kml_untup_undef"] = true
		e.emitGlobal(fmt.Sprintf("@__kml_untup_undef = private constant i64 %d, align 8", nbUndefined))
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: target}
}
