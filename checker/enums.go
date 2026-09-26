package checker

import (
	"strconv"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// enumMembers types each member of the enum sym declares, in order: a
// number or string literal carrying the enum and its name. Members
// auto-increment from the previous numeric value (from 0); a string member
// needs its literal. nil when a value is computed (not modelled yet).
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
	for _, m := range decl.Members {
		flags, value := NumberLiteral, ""
		switch v := m.Value.(type) {
		case nil:
			if !numeric {
				return nil
			}
			value = canonicalNumber(strconv.FormatFloat(next, 'g', -1, 64))
			next++
		case *ast.NumberLiteral:
			f, err := strconv.ParseFloat(canonicalNumber(v.Value), 64)
			if err != nil || v.IsBigInt {
				return nil
			}
			value, next, numeric = canonicalNumber(v.Value), f+1, true
		case *ast.StringLiteral:
			flags, value, numeric = StringLiteral, v.Value, false
		default:
			return nil
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
// property per member.
func (c *Checker) enumObject(sym *binder.Symbol) *Type {
	ms := c.enumMembers(sym)
	if ms == nil {
		return c.unanswered
	}
	props := make([]*Property, len(ms))
	for i, m := range ms {
		props[i] = &Property{Name: m.Member, Type: m}
	}
	return c.in.object(props)
}
