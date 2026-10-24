// infer_memo.go — inferExprType memoises member/call nodes for the duration of
// one outermost inference.
//
// inferExprType decides a member call's type by asking "what is the receiver?"
// once per builtin family it knows (dozens of `e.inferExprType(mem.Object)`
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
	"fmt"
	"strings"

	"reflect"

	"KlainMainLang/ast"
)

type inferMemoLayer struct {
	depth int     // len(e.scopes) the layer was opened under
	base  uintptr // identity of e.scopes[0].syms (0 when there is no scope)
	types map[ast.Expression]Type
	// full holds inferExprType's own answers (types holds the inner
	// inferExprTypeMemo's, which can differ for the same node).
	full map[ast.Expression]Type
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
		e.inferMemo[len(e.inferMemo)-1].types = nil
		e.inferMemo[len(e.inferMemo)-1].full = nil
	}
}

// inferExprType: compile-time type of expr, no codegen. A name, member read
// or call is memoised like inferExprTypeMemo's nodes: the fallbacks for
// nodes the checker never saw (a binding's initializer, a receiver) re-infer
// their parts, which compounds with nesting.
func (e *Emitter) inferExprType(expr ast.Expression) Type {
	if c, ok := expr.(*ast.CallExpression); ok && (c.Intrinsic == ast.ModuleNamespaceIntrinsic || c.Intrinsic == ast.LiveBindingsIntrinsic) {
		return TypeAny // a module namespace object (moduleNamespaceRule)
	}
	switch expr.(type) {
	case *ast.CallExpression, *ast.MemberExpression, *ast.Identifier:
	default:
		return e.inferExprTypeFull(expr)
	}
	if e.inferDepth == 0 {
		e.inferMemo = e.inferMemo[:0]
		e.inferDepth++
		e.inferMemoOpenLayer()
		ty := e.inferExprTypeFull(expr)
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
	ty := e.inferExprTypeFull(expr)
	e.inferDepth--
	if l := e.inferMemoLayerTop(); l != nil {
		if l.full == nil {
			l.full = map[ast.Expression]Type{}
		}
		l.full[expr] = ty
	}
	return ty
}

func (e *Emitter) inferExprTypeFull(expr ast.Expression) Type {
	if t, ok := e.boxOverride(expr); ok {
		return t // mirrors emitAnyArgDispatch, emitShadowableRead
	}
	// A bound name reads its storage: its representation is the binding's
	// (whose type the checker gave through its initializer), refined by
	// what flow proves (identStorageType).
	if id, ok := expr.(*ast.Identifier); ok && e.boundName(id) {
		return e.inferRefined(expr, e.identStorageType(id))
	}
	// A user class's prototype object is a dynamic object (emitClassProto),
	// whatever the checker types it as.
	if m, ok := expr.(*ast.MemberExpression); ok && m.Property == "prototype" && e.isClassName(m.Object) {
		return TypeAny
	}
	// A property of a value held boxed is read by the dynamic dispatch, so
	// its type is that dispatch's, whatever the checker knows of the value
	// (a js-lane parameter typed from its call sites but stored as `any`).
	if m, ok := expr.(*ast.MemberExpression); ok && !m.Optional && !e.unboundName(m.Object) && !e.enumName(m.Object) {
		rt := e.inferExprType(m.Object)
		if isUnconstrainedDynamic(rt) && rt.DynPropTy == nil {
			return e.inferRefined(expr, e.structuralMemberType(m))
		}
		// A field whose slot the object's layout holds boxed (`{ f:
		// String.prototype.normalize }`) reads that box.
		if _, fty, ok := rt.FieldIndex(m.Property); ok && fty.IsDynamic() {
			return e.inferRefined(expr, e.structuralMemberType(m))
		}
		// An array's or a string's method read as a value is its
		// prototype's record (emitMember), whatever signature the checker
		// gives it.
		for _, class := range []string{valueProtoClass(rt), primMethodClass(rt)} {
			if _, ok := e.classMethod(class, m.Property); ok && class != "" {
				return TypeAny
			}
		}
		// A generator's member other than its protocol's is read off its
		// prototype chain (shape.c).
		if _, own := generatorMethods[m.Property]; rt.IsGenerator() && !own && e.genProtos {
			return TypeAny
		}
		// A function value's property other than its own (`g.toString`) is
		// read off the boxed function the same way.
		if rt.IsFunc() {
			if st := e.structuralMemberType(m); st.IsDynamic() {
				return e.inferRefined(expr, st)
			}
		}
	}
	// An array's symbol-keyed property is read through its box
	// (emitIndexUnguarded).
	if ix, ok := expr.(*ast.IndexExpression); ok && !ix.Optional {
		if ot := e.inferExprType(ix.Object); ot.IsArray() && !ot.IsDynamic() && !ot.IsFlatArray() && (isWellKnownSymbolExpr(ix.Index) || e.inferExprType(ix.Index).IsSymbol()) {
			return TypeAny
		}
	}
	if _, isThis := expr.(*ast.ThisExpression); isThis {
		// A bound `this` reads its binding, as emitThisExpression does,
		// whatever the checker types it as (an object literal's accessor
		// binds its synthetic class).
		if sym, ok := e.lookup("this"); ok {
			return sym.Ty
		}
	}
	if switchedExprKind(expr) {
		if o := e.reprOracle(); o != nil {
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
		if settledExprKind(expr) {
			// No checker fallback remains: a node the checker has no answer
			// for (code built after parsing, a synthesized helper or a static
			// eval's text; or a checker gap, which the shadow report lists)
			// is typed from its binding, the dispatch its emission runs, or
			// its syntax (settledFallbackType).
			ft := e.settledFallbackType(expr)
			if o := e.reprOracle(); o != nil && o.c.InProgram(expr) && !e.loweredNodes[expr] && !virtualModuleMarker(expr) {
				e.shadowSwitchFallback(expr, ft)
			}
			return e.inferRefined(expr, ft)
		}
		ft := e.inferExprTypeMemo(expr)
		if o := e.reprOracle(); o != nil && o.c.InProgram(expr) && !e.loweredNodes[expr] && !virtualModuleMarker(expr) {
			e.shadowSwitchFallback(expr, ft) // a synthesized node is no checker gap
		}
		return e.inferRefined(expr, ft)
	}
	return e.inferRefined(expr, e.inferExprTypeMemo(expr))
}

// settledFallbackType is a settled kind's type without a checker answer:
// its binding, its parts' representation, or its syntax.
func (e *Emitter) settledFallbackType(expr ast.Expression) Type {
	if t, ok := e.inferBuiltPreamble(expr); ok {
		return t
	}
	switch x := expr.(type) {
	case *ast.Identifier:
		return e.identStorageType(x)
	case *ast.MemberExpression:
		return e.structuralMemberType(x)
	case *ast.CallExpression:
		return e.structuralCallType(x)
	case *ast.IndexExpression:
		return e.structuralIndexType(x)
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

// builtinCount reports whether ex reads a builtin count (`length`, `size`,
// `byteLength`, …) of an array, string, collection or buffer: a `number`,
// which is a double (TDD-00230 P3.3), however the runtime keeps it.
func (e *Emitter) builtinCount(ex *ast.MemberExpression) bool {
	if !builtinCountNames[ex.Property] {
		return false
	}
	t := e.inferExprTypeMemo(ex.Object)
	if t.IsDynamic() || t.IsClass() || (t.IsObject() && !t.IsArray()) || t.IsDynamicObject() {
		return false
	}
	return t.IsArray() || t.IsTypedArray() || t.IsBuffer() || t.IsArrayBuffer() || t.IsMap() || t.IsSet() || isStringTy(t)
}

// builtinCountNames are the members builtinCount looks at.
var builtinCountNames = map[string]bool{"length": true, "size": true, "byteLength": true, "byteOffset": true, "maxByteLength": true}

func (e *Emitter) inferExprTypeMemo(expr ast.Expression) Type {
	// Only the nodes that carry the receiver recursion are memoised.
	switch expr.(type) {
	case *ast.CallExpression, *ast.MemberExpression:
	default:
		return e.inferExprTypeUncached(expr)
	}
	if e.inferDepth == 0 {
		e.inferMemo = e.inferMemo[:0]
		e.inferDepth++
		e.inferMemoOpenLayer()
		ty := e.inferExprTypeUncached(expr)
		e.inferDepth--
		e.inferMemo = e.inferMemo[:0]
		return ty
	}
	if l := e.inferMemoLayerTop(); l != nil {
		if ty, ok := l.types[expr]; ok {
			return ty
		}
	}
	e.inferDepth++
	ty := e.inferExprTypeUncached(expr)
	e.inferDepth--
	if l := e.inferMemoLayerTop(); l != nil {
		if l.types == nil {
			l.types = map[ast.Expression]Type{}
		}
		l.types[expr] = ty
	}
	return ty
}

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
			inTy = e.resolveType(ex.InType)
		}
		if ex.OutType != nil {
			outTy = e.resolveType(ex.OutType)
		}
		return Type{IR: "ptr", Host: hostTransformStream, StreamChunk: &inTy, StreamOut: &outTy}
	case *ast.NewWritableStreamExpression:
		if ex.ChunkType != nil {
			return WritableStreamType(e.resolveType(ex.ChunkType))
		}
		return WritableStreamType(TypeAny)
	case *ast.NewReadableStreamExpression:
		if ex.ChunkType != nil {
			return ReadableStreamType(e.resolveType(ex.ChunkType))
		}
		return ReadableStreamType(TypeAny)
	case *ast.NewDateExpression:
		return TypeDate
	case *ast.NewTypedArrayExpression:
		return TypedArrayType(ex.ElemKind)
	case *ast.NewRegExpExpression:
		return RegExpType()
	case *ast.NewRequestExpression:
		return FetchRequestType()
	}
	panic(fmt.Sprintf("internal error: no syntactic type for the %T", expr))
}

// settledExprKind reports the switched kinds whose old inference is
// deleted. A node the checker has no answer for is typed from its syntax
// (a literal), its binding (an identifier, identStorageType), or the
// representation of the parts codegen built it from (a member read or
// call, infer_structural.go).
func settledExprKind(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.TemplateLiteral,
		*ast.NewRegExpExpression, *ast.NewTypedArrayExpression, *ast.NewDateExpression,
		*ast.NewArrayExpression, *ast.NewReadableStreamExpression, *ast.NewWritableStreamExpression,
		*ast.NewTransformStreamExpression, *ast.NewRequestExpression,
		*ast.IndexExpression, *ast.Identifier, *ast.MemberExpression, *ast.CallExpression:
		return true
	}
	return false
}

// switchedExprKind reports the expression kinds whose type inferExprType
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

// reprOracle is the checker's side of inferExprType for the switched
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
	o := frontOracle{b: b, c: c, r: r, q: &oracleQuery{}, storesBusy: map[*ast.Identifier]bool{}, litBusy: map[*ast.ObjectLiteral]bool{}, forOfBusy: map[*ast.ForOfStatement]bool{}}.withPromiseCallbacks(e.prog)
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
