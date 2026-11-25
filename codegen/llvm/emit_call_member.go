package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// memberCall is what a member callee `o.m(…)` resolves to, decided once
// (memberCallOf) before anything is emitted.
type memberCall int

const (
	memberNone              memberCall = iota
	memberObjFuncField                 // a plain object's function-typed field
	memberExpando                      // a function's own function-valued property (a TS expando)
	memberNSAbsent                     // a namespace member not exported to this scope
	memberNSMember                     // a namespace member: its desugared flat function
	memberClassCallOn                  // `C.call(this, …)` on a class called that way
	memberStaticMethod                 // a class's static method
	memberGenericStatic                // a generic class's static method
	memberProtoCtorChain               // `Base.call(this, …)` on a prototype constructor
	memberCombinatorOn                 // `Promise.all.call(C, iterable)`
	memberNSQualifiedStatic            // `X.C.method()`
	memberNSChainAbsent                // `A.B.f()` not exported to this scope
	memberNSChain                      // `A.B.f()`
	memberFlatArray                    // a `@value` flat array's method
	memberNamespaceMethod              // a builtin namespace's method compiled here
	memberClassMethod                  // a class instance's method
	memberClassDynField                // a class instance's `any` field, called
	memberHostBase                     // the Map/Set a class extends
	memberBigIntRejected               // a method no BigInt typed array has
	memberDynArray                     // an Array method on an `any`
	memberDictHasOwn                   // hasOwnProperty on a dictionary or array
	memberGeneratorProto               // a method a generator's prototype chain holds
	memberFuncField                    // a function-typed field, called as a closure
	memberDynMethod                    // a method of a boxed receiver
	memberDynInherited                 // a method inherited from a dynamically represented builtin
	memberFuncOwnProp                  // a function's own property the checker did not see declared
	memberBoxedMethod                  // a method of a receiver held boxed
	memberClassOwnProp                 // a method a class instance holds as its own property
	memberObjDynField                  // a fixed-layout object's boxed field
	memberArrayOwnFn                   // an array's own function property
	memberAbsentMethod                 // a method the library does not declare: TypeError
	memberCallableValue                // the property's value, called
	memberNoMethod                     // the "has no method" diagnostic
)

// memberCallOf classifies the call ex of member callee mem: the owner of
// the method in the order JavaScript's lookup reaches it (compile-time
// namespaces and classes, the receiver's representation, the values it
// holds), then the diagnostics. Classifying a generic class's receiver
// registers the instantiation its methods are looked up in.
func (e *Emitter) memberCallOf(ex *ast.CallExpression, mem *ast.MemberExpression) (memberCall, error) {
	if !mem.Optional {
		if ot := e.typeOfExpr(mem.Object); ot.IsObject() && !ot.IsClass() && !ot.IsArray() && !ot.IsDynamic() && !isHostHandle(ot) && !ot.IsRegExp() {
			if _, fty, isField := ot.FieldIndex(mem.Property); isField && fty.IsFunc() {
				if mt := e.typeOfExpr(mem); mt.IsFunc() || isUnconstrainedDynamic(mt) {
					return memberObjFuncField, nil
				}
			}
		}
	}
	if e.isExpandoCall(ex) && e.isFuncOwnPropCall(ex) {
		return memberExpando, nil
	}
	if id, ok := mem.Object.(*ast.Identifier); ok {
		if members, nsName := e.namespaceMembers(id.Name); members != nil {
			if exported, present := members[mem.Property]; present && !e.isShadowedByLocal(id.Name) {
				if !exported && e.curNamespace != nsName {
					return memberNSAbsent, nil
				}
				return memberNSMember, nil
			}
		}
		if _, found := e.classes[id.Name]; found {
			if mem.Property == "call" && e.callOnClasses()[id.Name] {
				return memberClassCallOn, nil
			}
			return memberStaticMethod, nil
		}
		if _, generic := e.genericClasses[id.Name]; generic && !e.isShadowedByLocal(id.Name) {
			if _, found := e.classes[genericStaticsClass(id.Name)]; found {
				return memberGenericStatic, nil
			}
		}
		if e.jsProtoCtor[id.Name] && mem.Property == "call" {
			return memberProtoCtorChain, nil
		}
	}
	if e.combinatorCallOn(mem, ex.Args) != "" {
		return memberCombinatorOn, nil
	}
	if bare := e.stripNSTypeQualifier(mem.Object); bare != nil {
		if bid, ok := bare.(*ast.Identifier); ok {
			if _, found := e.classes[bid.Name]; found {
				return memberNSQualifiedStatic, nil
			}
		}
	}
	if members, nsName := e.namespaceByChain(mem.Object); members != nil {
		if exported, present := members[mem.Property]; present {
			if !exported && e.curNamespace != nsName {
				return memberNSChainAbsent, nil
			}
			return memberNSChain, nil
		}
	}
	objTy := e.typeOfExpr(mem.Object)
	if objTy.IsFlatArray() {
		return memberFlatArray, nil
	}
	if id, ok := mem.Object.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) && namespaceMethods[id.Name][mem.Property] != nil {
		return memberNamespaceMethod, nil
	}
	if objTy.IsClass() {
		if kind, err := e.classReceiverCallOf(objTy, mem); err != nil || kind != memberNone {
			return kind, err
		}
	}
	if bigIntElemRejectedMethods[mem.Property] && objTy.BigIntElem {
		return memberBigIntRejected, nil
	}
	if _, ok := dynArrayMethods[mem.Property]; ok && (isUnconstrainedDynamic(objTy) || unionOfArrays(objTy)) {
		return memberDynArray, nil
	}
	if mem.Property == "hasOwnProperty" && len(ex.Args) == 1 && (objTy.IsDynamicObject() || objTy.IsArray() && !objTy.IsTuple() && !objTy.Nullable()) {
		return memberDictHasOwn, nil
	}
	if _, own := generatorMethods[mem.Property]; !own && e.genProtos && !ex.Optional && !mem.Optional && objTy.IsGenerator() {
		return memberGeneratorProto, nil
	}
	// A primitive's method the program's own global interface declares
	// (a script's `interface String { shout(): string }`): read from the
	// prototype when the call runs.
	if c := e.front(); c != nil && e.scalarMethodRecv(mem, objTy) {
		if d := c.MemberDecl(mem); d != nil && c.InProgram(d) {
			return memberBoxedMethod, nil
		}
	}
	unionMethod := e.unionClassMethod(mem)
	if !unionMethod && e.typeOfExpr(mem).IsFunc() {
		return memberFuncField, nil
	}
	if isUnconstrainedDynamic(objTy) || objTy.IsDynamic() && (isSelfDescribingBox(objTy) || isClassUnionBox(objTy)) {
		return memberDynMethod, nil
	}
	if !mem.Optional && isDynamicMethodRead(e.front(), mem, objTy) {
		return memberDynInherited, nil
	}
	if e.isFuncOwnPropCall(ex) {
		return memberFuncOwnProp, nil
	}
	if objTy.IsDynamic() && !strings.HasPrefix(mem.Property, "#") {
		return memberBoxedMethod, nil
	}
	if objTy.IsClass() {
		return memberClassOwnProp, nil
	}
	if objTy.IsObject() && !objTy.IsDynamic() {
		for _, f := range objTy.Fields {
			if f.Name == mem.Property && isUnconstrainedDynamic(f.Ty) {
				return memberObjDynField, nil
			}
		}
	}
	if objTy.IsArray() && !objTy.IsObject() && !objTy.IsDynamic() && ex.GetPos().Line > 0 {
		return memberArrayOwnFn, nil
	}
	c := e.front()
	if c != nil && c.InProgram(mem) && !objTy.IsDynamic() && c.MemberDecl(mem) == nil && !c.ExpandoMember(mem) {
		if objTy.IsError() && !objTy.IsClass() && !strings.HasPrefix(mem.Property, "#") {
			// An Error's prototype may be one it was given: read at run
			// time.
			return memberBoxedMethod, nil
		}
		return memberAbsentMethod, nil
	}
	if c != nil {
		if _, prop := c.MemberDecl(mem).(*ast.PropertySignature); prop {
			// A property the library declares (`n.constructor`) holds a
			// value: the call is through it.
			if calleeTy := e.typeOfExpr(ex.Callee); calleeTy.IsFunc() || calleeTy.IsDynamic() {
				return memberCallableValue, nil
			}
			return memberNone, nil
		}
	}
	// A boolean's, number's or string's method no native path knows (one
	// only the program's own `interface Boolean` or `String` declares) is read from the
	// prototype when the call runs, as in JavaScript: a function the
	// program put there is called, anything else throws its TypeError.
	if e.scalarMethodRecv(mem, objTy) {
		return memberBoxedMethod, nil
	}
	return memberNoMethod, nil
}

// classReceiverCallOf classifies a call on a class instance: a declared
// method, an `any` field holding a function, or the method of the Map or
// Set the class extends; memberNone for a field holding a typed closure,
// which memberFuncField takes.
func (e *Emitter) classReceiverCallOf(objTy Type, mem *ast.MemberExpression) (memberCall, error) {
	// `new Box<string>(…).get()`: the instantiation the receiver's `new`
	// makes, registered before its methods are looked up.
	if _, err := e.ensureGenericInstance(objTy.ClassName); err != nil {
		return memberNone, err
	}
	if _, ok := e.classes[objTy.ClassName]; !ok {
		if ne, isNew := mem.Object.(*ast.NewExpression); isNew {
			if genDecl, generic := e.genericClasses[ne.ClassName]; generic {
				if targs, ok := e.newTypeArgs(genDecl, ne); ok {
					if _, err := e.instantiateGenericClass(genDecl, e.buildTypeArgSubs(genDecl.TypeParams, targs)); err != nil {
						return memberNone, err
					}
				}
			}
		}
	}
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return memberNone, nil
	}
	if _, ok := info.MethodSigs[mem.Property]; ok {
		return memberClassMethod, nil
	}
	if _, fty, isField := info.Ty.FieldIndex(mem.Property); isField && isUnconstrainedDynamic(fty) {
		return memberClassDynField, nil
	}
	if info.HostBase != nil {
		return memberHostBase, nil
	}
	return memberNone, nil
}

// emitMemberCall emits the call ex of member callee mem by what it
// resolves to (memberCallOf); done is false for a callee it does not take.
func (e *Emitter) emitMemberCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	kind, err := e.memberCallOf(ex, mem)
	if err != nil {
		return handled(Value{}, err)
	}
	if kind == memberNone {
		return Value{}, false, nil
	}
	return handled(e.emitMemberCallKind(ex, mem, kind))
}

func (e *Emitter) emitMemberCallKind(ex *ast.CallExpression, mem *ast.MemberExpression, kind memberCall) (Value, error) {
	pos := ex.GetPos()
	// The receiver's value, boxed: `this` of a dynamic method call.
	boxedRecv := func() (Value, error) {
		v, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IsDynamic() {
			return v, nil
		}
		return e.emitBoxValue(v)
	}
	flatCall := func(name string) (Value, error) {
		return e.emitCall(ast.NewCallExpression(ast.NewIdentifier(name, pos), ex.Args, pos))
	}
	switch kind {
	case memberObjFuncField:
		ot := e.typeOfExpr(mem.Object)
		_, fty, _ := ot.FieldIndex(mem.Property)
		mt := e.typeOfExpr(mem)
		if isUnconstrainedDynamic(mt) {
			mt = fty // the layout's member, where the read answers no type
		}
		return e.emitFuncFieldCall(ex, mem, mt)
	case memberExpando:
		r, _ := e.funcOwnPropCall(ex)
		fn, err := e.emitExpr(r.Callee.(*ast.MemberExpression).Object)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMethodCallPlain(fn, mem.Property, ex.Args, pos)
	case memberNSAbsent:
		id := mem.Object.(*ast.Identifier)
		return e.emitCallOfAbsent(id.Name+"."+mem.Property, ex.Args, pos)
	case memberNSMember:
		_, nsName := e.namespaceMembers(mem.Object.(*ast.Identifier).Name)
		return flatCall(ast.NamespaceMangle(nsName, mem.Property))
	case memberClassCallOn:
		return e.emitClassCallOn(mem.Object.(*ast.Identifier).Name, ex.Args, pos)
	case memberStaticMethod:
		name := mem.Object.(*ast.Identifier).Name
		return e.emitStaticMethodCall(e.classes[name], name, mem.Property, ex.Args, pos)
	case memberGenericStatic:
		name := genericStaticsClass(mem.Object.(*ast.Identifier).Name)
		return e.emitStaticMethodCall(e.classes[name], name, mem.Property, ex.Args, pos)
	case memberProtoCtorChain:
		// The pre-ES6 inheritance chain (TDD-00155 Stage 4): the receiver is
		// passed through, unlike the generic .call path.
		return e.emitProtoCtorChainCall(mem.Object.(*ast.Identifier).Name, ex.Args, pos)
	case memberCombinatorOn:
		v, _, err := e.emitCombinatorCallOn(mem, ex.Args, pos)
		return v, err
	case memberNSQualifiedStatic:
		bid := e.stripNSTypeQualifier(mem.Object).(*ast.Identifier)
		return e.emitStaticMethodCall(e.classes[bid.Name], bid.Name, mem.Property, ex.Args, pos)
	case memberNSChainAbsent:
		_, nsName := e.namespaceByChain(mem.Object)
		return e.emitCallOfAbsent(nsName+"."+mem.Property, ex.Args, pos)
	case memberNSChain:
		_, nsName := e.namespaceByChain(mem.Object)
		return flatCall(ast.NamespaceMangle(nsName, mem.Property))
	case memberFlatArray:
		// A `/** @value */` flat array (TDD-00134 Stage 2) supports push;
		// everything else needs the pointer-slot layout.
		if mem.Property == "push" {
			return e.emitFlatArrayPush(e.typeOfExpr(mem.Object), mem, ex.Args, pos)
		}
		return Value{}, internalAt(pos, "@value array use the resolver admits")
	case memberNamespaceMethod:
		return namespaceMethods[mem.Object.(*ast.Identifier).Name][mem.Property](e, ex.Args, pos)
	case memberClassMethod:
		objTy := e.typeOfExpr(mem.Object)
		v, err := e.emitClassMethodCall(objTy, mem.Object, mem.Property, ex.Args, pos)
		if err == nil && v.Ty.IsClass() {
			v.Ty = e.genericMethodResult(ex, objTy, mem.Property, v.Ty)
		} else if err == nil && isUnconstrainedDynamic(v.Ty) {
			if ct := e.genericMethodResult(ex, objTy, mem.Property, v.Ty); ct.IsClass() {
				v = e.convert(v, ct)
			}
		}
		return v, err
	case memberClassDynField, memberClassOwnProp:
		return e.emitClassMethodCall(e.typeOfExpr(mem.Object), mem.Object, mem.Property, ex.Args, pos)
	case memberHostBase:
		inst, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		return e.emitHostBaseCall(inst, *e.classes[e.typeOfExpr(mem.Object).ClassName].HostBase, mem.Property, ex.Args, pos)
	case memberBigIntRejected:
		return Value{}, internalAt(pos, "a BigInt64Array/BigUint64Array has no .%s() (no TypedArray does)", mem.Property)
	case memberDynArray, memberDynMethod:
		objVal, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMethodCall(objVal, mem.Property, ex.Args, pos)
	case memberDictHasOwn:
		return e.emitHasOwnProperty(mem.Object, ex.Args[0], "hasOwnProperty", true, pos)
	case memberBoxedMethod:
		boxed, err := boxedRecv()
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMethodCall(boxed, mem.Property, ex.Args, pos)
	case memberGeneratorProto, memberDynInherited, memberObjDynField, memberArrayOwnFn:
		v, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMethodCall(boxed, mem.Property, ex.Args, pos)
	case memberFuncField:
		return e.emitFuncFieldCall(ex, mem, e.typeOfExpr(mem))
	case memberFuncOwnProp:
		r, _ := e.funcOwnPropCall(ex)
		return e.emitCall(r)
	case memberAbsentMethod:
		// The library has no such member (`ArrayBuffer.prototype.
		// transferToImmutable`): it reads undefined, so the call throws once
		// its arguments are evaluated.
		if _, err := e.emitExpr(mem.Object); err != nil {
			return Value{}, err
		}
		for _, a := range ex.Args {
			if sp, ok := a.(*ast.SpreadElement); ok {
				a = sp.Arg
			}
			if _, err := e.emitExpr(a); err != nil {
				return Value{}, err
			}
		}
		e.emitThrowTypeError(calleeDisplayName(mem) + " is not a function")
		e.emitLabel(e.freshLabel("absentcall.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	case memberCallableValue:
		v, _, err := e.callableValueRule(ex)
		return v, err
	case memberNoMethod:
		recv := describeReceiverType(e.typeOfExpr(mem.Object))
		// A bare namespace/global identifier receiver (Object.setPrototypeOf,
		// process.send, Math.foo, …) is named explicitly, so the diagnostic
		// attributes the missing member to the right namespace.
		if id, ok := mem.Object.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
			if n := strings.TrimSuffix(id.Name, "__kml_builtin"); n != id.Name {
				recv = n
			} else if knownGlobalNamespace[id.Name] {
				recv = id.Name
			}
		}
		return Value{}, internalAt(pos, "%s has no method '%s'", recv, mem.Property)
	}
	return Value{}, internalAt(pos, "unclassified member call")
}

// namespaceReceiver is whether x names a builtin namespace or global
// (Math, process, a lowered builtin), not a value of its own.
func (e *Emitter) namespaceReceiver(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	if !ok || e.isShadowedByLocal(id.Name) {
		return false
	}
	return strings.HasSuffix(id.Name, "__kml_builtin") || knownGlobalNamespace[id.Name]
}

// scalarMethodRecv is whether mem calls a method on a boolean or number
// value (not a namespace) that reaches the classifier: one no native path
// answered, read from the prototype when the call runs.
func (e *Emitter) scalarMethodRecv(mem *ast.MemberExpression, objTy Type) bool {
	// A dynamic call's static probe (emitDynStaticBranch) fails instead:
	// going dynamic again would probe the same value forever.
	return !e.inDynBufferCall && !mem.Optional && !objTy.IsDynamic() && !objTy.IsObject() && !objTy.IsArray() &&
		(objTy.IR == "i1" || objTy.IR == "i64" || objTy.IR == "double" || isStringTy(objTy)) && !e.namespaceReceiver(mem.Object)
}
