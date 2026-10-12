package ast

import "fmt"

// SymbolMemberPrefix starts the name of a class member keyed by a declared
// symbol (TDD-00246). Like a Symbol's own run-time key ("\x01@@sym:%p") it
// begins with \x01, so string enumerations skip it; util.inspect shows it as
// `Symbol(description)` from the text up to the \x02 before the binding.
const SymbolMemberPrefix = "\x01@@symf:"

// SymbolMemberName is the name of a member keyed by the const named key
// (its resolved binding), whose symbol has description desc.
func SymbolMemberName(desc, key string) string {
	return SymbolMemberPrefix + desc + "\x02" + key
}

// NameComputedMembers names every class member whose computed key is an
// identifier (`[k]`): a const symbol keys it (SymbolMemberName, Key kept),
// a const string or number literal names it by value (Key dropped), and
// anything else is an error. Run after the resolver's renaming, before
// type checking; running it again changes nothing.
func NameComputedMembers(prog *Program) error {
	descs := map[string]string{}
	literals := map[string]string{}
	Inspect(prog, func(n Node) bool {
		v, ok := n.(*VarDeclaration)
		if !ok || v.Kind != "const" {
			return true
		}
		switch init := v.Init.(type) {
		case *CallExpression:
			if id, ok := init.Callee.(*Identifier); ok && id.Name == "Symbol" {
				desc := ""
				if len(init.Args) == 1 {
					if s, ok := init.Args[0].(*StringLiteral); ok {
						desc = s.Value
					}
				}
				descs[v.Name] = desc
			}
		case *StringLiteral:
			literals[v.Name] = init.Value
		case *NumberLiteral:
			literals[v.Name] = init.Value
		}
		return true
	})
	var err error
	name := func(key *Identifier) (string, bool) {
		if desc, ok := descs[key.Name]; ok {
			return SymbolMemberName(desc, key.Name), true
		}
		if lit, ok := literals[key.Name]; ok {
			return lit, false
		}
		if err == nil {
			p := key.GetPos()
			err = fmt.Errorf("%d:%d: a computed class member name must be a constant string or number literal, a well-known symbol, or a const holding one or a symbol — a key computed at run time is not yet supported", p.Line, p.Col)
		}
		return "", false
	}
	Inspect(prog, func(n Node) bool {
		c, ok := n.(*ClassDeclaration)
		if !ok {
			return true
		}
		for _, m := range c.Methods {
			if m.Key != nil {
				nm, sym := name(m.Key)
				m.Name = nm
				if !sym {
					m.Key = nil
				}
			}
		}
		for i := range c.Fields {
			if k := c.Fields[i].Key; k != nil {
				nm, sym := name(k)
				c.Fields[i].Name = nm
				if !sym {
					c.Fields[i].Key = nil
				}
			}
		}
		return true
	})
	return err
}
