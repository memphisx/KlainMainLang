package ast

import "fmt"

// SymbolMemberPrefix starts the name of a class member keyed by a declared
// symbol (TDD-00246). Like a Symbol's own run-time key ("\x01@@sym:%p") it
// begins with \x01, so string enumerations skip it; util.inspect shows it as
// `Symbol(description)` from the text up to the \x02 before the binding.
const SymbolMemberPrefix = "\x01@@symf:"

// RuntimeKeyPrefix starts the placeholder name of a class member whose
// computed key is evaluated at run time (KeyExpr). Its \x01 keeps it out of
// the string key lists, as a symbol's key.
const RuntimeKeyPrefix = "\x01@@ck:"

// RuntimeKeyName is the placeholder name of the member whose key is at p.
func RuntimeKeyName(p Pos) string { return fmt.Sprintf("%s%d:%d", RuntimeKeyPrefix, p.Line, p.Col) }

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
	// A `let` or `var` holding a literal names a member as a const does when
	// it is its name's only declaration and nothing writes it.
	decls, written := map[string]int{}, map[string]bool{}
	mutable, mutableSyms := map[string]string{}, map[string]string{}
	symDecls := map[string]*VarDeclaration{}
	Inspect(prog, func(n Node) bool {
		switch x := n.(type) {
		case *VarDeclaration:
			decls[x.Name]++
		case *FunctionDeclaration:
			decls[x.Name]++
			for _, p := range x.Params {
				decls[p.Name]++
			}
		case *AssignmentExpression:
			Inspect(x.Left, func(m Node) bool {
				switch t := m.(type) {
				case *Identifier:
					written[t.Name] = true
				case *MemberExpression, *IndexExpression:
					return false // writes a property, not a binding
				}
				return true
			})
		case *UpdateExpression:
			if id, ok := x.Arg.(*Identifier); ok {
				written[id.Name] = true
			}
		case *ForInStatement:
			written[x.VarName] = true
		case *ForOfStatement:
			written[x.VarName] = true
		}
		return true
	})
	Inspect(prog, func(n Node) bool {
		v, ok := n.(*VarDeclaration)
		if !ok {
			return true
		}
		if v.Kind != "const" {
			switch init := v.Init.(type) {
			case *StringLiteral:
				mutable[v.Name] = init.Value
			case *NumberLiteral:
				mutable[v.Name] = init.Value
			case *CallExpression:
				if desc, ok := symbolCallDesc(init); ok {
					mutableSyms[v.Name], symDecls[v.Name] = desc, v
				}
			}
			return true
		}
		switch init := v.Init.(type) {
		case *CallExpression:
			if desc, ok := symbolCallDesc(init); ok {
				descs[v.Name] = desc
			}
		case *StringLiteral:
			literals[v.Name] = init.Value
		case *NumberLiteral:
			literals[v.Name] = init.Value
		}
		return true
	})
	for nm, lit := range mutable {
		if _, ok := literals[nm]; !ok && decls[nm] == 1 && !written[nm] {
			literals[nm] = lit
		}
	}
	for nm, desc := range mutableSyms {
		if _, ok := descs[nm]; !ok && decls[nm] == 1 && !written[nm] {
			descs[nm] = desc
			symDecls[nm].SymbolKey = true
		}
	}
	// name is key's member name, sym whether a symbol keys it; found is
	// false for a key named by neither: one evaluated at run time.
	name := func(key *Identifier) (nm string, sym, found bool) {
		if desc, ok := descs[key.Name]; ok {
			return SymbolMemberName(desc, key.Name), true, true
		}
		if lit, ok := literals[key.Name]; ok {
			return lit, false, true
		}
		return "", false, false
	}
	Inspect(prog, func(n Node) bool {
		c, ok := n.(*ClassDeclaration)
		if !ok {
			return true
		}
		for _, m := range c.Methods {
			if m.Key != nil {
				if nm, sym, found := name(m.Key); found {
					m.Name = nm
					if !sym {
						m.Key = nil
					}
				} else {
					m.Name, m.KeyExpr, m.Key = RuntimeKeyName(m.Key.GetPos()), m.Key, nil
				}
			}
		}
		for i := range c.Fields {
			if k := c.Fields[i].Key; k != nil {
				f := &c.Fields[i]
				if nm, sym, found := name(k); found {
					f.Name = nm
					if !sym {
						f.Key = nil
					}
				} else {
					f.Name, f.KeyExpr, f.Key = RuntimeKeyName(k.GetPos()), k, nil
				}
			}
		}
		return true
	})
	return nil
}

// RuntimeKeyError is the rejection of the first class member left with a
// key evaluated at run time (KeyExpr), or nil when there is none.
func RuntimeKeyError(prog *Program) error {
	var err error
	Inspect(prog, func(n Node) bool {
		c, ok := n.(*ClassDeclaration)
		if !ok || err != nil {
			return err == nil
		}
		at := func(x Expression) {
			if err == nil && x != nil {
				p := x.GetPos()
				err = fmt.Errorf("%d:%d: a computed class member name must be a constant string or number literal, a well-known symbol, or a const holding one or a symbol — a key computed at run time is not yet supported", p.Line, p.Col)
			}
		}
		for _, m := range c.Methods {
			at(m.KeyExpr)
		}
		for _, f := range c.Fields {
			at(f.KeyExpr)
		}
		return true
	})
	return err
}

// symbolCallDesc is the description of a `Symbol()` or `Symbol("d")` call.
func symbolCallDesc(c *CallExpression) (string, bool) {
	id, ok := c.Callee.(*Identifier)
	if !ok || id.Name != "Symbol" || len(c.Args) > 1 {
		return "", false
	}
	if len(c.Args) == 1 {
		s, ok := c.Args[0].(*StringLiteral)
		if !ok {
			return "", false
		}
		return s.Value, true
	}
	return "", true
}

// PrivateScopeSep joins a private name shared by two classes to the
// number that tells each class's apart (the resolver's scopePrivateNames).
const PrivateScopeSep = "__kml_ps"

// PrivateDisplay is a private name as the program spells it.
func PrivateDisplay(name string) string {
	if len(name) == 0 || name[0] != '#' {
		return name
	}
	for i := 1; i+len(PrivateScopeSep) <= len(name); i++ {
		if name[i:i+len(PrivateScopeSep)] == PrivateScopeSep {
			return name[:i]
		}
	}
	return name
}
