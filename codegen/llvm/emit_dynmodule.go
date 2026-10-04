package llvm

import (
	_ "embed"
	"fmt"
	"runtime"
	"strings"

	"KlainMainLang/ast"
)

// emit_dynmodule.go — dynamic import() under the bundled and lazy backends
// (TDD-00238 Stage 5). The target is merged into the program, but the files
// only import() edges reach (the program's DynStatements) do not run in
// main: each target gets a module body, @__kml_dyn_<h>_body, running them
// behind per-file run-once flags. __kml_dynimport_init (dynwhen.c) spawns it
// as a task on the first import() and returns the module promise every later
// import() reuses. The task's trampoline rejects that promise on a top-level
// throw, and a top-level await parks it on the one event loop, as in Node.
// import() itself is a promise settled from the module promise with the
// target's namespace object.

//go:embed dynimportsrc/dynwhen.c
var dynWhenSource string

// DynWhenSource is dynwhen.c, behind kml_layout.h.
func DynWhenSource() string { return layoutHeader() + dynWhenSource }

// UsesDynWhen reports whether the program links dynwhen.c.
func (e *Emitter) UsesDynWhen() bool { return e.usedDynWhen }

func (e *Emitter) ensureDynWhen() {
	if e.usedDynWhen {
		return
	}
	e.usedDynWhen = true
	e.ensurePromiseAdopt() // __kml_promise_attach
	e.ensureTaskRuntime()
	e.ensurePromiseDeferSettle()
	e.emitGlobal("declare void @__kml_dynimport_when(ptr, ptr, ptr)\ndeclare ptr @__kml_dynimport_init(ptr, ptr, i64)")
}

// groupDynStatements records each dynamic-only file's top-level statements,
// in order, by file.
func (e *Emitter) groupDynStatements(prog *ast.Program) {
	e.dynStmts = prog.DynStatements
	e.dynModules = prog.DynModules
	e.dynGroups = map[string][]ast.Statement{}
	for _, st := range prog.Body {
		if f := e.dynStmts[st]; f != "" {
			e.dynGroups[f] = append(e.dynGroups[f], st)
		}
	}
}

// dynModuleInit is target's module body, emitted on first use, which
// __kml_dynimport_init (dynwhen.c) spawns as a task on the first import();
// its module promise lives in @__kml_dyn_<h>_prom. dynInits records the
// bodies (by hash) and run-once flags (by name) emitted.
func (e *Emitter) dynModuleInit(target string) (string, error) {
	h := IslandHash(target)
	name := "@__kml_dyn_" + h + "_body"
	if e.dynInits[h] {
		return name, nil
	}
	if e.dynInits == nil {
		e.dynInits = map[string]bool{}
	}
	e.dynInits[h] = true
	files := e.dynModules[target]
	var body []ast.Statement
	for _, f := range files {
		body = append(body, e.dynGroups[f]...)
	}
	e.ensureModuleTaskRuntime()
	prom := "@__kml_dyn_" + h + "_prom"
	e.emitGlobal(fmt.Sprintf("%s = %s%sglobal ptr null, align 8", prom, e.dynLinkage(), e.isolateTLS()))
	for _, f := range files {
		flag := "@__kml_dynfile_" + IslandHash(f) + "_done"
		if !e.dynInits[flag] { // a file two targets reach has one flag
			e.dynInits[flag] = true
			e.emitGlobal(fmt.Sprintf("%s = internal %sglobal i1 false", flag, e.isolateTLS()))
		}
	}

	// The module body: each file's statements behind its run-once flag (a
	// file two targets reach runs once).
	restore := e.beginDetachedFunc()
	e.scopes[0].top = true
	e.inModuleTask = stmtsHaveTopLevelAwait(body)
	var err error
	for _, f := range files {
		done := "@__kml_dynfile_" + IslandHash(f) + "_done"
		ran := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s", ran, done))
		skipL, runL := e.freshLabel("dyn.ran"), e.freshLabel("dyn.run")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ran, skipL, runL))
		e.emitLabel(runL)
		e.emitInstr(fmt.Sprintf("store i1 true, ptr %s", done))
		for _, st := range e.dynGroups[f] {
			if _, ok := st.(*ast.FunctionDeclaration); ok {
				continue
			}
			if err = e.emitStmt(st); err != nil {
				break
			}
			e.emitOwnedFreesAfter(st)
		}
		if err != nil {
			break
		}
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
		}
		e.emitLabel(skipL)
	}
	if !e.blockDone {
		e.emitTerminator("ret void")
	}
	fnBody := e.allocas.String() + e.body.String()
	restore()
	if err != nil {
		return "", err
	}
	e.writeDetachedFunc(e.dynLinkage(), "void", "@__kml_dyn_"+h+"_body", "ptr %__kml_module_args", fnBody)

	return name, nil
}

// dynLinkage is the linkage of a target's body, module promise and
// fulfill: exported under lazy, where the importer finds them in the
// target's shared library.
func (e *Emitter) dynLinkage() string {
	if e.opts.DynamicImport == "lazy" {
		return ""
	}
	return "internal "
}

// emitBundledImport lowers import(target) under the bundled and lazy
// backends: the target's module promise, then its namespace object. Under
// lazy the target's code is a shared library beside the executable, opened
// on first use, and the init and fulfill are looked up in it.
func (e *Emitter) emitBundledImport(ex *ast.ImportCallExpression) (Value, error) {
	body, err := e.dynModuleInit(ex.ResolvedPath)
	if err != nil {
		return Value{}, err
	}
	e.ensureDynWhen()
	objTy := e.importCallResultObjectType(ex)
	fulfill := "@" + e.emitBundledFulfillFn(ex, objTy)
	prom := "@__kml_dyn_" + IslandHash(ex.ResolvedPath) + "_prom"
	if e.opts.DynamicImport == "lazy" {
		e.ensureDynImportShim()
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_open(ptr %s)", h, e.internString(IslandHash(ex.ResolvedPath))))
		sym := func(name string) string {
			fp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_sym(ptr %s, ptr %s)", fp, h, e.internString(strings.TrimPrefix(name, "@"))))
			return fp
		}
		body, fulfill, prom = sym(body), sym(fulfill), sym(prom)
	}
	mp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_init(ptr %s, ptr %s, i64 %d)", mp, prom, body, e.moduleTaskStackBytes()))
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynimport_when(ptr %s, ptr %s, ptr %s)", mp, q, fulfill))
	promTy := PromiseOf(objTy)
	promTy.PromiseTask = true
	return Value{Ref: q, Ty: promTy}, nil
}

// emitBundledFulfillFn defines fulfill(q): the namespace object, read from
// the target's module globals, stored as q's value (dynwhen.c settles q).
func (e *Emitter) emitBundledFulfillFn(ex *ast.ImportCallExpression, objTy Type) string {
	e.importSettleCtr++
	name := fmt.Sprintf("__kml_dyn_%s_fulfill_%d", IslandHash(ex.ResolvedPath), e.importSettleCtr)
	mangled := map[string]string{}
	for _, x := range ex.Exports {
		mangled[x.Name] = x.Mangled
	}
	restore := e.beginDetachedFunc()
	e.ensureCalloc()
	obj := e.freshReg()
	e.emitObjAllocInto(obj, objTy)
	structIR := objTy.StructIR()
	for _, f := range objTy.UserFields() {
		sym, ok := e.moduleGlobals[mangled[f.Name]]
		if !ok || sym.Ptr == "" {
			continue
		}
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", v, f.Ty.IR, sym.Ptr, f.Ty.Align()))
		idx, _, _ := objTy.FieldIndex(f.Name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", f.Ty.IR, v, gep, f.Ty.Align()))
	}
	e.storePromiseValue("%q", Value{Ref: obj, Ty: objTy})
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeDetachedFunc(e.dynLinkage(), "void", "@"+name, "ptr %q", body)
	return name
}

// SharedLibExt is the host's shared-library file extension.
func SharedLibExt() string {
	switch runtime.GOOS {
	case "darwin":
		return ".dylib"
	case "windows":
		return ".dll"
	}
	return ".so"
}

// ExportDynamicFlags make the executable export every symbol, which the
// lazy backend's shared libraries bind to.
func ExportDynamicFlags() []string {
	if runtime.GOOS == "darwin" {
		return []string{"-Wl,-export_dynamic"}
	}
	return []string{"-rdynamic"}
}

// IslandLinkFlags leave a lazy shared library's references to the
// executable undefined until it is loaded (a Linux shared library allows
// that by default).
func IslandLinkFlags() []string {
	if runtime.GOOS == "darwin" {
		return []string{"-Wl,-undefined,dynamic_lookup"}
	}
	return nil
}
