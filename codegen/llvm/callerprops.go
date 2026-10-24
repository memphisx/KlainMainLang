// callerprops.go — `f.caller` and `f.arguments` of a sloppy function (a
// plain `function`, as Node runs a CommonJS `.ts`): during a call of f its
// caller (null for a strict one, the module wrapper at top level) and its
// arguments object, null outside one; a strict function (an arrow, a
// method, an async or generator function, a builtin) throws. A program that
// reads such a property records each frame's function and arguments object
// in its shadow-stack record (frames.go), which __kml_fn_caller_get
// (fnmeta.c) walks.
package llvm

import (
	"fmt"
	"path/filepath"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
)

// fnKindSloppy flags a sloppy function's metadata row (FN_SLOPPY).
const fnKindSloppy = 0x1000

// usesCallerProps reports whether the program reads a `caller` or
// `arguments` property (a member name or a string key).
func (e *Emitter) usesCallerProps() bool {
	return e.callerProps
}

// prepareCallerProps scans the program for a `caller`/`arguments` property
// read and, when there is one, makes each sloppy function declaration build
// its arguments object (a reference to `arguments` added to its body),
// recorded in sloppyDecls and sloppyFuncs.
func (e *Emitter) prepareCallerProps() {
	if e.prog == nil {
		return
	}
	for _, st := range e.prog.Body {
		ast.Inspect(st, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.MemberExpression:
				if n.Property == "caller" || n.Property == "arguments" {
					e.callerProps = true
				}
			case *ast.StringLiteral:
				if n.Value == "caller" || n.Value == "arguments" {
					e.callerProps = true
				}
			}
			return !e.callerProps
		})
		if e.callerProps {
			break
		}
	}
	if !e.callerProps {
		return
	}
	e.sloppyDecls, e.sloppyFuncs = map[*ast.FunctionDeclaration]bool{}, map[string]bool{}
	e.strictFns = strictFunctions(e.prog, cjsFiles(e.prog))
	methods := map[*ast.FunctionDeclaration]bool{}
	for _, st := range e.prog.Body {
		if e.fileIsLib(e.prog.StmtFiles[st]) {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			if c, ok := n.(*ast.ClassDeclaration); ok {
				for _, m := range c.Methods {
					methods[m] = true
				}
			}
			return true
		})
		ast.Inspect(st, func(n ast.Node) bool {
			fd, ok := n.(*ast.FunctionDeclaration)
			if !ok || methods[fd] || e.strictFns[fd] || !sloppyDecl(fd) {
				return true
			}
			e.sloppyDecls[fd] = true
			e.sloppyFuncs[fd.Name] = true
			if !paramsUseArguments(fd.Params, fd.Body.Body) {
				fd.Body.Body = append([]ast.Statement{ast.NewExpressionStatement(ast.NewIdentifier("arguments", fd.GetPos()), fd.GetPos())}, fd.Body.Body...)
			}
			return true
		})
	}
}

// cjsFiles is the set of the program's files Node runs as sloppy CommonJS
// (a .ts one too, type-stripped): no import or export, no leading
// "use strict" directive, not ESM.
func cjsFiles(prog *ast.Program) map[string]bool {
	strict, first := map[string]bool{}, map[string]bool{}
	for _, s := range prog.Body {
		f := prog.StmtFiles[s]
		switch st := s.(type) {
		case *ast.ImportDeclaration, *ast.ExportDeclaration, *ast.ExportFromDeclaration:
			strict[f] = true
		case *ast.ExpressionStatement:
			if lit, ok := st.Expr.(*ast.StringLiteral); ok && lit.Value == "use strict" && !first[f] {
				strict[f] = true
			}
		}
		first[f] = true
	}
	out := map[string]bool{}
	for f := range first {
		ext := strings.ToLower(filepath.Ext(f))
		if !strict[f] && !prog.ESMFiles[f] && ext != ".mjs" && ext != ".mts" {
			out[f] = true
		}
	}
	return out
}

// strictFunctions is the set of function declarations and expressions
// whose code is strict: in a strict file, a class body, or a function with
// a "use strict" directive (its own or an enclosing one's). A strict one's
// frame is a strict caller (f.caller reads null) and its own caller and
// arguments throw.
func strictFunctions(prog *ast.Program, sloppy map[string]bool) map[ast.Node]bool {
	out := map[ast.Node]bool{}
	var walk func(n ast.Node, strict bool)
	walk = func(root ast.Node, strict bool) {
		ast.Inspect(root, func(n ast.Node) bool {
			enters := false
			switch x := n.(type) {
			case *ast.ClassDeclaration, *ast.ClassExpression:
				enters = true
			case *ast.FunctionDeclaration:
				enters = x.Body != nil && hasUseStrict(x.Body.Body)
				if strict || enters {
					out[n] = true
				}
			case *ast.FunctionExpression:
				enters = x.Body != nil && hasUseStrict(x.Body.Body)
				if strict || enters {
					out[n] = true
				}
			}
			if !strict && enters {
				walk(n, true)
				return false
			}
			return true
		})
	}
	for _, st := range prog.Body {
		walk(st, !sloppy[prog.StmtFiles[st]])
	}
	return out
}

// sloppyDecl reports whether fd is a sloppy function whose arguments object
// can be built: a plain function with a body, no rest or destructured
// parameter, no parameter named `arguments`.
func sloppyDecl(fd *ast.FunctionDeclaration) bool {
	if fd.Body == nil || fd.IsAsync || fd.IsGenerator {
		return false
	}
	for _, p := range fd.Params {
		if (p.Rest && p.Name != argumentsRestParam) || p.ArrayPattern != nil || p.ObjectPattern != nil || p.Name == "arguments" {
			return false
		}
	}
	return true
}

// fileIsLib reports whether path is a builtin library module's.
func (e *Emitter) fileIsLib(path string) bool {
	return path == "" || strings.HasPrefix(path, lib.ModuleRoot)
}

// storeFrameActivation stores a frame record's function and arguments words.
func (e *Emitter) storeFrameActivation(fr, self, args string) {
	for i, v := range []string{self, args} {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", p, frameIR, fr, 3+i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, p))
	}
}

// markModuleFrame marks main's frame as the module wrapper's, the caller a
// top-level call of a sloppy function sees, and turns the runtime's frame
// walk on.
func (e *Emitter) markModuleFrame() {
	if !e.usesCallerProps() || e.frame == "" {
		return
	}
	e.storeFrameActivation(e.frame, "1", "0")
	e.emitGlobal("@__kml_caller_frames = external global i32")
	e.emitInstr("store i32 1, ptr @__kml_caller_frames, align 4")
}

// recordActivation stores sloppy decl's own boxed value and its arguments
// object in its running frame.
func (e *Emitter) recordActivation(decl *ast.FunctionDeclaration) {
	if !e.sloppyDecls[decl] || e.frame == "" {
		return
	}
	pos := decl.GetPos()
	self, err := e.emitExpr(ast.NewIdentifier(decl.Name, pos))
	if err == nil && !self.Ty.IsDynamic() {
		self, err = e.emitBoxValue(self)
	}
	if err != nil {
		return
	}
	args, err := e.emitExpr(ast.NewIdentifier("arguments", pos))
	if err == nil && !args.Ty.IsDynamic() {
		args, err = e.emitBoxValue(args)
	}
	if err != nil {
		return
	}
	e.storeFrameActivation(e.frame, self.Ref, args.Ref)
}
