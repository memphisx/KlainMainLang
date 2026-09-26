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
	if fe.IsGenerator || fe.IsAsync {
		return Value{}, fmt.Errorf("%d:%d: an async/generator function is not supported as a dynamic (prototype-method) function yet", pos.Line, pos.Col)
	}
	name := fe.Name
	if name == "" {
		name = e.fnLitName(fe)
	}
	return e.emitDynCallable(fe.Name, name, fe.Params, fe.Body.Body, true, false, pos)
}

// emitDynArrowFunction compiles an arrow function under the dynamic ABI —
// the arrow-shaped values untyped JS passes into dynamic positions
// (`{ callback: () => ... }`). Lexical-`this` semantics: the receiver word
// the ABI passes is ignored, and a body using `this` captures the enclosing
// one, as a typed arrow does (ADR-00460).
func (e *Emitter) emitDynArrowFunction(af *ast.ArrowFunction, pos ast.Pos) (Value, error) {
	if af.IsAsync {
		return Value{}, fmt.Errorf("%d:%d: an async arrow is not supported as a dynamic function yet", pos.Line, pos.Col)
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
			if !ok || r.IsArray || r.IsError {
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
	// Free-variable scan, mirroring emitFunctionExpression: anything that
	// resolves to an enclosing local would need an env capture — out of the
	// Stage-4 V1 scope, rejected cleanly.
	refs := make(map[string]bool)
	bound := map[string]bool{"this": true}
	addParamBoundNames(bound, params)
	scanStmtsFV(bodyStmts, bound, refs)
	// Enclosing locals referenced by the body are captured into the tag-12
	// record's env (by shared heap cell, exactly like an arrow/closure), read
	// back in the body below. Sorted for a deterministic env layout.
	var capNames []string
	for name := range refs {
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

	fnName := fmt.Sprintf("@__kml_dynfn_%d", e.dynFnCtr)
	e.dynFnCtr++
	e.registerFnMeta(fnName, displayName, fnLengthFromParams(params), fnKindPlain)

	// Save emitter state — the same discipline emitFunctionExpression uses.
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeAny
	e.pushScope()

	// Bind `this` (a boxed slot — emitThisExpression's IsDynamic arm);
	// an arrow keeps lexical `this` and skips the binding.
	if bindThis {
		thisPtr := "%v_this"
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", thisPtr))
		e.emitInstr(fmt.Sprintf("store i64 %%p_this, ptr %s, align 8", thisPtr))
		e.define("this", Symbol{Ptr: thisPtr, Ty: TypeAny})
	}

	// Bind each parameter from argv: argv[i] when provided, undefined when
	// the caller passed fewer arguments (JS's missing-argument rule).
	for i, p := range params {
		if p.ArrayPattern != nil || p.ObjectPattern != nil {
			e.restoreDynFnState(savedAllocas, savedBody, savedRegCtr, savedLabelCtr, savedScopes, savedRetType, savedBlockDone)
			return Value{}, fmt.Errorf("%d:%d: destructured parameters are not supported on a dynamic function yet", pos.Line, pos.Col)
		}
		// A rest parameter collects the remaining argv words into a dynamic
		// array (tag 11) — `(...args) => args.length` works.
		if p.Rest {
			e.ensureDynArr()
			restArr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 0)", restArr))
			jPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", jPtr))
			e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", i, jPtr))
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
			e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", restArr, w))
			jn := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", jn, j))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", jn, jPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(doneL)
			restBox := e.emitNbTagPtr(restArr, kmlTagDynArray)
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", ptrName))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", restBox, ptrName))
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: TypeAny})
			continue
		}
		boxSlot := "%v_" + p.Name + "_box"
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
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		}
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", box, boxSlot))
		if pty.IsArray && !pty.IsTuple {
			// An array (a Buffer, `T[]`): the boxed array itself — the slot
			// holds its live header, so the parameter aliases the argument.
			uv := e.emitUnboxBoxToType(box, pty)
			hdr := uv.ArrayHeader
			if hdr == "" {
				hdr = e.boxArrayValue(uv)
			}
			arrSlot := "%v_" + p.Name + "_ptr"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", arrSlot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, arrSlot))
			e.define(p.Name, Symbol{Ptr: arrSlot, Ty: pty})
		} else if pty.IsDynamic || pty.IR == "" || pty.IsArray || isNullableScalar(pty) {
			e.define(p.Name, Symbol{Ptr: boxSlot, Ty: TypeAny})
		} else {
			uv := e.emitUnboxBoxToType(box, pty)
			typedSlot := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", typedSlot, pty.IR, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", pty.IR, uv.Ref, typedSlot, pty.Align()))
			e.define(p.Name, Symbol{Ptr: typedSlot, Ty: pty})
		}
	}

	// Bind each captured variable to its shared heap cell, read from %env.
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

	e.emitSafepoint()
	for _, stmt := range bodyStmts {
		if err := e.emitStmt(stmt); err != nil {
			e.restoreDynFnState(savedAllocas, savedBody, savedRegCtr, savedLabelCtr, savedScopes, savedRetType, savedBlockDone)
			return Value{}, err
		}
	}
	// Fall-off end returns undefined, matching JS.
	e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))

	e.functions.WriteString(fmt.Sprintf("\ndefine i64 %s(ptr %%env, i64 %%p_this, i64 %%p_argc, ptr %%p_argv) {\nentry:\n", fnName))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.restoreDynFnState(savedAllocas, savedBody, savedRegCtr, savedLabelCtr, savedScopes, savedRetType, savedBlockDone)

	// The tag-12 record: { fnptr, env, i64 arity }. env carries the captured
	// variables' shared heap cells (or null when nothing is captured).
	e.ensureMalloc()
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnName, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	if len(caps) > 0 {
		envIR := envStructIR(caps)
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, envStructSize(caps)))
		for i, cap := range caps {
			cellPtr := cap.Sym.Ptr
			if !cap.Sym.Boxed {
				cellPtr = e.promoteCaptureToCell(cap.Name, cap.Ty, cap.Sym.Ptr, cap.Sym.IsConst)
			}
			slotReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slotReg, envIR, env, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cellPtr, slotReg))
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, envSlot))
	} else {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", envSlot))
	}
	aritySlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", aritySlot, rec))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", len(params), aritySlot))

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
	for i, pty := range v.Ty.FuncParams {
		isRestSlot := v.Ty.FuncHasRest && i == len(v.Ty.FuncParams)-1
		hasDefault := i < len(v.Ty.FuncParamDefaults) && v.Ty.FuncParamDefaults[i] != nil
		if pty.IsArray && !isRestSlot && hasDefault {
			return Value{}, fmt.Errorf("a closure with a defaulted array parameter cannot be boxed into a dynamic value yet")
		}
	}
	retTy := TypeVoid
	if v.Ty.FuncRetType != nil {
		retTy = *v.Ty.FuncRetType
	}

	fnName := fmt.Sprintf("@__kml_dynadapt_%d", e.dynFnCtr)
	e.dynFnCtr++
	// The record's env is the adapted closure header: name/length/kind are
	// that function's (TDD-00229).
	e.registerFnMeta(fnName, "", 0, fnFlagThroughEnv)

	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeAny
	e.pushScope()

	// env IS the closure header: {fnptr, closureEnv}.
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", fp))
	cenvSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 8", cenvSlot))
	cenv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cenv, cenvSlot))

	argParts := []string{"ptr " + cenv}
	callFailed := false
	var presentBits []string
	for i, pty := range v.Ty.FuncParams {
		// The rest slot gathers surplus argv words, each unboxed to the
		// element type, into the callee's (ptr, len) typed-array pair.
		if v.Ty.FuncHasRest && i == len(v.Ty.FuncParams)-1 {
			elemTy := TypeI64
			if pty.ElemType != nil {
				elemTy = *pty.ElemType
			}
			nRest := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %%p_argc, %d", nRest, i))
			neg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, nRest))
			n := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, neg, nRest))
			bytes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, n, elemTy.Align()))
			e.ensureMalloc()
			data := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, bytes))
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
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", srcIdx, j, i))
			slot := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %s", slot, srcIdx))
			w := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, slot))
			ev := Value{Ref: w, Ty: elemTy} // an any element keeps its box
			if !elemTy.IsDynamic {
				ev = e.emitUnboxBoxToType(w, elemTy)
			}
			dst := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dst, elemTy.IR, data, j))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, ev.Ref, dst, elemTy.Align()))
			jn := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", jn, j))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", jn, jPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(doneL)
			// The callee ABI takes a {data,len} header pointer + len.
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", data, hdr))
			lenSlot := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", lenSlot, hdr))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, lenSlot))
			argParts = append(argParts, "ptr "+hdr, "i64 "+n)
			continue
		}
		word := e.freshReg()
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%p_argc, %d", have, i))
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %d", slot, i))
		loaded := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loaded, slot))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, loaded, nbUndefined))
		// JS fills a default whenever the argument is `undefined` — omitted or
		// passed explicitly (TDD-00229): a call-site default is evaluated here,
		// with the earlier parameters in scope (`(a, b = a) => …`); a
		// body-filled default is signalled through the presence mask below.
		isUndef := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, word, nbUndefined))
		presentBits = append(presentBits, isUndef)
		var argVal Value
		if i < len(v.Ty.FuncParamDefaults) && v.Ty.FuncParamDefaults[i] != nil {
			valSlot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", valSlot, pty.IR))
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
			dv = e.coerce(dv, pty)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", pty.IR, dv.Ref, valSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(argL)
			av := e.emitUnboxBoxToType(word, pty)
			if pty.IsDynamic {
				av = Value{Ref: word, Ty: pty}
			}
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", pty.IR, av.Ref, valSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, pty.IR, valSlot))
			argVal = Value{Ref: r, Ty: pty}
		} else if pty.IsDynamic {
			argVal = Value{Ref: word, Ty: pty}
		} else if pty.IsArray {
			// An array parameter's ABI is its header pointer and length
			// (TDD-00127), read out of the unboxed aggregate.
			header, n := e.arrayArgFromAggregate(e.emitUnboxBoxToType(word, pty))
			argParts = append(argParts, "ptr "+header, "i64 "+n)
			continue
		} else {
			argVal = e.unboxArgToParam(word, pty)
		}
		// Later defaults may reference this parameter by name.
		if i < len(v.Ty.FuncParamNames) && v.Ty.FuncParamNames[i] != "" && pty.IR != "" && pty.IR != "void" {
			ps := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", ps, storageIR(pty)))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(pty), argVal.Ref, ps))
			e.define(v.Ty.FuncParamNames[i], Symbol{Ptr: ps, Ty: pty})
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", storageIR(pty), argVal.Ref))
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
	}
	if retTy.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(%s)", fp, joinArgs(argParts)))
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", r, retTy.LLVMRetType(), fp, joinArgs(argParts)))
		callResult := Value{Ref: r, Ty: retTy}
		if retTy.IsArray {
			// Array return ABI is a header pointer (TDD-00213 Stage 3): deref
			// before boxing so emitBoxValue sees the {ptr,i64} aggregate.
			callResult = e.arrayValueFromHeaderReg(r, retTy)
		}
		b, err := e.emitBoxValue(callResult)
		if err != nil {
			callFailed = true
		} else {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", b.Ref))
		}
	}

	e.functions.WriteString(fmt.Sprintf("\ndefine i64 %s(ptr %%env, i64 %%p_this, i64 %%p_argc, ptr %%p_argv) {\nentry:\n", fnName))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.restoreDynFnState(savedAllocas, savedBody, savedRegCtr, savedLabelCtr, savedScopes, savedRetType, savedBlockDone)
	if callFailed {
		return Value{}, fmt.Errorf("a closure with this return type cannot be boxed into a dynamic value yet")
	}

	e.ensureMalloc()
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnName, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, envSlot))
	aritySlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", aritySlot, rec))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", len(v.Ty.FuncParams), aritySlot))
	return Value{Ref: e.emitNbTagPtr(rec, kmlTagDynFunc), Ty: TypeAny}, nil
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

// restoreDynFnState puts the emitter back the way emitDynFunctionExpression
// found it (shared by the success and every error path).
func (e *Emitter) restoreDynFnState(allocas, body strings.Builder, regCtr, labelCtr int, scopes []scope, retType Type, blockDone bool) {
	e.allocas = allocas
	e.body = body
	e.regCtr = regCtr
	e.labelCtr = labelCtr
	e.scopes = scopes
	e.currentRetType = retType
	e.blockDone = blockDone
}

// emitDynAnyMethodCall dispatches `obj.m(args)` on a bare any/unknown
// receiver: a Stage-3 chain-walking property read, then an indirect call
// through the tag-12 dynamic-function record with the receiver as `this` and
// every argument boxed. A non-function property is the JS TypeError.
func (e *Emitter) emitDynAnyMethodCall(objVal Value, propName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if propName == "apply" || propName == "call" {
		return e.emitDynApplyOrCall(objVal, propName, args, pos)
	}
	return e.emitDynAnyMethodCallPlain(objVal, propName, args, pos)
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
	r, err := e.emitDynFnBoxCallN(objVal, thisVal, argv, n, propName+" is not a function", pos)
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
	return e.emitDynFnBoxCallN(fnBox, objVal, argv, n, propName+" is not a function", pos)
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
			if !v.Ty.IsArray || v.Ty.ElemType == nil || !v.Ty.ElemType.IsDynamic {
				return "", "", fmt.Errorf("%d:%d: the spread arguments of a dynamic call must be an array", pos.Line, pos.Col)
			}
			data, length := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", data, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", length, v.Ref))
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

// emitDynCallValue calls a boxed function value (a tag-12 dynamic function)
// directly, with an `undefined` receiver — the general "call the result of an
// expression that produced a function value" path (a factory-returned decorator
// `@tag(...)`, `getHandler()(x)`, a function element read out of an `any`).
// Boxes the arguments and dispatches through the tag-12 ABI.
func (e *Emitter) emitDynCallValue(fnBox Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	argv, n, err := e.emitDynArgv(args, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFnBoxCallN(fnBox, Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, argv, n, "value is not a function", pos)
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
func (e *Emitter) emitAnyToClosure(v Value, target Type) (Value, bool) {
	if target.FuncHasRest || target.FuncHasDefaultMask {
		return Value{}, false
	}
	e.ensureMalloc()
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", env))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, env))
	thunk := e.emitDynToStaticThunk(target)
	hdr := e.buildBuiltinClosure(thunk, env)
	return Value{Ref: hdr, Ty: target}, true
}

// emitDynToStaticThunk emits emitAnyToClosure's thunk for target.
func (e *Emitter) emitDynToStaticThunk(target Type) string {
	e.dynToStaticCtr++
	name := fmt.Sprintf("@__kml_dyn2static_%d", e.dynToStaticCtr)
	restore := e.beginThunkEmit()
	params := []string{"ptr %env"}
	for i, p := range target.FuncParams {
		if p.IsArray {
			// A closure's array parameter: its header and length.
			params = append(params, fmt.Sprintf("ptr %%a%d, i64 %%a%d_len", i, i))
			continue
		}
		params = append(params, fmt.Sprintf("%s %%a%d", storageIR(p), i))
	}
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%env, align 8", box))
	n := len(target.FuncParams)
	argv := e.freshReg()
	if n > 0 {
		e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", argv, n))
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argv))
	}
	for i, p := range target.FuncParams {
		arg := Value{Ref: fmt.Sprintf("%%a%d", i), Ty: p}
		if p.IsArray {
			// Boxed from its live header: the callee aliases the same array.
			arg = e.arrayValueFromHeaderReg(arg.Ref, p)
		}
		bv, err := e.emitBoxValue(arg)
		if err != nil {
			bv = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, argv, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bv.Ref, gep))
	}
	r := e.emitDynFnBoxCallUnchecked(box, fmt.Sprintf("%d", nbUndefined), argv, n)
	ret := TypeVoid
	if target.FuncRetType != nil {
		ret = *target.FuncRetType
	}
	retIR := "void"
	if ret.IR != "" && ret.IR != "void" {
		rv := e.coerce(Value{Ref: r, Ty: TypeAny}, ret)
		e.emitInstr(fmt.Sprintf("ret %s %s", ret.LLVMRetType(), rv.Ref))
		retIR = ret.LLVMRetType()
	} else {
		e.emitInstr("ret void")
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n%s}\n", retIR, name, strings.Join(params, ", "), body))
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
	if !isForOfStringTy(pty) || pty.IsStrLiteral || pty.IsClass || pty.IsDynamic || isNullableScalar(pty) {
		return Value{}, false
	}
	return e.emitAnyIntoString(Value{Ref: word, Ty: TypeAny}, pty)
}
