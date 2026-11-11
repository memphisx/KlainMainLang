package llvm

import (
	"fmt"
	"strings"

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
}{{"toString", 0}, {"call", 1}, {"apply", 2}, {"bind", 1}, {"@@hasInstance", 1}}

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
		sym += "_" + strings.Replace(name, "@@", "sym_", 1) // `@@hasInstance`: a symbol-keyed one
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
	kind := 0
	if name == "" {
		kind = fnFlagObjectProto
	}
	e.registerFnMeta("@"+sym, name, length, kind)
	e.emitStaticFnRecord(rec, "internal", "{ "+irPtr+", "+irPtr+", i64 }", fmt.Sprintf("{ "+irPtr+" @%s, "+irPtr+" null, i64 %d }", sym, length), "@"+sym)
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
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", undef, slot))
		has := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, %d", has, i))
		yesL, doneL := e.freshLabel("fnproto.arg"), e.freshLabel("fnproto.argdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, yesL, doneL))
		e.emitLabel(yesL)
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %%argv, i64 %d", p, i))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", v, p))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", v, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypeAny}
	}
	// rest is the arguments from i on, as an argv and its count.
	rest := func(i int) (string, string) {
		n, pos, cnt, p := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %%argc, %d", n, i))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", pos, n))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", cnt, pos, n))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %%argv, i64 %d", p, i))
		return p, cnt
	}
	// requireFn throws unless the receiver is a function.
	requireFn := func(msg string) {
		tag, _ := e.emitUnboxTagPayload(this)
		isFn, isRef, ok := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRef, tag, kmlTagFuncRef))
		fnOrRef := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", fnOrRef, isFn, isRef))
		pxFn, isPx := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_proxy_is_fn(i64 %s)", pxFn, this.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isPx, pxFn))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, fnOrRef, isPx)) // a Proxy of a function too
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
				e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", r.Ref, slot))
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(someL)
			spread := []ast.Expression{ast.NewSpreadElement(ast.NewIdentifier("__kml_fnproto_list", ast.Pos{}), ast.Pos{})}
			if argv, n, err := e.emitDynArgv(spread, ast.Pos{}); err == nil {
				if r, err := e.emitDynFnBoxCallN(this, thisArg, argv, n, "Function.prototype.apply was called on a non-function", ast.Pos{}); err == nil {
					e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", r.Ref, slot))
				}
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, slot))
			result = r
		},
		"bind": func() {
			requireFn("Bind must be called on a function")
			result = e.emitFnProtoBind(this, argAt(0)).Ref
		},
		"@@hasInstance": func() {
			// OrdinaryHasInstance(this, value): instanceof without the
			// receiver's own @@hasInstance.
			e.ensureNanBox()
			is := e.emitOrdinaryHasInstance(argAt(0).Ref, "%this")
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %d", r, is, nbTrue, nbFalse))
			result = r
		},
	}
	if m := methods[name]; m != nil {
		m()
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", result))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 @%s("+irPtr+" %%env, i64 %%this, i64 %%argc, "+irPtr+" %%argv)", sym), body)
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
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %%argv, i64 1", p))
		argv, n = p, c
	}
	words, bytes := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 3", words, n))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, words))
	env := e.freshReg()
	e.heapMallocInto(env, allocClosure, bytes)
	e.heapStore("i64", target.Ref, env, 8)
	slot := func(i int) string {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %d", p, env, i))
		return p
	}
	e.heapStore("i64", thisArg.Ref, slot(1), 8)
	e.heapStore("i64", n, slot(2), 8)
	nb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", nb, n))
	e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", slot(3), argv, nb))
	// Its length: the target's, less the bound arguments (never negative);
	// a constructor reference's is 0 here.
	tag, payload := e.emitUnboxTagPayload(target)
	isFn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
	lenSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", lenSlot))
	fnL, doneL := e.freshLabel("fnproto.tlen"), e.freshLabel("fnproto.tlendone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, fnL, doneL))
	e.emitLabel(fnL)
	trec := e.emitIntToPtr(payload)
	tl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 16", tl, trec))
	tv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", tv, tl))
	left, neg, ar := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", left, tv, n))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, left))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", ar, neg, left))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", ar, lenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	arity := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", arity, lenSlot))
	rec := e.freshReg()
	e.emitClosureAlloc(rec, "24", "@__kml_dyn_bound")
	e.heapStore(irPtr, "@__kml_dyn_bound", rec, 8)
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 8", envSlot, rec))
	e.heapStore(irPtr, env, envSlot, 8)
	arSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 16", arSlot, rec))
	e.heapStore("i64", arity, arSlot, 8)
	return Value{Ref: e.emitNbTagPtr(rec, kmlTagDynFunc), Ty: TypeAny}
}

// spillAny stores box v in a fresh slot of its own, returning the slot.
func (e *Emitter) spillAny(v Value) string {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", v.Ref, slot))
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
		fnSub := e.prog != nil && e.prog.FunctionSubclasses[c]
		if base == "" && !fnSub {
			continue
		}
		if _, ok := e.classes[base]; !ok && !fnSub {
			continue
		}
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %%pay, %s", is, e.classRefPtr(c)))
		hitL, nextL := e.freshLabel("crproto.hit"), e.freshLabel("crproto.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
		e.emitLabel(hitL)
		if fnSub {
			// A class extending Function (lowered by the resolver): Function.
			fv, err := e.emitExpr(e.builtinIdent("Function", ast.Pos{}))
			if err == nil && !fv.Ty.IsDynamic() {
				fv, err = e.emitBoxValue(fv)
			}
			if err == nil {
				e.emitTerminator(fmt.Sprintf("ret i64 %s", fv.Ref))
				e.emitLabel(nextL)
				continue
			}
		}
		if fn := fnBaseOf(base); fn != "" {
			// A class extending a function or a value: that base itself.
			var fv Value
			var err error
			if e.valueBases[fn] {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, e.ensureHeritageValue(fn)))
				fv = Value{Ref: r, Ty: TypeAny}
			} else if fv, err = e.emitExpr(ast.NewIdentifier(fn, ast.Pos{})); err == nil && !fv.Ty.IsDynamic() {
				fv, err = e.emitBoxValue(fv)
			}
			if err == nil {
				e.emitTerminator(fmt.Sprintf("ret i64 %s", fv.Ref))
				e.emitLabel(nextL)
				continue
			}
		}
		if base == objectBaseClass {
			// A class extending Object: Object itself.
			ov, err := e.emitExpr(e.builtinIdent("Object", ast.Pos{}))
			if err == nil && !ov.Ty.IsDynamic() {
				ov, err = e.emitBoxValue(ov)
			}
			if err == nil {
				e.emitTerminator(fmt.Sprintf("ret i64 %s", ov.Ref))
				e.emitLabel(nextL)
				continue
			}
		}
		e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(e.classRefPtr(base), kmlTagFuncRef)))
		e.emitLabel(nextL)
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitFnProtoBox("").Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i64 @__kml_classref_proto("+irPtr+" %pay)", body)
}

// emitClassRefHas defines @__kml_classref_has(pay, key, own): whether the
// class whose reference payload it takes has key as its own property
// (length, name, prototype, its static members), or with own false also
// an inherited static one (Function.prototype's members are the caller's).
// A reference to no class of the program (a builtin constructor) has
// length, name and prototype.
func (e *Emitter) emitClassRefHas() {
	if e.emittedClassRefHas {
		return
	}
	e.emittedClassRefHas = true
	restore := e.beginDetachedFunc()
	hit := func(keys []string, label string) {
		for _, k := range keys {
			c, eq := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp("+irPtr+" %%key, "+irPtr+" %s)", c, e.internString(k)))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, c))
			yes, no := e.freshLabel(label+".yes"), e.freshLabel(label+".no")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eq, yes, no))
			e.emitLabel(yes)
			e.emitTerminator("ret i1 true")
			e.emitLabel(no)
		}
	}
	// A property it was given at run time (hostbox.c's own bag).
	e.declareFn("__kml_classref_extra_has", "declare i1 @__kml_classref_extra_has("+irPtr+", "+irPtr+")")
	xh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_classref_extra_has("+irPtr+" %%pay, "+irPtr+" %%key)", xh))
	xyes, xno := e.freshLabel("crhas.extra"), e.freshLabel("crhas.static")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", xh, xyes, xno))
	e.emitLabel(xyes)
	e.emitTerminator("ret i1 true")
	e.emitLabel(xno)
	for _, c := range e.classRefClasses(true) {
		if _, ok := e.classes[c]; !ok {
			continue
		}
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %%pay, %s", is, e.classRefPtr(c)))
		hitL, nextL := e.freshLabel("crhas.class"), e.freshLabel("crhas.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
		e.emitLabel(hitL)
		hit(e.classOwnStaticKeys(c, false), "crhas.own")
		inhL := e.freshLabel("crhas.inherited")
		e.emitTerminator(fmt.Sprintf("br i1 %%own, label %%%s, label %%%s", "crhas.miss", inhL))
		e.emitLabel(inhL)
		for b := e.classes[c].BaseClass; b != ""; b = e.classes[b].BaseClass {
			if _, ok := e.classes[b]; !ok {
				break
			}
			hit(e.classOwnStaticKeys(b, false), "crhas.base")
		}
		e.emitTerminator("br label %crhas.miss")
		e.emitLabel(nextL)
	}
	hit([]string{"length", "name", "prototype"}, "crhas.builtin")
	e.emitTerminator("br label %crhas.miss")
	e.emitLabel("crhas.miss")
	e.emitTerminator("ret i1 false")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i1 @__kml_classref_has("+irPtr+" %pay, "+irPtr+" %key, i1 %own)", body)
}

// emitClassRefBag defines @__kml_classref_bag(pay): a fresh bag of the own
// properties of the class whose reference payload it takes, for reflection
// over a class as an object (getOwnPropertyNames, a descriptor): length and
// name (configurable), prototype (fixed), its static methods (writable,
// configurable) in declaration order, then its static fields (writable,
// enumerable, configurable) with their values now. Null for a reference to
// no class of the program.
func (e *Emitter) emitClassRefBag() {
	if e.emittedClassRefBag {
		return
	}
	e.emittedClassRefBag = true
	e.ensureNanBox()
	e.declareFn("__kml_dynobj_new", "declare "+irPtr+" @__kml_dynobj_new()")
	e.declareFn("__kml_dynobj_set_attrs", "declare void @__kml_dynobj_set_attrs("+irPtr+", "+irPtr+", i64, i64)")
	e.declareFn("__kml_classref_bag_merge", "declare void @__kml_classref_bag_merge("+irPtr+", "+irPtr+")")
	restore := e.beginDetachedFunc()
	const writable, enum, config = 1, 2, 4
	for _, c := range e.classRefClasses(true) {
		info, ok := e.classes[c]
		if !ok {
			continue
		}
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %%pay, %s", is, e.classRefPtr(c)))
		hitL, nextL := e.freshLabel("crbag.class"), e.freshLabel("crbag.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
		e.emitLabel(hitL)
		bag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_dynobj_new()", bag))
		set := func(k string, v Value, attrs int) {
			if e.blockDone {
				return
			}
			if !v.Ty.IsDynamic() {
				b, err := e.emitBoxValue(v)
				if err != nil {
					return
				}
				v = b
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set_attrs("+irPtr+" %s, "+irPtr+" %s, i64 %s, i64 %d)", bag, e.internString(k), v.Ref, attrs))
		}
		read := func(k string) (Value, bool) {
			v, err := e.emitStaticFieldRead(info, c, k, ast.Pos{})
			return v, err == nil && !e.blockDone
		}
		for _, k := range []string{"length", "name"} {
			if v, ok := read(k); ok {
				set(k, v, config)
			}
		}
		set("prototype", e.emitClassProto(c), 0)
		if cd := e.programClassDecl(c); cd != nil {
			for _, m := range cd.Methods {
				if m.IsStatic && m.Key == nil && (m.AccessorKind == "get" || m.AccessorKind == "set") && !strings.HasPrefix(m.Name, "#") && !strings.HasPrefix(m.Name, "\x01") {
					// A static accessor: a configurable, non-enumerable
					// accessor property over its function.
					mangled := accessorMethodName(m.AccessorKind, m.Name)
					if info.StaticMethodImplementor[mangled] != c {
						continue
					}
					fv, ok := read(mangled)
					if !ok {
						continue
					}
					if !fv.Ty.IsDynamic() {
						b, err := e.emitBoxValue(fv)
						if err != nil {
							continue
						}
						fv = b
					}
					pay := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, fv.Ref))
					rec := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, rec, pay))
					g, st := rec, "null"
					if m.AccessorKind == "set" {
						g, st = "null", rec
					}
					e.declareFn("__kml_dynobj_defacc_hidden", "declare void @__kml_dynobj_defacc_hidden("+irPtr+", "+irPtr+", "+irPtr+", "+irPtr+")")
					e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_defacc_hidden("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)", bag, e.internString(m.Name), g, st))
					continue
				}
				if !m.IsStatic || m.AccessorKind != "" || strings.HasPrefix(m.Name, "#") || info.StaticMethodImplementor[m.Name] != c {
					continue
				}
				v, ok := read(m.Name)
				if !ok {
					continue
				}
				if m.Key == nil {
					set(m.Name, v, writable|config)
					continue
				}
				// A symbol-keyed one (`static [k]() {}`): under the
				// symbol's own key, which its binding holds.
				sv, err := e.emitExpr(ast.NewIdentifier(m.Key.Name, ast.Pos{}))
				if err != nil || e.blockDone || !sv.Ty.IsSymbol() {
					continue
				}
				if !v.Ty.IsDynamic() {
					b, err := e.emitBoxValue(v)
					if err != nil {
						continue
					}
					v = b
				}
				e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set_attrs("+irPtr+" %s, "+irPtr+" %s, i64 %s, i64 %d)", bag, e.emitSymbolPropertyKey(sv), v.Ref, writable|config))
			}
		}
		for _, f := range info.OwnStaticFieldOrder {
			if strings.HasPrefix(f, "#") || strings.HasPrefix(f, "\x01") {
				continue
			}
			if v, ok := read(f); ok {
				set(f, v, writable|enum|config)
			}
		}
		if !e.blockDone {
			// With the properties it was given at run time.
			e.emitInstr(fmt.Sprintf("call void @__kml_classref_bag_merge("+irPtr+" %%pay, "+irPtr+" %s)", bag))
			e.emitTerminator(fmt.Sprintf("ret "+irPtr+" %s", bag))
		}
		e.emitLabel(nextL)
	}
	e.emitTerminator("ret " + irPtr + " null")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(irPtr+" @__kml_classref_bag("+irPtr+" %pay)", body)
}
