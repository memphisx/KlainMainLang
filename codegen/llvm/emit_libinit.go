package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// libModuleInit is a builtin module's init function,
// @__kml_lib_<key>_init (TDD-00238 Stage 4): the module's top-level
// statements, run once per isolate however many importers reach it. Every
// binding they declare is a module global (registerModuleGlobals), so the
// function keeps no state of its own. Emitted on first use: the main
// program calls it where the module's statements stood, and a worker calls
// it again on its own thread, where the run-once flag starts clear.
func (e *Emitter) libModuleInit(key string) (string, error) {
	name := "@__kml_lib_" + key + "_init"
	if e.libInits[key] {
		return name, nil
	}
	if e.libInits == nil {
		e.libInits = map[string]bool{}
	}
	e.libInits[key] = true
	done := "@__kml_lib_" + key + "_done"
	e.emitGlobal(fmt.Sprintf("%s = internal %sglobal i1 false", done, e.isolateTLS()))
	restore := e.beginDetachedFunc()
	e.scopes[0].top = true
	ran := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s", ran, done))
	skipL, runL := e.freshLabel("lib.ran"), e.freshLabel("lib.run")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ran, skipL, runL))
	e.emitLabel(skipL)
	e.emitTerminator("ret void")
	e.emitLabel(runL)
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s", done))
	savedInLib, savedWorker := e.inLib, e.currentWorkerMod
	e.inLib, e.currentWorkerMod = key, ""
	var err error
	for _, st := range e.libGroups[key] {
		if _, ok := st.(*ast.FunctionDeclaration); ok {
			continue
		}
		if err = e.emitStmt(st); err != nil {
			break
		}
		e.emitOwnedFreesAfter(st)
	}
	e.inLib, e.currentWorkerMod = savedInLib, savedWorker
	if !e.blockDone {
		e.emitTerminator("ret void")
	}
	body := e.allocas.String() + e.body.String()
	restore()
	if err != nil {
		return "", err
	}
	e.writeDetachedFunc("internal ", "void", name, "", body)
	return name, nil
}

// writeDetachedFunc writes a function compiled detached from its caller
// (beginDetachedFunc): a module init, an import() target's body.
func (e *Emitter) writeDetachedFunc(linkage, ret, name, params, body string) {
	e.functions.WriteString(fmt.Sprintf("\ndefine %s%s %s(%s) {\nentry:\n%s}\n", linkage, ret, name, params, body))
}

// emitLibInitCalls emits stmts, calling a builtin module's init function in
// place of the run of that module's statements; emit emits any other.
func (e *Emitter) emitLibInitCalls(stmts []ast.Statement, emit func(ast.Statement) error) error {
	for i := 0; i < len(stmts); {
		if e.dynStmts[stmts[i]] != "" {
			i++ // runs from its import() target's init (emit_dynmodule.go)
			continue
		}
		key := e.libStmts[stmts[i]]
		if key == "" {
			if err := emit(stmts[i]); err != nil {
				return err
			}
			i++
			continue
		}
		for i < len(stmts) && e.libStmts[stmts[i]] == key {
			i++
		}
		name, err := e.libModuleInit(key)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("call void %s()", name))
	}
	return nil
}

// groupLibStatements records each builtin module's top-level statements,
// in order, by module key.
func (e *Emitter) groupLibStatements(prog *ast.Program) {
	e.libGroups = map[string][]ast.Statement{}
	for _, st := range prog.Body {
		if key := e.libStmts[st]; key != "" {
			e.libGroups[key] = append(e.libGroups[key], st)
		}
	}
}
