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
	"strings"

	"KlainMainLang/ast"
)

// emitDynFunctionExpression compiles fe under the dynamic ABI and returns
// the boxed (tag kmlTagDynFunc) record. V1 scope: no captures of enclosing
// locals (module globals and named functions are referenced directly and
// need no capture), no async/generator forms.
func (e *Emitter) emitDynFunctionExpression(fe *ast.FunctionExpression, pos ast.Pos) (Value, error) {
	if fe.IsGenerator || fe.IsAsync {
		return Value{}, fmt.Errorf("%d:%d: an async/generator function is not supported as a dynamic (prototype-method) function yet", pos.Line, pos.Col)
	}
	return e.emitDynCallable(fe.Name, fe.Params, fe.Body.Body, true, pos)
}

// emitDynArrowFunction compiles an arrow function under the dynamic ABI —
// the arrow-shaped values untyped JS passes into dynamic positions
// (`{ callback: () => ... }`). Lexical-`this` semantics: the receiver word
// the ABI passes is ignored and `this` is NOT bound (an arrow body reading
// `this` in a dynamic position is a clean rejection in V1).
func (e *Emitter) emitDynArrowFunction(af *ast.ArrowFunction, pos ast.Pos) (Value, error) {
	if af.IsAsync {
		return Value{}, fmt.Errorf("%d:%d: an async arrow is not supported as a dynamic function yet", pos.Line, pos.Col)
	}
	body := af.Block
	if body == nil {
		// Expression body: `=> expr` is `=> { return expr }`.
		body = ast.NewBlockStatement([]ast.Statement{ast.NewReturnStatement(af.Body, pos)}, pos)
	}
	return e.emitDynCallable("", af.Params, body.Body, false, pos)
}

// emitDynCallable is the shared dynamic-ABI compilation behind function
// expressions (bindThis=true — `this` is the boxed receiver) and arrows
// (bindThis=false — lexical `this`, unbound here).
func (e *Emitter) emitDynCallable(selfName string, params []ast.Param, bodyStmts []ast.Statement, bindThis bool, pos ast.Pos) (Value, error) {
	// Free-variable scan, mirroring emitFunctionExpression: anything that
	// resolves to an enclosing local would need an env capture — out of the
	// Stage-4 V1 scope, rejected cleanly.
	refs := make(map[string]bool)
	bound := map[string]bool{"this": true}
	addParamBoundNames(bound, params)
	scanStmtsFV(bodyStmts, bound, refs)
	for name := range refs {
		if name == selfName {
			continue
		}
		if _, isGlobal := e.moduleGlobals[name]; isGlobal {
			continue
		}
		if _, found := e.lookup(name); found {
			return Value{}, fmt.Errorf("%d:%d: capturing variable '%s' in a dynamic (prototype-method) function is not supported yet", pos.Line, pos.Col, name)
		}
	}

	fnName := fmt.Sprintf("@__kml_dynfn_%d", e.dynFnCtr)
	e.dynFnCtr++

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
		ptrName := "%v_" + p.Name
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", ptrName))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, ptrName))
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
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, ptrName))
		e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
		e.emitLabel(nextL)
		e.define(p.Name, Symbol{Ptr: ptrName, Ty: TypeAny})
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

	// The tag-12 record: { fnptr, env (null in V1), i64 arity }.
	e.ensureMalloc()
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnName, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", envSlot))
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
		if (pty.IsArray && !isRestSlot) || isNullableScalar(pty) {
			return Value{}, fmt.Errorf("a closure with an array/nullable parameter cannot be boxed into a dynamic value yet")
		}
	}
	retTy := TypeVoid
	if v.Ty.FuncRetType != nil {
		retTy = *v.Ty.FuncRetType
	}

	fnName := fmt.Sprintf("@__kml_dynadapt_%d", e.dynFnCtr)
	e.dynFnCtr++

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
			ev := e.emitUnboxBoxToType(w, elemTy)
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
		if pty.IsDynamic {
			argParts = append(argParts, "i64 "+word)
			continue
		}
		uv := e.emitUnboxBoxToType(word, pty)
		argParts = append(argParts, fmt.Sprintf("%s %s", pty.IR, uv.Ref))
	}
	if retTy.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(%s)", fp, joinArgs(argParts)))
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", r, retTy.LLVMRetType(), fp, joinArgs(argParts)))
		b, err := e.emitBoxValue(Value{Ref: r, Ty: retTy})
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
	fnBox, err := e.emitDynAnyMemberGetNamed(objVal, e.internString(propName), propName, pos)
	if err != nil {
		return Value{}, err
	}
	// Box the arguments into a stack argv array before the tag dispatch.
	n := len(args)
	argv := e.freshReg()
	if n > 0 {
		e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", argv, n))
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argv)) // never read
	}
	for i, a := range args {
		av, err := e.emitExprWithObjectHint(a, TypeAny)
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(av)
		if err != nil {
			return Value{}, err
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", slot, argv, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, slot))
	}

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
	recvBox, err := e.emitBoxValue(objVal)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, i64 %s, i64 %d, ptr %s)", r, fp, env, recvBox.Ref, n, argv))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	e.emitThrowTypeError(fmt.Sprintf("%s is not a function", propName))
	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypeAny}, nil
}
