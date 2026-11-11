package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// Module-level dead zones: a top-level let, const or class that a
// function reads or writes, where code before its declaration may call
// that function (`f(); let x; function f() { x = 1 }`). Each gets a flag
// its declaration sets; a function's access before then throws
// JavaScript's ReferenceError. A binding no earlier statement can reach
// (no call or `new` before it) needs no flag.

// planModuleTDZ picks the flagged bindings of the program's top level.
func (e *Emitter) planModuleTDZ(prog *ast.Program) {
	if !e.compatJS() {
		return // tsc's TS2448/TS2454 rule out the reads it can see
	}
	mayRun := false
	for _, st := range prog.Body {
		if e.libStmts[st] != "" || e.dynStmts[st] != "" {
			continue
		}
		var names []string
		switch s := st.(type) {
		case *ast.VarDeclaration:
			if s.Kind != "var" {
				names = append(names, s.Name)
			}
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				if d.Kind != "var" {
					names = append(names, d.Name)
				}
			}
		}
		if mayRun {
			for _, n := range names {
				if _, ok := e.moduleGlobals[n]; ok {
					if e.moduleTDZ == nil {
						e.moduleTDZ = map[string]string{}
					}
					flag := "@" + llvmSafeSymbol(n+".tdz")
					e.emitGlobal(fmt.Sprintf("%s = internal %sglobal i8 0, align 1", flag, e.isolateTLS()))
					e.moduleTDZ[n] = flag
				}
			}
		}
		if _, ok := st.(*ast.FunctionDeclaration); ok {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.FunctionExpression, *ast.ArrowFunction, *ast.FunctionDeclaration:
				return false
			case *ast.CallExpression, *ast.NewExpression, *ast.TaggedTemplateExpression:
				mayRun = true
			}
			return !mayRun
		})
	}
}

// endModuleTDZ ends the dead zones of the bindings st declares.
func (e *Emitter) endModuleTDZ(st ast.Statement) {
	if len(e.moduleTDZ) == 0 || e.blockDone {
		return
	}
	end := func(n string) {
		if flag := e.moduleTDZ[n]; flag != "" {
			e.emitInstr(fmt.Sprintf("store i8 1, "+irPtr+" %s, align 1", flag))
		}
	}
	switch s := st.(type) {
	case *ast.VarDeclaration:
		end(s.Name)
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			end(d.Name)
		}
	}
}

// emitModuleTDZCheck throws the ReferenceError of an access, from a
// function, to a top-level binding whose declaration has not run.
func (e *Emitter) emitModuleTDZCheck(name string) {
	flag := e.moduleTDZ[name]
	if flag == "" || e.funcContext == e.mainFuncCtx {
		return
	}
	if sym, ok := e.lookup(name); !ok || sym.Ptr != e.moduleGlobals[name].Ptr {
		return // a local of the same name
	}
	v, dead := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", v, flag))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 0", dead, v))
	tdzL, okL := e.freshLabel("tdz.throw"), e.freshLabel("tdz.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", dead, tdzL, okL))
	e.emitLabel(tdzL)
	e.ensureExceptionHelpers()
	errObj := e.buildErrorObj(errorKindIDs["ReferenceError"], e.internString("Cannot access '"+unmangleTopLevelName(name)+"' before initialization"), e.internString("ReferenceError"))
	e.excThrow(errObj)
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}
