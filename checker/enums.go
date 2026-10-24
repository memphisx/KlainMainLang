package checker

import (
	"strconv"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// enumMembers types each member of the enum sym declares, in order: a
// number or string literal carrying the enum and its name. Members
// auto-increment from the previous numeric value (from 0); an initializer
// is TypeScript's constant evaluation of it. A computed member (`A = f()`)
// is a numeric member of its own whose value is unknown (an empty Value),
// as TypeScript 5.0 types it.
func (c *Checker) enumMembers(sym *binder.Symbol) []*Type {
	if ms, ok := c.enums[sym]; ok {
		return ms
	}
	c.enums[sym] = nil
	var decl *ast.EnumDeclaration
	for _, d := range sym.Declarations {
		if ed, ok := d.Node.(*ast.EnumDeclaration); ok {
			if decl != nil {
				return nil // merged enums: not modelled
			}
			decl = ed
		}
	}
	if decl == nil {
		return nil
	}
	var out []*Type
	next, numeric := 0.0, true
	prior := map[string]ast.EnumConst{}
	for _, m := range decl.Members {
		flags, value := NumberLiteral, ""
		var v ast.EnumConst
		computed := false
		if m.Value == nil {
			if !numeric {
				return nil
			}
			v = ast.EnumConst{Num: next}
		} else {
			// TypeScript's constant enum expression: literals, earlier
			// members, the arithmetic, bitwise and concatenation operators.
			var ok bool
			v, ok = ast.FoldEnumConst(m.Value, decl.Name, prior)
			computed = !ok
		}
		switch {
		case computed:
			// The member after it needs an initializer (TS1061).
			numeric = false
		case v.IsStr:
			prior[m.Name] = v
			flags, value, numeric = StringLiteral, v.Str, false
		default:
			prior[m.Name] = v
			value, next, numeric = canonicalNumber(ast.JSNumberString(v.Num)), v.Num+1, true
		}
		name, size := m.Name, len(decl.Members)
		out = append(out, c.in.intern("e"+strconv.Itoa(sym.ID)+":"+name, func() *Type {
			return &Type{Flags: flags, Value: value, Symbol: sym, Member: name, EnumSize: size}
		}))
	}
	c.enums[sym] = out
	return out
}

// enumType is the type an enum's name denotes: the union of its members.
func (c *Checker) enumType(sym *binder.Symbol) *Type {
	ms := c.enumMembers(sym)
	if ms == nil {
		return c.unanswered
	}
	return c.in.union(ms...)
}

// enumObject is the type of an enum's value: an object with a read-only
// property per member, and, when a member is numeric, tsc's reverse-mapping
// index signature `[x: number]: string`.
func (c *Checker) enumObject(sym *binder.Symbol) *Type {
	ms := c.enumMembers(sym)
	if ms == nil {
		return c.unanswered
	}
	props := make([]*Property, len(ms))
	numeric := false
	for i, m := range ms {
		props[i] = &Property{Name: m.Member, Type: m}
		numeric = numeric || m.Flags&NumberLiteral != 0
	}
	if numeric {
		return c.in.indexed(props, nil, c.strT)
	}
	return c.in.object(props)
}
