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

	"KlainMainLang/ast"
)

// jsCollectProtoCtors recognizes prototype constructors: top-level function
// declarations that appear in a `new` expression (already collected into
// jsCtorParamTy) or whose `.prototype` is written at top level. Runs after
// registerFunctions; also folds the `new`-site argument types into the
// plain-function slot map so buildFunctionSig's call-site inference sees
// them — but sigs were already built, so the fold re-registers the sig.
func (e *Emitter) jsCollectProtoCtors(prog *ast.Program) {
	e.jsProtoCtor = map[string]bool{}
	fnDecls := map[string]*ast.FunctionDeclaration{}
	for _, s := range prog.Body {
		if fd, ok := s.(*ast.FunctionDeclaration); ok && !fd.IsGenerator && !fd.IsAsync && len(fd.TypeParams) == 0 {
			fnDecls[fd.Name] = fd
		}
	}
	for name := range e.jsCtorParamTy {
		if fnDecls[name] != nil {
			e.jsProtoCtor[name] = true
		}
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
	// The `new F(...)` argument types were collected into jsCtorParamTy (the
	// class-keyed map); a recognized constructor is a plain function, so its
	// sig — already built — must be rebuilt with those types folded in.
	for name := range e.jsProtoCtor {
		slots := e.jsFuncParamTy[name]
		for i, t := range e.jsCtorParamTy[name] {
			if t.IR == "" {
				continue
			}
			for len(slots) <= i {
				slots = append(slots, jsParamSlot{})
			}
			if slots[i].ty.IR == "" {
				slots[i] = jsParamSlot{ty: t}
			} else if slots[i].ty.IR != t.IR {
				slots[i].conflict = true
			}
		}
		e.jsFuncParamTy[name] = slots
		if fd := fnDecls[name]; fd != nil {
			e.funcs[name] = e.buildFunctionSig(fd)
		}
	}
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
	e.emitGlobal(fmt.Sprintf("%s = internal global ptr null, align 8", global))
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
// sig. V1 scope: exact arity, scalar/string/object/dynamic params (array and
// nullable-scalar params are rejected cleanly).
func (e *Emitter) emitProtoCtorArgs(name string, sig FuncSig, args []ast.Expression, pos ast.Pos) ([]string, error) {
	if len(args) != len(sig.ParamTypes) {
		return nil, fmt.Errorf("%d:%d: constructor function '%s' expects %d argument(s), got %d", pos.Line, pos.Col, name, len(sig.ParamTypes), len(args))
	}
	var parts []string
	for i, a := range args {
		pty := sig.ParamTypes[i]
		if pty.IsArray || isNullableScalar(pty) {
			return nil, fmt.Errorf("%d:%d: an array/nullable parameter on a prototype constructor is not supported yet", pos.Line, pos.Col)
		}
		val, err := e.emitExprWithObjectHint(a, pty)
		if err != nil {
			return nil, err
		}
		if pty.Inferred && !isSafeNumericArg(val.Ty) {
			return nil, fmt.Errorf("%d:%d: constructor parameter %d of '%s' has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", a.GetPos().Line, a.GetPos().Col, i+1, name)
		}
		if pty.IsDynamic {
			b, err := e.emitBoxValue(val)
			if err != nil {
				return nil, err
			}
			parts = append(parts, "i64 "+b.Ref)
			continue
		}
		val = e.coerce(val, pty)
		parts = append(parts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
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

	parts, err := e.emitProtoCtorArgs(ex.ClassName, sig, ex.Args, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	argStr := "i64 " + thisBox.Ref
	for _, p := range parts {
		argStr += ", " + p
	}
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argStr))
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", r, sig.RetType.LLVMRetType(), llvmName, argStr))
	}
	return thisBox, nil
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
	parts, err := e.emitProtoCtorArgs(ctorName, sig, args[1:], pos)
	if err != nil {
		return Value{}, err
	}
	argStr := "i64 " + thisBox.Ref
	for _, p := range parts {
		argStr += ", " + p
	}
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argStr))
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", r, sig.RetType.LLVMRetType(), llvmName, argStr))
	}
	return Value{Ref: "0", Ty: TypeUndefined}, nil
}
