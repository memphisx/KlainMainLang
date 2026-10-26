// emit_exprs.go — core expression dispatch (emitExpr) plus the leaf literal
// emitters. Everything else expression-related lives in the other
// emit_exprs_*.go files: operators (emit_exprs_operators.go), assignment
// (emit_exprs_assign.go), member/index access (emit_exprs_member.go), type
// inference/conversion (emit_exprs_types.go), scalar coercion
// (emit_convert.go), and variable declarations (emit_exprs_vardecl.go).
package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/resolver"
	"KlainMainLang/sema"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

func (e *Emitter) emitExpr(expr ast.Expression) (Value, error) {
	if sloppy, ok := e.sloppyWriteNodes[expr]; ok {
		saved := e.sloppyWrite
		e.sloppyWrite = sloppy
		defer func() { e.sloppyWrite = saved }()
	}
	if v, handled, err := e.emitForeignPrivate(expr); handled {
		return v, err
	}
	v, err := e.emitExprUnguarded(expr)
	// A call returning nothing whose result is an argument or interpolation
	// (`s.split(f())`) is the value undefined there.
	if err == nil && (v.Ty.IR == "void" || v.Ty.IR == "") && e.voidValueSites()[expr] {
		v = Value{Ref: "null", Ty: TypeUndefined}
	}
	// The base of a member/index/call access: an absent value throws the
	// TypeError instead of being dereferenced (emit_nullderef.go).
	if err == nil && len(e.pendingDeref) > 0 {
		if gerr := e.applyPendingDeref(expr, v); gerr != nil {
			return Value{}, gerr
		}
	}
	return v, err
}

func (e *Emitter) emitExprUnguarded(expr ast.Expression) (Value, error) {
	// A chain that continues past a `?.` short-circuits as a whole.
	if v, handled, err := e.emitOptionalChain(expr); handled {
		return v, err
	}
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		return e.emitNumberLit(ex)
	case *ast.StringLiteral:
		ptr := e.internString(ex.Value)
		return Value{Ref: ptr, Ty: TypePtr}, nil
	case *ast.BooleanLiteral:
		v := "0"
		if ex.Value {
			v = "1"
		}
		return Value{Ref: v, Ty: TypeBool}, nil
	case *ast.Identifier:
		if !e.classRecordSites[ex] {
			e.emitClassReeval(ex) // a record site re-runs once its record is made
		}
		return e.emitIdent(ex)
	case *ast.BinaryExpression:
		if ex.Op == "??" {
			return e.emitNullCoalesce(ex)
		}
		if ex.Op == "instanceof" {
			return e.emitInstanceOf(ex)
		}
		if ex.Op == "in" {
			return e.emitInOperator(ex)
		}
		return e.emitBinary(ex)
	case *ast.UnaryExpression:
		return e.emitUnary(ex)
	case *ast.UpdateExpression:
		return e.emitUpdate(ex)
	case *ast.AssignmentExpression:
		return e.emitAssign(ex)
	case *ast.CallExpression:
		v, err := e.emitCall(ex)
		if err == nil && v.Ty.IsTuple() && !v.Ty.IsArray() {
			// A tuple where inference reads the call as an array (a builtin
			// declared through a callable interface): its elements, as every
			// consumer expects.
			if at := e.inferExprType(ex); at.IsArray() && !at.IsTuple() {
				v = e.convert(v, at)
			}
		}
		if err == nil && isUnconstrainedDynamic(v.Ty) && v.Ty.DynPropTy == nil && !e.exprHoldsBox(ex) {
			// A call answered `any` (an implementation signature behind
			// overloads) whose overload the checker resolves to a primitive:
			// that primitive, mirroring inferExprType.
			if pt, ok := e.checkerPrimitive(ex); ok {
				return e.convert(v, pt), nil
			}
		}
		return v, err
	case *ast.TaggedTemplateExpression:
		return e.emitCall(e.desugarTaggedTemplate(ex))
	case *ast.IndexExpression:
		return e.emitIndex(ex)
	case *ast.MemberExpression:
		if e.isStackTraceLimit(ex) {
			e.ensureFrames()
			e.declareFn("__kml_stack_limit_get", "declare double @__kml_stack_limit_get()")
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call double @__kml_stack_limit_get()", r))
			return Value{Ref: r, Ty: TypeF64}, nil
		}
		if b, ok := e.builtinRef(ex); ok {
			return e.emitBuiltinValue(b) // `const f = Math.floor`
		}
		done := e.accessSite(ex)
		v, err := e.emitMember(ex)
		done()
		if err == nil && e.checkerPresentRef(ex, v.Ty) {
			v.Ty = v.Ty.withoutNullable() // flow proves it present (inferExprType)
		}
		if err == nil && !v.Ty.IsDynamic() && !isNullableScalar(v.Ty) && e.builtinCount(ex) {
			v = e.countToNumber(v) // a number, as every JS count is (inferExprType)
		}
		return v, err
	case *ast.SpreadElement:
		return Value{}, fmt.Errorf("%d:%d: a spread (...) is only supported inside an array literal or as a single array filling a function's rest parameter (e.g. f(...arr)) — not here", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.ArrayLiteral:
		// No target-type hint available in this bare-dispatch path — falls
		// back to inferArrayType's first-element inference (unchanged
		// convention). A caller that already knows the expected element
		// type (a declared parameter, a var-decl annotation, an
		// object-literal field, a return type) should go through
		// emitExprWithObjectHint instead, which threads it through as a
		// hint (TDD-00028) — every existing hint-aware call site already
		// gets that for free.
		return e.emitArrayLiteralAggregate(ex, nil)
	case *ast.NewArrayExpression:
		// `new Array(n)` without a type argument is an any[], as TypeScript
		// types it.
		return e.emitNewArraySizedAggregate(ex, newArrayElemType(e, ex))
	case *ast.NewMapExpression:
		// A redeclared top-level `var m = new Map()` passes the promoted
		// binding's K/V down (emitVarRedeclaration, ADR-01061).
		if h := e.newCollectionHint; h != nil && h.IsMap() && h.MapKey != nil && h.MapVal != nil {
			e.newCollectionHint = nil
			return e.emitNewMapValueTyped(ex, *h.MapKey, *h.MapVal)
		}
		return e.emitNewMapValue(ex)
	case *ast.NewSetExpression:
		if h := e.newCollectionHint; h != nil && h.IsSet() && h.MapKey != nil {
			e.newCollectionHint = nil
			return e.emitNewSetValueTyped(ex, h.MapKey)
		}
		return e.emitNewSetValue(ex)
	case *ast.NewWeakMapExpression:
		return e.emitNewWeakMapValue(ex)
	case *ast.NewWeakSetExpression:
		return e.emitNewWeakSetValue(ex)
	case *ast.NewReadableStreamExpression:
		return e.emitNewReadableStream(ex)
	case *ast.NewWritableStreamExpression:
		return e.emitNewWritableStream(ex)
	case *ast.NewTransformStreamExpression:
		return e.emitNewTransformStream(ex)
	case *ast.NewCompressionStreamExpression:
		return e.emitNewCompressionStream(ex)
	case *ast.NewErrorExpression:
		return e.emitNewError(ex)
	case *ast.NewDateExpression:
		return e.emitNewDate(ex)
	case *ast.ObjectLiteral:
		// `-compat=js`: a bare (context-free) object literal is a D1 dynamic
		// object — untyped JS returns/passes literals ad hoc, and only a
		// hint-typed context (an annotated slot, a typed parameter) keeps the
		// native static-struct form (emitExprWithObjectHint routes those
		// before reaching here).
		if e.compatJS() {
			return e.emitDynObjLiteral(ex)
		}
		return e.emitObjectLiteral(ex)
	case *ast.ArrowFunction:
		return e.emitArrowFunction(ex)
	case *ast.FunctionExpression:
		return e.emitFunctionExpression(ex, nil)
	case *ast.TemplateLiteral:
		return e.emitTemplateLiteral(ex)
	case *ast.ConditionalExpression:
		return e.emitConditional(ex)
	case *ast.NonNullExpression:
		return e.emitNonNull(ex)
	case *ast.AsExpression:
		return e.emitAsExpression(ex)
	case *ast.SequenceExpression:
		// Comma operator: evaluate each operand for its side effects, yield the
		// last. An empty list can't occur (the parser always has a first
		// operand before the comma).
		var last Value
		for _, sub := range ex.Exprs {
			v, err := e.emitExpr(sub)
			if err != nil {
				return Value{}, err
			}
			last = v
		}
		return last, nil
	case *ast.NullLiteral:
		if ex.IsUndefined {
			return Value{Ref: "null", Ty: TypeUndefined}, nil
		}
		return Value{Ref: "null", Ty: TypeNull}, nil
	case *ast.AwaitExpression:
		return e.emitAwait(ex)
	case *ast.YieldExpression:
		// TDD-00061/ADR-00172: gated on e.currentGenerator, set only while
		// emitting a generator function's own body (emitGeneratorFunctionDecl).
		// The parser rejects a `yield` outside any generator body (TS1163);
		// this is the backstop for a generator form codegen lowers apart.
		if e.currentGenerator == nil {
			return Value{}, fmt.Errorf("%d:%d: 'yield' is only valid inside a generator function body", ex.GetPos().Line, ex.GetPos().Col)
		}
		return e.emitYieldExpression(ex)
	case *ast.ThisExpression:
		return e.emitThisExpression(ex.GetPos())
	case *ast.NewTargetExpression:
		return e.emitNewTarget(ex)
	case *ast.NewExpression:
		e.noteWebviewNew(ex)
		return e.emitNewExpression(ex)
	case *ast.NewArrayBufferExpression:
		return e.emitNewArrayBufferExpression(ex)
	case *ast.NewTypedArrayExpression:
		if _, ok := e.patchedIterInit(ex.Arg); ok {
			// (A typed-array literal takes no spread: Array.from's.)
			nx := *ex
			nx.Arg = arrayFromCall(ex.Arg.GetPos(), []ast.Expression{ex.Arg})
			return e.emitNewTypedArrayAggregate(&nx)
		}
		return e.emitNewTypedArrayAggregate(ex)
	case *ast.ImportCallExpression:
		return e.emitImportCall(ex)
	case *ast.NewRegExpExpression:
		return e.emitNewRegExpExpression(ex)
	case *ast.NewRequestExpression:
		return e.emitNewRequestExpression(ex)
	case *ast.NewXMLHttpRequestExpression:
		return e.emitNewXMLHttpRequestExpression(ex)
	case *ast.ClassExpression:
		// Every class expression that captures no enclosing local was hoisted
		// to a top-level class before emission (emit_classexpr.go); this one
		// reads a local of the function it is written in.
		return Value{}, fmt.Errorf("%d:%d: a class expression that uses '%s', a local of its enclosing function, is not supported", ex.GetPos().Line, ex.GetPos().Col, e.classExprCaptures[ex])
	}
	return Value{}, fmt.Errorf("unknown expression type %T", expr)
}

func (e *Emitter) emitNumberLit(n *ast.NumberLiteral) (Value, error) {
	if n.IsBigInt {
		return e.emitBigIntLiteral(n)
	}
	v := n.Value
	// TDD-00123 Stage 1: every JS number literal is a double, so a numeric
	// literal is TypeF64 regardless of whether it has a fractional part. A
	// decimal fraction passes through verbatim (LLVM rounds to nearest double);
	// an integer/hex/bin/oct literal is converted to its float64 value (rounded
	// past 2^53 exactly as JS does). Real integers come from the explicit
	// `/** @type {intN/uintN} */` types, not literals.
	// Hex (0x), binary (0b), octal (0o). Checked before the fraction/exponent
	// handling below, since a hex literal's digits can themselves be `e`/`E`
	// (`0x1e`) or contain no separator the decimal path expects.
	if len(v) >= 2 && v[0] == '0' && (v[1]|32 == 'x' || v[1]|32 == 'b' || v[1]|32 == 'o') {
		n64, err := strconv.ParseInt(v, 0, 64)
		if err != nil {
			// Past 64 bits (`0xFFFFFFFFFFFFFFFFF`): the nearest double, as JS
			// reads any integer literal.
			wide, ok := new(big.Int).SetString(v, 0)
			if !ok {
				return Value{}, fmt.Errorf("invalid numeric literal %q: %v", v, err)
			}
			f, _ := new(big.Float).SetInt(wide).Float64()
			return Value{Ref: llvmDoubleLit(f), Ty: TypeF64}, nil
		}
		return Value{Ref: llvmDoubleLit(float64(n64)), Ty: TypeF64}, nil
	}
	// A decimal exponent literal (`1e3`, `1.5e-2`) is normalized to the exact
	// double bit-pattern form — LLVM reads `e`-notation ambiguously in a double
	// slot (see llvmDoubleLit), so it can't be passed verbatim. A plain fraction
	// with no exponent still passes through verbatim (LLVM parses `1.5` fine).
	if strings.ContainsAny(v, "eE") {
		if f, ferr := parseJSFloat(v); ferr == nil {
			return Value{Ref: llvmDoubleLit(f), Ty: TypeF64}, nil
		}
		return Value{}, fmt.Errorf("invalid numeric literal %q", v)
	}
	if strings.ContainsRune(v, '.') {
		// Normalize to the exact double bit-pattern rather than passing the
		// source spelling through verbatim: a leading- or trailing-dot fraction
		// (`.9`, `-.9`, `9.`) is valid JS but invalid as an LLVM double token,
		// which requires a digit on both sides. llvmDoubleLit is bit-exact (LLVM
		// and Go both round-to-nearest), so this changes no value.
		if f, ferr := parseJSFloat(v); ferr == nil {
			return Value{Ref: llvmDoubleLit(f), Ty: TypeF64}, nil
		}
		return Value{}, fmt.Errorf("invalid numeric literal %q", v)
	}
	// Decimal integer literal.
	if n64, err := strconv.ParseInt(v, 10, 64); err == nil {
		return Value{Ref: llvmDoubleLit(float64(n64)), Ty: TypeF64}, nil
	}
	// Oversize decimal (beyond i64) — parse as float directly.
	if f, ferr := strconv.ParseFloat(v, 64); ferr == nil {
		return Value{Ref: llvmDoubleLit(f), Ty: TypeF64}, nil
	}
	return Value{}, fmt.Errorf("invalid numeric literal %q", v)
}

// emitInt64LiteralForTarget handles a numeric literal whose target is an
// explicit 64-bit integer type (`int64`/`uint64`). Every literal is otherwise a
// `float64` (TDD-00123 Stage 1), so a value above 2^53 would round when it went
// through the double model on its way into the integer slot — defeating the
// escape hatch, whose whole purpose is exact integers. Here the original digit
// string is parsed straight to a 64-bit integer, preserving the full value
// (e.g. `let a: int64 = 9007199254740993` stays exact). Returns ok=false for a
// bigint literal, a non-64-bit or float target, or a fractional/exponent/
// out-of-range literal — all of which fall back to the normal double path
// (correct there, since values ≤ 2^53 and narrow-int targets lose nothing).
func (e *Emitter) emitInt64LiteralForTarget(n *ast.NumberLiteral, ty Type) (Value, bool) {
	// A dynamic target shares i64's IR since the NaN-box migration
	// (TDD-00156) but is NOT an integer slot — without this guard the raw
	// literal flowed into the box-idempotence passthrough unboxed.
	if n.IsBigInt || ty.Float || ty.IR != "i64" || ty.IsDynamic() || ty.IsDate() {
		return Value{}, false
	}
	if ty.Signed {
		if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
			return Value{Ref: strconv.FormatInt(i, 10), Ty: ty}, true
		}
		return Value{}, false
	}
	if u, err := strconv.ParseUint(n.Value, 0, 64); err == nil {
		return Value{Ref: strconv.FormatUint(u, 10), Ty: ty}, true
	}
	return Value{}, false
}

// llvmDoubleLit formats a float64 as an LLVM `double` constant using the
// hexadecimal bit-pattern form (`0x` + 16 upper-hex digits) — exact and
// unambiguous for every value, avoiding the decimal-point/exponent pitfalls of
// FormatFloat (e.g. `7e+00`, which LLVM reads as an integer constant in a
// double slot). TDD-00123.
func llvmDoubleLit(f float64) string {
	return fmt.Sprintf("0x%016X", math.Float64bits(f))
}

func (e *Emitter) emitIdent(id *ast.Identifier) (Value, error) {
	if call, ok := e.tsSubtleAlias(id); ok {
		return e.emitExpr(call)
	}
	sym, ok := e.lookup(id.Name)
	if hidden, isEnum := e.enumObjects[id.Name]; isEnum && !ok {
		return e.emitIdent(ast.NewIdentifier(hidden, id.GetPos())) // the enum as a value
	}
	if !ok {
		if ns := e.wholeNamespace(id); ns != "" {
			return e.emitNSObject(ns, id.GetPos()) // the namespace as a value
		}
	}
	if ok && sym.NotDefined {
		e.ensureExceptionHelpers()
		errObj := e.buildErrorObj(errorKindIDs["ReferenceError"], e.internString(id.Name+" is not defined"), e.internString("ReferenceError"))
		e.excThrow(errObj)
		e.emitTerminator("unreachable")
		e.emitLabel(e.freshLabel("undef.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	if ok && sym.TDZ {
		// A parameter read by an earlier parameter's default (`(x = y, y)`):
		// still in its dead zone.
		e.ensureExceptionHelpers()
		errObj := e.buildErrorObj(errorKindIDs["ReferenceError"], e.internString("Cannot access '"+id.Name+"' before initialization"), e.internString("ReferenceError"))
		e.excThrow(errObj)
		e.emitTerminator("unreachable")
		e.emitLabel(e.freshLabel("tdz.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	if !ok {
		// A bare reference to a sibling namespace member from inside a
		// member function body (TDD-00148 Stage 4) — retried under the
		// mangled name. Only after the local lookup miss, so any local
		// shadows the sibling.
		if m := e.nsSibling(id.Name); m != "" {
			e.shadowReference(id, true)
			return e.emitIdent(ast.NewIdentifier(m, id.GetPos()))
		}
		// Bare NaN/Infinity globals (real JS also has these outside the
		// Number.* namespace) — only after a local lookup miss, so a
		// user-declared variable of the same name still shadows them.
		if bits, ok := globalNumberConsts[id.Name]; ok {
			e.shadowReference(id, false)
			return Value{Ref: bits, Ty: TypeF64}, nil
		}
		// A bare reference to a named function in a value position (`const g =
		// f`, `apply(f, ...)`) — materialize a closure value wrapping it via an
		// env-dropping trampoline. A direct call `f(...)` never reaches here
		// (emitCall dispatches a named callee straight to a direct call).
		// A generator function in value position (`return g`, `const G = g`) is
		// a first-class constructor closure — calling it builds an instance
		// (TDD-00129 Stage 3 escape; env packed here at the escape point).
		// Checked before resolveFuncRef: a nested generator is registered
		// there too (with only Gen info, no callable Sig), and the plain
		// funcref wrapper would emit a return-typeless function.
		if info, found := e.lookupGenerator(id.Name); found {
			e.shadowReference(id, true)
			shown := id.Name
			if n, ok := e.genShownNames[id.Name]; ok {
				shown = n
			}
			return e.emitGeneratorCtorClosure(info, shown, id.GetPos())
		}
		if mangled, sig, found := e.resolveFuncRef(id.Name); found {
			e.shadowReference(id, true)
			return e.emitNamedFuncValue(mangled, sig, id.Name), nil
		}
		// A generic class by value: its erased instantiation (every type
		// parameter `any`), the one class JavaScript has.
		if decl, ok := e.genericClasses[id.Name]; ok && !e.isShadowedByLocal(id.Name) {
			mangled, err := e.erasedGenericClass(decl)
			if err != nil {
				return Value{}, err
			}
			return e.emitClassRef(mangled), nil
		}
		// A generic function by value: its erased instantiation, every type
		// parameter `any`, as the one function JavaScript has.
		if decl, ok := e.genericFuncs[id.Name]; ok && !e.isShadowedByLocal(id.Name) {
			if mangled, sig, err := e.erasedGenericFunc(decl); err == nil {
				// Its results reach callers as values of any shape, read
				// through the layout table.
				e.noteBoxedLayout(sig.RetType)
				return e.emitNamedFuncValue(mangled, sig, id.Name), nil
			}
		}
		// A builtin function by value (`arr.map(parseFloat)`).
		if b, ok := e.builtinRef(id); ok {
			return e.emitBuiltinValue(b)
		}
		// A class in value position (`assert.throws(fn, MyError)`): a
		// constructor reference (emit_classref.go).
		if _, isClass := e.classes[id.Name]; isClass {
			e.shadowReference(id, true) // the class's declaration binds it
			if e.classRecordSites[id] {
				// A class expression's evaluation makes its record, then
				// initializes its statics.
				if err := e.emitClassRecord(id.Name); err != nil {
					return Value{}, err
				}
				e.emitClassReeval(id)
			}
			if rec, ok := e.lookup(id.Name + classRecSuffix); ok {
				// This evaluation's class (TDD-00242).
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, rec.Ptr))
				return Value{Ref: e.emitNbTagPtr(r, kmlTagFuncRef), Ty: TypeAny}, nil
			}
			return e.emitClassRef(id.Name), nil
		}
		// A built-in error constructor in value position (`assert.throws(
		// TypeError, fn)`, `x === RangeError`): a boxed funcref carrying the
		// interned constructor name (ADR-00289). `new TypeError(...)` and
		// `e instanceof TypeError` never reach here — both have their own
		// dedicated paths.
		if isErrorKindName(id.Name) {
			e.shadowReference(id, false)
			e.noteHostRef(id.Name)
			return Value{Ref: e.emitNbTagPtr(e.internString(id.Name), kmlTagFuncRef), Ty: TypeAny}, nil
		}
		// A builtin-module marker reaching the generic identifier path means
		// an unhandled member/usage of that module leaked past its dispatch —
		// name the module instead of the internal marker.
		if mod, ok := strings.CutSuffix(id.Name, "__kml_builtin"); ok {
			e.shadowReference(id, true)
			return Value{}, fmt.Errorf("%d:%d: this usage of the built-in '%s' module is not supported", id.GetPos().Line, id.GetPos().Col, mod)
		}
		// TDD-00129 Stage 2: a capturing nested function declaration referenced
		// (in value position) before its declaration statement hoists — emit it
		// on demand and yield the closure value.
		if ok, lerr := e.letrecEmitCapturingNested(id.Name); lerr != nil {
			return Value{}, lerr
		} else if ok {
			if _, found := e.lookup(id.Name); found {
				return e.emitExpr(id) // re-enter: the binding now exists
			}
		}
		// A builtin constructor in value position (`const M = Map`, `mk(Set)`):
		// a constructor reference carrying its name, as the Error kinds'.
		if sema.IsBuiltinConstructor(id.Name) || id.Name == "Promise" || (id.Name == "Object" || id.Name == "Function" || e.isLibConstructorValue(id)) && e.isGlobalName(id) {
			if e.inHostRow == id.Name {
				return Value{}, fmt.Errorf("%d:%d: %s's static is read through its own reference", id.GetPos().Line, id.GetPos().Col, id.Name)
			}
			e.shadowReference(id, false)
			e.noteHostRef(id.Name)
			return Value{Ref: e.emitNbTagPtr(e.internString(id.Name), kmlTagFuncRef), Ty: TypeAny}, nil
		}
		// `Math`, `JSON`, `console`, `Reflect`, `Atomics` as a value: the
		// object lib/node/internal_namespaces.ts makes.
		if key, ok := namespaceValueExport(id.Name); ok && (e.isGlobalName(id) || id.Name == "globalThis" && !e.isShadowedByLocal(id.Name)) {
			if m, ok := e.libExports[key]; ok {
				e.shadowReference(id, false)
				if id.Name == "globalThis" {
					if err := e.emitGlobalThisLinks(id.GetPos()); err != nil {
						return Value{}, err
					}
				}
				return e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(m, id.GetPos()), nil, id.GetPos()))
			}
		}
		// `process` as a value: its emitter object (lib/node/internal_process.ts).
		if id.Name == "process" {
			if obj, ok := e.processEmitterValue(id.GetPos()); ok {
				return e.emitExpr(obj)
			}
		}
		// A builtin function lowered to a library function (`const st =
		// setTimeout`): that function's value.
		if m, ok := e.loweredGlobalFn(id); ok {
			e.shadowReference(id, false)
			return e.emitExpr(ast.NewIdentifier(m, id.GetPos()))
		}
		// A parameter read before it is bound (`(x = y, y) => …`): JavaScript's
		// dead-zone ReferenceError when the default is evaluated.
		if e.unboundParam(id) {
			e.shadowReference(id, true)
			e.ensureExceptionHelpers()
			errObj := e.buildErrorObj(errorKindIDs["ReferenceError"], e.internString("Cannot access '"+id.Name+"' before initialization"), e.internString("ReferenceError"))
			e.excThrow(errObj)
			e.emitTerminator("unreachable")
			e.emitLabel(e.freshLabel("tdz.dead"))
			return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
		}
		// A name no scope declares, read in JavaScript (`-compat=js`):
		// its ReferenceError when the read runs ("s is not defined").
		if e.unresolvedReference(id) {
			e.shadowReference(id, false)
			return e.emitGlobalPropertyRead(id)
		}
		e.shadowReference(id, false)
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", id.GetPos().Line, id.GetPos().Col, id.Name)
	}
	e.shadowReference(id, true)
	if sym.NarrowedTo != nil {
		// A union-typed local flow-narrowed in this region (TDD-00114): load the
		// { i8, i64 } box and unbox it to the concrete narrowed type, so the
		// value reads as a real string/number/boolean (or a smaller union).
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", box, sym.Ptr))
		return e.emitUnboxBoxToType(box, e.flowNarrowedTy(id, sym)), nil
	}
	if sym.Ty.IsArray() {
		// A named array variable's slot holds its shared header pointer
		// (see emitArrayVarDecl/emitFunctionDecl's parameter handling).
		// See docs/adr/00/ADR-00061.md.
		// The binding's header is the value. It can be null: an absent
		// `T[] | undefined`, or a binding typed as an array holding undefined
		// at run time (a callback's data argument on error, through `any`),
		// which prints and boxes as undefined.
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, sym.Ptr))
		return Value{Ref: hdr, Ty: sym.Ty}, nil
	}
	if sym.isNullableScalarLocal() {
		// A nullable scalar is stored as { i1 present, T value } and reads
		// as that aggregate, like any nullable value; a read flow analysis
		// proved present is the bare payload.
		if sym.NarrowedNonNull || e.checkerPresent(id, sym.Ty) {
			payload := e.loadNullableScalarPayload(sym.Ptr, sym.Ty)
			return Value{Ref: payload, Ty: sym.Ty.withoutNullable()}, nil
		}
		return e.loadNullableScalarAgg(sym.Ptr, sym.Ty), nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))
	if isNullableScalar(sym.Ty) && e.checkerPresent(id, sym.Ty) {
		// A `T | undefined` parameter read where flow proves it present.
		return e.convert(Value{Ref: reg, Ty: sym.Ty}, sym.Ty.withoutNullable()), nil
	}
	if info, ok := e.checkerHostSubclass(id, sym.Ty); ok {
		// A Map, Set or RegExp the checker narrows to a class extending it
		// (TDD-00247): the instance starts at the word before its body.
		inst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", inst, reg))
		return Value{Ref: inst, Ty: info.Ty}, nil
	}
	if t, ok := e.checkerNarrowed(id, sym.Ty); ok {
		// An `any` the checker narrows to a primitive reads unboxed; a box
		// narrowed to an array (or Buffer) member unboxes to its header.
		if sym.Ty.IsDynamic() && (t.IsArray() || isNullableScalar(t) || t.IsClass() || isHostHandle(t) || t.IsReadableStream() || t.IsPromise()) {
			return e.emitUnboxBoxToType(reg, t), nil
		}
		return e.convert(Value{Ref: reg, Ty: sym.Ty}, t), nil
	}
	if e.checkerPresentRef(id, sym.Ty) || e.assignedString(id, sym.Ty) {
		return Value{Ref: reg, Ty: sym.Ty.withoutNullable()}, nil
	}
	return Value{Ref: reg, Ty: sym.Ty}, nil
}

// newArrayElemType is `new Array<T>(n)`'s element type: T, or any.
func newArrayElemType(e *Emitter, na *ast.NewArrayExpression) Type {
	if na.ElemType == nil {
		return TypeAny
	}
	return e.resolveType(na.ElemType)
}

// flowNarrowedTy is the type a flow-narrowed union local reads as: its
// narrowed type, or, when that is still a union, the one member the checker
// narrows the reference to (a type predicate's `Buffer.isBuffer(x)`, which
// no statement-level guard models).
func (e *Emitter) flowNarrowedTy(id *ast.Identifier, sym Symbol) Type {
	t := e.canonicalizeClassTy(*sym.NarrowedTo)
	if t.IsDynamic() && len(t.UnionMembers) > 0 {
		if nt, ok := e.checkerNarrowedUnion(id, t); ok {
			return e.canonicalizeClassTy(nt)
		}
	}
	return t
}

// isGlobalName reports whether id names the builtin global of its name, not
// a binding of the program's.
func (e *Emitter) isGlobalName(id *ast.Identifier) bool {
	c := e.front()
	return c != nil && e.isBuiltinGlobal(c, id)
}

// globalOnGlobalThis are the globals global modules implement that Node's
// global object carries, each with whether it is enumerable there
// (`performance` is; the classes are not). The others the modules export
// (`Worker`, `self`, `Histogram`, `IteratorHelper`, `ErrorEvent`,
// `EventSource`) are no property of Node's globalThis.
var globalOnGlobalThis = map[string]bool{
	"performance":  true,
	"SubtleCrypto": false, "Blob": false, "DataView": false, "TextEncoder": false,
	"TextDecoder": false, "Event": false, "CustomEvent": false, "EventTarget": false,
	"AbortSignal": false, "AbortController": false, "MessageEvent": false,
	"CloseEvent": false, "Headers": false, "URLSearchParams": false, "URL": false,
	"URLPattern": false, "WeakRef": false, "WebSocket": false,
	"PerformanceEntry": false, "PerformanceMark": false, "PerformanceMeasure": false,
	"PerformanceObserverEntryList": false, "PerformanceObserver": false,
	"Performance": false, "MessagePort": false, "MessageChannel": false,
	"BroadcastChannel": false,
}

// emitGlobalThisLinks puts the program's global-module globals on the
// global object before `globalThis` is read as a value
// (internal_namespaces.ts _kmlGlobalThisLink): the modules are in the program
// only when it names them, so the object cannot list them itself.
func (e *Emitter) emitGlobalThisLinks(pos ast.Pos) error {
	link, ok := e.libExports["internal_namespaces:_kmlGlobalThisLink"]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(e.globalLinks))
	for name := range e.globalLinks {
		if _, on := globalOnGlobalThis[name]; on {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		args := []ast.Expression{
			ast.NewStringLiteral(name, pos),
			ast.NewIdentifier(e.globalLinks[name], pos),
			ast.NewBooleanLiteral(globalOnGlobalThis[name], pos),
		}
		if _, err := e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(link, pos), args, pos)); err != nil {
			return err
		}
	}
	return nil
}

// namespaceValueExport is the library export that makes the global object
// named name when it is read as a value (`Math`, `crypto`, `globalThis`).
func namespaceValueExport(name string) (string, bool) {
	if name == "crypto" {
		return "internal_crypto_global:_kmlCrypto", true
	}
	maker, ok := resolver.NamespaceValueNames[name]
	return "internal_namespaces:" + maker, ok
}

// isLibConstructorValue reports a library global declared as a constructor
// object (`declare var Buffer: BufferConstructor`) with no implementation of
// its own as a value: it is boxed as a constructor reference.
func (e *Emitter) isLibConstructorValue(id *ast.Identifier) bool {
	c := e.front()
	return c != nil && isConstructorValue(c.TypeOf(id))
}

// globalNumberConsts are the global number constants, as IR double bits.
var globalNumberConsts = map[string]string{"NaN": "0x7FF8000000000000", "Infinity": "0x7FF0000000000000"}

// unresolvedReference reports id, under -compat=js, as a reference the
// binder looked up and found no declaration for (it reads a global no
// program or library declares).
func (e *Emitter) unresolvedReference(id *ast.Identifier) bool {
	if !e.compatJS() {
		return false
	}
	c := e.front()
	if c == nil {
		return false
	}
	b := c.Binding()
	if _, ns := namespaceValueExport(id.Name); ns {
		return false // the global object and the builtin namespaces
	}
	sym, _ := b.Resolve(id)
	return sym == nil && b.LookupScope(id) != nil
}

// emitNotDefined throws the ReferenceError reading an undeclared name
// raises ("s is not defined"); the value after it is unreachable.
func (e *Emitter) emitNotDefined(id *ast.Identifier) Value {
	e.ensureExceptionHelpers()
	errObj := e.buildErrorObj(errorKindIDs["ReferenceError"], e.internString(id.Name+" is not defined"), e.internString("ReferenceError"))
	e.excThrow(errObj)
	e.emitTerminator("unreachable")
	e.emitLabel(e.freshLabel("unresolved.dead"))
	return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
}

// emitGlobalPropertyRead reads an undeclared name as JavaScript does: the
// global object's property (one a sloppy assignment or `globalThis.x = …`
// made), else its ReferenceError.
func (e *Emitter) emitGlobalPropertyRead(id *ast.Identifier) (Value, error) {
	pos := id.GetPos()
	if key, ok := namespaceValueExport("globalThis"); !ok || e.libExports[key] == "" {
		// No global object linked: nothing gave it a property.
		return e.emitNotDefined(id), nil
	}
	g := ast.NewIdentifier("globalThis", pos)
	has, err := e.emitExpr(ast.NewBinaryExpression("in", ast.NewStringLiteral(id.Name, pos), g, pos))
	if err != nil {
		return Value{}, err
	}
	has = e.toBool(has)
	hitL, missL := e.freshLabel("global.has"), e.freshLabel("global.miss")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has.Ref, hitL, missL))
	e.emitLabel(missL)
	e.emitNotDefined(id)
	e.emitTerminator(fmt.Sprintf("br label %%%s", hitL))
	e.emitLabel(hitL)
	v, err := e.emitExpr(ast.NewMemberExpression(g, id.Name, pos))
	if err != nil {
		return Value{}, err
	}
	return e.emitBoxValue(v)
}

// parseJSFloat reads a decimal number literal as JS does: past the double
// range it is Infinity (`1e999`), below it 0, which strconv reports as a
// range error alongside that same value.
func parseJSFloat(v string) (float64, error) {
	f, err := strconv.ParseFloat(v, 64)
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		return f, nil
	}
	return f, err
}

// voidValueSites are the call expressions in argument and template
// interpolation position: their value is used even when they return nothing.
func (e *Emitter) voidValueSites() map[ast.Expression]bool {
	if e.voidSites != nil {
		return e.voidSites
	}
	e.voidSites = map[ast.Expression]bool{}
	mark := func(xs []ast.Expression) {
		for _, x := range xs {
			if c, ok := x.(*ast.CallExpression); ok {
				e.voidSites[c] = true
			}
		}
	}
	if e.prog != nil {
		ast.Inspect(e.prog, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpression:
				mark(x.Args)
			case *ast.NewExpression:
				mark(x.Args)
			case *ast.TemplateLiteral:
				mark(x.Exprs)
			}
			return true
		})
	}
	return e.voidSites
}
