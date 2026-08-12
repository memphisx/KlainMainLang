package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
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
	e.ensureSprintf()
	e.ensureMalloc()
	scratch := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", scratch))
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
		fmtPtr := e.internString("%g")
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, double %s)", scratch, fmtPtr, val.Ref))
	case v.Ty.IsInteger():
		val := v
		if v.Ty.IR != "i64" {
			r := e.freshReg()
			ext := "sext"
			if !v.Ty.Signed {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to i64", r, ext, v.Ty.IR, v.Ref))
			val = Value{Ref: r, Ty: TypeI64}
		}
		fmtPtr := e.internString("%lld")
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, fmtPtr, val.Ref))
	default:
		return Value{}, fmt.Errorf("cannot convert type %s to string in template literal", v.Ty.IR)
	}
	return Value{Ref: scratch, Ty: TypePtr}, nil
}

// inferArrayType picks an element type by looking at the first element of a literal.
func (e *Emitter) inferArrayType(lit *ast.ArrayLiteral) Type {
	if len(lit.Elements) == 0 {
		return ArrayOf(TypeI64) // default: number[]
	}
	first := lit.Elements[0]
	if sp, ok := first.(*ast.SpreadElement); ok {
		// Spread of an array — infer from the spread source.
		if ty := e.inferExprType(sp.Arg); ty.IsArray {
			return ty
		}
		return ArrayOf(TypeI64)
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
func (e *Emitter) callbackReturnType(arg ast.Expression) (Type, bool) {
	switch cb := arg.(type) {
	case *ast.ArrowFunction:
		if cb.RetType != nil {
			return e.resolveType(cb.RetType), true
		}
		if cb.Body != nil {
			return e.inferExprType(cb.Body), true
		}
		return TypeI64, true
	case *ast.FunctionExpression:
		if cb.RetType != nil {
			return e.resolveType(cb.RetType), true
		}
		paramNames := make([]string, len(cb.Params))
		paramTypes := make([]Type, len(cb.Params))
		for i, p := range cb.Params {
			paramNames[i] = p.Name
			if p.Type != nil {
				paramTypes[i] = e.resolveType(p.Type)
			} else {
				paramTypes[i] = TypeI64
			}
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
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		if strings.ContainsRune(ex.Value, '.') {
			return TypeF64
		}
		return TypeI64
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
		return TypeI64
	case *ast.Identifier:
		if sym, ok := e.lookup(ex.Name); ok {
			return sym.Ty
		}
		switch ex.Name {
		case "NaN", "Infinity":
			return TypeF64
		}
		if _, _, ok := e.resolveFuncRef(ex.Name); ok {
			return Type{IR: "ptr", IsFunc: true}
		}
	case *ast.IndexExpression:
		if e.isProcessEnvExpr(ex.Object) {
			return TypePtr
		}
		objTy := e.inferExprType(ex.Object)
		if objTy.IsGroupMap {
			if objTy.ElemType != nil {
				return ArrayOf(*objTy.ElemType)
			}
			return ArrayOf(TypeI64)
		}
		if isStringTy(objTy) {
			return TypePtr
		}
		if objTy.IsArray && objTy.ElemType != nil {
			return *objTy.ElemType
		}
	case *ast.BinaryExpression:
		switch ex.Op {
		case "===", "!==", "==", "!=", "<", ">", "<=", ">=", "instanceof", "in":
			return TypeBool
		case "+":
			lt := e.inferExprType(ex.Left)
			rt := e.inferExprType(ex.Right)
			if isStringTy(lt) || isStringTy(rt) {
				return TypePtr
			}
			// Date + number / number + Date: add a duration, stays a Date
			// (see emitBinary for the full Date-arithmetic rules).
			if lt.IsDate != rt.IsDate {
				return TypeDate
			}
			return TypeI64
		case "-":
			lt := e.inferExprType(ex.Left)
			rt := e.inferExprType(ex.Right)
			// Date - Date: a plain number (ms difference). Date - number:
			// subtract a duration, stays a Date. number - Date is rejected
			// by emitBinary, so its inferred type here is moot.
			if lt.IsDate && rt.IsDate {
				return TypeI64
			}
			if lt.IsDate && !rt.IsDate {
				return TypeDate
			}
			return TypeI64
		case "&&", "||":
			return e.inferExprType(ex.Left)
		case "??":
			lt := e.inferExprType(ex.Left)
			if lt.IR == "ptr" {
				return e.inferExprType(ex.Right)
			}
			return lt
		case "&", "|", "^", "<<", ">>", ">>>":
			return TypeI64
		}
	case *ast.MemberExpression:
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
				case "pid":
					return TypeI64
				case "platform":
					return TypePtr
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
			if _, fieldTy, ok := objTy.FieldIndex(ex.Property); ok {
				return e.canonicalizeClassTy(fieldTy)
			}
		}
	case *ast.ThisExpression:
		if sym, ok := e.lookup("this"); ok {
			return sym.Ty
		}
	case *ast.NewExpression:
		if info, ok := e.classes[ex.ClassName]; ok {
			return info.Ty
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
		// TDD-00059: same desugaring emitExpr's own case uses — a tagged
		// template's type is exactly its tag function's return type.
		return e.inferExprType(desugarTaggedTemplate(ex))
	case *ast.CallExpression:
		// Static method call: ClassName.staticMethod(args) (TDD-00009 Stage 4).
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if id, ok := mem.Object.(*ast.Identifier); ok {
				if info, found := e.classes[id.Name]; found {
					if sig, ok := info.StaticMethodSigs[mem.Property]; ok {
						return sig.RetType
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
						return sig.RetType
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
		// Generator construction (TDD-00061/ADR-00172) — `gen(args)`'s own
		// type is the constructed instance's GenTy, not ElemTy (that's
		// `.next()`'s own result's `value` field type instead, handled by
		// the member-expression case just below).
		if id, ok := ex.Callee.(*ast.Identifier); ok {
			if info, found := e.generators[id.Name]; found {
				return info.GenTy
			}
		}
		// gen.next(value)'s own result type ({value: T, done: bool}) —
		// checked before the generic member-expression dispatch further
		// down, same as every other type-tag-gated case there.
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok && mem.Property == "next" {
			if objTy := e.inferExprType(mem.Object); objTy.IsGenerator {
				return genNextResultType(*objTy.GeneratorElemType)
			}
		}
		// If calling a named function, use its registered return type (handles async too).
		if id, ok := ex.Callee.(*ast.Identifier); ok {
			if _, sig, found := e.resolveFuncRef(id.Name); found {
				return sig.RetType
			}
			// A generic function (TDD-00010 V1) is never itself in e.funcs
			// — infer what its return type *would* be for this call's own
			// argument types, purely (see genericCallReturnType's doc
			// comment for why this must never trigger real emission).
			if decl, found := e.genericFuncs[id.Name]; found {
				if ty, ok := e.genericCallReturnType(decl, ex.Args); ok {
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
			case "parseInt":
				return TypeI64
			case "parseFloat":
				return TypeF64
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
				case "parseInt":
					return TypeI64
				case "parseFloat":
					return TypeF64
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Math" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "random", "sqrt", "pow", "hypot", "log", "log2", "log10", "sin", "cos", "tan",
					"asin", "acos", "atan", "atan2", "sinh", "cosh", "tanh", "cbrt", "expm1", "log1p", "fround":
					return TypeF64
				case "floor", "ceil", "round", "trunc", "sign", "clz32", "imul":
					return TypeI64
				case "abs":
					if len(ex.Args) == 1 {
						return e.inferExprType(ex.Args[0])
					}
				case "min", "max", "clamp":
					if len(ex.Args) > 0 {
						return e.inferExprType(ex.Args[0])
					}
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "JSON" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "stringify":
					return TypePtr
				case "parse":
					return TypePtr
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
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
				switch mem.Property {
				case "readLineSync", "execFileSync", "cwd":
					return TypePtr
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
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "assert__kml_builtin" {
				return TypeVoid
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
					if len(ex.Args) == 1 {
						argTy := e.inferExprType(ex.Args[0])
						if argTy.IsArray {
							return ArrayOf(*argTy.ElemType)
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
						}
					}
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
								entryTy := ObjectType([]Field{{Name: "key", Ty: keyTy}, {Name: "value", Ty: valTy}})
								return ArrayOf(entryTy)
							}
						}
					}
					if mem.Property == "entries" {
						entryTy := ObjectType([]Field{{Name: "key", Ty: TypePtr}, {Name: "value", Ty: TypePtr}})
						return ArrayOf(entryTy)
					}
					return ArrayOf(TypePtr)
				case "hasOwn":
					return TypeBool
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
			if haveObjTy && objTy.IsMap {
				switch mem.Property {
				case "get":
					if objTy.MapVal != nil {
						return *objTy.MapVal
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
					entryTy := ObjectType([]Field{{Name: "key", Ty: keyTy}, {Name: "value", Ty: valTy}})
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
				if isNumberTy(e.inferExprType(mem.Object)) {
					return TypePtr
				}
			case "text":
				if e.inferExprType(mem.Object).IsResponse {
					return TypePtr
				}
			case "json":
				if e.inferExprType(mem.Object).IsResponse {
					// No declaration context here to parse into (that's
					// handled separately, see emitResponseJSON) — TypePtr
					// matches bare JSON.parse's own default-context type.
					return TypePtr
				}
			case "arrayBuffer":
				if e.inferExprType(mem.Object).IsResponse {
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
				if e.inferExprType(mem.Object).IsRegExp {
					return TypeBool
				}
			case "exec":
				if e.inferExprType(mem.Object).IsRegExp {
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
			case "indexOf", "charCodeAt", "findIndex", "findLastIndex", "codePointAt", "search", "localeCompare":
				return TypeI64
			case "includes", "startsWith", "endsWith", "some", "every":
				return TypeBool
			case "join", "repeat", "padStart", "padEnd", "toFixed", "charAt", "toPrecision", "toExponential":
				return TypePtr
			case "at", "findLast":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && objTy.ElemType != nil {
					return *objTy.ElemType
				}
				return TypePtr // string.at returns a char string
			case "concat", "reverse", "fill", "toReversed", "toSorted", "toSpliced", "with", "copyWithin", "values":
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
					entryTy := ObjectType([]Field{{Name: "index", Ty: TypeI64}, {Name: "value", Ty: *objTy.ElemType}})
					return ArrayOf(entryTy)
				}
			case "slice":
				objTy := e.inferExprType(mem.Object)
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
					if retTy, ok := e.callbackReturnType(ex.Args[0]); ok {
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
					if retTy, ok := e.callbackReturnType(ex.Args[0]); ok {
						if retTy.IsArray && retTy.ElemType != nil {
							return ArrayOf(*retTy.ElemType)
						}
						return ArrayOf(retTy)
					}
					return objTy
				}
			case "push", "unshift":
				// Returns the new length (i64), matching JS semantics.
				return TypeI64
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
		if ex.Op == "typeof" {
			return TypePtr
		}
	case *ast.ConditionalExpression:
		return e.inferExprType(ex.Consequent)
	case *ast.NewErrorExpression:
		return errorObjType
	case *ast.NewDateExpression:
		return TypeDate
	case *ast.NewURLExpression:
		return URLType()
	case *ast.NewURLSearchParamsExpression:
		return URLSearchParamsType()
	case *ast.NewArrayBufferExpression:
		return ArrayBufferType()
	case *ast.NewTextEncoderExpression:
		return TextEncoderType()
	case *ast.NewTextDecoderExpression:
		return TextDecoderType()
	case *ast.NewRegExpExpression:
		return RegExpType()
	case *ast.NewEventSourceExpression:
		return EventSourceType()
	case *ast.NewWebSocketExpression:
		return WebSocketClientType()
	case *ast.NewHeadersExpression:
		return HeadersType()
	case *ast.NewRequestExpression:
		return FetchRequestType()
	case *ast.NewXMLHttpRequestExpression:
		return XMLHttpRequestType()
	case *ast.ObjectLiteral:
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
		fty := FuncType(params, ret)
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
		fty := FuncType(params, ret)
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
		!ty.IsArrayBuffer && !ty.IsTextEncoder && !ty.IsTextDecoder && !ty.IsPromise
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
