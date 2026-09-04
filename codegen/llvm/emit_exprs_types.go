package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitTemplateLiteral builds the concatenated result of a template literal.
func (e *Emitter) emitTemplateLiteral(tl *ast.TemplateLiteral) (Value, error) {
	acc := Value{Ref: e.internString(tl.Quasis[0]), Ty: TypePtr}
	for i, expr := range tl.Exprs {
		val, err := e.emitExpr(expr)
		if err != nil {
			return Value{}, err
		}
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: %w", tl.GetPos().Line, tl.GetPos().Col, err)
		}
		acc, err = e.emitStringConcat(acc, strVal)
		if err != nil {
			return Value{}, err
		}
		tail := Value{Ref: e.internString(tl.Quasis[i+1]), Ty: TypePtr}
		acc, err = e.emitStringConcat(acc, tail)
		if err != nil {
			return Value{}, err
		}
	}
	return acc, nil
}

// emitValueToString converts any value to a null-terminated string ptr.
// Strings pass through; numbers and bools are formatted via sprintf into a 32-byte scratch buffer.
func (e *Emitter) emitValueToString(v Value) (Value, error) {
	if v.Ty.IsDynamic {
		return e.emitDynamicToString(v)
	}
	if v.Ty.IsBigInt {
		// String(10n) / `${10n}` render the bare digits, no `n` suffix (that
		// suffix is console.log-only — see emit_call_console.go).
		return e.emitBigIntToString(v, false)
	}
	if v.Ty.IsSymbol {
		// V1 deliberately treats template-literal interpolation the same as
		// .toString()/console.log (both format as "Symbol(desc)"); real JS
		// is stricter and throws TypeError here — see docs/tdd/TDD-00044.md.
		return e.emitSymbolToString(v)
	}
	if v.Ty.IsNull {
		label := "null"
		if v.Ty.IsUndefined {
			label = "undefined"
		}
		return Value{Ref: e.internString(label), Ty: TypePtr}, nil
	}
	// A tuple stringifies to its elements joined by commas (TDD-00066),
	// matching real JS's `String([a, b])` / `${tuple}` — checked before the
	// generic ptr/object handling below (a tuple is structurally an object).
	if v.Ty.IsTuple {
		return e.emitTupleToString(v)
	}
	// An array stringifies to its elements joined by commas (real JS's
	// Array.prototype.toString / String([a,b]) / `${arr}`) — the same routine
	// arr.join() uses, with the default "," separator. A nested-array element
	// recurses through the join core's per-element emitValueToString, so
	// `String([[1,2],[3]])` renders "1,2,3". v.Ref is the {ptr,i64} aggregate.
	if v.Ty.IsArray && v.Ty.ElemType != nil {
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, v.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, v.Ref))
		sepVal := Value{Ref: e.internString(","), Ty: TypePtr}
		return e.emitArrayJoinCore(ptrReg, lenReg, *v.Ty.ElemType, sepVal)
	}
	// A nullable-scalar aggregate (a T|null field/return value, TDD-00064 Stage
	// 3) stringifies to its value's rendering when present, the literal "null"
	// when absent.
	if isNullableScalar(v.Ty) {
		present, payload := e.nullableScalarAggParts(v)
		payloadStr, err := e.emitValueToString(payload)
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, present, payloadStr.Ref, e.internString("null")))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	if v.Ty.IR == "ptr" && !v.Ty.IsObject && !v.Ty.IsArray && !v.Ty.IsFunc {
		// Nullable string: at runtime select "null" string when ptr is null.
		if v.Ty.Nullable {
			isNull := e.freshReg()
			result := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s",
				result, isNull, e.internString("null"), v.Ref))
			return Value{Ref: result, Ty: TypePtr}, nil
		}
		return v, nil
	}
	if isInspectableObject(v.Ty) {
		// A user-defined class `toString()` is honored in both modes — the
		// developer chose it (matching real JS's ToString).
		canon := e.canonicalizeClassTy(v.Ty)
		if canon.IsClass {
			if info, ok := e.classes[canon.ClassName]; ok {
				if _, has := info.MethodSigs["toString"]; has {
					return e.emitClassCall(canon, Value{Ref: v.Ref, Ty: canon}, "toString", nil, ast.Pos{}, false)
				}
			}
		}
		// An error subclass without its own toString() (TDD-00155 Stage 6):
		// JS's Error.prototype.toString — "name: message", or just the name
		// when the message is empty.
		if canon.IsClass {
			if info, ok := e.classes[canon.ClassName]; ok && info.IsErrorSubclass {
				loadStr := func(field string) string {
					idx, _, _ := info.Ty.FieldIndex(field)
					gep := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.Ty.StructIR(), v.Ref, idx))
					r := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, gep))
					return r
				}
				namePtr := loadStr("name")
				msgPtr := loadStr("message")
				joined, err := e.emitStringConcat(
					Value{Ref: namePtr, Ty: TypePtr},
					Value{Ref: e.internString(": "), Ty: TypePtr})
				if err != nil {
					return Value{}, err
				}
				full, err := e.emitStringConcat(joined, Value{Ref: msgPtr, Ty: TypePtr})
				if err != nil {
					return Value{}, err
				}
				lenGep := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", lenGep, msgPtr))
				msgLen := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", msgLen, lenGep))
				isEmpty := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, msgLen))
				res := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", res, isEmpty, namePtr, full.Ref))
				return Value{Ref: res, Ty: TypePtr}, nil
			}
		}
		// No user toString(): -compat=js gives JS's primitive `[object Object]`;
		// -compat=strict (default) gives the useful util.inspect view.
		if e.compatJS() {
			return Value{Ref: e.internString("[object Object]"), Ty: TypePtr}, nil
		}
		return e.emitInspectObject(v, 0)
	}
	e.ensureSprintf()
	scratch := e.emitStringScratch(32) // TDD-00120: length-prefixed; finalized below
	switch {
	case v.Ty.IR == "i1":
		truePtr := e.internString("true")
		falsePtr := e.internString("false")
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, v.Ref, truePtr, falsePtr))
		return Value{Ref: r, Ty: TypePtr}, nil
	case v.Ty.Float:
		val := v
		if v.Ty.IR == "float" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, v.Ref))
			val = Value{Ref: r, Ty: TypeF64}
		}
		// Shortest round-trip, JS-faithful formatting (TDD-00080) instead of
		// bare %g's 6-significant-digit truncation.
		e.ensureDtoa()
		e.emitInstr(fmt.Sprintf("call void @__kml_dtoa(ptr %s, double %s)", scratch, val.Ref))
	case v.Ty.IsInteger():
		val := v
		unsigned := !v.Ty.Signed
		if v.Ty.IR != "i64" {
			r := e.freshReg()
			ext := "sext"
			if unsigned {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to i64", r, ext, v.Ty.IR, v.Ref))
			val = Value{Ref: r, Ty: TypeI64}
		}
		// An unsigned 64-bit value with its high bit set (`uint64` above 2^63)
		// must print via `%llu`; `%lld` would render it as a negative number.
		// Narrow unsigned types zext to a non-negative i64, so `%llu` is correct
		// for every unsigned width (TDD-00123 — the integer escape hatch).
		intFmt := "%lld"
		if unsigned {
			intFmt = "%llu"
		}
		fmtPtr := e.internString(intFmt)
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, fmtPtr, val.Ref))
	default:
		return Value{}, fmt.Errorf("cannot convert type %s to string in template literal", v.Ty.IR)
	}
	e.emitStringFinalizeLen(scratch)
	return Value{Ref: scratch, Ty: TypePtr}, nil
}

// inferArrayType picks an element type by looking at the first element of a literal.
func (e *Emitter) inferArrayType(lit *ast.ArrayLiteral) Type {
	if len(lit.Elements) == 0 {
		return ArrayOf(TypeF64) // default: number[] (TDD-00123: number is float64)
	}
	first := lit.Elements[0]
	if sp, ok := first.(*ast.SpreadElement); ok {
		// Spread of an array — infer from the spread source.
		if ty := e.inferExprType(sp.Arg); ty.IsArray {
			return ty
		}
		return ArrayOf(TypeF64)
	}
	return ArrayOf(e.inferExprType(first))
}

// inferObjectType determines field types by inspecting the literal's values.
// inferObjectType computes the merged field layout for an object literal.
// A field's position is fixed by its first occurrence (via a spread's source
// fields or an explicit property); a later property or spread with the same
// name overrides its type in place rather than moving it — matching JS's
// object spread semantics, where re-assigning an existing key doesn't change
// its enumeration order.
func (e *Emitter) inferObjectType(lit *ast.ObjectLiteral) Type {
	if lit.HasComputedKey() {
		return e.inferDynamicObjectType(lit)
	}
	if lit.HasAccessors() {
		// TDD-00153: an accessor-bearing literal is a synthetic-class instance;
		// its type must be that class (registered lazily & idempotently so the
		// variable-slot type computed here matches the value the emit site
		// produces). Registration builds only class metadata, never IR.
		return e.classes[e.ensureObjLitClass(lit)].Ty
	}
	var fields []Field
	upsert := func(f Field) {
		for i, existing := range fields {
			if existing.Name == f.Name {
				fields[i] = f
				return
			}
		}
		fields = append(fields, f)
	}
	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			srcTy := e.inferExprType(spread.Arg)
			for _, f := range srcTy.VisibleFields() {
				upsert(f)
			}
			continue
		}
		upsert(Field{Name: prop.Key, Ty: e.inferExprType(prop.Value)})
	}
	return ObjectType(fields)
}

// inferDynamicObjectType computes the type of an object literal that has at
// least one computed property key (`{ [expr]: value }`). Storage-wise this is
// a real Map<string,V> (see docs/tdd/TDD-00012.md) — V is inferred from the
// first non-spread property's value, the same "first element wins"
// convention inferArrayType already uses for array literals rather than
// unifying types across every property.
func (e *Emitter) inferDynamicObjectType(lit *ast.ObjectLiteral) Type {
	valTy := TypeI64
	for _, prop := range lit.Properties {
		if _, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			continue
		}
		valTy = e.inferExprType(prop.Value)
		break
	}
	keyTy := TypePtr
	return Type{IR: "ptr", IsMap: true, IsDynamicObject: true, MapKey: &keyTy, MapVal: &valTy}
}

// callbackReturnType returns a HOF callback argument's inferred return
// type, purely (no codegen) — whether it's a literal arrow function, a
// named top-level function reference, or a closure-typed variable, the
// same three shapes resolveCallback (emit_func.go) resolves at emission
// time. Used by .map()/.flatMap()'s own inferExprType case: previously
// only a literal arrow function was recognized, so `arr.map(namedFn)`
// silently fell through to "same type as the receiver" — usually
// harmless by coincidence (a callback that happens to return the same
// element type the receiver already has), but genuinely wrong whenever it
// doesn't (found via `matrix.map(rowSum)`, matrix: number[][], rowSum
// returning a plain number — the result was mistyped as number[][]
// instead of number[]). Not a general callback-type inference utility;
// only handles what .map()/.flatMap() need.
// asyncClosureRetType normalizes an async arrow/function-expression's inferred
// return type into the task-shaped `Promise<T>` a call through the closure yields
// (TDD-00084 Part A): a non-async closure keeps ret; an async one wraps ret's
// inner type in a PromiseTask promise (unwrapping an already-`Promise<T>`
// annotation first), so `await`/`.then` on the result take the task path.
func asyncClosureRetType(isAsync bool, ret Type) Type {
	if !isAsync {
		return ret
	}
	inner := ret
	if ret.IsPromise && ret.PromiseType != nil {
		inner = *ret.PromiseType
	}
	pt := PromiseOf(inner)
	pt.PromiseTask = true
	return pt
}

// taskTaggedRet tags a method/function signature's return type PromiseTask when
// it's an async `Promise<T>`, so a later `await`/`.then` on the call takes the
// task path — the same treatment the plain-function call path applies (TDD-00087
// follow-up: async methods now emit task-struct promises like async functions).
func taskTaggedRet(sig FuncSig) Type {
	if sig.IsAsync && sig.RetType.IsPromise {
		rt := sig.RetType
		rt.PromiseTask = true
		return rt
	}
	return sig.RetType
}

// callbackReturnType infers a callback's return type for the array/promise
// method return-type inference. The optional paramHints supply the caller's
// known parameter types (e.g. a HOF receiver's element type) so an unannotated
// param resolves to the same type the actual emission (emitArrowFunctionWithHints)
// binds it to — without them an unannotated param would default to a scalar and
// the inferred type would disagree with the emitted one (TDD-00123: matters now
// that `number` is float64, so a mis-defaulted i64 corrupts the read type).
// hofElemHint returns the element type of an array/HOF receiver expression, to
// hint a callback's first parameter type during return-type inference. Falls
// back to TypeF64 (a bare `number`) when the receiver's element type isn't
// statically known — the correct default now that `number` is a double.
func (e *Emitter) hofElemHint(recv ast.Expression) Type {
	if ty := e.inferExprType(recv); ty.IsArray && ty.ElemType != nil {
		return *ty.ElemType
	}
	return TypeF64
}

func (e *Emitter) callbackReturnType(arg ast.Expression, paramHints ...Type) (Type, bool) {
	hintFor := func(i int, pt *ast.TypeAnnotation) Type {
		if pt != nil {
			return e.resolveType(pt)
		}
		if i < len(paramHints) {
			return paramHints[i]
		}
		return TypeF64
	}
	switch cb := arg.(type) {
	case *ast.ArrowFunction:
		if cb.RetType != nil {
			return e.resolveType(cb.RetType), true
		}
		if cb.Body != nil {
			e.pushScope()
			for i, p := range cb.Params {
				e.define(p.Name, Symbol{Ty: hintFor(i, p.Type)})
			}
			rt := e.inferExprType(cb.Body)
			e.popScope()
			return rt, true
		}
		if cb.Block != nil {
			paramNames := make([]string, len(cb.Params))
			paramTypes := make([]Type, len(cb.Params))
			for i, p := range cb.Params {
				paramNames[i] = p.Name
				paramTypes[i] = hintFor(i, p.Type)
			}
			if inferred, ok := e.inferUnannotatedReturnType(cb.Block, paramNames, paramTypes); ok {
				return inferred, true
			}
		}
		return TypeF64, true
	case *ast.FunctionExpression:
		if cb.RetType != nil {
			return e.resolveType(cb.RetType), true
		}
		paramNames := make([]string, len(cb.Params))
		paramTypes := make([]Type, len(cb.Params))
		for i, p := range cb.Params {
			paramNames[i] = p.Name
			paramTypes[i] = hintFor(i, p.Type)
		}
		if inferred, ok := e.inferUnannotatedReturnType(cb.Body, paramNames, paramTypes); ok {
			return inferred, true
		}
		return TypeVoid, true
	case *ast.Identifier:
		if _, sig, found := e.resolveFuncRef(cb.Name); found {
			return sig.RetType, true
		}
		if sym, found := e.lookup(cb.Name); found && sym.Ty.IsFunc && sym.Ty.FuncRetType != nil {
			return *sym.Ty.FuncRetType, true
		}
	}
	return Type{}, false
}

func (e *Emitter) inferExprType(expr ast.Expression) Type {
	// `globalThis.X` (and its call form) infers as the bare global X — mirror
	// the same alias-peeling emitMember/emitCall apply, so member-type-driven
	// dispatch agrees with codegen.
	if mem, ok := expr.(*ast.MemberExpression); ok {
		if unwrapped := e.unwrapGlobalThis(mem); unwrapped != ast.Expression(mem) {
			return e.inferExprType(unwrapped)
		}
	}
	if call, ok := expr.(*ast.CallExpression); ok {
		if unwrapped := e.unwrapGlobalThis(call.Callee); unwrapped != call.Callee {
			rewritten := ast.NewCallExpression(unwrapped, call.Args, call.GetPos())
			rewritten.TypeArgs = call.TypeArgs
			return e.inferExprType(rewritten)
		}
	}
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		if ex.IsBigInt {
			return BigIntType()
		}
		// TDD-00123 Stage 1: every numeric literal is a double (mirrors
		// emitNumberLit). Real integers come from the explicit intN/uintN types.
		return TypeF64
	case *ast.BooleanLiteral:
		return TypeBool
	case *ast.StringLiteral:
		return TypePtr
	case *ast.TemplateLiteral:
		return TypePtr
	case *ast.NullLiteral:
		if ex.IsUndefined {
			return TypeUndefined
		}
		return TypeNull
	case *ast.AwaitExpression:
		// Unwrap Promise<T> → T.
		argTy := e.inferExprType(ex.Argument)
		if argTy.IsPromise {
			if argTy.PromiseType != nil {
				return *argTy.PromiseType
			}
			return TypeVoid
		}
		// `await` of a non-thenable is identity — its type is the argument's
		// own (e.g. Response.text() → string), matching emitAwait's pass-through.
		return argTy
	case *ast.Identifier:
		if sym, ok := e.lookup(ex.Name); ok {
			// A union local flow-narrowed in this region reads as its narrowed
			// type (TDD-00114), matching emitIdent's own unboxing.
			if sym.NarrowedTo != nil {
				return *sym.NarrowedTo
			}
			return sym.Ty
		}
		switch ex.Name {
		case "NaN", "Infinity":
			return TypeF64
		}
		if _, _, ok := e.resolveFuncRef(ex.Name); ok {
			return Type{IR: "ptr", IsFunc: true}
		}
		if isErrorKindName(ex.Name) {
			// Built-in error constructor in value position — a boxed funcref
			// (must match emitIdent's own emission).
			return TypeAny
		}
	case *ast.IndexExpression:
		if e.isProcessEnvExpr(ex.Object) {
			return TypePtr
		}
		objTy := e.inferExprType(ex.Object)
		// A bracket read off a bare any/unknown base is itself dynamic
		// (TDD-00155): the runtime tag dispatch yields another box.
		if isUnconstrainedDynamic(objTy) {
			return TypeAny
		}
		// String-keyed Map bracket access yields the value type (TDD-00139).
		if objTy.IsMap && objTy.MapKey != nil && isPlainStringType(*objTy.MapKey) {
			if objTy.MapVal != nil {
				return *objTy.MapVal
			}
			return TypePtr
		}
		if objTy.IsGroupMap {
			if objTy.ElemType != nil {
				return ArrayOf(*objTy.ElemType)
			}
			return ArrayOf(TypeI64)
		}
		if isStringTy(objTy) {
			return TypePtr
		}
		// Tuple constant-index (TDD-00066): `t[i]` has the type of field "i".
		if objTy.IsTuple {
			if idx, ok := tupleConstIndex(ex.Index); ok && idx < int64(len(objTy.Fields)) {
				return objTy.Fields[idx].Ty
			}
		}
		// TDD-00101: BigInt64Array/BigUint64Array elements surface as bigint.
		if objTy.BigIntElem {
			return BigIntType()
		}
		if objTy.IsArray && objTy.ElemType != nil {
			return *objTy.ElemType
		}
	case *ast.BinaryExpression:
		// Infer each operand's type exactly once, up front, and reuse it in
		// every branch below. Re-calling inferExprType(ex.Left/Right) separately
		// in the bigint check AND again in each operator branch made inference
		// cost O(2^depth) on a left-associative chain like `a + b + c + ...`
		// (each node re-inferred both children, which re-inferred theirs …) —
		// enough to peg a core for minutes on a deep expression, found as an
		// in-process hang while running the Test262 corpus. inferExprType does
		// no codegen and has no side effects, so inferring both operands eagerly
		// — even for `&&`/`||`/`??`, which may not need the right one — is free;
		// the bigint check already forced both anyway.
		lt := e.inferExprType(ex.Left)
		rt := e.inferExprType(ex.Right)
		// A dynamic operand under `-compat=js` makes an arithmetic result
		// dynamic — the runtime dispatch (TDD-00076 A2) yields a NaN-boxed
		// word (`+` may concatenate, others produce a boxed number); a
		// comparison stays a bool.
		if e.compatJS() && (isUnconstrainedDynamic(lt) || isUnconstrainedDynamic(rt)) {
			switch ex.Op {
			case "===", "!==", "==", "!=", "<", ">", "<=", ">=":
				return TypeBool
			case "+", "-", "*", "/", "%", "**":
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
		// A bigint operand makes an arithmetic/bitwise result a bigint (a
		// comparison stays a bool) — so `const x = 2n ** 53n + 1n` types as
		// bigint, not the i64/string the generic cases below would infer.
		if lt.IsBigInt || rt.IsBigInt {
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
			// Date + number / number + Date: add a duration, stays a Date
			// (see emitBinary for the full Date-arithmetic rules).
			if lt.IsDate != rt.IsDate {
				return TypeDate
			}
			if lt.Float || rt.Float {
				// Mixed int/float promotes to double — must match
				// emitBinary's numeric-promotion rule.
				return TypeF64
			}
			return TypeI64
		case "-":
			// Date - Date: a plain number (ms difference). Date - number:
			// subtract a duration, stays a Date. number - Date is rejected
			// by emitBinary, so its inferred type here is moot.
			if lt.IsDate && rt.IsDate {
				return TypeI64
			}
			if lt.IsDate && !rt.IsDate {
				return TypeDate
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
			return lt
		case "??":
			if lt.IR == "ptr" {
				return rt
			}
			return lt
		case "&", "|", "^", "<<", ">>", ">>>":
			// JS bitwise/shift ops compute in the 32-bit integer domain but
			// return a Number (double) — TDD-00123 Stage 2. Must mirror
			// emitBinary/emitBitShift's sitofp-to-double result.
			return TypeF64
		}
	case *ast.MemberExpression:
		// `F.prototype` on a recognized prototype constructor is a dynamic
		// object (TDD-00155 Stage 4).
		if id, ok := ex.Object.(*ast.Identifier); ok && e.compatJS() && e.jsProtoCtor[id.Name] && ex.Property == "prototype" {
			return TypeAny
		}
		// A property read off a bare any/unknown base is itself dynamic
		// (TDD-00155): the runtime tag dispatch yields another box.
		if baseTy := e.inferExprType(ex.Object); isUnconstrainedDynamic(baseTy) {
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
		if objTy := e.inferExprType(ex.Object); objTy.IsDCChannel {
			if ex.Property == "hasSubscribers" {
				return TypeBool
			}
			if ex.Property == "name" {
				return TypePtr
			}
		}
		// node:sqlite computed properties (ADR-00540).
		if ex.Property == "isTransaction" && e.inferExprType(ex.Object).IsSQLiteDatabase {
			return TypeBool
		}
		if ex.Property == "expandedSQL" && e.inferExprType(ex.Object).IsSQLiteStatement {
			return TypePtr
		}
		// res.statusCode (ServerResponse, TDD-00131) — the response status,
		// stored as the object's i64 `status` field.
		if ex.Property == "statusCode" {
			if e.inferExprType(ex.Object).IsServerResponse {
				return TypeI64
			}
		}
		// http2.constants members (TDD-00139 Stage 4).
		if e.isH2ConstantsExpr(ex.Object) {
			if c, ok := h2Constants[ex.Property]; ok && c.isStr {
				return TypePtr
			}
			return TypeF64
		}
		if ex.Property == "constants" {
			if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "http2__kml_builtin" {
				ty := TypeI64
				ty.IsH2Constants = true
				return ty
			}
		}
		// `.length` on an array/string/tuple/typed-array is a Number (double)
		// — TDD-00123 Stage 3. Must mirror emitMemberExpr's `.length` sites,
		// which sitofp the i64 count to double. (An object field literally
		// named `length` is resolved by the field-lookup path further below.)
		if ex.Property == "length" {
			ot := e.inferExprType(ex.Object)
			if ot.IsArray || ot.IsTuple || ot.IsTypedArray || isStringTy(ot) {
				return TypeF64
			}
		}
		// cluster.isPrimary/isWorker (bool), cluster.workerId (i64).
		if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "cluster__kml_builtin" {
			switch ex.Property {
			case "isPrimary", "isWorker":
				return TypeBool
			case "workerId":
				return TypeI64
			}
		}
		// cluster.fork() Worker's `.id`.
		if ex.Property == "process" && e.inferExprType(ex.Object).IsClusterWorker {
			return ChildProcessType()
		}
		if ex.Property == "id" && e.inferExprType(ex.Object).IsClusterWorker {
			return TypeI64
		}
		// ChildProcess members — must match emitChildProcessMember.
		if ex.Property == "stdout" || ex.Property == "stderr" || ex.Property == "stdin" || ex.Property == "pid" {
			if objTy := e.inferExprType(ex.Object); objTy.IsChildProcess {
				switch ex.Property {
				case "stdout":
					return CPStreamType(0)
				case "stderr":
					return CPStreamType(1)
				case "stdin":
					return CPStdinType()
				case "pid":
					return TypeI64
				}
			}
		}
		// Response.body — must match emitResponseBodyStream (TDD-00097 St. 4).
		if ex.Property == "body" {
			if objTy := e.inferExprType(ex.Object); objTy.IsResponse {
				return ReadableStreamType(TypedArrayType("uint8"))
			}
		}
		// Response.headers — must match the ADR-00490 emit path.
		if ex.Property == "headers" {
			if objTy := e.inferExprType(ex.Object); objTy.IsResponse {
				return HeadersType()
			}
		}
		// TransformStream sides — must match emitTransformStreamProperty.
		if ex.Property == "readable" || ex.Property == "writable" {
			if objTy := e.inferExprType(ex.Object); objTy.IsTransformStream {
				if ex.Property == "readable" {
					if objTy.StreamOut != nil {
						return ReadableStreamType(*objTy.StreamOut)
					}
					return ReadableStreamType(TypeI64)
				}
				if objTy.StreamChunk != nil {
					return WritableStreamType(*objTy.StreamChunk)
				}
				return WritableStreamType(TypeI64)
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
		// MessageChannel ports (TDD-00099) — must match
		// emitMessageChannelPortRead.
		if ex.Property == "port1" || ex.Property == "port2" {
			if objTy := e.inferExprType(ex.Object); objTy.IsMessageChannel {
				return MessagePortType(*objTy.ElemType)
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
		// DataView properties — must match emitDataViewProp.
		if ex.Property == "byteLength" || ex.Property == "byteOffset" || ex.Property == "buffer" {
			if objTy := e.inferExprType(ex.Object); objTy.IsDataView {
				if ex.Property == "buffer" {
					return ArrayBufferType()
				}
				return TypeI64
			}
		}
		// Blob properties — must match emitBlobProp.
		if ex.Property == "size" || ex.Property == "type" {
			if objTy := e.inferExprType(ex.Object); objTy.IsBlob {
				if ex.Property == "size" {
					return TypeI64
				}
				return TypePtr
			}
		}
		// CryptoKey properties — must match emitCryptoKeyProp.
		if ex.Property == "type" || ex.Property == "extractable" {
			if objTy := e.inferExprType(ex.Object); objTy.IsCryptoKey {
				if ex.Property == "type" {
					return TypePtr
				}
				return TypeBool
			}
		}
		// CryptoKeyPair properties — must match emitCryptoKeyPairProp.
		if ex.Property == "publicKey" || ex.Property == "privateKey" {
			if e.inferExprType(ex.Object).IsCryptoKeyPair {
				return CryptoKeyType()
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
		// Static field read: ClassName.staticField (TDD-00009 Stage 4).
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if info, found := e.classes[id.Name]; found {
				if fty, ok := info.StaticFieldTypes[ex.Property]; ok {
					return fty
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
			case "Math":
				switch ex.Property {
				case "PI", "E", "LN2", "LN10", "SQRT2", "LOG2E", "LOG10E":
					return TypeF64
				}
			case "Number":
				switch ex.Property {
				case "MAX_SAFE_INTEGER", "MIN_SAFE_INTEGER":
					return TypeI64
				case "EPSILON", "MAX_VALUE", "MIN_VALUE", "POSITIVE_INFINITY", "NEGATIVE_INFINITY", "NaN":
					return TypeF64
				}
			case "process":
				switch ex.Property {
				case "argv":
					return ArrayOf(TypePtr)
				case "pid", "exitCode":
					return TypeI64
				case "platform", "arch", "execPath", "version":
					return TypePtr
				case "versions":
					return processVersionsType()
				case "stdin":
					return StdinType()
				}
			case "path__kml_builtin":
				switch ex.Property {
				case "sep", "delimiter":
					return TypePtr
				}
			case "os__kml_builtin":
				switch ex.Property {
				case "EOL":
					return TypePtr
				}
			}
		}
		if e.isProcessEnvExpr(ex.Object) {
			return TypePtr
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
				return e.canonicalizeClassTy(fieldTy)
			}
		}
	case *ast.ThisExpression:
		if sym, ok := e.lookup("this"); ok {
			return sym.Ty
		}
	case *ast.NewExpression:
		// new Promise<T>(executor) → task Promise<T> (default number) — TDD-00087.
		if ex.ClassName == "Promise" {
			valTy := TypeI64
			if len(ex.TypeArgs) == 1 {
				valTy = e.resolveType(ex.TypeArgs[0])
			}
			pt := PromiseOf(valTy)
			pt.PromiseTask = true
			return pt
		}
		// new WebSocketServer({server}) → the klain:ws handle (TDD-00158).
		if ex.ClassName == "WebSocketServer" && e.usedKlainWS {
			return WebSocketServerType()
		}
		// new PerformanceObserver(cb) → the perf_hooks handle (TDD-00166).
		if ex.ClassName == "PerformanceObserver" {
			return PerfObserverType()
		}
		// new AsyncLocalStorage<T>() → the async_hooks handle (TDD-00168).
		if ex.ClassName == "AsyncLocalStorage" {
			elem := TypeAny
			if len(ex.TypeArgs) == 1 && ex.TypeArgs[0] != nil {
				elem = e.resolveType(ex.TypeArgs[0])
			}
			return AsyncLocalStorageType(elem)
		}
		// new AsyncResource(name?, opts?) → the async_hooks handle (TDD-00168 St 4).
		if ex.ClassName == "AsyncResource" {
			return AsyncResourceType()
		}
		if info, ok := e.classes[ex.ClassName]; ok {
			return info.Ty
		}
		// A vanilla-JS prototype-constructor instance is a dynamic object
		// (TDD-00155 Stage 4, `-compat=js`); a Proxy is one too (Stage 7).
		if ex.ClassName == "Proxy" {
			return TypeAny
		}
		if e.compatJS() && e.jsProtoCtor[ex.ClassName] {
			return TypeAny
		}
		// A generic class (TDD-00010 V1) is never itself in e.classes — ask
		// genericClassInstanceType for the shape its `new
		// ClassName<T>(...)` instantiation would have, purely (no IR
		// emission, no e.classes registration): inferExprType must never
		// trigger real emission as a side effect of merely asking "what
		// type is this."
		if genDecl, ok := e.genericClasses[ex.ClassName]; ok && len(ex.TypeArgs) == len(genDecl.TypeParams) {
			subs := e.buildTypeArgSubs(genDecl.TypeParams, ex.TypeArgs)
			if ty, err := e.genericClassInstanceType(genDecl, subs); err == nil {
				return ty
			}
		}
	case *ast.TaggedTemplateExpression:
		// String.raw always yields a string (ADR-00562); other tags: same
		// desugaring emitExpr's own case uses — a tagged template's type is
		// exactly its tag function's return type (TDD-00059).
		if e.isStringRawTag(ex.Tag) {
			return TypePtr // a string
		}
		return e.inferExprType(desugarTaggedTemplate(ex))
	case *ast.CallExpression:
		// klain:assets (TDD-00142 Stage 7): embedDir(...) → EmbeddedAssets,
		// assets.get(...) → ArrayBuffer.
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			// Symbol.for / Symbol.keyFor (ADR-00488) — must match emitSymbolStatic.
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Symbol" && !e.isShadowedByLocal("Symbol") {
				if mem.Property == "for" {
					return SymbolType()
				}
				if mem.Property == "keyFor" {
					nt := TypePtr
					nt.Nullable = true
					return nt
				}
			}
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "assets__kml_builtin" && mem.Property == "embedDir" {
				return EmbeddedAssetsType()
			}
			if mem.Property == "get" && e.inferExprType(mem.Object).IsEmbeddedAssets {
				return ArrayBufferType()
			}
			// PerformanceObserver.observe/disconnect → void; the list's
			// getEntries() → PerformanceEntry[] (TDD-00166).
			if mt := e.inferExprType(mem.Object); mt.IsPerfObserver {
				return TypeVoid
			}
			if mem.Property == "getEntries" && e.inferExprType(mem.Object).IsPerfEntryList {
				return ArrayOf(PerformanceEntryType())
			}
			// WSMessageEvent.dataBytes() → ArrayBuffer (TDD-00160): the
			// byte-exact accessor for a binary frame, guarded structurally on
			// the hidden byte field the event type carries.
			if mem.Property == "dataBytes" {
				if ot := e.inferExprType(mem.Object); ot.IsObject {
					if _, _, ok := ot.FieldIndex(WSMsgBytesField); ok {
						return ArrayBufferType()
					}
				}
			}
			// node:sqlite (ADR-00540): db.prepare() → StatementSync; the
			// StatementSync reads take their row shape from an explicit call-site
			// type argument (`stmt.all<Row>()` / `stmt.get<Row>()`), matching the
			// Node .d.ts get<T>()/all<T>() signatures.
			if ot := e.inferExprType(mem.Object); ot.IsHash || ot.IsHmac {
				switch mem.Property {
				case "update":
					return ot // chainable (Hash or Hmac)
				case "digest":
					// an encoding → string; omitted → Buffer.
					if len(ex.Args) == 1 {
						return TypePtr
					}
					return TypedArrayType("uint8")
				}
			}
			if e.inferExprType(mem.Object).IsSQLiteDatabase {
				switch mem.Property {
				case "prepare":
					return SQLiteStatementType()
				case "location":
					nt := TypePtr
					nt.Nullable = true
					return nt
				case "exec", "close", "open", "function", "aggregate":
					return TypeVoid
				}
			}
			if e.inferExprType(mem.Object).IsSQLiteStatement {
				switch mem.Property {
				case "all", "iterate":
					if len(ex.TypeArgs) == 1 {
						return ArrayOf(e.resolveType(ex.TypeArgs[0]))
					}
					return ArrayOf(TypePtr)
				case "get":
					if len(ex.TypeArgs) == 1 {
						rt := e.resolveType(ex.TypeArgs[0])
						rt.Nullable = true
						return rt
					}
					nt := TypePtr
					nt.Nullable = true
					return nt
				case "run":
					return SQLiteRunResultType()
				case "columns":
					return ArrayOf(SQLiteColumnMetaType())
				case "setReadBigInts", "setAllowBareNamedParameters":
					return TypeVoid
				}
			}
		}
		// Static method call: ClassName.staticMethod(args) (TDD-00009 Stage 4).
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if id, ok := mem.Object.(*ast.Identifier); ok {
				if info, found := e.classes[id.Name]; found {
					if sig, ok := info.StaticMethodSigs[mem.Property]; ok {
						return taskTaggedRet(sig)
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
				if info, ok := e.classes[objTy.ClassName]; ok {
					if sig, ok := info.MethodSigs[mem.Property]; ok {
						return taskTaggedRet(sig)
					}
					// EventEmitter-embedded chainable methods (TDD-00023)
					// return the *class* type (not a bare EventEmitterType),
					// so `x.on(...).on(...)` and `x.on(...).someMethod()`
					// both type-check correctly after chaining.
					if info.HasEventEmitter {
						switch mem.Property {
						case "on", "once", "off", "removeListener", "removeAllListeners":
							return objTy
						case "emit":
							return TypeBool
						case "listenerCount":
							return TypeI64
						case "eventNames":
							return ArrayOf(TypePtr)
						}
					}
				}
			}
		}
		// EventTarget bus (TDD-00081): dispatchEvent returns bool; add/remove
		// return void.
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if e.inferExprType(mem.Object).IsEventTarget {
				switch mem.Property {
				case "dispatchEvent":
					return TypeBool
				case "addEventListener", "removeEventListener":
					return TypeVoid
				}
			}
		}
		// AbortController.abort() returns void (TDD-00081 Stage 3).
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if mem.Property == "abort" && e.inferExprType(mem.Object).IsAbortController {
				return TypeVoid
			}
			// Static AbortSignal.timeout/abort/any → AbortSignal (Stage 3).
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "AbortSignal" {
				switch mem.Property {
				case "timeout", "abort", "any":
					return AbortSignalType()
				}
			}
		}
		// Event/CustomEvent methods (TDD-00081) all return void — matters so an
		// expression-body arrow `(e) => e.preventDefault()` infers a void return
		// rather than the i64 default (which emits a malformed `ret i64`).
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if e.inferExprType(mem.Object).IsEvent &&
				(mem.Property == "preventDefault" || mem.Property == "stopPropagation" || mem.Property == "stopImmediatePropagation") {
				return TypeVoid
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
			if objTy := e.inferExprType(mem.Object); objTy.IsFunc && !objTy.FuncHasRest {
				boundCount := len(ex.Args) - 1
				if boundCount < 0 {
					boundCount = 0
				}
				if boundCount <= len(objTy.FuncParams) {
					ret := TypeVoid
					if objTy.FuncRetType != nil {
						ret = *objTy.FuncRetType
					}
					return FuncType(objTy.FuncParams[boundCount:], ret)
				}
			}
		}
		// net.Server/net.Socket .address() → { address, family, port } (TDD-00131).
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok && mem.Property == "address" {
			if objTy := e.inferExprType(mem.Object); objTy.IsNetServer || objTy.IsNetSocket || objTy.IsHTTPServer || objTy.IsDgramSocket {
				return netAddressType()
			}
		}
		// Node-stream method results (TDD-00097 Stage 8).
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if objTy := e.inferExprType(mem.Object); objTy.IsNodeReadable || objTy.IsNodeWritable {
				switch mem.Property {
				case "on", "once", "pause", "resume", "end", "destroy", "setEncoding", "unshift":
					return objTy
				case "read":
					// Sync read yields the chunk type (ADR-00484).
					if objTy.StreamOut != nil {
						return *objTy.StreamOut
					}
					return TypePtr
				case "push", "write":
					return TypeBool
				case "pipe":
					if len(ex.Args) == 1 {
						return e.inferExprType(ex.Args[0])
					}
				case "toWeb":
					if objTy.IsNodeReadable && !objTy.IsNodeWritable {
						out := TypeI64
						if objTy.StreamOut != nil {
							out = *objTy.StreamOut
						}
						return ReadableStreamType(out)
					}
					in := TypeI64
					if objTy.StreamChunk != nil {
						in = *objTy.StreamChunk
					}
					return WritableStreamType(in)
				}
			}
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Readable" && (mem.Property == "from" || mem.Property == "fromWeb") && len(ex.Args) == 1 {
				if _, found := e.lookup(id.Name); !found {
					argTy := e.inferExprType(ex.Args[0])
					out := TypeI64
					if argTy.IsArray && argTy.ElemType != nil {
						out = *argTy.ElemType
					} else if argTy.IsReadableStream && argTy.StreamChunk != nil {
						out = *argTy.StreamChunk
					}
					return NodeReadableType(out)
				}
			}
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "streampromises__kml_builtin" {
				pt := PromiseOf(TypeVoid)
				pt.PromiseTask = true
				return pt
			}
			// Chained `http.createServer(cb).listen(...)` yields the server
			// handle (Node's listen() returns the server).
			if _, _, isChain := chainedCreateServerListen(ex); isChain {
				return HTTPServerType()
			}
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "stream__kml_builtin" {
				// Callback forms: finished() yields nothing; pipeline()
				// yields its destination (last-stream) argument;
				// duplexPair() yields a 2-element array of string Duplexes.
				if mem.Property == "pipeline" && len(ex.Args) >= 2 {
					return e.inferExprType(ex.Args[len(ex.Args)-2])
				}
				if mem.Property == "duplexPair" {
					return ArrayOf(NodeTransformType(TypePtr, TypePtr))
				}
				return TypeVoid
			}
		}
		// Stream/reader/controller method results (TDD-00097 Stage 1) —
		// checked before the property-name-based chains, same as generators.
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if objTy := e.inferExprType(mem.Object); objTy.IsReadableStream || objTy.IsStreamReader || objTy.IsRSController || objTy.IsWritableStream || objTy.IsStreamWriter || objTy.IsWSController {
				chunkTy := TypeI64
				if objTy.StreamChunk != nil {
					chunkTy = *objTy.StreamChunk
				}
				switch mem.Property {
				case "getReader", "values":
					return StreamReaderType(chunkTy)
				case "getWriter":
					return WSWriterType(chunkTy)
				case "read":
					pt := PromiseOf(genNextResultType(chunkTy))
					pt.PromiseTask = true
					return pt
				case "cancel", "write", "close", "abort", "pipeTo":
					pt := PromiseOf(TypeVoid)
					pt.PromiseTask = true
					return pt
				case "pipeThrough":
					if len(ex.Args) >= 1 {
						if tTy := e.inferExprType(ex.Args[0]); tTy.IsTransformStream && tTy.StreamOut != nil {
							return ReadableStreamType(*tTy.StreamOut)
						}
					}
					return ReadableStreamType(TypeI64)
				case "tee":
					return ArrayOf(ReadableStreamType(chunkTy))
				}
			}
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "ReadableStream" && mem.Property == "from" && len(ex.Args) == 1 {
				if _, found := e.lookup(id.Name); !found {
					if argTy := e.inferExprType(ex.Args[0]); argTy.IsArray {
						return ReadableStreamType(*argTy.ElemType)
					}
				}
			}
		}
		// AsyncLocalStorage instance-method result types (TDD-00168):
		// run/exit evaluate to their callback's own return type; getStore to
		// the stored element type T; enterWith/disable to void.
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if objTy := e.inferExprType(mem.Object); objTy.IsAsyncLocalStorage {
				switch mem.Property {
				case "getStore":
					if objTy.ElemType != nil {
						return *objTy.ElemType
					}
					return TypeAny
				case "run":
					if len(ex.Args) >= 2 {
						if cbTy := e.inferExprType(ex.Args[1]); cbTy.IsFunc && cbTy.FuncRetType != nil {
							return *cbTy.FuncRetType
						}
					}
					return TypeVoid
				case "exit":
					if len(ex.Args) >= 1 {
						if cbTy := e.inferExprType(ex.Args[0]); cbTy.IsFunc && cbTy.FuncRetType != nil {
							return *cbTy.FuncRetType
						}
					}
					return TypeVoid
				case "enterWith", "disable":
					return TypeVoid
				}
			}
			// AsyncResource.runInAsyncScope(fn, …) evaluates to fn's return type.
			if objTy := e.inferExprType(mem.Object); objTy.IsAsyncResource && mem.Property == "runInAsyncScope" {
				if len(ex.Args) >= 1 {
					if cbTy := e.inferExprType(ex.Args[0]); cbTy.IsFunc && cbTy.FuncRetType != nil {
						return *cbTy.FuncRetType
					}
				}
				return TypeVoid
			}
			// AsyncLocalStorage.bind(fn) evaluates to fn's own function type.
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "AsyncLocalStorage" && mem.Property == "bind" {
				if len(ex.Args) == 1 {
					return e.inferExprType(ex.Args[0])
				}
			}
		}
		// gen.next(value)'s own result type ({value: T, done: bool}) —
		// checked before the generic member-expression dispatch further
		// down, same as every other type-tag-gated case there.
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok && (mem.Property == "next" || mem.Property == "throw" || mem.Property == "return") {
			if objTy := e.inferExprType(mem.Object); objTy.IsGenerator {
				res := genNextResultType(*objTy.GeneratorElemType)
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
			if e.isShadowedByLocal(id.Name) {
				return TypeI64 // matches this function's own generic identifier-call fallback below
			}
			switch id.Name {
			case "parseInt", "parseFloat":
				// Both return a double (as real JS) so a no-digits input can
				// be NaN — see emitParseInt/emitParseFloat.
				return TypeF64
			case "String":
				return TypePtr
			case "Boolean":
				return TypeBool
			case "Number":
				// Must match emitGlobalNumberConv: numeric input passes
				// through, a string parses to a double, everything else i64.
				if len(ex.Args) == 1 {
					argTy := e.inferExprType(ex.Args[0])
					if argTy.Float {
						return TypeF64
					}
					if isStringTy(argTy) {
						return TypeF64
					}
				}
				return TypeI64
			case "isNaN", "isFinite":
				return TypeBool
			case "fetch":
				return PromiseOf(ResponseType())
			case "btoa", "atob", "encodeURIComponent", "decodeURIComponent", "encodeURI", "decodeURI":
				return TypePtr
			case "setTimeout", "setInterval", "setImmediate":
				return TypeI64
			case "structuredClone":
				if len(ex.Args) == 1 {
					return e.inferExprType(ex.Args[0])
				}
			case "Symbol":
				return SymbolType()
			case "assert__kml_builtin":
				return TypeVoid
			}
		}
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			// DataView accessors — get* return i64 (Float* a double), set*
			// void; must match emitDataViewGet/Set.
			if op, kind, ok2 := dataViewMethodKind(mem.Property); ok2 {
				if e.inferExprType(mem.Object).IsDataView {
					if op == "set" {
						return TypeVoid
					}
					if dataViewAccessKinds[kind].float {
						return TypeF64
					}
					if dataViewAccessKinds[kind].bigint {
						return BigIntType()
					}
					return TypeI64
				}
			}
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
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "console" && !e.isShadowedByLocal(id.Name) {
				// Every console.* method returns void (emitConsolePrint and
				// everything that delegates to it, e.g. emitConsoleDir, all
				// return Value{Ty: TypeVoid}) — without this case, an
				// expression-bodied arrow whose only statement is e.g.
				// console.log(...) (a common HOF-callback shape, like
				// arr.forEach((n) => console.log(n))) fell through to this
				// function's blind TypeI64 fallback below, so the closure
				// got built expecting to return a number that emitExpr's
				// real (correctly void) evaluation never produces — a hard
				// clang-stage type mismatch. See docs/adr/ADR-00043.md.
				return TypeVoid
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "String" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "fromCharCode", "fromCodePoint":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Number" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "isInteger", "isFinite", "isNaN", "isSafeInteger":
					return TypeBool
				case "parseInt", "parseFloat":
					// Both return a double (as real JS) so a no-digits input
					// can be NaN — see emitParseInt/emitParseFloat.
					return TypeF64
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Buffer" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "from", "alloc", "allocUnsafe", "concat":
					return BufferType()
				case "compare", "byteLength":
					return TypeI64
				case "isBuffer":
					return TypeBool
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Atomics" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "wait":
					return TypePtr // "ok" / "not-equal" / "timed-out"
				case "notify":
					return TypeI64
				case "isLockFree":
					return TypeBool
				default:
					// load/store/RMW/compareExchange return the receiver's
					// element type (a bigint for BigInt64/BigUint64Array).
					if len(ex.Args) > 0 {
						if taTy := e.inferExprType(ex.Args[0]); taTy.IsTypedArray && taTy.ElemType != nil {
							if taTy.BigIntElem {
								return BigIntType()
							}
							return *taTy.ElemType
						}
					}
					return TypeI64
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "BigInt" && !e.isShadowedByLocal(id.Name) &&
				(mem.Property == "asIntN" || mem.Property == "asUintN") {
				return BigIntType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Math" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "random", "sqrt", "pow", "hypot", "log", "log2", "log10", "sin", "cos", "tan",
					"asin", "acos", "atan", "atan2", "sinh", "cosh", "tanh", "cbrt", "expm1", "log1p", "fround":
					return TypeF64
				case "clz32", "imul":
					return TypeI64
				case "floor", "ceil", "round", "trunc", "sign":
					// Integer input stays i64; float input stays a double
					// (preserving NaN/±Infinity) — must match emitMathRound/
					// emitMathSign's own result types.
					if len(ex.Args) == 1 && e.inferExprType(ex.Args[0]).Float {
						return TypeF64
					}
					return TypeI64
				case "abs":
					if len(ex.Args) == 1 {
						return e.inferExprType(ex.Args[0])
					}
				case "min", "max":
					// Any float argument promotes the whole fold to a double
					// (llvm.minimum/maximum) — must match emitMathMinMax. A
					// spread argument contributes its array's element type.
					for _, a := range ex.Args {
						if sp, ok := a.(*ast.SpreadElement); ok {
							at := e.inferExprType(sp.Arg)
							if at.ElemType != nil && at.ElemType.Float {
								return TypeF64
							}
							continue
						}
						if e.inferExprType(a).Float {
							return TypeF64
						}
					}
					return TypeI64
				case "clamp":
					if len(ex.Args) > 0 {
						return e.inferExprType(ex.Args[0])
					}
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "JSON" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "stringify":
					// A dynamic argument stringifies through the dynamic
					// walker, whose result is `any` (string or undefined —
					// TDD-00155 Stage 2); every static path returns string.
					if len(ex.Args) >= 1 && isUnconstrainedDynamic(e.inferExprType(ex.Args[0])) {
						return TypeAny
					}
					return TypePtr
				case "parse":
					// Context-free JSON.parse is untyped dynamic parse
					// (TDD-00155 Stage 2); a typed declaration context never
					// consults this default (emitDeclJSONProjection).
					return TypeAny
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Date" && mem.Property == "now" {
				return TypeDate
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Date" && mem.Property == "parse" {
				return TypeI64
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "performance" && !e.isShadowedByLocal(id.Name) && (mem.Property == "now" || mem.Property == "measure") {
				return TypeF64
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "tui__kml_builtin" {
				// TDD-00150: every builder returns an opaque node handle (a
				// ptr); render/enter/leave return void. A node is only ever
				// passed back into another builder or render, never
				// method-dispatched, so a plain TypePtr suffices.
				switch mem.Property {
				case "Box", "Text", "List", "Spinner", "Progress", "TextInput":
					return TypePtr
				default:
					return TypeVoid
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "fs__kml_builtin" {
				switch mem.Property {
				case "readFileSync":
					return TypePtr
				case "readFileSyncBytes":
					return TypedArrayType("uint8")
				case "existsSync":
					return TypeBool
				case "readdirSync":
					return ArrayOf(TypePtr)
				case "statSync", "lstatSync", "fstatSync":
					return StatsType()
				case "openSync", "writeSync", "readSync":
					return TypeI64
				case "realpathSync", "mkdtempSync", "readlinkSync":
					return TypePtr
				case "createReadStream":
					return NodeReadableType(TypePtr)
				case "createWriteStream":
					return NodeWritableType(TypePtr)
				}
				// Async callback form (TDD-00107): fs.readFile(path, cb) etc.
				// return void — the result is delivered through the callback.
				if _, ok := fsAsyncOps()[mem.Property]; ok {
					return TypeVoid
				}
			}
			// Promise form (TDD-00107): fs.promises.<op>(...) and the
			// fs/promises named import both return a settled task Promise.
			if inner, ok2 := mem.Object.(*ast.MemberExpression); ok2 {
				if id, ok3 := inner.Object.(*ast.Identifier); ok3 && id.Name == "fs__kml_builtin" && inner.Property == "promises" {
					if qt, ok := fsAsyncPromiseResult(mem.Property); ok {
						return qt
					}
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "fspromises__kml_builtin" {
				if qt, ok := fsAsyncPromiseResult(mem.Property); ok {
					return qt
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "readLineSync", "execFileSync", "cwd":
					return TypePtr
				case "uptime":
					return TypeF64
				case "hrtime":
					return TupleType([]Type{TypeI64, TypeI64})
				case "memoryUsage":
					return memoryUsageType()
				case "nextTick":
					return TypeVoid
				case "send":
					return TypeBool
				}
			}
			// fs.statSync Stats methods (ADR-00495).
			if mem.Property == "isFile" || mem.Property == "isDirectory" || mem.Property == "isSymbolicLink" {
				if e.inferExprType(mem.Object).IsStats {
					return TypeBool
				}
			}
			// XMLHttpRequest response-header reads (ADR-00490).
			if mem.Property == "getResponseHeader" || mem.Property == "getAllResponseHeaders" {
				if e.inferExprType(mem.Object).IsXHR {
					return TypePtr
				}
			}
			// ClientRequest methods chain (end/abort/on return the handle);
			// req.write(body) returns a boolean (ADR-00575).
			if objTy := e.inferExprType(mem.Object); objTy.IsClientRequest {
				if mem.Property == "write" {
					return TypeBool
				}
				return ClientRequestType()
			}
			// child.send(msg) / worker.send(msg) → boolean (the fork IPC
			// channel, TDD-00141).
			if mem.Property == "send" {
				if objTy := e.inferExprType(mem.Object); objTy.IsChildProcess || objTy.IsClusterWorker {
					return TypeBool
				}
			}
			// process.hrtime.bigint()
			if inner, ok2 := mem.Object.(*ast.MemberExpression); ok2 && mem.Property == "bigint" {
				if id, ok3 := inner.Object.(*ast.Identifier); ok3 && id.Name == "process" && inner.Property == "hrtime" && !e.isShadowedByLocal(id.Name) {
					return BigIntType()
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "path__kml_builtin" {
				switch mem.Property {
				case "join", "resolve", "dirname", "basename", "extname", "format":
					return TypePtr
				case "isAbsolute":
					return TypeBool
				case "parse":
					return PathParsedType()
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "os__kml_builtin" {
				switch mem.Property {
				case "platform", "homedir", "tmpdir", "hostname":
					return TypePtr
				case "totalmem", "freemem":
					return TypeI64
				case "cpus":
					return ArrayOf(CPUInfoType())
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "querystring__kml_builtin" {
				switch mem.Property {
				case "parse":
					return MapType(TypePtr, TypePtr)
				case "stringify":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "events__reexport_kml_builtin" && mem.Property == "once" {
				if pt, ok := e.eventsOncePromiseType(ex.Args); ok {
					return pt
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "events__reexport_kml_builtin" && mem.Property == "on" {
				if gt, ok := e.eventsOnGeneratorType(ex.Args, ex.GetPos()); ok {
					return gt
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "url__reexport_kml_builtin" {
				switch mem.Property {
				case "parse":
					return LegacyUrlType()
				case "format":
					return TypePtr
				case "fileURLToPath":
					return TypePtr
				case "pathToFileURL":
					return URLType()
				case "resolve":
					return TypePtr
				case "urlToHttpOptions":
					return HttpOptionsType()
				case "domainToASCII", "domainToUnicode":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "assert__kml_builtin" {
				return TypeVoid
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "test__kml_builtin" {
				// mustCall/mustNotCall/… return a wrapper with the SAME function
				// type as the wrapped callback, so a `const cb = mustCall(fn)` is
				// callable exactly like fn; the rest are void (TDD-00122).
				switch mem.Property {
				case "mustCall", "mustCallAtLeast", "mustSucceed":
					if len(ex.Args) >= 1 {
						if t := e.inferExprType(ex.Args[0]); t.IsFunc {
							return t
						}
					}
					// `mustCall()` / `mustCall(n)`: a counted noop wrapper.
					return FuncType(nil, TypeVoid)
				case "mustNotCall":
					return FuncType(nil, TypeVoid) // a zero-arg () => void wrapper
				default:
					return TypeVoid
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "zlib__kml_builtin" {
				// The *Sync forms return a Buffer; the callback forms return void.
				if n := len(mem.Property); n > 4 && mem.Property[n-4:] == "Sync" {
					return TypedArrayType("uint8")
				}
				return TypeVoid
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "childprocess__kml_builtin" {
				// The blocking forms return a result record / stdout string;
				// spawn/exec/execFile return a ChildProcess handle.
				switch mem.Property {
				case "spawnSync":
					return cpSpawnSyncResultType()
				case "execSync", "execFileSync":
					return TypePtr
				}
				return ChildProcessType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "readline__kml_builtin" {
				return ReadlineType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "net__kml_builtin" {
				// connect/createConnection return a client Socket; createServer a Server.
				switch mem.Property {
				case "connect", "createConnection":
					return NetSocketType()
				case "isIP":
					return TypeF64
				case "isIPv4", "isIPv6":
					return TypeBool
				}
				return NetServerType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "http__kml_builtin" {
				// http.get/request return the ClientRequest handle (ADR-00430).
				if mem.Property == "get" || mem.Property == "request" {
					return ClientRequestType()
				}
				// Variable-bound http.createServer(cb) returns a Server handle.
				if mem.Property == "createServer" {
					return HTTPServerType()
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "diagch__kml_builtin" {
				switch mem.Property {
				case "channel":
					return DiagChannelType()
				case "hasSubscribers", "unsubscribe":
					return TypeBool
				}
				return TypeVoid
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "nodecrypto__kml_builtin" {
				switch mem.Property {
				case "generateKeyPairSync":
					return nodeCryptoKeyPairResultType()
				case "randomBytes":
					return TypedArrayType("uint8")
				case "randomUUID":
					return TypePtr
				case "createHash":
					return HashType()
				case "createHmac":
					return HmacType()
				}
				return TypeVoid
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "https__kml_builtin" {
				// https.createServer shares the http server handle (TDD-00111);
				// get/request mirror the http client (ADR-00430).
				if mem.Property == "createServer" {
					return HTTPServerType()
				}
				return ClientRequestType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "http2__kml_builtin" {
				// http2.createServer shares the http server handle (TDD-00139 Stage 1);
				// connect returns a client session (Stage 3).
				if mem.Property == "createServer" || mem.Property == "createSecureServer" {
					return HTTPServerType()
				}
				if mem.Property == "connect" {
					return Http2ClientSessionType()
				}
				if mem.Property == "getDefaultSettings" || mem.Property == "getUnpackedSettings" {
					return h2SettingsObjectType()
				}
				if mem.Property == "getPackedSettings" {
					return BufferType()
				}
				return TypeVoid
			}
			// session.request returns a ClientHttp2Stream (TDD-00139 Stage 3).
			if e.inferExprType(mem.Object).IsH2ClientSession && mem.Property == "request" {
				return Http2ClientStreamType()
			}
			// res.write(chunk) on a ServerResponse returns Node's boolean
			// backpressure signal (TDD-00131) — always true here, since the
			// buffered response sink never fills. Other res methods are void.
			if mem.Property == "write" && e.inferExprType(mem.Object).IsServerResponse {
				return TypeBool
			}
			// res.on(...)/setEncoding(...) on an http IncomingMessage chain back
			// to the same object (TDD-00138).
			if e.inferExprType(mem.Object).IsIncomingMessage {
				return IncomingMessageType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "tls__kml_builtin" {
				// tls.connect returns a (net-shaped) TLS client Socket; createServer a Server.
				if mem.Property == "createServer" {
					return NetServerType()
				}
				return NetSocketType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "util__kml_builtin" {
				// inspect/format both return a string.
				return TypePtr
			}
			// dns.promises.lookup(...) resolves to { address, family }.
			if inner, ok2 := mem.Object.(*ast.MemberExpression); ok2 && mem.Property == "lookup" {
				if id, ok3 := inner.Object.(*ast.Identifier); ok3 && id.Name == "dns__kml_builtin" && inner.Property == "promises" {
					qt := PromiseOf(dnsLookupObjType())
					qt.PromiseTask = true
					return qt
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "dns__kml_builtin" {
				// lookup/resolve4/resolve return void (callback-based).
				return TypeVoid
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "dgram__kml_builtin" {
				// createSocket returns a DgramSocket handle.
				return DgramSocketType()
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "cluster__kml_builtin" {
				// fork() returns a Worker handle.
				return ClusterWorkerType()
			}
			if e.isCryptoSubtle(mem.Object) {
				task := func(inner Type) Type {
					ty := PromiseOf(inner)
					ty.PromiseTask = true
					return ty
				}
				switch mem.Property {
				case "digest", "encrypt", "decrypt", "sign", "deriveBits":
					return task(ArrayBufferType())
				case "deriveKey":
					return task(CryptoKeyType())
				case "verify":
					return task(TypeBool)
				case "importKey":
					return task(CryptoKeyType())
				case "generateKey":
					if len(ex.Args) >= 1 {
						if name, ok3 := subtleAlgoName(ex.Args[0]); ok3 {
							switch name {
							case "RSA-OAEP", "RSA-PSS", "ECDSA":
								return task(CryptoKeyPairType())
							}
						}
					}
					return task(CryptoKeyType())
				case "exportKey":
					if len(ex.Args) >= 1 {
						if f, ok3 := ex.Args[0].(*ast.StringLiteral); ok3 && f.Value == "jwk" {
							return task(MapType(TypePtr, TypePtr))
						}
					}
					return task(ArrayBufferType())
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "crypto" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "getRandomValues":
					if len(ex.Args) == 1 {
						return e.inferExprType(ex.Args[0])
					}
				case "randomUUID":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Array" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "of":
					if len(ex.Args) > 0 {
						return ArrayOf(e.inferExprType(ex.Args[0]))
					}
					return ArrayOf(TypeI64)
				case "from":
					// 2-arg mapFn form (ADR-00491): infer through the same
					// .map() desugar the emitter uses, so the closure's
					// parameters are typed contextually against the source
					// element type (inferring the bare closure here would
					// bind its `number` params to the standalone default
					// instead).
					if len(ex.Args) == 2 {
						fromCall := ast.NewCallExpression(
							ast.NewMemberExpression(ast.NewIdentifier("Array", ex.GetPos()), "from", ex.GetPos()),
							ex.Args[:1], ex.GetPos())
						mapCall := ast.NewCallExpression(
							ast.NewMemberExpression(fromCall, "map", ex.GetPos()),
							ex.Args[1:2], ex.GetPos())
						return e.inferExprType(mapCall)
					}
					if len(ex.Args) == 1 {
						argTy := e.inferExprType(ex.Args[0])
						if argTy.IsArray {
							return ArrayOf(*argTy.ElemType)
						}
						// Map → entries tuple array; Set → element array;
						// string → string[] (ADR-00482, matching emitArrayFrom).
						if argTy.IsMap && !argTy.IsSet {
							keyTy, valTy := TypePtr, TypePtr
							if argTy.MapKey != nil {
								keyTy = *argTy.MapKey
							}
							if argTy.MapVal != nil {
								valTy = *argTy.MapVal
							}
							return ArrayOf(TupleType([]Type{keyTy, valTy}))
						}
						if argTy.IsSet {
							elemTy := TypePtr
							if argTy.MapKey != nil {
								elemTy = *argTy.MapKey
							}
							return ArrayOf(elemTy)
						}
						if isStringTy(argTy) && !argTy.IsClass && !argTy.IsObject {
							return ArrayOf(TypePtr)
						}
						if argTy.IsClass {
							if info, ok3 := e.classes[argTy.ClassName]; ok3 {
								if sig, ok3 := info.MethodSigs["next"]; ok3 {
									elemTy := sig.RetType
									elemTy.Nullable = false
									return ArrayOf(elemTy)
								}
							}
						}
					}
					return ArrayOf(TypeI64)
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Promise" {
				// Promise.resolve(v) → task Promise<typeof v> (an already-promise arg
				// passes through); Promise.reject(e) → task Promise<number> (the value
				// type is never observed — await re-throws). TDD-00086 follow-on.
				if mem.Property == "resolve" {
					if len(ex.Args) == 0 {
						pt := PromiseOf(TypeVoid)
						pt.PromiseTask = true
						return pt
					}
					argTy := e.inferExprType(ex.Args[0])
					if argTy.IsPromise {
						return argTy
					}
					pt := PromiseOf(argTy)
					pt.PromiseTask = true
					return pt
				}
				if mem.Property == "reject" {
					pt := PromiseOf(TypeNever)
					pt.PromiseTask = true
					return pt
				}
				if len(ex.Args) == 1 {
					if innerTy, err := e.promiseArrayElemType(ex.Args[0], mem.Property, ex.GetPos()); err == nil {
						switch mem.Property {
						case "all":
							return PromiseOf(ArrayOf(innerTy))
						case "race":
							// .race's Response branch resolves the winner
							// synchronously and wraps it via
							// wrapResolvedPromise, which marks its
							// PromiseType.PromiseResolved so emitAwait
							// doesn't mistake it for a still-pending fetch
							// handle (see PromiseResolved's doc comment,
							// types.go) — mirrored here so this static
							// path (emitAwait's inferExprType fallback for
							// an indirect `const p = Promise.race(...);
							// await p`) agrees with emitPromiseRace's own
							// codegen-time type.
							raceTy := PromiseOf(innerTy)
							if innerTy.IsResponse && raceTy.PromiseType != nil {
								raceTy.PromiseType.PromiseResolved = true
							}
							return raceTy
						case "allSettled":
							return PromiseOf(ArrayOf(SettlementType(innerTy)))
						case "any":
							// .any resolves to the first fulfilled member's value
							// (TDD-00083 Stage 2), same shape as .race.
							anyTy := PromiseOf(innerTy)
							if innerTy.IsResponse && anyTy.PromiseType != nil {
								anyTy.PromiseType.PromiseResolved = true
							}
							return anyTy
						}
					}
				}
			}
			// .then/.catch/.finally on a promise return a task Promise<U> where U
			// is the value the returned promise settles to (TDD-00083 Stage 3 +
			// value-chaining, ADR-00248): the callback's return type for then/catch,
			// the source's own inner type for finally (pass-through).
			if mem.Property == "then" || mem.Property == "catch" || mem.Property == "finally" {
				if srcTy := e.inferExprType(mem.Object); srcTy.IsPromise {
					retTy := TypeVoid
					switch mem.Property {
					case "then", "catch":
						if len(ex.Args) >= 1 {
							if t, ok := e.callbackReturnType(ex.Args[0]); ok {
								retTy = t
							}
						}
					case "finally":
						if srcTy.PromiseType != nil {
							retTy = *srcTy.PromiseType
						}
					}
					vt := PromiseOf(retTy)
					vt.PromiseTask = true
					return vt
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Reflect" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "get", "getPrototypeOf":
					return TypeAny
				case "ownKeys":
					return ArrayOf(TypePtr)
				case "set", "has", "deleteProperty", "setPrototypeOf", "isExtensible", "preventExtensions", "defineProperty":
					return TypeBool
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Object" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "groupBy":
					if len(ex.Args) >= 1 {
						arrTy := e.inferExprType(ex.Args[0])
						if arrTy.IsArray && arrTy.ElemType != nil {
							et := *arrTy.ElemType
							return Type{IR: "ptr", IsGroupMap: true, ElemType: &et}
						}
					}
					return Type{IR: "ptr", IsGroupMap: true}
				case "create", "getPrototypeOf", "setPrototypeOf":
					// Prototype statics operate on and return dynamic values
					// (TDD-00155 Stage 3).
					return TypeAny
				case "defineProperty", "getOwnPropertyDescriptor":
					// Descriptor statics (Stage 5): the object back / a
					// descriptor object (or undefined).
					return TypeAny
				case "getOwnPropertyNames":
					return ArrayOf(TypePtr)
				case "isExtensible", "isSealed", "isFrozen":
					return TypeBool
				case "keys", "values", "entries":
					// A dynamic object (or any string-keyed Map<string,V>) is
					// Map-backed — Object.keys/values/entries on it delegate
					// to the Map's own methods (emitObjectKeys/Values/
					// Entries, docs/tdd/TDD-00012.md) and so return real
					// typed keys/values, not the fixed-shape-object fallback
					// below (always string[] / string-keyed-and-valued
					// entries).
					if len(ex.Args) >= 1 {
						if argTy := e.inferExprType(ex.Args[0]); argTy.IsMap && argTy.MapKey != nil {
							keyTy := *argTy.MapKey
							valTy := TypeI64
							if argTy.MapVal != nil {
								valTy = *argTy.MapVal
							}
							switch mem.Property {
							case "keys":
								return ArrayOf(keyTy)
							case "values":
								return ArrayOf(valTy)
							case "entries":
								entryTy := TupleType([]Type{keyTy, valTy})
								return ArrayOf(entryTy)
							}
						}
					}
					if mem.Property == "values" {
						// Same homogeneous-typed-values rule as entries
						// (ADR-00492) — must match emitObjectValues.
						if len(ex.Args) >= 1 {
							if argTy := e.inferExprType(ex.Args[0]); argTy.IsObject {
								if vt, ok := homogeneousFieldType(argTy.VisibleFields()); ok {
									return ArrayOf(vt)
								}
							}
						}
						return ArrayOf(TypePtr)
					}
					if mem.Property == "entries" {
						// Homogeneous fixed-shape objects keep real typed
						// values (ADR-00492) — must match emitObjectEntries.
						valTy := TypePtr
						if len(ex.Args) >= 1 {
							if argTy := e.inferExprType(ex.Args[0]); argTy.IsObject {
								if vt, ok := homogeneousFieldType(argTy.VisibleFields()); ok {
									valTy = vt
								}
							}
						}
						entryTy := TupleType([]Type{TypePtr, valTy})
						return ArrayOf(entryTy)
					}
					return ArrayOf(TypePtr)
				case "hasOwn":
					return TypeBool
				case "fromEntries":
					// Object.fromEntries(entries) → a dynamic object (a
					// Map<string,V>-backed value, docs/tdd/TDD-00012.md);
					// V comes from the [string, V][] entries' tuple field 1.
					valTy := TypeI64
					if len(ex.Args) >= 1 {
						if elemTy := e.inferExprType(ex.Args[0]); elemTy.IsArray && elemTy.ElemType != nil &&
							elemTy.ElemType.IsTuple && len(elemTy.ElemType.Fields) == 2 {
							valTy = elemTy.ElemType.Fields[1].Ty
						}
					}
					keyTy := TypePtr
					return Type{IR: "ptr", IsMap: true, IsDynamicObject: true, MapKey: &keyTy, MapVal: &valTy}
				case "assign", "freeze", "seal":
					if len(ex.Args) >= 1 {
						return e.inferExprType(ex.Args[0])
					}
				}
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
			objTy, haveObjTy := Type{}, false
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 {
				if sym, found := e.lookup(id.Name); found {
					objTy, haveObjTy = sym.Ty, true
				}
			}
			if !haveObjTy {
				objTy, haveObjTy = e.inferExprType(mem.Object), true
			}
			if haveObjTy && objTy.IsWeakRef {
				if mem.Property == "deref" && objTy.MapKey != nil {
					return *objTy.MapKey
				}
			}
			if haveObjTy && objTy.IsMap {
				switch mem.Property {
				case "get":
					if objTy.MapVal != nil {
						v := *objTy.MapVal
						// A scalar value type's get() is `V | null` (bug #3);
						// a pointer value keeps its own type (null-pointer miss).
						if isNullableScalarMapValue(v) {
							v.Nullable = true
						}
						return v
					}
				case "has", "delete":
					return TypeBool
				case "keys":
					if objTy.MapKey != nil {
						return ArrayOf(*objTy.MapKey)
					}
				case "values":
					if objTy.MapVal != nil {
						return ArrayOf(*objTy.MapVal)
					}
				case "entries":
					keyTy, valTy := TypePtr, TypeI64
					if objTy.MapKey != nil {
						keyTy = *objTy.MapKey
					}
					if objTy.MapVal != nil {
						valTy = *objTy.MapVal
					}
					entryTy := TupleType([]Type{keyTy, valTy})
					return ArrayOf(entryTy)
				case "set":
					return objTy
				case "toString":
					if objTy.IsURLSearchParams {
						return TypePtr
					}
				case "getAll":
					if objTy.IsURLSearchParams {
						return ArrayOf(TypePtr)
					}
				}
			}
			if haveObjTy && objTy.IsSet {
				switch mem.Property {
				case "has", "delete":
					return TypeBool
				case "add":
					return objTy
				case "values":
					if objTy.MapKey != nil {
						return ArrayOf(*objTy.MapKey)
					}
				}
			}
			if haveObjTy && objTy.IsEventEmitter {
				switch mem.Property {
				case "on", "once", "off", "removeListener", "removeAllListeners":
					return objTy
				case "emit":
					return TypeBool
				case "listenerCount":
					return TypeI64
				case "eventNames":
					return ArrayOf(TypePtr)
				}
			}
		}
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			switch mem.Property {
			case "getTime", "valueOf", "getFullYear", "getMonth", "getDate", "getDay",
				"getHours", "getMinutes", "getSeconds", "getMilliseconds",
				"setFullYear", "setMonth", "setDate", "setHours", "setMinutes",
				"setSeconds", "setMilliseconds", "setTime":
				if e.inferExprType(mem.Object).IsDate {
					return TypeI64
				}
			case "toISOString", "toDateString", "toLocaleDateString":
				if e.inferExprType(mem.Object).IsDate {
					return TypePtr
				}
			case "hasOwnProperty":
				if e.inferExprType(mem.Object).IsObject {
					return TypeBool
				}
			case "toString":
				if e.inferExprType(mem.Object).IsBuffer {
					return TypePtr
				}
				if isNumberTy(e.inferExprType(mem.Object)) {
					return TypePtr
				}
			case "write", "copy":
				if e.inferExprType(mem.Object).IsBuffer {
					return TypeI64
				}
			case "equals":
				if e.inferExprType(mem.Object).IsBuffer {
					return TypeBool
				}
			case "compare":
				if e.inferExprType(mem.Object).IsBuffer {
					return TypeI64
				}
			case "text":
				if ty := e.inferExprType(mem.Object); ty.IsResponse || ty.IsBlob {
					return TypePtr
				}
			case "bytes":
				if e.inferExprType(mem.Object).IsBlob {
					return TypedArrayType("uint8")
				}
			case "stream":
				if e.inferExprType(mem.Object).IsBlob {
					return ReadableStreamType(TypedArrayType("uint8"))
				}
			case "json":
				if e.inferExprType(mem.Object).IsResponse {
					// No declaration context here to parse into (that's
					// handled separately, see emitResponseJSON) — TypePtr
					// matches bare JSON.parse's own default-context type.
					return TypePtr
				}
			case "arrayBuffer":
				if ty := e.inferExprType(mem.Object); ty.IsResponse || ty.IsBlob {
					return ArrayBufferType()
				}
			case "encode":
				if e.inferExprType(mem.Object).IsTextEncoder {
					return TypedArrayType("uint8")
				}
			case "decode":
				if e.inferExprType(mem.Object).IsTextDecoder {
					return TypePtr
				}
			case "test":
				if ty := e.inferExprType(mem.Object); ty.IsRegExp || ty.IsURLPattern {
					return TypeBool
				}
			case "exec":
				ty := e.inferExprType(mem.Object)
				if ty.IsURLPattern {
					return MapType(TypePtr, TypePtr)
				}
				if ty.IsRegExp {
					return regExpExecResultType()
				}
			case "split":
				return ArrayOf(TypePtr)
			case "match":
				return regExpExecResultType()
			case "matchAll":
				return ArrayOf(ArrayOf(TypePtr))
			case "substring", "trim", "toUpperCase", "toLowerCase", "replace":
				if isStringTy(e.inferExprType(mem.Object)) {
					return TypePtr
				}
			case "indexOf", "lastIndexOf", "findIndex", "findLastIndex", "search", "localeCompare":
				// JS index/count results are Numbers (doubles) — TDD-00123
				// Stage 3. Must mirror the emit sites, which sitofp to double.
				return TypeF64
			case "charCodeAt", "codePointAt":
				// A double so an out-of-range index can be NaN — must match
				// emitStringCharCodeAt.
				return TypeF64
			case "includes", "startsWith", "endsWith", "some", "every":
				return TypeBool
			case "join", "repeat", "padStart", "padEnd", "toFixed", "charAt", "toPrecision", "toExponential":
				return TypePtr
			case "at", "findLast":
				objTy := e.inferExprType(mem.Object)
				if objTy.BigIntElem {
					return BigIntType()
				}
				if objTy.IsArray && objTy.ElemType != nil {
					return *objTy.ElemType
				}
				return TypePtr // string.at returns a char string
			case "sort", "concat", "reverse", "fill", "toReversed", "toSorted", "toSpliced", "with", "copyWithin", "values":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
			case "keys":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return ArrayOf(TypeI64)
				}
			case "entries":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && objTy.ElemType != nil {
					entryTy := TupleType([]Type{TypeI64, *objTy.ElemType})
					return ArrayOf(entryTy)
				}
			case "subarray":
				// TypedArray-only; a view with the receiver's own type.
				objTy := e.inferExprType(mem.Object)
				if objTy.IsTypedArray {
					return objTy
				}
			case "slice":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsBlob {
					return BlobType()
				}
				if objTy.IsArrayBuffer {
					return objTy // copy of the receiver's own buffer kind
				}
				if objTy.IsArray {
					return objTy
				}
				return TypePtr // string.slice
			case "map":
				// A TypedArray's .map() always returns the same TypedArray
				// kind as the receiver (matching emitArrayMap's own
				// behavior, emit_arrays_hof.go) — checked before the
				// callback-return-type inference below, which would
				// otherwise disagree with what actually gets emitted for
				// an unannotated `const x = typedArr.map(...)`.
				if recvTy := e.inferExprType(mem.Object); recvTy.IsTypedArray {
					return recvTy
				}
				if len(ex.Args) == 1 {
					if retTy, ok := e.callbackReturnType(ex.Args[0], e.hofElemHint(mem.Object)); ok {
						return ArrayOf(retTy)
					}
				}
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
			case "filter":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
			case "find":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && objTy.ElemType != nil {
					return *objTy.ElemType
				}
			case "reduce":
				if len(ex.Args) == 2 {
					return e.inferExprType(ex.Args[1])
				}
			case "flat":
				// Mirrors emitArrayFlat's own unwrap loop exactly (emit_arrays_transform.go)
				// so an unannotated `const x = arr.flat(N)` infers the same
				// element type codegen actually produces. Best-effort here —
				// an invalid depth falls back to the default (1) rather than
				// erroring, since resolveFlatDepth's own error is what
				// actually surfaces to the user when emitArrayFlat runs.
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && objTy.ElemType != nil {
					depth := 1
					if d, err := e.resolveFlatDepth(ex.Args, ex.GetPos()); err == nil {
						depth = d
					}
					curElemTy := *objTy.ElemType
					for i := 0; i < depth && curElemTy.IsArray && curElemTy.ElemType != nil; i++ {
						curElemTy = *curElemTy.ElemType
					}
					return ArrayOf(curElemTy)
				}
			case "flatMap":
				// Same "map"-shaped inference above, plus flatMap's own fixed
				// one-level unwrap when the callback returns an array —
				// mirrors emitArrayFlatMap exactly.
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && len(ex.Args) == 1 {
					if retTy, ok := e.callbackReturnType(ex.Args[0], e.hofElemHint(mem.Object)); ok {
						if retTy.IsArray && retTy.ElemType != nil {
							return ArrayOf(*retTy.ElemType)
						}
						return ArrayOf(retTy)
					}
					return objTy
				}
			case "push", "unshift":
				// Returns the new length — a Number (double) in JS (TDD-00123
				// Stage 3); must mirror emitPush/emitUnshift.
				return TypeF64
			case "pop", "shift":
				// Returns the removed element (or the element type's zero
				// value on an empty array).  Must match emitPop/emitShift's
				// own codegen-time type (the receiver's element type), not
				// the generic fallback below (which would be TypeI64 for
				// every untracked method, wrongly coercing a string element
				// to an i64 store target).
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && objTy.ElemType != nil {
					return *objTy.ElemType
				}
				return objTy
			case "splice":
				// Returns the removed elements as a new array of the same
				// kind as the receiver.
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
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
		}
	case *ast.UnaryExpression:
		switch ex.Op {
		case "typeof":
			return TypePtr
		case "!":
			return TypeBool
		case "-", "+":
			// Unary +/- preserve the operand's numeric type (i64 or float).
			return e.inferExprType(ex.Arg)
		case "~":
			return TypeI64
		}
	case *ast.ConditionalExpression:
		// `cond ? scalar : null` is `T | null` (ADR-00538) — mirror
		// emitConditional so an unannotated `const x = b ? 3 : null` gets
		// nullable-scalar storage rather than a collapsed bare scalar.
		if nty, ok := ternaryNullableScalarType(e.inferExprType(ex.Consequent), e.inferExprType(ex.Alternate)); ok {
			return nty
		}
		// A dynamic branch makes the whole ternary `any` (mirrors
		// emitConditionalAny).
		if e.inferExprType(ex.Consequent).IsDynamic || e.inferExprType(ex.Alternate).IsDynamic {
			return TypeAny
		}
		return e.inferExprType(ex.Consequent)
	case *ast.SequenceExpression:
		// The comma operator's value (and type) is its last operand's.
		if len(ex.Exprs) > 0 {
			return e.inferExprType(ex.Exprs[len(ex.Exprs)-1])
		}
		return TypeI64
	case *ast.NewNodeStreamExpression:
		// Mirrors emitNewNodeStream: every Node-stream constructor defaults to
		// string chunks (Node's non-objectMode streams carry strings/Buffers);
		// `<T>` overrides.
		inTy, outTy := TypePtr, TypePtr
		if ex.InType != nil {
			inTy = e.resolveType(ex.InType)
		}
		if ex.OutType != nil {
			outTy = e.resolveType(ex.OutType)
		}
		switch ex.Kind {
		case "readable":
			return NodeReadableType(outTy)
		case "writable":
			return NodeWritableType(inTy)
		}
		return NodeTransformType(inTy, outTy)
	case *ast.NewCompressionStreamExpression:
		u8i := TypedArrayType("uint8")
		u8o := TypedArrayType("uint8")
		return Type{IR: "ptr", IsTransformStream: true, StreamChunk: &u8i, StreamOut: &u8o}
	case *ast.NewTransformStreamExpression:
		inTy, outTy := TypeI64, TypeI64
		if ex.InType != nil {
			inTy = e.resolveType(ex.InType)
		}
		if ex.OutType != nil {
			outTy = e.resolveType(ex.OutType)
		}
		i, o := inTy, outTy
		return Type{IR: "ptr", IsTransformStream: true, StreamChunk: &i, StreamOut: &o}
	case *ast.NewWritableStreamExpression:
		if ex.ChunkType != nil {
			return WritableStreamType(e.resolveType(ex.ChunkType))
		}
		return WritableStreamType(TypeI64)
	case *ast.NewReadableStreamExpression:
		if ex.ChunkType != nil {
			return ReadableStreamType(e.resolveType(ex.ChunkType))
		}
		return ReadableStreamType(TypeI64)
	case *ast.NewMapExpression:
		// Mirrors emitNewMapValue's K/V resolution (mapKVTypes): explicit
		// `<K, V>` wins, else infer from an initial `[K, V][]` entries array,
		// else the string-key/number-value defaults.
		keyTy, valTy := e.mapKVTypes(ex.KeyType, ex.ValType, ex.Init)
		return MapType(keyTy, valTy)
	case *ast.NewSetExpression:
		// Mirrors emitNewSetValue's shape without emitting the initializer;
		// an initializer-inferred element type falls back to the string
		// default here (typeof/inference only needs the Set-ness).
		elemTy := TypePtr
		if ex.ElemType != nil {
			elemTy = e.resolveType(ex.ElemType)
		}
		return SetType(elemTy)
	case *ast.NewWeakMapExpression:
		keyTy := TypePtr
		valTy := TypeI64
		if ex.KeyType != nil {
			keyTy = e.resolveType(ex.KeyType)
		}
		if ex.ValType != nil {
			valTy = e.resolveType(ex.ValType)
		}
		return WeakMapType(keyTy, valTy)
	case *ast.NewWeakSetExpression:
		elemTy := TypePtr
		if ex.ElemType != nil {
			elemTy = e.resolveType(ex.ElemType)
		}
		return WeakSetType(elemTy)
	case *ast.NewWeakRefExpression:
		referentTy := TypePtr
		if ex.ElemType != nil {
			referentTy = e.resolveType(ex.ElemType)
		} else if ex.Init != nil {
			referentTy = e.inferExprType(ex.Init)
		}
		return WeakRefType(referentTy)
	case *ast.NewErrorExpression:
		return errorObjType
	case *ast.NewDateExpression:
		return TypeDate
	case *ast.NewDatabaseSyncExpression:
		return SQLiteDatabaseType()
	case *ast.NewURLExpression:
		return URLType()
	case *ast.NewURLSearchParamsExpression:
		return URLSearchParamsType()
	case *ast.NewURLPatternExpression:
		return URLPatternType()
	case *ast.ImportCallExpression:
		// A dynamic import yields Promise<{ ...exports }> (TDD-00056 lazy).
		return PromiseOf(e.importCallResultObjectType(ex))
	case *ast.NewArrayBufferExpression:
		ty := ArrayBufferType()
		if ex.Shared {
			ty = SharedArrayBufferType()
		}
		ty.BufferGrowable = ex.MaxByteLength != nil
		return ty
	case *ast.NewTypedArrayExpression:
		return TypedArrayType(ex.ElemKind)
	case *ast.NewBroadcastChannelExpression:
		return BroadcastChannelType(ex.Name)
	case *ast.NewMessageChannelExpression:
		if ex.TypeArg != nil {
			return MessageChannelType(e.resolveType(ex.TypeArg))
		}
		return MessageChannelType(TypeI64)
	case *ast.NewChannelExpression:
		if ex.TypeArg != nil {
			return ChannelType(e.resolveType(ex.TypeArg))
		}
		return ChannelType(TypeI64)
	case *ast.NewDataViewExpression:
		return DataViewType()
	case *ast.NewBlobExpression:
		if gen := e.blobShadowedByClass(ex); gen != nil {
			return e.inferExprType(gen)
		}
		return BlobType()
	case *ast.NewTextEncoderExpression:
		return TextEncoderType()
	case *ast.NewTextDecoderExpression:
		return TextDecoderType()
	case *ast.NewRegExpExpression:
		return RegExpType()
	case *ast.NewEventEmitterExpression:
		payload := TypePtr
		if ex.PayloadType != nil {
			payload = e.resolveEventEmitterPayloadType(ex.PayloadType)
		}
		return EventEmitterType(payload)
	case *ast.NewAbortControllerExpression:
		return AbortControllerType()
	case *ast.NewEventTargetExpression:
		return EventTargetType()
	case *ast.NewHTTPAgentExpression:
		return HTTPAgentType()
	case *ast.NewWebviewExpression:
		return WebviewType()
	case *ast.NewEventExpression:
		return EventType()
	case *ast.NewCustomEventExpression:
		detailTy := TypePtr
		if ex.Detail != nil {
			detailTy = e.inferExprType(ex.Detail)
		}
		return CustomEventType(detailTy)
	case *ast.NewEventSourceExpression:
		return EventSourceType()
	case *ast.NewWebSocketExpression:
		return WebSocketClientType()
	case *ast.NewWorkerExpression:
		return WorkerType(ex.ResolvedPath)
	case *ast.NewHeadersExpression:
		return HeadersType()
	case *ast.NewRequestExpression:
		return FetchRequestType()
	case *ast.NewXMLHttpRequestExpression:
		return XMLHttpRequestType()
	case *ast.ObjectLiteral:
		// Bare literals are dynamic under `-compat=js` (matching emitExpr's
		// routing); hint-typed contexts never consult this default.
		if e.compatJS() {
			return TypeAny
		}
		return e.inferObjectType(ex)
	case *ast.ArrayLiteral:
		// TDD-00028: inferExprType previously had no case for a bare array
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
				params[i] = ArrayOf(TypeI64)
			} else if p.Type == nil {
				params[i] = TypeI64
				params[i].Inferred = true // no annotation — see docs/adr/ADR-00042.md
			} else {
				params[i] = e.resolveType(p.Type)
			}
		}
		var ret Type
		if ex.RetType != nil {
			ret = e.resolveType(ex.RetType)
		} else if ex.Body != nil {
			ret = e.inferExprType(ex.Body)
		} else if blockHasReturn(ex.Block) {
			// Same best-effort inference emitArrowFunctionWithHints uses when
			// actually emitting this closure — this duplicate exists because
			// inferExprType has to answer "what type is this arrow function"
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
			if inferred, ok := e.inferUnannotatedReturnType(ex.Block, paramNames, params); ok {
				ret = inferred
			} else {
				ret = TypeI64
			}
		} else {
			ret = TypeVoid
		}
		fty := FuncType(params, asyncClosureRetType(ex.IsAsync, ret))
		if len(ex.Params) > 0 && ex.Params[len(ex.Params)-1].Rest {
			fty.FuncHasRest = true
		}
		return fty
	case *ast.FunctionExpression:
		// Same type-inference as ArrowFunction above — a function expression
		// is a block-only closure whose type is determined by param/return
		// types and captures. Params use the same resolver; unlike arrows,
		// function expressions never have expression bodies (Body is always
		// a BlockStatement, never an Expression).
		params := make([]Type, len(ex.Params))
		for i, p := range ex.Params {
			if p.Rest && p.Type == nil {
				params[i] = ArrayOf(TypeI64)
			} else if p.Type == nil {
				params[i] = TypeI64
				params[i].Inferred = true
			} else {
				params[i] = e.resolveType(p.Type)
			}
		}
		var ret Type
		if ex.RetType != nil {
			ret = e.resolveType(ex.RetType)
		} else if blockHasReturn(ex.Body) {
			paramNames := make([]string, len(ex.Params))
			for i, p := range ex.Params {
				paramNames[i] = p.Name
			}
			if inferred, ok := e.inferUnannotatedReturnType(ex.Body, paramNames, params); ok {
				ret = inferred
			} else {
				ret = TypeI64
			}
		} else {
			ret = TypeVoid
		}
		fty := FuncType(params, asyncClosureRetType(ex.IsAsync, ret))
		if len(ex.Params) > 0 && ex.Params[len(ex.Params)-1].Rest {
			fty.FuncHasRest = true
		}
		return fty
	}
	return TypeI64
}

// isPlainStringTy reports whether ty is a genuine string — not an object,
// array, closure, or any other ptr-backed builtin marker type (Map/Set/
// EventEmitter/ArrayBuffer/TextEncoder/TextDecoder/Promise all share
// string's bare IR=="ptr" shape with none of the other flags isStringTy
// already excludes, so isStringTy alone isn't precise enough here). Used
// only by toBool: a string is the one JS primitive whose truthiness
// depends on its content (empty string is falsy) rather than "is this
// value present at all," and treating e.g. a Map as string-shaped would
// wrongly base its truthiness on the first byte of its internal
// representation instead of always being truthy like any other object.
func isPlainStringTy(ty Type) bool {
	return isStringTy(ty) && !ty.IsMap && !ty.IsSet && !ty.IsEventEmitter &&
		!ty.IsArrayBuffer && !ty.IsDataView && !ty.IsTextEncoder && !ty.IsTextDecoder && !ty.IsPromise
}

// toBool converts a Value to i1 (truthiness).
//
// A bare integer 0 literal is invalid LLVM syntax against a ptr-typed
// operand (LLVM requires the `null` keyword) — found as a real, pre-
// existing bug while wiring RegExp.exec()'s `T[] | null` return: any bare
// ptr-typed truthiness check (`if (someObj)`, ...) previously emitted
// unparseable IR, a hard clang-stage failure, not just a wrong runtime
// answer. See ADR-00116.
//
// An array value is a {ptr,i64} aggregate, not a plain ptr — icmp cannot
// compare an aggregate directly, so its data pointer is extracted first.
// Only a Nullable array's truthiness actually depends on that pointer
// (this compiler's own null-array sentinel, {ptr: null, len: 0} — see
// emitRegexExec); a non-Nullable array is always truthy regardless of its
// pointer, matching real JS ("any array, even an empty one, is truthy")
// and avoiding a false "falsy" result from libc's malloc(0) sometimes
// returning NULL for an ordinary empty array.
//
// A genuine string also needs its own path, found the same way: falsy for
// a real null (a `string | null` null value) OR an empty string (""),
// truthy for anything else — content-dependent, unlike every other
// ptr-backed value here, which is truthy whenever merely non-null. A bare
// "is the pointer null" check alone (what every other ptr type uses) would
// leave `if ("")` truthy, since an empty string is still a real, non-null
// 1-byte buffer, not a null pointer.
func (e *Emitter) toBool(v Value) Value {
	if v.Ty.IR == "i1" {
		return v
	}
	// A void/undefined/null value is falsy (JS ToBoolean(undefined)===false,
	// ToBoolean(null)===false). This reaches here from a void-returning
	// predicate used by filter/some/every/find — a callback with no `return`
	// statement — where the result carries no value; producing `false` avoids
	// emitting an `icmp`/`br` against a `void` operand (invalid IR). ADR-00687.
	if v.Ty.IR == "void" || v.Ty.IR == "" || v.Ty.IsUndefined || v.Ty.IsNull {
		return Value{Ref: zeroRef(TypeBool), Ty: TypeBool}
	}
	// A dynamic value's truthiness is the real JS ToBoolean over the
	// NaN-boxed word (TDD-00076): false/null/undefined/±0/NaN/"" are false,
	// everything else true. Runs in both compat modes — truthiness has one
	// correct answer, unlike the js-gated operator dispatch.
	if v.Ty.IsDynamic {
		return e.emitAnyTruthy(v)
	}
	if v.Ty.IsBigInt {
		// 0n is falsy, every other bigint truthy — a bigint handle is never
		// null, so the generic ptr!=null path below would wrongly call 0n truthy.
		e.ensureBigInt()
		zero := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 0)", zero))
		cmpReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_bigint_cmp(ptr %s, ptr %s)", cmpReg, v.Ref, zero))
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", reg, cmpReg))
		return Value{Ref: reg, Ty: TypeBool}
	}
	if v.Ty.IsArray {
		if !v.Ty.Nullable {
			return Value{Ref: "1", Ty: TypeBool}
		}
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, v.Ref))
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", reg, ptrReg))
		return Value{Ref: reg, Ty: TypeBool}
	}
	if isPlainStringTy(v.Ty) {
		return e.emitStringTruthiness(v)
	}
	reg := e.freshReg()
	switch {
	case v.Ty.Float:
		// "one" (ordered-and-not-equal) is NaN-safe: a NaN comparison is
		// "unordered," so this correctly evaluates false for NaN, matching
		// real JS's Boolean(NaN) === false.
		e.emitInstr(fmt.Sprintf("%s = fcmp one %s %s, 0.0", reg, v.Ty.IR, v.Ref))
	case v.Ty.IR == "ptr":
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", reg, v.Ref))
	default:
		e.emitInstr(fmt.Sprintf("%s = icmp ne %s %s, 0", reg, v.Ty.IR, v.Ref))
	}
	return Value{Ref: reg, Ty: TypeBool}
}

// emitStringTruthiness implements real JS string truthiness: falsy for a
// real null (a `string | null` null value — checked first, since loading a
// byte through a genuinely null pointer would be undefined behavior) or an
// empty string, truthy for anything else.
func (e *Emitter) emitStringTruthiness(v Value) Value {
	isNullReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNullReg, v.Ref))

	nullL := e.freshLabel("strbool.null")
	checkL := e.freshLabel("strbool.check")
	mergeL := e.freshLabel("strbool.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullReg, nullL, checkL))

	resultSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultSlot))

	e.emitLabel(nullL)
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(checkL)
	firstByte := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", firstByte, v.Ref))
	nonEmptyReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", nonEmptyReg, firstByte))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", nonEmptyReg, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resultSlot))
	return Value{Ref: result, Ty: TypeBool}
}
