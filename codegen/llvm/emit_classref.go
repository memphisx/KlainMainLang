package llvm

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// emit_classref.go — a class as a value, and `instanceof` against one held
// in `any`.
//
// A class name in value position (`const C: any = Foo`, `assert.throws(fn,
// Foo)`) boxes as a constructor reference, tag kmlTagFuncRef, as the
// built-in Error constructors do. Its payload is a string constant of its
// own holding the class's name: `.name` reads it, and the pointer stays
// distinct from every other class's (two modules' `Foo`s). `x instanceof C`
// with `C` a value dispatches at run time (__kml_dyn_instanceof) on that
// pointer to the static test of the class it names.

// classRefPtr is the payload of class mangled's constructor reference.
func (e *Emitter) classRefPtr(mangled string) string {
	if e.classRefs == nil {
		e.classRefs = map[string]string{}
	}
	if ref, ok := e.classRefs[mangled]; ok {
		return ref
	}
	name := fmt.Sprintf("@__kml_classref_%d", len(e.classRefs))
	esc, length := escapeLLVM(inspectClassName(mangled))
	e.emitGlobal(fmt.Sprintf("%s = private constant { i64, [%d x i8] } { i64 %d, [%d x i8] c\"%s\" }, align 8", name, length, length-1, length, esc))
	ref := fmt.Sprintf("getelementptr inbounds ({ i64, [%d x i8] }, ptr %s, i32 0, i32 1)", length, name)
	e.classRefs[mangled] = ref
	return ref
}

// emitClassRef boxes class mangled as a constructor reference.
func (e *Emitter) emitClassRef(mangled string) Value {
	return Value{Ref: e.emitNbTagPtr(e.classRefPtr(mangled), kmlTagFuncRef), Ty: TypeAny}
}

// instanceofNeedsRuntime reports an `instanceof` whose right-hand side is a
// value rather than a class name: a binding, a member, a call.
func (e *Emitter) instanceofNeedsRuntime(right ast.Expression) bool {
	id, ok := right.(*ast.Identifier)
	if !ok {
		return true
	}
	_, found := e.lookup(id.Name)
	return found
}

// emitDynInstanceOf is `left instanceof right` with right a value: the
// run-time dispatch on the constructor it holds.
func (e *Emitter) emitDynInstanceOf(ex *ast.BinaryExpression) (Value, error) {
	l, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	lb, err := e.emitBoxValue(l)
	if err != nil {
		return Value{}, err
	}
	r, err := e.emitExpr(ex.Right)
	if err != nil {
		return Value{}, err
	}
	rb, err := e.emitBoxValue(r)
	if err != nil {
		return Value{}, err
	}
	e.ensureNanBox()
	e.usedDynInstanceof = true
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dyn_instanceof(i64 %s, i64 %s)", res, lb.Ref, rb.Ref))
	return Value{Ref: res, Ty: TypeBool}, nil
}

// ensureCtorKind marks __kml_ctor_kind (lib/native.d.ts ctorKind) used.
func (e *Emitter) ensureCtorKind() {
	e.ensureNanBox()
	e.usedCtorKind = true
}

// ctorRefTargets are the constructors a reference can name: every Error
// kind, and every class boxed as a value, as (payload, name) pairs.
func (e *Emitter) ctorRefTargets() (ptrs, names []string) {
	var kinds []string
	for k := range errorKindIDs {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		ptrs = append(ptrs, e.internString(k))
		names = append(names, k)
	}
	var classes []string
	for c := range e.classRefs {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		ptrs = append(ptrs, e.classRefs[c])
		names = append(names, c)
	}
	return ptrs, names
}

// emitClassRefFinalize defines __kml_dyn_instanceof and __kml_ctor_kind when
// used.
func (e *Emitter) emitClassRefFinalize() {
	e.emitClassRefTable()
	if e.usedDynInstanceof {
		e.emitDynInstanceofDispatch()
	}
	if e.usedCtorKind {
		e.emitCtorKindDispatch()
	}
}

// emitDynInstanceofDispatch defines `i1 __kml_dyn_instanceof(i64 v, i64 c)`:
// c a constructor reference → the static `v instanceof <its class>`; any
// other c → false.
func (e *Emitter) emitDynInstanceofDispatch() {
	ptrs, names := e.ctorRefTargets()
	restore := e.beginDetachedFunc()
	vSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", vSlot))
	e.emitInstr(fmt.Sprintf("store i64 %%v, ptr %s, align 8", vSlot))
	e.define("__kml_iv", Symbol{Ptr: vSlot, Ty: TypeAny})
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %%c)", tag))
	isRef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRef, tag, kmlTagFuncRef))
	refL, noL := e.freshLabel("dyninst.ref"), e.freshLabel("dyninst.no")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRef, refL, noL))
	e.emitLabel(refL)
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %%c)", pay))
	p := e.emitIntToPtr(pay)
	for i := range ptrs {
		hit, next := e.freshLabel("dyninst.hit"), e.freshLabel("dyninst.next")
		eq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", eq, p, ptrs[i]))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eq, hit, next))
		e.emitLabel(hit)
		test := ast.NewBinaryExpression("instanceof", ast.NewIdentifier("__kml_iv", ast.Pos{}), ast.NewIdentifier(names[i], ast.Pos{}), ast.Pos{})
		r, err := e.emitInstanceOf(test)
		if err != nil || e.blockDone {
			if !e.blockDone {
				e.emitTerminator("ret i1 false")
			}
		} else {
			e.emitTerminator(fmt.Sprintf("ret i1 %s", r.Ref))
		}
		e.emitLabel(next)
	}
	e.emitTerminator("ret i1 false")
	e.emitLabel(noL)
	e.emitTerminator("ret i1 false")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine i1 @__kml_dyn_instanceof(i64 %%v, i64 %%c) {\nentry:\n%s}\n", body))
}

// emitCtorKindDispatch defines `double __kml_ctor_kind(i64 c)`: 0 for a value
// that is no constructor reference, 2 for an Error constructor (a built-in
// kind or a class extending Error), 1 for any other.
func (e *Emitter) emitCtorKindDispatch() {
	ptrs, names := e.ctorRefTargets()
	var b strings.Builder
	b.WriteString("\ndefine double @__kml_ctor_kind(i64 %c) {\nentry:\n")
	b.WriteString("  %tag = call i8 @__kml_nb_tag(i64 %c)\n  %isref = icmp eq i8 %tag, " + fmt.Sprint(kmlTagFuncRef) + "\n  br i1 %isref, label %ref, label %no\nref:\n")
	b.WriteString("  %pay = call i64 @__kml_nb_pay(i64 %c)\n  %p = inttoptr i64 %pay to ptr\n")
	for i := range ptrs {
		kind := "1.0"
		if isErrorKindName(names[i]) || e.classes[names[i]].IsErrorSubclass {
			kind = "2.0"
		}
		fmt.Fprintf(&b, "  %%e%d = icmp eq ptr %%p, %s\n  br i1 %%e%d, label %%hit%d, label %%next%d\nhit%d:\n  ret double %s\nnext%d:\n", i, ptrs[i], i, i, i, i, kind, i)
	}
	b.WriteString("  ret double 1.0\nno:\n  ret double 0.0\n}\n")
	e.functions.WriteString(b.String())
}

// emitClassRefTable defines the class-reference accessors the runtime's
// util.inspect reads (dynjson.c), always, since the runtime links them:
//   - `ptr __kml_classref_shown(ptr payload)`: `[class B extends A]`, or
//     null for a built-in constructor (`[Function: TypeError]`), which
//     shares the tag;
//   - `i64 __kml_classref_nstatic(ptr)`: its own public static fields;
//   - `ptr __kml_classref_skey(ptr, i64 i)` and `i64
//     __kml_classref_sget(ptr, i64 i)`: the i-th one's name and current
//     value, boxed.
func (e *Emitter) emitClassRefTable() {
	var classes []string
	for c := range e.classRefs {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	statics := func(c string) []string {
		var out []string
		for _, f := range e.classes[c].OwnStaticFieldOrder {
			if !strings.HasPrefix(f, "#") {
				out = append(out, f)
			}
		}
		return out
	}
	var shown, nstatic, skey strings.Builder
	shown.WriteString("\ndefine ptr @__kml_classref_shown(ptr %p) {\nentry:\n")
	nstatic.WriteString("\ndefine i64 @__kml_classref_nstatic(ptr %p) {\nentry:\n")
	skey.WriteString("\ndefine ptr @__kml_classref_skey(ptr %p, i64 %i) {\nentry:\n")
	for n, c := range classes {
		ref := e.classRefs[c]
		fields := statics(c)
		fmt.Fprintf(&shown, "  %%e%d = icmp eq ptr %%p, %s\n  br i1 %%e%d, label %%hit%d, label %%next%d\nhit%d:\n  ret ptr %s\nnext%d:\n", n, ref, n, n, n, n, e.internString(classShown(e, c)), n)
		fmt.Fprintf(&nstatic, "  %%e%d = icmp eq ptr %%p, %s\n  br i1 %%e%d, label %%hit%d, label %%next%d\nhit%d:\n  ret i64 %d\nnext%d:\n", n, ref, n, n, n, n, len(fields), n)
		for i, f := range fields {
			fmt.Fprintf(&skey, "  %%e%d_%d = icmp eq ptr %%p, %s\n  %%i%d_%d = icmp eq i64 %%i, %d\n  %%b%d_%d = and i1 %%e%d_%d, %%i%d_%d\n  br i1 %%b%d_%d, label %%hit%d_%d, label %%next%d_%d\nhit%d_%d:\n  ret ptr %s\nnext%d_%d:\n",
				n, i, ref, n, i, i, n, i, n, i, n, i, n, i, n, i, n, i, n, i, e.internString(f), n, i)
		}
	}
	shown.WriteString("  ret ptr null\n}\n")
	nstatic.WriteString("  ret i64 0\n}\n")
	skey.WriteString("  ret ptr null\n}\n")
	e.functions.WriteString(shown.String())
	e.functions.WriteString(nstatic.String())
	e.functions.WriteString(skey.String())

	// sget loads the field's global and boxes it: emitted as a detached
	// function so the boxing is the ordinary emitBoxValue.
	restore := e.beginDetachedFunc()
	for n, c := range classes {
		for i, f := range statics(c) {
			hit, next := e.freshLabel("classref.hit"), e.freshLabel("classref.next")
			eq, ix, both := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %%p, %s", eq, e.classRefs[c]))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %%i, %d", ix, i))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, eq, ix))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", both, hit, next))
			e.emitLabel(hit)
			ty := e.classes[c].OwnStaticFieldTypes[f]
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr @%s, align 8", r, storageIR(ty), llvmSafeSymbol(c+"_static_"+f)))
			if boxed, err := e.emitBoxValue(Value{Ref: r, Ty: ty}); err == nil {
				e.emitTerminator(fmt.Sprintf("ret i64 %s", boxed.Ref))
			} else {
				e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
			}
			e.emitLabel(next)
			_ = n
		}
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine i64 @__kml_classref_sget(ptr %%p, i64 %%i) {\nentry:\n%s}\n", body))
}

// classShown is util.inspect's rendering of class c as a value.
func classShown(e *Emitter, c string) string {
	shown := "[class " + inspectClassName(c)
	if base := e.classes[c].BaseClass; base != "" {
		shown += " extends " + inspectClassName(base)
	}
	return shown + "]"
}

// ensureClassRefInspect declares the runtime's rendering of a constructor
// reference at a depth (dynjson.c).
func (e *Emitter) ensureClassRefInspect() {
	if e.declaredClassRefInspect {
		return
	}
	e.declaredClassRefInspect = true
	e.emitGlobal("declare ptr @__kml_classref_inspect_at(ptr, i64)")
}
