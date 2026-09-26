package checker

import (
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
)

// checkAccessibility reports a private or protected class member read
// outside where TypeScript allows it (checkPropertyAccessibilityAtLocation):
//   - a private member only within its class's body (TS2341);
//   - a protected one only within its class or a subclass (TS2445), and
//     for an instance member not through super, only through an instance of
//     that enclosing class or one derived from it (TS2446).
func (c *Checker) checkAccessibility(e *ast.MemberExpression) {
	c.checkMemberAccess(e, e.Object, e.Property)
}

// checkMemberAccess is checkAccessibility for the access e of object's member
// prop. An element access (`o["p"]`) is never checked: TypeScript leaves it
// as the way around a modifier.
func (c *Checker) checkMemberAccess(e ast.Expression, object ast.Expression, prop string) {
	if strings.HasPrefix(prop, "#") || strings.HasPrefix(prop, "__kml_") {
		return
	}
	var vis string
	var owner *binder.Symbol
	static := false
	if sym := c.calleeSymbol(object); sym != nil && sym.Flags&binder.Class != 0 {
		if classDecls(sym) != 1 {
			return
		}
		vis, owner = staticMember(sym, prop)
		static = true
	} else {
		obj := c.TypeOf(object)
		if obj.Flags&TypeParam != 0 && obj.Constraint != nil {
			obj = obj.Constraint
		}
		if c.Unanswered(obj) || obj.Flags&Object == 0 || obj.Flags&Union != 0 || obj.Kind != Instance {
			return
		}
		p := obj.Prop(prop)
		if p == nil {
			return
		}
		if _, super := object.(*ast.SuperExpression); super && p.Field {
			// A field is the instance's own, never the prototype's (TS2855,
			// ES2022 class fields).
			c.report(diag.SuperField, e.GetPos(), prop)
			return
		}
		vis, owner = p.Visibility, p.Owner
		if p.Accessor && c.writes(e) {
			vis = p.WriteVisibility
		}
	}
	if vis == "" || owner == nil {
		return
	}
	enclosing := c.enclosingClasses(e)
	name := sourceName(owner.Name)
	if vis == "private" {
		for _, s := range enclosing {
			if s == owner {
				return
			}
		}
		c.report(diag.PrivateMember, e.GetPos(), prop, name)
		return
	}
	var within *binder.Symbol
	for _, s := range enclosing {
		if derivedFrom(s, owner) {
			within = s
			break
		}
	}
	if within == nil && !static {
		// A function whose `this` is typed a subclass instance (`function
		// f(this: C)`, or contextually) reads it too
		// (getEnclosingClassFromThisParameter).
		if tt := c.thisType(e); tt.Flags&TypeParam != 0 && tt.Constraint != nil || tt.Flags&Object != 0 && tt.Kind == Instance {
			if tt.Flags&TypeParam != 0 {
				tt = tt.Constraint
			}
			if tt.Flags&Object != 0 && tt.Kind == Instance && tt.Symbol != nil && derivedFrom(tt.Symbol, owner) {
				within = tt.Symbol
			}
		}
	}
	if within == nil {
		c.report(diag.ProtectedMember, e.GetPos(), prop, name)
		return
	}
	if _, super := object.(*ast.SuperExpression); static || super {
		return
	}
	obj := c.TypeOf(object)
	if obj.Flags&TypeParam != 0 && obj.Constraint != nil {
		obj = obj.Constraint
	}
	if obj.Flags&Object == 0 || obj.Kind != Instance || obj.Symbol == nil {
		return // the polymorphic `this`, an enclosing class's own
	}
	if !derivedFrom(obj.Symbol, within) {
		c.report(diag.ProtectedReceiver, e.GetPos(), prop, sourceName(within.Name), sourceName(obj.Symbol.Name))
	}
}

// enclosingClasses are the classes whose bodies contain n, innermost first.
func (c *Checker) enclosingClasses(n ast.Node) []*binder.Symbol {
	var out []*binder.Symbol
	for p := c.parentOf(n); p != nil; p = c.parentOf(p) {
		if cd, ok := p.(*ast.ClassDeclaration); ok {
			if s := c.symbolOf(cd); s != nil {
				out = append(out, s)
			}
		}
	}
	return out
}

// derivedFrom reports whether class s is base or derives from it.
func derivedFrom(s, base *binder.Symbol) bool {
	for seen := 0; s != nil && seen < 64; seen++ {
		if s == base {
			return true
		}
		decl := classDecl(s)
		if decl == nil || decl.BaseClass == "" {
			return false
		}
		s = resolveTypeName(decl.BaseClass, s.Scope)
		if s != nil && s.Flags&binder.Class == 0 {
			return false
		}
	}
	return false
}

// staticMember is the visibility and declaring class of class sym's static
// member name, searching its bases; "" when it is public or not found.
func staticMember(sym *binder.Symbol, name string) (string, *binder.Symbol) {
	for seen := 0; sym != nil && seen < 64; seen++ {
		decl := classDecl(sym)
		if decl == nil {
			return "", nil
		}
		for _, f := range decl.Fields {
			if f.Static && f.Name == name {
				return f.Visibility, sym
			}
		}
		for _, m := range decl.Methods {
			if m.IsStatic && m.Name == name {
				return m.Visibility, sym
			}
		}
		if decl.BaseClass == "" {
			return "", nil
		}
		sym = resolveTypeName(decl.BaseClass, sym.Scope)
		if sym != nil && sym.Flags&binder.Class == 0 {
			return "", nil
		}
	}
	return "", nil
}

// writes reports whether e is assigned to (an assignment's or an update's
// target).
func (c *Checker) writes(e ast.Expression) bool {
	switch p := c.parentOf(e).(type) {
	case *ast.AssignmentExpression:
		return p.Left == e
	case *ast.UpdateExpression:
		return p.Arg == e
	}
	return false
}

// checkConstructorAccess reports `new C()` of a class whose constructor
// (its own or the one it inherits) is private outside that class (TS2673),
// or protected outside it and its subclasses (TS2674), as
// isConstructorAccessible.
func (c *Checker) checkConstructorAccess(n *ast.NewExpression, sym *binder.Symbol) {
	if classDecls(sym) != 1 {
		return
	}
	owner := sym
	for seen := 0; owner != nil && seen < 64; seen++ {
		decl := classDecl(owner)
		if decl == nil {
			return
		}
		if decl.Constructor != nil {
			break
		}
		if decl.BaseClass == "" {
			return
		}
		owner = resolveTypeName(decl.BaseClass, owner.Scope)
		if owner == nil || owner.Flags&binder.Class == 0 {
			return
		}
	}
	if owner == nil || classDecl(owner) == nil || classDecl(owner).Constructor == nil {
		return // a circular `extends`
	}
	vis := classDecl(owner).Constructor.Visibility
	if vis == "" {
		return
	}
	for _, s := range c.enclosingClasses(n) {
		if s == owner || vis == "protected" && derivedFrom(s, owner) {
			return
		}
	}
	m := diag.PrivateConstructor
	if vis == "protected" {
		m = diag.ProtectedConstructor
	}
	c.report(m, n.GetPos(), sourceName(owner.Name))
}
