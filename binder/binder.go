package binder

import (
	"reflect"
	"sort"
	"strings"

	"KlainMainLang/diag"

	"KlainMainLang/ast"
)

// Binding is the binder's result for one program.
type Binding struct {
	Module *Scope
	// refs maps every identifier reference to the symbol it resolves to; an
	// identifier absent from the map (and listed in globals) names nothing
	// the program declares.
	refs    map[*ast.Identifier]*Symbol
	globals map[*ast.Identifier]bool
	// lookupScopes is the scope each unresolved reference was looked up
	// from.
	lookupScopes map[ast.Node]*Scope
	// newTargets maps a `new X(…)` to the symbol X resolves to.
	newTargets map[*ast.NewExpression]*Symbol
	// Conflicts lists the declarations that clash with an earlier symbol of
	// the same name in the same scope.
	Conflicts []Conflict
	symbols   []*Symbol
	// accesses lists every reference in source order; written marks the ones
	// that store without reading (the target of a plain `=` or of a
	// destructuring assignment). refFlow and refContainer give each
	// reference's flow node and the function (or module) scope it occurs in.
	accesses     []*ast.Identifier
	written      map[*ast.Identifier]bool
	refFlow      map[*ast.Identifier]*FlowNode
	refContainer map[*ast.Identifier]*Scope
	// stores lists, per symbol, the nodes that store to it (see StoresTo).
	stores map[*Symbol][]ast.Node
	// endReachable marks the function bodies whose end some path reaches.
	endReachable map[*ast.BlockStatement]bool
	// fnScopes maps each function-like node to the scope its parameters,
	// type parameters and body share.
	fnScopes map[ast.Node]*Scope
	// refOrd and declOrd place references and declarations in visiting
	// order; lastAssign is each symbol's last assignment in that order,
	// extended to its enclosing statement's end (maxOrd: assigned in a
	// nested function).
	refOrd     map[*ast.Identifier]int
	declOrd    map[*Symbol]int
	lastAssign map[*Symbol]int
	// endFlow is each function body's flow node at its end.
	endFlow map[*ast.BlockStatement]*FlowNode
	// exported marks the declarations an `export` wraps.
	exported map[ast.Node]bool
	// thisFlow and thisLocal are each `this` expression's flow node, and
	// whether it is in the function its `this` belongs to; thisSyms is the
	// symbol standing for each function's `this`, for narrowing `this.x`.
	thisFlow  map[ast.Expression]*FlowNode
	thisOwner map[ast.Expression]*Symbol
	thisLocal map[ast.Expression]bool
	thisSyms  map[*Scope]*Symbol
	// nsScopes is each namespace symbol's scope, where its members are
	// declared under their source names.
	nsScopes map[*Symbol]*Scope
	// Script marks a program without imports or exports: its top-level
	// bindings are globals.
	Script bool
	// Globals is the scope the builtin declarations are bound into, around
	// Module; nil when none were given.
	Globals *Scope
	// AmbientModules are the builtin declarations' modules (`declare module
	// "path" { … }`), each its own scope of exports.
	AmbientModules map[string]*Scope
	// Program is the program bound.
	Program *ast.Program
}

const maxOrd = int(^uint(0) >> 1)

// ScopeOf returns the scope a function-like node opens, or nil.
func (b *Binding) ScopeOf(fn ast.Node) *Scope { return b.fnScopes[fn] }

// Conflict is a declaration its scope already had an incompatible symbol for.
type Conflict struct {
	Symbol *Symbol
	Decl   Declaration
	Clash  Clash
}

// Resolve returns the symbol a reference resolves to. ok is false when the
// identifier is not a reference the binder saw; sym is nil for a reference to
// a name the program does not declare.
func (b *Binding) Resolve(id *ast.Identifier) (sym *Symbol, ok bool) {
	if s, found := b.refs[id]; found {
		return s, true
	}
	return nil, b.globals[id]
}

// LookupScope returns the scope an unresolved reference (an identifier, or a
// `new X(…)`) was looked up from (the innermost around it), or nil.
func (b *Binding) LookupScope(ref ast.Node) *Scope { return b.lookupScopes[ref] }

// NewTarget returns the symbol a `new X(…)` constructs, or nil for a name the
// program does not declare.
func (b *Binding) NewTarget(n *ast.NewExpression) *Symbol { return b.newTargets[n] }

// Symbols returns every symbol in creation order.
func (b *Binding) Symbols() []*Symbol { return b.symbols }

// scopeKey identifies a scope by the node that opens it; a node that opens
// two (a named function expression: its name, then its body) tells them
// apart by tag.
type scopeKey struct {
	node any
	tag  int
}

type binder struct {
	*Binding
	declaring bool // pass 1 declares; pass 2 resolves
	scopes    map[scopeKey]*Scope
	cur       *Scope
	// memberOf maps a top-level statement a namespace member desugared to
	// onto its innermost namespace; nsPrefix is the mangled-name prefix of
	// the namespace being bound (see namespaceOf).
	memberOf map[ast.Statement]*namespace
	nsPrefix string
	hoists   []hoist
	annexB   bool
	plain    bool // the function declaration being declared is plain

	// The flow graph under construction (the resolving pass only): the
	// current node, the function or module scope being walked, the targets
	// of break/continue, and a label to hand the next loop.
	flow      *FlowNode
	container *Scope
	// inBody is set while a function body's statements are declared into
	// bodyScope; inParams while its parameters' defaults are resolved in
	// paramScope.
	inBody, inParams      bool
	bodyScope, paramScope *Scope
	jumps                 []jumpTarget
	pendingLabel          string
	writing               bool // the identifiers being visited are assignment targets
	// storing is the node the next assignment flow node stores through.
	storing ast.Node
	// ord numbers the nodes in visiting order (the resolving pass); stmts
	// are the statements being visited that extend an assignment's
	// position to their end (see LastAssignment).
	ord   int
	stmts []stmtFrame
	// exprClass is the class expression's declaration being bound, and
	// exprMethod the method of it: a closure over its surroundings.
	exprClass  *ast.ClassDeclaration
	exprMethod ast.Node
	// iife is the function expression about to be bound in line (the
	// callee of the call being bound); returnTo collects the returns of the
	// inline function being bound; fnScope is the innermost function's
	// scope, inline ones included.
	iife     ast.Node
	returnTo *FlowNode
	fnScope  *Scope
}

// iifeCallee is the function a call invokes where it is written: a
// non-async, non-generator function expression or arrow function.
func iifeCallee(e ast.Expression) ast.Node {
	switch f := e.(type) {
	case *ast.FunctionExpression:
		if !f.IsAsync && !f.IsGenerator {
			return f
		}
	case *ast.ArrowFunction:
		if !f.IsAsync {
			return f
		}
	}
	return nil
}

// stmtFrame is a statement being visited: where it starts, and the symbols
// assigned inside it whose position it extends.
type stmtFrame struct {
	start    int
	assigned []*Symbol
}

// extendsAssignments reports the statements TypeScript's
// extendAssignmentPosition extends an assignment within to their end.
func extendsAssignments(n ast.Node) bool {
	switch n.(type) {
	case *ast.VarDeclaration, *ast.ExpressionStatement, *ast.IfStatement, *ast.DoWhileStatement, *ast.WhileStatement,
		*ast.ForStatement, *ast.ForInStatement, *ast.ForOfStatement, *ast.SwitchStatement, *ast.TryStatement, *ast.ClassDeclaration:
		return true
	}
	return false
}

// Options configure a binding.
type Options struct {
	// AnnexB applies sloppy-script rules (-compat=js): two plain function
	// declarations in one block merge.
	AnnexB bool
	// Script binds a program without imports or exports: its top-level
	// bindings are globals, which a closure never sees narrowed.
	Script bool
	// Lib are the builtin declaration files (TDD-00230 P3.1): bound first,
	// into Globals, the scope around the program's own.
	Lib []*ast.Program
}

// namespace is one namespace of the program: its dotted name, its parent,
// and the statement its scope is keyed by.
type namespace struct {
	name   string
	parent *namespace
	key    *ast.NamespaceGroup
	// instantiated marks a namespace that declares a value (TypeScript's
	// module instance state): its name is a value too.
	instantiated bool
}

// Bind binds prog: it declares every name in the scope that owns it, then
// resolves every identifier reference through the scope chain.
func Bind(prog *ast.Program) *Binding { return BindWith(prog, Options{}) }

// BindWith binds prog under opts.
func BindWith(prog *ast.Program, opts Options) *Binding {
	b := &binder{
		annexB: opts.AnnexB,
		Binding: &Binding{
			Program:        prog,
			refs:           map[*ast.Identifier]*Symbol{},
			globals:        map[*ast.Identifier]bool{},
			lookupScopes:   map[ast.Node]*Scope{},
			newTargets:     map[*ast.NewExpression]*Symbol{},
			refFlow:        map[*ast.Identifier]*FlowNode{},
			written:        map[*ast.Identifier]bool{},
			refContainer:   map[*ast.Identifier]*Scope{},
			stores:         map[*Symbol][]ast.Node{},
			endReachable:   map[*ast.BlockStatement]bool{},
			fnScopes:       map[ast.Node]*Scope{},
			refOrd:         map[*ast.Identifier]int{},
			declOrd:        map[*Symbol]int{},
			lastAssign:     map[*Symbol]int{},
			exported:       map[ast.Node]bool{},
			endFlow:        map[*ast.BlockStatement]*FlowNode{},
			thisFlow:       map[ast.Expression]*FlowNode{},
			thisOwner:      map[ast.Expression]*Symbol{},
			thisLocal:      map[ast.Expression]bool{},
			thisSyms:       map[*Scope]*Symbol{},
			nsScopes:       map[*Symbol]*Scope{},
			AmbientModules: map[string]*Scope{},
			Script:         opts.Script,
		},
		scopes: map[scopeKey]*Scope{},
	}
	b.memberOf = namespacesOf(prog)
	if len(opts.Lib) > 0 {
		b.Globals = &Scope{Kind: ModuleScope}
		for _, declaring := range []bool{true, false} {
			b.declaring = declaring
			b.cur = b.Globals
			b.container, b.fnScope = b.Globals, b.Globals
			b.flow = &FlowNode{Flags: FlowStart, Scope: b.Globals}
			for _, lp := range opts.Lib {
				// A declaration file's namespaces (`declare namespace NodeJS
				// { interface Process … }`) scope their members, as the
				// program's do.
				members := namespacesOf(lp)
				for _, st := range lp.Body {
					if ns := members[st]; ns != nil {
						b.inNamespace(ns, func() { b.visit(st) })
						continue
					}
					b.visit(st)
				}
			}
		}
		// The declarations' own references are no concern of the program's
		// flow checks.
		b.accesses = nil
	}
	for _, declaring := range []bool{true, false} {
		b.declaring = declaring
		b.cur = b.Globals
		b.Module = b.enter(prog, 0, ModuleScope)
		b.container, b.fnScope = b.Module, b.Module
		b.flow = &FlowNode{Flags: FlowStart, Scope: b.Module}
		for _, st := range prog.Body {
			if ns := b.memberOf[st]; ns != nil {
				b.inNamespace(ns, func() { b.visit(st) })
				continue
			}
			b.visit(st)
		}
		b.leave()
		if declaring {
			b.checkHoists()
		}
	}
	return b.Binding
}

// namespacesOf maps every namespace member statement to its innermost
// namespace. A namespace's group lists its nested namespaces' members too;
// a longer (more nested) name wins.
func namespacesOf(prog *ast.Program) map[ast.Statement]*namespace {
	byName := map[string]*namespace{}
	var get func(name string) *namespace
	get = func(name string) *namespace {
		if ns := byName[name]; ns != nil {
			return ns
		}
		ns := &namespace{name: name}
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			ns.parent = get(name[:i])
		}
		byName[name] = ns
		return ns
	}
	out := map[ast.Statement]*namespace{}
	for i := range prog.NamespaceGroups {
		g := &prog.NamespaceGroups[i]
		ns := get(g.Name)
		if ns.key == nil {
			ns.key = g
		}
		for _, st := range g.Members {
			if cur := out[st]; cur == nil || len(ns.name) > len(cur.name) {
				out[st] = ns
			}
		}
	}
	for st, ns := range out {
		if instantiates(st) {
			ns.markInstantiated()
		}
	}
	for _, a := range prog.NSAliases {
		// An exported alias (`export import a = A;`) is a value member.
		if ns := byName[a.Scope]; ns != nil && a.Exported {
			ns.markInstantiated()
		}
	}
	return out
}

func (ns *namespace) markInstantiated() {
	for n := ns; n != nil; n = n.parent {
		n.instantiated = true
	}
}

// instantiates reports whether a namespace member statement declares a
// value or runs code: anything but an interface, a type alias, or the empty
// statement an erased declaration (a nested empty namespace's) leaves.
func instantiates(st ast.Statement) bool {
	switch s := st.(type) {
	case *ast.InterfaceDeclaration, *ast.TypeAliasDeclaration:
		return false
	case *ast.BlockStatement:
		return len(s.Body) > 0
	case *ast.ExportDeclaration:
		return instantiates(s.Decl)
	}
	return true
}

// inNamespace binds f inside ns's scope (and its parents'), declaring each
// namespace's name in the scope around it. Inside, a member declared under
// its mangled top-level name is declared under the name the source gave it.
func (b *binder) inNamespace(ns *namespace, f func()) {
	var chain []*namespace
	for n := ns; n != nil; n = n.parent {
		chain = append([]*namespace{n}, chain...)
	}
	saved := b.nsPrefix
	for _, n := range chain {
		short := n.name[strings.LastIndexByte(n.name, '.')+1:]
		flag := TypeModule
		if n.instantiated {
			flag = NamespaceModule
		}
		if sym := b.cur.Symbols.Get(short); sym == nil || sym.Flags&flag == 0 {
			b.declare(b.cur, short, flag, NamespaceDecl, nil)
		}
		nsSym := b.cur.Symbols.Get(short)
		// An intermediate namespace of a dotted name (`A` and `A.B` of
		// `module A.B.C`) has no group of its own: it is keyed by its
		// record, never by a nil shared by all of them.
		var key any = n.key
		if n.key == nil {
			key = n
		}
		inner := b.enter(key, 0, NamespaceScope)
		if nsSym != nil {
			b.nsScopes[nsSym] = inner
		}
	}
	b.nsPrefix = ast.NamespaceMangle(ns.name, "")
	f()
	b.nsPrefix = saved
	for range chain {
		b.leave()
	}
}

// enter makes the scope node opens current, creating it on the first pass.
func (b *binder) enter(node any, tag int, kind ScopeKind) *Scope {
	k := scopeKey{node, tag}
	s := b.scopes[k]
	if s == nil {
		s = &Scope{Kind: kind, Parent: b.cur}
		s.Node, _ = node.(ast.Node)
		b.scopes[k] = s
	}
	b.cur = s
	return s
}

func (b *binder) leave() { b.cur = b.cur.Parent }

// functionScope is the nearest scope `var` declarations hoist to (a
// namespace body is one).
func (b *binder) functionScope() *Scope {
	s := b.cur
	for s.Kind != FunctionScope && s.Kind != ModuleScope && s.Kind != NamespaceScope {
		s = s.Parent
	}
	return s
}

// declare adds a declaration of name to scope s (on the first pass).
func (b *binder) declare(s *Scope, name string, flags SymbolFlags, kind DeclKind, node ast.Node) *Symbol {
	if !b.declaring || name == "" {
		return nil
	}
	if b.nsPrefix != "" && s.Kind == NamespaceScope {
		name = strings.TrimPrefix(name, b.nsPrefix)
	}
	d := Declaration{Node: node, Flags: flags, Kind: kind, Plain: b.plain, Body: b.inBody && s == b.bodyScope}
	if node != nil {
		d.Pos = node.GetPos()
	}
	if sym := s.Symbols.Get(name); sym != nil && !sym.Implicit {
		for _, prev := range sym.Declarations {
			if c := clashes(prev, d, b.annexB); c != NoClash {
				b.Conflicts = append(b.Conflicts, Conflict{Symbol: sym, Decl: d, Clash: c})
				break
			}
		}
		sym.Flags |= flags
		sym.Declarations = append(sym.Declarations, d)
		return sym
	}
	sym := &Symbol{ID: len(b.symbols) + 1, Name: name, Flags: flags, Declarations: []Declaration{d}, Scope: s}
	s.Symbols.put(sym)
	b.symbols = append(b.symbols, sym)
	return sym
}

// resolveName finds the symbol a value reference to name denotes, from the
// current scope outwards. In a parameter's default, the function's own scope
// offers only what its parameters declare.
func (b *binder) resolveName(name string) *Symbol {
	for s := b.cur; s != nil; s = s.Parent {
		sym := s.Symbols.Get(name)
		if sym == nil || sym.Flags&Value == 0 {
			continue
		}
		if b.inParams && s == b.paramScope && onlyBody(sym) {
			continue
		}
		return sym
	}
	return nil
}

// onlyBody reports whether every declaration of sym is a function body's.
func onlyBody(sym *Symbol) bool {
	if sym.Implicit || len(sym.Declarations) == 0 {
		return false
	}
	for _, d := range sym.Declarations {
		if !d.Body {
			return false
		}
	}
	return true
}

func (b *binder) statements(stmts []ast.Statement) {
	for _, s := range stmts {
		b.visit(s)
	}
}

// visit binds one node and everything under it.
func (b *binder) visit(n ast.Node) {
	if n == nil {
		return
	}
	if v := reflect.ValueOf(n); v.Kind() == reflect.Pointer && v.IsNil() {
		return
	}
	if ed, ok := n.(*ast.ExportDeclaration); ok {
		b.exported[ed.Decl] = true
	}
	if b.declaring {
		b.visitNode(n)
		return
	}
	b.ord++
	if !extendsAssignments(n) {
		b.visitNode(n)
		return
	}
	b.stmts = append(b.stmts, stmtFrame{start: b.ord})
	b.visitNode(n)
	top := b.stmts[len(b.stmts)-1]
	b.stmts = b.stmts[:len(b.stmts)-1]
	for _, sym := range top.assigned {
		if b.ord > b.lastAssign[sym] {
			b.lastAssign[sym] = b.ord
		}
	}
}

func (b *binder) visitNode(n ast.Node) {
	switch n := n.(type) {
	case *ast.AmbientModuleDeclaration:
		scope := b.enter(n, 0, ModuleScope)
		b.AmbientModules[n.Name] = scope
		for _, st := range n.Body {
			b.visit(st)
		}
		b.leave()
		return
	case *ast.ThisExpression, *ast.SuperExpression:
		// `super` stands on the same receiver as `this`.
		if !b.declaring {
			n := n.(ast.Expression)
			owner := thisScope(b.cur)
			sym := b.thisSyms[owner]
			if sym == nil {
				sym = &Symbol{Name: "this", Scope: owner}
				b.thisSyms[owner] = sym
			}
			b.thisOwner[n], b.thisFlow[n], b.thisLocal[n] = sym, b.flow, b.container == containerOf(owner)
		}
		return
	case *ast.Identifier:
		if !b.declaring {
			sym := b.resolveName(n.Name)
			if sym != nil {
				b.refs[n] = sym
			} else {
				b.globals[n] = true
				b.lookupScopes[n] = b.cur
			}
			b.refFlow[n] = b.flow
			b.refContainer[n] = b.container
			b.refOrd[n] = b.ord
			b.accesses = append(b.accesses, n)
			if b.writing {
				b.written[n] = true
				b.assignFlow(sym, false, true)
			}
		}
		return
	case *ast.NewExpression:
		if !b.declaring {
			if sym := b.resolveName(n.ClassName); sym != nil {
				b.newTargets[n] = sym
			} else {
				b.lookupScopes[n] = b.cur
			}
		}
	case *ast.BlockStatement:
		b.scopeStartFlow(b.enter(n, 0, BlockScope))
		b.statements(n.Body)
		b.leave()
		return
	case *ast.VarDeclaration:
		b.declareVariable(n.Kind, n.Name, n)
		b.visit(n.Init)
		b.storing = n
		defer func() { b.storing = nil }()
		if n.Kind != "var" {
			b.assignFlow(b.local(n.Name), true, n.Init != nil)
		} else if n.Init != nil {
			b.assignFlow(b.resolveName(b.unmangled(n.Name)), false, true)
		}
		return
	case *ast.ArrayDestructuring:
		names := arrayPatternNames(n.Elems)
		for _, name := range names {
			b.declareVariable(n.Kind, name, n)
		}
		b.visit(n.Init)
		b.bindPattern(n.Kind, n.Elems, nil)
		return
	case *ast.ObjectDestructuring:
		names := objectPatternNames(n.Props)
		for _, name := range names {
			b.declareVariable(n.Kind, name, n)
		}
		b.visit(n.Init)
		b.bindPattern(n.Kind, nil, n.Props)
		return
	case *ast.FunctionDeclaration:
		// A function declaration is scoped to its block (module code is
		// strict), or hoists to the function or module it sits directly in.
		if n.Body != nil {
			b.plain = !n.IsAsync && !n.IsGenerator
			b.declare(b.cur, n.Name, Function, b.functionDeclKind(), n)
			b.plain = false
		}
		b.function(n, n.Params, n.Body, nil, false)
		return
	case *ast.FunctionExpression:
		if n.Name != "" {
			b.enter(n, 1, NameScope)
			b.declare(b.cur, n.Name, Function, NameOnly, n)
		}
		b.function(n, n.Params, n.Body, nil, false)
		if n.Name != "" {
			b.leave()
		}
		return
	case *ast.ArrowFunction:
		b.function(n, n.Params, n.Block, n.Body, true)
		return
	case *ast.ClassDeclaration:
		b.declare(b.cur, n.Name, Class, Lexical, n)
		b.class(n)
		b.assignFlow(b.local(n.Name), true, true)
		return
	case *ast.ClassExpression:
		if n.Decl == nil {
			return
		}
		named := n.Decl.Name != "" && n.Decl.Name[0] != '$'
		if named {
			b.enter(n, 1, NameScope)
			b.declare(b.cur, n.Decl.Name, Class, NameOnly, n)
		}
		b.exprClass = n.Decl
		b.class(n.Decl)
		if named {
			b.leave()
		}
		return
	case *ast.EnumDeclaration:
		flags := RegularEnum
		if n.Const {
			flags = ConstEnum
		}
		b.declare(b.cur, n.Name, flags, Lexical, n)
		// A merged enum's declarations share one member scope: a member
		// initializer reads another declaration's members unqualified.
		var key ast.Node = n
		if sym := b.cur.Symbols.Get(n.Name); sym != nil {
			for _, d := range sym.Declarations {
				if ed, ok := d.Node.(*ast.EnumDeclaration); ok {
					key = ed
					break
				}
			}
		}
		b.enter(key, 0, EnumScope)
		for _, m := range n.Members {
			b.declare(b.cur, m.Name, EnumMember, Lexical, n)
		}
		for _, m := range n.Members {
			b.visit(m.Value)
		}
		b.leave()
		b.assignFlow(b.local(n.Name), true, true)
		return
	case *ast.InterfaceDeclaration:
		b.declare(b.cur, n.Name, Interface, TypeOnly, n)
		return
	case *ast.TypeAliasDeclaration:
		b.declare(b.cur, n.Name, TypeAlias, TypeOnly, n)
		return
	case *ast.ImportDeclaration:
		for _, sp := range n.Specifiers {
			b.declare(b.cur, sp.Local, Alias, Lexical, n)
		}
		b.declare(b.cur, n.Namespace, Alias, Lexical, n)
		return
	case *ast.ForStatement:
		b.scopeStartFlow(b.enter(n, 0, BlockScope))
		b.visit(n.Init)
		loop := b.newLabel(FlowLoopLabel, b.flow)
		b.flow = loop
		b.visit(n.Test)
		exit := b.newLabel(FlowBranchLabel)
		if !alwaysTrue(n.Test) {
			addAntecedent(exit, b.condition(b.flow, n.Test, false))
		}
		b.flow = b.condition(b.flow, n.Test, true)
		cont := b.newLabel(FlowBranchLabel)
		b.loopBody(exit, cont, func() { b.visitBody(n.Body) })
		addAntecedent(cont, b.flow)
		b.flow = finish(cont)
		for _, u := range n.Update {
			b.visit(u)
		}
		addAntecedent(loop, b.flow)
		b.flow = finish(exit)
		b.leave()
		return
	case *ast.WhileStatement:
		loop := b.newLabel(FlowLoopLabel, b.flow)
		b.flow = loop
		b.visit(n.Test)
		exit := b.newLabel(FlowBranchLabel)
		if !alwaysTrue(n.Test) {
			addAntecedent(exit, b.condition(b.flow, n.Test, false))
		}
		b.flow = b.condition(b.flow, n.Test, true)
		b.loopBody(exit, loop, func() { b.visitBody(n.Body) })
		addAntecedent(loop, b.flow)
		b.flow = finish(exit)
		return
	case *ast.DoWhileStatement:
		loop := b.newLabel(FlowLoopLabel, b.flow)
		b.flow = loop
		exit := b.newLabel(FlowBranchLabel)
		cont := b.newLabel(FlowBranchLabel)
		b.loopBody(exit, cont, func() { b.visitBody(n.Body) })
		addAntecedent(cont, b.flow)
		b.flow = finish(cont)
		b.visit(n.Test)
		addAntecedent(loop, b.condition(b.flow, n.Test, true))
		if !alwaysTrue(n.Test) {
			addAntecedent(exit, b.condition(b.flow, n.Test, false))
		}
		b.flow = finish(exit)
		return
	case *ast.IfStatement:
		b.visit(n.Test)
		pre := b.flow
		b.flow = b.condition(pre, n.Test, true)
		b.visitBody(n.Consequent)
		then := b.flow
		b.flow = b.condition(pre, n.Test, false)
		b.visit(n.Alternate)
		b.flow = b.join(then, b.flow)
		return
	case *ast.LabeledStatement:
		switch n.Body.(type) {
		case *ast.ForStatement, *ast.ForOfStatement, *ast.ForInStatement, *ast.WhileStatement, *ast.DoWhileStatement:
			// The loop takes the label: `continue label` targets it.
			b.pendingLabel = n.Label
			b.visit(n.Body)
			return
		}
		exit := b.newLabel(FlowBranchLabel)
		b.jumps = append(b.jumps, jumpTarget{label: n.Label, breakTo: exit, breakOnly: true})
		b.visit(n.Body)
		b.jumps = b.jumps[:len(b.jumps)-1]
		addAntecedent(exit, b.flow)
		b.flow = finish(exit)
		return
	case *ast.BreakStatement:
		if t := b.jumpTarget(n.Label, false); t != nil && b.flowing() {
			addAntecedent(t.breakTo, b.flow)
		}
		b.unreachable()
		return
	case *ast.ContinueStatement:
		if t := b.jumpTarget(n.Label, true); t != nil && b.flowing() {
			addAntecedent(t.continueTo, b.flow)
		}
		b.unreachable()
		return
	case *ast.ReturnStatement, *ast.ThrowStatement:
		ast.ForEachChild(n, func(c ast.Node) bool {
			b.visit(c)
			return true
		})
		if _, ret := n.(*ast.ReturnStatement); ret && b.returnTo != nil && b.flowing() {
			addAntecedent(b.returnTo, b.flow) // out of an inline function
		}
		b.unreachable()
		return
	case *ast.CallExpression:
		if fn := iifeCallee(n.Callee); fn != nil {
			// The arguments run first, then the body in line.
			for _, a := range n.Args {
				b.visit(a)
			}
			b.iife = fn
			b.visit(n.Callee)
			b.iife = nil
			return
		}
		ast.ForEachChild(n, func(c ast.Node) bool {
			b.visit(c)
			return true
		})
		// Every call through a dotted name may be an `asserts` call, in any
		// expression position (`(assert(x), x)`), as TypeScript binds it.
		if dottedName(n.Callee) && b.flowing() && b.flow != unreachableFlow {
			b.flow = &FlowNode{Flags: FlowCall, Antecedent: b.flow, Node: n}
		}
		return
	case *ast.AssignmentExpression:
		b.visitAssignment(n)
		return
	case *ast.UpdateExpression:
		b.visit(n.Arg)
		if id, ok := n.Arg.(*ast.Identifier); ok && b.flowing() {
			b.assignFlow(b.refs[id], false, true)
		}
		return
	case *ast.BinaryExpression:
		if n.Op == "&&" || n.Op == "||" || n.Op == "??" {
			b.visit(n.Left)
			pre := b.flow
			// The right operand runs when the left is truthy (&&) or falsy
			// (||); ?? is not a truthiness condition.
			short := pre
			if n.Op != "??" {
				b.flow = b.condition(pre, n.Left, n.Op == "&&")
				short = b.condition(pre, n.Left, n.Op != "&&")
			}
			b.visit(n.Right)
			b.flow = b.join(short, b.flow)
			return
		}
	case *ast.ConditionalExpression:
		b.visit(n.Test)
		pre := b.flow
		b.flow = b.condition(pre, n.Test, true)
		b.visit(n.Consequent)
		then := b.flow
		b.flow = b.condition(pre, n.Test, false)
		b.visit(n.Alternate)
		b.flow = b.join(then, b.flow)
		return
	case *ast.ForOfStatement:
		b.visit(n.Iterable)
		scope := b.enter(n, 0, BlockScope)
		names := append(append([]string{n.VarName}, arrayPatternNames(n.ArrayPattern)...), objectPatternNames(n.ObjectPattern)...)
		for _, name := range names {
			b.declareVariable(n.Kind, name, n)
		}
		loop := b.newLabel(FlowLoopLabel, b.flow)
		b.flow = loop
		exit := b.newLabel(FlowBranchLabel, b.flow)
		b.scopeStartFlow(scope)
		b.assignNames(n.Kind, []string{n.VarName})
		b.bindPattern(n.Kind, n.ArrayPattern, n.ObjectPattern)
		b.loopBody(exit, loop, func() { b.visitBody(n.Body) })
		addAntecedent(loop, b.flow)
		b.flow = finish(exit)
		b.leave()
		return
	case *ast.ForInStatement:
		b.visit(n.Object)
		scope := b.enter(n, 0, BlockScope)
		b.declareVariable(n.Kind, n.VarName, n)
		loop := b.newLabel(FlowLoopLabel, b.flow)
		b.flow = loop
		exit := b.newLabel(FlowBranchLabel, b.flow)
		b.scopeStartFlow(scope)
		b.storing = n // the store's node: the loop, which also asserts its object non-null
		b.assignNames(n.Kind, []string{n.VarName})
		b.loopBody(exit, loop, func() { b.visitBody(n.Body) })
		addAntecedent(loop, b.flow)
		b.flow = finish(exit)
		b.leave()
		return
	case *ast.SwitchStatement:
		b.visit(n.Discriminant)
		b.scopeStartFlow(b.enter(n, 0, BlockScope))
		pre := b.flow
		exit := b.newLabel(FlowBranchLabel)
		b.jumps = append(b.jumps, jumpTarget{label: b.takeLabel(), breakTo: exit})
		fallthroughFlow := unreachableFlow
		hasDefault := false
		for i, c := range n.Cases {
			if c.Test == nil {
				hasDefault = true
			}
			b.flow = b.join(b.switchClause(pre, n, i), fallthroughFlow)
			b.visit(c.Test)
			b.statements(c.Body)
			fallthroughFlow = b.flow
		}
		b.jumps = b.jumps[:len(b.jumps)-1]
		addAntecedent(exit, fallthroughFlow)
		if !hasDefault {
			addAntecedent(exit, b.switchClause(pre, n, -1))
		}
		b.flow = finish(exit)
		b.leave()
		return
	case *ast.TryStatement:
		pre := b.flow
		b.visit(n.Body)
		after := b.flow
		if c := n.Catch; c != nil {
			// An exception can leave the try block anywhere: the catch clause
			// is entered from the state before it (nothing the block did is
			// taken as done).
			b.flow = pre
			b.scopeStartFlow(b.enter(n, 1, CatchScope))
			b.declare(b.cur, c.Param, FunctionScopedVariable, VarLike, n)
			for _, name := range objectPatternNames(c.ObjectPattern) {
				b.declare(b.cur, name, FunctionScopedVariable, VarLike, n)
			}
			b.patternDefaults(nil, c.ObjectPattern)
			// The catch body shares the catch variable's scope: a lexical
			// declaration of the same name in it is a redeclaration.
			if c.Body != nil {
				b.statements(c.Body.Body)
			}
			b.leave()
			after = b.join(after, b.flow)
		}
		// finally runs on the way out of either; the paths it is reached on
		// by an exception are not modelled (a read there is not checked
		// against them).
		b.flow = after
		b.visit(n.Finally)
		return
	}
	ast.ForEachChild(n, func(c ast.Node) bool {
		b.visit(c)
		return true
	})
}

// dottedName reports an identifier, `this`, or a property access chain on
// one: the callees an assertion call can have.
func dottedName(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.Identifier, *ast.ThisExpression, *ast.SuperExpression:
		return true
	case *ast.MemberExpression:
		return !x.Optional && dottedName(x.Object)
	}
	return false
}

// visitBody binds a loop or catch body. Its block is a scope of its own,
// nested in the loop head's or the catch variable's.
func (b *binder) visitBody(body *ast.BlockStatement) {
	if body != nil {
		b.visit(body)
	}
}

// declareVariable declares a variable of kind var, let or const.
func (b *binder) declareVariable(kind, name string, node ast.Node) {
	if kind == "var" {
		target := b.functionScope()
		if sym := b.declare(target, name, FunctionScopedVariable, VarLike, node); sym != nil && b.cur != target {
			// Remember the scopes the var hoists through: one of them may
			// declare the name lexically (checked once every name is known).
			var through []*Scope
			for s := b.cur; s != target; s = s.Parent {
				through = append(through, s)
			}
			b.hoists = append(b.hoists, hoist{sym: sym, decl: sym.Declarations[len(sym.Declarations)-1], through: through})
		}
		return
	}
	b.declare(b.cur, name, BlockScopedVariable, Lexical, node)
}

// functionDeclKind is how a function declaration here binds: lexically in a
// block (module code is strict), like a var directly in a function,
// namespace or module.
func (b *binder) functionDeclKind() DeclKind {
	if b.cur.Kind == BlockScope || b.cur.Kind == CatchScope {
		return Lexical
	}
	return VarLike
}

// hoist is a var declared in a nested scope, and the scopes it hoists
// through to its function's.
type hoist struct {
	sym     *Symbol
	decl    Declaration
	through []*Scope
}

// checkHoists records a conflict for each var that hoists through a scope
// declaring the same name lexically (`let x; { var x }`).
func (b *binder) checkHoists() {
	for _, h := range b.hoists {
		for _, s := range h.through {
			sym := s.Symbols.Get(h.sym.Name)
			if sym == nil {
				continue
			}
			lexical := false
			for _, d := range sym.Declarations {
				lexical = lexical || d.Kind == Lexical
			}
			if lexical {
				b.Conflicts = append(b.Conflicts, Conflict{Symbol: sym, Decl: h.decl, Clash: Redeclaration})
				break
			}
		}
	}
}

// function binds a function-like node: its parameters and body share one
// scope, and a non-arrow function has an implicit `arguments`.
func (b *binder) function(node ast.Node, params []ast.Param, body *ast.BlockStatement, exprBody ast.Expression, arrow bool) {
	scope := b.enter(node, 0, FunctionScope)
	b.fnScopes[node] = scope
	if fd, ok := node.(*ast.FunctionDeclaration); ok {
		// Type parameters are types of the function's own scope: its
		// parameters' and result's annotations see them.
		for _, tp := range fd.TypeParams {
			b.declare(scope, tp, TypeParameter, TypeOnly, node)
		}
	}
	savedFlow, savedContainer, savedJumps, savedReturn, savedFn := b.flow, b.container, b.jumps, b.returnTo, b.fnScope
	// A non-async, non-generator function expression called where it is
	// written is part of the caller's flow, as TypeScript binds it: its body
	// continues from its arguments, and a return goes past the call.
	inline := node == b.iife && node != nil
	b.iife = nil
	b.fnScope, b.jumps = scope, nil
	if inline {
		scope.Inline = true
		b.returnTo = &FlowNode{Flags: FlowBranchLabel}
		b.scopeStartFlow(scope)
		defer func() {
			if b.flowing() {
				addAntecedent(b.returnTo, b.flow)
				b.flow = finish(b.returnTo)
			}
			b.jumps, b.returnTo, b.fnScope = savedJumps, savedReturn, savedFn
		}()
	} else {
		start := &FlowNode{Flags: FlowStart, Scope: scope}
		switch node.(type) {
		case *ast.FunctionExpression, *ast.ArrowFunction:
			start.Outer = savedFlow
		default:
			if node == b.exprMethod {
				start.Outer = savedFlow // a class expression's method or accessor
			}
		}
		b.flow, b.container, b.returnTo = start, scope, nil
		defer func() {
			b.flow, b.container, b.jumps, b.returnTo, b.fnScope = savedFlow, savedContainer, savedJumps, savedReturn, savedFn
		}()
	}
	if b.flowing() {
		for _, p := range params {
			if sym := scope.Symbols.Get(p.Name); sym != nil {
				b.declOrd[sym] = b.ord
			}
		}
	}
	if !arrow && b.declaring && scope.Symbols.Get("arguments") == nil {
		if s := b.declare(scope, "arguments", FunctionScopedVariable, NameOnly, nil); s != nil {
			s.Implicit = true
			s.Declarations = nil
		}
	}
	for _, p := range params {
		b.declare(scope, p.Name, FunctionScopedVariable, VarLike, node)
		for _, name := range arrayPatternNames(p.ArrayPattern) {
			b.declare(scope, name, FunctionScopedVariable, VarLike, node)
		}
		for _, name := range objectPatternNames(p.ObjectPattern) {
			b.declare(scope, name, FunctionScopedVariable, VarLike, node)
		}
	}
	savedParams, savedParamScope := b.inParams, b.paramScope
	b.inParams, b.paramScope = true, scope
	for _, p := range params {
		for _, d := range p.Decorators {
			b.visit(d)
		}
		b.visit(p.Default)
		b.patternDefaults(p.ArrayPattern, p.ObjectPattern)
	}
	b.inParams, b.paramScope = savedParams, savedParamScope
	savedBody, savedBodyScope := b.inBody, b.bodyScope
	b.inBody, b.bodyScope = true, scope
	if body != nil {
		b.statements(body.Body)
		if b.flowing() && b.flow != unreachableFlow {
			b.endReachable[body] = true
			b.endFlow[body] = b.flow
		}
	}
	b.visit(exprBody)
	b.inBody, b.bodyScope = savedBody, savedBodyScope
	b.leave()
}

// class binds a class body: member names are properties of the class, not
// scope bindings, so only the code inside members is visited.
func (b *binder) class(c *ast.ClassDeclaration) {
	for _, d := range c.Decorators {
		b.visit(d)
	}

	for _, f := range c.Fields {
		for _, d := range f.Decorators {
			b.visit(d)
		}
	}
	// Field initializers run when an instance is constructed, not where the
	// class is declared: they are a function of their own for the flow.
	init := b.enter(c, 2, FunctionScope)
	savedFlow, savedContainer := b.flow, b.container
	b.flow, b.container = &FlowNode{Flags: FlowStart, Scope: init}, init
	// A parameter property's field shares the parameter's default
	// (`constructor(public x = f())`): it is bound once, as the parameter.
	ctorDefaults := map[ast.Expression]bool{}
	if c.Constructor != nil {
		for _, p := range c.Constructor.Params {
			if p.Default != nil {
				ctorDefaults[p.Default] = true
			}
		}
	}
	for _, f := range c.Fields {
		if !ctorDefaults[f.Initializer] {
			b.visit(f.Initializer)
		}
	}
	b.flow, b.container = savedFlow, savedContainer
	b.leave()
	for _, a := range c.AutoAccessors {
		for _, d := range a.Decorators {
			b.visit(d)
		}
	}
	if c.Constructor != nil {
		b.function(c.Constructor, c.Constructor.Params, c.Constructor.Body, nil, false)
	}
	expr := b.exprClass == c
	b.exprClass = nil
	for _, m := range c.Methods {
		for _, d := range m.Decorators {
			b.visit(d)
		}
		if expr {
			b.exprMethod = m
		}
		b.function(m, m.Params, m.Body, nil, false)
	}
	for _, blk := range c.StaticBlocks {
		b.function(blk, nil, blk, nil, false)
	}
}

// patternDefaults visits the default-value expressions inside patterns.
func (b *binder) patternDefaults(arr []ast.ArrayPatternElem, obj []ast.DestructProp) {
	for _, e := range arr {
		b.visit(e.Default)
		b.patternDefaults(e.SubArray, e.SubObject)
	}
	for _, p := range obj {
		b.visit(p.Default)
		b.patternDefaults(p.SubArray, p.SubObject)
	}
}

func arrayPatternNames(elems []ast.ArrayPatternElem) []string {
	var out []string
	for _, e := range elems {
		switch {
		case e.SubArray != nil:
			out = append(out, arrayPatternNames(e.SubArray)...)
		case e.SubObject != nil:
			out = append(out, objectPatternNames(e.SubObject)...)
		case e.Name != "":
			out = append(out, e.Name)
		}
	}
	return out
}

func objectPatternNames(props []ast.DestructProp) []string {
	var out []string
	for _, p := range props {
		switch {
		case p.SubArray != nil:
			out = append(out, arrayPatternNames(p.SubArray)...)
		case p.SubObject != nil:
			out = append(out, objectPatternNames(p.SubObject)...)
		case p.Local != "":
			out = append(out, p.Local)
		}
	}
	return out
}

// Diagnostics reports each conflicting declaration, in source order, at the
// declaration that clashes with an earlier one (or that hoists through a
// lexical declaration of its name). offset gives a line and column's byte
// offset in the source; with nil, offsets are left unknown (-1).
func (b *Binding) Diagnostics(offset func(line, col int) int) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for _, c := range b.Conflicts {
		msg := diag.DuplicateIdentifier
		switch {
		case c.Clash == UnsupportedMerge:
			msg = diag.UnsupportedDeclarationMerge
		case c.Decl.Flags&BlockScopedVariable != 0 || c.Symbol.Flags&BlockScopedVariable != 0:
			msg = diag.RedeclaredBlockScoped
		}
		start := -1
		if offset != nil {
			start = offset(c.Decl.Pos.Line, c.Decl.Pos.Col)
		}
		out = append(out, diag.New(msg, diag.Span{Pos: diag.Pos{Line: c.Decl.Pos.Line, Col: c.Decl.Pos.Col}, Start: start, End: start}, c.Symbol.Name))
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Pos, out[j].Pos
		return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
	})
	return out
}

// local returns the symbol name declares in the current scope (under its
// source name, for a namespace member).
func (b *binder) local(name string) *Symbol {
	return b.cur.Symbols.Get(b.unmangled(name))
}

// unmangled is name as the current namespace scope declares it.
func (b *binder) unmangled(name string) string {
	if b.nsPrefix != "" && b.cur.Kind == NamespaceScope {
		return strings.TrimPrefix(name, b.nsPrefix)
	}
	return name
}

// assignNames records the destructured or loop-bound names of a declaration
// of kind as initialized and assigned.
func (b *binder) assignNames(kind string, names []string) {
	if !b.flowing() {
		return
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if kind == "var" {
			b.assignFlow(b.resolveName(name), false, true)
		} else {
			b.assignFlow(b.local(name), true, true)
		}
	}
}

// visitAssignment binds an assignment: the value, then the target. A plain
// `=` to a name stores without reading it; a compound one reads first; a
// destructuring target stores into each name it binds.
func (b *binder) visitAssignment(n *ast.AssignmentExpression) {
	switch l := n.Left.(type) {
	case *ast.Identifier:
		if n.Op == "&&=" || n.Op == "||=" || n.Op == "??=" {
			// A logical assignment reads the target, then stores the right
			// side only when the target is falsy (||=), truthy (&&=) or
			// nullish (??=); the other path keeps it as tested.
			b.visit(l)
			pre := b.flow
			test, stores := ast.Expression(l), n.Op == "&&="
			if n.Op == "??=" {
				test = nonNullTest(l)
				stores = false
			}
			b.flow = b.condition(pre, test, stores)
			b.visit(n.Right)
			b.storing = n
			if b.flowing() {
				b.assignFlow(b.refs[l], false, true)
			}
			b.storing = nil
			b.flow = b.join(b.condition(pre, test, !stores), b.flow)
			return
		}
		b.visit(n.Right)
		b.storing = n
		defer func() { b.storing = nil }()
		if n.Op == "=" {
			b.writing = true
			b.visit(l)
			b.writing = false
			return
		}
		b.visit(l) // compound: a read of the target
		if b.flowing() {
			b.assignFlow(b.refs[l], false, true)
		}
		return
	case *ast.ArrayLiteral, *ast.ObjectLiteral:
		b.visit(n.Right)
		b.visitTarget(l)
		return
	}
	b.visit(n.Left)
	b.visit(n.Right)
	if b.flowing() && b.flow != unreachableFlow && rootIdentifier(n.Left) {
		b.flow = &FlowNode{Flags: FlowPropertyAssignment, Antecedent: b.flow, Node: n}
	}
}

// rootIdentifier reports whether e is a property or element access chain
// that starts at a name.
func rootIdentifier(e ast.Expression) bool {
	switch e.(type) {
	case *ast.MemberExpression, *ast.IndexExpression:
	default:
		return false
	}
	for {
		switch x := e.(type) {
		case *ast.MemberExpression:
			e = x.Object
		case *ast.IndexExpression:
			e = x.Object
		case *ast.Identifier, *ast.ThisExpression:
			return true
		default:
			return false
		}
	}
}

// visitTarget binds a destructuring assignment target: each name in it is
// stored to; a member expression's object and a default are read.
func (b *binder) visitTarget(t ast.Expression) {
	switch t := t.(type) {
	case *ast.Identifier:
		// The name itself is the store: its value is the part of the right
		// side the pattern gives it.
		saved := b.storing
		b.storing, b.writing = t, true
		b.visit(t)
		b.storing, b.writing = saved, false
	case *ast.ArrayLiteral:
		for _, e := range t.Elements {
			b.visitTarget(e)
		}
	case *ast.ObjectLiteral:
		for _, p := range t.Properties {
			if p.KeyExpr != nil {
				b.visit(p.KeyExpr)
			}
			b.visitTarget(p.Value)
		}
	case *ast.AssignmentExpression: // a default: `[a = 1] = …`
		b.visit(t.Right)
		b.visitTarget(t.Left)
	case *ast.SpreadElement:
		b.visitTarget(t.Arg)
	default:
		b.visit(t)
	}
}

// loopBody binds a loop body with break going to exit and continue to cont.
func (b *binder) loopBody(exit, cont *FlowNode, body func()) {
	b.jumps = append(b.jumps, jumpTarget{label: b.takeLabel(), breakTo: exit, continueTo: cont})
	body()
	b.jumps = b.jumps[:len(b.jumps)-1]
}

// takeLabel returns the label a labeled statement handed the loop or switch
// being bound, and clears it.
func (b *binder) takeLabel() string {
	l := b.pendingLabel
	b.pendingLabel = ""
	return l
}

// jumpTarget finds where a break (or, with cont, a continue) with label
// goes: the innermost loop or switch for none, else the labeled statement.
func (b *binder) jumpTarget(label string, cont bool) *jumpTarget {
	for i := len(b.jumps) - 1; i >= 0; i-- {
		t := &b.jumps[i]
		switch {
		case label != "" && t.label != label:
			continue
		case cont && t.continueTo == nil:
			if label != "" {
				return nil
			}
			continue
		case label == "" && t.breakOnly:
			continue
		}
		return t
	}
	return nil
}

// unreachable marks the flow after a jump.
func (b *binder) unreachable() {
	if b.flowing() {
		b.flow = unreachableFlow
	}
}

// bindPattern binds a destructuring pattern of a declaration of kind in
// order: each element's default is evaluated, then its names are bound, so a
// later default may read an earlier element.
func (b *binder) bindPattern(kind string, arr []ast.ArrayPatternElem, obj []ast.DestructProp) {
	for _, e := range arr {
		b.visit(e.Default)
		switch {
		case e.SubArray != nil || e.SubObject != nil:
			b.bindPattern(kind, e.SubArray, e.SubObject)
		default:
			b.assignNames(kind, []string{e.Name})
		}
	}
	for _, p := range obj {
		b.visit(p.Default)
		switch {
		case p.SubArray != nil || p.SubObject != nil:
			b.bindPattern(kind, p.SubArray, p.SubObject)
		default:
			b.assignNames(kind, []string{p.Local})
		}
	}
}

// nonNullTest is the condition `x != null` a `x ??= v` tests: it is a flow
// condition's expression only, never part of the program.
func nonNullTest(x ast.Expression) ast.Expression {
	return ast.NewBinaryExpression("!=", x, ast.NewNullLiteral(false, x.GetPos()), x.GetPos())
}
