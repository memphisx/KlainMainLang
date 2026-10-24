package llvm

// emit_protoclass.go — vanilla-JS prototype "classes" (TDD-00155 Stage 4,
// `-compat=js` only): pre-ES6 function constructors.
//
//	function Animal(name) { this.name = name; }
//	Animal.prototype.speak = function() { return this.name + " speaks"; };
//	const a = new Animal("Rex");   // tag-10 bag, proto → Animal.prototype
//	a.speak();                     // Stage-3 chain walk + dynamic-ABI call
//
// A top-level function declaration is *recognized* as a prototype
// constructor when the program uses it with `new` or writes to its
// `.prototype`. A recognized constructor is emitted with a hidden boxed
// `this` first parameter (its body's `this.x = v` writes are Stage-1
// dynamic member sets), its per-function prototype bag lives in a lazily
// created module global, and `new F(args)` builds a fresh bag linked to it.
// Direct calls (`F()`) are rejected — except the classic inheritance
// chain `Base.call(this, args)`, which passes the current receiver through.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/ast"
)

// jsCollectProtoCtors recognizes prototype constructors: top-level function
// declarations that appear in a `new` expression or whose `.prototype` is
// written at top level. Runs after registerFunctions; the checker types a
// constructor's parameters from its `new` sites too.
func (e *Emitter) jsCollectProtoCtors(prog *ast.Program) {
	e.jsProtoCtor = map[string]bool{}
	e.sloppyFileSet = e.sloppyFiles(prog)
	e.sloppyWriteNodes = sloppyWriteSites(prog, e.sloppyFileSet)
	fnDecls := map[string]*ast.FunctionDeclaration{}
	for _, s := range prog.Body {
		if fd, ok := s.(*ast.FunctionDeclaration); ok && !fd.IsGenerator && !fd.IsAsync && len(fd.TypeParams) == 0 {
			fnDecls[fd.Name] = fd
		}
	}
	defer func() {
		// One declared with a `this: T` parameter takes the receiver there.
		e.protoCtorOwnThis = map[string]bool{}
		for n := range e.jsProtoCtor {
			// What one returns (`return new F(v)` from a plain call) is any.
			if mangled, sig, ok := e.resolveFuncRef(n); ok && sig.RetType.IR != "void" && !sig.RetType.IsDynamic() {
				sig.RetType = TypeAny
				e.funcs[mangled] = sig
			}
			if fd := fnDecls[n]; e.maybeAddThisParam(fd) {
				e.protoCtorOwnThis[n] = true
			}
		}
	}()
	ast.Inspect(prog, func(n ast.Node) bool {
		if nx, ok := n.(*ast.NewExpression); ok && fnDecls[protoCtorCallee(nx)] != nil {
			e.jsProtoCtor[protoCtorCallee(nx)] = true
		}
		return true
	})
	if !e.compatJS() {
		// TypeScript constructs a function only through a cast
		// (`new (F as any)(…)`); a `.prototype` write alone is no sign.
		return
	}
	for _, s := range prog.Body {
		es, ok := s.(*ast.ExpressionStatement)
		if !ok {
			continue
		}
		assign, ok := es.Expr.(*ast.AssignmentExpression)
		if !ok {
			continue
		}
		// `F.prototype = ...` or `F.prototype.m = ...` (any depth under it).
		mem, ok := assign.Left.(*ast.MemberExpression)
		for ok {
			if id, isID := mem.Object.(*ast.Identifier); isID {
				if mem.Property == "prototype" && fnDecls[id.Name] != nil {
					e.jsProtoCtor[id.Name] = true
				}
				break
			}
			mem, ok = mem.Object.(*ast.MemberExpression)
		}
	}
}

// sloppyFiles is the set of the program's own JavaScript files that run in
// sloppy mode, as Node runs them: CommonJS (no import or export), no
// "use strict" directive, not .mjs.
func (e *Emitter) sloppyFiles(prog *ast.Program) map[string]bool {
	strict := map[string]bool{}
	first := map[string]bool{}
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
		if !strict[f] && !prog.ESMFiles[f] && !e.fileIsLib(f) && (ext == ".js" || ext == ".cjs") {
			out[f] = true
		}
	}
	return out
}

// sloppyWriteSites records, for each assignment, update and delete in a
// sloppy file, whether it runs in sloppy code: a class body and a
// function with a "use strict" directive are strict. A sloppy write that
// fails (a read-only or non-configurable property, a frozen object) does
// nothing instead of throwing.
func sloppyWriteSites(prog *ast.Program, sloppy map[string]bool) map[ast.Node]bool {
	out := map[ast.Node]bool{}
	entersStrict := func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ClassDeclaration, *ast.ClassExpression:
			return true
		case *ast.FunctionDeclaration:
			return x.Body != nil && hasUseStrict(x.Body.Body)
		case *ast.FunctionExpression:
			return x.Body != nil && hasUseStrict(x.Body.Body)
		case *ast.ArrowFunction:
			return x.Block != nil && hasUseStrict(x.Block.Body)
		}
		return false
	}
	var walk func(n ast.Node, strict bool)
	walk = func(root ast.Node, strict bool) {
		strict = strict || entersStrict(root)
		ast.Inspect(root, func(n ast.Node) bool {
			if !strict && n != root && entersStrict(n) {
				walk(n, true)
				return false
			}
			switch x := n.(type) {
			case *ast.AssignmentExpression, *ast.UpdateExpression:
				out[n] = !strict
			case *ast.UnaryExpression:
				if x.Op == "delete" {
					out[n] = !strict
				}
			}
			return true
		})
	}
	for _, st := range prog.Body {
		if sloppy[prog.StmtFiles[st]] {
			walk(st, false)
		}
	}
	return out
}

// hasUseStrict reports a body whose directive prologue has "use strict".
func hasUseStrict(body []ast.Statement) bool {
	for _, st := range body {
		es, ok := st.(*ast.ExpressionStatement)
		if !ok {
			return false
		}
		lit, ok := es.Expr.(*ast.StringLiteral)
		if !ok {
			return false
		}
		if lit.Value == "use strict" {
			return true
		}
	}
	return false
}

// ensureProtoBag emits (once per constructor) the lazily-initialized
// module global holding F's prototype bag, and returns the getter's name.
// A plain (non-thread-local) global — Boehm scans the data segment, so no
// uncollectable treatment is needed (unlike the frozen-set's TLS slot).
func (e *Emitter) ensureProtoBag(ctorName string) string {
	if e.protoBagEmitted == nil {
		e.protoBagEmitted = map[string]bool{}
	}
	getter := "@__kml_protoget_" + ctorName
	if e.protoBagEmitted[ctorName] {
		return getter
	}
	e.protoBagEmitted[ctorName] = true
	e.ensureDynObj()
	global := "@__kml_proto_" + ctorName
	e.emitGlobal(fmt.Sprintf("%s = internal %sglobal ptr null, align 8", global, e.isolateTLS()))
	e.emitGlobal(fmt.Sprintf(`
define ptr %s() {
entry:
  %%cur = load ptr, ptr %s, align 8
  %%isnull = icmp eq ptr %%cur, null
  br i1 %%isnull, label %%init, label %%have
init:
  %%bag = call ptr @__kml_dynobj_new()
  store ptr %%bag, ptr %s, align 8
  br label %%have
have:
  %%out = load ptr, ptr %s, align 8
  ret ptr %%out
}`, getter, global, global, global))
	return getter
}

// emitProtoBagRead evaluates `F.prototype` — the boxed bag.
func (e *Emitter) emitProtoBagRead(ctorName string) Value {
	getter := e.ensureProtoBag(ctorName)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s()", bag, getter))
	return e.emitDynObjBox(bag)
}

// emitProtoBagWrite implements `F.prototype = value` — re-points the global
// at a dynamic object (or null); anything else is the JS-style TypeError.
func (e *Emitter) emitProtoBagWrite(ctorName string, rhs Value, pos ast.Pos) (Value, error) {
	e.ensureProtoBag(ctorName)
	boxed, err := e.emitBoxValue(rhs)
	if err != nil {
		return Value{}, err
	}
	ptrReg, _, err := e.emitDynProtoOperand(boxed, true)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_proto_%s, align 8", ptrReg, ctorName))
	return boxed, nil
}

// emitProtoCtorArgs emits the typed argument list for a constructor-function
// invocation (`new F(...)` / `Base.call(this, ...)`), per its registered
// sig: scalar/string/object/dynamic params (array and nullable-scalar
// params are rejected cleanly). As in JavaScript, a missing argument is
// undefined (the stand-in a call's omitted argument gets) and an extra one
// is evaluated, then dropped.
func (e *Emitter) emitProtoCtorArgs(name string, sig FuncSig, args []ast.Expression, pos ast.Pos) ([]string, error) {
	var parts []string
	for i, pty := range sig.ParamTypes {
		if sig.HasRest && i == len(sig.ParamTypes)-1 && pty.IsArray() {
			// The rest parameter (an explicit one, or the one collecting
			// `arguments`' overflow): the arguments from here, as an array.
			var rest []ast.Expression
			if i < len(args) {
				rest = args[i:]
			}
			lit := ast.NewArrayLiteral(rest, pos)
			val, err := e.emitExprWithObjectHint(lit, pty)
			if err != nil {
				return nil, err
			}
			header, lenReg := e.packArrayArg(lit, e.convert(val, pty), pty)
			parts = append(parts, "ptr "+header, "i64 "+lenReg)
			args = args[:min(len(args), i)]
			break
		}
		if pty.IsArray() || isNullableScalar(pty) {
			return nil, fmt.Errorf("%d:%d: an array/nullable parameter on a prototype constructor is not supported yet", pos.Line, pos.Col)
		}
		if i >= len(args) {
			if pty.IsDynamic() {
				parts = append(parts, fmt.Sprintf("i64 %d", nbUndefined))
			} else {
				parts = append(parts, fmt.Sprintf("%s %s", pty.IR, pty.zeroLiteral()))
			}
			continue
		}
		a := args[i]
		val, err := e.emitExprWithObjectHint(a, pty)
		if err != nil {
			return nil, err
		}
		if pty.Inferred && !isSafeNumericArg(val.Ty) {
			return nil, fmt.Errorf("%d:%d: constructor parameter %d of '%s' has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", a.GetPos().Line, a.GetPos().Col, i+1, name)
		}
		if pty.IsDynamic() {
			b, err := e.emitBoxValue(val)
			if err != nil {
				return nil, err
			}
			parts = append(parts, "i64 "+b.Ref)
			continue
		}
		val = e.convert(val, pty)
		parts = append(parts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}
	for _, a := range args[min(len(args), len(sig.ParamTypes)):] {
		if _, err := e.emitExpr(a); err != nil {
			return nil, err
		}
	}
	return parts, nil
}

// emitProtoCtorNew implements `new F(args)` for a recognized constructor
// function: a fresh bag whose proto is F's prototype bag, the constructor
// body run with the boxed bag as `this`, and the bag as the result (a
// constructor return value is ignored, as in JS for primitive returns).
func (e *Emitter) emitProtoCtorNew(ex *ast.NewExpression) (Value, error) {
	llvmName, sig, ok := e.resolveFuncRef(ex.ClassName)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unknown constructor function '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
	}
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	getter := e.ensureProtoBag(ex.ClassName)
	proto := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s()", proto, getter))
	ok2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_set_proto(ptr %s, ptr %s)", ok2, bag, proto))
	thisBox := e.emitDynObjBox(bag)

	r, err := e.emitProtoCtorInvoke(ex.ClassName, llvmName, sig, thisBox, ex.Args, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	if r.Ty.IR == "void" || r.Ty.IsUndefined() || !(r.Ty.IsDynamic() || r.Ty.IsObject() || r.Ty.IsClass() || r.Ty.IsArray() || r.Ty.IsFunc()) {
		return thisBox, nil
	}
	// A constructor returning an object makes it the result.
	rb, err := e.emitBoxValue(r)
	if err != nil {
		return Value{}, err
	}
	// The object tags run from kmlTagObject through kmlTagError.
	tag, _ := e.emitUnboxTagPayload(rb)
	lo, hi, isObj, res := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i8 %s, %d", lo, tag, kmlTagObject))
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", hi, tag, kmlTagError))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", isObj, lo, hi))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", res, isObj, rb.Ref, thisBox.Ref))
	return Value{Ref: res, Ty: TypeAny}, nil
}

// emitProtoCtorChainCall implements `Base.call(thisExpr, args...)` — the
// classic pre-ES6 inheritance chain: run Base's constructor body against the
// caller's own receiver.
func (e *Emitter) emitProtoCtorChainCall(ctorName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: %s.call needs a `this` argument", pos.Line, pos.Col, ctorName)
	}
	llvmName, sig, ok := e.resolveFuncRef(ctorName)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unknown constructor function '%s'", pos.Line, pos.Col, ctorName)
	}
	thisVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	thisBox, err := e.emitBoxValue(thisVal)
	if err != nil {
		return Value{}, err
	}
	if _, err := e.emitProtoCtorInvoke(ctorName, llvmName, sig, thisBox, args[1:], pos); err != nil {
		return Value{}, err
	}
	return Value{Ref: "0", Ty: TypeUndefined}, nil
}

// emitProtoCtorInvoke runs constructor function name's body with the boxed
// receiver thisBox and returns its result.
func (e *Emitter) emitProtoCtorInvoke(name, llvmName string, sig FuncSig, thisBox Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	parts, err := e.emitProtoCtorArgs(name, e.protoCtorArgSig(name, sig), args, pos)
	if err != nil {
		return Value{}, err
	}
	argStr := "i64 " + thisBox.Ref
	for _, p := range parts {
		argStr += ", " + p
	}
	// A body reading `arguments` takes how many parameters the call passed.
	as := e.protoCtorArgSig(name, sig)
	regular := len(as.ParamTypes)
	if as.HasRest {
		regular--
	}
	e.noteArgc(countsArgs(sig.HasRest, sig.ParamNames), args, regular)
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argStr))
		return Value{Ref: "0", Ty: TypeUndefined}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", r, sig.RetType.LLVMRetType(), llvmName, argStr))
	return Value{Ref: r, Ty: sig.RetType}, nil
}

// emitProtoCtorPlainCall is `F(args)` for a constructor function F: its
// body run with globalThis as `this`, as a sloppy-mode call is.
func (e *Emitter) emitProtoCtorPlainCall(name string, args []ast.Expression, pos ast.Pos) (Value, error) {
	llvmName, sig, ok := e.resolveFuncRef(name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unknown constructor function '%s'", pos.Line, pos.Col, name)
	}
	gb := Value{Ref: strconv.FormatInt(nbUndefined, 10), Ty: TypeAny}
	if key, ok := namespaceValueExport("globalThis"); ok && e.libExports[key] != "" {
		g, err := e.emitExpr(ast.NewIdentifier("globalThis", pos))
		if err != nil {
			return Value{}, err
		}
		if gb, err = e.emitBoxValue(g); err != nil {
			return Value{}, err
		}
	}
	return e.emitProtoCtorInvoke(name, llvmName, sig, gb, args, pos)
}

// emitProtoCtorInstanceOf is `left instanceof F` for a constructor function
// F: whether left is an object whose prototype chain holds F.prototype.
func (e *Emitter) emitProtoCtorInstanceOf(left ast.Expression, ctorName string) (Value, error) {
	l, err := e.emitExpr(left)
	if err != nil {
		return Value{}, err
	}
	boxed, err := e.emitBoxValue(l)
	if err != nil {
		return Value{}, err
	}
	getter := e.ensureProtoBag(ctorName)
	tag, payload := e.emitUnboxTagPayload(boxed)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", out))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", out))
	doneL := e.freshLabel("protoinst.done")
	objL, noL := e.emitTagCheck(tag, kmlTagDynObject, "protoinst.obj")
	e.emitLabel(objL)
	obj, bag, has, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", obj, payload))
	e.emitInstr(fmt.Sprintf("%s = call ptr %s()", bag, getter))
	e.declareFn("__kml_dynobj_chain_has", "declare i64 @__kml_dynobj_chain_has(ptr, ptr)")
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_chain_has(ptr %s, ptr %s)", has, obj, bag))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, has))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, out))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(noL)
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", res, out))
	return Value{Ref: res, Ty: TypeBool}, nil
}

// seedProtoCtorConstructors gives each constructor function's prototype
// its non-enumerable `constructor`, the function itself, at program start.
func (e *Emitter) seedProtoCtorConstructors() error {
	if len(e.jsProtoCtor) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.jsProtoCtor))
	for n := range e.jsProtoCtor {
		names = append(names, n)
	}
	sort.Strings(names)
	e.declareFn("__kml_dynobj_set_hidden", "declare void @__kml_dynobj_set_hidden(ptr, ptr, i64)")
	for _, n := range names {
		fv, err := e.emitExpr(ast.NewIdentifier(n, ast.Pos{}))
		if err != nil {
			return err
		}
		fb, err := e.emitBoxValue(fv)
		if err != nil {
			return err
		}
		bag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr %s()", bag, e.ensureProtoBag(n)))
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set_hidden(ptr %s, ptr %s, i64 %s)", bag, e.internString("constructor"), fb.Ref))
	}
	return nil
}

// protoCtorCallee is the function name a `new` expression constructs: its
// class name, or a name under casts (`new (F as any)(…)`); "" otherwise.
func protoCtorCallee(nx *ast.NewExpression) string {
	if nx.ClassName != "" || nx.Callee == nil {
		return nx.ClassName
	}
	c := nx.Callee
	for {
		switch x := c.(type) {
		case *ast.AsExpression:
			c = x.Expr
		case *ast.NonNullExpression:
			c = x.Arg
		case *ast.Identifier:
			return x.Name
		default:
			return ""
		}
	}
}

// protoCtorArgSig is sig without a declared `this` parameter: the
// arguments a `new` or chain call passes after the receiver.
func (e *Emitter) protoCtorArgSig(name string, sig FuncSig) FuncSig {
	if e.protoCtorOwnThis[name] && len(sig.ParamTypes) > 0 {
		sig.ParamTypes = sig.ParamTypes[1:]
	}
	return sig
}
