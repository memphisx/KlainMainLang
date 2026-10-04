package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/resolver"
)

// emit_classexpr.go — a class expression as a value (`make(class { … })`,
// `return class R { … }`, a nested binding).
//
// A class expression that captures no enclosing function's locals is the
// same class wherever it is evaluated, so it is hoisted to a top-level class
// under a name of its own and the expression becomes a reference to it: a
// constructor reference, as a class name in value position is. Its source
// name (or the binding's, for `const K = class {}`) is what `.name` and
// util.inspect show.

// hoistClassExpressions hoists every class expression in prog that captures
// no function local; the rest stay in place and are rejected at emission.
func (e *Emitter) hoistClassExpressions(prog *ast.Program) {
	c := e.front()
	if c == nil {
		return
	}
	b := c.Binding()
	var hoisted []ast.Statement
	boundTo := map[*ast.ClassExpression]*ast.VarDeclaration{}
	// hoistedDecls is every block-level class declaration hoisted so far: a
	// later class naming one (`class B extends A`, both in one function)
	// names a top-level class, not a local.
	hoistedDecls := map[ast.Node]bool{}
	var rewrite func(n ast.Node) ast.Node
	rewrite = func(n ast.Node) ast.Node {
		// `function h() { class D {} return new D() }`: a class declared in a
		// block that captures none of its locals is hoisted like a class
		// expression, and the block's references renamed to it.
		if blk, ok := n.(*ast.BlockStatement); ok {
			kept := blk.Body[:0:0]
			var mine []*ast.ClassDeclaration
			renames := map[string]string{}
			atOf := map[*ast.ClassDeclaration]*ast.ClassDeclaration{}
			origLen := len(blk.Body)
			for _, st := range blk.Body {
				cd, ok := st.(*ast.ClassDeclaration)
				if !ok || len(cd.Decorators) > 0 {
					kept = append(kept, st)
					continue
				}
				if classDeclCaptures(b, cd, hoistedDecls) != "" && !e.closureConvertClass(c, b, blk, cd, hoistedDecls) {
					kept = append(kept, st)
					continue
				}
				hoistedDecls[cd] = true
				e.classExprSeq++
				name := fmt.Sprintf("%s__kml_cx%d", cd.Name, e.classExprSeq)
				if e.classShownNames == nil {
					e.classShownNames = map[string]string{}
				}
				e.classShownNames[name] = cd.Name
				renames[cd.Name] = name
				mine = append(mine, cd)
				hoisted = append(hoisted, cd)
				// The declaration's place in the block re-evaluates the class:
				// its statics start over each time, as a fresh class's do.
				at := *cd
				at.Name = name
				e.markClassReeval(&at, cd)
				atOf[cd] = &at
				if e.classRecordsPending[cd] {
					// The declaration's place makes this evaluation's record.
					if e.classRecordSites == nil {
						e.classRecordSites = map[ast.Node]bool{}
					}
					e.classRecordSites[&at] = true
				}
				kept = append(kept, &at)
			}
			// A parameter's cell (emit_classclosure.go) opens the block.
			blk.Body = append(blk.Body[:len(blk.Body)-origLen:len(blk.Body)-origLen], kept...)
			for old, name := range renames {
				resolver.RenameInBlock(blk, old, name)
				for _, cd := range mine {
					resolver.RenameInClass(cd, old, name)
				}
			}
			for _, cd := range mine {
				cd.Name = renames[cd.Name]
				if caps, ok := e.classCapturesPending[cd]; ok {
					if e.classCaptures == nil {
						e.classCaptures = map[string][]classCapture{}
					}
					e.classCaptures[cd.Name] = caps
				}
				e.takeStaticCaps(cd)
				if e.classRecordsPending[cd] {
					if e.classRecords == nil {
						e.classRecords = map[string]bool{}
					}
					e.classRecords[cd.Name] = true
				}
			}
			// A block class extending a closure-converted one holds the same
			// bindings (its inherited fields), and is an evaluation record
			// when its base is.
			for changed := true; changed; {
				changed = false
				for _, cd := range mine {
					caps, ok := e.classCaptures[cd.BaseClass]
					if !ok {
						continue
					}
					if _, own := e.classCaptures[cd.Name]; !own {
						e.classCaptures[cd.Name] = caps
						changed = true
					}
					if e.classRecords[cd.BaseClass] && !e.classRecords[cd.Name] {
						e.classRecords[cd.Name] = true
						if e.classRecordSites == nil {
							e.classRecordSites = map[ast.Node]bool{}
						}
						e.classRecordSites[atOf[cd]] = true
						changed = true
					}
				}
			}
		}
		// `const K = class {}`: an anonymous class takes its binding's name
		// (JavaScript's NamedEvaluation).
		if v, ok := n.(*ast.VarDeclaration); ok {
			if ce, ok := v.Init.(*ast.ClassExpression); ok {
				boundTo[ce] = v
				if anonymousClass(ce) {
					ce.Decl.Name = v.Name
				}
			}
		}
		ast.RewriteChildren(n, rewrite)
		switch n := n.(type) {
		case *ast.ClassExpression:
			captures := false
			if local := classCaptures(b, n, boundTo[n]); local != "" {
				// A class expression reading a local is closure-converted,
				// and each evaluation makes its record (TDD-00242).
				if len(n.Decl.Decorators) > 0 || !e.closureConvertClass(c, b, nil, n.Decl, hoistedDecls) {
					if e.classExprCaptures == nil {
						e.classExprCaptures = map[*ast.ClassExpression]string{}
					}
					e.classExprCaptures[n] = local
					return n
				}
				captures = true
			}
			e.classExprSeq++
			own := n.Decl.Name
			if anonymousClass(n) {
				own = ""
			}
			name := fmt.Sprintf("%s__kml_cx%d", own, e.classExprSeq)
			if own != "" {
				resolver.RenameInClass(n.Decl, own, name)
			} else if n.Decl.Name != "" {
				// The placeholder name the closure conversion's references
				// to the class read.
				resolver.RenameInClass(n.Decl, n.Decl.Name, name)
			}
			n.Decl.Name = name
			hoisted = append(hoisted, n.Decl)
			ref := ast.NewIdentifier(name, n.GetPos())
			e.markClassReeval(ref, n.Decl)
			if captures {
				if e.classCaptures == nil {
					e.classCaptures = map[string][]classCapture{}
				}
				e.classCaptures[name] = e.classCapturesPending[n.Decl]
				e.takeStaticCaps(n.Decl)
				if e.classRecords == nil {
					e.classRecords = map[string]bool{}
				}
				e.classRecords[name] = true
				if e.classRecordSites == nil {
					e.classRecordSites = map[ast.Node]bool{}
				}
				e.classRecordSites[ref] = true
			}
			return ref
		}
		return n
	}
	for i, st := range prog.Body {
		prog.Body[i] = rewrite(st).(ast.Statement)
	}
	prog.Body = append(hoisted, prog.Body...)
}

// markClassReeval records site, where hoisted class cd is evaluated inside a
// function, as re-running cd's static initialization (none without static
// initializers): `function f() { class C { static n = 0 } … }` gives every
// call of f a C whose statics start from their initializers.
func (e *Emitter) markClassReeval(site ast.Node, cd *ast.ClassDeclaration) {
	if len(cd.TypeParams) > 0 || len(cd.StaticBlocks) == 0 && !classHasStaticFieldInit(cd) {
		return
	}
	if e.classReeval == nil {
		e.classReeval = map[ast.Node]*ast.ClassDeclaration{}
	}
	e.classReeval[site] = cd
}

// emitClassReeval re-runs the static initialization of the class site
// evaluates, if it is a hoisted class with any.
func (e *Emitter) emitClassReeval(site ast.Node) {
	if cd, ok := e.classReeval[site]; ok {
		// A class per evaluation initializes this evaluation's statics.
		restore := e.passStaticReceiver(cd.Name)
		e.emitInstr(fmt.Sprintf("call void @%s_staticinit()", cd.Name))
		restore()
	}
}

// classCaptures is the first binding of an enclosing function or block a
// class expression's body names ("" for none): hoisting it would lose that
// binding.
func classCaptures(b *binder.Binding, ce *ast.ClassExpression, binding *ast.VarDeclaration) string {
	inside := map[*binder.Scope]bool{}
	captured := ""
	ast.Inspect(ce.Decl, func(n ast.Node) bool {
		if captured != "" {
			return false
		}
		if s := b.ScopeOf(n); s != nil {
			inside[s] = true
		}
		id, ok := n.(*ast.Identifier)
		if !ok {
			return true
		}
		sym, _ := b.Resolve(id)
		if sym == nil || sym.Scope == nil {
			return true
		}
		for _, d := range sym.Declarations {
			if binding != nil && d.Node == ast.Node(binding) {
				return true // the binding the class is assigned to: itself
			}
		}
		for s := sym.Scope; s != nil; s = s.Parent {
			if inside[s] || s.Kind == binder.NameScope && s.Node == ast.Node(ce) {
				return true // the class's own
			}
		}
		if sym.Scope.Kind != binder.ModuleScope {
			captured = id.Name
		}
		return true
	})
	return captured
}

// classDeclCaptures is classCaptures for a class declared in a block: the
// first local of an enclosing function or block its body names, other than
// itself and the block classes already hoisted.
func classDeclCaptures(b *binder.Binding, cd *ast.ClassDeclaration, hoisted map[ast.Node]bool) string {
	inside := map[*binder.Scope]bool{}
	captured := ""
	ast.Inspect(cd, func(n ast.Node) bool {
		if captured != "" {
			return false
		}
		if s := b.ScopeOf(n); s != nil {
			inside[s] = true
		}
		id, ok := n.(*ast.Identifier)
		if !ok {
			return true
		}
		sym, _ := b.Resolve(id)
		if sym == nil || sym.Scope == nil {
			return true
		}
		for _, d := range sym.Declarations {
			if d.Node == ast.Node(cd) || hoisted[d.Node] {
				return true
			}
		}
		for s := sym.Scope; s != nil; s = s.Parent {
			if inside[s] {
				return true
			}
		}
		if sym.Scope.Kind != binder.ModuleScope {
			captured = id.Name
		}
		return true
	})
	return captured
}

// anonymousClass reports a class expression without a name of its own (the
// parser's placeholder starts with `$`).
func anonymousClass(ce *ast.ClassExpression) bool {
	return ce.Decl.Name == "" || ce.Decl.Name[0] == '$'
}

// classDisplayName is the name `.name` and util.inspect give class c: its
// source name, or a class expression's own name where its binding renamed
// it (`const Box = class Impl {}` is `Impl`).
func (e *Emitter) classDisplayName(c string) string {
	if shown, ok := e.classShownNames[c]; ok {
		return shown
	}
	return inspectClassName(c)
}

// takeStaticCaps files cd's static captures under its final name.
func (e *Emitter) takeStaticCaps(cd *ast.ClassDeclaration) {
	if caps, ok := e.classStaticCapsPending[cd]; ok {
		if e.classStaticCaps == nil {
			e.classStaticCaps = map[string][]classCapture{}
		}
		e.classStaticCaps[cd.Name] = caps
	}
}
