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
		if _, ok := e.funcs[ex.Name]; ok {
			return Type{IR: "ptr", IsFunc: true}
		}
	case *ast.IndexExpression:
		if isProcessEnvExpr(ex.Object) {
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
		case "===", "!==", "==", "!=", "<", ">", "<=", ">=", "instanceof":
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
		if id, ok := ex.Object.(*ast.Identifier); ok {
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
			}
		}
		if isProcessEnvExpr(ex.Object) {
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
	case *ast.CallExpression:
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
				}
			}
		}
		// If calling a named function, use its registered return type (handles async too).
		if id, ok := ex.Callee.(*ast.Identifier); ok {
			if sig, found := e.funcs[id.Name]; found {
				return sig.RetType
			}
			// Calling a closure-typed variable (e.g. a const-bound arrow
			// function) — same fallback resolveCallback (emit_func.go)
			// already uses, so a call's result is correctly typed regardless
			// of whether the callee is a named declaration or a value.
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc && sym.Ty.FuncRetType != nil {
				return *sym.Ty.FuncRetType
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
			case "setTimeout", "setInterval":
				return TypeI64
			}
		}
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "console" {
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
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "String" {
				switch mem.Property {
				case "fromCharCode", "fromCodePoint":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Number" {
				switch mem.Property {
				case "isInteger", "isFinite", "isNaN", "isSafeInteger":
					return TypeBool
				case "parseInt":
					return TypeI64
				case "parseFloat":
					return TypeF64
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Math" {
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
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "JSON" {
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
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "performance" && mem.Property == "now" {
				return TypeF64
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "fs" {
				switch mem.Property {
				case "readFileSync":
					return TypePtr
				case "existsSync":
					return TypeBool
				case "readdirSync":
					return ArrayOf(TypePtr)
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "process" {
				switch mem.Property {
				case "readLineSync", "execFileSync", "cwd":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "crypto" {
				switch mem.Property {
				case "getRandomValues":
					if len(ex.Args) == 1 {
						return e.inferExprType(ex.Args[0])
					}
				case "randomUUID":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Array" {
				switch mem.Property {
				case "of":
					if len(ex.Args) > 0 {
						return ArrayOf(e.inferExprType(ex.Args[0]))
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
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Object" {
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
			case "split":
				return ArrayOf(TypePtr)
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
					if af, ok := ex.Args[0].(*ast.ArrowFunction); ok {
						var retTy Type
						if af.RetType != nil {
							retTy = e.resolveType(af.RetType)
						} else if af.Body != nil {
							retTy = e.inferExprType(af.Body)
						} else {
							retTy = TypeI64
						}
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
	case *ast.ObjectLiteral:
		return e.inferObjectType(ex)
	case *ast.ArrowFunction:
		params := make([]Type, len(ex.Params))
		for i, p := range ex.Params {
			if p.Type == nil {
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
		return FuncType(params, ret)
	}
	return TypeI64
}

// toBool converts a Value to i1 via icmp ne 0.
func (e *Emitter) toBool(v Value) Value {
	if v.Ty.IR == "i1" {
		return v
	}
	reg := e.freshReg()
	if v.Ty.Float {
		e.emitInstr(fmt.Sprintf("%s = fcmp one %s %s, 0.0", reg, v.Ty.IR, v.Ref))
	} else {
		e.emitInstr(fmt.Sprintf("%s = icmp ne %s %s, 0", reg, v.Ty.IR, v.Ref))
	}
	return Value{Ref: reg, Ty: TypeBool}
}
