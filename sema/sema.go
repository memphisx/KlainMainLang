// Package sema holds the analyses that run between parsing (and module
// resolution) and code generation (TDD-00230). Today that is one pass:
// deciding which `new X(…)` expressions construct a builtin. Phase 2 of the
// restructure adds the binder and the checker here.
package sema

import (
	"KlainMainLang/ast"
	"KlainMainLang/resolver"
)

// Prepare runs the semantic passes over prog in place. It is idempotent:
// a program already prepared is left as it is.
func Prepare(prog *ast.Program) error {
	w := &walker{resolved: prog.ResolvedModules, prog: prog}
	w.push(declaredNames(prog.Body))
	for i, st := range prog.Body {
		prog.Body[i] = w.rewrite(st).(ast.Statement)
	}
	for _, wm := range prog.WorkerModules {
		ww := &walker{resolved: prog.ResolvedModules, prog: prog}
		ww.push(declaredNames(wm.Body))
		for i, st := range wm.Body {
			wm.Body[i] = ww.rewrite(st).(ast.Statement)
		}
		if ww.err != nil && w.err == nil {
			w.err = ww.err
		}
	}
	return w.err
}

// walker rewrites a module body, tracking the names user code declares in
// each enclosing scope: a `new X(…)` constructs the builtin X only where no
// user binding named X is in scope.
type walker struct {
	frames   []map[string]bool
	resolved map[*ast.StringLiteral]string // the resolver's module-specifier table
	prog     *ast.Program
	err      error
}

// moduleOnly are the builtin constructors no global declares: Node's and
// this compiler's module exports, which exist only where an import of their
// module brings them in (`import { EventEmitter } from 'events'`, or the
// module's namespace for `new events.EventEmitter()`).
var moduleOnly = map[string][]string{
	"EventEmitter": {"events"},
	"Readable":     {"stream"},
	"Writable":     {"stream"},
	"Transform":    {"stream"},
	"PassThrough":  {"stream"},
	"Duplex":       {"stream"},
	"Agent":        {"http", "https"},
	"DatabaseSync": {"node:sqlite"},
	"Channel":      {"klain:sync"},
	"Webview":      {"klain:webview"},
}

// imported reports whether a module-only builtin name is in scope: the name
// itself was imported, or its module was (a qualified `new mod.Name()`
// keeps only the name).
func (w *walker) imported(name string) bool {
	homes, ok := moduleOnly[name]
	if !ok || w.prog == nil {
		return true
	}
	if w.prog.BuiltinImports[name] {
		return true
	}
	for _, spec := range w.prog.BuiltinMarkers {
		for _, h := range homes {
			if spec == h || spec == "node:"+h {
				return true
			}
		}
	}
	return false
}

func (w *walker) push(names map[string]bool) { w.frames = append(w.frames, names) }
func (w *walker) pop()                       { w.frames = w.frames[:len(w.frames)-1] }

func (w *walker) declared(name string) bool {
	for i := len(w.frames) - 1; i >= 0; i-- {
		if w.frames[i][name] {
			return true
		}
	}
	return false
}

func (w *walker) rewrite(n ast.Node) ast.Node {
	switch n := n.(type) {
	case *ast.FunctionDeclaration:
		w.push(functionScope(n.Params, n.Body, ""))
		defer w.pop()
	case *ast.FunctionExpression:
		w.push(functionScope(n.Params, n.Body, n.Name))
		defer w.pop()
	case *ast.ArrowFunction:
		w.push(functionScope(n.Params, n.Block, ""))
		defer w.pop()
	}
	ast.RewriteChildren(n, w.rewrite)
	if ne, ok := n.(*ast.NewExpression); ok {
		if build, ok := builtinConstructors[ne.ClassName]; ok && !w.declared(ne.ClassName) && w.imported(ne.ClassName) {
			out, err := build(ne)
			if err != nil {
				if w.err == nil {
					w.err = err
				}
				return n
			}
			// A worker's path literal was resolved to its module file by the
			// resolver (relative to the file it is written in).
			if nw, ok := out.(*ast.NewWorkerExpression); ok {
				if lit, ok := ne.Args[0].(*ast.StringLiteral); ok {
					nw.ResolvedPath = w.resolved[lit]
				}
			}
			return out
		}
	}
	return n
}

// functionScope is the set of names a function introduces: its parameters, a
// function expression's own name, and every declaration in its body outside
// nested functions. Block-scoped declarations are counted for the whole body
// — a conservative reading: a name declared in any block of the function is
// never taken for the builtin anywhere in it.
func functionScope(params []ast.Param, body *ast.BlockStatement, selfName string) map[string]bool {
	names := map[string]bool{}
	if selfName != "" {
		names[selfName] = true
	}
	for _, p := range params {
		addParam(names, p)
	}
	if body != nil {
		for k := range declaredNames(body.Body) {
			names[k] = true
		}
	}
	return names
}

func addParam(names map[string]bool, p ast.Param) {
	if p.Name != "" {
		names[p.Name] = true
	}
	addPattern(names, p.ArrayPattern, p.ObjectPattern)
}

func addPattern(names map[string]bool, elems []ast.ArrayPatternElem, props []ast.DestructProp) {
	for _, el := range elems {
		if el.Name != "" {
			names[el.Name] = true
		}
		addPattern(names, el.SubArray, el.SubObject)
	}
	for _, pr := range props {
		if pr.Local != "" {
			names[pr.Local] = true
		}
		addPattern(names, pr.SubArray, pr.SubObject)
	}
}

// declaredNames returns the value bindings the statements introduce, outside
// nested functions: variables, functions, classes, enums, loop and catch
// bindings, destructured names, and imports from user modules. An import of
// a builtin module (`import { Worker } from 'node:worker_threads'`) binds the
// builtin itself, so it is not a user declaration.
func declaredNames(stmts []ast.Statement) map[string]bool {
	names := map[string]bool{}
	for _, st := range stmts {
		ast.Inspect(st, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.VarDeclaration:
				names[n.Name] = true
			case *ast.ArrayDestructuring:
				addPattern(names, n.Elems, nil)
			case *ast.ObjectDestructuring:
				addPattern(names, nil, n.Props)
			case *ast.ForOfStatement:
				names[n.VarName] = true
				addPattern(names, n.ArrayPattern, n.ObjectPattern)
			case *ast.ForInStatement:
				names[n.VarName] = true
			case *ast.TryStatement:
				if n.Catch != nil && n.Catch.Param != "" {
					names[n.Catch.Param] = true
				}
			case *ast.ClassDeclaration:
				names[n.Name] = true
			case *ast.EnumDeclaration:
				names[n.Name] = true
			case *ast.ImportDeclaration:
				if !resolver.IsBuiltinModule(n.Source) {
					for _, sp := range n.Specifiers {
						names[sp.Local] = true
					}
					if n.Namespace != "" {
						names[n.Namespace] = true
					}
				}
			case *ast.FunctionDeclaration:
				names[n.Name] = true
				return false // its body is its own scope
			case *ast.FunctionExpression, *ast.ArrowFunction:
				return false
			}
			return true
		})
	}
	return names
}
