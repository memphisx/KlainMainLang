package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/checker"
	"KlainMainLang/sema"
	"fmt"
	"sort"
	"strings"
)

// collectTopLevelNames records every name the program itself binds at top
// level (e.topLevelNames). A call through one of these (`const f = async () =>
// …; const p = f()`) is a *user* call whose result type the pre-pass cannot
// know unless the binding is already a registered function or module global —
// it must never be mistaken for a builtin (prePassStableBuiltinCall); and an
// identifier naming one, met while a function signature is inferred before
// the binding is a module global, types from its declaration (typeOfExpr's
// Identifier case, ADR-01060). Idempotent; runs before Pass 1 and again at
// registerModuleGlobals.
func (e *Emitter) collectTopLevelNames(prog *ast.Program) {
	e.topLevelNames = map[string]bool{}
	e.topLevelCallables = map[string]bool{}
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			e.topLevelNames[s.Name] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				e.topLevelNames[d.Name] = true
			}
		case *ast.FunctionDeclaration:
			e.topLevelNames[s.Name] = true
			e.topLevelCallables[s.Name] = true
		case *ast.ClassDeclaration:
			e.topLevelNames[s.Name] = true
			e.topLevelCallables[s.Name] = true
		case *ast.ArrayDestructuring:
			// A pattern's names are module-level too (`var [x, y] = a`),
			// typed only when the statement runs.
			for _, n := range ast.ArrayPatternNames(s.Elems) {
				e.topLevelNames[n] = true
			}
		case *ast.ObjectDestructuring:
			for _, n := range ast.ObjectPatternNames(s.Props) {
				e.topLevelNames[n] = true
			}
		}
	}
}

// initRefsCallable reports whether an object literal initializer holds a
// top-level function or class by name (`{ lookup: lookup, Resolver }`):
// promoted like one holding an inline function, so a function reads it.
func (e *Emitter) initRefsCallable(init ast.Expression) bool {
	lit, ok := init.(*ast.ObjectLiteral)
	if !ok {
		return false
	}
	for _, p := range lit.Properties {
		if id, ok := p.Value.(*ast.Identifier); ok && e.topLevelCallables[id.Name] {
			return true
		}
		if mem, ok := p.Value.(*ast.MemberExpression); ok {
			if id, ok := mem.Object.(*ast.Identifier); ok && e.topLevelCallables[id.Name+"."+mem.Property] {
				return true
			}
		}
	}
	return false
}

// unmangleTopLevelName strips the resolver's per-file `__kml_mod<N>` suffix
// from a top-level binding's name.
func unmangleTopLevelName(name string) string {
	name = ast.Unmangle(name)
	// A namespace member's function is named as written (`f` of `N.f`).
	if i := strings.LastIndex(name, "__kmlns_"); i >= 0 {
		name = name[i+len("__kmlns_"):]
	}
	return name
}

// registerModuleGlobals promotes each top-level `const`/`let`/`var` of a simple
// scalar/string type to an LLVM module global (TDD-00093), so a named `function`
// declaration — emitted with its own fresh scope, unlike an arrow/closure that
// captures — can read it. Run before function bodies are emitted (they resolve
// the name through e.moduleGlobals). The global is zero-initialized; the actual
// initializer runs in `main()` at the declaration's position (emitVarDecl stores
// into the same global). Only reliably-simple types are promoted (annotated, or a
// literal initializer) — an array/object/Map/complex value stays a `main()` local
// (the pre-existing behavior), never miscompiled.
func (e *Emitter) registerModuleGlobals(prog *ast.Program) {
	e.collectTopLevelNames(prog)
	readByFunctions := namesReadInFunctions(prog)
	// A library module's statements run in its init function, and a
	// dynamic-only file's in its import() target's: every one of their
	// bindings is a global, whether or not a function reads it.
	e.initFnDecls = map[*ast.VarDeclaration]bool{}
	// A worker module's statements (its own and the modules it imports) are
	// emitted again into its entry function, by name.
	workerNames := map[string]bool{}
	for _, wm := range prog.WorkerModules {
		for _, st := range append(append([]ast.Statement{}, wm.Deps...), wm.Body...) {
			switch s := st.(type) {
			case *ast.VarDeclaration:
				workerNames[s.Name] = true
			case *ast.VarDeclarationList:
				for _, d := range s.Decls {
					workerNames[d.Name] = true
				}
			}
		}
	}
	for _, stmt := range prog.Body {
		if e.libStmts[stmt] == "" && e.dynStmts[stmt] == "" {
			switch s := stmt.(type) {
			case *ast.VarDeclaration:
				if workerNames[s.Name] {
					e.initFnDecls[s] = true
				}
			case *ast.VarDeclarationList:
				for _, d := range s.Decls {
					if workerNames[d.Name] {
						e.initFnDecls[d] = true
					}
				}
			}
			continue
		}
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			e.initFnDecls[s] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				e.initFnDecls[d] = true
			}
		}
	}
	promote := func(v *ast.VarDeclaration) {
		if _, exists := e.moduleGlobals[v.Name]; exists {
			return
		}
		ty, forced := e.forcedGlobalTypes[v]
		ok := forced
		if !ok {
			// The front end's answer: one it leaves unanswered stays a
			// local, and an init function's binding it leaves untyped (an
			// unread one) is typed by registerModuleGlobals' own pass.
			answered := false
			if o := e.reprOracle(); o != nil {
				e.prePassSelf = v.Name
				gty, promotable, gok := o.GlobalType(v)
				e.prePassSelf = ""
				if gok && (promotable || !e.initFnDecls[v] || gty.IR != "") {
					ty, ok = gty, promotable || e.initFnDecls[v]
					answered = true // a local by the front end's own answer
				}
			}
			if !answered {
				e.shadowGlobalFallback(v)
			}
		}
		if !ok {
			return
		}
		// Whether a declaration becomes a global is codegen's choice, as is a
		// forced box; a global a function reads is stored as the checker's
		// type, mapped by reprOf (TDD-00230 P3.3: the shadow corpus agreed on
		// every one). The rest keep the type their own declaration stores.
		if o := e.reprOracle(); o != nil && !forced && readByFunctions[v.Name] {
			if oty, promotable, ook := o.GlobalType(v); ook && promotable {
				ty = oty
			}
		}
		if ty.IR == "void" {
			ty = TypeUndefined // a void call's result (`const r = m.clear()`)
		}
		ty = e.globalUndefinedStorage(v, ty)
		// A promoted generic class instance (`const b = new Box<number>()`) needs
		// its monomorphized ClassInfo (methods, dispatch) registered now, before
		// Pass 2 emits the function bodies that will call `b.get()` — otherwise
		// the method dispatch finds no registered class. genericClassInstanceType
		// (via typeOfExpr above) only computed the *shape*; instantiateGeneric-
		// Class does the real, memoized registration (a no-op if the top-level
		// `new` already forced it). Field access rides the type's own Fields and
		// needs no registration, but the method side does.
		if ne, isNew := v.Init.(*ast.NewExpression); isNew {
			if genDecl, gen := e.genericClasses[ne.ClassName]; gen {
				if targs, ok := e.newTypeArgs(genDecl, ne); ok {
					subs := e.buildTypeArgSubs(genDecl.TypeParams, targs)
					_, _ = e.instantiateGenericClass(genDecl, subs)
				}
			}
		}
		safe := llvmSafeSymbol(v.Name)
		if ty.IsArray() {
			// Object-reference model (TDD-00127): an array global is a single
			// slot holding a pointer to a heap {data, len} header, so a named
			// function reads/mutates the same header the top-level init built —
			// and a mutation propagates, exactly as for a local. emitArrayVarDecl
			// mallocs the header at init time and stores its pointer here.
			hdrG := "@__kml_global_" + safe + "_hdr"
			e.emitGlobal(fmt.Sprintf("%s = internal %sglobal "+irPtr+" null, align 8", hdrG, e.isolateTLS()))
			e.moduleGlobals[v.Name] = Symbol{Ptr: hdrG, Ty: ty, IsConst: v.Kind == "const"}
		} else if isNullableScalar(ty) {
			// A nullable scalar's { i1, T } aggregate (TDD-00064), absent.
			gname := "@__kml_global_" + safe
			e.emitGlobal(fmt.Sprintf("%s = internal %sglobal %s zeroinitializer, align %d", gname, e.isolateTLS(), nullableScalarStorageIR(ty), storageAlign(ty)))
			e.moduleGlobals[v.Name] = Symbol{Ptr: gname, Ty: ty, IsConst: v.Kind == "const"}
		} else {
			gname := "@__kml_global_" + safe
			e.emitGlobal(fmt.Sprintf("%s = internal %sglobal %s %s, align %d", gname, e.isolateTLS(), ty.IR, ty.zeroLiteral(), ty.Align()))
			e.moduleGlobals[v.Name] = Symbol{Ptr: gname, Ty: ty, IsConst: v.Kind == "const"}
		}
		e.promotedGlobalDecls[v] = true
	}
	var withFunctions []*ast.VarDeclaration
	savedInLib := e.inLib
	defer func() { e.inLib = savedInLib }()
	stmtOf := map[*ast.VarDeclaration]ast.Statement{}
	// `var f = 1` beside `function f() {}`: one binding, the function until
	// the assignment runs (hoisting binds it first). It is an any global
	// main seeds with the function's value (seedFuncGlobals).
	topFuncs := map[string]bool{}
	for _, stmt := range prog.Body {
		if fd, ok := stmt.(*ast.FunctionDeclaration); ok && fd.Body != nil && len(fd.TypeParams) == 0 {
			topFuncs[fd.Name] = true
		}
	}
	for _, stmt := range prog.Body {
		var ds []*ast.VarDeclaration
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			ds = []*ast.VarDeclaration{s}
		case *ast.VarDeclarationList:
			ds = s.Decls
		}
		for _, d := range ds {
			if d.Kind == "var" && d.Init != nil && d.TypeAnnot == nil && topFuncs[d.Name] && !e.funcSeeded[d.Name] {
				e.forcedGlobalTypes[d] = TypeAny
				e.inLib = e.libStmts[stmt]
				promote(d)
				if _, ok := e.moduleGlobals[d.Name]; ok {
					if e.funcSeeded == nil {
						e.funcSeeded = map[string]bool{}
					}
					e.funcSeeded[d.Name] = true
					e.funcSeedOrder = append(e.funcSeedOrder, d.Name)
				}
			}
		}
	}
	// JavaScript (-compat=js): an untyped global assigned (anywhere, a
	// function's body included) a value of another kind than its
	// initializer's (`var t = function () {}` a function sets to an object)
	// holds its value as `any`, as JavaScript holds every binding.
	if e.compatJS() {
		other := e.globalsAssignedOtherKind(prog)
		for _, stmt := range prog.Body {
			var ds []*ast.VarDeclaration
			switch s := stmt.(type) {
			case *ast.VarDeclaration:
				ds = []*ast.VarDeclaration{s}
			case *ast.VarDeclarationList:
				ds = s.Decls
			}
			for _, d := range ds {
				if _, done := e.moduleGlobals[d.Name]; done || !other[d] || e.libStmts[stmt] != "" || e.dynStmts[stmt] != "" {
					continue
				}
				e.forcedGlobalTypes[d] = TypeAny
				e.inLib = ""
				promote(d)
				if _, ok := e.moduleGlobals[d.Name]; ok {
					if e.jsHeldGlobals == nil {
						e.jsHeldGlobals = map[*ast.VarDeclaration]bool{}
					}
					e.jsHeldGlobals[d] = true
				}
			}
		}
	}
	for _, stmt := range prog.Body {
		e.inLib = e.libStmts[stmt]
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			promote(s)
			withFunctions = append(withFunctions, s)
			stmtOf[s] = stmt
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				promote(d)
				withFunctions = append(withFunctions, d)
				stmtOf[d] = stmt
			}
		}
	}
	e.shadowTopDecls = map[*ast.VarDeclaration]bool{}
	for _, v := range withFunctions {
		e.shadowTopDecls[v] = true
	}
	// A function in an initializer runs later, so it may read a global
	// declared after it (`const o = { m() { return later; } }`): once every
	// other global is registered, such a declaration promotes too.
	for _, v := range withFunctions {
		if initHasFunction(v.Init) || e.initRefsCallable(v.Init) {
			e.inLib = e.libStmts[stmtOf[v]]
			promote(v)
		}
	}
	// A declaration a function or class reads (`const crlf =
	// Buffer.from('\r\n')` read in a method) whose initializer's type codegen
	// infers as the checker types it: that type cannot drift.
	e.registerPatternGlobals(prog, readByFunctions)
	read := namesReadInFunctions(prog)
	// A namespace member function reads a sibling by its bare name.
	for ns, members := range e.namespaces {
		for m := range members {
			if read[m] {
				read[ast.NamespaceMangle(ns, m)] = true
			}
		}
	}
	// A generator's destructured parameters were moved off its signature
	// (desugarGeneratorPatternParams); their defaults read names too.
	for _, sts := range e.genPatternInits {
		for _, st := range sts {
			ast.Inspect(st, func(n ast.Node) bool {
				if id, ok := n.(*ast.Identifier); ok {
					read[id.Name] = true
				}
				return true
			})
		}
	}
	for _, v := range withFunctions {
		// An annotated `new Set([…])` too: emitSetVarDecl types a seeded Set
		// from its initializer, not the annotation.
		_, annotatedSet := v.Init.(*ast.NewSetExpression)
		if _, done := e.moduleGlobals[v.Name]; !done && v.Init == nil && v.TypeAnnot == nil && read[v.Name] && !e.widenedBindings[v.Name] {
			// `var value;` a function assigns: tsc types a variable with no
			// initializer that a function captures as `any`, one box.
			e.inLib = e.libStmts[stmtOf[v]]
			e.forcedGlobalTypes[v] = TypeAny
			promote(v)
			continue
		}
		if _, done := e.moduleGlobals[v.Name]; done || (v.TypeAnnot != nil && !annotatedSet) || v.Init == nil || !read[v.Name] || e.widenedBindings[v.Name] {
			continue
		}
		if annotatedSet && e.annotationType(v.TypeAnnot).IsDynamic() {
			continue // boxed into its annotation
		}
		e.inLib = e.libStmts[stmtOf[v]]
		if o := e.reprOracle(); o != nil && o.c.InProgram(v) && accessorLiteral(o.b, v) {
			// An accessor is defined on it: held boxed, its reads calling it.
			e.forcedGlobalTypes[v] = TypeAny
			promote(v)
			continue
		}
		ty := e.typeOfExpr(v.Init)
		if as, isAs := v.Init.(*ast.AsExpression); isAs {
			ty = e.asBindingType(as) // as emitVarDecl types it
		}
		switch init := v.Init.(type) {
		case *ast.NewMapExpression:
			// Typed as emitMapVarDecl types it, from entries stable here.
			if init.Init == nil || !e.promotableInitInPrePass(init.Init) {
				continue
			}
			k, val := e.mapKVTypes(init.KeyType, init.ValType, init.Init)
			e.forcedGlobalTypes[v] = MapType(k, forceAnyMapVal(k, val))
			promote(v)
			continue
		case *ast.NewSetExpression:
			// `new Set([…])` of a literal whose elements are stable here: the
			// element type emitNewSetValueTyped takes from that array.
			lit, isLit := init.Init.(*ast.ArrayLiteral)
			if init.Init == nil || !isLit || !e.promotableInitInPrePass(lit) {
				continue
			}
			elemTy := TypePtr
			if at := e.typeOfExpr(lit); at.IsArray() && at.ElemType != nil {
				elemTy = *at.ElemType
			}
			if init.ElemType != nil {
				elemTy = e.annotationType(init.ElemType)
			}
			e.forcedGlobalTypes[v] = SetType(elemTy)
			promote(v)
			continue
		}
		// A call whose result codegen holds as `any` (a builtin module
		// function implemented as returning `any`, such as fs.readFileSync):
		// the declaration stores that box, one NaN-box slot.
		if _, isCall := v.Init.(*ast.CallExpression); isCall && isUnconstrainedDynamic(ty) {
			e.forcedGlobalTypes[v] = ty
			promote(v)
			continue
		}
		if e.checkerAgreesOnGlobal(v.Init, ty) {
			e.forcedGlobalTypes[v] = e.checkerClassType(v.Init, ty)
			promote(v)
			continue
		}
		// `var seen = null` a function later assigns: the checker's declared
		// type (JavaScript widens the null to any; a later store's type
		// otherwise), one slot holding the null until then.
		if dt, ok := e.nullInitDeclaredType(v); ok {
			e.forcedGlobalTypes[v] = dt
			promote(v)
			continue
		}
		// Any other binding whose declared type the checker represents in
		// the storage the declaration stores (`let u = 0 as number |
		// string`, one box): every later store coerces into it.
		if e.declaredStorageAgrees(v, ty) {
			e.forcedGlobalTypes[v] = ty
			promote(v)
		}
	}
	// JavaScript (-compat=js): an untyped binding a function reads that no
	// rule above types for a global (`var all = [Int8Array].concat(more)`)
	// holds its value as `any`, as JavaScript holds every binding: the
	// declaration stores the box (anyHeldDecl).
	if e.compatJS() {
		for _, v := range withFunctions {
			st := stmtOf[v]
			if _, done := e.moduleGlobals[v.Name]; done || v.TypeAnnot != nil || v.Init == nil || !read[v.Name] || e.widenedBindings[v.Name] || e.libStmts[st] != "" || e.dynStmts[st] != "" {
				continue
			}
			e.inLib = ""
			e.forcedGlobalTypes[v] = TypeAny
			promote(v)
			if _, ok := e.moduleGlobals[v.Name]; ok {
				if e.jsHeldGlobals == nil {
					e.jsHeldGlobals = map[*ast.VarDeclaration]bool{}
				}
				e.jsHeldGlobals[v] = true
			}
		}
	}
	// A library module's statements run in its init function, and a
	// dynamic-only file's in its import() target's, so every one of their
	// bindings is a global, typed as its initializer is.
	for _, v := range withFunctions {
		if _, done := e.moduleGlobals[v.Name]; done || (e.libStmts[stmtOf[v]] == "" && e.dynStmts[stmtOf[v]] == "") || v.Init == nil {
			continue
		}
		e.inLib = e.libStmts[stmtOf[v]]
		if v.TypeAnnot != nil {
			e.forcedGlobalTypes[v] = e.annotationType(v.TypeAnnot) // as its declaration stores it
		} else {
			e.forcedGlobalTypes[v] = e.typeOfExpr(v.Init)
		}
		promote(v)
	}
	// A `var` nested in a top-level block (`if (c) { var x = 1 }`) is module
	// scoped too: a function that reads it reads the global, which holds
	// undefined until a declaration runs (the declaration can be skipped).
	nested := map[string][]*ast.VarDeclaration{}
	var order []string
	for _, stmt := range prog.Body {
		switch stmt.(type) {
		case *ast.VarDeclaration, *ast.VarDeclarationList, *ast.FunctionDeclaration, *ast.ClassDeclaration:
			continue
		}
		var forInit map[*ast.VarDeclaration]bool
		if fs, ok := stmt.(*ast.ForStatement); ok && fs.Init != nil {
			// The loop's own `var` always runs: it stays the main body's
			// local, captured as such.
			forInit = map[*ast.VarDeclaration]bool{}
			for _, v := range nestedVarDecls(fs.Init) {
				forInit[v] = true
			}
		}
		for _, v := range nestedVarDecls(stmt) {
			if forInit[v] {
				continue
			}
			if len(nested[v.Name]) == 0 {
				order = append(order, v.Name)
			}
			nested[v.Name] = append(nested[v.Name], v)
			stmtOf[v] = stmt
		}
	}
	for _, name := range order {
		decls := nested[name]
		if _, done := e.moduleGlobals[name]; done || e.topLevelNames[name] || !read[name] || e.widenedBindings[name] {
			continue
		}
		var ty Type
		agree := true
		for i, v := range decls {
			e.inLib = e.libStmts[stmtOf[v]]
			vt := TypeAny
			switch {
			case v.TypeAnnot != nil:
				vt = e.annotationType(v.TypeAnnot)
			case v.Init != nil:
				vt = e.typeOfExpr(v.Init)
			}
			if vt.IR == "void" {
				vt = TypeUndefined
			}
			if !vt.IsDynamic() && !vt.IsUndefined() {
				vt = indexReadType(vt) // what a skipped declaration leaves
			}
			if i > 0 && reprKey(vt) != reprKey(ty) {
				agree = false
			}
			ty = vt
		}
		if !agree {
			continue // two declarations of different types: not modelled
		}
		e.inLib = e.libStmts[stmtOf[decls[0]]]
		e.forcedGlobalTypes[decls[0]] = ty
		promote(decls[0])
		if _, done := e.moduleGlobals[name]; !done {
			continue
		}
		e.topLevelNames[name] = true
		for _, v := range decls[1:] {
			e.promotedGlobalDecls[v] = true
		}
	}
	if e.shadowOracle != nil {
		// The final decision, once per declaration; one no function reads
		// and that stays a local decides nothing a body depends on.
		for _, v := range withFunctions {
			promoted := e.promotedGlobalDecls[v]
			if _, shared := e.moduleGlobals[v.Name]; shared && !promoted {
				continue // a redeclaration stores into the earlier one's global, which decides
			}
			if !promoted && !read[v.Name] {
				continue
			}
			var ty Type
			if promoted {
				ty = e.moduleGlobals[v.Name].Ty
			}
			e.inLib = e.libStmts[stmtOf[v]] // the row's module
			e.shadowGlobalType(v, ty, promoted)
		}
	}
}

// namesReadInFunctions is every identifier read inside a top-level function
// or class body.
// registerPatternGlobals promotes a top-level destructuring's names that a
// named function reads to module globals, as registerModuleGlobals does a
// plain declaration's: a function declaration has its own scope, so it
// cannot see main's locals. Each global has the checker's declared type of
// the name; main stores the bound value into it after the destructuring
// runs (bindPatternGlobals).
func (e *Emitter) registerPatternGlobals(prog *ast.Program, readByFunctions map[string]bool) {
	c := e.front()
	if c == nil || e.frontBinding == nil {
		return
	}
	refs := map[string][]*ast.Identifier{}
	for _, st := range prog.Body {
		if e.libStmts[st] != "" {
			continue
		}
		switch st.(type) {
		case *ast.FunctionDeclaration, *ast.ClassDeclaration:
			ast.Inspect(st, func(n ast.Node) bool {
				if id, ok := n.(*ast.Identifier); ok {
					refs[id.Name] = append(refs[id.Name], id)
				}
				return true
			})
		}
	}
	for _, st := range prog.Body {
		e.inLib = e.libStmts[st] // the lane of this statement, not the last one seen
		names := map[string]bool{}
		switch s := st.(type) {
		case *ast.ArrayDestructuring:
			collectArrayPatternNames(s.Elems, names)
		case *ast.ObjectDestructuring:
			collectObjectPatternNames(s.Props, names)
		default:
			continue
		}
		var bound []string
		for name := range names {
			if !readByFunctions[name] {
				continue
			}
			if _, exists := e.moduleGlobals[name]; exists {
				continue
			}
			// A reference that names this declaration, not a parameter or
			// local of the same name.
			var id *ast.Identifier
			for _, r := range refs[name] {
				sym, _ := e.frontBinding.Resolve(r)
				if sym == nil {
					continue
				}
				for _, d := range sym.Declarations {
					if d.Node == st {
						id = r
					}
				}
				if id != nil {
					break
				}
			}
			if id == nil {
				continue
			}
			t := c.StorageTypeOf(id)
			if c.Unanswered(t) && !e.compatJS() {
				continue
			}
			ty, ok := Type{}, false
			if !c.Unanswered(t) {
				ty, ok = reprCtx{class: e.classRepr}.of(t)
			}
			if !ok || ty.IR == "" {
				// A type with no representation of its own (an object
				// literal's): the field's static type, which the local the
				// pattern binds holds.
				ty, ok = e.patternFieldType(st, name)
				if !ok && e.compatJS() {
					// JavaScript holds the binding as any.
					ty, ok = TypeAny, true
				}
			}
			if !ok || ty.IR == "" || ty.IR == "void" || strings.HasPrefix(ty.IR, "{") {
				continue
			}
			safe := llvmSafeSymbol(name)
			gname := "@__kml_global_" + safe
			switch {
			case ty.IsArray():
				gname += "_hdr"
				e.emitGlobal(fmt.Sprintf("%s = internal %sglobal "+irPtr+" null, align 8", gname, e.isolateTLS()))
			case isNullableScalar(ty):
				e.emitGlobal(fmt.Sprintf("%s = internal %sglobal %s zeroinitializer, align %d", gname, e.isolateTLS(), nullableScalarStorageIR(ty), storageAlign(ty)))
			default:
				e.emitGlobal(fmt.Sprintf("%s = internal %sglobal %s %s, align %d", gname, e.isolateTLS(), ty.IR, ty.zeroLiteral(), ty.Align()))
			}
			kind := ""
			switch s := st.(type) {
			case *ast.ArrayDestructuring:
				kind = s.Kind
			case *ast.ObjectDestructuring:
				kind = s.Kind
			}
			e.moduleGlobals[name] = Symbol{Ptr: gname, Ty: ty, IsConst: kind == "const"}
			bound = append(bound, name)
		}
		if len(bound) > 0 {
			sort.Strings(bound)
			if e.patternGlobals == nil {
				e.patternGlobals = map[ast.Statement][]string{}
			}
			e.patternGlobals[st] = bound
		}
	}
}

// patternFieldType is the static type of the field a top-level object
// pattern binds to name directly (`const { k } = o`, no default, no nested
// pattern), as unpackObjectPatternInto types the local.
func (e *Emitter) patternFieldType(st ast.Statement, name string) (Type, bool) {
	od, ok := st.(*ast.ObjectDestructuring)
	if !ok || od.TypeAnnot != nil {
		return Type{}, false
	}
	objTy := e.typeOfExpr(od.Init)
	if !objTy.IsObject() || objTy.IsDynamic() {
		return Type{}, false
	}
	for _, p := range od.Props {
		if p.Local != name || p.KeyExpr != nil || p.Default != nil || p.SubArray != nil || p.SubObject != nil {
			continue
		}
		if _, ft, ok := objTy.FieldIndex(p.Key); ok && ft.IsObject() && !ft.IsDynamic() {
			return e.canonicalizeClassTy(ft), true
		}
	}
	return Type{}, false
}

// bindPatternGlobals stores each promoted name a top-level destructuring
// just bound into its module global, and rebinds the name to the global, so
// main and every function share one slot.
func (e *Emitter) bindPatternGlobals(st ast.Statement) error {
	for _, name := range e.patternGlobals[st] {
		g := e.moduleGlobals[name]
		v, err := e.emitExpr(ast.NewIdentifier(name, st.GetPos()))
		if err != nil {
			return err
		}
		switch {
		case g.Ty.IsArray():
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.arrayReturnHeader(e.convert(v, g.Ty)), g.Ptr))
		case isNullableScalar(g.Ty):
			if g.Ty.NullAndUndef() && !v.Ty.IsDynamic() {
				v = e.toThreeState(v, g.Ty)
			} else {
				v = e.convert(v, g.Ty)
			}
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", storageIR(g.Ty), v.Ref, g.Ptr))
		default:
			v = e.convert(v, g.Ty)
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", g.Ty.IR, v.Ref, g.Ptr, g.Ty.Align()))
		}
		e.define(name, g)
	}
	return nil
}

func namesReadInFunctions(prog *ast.Program) map[string]bool {
	out := map[string]bool{}
	collect := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Identifier:
				out[x.Name] = true
			case *ast.NewExpression:
				if x.Callee == nil && !x.Qualified {
					out[x.ClassName] = true // `new EE()`: the constructor's binding
				}
			case *ast.ClassDeclaration:
				if x.BaseClass != "" && !x.BaseQualified {
					out[x.BaseClass] = true // its heritage, read where it is defined
				}
			}
			return true
		})
	}
	// A function value in a top-level initializer (`var cb = function () {
	// … }`, as a `this`-reading declaration is rewritten) runs later too.
	inInit := func(init ast.Expression) {
		if init == nil {
			return
		}
		ast.Inspect(init, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.FunctionExpression, *ast.ArrowFunction:
				collect(n)
				return false
			}
			return true
		})
	}
	// A function value in any other statement before a declaration (a
	// callback passed in a call) is built before the binding is, and reads
	// or writes the global it becomes.
	declared := map[string]bool{}
	early := map[string]bool{}
	for _, st := range prog.Body {
		// An object literal's accessor (`{ get size() { … } }`), wherever
		// it is, is lowered to a function of its own, which reads the global.
		ast.Inspect(st, func(n ast.Node) bool {
			if ol, ok := n.(*ast.ObjectLiteral); ok {
				for _, p := range ol.Properties {
					if p.AccessorKind != "" && p.Value != nil {
						collect(p.Value)
					}
				}
			}
			return true
		})
	}
	for _, st := range prog.Body {
		switch s := st.(type) {
		case *ast.FunctionDeclaration, *ast.ClassDeclaration:
			collect(st)
		case *ast.VarDeclaration:
			inInit(s.Init)
			declared[s.Name] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				inInit(d.Init)
				declared[d.Name] = true
			}
		default:
			ast.Inspect(st, func(n ast.Node) bool {
				switch n.(type) {
				case *ast.FunctionDeclaration, *ast.ClassDeclaration:
					// Declared in a block (`{ async function run() { … } }`):
					// a function of its own, which reads the global.
					collect(n)
					return false
				case *ast.FunctionExpression, *ast.ArrowFunction:
					ast.Inspect(n, func(m ast.Node) bool {
						if id, ok := m.(*ast.Identifier); ok && !declared[id.Name] {
							early[id.Name] = true
						}
						return true
					})
					return false
				}
				return true
			})
		}
	}
	for name := range early {
		if declared[name] {
			out[name] = true
		}
	}
	return out
}

// checkerAgreesOnGlobal reports whether codegen's inferred type of a
// top-level initializer is the type the checker gives it: a string, number,
// boolean, byte array, or an instance of the same class.
func (e *Emitter) checkerAgreesOnGlobal(init ast.Expression, ty Type) bool {
	c := e.front()
	if c == nil {
		return false
	}
	t := c.TypeOf(init)
	if c.Unanswered(t) || t.Flags&(checker.Any|checker.Unknown|checker.Union|checker.Nullish) != 0 {
		return false
	}
	switch {
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return isStringTy(ty)
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
		return ty.Float && ty.IR == "double" && !ty.Nullable()
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return ty.IR == "i1" && !ty.Nullable()
	case isBytesType(t):
		return ty.IsArray() && (ty.IsBuffer() || ty.IsTypedArray()) && (declaredLowerType(t.Symbol) == "buffer") == ty.IsBuffer()
	case t.Flags&checker.Object != 0 && t.Kind == checker.Instance && t.Symbol != nil:
		return ty.IsClass() && (ty.ClassName == t.Symbol.Name || e.classDerives(ty.ClassName, t.Symbol.Name) || e.classDerives(t.Symbol.Name, ty.ClassName))
	}
	return false
}

// asBindingType is the type an unannotated binding initialized by `expr as
// T` takes: typeOfExpr decides narrow-vs-erase; a widening to a union
// (`"5" as string | number`) types the binding by it, as tsc does: the slot
// is the box.
func (e *Emitter) asBindingType(init *ast.AsExpression) Type {
	ty := e.typeOfExpr(init)
	if init.TypeAnnot != nil && !ty.IsDynamic() {
		if t := e.annotationType(init.TypeAnnot); t.IsDynamic() && len(t.UnionMembers) > 0 {
			ty = t
		}
	}
	return ty
}

// declaredStorageAgrees reports whether the checker's declared type for the
// module binding v maps to the storage ty, which the declaration stores.
// nullInitDeclaredType is the storage of an unannotated binding
// initialized with null or undefined, from the checker's declared type: a
// box, or a nullable pointer, which holds the absent value as it is.
func (e *Emitter) nullInitDeclaredType(v *ast.VarDeclaration) (Type, bool) {
	if v.TypeAnnot != nil {
		return Type{}, false
	}
	if _, isNull := v.Init.(*ast.NullLiteral); !isNull {
		return Type{}, false
	}
	c := e.front()
	if c == nil {
		return Type{}, false
	}
	t := c.ModuleVarType(v)
	if c.Unanswered(t) {
		return Type{}, false
	}
	r, ok := e.repr().of(t)
	if !ok || !(isUnconstrainedDynamic(r) || r.IR == irPtr && r.Nullable() && !isNullableScalar(r)) {
		return Type{}, false
	}
	return r, true
}

func (e *Emitter) declaredStorageAgrees(v *ast.VarDeclaration, ty Type) bool {
	c := e.front()
	if c == nil || ty.IR == "" || ty.IR == "void" {
		return false
	}
	t := c.ModuleVarType(v)
	if c.Unanswered(t) {
		return false
	}
	r, ok := e.repr().of(t)
	if !ok {
		return false
	}
	return r.IR == ty.IR && r.IsArray() == ty.IsArray() && r.IsDynamic() == ty.IsDynamic() && r.IsTypedArray() == ty.IsTypedArray() &&
		isNullableScalar(r) == isNullableScalar(ty) && r.Nullable() == ty.Nullable() && reprKey(r) == reprKey(ty)
}

// classDerives reports whether class sub has base among its ancestors.
func (e *Emitter) classDerives(sub, base string) bool {
	info, ok := e.classes[sub]
	if !ok {
		return false
	}
	for _, anc := range info.AncestorChain {
		if anc == base {
			return true
		}
	}
	return false
}

// checkerClassType is the class the checker types init as, when it is a
// subclass of the one codegen infers (a method returning `this`,
// `http.createServer(…).listen(…)`, which codegen types by the method's
// declaring class): the more specific of the two.
func (e *Emitter) checkerClassType(init ast.Expression, ty Type) Type {
	c := e.front()
	if c == nil || !ty.IsClass() {
		return ty
	}
	t := c.TypeOf(init)
	if c.Unanswered(t) || t.Symbol == nil || t.Symbol.Name == ty.ClassName || !e.classDerives(t.Symbol.Name, ty.ClassName) {
		return ty
	}
	if info, ok := e.classes[t.Symbol.Name]; ok {
		return info.Ty
	}
	return ty
}

// localDeclType is the checker's type for a function's own unannotated
// local whose initializer is not a literal (LocalType).
func (e *Emitter) localDeclType(v *ast.VarDeclaration) (Type, bool) {
	if v.TypeAnnot != nil || v.Init == nil {
		return Type{}, false
	}
	// An object literal or class instance an accessor is defined on is
	// held boxed, its reads calling its accessors (a top-level one
	// included).
	if o := e.reprOracle(); o != nil && o.c.InProgram(v) && accessorLiteral(o.b, v) {
		return TypeAny, true
	}
	if e.shadowTopDecls[v] {
		return Type{}, false
	}
	if o := e.reprOracle(); o != nil && o.c.InProgram(v) && o.varSharesFunction(v) {
		return TypeAny, true
	}
	switch v.Init.(type) {
	case *ast.NullLiteral, *ast.BooleanLiteral, *ast.StringLiteral, *ast.TemplateLiteral,
		*ast.NumberLiteral, *ast.ArrayLiteral, *ast.ObjectLiteral, *ast.NewArrayExpression,
		*ast.NewMapExpression, *ast.NewSetExpression:
		return Type{}, false
	}
	o := e.reprOracle()
	if o == nil || !o.c.InProgram(v) {
		return Type{}, false
	}
	return o.LocalType(v)
}

// initHasFunction reports whether an initializer is, or is an object literal
// holding, a function.
func initHasFunction(init ast.Expression) bool {
	switch x := init.(type) {
	case *ast.ArrowFunction, *ast.FunctionExpression:
		return true
	case *ast.ObjectLiteral:
		for _, p := range x.Properties {
			if initHasFunction(p.Value) {
				return true
			}
		}
	}
	return false
}

// promotableInitInPrePass reports whether an un-annotated initializer's type is
// fully determinable in registerModuleGlobals' context. That pass runs before
// main()'s body, so only things resolvable then may appear: literals, an *earlier
// module global* (registered in source order, so visible via lookup exactly as in
// emitVarDecl), and a named non-generic non-async function's composite return
// type. An identifier bound to a runtime local (a destructuring target, a block
// local), a spread, a computed key, a generic/async call, or a scalar-returning
// call (which hits emitVarDecl's i64 default) would let the pre-pass type diverge
// from emitVarDecl's — so it is not promoted. This is the invariant the whole
// feature rests on ("promote iff the type cannot drift"), not a per-symptom gate.
func (e *Emitter) promotableInitInPrePass(expr ast.Expression) bool {
	// A promotable `new`-expression handle (class instance / Blob / Date / …) is
	// fully determinable here: classes are registered (Pass 0.5) before this
	// pre-pass (Pass 1.7), and a builtin handle's type is a constant.
	if _, ok := e.promotableNewExpr(expr); ok {
		return true
	}
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		return true
	case *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.TemplateLiteral:
		return true
	case *ast.Identifier:
		// An earlier module global, or a top-level function or class (its
		// value is fixed from the start).
		name := ex.Name
		if _, ok := e.moduleGlobals[name]; !ok {
			if m := e.nsSibling(name); m != "" {
				name = m // a sibling namespace member
			}
		}
		_, ok := e.moduleGlobals[name]
		return ok || e.topLevelCallables[name]
	case *ast.CallExpression:
		// `Symbol(desc)` / `Symbol.for(key)`: a Symbol, whatever the
		// (stable) argument.
		if e.isBuiltinSymbolCall(ex) {
			return e.prePassStableArgs(ex.Args)
		}
		id, ok := ex.Callee.(*ast.Identifier)
		if !ok {
			// A call of a builtin declaration's @lower target (`Buffer.from('…')`)
			// whose arguments are stable here: the decider's result type is the
			// one emission reads.
			if _, lowered := e.loweredCall(ex); lowered && e.prePassStableArgs(ex.Args) {
				return true
			}
			return e.prePassStableBuiltinCall(ex)
		}
		if _, isGeneric := e.genericFuncs[id.Name]; isGeneric {
			return false
		}
		// A generator object (`const it = gen()`): its type is the
		// generator's, fixed at registration.
		if _, isGen := e.generators[id.Name]; isGen && !e.isShadowedByLocal(id.Name) {
			return e.prePassStableArgs(ex.Args)
		}
		_, sig, found := e.resolveFuncRef(id.Name)
		if !found {
			// A builtin lowered to a library function returning a class
			// instance (`setInterval(f, ms)`'s Timeout), whatever its
			// arguments.
			if l, lowered := e.loweredCall(ex); lowered && l.libFn != "" {
				if _, lsig, ok := e.resolveFuncRef(l.libFn); ok && lsig.RetType.IsClass() && !lsig.MaySuspend && !lsig.IsAsync {
					return true
				}
			}
			return e.prePassStableBuiltinCall(ex)
		}
		if sig.MaySuspend || sig.IsAsync {
			return false
		}
		rt := sig.RetType
		return rt.IsArray() || rt.IsObject() || rt.IsMap() || rt.IsSet() || rt.IsDate() || rt.IsFunc() || isStringTy(rt) || isSimpleGlobalType(rt)
	case *ast.ArrayLiteral:
		for _, el := range ex.Elements {
			if _, isSpread := el.(*ast.SpreadElement); isSpread {
				return false
			}
			if !e.promotableInitInPrePass(el) {
				return false
			}
		}
		return true
	case *ast.ArrowFunction, *ast.FunctionExpression:
		// A function (a method of an object literal) whose free names are all
		// resolvable here: module globals already registered, top-level
		// functions and classes, and builtins. Its type then comes out the
		// same in the pre-pass as at the declaration.
		refs := map[string]bool{}
		scanExprFV(expr, map[string]bool{}, refs)
		for name := range refs {
			if _, ok := e.moduleGlobals[name]; ok {
				continue
			}
			// The binding itself, read by its own method when called
			// (`const o = { n() { return o.m() } }`).
			if name == e.prePassSelf && name != "" {
				continue
			}
			if e.topLevelNames[name] {
				if _, _, fn := e.resolveFuncRef(name); fn {
					continue
				}
				if _, cls := e.classes[name]; cls {
					continue
				}
				return false
			}
		}
		return true
	case *ast.ObjectLiteral:
		for _, prop := range ex.Properties {
			// A computed key, or a spread (Key=="" with a SpreadElement value),
			// depends on scope.
			if prop.KeyExpr != nil || prop.Key == "" {
				return false
			}
			if !e.promotableInitInPrePass(prop.Value) {
				return false
			}
		}
		return true
	case *ast.MemberExpression, *ast.IndexExpression:
		if e.dynamicGlobalPath(ex) {
			return true
		}
		if mem, ok := ex.(*ast.MemberExpression); ok {
			return e.constFoldableScalarInit(mem)
		}
		return false
	case *ast.BinaryExpression, *ast.UnaryExpression, *ast.ConditionalExpression:
		// A constant scalar expression (`4 * Math.PI * Math.PI`, `-Number.EPSILON`,
		// `A + B` over earlier module globals) — every leaf is a literal, a known
		// numeric builtin constant, or an already-registered module global, so its
		// type is context-stable and typeOfExpr returns the same scalar here as
		// in emitVarDecl's Binary/Unary/Member cases.
		return e.constFoldableScalarInit(ex)
	}
	return false
}

// elementBindingType is the type of a binding initialized by an element
// read: a typed array's element is a number, so the binding holds one, not
// the element's storage width (`let n = bytes[i]; n = n * 256 + …`).
func elementBindingType(ty Type) Type {
	if (ty.IsInteger() && ty.IR != "i64" || ty.IR == "float") && !ty.IsBigInt() && ty.IR != "i1" && !ty.IsDynamic() {
		wide := TypeF64
		wide.setNullable(ty.Nullable())
		wide.setUndefined(ty.IsUndefined())
		return wide
	}
	return ty
}

// dynamicGlobalPath reports whether x reads a property path (`c.a.b`,
// `c["k"]` with a constant key) off an `any` module global: its value is an
// `any` in the pre-pass and at the declaration alike.
func (e *Emitter) dynamicGlobalPath(x ast.Expression) bool {
	for {
		switch n := x.(type) {
		case *ast.MemberExpression:
			if n.Optional {
				return false
			}
			x = n.Object
			continue
		case *ast.IndexExpression:
			if n.Optional || !e.constFoldableScalarInit(n.Index) {
				return false
			}
			x = n.Object
			continue
		case *ast.Identifier:
			g, ok := e.moduleGlobals[n.Name]
			return ok && isUnconstrainedDynamic(g.Ty)
		}
		return false
	}
}

// constFoldableScalarInit reports whether an initializer is a compile-time
// constant scalar/string expression: built only from literals, the numeric
// builtin constants (`Math.PI`/`Number.EPSILON`/…), the special globals
// `NaN`/`Infinity`, and already-registered earlier module globals, combined with
// unary/binary operators. Every leaf resolves identically in the pre-pass and at
// emit time, so its typeOfExpr is context-stable — the invariant module-global
// promotion rests on. (A binding whose value is such an expression, e.g. a
// top-level `const SOLAR_MASS = 4 * Math.PI * Math.PI`, could not previously be
// read by a named function because it was never promoted.)
func (e *Emitter) constFoldableScalarInit(expr ast.Expression) bool {
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		return true // a number, or a bigint (one handle slot)
	case *ast.StringLiteral, *ast.BooleanLiteral, *ast.TemplateLiteral:
		return true
	case *ast.Identifier:
		if ex.Name == "NaN" || ex.Name == "Infinity" {
			return true
		}
		_, ok := e.moduleGlobals[ex.Name]
		if !ok {
			if m := e.nsSibling(ex.Name); m != "" {
				_, ok = e.moduleGlobals[m] // a sibling namespace member
			}
		}
		return ok
	case *ast.MemberExpression:
		return numericBuiltinConst(ex) || e.processConstMember(ex)
	case *ast.UnaryExpression:
		return e.constFoldableScalarInit(ex.Arg)
	case *ast.BinaryExpression:
		return e.constFoldableScalarInit(ex.Left) && e.constFoldableScalarInit(ex.Right)
	case *ast.ConditionalExpression:
		// `const lib = process.platform === 'darwin' ? 'a.dylib' : 'a.so'` —
		// the platform-pick idiom. Both arms must also be constant; the
		// caller's isSimpleGlobalType gate on the inferred type rejects arms
		// of differing kinds.
		return e.constFoldableScalarInit(ex.Test) && e.constFoldableScalarInit(ex.Consequent) && e.constFoldableScalarInit(ex.Alternate)
	}
	return false
}

// numericBuiltinConst reports whether a member expression is one of the constant
// numeric properties of the `Math`/`Number` builtins — all doubles, so an
// expression built from them has an unambiguous scalar type.
func numericBuiltinConst(mem *ast.MemberExpression) bool {
	id, ok := mem.Object.(*ast.Identifier)
	if !ok {
		return false
	}
	_, ok = builtinNumberConst(id.Name, mem.Property)
	return ok
}

// processConstMember reports whether a member expression is one of the
// context-stable `process` constants — `platform`/`arch`/`execPath`/`version`
// (strings) and `pid` (a number) — so `const isWin = process.platform ===
// 'win32'` promotes to a module global a named function can read (ADR-01080).
// `process.env.X`/`argv[i]` are excluded: their `string | undefined` shape is
// not a simple slot.
func (e *Emitter) processConstMember(mem *ast.MemberExpression) bool {
	id, ok := mem.Object.(*ast.Identifier)
	if !ok || id.Name != "process" || e.isShadowedByLocal("process") {
		return false
	}
	return processConstMembers[mem.Property]
}

// processConstMembers are process's members fixed for the life of the
// program.
var processConstMembers = map[string]bool{"platform": true, "arch": true, "execPath": true, "version": true, "pid": true}

// promotableNewExpr returns the type of a top-level binding initialized by a
// `new`-expression that is promotable to a module global (TDD-00093): a class
// instance (concrete or a fully-type-argumented generic, `new Box<number>()`),
// or a single-ptr-slot builtin handle (Blob/Date/URL/RegExp/Headers/…). It returns ok=false for anything not a single fixed slot (arrays
// and TypedArrays take the array-global path), for a dynamic/`any` object, and — as
// an exclusion — for `new Promise` (task-promise semantics). typeOfExpr is the
// canonical type source (it computes a generic instantiation's shape purely);
// registerModuleGlobals forces the generic class's real registration so a named
// function's method dispatch finds it.
func (e *Emitter) promotableNewExpr(init ast.Expression) (Type, bool) {
	init = e.dealiasNew(init)
	switch ne := init.(type) {
	case *ast.NewExpression:
		if ne.ClassName == "Promise" {
			return Type{}, false
		}
		// FinalizationRegistry is an opaque single-ptr handle (TDD-00163), not a
		// user/generic class — promote it, falling through to the
		// typeOfExpr + single-slot gate below.
		if ne.ClassName == "FinalizationRegistry" {
			break
		}
		_, concrete := e.classes[ne.ClassName]
		genDecl, generic := e.genericClasses[ne.ClassName]
		if !concrete && !generic {
			return Type{}, false
		}
		// A generic class instance (`new Box<number>()`) promotes too: its shape
		// comes from genericClassInstanceType via typeOfExpr below — a *pure*
		// lookup (no IR emission / no e.classes registration as a side effect),
		// safe in this pre-pass. Only a full type-argument list is resolvable.
		if generic {
			if _, ok := e.newTypeArgs(genDecl, ne); !ok {
				return Type{}, false
			}
		}
	default:
		if !isHandleNewExpr(init) {
			return Type{}, false
		}
	}
	ty := e.typeOfExpr(init)
	// A single fixed slot only: exclude an array/TypedArray
	// and a dynamic/`any` object. A ptr handle (Blob/URL/class/…) or a
	// scalar-backed handle (a `Date` is an i64 epoch) both store into one global
	// via the shape-appropriate path emitVarDecl already dispatches on.
	if ty.IsDynamicObject() || ty.IsArray() || ty.IR == "" || ty.IR == "void" {
		return Type{}, false
	}
	return ty, true
}

// isHandleNewExpr reports whether init is one of the builtin `new`-handle
// constructors this promotion covers — the plain value/data handles verified
// (targeted probes + a full E2E-suite pass) to construct and dispatch correctly
// as a module global. Class instances (`new C()`) are handled separately in
// promotableNewExpr and ride the ordinary class object-var path.
//
// Deliberately excluded: the connection handle `Worker` (its construction
// opens a thread, ill-suited to a module-global-at-startup),
// `BroadcastChannel`/`MessageChannel`, and `XMLHttpRequest`. They stay `main()` locals (the prior behavior).
//
// (The streams use dedicated var-decl emitters that ignored
// the module global; `storePtrHandleVarDecl` makes those promotion-aware.)
func isHandleNewExpr(init ast.Expression) bool {
	switch init.(type) {
	case *ast.NewDateExpression,
		*ast.NewErrorExpression, *ast.NewRegExpExpression,
		*ast.NewArrayBufferExpression,
		*ast.NewRequestExpression,
		*ast.NewReadableStreamExpression, *ast.NewWritableStreamExpression,
		*ast.NewTransformStreamExpression, *ast.NewCompressionStreamExpression:
		return true
	}
	return false
}

// moduleGlobalPtrOrLocal returns the storage pointer for a single-ptr binding v
// (object/`Map`/`Set`): the pre-registered module global (TDD-00093) for a
// promoted decl — already in e.moduleGlobals and zero-initialized — or a fresh
// local ptr alloca with the Symbol defined, the prior behavior.
func (e *Emitter) moduleGlobalPtrOrLocal(v *ast.VarDeclaration, ty Type) string {
	e.shadowLocalGlobalType(v, ty)
	if e.promotedGlobalDecls[v] {
		return e.moduleGlobals[v.Name].Ptr
	}
	if fsym, ok := e.forwardBoxes[v]; ok {
		// A closure built before this declaration already captured its cell.
		fsym.Ty = ty
		e.define(v.Name, e.forwardInitialized(fsym))
		return fsym.Ptr
	}
	if e.hoistedCaptures[v.Name] && ty.IR == irPtr && !ty.IsArray() {
		// Captured by a nested closure: the heap cell is made here, where it
		// dominates every use — not at the capturing closure, which may sit
		// in one branch.
		cell := e.boxHoistedCapture(v.Name, TypePtr, "null", v.Kind == "const", v.Kind == "var")
		sym, _ := e.lookup(v.Name)
		sym.Ty = ty
		e.define(v.Name, sym)
		return cell
	}
	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", ptrName))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})
	return ptrName
}

// prePassStableBuiltinCall reports whether an un-annotated top-level initializer
// that is a call to something other than a user function has a type the pre-pass
// can rely on: a simple scalar/string result from a call whose receiver (if any)
// is itself resolvable before main() runs — a literal, an earlier module global,
// or a builtin namespace — never a main()-local, whose type is not known yet.
// Without this, `const t0 = Date.now()` or `const iv = setInterval(f, 50)`
// stayed a main() local and no named function could reference it at all
// ("undefined variable").
func (e *Emitter) prePassStableBuiltinCall(ex *ast.CallExpression) bool {
	// An explicit list, not "whatever typeOfExpr says": for a call it does not
	// model, inference falls back to a default scalar, and promoting on that
	// guess would give the global the wrong type (`structuredClone(set)` is not
	// a number). Every entry here has a fixed scalar/string result.
	switch callee := ex.Callee.(type) {
	case *ast.Identifier:
		if e.topLevelNames[callee.Name] {
			return false
		}
		if _, isLocal := e.lookup(callee.Name); isLocal {
			return false
		}
		if !prePassScalarGlobalFns[callee.Name] {
			return false
		}
	case *ast.MemberExpression:
		switch obj := callee.Object.(type) {
		case *ast.Identifier:
			if g, isGlobal := e.moduleGlobals[obj.Name]; isGlobal {
				// a method on an earlier string global
				if !isStringTy(g.Ty) || !prePassScalarStringMethods[callee.Property] {
					return false
				}
				break
			}
			if e.topLevelNames[obj.Name] {
				return false // a top-level binding whose type is not known yet
			}
			if _, isLocal := e.lookup(obj.Name); isLocal {
				return false
			}
			methods, ok := prePassScalarNamespaces[obj.Name]
			if !ok || (methods != nil && !methods[callee.Property]) {
				return false
			}
		case *ast.StringLiteral, *ast.TemplateLiteral:
			if !prePassScalarStringMethods[callee.Property] {
				return false
			}
		default:
			return false
		}
	default:
		return false
	}
	return isSimpleGlobalType(e.typeOfExpr(ex))
}

// isBuiltinSymbolCall reports `Symbol(…)` or `Symbol.for(…)` naming the
// builtin, not a program binding.
func (e *Emitter) isBuiltinSymbolCall(ex *ast.CallExpression) bool {
	var name string
	switch c := ex.Callee.(type) {
	case *ast.Identifier:
		name = c.Name
	case *ast.MemberExpression:
		id, ok := c.Object.(*ast.Identifier)
		if !ok || c.Property != "for" {
			return false
		}
		name = id.Name
	default:
		return false
	}
	if name != "Symbol" || e.topLevelNames[name] {
		return false
	}
	_, isLocal := e.lookup(name)
	return !isLocal
}

// Builtin calls whose result is always a plain number / string / boolean.
var prePassScalarGlobalFns = map[string]bool{
	"parseInt": true, "parseFloat": true, "Number": true, "String": true, "Boolean": true,
	"isNaN": true, "isFinite": true,
	"encodeURIComponent": true, "decodeURIComponent": true, "encodeURI": true, "decodeURI": true,
	"btoa": true, "atob": true, "BigInt": true,
}

// Namespaces: nil = every method qualifies (all of Math returns a number).
var prePassScalarNamespaces = map[string]map[string]bool{
	"Math":   nil,
	"Date":   {"now": true, "parse": true, "UTC": true},
	"Number": {"parseInt": true, "parseFloat": true, "isInteger": true, "isFinite": true, "isNaN": true, "isSafeInteger": true},
	"String": {"fromCharCode": true, "fromCodePoint": true},
	"JSON":   {"stringify": true},
}

var prePassScalarStringMethods = map[string]bool{
	"toUpperCase": true, "toLowerCase": true, "trim": true, "trimStart": true, "trimEnd": true, "trimLeft": true, "trimRight": true,
	"slice": true, "substring": true, "substr": true, "repeat": true, "padStart": true, "padEnd": true,
	"replace": true, "replaceAll": true, "concat": true, "charAt": true, "normalize": true,
	"indexOf": true, "lastIndexOf": true, "includes": true, "startsWith": true, "endsWith": true,
	"charCodeAt": true, "codePointAt": true, "localeCompare": true,
}

// isSimpleGlobalType reports whether a type occupies a single scalar/string slot —
// the only shapes registerModuleGlobals promotes, since they reach emitVarDecl's
// plain alloca path (never the array/object/Map/nullable-scalar sub-emitters).
func isSimpleGlobalType(ty Type) bool {
	if ty.IsArray() || ty.IsObject() || ty.IsDynamicObject() || ty.IsMap() || ty.IsSet() || ty.IsDynamic() || ty.IsFunc() || isNullableScalar(ty) {
		return false
	}
	switch ty.IR {
	case "i1", "i8", "i16", "i32", "i64", "float", "double":
		return true
	}
	return isStringTy(ty) || ty.IsBigInt()
}

// emitVarSlotDefault pre-initializes a scalar or dynamic `var` slot in the
// entry block to a deterministic default (an any-typed var to the `undefined`
// box `{ i8 5, i64 0 }`, a typed scalar to its zero literal). Only scalar and
// dynamic types reach this — array/object/map/set var declarations are handled
// by their own sub-emitters before the scalar path. The store is a compile-time
// constant, so it is valid to place in the entry (alloca) block. See TDD-00070.
func (e *Emitter) emitVarSlotDefault(ptrName string, ty Type) {
	if ty.IsDynamic() {
		e.emitAlloca(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, ptrName))
		return
	}
	e.emitAlloca(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", ty.IR, ty.zeroLiteral(), ptrName, ty.Align()))
}

// emitVarDecl handles variable declarations (scalar, array, and object).
// storePtrHandleVarDecl stores a just-built single-ptr handle value (a
// stream/CompressionStream/… whose var-decl is emitted by a dedicated
// emitNewX rather than the generic object path) into v's storage: the
// pre-registered module global when v is promoted (TDD-00093/ADR-00342), else a
// fresh local ptr alloca with the Symbol defined. Without the promoted branch a
// promoted binding's global stayed null and a named function reading it saw an
// empty handle (a promoted `ReadableStream` silently dropped its chunks).
func (e *Emitter) storePtrHandleVarDecl(v *ast.VarDeclaration, val Value) {
	e.shadowLocalGlobalType(v, val.Ty)
	var ptrName string
	if e.promotedGlobalDecls[v] {
		ptrName = e.moduleGlobals[v.Name].Ptr
	} else if fsym, ok := e.forwardBoxes[v]; ok {
		// A closure built before this declaration already captured its cell.
		ptrName = fsym.Ptr
		fsym.Ty = val.Ty
		e.define(v.Name, e.forwardInitialized(fsym))
	} else if e.hoistedCaptures[v.Name] {
		// Captured by a nested closure: the heap cell is made here, where it
		// dominates every use — not at the capturing closure, which may sit
		// in one branch.
		ptrName = e.boxHoistedCapture(v.Name, TypePtr, "null", v.Kind == "const", v.Kind == "var")
		sym, _ := e.lookup(v.Name)
		sym.Ty = val.Ty
		e.define(v.Name, sym)
	} else {
		ptrName = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", ptrName))
		e.define(v.Name, Symbol{Ptr: ptrName, Ty: val.Ty, IsConst: v.Kind == "const"})
	}
	e.heapStore(irPtr, val.Ref, ptrName, 8)
}

func (e *Emitter) emitVarDecl(v *ast.VarDeclaration) error {
	if e.anyHeldDecl(v) {
		// Holds the box its initializer gives (emit_anyarg.go). Annotated in
		// place, not on a copy: the declaration keys every per-declaration
		// table (a promoted module global's storage among them).
		saved := v.TypeAnnot
		v.TypeAnnot = ast.AnyAnnotation("ts")
		if e.anyHeldEmitting == nil {
			e.anyHeldEmitting = map[*ast.VarDeclaration]bool{}
		}
		e.anyHeldEmitting[v] = true
		defer func() { v.TypeAnnot = saved; delete(e.anyHeldEmitting, v) }()
	}
	if err := e.emitVarDeclBody(v); err != nil {
		return err
	}
	if err := e.settleForwardCell(v); err != nil {
		return err
	}
	// `const f = k.greet` holds a method value, which reads its `this`.
	if me, ok := v.Init.(*ast.MemberExpression); ok && v.Kind == "const" && e.isClassMethodRead(me) {
		if sym, ok := e.lookup(v.Name); ok && sym.Ptr != "" {
			if e.methodValueSlots == nil {
				e.methodValueSlots = map[string]bool{}
			}
			e.methodValueSlots[sym.Ptr] = true
		}
	}
	// `const a = new C(…)` holds a C for its whole life: its field accesses
	// need no foreign-instance check (foreignFieldTags).
	if ne, ok := v.Init.(*ast.NewExpression); ok && v.Kind == "const" && !ne.Qualified {
		if sym, ok := e.lookup(v.Name); ok && sym.Ty.IsClass() && !sym.Boxed && sym.Ptr != "" {
			if info, ok := e.classes[sym.Ty.ClassName]; ok && info.Ty.ClassName == sym.Ty.ClassName {
				if e.exactClassSlots == nil {
					e.exactClassSlots = map[string]bool{}
				}
				e.exactClassSlots[sym.Ptr] = true
			}
		}
	}
	return nil
}

func (e *Emitter) emitVarDeclBody(v *ast.VarDeclaration) error {
	v.Init = e.dealiasNew(v.Init)
	// TDD-00134 Stage 1 (-optimize-memory): a planned declaration's object
	// literal is emitted into an entry-block alloca. The marker names the
	// exact literal node so nested literals inside it stay heap-allocated;
	// emitObjectLiteralWithHint consumes (or, on a path that isn't a plain
	// fixed-shape struct, clears) it.
	// A module global outlives the function that initializes it (a builtin
	// module's init returns), so its value stays on the heap.
	if e.stackAllocPlan[v] && !e.promotedGlobalDecls[v] {
		switch v.Init.(type) {
		case *ast.ObjectLiteral, *ast.ArrowFunction, *ast.FunctionExpression,
			*ast.ArrayLiteral, *ast.NewExpression:
			e.pendingStackAllocLit = v.Init
			defer func() { e.pendingStackAllocLit = nil }()
		}
	}
	// A Map/Set/WeakMap/WeakSet/WeakRef/channel under an `any`/`unknown`/
	// union annotation is a boxed value like any other (a host cell): the
	// typed declarations below are for its own type.
	dynAnnot := v.TypeAnnot != nil && e.annotationType(v.TypeAnnot).IsDynamic()
	if v.TypeAnnot != nil && !dynAnnot && e.newCollectionHint == nil {
		if _, direct := v.Init.(*ast.ConditionalExpression); direct {
			defer e.setCollectionHint(e.annotationType(v.TypeAnnot), v.Init)()
		}
	}
	if !dynAnnot {
		if init, ok := v.Init.(*ast.NewMapExpression); ok {
			return e.emitMapVarDecl(v, init)
		}
		if init, ok := v.Init.(*ast.NewSetExpression); ok {
			return e.emitSetVarDecl(v, init)
		}
		if init, ok := v.Init.(*ast.NewWeakMapExpression); ok {
			val, err := e.emitNewWeakMapValueAnnotated(init, v.TypeAnnot)
			if err != nil {
				return err
			}
			e.storePtrHandleVarDecl(v, val)
			return nil
		}
		if init, ok := v.Init.(*ast.NewWeakSetExpression); ok {
			val, err := e.emitNewWeakSetValue(init)
			if err != nil {
				return err
			}
			e.storePtrHandleVarDecl(v, val)
			return nil
		}
	}
	if init, ok := v.Init.(*ast.NewCompressionStreamExpression); ok && !dynAnnot {
		val, err := e.emitNewCompressionStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewTransformStreamExpression); ok && !dynAnnot {
		val, err := e.emitNewTransformStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewWritableStreamExpression); ok && !dynAnnot {
		val, err := e.emitNewWritableStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewReadableStreamExpression); ok && !dynAnnot {
		val, err := e.emitNewReadableStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}

	ty := e.annotationType(v.TypeAnnot)
	// A binding annotated `void` holds only undefined.
	if ty.IR == "void" {
		ty = TypeUndefined
	}
	// An untyped, uninitialized binding (`let c;`) is a dynamic `undefined`,
	// exactly as JS, and TypeScript's evolving `any`: each read takes the type
	// its last assignment gives it (the checker's flow type, unboxed by
	// checkerNarrowed), not a number slot (which read `c = q.read()` as 0).
	if v.TypeAnnot == nil && v.Init == nil {
		ty = TypeAny
	}
	// -compat=js widening (TDD-00162): an untyped binding assigned two or more
	// distinct scalar kinds over its lifetime (`let x = 5; x = 'hi'`) is backed
	// by the any-box from declaration, so every store boxes and the later
	// different-kinded assignment works instead of being a strict-lane rejection
	// (ADR-00651). The pre-pass (crossTypeWidenedBindings) already gated this on
	// -compat=js and on the binding being untyped, so honor it unconditionally.
	if v.TypeAnnot == nil && e.widenedBindings[v.Name] {
		ty = TypeAny
	}

	// Infer type from init when no annotation — not for a widened binding,
	// whose any-box the initializer's own type would undo.
	if !ty.IsArray() && !ty.IsObject() && v.TypeAnnot == nil && !e.widenedBindings[v.Name] {
		// A chain that continues past a `?.` (`a?.b.c`) is `T | undefined` as a
		// whole (emitOptionalChain): the per-shape cases below look only at the
		// outermost link and would size the slot for a bare T.
		preInit := v.Init
		if optionalChainDepth(v.Init) >= 0 {
			ty = e.typeOfExpr(v.Init)
			preInit = nil
		}
		// A function's own local takes the checker's type for it
		// (LocalType); the cases below type the rest: a literal by its
		// syntax, a binding the checker never saw by its parts.
		if lt, ok := e.localDeclType(v); ok && preInit != nil {
			ty = lt
			preInit = nil
		}
		switch init := preInit.(type) {
		case *ast.NullLiteral:
			if init.IsUndefined {
				ty = TypeUndefined
			} else {
				ty = TypeNull
			}
		case *ast.BooleanLiteral:
			// Without this case an unannotated `let b = true` fell through to
			// the switch's blind TypeI64 default, so the boolean stored as i64
			// and printed as `1`/`0` instead of `true`/`false` (a comparison- or
			// `!`-valued initializer already infers bool via the BinaryExpression
			// /UnaryExpression cases — only a bare boolean literal was missed).
			ty = TypeBool
		case *ast.StringLiteral:
			ty = TypePtr
		case *ast.TemplateLiteral:
			ty = TypePtr
		case *ast.Identifier:
			if sym, ok := e.lookup(init.Name); ok {
				ty = sym.Ty
			} else if _, sig, found := e.resolveFuncRef(init.Name); found {
				// A named function taken by value (`const g = f`) is a closure
				// value — a ptr — so the slot must be sized as one.
				ty = funcValueType(sig)
			} else if decl, isGeneric := e.genericFuncs[init.Name]; isGeneric {
				ty = e.erasedGenericValueType(decl) // its erased instantiation
			} else if m, lowered := e.loweredGlobalFn(init); lowered {
				// A builtin lowered to a library function (`const st =
				// setTimeout`): that function's value.
				if _, sig, found := e.resolveFuncRef(m); found {
					ty = funcValueType(sig)
				}
			} else if _, isClass := e.classes[init.Name]; isClass {
				ty = TypeAny // `const K = C`: the constructor reference
			} else if isErrorKindName(init.Name) || sema.IsBuiltinConstructor(init.Name) || init.Name == "Promise" {
				ty = TypeAny // `const M = Map`: a builtin constructor reference
			} else if lty := e.typeOfExpr(init); lty.IsFunc() || init.Name == "process" && e.processIsValue(init) {
				// A capturing nested function declaration referenced before its
				// declaration statement (TDD-00129 Stage 2 letrec) — the closure
				// type typeOfExpr mirrors from the pending declaration. Or
				// `process` as a value, its emitter object.
				ty = lty
				if init.Name == "process" {
					ty = TypeAny
				}
			} else {
				// Any other global read as a value (NaN, a namespace object):
				// what emitIdent produces for it.
				ty = e.typeOfExpr(init)
			}
		case *ast.IndexExpression:
			ty = elementBindingType(e.typeOfExpr(init))
		case *ast.AsExpression:
			// `expr as T` — a narrowing from a dynamic operand adopts T, so the
			// binding's slot is the concrete asserted type (ADR-00929);
			// typeOfExpr decides narrow-vs-erase. A widening to a union
			// (`"5" as string | number`) types the binding by it, as tsc does:
			// the slot is the box.
			ty = e.asBindingType(init)
		case *ast.ArrayLiteral:
			ty = e.declaredArrayType(v.Name, init)
		case *ast.ObjectLiteral:
			// `-compat=js` (TDD-00022 break shape 4): an untyped object
			// literal is a D1 dynamic object — dynamic add/delete,
			// descriptors, accessors all work on it; the boxed-field
			// arithmetic runs through the TDD-00076 dispatch. strict keeps
			// the static struct (native-speed fields).
			if e.compatJS() || len(init.Properties) == 0 {
				ty = TypeAny // an empty one's `{}` holds any value
			} else if st, ok := e.selfReadLiteralType(v); ok {
				ty = st
			} else if e.hasDynamicSpread(init) {
				ty = TypeAny // built as the object JavaScript makes (emitObjectLiteralWithHint)
			} else {
				ty = e.typeOfExpr(init)
			}
		case *ast.NewArrayExpression:
			if init.ElemType != nil {
				ty = ArrayOf(e.annotationType(init.ElemType))
			}
		case nil:
		default:
			// Every other initializer is held as it is emitted: its type.
			ty = e.typeOfExpr(init)
		}
	}
	if call, ok := v.Init.(*ast.CallExpression); ok && v.TypeAnnot == nil && e.compatJS() && !ty.IsDynamic() {
		// A JavaScript array intrinsic whose result holds what TypeScript's
		// type does not (`[1].concat(["z"])`, a reduce to a string from a
		// number seed): the value it emits.
		key := call.Intrinsic
		if mem, ok := call.Callee.(*ast.MemberExpression); ok && key == "" && e.typeOfExpr(mem.Object).IsArray() {
			key = "Array.prototype." + mem.Property
		}
		if in, known := intrinsics[key]; known && in.ty != nil {
			if it := in.ty(e, call); isUnconstrainedDynamic(it) || it.IsArray() && ty.IsArray() && it.ElemType != nil && isUnconstrainedDynamic(*it.ElemType) && arrayStorageKey(it) != arrayStorageKey(ty) {
				ty = it
			}
		}
	}
	if na, ok := v.Init.(*ast.NewArrayExpression); ok && !ty.IsArray() && !ty.IsDynamic() {
		ty = ArrayOf(newArrayElemType(e, na)) // an any[] without a type argument
	}

	// --no-any (TDD-00209 Stage 2): an unannotated binding whose value is itself
	// `any` — e.g. `const x = JSON.parse(s)` with no `as T` — is the value-level
	// escape hatch. Reject it too (a constrained union has UnionMembers and is a
	// checked type, not `any`). Explicit `: any` annotations are already caught in
	// annotationType.
	// A call answered `any` (an implementation signature behind overloads)
	// whose overload the checker resolves to a primitive or a Buffer: that
	// type, as the call's own value is (emitExpr).
	if call, ok := v.Init.(*ast.CallExpression); ok && v.TypeAnnot == nil && isUnconstrainedDynamic(ty) && ty.DynPropTy == nil {
		if pt, ok := e.checkerPrimitive(call); ok {
			ty = pt
		}
	}
	if err := validateCompositeType(ty, v.GetPos().Line, v.GetPos().Col); err != nil {
		return err
	}
	// An uninitialized union-typed binding defaults to a boxed `undefined`
	// (the IsDynamic no-init path below), matching JS's hoisted-var/`let`
	// semantics — previously a rejection (ADR-00475); `const` without an
	// initializer is still rejected at parse time, and `let` reads before
	// assignment stay definite-assignment errors.
	//
	// A `var` whose declaration can be skipped on the way to a read is really
	// `T | undefined` at run time (sloppy hoisting: `if (c) { var r = 1 }
	// use(r)`), even though tsc types the binding as plain `T` — the same shape
	// an out-of-range `a[i]` read has (indexReadType, ADR-01040). Widen it so a
	// read on the skipped path yields a real `undefined` rather than the type's
	// zero (ADR-01057).
	hoisted := v.Kind == "var" && e.hoistedVarMaySkip()
	if hoisted && (ty.IsArray() || ty.IsObject() || ty.IsDynamicObject()) {
		ty = indexReadType(ty)
	}
	if g, ok := e.moduleGlobals[v.Name]; ok && e.promotedGlobalDecls[v] && g.Ty.IsDynamic() && !ty.IsDynamic() {
		// A module global the pre-pass made any (a JavaScript binding
		// read by functions): its initializer is boxed into it below, not
		// stored in an array's or object's own layout.
		ty = g.Ty
	}
	if ty.IsArray() {
		// TDD-00134 Stage 2: `/** @value */` opts this binding into the flat
		// value-type layout instead of the default pointer-slot array.
		if v.ValueArr {
			return e.emitFlatArrayVarDecl(v, ty)
		}
		return e.emitArrayVarDecl(v, ty)
	}
	if v.ValueArr {
		return internalAt(v.GetPos(), "@value applies to an array declaration")
	}
	if ty.IsObject() || ty.IsDynamicObject() {
		return e.emitObjectVarDecl(v, ty)
	}

	// With no explicit type, a numeric-literal initializer is a `number`
	// (float64, TDD-00123) — or a bigint for a `123n` literal. Every numeric
	// literal is a double, so an integer-looking `1` gets an f64 slot too, not
	// the old i64 default (which corrupted a store-back or closure capture of an
	// unannotated `let a = 1`).
	if v.TypeAnnot == nil && !ty.IsDynamic() {
		if nl, ok := v.Init.(*ast.NumberLiteral); ok {
			if nl.IsBigInt {
				ty = BigIntType()
			} else {
				ty = TypeF64
			}
		}
	}

	// Skippable `var` (see above): a scalar/string slot becomes `T | undefined`.
	if hoisted && !ty.IsDynamic() && ty.IR != "void" {
		ty = indexReadType(ty)
	}

	// A module global registered with undefined storage
	// (globalUndefinedStorage) is stored as that.
	if e.promotedGlobalDecls[v] && v.TypeAnnot != nil {
		if g := e.moduleGlobals[v.Name]; g.Ty.IsUndefined() && !ty.Nullable() && (isNullableScalar(g.Ty) || isStringTy(g.Ty)) {
			ty = g.Ty
		}
	}
	// A typed local declared without a value (`let n: number;`) holds
	// undefined until assigned: a read before then (a path the definite-
	// assignment check cannot see, a closure) reads undefined, as in Node.
	if v.Init == nil && v.TypeAnnot != nil && !v.Ambient && !e.promotedGlobalDecls[v] && !ty.Nullable() && !ty.IsDynamic() && len(ty.UnionMembers) == 0 {
		switch {
		case (ty.Float && ty.IR == "double") || ty.IR == "i1":
			ty = undefinedableElem(ty)
			ty.setUndefined(true)
		case isStringTy(ty):
			ty.setNullable(true)
			ty.setUndefined(true)
		}
	}

	// A nullable non-pointer scalar (`number | null`, `boolean | null`, ...)
	// gets a presence-flagged { i1, T } slot rather than a bare scalar — see
	// emit_nullable_scalar.go / TDD-00064.
	if isNullableScalar(ty) {
		e.shadowLocalGlobalType(v, ty)
		if hoisted {
			// The slot must read absent on the path where this declaration never
			// ran; the { i1, T } alloca gets its `undefined` in the entry block.
			ptrName := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, nullableScalarStorageIR(ty), storageAlign(ty)))
			e.emitAlloca(fmt.Sprintf("store %s zeroinitializer, "+irPtr+" %s, align %d", nullableScalarStorageIR(ty), ptrName, storageAlign(ty)))
			e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: false})
			if v.Init == nil {
				return nil
			}
			return e.storeNullableScalar(ptrName, ty, v.Init)
		}
		if e.promotedGlobalDecls[v] || e.hoistedCaptures[v.Name] {
			// A module global (registerModuleGlobals) or an eagerly boxed
			// capture (boxHoistedCapture): the same { i1, T } aggregate, in a
			// global or a heap cell instead of an alloca.
			var ptrName string
			if e.promotedGlobalDecls[v] {
				ptrName = e.moduleGlobals[v.Name].Ptr
			} else {
				ptrName = e.boxHoistedNullableCapture(v.Name, ty, v.Kind == "const")
			}
			if v.Init == nil {
				e.storeNullableScalarAbsent(ptrName, ty)
				return nil
			}
			return e.storeNullableScalar(ptrName, ty, v.Init)
		}
		return e.emitNullableScalarVarDecl(v, ty)
	}

	// An initializer that produces no value (`var r = (function () { … })()`,
	// a void call through any callee shape the inference above resolved) must
	// not make `void` the slot type: `alloca void` is invalid IR, and it would be
	// emitted before the void-initializer handler below gets to rebind the name
	// as `undefined` (ADR-00479). Each inference branch above guards this for its
	// own shape; this is the one net under all of them. The default slot is left
	// dead, exactly as in those branches.
	declared := ty // a void initializer's undefined (the placeholder below)
	voidInit := ty.IR == "void"
	if voidInit {
		declared = TypeUndefined
	}
	if ty.IR == "void" {
		ty = TypeI64
	}

	// Module-global promotion (TDD-00093): at module scope, a top-level
	// const/let/var of a simple scalar/string type is backed by a pre-registered
	// LLVM global (registerModuleGlobals) so a named `function` can read it. Store
	// into that global rather than a fresh local alloca; it's already zero-init'd
	// (the global's own initializer) and already in e.moduleGlobals (so lookup
	// resolves it), so neither the default-init nor a local define is needed.
	e.shadowLocalGlobalType(v, declared)
	var ptrName string
	if e.promotedGlobalDecls[v] {
		ptrName = e.moduleGlobals[v.Name].Ptr
		// The global's type is the declaration's (the checker's, as the
		// pre-pass read it): the initializer is coerced to it, or rejected
		// cleanly if it cannot be.
		ty = e.moduleGlobals[v.Name].Ty
	} else if fsym, ok := e.forwardBoxes[v]; ok {
		// A closure built before this declaration already captured its
		// cell (forwardClosureSym): store into that one.
		ptrName, ty = fsym.Ptr, fsym.Ty
		e.define(v.Name, e.forwardInitialized(fsym))
	} else if e.hoistedCaptures[v.Name] {
		// Captured by a nested closure: heap-box eagerly here at the declaration
		// point (which dominates the whole lexical scope) rather than lazily at the
		// capturing closure's construction site — that site may sit inside a
		// conditional block that doesn't dominate a later read of the box, which
		// clang rejects as "instruction does not dominate all uses" (see
		// hoistedCaptures / boxHoistedCapture). Seed with the type default; the
		// initializer's own store below re-resolves through the symbol to this box.
		// This is exactly the set promoteCaptureToCell would have boxed, only earlier.
		ptrName = e.boxHoistedCapture(v.Name, ty, "", v.Kind == "const", v.Kind == "var")
	}
	if ptrName == "" {
		ptrName = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, ty.IR, ty.Align()))
		if v.Kind == "var" || v.Init == nil {
			// Pre-initialize the slot in the entry block to a deterministic default
			// (`undefined` for an any-typed slot, zero for a typed scalar) whenever a
			// read could reach it before a store:
			//   - a `var` is function-scoped (promoteVarToFuncScope), so its slot can
			//     be read on a path where its own initializer never ran (e.g.
			//     `if (c) { var r = 1 } use(r)` with c false); and
			//   - a `let`/`const` with no initializer (`let x: number;`) can be read
			//     on a path the definite-assignment analysis soundly misses (a var
			//     assigned only inside a maybe-skipped loop, ADR-00214). Without this
			//     that read would be uninitialized memory rather than a defined value.
			// A read textually *before* the declaration is still a clean
			// undefined-variable/TDZ error, since the binding isn't visible until its
			// declaration point. See TDD-00070/TDD-00071.
			e.emitVarSlotDefault(ptrName, ty)
		}
		e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})
	}

	if v.Init != nil {
		// JSON.parse / Response.json() (optionally awaited) need the target type
		// for correct deserialization — shared with the object/array var-decl
		// emitters via emitDeclJSONProjection (emit_objects.go).
		if val, ok, err := e.emitDeclJSONProjection(v.Init, ty); ok {
			if err != nil {
				return err
			}
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", ty.IR, val.Ref, ptrName, ty.Align()))
			return nil
		}
		// Mark this binding as mid-initialization so a closure emitted inside its
		// own initializer that captures it (`const s = f(() => use(s))`) seeds its
		// capture cell with a default rather than by loading the still-unwritten
		// slot — see promoteCaptureToCell. Restored (not just deleted) so a nested
		// same-named binding in an outer initializer is unaffected.
		wasInitializing := e.varsBeingInitialized[v.Name]
		e.varsBeingInitialized[v.Name] = true
		val, err := e.emitExprWithObjectHint(v.Init, ty)
		e.varsBeingInitialized[v.Name] = wasInitializing
		if err != nil {
			return err
		}
		// A void-typed initializer (a call to a function/method that returns
		// nothing). An *untyped* binding takes JS's own semantics — the call
		// ran for its side effects and the binding is `undefined`
		// (ADR-00479; the pre-inference default alloca above is simply left
		// dead), as does one annotated `void`; one annotated `any` or
		// `unknown` holds undefined. Any other annotation keeps the clean
		// rejection (tsc's: void is assignable to nothing else). A call typed
		// void that emits a real undefined (FinalizationRegistry.register)
		// is the same binding.
		if val.Ty.IR == "void" || voidInit && val.Ty.IsUndefined() {
			if ty.IsDynamic() && ty.UnionMembers == nil && ty.IR == "i64" {
				e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, ptrName))
				return nil
			}
			if v.TypeAnnot != nil && !annotIs(v.TypeAnnot, "void") {
				return internalAt(v.GetPos(), "checker-rejected: cannot assign the result of a void expression to a variable")
			}
			undefPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", undefPtr))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", undefPtr))
			e.define(v.Name, Symbol{Ptr: undefPtr, Ty: TypeUndefined, IsConst: v.Kind == "const"})
			return nil
		}
		if ty.IsDynamic() {
			if ty.UnionMembers != nil && !unionAllowsAssignmentFrom(ty, val.Ty) {
				return internalAt(v.GetPos(), "checker-rejected: value's type is not a member of the declared union type")
			}
			// Allocation-site widening (TDD-00155 Stage 6): a fresh `new C()`
			// flowing straight into an `any` slot is realized as a D1 bag rather
			// than a tag-6 box, so `a.x`/`Object.keys(a)`/index access work.
			val, err = e.emitBoxValueWidened(val, v.Init)
			if err != nil {
				return err
			}
		} else {
			if !convertiblePure(val.Ty, ty) {
				return internalAt(v.GetPos(), "checker-rejected: initializer's type is incompatible with the variable's type")
			}
			// A string slot given a value that may be undefined (a read past an
			// array's end) holds that undefined: the binding keeps it, so it
			// prints, compares and types as undefined (TS types it string).
			keepUndef := false
			switch {
			case isStringTy(ty) && !ty.Nullable() && !ty.IsDynamic() && val.Ty.Nullable() && val.Ty.IsUndefined() && !val.Ty.NullAndUndef() && isStringTy(val.Ty.withoutNullable()):
				ty.setNullable(true)
				ty.setUndefined(true)
				keepUndef = true
			case ty.Float && ty.IR == "double" && !ty.Nullable() && isNullableScalar(val.Ty) && val.Ty.IsUndefined() && !val.Ty.NullAndUndef() && val.Ty.withoutNullable().Float:
				// The same for a local number (`const q: number = a[4]`): a
				// slot of its own, as `number | undefined` has. A module
				// global's storage is fixed before its initializer runs.
				if sym, ok := e.lookup(v.Name); ok && strings.HasPrefix(sym.Ptr, "%") && !sym.Boxed {
					nty := undefinedableElem(ty)
					nty.setUndefined(true)
					slot := e.freshReg()
					e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, storageIR(nty)))
					cv := e.convert(val, nty)
					e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", storageIR(nty), cv.Ref, slot))
					sym.Ptr, sym.Ty = slot, nty
					e.define(v.Name, sym)
					return nil
				}
			}
			if keepUndef {
				if sym, ok := e.lookup(v.Name); ok {
					sym.Ty = ty
					e.define(v.Name, sym)
				}
			}
			val = e.convert(val, ty)
		}
		// Re-resolve the variable's current storage location rather than
		// trusting ptrName (captured above, before the initializer ran):
		// if evaluating the initializer itself created a closure that
		// captures this same variable — e.g. the self-cancelling-timer
		// idiom `const id = setInterval(() => { ...; clearInterval(id) },
		// ms)` — ADR-00001's capture-time promotion (boxing) already moved
		// v.Name from ptrName to a new shared heap cell via
		// updateSymbolInPlace. Storing into the now-stale ptrName in that
		// case would silently write the real value nowhere anyone (least
		// of all the closure itself) still reads from.
		finalPtr := ptrName
		if sym, ok := e.lookup(v.Name); ok {
			finalPtr = sym.Ptr
		}
		e.heapStore(ty.IR, val.Ref, finalPtr, ty.Align())
	} else if ty.IsDynamic() {
		// No initializer: any/unknown default to undefined (matching JS `let x: any;`
		// -> x === undefined), rather than leaving the tag byte as uninitialized
		// garbage, which would drive real runtime branching in print/typeof/equality.
		undef, err := e.emitBoxValue(Value{Ty: TypeUndefined})
		if err != nil {
			return err
		}
		e.heapStore(ty.IR, undef.Ref, ptrName, ty.Align())
	}
	return nil
}

// prePassStableArgs reports whether call arguments resolve the same in the
// module-global pre-pass as at the declaration: literals and earlier globals.
func (e *Emitter) prePassStableArgs(args []ast.Expression) bool {
	for _, a := range args {
		switch x := a.(type) {
		case *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.TemplateLiteral:
		case *ast.Identifier:
			if _, ok := e.moduleGlobals[x.Name]; !ok {
				return false
			}
		case *ast.ArrayLiteral, *ast.ObjectLiteral:
			if !e.promotableInitInPrePass(x) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// globalUndefinedStorage is a module global's storage type when it may hold
// undefined though its annotation is a plain number, boolean or string: no
// initializer (`let n: number;`), or one that may be undefined (`const c:
// number = a[9]`) — the `T | undefined` form, as for a local.
func (e *Emitter) globalUndefinedStorage(v *ast.VarDeclaration, ty Type) Type {
	if v.TypeAnnot == nil || v.Ambient || ty.Nullable() || ty.IsDynamic() || len(ty.UnionMembers) > 0 {
		return ty
	}
	scalar := (ty.Float && ty.IR == "double") || ty.IR == "i1"
	if !scalar && !isStringTy(ty) {
		return ty
	}
	if v.Init != nil {
		it := e.typeOfExpr(v.Init)
		if !it.Nullable() || !it.IsUndefined() || it.NullAndUndef() {
			return ty
		}
		if scalar != isNullableScalar(it) {
			return ty
		}
	}
	if scalar {
		nt := undefinedableElem(ty)
		nt.setUndefined(true)
		return nt
	}
	ty.setNullable(true)
	ty.setUndefined(true)
	return ty
}

// nestedVarDecls is the `var` declarations inside a top-level statement's
// blocks, outside any function or class body: each is module scoped.
func nestedVarDecls(stmt ast.Statement) []*ast.VarDeclaration {
	var out []*ast.VarDeclaration
	ast.Inspect(stmt, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.VarDeclaration:
			if x.Kind == "var" && x.Name != "" {
				out = append(out, x)
			}
		}
		return true
	})
	return out
}

// seedFuncGlobals stores each function declaration's value into the any
// global of a `var` sharing its name (registerModuleGlobals), before any
// statement runs: the binding is the function until the var's
// initializer assigns it.
func (e *Emitter) seedFuncGlobals() error {
	for _, name := range e.funcSeedOrder {
		g := e.moduleGlobals[name]
		mangled, sig, ok := e.resolveFuncRef(name)
		if !ok {
			continue
		}
		v := e.emitNamedFuncValue(mangled, sig, demangleModuleName(name))
		b, err := e.emitBoxValue(v)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", b.Ref, g.Ptr))
	}
	return nil
}

// selfReadLiteralType is the type of a strict-lane object literal binding
// its own functions read (`let x = { f: () => x }`): the checker's, as a
// global a function reads is stored, so each read is the one layout.
func (e *Emitter) selfReadLiteralType(v *ast.VarDeclaration) (Type, bool) {
	lit, ok := v.Init.(*ast.ObjectLiteral)
	if !ok || v.TypeAnnot != nil || e.compatJS() || len(lit.Properties) == 0 {
		return Type{}, false
	}
	o := e.reprOracle()
	if o == nil || !o.readsOwnBinding(v, lit) {
		return Type{}, false
	}
	return o.literalBindingType(v)
}

// settleForwardCell ends the dead zone of a forward cell (forwardClosureSym)
// a declaration path stored around: the binding's value moves into the
// cell the earlier closures hold, which the binding is from then on.
func (e *Emitter) settleForwardCell(v *ast.VarDeclaration) error {
	fsym, ok := e.forwardBoxes[v]
	if !ok || fsym.ForwardFlag == 0 || e.blockDone {
		return nil
	}
	cur, found := e.lookup(v.Name)
	if !found || cur.Ptr == "" {
		return nil
	}
	if cur.Ptr == fsym.Ptr {
		if cur.ForwardFlag > 0 {
			e.define(v.Name, e.forwardInitialized(cur))
		}
		return nil
	}
	val := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", val, cur.Ty.IR, cur.Ptr, cur.Ty.Align()))
	// The closures read the cell as the type they were built with.
	cv, err := e.convertChecked(Value{Ref: val, Ty: cur.Ty}, fsym.Ty, v.GetPos(), "'"+v.Name+"'")
	if err != nil {
		return err
	}
	e.heapStore(fsym.Ty.IR, cv.Ref, fsym.Ptr, fsym.Ty.Align())
	if cur.Ty.IR == fsym.Ty.IR {
		fsym.Ty = cur.Ty
	}
	e.define(v.Name, e.forwardInitialized(fsym))
	return nil
}

// globalsAssignedOtherKind is the untyped top-level declarations with an
// initializer that an assignment gives a value of another representation
// kind (a function, an array, a string, a number, a boolean, an object, a
// nullish value, an any) than the initializer's.
func (e *Emitter) globalsAssignedOtherKind(prog *ast.Program) map[*ast.VarDeclaration]bool {
	out := map[*ast.VarDeclaration]bool{}
	o := e.reprOracle()
	if o == nil {
		return out
	}
	kind := func(x ast.Expression) string {
		t := o.c.TypeOf(x)
		if o.c.Unanswered(t) {
			return "any"
		}
		r, ok := o.r.of(t)
		if !ok {
			return "any"
		}
		switch {
		case r.IsDynamic():
			return "any"
		case r.IsNull() || r.IsUndefined():
			return "nullish"
		case r.IsFunc():
			return "func"
		case r.IsArray():
			return "array"
		case isStringTy(r):
			return "string"
		case r.IsObject() || r.IsClass():
			return "object"
		}
		return r.IR
	}
	top := map[*ast.VarDeclaration]bool{}
	for _, st := range prog.Body {
		switch s := st.(type) {
		case *ast.VarDeclaration:
			top[s] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				top[d] = true
			}
		}
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignmentExpression)
		if !ok || a.Op != "=" {
			return true
		}
		id, ok := a.Left.(*ast.Identifier)
		if !ok {
			return true
		}
		sym, _ := o.b.Resolve(id)
		if sym == nil {
			return true
		}
		v, ok := singleVarDecl(sym)
		if !ok || !top[v] || v.TypeAnnot != nil || v.Init == nil || v.Ambient {
			return true
		}
		if ki, kr := kind(v.Init), kind(a.Right); ki != kr && ki != "any" {
			out[v] = true
		}
		return true
	})
	return out
}
