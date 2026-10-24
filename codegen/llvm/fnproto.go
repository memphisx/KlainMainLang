package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// Function.prototype (TDD-00229): a function doing nothing, whose
// toString, call, apply and bind act on their receiver. Each is a dynamic
// function record of its own, so a function value's member read
// (`f.call`, `Function.prototype.toString`) yields it unbound, and
// `.call(g, …)` gives it its receiver as any function method takes one.

// fnProtoMethods are Function.prototype's own methods, with their lengths.
var fnProtoMethods = []struct {
	name   string
	length int
}{{"toString", 0}, {"call", 1}, {"apply", 2}, {"bind", 1}}

// isFnProtoMethod reports whether name is one of Function.prototype's methods.
func isFnProtoMethod(name string) bool {
	for _, m := range fnProtoMethods {
		if m.name == name {
			return true
		}
	}
	return false
}

// fnProtoRec is the static dynamic-function record of Function.prototype's
// method name ("" for Function.prototype itself), defined on first use.
func (e *Emitter) fnProtoRec(name string) string {
	sym := "__kml_fnproto"
	if name != "" {
		sym += "_" + name
	}
	rec := "@" + sym + "_rec"
	if e.fnDecls[sym] {
		return rec
	}
	e.fnDecls[sym] = true
	length := 0
	for _, m := range fnProtoMethods {
		if m.name == name {
			length = m.length
		}
	}
	e.emitFnProtoBody(sym, name)
	e.registerFnMeta("@"+sym, name, length, 0)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr @%s, ptr null, i64 %d }", rec, sym, length))
	return rec
}

// emitFnProtoBox is the box of Function.prototype's method name ("" for
// Function.prototype itself).
func (e *Emitter) emitFnProtoBox(name string) Value {
	return Value{Ref: e.emitNbTagPtr(e.fnProtoRec(name), kmlTagDynFunc), Ty: TypeAny}
}

// emitFnProtoBody writes the dynamic-ABI body of method name.
func (e *Emitter) emitFnProtoBody(sym, name string) {
	restore := e.sharedBody(e.beginDetachedFunc())
	this := Value{Ref: "%this", Ty: TypeAny}
	undef := fmt.Sprint(nbUndefined)
	// argAt is argument i, undefined when not passed.
	argAt := func(i int) Value {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", undef, slot))
		has := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, %d", has, i))
		yesL, doneL := e.freshLabel("fnproto.arg"), e.freshLabel("fnproto.argdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, yesL, doneL))
		e.emitLabel(yesL)
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %d", p, i))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", v, p))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypeAny}
	}
	// rest is the arguments from i on, as an argv and its count.
	rest := func(i int) (string, string) {
		n, pos, cnt, p := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %%argc, %d", n, i))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", pos, n))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", cnt, pos, n))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %d", p, i))
		return p, cnt
	}
	// requireFn throws unless the receiver is a function.
	requireFn := func(msg string) {
		tag, _ := e.emitUnboxTagPayload(this)
		isFn, isRef, ok := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRef, tag, kmlTagFuncRef))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, isFn, isRef))
		okL, badL := e.freshLabel("fnproto.fn"), e.freshLabel("fnproto.notfn")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
		e.emitLabel(badL)
		e.emitThrowTypeError(msg)
		e.emitLabel(okL)
	}
	result := undef
	methods := map[string]func(){
		"toString": func() {
			requireFn("Function.prototype.toString requires that 'this' be a Function")
			if s, err := e.emitDynToStringCall(this, ast.Pos{}); err == nil {
				if b, err := e.emitBoxValue(s); err == nil {
					result = b.Ref
				}
			}
		},
		"call": func() {
			requireFn("Function.prototype.call called on a non-function")
			argv, n := rest(1)
			if r, err := e.emitDynFnBoxCallN(this, argAt(0), argv, n, "Function.prototype.call called on a non-function", ast.Pos{}); err == nil {
				result = r.Ref
			}
		},
		"apply": func() {
			requireFn("Function.prototype.apply was called on a non-function")
			thisArg := argAt(0)
			list := argAt(1)
			e.define("__kml_fnproto_list", Symbol{Ptr: e.spillAny(list), Ty: TypeAny})
			// A null or undefined argument list is no arguments.
			absent := e.emitIsNullish(list)
			noneL, someL, doneL := e.freshLabel("fnproto.noargs"), e.freshLabel("fnproto.args"), e.freshLabel("fnproto.applied")
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, noneL, someL))
			e.emitLabel(noneL)
			if r, err := e.emitDynFnBoxCallN(this, thisArg, "null", "0", "Function.prototype.apply was called on a non-function", ast.Pos{}); err == nil {
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, slot))
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(someL)
			spread := []ast.Expression{ast.NewSpreadElement(ast.NewIdentifier("__kml_fnproto_list", ast.Pos{}), ast.Pos{})}
			if argv, n, err := e.emitDynArgv(spread, ast.Pos{}); err == nil {
				if r, err := e.emitDynFnBoxCallN(this, thisArg, argv, n, "Function.prototype.apply was called on a non-function", ast.Pos{}); err == nil {
					e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r.Ref, slot))
				}
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
			result = r
		},
		"bind": func() {
			requireFn("Bind must be called on a function")
			result = e.emitFnProtoBind(this, argAt(0)).Ref
		},
	}
	if m := methods[name]; m != nil {
		m()
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", result))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 @%s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv)", sym), body)
}

// emitFnProtoBind is target bound to thisArg and the arguments after it,
// in the dynamic bound-function form (__kml_dyn_bound's env: target, this,
// n, then the bound arguments).
func (e *Emitter) emitFnProtoBind(target, thisArg Value) Value {
	e.ensureDynBound()
	argv, n := "", ""
	{
		cnt, pos, c, p := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %%argc, 1", cnt))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", pos, cnt))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", c, pos, cnt))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 1", p))
		argv, n = p, c
	}
	words, bytes := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 3", words, n))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, words))
	env := e.freshReg()
	e.heapMallocInto(env, bytes)
	e.heapStore("i64", target.Ref, env, 8)
	slot := func(i int) string {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", p, env, i))
		return p
	}
	e.heapStore("i64", thisArg.Ref, slot(1), 8)
	e.heapStore("i64", n, slot(2), 8)
	nb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", nb, n))
	e.ensureMemcpy()
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", slot(3), argv, nb))
	// Its length: the target's, less the bound arguments (never negative);
	// a constructor reference's is 0 here.
	tag, payload := e.emitUnboxTagPayload(target)
	isFn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
	lenSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lenSlot))
	fnL, doneL := e.freshLabel("fnproto.tlen"), e.freshLabel("fnproto.tlendone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, fnL, doneL))
	e.emitLabel(fnL)
	trec := e.emitIntToPtr(payload)
	tl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", tl, trec))
	tv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", tv, tl))
	left, neg, ar := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", left, tv, n))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, left))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", ar, neg, left))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ar, lenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	arity := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", arity, lenSlot))
	rec := e.freshReg()
	e.heapMallocInto(rec, "24")
	e.heapStore("ptr", "@__kml_dyn_bound", rec, 8)
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	e.heapStore("ptr", env, envSlot, 8)
	arSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", arSlot, rec))
	e.heapStore("i64", arity, arSlot, 8)
	return Value{Ref: e.emitNbTagPtr(rec, kmlTagDynFunc), Ty: TypeAny}
}

// spillAny stores box v in a fresh slot of its own, returning the slot.
func (e *Emitter) spillAny(v Value) string {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, slot))
	return slot
}

// emitIsNullish is whether box v is null or undefined.
func (e *Emitter) emitIsNullish(v Value) string {
	isU, isN, r := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, v.Ref, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isN, v.Ref, nbNull))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, isU, isN))
	return r
}

// emitClassRefProto defines @__kml_classref_proto: the prototype of the
// class whose reference payload it takes — the class it extends, else
// Function.prototype (a builtin constructor's too).
func (e *Emitter) emitClassRefProto() {
	if e.emittedClassRefProto {
		return
	}
	e.emittedClassRefProto = true
	restore := e.beginDetachedFunc()
	for _, c := range e.classRefClasses(true) {
		base := e.classes[c].BaseClass
		if base == "" {
			continue
		}
		if _, ok := e.classes[base]; !ok {
			continue
		}
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %%pay, %s", is, e.classRefPtr(c)))
		hitL, nextL := e.freshLabel("crproto.hit"), e.freshLabel("crproto.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
		e.emitLabel(hitL)
		e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(e.classRefPtr(base), kmlTagFuncRef)))
		e.emitLabel(nextL)
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitFnProtoBox("").Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i64 @__kml_classref_proto(ptr %pay)", body)
}
