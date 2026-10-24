package llvm

// emit_wrappers.go — a primitive's wrapper object (`new Number(3)`, `new
// String("a")`, `new Boolean(false)`). The wrapper is a headered host: an
// i64 body holding the boxed primitive, its host header word before it. A
// wrapper is only ever held as `any` (the checker's Number, String and
// Boolean interfaces lower to it), so it reads as an object: `typeof`
// "object", truthy, its own identity. Its row gives the primitive back to
// ToPrimitive and String(), and renders `[Number: 3]`; JSON.stringify
// serializes the primitive. A property read or method call on it reads the
// primitive's, which are the prototype's.

import (
	_ "embed"
	"fmt"

	"KlainMainLang/ast"
)

//go:embed wrappersrc/wrapper.c
var wrapperSource string

// ensureWrapperC links wrapper.c, the wrapper's own-property bag and keys.
func (e *Emitter) ensureWrapperC() {
	if e.usedWrapper {
		return
	}
	e.usedWrapper = true
	e.ensureDynObj()
	e.ensureHostBoxHooks()
	e.emitGlobal(`declare i64 @__kml_host_unwrap(i64)
declare i64 @__kml_wrapper_recv(i64, ptr)
declare i32 @__kml_wrapper_set(i64, ptr, i64, i32)
declare i32 @__kml_wrapper_is(i64)
declare ptr @__kml_wrapper_bag(i64)
declare ptr @__kml_wrapper_keys_c(i64, i32, ptr)
declare i32 @__kml_wrapper_has(i64, ptr)
declare i32 @__kml_wrapper_delete(i64, ptr)
declare i32 @__kml_str_has_own(ptr, ptr)`)
}

// primWrapperType is the wrapper object type of the class named name.
func primWrapperType(name string) Type {
	return Type{IR: "ptr", Host: hostPrimWrapper, HostTag: name}
}

// isPrimWrapperCtor reports `new Number(…)`, `new String(…)` or `new
// Boolean(…)` naming the global constructor.
func (e *Emitter) isPrimWrapperCtor(name string, qualified bool) bool {
	if qualified || (name != "Number" && name != "String" && name != "Boolean") {
		return false
	}
	if _, user := e.classes[name]; user {
		return false
	}
	_, bound := e.lookup(name)
	return !bound
}

// emitNewPrimWrapper builds the wrapper: the primitive the constructor
// called as a function gives (`Number(x)`), boxed into the body.
func (e *Emitter) emitNewPrimWrapper(ex *ast.NewExpression) (Value, error) {
	call := ast.NewCallExpression(ast.NewIdentifier(ex.ClassName, ex.GetPos()), ex.Args, ex.GetPos())
	call.Intrinsic = ex.ClassName
	prim, err := e.emitExpr(call)
	if err != nil {
		return Value{}, err
	}
	word, err := e.emitBoxValue(prim)
	if err != nil {
		return Value{}, err
	}
	return e.emitBuildWrapper(word.Ref, ex.ClassName), nil
}

// emitBuildWrapper is a new wrapper of class (Number, String, Boolean)
// holding the boxed primitive word, boxed.
func (e *Emitter) emitBuildWrapper(word, class string) Value {
	ty := primWrapperType(class)
	e.ensureMalloc()
	e.ensureWrapperC()
	base := e.freshReg()
	e.heapMallocInto(base, "24")
	e.heapStore("i64", fmt.Sprint(e.hostID(ty)), base, 8)
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, base))
	e.heapStore("i64", word, body, 8)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", bag, base))
	e.heapStore("ptr", "null", bag, 8)
	// It is only ever held boxed.
	return e.emitBoxHost(Value{Ref: body, Ty: ty})
}

// emitWrapperValue is the boxed primitive a wrapper's body holds.
func (e *Emitter) emitWrapperValue(body string) Value {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, body))
	return Value{Ref: r, Ty: TypeAny}
}

// emitInspectWrapper is console.log's `[Number: 3]`, `[String: 'a']`.
func (e *Emitter) emitInspectWrapper(v Value, depth int) (Value, error) {
	inner, err := e.emitInspectField(e.emitWrapperValue(v.Ref), depth)
	if err != nil {
		return Value{}, err
	}
	s, err := e.emitStringConcat(Value{Ref: e.internString("[" + v.Ty.HostTag + ": "), Ty: TypePtr}, inner)
	if err != nil {
		return Value{}, err
	}
	s, err = e.emitStringConcat(s, Value{Ref: e.internString("]"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	// Its own properties follow, as an object's: `[Number: 4] { tag: 'x' }`.
	bagP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", bagP, v.Ref))
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bag, bagP))
	e.ensureDynObj()
	cnt := e.freshReg()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s.Ref, slot))
	has, some := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, bag))
	propsL, doneL := e.freshLabel("wrap.props"), e.freshLabel("wrap.propsdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, propsL, doneL))
	e.emitLabel(propsL)
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_count(ptr %s)", cnt, bag))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", some, cnt))
	someL := e.freshLabel("wrap.someprops")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", some, someL, doneL))
	e.emitLabel(someL)
	boxed := e.emitNbTagPtr(bag, kmlTagDynObject)
	props, err := e.emitInspectField(Value{Ref: boxed, Ty: TypeAny}, depth)
	if err != nil {
		return Value{}, err
	}
	joined, err := e.emitStringConcat(s, Value{Ref: e.internString(" "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	if joined, err = e.emitStringConcat(joined, props); err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", joined.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// wrappersUsed reports whether the program constructs a wrapper object:
// only then can a dynamic value be one.
func (e *Emitter) wrappersUsed() bool {
	if e.programIndependent() {
		return false
	}
	if e.wrapperScan == 0 {
		e.wrapperScan = 1
		if e.prog != nil {
			ast.Inspect(e.prog, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.NewExpression:
					if !x.Qualified && (x.ClassName == "Number" || x.ClassName == "String" || x.ClassName == "Boolean" || x.ClassName == "Object" && len(x.Args) > 0) {
						e.wrapperScan = 2
					}
				case *ast.CallExpression:
					// Object(primitive) wraps it.
					if id, ok := x.Callee.(*ast.Identifier); ok && id.Name == "Object" && len(x.Args) > 0 {
						e.wrapperScan = 2
					}
				}
				return e.wrapperScan != 2
			})
		}
	}
	return e.wrapperScan == 2
}

// emitUnwrapPrim is v, or the primitive v wraps: a wrapper's properties
// and methods are its primitive's.
func (e *Emitter) emitUnwrapPrim(v Value) Value {
	if e.programIndependent() {
		return e.emitWrapperHookCall("__kml_host_unwrap_hook", v, "")
	}
	if !e.wrappersUsed() {
		return v
	}
	e.ensureWrapperC()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_host_unwrap(i64 %s)", r, v.Ref))
	return Value{Ref: r, Ty: v.Ty}
}

// emitWrapperRecv is what a read of key on v reads from: a wrapper's own
// property bag when it holds key, else its primitive; v for any other value.
func (e *Emitter) emitWrapperRecv(v Value, keyRef string) Value {
	if e.programIndependent() {
		return e.emitWrapperHookCall("__kml_wrapper_recv_hook", v, keyRef)
	}
	if !e.wrappersUsed() {
		return v
	}
	e.ensureWrapperC()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_wrapper_recv(i64 %s, ptr %s)", r, v.Ref, keyRef))
	return Value{Ref: r, Ty: v.Ty}
}

// emitWrapperHookCall is emitWrapperRecv (keyRef set) or emitUnwrapPrim in
// program-independent code, which cannot know whether the program makes a
// wrapper: it calls through hook, a weak null pointer here that wrapper.c,
// linked when the program makes one, defines as its routine; v when null.
func (e *Emitter) emitWrapperHookCall(hook string, v Value, keyRef string) Value {
	e.declareFn(hook, fmt.Sprintf("@%s = weak global ptr null, align 8", hook))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, slot))
	fp, has := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @%s, align 8", fp, hook))
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, fp))
	callL, joinL := e.freshLabel("wrap.hook"), e.freshLabel("wrap.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, callL, joinL))
	e.emitLabel(callL)
	r := e.freshReg()
	if keyRef != "" {
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(i64 %s, ptr %s)", r, fp, v.Ref, keyRef))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(i64 %s)", r, fp, v.Ref))
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(joinL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, slot))
	return Value{Ref: out, Ty: v.Ty}
}

// The primitives' valueOf: the primitive itself, or a wrapper's.
func init() {
	valueOf := intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			v, err := e.emitExpr(ex.Callee.(*ast.MemberExpression).Object)
			if err != nil || !isUnconstrainedDynamic(v.Ty) {
				return v, err
			}
			return e.emitUnwrapPrim(v), nil
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			return e.inferExprType(ex.Callee.(*ast.MemberExpression).Object)
		},
	}
	for _, c := range []string{"Number", "String", "Boolean"} {
		intrinsics[c+".prototype.valueOf"] = valueOf
	}
}

// emitWrapperKeys stores a wrapper's own keys (all of them, or the
// enumerable ones) into the { ptr, i64 } at resPtr and branches to doneL;
// any other value continues in a fresh block.
func (e *Emitter) emitWrapperKeys(v Value, all bool, resPtr, doneL string) {
	if !e.wrappersUsed() {
		return
	}
	e.ensureWrapperC()
	is, hit := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_wrapper_is(i64 %s)", is, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", hit, is))
	wrapL, contL := e.freshLabel("keys.wrapper"), e.freshLabel("keys.notwrapper")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, wrapL, contL))
	e.emitLabel(wrapL)
	np := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", np))
	flag := 0
	if all {
		flag = 1
	}
	arr, n, a0, a1 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_wrapper_keys_c(i64 %s, i32 %d, ptr %s)", arr, v.Ref, flag, np))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, np))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", a0, arr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", a1, a0, n))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", a1, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(contL)
}

// emitDynPrimValueOf is `v.valueOf()` of an `any`: a primitive's is the
// primitive itself (Number/String/Boolean.prototype.valueOf), any other
// value's its own method.
func (e *Emitter) emitDynPrimValueOf(v Value, pos ast.Pos) (Value, error) {
	tag, _ := e.emitUnboxTagPayload(v)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, slot))
	prim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagBoolean))
	objL, doneL := e.freshLabel("valueof.obj"), e.freshLabel("valueof.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", prim, doneL, objL))
	e.emitLabel(objL)
	e.inDynValueOf = true
	r, err := e.emitDynAnyMethodCall(v, "valueOf", nil, pos)
	e.inDynValueOf = false
	if err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.convert(r, TypeAny).Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, slot))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// emitWrapperHas is __kml_wrapper_has(v, key): 1 when v is a wrapper with
// key as an own property. Program-independent code calls it through its
// weak hook, answering 0 when the program makes no wrapper.
func (e *Emitter) emitWrapperHas(v Value, keyRef string) string {
	return e.emitWrapperKeyCall("has", v, keyRef, 0)
}

// emitWrapperDelete is __kml_wrapper_delete(v, key): 1 deleted, 0 refused
// (a String's index or length), -1 when v is no wrapper.
func (e *Emitter) emitWrapperDelete(v Value, keyRef string) string {
	return e.emitWrapperKeyCall("delete", v, keyRef, -1)
}

// emitWrapperSet is __kml_wrapper_set(v, key, val, sloppy): nonzero when
// v is a wrapper, which took the write (or threw). Program-independent code
// calls it through its weak hook, 0 when there is none.
func (e *Emitter) emitWrapperSet(v, keyRef, val string, sloppy int) string {
	if !e.programIndependent() {
		e.ensureWrapperC()
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_wrapper_set(i64 %s, ptr %s, i64 %s, i32 %d)", h, v, keyRef, val, sloppy))
		return h
	}
	hook := "__kml_wrapper_set_hook"
	e.declareFn(hook, fmt.Sprintf("@%s = weak global ptr null, align 8", hook))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", slot))
	e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", slot))
	fp, has := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @%s, align 8", fp, hook))
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, fp))
	callL, joinL := e.freshLabel("wrap.sethook"), e.freshLabel("wrap.setjoin")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, callL, joinL))
	e.emitLabel(callL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 %s(i64 %s, ptr %s, i64 %s, i32 %d)", r, fp, v, keyRef, val, sloppy))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", r, slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(joinL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", out, slot))
	return out
}

// emitWrapperKeyCall calls __kml_wrapper_<op>(v, key), through its weak
// hook in program-independent code, where none answers dflt.
func (e *Emitter) emitWrapperKeyCall(op string, v Value, keyRef string, dflt int) string {
	if !e.programIndependent() {
		e.ensureWrapperC()
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_wrapper_%s(i64 %s, ptr %s)", h, op, v.Ref, keyRef))
		return h
	}
	hook := "__kml_wrapper_" + op + "_hook"
	e.declareFn(hook, fmt.Sprintf("@%s = weak global ptr null, align 8", hook))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", slot))
	e.emitInstr(fmt.Sprintf("store i32 %d, ptr %s, align 4", dflt, slot))
	fp, has := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @%s, align 8", fp, hook))
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, fp))
	callL, joinL := e.freshLabel("wrap.hashook"), e.freshLabel("wrap.hasjoin")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, callL, joinL))
	e.emitLabel(callL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 %s(i64 %s, ptr %s)", r, fp, v.Ref, keyRef))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", r, slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(joinL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", out, slot))
	return out
}
