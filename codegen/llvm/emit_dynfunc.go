package llvm

// emit_dynfunc.go — dynamic functions (TDD-00155 Stage 4): the function
// values behind vanilla-JS prototype methods. A function expression compiled
// in a dynamic (`any`) context — `F.prototype.speak = function() {...}` —
// gets the uniform dynamic ABI
//
//	define i64 @dynfn(ptr %env, i64 %this, i64 %argc, ptr %argv)   ; NaN-boxed words (TDD-00156)
//
// so any call site can invoke it without knowing its signature: `this` is
// the boxed receiver, parameters are boxed values unpacked from argv (an
// omitted argument reads undefined, matching JS), and the return is a box
// (a fall-off end returns undefined). The value itself is box tag 12
// (kmlTagDynFunc): a 24-byte record { fnptr, env, i64 arity } — identity is
// the record pointer, one per evaluation of the function expression, like a
// JS closure. Dispatch (`obj.m(args)`) walks the prototype chain via the
// Stage-3 get, so inherited methods just work.

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// emitDynFunctionExpression compiles fe under the dynamic ABI and returns
// the boxed (tag kmlTagDynFunc) record. V1 scope: no captures of enclosing
// locals (module globals and named functions are referenced directly and
// need no capture), no async/generator forms.
func (e *Emitter) emitDynFunctionExpression(fe *ast.FunctionExpression, pos ast.Pos) (Value, error) {
	if pos == (ast.Pos{}) {
		pos = fe.GetPos() // names the function in the library
	}
	// A generator expression is its boxed constructor closure; an async
	// function keeps its typed coroutine body (parameters `any` unless
	// annotated) and is boxed through the closure adapter.
	if fe.IsGenerator {
		return e.emitGeneratorExpressionValue(fe)
	}
	if fe.IsAsync {
		hints := make([]Type, len(fe.Params))
		for i := range hints {
			hints[i] = TypeAny
		}
		v, err := e.emitFunctionExpression(fe, hints)
		if err != nil {
			return Value{}, err
		}
		return e.emitBoxValue(v)
	}
	name := fe.Name
	if name == "" {
		name = e.fnLitName(fe)
	}
	e.dynSloppy = e.sloppyFileSet[e.fileOfFunc(fe)]
	defer func() { e.dynSloppy = false }()
	return e.emitDynCallable(fe.Name, name, fe.Params, fe.Body.Body, true, false, pos)
}

// emitSloppyThis makes the `this` in slot a sloppy-mode function's: an
// undefined or null receiver is globalThis, a primitive one its wrapper
// object (`Object(this)`, the identity on an object).
func (e *Emitter) emitSloppyThis(slot string, pos ast.Pos) {
	key, ok := namespaceValueExport("globalThis")
	hasGlobal := ok && e.libExports[key] != ""
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, slot))
	isU, isN, absent := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, cur, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isN, cur, nbNull))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, isU, isN))
	gL, objL, doneL := e.freshLabel("sloppy.global"), e.freshLabel("sloppy.obj"), e.freshLabel("sloppy.this")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, gL, objL))
	e.emitLabel(objL)
	wrap := ast.NewCallExpression(ast.NewIdentifier("Object", pos), []ast.Expression{e.bindValue(Value{Ref: cur, Ty: TypeAny}, pos)}, pos)
	if w, err := e.emitExpr(wrap); err == nil {
		if b, err := e.emitBoxValue(w); err == nil {
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, slot))
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(gL)
	if !hasGlobal {
		// No globalThis linked: an absent receiver stays as it is.
	} else if g, err := e.emitExpr(ast.NewIdentifier("globalThis", pos)); err == nil {
		if b, err := e.emitBoxValue(g); err == nil {
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, slot))
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// usesOwnThis reports whether a function body reads its own `this`: a
// `this` outside any nested function or class (an arrow's `this` is the
// enclosing function's).
func usesOwnThis(body []ast.Statement) bool {
	found := false
	for _, st := range body {
		ast.Inspect(st, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.ThisExpression:
				found = true
			case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
				return false
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// jsThisGeneratorsAsFunctions lowers, under `-compat=js`, each generator
// function of the program's own code that reads its own `this` into a
// plain function that takes the receiver (then compiled as any other
// this-reading function) and returns an inner generator over the same body,
// its `this` and `arguments` the outer call's. A generator's parameters are
// bound at the call already, so the split keeps their evaluation order.
func jsThisGeneratorsAsFunctions(prog *ast.Program, lib map[ast.Statement]string) {
	for _, st := range prog.Body {
		if lib[st] != "" {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			switch f := n.(type) {
			case *ast.FunctionDeclaration:
				if f.IsGenerator && f.Body != nil && !hasThisParam(f.Params) && usesOwnThis(f.Body.Body) {
					f.Body = generatorThisShim(f.Body, f.IsAsync)
					f.IsGenerator, f.IsAsync = false, false
				}
			case *ast.FunctionExpression:
				if f.IsGenerator && !f.Method && f.Body != nil && !hasThisParam(f.Params) && usesOwnThis(f.Body.Body) {
					f.Body = generatorThisShim(f.Body, f.IsAsync)
					f.IsGenerator, f.IsAsync = false, false
				}
			}
			return true
		})
	}
}

func hasThisParam(params []ast.Param) bool {
	return len(params) > 0 && params[0].Name == "this"
}

// generatorThisShim is jsThisGeneratorsAsFunctions' outer body for the
// generator body b: `const __kml_gthis = this; const __kml_gargs =
// arguments; return (function* () { b })();`, b's own `this` and
// `arguments` renamed to those bindings.
func generatorThisShim(b *ast.BlockStatement, isAsync bool) *ast.BlockStatement {
	pos := b.GetPos()
	usesArgs := false
	var rw func(ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		switch x := n.(type) {
		case *ast.ThisExpression:
			return ast.NewIdentifier("__kml_gthis", x.GetPos())
		case *ast.Identifier:
			if x.Name == "arguments" {
				usesArgs = true
				return ast.NewIdentifier("__kml_gargs", x.GetPos())
			}
			return n
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return n // its own `this` and `arguments`
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	for i, st := range b.Body {
		b.Body[i] = rw(st).(ast.Statement)
	}
	inner := ast.NewFunctionExpression("", nil, nil, b, isAsync, pos)
	inner.IsGenerator = true
	out := []ast.Statement{ast.NewVarDeclaration("const", "__kml_gthis", nil, ast.NewThisExpression(pos), pos)}
	if usesArgs {
		out = append(out, ast.NewVarDeclaration("const", "__kml_gargs", nil, ast.NewIdentifier("arguments", pos), pos))
	}
	out = append(out, ast.NewReturnStatement(ast.NewCallExpression(inner, nil, pos), pos))
	return ast.NewBlockStatement(out, pos)
}

// jsThisFunctionsAsBindings rewrites, under `-compat=js`, each function
// declaration of the program's own code that reads its own `this` and is
// not a prototype constructor (used with `new`, or its `.prototype`
// touched) into `const f = function f(…) {…}`, so it takes the receiver
// its caller supplies, as an expression does (emitFunctionExpression). A
// top-level declaration moves to the top of the program — where a
// function declaration is initialized — and a nested one stays in place.
func jsThisFunctionsAsBindings(prog *ast.Program, lib map[ast.Statement]string) {
	ctors := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.NewExpression:
			if !x.Qualified {
				ctors[x.ClassName] = true
			}
		case *ast.MemberExpression:
			if id, ok := x.Object.(*ast.Identifier); ok && x.Property == "prototype" {
				ctors[id.Name] = true
			}
		}
		return true
	})
	convert := func(st ast.Statement) (*ast.VarDeclaration, bool) {
		fd, ok := st.(*ast.FunctionDeclaration)
		if !ok || fd.Body == nil || fd.IsAsync || fd.IsGenerator || len(fd.TypeParams) > 0 || ctors[fd.Name] || !usesOwnThis(fd.Body.Body) {
			return nil, false
		}
		if len(fd.Params) > 0 && fd.Params[0].Name == "this" {
			return nil, false
		}
		fe := ast.NewFunctionExpression(fd.Name, fd.Params, fd.ReturnType, fd.Body, false, fd.GetPos())
		fe.Src = fd.Src
		return ast.NewVarDeclaration("const", fd.Name, nil, fe, fd.GetPos()), true
	}
	var hoisted, rest []ast.Statement
	for _, st := range prog.Body {
		if lib[st] != "" {
			rest = append(rest, st)
			continue
		}
		if vd, ok := convert(st); ok {
			if f, ok := prog.StmtFiles[st]; ok {
				prog.StmtFiles[vd] = f // its frames name the file
			}
			hoisted = append(hoisted, vd)
			continue
		}
		rest = append(rest, st)
		ast.Inspect(st, func(n ast.Node) bool {
			if b, ok := n.(*ast.BlockStatement); ok {
				for i, inner := range b.Body {
					if vd, ok := convert(inner); ok {
						b.Body[i] = vd
					}
				}
			}
			return true
		})
	}
	if len(hoisted) == 0 {
		return
	}
	// After the imports, which bind first.
	i := 0
	for i < len(rest) {
		if _, ok := rest[i].(*ast.ImportDeclaration); !ok {
			break
		}
		i++
	}
	out := append([]ast.Statement{}, rest[:i]...)
	out = append(out, hoisted...)
	prog.Body = append(out, rest[i:]...)
}

// emitDynArrowFunction compiles an arrow function under the dynamic ABI —
// the arrow-shaped values untyped JS passes into dynamic positions
// (`{ callback: () => ... }`). Lexical-`this` semantics: the receiver word
// the ABI passes is ignored, and a body using `this` captures the enclosing
// one, as a typed arrow does (ADR-00460).
func (e *Emitter) emitDynArrowFunction(af *ast.ArrowFunction, pos ast.Pos) (Value, error) {
	if pos == (ast.Pos{}) {
		pos = af.GetPos() // names the function in the library
	}
	if af.IsAsync {
		// An async arrow keeps its typed coroutine body (its parameters `any`
		// unless annotated) and is boxed through the closure adapter, its
		// Promise result held in the dynamic value (TDD-00230 phase 5).
		hints := make([]Type, len(af.Params))
		for i := range hints {
			hints[i] = TypeAny
		}
		v, err := e.emitArrowFunctionWithHints(af, hints)
		if err != nil {
			return Value{}, err
		}
		return e.emitBoxValue(v)
	}
	body := af.Block
	if body == nil {
		// Expression body: `=> expr` is `=> { return expr }`.
		body = ast.NewBlockStatement([]ast.Statement{ast.NewReturnStatement(af.Body, pos)}, pos)
	}
	params := e.contextuallyTypedParams(af, af.Params)
	return e.emitDynCallable("", e.fnLitName(af), params, body.Body, false, lexicalThisIn(af), pos)
}

// contextuallyTypedParams gives each unannotated parameter of fn the type the
// checker gives it from its context (`fs.read(fd, buf, o, (err, n, b) => …)`
// types b as a Buffer), when that type has a representation of its own here —
// a number, string, boolean, Buffer or Error; any other stays `any`.
func (e *Emitter) contextuallyTypedParams(fn ast.Node, params []ast.Param) []ast.Param {
	c := e.front()
	if c == nil {
		return params
	}
	ft := c.TypeOf(fn.(ast.Expression))
	if c.Unanswered(ft) || ft.Flags&checker.Object == 0 || ft.Kind != checker.Function {
		return params
	}
	var out []ast.Param
	for i, p := range params {
		if p.Type != nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil || i >= len(ft.Params) {
			continue
		}
		name := ""
		pt := ft.Params[i]
		switch {
		case isBytesType(pt):
			name = "Buffer"
		default:
			r, ok := lowerRepr(pt)
			if !ok || r.IsArray() || r.IsError() {
				continue
			}
			switch {
			case r.IR == "i1":
				name = "boolean"
			case r.Float:
				name = "number"
			case r.IR == "ptr":
				name = "string"
			}
		}
		if name == "" {
			continue
		}
		if out == nil {
			out = append([]ast.Param(nil), params...)
		}
		out[i].Type = &ast.TypeAnnotation{Name: name}
	}
	if out == nil {
		return params
	}
	return out
}

// contextualClassParam is the class the checker types an unannotated
// parameter of fn as from its context (`server.on('connection', (socket) =>
// …)` against an overload `(socket: Socket) => void`), where code
// generation's own hint is `any` or absent: the implementation signature
// behind an overload set takes `(...args: any[]) => void`.
func (e *Emitter) contextualClassParam(fn ast.Expression, p ast.Param, i int, hints []Type) (Type, bool) {
	if p.Type != nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil || p.Default != nil {
		return Type{}, false
	}
	if i < len(hints) && !isUnconstrainedDynamic(hints[i]) {
		return Type{}, false
	}
	c := e.front()
	if c == nil {
		return Type{}, false
	}
	ft := c.TypeOf(fn)
	if c.Unanswered(ft) || ft.Flags&checker.Object == 0 || ft.Kind != checker.Function || i >= len(ft.Params) {
		return Type{}, false
	}
	pt := ft.Params[i]
	if pt.Flags&checker.Object == 0 || pt.Kind != checker.Instance || pt.Symbol == nil || len(pt.TypeArgs) > 0 {
		return Type{}, false
	}
	info, ok := e.classes[pt.Symbol.Name]
	if !ok || info.IsErrorSubclass {
		return Type{}, false
	}
	return info.Ty, true
}

// emitDynCallable is the shared dynamic-ABI compilation behind function
// expressions (bindThis=true — `this` is the boxed receiver) and arrows
// (bindThis=false — lexical `this`, captured from the enclosing scope when
// captureThis).
func (e *Emitter) emitDynCallable(selfName, displayName string, params []ast.Param, bodyStmts []ast.Statement, bindThis, captureThis bool, pos ast.Pos) (Value, error) {
	// A sloppy-mode function expression's own (emitDynFunctionExpression);
	// nested callables decide for themselves.
	sloppy := bindThis && e.dynSloppy
	e.dynSloppy = false
	// Free-variable scan, mirroring emitFunctionExpression: anything that
	// resolves to an enclosing local would need an env capture — out of the
	// Stage-4 V1 scope, rejected cleanly.
	refs := make(map[string]bool)
	bound := map[string]bool{"this": true}
	addParamBoundNames(bound, params)
	scanStmtsFV(bodyStmts, bound, refs)
	scanParamDefaultsFV(params, bound, refs)
	// Enclosing locals referenced by the body are captured into the tag-12
	// record's env (by shared heap cell, exactly like an arrow/closure), read
	// back in the body below. Sorted for a deterministic env layout.
	var capNames []string
	for _, name := range sortedSet(refs) {
		if name == selfName {
			continue
		}
		if _, isGlobal := e.moduleGlobals[name]; isGlobal {
			continue
		}
		if _, found := e.lookup(name); found {
			capNames = append(capNames, name)
		} else if _, found := e.forwardClosureSym(name); found {
			capNames = append(capNames, name)
		}
	}
	if captureThis {
		if _, found := e.lookup("this"); found {
			capNames = append(capNames, "this")
		}
	}
	sort.Strings(capNames)
	var caps []CapturedVar
	for _, name := range capNames {
		sym, found := e.lookup(name)
		if !found {
			sym, _ = e.forwardClosureSym(name)
		}
		caps = append(caps, CapturedVar{Name: name, Ty: sym.Ty, Sym: sym})
	}

	fnName := e.literalSymbol("@__kml_dynfn", pos)
	e.registerFnMeta(fnName, displayName, fnLengthFromParams(params), fnKindPlain)

	// Save emitter state — the same discipline emitFunctionExpression uses.
	frameFile := e.frameFile
	restoreFn := e.beginDetachedFunc()
	// Its frame, in the file of the function that makes it.
	e.enterFrameIn(frameFile, displayName, pos)
	// The body's own captured locals and parameters (a nested closure's
	// free variables), boxed where they are bound so every capture shares
	// one cell, whichever branch creates the closure.
	savedHoisted := e.hoistedCaptures
	defer func() { e.hoistedCaptures = savedHoisted }()
	{
		e.hoistedCaptures = capturedLocalNames(bodyStmts, paramBindingNames(params))
	}
	bindParam := func(name, slot string, ty Type) {
		if !e.hoistedCaptures[name] {
			e.define(name, Symbol{Ptr: slot, Ty: ty})
			return
		}
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", cur, ty.IR, slot, ty.Align()))
		e.boxHoistedCapture(name, ty, cur, false, false)
	}

	// Bind `this` (a boxed slot — emitThisExpression's IsDynamic arm);
	// an arrow keeps lexical `this` and skips the binding.
	if bindThis {
		thisPtr := "%v_this"
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", thisPtr))
		e.emitInstr(fmt.Sprintf("store i64 %%p_this, ptr %s, align 8", thisPtr))
		e.define("this", Symbol{Ptr: thisPtr, Ty: TypeAny})
		if sloppy {
			e.emitSloppyThis(thisPtr, pos)
		}
	}

	// A function (not an arrow) reading `arguments` binds it to every
	// argument the caller passed, as a dynamic array.
	if bindThis && paramsUseArguments(params, bodyStmts) {
		argsSlot := "%v_arguments"
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argsSlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitDynArgvFrom(0), argsSlot))
		e.define("arguments", Symbol{Ptr: argsSlot, Ty: TypeAny})
	}

	// Bind each captured variable to its shared heap cell, read from %env,
	// before the parameters: a default reads them, and a parameter shadows one.
	if len(caps) > 0 {
		envIR := envStructIR(caps)
		for i, cap := range caps {
			slotGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%env, i32 0, i32 %d", slotGep, envIR, i))
			cellPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cellPtr, slotGep))
			sym := cap.Sym
			sym.Ptr = cellPtr
			sym.Boxed = true
			e.define(cap.Name, sym)
		}
	}

	// A class-typed parameter binds after the rest: its argument may be
	// another object, which only a body reading it as a box can read
	// (emit_dynentry.go).
	type classParam struct {
		name, box string
		ty        Type
		info      ClassInfo
	}
	var classParams []classParam
	var boxedParams []string

	// Bind each parameter from argv: argv[i] when provided, undefined when
	// the caller passed fewer arguments (JS's missing-argument rule).
	for i, p := range params {
		// A rest parameter collects the remaining argv words into a dynamic
		// array (tag 11) — `(...args) => args.length` works.
		if p.Rest {
			restBox := e.emitDynArgvFrom(i)
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", ptrName))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", restBox, ptrName))
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: TypeAny})
			continue
		}
		isPattern := p.ArrayPattern != nil || p.ObjectPattern != nil
		boxSlot := "%v_" + p.Name + "_box"
		if isPattern {
			boxSlot = fmt.Sprintf("%%v_dp%d_box", i)
		}
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", boxSlot))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, boxSlot))
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%p_argc, %d", have, i))
		haveL := e.freshLabel("dynfn.arg")
		nextL := e.freshLabel("dynfn.argdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", have, haveL, nextL))
		e.emitLabel(haveL)
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %d", slot, i))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", v, slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, boxSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
		e.emitLabel(nextL)
		// A parameter with a concrete (non-dynamic) declared type binds as that
		// type — unbox the received box to it — so the body can do typed
		// operations (`initial * 2`, a decorator field initializer). An
		// unannotated / `any` / `unknown` parameter stays a boxed `any`.
		pty := TypeAny
		if p.Type != nil && !isPattern {
			// A `?` parameter reads undefined when omitted.
			pty = optionalParamType(p, e.resolveType(p.Type))
		}
		// JS fills a default whenever the argument is `undefined`, omitted or
		// passed, with the earlier parameters in scope (`(a, b = a) => …`).
		if p.Default != nil {
			cur := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, boxSlot))
			isUndef := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, cur, nbUndefined))
			defL := e.freshLabel("dynfn.default")
			doneL := e.freshLabel("dynfn.defdone")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUndef, defL, doneL))
			e.emitLabel(defL)
			dv, derr := e.emitExprWithObjectHint(p.Default, pty)
			if derr != nil {
				restoreFn()
				return Value{}, derr
			}
			if !pty.IsDynamic() && pty.IR != "" {
				dv = e.convert(dv, pty)
			}
			bv, berr := e.emitBoxValue(dv)
			if berr != nil {
				restoreFn()
				return Value{}, berr
			}
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bv.Ref, boxSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
		}
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", box, boxSlot))
		if isPattern {
			// The argument is whatever the caller passed: destructure it
			// as a dynamic value, each leaf an `any` local.
			var uerr error
			if p.ObjectPattern != nil {
				uerr = e.unpackDynObjectPatternInto(Value{Ref: box, Ty: TypeAny}, p.ObjectPattern, pos)
			} else {
				uerr = e.unpackDynArrayPatternInto(Value{Ref: box, Ty: TypeAny}, p.ArrayPattern, pos)
			}
			if uerr != nil {
				restoreFn()
				return Value{}, uerr
			}
			continue
		}
		if pty.IsArray() && !pty.IsTuple() {
			// An array (a Buffer, `T[]`): the boxed array itself — the slot
			// holds its live header, so the parameter aliases the argument.
			uv := e.emitUnboxBoxToType(box, pty)
			hdr := uv.Ref
			if hdr == "" {
				hdr = e.boxArrayValue(uv)
			}
			arrSlot := "%v_" + p.Name + "_ptr"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", arrSlot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, arrSlot))
			bindParam(p.Name, arrSlot, pty)
		} else if pty.IsDynamic() || pty.IR == "" || pty.IsArray() || isNullableScalar(pty) {
			bindParam(p.Name, boxSlot, TypeAny)
			if p.Type == nil {
				// The checker may type it from its context (a class
				// instance); the argument is whatever the caller passed,
				// so the body reads the box.
				boxedParams = append(boxedParams, p.Name)
			}
		} else if info, isCls := e.dynEntryClass(pty); isCls && p.Default == nil {
			classParams = append(classParams, classParam{name: p.Name, box: box, ty: pty, info: info})
		} else {
			uv := e.unboxArgToParam(box, pty)
			typedSlot := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", typedSlot, pty.IR, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", pty.IR, uv.Ref, typedSlot, pty.Align()))
			bindParam(p.Name, typedSlot, pty)
		}
	}

	if len(boxedParams) > 0 {
		saved := e.dynEntryWidened
		widened := map[string]bool{}
		for k := range saved {
			widened[k] = true
		}
		for _, n := range boxedParams {
			widened[n] = true
		}
		e.dynEntryWidened = widened
		defer func() { e.dynEntryWidened = saved }()
	}
	// The body's own analyses, as every other function body gets them
	// (emit_func.go): untyped bindings assigned several kinds, and an
	// empty [] or Map typed by its later use — without them a `var a = []`
	// here stayed number[] whatever was assigned to it.
	savedWidened, savedEmptyArr, savedEmptyMap := e.widenedBindings, e.emptyArrayElems, e.emptyMapKV
	e.widenedBindings = e.crossTypeWidenedBindings(bodyStmts)
	e.inferEmptyContainers(bodyStmts)
	defer func() {
		e.widenedBindings, e.emptyArrayElems, e.emptyMapKV = savedWidened, savedEmptyArr, savedEmptyMap
	}()
	e.emitSafepoint()
	emitBody := func() error {
		for _, stmt := range bodyStmts {
			if err := e.emitStmt(stmt); err != nil {
				return err
			}
		}
		// Fall-off end returns undefined, matching JS.
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		return nil
	}
	var bodyErr error
	if len(classParams) == 0 {
		bodyErr = emitBody()
	} else {
		// Another object for a class-typed parameter runs the body with
		// that parameter a box; an instance of the class (or nothing), the
		// typed body.
		var foreign string
		for _, cp := range classParams {
			b := e.emitForeignClassArg(cp.box, cp.info)
			if foreign == "" {
				foreign = b
				continue
			}
			o := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, foreign, b))
			foreign = o
		}
		altL, ownL := e.freshLabel("dynentry.alt"), e.freshLabel("dynentry.own")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", foreign, altL, ownL))
		e.emitLabel(altL)
		e.pushScope()
		saved := e.dynEntryWidened
		widened := map[string]bool{}
		for k := range saved {
			widened[k] = true
		}
		for _, cp := range classParams {
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", cp.box, slot))
			bindParam(cp.name, slot, TypeAny)
			widened[cp.name] = true
		}
		e.dynEntryWidened = widened
		bodyErr = emitBody()
		e.dynEntryWidened = saved
		e.popScope()
		if bodyErr == nil {
			e.emitLabel(ownL)
			e.pushScope()
			for _, cp := range classParams {
				uv := e.unboxArgToParam(cp.box, cp.ty)
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, cp.ty.IR, cp.ty.Align()))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", cp.ty.IR, uv.Ref, slot, cp.ty.Align()))
				bindParam(cp.name, slot, cp.ty)
			}
			bodyErr = emitBody()
			e.popScope()
		}
	}
	if bodyErr != nil {
		restoreFn()
		return Value{}, bodyErr
	}

	e.writeFunc(fmt.Sprintf("i64 %s(ptr %%env, i64 %%p_this, i64 %%p_argc, ptr %%p_argv)", fnName), e.allocas.String()+e.body.String())

	restoreFn()

	// The tag-12 record: { fnptr, env, i64 arity }. env carries the captured
	// variables' shared heap cells (or null when nothing is captured).
	e.ensureMalloc()
	rec := e.freshReg()
	e.heapMallocInto(rec, "24")
	e.heapStore("ptr", fnName, rec, 8)
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	if len(caps) > 0 {
		envIR := envStructIR(caps)
		env := e.freshReg()
		e.heapMallocInto(env, fmt.Sprint(envStructSize(caps)))
		for i, cap := range caps {
			cellPtr := cap.Sym.Ptr
			if !cap.Sym.Boxed {
				cellPtr = e.promoteCaptureToCell(cap.Name, cap.Ty, cap.Sym.Ptr, cap.Sym.IsConst)
			}
			slotReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slotReg, envIR, env, i))
			e.heapStore("ptr", cellPtr, slotReg, 8)
		}
		e.heapStore("ptr", env, envSlot, 8)
	} else {
		e.heapStore("ptr", "null", envSlot, 8)
	}
	aritySlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", aritySlot, rec))
	e.heapStore("i64", fmt.Sprint(len(params)), aritySlot, 8)

	return Value{Ref: e.emitNbTagPtr(rec, kmlTagDynFunc), Ty: TypeAny}, nil
}

// emitDynClosureAdapter wraps a statically-typed closure value into a tag-12
// dynamic-function record: a per-signature adapter unboxes each argv word to
// the concrete parameter type, calls through the closure header (carried as
// the record's env), and boxes the result — so a typed closure is a
// first-class dynamic value (`{ cb }` shorthand, dynamic-object properties,
// Reflect arguments). V1 scope: scalar/string/dynamic parameters (array and
// nullable-scalar parameters stay a clean rejection); any return boxes,
// void returns undefined.
func (e *Emitter) emitDynClosureAdapter(v Value) (Value, error) {
	retTy := TypeVoid
	if v.Ty.FuncRetType != nil {
		retTy = *v.Ty.FuncRetType
	}

	restoreFn := e.beginDetachedFunc()
	restoreFn = e.sharedBody(restoreFn) // content-named: the same in every program

	// env IS the closure header: {fnptr, closureEnv}.
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", fp))
	cenvSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 8", cenvSlot))
	cenv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cenv, cenvSlot))

	argParts := []string{"ptr " + cenv}
	// altParts are the dynamic entry's arguments (emit_dynentry.go): a
	// class-typed parameter's box as itself; foreign holds, per such
	// parameter, whether its argument is another object.
	altParts := []string{"ptr " + cenv}
	var foreign []string
	altOK := true
	callFailed := false
	var presentBits []string
	// A `this: T` parameter (FuncThis) takes the receiver word; the
	// arguments start at argv[0] with the next parameter.
	off := 0
	if v.Ty.FuncThis {
		off = 1
	}
	for i, pty := range v.Ty.FuncParams {
		ai := i - off // the parameter's argv index
		// The rest slot gathers surplus argv words, each unboxed to the
		// element type, into the callee's (ptr, len) typed-array pair.
		if v.Ty.FuncHasRest && i == len(v.Ty.FuncParams)-1 {
			elemTy := TypeI64
			if pty.ElemType != nil {
				elemTy = *pty.ElemType
			}
			// The callee ABI takes a {data,len} header pointer + len.
			hdr, n := e.emitArgvRestArray(ai, elemTy)
			argParts = append(argParts, "ptr "+hdr, "i64 "+n)
			altParts = append(altParts, "ptr "+hdr, "i64 "+n)
			continue
		}
		word := e.freshReg()
		if ai < 0 {
			e.emitInstr(fmt.Sprintf("%s = add i64 %%p_this, 0", word))
		} else {
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 0", word, e.emitArgWord(ai)))
		}
		// JS fills a default whenever the argument is `undefined` — omitted or
		// passed explicitly (TDD-00229): a call-site default is evaluated here,
		// with the earlier parameters in scope (`(a, b = a) => …`); a
		// body-filled default is signalled through the presence mask below.
		isUndef := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, word, nbUndefined))
		presentBits = append(presentBits, isUndef)
		var argVal Value
		if i < len(v.Ty.FuncParamDefaults) && v.Ty.FuncParamDefaults[i] != nil && pty.IsArray() {
			// An array parameter's ABI is its header pointer and length,
			// from the default or the unboxed argument.
			hSlot, nSlot := e.freshReg(), e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", hSlot))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", nSlot))
			defL, argL, joinL := e.freshLabel("adapt.default"), e.freshLabel("adapt.arg"), e.freshLabel("adapt.join")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUndef, defL, argL))
			e.emitLabel(defL)
			dv, derr := e.emitExprWithObjectHint(v.Ty.FuncParamDefaults[i], pty)
			if derr != nil {
				callFailed = true
				break
			}
			h, n := e.arrayArgFromAggregate(e.convert(dv, pty))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", h, hSlot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, nSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(argL)
			h, n = e.arrayArgFromAggregate(e.emitUnboxBoxToType(word, pty))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", h, hSlot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, nSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			hr, nr := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hr, hSlot))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", nr, nSlot))
			argParts = append(argParts, "ptr "+hr, "i64 "+nr)
			altParts = append(altParts, "ptr "+hr, "i64 "+nr)
			continue
		} else if i < len(v.Ty.FuncParamDefaults) && v.Ty.FuncParamDefaults[i] != nil {
			valSlot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", valSlot, storageIR(pty)))
			defL := e.freshLabel("adapt.default")
			argL := e.freshLabel("adapt.arg")
			joinL := e.freshLabel("adapt.join")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUndef, defL, argL))
			e.emitLabel(defL)
			dv, derr := e.emitExprWithObjectHint(v.Ty.FuncParamDefaults[i], pty)
			if derr != nil {
				callFailed = true
				break
			}
			dv = e.convert(dv, pty)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(pty), dv.Ref, valSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(argL)
			av := e.emitUnboxBoxToType(word, pty)
			if pty.IsDynamic() {
				av = Value{Ref: word, Ty: pty}
			}
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(pty), av.Ref, valSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, storageIR(pty), valSlot))
			argVal = Value{Ref: r, Ty: pty}
			if _, isCls := e.dynEntryClass(pty); isCls {
				altOK = false
			}
		} else if pty.IsDynamic() {
			argVal = Value{Ref: word, Ty: pty}
		} else if pty.IsArray() {
			// An array parameter's ABI is its header pointer and length
			// (TDD-00127), taken from the unboxed array value.
			header, n := e.arrayArgFromAggregate(e.emitUnboxBoxToType(word, pty))
			argParts = append(argParts, "ptr "+header, "i64 "+n)
			altParts = append(altParts, "ptr "+header, "i64 "+n)
			continue
		} else {
			altWord := ""
			if info, isCls := e.dynEntryClass(pty); isCls {
				foreign = append(foreign, e.emitForeignClassArg(word, info))
				altWord = word
			}
			argVal = e.unboxArgToParam(word, pty)
			if altWord != "" {
				argParts = append(argParts, fmt.Sprintf("%s %s", storageIR(pty), argVal.Ref))
				altParts = append(altParts, "i64 "+altWord)
				continue
			}
		}
		// Later defaults may reference this parameter by name.
		if i < len(v.Ty.FuncParamNames) && v.Ty.FuncParamNames[i] != "" && pty.IR != "" && pty.IR != "void" {
			ps := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", ps, storageIR(pty)))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(pty), argVal.Ref, ps))
			e.define(v.Ty.FuncParamNames[i], Symbol{Ptr: ps, Ty: pty})
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", storageIR(pty), argVal.Ref))
		altParts = append(altParts, fmt.Sprintf("%s %s", storageIR(pty), argVal.Ref))
	}
	if v.Ty.FuncHasDefaultMask && !callFailed {
		// Bit i set = argument i supplied (and not `undefined`); a body-filled
		// default reads a clear bit as "use the default".
		mask := "0"
		for i, u := range presentBits {
			bit := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i32 0, i32 %d", bit, u, uint32(1)<<uint(i)))
			m := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i32 %s, %s", m, mask, bit))
			mask = m
		}
		argParts = append(argParts, "i32 "+mask)
		altParts = append(altParts, "i32 "+mask)
	}
	if v.Ty.FuncCountsArgs && !callFailed {
		// A callee reading `arguments` takes how many of its declared
		// parameters the call passed (TDD-00210): the argv words, up to them.
		regular := len(v.Ty.FuncParams) - off
		if v.Ty.FuncHasRest {
			regular--
		}
		e.ensureArgc()
		lt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %%p_argc, %d", lt, regular))
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %%p_argc, i64 %d", n, lt, regular))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_argc, align 8", n))
	}
	// The callee: the closure's own code, or its dynamic entry when a
	// class-typed parameter's argument is another object.
	useAlt, altFp := "", ""
	if altOK && len(foreign) > 0 && !callFailed {
		any := foreign[0]
		for _, b := range foreign[1:] {
			o := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, any, b))
			any = o
		}
		altFp = e.emitDynEntryOf(fp)
		has := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, altFp))
		useAlt = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", useAlt, any, has))
	}
	var boxErr error
	if retTy.IR == "void" {
		if useAlt != "" {
			altL, ownL := e.freshLabel("dynentry.alt"), e.freshLabel("dynentry.own")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", useAlt, altL, ownL))
			e.emitLabel(altL)
			e.emitInstr(fmt.Sprintf("call void %s(%s)", altFp, joinArgs(altParts)))
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
			e.emitLabel(ownL)
		}
		e.emitInstr(fmt.Sprintf("call void %s(%s)", fp, joinArgs(argParts)))
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	} else {
		r := e.freshReg()
		if useAlt != "" {
			altL, ownL, joinL := e.freshLabel("dynentry.alt"), e.freshLabel("dynentry.own"), e.freshLabel("dynentry.join")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", useAlt, altL, ownL))
			e.emitLabel(altL)
			ra := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", ra, retTy.LLVMRetType(), altFp, joinArgs(altParts)))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(ownL)
			ro := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", ro, retTy.LLVMRetType(), fp, joinArgs(argParts)))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", r, retTy.LLVMRetType(), ra, altL, ro, ownL))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", r, retTy.LLVMRetType(), fp, joinArgs(argParts)))
		}
		callResult := Value{Ref: r, Ty: retTy}
		if retTy.IsArray() {
			// Array return ABI is a header pointer (TDD-00213 Stage 3): wrap
			// it as the array value before boxing.
			callResult = e.arrayValueFromHeaderReg(r, retTy)
		}
		b, err := e.emitBoxValue(callResult)
		if err != nil {
			callFailed = true
			boxErr = err
		} else {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", b.Ref))
		}
	}

	body := e.allocas.String() + e.body.String()
	restoreFn()
	fnName := e.defineContentNamed("@__kml_dynadapt.", "i64", "ptr %env, i64 %p_this, i64 %p_argc, ptr %p_argv", body)
	// The record's env is the adapted closure header: name/length/kind are
	// that function's (TDD-00229).
	e.registerFnMeta(fnName, "", 0, fnFlagThroughEnv)
	if callFailed {
		if boxErr != nil {
			return Value{}, fmt.Errorf("a closure returning this value cannot be boxed into a dynamic value yet: %v", boxErr)
		}
		return Value{}, fmt.Errorf("a closure with this return type cannot be boxed into a dynamic value yet")
	}

	e.ensureMalloc()
	// A closure that is a dynamic function's thunk (emitAnyToClosure), or an
	// adapter over one, boxes back to that function.
	e.ensureD2SEnvTag()
	origBox := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", origBox))
	e.ensureFnMeta()
	ob := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fn_thunk_box(ptr %s, ptr @__kml_d2s_env)", ob, v.Ref))
	isThunk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isThunk, ob))
	origL, buildL, doneL := e.freshLabel("box.fn.orig"), e.freshLabel("box.fn.build"), e.freshLabel("box.fn.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isThunk, origL, buildL))
	e.emitLabel(origL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ob, origBox))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(buildL)
	rec := e.freshReg()
	e.heapMallocInto(rec, "24")
	e.heapStore("ptr", fnName, rec, 8)
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	e.heapStore("ptr", v.Ref, envSlot, 8)
	aritySlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", aritySlot, rec))
	e.heapStore("i64", fmt.Sprint(len(v.Ty.FuncParams)-off), aritySlot, 8)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(rec, kmlTagDynFunc), origBox))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, origBox))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// ensureD2SEnvTag declares the private global whose address tags a dynamic
// function's closure-thunk env (emitAnyToClosure).
func (e *Emitter) ensureD2SEnvTag() {
	if e.fnDecls["__kml_d2s_env"] {
		return
	}
	e.fnDecls["__kml_d2s_env"] = true
	e.emitGlobal("@__kml_d2s_env = internal constant i8 0, align 8")
}

func joinArgs(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// emitDynAnyMethodCall dispatches `obj.m(args)` on a bare any/unknown
// receiver: a Stage-3 chain-walking property read, then an indirect call
// through the tag-12 dynamic-function record with the receiver as `this` and
// every argument boxed. A non-function property is the JS TypeError.
func (e *Emitter) emitDynAnyMethodCall(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if !objVal.Ty.IsDynamic() {
		// A receiver inferred dynamic but emitted static (a number constant
		// under -compat=js): every path below reads a box.
		b, err := e.emitBoxValue(objVal)
		if err != nil {
			return Value{}, err
		}
		objVal = b
	}
	if propName == "apply" || propName == "call" {
		return e.emitDynApplyOrCall(objVal, propName, args, pos)
	}
	objVal = e.emitWrapperRecv(objVal, e.internString(propName))
	if propName == "valueOf" && len(args) == 0 && !e.inDynValueOf {
		return e.emitDynPrimValueOf(objVal, pos)
	}
	if dynBufferMethods[propName] && !e.inDynBufferCall && !e.anyArgIsFunction(args) {
		return e.emitDynBufferMethodCall(objVal, propName, args, pos)
	}
	if propName == "toString" && len(args) > 0 && !e.inDynNumberCall {
		return e.emitDynNumberToString(objVal, args, pos)
	}
	if _, ok := dynArrayMethods[propName]; ok {
		return e.emitDynArrayMethodCall(objVal, propName, args, pos)
	}
	if dynStringMethods[propName] {
		return e.emitDynStringMethodCall(objVal, propName, args, pos)
	}
	if dynNumberMethods[propName] {
		return e.emitDynNumberMethodCall(objVal, propName, args, pos)
	}
	if propName == "hasOwnProperty" && len(args) == 1 {
		return e.emitDynHasOwnPropertyCall(objVal, args, pos)
	}
	if propName == "propertyIsEnumerable" && len(args) == 1 {
		return e.emitPropertyIsEnumerable(objVal, args[0], pos)
	}
	if propName == "toString" && len(args) == 0 {
		return e.emitDynToStringCall(objVal, pos)
	}
	return e.emitDynAnyMethodCallPlain(objVal, propName, args, pos)
}

// emitDynToStringCall is `x.toString()` on an `any`: the receiver's own
// `toString` when it has one, else the prototype's, which for every value
// but null and undefined (whose read throws) is ToString of it: a
// primitive's String(x), an array's join, an object's "[object Object]".
func (e *Emitter) emitDynToStringCall(objVal Value, pos ast.Pos) (Value, error) {
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	ownL, builtinL, doneL := e.freshLabel("dyntos.own"), e.freshLabel("dyntos.builtin"), e.freshLabel("dyntos.done")
	// Only a dynamic object can carry a toString of its own (and a null or
	// undefined receiver's read throws); every other value is ToString'd.
	tag, _ := e.emitUnboxTagPayload(objVal)
	lookL := e.freshLabel("dyntos.look")
	isObj, isNull, isUndefTag, look := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagDynObject))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndefTag, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", look, isObj, isNull))
	look2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", look2, look, isUndefTag))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", look2, lookL, builtinL))
	e.emitLabel(lookL)
	fnBox, err := e.emitDynAnyMemberGetNamed(objVal, e.internString("toString"), "toString", pos)
	if err != nil {
		return Value{}, err
	}
	isUndef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, fnBox.Ref, nbUndefined))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUndef, builtinL, ownL))
	e.emitLabel(ownL)
	argv, n, err := e.emitDynArgv(nil, pos)
	if err != nil {
		return Value{}, err
	}
	r, err := e.emitDynFnBoxCallN(fnBox, objVal, argv, n, "toString is not a function", pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(builtinL)
	sv, err := e.emitArgToString(Value{Ref: objVal.Ref, Ty: TypeAny})
	if err != nil {
		return Value{}, err
	}
	sb, err := e.emitBoxValue(sv)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sb.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// emitDynHasOwnPropertyCall is `obj.hasOwnProperty(key)` on an `any`: the
// receiver's own `hasOwnProperty` when it has one, else Object.prototype's —
// own-property membership: a string's indices and `length`, none for a
// number or boolean.
func (e *Emitter) emitDynHasOwnPropertyCall(objVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	fnBox, err := e.emitDynAnyMemberGetNamed(objVal, e.internString("hasOwnProperty"), "hasOwnProperty", pos)
	if err != nil {
		return Value{}, err
	}
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	ownL, builtinL, doneL := e.freshLabel("dynhop.own"), e.freshLabel("dynhop.builtin"), e.freshLabel("dynhop.done")
	isUndef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, fnBox.Ref, nbUndefined))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUndef, builtinL, ownL))
	e.emitLabel(ownL)
	argv, n, err := e.emitDynArgv(args, pos)
	if err != nil {
		return Value{}, err
	}
	r, err := e.emitDynFnBoxCallN(fnBox, objVal, argv, n, "obj.hasOwnProperty is not a function", pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(builtinL)
	keyRef, err := e.dynAnyKeyRef(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	tag, _ := e.emitUnboxTagPayload(objVal)
	// A string's own keys are its indices and `length`, as an array's.
	strL, notStrL := e.emitTagCheck(tag, kmlTagString, "dynhop.str")
	e.emitLabel(strL)
	lenVal, err := e.emitDynAnyMemberGetNamed(objVal, e.internString("length"), "length", pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureFnMeta()
	sHas := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_array_has_key(i64 %s, ptr %s, i1 1, i64 0)", sHas, e.convert(lenVal, TypeI64).Ref, keyRef))
	sb, err := e.emitBoxValue(Value{Ref: sHas, Ty: TypeBool})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sb.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(notStrL)
	isPrim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", isPrim, tag, kmlTagBoolean))
	primL, objL := e.freshLabel("dynhop.prim"), e.freshLabel("dynhop.obj")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPrim, primL, objL))
	e.emitLabel(primL)
	f, err := e.emitBoxValue(Value{Ref: "false", Ty: TypeBool})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", f.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(objL)
	has, err := e.emitDynAnyHas(Value{Ref: objVal.Ref, Ty: TypeAny}, keyRef, true, pos)
	if err != nil {
		return Value{}, err
	}
	hb, err := e.emitBoxValue(has)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", hb.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// dynArrayMethods are the Array.prototype methods an `any` holding an array
// calls as the array's own (`anyValue.slice(1)`); true for a mutator.
var dynArrayMethods = map[string]bool{
	"at": false, "concat": false, "every": false, "filter": false, "find": false,
	"findIndex": false, "findLast": false, "findLastIndex": false, "flat": false,
	"flatMap": false, "forEach": false, "includes": false, "indexOf": false,
	"join": false, "lastIndexOf": false, "map": false, "reduce": false,
	"reduceRight": false, "slice": false, "some": false, "toReversed": false,
	"toSorted": false, "toSpliced": false, "with": false,
	"push": true, "pop": true, "shift": true, "unshift": true, "splice": true,
	"sort": true, "reverse": true, "fill": true, "copyWithin": true,
}

// emitDynArrayMethodCall is `obj.m(args)` on an `any` for an Array method
// name: an array receiver (a boxed static array or a dynamic one) runs the
// `any[]` method over its view (__kml_anyarr_view) — a mutator's result
// written back into it — with the result boxed; anything else takes the
// string or plain dynamic path.
func (e *Emitter) emitDynArrayMethodCall(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureDynJSONC()
	if !e.declaredAnyArrView {
		e.declaredAnyArrView = true
		e.emitGlobal("declare ptr @__kml_anyarr_view(i64)")
		e.emitGlobal("declare void @__kml_anyarr_sync(i64, ptr)")
	}
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	arrVal := e.emitArrayClassReceiver(objVal, propName)
	doneL := e.freshLabel("dynarrm.done")
	if !e.programIndependent() && e.usesTypedArrays() {
		// A TypedArray's own method (its result a TypedArray).
		e.declareFn("__kml_any_typed_index", "declare i64 @__kml_any_typed_index(i64)")
		k, isTyped := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_typed_index(i64 %s)", k, arrVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", isTyped, k))
		typedL, untypedL := e.freshLabel("dynarrm.typed"), e.freshLabel("dynarrm.untyped")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isTyped, typedL, untypedL))
		e.emitLabel(typedL)
		tv, err := e.emitDynAnyMethodCallPlain(arrVal, propName, args, pos)
		if err != nil {
			return Value{}, err
		}
		if !e.blockDone {
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", tv.Ref, resPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(untypedL)
	}
	view := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_view(i64 %s)", view, arrVal.Ref))
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isArr, view))
	arrL, otherL := e.freshLabel("dynarrm.arr"), e.freshLabel("dynarrm.other")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, otherL))
	e.emitLabel(arrL)
	// Named by the call's position: unique per site, the same in every program.
	tmp := fmt.Sprintf("__kml_dynarr_%d_%d", pos.Line, pos.Col)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", view, slot))
	e.define(tmp, Symbol{Ptr: slot, Ty: ArrayOf(TypeAny)})
	av, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(tmp, pos), propName, pos), e.dynArrayCallbackArgs(propName, args), pos))
	if err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		if dynArrayMethods[propName] {
			e.emitInstr(fmt.Sprintf("call void @__kml_anyarr_sync(i64 %s, ptr %s)", arrVal.Ref, view))
		}
		boxed, err := e.emitBoxValue(av)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(otherL)
	var ov Value
	if dynStringMethods[propName] {
		ov, err = e.emitDynStringMethodCall(objVal, propName, args, pos)
	} else {
		ov, err = e.emitDynAnyMethodCallPlain(objVal, propName, args, pos)
	}
	if err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		ob, err := e.emitBoxValue(ov)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ob.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, resPtr))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// dynCallbackMethods are the Array methods whose first argument is a
// callback.
var dynCallbackMethods = map[string]bool{
	"every": true, "filter": true, "find": true, "findIndex": true, "findLast": true,
	"findLastIndex": true, "flatMap": true, "forEach": true, "map": true, "reduce": true,
	"reduceRight": true, "some": true,
}

// dynArrayCallbackArgs is args for the array branch of an `any` receiver's
// Array method: a function literal whose annotated index or array parameter
// does not hold a number or an array (`(v: T[], k: string) => …`, written
// for the Map the value may be) is passed as a dynamic function, called with
// boxed arguments, rather than compiled against the array method's typed
// callback.
func (e *Emitter) dynArrayCallbackArgs(method string, args []ast.Expression) []ast.Expression {
	if len(args) == 0 && dynCallbackMethods[method] {
		// No callback: the method calls undefined, a TypeError.
		return []ast.Expression{e.bindValue(Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, ast.Pos{})}
	}
	idx := 1
	if method == "reduce" || method == "reduceRight" {
		idx = 2
	}
	out := args
	for i, a := range args {
		var params []ast.Param
		switch f := a.(type) {
		case *ast.ArrowFunction:
			params = f.Params
		case *ast.FunctionExpression:
			params = f.Params
		default:
			continue
		}
		fits := true
		for j, p := range params {
			if p.Type == nil || (j != idx && j != idx+1) {
				continue
			}
			t := e.resolveType(p.Type)
			if t.IsDynamic() {
				continue
			}
			if (j == idx && !isNumberTy(t)) || (j == idx+1 && !t.IsArray()) {
				fits = false
			}
		}
		if !fits {
			if &out[0] == &args[0] {
				out = append([]ast.Expression(nil), args...)
			}
			out[i] = ast.NewAsExpression(a, &ast.TypeAnnotation{Name: "any"}, a.GetPos())
		}
	}
	return out
}

// dynStringMethods are the String.prototype methods an `any` holding a
// string calls as the string's own (`anyValue.trim()`).
var dynStringMethods = map[string]bool{
	"trim": true, "trimStart": true, "trimEnd": true, "split": true, "slice": true,
	"substring": true, "toUpperCase": true, "toLowerCase": true, "indexOf": true,
	"lastIndexOf": true, "includes": true, "startsWith": true, "endsWith": true,
	"replace": true, "replaceAll": true, "charAt": true, "charCodeAt": true,
	"codePointAt": true, "padStart": true, "padEnd": true, "repeat": true,
	"at": true, "concat": true, "localeCompare": true,
}

// dynNumberMethods are the Number.prototype methods an `any` holding a
// number calls as the number's own (`anyValue.toFixed(2)`).
var dynNumberMethods = map[string]bool{"toFixed": true, "toPrecision": true, "toExponential": true}

// emitDynNumberMethodCall is `obj.m(args)` on an `any` for a Number method
// name: a number receiver runs the number's method (its string result
// boxed); anything else takes the dynamic path.
func (e *Emitter) emitDynNumberMethodCall(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	tag, _ := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	numL, otherL, doneL := e.freshLabel("dynnumm.num"), e.freshLabel("dynnumm.other"), e.freshLabel("dynnumm.done")
	isF, isI, isNum := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isF, tag, kmlTagFloat))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isI, tag, kmlTagInt))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isNum, isF, isI))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNum, numL, otherL))
	e.emitLabel(numL)
	tmp := fmt.Sprintf("__kml_dynnum_%d", e.dynFnCtr)
	e.dynFnCtr++
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca double, align 8", slot))
	e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", e.convert(objVal, TypeF64).Ref, slot))
	e.define(tmp, Symbol{Ptr: slot, Ty: TypeF64})
	nv, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(tmp, pos), propName, pos), args, pos))
	if err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		boxed, err := e.emitBoxValue(nv)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(otherL)
	ov, err := e.emitDynAnyMethodCallPlain(objVal, propName, args, pos)
	if err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		ob, err := e.emitBoxValue(ov)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ob.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, resPtr))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// emitDynStringMethodCall is `obj.m(args)` on an `any` for a String method
// name: a string receiver runs the string's method (its result boxed);
// anything else takes the dynamic path, which reads the property (a
// TypeError on null or undefined) and calls it.
func (e *Emitter) emitDynStringMethodCall(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	tag, pay := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	strL, otherL, doneL := e.freshLabel("dynstrm.str"), e.freshLabel("dynstrm.other"), e.freshLabel("dynstrm.done")
	isStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStr, strL, otherL))
	e.emitLabel(strL)
	// The string, bound to a temporary the ordinary method call reads.
	tmp := fmt.Sprintf("__kml_dynstr_%d", e.dynFnCtr)
	e.dynFnCtr++
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.emitIntToPtr(pay), slot))
	e.define(tmp, Symbol{Ptr: slot, Ty: TypePtr})
	sv, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(tmp, pos), propName, pos), args, pos))
	if err != nil {
		return Value{}, err
	}
	boxed, err := e.emitBoxValue(sv)
	if err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(otherL)
	dv, err := e.emitDynAnyMethodCallPlain(objVal, propName, args, pos)
	if err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		db, err := e.emitBoxValue(dv)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", db.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, resPtr))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// emitDynApplyOrCall is `f.apply(thisArg, args)` / `f.call(thisArg, …)` on
// an `any`: Function.prototype's when f holds a function, else f's own
// method of that name.
func (e *Emitter) emitDynApplyOrCall(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	tag, _ := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	fnL, propL := e.emitTagCheck(tag, kmlTagDynFunc, "dynapply")
	doneL := e.freshLabel("dynapply.done")
	e.emitLabel(fnL)
	thisVal := Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	if len(args) > 0 {
		tv, err := e.emitExprWithObjectHint(args[0], TypeAny)
		if err != nil {
			return Value{}, err
		}
		// A typed thisArg (a string literal) is the receiver as a box.
		if !tv.Ty.IsDynamic() {
			if tv, err = e.emitBoxValue(tv); err != nil {
				return Value{}, err
			}
		}
		thisVal = tv
	}
	var rest []ast.Expression
	if propName == "call" {
		if len(args) > 1 {
			rest = args[1:]
		}
	} else if len(args) > 1 {
		if _, isNull := args[1].(*ast.NullLiteral); !isNull {
			rest = []ast.Expression{ast.NewSpreadElement(args[1], args[1].GetPos())}
		}
	}
	argv, n, err := e.emitDynArgv(rest, pos)
	if err != nil {
		return Value{}, err
	}
	r, err := e.emitDynFnBoxCallN(objVal, thisVal, argv, n, e.notFunctionMsg(propName), pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(propL)
	pv, err := e.emitDynAnyMethodCallPlain(objVal, propName, args, pos)
	if err != nil {
		return Value{}, err
	}
	pb, err := e.emitBoxValue(pv)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pb.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}

func (e *Emitter) emitDynAnyMethodCallPlain(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	fnBox, err := e.emitDynAnyMemberGetNamed(objVal, e.internString(propName), propName, pos)
	if err != nil {
		return Value{}, err
	}
	argv, n, err := e.emitDynArgv(args, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFnBoxCallN(fnBox, objVal, argv, n, e.notFunctionMsg(propName), pos)
}

// emitDynArgv boxes call arguments into the dynamic ABI's argv (i64 boxes)
// and count. With a spread among them (`f(...args, cb)`) the arguments are
// built as an `any[]` and its elements are the argv.
func (e *Emitter) emitDynArgv(args []ast.Expression, pos ast.Pos) (argv, n string, err error) {
	for _, a := range args {
		if _, spread := a.(*ast.SpreadElement); spread {
			lit := ast.NewArrayLiteral(args, pos)
			v, err := e.emitExprWithObjectHint(lit, ArrayOf(TypeAny))
			if err != nil {
				return "", "", err
			}
			if !v.Ty.IsArray() || v.Ty.ElemType == nil || !v.Ty.ElemType.IsDynamic() {
				return "", "", fmt.Errorf("%d:%d: the spread arguments of a dynamic call must be an array", pos.Line, pos.Col)
			}
			data, length := e.freshReg(), e.freshReg()
			data = e.arrayData(v)
			length = e.arrayLen(v)
			return data, length, nil
		}
	}
	argv = e.freshReg()
	if len(args) > 0 {
		e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", argv, len(args)))
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argv)) // never read
	}
	for i, a := range args {
		av, err := e.emitExprWithObjectHint(a, TypeAny)
		if err != nil {
			return "", "", err
		}
		boxed, err := e.emitBoxValue(av)
		if err != nil {
			return "", "", err
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", slot, argv, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, slot))
	}
	return argv, fmt.Sprintf("%d", len(args)), nil
}

// emitClosureCallWithThis is `fn.call(thisArg, …)` / `fn.apply(thisArg,
// args)` on a function value under `-compat=js`: the function boxed (a
// dynamic function behind a thunk boxes back to itself) and called through
// the dynamic ABI with thisArg as the receiver; the result converted to
// fn's return type.
func (e *Emitter) emitClosureCallWithThis(fnVal Value, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	box, err := e.emitBoxValue(fnVal)
	if err != nil {
		return Value{}, err
	}
	thisVal := Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	if len(args) > 0 {
		if thisVal, err = e.emitExprWithObjectHint(args[0], TypeAny); err != nil {
			return Value{}, err
		}
	}
	var rest []ast.Expression
	if method == "call" {
		if len(args) > 1 {
			rest = args[1:]
		}
	} else if len(args) > 1 {
		if lit, ok := args[1].(*ast.ArrayLiteral); ok {
			rest = lit.Elements
		} else if _, isNull := args[1].(*ast.NullLiteral); !isNull {
			rest = []ast.Expression{ast.NewSpreadElement(args[1], args[1].GetPos())}
		}
	}
	argv, n, err := e.emitDynArgv(rest, pos)
	if err != nil {
		return Value{}, err
	}
	r, err := e.emitDynFnBoxCallN(box, thisVal, argv, n, e.notFunctionMsg(method), pos)
	if err != nil {
		return Value{}, err
	}
	if !fnVal.Ty.IsFunc() {
		return r, nil // a value of no static signature: the result as is
	}
	ret := TypeVoid
	if fnVal.Ty.FuncRetType != nil {
		ret = *fnVal.Ty.FuncRetType
	}
	if ret.IR == "" || ret.IR == "void" {
		return Value{Ty: TypeVoid}, nil
	}
	return e.convert(r, ret), nil
}

// emitDynBind is `fn.bind(thisArg, …bound)` under `-compat=js`: a dynamic
// function over @__kml_dyn_bound whose env holds fn (boxed), thisArg and
// the bound arguments; its length is fn's less the bound count. Converted
// to the bound signature.
func (e *Emitter) emitDynBind(fnVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	box, err := e.emitBoxValue(fnVal)
	if err != nil {
		return Value{}, err
	}
	thisVal := Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	if len(args) > 0 {
		if thisVal, err = e.emitExprWithObjectHint(args[0], TypeAny); err != nil {
			return Value{}, err
		}
		if thisVal, err = e.emitBoxValue(thisVal); err != nil {
			return Value{}, err
		}
	}
	var bound []ast.Expression
	if len(args) > 1 {
		bound = args[1:]
	}
	e.ensureDynBound()
	env := e.freshReg()
	e.heapMallocInto(env, fmt.Sprint(8*(3+len(bound))))
	e.heapStore("i64", box.Ref, env, 8)
	slot := func(i int) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", r, env, i))
		return r
	}
	e.heapStore("i64", thisVal.Ref, slot(1), 8)
	e.heapStore("i64", fmt.Sprint(len(bound)), slot(2), 8)
	for i, a := range bound {
		av, err := e.emitExprWithObjectHint(a, TypeAny)
		if err != nil {
			return Value{}, err
		}
		b, err := e.emitBoxValue(av)
		if err != nil {
			return Value{}, err
		}
		e.heapStore("i64", b.Ref, slot(3+i), 8)
	}
	// The record's length: fn's, less the bound arguments (never negative).
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, box.Ref))
	trec := e.emitIntToPtr(pay)
	tarSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", tarSlot, trec))
	tar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", tar, tarSlot))
	left := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", left, tar, len(bound)))
	neg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, left))
	arity := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", arity, neg, left))
	rec := e.freshReg()
	e.heapMallocInto(rec, "24")
	e.heapStore("ptr", "@__kml_dyn_bound", rec, 8)
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	e.heapStore("ptr", env, envSlot, 8)
	arSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", arSlot, rec))
	e.heapStore("i64", arity, arSlot, 8)
	out := Value{Ref: e.emitNbTagPtr(rec, kmlTagDynFunc), Ty: TypeAny}
	if !fnVal.Ty.IsFunc() {
		return out, nil // no static signature to take
	}
	n := len(bound)
	if n > len(fnVal.Ty.FuncParams) {
		n = len(fnVal.Ty.FuncParams)
	}
	ret := TypeVoid
	if fnVal.Ty.FuncRetType != nil {
		ret = *fnVal.Ty.FuncRetType
	}
	return e.convert(out, FuncType(fnVal.Ty.FuncParams[n:], ret)), nil
}

// ensureDynBound defines @__kml_dyn_bound, the dynamic-ABI body of a
// bound function: its env is { i64 target, i64 this, i64 n, [n x i64]
// bound }, and it calls target with that `this` and the bound arguments
// before its own.
func (e *Emitter) ensureDynBound() {
	if e.fnDecls["__kml_dyn_bound"] {
		return
	}
	e.fnDecls["__kml_dyn_bound"] = true
	e.registerFnMeta("@__kml_dyn_bound", "", 0, fnFlagBoundBox)
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureNanBox()
	e.emitGlobal(`define i64 @__kml_dyn_bound(ptr %env, i64 %this, i64 %argc, ptr %argv) {
entry:
  %target = load i64, ptr %env, align 8
  %thisp = getelementptr i64, ptr %env, i64 1
  %thisv = load i64, ptr %thisp, align 8
  %np = getelementptr i64, ptr %env, i64 2
  %n = load i64, ptr %np, align 8
  %bound = getelementptr i64, ptr %env, i64 3
  %total = add i64 %n, %argc
  %bytes = mul i64 %total, 8
  %sz = add i64 %bytes, 8
  %nargv = call ptr @malloc(i64 %sz)
  %nb = mul i64 %n, 8
  call ptr @memcpy(ptr %nargv, ptr %bound, i64 %nb)
  %tail = getelementptr i8, ptr %nargv, i64 %nb
  %ab = mul i64 %argc, 8
  call ptr @memcpy(ptr %tail, ptr %argv, i64 %ab)
  %pay = call i64 @__kml_nb_pay(i64 %target)
  %rec = inttoptr i64 %pay to ptr
  %fp = load ptr, ptr %rec, align 8
  %tenvp = getelementptr i8, ptr %rec, i64 8
  %tenv = load ptr, ptr %tenvp, align 8
  %r = call i64 %fp(ptr %tenv, i64 %thisv, i64 %total, ptr %nargv)
  ret i64 %r
}`)
}

// emitDynCallValue calls a boxed function value (a tag-12 dynamic function)
// directly, with an `undefined` receiver — the general "call the result of an
// expression that produced a function value" path (a factory-returned decorator
// `@tag(...)`, `getHandler()(x)`, a function element read out of an `any`).
// Boxes the arguments and dispatches through the tag-12 ABI.
func (e *Emitter) emitDynCallValue(fnBox Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitDynCallValueOf(fnBox, nil, args, pos)
}

// emitDynCallValueOf is emitDynCallValue for the call of callee, named in
// the TypeError a non-function throws as V8 names it.
func (e *Emitter) emitDynCallValueOf(fnBox Value, callee ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	argv, n, err := e.emitDynArgv(args, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFnBoxCallN(fnBox, Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, argv, n, calleeDisplayName(callee)+" is not a function", pos)
}

// notFunctionMsg is the TypeError of calling method prop when it is no
// function: named by the call's whole callee (`o.x`) when that is the
// member read, else by prop.
func (e *Emitter) notFunctionMsg(prop string) string {
	switch c := e.curCallee.(type) {
	case *ast.MemberExpression:
		if c.Property == prop {
			return calleeDisplayName(c) + " is not a function"
		}
	case *ast.IndexExpression:
		if k, ok := c.Index.(*ast.StringLiteral); ok && k.Value == prop {
			return calleeDisplayName(c) + " is not a function"
		}
	}
	return prop + " is not a function"
}

// calleeDisplayName is how V8 names a callee in "… is not a function":
// `u`, `o.x`, `a[0]`, `f(...)`, `this.q`; "value" for none.
func calleeDisplayName(x ast.Expression) string {
	switch x := x.(type) {
	case *ast.Identifier:
		return demangleModuleName(x.Name)
	case *ast.ThisExpression:
		return "this"
	case *ast.MemberExpression:
		return calleeDisplayName(x.Object) + "." + x.Property
	case *ast.IndexExpression:
		switch k := x.Index.(type) {
		case *ast.StringLiteral:
			return calleeDisplayName(x.Object) + "." + k.Value
		case *ast.NumberLiteral:
			return calleeDisplayName(x.Object) + "[" + k.Value + "]"
		}
		return calleeDisplayName(x.Object) + "[...]"
	case *ast.NonNullExpression:
		return calleeDisplayName(x.Arg)
	case *ast.CallExpression:
		return calleeDisplayName(x.Callee) + "(...)"
	}
	return "value"
}

// emitDynFnBoxCallUnchecked calls a boxed value already known to be a tag-12
// dynamic function (no tag check, no throw), against a pre-built argv and a
// pre-boxed receiver. Returns the boxed result register. For callers that have
// already guarded the tag themselves (e.g. the standard-decorator constructor
// tail), avoiding emitDynFnBoxCall's internal branch/throw.
func (e *Emitter) emitDynFnBoxCallUnchecked(fnBox, recvBox string, argv string, n int) string {
	payload := e.freshReg()
	e.ensureNanBox()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", payload, fnBox))
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rec, payload))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", env, envSlot))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, i64 %s, i64 %d, ptr %s)", r, fp, env, recvBox, n, argv))
	return r
}

// emitDynFnBoxCall dispatches a boxed function value (tag 12) against a
// pre-built argv, passing recvVal as the receiver and throwing errMsg on a
// non-function. Shared by dynamic method calls and bare dynamic-value calls.
func (e *Emitter) emitDynFnBoxCall(fnBox, recvVal Value, argv string, n int, errMsg string, pos ast.Pos) (Value, error) {
	return e.emitDynFnBoxCallN(fnBox, recvVal, argv, fmt.Sprintf("%d", n), errMsg, pos)
}

// emitDynFnBoxCallN is emitDynFnBoxCall with the argument count a register
// or constant.
func (e *Emitter) emitDynFnBoxCallN(fnBox, recvVal Value, argv string, n string, errMsg string, pos ast.Pos) (Value, error) {

	tag, payload := e.emitUnboxTagPayload(fnBox)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynFunc, "dyncall.fn")
	doneL := e.freshLabel("dyncall.done")
	e.emitLabel(matchL)
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rec, payload))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", env, envSlot))
	recvBox, err := e.emitBoxValue(recvVal)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, i64 %s, i64 %s, ptr %s)", r, fp, env, recvBox.Ref, n, argv))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	// A builtin constructor held as a value (`const O: any = Object`): its
	// call as a function (dynobj.c).
	refL, notRefL := e.emitTagCheck(tag, kmlTagFuncRef, "dyncall.classref")
	e.emitLabel(refL)
	e.ensureDynObj()
	cr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classref_call(ptr %s, i64 %s, ptr %s)", cr, e.emitIntToPtr(payload), n, argv))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", cr, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(notRefL)
	e.emitThrowTypeError(errMsg)
	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypeAny}, nil
}

// emitAnyToClosure converts a boxed function value (a tag-12 dynamic
// function) to the static closure type target: a thunk with target's
// signature boxes its arguments, calls the record through the dynamic ABI
// with an undefined receiver, and converts the result back. ok is false for a
// signature the thunk does not take (an array parameter).
//
// The thunk's env is { @__kml_d2s_env, box }: boxing the closure again
// (emitDynClosureAdapter) recognises the tag and returns the original
// function, so a caller that supplies a receiver (an emitter's emit, a
// method call) reaches it with its `this`, and identity survives the trip.
func (e *Emitter) emitAnyToClosure(v Value, target Type) (Value, bool) {
	if target.FuncHasDefaultMask {
		return Value{}, false
	}
	e.ensureMalloc()
	e.ensureD2SEnvTag()
	env := e.freshReg()
	e.heapMallocInto(env, "16")
	e.heapStore("ptr", "@__kml_d2s_env", env, 8)
	boxSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", boxSlot, env))
	e.heapStore("i64", v.Ref, boxSlot, 8)
	thunk := e.emitDynToStaticThunk(target)
	hdr := e.buildBuiltinClosure(thunk, env)
	// An absent function (`undefined`/`null`, an optional member not given)
	// is the null closure, not a thunk around the absence.
	isU, isN, absent := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, v.Ref, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isN, v.Ref, nbNull))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, isU, isN))
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", out, absent, hdr))
	return Value{Ref: out, Ty: target}, true
}

// emitDynToStaticThunk emits emitAnyToClosure's thunk for target.
func (e *Emitter) emitDynToStaticThunk(target Type) string {
	restore := e.beginThunkEmit()
	restore = e.sharedBody(restore) // content-named: the same in every program
	params := []string{"ptr %env"}
	for i, p := range target.FuncParams {
		if p.IsArray() {
			// A closure's array parameter: its header and length.
			params = append(params, fmt.Sprintf("ptr %%a%d, i64 %%a%d_len", i, i))
			continue
		}
		params = append(params, fmt.Sprintf("%s %%a%d", storageIR(p), i))
	}
	boxSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 8", boxSlot))
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", box, boxSlot))
	n := len(target.FuncParams)
	// A `this: T` parameter (FuncParams[0]) is the call's receiver.
	first, recv := 0, fmt.Sprintf("%d", nbUndefined)
	if target.FuncThis && n > 0 {
		first = 1
		if bv, err := e.emitBoxValue(Value{Ref: "%a0", Ty: target.FuncParams[0]}); err == nil {
			recv = bv.Ref
		}
	}
	if target.FuncHasRest && n > first {
		// A rest parameter spreads into the dynamic call's arguments: argv is
		// the fixed parameters' boxes followed by each rest element's.
		fixed := n - 1
		restTy := target.FuncParams[fixed]
		elemTy := TypeAny
		if restTy.ElemType != nil {
			elemTy = *restTy.ElemType
		}
		e.ensureMalloc()
		rest := e.arrayValueFromHeaderReg(fmt.Sprintf("%%a%d", fixed), restTy)
		rdata, rlen := e.freshReg(), e.freshReg()
		rdata = e.arrayData(rest)
		rlen = e.arrayLen(rest)
		total := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", total, rlen, fixed-first))
		bytes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, total))
		argv := e.freshReg()
		e.heapMallocInto(argv, bytes)
		for i := first; i < fixed; i++ {
			p := target.FuncParams[i]
			arg := Value{Ref: fmt.Sprintf("%%a%d", i), Ty: p}
			if p.IsArray() {
				arg = e.arrayValueFromHeaderReg(arg.Ref, p)
			}
			bv, err := e.emitBoxValue(arg)
			if err != nil {
				bv = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
			}
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, argv, i-first))
			e.heapStore("i64", bv.Ref, gep, 8)
		}
		jp := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", jp))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", jp))
		condL, bodyL, doneL := e.freshLabel("d2s.rest"), e.freshLabel("d2s.restbody"), e.freshLabel("d2s.restdone")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		j := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", j, jp))
		more := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, j, rlen))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
		e.emitLabel(bodyL)
		ep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", ep, elemTy.IR, rdata, j))
		ev := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", ev, elemTy.IR, ep, elemTy.Align()))
		ebv, err := e.emitBoxValue(Value{Ref: ev, Ty: elemTy})
		if err != nil {
			ebv = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
		}
		di := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", di, j, fixed-first))
		dp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", dp, argv, di))
		e.heapStore("i64", ebv.Ref, dp, 8)
		jn := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", jn, j))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", jn, jp))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(doneL)
		fp := e.freshReg()
		pay := e.freshReg()
		e.ensureNanBox()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, box))
		rec := e.emitIntToPtr(pay)
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, rec))
		envSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", env, envSlot))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, i64 %s, i64 %s, ptr %s)", r, fp, env, recv, total, argv))
		return e.finishDynToStaticThunk(target, params, r, restore)
	}
	argv := e.freshReg()
	if n-first > 0 {
		e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", argv, n-first))
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argv))
	}
	for i, p := range target.FuncParams {
		if i < first {
			continue
		}
		arg := Value{Ref: fmt.Sprintf("%%a%d", i), Ty: p}
		if p.IsArray() {
			// Boxed from its live header: the callee aliases the same array.
			arg = e.arrayValueFromHeaderReg(arg.Ref, p)
		}
		bv, err := e.emitBoxValue(arg)
		if err != nil {
			bv = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, argv, i-first))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bv.Ref, gep))
	}
	r := e.emitDynFnBoxCallUnchecked(box, recv, argv, n-first)
	return e.finishDynToStaticThunk(target, params, r, restore)
}

// finishDynToStaticThunk converts the dynamic call's result r to the
// thunk's return type and writes the function out.
func (e *Emitter) finishDynToStaticThunk(target Type, params []string, r string, restore func()) string {
	ret := TypeVoid
	if target.FuncRetType != nil {
		ret = *target.FuncRetType
	}
	retIR := "void"
	if ret.IR != "" && ret.IR != "void" {
		rv := e.convert(Value{Ref: r, Ty: TypeAny}, ret)
		if ret.IsArray() {
			// An array returns its header pointer (LLVMRetType).
			rv.Ref = e.arrayReturnHeader(rv)
		}
		e.emitInstr(fmt.Sprintf("ret %s %s", ret.LLVMRetType(), rv.Ref))
		retIR = ret.LLVMRetType()
	} else {
		e.emitInstr("ret void")
	}
	body := e.allocas.String() + e.body.String()
	restore()
	name := e.defineContentNamed("@__kml_dyn2static.", retIR, strings.Join(params, ", "), body)
	e.registerFnMeta(name, "", 0, fnFlagThroughBox)
	return name
}

// unboxArgToParam is a boxed argument bound to a parameter of type pty.
func (e *Emitter) unboxArgToParam(word string, pty Type) Value {
	if s, ok := e.dynArgIntoString(word, pty); ok {
		return s
	}
	return e.emitUnboxBoxToType(word, pty)
}

// dynArgIntoString is a dynamic-ABI argument bound to a string parameter:
// a value of another kind (a Buffer emitted to a `(chunk: string) => …`
// listener) is converted with ToString at its uses in JS, so here.
func (e *Emitter) dynArgIntoString(word string, pty Type) (Value, bool) {
	if !isForOfStringTy(pty) || pty.IsStrLiteral() || pty.IsClass() || pty.IsDynamic() || isNullableScalar(pty) {
		return Value{}, false
	}
	return e.emitAnyIntoString(Value{Ref: word, Ty: TypeAny}, pty)
}

// dynBufferMethods are Buffer.prototype's own methods (and those whose
// result on a Buffer differs from an array's): on an `any` holding a Buffer
// they run as the Buffer method itself.
var dynBufferMethods = map[string]bool{
	"toString": true, "toJSON": true, "equals": true, "compare": true, "copy": true,
	"slice": true, "subarray": true, "write": true, "fill": true, "indexOf": true,
	"lastIndexOf": true, "includes": true, "swap16": true, "swap32": true, "swap64": true,
	"readUInt8": true, "readUint8": true, "readInt8": true,
	"readUInt16LE": true, "readUInt16BE": true, "readUint16LE": true, "readUint16BE": true,
	"readInt16LE": true, "readInt16BE": true,
	"readUInt32LE": true, "readUInt32BE": true, "readUint32LE": true, "readUint32BE": true,
	"readInt32LE": true, "readInt32BE": true,
	"readFloatLE": true, "readFloatBE": true, "readDoubleLE": true, "readDoubleBE": true,
	"readBigInt64LE": true, "readBigInt64BE": true, "readBigUInt64LE": true, "readBigUInt64BE": true,
	"writeUInt8": true, "writeUint8": true, "writeInt8": true,
	"writeUInt16LE": true, "writeUInt16BE": true, "writeInt16LE": true, "writeInt16BE": true,
	"writeUInt32LE": true, "writeUInt32BE": true, "writeInt32LE": true, "writeInt32BE": true,
	"writeFloatLE": true, "writeFloatBE": true, "writeDoubleLE": true, "writeDoubleBE": true,
}

// emitDynBufferMethodCall is `x.method(args)` on an `any`: a Buffer's
// method when x holds a Buffer at run time, else the dynamic call as for
// any other value.
func (e *Emitter) emitDynBufferMethodCall(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	tag, pay := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
	probeL, bufL, otherL, doneL := e.freshLabel("dynbuf.probe"), e.freshLabel("dynbuf.buf"), e.freshLabel("dynbuf.other"), e.freshLabel("dynbuf.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, probeL, otherL))
	e.emitLabel(probeL)
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, pay))
	tp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", tp, anyArrayBoxTy, box))
	typed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", typed, tp))
	isBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBuf, typed, anyArrayBuffer))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBuf, bufL, otherL))

	e.emitLabel(bufL)
	// The Buffer method may not take these arguments (a stream's
	// `write(chunk, cb)`): then the branch is the dynamic call too.
	savedBody, savedAllocas, savedDone := e.body.String(), e.allocas.String(), e.blockDone
	if !e.emitDynBufferBranch(objVal, propName, args, pos, resPtr, doneL) {
		e.body.Reset()
		e.body.WriteString(savedBody)
		e.allocas.Reset()
		e.allocas.WriteString(savedAllocas)
		e.blockDone = savedDone
		e.emitTerminator(fmt.Sprintf("br label %%%s", otherL))
	}

	e.emitLabel(otherL)
	prevDyn := e.inDynBufferCall
	e.inDynBufferCall = true
	ov, err := e.emitDynAnyMethodCall(objVal, propName, args, pos)
	e.inDynBufferCall = prevDyn
	if err != nil {
		return Value{}, err
	}
	ob, err := e.emitBoxValue(ov)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ob.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// emitDynBufferBranch emits the Buffer method call on objVal (known to hold
// a Buffer) into resPtr, then branches to doneL; false when the method does
// not take these arguments.
func (e *Emitter) emitDynBufferBranch(objVal Value, propName string, args []ast.Expression, pos ast.Pos, resPtr, doneL string) bool {
	return e.emitDynStaticBranch(objVal, BufferType(), propName, args, pos, resPtr, doneL)
}

// builtinMethodLibFn is the TypeScript function a builtin type's method
// declaration lowers to (`/** @lower __kml_Buffer_swap16 */ swap16()`),
// when the program links it.
func (e *Emitter) builtinMethodLibFn(ty Type, prop string) (string, bool) {
	name := ""
	switch {
	case ty.IsBuffer():
		name = "Buffer"
	default:
		return "", false
	}
	sym := e.lookupBuiltinType(name)
	if sym == nil {
		return "", false
	}
	for _, d := range sym.Declarations {
		decl, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok {
			continue
		}
		for _, m := range decl.Members {
			if ms, ok := m.(*ast.MethodSignature); ok && ms.Name == prop && ms.Lower != "" {
				fn, linked := e.globalLinks[ms.Lower]
				return fn, linked
			}
		}
	}
	return "", false
}

// emitDynStaticBranch emits ty's own method call on objVal (known to hold a
// ty at run time) into resPtr, then branches to doneL; false when the
// method does not take these arguments.
func (e *Emitter) emitDynStaticBranch(objVal Value, ty Type, propName string, args []ast.Expression, pos ast.Pos, resPtr, doneL string) bool {
	slot := e.freshReg()
	if ty.IsArray() {
		buf := e.emitUnboxBoxToType(objVal.Ref, ty)
		hdr := buf.Ref
		if hdr == "" {
			hdr = e.boxArrayValue(buf)
		}
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, slot))
	} else {
		v := e.convert(objVal, ty)
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, ty.IR, ty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, v.Ref, slot, ty.Align()))
	}
	// Named by the call's position: the same in every program.
	name := fmt.Sprintf("__kml_dynrecv_%d_%d", pos.Line, pos.Col)
	e.pushScope()
	e.define(name, Symbol{Ptr: slot, Ty: ty})
	call := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(name, pos), propName, pos), args, pos)
	if fn, ok := e.builtinMethodLibFn(ty, propName); ok {
		// A method written in TypeScript (its declaration's `@lower`): the
		// receiver is its function's first argument, as a checked call
		// lowers it.
		call = ast.NewCallExpression(ast.NewIdentifier(fn, pos), append([]ast.Expression{ast.NewIdentifier(name, pos)}, args...), pos)
	}
	// The static call does not come back here: a method it cannot take is
	// the branch's failure, not another dynamic probe of the same value.
	prevDyn := e.inDynBufferCall
	e.inDynBufferCall = true
	v, err := e.emitExpr(call)
	e.inDynBufferCall = prevDyn
	e.popScope()
	if err != nil {
		return false
	}
	var boxed Value
	if v.Ref == "" || v.Ty.IR == "void" {
		boxed = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	} else if boxed, err = e.emitBoxValue(v); err != nil {
		return false
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	return true
}

// emitDynNumberToString is `x.toString(radix)` on an `any`: Number's when x
// holds a number, else the dynamic call as for any other value.
func (e *Emitter) emitDynNumberToString(objVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	tag, _ := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	isNum := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, 1", isNum, tag))
	numL, otherL, doneL := e.freshLabel("dynnum.num"), e.freshLabel("dynnum.other"), e.freshLabel("dynnum.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNum, numL, otherL))
	e.emitLabel(numL)
	savedBody, savedAllocas, savedDone := e.body.String(), e.allocas.String(), e.blockDone
	if !e.emitDynStaticBranch(objVal, TypeF64, "toString", args, pos, resPtr, doneL) {
		e.body.Reset()
		e.body.WriteString(savedBody)
		e.allocas.Reset()
		e.allocas.WriteString(savedAllocas)
		e.blockDone = savedDone
		e.emitTerminator(fmt.Sprintf("br label %%%s", otherL))
	}
	e.emitLabel(otherL)
	e.inDynNumberCall = true
	ov, err := e.emitDynAnyMethodCall(objVal, "toString", args, pos)
	e.inDynNumberCall = false
	if err != nil {
		return Value{}, err
	}
	ob, err := e.emitBoxValue(ov)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ob.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// anyArgIsFunction reports whether an argument is a function: no Buffer
// method takes one, so such a call (a stream's `write(chunk, cb)`) is never
// a Buffer method's.
func (e *Emitter) anyArgIsFunction(args []ast.Expression) bool {
	for _, a := range args {
		switch a.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			return true
		}
		if e.inferExprType(a).IsFunc() {
			return true
		}
	}
	return false
}

// emitDynArgvFrom collects the dynamic ABI's argv words from index start on
// into a dynamic array (tag 11), returned boxed: a rest parameter's value,
// or from 0 a function's `arguments`.
func (e *Emitter) emitDynArgvFrom(start int) string {
	e.ensureDynArr()
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 0)", arr))
	jPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", jPtr))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", start, jPtr))
	condL := e.freshLabel("dynrest.cond")
	bodyL := e.freshLabel("dynrest.body")
	doneL := e.freshLabel("dynrest.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	j := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", j, jPtr))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %%p_argc", more, j))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %s", slot, j))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, slot))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", arr, w))
	jn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", jn, j))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", jn, jPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return e.emitNbTagPtr(arr, kmlTagDynArray)
}

// emitArgvRestArray gathers the dynamic ABI's argv words from index start
// on, each unboxed to elemTy (an any element keeps its box), into a fresh
// typed array: its header and length.
func (e *Emitter) emitArgvRestArray(start int, elemTy Type) (hdr, n string) {
	nRest := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %%p_argc, %d", nRest, start))
	neg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, nRest))
	n = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, neg, nRest))
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, n, elemTy.Align()))
	e.ensureMalloc()
	data := e.freshReg()
	e.heapMallocInto(data, bytes)
	jPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", jPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", jPtr))
	condL := e.freshLabel("adrest.cond")
	bodyL := e.freshLabel("adrest.body")
	doneL := e.freshLabel("adrest.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	j := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", j, jPtr))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, j, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	srcIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", srcIdx, j, start))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %s", slot, srcIdx))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, slot))
	ev := Value{Ref: w, Ty: elemTy} // an any element keeps its box
	if !elemTy.IsDynamic() {
		ev = e.emitUnboxBoxToType(w, elemTy)
	}
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dst, elemTy.IR, data, j))
	e.heapStore(elemTy.IR, ev.Ref, dst, elemTy.Align())
	jn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", jn, j))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", jn, jPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return e.newArrayHeader(data, n), n
}

// emitArgWord is argument i of an adapter's (argc, argv) as a box: undefined
// when the caller passed fewer, argv then never read (a call with no
// arguments may pass a null argv).
func (e *Emitter) emitArgWord(i int) string {
	box := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", box))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, box))
	have := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%p_argc, %d", have, i))
	haveL, doneL := e.freshLabel("arg.have"), e.freshLabel("arg.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", have, haveL, doneL))
	e.emitLabel(haveL)
	slot, loaded := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %d", slot, i))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loaded, slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", loaded, box))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	word := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", word, box))
	return word
}
