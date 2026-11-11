// emit_symfields.go — class fields keyed by a declared symbol (TDD-00246):
// `const kRefed = Symbol('refed'); class T { [kRefed]: boolean = true }`.
package llvm

import (
	"fmt"
	"slices"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
)

// symbolFieldPrefix starts a symbol-keyed field's name. Like a Symbol's own
// run-time key ("\x01@@sym:%p") it begins with \x01, so the key lists skip
// it as a symbol's; util.inspect shows it as `Symbol(description)` from the
// text up to the \x02 that precedes the const's binding.
const symbolFieldPrefix = ast.SymbolMemberPrefix

// lowerSymbolFields names every symbol-keyed class field and rewrites each
// `obj[k]` whose key is such a const and whose receiver the checker types
// as an instance of a class with that field (or a subclass of one) to the
// field's member access, which every read, write and compound-assignment
// path already takes. Any other `obj[k]` keeps the dynamic path.
func (e *Emitter) lowerSymbolFields(prog *ast.Program) error {
	if err := ast.NameComputedMembers(prog); err != nil {
		return err
	}
	if err := ast.RuntimeKeyError(prog); err != nil {
		return err
	}
	e.lowerConstLiteralKeys(prog)
	// The class each symbol-keyed member is declared on, by key, and each
	// class's base.
	// (statics, the class's own members, apart from its instances'.)
	fields, statics := map[string]map[string]string{}, map[string]map[string]string{}
	bases := map[string]string{}
	ast.Inspect(prog, func(n ast.Node) bool {
		c, ok := n.(*ast.ClassDeclaration)
		if !ok {
			return true
		}
		bases[c.Name] = c.BaseClass
		add := func(key *ast.Identifier, name string, static bool) {
			m := fields
			if static {
				m = statics
			}
			if m[c.Name] == nil {
				m[c.Name] = map[string]string{}
			}
			m[c.Name][key.Name] = name
		}
		for _, m := range c.Methods {
			if m.Key != nil {
				add(m.Key, m.Name, m.IsStatic)
			}
		}
		for _, f := range c.Fields {
			if f.Key != nil {
				add(f.Key, f.Name, f.Static)
			}
		}
		return true
	})
	if len(fields) == 0 && len(statics) == 0 {
		return nil
	}
	keys := map[string]bool{}
	for _, ms := range []map[string]map[string]string{fields, statics} {
		for _, m := range ms {
			for k := range m {
				keys[k] = true
			}
		}
	}
	c := e.front()
	if c == nil {
		return nil
	}
	memberOf := func(ms map[string]map[string]string, class, key string) (string, bool) {
		for seen := 0; class != "" && seen < 64; seen++ {
			if f, ok := ms[class][key]; ok {
				return f, true
			}
			class = bases[class]
		}
		return "", false
	}
	fieldOf := func(class, key string) (string, bool) { return memberOf(fields, class, key) }
	// The class being rewritten: `super[k]` is its base's member.
	class := ""
	var rewrite func(ast.Node) ast.Node
	rewrite = func(n ast.Node) ast.Node {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			saved := class
			class = cd.Name
			ast.RewriteChildren(n, rewrite)
			class = saved
			return n
		}
		ast.RewriteChildren(n, rewrite)
		ie, ok := n.(*ast.IndexExpression)
		if !ok {
			return n
		}
		id, ok := ie.Index.(*ast.Identifier)
		if !ok || !keys[id.Name] {
			return n
		}
		if _, isSuper := ie.Object.(*ast.SuperExpression); isSuper {
			if f, ok := fieldOf(bases[class], id.Name); ok {
				return ast.NewMemberExpression(ie.Object, f, ie.GetPos())
			}
			return n
		}
		if cid, ok := ie.Object.(*ast.Identifier); ok {
			if _, isClass := bases[cid.Name]; isClass {
				if sym, _ := c.Binding().Resolve(cid); sym != nil && sym.Flags&binder.Class != 0 {
					// `C[k]`: the class's own (static) member.
					if f, ok := memberOf(statics, cid.Name, id.Name); ok {
						return ast.NewMemberExpression(ie.Object, f, ie.GetPos())
					}
					return n
				}
			}
		}
		t := c.TypeOf(ie.Object)
		if c.Unanswered(t) || t.Symbol == nil {
			return n
		}
		if f, ok := fieldOf(t.Symbol.Name, id.Name); ok {
			return ast.NewMemberExpression(ie.Object, f, ie.GetPos())
		}
		return n
	}
	ast.RewriteChildren(prog, rewrite)
	return nil
}

// lowerConstLiteralKeys rewrites `obj[k]` whose key is a const holding a
// string or number literal to `obj[<literal>]`: the const never changes, so
// the two are the same access, and the literal form reaches the member.
func (e *Emitter) lowerConstLiteralKeys(prog *ast.Program) {
	c := e.front()
	if c == nil {
		return
	}
	var rewrite func(ast.Node) ast.Node
	rewrite = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rewrite)
		if call, ok := n.(*ast.CallExpression); ok {
			// `obj['m x'](…)` / `obj[0](…)` calling a member of a class or
			// object type is the member's call, `this` bound alike.
			if ie, ok := call.Callee.(*ast.IndexExpression); ok && !ie.Optional {
				if key, ok := literalIndexKey(ie.Index); ok {
					if t := c.TypeOf(ie.Object); !c.Unanswered(t) && t.Flags&checker.Object != 0 && t.Flags&checker.Union == 0 && t.Prop(key) != nil {
						call.Callee = ast.NewMemberExpression(ie.Object, key, ie.GetPos())
					}
				}
			}
			return n
		}
		ie, ok := n.(*ast.IndexExpression)
		if !ok {
			return n
		}
		id, ok := ie.Index.(*ast.Identifier)
		if !ok {
			return n
		}
		if v, number, ok := c.ConstLiteralKey(id); ok {
			if number {
				ie.Index = ast.NewNumberLiteral(v, id.GetPos())
			} else {
				ie.Index = ast.NewStringLiteral(v, id.GetPos())
			}
		}
		return n
	}
	ast.RewriteChildren(prog, rewrite)
}

// literalIndexKey is the property key a string or numeric literal index
// names.
func literalIndexKey(e ast.Expression) (string, bool) {
	switch k := e.(type) {
	case *ast.StringLiteral:
		return k.Value, true
	case *ast.NumberLiteral:
		if !k.IsBigInt {
			return ast.NumberKey(k.Value), true
		}
	}
	return "", false
}

// symbolFields are t's symbol-keyed fields, in declaration order.
func symbolFields(t Type) []Field {
	var out []Field
	for _, f := range t.UserFields() {
		if isSymbolFieldName(f.Name) {
			out = append(out, f)
		}
	}
	return out
}

// isSymbolFieldName reports a field keyed by a symbol: a declared one's
// (symbolFieldPrefix) or a well-known one's (`@@iterator`).
func isSymbolFieldName(name string) bool {
	if wk, ok := strings.CutPrefix(name, "@@"); ok {
		return slices.Contains(wellKnownMemberKeys, wk)
	}
	return strings.HasPrefix(name, symbolFieldPrefix)
}

// symbolFieldDisplay is how util.inspect names a field: a symbol-keyed
// one as `Symbol(description)`, an identifier bare, any other key quoted
// (`'2'`, `'a-b'`).
func symbolFieldDisplay(name string) string {
	if wk, ok := strings.CutPrefix(name, "@@"); ok && slices.Contains(wellKnownMemberKeys, wk) {
		return "Symbol(Symbol." + wk + ")"
	}
	rest, ok := strings.CutPrefix(name, symbolFieldPrefix)
	if !ok {
		if isInspectIdent(name) {
			return name
		}
		return "'" + strings.ReplaceAll(strings.ReplaceAll(name, "\\", "\\\\"), "'", "\\'") + "'"
	}
	desc, _, _ := strings.Cut(rest, "\x02")
	return "Symbol(" + desc + ")"
}

// symbolFieldGlobal is the module global holding the symbol a
// symbol-keyed field's name was made from, when the const is one.
func (e *Emitter) symbolFieldGlobal(name string) (string, bool) {
	if wk, ok := strings.CutPrefix(name, "@@"); ok && slices.Contains(wellKnownMemberKeys, wk) {
		// A well-known symbol's member (`[Symbol.iterator]`): a constant
		// holding the symbol.
		g := "@__kml_wksym_slot_" + wk
		if !e.wkSymbolSlots[wk] {
			if e.wkSymbolSlots == nil {
				e.wkSymbolSlots = map[string]bool{}
			}
			e.wkSymbolSlots[wk] = true
			e.emitGlobal(fmt.Sprintf("%s = internal"+irGlobalAS+" constant "+irPtr+" %s, align 8", g, e.wellKnownSymbol(wk)))
		}
		return g, true
	}
	rest, ok := strings.CutPrefix(name, symbolFieldPrefix)
	if !ok {
		return "", false
	}
	_, key, ok := strings.Cut(rest, "\x02")
	if !ok {
		return "", false
	}
	g, ok := e.moduleGlobals[key]
	if !ok || !g.Ty.IsSymbol() {
		return "", false
	}
	return g.Ptr, true
}

// fieldPropertyKey is the property key a field is as a dynamic object's: its
// name, or for a symbol-keyed field its symbol's run-time key.
func (e *Emitter) fieldPropertyKey(name string) string {
	if g, ok := e.symbolFieldGlobal(name); ok {
		sym := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", sym, g))
		return e.emitSymbolPropertyKey(Value{Ref: sym, Ty: SymbolType()})
	}
	return e.internString(name)
}

// isInspectIdent reports a key util.inspect prints bare: a valid
// identifier of ASCII letters, digits, `_` and `$`, not starting with a
// digit (dynjson.c's key_is_ident).
func isInspectIdent(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		alpha := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r == '$'
		if !alpha && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}
