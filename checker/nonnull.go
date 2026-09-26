package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
)

// nullFacts are the absent values a type admits.
type nullFacts int

const (
	isUndefined nullFacts = 1 << iota
	isNull
)

// nullFactsOf reports which of null and undefined t admits (an absent
// optional property reads as undefined; void is not an absent value here,
// as in tsc's checkNonNullType).
func nullFactsOf(t *Type) nullFacts {
	var f nullFacts
	for _, m := range members(t) {
		switch {
		case m.Flags&Undefined != 0:
			f |= isUndefined
		case m.Flags&Null != 0:
			f |= isNull
		}
	}
	return f
}

// checkNonNull is TypeScript's checkNonNullType on the operand e: a value
// that may be null or undefined (TS18047-18049 for a name, TS2531-2533
// otherwise), a bare null or undefined (TS18050), or unknown (TS18046,
// TS2571) cannot be used where an object or a number is needed. It returns
// the type with null and undefined removed, or nil when nothing is left
// (or e was reported, or its type is not modelled).
func (c *Checker) checkNonNull(e ast.Expression) *Type {
	t := c.TypeOf(e)
	if c.Unanswered(t) || t.Flags&Any != 0 {
		return nil
	}
	if t.Flags&TypeParam != 0 {
		// A type parameter is checked as its constraint; unconstrained, it
		// is not reported here (tsc's getTypeFacts of a bare T).
		if t.Constraint == nil {
			return t
		}
		if c.checkNonNullType(e, c.narrowedConstraint(e, t.Constraint)) == nil {
			return nil
		}
		return t
	}
	return c.checkNonNullType(e, t)
}

// checkNonNullType is checkNonNull on e, typed t.
func (c *Checker) checkNonNullType(e ast.Expression, t *Type) *Type {
	if c.Unanswered(t) || t.Flags&(Any|TypeParam) != 0 {
		return nil
	}
	name, entity := entityName(e)
	if t.Flags&Unknown != 0 {
		if entity {
			c.report(diag.IsOfTypeUnknown, startOf(e), name)
		} else {
			c.report(diag.ObjectOfTypeUnknown, startOf(e))
		}
		return nil
	}
	f := nullFactsOf(t)
	if f == 0 {
		return t
	}
	if id, ok := e.(*ast.Identifier); ok && f == isUndefined && t.Flags&Union != 0 {
		if sym, _ := c.b.Resolve(id); sym != nil && c.evolving[sym] {
			// An evolving variable some path leaves unassigned: TypeScript's
			// "used before being assigned" (TS2454), not "possibly undefined".
			return nil
		}
	}
	for _, m := range members(t) {
		if m.Flags&TypeParam != 0 {
			return nil // a type parameter's constraint may exclude them
		}
	}
	switch {
	case isNullLiteral(e):
		c.report(diag.ValueCannotBeUsed, startOf(e), "null")
	case entity && name == "undefined":
		c.report(diag.ValueCannotBeUsed, startOf(e), "undefined")
	case entity && f == isUndefined|isNull:
		c.report(diag.PossiblyNullish, startOf(e), name)
	case entity && f == isUndefined:
		c.report(diag.PossiblyUndefined, startOf(e), name)
	case entity:
		c.report(diag.PossiblyNull, startOf(e), name)
	case f == isUndefined|isNull:
		c.report(diag.ObjectPossiblyNullish, startOf(e))
	case f == isUndefined:
		c.report(diag.ObjectPossiblyUndef, startOf(e))
	default:
		c.report(diag.ObjectPossiblyNull, startOf(e))
	}
	return nil
}

// checkCallee is checkNonNull for a callee, reported as TypeScript's
// "cannot invoke" family (TS2721-2723).
func (c *Checker) checkCallee(e ast.Expression) {
	t := c.TypeOf(e)
	if c.Unanswered(t) || t.Flags&Union == 0 {
		return
	}
	var f nullFacts
	for _, m := range t.Types {
		switch {
		case m.Flags&Undefined != 0:
			f |= isUndefined
		case m.Flags&Null != 0:
			f |= isNull
		case m.Flags&Object != 0 && m.Kind == Function:
		default:
			return // a member the checker does not know to be callable
		}
	}
	switch f {
	case isUndefined | isNull:
		c.report(diag.InvokePossiblyNullish, startOf(e))
	case isUndefined:
		c.report(diag.InvokePossiblyUndef, startOf(e))
	case isNull:
		c.report(diag.InvokePossiblyNull, startOf(e))
	}
}

func isNullLiteral(e ast.Expression) bool {
	n, ok := e.(*ast.NullLiteral)
	return ok && !n.IsUndefined
}

// entityName is e's text when it is an entity name expression (an
// identifier, or a property access chain on one): TypeScript names the
// operand in its message then.
func entityName(e ast.Expression) (string, bool) {
	switch e := e.(type) {
	case *ast.Identifier:
		return sourceName(e.Name), true
	case *ast.NullLiteral:
		if e.IsUndefined {
			return "undefined", true
		}
	case *ast.MemberExpression:
		if e.Optional || e.ChainEnd {
			return "", false
		}
		if base, ok := entityName(e.Object); ok {
			return base + "." + e.Property, true
		}
	}
	return "", false
}

// startOf is where e's source text begins: TypeScript reports an operand's
// error there, and a member or call node's own position is its operator.
func startOf(e ast.Expression) ast.Pos {
	for {
		switch x := e.(type) {
		case *ast.MemberExpression:
			e = x.Object
		case *ast.IndexExpression:
			e = x.Object
		case *ast.CallExpression:
			e = x.Callee
		case *ast.BinaryExpression:
			e = x.Left
		case *ast.NonNullExpression:
			e = x.Arg
		default:
			return e.GetPos()
		}
	}
}

// narrowedConstraint is a type parameter's constraint narrowed along e's
// flow, as tsc narrows T in a constraint position (`if (x) x.length`, x:
// T extends string | undefined).
func (c *Checker) narrowedConstraint(e ast.Expression, constraint *Type) *Type {
	if !narrowable(constraint) {
		return constraint
	}
	r, root, ok := c.refOf(e)
	if !ok {
		return constraint
	}
	f, local := c.flowAtRoot(root)
	if f == nil || !local {
		return constraint
	}
	t, _ := c.flowType(f, r, constraint, newFlowWalk())
	return t
}
