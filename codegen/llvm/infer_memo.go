// infer_memo.go — typeOfExpr memoises member/call nodes for the duration of
// one outermost inference.
//
// typeOfExpr decides a member call's type by asking "what is the receiver?"
// once per builtin family it knows (dozens of `e.typeOfExpr(mem.Object)`
// probes per call node). Each probe re-infers the whole receiver, so a chain
// costs probes^depth: a five-link `.then` chain took ~52 s to compile, one link
// more would not finish. Inference emits no code and reads only the scope
// stack, so within one outermost call a node's type is a pure function of the
// scopes in force — which is exactly what the memo is keyed on:
//
//   - it lives only while an inference is running (cleared when the outermost
//     call returns), so nothing emission does in between — declarations,
//     flow narrowing, a function body's fresh scope stack — can be observed
//     stale;
//   - inference itself pushes a scope to type a callback body with its
//     parameters bound (callbackReturnType, the arrow case): pushScope opens a
//     fresh layer, popScope drops it, define clears the current one;
//   - a layer remembers the scope stack it was filled under (depth + identity
//     of the function frame) and is ignored under any other, which covers the
//     helpers that swap e.scopes wholesale.
package llvm

import (
	"KlainMainLang/binder"
	"strings"

	"reflect"

	"KlainMainLang/ast"
)

// inferState is what type inference keeps while it runs: the memo
// layers, how deep the outermost inference is, and the guards that stop a
// recursive question (a signature, a typeof, an annotation, a return).
type inferState struct {
	// newClassArgs: a generic class construction's type arguments as the
	// checker inferred them, by the `new`'s class and position.
	storesPresentBusy map[*ast.Identifier]bool // storesPresent's recursion guard
	// sigInferInProgress holds the declarations whose unannotated return
	// type buildFunctionSig is currently inferring. Re-entry for the same
	// declaration (a recursive nested function whose first reachable return
	// is the recursive call itself, reached back through the letrec
	// identifier inference or inferBlockReturnExpr) keeps the void default
	// instead of recursing without bound.
	sigInferInProgress map[*ast.FunctionDeclaration]bool
	// sigInferCycleHit is set when that guard trips, telling
	// funcBodyReturnType the candidate return it just inferred was
	// poisoned by the cycle so it should try the next return statement
	// (`if (n > 0) return f(n - 1); return p + "done";` must infer from the
	// base case, not void).
	sigInferCycleHit bool
	// typeofResolving guards against infinite recursion while resolving a
	// self- or mutually-referential `typeof value` query (ADR-00389).
	typeofResolving map[string]bool
	// anyHeldMemo caches anyHeldDecl.
	anyHeldMemo map[*ast.VarDeclaration]bool
	// anyHeldEmitting holds the anyHeldDecl bindings being emitted, under
	// the `any` annotation emitVarDecl gives them for the while.
	anyHeldEmitting map[*ast.VarDeclaration]bool
	inferDepth      int                          // nesting of memoised typeOfExpr calls (infer_memo.go)
	annotationBusy  map[ast.TypeNode]bool        // annotations annotationType is asking the checker about
	returnBusy      map[*ast.BlockStatement]bool // bodies funcBodyReturnType is asking the checker about
	inferMemo       []inferMemoLayer
}

type inferMemoLayer struct {
	depth int                     // len(e.scopes) the layer was opened under
	base  uintptr                 // identity of e.scopes[0].syms (0 when there is no scope)
	full  map[ast.Expression]Type // typeOfExpr's answers
}

func (e *Emitter) inferScopeIdentity() (int, uintptr) {
	if len(e.scopes) == 0 {
		return 0, 0
	}
	return len(e.scopes), reflect.ValueOf(e.scopes[0].syms).Pointer()
}

func (e *Emitter) inferMemoOpenLayer() {
	d, b := e.inferScopeIdentity()
	e.inferMemo = append(e.inferMemo, inferMemoLayer{depth: d, base: b})
}

// inferMemoLayerTop returns the current layer when it was filled under the
// scope stack now in force, nil otherwise. Its maps are made on first store.
func (e *Emitter) inferMemoLayerTop() *inferMemoLayer {
	if e.inferDepth == 0 || len(e.inferMemo) == 0 {
		return nil
	}
	top := &e.inferMemo[len(e.inferMemo)-1]
	if d, b := e.inferScopeIdentity(); d != top.depth || b != top.base {
		return nil
	}
	return top
}

// inferMemoScopePushed / Popped / Defined are called by pushScope, popScope and
// define; they do nothing outside a running inference.
func (e *Emitter) inferMemoScopePushed() {
	if e.inferDepth > 0 {
		e.inferMemoOpenLayer()
	}
}

func (e *Emitter) inferMemoScopePopped() {
	if e.inferDepth > 0 && len(e.inferMemo) > 1 {
		e.inferMemo = e.inferMemo[:len(e.inferMemo)-1]
	}
}

func (e *Emitter) inferMemoDefined() {
	if e.inferDepth > 0 && len(e.inferMemo) > 0 {
		e.inferMemo[len(e.inferMemo)-1].full = nil
	}
}

// typeOfExpr is expr's type, without emitting code: for an expression of
// the program, the checker's type mapped by reprOf (one it leaves
// unanswered is an internal compiler error); for a node codegen builds,
// which stands for no expression of the program, the type recorded when it
// was built (typed), else its binding's or its parts' (builtNodeType). A
// built name, member read or call is memoised, since its parts are typed
// again for each enclosing node.
func (e *Emitter) typeOfExpr(expr ast.Expression) Type {
	if c, ok := expr.(*ast.CallExpression); ok && (c.Intrinsic == ast.ModuleNamespaceIntrinsic || c.Intrinsic == ast.LiveBindingsIntrinsic) {
		return TypeAny // a module namespace object (moduleNamespaceRule)
	}
	if c, ok := expr.(*ast.CallExpression); ok {
		if _, super := c.Callee.(*ast.SuperExpression); super {
			// A super call evaluates to the `this` it initializes.
			return e.typeOfExpr(ast.NewThisExpression(c.GetPos()))
		}
	}
	switch expr.(type) {
	case *ast.CallExpression, *ast.MemberExpression, *ast.Identifier:
	default:
		return e.typeOfExprFull(expr)
	}
	if e.inferDepth == 0 {
		e.inferMemo = e.inferMemo[:0]
		e.inferDepth++
		e.inferMemoOpenLayer()
		ty := e.typeOfExprFull(expr)
		e.inferDepth--
		e.inferMemo = e.inferMemo[:0]
		return ty
	}
	if l := e.inferMemoLayerTop(); l != nil {
		if ty, ok := l.full[expr]; ok {
			return ty
		}
	}
	e.inferDepth++
	ty := e.typeOfExprFull(expr)
	e.inferDepth--
	if l := e.inferMemoLayerTop(); l != nil {
		if l.full == nil {
			l.full = map[ast.Expression]Type{}
		}
		l.full[expr] = ty
	}
	return ty
}

func (e *Emitter) typeOfExprFull(expr ast.Expression) Type {
	ty := e.typeOfExprRules(expr)
	if e.shadowOracle != nil && switchedExprKind(expr) {
		// Every answer, the rules' that run before the checker's included,
		// against the checker's.
		e.shadowExprType(expr, ty)
	}
	return ty
}

func (e *Emitter) typeOfExprRules(expr ast.Expression) Type {
	if call, ok := expr.(*ast.CallExpression); ok {
		if mem, ok := call.Callee.(*ast.MemberExpression); ok && e.combinatorCallOn(mem, call.Args) != "" {
			return TypeAny // the receiver's promise (emitCombinatorCallOn)
		}
	}
	if ix, ok := expr.(*ast.IndexExpression); ok {
		// An element of a binding held as an array of boxes (a rest
		// parameter its context types as a tuple) is a box.
		if id, ok := ix.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsArray() && !sym.Ty.IsTuple() && sym.Ty.ElemType != nil && isUnconstrainedDynamic(*sym.Ty.ElemType) {
				return TypeAny
			}
		}
	}
	if switchedExprKind(expr) {
		o := e.reprOracle()
		if o == nil && e.foreignThisBody && !e.foreignTainted(expr) {
			// A body compiled for a foreign receiver differs from the
			// checker's only in `this`: a node no `this` value reaches is
			// the checker's.
			e.foreignThisBody = false
			o = e.reprOracle()
			e.foreignThisBody = true
		}
		if o != nil {
			if ty, ok := o.ExprType(expr); ok {
				if nb, isBuf := expr.(*ast.NewArrayBufferExpression); isBuf && ty.IsArrayBuffer() {
					// A `maxByteLength` option makes the buffer growable: a
					// fact about this value, not its type.
					ty.BufferGrowable = nb.MaxByteLength != nil
				}
				return ty
			}
		}
		if _, isThis := expr.(*ast.ThisExpression); isThis {
			// `this` without a checker answer (a synthesized field
			// initializer's `this.f = …`; a function expression's own
			// `this`) is what the function's calling convention binds it
			// to; unbound (a pre-pass over a plain function, whose `this`
			// is bound dynamic when it is emitted), any.
			if sym, ok := e.lookup("this"); ok {
				return sym.Ty
			}
			return TypeAny
		}
		// No checker fallback remains: a node the checker has no answer
		// for (code built after parsing, a synthesized helper or a static
		// eval's text; or a checker gap, which the shadow report lists)
		// is typed from its binding, the dispatch its emission runs, or
		// its syntax (builtNodeType).
		if t, ok := e.builtTypes[expr]; ok {
			return t
		}
		ft := e.builtNodeType(expr)
		if o := e.reprOracle(); o != nil && o.c.InProgram(expr) && !e.loweredNodes[expr] && !virtualModuleMarker(expr) {
			e.shadowSwitchFallback(expr, ft)
			e.typeGap(nodeKind(expr), expr.GetPos())
		}
		return e.inferRefined(expr, ft)
	}
	// A kind the checker does not type (a spread, a class expression):
	// a preamble-built node's own type, else the number default.
	if t, ok := e.inferBuiltPreamble(expr); ok {
		return e.inferRefined(expr, t)
	}
	return e.inferRefined(expr, TypeI64)
}

// builtNodeType is the type of a node codegen builds: its binding's, the
// registered member or signature its parts reach, or its syntax's.
func (e *Emitter) builtNodeType(expr ast.Expression) Type {
	if t, ok := e.inferBuiltPreamble(expr); ok {
		return t
	}
	switch x := expr.(type) {
	case *ast.Identifier:
		return e.identStorageType(x)
	case *ast.MemberExpression:
		return e.builtMemberType(x)
	case *ast.CallExpression:
		return e.builtCallType(x)
	case *ast.IndexExpression:
		return e.builtIndexType(x)
	}
	return e.syntacticExprType(expr)
}

// inferRefined applies what flow and the checker's resolution add to an
// answer neither the mapping nor the checker gave directly.
func (e *Emitter) inferRefined(expr ast.Expression, ty Type) Type {
	// A union-typed property read the checker narrows (`typeof o.v ===
	// "object" ? o.v.slice() : …`) reads as that member (mirrors emitMember).
	if m, ok := expr.(*ast.MemberExpression); ok && ty.IsDynamic() && len(ty.UnionMembers) > 0 {
		if nt, ok := e.checkerNarrowedUnion(m, ty); ok {
			ty = nt
		}
	}
	// A nullable property read flow proves present (mirrors emitExpr).
	if m, ok := expr.(*ast.MemberExpression); ok && e.checkerPresentRef(m, ty) {
		ty = ty.withoutNullable()
	}
	// A call answered `any` (an implementation signature behind overloads)
	// whose overload the checker resolves to a primitive is that primitive
	// (mirrors emitExpr's call result).
	if call, ok := expr.(*ast.CallExpression); ok && isUnconstrainedDynamic(ty) && ty.DynPropTy == nil {
		if pt, ok := e.checkerPrimitive(call); ok {
			ty = pt
		}
	}
	// A method read before its class's methods are registered (a getter's
	// `return this.m`, inferred while the class registers) falls to the
	// integer default; the checker's function type is the member's.
	if m, ok := expr.(*ast.MemberExpression); ok && reprKey(ty) == reprKey(TypeI64) {
		if o := e.reprOracle(); o != nil {
			if ct, ok := o.ExprType(m); ok && ct.IsFunc() {
				// A method reading `arguments` takes its implicit rest, as its
				// signature is built (maybeAddArgumentsRestParam).
				if fd, ok := o.c.MemberDecl(m).(*ast.FunctionDeclaration); ok && fd.Body != nil && !ct.FuncHasRest {
					if ps := maybeAddArgumentsRestParams(fd.Params, fd.Body.Body); len(ps) > len(fd.Params) || hasArgumentsRestParamSlice(fd.Params) {
						ct.FuncParams = append(append([]Type(nil), ct.FuncParams...), ArrayOf(TypeAny))
						ct.FuncHasRest = true
					}
				}
				ty = ct
			}
		}
	}
	if m, ok := expr.(*ast.MemberExpression); ok && ty.IR == "i64" && !ty.Float && !ty.IsDynamic() && !isNullableScalar(ty) && e.builtinCount(m) {
		ty = TypeF64 // emitExpr's countToNumber
	}
	if e.shadowOracle != nil {
		e.shadowExprType(expr, ty)
	}
	return ty
}

// methodRecordRead reports an array's or a string's method read as a
// value (rt the receiver's type): its prototype's record, a box.
func (e *Emitter) methodRecordRead(m *ast.MemberExpression) bool {
	if m.Optional || e.unboundName(m.Object) || e.enumName(m.Object) {
		return false
	}
	return e.protoMethodRecordOf(m, e.typeOfExpr(m.Object))
}

func (e *Emitter) protoMethodRecordOf(m *ast.MemberExpression, rt Type) bool {
	for _, class := range []string{valueProtoClass(rt), primMethodClass(rt)} {
		if _, ok := e.classMethod(class, m.Property); ok && class != "" {
			return true
		}
	}
	return false
}

// builtinCount reports whether ex reads a builtin count (`length`, `size`,
// `byteLength`, …) of an array, string, collection or buffer: a `number`,
// which is a double (TDD-00230 P3.3), however the runtime keeps it.
func (e *Emitter) builtinCount(ex *ast.MemberExpression) bool {
	if !builtinCountNames[ex.Property] {
		return false
	}
	t := e.typeOfExpr(ex.Object)
	if t.IsDynamic() || t.IsClass() || (t.IsObject() && !t.IsArray()) || t.IsDynamicObject() {
		return false
	}
	return t.IsArray() || t.IsTypedArray() || t.IsBuffer() || t.IsArrayBuffer() || t.IsMap() || t.IsSet() || isStringTy(t)
}

// builtinCountNames are the members builtinCount looks at.
var builtinCountNames = map[string]bool{"length": true, "size": true, "byteLength": true, "byteOffset": true, "maxByteLength": true}

// syntacticExprType is the type of a settled kind's node the checker never
// saw, from its syntax alone (the representation emission produces).
func (e *Emitter) syntacticExprType(expr ast.Expression) Type {
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		if ex.IsBigInt {
			return BigIntType()
		}
		return TypeF64
	case *ast.BooleanLiteral:
		return TypeBool
	case *ast.StringLiteral, *ast.TemplateLiteral:
		return TypePtr
	case *ast.NullLiteral:
		if ex.IsUndefined {
			return TypeUndefined
		}
		return TypeNull
	case *ast.NewArrayExpression:
		return ArrayOf(newArrayElemType(e, ex))
	case *ast.NewTransformStreamExpression:
		inTy, outTy := TypeAny, TypeAny
		if ex.InType != nil {
			inTy = e.annotationType(ex.InType)
		}
		if ex.OutType != nil {
			outTy = e.annotationType(ex.OutType)
		}
		return Type{IR: irPtr, Host: hostTransformStream, StreamChunk: &inTy, StreamOut: &outTy}
	case *ast.NewWritableStreamExpression:
		if ex.ChunkType != nil {
			return WritableStreamType(e.annotationType(ex.ChunkType))
		}
		return WritableStreamType(TypeAny)
	case *ast.NewReadableStreamExpression:
		if ex.ChunkType != nil {
			return ReadableStreamType(e.annotationType(ex.ChunkType))
		}
		return ReadableStreamType(TypeAny)
	case *ast.NonNullExpression:
		// `expr!` strips null/undefined from the operand's type (TDD-00187).
		t := e.typeOfExpr(ex.Arg)
		t.setNullable(false)
		t.setUndefined(false)
		return t
	case *ast.UnaryExpression:
		if ex.Op == "delete" {
			return TypeBool // whether the key is gone (emitUnary)
		}
		switch ex.Op {
		case "typeof":
			return TypePtr
		case "!":
			return TypeBool
		case "-":
			// Unary - preserves the operand's numeric type (i64 or float); any
			// other operand is ToNumber'd to a double (emitUnary).
			at := e.typeOfExpr(ex.Arg)
			if at.IsBigInt() || at.IsDynamic() || at.Float || numericIntRepr(at) {
				return at
			}
			if isNullableScalar(at) {
				return e.typeOfExpr(&ast.UnaryExpression{Op: "+", Arg: ex.Arg})
			}
			return TypeF64
		case "+":
			// Unary + is ToNumber: a numeric operand keeps its type, anything
			// else (string, boolean, null/undefined, any, object) becomes a
			// double (mirrors emitUnaryPlus).
			at := e.typeOfExpr(ex.Arg)
			if at.Float || (at.IsInteger() && at.IR != "i1" && !at.IsDynamic() && !isNullableScalar(at)) {
				return at
			}
			return TypeF64
		case "~":
			// ToInt32(x) ^ -1 as a Number (emitUnary); a bigint stays one.
			if at := e.typeOfExpr(ex.Arg); at.IsBigInt() {
				return at
			}
			return TypeF64
		}
		return TypeUndefined // `void x`
	case *ast.SequenceExpression:
		// The comma operator's value (and type) is its last operand's.
		if len(ex.Exprs) > 0 {
			return e.typeOfExpr(ex.Exprs[len(ex.Exprs)-1])
		}
		return TypeI64
	case *ast.NewCompressionStreamExpression:
		return compressionStreamType(ex.Decompress)
	case *ast.NewMapExpression:
		// Mirrors emitNewMapValue's K/V resolution (mapKVTypes): explicit
		// `<K, V>` wins, else infer from an initial `[K, V][]` entries array,
		// else the string-key/number-value defaults.
		keyTy, valTy := e.mapKVTypes(ex.KeyType, ex.ValType, ex.Init)
		return MapType(keyTy, valTy)
	case *ast.NewSetExpression:
		// Mirrors emitNewSetValue's shape without emitting the initializer.
		// Without type arguments the element is the checker's (an iterator
		// over the set reads it), else the string default.
		elemTy := TypePtr
		if ex.ElemType != nil {
			elemTy = e.annotationType(ex.ElemType)
		} else if c := e.front(); c != nil && ex.Init != nil {
			if ct := c.TypeOf(ex); !c.Unanswered(ct) {
				if rt, ok := e.repr().of(ct); ok && rt.IsSet() && rt.MapKey != nil {
					elemTy = *rt.MapKey
				}
			}
		}
		return SetType(elemTy)
	case *ast.NewWeakMapExpression:
		keyTy, valTy := e.weakMapKV(ex, nil)
		return WeakMapType(keyTy, valTy)
	case *ast.NewWeakSetExpression:
		return WeakSetType(e.weakSetElem(ex))
	case *ast.NewErrorExpression:
		return errorObjType
	case *ast.ImportCallExpression:
		// A dynamic import yields Promise<{ ...exports }>, a task promise.
		pt := PromiseOf(e.importCallResultObjectType(ex))
		pt.PromiseTask = true
		return pt
	case *ast.NewArrayBufferExpression:
		ty := ArrayBufferType()
		if ex.Shared {
			ty = SharedArrayBufferType()
		}
		ty.BufferGrowable = ex.MaxByteLength != nil
		return ty
	case *ast.NewXMLHttpRequestExpression:
		return XMLHttpRequestType()
	case *ast.UpdateExpression:
		// `x++`/`--x` yield the stepped number (or bigint); a dynamic or
		// nullable-scalar operand goes through ToNumeric first, so the result
		// is a plain `number` (ADR-01061).
		t := e.typeOfExpr(ex.Arg)
		if t.IsBigInt() || (!t.IsDynamic() && !t.Nullable() && (t.Float || t.IsInteger())) {
			return t
		}
		return TypeF64
	case *ast.NewTargetExpression:
		// What emitNewTarget emits: the constructor read from the receiver
		// in a class constructor, else undefined or the function (any).
		pos := ex.GetPos()
		switch ex.Ctx {
		case ast.NewTargetConstructor:
			if _, ok := e.lookup("this"); ok {
				return e.typeOfExpr(ast.NewMemberExpression(ast.NewThisExpression(pos), "constructor", pos))
			}
		case ast.NewTargetFunction:
			if fn := e.newTargetFunc(); fn != "" && ex.FnID != 0 && ex.FnID == e.newTargetDecl.NewTargetID {
				if _, ok := e.lookup("this"); ok {
					return TypeAny
				}
			}
		}
		return e.typeOfExpr(ast.NewNullLiteral(true, pos))
	case *ast.YieldExpression:
		// A `yield` expression is the value `.next(v)` sends: TNext, `any`
		// (emitYieldExpression). `yield*` is the inner's return value.
		if !ex.Delegate {
			return TypeAny
		}
		// `yield*`: the value the inner iterator's done result carries,
		// which only the checker types; without it, any.
		return TypeAny
	case *ast.AwaitExpression:
		// Unwrap Promise<T> → T.
		argTy := e.typeOfExpr(ex.Argument)
		if argTy.IsPromise() {
			if argTy.PromiseType != nil {
				return *argTy.PromiseType
			}
			return TypeVoid
		}
		// A thenable class instance: what its `then` fulfills with.
		if sig, ok := e.thenableClass(argTy); ok {
			return thenableValueType(sig)
		}
		// `await` of a non-thenable is identity — its type is the argument's
		// own (e.g. Response.text() → string), matching emitAwait's pass-through.
		return argTy
	case *ast.TaggedTemplateExpression:
		// The desugaring emitExpr's own case uses: a tagged template's type is
		// its tag function's return type (TDD-00059).
		return e.typeOfExpr(e.desugarTaggedTemplate(ex))
	case *ast.AssignmentExpression:
		// A destructuring assignment's value is its right-hand side; the
		// object destructuring path holds a dynamic or computed-key source
		// boxed (emitDynDestructAssign).
		if ol, ok := ex.Left.(*ast.ObjectLiteral); ok && ex.Op == "=" {
			rt := e.typeOfExpr(ex.Right)
			if isUnconstrainedDynamic(rt) || ol.HasComputedKey() {
				return TypeAny
			}
			return rt
		}
		// An assignment's value is the value stored: the target's type
		// (`b += c` on a string `b` is a string).
		return e.typeOfExpr(ex.Left)
	case *ast.BinaryExpression:
		// Infer each operand's type exactly once, up front, and reuse it in
		// every branch below. Re-calling typeOfExpr(ex.Left/Right) separately
		// in the bigint check AND again in each operator branch made inference
		// cost O(2^depth) on a left-associative chain like `a + b + c + ...`
		// (each node re-inferred both children, which re-inferred theirs …) —
		// enough to peg a core for minutes on a deep expression, found as an
		// in-process hang while running the Test262 corpus. typeOfExpr does
		// no codegen and has no side effects, so inferring both operands eagerly
		// — even for `&&`/`||`/`??`, which may not need the right one — is free;
		// the bigint check already forced both anyway.
		lt := e.typeOfExpr(ex.Left)
		rt := e.typeOfExpr(ex.Right)
		// A dynamic operand makes an arithmetic result dynamic — the runtime
		// dispatch (TDD-00076 A2) yields a NaN-boxed word (`+` may
		// concatenate, others produce a boxed number); a comparison stays a
		// bool. TypeScript allows every operator on `any`.
		if isUnconstrainedDynamic(lt) || isUnconstrainedDynamic(rt) {
			switch ex.Op {
			case "===", "!==", "==", "!=", "<", ">", "<=", ">=":
				return TypeBool
			case "+":
				// With a string side it is concatenation: a string, as
				// emitBinary's ToString path produces.
				if isStringTy(lt) || isStringTy(rt) {
					return TypePtr
				}
				return TypeAny
			case "-", "*", "/", "%", "**":
				if anyArithIsNumber(ex.Op, lt, rt) {
					return TypeF64
				}
				return TypeAny
			}
		}
		// A mixed concrete scalar pair (`n * "4"`, `true + 1`) also routes
		// through the runtime dispatch under `-compat=js` — its result is a
		// boxed dynamic value too.
		if e.compatJS() {
			lk, rk := scalarTypeKind(lt), scalarTypeKind(rt)
			if lk != "" && rk != "" && lk != rk {
				switch ex.Op {
				case "<", ">", "<=", ">=":
					return TypeBool
				case "+":
					// `+` with a string side is always concatenation → string
					// (the pre-existing concat path emits it; only a
					// string-free mixed pair reaches the dynamic dispatch).
					if lk == "string" || rk == "string" {
						return TypePtr
					}
					return TypeAny
				case "-", "*", "/", "%", "**":
					return TypeAny
				}
			}
		}
		// Bare null/undefined in arithmetic/relational routes through the runtime
		// dispatch under -compat=js (null→0, undefined→NaN) — see emitBinary
		// (ADR-00901). Infer TypeAny / bool so the slot type agrees with the boxed
		// value emitBinary stores (otherwise `var x = null + undefined` infers a
		// string/ptr slot and the boxed result is mis-stored). A real-string side
		// keeps the concat result (string); object/array operands keep their paths.
		if e.compatJS() {
			lNullish := (lt.IsNull() || lt.IsUndefined()) && !lt.UncheckedIndex
			rNullish := (rt.IsNull() || rt.IsUndefined()) && !rt.UncheckedIndex
			lRealStr := isStringTy(lt) && !lNullish
			rRealStr := isStringTy(rt) && !rNullish
			if (lNullish || rNullish) && !lRealStr && !rRealStr &&
				!lt.IsObject() && !rt.IsObject() && !lt.IsArray() && !rt.IsArray() {
				switch ex.Op {
				case "<", ">", "<=", ">=":
					return TypeBool
				case "+", "-", "*", "/", "%", "**":
					return TypeAny
				}
			}
		}
		// A bigint operand makes an arithmetic/bitwise result a bigint (a
		// comparison stays a bool) — so `const x = 2n ** 53n + 1n` types as
		// bigint, not the i64/string the generic cases below would infer.
		if lt.IsBigInt() || rt.IsBigInt() {
			switch ex.Op {
			case "===", "!==", "==", "!=", "<", ">", "<=", ">=":
				return TypeBool
			default:
				return BigIntType()
			}
		}
		switch ex.Op {
		case "===", "!==", "==", "!=", "<", ">", "<=", ">=", "instanceof", "in":
			return TypeBool
		case "+":
			if isStringTy(lt) || isStringTy(rt) {
				return TypePtr
			}
			// A Date operand concatenates its date string (emitBinary).
			if lt.IsDate() || rt.IsDate() {
				return TypePtr
			}
			if lt.Float || rt.Float {
				// Mixed int/float promotes to double — must match
				// emitBinary's numeric-promotion rule.
				return TypeF64
			}
			return TypeI64
		case "-":
			// A Date operand is its time value: a number (emitBinary).
			if lt.IsDate() || rt.IsDate() {
				return TypeF64
			}
			if lt.Float || rt.Float {
				return TypeF64
			}
			return TypeI64
		case "*", "/", "%":
			// Same promotion rule as emitBinary: any float operand makes the
			// result a double; all-integer stays exact i64.
			if lt.Float || rt.Float {
				return TypeF64
			}
			return TypeI64
		case "**":
			// Exponentiation promotes to float if either operand is float,
			// otherwise stays this compiler's default i64 `number` — matching
			// emitBinary's own `**` result type.
			if lt.Float || rt.Float {
				return TypeF64
			}
			return TypeI64
		case "&&", "||":
			// Mirrors emitShortCircuit exactly: the value-preserving type when
			// there is one, else the bool the emitter falls back to.
			if ty, ok := andAbsentScalar(ex.Op, lt, rt); ok {
				return ty
			}
			if ty, ok := shortCircuitValueType(ex.Op, lt, rt); ok {
				return ty
			}
			return TypeBool
		case "??":
			return nullCoalesceType(lt, rt)
		case "&", "|", "^", "<<", ">>", ">>>":
			// JS bitwise/shift ops compute in the 32-bit integer domain but
			// return a Number (double) — TDD-00123 Stage 2. Must mirror
			// emitBinary/emitBitShift's sitofp-to-double result.
			return TypeF64
		}
	case *ast.SuperExpression:
		// `super` is bound in scope typed as the base class (emitClassMember), so
		// `super.method()` resolves against the base's method table — mirror that
		// here so a method returning `super.method()` infers the base method's
		// return type instead of the i64 default (ADR-00906).
		if sym, ok := e.lookup("super"); ok {
			if this, ok := e.lookup("this"); ok && !sym.Ty.IsClass() {
				// A class extending Array: its super's methods are Array's,
				// run on the instance's body (emitSuperMethodCall).
				if info, ok := e.classes[this.Ty.ClassName]; ok && info.HostBase != nil && info.HostBase.IsArray() {
					return *info.HostBase
				}
			}
			return sym.Ty
		}
	case *ast.NewExpression:
		if e.isGlobalObjectCtor(ex.ClassName, ex.Qualified) {
			return TypeAny // `new Object(…)`: a dynamic object
		}
		if e.newOfNamespace(ex) {
			return TypeAny // `new Math()`: its TypeError (emitNewExpression)
		}
		if e.isPrimWrapperCtor(ex.ClassName, ex.Qualified) {
			return TypeAny // a wrapper object is held boxed
		}
		if target, ok := e.constClassAlias(ex); ok {
			alias := *ex
			alias.ClassName = target
			return e.typeOfExpr(&alias) // `const K = C; new K(…)`
		}
		if instTy, ok := e.dynNewInstanceType(ex); ok {
			return instTy
		}
		// new Promise<T>(executor) → task Promise<T> (TDD-00087).
		if ex.ClassName == "Promise" {
			valTy := e.newPromiseValueType(ex)
			pt := PromiseOf(valTy)
			pt.PromiseTask = true
			return pt
		}
		// new DynamicLibrary(path) → the node:ffi handle (TDD-00164).
		if _, user := e.classes[ex.ClassName]; ex.ClassName == "Response" && !user && !ex.Qualified {
			return ResponseType()
		}
		// new FinalizationRegistry<T>(cb) → the registry handle (TDD-00163).
		if ex.ClassName == "FinalizationRegistry" {
			held := TypeI64
			if len(ex.TypeArgs) == 1 && ex.TypeArgs[0] != nil {
				held = e.annotationType(ex.TypeArgs[0])
			}
			if len(ex.Args) == 1 {
				if cbTy := e.typeOfExpr(ex.Args[0]); cbTy.IsFunc() && len(cbTy.FuncParams) == 1 {
					held = cbTy.FuncParams[0]
				}
			}
			return FinalizationRegistryType(held)
		}
		if info, ok := e.classes[ex.ClassName]; ok && info.Ty.IsObject() {
			if e.compatJS() && ex.Callee == nil && e.ctorMayOverride(ex.ClassName) {
				return TypeAny // its constructor may give another object
			}
			return info.Ty
		}
		// During class registration, a class's own unannotated-field inference
		// (`p = new C()`) can reference a sibling class whose ClassInfo already
		// exists in e.classes but whose `.Ty` is not finalized until a later pass
		// (a zero Type with no fields) — and, for a sibling not yet reached in the
		// topological pass, is absent entirely. Either way the nominal class type
		// published into e.interfaces (registerClassNamePlaceholders, then the
		// provisional in registerClasses) is a real `IsClass` object type that
		// canonicalizeClassTy re-resolves to its full field shape on demand at
		// each access. Returning it gives the field C's real object shape instead
		// of falling through to the zero-Type default, which produced an empty
		// struct-field IR and a "field access on non-object" on `obj.p.x`.
		if it, ok := e.interfaces[ex.ClassName]; ok && it.IsClass() {
			return it
		}
		// A vanilla-JS prototype-constructor instance is a dynamic object
		// (TDD-00155 Stage 4, `-compat=js`); a Proxy is one too (Stage 7).
		if ex.ClassName == "Proxy" {
			return TypeAny
		}
		if e.jsProtoCtor[ex.ClassName] {
			return TypeAny
		}
		// A generic class (TDD-00010 V1) is never itself in e.classes — ask
		// genericClassInstanceType for the shape its `new
		// ClassName<T>(...)` instantiation would have, purely (no IR
		// emission, no e.classes registration): typeOfExpr must never
		// trigger real emission as a side effect of merely asking "what
		// type is this."
		if genDecl, ok := e.genericClasses[ex.ClassName]; ok {
			if targs, ok := e.newTypeArgs(genDecl, ex); ok {
				subs := e.buildTypeArgSubs(genDecl.TypeParams, targs)
				if ty, err := e.genericClassInstanceType(genDecl, subs); err == nil {
					return ty
				}
			}
		}
	case *ast.AsExpression:
		if t, ok := e.nullAssertedType(ex); ok {
			return t // mirrors emitAsExpression
		}
		// `expr as T` (ADR-00929): a narrowing from a dynamic operand adopts the
		// asserted concrete type (`x as number` where `x: any` → number, so the
		// binding/operand it flows into is typed concretely); every other
		// assertion stays erased — the operand keeps its own type (ADR-00371).
		if lit, isLit := ex.Expr.(*ast.ArrayLiteral); isLit && ex.TypeAnnot != nil {
			if t := e.annotationType(ex.TypeAnnot); t.IsArray() && t.ElemType != nil && (t.ElemType.IsDynamic() || len(lit.Elements) == 0) {
				return t // mirrors emitAsExpression
			}
		}
		if lit, isLit := ex.Expr.(*ast.ObjectLiteral); isLit && ex.TypeAnnot != nil {
			if t := e.annotationType(ex.TypeAnnot); t.IsObject() && !t.IsDynamic() && !t.IsClass() && len(t.Fields) > 0 && literalKeysAreFields(lit, t) {
				return t // mirrors emitAsExpression
			}
		}
		if t, ok := e.tupleAsArray(ex); ok {
			return t
		}
		inner := e.typeOfExpr(ex.Expr)
		if !inner.IsDynamic() && inner.IR != "void" && ex.TypeAnnot != nil {
			t := e.annotationType(ex.TypeAnnot)
			if isUnconstrainedDynamic(t) {
				return t // the value boxed (mirrors emitAsExpression)
			}
			if e.nativeIfaceMembers[t.RefName] != nil && t.IR == inner.IR && t.StructIR() == inner.StructIR() {
				t.setNullable(inner.Nullable())
				return t // an interface over its native layout (mirrors emitAsExpression)
			}
		}
		if inner.IsDynamic() && ex.TypeAnnot != nil && e.keepsBox(ex) {
			return inner // mirrors emitAsExpression
		}
		if inner.IsDynamic() && ex.TypeAnnot != nil {
			// A box (any, or a union) asserted to a concrete type unboxes to it
			// (mirrors emitAsExpression).
			if isUnconstrainedDynamic(inner) {
				return e.annotationType(ex.TypeAnnot)
			}
			if t := e.annotationType(ex.TypeAnnot); !t.IsDynamic() || isUnconstrainedDynamic(t) {
				return t // a member, or `any`: the same box, retyped
			}
		}
		return inner
	case *ast.ConditionalExpression:
		// `cond ? scalar : null` is `T | null` (ADR-00538) — mirror
		// emitConditional so an unannotated `const x = b ? 3 : null` gets
		// nullable-scalar storage rather than a collapsed bare scalar.
		if nty, ok := ternaryNullableScalarType(e.typeOfExpr(ex.Consequent), e.typeOfExpr(ex.Alternate)); ok {
			return nty
		}
		// An array-valued ternary (mirrors emitConditionalArray).
		if aty, ok := e.conditionalArrayType(ex); ok {
			return aty
		}
		// A dynamic branch makes the whole ternary `any` (mirrors
		// emitConditionalAny).
		if e.typeOfExpr(ex.Consequent).IsDynamic() || e.typeOfExpr(ex.Alternate).IsDynamic() {
			return TypeAny
		}
		// Scalar branches of different kinds are their union (mirrors
		// emitConditional).
		if uTy, ok := ternaryUnion(e.typeOfExpr(ex.Consequent), e.typeOfExpr(ex.Alternate)); ok {
			return uTy
		}
		if nty, ok := ternaryNullablePointerType(e.typeOfExpr(ex.Consequent), e.typeOfExpr(ex.Alternate)); ok {
			return nty
		}
		if ternaryMixed(e.typeOfExpr(ex.Consequent), e.typeOfExpr(ex.Alternate)) {
			return TypeAny // mirrors emitConditional's boxed union
		}
		return e.typeOfExpr(ex.Consequent)
	case *ast.ObjectLiteral:
		// Bare literals are dynamic under `-compat=js` (matching emitExpr's
		// routing); hint-typed contexts never consult this default.
		if e.compatJS() {
			return TypeAny
		}
		return e.inferObjectType(ex)
	case *ast.ArrayLiteral:
		// TDD-00028: typeOfExpr previously had no case for a bare array
		// literal at all (falling through to the final TypeI64 default) —
		// harmless while array literals could only ever appear in
		// var-decl position (var-decl's own inference went through
		// inferArrayType directly, never through here), but a real gap now
		// that a literal can appear as a general sub-expression: without
		// this, inferArrayType's own first-element inference couldn't tell
		// a nested array-literal element apart from any other unresolvable
		// expression, silently mistyping it as i64 instead of correctly
		// recognizing (and then, since real array-of-arrays storage isn't
		// supported yet, cleanly rejecting) it as array-typed. See
		// emitArrayLiteralAggregate's nested-array guard.
		return e.inferArrayType(ex)
	case *ast.ArrowFunction:
		params := make([]Type, len(ex.Params))
		for i, p := range ex.Params {
			if p.Rest && p.Type == nil {
				// Same default rest-element type emitArrowFunctionWithHints
				// (emit_func.go) and buildFunctionSig (emitter.go) already
				// give an unannotated rest param — this duplicate
				// computation (see the comment on the return-type mirror
				// below) needs to agree, not fall into the bare-scalar
				// unannotated-param default just below.
				params[i] = untypedRestType()
			} else if dt, ok := e.paramDefaultType(p); ok {
				params[i] = dt // mirrors emitArrowFunctionWithHints
			} else if ct, ok := e.checkerContextualParam(ex, i); p.Type == nil && ok {
				params[i] = ct
			} else if st, ok := e.jsSiteParam(ex, i); p.Type == nil && ok {
				params[i] = st // its call sites' type (-compat=js)
			} else if p.Type == nil && e.front() != nil {
				params[i] = TypeAny // an implicit any (mirrors emitArrowFunctionWithHints)
			} else if p.Type == nil {
				params[i] = TypeI64
				params[i].Inferred = true // no annotation — see docs/adr/00/ADR-00042.md
			} else {
				params[i] = e.annotationType(p.Type)
			}
			// Optional `x?: T` → `T | undefined`, matching the closure's actual
			// emitted parameter ABI (emit_func.go) so a `const f = …` symbol's
			// type agrees with the body — the same reason FuncHasRest is mirrored
			// here (TDD-00187).
			params[i] = optionalParamType(p, params[i])
		}
		var ret Type
		if ex.RetType != nil {
			ret = e.annotationType(ex.RetType)
		} else if ex.Body != nil {
			// The body is typed with its parameters in scope: `(s: string) =>
			// s.length` / `=> s[0]` are a number / a string only once `s` is
			// known — unresolved, they fell to the i64 default while the closure
			// itself was emitted returning double / ptr.
			e.pushScope()
			for i, p := range ex.Params {
				e.definePatternParamForInference(p, params[i], i)
			}
			ret = e.typeOfExpr(ex.Body)
			e.popScope()
		} else if hasReturn(ex.Block) {
			// Same best-effort inference emitArrowFunctionWithHints uses when
			// actually emitting this closure — this duplicate exists because
			// typeOfExpr has to answer "what type is this arrow function"
			// before any closure value exists yet (e.g. right when a `const`
			// binding to it is being declared). The two computations used to
			// disagree (this one unconditionally defaulted to TypeI64
			// regardless of what was returned), which silently mistyped the
			// variable itself even though the actual closure body was
			// correctly built to return an object/array/Date — a real bug,
			// not just a missed optimization, since callers trust this type.
			paramNames := make([]string, len(ex.Params))
			for i, p := range ex.Params {
				paramNames[i] = p.Name
			}
			if inferred, ok := e.funcBodyReturnType(ex.Block, paramNames, e.bodyParamTypes(ex.Params, params)); ok {
				ret = inferred
			} else if firstReturnExpr(ex.Block) == nil {
				ret = TypeVoid // every return is bare (mirrors emitArrowFunctionWithHints)
			} else {
				ret = TypeF64
			}
		} else if blockAlwaysDiverges(ex.Block) {
			ret = TypeNever // block-body arrow that only throws (ADR-00869)
		} else {
			ret = TypeVoid
		}
		fty := FuncType(params, asyncClosureRetType(ex.IsAsync, ret))
		if len(ex.Params) > 0 && ex.Params[len(ex.Params)-1].Rest {
			fty.FuncHasRest = true
		}
		setFuncParamDefaults(&fty, ex.Params)
		return fty
	case *ast.FunctionExpression:
		// A generator expression is a boxed constructor closure (mirrors
		// emitGeneratorExpressionValue).
		if ex.IsGenerator {
			return TypeAny
		}
		// Same type-inference as ArrowFunction above — a function expression
		// is a block-only closure whose type is determined by param/return
		// types and captures. Params use the same resolver; unlike arrows,
		// function expressions never have expression bodies (Body is always
		// a BlockStatement, never an Expression).
		// Mirror emitFunctionExpression's implicit `arguments` rest (TDD-00210)
		// so the inferred type's arity matches the emitted function's.
		ex.Params = maybeAddArgumentsRestParams(ex.Params, ex.Body.Body)
		thisTaking := e.maybeAddThisParamExpr(ex)
		if !thisTaking && e.compatJS() && !ex.IsAsync && usesOwnThis(ex.Body.Body) && hasArgumentsRestParamSlice(ex.Params) {
			return TypeAny // stays dynamic (mirrors emitFunctionExpression)
		}
		params := make([]Type, len(ex.Params))
		for i, p := range ex.Params {
			if p.Rest && p.Name == argumentsRestParam {
				params[i] = ArrayOf(TypeAny) // implicit `arguments` rest (TDD-00210)
			} else if p.Rest && p.Type == nil {
				params[i] = untypedRestType()
			} else if dt, ok := e.paramDefaultType(p); ok && p.Type == nil {
				params[i] = dt
			} else if ct, ok := e.checkerContextualParam(ex, i); p.Type == nil && ok {
				params[i] = ct // mirrors emitFunctionExpression
			} else if st, ok := e.jsSiteParam(ex, i); p.Type == nil && ok {
				params[i] = st // its call sites' type (-compat=js)
			} else if p.Type == nil && e.front() != nil {
				params[i] = TypeAny // an implicit any (mirrors emitFunctionExpression)
			} else if p.Type == nil {
				params[i] = TypeI64
				params[i].Inferred = true
			} else {
				params[i] = e.annotationType(p.Type)
			}
			// Optional `x?: T` → `T | undefined` — mirror of the arrow case above.
			params[i] = optionalParamType(p, params[i])
		}
		var ret Type
		if ex.RetType == nil && hasReturn(ex.Body) {
			defer e.bindSelfForInference(ex, params)()
		}
		if ex.RetType != nil {
			ret = e.annotationType(ex.RetType)
		} else if hasReturn(ex.Body) {
			paramNames := make([]string, len(ex.Params))
			for i, p := range ex.Params {
				paramNames[i] = p.Name
			}
			if inferred, ok := e.funcBodyReturnType(ex.Body, paramNames, e.bodyParamTypes(ex.Params, params)); ok {
				ret = inferred
			} else if firstReturnExpr(ex.Body) == nil {
				ret = TypeVoid // every return is bare (mirrors emitFunctionExpression)
			} else {
				ret = TypeF64
			}
		} else if blockAlwaysDiverges(ex.Body) {
			ret = TypeNever // function expression that only throws (ADR-00869)
		} else {
			ret = TypeVoid
		}
		fty := FuncType(params, asyncClosureRetType(ex.IsAsync, ret))
		if len(ex.Params) > 0 && ex.Params[len(ex.Params)-1].Rest {
			fty.FuncHasRest = true
		}
		fty.FuncThis = thisTaking
		setFuncParamDefaults(&fty, ex.Params)
		// Reading `arguments`, a call stores its count (emitFuncValue).
		fty.FuncCountsArgs = fty.FuncHasRest && ex.Params[len(ex.Params)-1].Name == argumentsRestParam
		return fty
	case *ast.NewDateExpression:
		return TypeDate
	case *ast.NewTypedArrayExpression:
		return TypedArrayType(ex.ElemKind)
	case *ast.NewRegExpExpression:
		return RegExpType()
	case *ast.NewRequestExpression:
		return FetchRequestType()
	}
	// A form none of the cases above answers (a binary operator whose
	// operands settle nothing, a `super` outside a class) is emitted as a
	// number.
	return TypeI64
}

// switchedExprKind reports the expression kinds whose type typeOfExpr
// takes from the checker, mapped by reprOf (TDD-00230 P3.3): on each, the
// old inference agreed with it over the whole corpus. A kind the checker
// has no answer for falls back to the old inference, until it is settled.
func switchedExprKind(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.TemplateLiteral,
		*ast.ThisExpression, *ast.ConditionalExpression, *ast.UnaryExpression, *ast.UpdateExpression,
		*ast.SequenceExpression, *ast.SuperExpression, *ast.AsExpression, *ast.NonNullExpression,
		*ast.YieldExpression, *ast.NewTargetExpression, *ast.AwaitExpression,
		*ast.NewMapExpression, *ast.NewSetExpression, *ast.NewWeakMapExpression, *ast.NewWeakSetExpression,
		*ast.NewCompressionStreamExpression, *ast.NewXMLHttpRequestExpression,
		*ast.TaggedTemplateExpression, *ast.ImportCallExpression,
		*ast.NewRegExpExpression, *ast.NewTypedArrayExpression, *ast.NewErrorExpression, *ast.NewDateExpression,
		*ast.NewArrayExpression, *ast.NewReadableStreamExpression, *ast.NewWritableStreamExpression,
		*ast.NewTransformStreamExpression, *ast.NewRequestExpression, *ast.NewArrayBufferExpression,
		*ast.ArrayLiteral, *ast.NewExpression, *ast.AssignmentExpression, *ast.BinaryExpression, *ast.IndexExpression, *ast.Identifier, *ast.MemberExpression, *ast.CallExpression,
		*ast.ArrowFunction, *ast.FunctionExpression, *ast.ObjectLiteral:
		return true
	}
	return false
}

// reprOracle is the checker's side of typeOfExpr for the switched
// kinds, over codegen's own checker (front).
func (e *Emitter) reprOracle() *frontOracle {
	if e.foreignThisBody {
		// A method body compiled for a foreign receiver: the checker typed
		// its `this` as the class.
		return nil
	}
	if e.frontReprBuilt {
		return e.frontRepr
	}
	c := e.front()
	if c == nil {
		return nil // the program is not stored yet: ask again later
	}
	e.frontReprBuilt = true
	r := e.repr()
	b := e.frontBinding
	r.nodeJS = func(sym *binder.Symbol) bool {
		if b.Globals == nil {
			return false
		}
		ns := b.Globals.Symbols.Get("NodeJS")
		return ns != nil && b.NamespaceScope(ns) == sym.Scope
	}
	o := frontOracle{b: b, c: c, r: r, q: &oracleQuery{}, storesBusy: map[*ast.Identifier]bool{}, litBusy: map[*ast.ObjectLiteral]bool{}, storageBusy: map[*ast.ObjectLiteral]bool{}, returnsBusy: map[*ast.ObjectLiteral]bool{}, dynInit: map[*ast.VarDeclaration]bool{}, fnReads: map[string]bool{}, forOfBusy: map[*ast.ForOfStatement]bool{}}.withPromiseCallbacks(e.prog)
	e.frontRepr = &o
	return e.frontRepr
}

// unboundName reports x as a name with no binding: a builtin namespace or
// global (`Math`, `console`), whose members emission builds as builtin
// values (`[4, 9].map(Math.sqrt)`).
func (e *Emitter) unboundName(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	return ok && !e.boundName(id)
}

// boundName reports id as a name codegen has storage for: a binding in
// scope or a function declaration.
func (e *Emitter) boundName(id *ast.Identifier) bool {
	if _, ok := e.lookup(id.Name); ok {
		return true
	}
	_, _, ok := e.resolveFuncRef(id.Name)
	return ok
}

// constLiteralTree reports x as a number, string, boolean or null literal, or
// an array literal of those (nested).
func constLiteralTree(x ast.Expression) bool {
	switch v := x.(type) {
	case *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
		return true
	case *ast.ArrayLiteral:
		for _, el := range v.Elements {
			if el == nil || !constLiteralTree(el) {
				return false
			}
		}
		return true
	}
	return false
}

// virtualModuleMarker reports the binding a virtual module's import was
// renamed to (`Memory__kml_builtin`): its members dispatch by name, and the
// checker has no declaration for it.
func virtualModuleMarker(expr ast.Expression) bool {
	id, ok := expr.(*ast.Identifier)
	return ok && strings.HasSuffix(id.Name, "_kml_builtin")
}

// foreignTainted reports whether a `this` value can reach expr in the
// body compiled for a foreign receiver: expr reads `this`, `super` or
// `arguments`, or a local of the body some such value was stored to.
func (e *Emitter) foreignTainted(expr ast.Expression) bool {
	if e.foreignClass == nil {
		return true
	}
	tainted := e.foreignTaint()
	hit := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if hit {
			return false
		}
		switch x := n.(type) {
		case *ast.ThisExpression, *ast.SuperExpression:
			hit = true
		case *ast.Identifier:
			if x.Name == "arguments" || tainted[x.Name] {
				hit = true
			}
		}
		return !hit
	})
	return hit
}

// foreignTaint is the names of the locals of the foreign-receiver body
// being compiled that a `this` value can be stored to, to a fixed point
// over its declarations and assignments.
func (e *Emitter) foreignTaint() map[string]bool {
	fe := e.foreignBody
	if fe == nil {
		return map[string]bool{}
	}
	if t, ok := e.foreignTaints[fe]; ok {
		return t
	}
	tainted := map[string]bool{}
	reaches := func(x ast.Node) bool {
		hit := false
		ast.Inspect(x, func(n ast.Node) bool {
			switch y := n.(type) {
			case *ast.ThisExpression, *ast.SuperExpression:
				hit = true
			case *ast.Identifier:
				if y.Name == "arguments" || tainted[y.Name] {
					hit = true
				}
			}
			return !hit
		})
		return hit
	}
	for changed := true; changed; {
		changed = false
		mark := func(name string) {
			if !tainted[name] {
				tainted[name], changed = true, true
			}
		}
		ast.Inspect(fe.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.VarDeclaration:
				if x.Init != nil && reaches(x.Init) {
					mark(x.Name)
				}
			case *ast.AssignmentExpression:
				if id, ok := x.Left.(*ast.Identifier); ok && reaches(x.Right) {
					mark(id.Name)
				}
			case *ast.ForOfStatement, *ast.ForInStatement, *ast.ArrayDestructuring, *ast.ObjectDestructuring:
				// A binding a loop or pattern gets from a `this` value:
				// every name in it, conservatively.
				if reaches(n) {
					ast.Inspect(n, func(m ast.Node) bool {
						switch y := m.(type) {
						case *ast.Identifier:
							mark(y.Name)
						case *ast.VarDeclaration:
							mark(y.Name)
						}
						return true
					})
				}
			case *ast.FunctionDeclaration:
				// A nested function reading a tainted value.
				if reaches(x.Body) {
					mark(x.Name)
				}
			}
			return true
		})
	}
	if e.foreignTaints == nil {
		e.foreignTaints = map[*ast.FunctionExpression]map[string]bool{}
	}
	e.foreignTaints[fe] = tainted
	return tainted
}

// typed records x, an expression codegen builds, as having type t: what
// the checker, which never sees it, would answer for it.
func typed[T ast.Expression](e *Emitter, x T, t Type) T {
	if e.builtTypes == nil {
		e.builtTypes = map[ast.Expression]Type{}
	}
	e.builtTypes[x] = t
	return x
}
