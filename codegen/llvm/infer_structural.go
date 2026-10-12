// infer_structural.go — the representation of a member read or call the
// checker never saw: a node codegen built itself after checking (a rewritten
// or lowered call, a namespace-mangled name, an intrinsic's desugaring, a
// temporary's member read). It mirrors the dispatch emitCall and emitMember
// run on that node, so the type is the one its emission produces. A node the
// checker saw takes the checker's type (inferExprType).
package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// structuralMemberType is a member read's representation from emitMember's
// dispatch on its receiver.
func (e *Emitter) structuralMemberType(ex *ast.MemberExpression) Type {
	if e.builtinCtorRead(ex) {
		return TypeAny
	}
	if call, ok := e.tsSubtleRef(ex); ok {
		return e.inferExprType(call)
	}
	if e.isTemplateRaw(ex) {
		return ArrayOf(TypePtr)
	}
	if _, ok := domExceptionConstants[ex.Property]; ok && e.isDOMExceptionConstantOwner(ex.Object) {
		return TypeF64
	}
	if e.hostMethodValue(ex) {
		return TypeAny // the method as a dynamic function (emitMember)
	}
	if ex.Property == "code" && e.isDOMExceptionExpr(ex.Object) {
		if ot := e.inferExprType(ex.Object); ot.IsError && !ot.IsClass {
			return TypeF64 // must match emitMemberRaw's DOMException code
		}
	}
	if _, _, ok := execArrayMemberType(ex.Property); ok {
		if ot := e.inferExprType(ex.Object); ot.ExecArray {
			return execMemberValueType(ex.Property, ot.ExecMaybePlain)
		}
	}
	// process.stdin/stdout/stderr: the stdio module's stream (mirrors
	// emitMemberRaw).
	if _, ok := e.processStdioGetter(ex); ok {
		return TypeAny
	}
	if r, ok := e.processMemberRewrite(ex); ok {
		return e.inferExprType(r)
	}
	// `o?.x` is `PropType | undefined` — mirror emitOptionalMember, which
	// wraps a pointer (non-array) object's optional read as `T | undefined`
	// (a scalar/pointer prop; undefinedableElem no-ops on array/tuple/dynamic).
	if ex.Optional {
		objTy := e.inferExprType(ex.Object)
		plain := ast.NewMemberExpression(ex.Object, ex.Property, ex.GetPos())
		inner := e.inferExprType(plain)
		if (objTy.IR == "ptr" && !objTy.IsArray) || isNullableScalar(objTy) ||
			(objTy.IsArray && objTy.Nullable && ex.Property == "length") {
			return optionalCallResultType(optCalleeValue, inner)
		}
		return inner
	}
	// A function's own property (TDD-00229) reads from its bag: the
	// checker's primitive, else any (mirrors emitMember).
	// A namespace merged into the function declares its members (below).
	if ot := e.inferExprType(ex.Object); ot.IsFunc && !ot.IsDynamic && ex.Property != "name" && ex.Property != "length" && !numericBuiltinConst(ex) && !e.isNamespaceMember(ex) {
		if pt, ok := e.checkerPrimitive(ex); ok {
			return pt
		}
		if dt, ok := e.checkerDictType(ex); ok {
			return dt
		}
		return TypeAny
	}
	// An optional method read as a value: its code, or undefined
	// (optionalMethodPresence).
	if ot := e.inferExprType(ex.Object); ot.IsClass && e.isOptionalMethod(ot.ClassName, ex.Property) {
		if _, _, isField := ot.FieldIndex(ex.Property); !isField {
			ty := TypePtr
			ty.Nullable, ty.IsUndefined = true, true
			return ty
		}
	}
	// A named property of an index-signature dictionary has its declared type.
	if ot := e.inferExprType(ex.Object); ot.IsDynamicObject {
		for _, f := range ot.DictFields {
			if f.Name == ex.Property {
				return f.Ty
			}
		}
		// Any other key reads the index signature's type (a box, for a
		// union: emitDynamicObjectGet).
		if ot.MapVal != nil && ot.MapVal.IsDynamic {
			return *ot.MapVal
		}
		// Otherwise the map's get(): the value, undefined on a miss
		// (emitDynamicObjectGet through emitMapCall).
		if ot.IsMap && ot.MapVal != nil {
			get := ast.NewCallExpression(ast.NewMemberExpression(ex.Object, "get", ex.GetPos()), []ast.Expression{ast.NewStringLiteral(ex.Property, ex.GetPos())}, ex.GetPos())
			if t, ok := e.inferCollectionCallType(get, get.Callee.(*ast.MemberExpression)); ok {
				return t
			}
		}
	}
	// An index-signature view (DynPropTy): every property has its type.
	if ot := e.inferExprType(ex.Object); isUnconstrainedDynamic(ot) && ot.DynPropTy != nil {
		return dynPropReadType(*ot.DynPropTy)
	}
	// A property of an `any` value the checker knows to be a primitive
	// (`r.stdout` of a `SpawnSyncReturns<string>` a TypeScript module
	// returned through `any`): that primitive, mirroring emitMember.
	if ot := e.inferExprType(ex.Object); isUnconstrainedDynamic(ot) {
		if pt, ok := e.checkerPrimitive(ex); ok {
			return pt
		}
	}
	// A member of an un-narrowed union of scalars is read off its box at
	// run time (emitMember, emitCall): a count is a number, anything else
	// a box.
	if ot := e.inferExprType(ex.Object); ot.IsDynamic && len(ot.UnionMembers) > 0 && isSelfDescribingBox(ot) && !unionAllHaveLength(ot) {
		if ex.Property == "length" {
			return TypeF64
		}
		return TypeAny
	}
	// A member read off a caught value (TDD-00202/00207) mirrors
	// emitCaughtMemberGet's result type: a known Error field keeps its real
	// type, `.errors` is an any[], anything else is `any`. This must
	// agree with the emit side or a callback's declared return type (e.g. a
	// reject handler `(e) => (e as Error).name`) disagrees with its body's IR.
	// An Error's `.errors` is AggregateError's any[] (mirrors emitMember's
	// kind-guarded accessor).
	if ot := e.inferExprType(ex.Object); ex.Property == "errors" && ot.IsError && ot.IsObject && !ot.IsClass && !ot.IsCaught {
		return ArrayOf(TypeAny)
	}
	if e.inferExprType(ex.Object).IsCaught {
		if ex.Property == "errors" {
			return ArrayOf(TypeAny)
		}
		if _, fieldTy, ok := errorObjType.FieldIndex(ex.Property); ok && caughtTypedField(ex.Property) {
			if nt := undefinedableElem(fieldTy); fieldTy.IR == "ptr" || isNullableScalar(nt) {
				return nt
			}
			return fieldTy
		}
		return TypeAny
	}
	// `F.prototype` on a recognized prototype constructor is a dynamic
	// object (TDD-00155 Stage 4).
	if id, ok := ex.Object.(*ast.Identifier); ok && e.compatJS() && e.jsProtoCtor[id.Name] && ex.Property == "prototype" {
		return TypeAny
	}
	// A property read off a bare any/unknown base is itself dynamic
	// (TDD-00155): the runtime tag dispatch yields another box.
	// (A builtin's numeric constant, `Math.E`, is the number emitMember
	// reads, below.)
	if baseTy := e.inferExprType(ex.Object); isUnconstrainedDynamic(baseTy) && !e.isClassName(ex.Object) && !e.builtinNumericConst(ex) {
		return TypeAny
	}
	// process.stdout/.stderr/.stdin `.isTTY` — a boolean isatty probe.
	if ex.Property == "isTTY" {
		if inner, ok := ex.Object.(*ast.MemberExpression); ok {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
				switch inner.Property {
				case "stdin", "stdout", "stderr":
					return TypeBool
				}
			}
		}
	}
	// res.statusCode (ServerResponse, TDD-00131) — the response status,
	// stored as the object's i64 `status` field.
	if ex.Property == "statusCode" {
		if e.inferExprType(ex.Object).IsServerResponse {
			return TypeI64
		}
	}
	// `.length` on an array/string/tuple/typed-array is a Number (double)
	// — TDD-00123 Stage 3. Must mirror emitMemberExpr's `.length` sites,
	// which sitofp the i64 count to double. (An object field literally
	// named `length` is resolved by the field-lookup path further below.)
	if ex.Property == "length" {
		ot := e.inferExprType(ex.Object)
		if ot.IsArray || ot.IsFlatArray || ot.IsTuple || ot.IsTypedArray || isStringTy(ot) || ot.IsFunc {
			return TypeF64
		}
		// … and on an instance of a class extending Array or a typed
		// array, whose body is one (ADR-01469).
		if ot.IsClass {
			if info, ok := e.classes[ot.ClassName]; ok && info.HostBase != nil && (info.HostBase.IsArray || info.HostBase.IsTypedArray) {
				return TypeF64
			}
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "Object" && ex.Property == "prototype" && !e.isShadowedByLocal("Object") {
		return TypeAny
	}
	// A function value's `name` is a string (TDD-00229).
	if ot := e.inferExprType(ex.Object); ex.Property == "name" && (ot.IsFunc) {
		return TypePtr
	}
	// Response.body — must match emitResponseBodyStream (TDD-00097 St. 4).
	if ex.Property == "body" {
		if objTy := e.inferExprType(ex.Object); hasBodyMixin(objTy) {
			return ReadableStreamType(TypedArrayType("uint8"))
		}
	}
	if ex.Property == "bodyUsed" {
		if objTy := e.inferExprType(ex.Object); hasBodyMixin(objTy) {
			return TypeBool
		}
	}
	// Response.statusText / .type — strings.
	if ex.Property == "statusText" || ex.Property == "type" {
		if objTy := e.inferExprType(ex.Object); objTy.IsResponse {
			return TypePtr
		}
	}
	// A Response's or Request's headers: a Headers.
	if ex.Property == "headers" {
		if objTy := e.inferExprType(ex.Object); objTy.IsResponse || objTy.IsFetchRequest {
			if cls, ok := e.globalClass("Headers"); ok {
				return cls
			}
		}
	}
	// TransformStream sides — must match emitTransformStreamProperty.
	if ex.Property == "readable" || ex.Property == "writable" {
		if objTy := e.inferExprType(ex.Object); objTy.IsTransformStream {
			if ex.Property == "readable" {
				if objTy.StreamOut != nil {
					return ReadableStreamType(*objTy.StreamOut)
				}
				return ReadableStreamType(TypeAny)
			}
			if objTy.StreamChunk != nil {
				return WritableStreamType(*objTy.StreamChunk)
			}
			return WritableStreamType(TypeAny)
		}
	}
	// Stream properties — must match emitStreamProperty (TDD-00097).
	if ex.Property == "locked" || ex.Property == "desiredSize" || ex.Property == "closed" {
		if objTy := e.inferExprType(ex.Object); objTy.IsReadableStream || objTy.IsStreamReader || objTy.IsRSController {
			switch ex.Property {
			case "locked":
				return TypeBool
			case "desiredSize":
				return TypeF64
			case "closed":
				pt := PromiseOf(TypeVoid)
				pt.PromiseTask = true
				return pt
			}
		}
	}
	// Growable-buffer properties (ADR-00494) — must match
	// emitBufferGrowableProps.
	if ex.Property == "growable" || ex.Property == "resizable" || ex.Property == "maxByteLength" {
		if objTy := e.inferExprType(ex.Object); objTy.IsArrayBuffer {
			if ex.Property == "maxByteLength" {
				return TypeI64
			}
			return TypeBool
		}
	}
	// `.byteLength` of an ArrayBuffer or typed array is a number — must
	// match emitMemberRaw's countToNumber.
	if ex.Property == "byteLength" {
		if objTy := e.inferExprType(ex.Object); objTy.IsArrayBuffer || objTy.IsTypedArray {
			return TypeF64
		}
	}
	// A typed array's view properties — must match emitTypedArrayViewProp.
	if ex.Property == "byteOffset" || ex.Property == "buffer" {
		if objTy := e.inferExprType(ex.Object); objTy.IsTypedArray {
			if ex.Property == "buffer" {
				return TypeAny
			}
			return TypeI64
		}
	}
	// TS namespace member (TDD-00095) — must match emitMember.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if members, nsName := e.namespaceMembers(id.Name); members != nil && members[ex.Property] {
			if !e.isShadowedByLocal(id.Name) {
				return e.inferExprType(ast.NewIdentifier(ast.NamespaceMangle(nsName, ex.Property), ex.GetPos()))
			}
		}
	}
	// Nested-namespace member `A.B.member` (TDD-00148 V3) — must match emitMember.
	if members, nsName := e.namespaceByChain(ex.Object); members != nil && members[ex.Property] {
		return e.inferExprType(ast.NewIdentifier(ast.NamespaceMangle(nsName, ex.Property), ex.GetPos()))
	}
	// Namespace-qualified type-member chain (ADR-00480) — must match emitMember.
	if bare := e.stripNSTypeQualifier(ex.Object); bare != nil {
		return e.inferExprType(&ast.MemberExpression{Object: bare, Property: ex.Property})
	}
	// `x.constructor` of a class instance: its run-time class, a
	// constructor reference (emitConstructorOf).
	if ex.Property == "constructor" {
		if ot := e.inferExprType(ex.Object); ot.IsTypedArray && !ot.IsDynamic {
			return TypeAny
		}
		if ot := e.inferExprType(ex.Object); ot.IsClass && !ot.IsDynamic {
			if _, _, own := e.canonicalizeClassTy(ot).FieldIndex("constructor"); !own {
				return TypeAny
			}
		}
	}
	// Static field read: ClassName.staticField (TDD-00009 Stage 4).
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if target, ok := e.identClassAlias(id); ok {
			return e.inferExprType(ast.NewMemberExpression(ast.NewIdentifier(target, id.GetPos()), ex.Property, ex.GetPos()))
		}
		if info, found := e.classes[id.Name]; found {
			if fty, ok := info.StaticFieldTypes[ex.Property]; ok {
				return fty
			}
			if sig, ok := info.StaticMethodSigs[accessorMethodName("get", ex.Property)]; ok {
				return sig.RetType // a static getter (mirrors emitStaticFieldRead)
			}
			if sig, ok := info.StaticMethodSigs[ex.Property]; ok {
				return funcValueType(sig) // a static method read as a value
			}
			switch ex.Property {
			case "name":
				return TypePtr
			case "length":
				return TypeF64
			}
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if members, found := e.enums[id.Name]; found {
			if val, ok := members[ex.Property]; ok {
				return val.Ty
			}
		}
	}
	if ex.Property == "size" {
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && (sym.Ty.IsMap || sym.Ty.IsSet) {
				return TypeI64
			}
		} else if objTy := e.inferExprType(ex.Object); objTy.IsMap || objTy.IsSet {
			// Not a named variable — e.g. c.scores.size where scores: Map<K,V>.
			return TypeI64
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
		switch id.Name {
		case "Symbol":
			if wellKnownSymbols[ex.Property] {
				return SymbolType()
			}
		case "Math":
			switch ex.Property {
			case "PI", "E", "LN2", "LN10", "SQRT2", "SQRT1_2", "LOG2E", "LOG10E":
				return TypeF64
			}
		case "Number":
			switch ex.Property {
			case "MAX_SAFE_INTEGER", "MIN_SAFE_INTEGER", "EPSILON", "MAX_VALUE", "MIN_VALUE", "POSITIVE_INFINITY", "NEGATIVE_INFINITY", "NaN":
				return TypeF64
			}
		case "process":
			switch ex.Property {
			case "platform", "arch":
				return TypePtr
			case "env":
				// The bare value is a dynamic bag (emitProcessEnvValue); a
				// keyed read is handled by the isProcessEnvExpr arm below.
				if !ex.Optional {
					return TypeAny
				}
			}
		}
	}
	// General object field read: any expression whose type is an object,
	// not just a bare identifier — e.g. a field access chained off
	// another field access (ev.when.getFullYear() needs to know
	// ev.when's type before it can resolve getFullYear on it).
	if objTy := e.inferExprType(ex.Object); objTy.IsObject {
		// A class getter's read type is its return type, not a field type —
		// consult accessors before FieldIndex (an accessor name is never a
		// real Field, so FieldIndex would miss it). Matches the read path in
		// emit_exprs_member.go (TDD-00030).
		if objTy.IsClass {
			if getter, _, ok := e.classAccessorSigs(objTy.ClassName, ex.Property); ok && getter != nil {
				return e.canonicalizeClassTy(getter.RetType)
			}
		}
		if _, fieldTy, ok := objTy.FieldIndex(ex.Property); ok {
			if e.isErrorOptionalNumber(objTy, ex.Property) {
				return undefinedableElem(fieldTy)
			}
			return e.canonicalizeClassTy(fieldTy)
		}
	}
	return TypeI64
}

// structuralCallType is a call's representation from emitCall's dispatch
// on its callee.
func (e *Emitter) structuralCallType(ex *ast.CallExpression) Type {
	if r, ok := e.builtinIteratorCall(ex); ok {
		return e.inferExprType(r)
	}
	if t, ok := e.thenableResolveCallType(ex); ok {
		return t
	}
	if t, ok := e.thenConstCallType(ex); ok {
		return t
	}
	if isResolveThenCall(ex) {
		return TypeVoid
	}
	if m := indexCalleeAsMember(ex); m != nil {
		return e.inferExprType(m)
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if r, ok := e.processMemberRewrite(mem); ok {
			return e.inferExprType(ast.NewCallExpression(r, ex.Args, ex.GetPos()))
		}
	}
	if r, ok := e.funcOwnPropCall(ex); ok {
		return e.inferExprType(r)
	}
	if ft, ok := e.checkerRefinedCallType(ex); ok {
		return ft
	}
	// `obj[key](...)` on a bare any/unknown receiver (mirrors emitCall).
	if ix, ok := ex.Callee.(*ast.IndexExpression); ok && !ix.Optional && isUnconstrainedDynamic(e.inferExprType(ix.Object)) {
		return TypeAny
	}
	// Calling a dynamic value (a local holding a boxed function) is a
	// dynamic call whose result is dynamic.
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if found && isUnconstrainedDynamic(sym.Ty) {
			return TypeAny
		} else if !found {
			// A binding not in scope yet (a pre-pass typing a local
			// before its function's parameters are bound): the
			// checker's type of it.
			if c := e.front(); c != nil {
				if t := c.TypeOf(id); !c.Unanswered(t) && t.Flags&checker.Any != 0 {
					return TypeAny
				}
			}
		}
	}

	// A function-style constructor called without `new` (mirrors emitCall).
	if id, ok := ex.Callee.(*ast.Identifier); ok && e.callableClasses[id.Name] {
		if _, local := e.lookup(id.Name); !local {
			if _, _, fn := e.resolveFuncRef(id.Name); !fn {
				return e.inferExprType(ast.NewNewExpression(id.Name, ex.Args, ex.GetPos()))
			}
		}
	}
	if c := e.receiverlessThisCall(ex); c != nil {
		return e.inferExprType(c)
	}
	// A call through a builtin declaration's @lower target: the same
	// decider emission reads (TDD-00230 P3.2).
	if l, ok := e.loweredCall(ex); ok {
		// An intrinsic without its own answer keeps the per-kind
		// inference below.
		if t := l.resultType(); t.IR != "" || l.intrinsic == "" {
			return t
		}
	}
	// Optional call `f?.(...)` — mirror emitOptionalCalleeCall.
	if ex.Optional {
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok && isUnconstrainedDynamic(e.inferExprType(mem.Object)) {
			return TypeAny
		}
		plain := *ex
		plain.Optional = false
		return optionalCallResultType(e.classifyOptionalCallee(ex.Callee), e.inferExprType(&plain))
	}
	// `a?.m(...)` is `RetType | undefined` — mirror emitOptionalCall, which
	// wraps a pointer (non-array) receiver's optional call as `T | undefined`.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && mem.Optional {
		objTy := e.inferExprType(mem.Object)
		plain := ast.NewCallExpression(&ast.MemberExpression{Object: mem.Object, Property: mem.Property}, ex.Args, ex.GetPos())
		plain.TypeArgs = ex.TypeArgs
		inner := e.inferExprType(plain)
		if (objTy.IR == "ptr" && !objTy.IsArray) || isNullableScalar(objTy) {
			return optionalCallResultType(optCalleeValue, inner)
		}
		return inner
	}
	// An immediately-invoked arrow / function expression returns its own
	// return type (`((x: number) => x === 1)(1)` is a boolean), and so
	// does a call of a call's function result (`mk()()`).
	if fe, ok := ex.Callee.(*ast.FunctionExpression); ok && fe.IsGenerator {
		return TypeAny // its generator, boxed (the constructor's call is dynamic)
	}
	switch ex.Callee.(type) {
	case *ast.ArrowFunction, *ast.FunctionExpression, *ast.CallExpression:
		if ft := e.inferExprType(ex.Callee); ft.IsFunc {
			if ft.FuncRetType == nil {
				return TypeVoid
			}
			return *ft.FuncRetType
		}
	}
	// Static method call: ClassName.staticMethod(args) (TDD-00009 Stage 4).
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok {
			if info, found := e.classes[id.Name]; found {
				if sig, ok := info.StaticMethodSigs[mem.Property]; ok {
					return taskTaggedRet(sig)
				}
				if info.HostBase != nil && info.HostBase.IsPromise {
					// Promise's static through the class: a C (emitPromiseSubclassStatic).
					pex := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Promise", mem.GetPos()), mem.Property, mem.GetPos()), ex.Args, ex.GetPos())
					if t, ok := e.promiseSubclassStaticType(mem.Property, pex); ok {
						if mem.Property == "resolve" && len(ex.Args) > 0 {
							if at := e.inferExprType(ex.Args[0]); at.IsClass && at.ClassName == id.Name {
								return at
							}
						}
						return promiseSubclassOf(info.Ty, t)
					}
				}
				if host := info.HostBase; host != nil && host.IsArray {
					// Array's (a typed array's) static through the class
					// (emitArrayClassStatic).
					switch {
					case mem.Property == "isArray" && !host.IsTypedArray:
						return TypeBool
					case (mem.Property == "from" || mem.Property == "of") && host.IsTypedArray:
						return *host
					case mem.Property == "from" || mem.Property == "of":
						call := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Array", mem.GetPos()), mem.Property, mem.GetPos()), ex.Args, ex.GetPos())
						call.Intrinsic = "Array." + mem.Property
						return e.inferExprType(call)
					}
				}
				if t, ok := e.forwardMethodRet[forwardMethodKey(id.Name, mem.Property, true)]; ok {
					return t // a sibling not registered yet
				}
			}
			if _, generic := e.genericClasses[id.Name]; generic && !e.isShadowedByLocal(id.Name) {
				if info, found := e.classes[genericStaticsClass(id.Name)]; found {
					if sig, ok := info.StaticMethodSigs[mem.Property]; ok {
						return taskTaggedRet(sig)
					}
				}
			}
		}
	}
	// Class method call: instance.method(args). Checked before every
	// mem.Property-name-based check below (console/String/Math/... and
	// the big built-in-name chain further down), since several of those
	// match purely on property name with no receiver-type guard — a
	// user-defined method named e.g. "slice" or "push" must not be
	// shadowed by the array/string built-in of the same name.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if objTy := e.inferExprType(mem.Object); objTy.IsClass {
			if t, ok := e.genericInstMethodRet(objTy.ClassName, mem.Property); ok {
				return t // an instantiation no construction has made yet
			}
			if info, ok := e.classes[objTy.ClassName]; ok {
				if sig, ok := info.MethodSigs[mem.Property]; ok {
					return e.genericMethodResult(ex, objTy, mem.Property, e.thisRetType(sig, objTy))
				}
				if t, ok := e.forwardMethodRet[forwardMethodKey(objTy.ClassName, mem.Property, false)]; ok {
					return t // a sibling not registered yet
				}
				// A field holding a function (mirrors emitClassFieldCall).
				if _, fty, isField := info.Ty.FieldIndex(mem.Property); isField {
					if isUnconstrainedDynamic(fty) {
						return TypeAny
					}
					if fty.IsFunc && !fty.IsDynamic {
						if fty.FuncRetType == nil {
							return TypeVoid
						}
						return *fty.FuncRetType
					}
				}
				if info.HostBase != nil && info.HostBase.IsPromise && (mem.Property == "then" || mem.Property == "catch" || mem.Property == "finally") {
					// A species construction: the class, settling as Promise's
					// method on its body would (emitPromiseSpecies).
					host := *info.HostBase
					if objTy.PromiseType != nil {
						host = PromiseOf(*objTy.PromiseType)
						host.PromiseTask = true
					}
					rt := e.inferExprType(ast.NewCallExpression(ast.NewMemberExpression(e.hostInferReceiver(host, mem.GetPos()), mem.Property, mem.GetPos()), ex.Args, ex.GetPos()))
					return promiseSubclassOf(objTy, rt)
				}
				if info.HostBase != nil {
					// The host base's method (mirrors emitClassCall).
					return e.inferExprType(ast.NewCallExpression(ast.NewMemberExpression(e.hostInferReceiver(*info.HostBase, mem.GetPos()), mem.Property, mem.GetPos()), ex.Args, ex.GetPos()))
				}
			}
		}
	}
	// Generator construction (TDD-00061/ADR-00172) — `gen(args)`'s own
	// type is the constructed instance's GenTy, not ElemTy (that's
	// `.next()`'s own result's `value` field type instead, handled by
	// the member-expression case just below).
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		if info, found := e.lookupGenerator(id.Name); found {
			return info.GenTy
		}
		// Static `eval("<expr>")` — its type is the parsed expression's type
		// (matches emitStaticEval). Checked after user bindings so a
		// user-defined `eval` still wins, mirroring emitCall's dispatch.
		if id.Name == "eval" {
			if _, isUser := e.lookup(id.Name); !isUser {
				if t, ok := e.inferStaticEvalType(ex.Args); ok {
					return t
				}
			}
		}
	}
	// req.stream() (TDD-00097 Stage 5b) — must match emitRequestStream.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && mem.Property == "stream" {
		if e.inferExprType(mem.Object).IsRequest {
			return ReadableStreamType(TypedArrayType("uint8"))
		}
	}
	// Function.prototype.call/.apply on a function value (TDD-00137) →
	// the function's own return type.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && (mem.Property == "call" || mem.Property == "apply") {
		if objTy := e.inferExprType(mem.Object); objTy.IsFunc {
			if objTy.FuncRetType != nil {
				return *objTy.FuncRetType
			}
			return TypeVoid
		}
	}
	// Function.prototype.bind (TDD-00137 Stage C) → a new function value
	// with the leading bound parameters removed.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && mem.Property == "bind" {
		if t, ok := e.boundFuncValueType(e.inferExprType(mem.Object), len(ex.Args)); ok {
			return t
		}
	}
	// Stream/reader/controller method results (TDD-00097 Stage 1) —
	// checked before the property-name-based chains, same as generators.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if t, ok := e.streamCallType(ex, mem); ok {
			return t
		}
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		// `/** @value */` flat array: push returns the new length.
		if objTy := e.inferExprType(mem.Object); objTy.IsFlatArray && mem.Property == "push" {
			return TypeF64
		}
		// FinalizationRegistry method result types (TDD-00163).
		if objTy := e.inferExprType(mem.Object); objTy.IsFinalizationRegistry {
			switch mem.Property {
			case "register":
				return TypeUndefined // register() evaluates to `undefined`
			case "unregister":
				return TypeBool
			}
		}
	}
	// An Array, Map or Set iterator's own methods (emitCollIterCall).
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if objTy := e.inferExprType(mem.Object); objTy.IsCollIter {
			switch mem.Property {
			case "next":
				return collIterResultType(objTy)
			case "toArray":
				return ArrayOf(collIterElemType(objTy))
			case "reduce":
				// The lib's Iterator.prototype.reduce returns a box.
				return TypeAny
			}
		}
	}
	// gen.next(value)'s own result type ({value: T, done: bool}) —
	// checked before the generic member-expression dispatch further
	// down, same as every other type-tag-gated case there.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && (mem.Property == "next" || mem.Property == "throw" || mem.Property == "return") {
		if objTy := e.inferExprType(mem.Object); objTy.IsGenerator {
			res := genNextResultType(*objTy.GeneratorElemType)
			if !objTy.GeneratorIsAsync {
				res = genUserResultType(*objTy.GeneratorElemType)
			}
			// An async generator's .next() returns Promise<{value,done}> (TDD-00085);
			// .throw()/.return() share the same {value,done} shape (TDD-00086).
			if objTy.GeneratorIsAsync {
				pt := PromiseOf(res)
				pt.PromiseTask = true
				return pt
			}
			return res
		}
	}
	// If calling a named function, use its registered return type (handles async too).
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		if _, sig, found := e.resolveFuncRef(id.Name); found {
			// Every async fn returns a real task-shaped promise (TDD-00084
			// Part A): a may-suspend one via a fiber, a non-suspending one via
			// the inline catch-and-settle wrapper. Tag the type so a later
			// `await`/`.then` (including through a variable) takes the task path.
			if sig.IsAsync && sig.RetType.IsPromise {
				rt := sig.RetType
				rt.PromiseTask = true
				return rt
			}
			return sig.RetType
		}
		// A generic function (TDD-00010 V1) is never itself in e.funcs
		// — infer what its return type *would* be for this call's own
		// argument types, purely (see genericCallReturnType's doc
		// comment for why this must never trigger real emission).
		if decl, found := e.genericFuncs[id.Name]; found {
			if ty, ok := e.genericCallReturnType(decl, ex.Args, ex.TypeArgs); ok {
				return ty
			}
		}
		// Calling a closure-typed variable (e.g. a const-bound arrow
		// function) — same fallback resolveCallback (emit_func.go)
		// already uses, so a call's result is correctly typed regardless
		// of whether the callee is a named declaration or a value.
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc && sym.Ty.FuncRetType != nil {
			return *sym.Ty.FuncRetType
		}
		// A call to a capturing nested function not yet bound (letrec
		// hoisting, TDD-00129 Stage 2 — including the self-recursive call
		// inside its own body while its return type is being inferred):
		// its type is the signature the on-demand emission will produce.
		// Must mirror the Identifier case above; buildFunctionSig's
		// recursion guard turns a cycle into sigInferCycleHit instead of
		// unbounded recursion.
		if len(e.nestedFuncScopes) > 0 {
			for fd := range e.nestedFuncScopes[len(e.nestedFuncScopes)-1].capturing {
				if fd.Name == id.Name {
					return e.buildFunctionSig(fd).RetType
				}
			}
		}
		if e.isShadowedByLocal(id.Name) {
			return TypeI64 // matches this function's own generic identifier-call fallback below
		}
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		// Buffer read*/write* accessors — must match emitBufferAccessor.
		if k, ok2 := bufferAccessorKindFor(mem.Property); ok2 {
			if e.inferExprType(mem.Object).IsBuffer {
				if k.write {
					return TypeI64 // offset + width
				}
				if k.float {
					return TypeF64
				}
				if k.bigint {
					return BigIntType()
				}
				return TypeI64
			}
		}
		// TS namespace member call (TDD-00095) — must match emitCall.
		if id, ok2 := mem.Object.(*ast.Identifier); ok2 {
			if members, nsName := e.namespaceMembers(id.Name); members != nil && members[mem.Property] {
				if !e.isShadowedByLocal(id.Name) {
					return e.inferExprType(ast.NewCallExpression(ast.NewIdentifier(ast.NamespaceMangle(nsName, mem.Property), ex.GetPos()), ex.Args, ex.GetPos()))
				}
			}
		}
		// Nested-namespace member call `A.B.f(...)` (TDD-00148 V3).
		if members, nsName := e.namespaceByChain(mem.Object); members != nil && members[mem.Property] {
			return e.inferExprType(ast.NewCallExpression(ast.NewIdentifier(ast.NamespaceMangle(nsName, mem.Property), ex.GetPos()), ex.Args, ex.GetPos()))
		}
		if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
			if mem.Property == "send" {
				return TypeBool
			}
		}

		// XMLHttpRequest response-header reads (ADR-00490).
		if mem.Property == "getResponseHeader" || mem.Property == "getAllResponseHeaders" {
			if e.inferExprType(mem.Object).IsXHR {
				return TypePtr
			}
		}
		// res.write(chunk) on a ServerResponse returns Node's boolean
		// backpressure signal (TDD-00131) — always true here, since the
		// buffered response sink never fills. Every other res method
		// (writeHead/setHeader/end/cork/uncork) is void — matching
		// emitServerResponseMethod's own returns, so an expression-bodied
		// handler `(req, res) => res.end(body)` infers a void return type
		// and emits `ret void` instead of a value-less `ret i64`.
		if e.inferExprType(mem.Object).IsServerResponse {
			if mem.Property == "write" {
				return TypeBool
			}
			return TypeVoid
		}
		if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Promise" {
			if t, ok := e.promiseStaticType(mem.Property, ex); ok {
				return t
			}
		}
		if t, ok := e.promiseThenType(ex, mem); ok {
			return t
		}
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		// objTy resolves mem.Object's type — via the identifier/lookup
		// path when possible (kept as the primary path since a looked-up
		// Symbol is what every other Map/Set method-call site already
		// relies on), falling back to inferExprType for anything else
		// (e.g. req.query.get(...), where mem.Object — req.query — is
		// itself a MemberExpression, not a bare identifier). Mirrors the
		// same identifier-or-inferExprType fallback the "size" property
		// case above already uses.
		if t, ok := e.inferCollectionCallType(ex, mem); ok {
			return t
		}
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		// A method of whatever an `any` holds is `any` (mirrors
		// emitDynAnyMethodCall).
		if _, arr := dynArrayMethods[mem.Property]; (arr || dynStringMethods[mem.Property] || dynNumberMethods[mem.Property] || mem.Property == "hasOwnProperty") && isUnconstrainedDynamic(e.inferExprType(mem.Object)) {
			return TypeAny
		}
		// A plain object's own method (an interface's `map<U>(…)`) is
		// what the call runs, not the built-in of the same name.
		if objTy := e.inferExprType(mem.Object); objTy.IsObject && !objTy.IsClass && !objTy.IsArray && !objTy.IsMap {
			for _, f := range objTy.UserFields() {
				if f.Name == mem.Property && f.Ty.IsFunc && f.Ty.FuncRetType != nil {
					return *f.Ty.FuncRetType
				}
			}
		}
		if t, ok := e.inferMethodNameType(ex, mem); ok {
			return t
		}
	}
	// General fallback: calling a plain (non-class) object's own
	// function-typed field — `obj.getHandler()` where `getHandler` is
	// declared `() => T`, not a hardcoded built-in method name above.
	// The MemberExpression case's own general object-field fallback
	// already resolves ex.Callee's type correctly (including a field
	// access chained off another call, e.g. `f().getHandler()`); this
	// just asks for that and unwraps the function type's return type.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if calleeTy := e.inferExprType(mem); calleeTy.IsFunc && calleeTy.FuncRetType != nil {
			return *calleeTy.FuncRetType
		}
		// Any other method call on a bare any/unknown receiver reaches
		// emitCall's dynamic dispatch, whose result is dynamic.
		if isUnconstrainedDynamic(e.inferExprType(mem.Object)) {
			return TypeAny
		}
	}
	return TypeI64
}
