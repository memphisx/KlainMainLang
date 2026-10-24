package checker

import (
	"reflect"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
)

// UnusedOptions selects TypeScript's two unused-declaration checks.
type UnusedOptions struct {
	// Locals is noUnusedLocals: a local variable, function, class, enum,
	// interface, type alias or import nothing reads.
	Locals bool
	// Parameters is noUnusedParameters: a function's parameter or a
	// generic's type parameter nothing reads.
	Parameters bool
	// Script marks a file without imports or exports: its top-level
	// declarations are globals, which neither check covers.
	Script bool
	// Exported names the file's exported top-level declarations.
	Exported map[string]bool
}

// Unused reports the declarations of one file's binding that nothing reads,
// as tsc does under noUnusedLocals (TS6133, TS6196, TS6192, TS6198) and
// noUnusedParameters (TS6133, TS6205). A reference counts when it reads: a
// plain store (`x = 1`), and an increment or compound assignment whose
// value is discarded, do not; neither does a reference from inside the
// declaration it names (a recursive call, an interface naming itself).
func Unused(b *binder.Binding, opts UnusedOptions) []*diag.Diagnostic {
	if (!opts.Locals && !opts.Parameters) || b.Module == nil || b.Module.Node == nil {
		return nil
	}
	u := &unusedPass{b: b, used: map[*binder.Symbol]bool{}, discarded: map[*ast.Identifier]bool{},
		ambient: map[ast.Node]bool{}, exportList: map[string]bool{},
		classScopes: map[ast.Node]*binder.Scope{}, typeParams: map[ast.Node]map[string]*binder.Symbol{}, ownerUses: map[ast.Node]map[string]bool{}, varList: map[*ast.VarDeclaration]*ast.VarDeclarationList{}}
	for _, sym := range b.Symbols() {
		if sym.Flags&binder.TypeParameter == 0 || len(sym.Declarations) == 0 {
			continue
		}
		n := sym.Declarations[0].Node
		if c, ok := n.(*ast.ClassDeclaration); ok {
			u.classScopes[c] = sym.Scope
		}
		if u.typeParams[n] == nil {
			u.typeParams[n] = map[string]*binder.Symbol{}
		}
		u.typeParams[n][sym.Name] = sym
	}
	u.walk(b.Module.Node, b.Module, false)
	return u.report(opts)
}

type unusedPass struct {
	b    *binder.Binding
	used map[*binder.Symbol]bool
	// discarded marks a reference that is the target of an increment or a
	// compound assignment used as a statement: a write only.
	discarded map[*ast.Identifier]bool
	// selfs are the declarations enclosing the walk's current node whose own
	// name a reference there does not use (tsc's self-reference locations).
	selfs []ast.Node
	// ambient marks the declarations in an ambient context (`declare …`).
	ambient map[ast.Node]bool
	// exportList names the module-level names a local `export { … }` lists.
	exportList map[string]bool
	// classScopes is each generic class's type parameter scope, which its
	// member annotations see.
	classScopes map[ast.Node]*binder.Scope
	// typeParams are each generic declaration's type parameters by name.
	typeParams map[ast.Node]map[string]*binder.Symbol
	// owners are the generic interfaces and type aliases enclosing the walk's
	// current node, innermost last: the binder declares no symbols for
	// their type parameters, so ownerUses records each name used inside.
	owners    []ast.Node
	ownerUses map[ast.Node]map[string]bool
	// classes are the program's non-ambient class declarations.
	classes []*ast.ClassDeclaration
	// varList is the `let a, b` list each of its declarations is in.
	varList map[*ast.VarDeclaration]*ast.VarDeclarationList
}

func isNilNode(x ast.Node) bool {
	if x == nil {
		return true
	}
	v := reflect.ValueOf(x)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func (u *unusedPass) walk(x ast.Node, scope *binder.Scope, ambient bool) {
	if isNilNode(x) {
		return
	}
	if s := u.b.OpenedScope(x); s != nil {
		scope = s
	}
	if s := u.classScopes[x]; s != nil {
		scope = s
	}
	switch n := x.(type) {
	case *ast.ClassDeclaration:
		if !ambient {
			u.classes = append(u.classes, n)
		}
	case *ast.VarDeclarationList:
		for _, d := range n.Decls {
			u.varList[d] = n
		}
	case *ast.FunctionDeclaration:
		ambient = ambient || n.Ambient
	case *ast.VarDeclaration:
		ambient = ambient || n.Ambient
	case *ast.AmbientModuleDeclaration:
		ambient = true
	}
	if ambient {
		u.ambient[x] = true
	}
	switch x.(type) {
	case *ast.InterfaceDeclaration, *ast.TypeAliasDeclaration:
		if names, _ := ownTypeParams(x); len(names) > 0 {
			u.owners = append(u.owners, x)
			defer func() { u.owners = u.owners[:len(u.owners)-1] }()
		}
	}
	switch x.(type) {
	case *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.InterfaceDeclaration,
		*ast.EnumDeclaration, *ast.TypeAliasDeclaration:
		u.selfs = append(u.selfs, x)
		defer func() { u.selfs = u.selfs[:len(u.selfs)-1] }()
	}
	switch n := x.(type) {
	case *ast.Identifier:
		if sym, ok := u.b.Resolve(n); ok && !u.b.WriteOnly(n) && !u.discarded[n] {
			u.use(sym)
		}
	case *ast.ExpressionStatement:
		switch e := n.Expr.(type) {
		case *ast.UpdateExpression:
			if id, ok := e.Arg.(*ast.Identifier); ok {
				u.discarded[id] = true
			}
		case *ast.AssignmentExpression:
			if id, ok := e.Left.(*ast.Identifier); ok && e.Op != "=" {
				u.discarded[id] = true
			}
		}
	case *ast.ClassDeclaration:
		if n.BaseClass != "" {
			if n.BaseQualified {
				u.use(lookupName(n.BaseQualifier, scope))
			} else {
				u.use(lookupName(n.BaseClass, scope))
			}
		}
		for _, name := range n.Implements {
			u.useTypeName(strings.SplitN(name, ".", 2)[0], scope)
		}
	case *ast.NewExpression:
		u.use(u.b.NewTarget(n))
	case *ast.ExportFromDeclaration:
		if n.Source == "" {
			for _, sp := range n.Specifiers {
				u.exportList[sp.Imported] = true
			}
		}
	case *ast.TypeReference, *ast.TypeQuery:
		u.useTypeNode(x, scope)
	}
	forEachTypeNode(reflect.ValueOf(x), func(t ast.Node) { u.useTypeNode(t, scope) })
	ast.ForEachChild(x, func(ch ast.Node) bool { u.walk(ch, scope, ambient); return true })
}

// useTypeNode marks every name the type tree t refers to.
func (u *unusedPass) useTypeNode(t ast.Node, scope *binder.Scope) {
	ast.Inspect(t, func(tn ast.Node) bool {
		switch r := tn.(type) {
		case *ast.TypeReference:
			if len(r.Qualifier) > 0 {
				u.use(lookupName(r.Qualifier[0], scope))
			} else {
				u.useTypeName(r.Name, scope)
			}
		case *ast.TypeQuery:
			u.use(lookupName(r.Name, scope))
		}
		return true
	})
}

// useTypeName marks the symbol a type reference's name denotes: its type
// meaning, else (a value used as a type, reported elsewhere) its value.
func (u *unusedPass) useTypeName(name string, scope *binder.Scope) {
	for i := len(u.owners) - 1; i >= 0; i-- {
		o := u.owners[i]
		if names, _ := ownTypeParams(o); indexOf(names, name) >= 0 {
			if u.ownerUses[o] == nil {
				u.ownerUses[o] = map[string]bool{}
			}
			u.ownerUses[o][name] = true
			return
		}
	}
	if sym := resolveTypeName(name, scope); sym != nil {
		u.use(sym)
		return
	}
	u.use(lookupName(name, scope))
}

func (u *unusedPass) use(sym *binder.Symbol) {
	if sym == nil {
		return
	}
	for _, d := range sym.Declarations {
		// Only the declaration's own name: a parameter or type parameter
		// shares its function's node.
		if d.Flags&(binder.Function|binder.Class|binder.Interface|binder.Enum|binder.TypeAlias) == 0 {
			continue
		}
		for _, s := range u.selfs {
			if d.Node == s {
				return // a reference from inside the declaration itself
			}
		}
	}
	u.used[sym] = true
}

// ownTypeParams returns the type parameters an interface or type alias
// declares, which the binder does not, and their positions.
func ownTypeParams(n ast.Node) ([]string, []ast.Pos) {
	var tps []*ast.TypeParameter
	switch d := n.(type) {
	case *ast.InterfaceDeclaration:
		tps = d.TypeParameters
	case *ast.TypeAliasDeclaration:
		tps = d.TypeParameters
	default:
		return nil, nil
	}
	var names []string
	var poss []ast.Pos
	for _, tp := range tps {
		names = append(names, tp.Name)
		poss = append(poss, tp.Range.Pos)
	}
	return names, poss
}

var typeParameterPtr = reflect.TypeOf((*ast.TypeParameter)(nil))

var (
	typeNodeIface   = reflect.TypeOf((*ast.TypeNode)(nil)).Elem()
	typeMemberIface = reflect.TypeOf((*ast.TypeMember)(nil)).Elem()
	nodeIface       = reflect.TypeOf((*ast.Node)(nil)).Elem()
)

// forEachTypeNode calls fn with each type tree held in v's fields — a type
// node or type member as written, or a TS annotation's node — without
// descending into the statements and expressions the child walk visits.
func forEachTypeNode(v reflect.Value, fn func(ast.Node)) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		if v.Type() == typeAnnotationPtr {
			if ta := v.Interface().(*ast.TypeAnnotation); ta.Source != "jsdoc" && ta.TypeNode() != nil {
				fn(ta.TypeNode())
			}
			return
		}
		if v.Kind() == reflect.Pointer && (v.Type().Implements(typeNodeIface) || v.Type().Implements(typeMemberIface)) {
			fn(v.Interface().(ast.Node))
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < v.NumField(); i++ {
		if !v.Type().Field(i).IsExported() {
			continue
		}
		forEachTypeField(v.Field(i), fn)
	}
}

func forEachTypeField(f reflect.Value, fn func(ast.Node)) {
	switch f.Kind() {
	case reflect.Pointer, reflect.Interface:
		if f.IsNil() {
			return
		}
		t := f.Type()
		if f.Kind() == reflect.Interface {
			t = f.Elem().Type()
		}
		if t == typeAnnotationPtr || t.Implements(typeNodeIface) || t.Implements(typeMemberIface) {
			forEachTypeNode(f, fn)
		}
		if t == typeParameterPtr {
			tp := f.Interface().(*ast.TypeParameter)
			if tp.Constraint != nil {
				fn(tp.Constraint)
			}
			if tp.Default != nil {
				fn(tp.Default)
			}
		}
		// Any other node is a statement or expression: the child walk's.
		if !t.Implements(nodeIface) && t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
			forEachTypeNode(f, fn)
		}
	case reflect.Struct:
		forEachTypeNode(f, fn)
	case reflect.Slice:
		for j := 0; j < f.Len(); j++ {
			forEachTypeField(f.Index(j), fn)
		}
	}
}

// unusedEntry is one unused declaration to report.
type unusedEntry struct {
	pos  ast.Pos
	msg  *diag.Message
	name string
}

// unusedGroup is a set of declarations reported once when all are unused
// (TS6192, TS6198, TS6199, TS6205) and one by one otherwise.
type unusedGroup struct {
	total  int
	unused []unusedEntry
	pos    ast.Pos
	all    *diag.Message
}

func (u *unusedPass) report(opts UnusedOptions) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	add := func(m *diag.Message, pos ast.Pos, args ...any) {
		d := diag.New(m, diag.Span{Pos: diag.Pos{Line: pos.Line, Col: pos.Col}}, args...)
		d.Hint = strictHint
		out = append(out, d)
	}
	groups := map[any]*unusedGroup{}
	var order []any
	grouped := func(key any, pos ast.Pos, all *diag.Message, total int) *unusedGroup {
		g := groups[key]
		if g == nil {
			g = &unusedGroup{pos: pos, all: all, total: total}
			groups[key] = g
			order = append(order, key)
		}
		return g
	}
	// binding reports a destructured name: an array element of a local one
	// by one, an object pattern's elements (and a parameter's array
	// pattern's) as a group.
	binding := func(pe patElem, name string, param bool) {
		if pe.exempt(name) {
			return
		}
		if pe.array && !param {
			add(diag.UnusedNeverRead, pe.pos, name)
			return
		}
		g := grouped(pe.key, pe.gpos, diag.UnusedAllDestructured, pe.total)
		g.unused = append(g.unused, unusedEntry{pe.pos, diag.UnusedNeverRead, name})
	}
	for _, sym := range u.b.Symbols() {
		if sym.Implicit || sym.Scope == nil || len(sym.Declarations) == 0 {
			continue
		}
		if sym.Flags&(binder.Property|binder.EnumMember) != 0 || strings.HasPrefix(sym.Name, "__") || strings.Contains(sym.Name, "__kmlns_") {
			continue
		}
		if !u.inProgram(sym.Scope) {
			continue // the builtin declarations'
		}
		d := sym.Declarations[0]
		if d.Node == nil || d.Kind == binder.NameOnly || u.ambient[d.Node] {
			continue
		}
		name := ast.Unmangle(sym.Name)
		switch sym.Scope.Kind {
		case binder.NamespaceScope, binder.EnumScope, binder.NameScope:
			continue
		case binder.ModuleScope:
			if opts.Script || opts.Exported[name] || opts.Exported[sym.Name] || u.exportList[sym.Name] || u.exported(sym) {
				continue
			}
		}
		used := u.used[sym] || sym.Flags&binder.TypeParameter != 0 && u.mergedTypeParamUsed(sym)
		// Type parameters: noUnusedParameters, grouped per declaration.
		if sym.Flags&binder.TypeParameter != 0 {
			if !opts.Parameters || u.bodiless(d.Node) {
				continue
			}
			names, poss := typeParamsOf(d.Node)
			idx := indexOf(names, sym.Name)
			if idx < 0 {
				continue
			}
			total := 0
			for _, n := range names {
				if !strings.HasPrefix(n, "_") {
					total++
				}
			}
			g := grouped(d.Node, poss[0], diag.UnusedAllTypeParams, total)
			if !used && !strings.HasPrefix(name, "_") {
				g.unused = append(g.unused, unusedEntry{poss[idx], diag.UnusedNeverRead, name})
			}
			continue
		}
		if used {
			continue
		}
		switch n := d.Node.(type) {
		case *ast.TryStatement:
			continue // a catch variable
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
			ps, hasBody := funcParams(n)
			p, ok := findParam(ps, sym.Name)
			if !ok {
				// A function declaration's own name.
				if fd, isDecl := n.(*ast.FunctionDeclaration); isDecl && fd.Name == sym.Name && opts.Locals {
					add(diag.UnusedNeverRead, namePosOf(fd, fd.GetPos()), name)
				}
				continue
			}
			if !opts.Parameters || !hasBody || p.Name == "this" || p.PropVisibility != "" || p.PropReadonly {
				continue
			}
			if p.Name == sym.Name {
				if !strings.HasPrefix(name, "_") {
					add(diag.UnusedNeverRead, namePos(p.Pos, n.GetPos()), name)
				}
				continue
			}
			if strings.HasPrefix(name, "_") {
				continue
			}
			if pe, ok := findPattern(sym.Name, p.ArrayPattern, p.ObjectPattern, namePos(p.Pos, n.GetPos())); ok {
				binding(pe, name, true)
			}
			continue
		}
		if !opts.Locals {
			continue
		}
		switch n := d.Node.(type) {
		case *ast.ImportDeclaration:
			if strings.HasPrefix(name, "_") {
				continue
			}
			total := len(n.Specifiers)
			if n.Namespace != "" {
				total++
			}
			m := diag.UnusedNeverRead
			if n.TypeOnly || specTypeOnly(n, sym.Name) {
				m = diag.UnusedNeverUsed
			}
			g := grouped(n, n.GetPos(), diag.UnusedAllImports, total)
			g.unused = append(g.unused, unusedEntry{n.GetPos(), m, name})
		case *ast.ArrayDestructuring:
			if pe, ok := findPattern(sym.Name, n.Elems, nil, n.GetPos()); ok {
				binding(pe, name, false)
			}
		case *ast.ObjectDestructuring:
			if pe, ok := findPattern(sym.Name, nil, n.Props, n.GetPos()); ok {
				binding(pe, name, false)
			}
		case *ast.ForOfStatement:
			if n.VarName == sym.Name {
				if !strings.HasPrefix(name, "_") {
					add(diag.UnusedNeverRead, n.GetPos(), name)
				}
			} else if pe, ok := findPattern(sym.Name, n.ArrayPattern, n.ObjectPattern, n.GetPos()); ok {
				binding(pe, name, false)
			}
		case *ast.ForInStatement:
			if !strings.HasPrefix(name, "_") {
				add(diag.UnusedNeverRead, n.GetPos(), name)
			}
		case *ast.ClassDeclaration, *ast.InterfaceDeclaration, *ast.TypeAliasDeclaration, *ast.EnumDeclaration:
			add(diag.UnusedNeverUsed, namePosOf(n, n.GetPos()), name)
		case *ast.VarDeclaration:
			pos := namePosOf(n, n.GetPos())
			if list := u.varList[n]; list != nil {
				g := grouped(list, list.GetPos(), diag.UnusedAllVariables, len(list.Decls))
				g.unused = append(g.unused, unusedEntry{pos, diag.UnusedNeverRead, name})
				continue
			}
			add(diag.UnusedNeverRead, pos, name)
		}
	}
	if opts.Locals {
		for _, c := range u.classes {
			for _, e := range unusedPrivateMembers(c, u.b) {
				add(e.msg, e.pos, e.name)
			}
		}
	}
	if opts.Parameters {
		for _, sym := range u.b.Symbols() {
			if sym.Flags&(binder.Interface|binder.TypeAlias) == 0 || !u.inProgram(sym.Scope) {
				continue
			}
			for _, d := range sym.Declarations {
				names, poss := ownTypeParams(d.Node)
				if len(names) == 0 || u.ambient[d.Node] {
					continue
				}
				total := 0
				for _, n := range names {
					if !strings.HasPrefix(n, "_") {
						total++
					}
				}
				g := grouped(d.Node, poss[0], diag.UnusedAllTypeParams, total)
				for i, n := range names {
					if strings.HasPrefix(n, "_") || u.ownerUses[d.Node][n] || u.mergedUse(sym, d.Node, n) {
						continue
					}
					g.unused = append(g.unused, unusedEntry{poss[i], diag.UnusedNeverRead, n})
				}
			}
		}
	}
	for _, key := range order {
		g := groups[key]
		if len(g.unused) == 0 {
			continue
		}
		if len(g.unused) == g.total && g.total > 1 {
			add(g.all, g.pos)
			continue
		}
		for _, e := range g.unused {
			add(e.msg, e.pos, e.name)
		}
	}
	return out
}

// mergedTypeParamUsed reports a type parameter whose namesake in another
// declaration merged with its owner (`class C<T>` and `interface C<T>`) is
// used: TypeScript merges them into one.
func (u *unusedPass) mergedTypeParamUsed(sym *binder.Symbol) bool {
	n := sym.Declarations[0].Node
	v := reflect.ValueOf(n)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	f := v.FieldByName("Name")
	if !f.IsValid() || f.Kind() != reflect.String || f.String() == "" {
		return false
	}
	owner := lookupName(f.String(), sym.Scope)
	if owner == nil {
		return false
	}
	return u.mergedUse(owner, n, sym.Name)
}

// mergedUse reports a use of type parameter name in a declaration of owner
// other than n.
func (u *unusedPass) mergedUse(owner *binder.Symbol, n ast.Node, name string) bool {
	for _, d := range owner.Declarations {
		if d.Node != n && (u.used[u.typeParams[d.Node][name]] || u.ownerUses[d.Node][name]) {
			return true
		}
	}
	return false
}

// inProgram reports a scope of the program's own: its module scope or one
// within it.
func (u *unusedPass) inProgram(s *binder.Scope) bool {
	for ; s != nil; s = s.Parent {
		if s == u.b.Module {
			return true
		}
	}
	return false
}

// exported reports a module-level declaration an `export` wraps.
func (u *unusedPass) exported(sym *binder.Symbol) bool {
	for _, d := range sym.Declarations {
		if u.b.Exported(d.Node) {
			return true
		}
	}
	return false
}

// bodiless reports a function-like declaration without a body: an overload
// signature, an abstract or ambient method, whose names nothing can read.
func (u *unusedPass) bodiless(n ast.Node) bool {
	if fd, ok := n.(*ast.FunctionDeclaration); ok {
		return fd.Body == nil || fd.Ambient
	}
	return false
}

func funcParams(n ast.Node) (ps []ast.Param, hasBody bool) {
	switch f := n.(type) {
	case *ast.FunctionDeclaration:
		return f.Params, f.Body != nil && !f.Ambient
	case *ast.FunctionExpression:
		return f.Params, f.Body != nil
	case *ast.ArrowFunction:
		return f.Params, true
	}
	return nil, false
}

// findParam finds the parameter that binds name, itself or in its pattern.
func findParam(ps []ast.Param, name string) (ast.Param, bool) {
	for _, p := range ps {
		if p.Name == name {
			return p, true
		}
		if _, ok := findPattern(name, p.ArrayPattern, p.ObjectPattern, p.Pos); ok {
			return p, true
		}
	}
	return ast.Param{}, false
}

// patElem is where a destructuring pattern binds a name: the element's
// position, the innermost pattern holding it (key, its element count and
// position), and how it binds — "array" (an element), "renamed"
// (`{ key: name }`), "plain" (`{ name }`), "rest-sibling" (an element of an
// object pattern ending in a rest, which removes its key from the rest) or
// "rest".
type patElem struct {
	kind  string
	pos   ast.Pos
	key   any
	total int
	gpos  ast.Pos
	array bool
}

// exempt reports a destructured name tsc leaves alone when unused: an
// `_`-prefixed array element or renamed property, and an element whose
// pattern ends in a rest.
func (pe patElem) exempt(name string) bool {
	switch pe.kind {
	case "rest-sibling":
		return true
	case "array", "renamed":
		return strings.HasPrefix(name, "_")
	}
	return false
}

func findPattern(name string, arr []ast.ArrayPatternElem, obj []ast.DestructProp, gpos ast.Pos) (patElem, bool) {
	for _, e := range arr {
		if e.SubArray != nil || e.SubObject != nil {
			if pe, ok := findPattern(name, e.SubArray, e.SubObject, namePos(e.Pos, gpos)); ok {
				return pe, true
			}
			continue
		}
		if e.Name == name {
			return patElem{kind: "array", pos: namePos(e.Pos, gpos), key: &arr[0], total: len(arr), gpos: gpos, array: true}, true
		}
	}
	hasRest := len(obj) > 0 && obj[len(obj)-1].Rest
	for _, p := range obj {
		if p.SubArray != nil || p.SubObject != nil {
			// A nested pattern is reported where its property is.
			if pe, ok := findPattern(name, p.SubArray, p.SubObject, namePos(p.Pos, gpos)); ok {
				return pe, true
			}
			continue
		}
		if p.Local != name {
			continue
		}
		pe := patElem{pos: namePos(p.Pos, gpos), key: &obj[0], total: len(obj), gpos: gpos}
		switch {
		case p.Rest:
			pe.kind = "rest"
		case hasRest:
			pe.kind = "rest-sibling"
		case p.Key != p.Local:
			pe.kind = "renamed"
		default:
			pe.kind = "plain"
		}
		return pe, true
	}
	return patElem{}, false
}

func specTypeOnly(n *ast.ImportDeclaration, local string) bool {
	for _, sp := range n.Specifiers {
		if sp.Local == local {
			return sp.TypeOnly
		}
	}
	return false
}

// typeParamsOf returns a generic declaration's type parameter names and
// their positions (the declaration's own where none was recorded).
func typeParamsOf(n ast.Node) ([]string, []ast.Pos) {
	v := reflect.ValueOf(n)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	var names []string
	var poss []ast.Pos
	if f := v.FieldByName("TypeParameters"); f.IsValid() && f.Len() > 0 {
		for i := 0; i < f.Len(); i++ {
			tp := f.Index(i).Interface().(*ast.TypeParameter)
			names = append(names, tp.Name)
			poss = append(poss, tp.Range.Pos)
		}
		return names, poss
	}
	if f := v.FieldByName("TypeParams"); f.IsValid() {
		for i := 0; i < f.Len(); i++ {
			names = append(names, f.Index(i).String())
			poss = append(poss, n.GetPos())
		}
	}
	if len(poss) == 0 {
		poss = []ast.Pos{n.GetPos()}
	}
	return names, poss
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// namePosOf is a declaration's NamePos, where it records one, else def.
func namePosOf(n ast.Node, def ast.Pos) ast.Pos {
	v := reflect.ValueOf(n)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if f := v.FieldByName("NamePos"); f.IsValid() {
		if p, ok := f.Interface().(ast.Pos); ok && p.Line > 0 {
			return p
		}
	}
	return def
}

// unusedPrivateMembers are a class's private members (`private`, `#name`,
// a `private` parameter property) that nothing in the class reads. A read
// is an access by name: `this.x`, `o.x`, `o["x"]`, a destructured key. A
// store does not read (a setter's does), nor does a method's access to
// itself.
func unusedPrivateMembers(c *ast.ClassDeclaration, b *binder.Binding) []unusedEntry {
	type member struct {
		name   string
		pos    ast.Pos
		method *ast.FunctionDeclaration
		param  bool
		setter bool
	}
	var members []member
	private := func(vis, name string) bool { return vis == "private" || strings.HasPrefix(name, "#") }
	getters := map[string]bool{}
	for _, m := range c.Methods {
		if m.AccessorKind == "get" {
			getters[m.Name] = true
		}
	}
	propParams := map[string]bool{}
	if c.Constructor != nil {
		for _, p := range c.Constructor.Params {
			propParams[p.Name] = p.IsPropertyParam()
		}
	}
	for _, f := range c.Fields {
		if private(f.Visibility, f.Name) && !propParams[f.Name] {
			members = append(members, member{name: f.Name, pos: f.Pos})
		}
	}
	for _, m := range c.Methods {
		if !private(m.Visibility, m.Name) || m.Ambient {
			continue
		}
		if m.AccessorKind == "set" && getters[m.Name] {
			continue // reported on the getter
		}
		members = append(members, member{name: m.Name, pos: namePosOf(m, m.GetPos()), method: m, setter: m.AccessorKind == "set"})
	}
	if c.Constructor != nil {
		for _, p := range c.Constructor.Params {
			if p.PropVisibility == "private" {
				members = append(members, member{name: p.Name, pos: namePos(p.Pos, c.Constructor.GetPos()), param: true})
			}
		}
	}
	if len(members) == 0 {
		return nil
	}
	setters := map[string]bool{}
	for _, m := range c.Methods {
		if m.AccessorKind == "set" {
			setters[m.Name] = true
		}
	}
	// reads[name] lists the methods (nil outside one) each read occurs in.
	reads := map[string][]*ast.FunctionDeclaration{}
	anyKey := false
	writes := map[ast.Node]bool{}
	var visit func(n ast.Node, in *ast.FunctionDeclaration)
	visit = func(n ast.Node, in *ast.FunctionDeclaration) {
		if isNilNode(n) {
			return
		}
		if fd, ok := n.(*ast.FunctionDeclaration); ok {
			for _, m := range c.Methods {
				if m == fd {
					in = fd
				}
			}
		}
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			if x.Op == "=" {
				writes[x.Left] = true
			}
			// `({ x } = this)` reads x from the source.
			if lit, ok := x.Left.(*ast.ObjectLiteral); ok && x.Op == "=" {
				for _, p := range lit.Properties {
					if p.KeyExpr == nil && p.Key != "" {
						reads[p.Key] = append(reads[p.Key], in)
					}
				}
			}
		case *ast.ExpressionStatement:
			switch e := x.Expr.(type) {
			case *ast.UpdateExpression:
				writes[e.Arg] = true
			case *ast.AssignmentExpression:
				writes[e.Left] = true
			}
		case *ast.MemberExpression:
			if !writes[x] || setters[x.Property] {
				reads[x.Property] = append(reads[x.Property], in)
			}
		case *ast.IndexExpression:
			if lit, ok := x.Index.(*ast.StringLiteral); ok {
				if !writes[x] || setters[lit.Value] {
					reads[lit.Value] = append(reads[lit.Value], in)
				}
				break
			}
			// A computed key reads the names its declared literal type
			// lists; any other key may read every member.
			keys, known := literalKeys(x.Index, b)
			if !known {
				anyKey = true
			}
			for _, k := range keys {
				reads[k] = append(reads[k], in)
			}
		case *ast.ObjectDestructuring:
			for _, p := range x.Props {
				reads[p.Key] = append(reads[p.Key], in)
			}
		}
		ast.ForEachChild(n, func(ch ast.Node) bool { visit(ch, in); return true })
	}
	visit(c, nil)
	if anyKey {
		return nil
	}
	var out []unusedEntry
	for _, m := range members {
		read := false
		for _, in := range reads[m.name] {
			if m.method == nil || in != m.method {
				read = true
				break
			}
		}
		if read {
			continue
		}
		if m.param {
			out = append(out, unusedEntry{m.pos, diag.UnusedProperty, m.name})
		} else {
			out = append(out, unusedEntry{m.pos, diag.UnusedNeverRead, m.name})
		}
	}
	return out
}

// literalKeys are the property names a computed key can be: those of the
// string-literal type a parameter or variable key is declared with.
func literalKeys(e ast.Expression, b *binder.Binding) ([]string, bool) {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return nil, false
	}
	sym, ok := b.Resolve(id)
	if !ok || len(sym.Declarations) == 0 {
		return nil, false
	}
	var ta *ast.TypeAnnotation
	switch n := sym.Declarations[0].Node.(type) {
	case *ast.VarDeclaration:
		ta = n.TypeAnnot
	case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
		ps, _ := funcParams(n)
		for _, p := range ps {
			if p.Name == sym.Name {
				ta = p.Type
			}
		}
	}
	if ta == nil {
		return nil, false
	}
	var keys []string
	if ta.IsStringLiteral {
		keys = append(keys, ta.LiteralValue)
	} else if len(ta.UnionMembers) == 0 {
		return nil, false
	}
	for _, m := range ta.UnionMembers {
		if m == nil || !m.IsStringLiteral {
			return nil, false
		}
		keys = append(keys, m.LiteralValue)
	}
	return keys, true
}
