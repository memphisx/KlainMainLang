// emit_exprs.go — core expression dispatch (emitExpr) plus the leaf literal
// emitters. Everything else expression-related lives in the other
// emit_exprs_*.go files: operators (emit_exprs_operators.go), assignment
// (emit_exprs_assign.go), member/index access (emit_exprs_member.go), type
// inference/conversion (emit_exprs_types.go), scalar coercion
// (emit_exprs_coerce.go), and variable declarations (emit_exprs_vardecl.go).
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func (e *Emitter) emitExpr(expr ast.Expression) (Value, error) {
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
		return e.emitCall(ex)
	case *ast.TaggedTemplateExpression:
		return e.emitCall(desugarTaggedTemplate(ex))
	case *ast.IndexExpression:
		return e.emitIndex(ex)
	case *ast.MemberExpression:
		return e.emitMember(ex)
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
		if ex.ElemType == nil {
			return Value{}, fmt.Errorf("%d:%d: new Array(n) needs an explicit element type here (e.g. new Array<number>(n)) — no declared target type is available in this position", ex.GetPos().Line, ex.GetPos().Col)
		}
		return e.emitNewArraySizedAggregate(ex, e.resolveType(ex.ElemType))
	case *ast.NewMapExpression:
		return e.emitNewMapValue(ex)
	case *ast.NewSetExpression:
		return e.emitNewSetValue(ex)
	case *ast.NewWeakMapExpression:
		return e.emitNewWeakMapValue(ex)
	case *ast.NewWeakSetExpression:
		return e.emitNewWeakSetValue(ex)
	case *ast.NewWeakRefExpression:
		return e.emitNewWeakRefValue(ex)
	case *ast.NewEventEmitterExpression:
		return e.emitNewEventEmitterValue(ex)
	case *ast.NewReadableStreamExpression:
		return e.emitNewReadableStream(ex)
	case *ast.NewWritableStreamExpression:
		return e.emitNewWritableStream(ex)
	case *ast.NewTransformStreamExpression:
		return e.emitNewTransformStream(ex)
	case *ast.NewCompressionStreamExpression:
		return e.emitNewCompressionStream(ex)
	case *ast.NewNodeStreamExpression:
		return e.emitNewNodeStream(ex)
	case *ast.NewErrorExpression:
		return e.emitNewError(ex)
	case *ast.NewDateExpression:
		return e.emitNewDate(ex)
	case *ast.ObjectLiteral:
		return e.emitObjectLiteral(ex)
	case *ast.ArrowFunction:
		return e.emitArrowFunction(ex)
	case *ast.FunctionExpression:
		return e.emitFunctionExpression(ex, nil)
	case *ast.TemplateLiteral:
		return e.emitTemplateLiteral(ex)
	case *ast.ConditionalExpression:
		return e.emitConditional(ex)
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
		// emitting a generator function's own body (emitGeneratorFunctionDecl)
		// — reached directly (not via that path) for a `yield` with no
		// enclosing generator function at all, since this compiler's parser
		// doesn't restrict `yield` to a generator body's own context (no
		// per-function context tracking to check that against at parse
		// time; see ADR-00171's own Investigation for why that was deferred
		// to codegen).
		if e.currentGenerator == nil {
			return Value{}, fmt.Errorf("%d:%d: 'yield' is only valid inside a generator function body", ex.GetPos().Line, ex.GetPos().Col)
		}
		return e.emitYieldExpression(ex)
	case *ast.ThisExpression:
		return e.emitThisExpression(ex.GetPos())
	case *ast.NewExpression:
		return e.emitNewExpression(ex)
	case *ast.NewURLExpression:
		return e.emitNewURLExpression(ex)
	case *ast.NewURLSearchParamsExpression:
		return e.emitNewURLSearchParamsExpression(ex)
	case *ast.NewURLPatternExpression:
		return e.emitNewURLPatternExpression(ex)
	case *ast.NewArrayBufferExpression:
		return e.emitNewArrayBufferExpression(ex)
	case *ast.NewBroadcastChannelExpression:
		return e.emitNewBroadcastChannelExpression(ex)
	case *ast.NewMessageChannelExpression:
		return e.emitNewMessageChannelExpression(ex)
	case *ast.NewTypedArrayExpression:
		return e.emitNewTypedArrayAggregate(ex)
	case *ast.ImportCallExpression:
		return e.emitImportCall(ex)
	case *ast.NewTextEncoderExpression:
		return Value{Ref: "null", Ty: TextEncoderType()}, nil
	case *ast.NewTextDecoderExpression:
		return e.emitNewTextDecoderExpression(ex)
	case *ast.NewRegExpExpression:
		return e.emitNewRegExpExpression(ex)
	case *ast.NewEventSourceExpression:
		return e.emitNewEventSourceExpression(ex)
	case *ast.NewEventTargetExpression:
		return e.emitNewEventTargetExpression()
	case *ast.NewHTTPAgentExpression:
		return e.emitNewHTTPAgent(ex)
	case *ast.NewWebviewExpression:
		return e.emitNewWebview(ex)
	case *ast.NewAbortControllerExpression:
		return e.emitNewAbortControllerExpression()
	case *ast.NewEventExpression:
		return e.emitNewEventExpression(ex)
	case *ast.NewCustomEventExpression:
		return e.emitNewCustomEventExpression(ex)
	case *ast.NewWebSocketExpression:
		return e.emitNewWebSocketClientExpression(ex)
	case *ast.NewWorkerExpression:
		return e.emitNewWorkerExpression(ex)
	case *ast.NewHeadersExpression:
		return e.emitNewHeadersExpression(ex)
	case *ast.NewDataViewExpression:
		return e.emitNewDataViewExpression(ex)
	case *ast.NewBlobExpression:
		return e.emitNewBlobExpression(ex)
	case *ast.NewRequestExpression:
		return e.emitNewRequestExpression(ex)
	case *ast.NewXMLHttpRequestExpression:
		return e.emitNewXMLHttpRequestExpression(ex)
	case *ast.ClassExpression:
		// A top-level `const X = class {...}` binding was already rewritten to
		// a nominal class declaration before emission (TDD-00063 Stage 4,
		// rewriteTopLevelClassExpressions). Reaching here means the class
		// expression was used as a value — an argument, a return, a nested or
		// non-top-level binding — which this compiler's nominal (non-first-
		// class) class model can't produce a runtime value for.
		return Value{}, fmt.Errorf("%d:%d: a class expression is only supported as a top-level `const/let/var X = class {...}` binding (V1) — using it as a value (an argument, a return value, or a nested/non-top-level binding) is not yet supported", ex.GetPos().Line, ex.GetPos().Col)
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
			return Value{}, fmt.Errorf("invalid numeric literal %q: %v", v, err)
		}
		return Value{Ref: llvmDoubleLit(float64(n64)), Ty: TypeF64}, nil
	}
	// A decimal exponent literal (`1e3`, `1.5e-2`) is normalized to the exact
	// double bit-pattern form — LLVM reads `e`-notation ambiguously in a double
	// slot (see llvmDoubleLit), so it can't be passed verbatim. A plain fraction
	// with no exponent still passes through verbatim (LLVM parses `1.5` fine).
	if strings.ContainsAny(v, "eE") {
		if f, ferr := strconv.ParseFloat(v, 64); ferr == nil {
			return Value{Ref: llvmDoubleLit(f), Ty: TypeF64}, nil
		}
		return Value{}, fmt.Errorf("invalid numeric literal %q", v)
	}
	if strings.ContainsRune(v, '.') {
		return Value{Ref: v, Ty: TypeF64}, nil
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
	if n.IsBigInt || ty.Float || ty.IR != "i64" {
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
	sym, ok := e.lookup(id.Name)
	if !ok {
		// A bare reference to a sibling namespace member from inside a
		// member function body (TDD-00148 Stage 4) — retried under the
		// mangled name. Only after the local lookup miss, so any local
		// shadows the sibling.
		if m := e.nsSibling(id.Name); m != "" {
			return e.emitIdent(ast.NewIdentifier(m, id.GetPos()))
		}
		// Bare NaN/Infinity globals (real JS also has these outside the
		// Number.* namespace) — only after a local lookup miss, so a
		// user-declared variable of the same name still shadows them.
		switch id.Name {
		case "NaN":
			return Value{Ref: "0x7FF8000000000000", Ty: TypeF64}, nil
		case "Infinity":
			return Value{Ref: "0x7FF0000000000000", Ty: TypeF64}, nil
		case "workerData":
			// TDD-00098: only meaningful inside a worker module; a local
			// binding of the same name shadows it via the lookup above.
			return e.emitWorkerDataRead(id.GetPos())
		}
		// A bare reference to a named function in a value position (`const g =
		// f`, `apply(f, ...)`) — materialize a closure value wrapping it via an
		// env-dropping trampoline. A direct call `f(...)` never reaches here
		// (emitCall dispatches a named callee straight to a direct call).
		if mangled, sig, found := e.resolveFuncRef(id.Name); found {
			return e.emitNamedFuncValue(mangled, sig), nil
		}
		// A built-in error constructor in value position (`assert.throws(
		// TypeError, fn)`, `x === RangeError`): a boxed funcref carrying the
		// interned constructor name (ADR-00289). `new TypeError(...)` and
		// `e instanceof TypeError` never reach here — both have their own
		// dedicated paths.
		if isErrorKindName(id.Name) {
			r0 := e.freshReg()
			r1 := e.freshReg()
			pay := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", pay, e.internString(id.Name)))
			e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } undef, i8 %d, 0", r0, kmlTagFuncRef))
			e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } %s, i64 %s, 1", r1, r0, pay))
			return Value{Ref: r1, Ty: TypeAny}, nil
		}
		// A builtin-module marker reaching the generic identifier path means
		// an unhandled member/usage of that module leaked past its dispatch —
		// name the module instead of the internal marker.
		if mod, ok := strings.CutSuffix(id.Name, "__kml_builtin"); ok {
			return Value{}, fmt.Errorf("%d:%d: this usage of the built-in '%s' module is not supported", id.GetPos().Line, id.GetPos().Col, mod)
		}
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", id.GetPos().Line, id.GetPos().Col, id.Name)
	}
	if sym.NarrowedTo != nil {
		// A union-typed local flow-narrowed in this region (TDD-00114): load the
		// { i8, i64 } box and unbox it to the concrete narrowed type, so the
		// value reads as a real string/number/boolean (or a smaller union).
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load { i8, i64 }, ptr %s, align 8", box, sym.Ptr))
		return e.emitUnboxBoxToType(box, *sym.NarrowedTo), nil
	}
	if sym.Ty.IsArray {
		// A named array variable is stored as two separate allocas
		// (Ptr/LenPtr — see emitArrayVarDecl/emitFunctionDecl's parameter
		// handling), not the single {ptr, i64} aggregate every other
		// context that evaluates an array as a plain value expects (return
		// values, .slice()/HOF results, struct field storage). A plain
		// `load sym.Ty.IR from sym.Ptr` — the path every other (non-array)
		// symbol takes below — would silently recover only the data
		// pointer and drop the length entirely. Rebuild the aggregate here
		// instead, once, so every caller of emitExpr on a bare array
		// identifier gets the same correctly-shaped Value a HOF result or
		// a return statement already would. See docs/adr/ADR-00061.md.
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, dataSlot))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
		return Value{Ref: r1, Ty: sym.Ty}, nil
	}
	if sym.isNullableScalarLocal() {
		// A nullable scalar is stored as { i1 present, T value }. Reading the
		// identifier into an ordinary expression auto-unwraps to the bare
		// payload (Nullable cleared) — the presence bit is consulted only at
		// the null-aware operators (emitNullCoalesce / the `=== null` path),
		// which read it straight from storage. See emit_nullable_scalar.go.
		payload := e.loadNullableScalarPayload(sym.Ptr, sym.Ty)
		return Value{Ref: payload, Ty: sym.Ty.withoutNullable()}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))
	return Value{Ref: reg, Ty: sym.Ty}, nil
}
