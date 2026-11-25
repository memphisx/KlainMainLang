package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"strings"
)

// DynamicBase lists the classes the value cd extends can hold at run
// time, when that value is not a class declaration (`const Base = c ? A :
// B; class C extends Base {}`, a mixin's `Base: Ctor` parameter). A member
// of the value's type that is a class's own value (`typeof A`) names that
// class. Any other member with construct signatures names every class of
// the program whose value fits it: the program is the whole world, so
// those are all the classes it can hold. cands are visible by name where
// cd is declared; hosts are builtin constructors (`TypeError`) named by
// their globals; hidden are the rest (a class expression, a class local
// to another function), for the caller to make nameable. None has a
// dynamic base of its own. dynamic is false when cd's base is a class, or
// no base at all.
func (c *Checker) DynamicBase(cd *ast.ClassDeclaration) (cands, hidden []*ast.ClassDeclaration, hosts []string, dynamic bool) {
	if cd.BaseClass == "" || cd.BaseQualified || cd.BaseCall != nil {
		return nil, nil, nil, false
	}
	// JavaScript makes such a class from the value at run time (a builtin,
	// a constructor function, a class): codegen's own path, except for a
	// value typed as exactly the program's own classes, each of which the
	// class is specialized over (the run-time path cannot give a class
	// instance the subclass's prototype).
	if c.opts.CompatJS() {
		cands, hidden, hosts, dynamic = c.dynamicBase(cd)
		if !dynamic || len(cands)+len(hidden)+len(hosts) == 0 {
			return nil, nil, nil, false
		}
		sym := c.symbolOf(cd)
		if sym == nil || sym.Scope == nil {
			return nil, nil, nil, false
		}
		if len(hidden) > 0 && len(c.b.StoresTo(lookupName(cd.BaseClass, sym.Scope))) < 2 {
			// A variable holding one class expression all along is that
			// class (codegen's own path); only one reassigned to others
			// selects among them.
			return nil, nil, nil, false
		}
		vt := c.storedBaseType(lookupName(cd.BaseClass, sym.Scope))
		if c.Unanswered(vt) || vt.Flags&Any != 0 {
			return nil, nil, nil, false // an untyped value: any constructor at all
		}
		// Every member one of the candidates themselves, or a builtin's
		// constructor (`const Err = globalThis.Error`): no other value (a
		// constructor function) can reach the base.
		members := []*Type{vt}
		if vt.Flags&Union != 0 {
			members = vt.Types
		}
		others := 0
		for _, m := range members {
			found := false
			for _, k := range cands {
				found = found || c.classValueOf(k) == m
			}
			for _, k := range hidden {
				found = found || c.classValueOf(k) == m
			}
			if !found {
				others++
			}
		}
		if len(hosts) > 0 {
			if others > len(hosts) {
				return nil, nil, nil, false
			}
			return cands, hidden, hosts, true
		}
		for _, m := range members {
			found := false
			for _, k := range cands {
				found = found || c.classValueOf(k) == m
			}
			for _, k := range hidden {
				// A class expression a variable holds (`var B = class {}`),
				// lifted to a name of its own.
				found = found || c.classValueOf(k) == m
			}
			if !found {
				return nil, nil, nil, false
			}
		}
		return cands, hidden, nil, true
	}
	return c.dynamicBase(cd)
}

func (c *Checker) dynamicBase(cd *ast.ClassDeclaration) (cands, hidden []*ast.ClassDeclaration, hosts []string, dynamic bool) {
	sym := c.symbolOf(cd)
	if sym == nil || sym.Scope == nil {
		return nil, nil, nil, false
	}
	bsym := lookupName(cd.BaseClass, sym.Scope)
	if c.dynamicBaseSymbol(bsym) == nil {
		return nil, nil, nil, false
	}
	vt := c.storedBaseType(bsym)
	if c.Unanswered(vt) && len(bsym.Declarations) == 1 {
		// Unanswered inside a cycle (a class's static getter naming
		// `typeof` its class, read while the class was typed): asked
		// again now the program is checked.
		if v, ok := bsym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.TypeAnnot == nil && v.Init != nil {
			vt = c.TypeOf(v.Init)
		}
	}
	if c.Unanswered(vt) {
		return nil, nil, nil, false
	}
	if c.generic(vt) {
		// A mixin's `Base: B` with `B extends Ctor`: any class the
		// constraint admits.
		if bc := c.baseConstraint(vt, 0); bc != nil {
			vt = bc
		}
	}
	type cls struct {
		decl    *ast.ClassDeclaration
		val     *Type
		visible bool
		untyped bool
	}
	var classes []cls
	consider := func(k *ast.ClassDeclaration, v *Type, visible bool) {
		if k == cd || k.Ambient || c.Unanswered(v) || v.Flags&Any != 0 {
			return
		}
		if k.BaseClass != "" && !k.BaseQualified && k.BaseCall == nil {
			if ks := c.symbolOf(k); ks != nil && c.dynamicBaseSymbol(lookupName(k.BaseClass, ks.Scope)) != nil {
				return // a dynamic base of its own
			}
		}
		// A class whose instance is untyped (a construct signature
		// returning any) fits every constructor type: a candidate only
		// when the value's type names it.
		untyped := false
		if ct := c.signaturesOf(v, true); !c.Unanswered(ct) && ct.Ret() != nil && ct.Ret().Flags&Any != 0 {
			untyped = true
		}
		classes = append(classes, cls{k, v, visible, untyped})
	}
	for _, s := range c.b.Symbols() {
		if s.Flags&binder.Class == 0 || s.Scope == nil || c.inLibrary(s.Scope) {
			continue
		}
		for _, d := range s.Declarations {
			if k, ok := d.Node.(*ast.ClassDeclaration); ok {
				consider(k, c.classValueOf(k), lookupName(k.Name, sym.Scope) == s)
			}
		}
	}
	// Anonymous class expressions declare no symbol.
	ast.Inspect(c.b.Program, func(n ast.Node) bool {
		if ce, ok := n.(*ast.ClassExpression); ok && c.symbolOf(ce.Decl) == nil {
			consider(ce.Decl, c.TypeOf(ce), false)
		}
		return true
	})
	if vt.Flags&Any != 0 {
		// An untyped base: any class of the program the class's body can
		// stand on, every member it reads through this or super that it
		// does not declare being the class's.
		used := inheritedUses(cd)
		for _, k := range classes {
			if k.decl.IsAbstract || len(k.decl.TypeParams) > 0 {
				continue
			}
			inst := c.signaturesOf(k.val, true).Ret()
			if k.untyped || inst == nil || inst.Flags&Object == 0 {
				continue
			}
			fits := true
			for name := range used {
				fits = fits && inst.Prop(name) != nil
			}
			if fits {
				if k.visible {
					cands = append(cands, k.decl)
				} else {
					hidden = append(hidden, k.decl)
				}
			}
		}
		return cands, hidden, nil, true
	}
	members := []*Type{vt}
	if vt.Flags&Union != 0 {
		members = vt.Types
	}
	seen := map[*ast.ClassDeclaration]bool{}
	add := func(k cls) {
		if seen[k.decl] {
			return
		}
		seen[k.decl] = true
		if k.visible {
			cands = append(cands, k.decl)
		} else {
			hidden = append(hidden, k.decl)
		}
	}
	for _, m := range members {
		if m.Flags&(Null|Undefined) != 0 {
			continue
		}
		exact := false
		for _, k := range classes {
			if k.val == m {
				add(k)
				exact = true
			}
		}
		if !exact && m.Symbol != nil && c.inLibrary(m.Symbol.Scope) && strings.HasSuffix(m.Symbol.Name, "Constructor") {
			// A builtin's constructor (`TypeErrorConstructor`): the global
			// whose value it is, where cd is declared.
			name := strings.TrimSuffix(m.Symbol.Name, "Constructor")
			if gs := lookupName(name, sym.Scope); gs != nil && c.inLibrary(gs.Scope) && c.typeOfSymbol(gs) == m {
				hosts = append(hosts, name)
				exact = true
			}
		}
		if exact || m.Flags&Object == 0 {
			continue
		}
		if ct := c.signaturesOf(m, true); c.Unanswered(ct) || ct.Flags&Object == 0 || ct.Kind != Function {
			continue
		}
		for _, k := range classes {
			if !k.decl.IsAbstract && k.untyped && c.genericClassFits(k.decl, m) {
				add(k)
				continue
			}
			if !k.decl.IsAbstract && !k.untyped && c.relate(k.val, m, relation{missingIsNo: true}, 0) == yes && c.namesEvery(k.val, m) {
				add(k)
			}
		}
	}
	return cands, hidden, hosts, true
}

// dynamicBaseSymbol is bsym when it names a program variable or parameter
// (no class), the base of a class known only at run time; else nil.
func (c *Checker) dynamicBaseSymbol(bsym *binder.Symbol) *binder.Symbol {
	if bsym == nil || bsym.Flags&binder.Class != 0 || bsym.Flags&binder.Variable == 0 || c.inLibrary(bsym.Scope) {
		return nil
	}
	return bsym
}

// namesEvery reports that the instance class value v constructs has every
// required member of the instance constructor type m makes, where v's
// instance is partial (a builtin base's members grafted, which relate
// leaves open).
func (c *Checker) namesEvery(v, m *Type) bool {
	vi, mi := c.signaturesOf(v, true).Ret(), c.signaturesOf(m, true).Ret()
	if vi == nil || mi == nil || !vi.partial {
		return true
	}
	for _, p := range mi.Props {
		if !p.Optional && vi.Prop(p.Name) == nil {
			return false
		}
	}
	return true
}

// genericClassFits reports a generic program class (whose construct
// signature this checker answers untyped) whose instance, its type
// parameters unbound, has every required member of the instance the
// constructor type m makes.
func (c *Checker) genericClassFits(k *ast.ClassDeclaration, m *Type) bool {
	ks := c.symbolOf(k)
	if ks == nil || c.inLibrary(ks.Scope) || len(k.TypeParams) == 0 {
		return false
	}
	args := make([]*Type, len(k.DeclaredTypeParams().Names))
	for i := range args {
		args[i] = c.unknownT
	}
	inst, mi := c.instanceOf(ks, args), c.signaturesOf(m, true).Ret()
	if inst == nil || mi == nil || mi.Flags&Object == 0 {
		return false
	}
	for _, p := range mi.Props {
		if !p.Optional && inst.Prop(p.Name) == nil {
			return false
		}
	}
	return true
}

// inheritedUses is the member names cd's body reads through this or super
// that cd does not declare itself.
func inheritedUses(cd *ast.ClassDeclaration) map[string]bool {
	own := map[string]bool{}
	for _, f := range cd.Fields {
		own[f.Name] = true
	}
	for _, m := range cd.Methods {
		own[m.Name] = true
	}
	used := map[string]bool{}
	ast.Inspect(cd, func(n ast.Node) bool {
		if m, ok := n.(*ast.MemberExpression); ok {
			switch m.Object.(type) {
			case *ast.ThisExpression, *ast.SuperExpression:
				if !own[m.Property] && !strings.HasPrefix(m.Property, "#") {
					used[m.Property] = true
				}
			}
		}
		return true
	})
	return used
}

// UnboundBase reports cd's base a bare name no scope around the class
// declares, the builtin library included (`class C extends Image {}` in
// Node, which has no such global).
func (c *Checker) UnboundBase(cd *ast.ClassDeclaration) bool {
	if cd.BaseClass == "" || cd.BaseQualified || cd.BaseCall != nil {
		return false
	}
	sym := c.symbolOf(cd)
	if sym == nil || sym.Scope == nil {
		return false
	}
	return lookupName(cd.BaseClass, sym.Scope) == nil
}

// storedBaseType is the type of the values a base variable holds: its
// declared type, and in a JavaScript program, which checks no assignment
// against it, the union of every value stored to it (`Base = B2` after
// `var Base = B1`); any when a store's value is not one expression.
func (c *Checker) storedBaseType(sym *binder.Symbol) *Type {
	vt := c.typeOfSymbol(sym)
	if !c.opts.CompatJS() || sym == nil || c.Unanswered(vt) {
		return vt
	}
	ts := []*Type{vt}
	for _, n := range c.b.StoresTo(sym) {
		var rhs ast.Expression
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			if x.Op != "=" {
				return c.anyT
			}
			rhs = x.Right
		case *ast.VarDeclaration:
			rhs = x.Init
		default:
			return c.anyT
		}
		if rhs == nil {
			continue
		}
		t := c.TypeOf(rhs)
		if c.Unanswered(t) {
			return t
		}
		ts = append(ts, t)
	}
	return c.in.union(ts...)
}
