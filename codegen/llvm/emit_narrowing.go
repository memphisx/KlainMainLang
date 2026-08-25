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
		if isUnionObjectMember(m) && len(m.Fields) > 0 && m.Fields[0].Ty.LitValue == lit {
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

// emitUnboxBoxToType unboxes a union's { i8, i64 } value (already loaded into
// boxRef) to the concrete narrowed type — the payload reinterpreted per the
// target's IR (inttoptr for a string/pointer, bitcast for a double, trunc for a
// bool, the raw i64 for an integer). A target that is still a (multi-member)
// union keeps the box as-is.
func (e *Emitter) emitUnboxBoxToType(boxRef string, target Type) Value {
	if target.IsDynamic {
		return Value{Ref: boxRef, Ty: target}
	}
	_, payload := e.emitUnboxTagPayload(Value{Ref: boxRef})
	switch {
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
		// An integer member: the payload already holds the i64 value.
		return Value{Ref: payload, Ty: target}
	}
}

