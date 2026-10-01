package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// emit_intrinsics.go — the builtins a declaration marks `/** @intrinsic
// Owner.name */` (TDD-00230 P3.2): calls the backend emits inline, with
// representation-aware paths (an integer stays an i64 through Math.floor),
// reached through the declaration like an @lower call rather than by name.

type intrinsic struct {
	emit func(e *Emitter, ex *ast.CallExpression) (Value, error)
	ty   func(e *Emitter, ex *ast.CallExpression) Type
}

var intrinsics = map[string]intrinsic{}

// arrayIntrinsics are the array methods with their own inline loops, the
// same emitters for a plain array, a typed array and a Buffer.
var arrayIntrinsics = map[string]func(e *Emitter, mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error){
	"map":           (*Emitter).emitArrayMap,
	"filter":        (*Emitter).emitArrayFilter,
	"find":          (*Emitter).emitArrayFind,
	"findIndex":     (*Emitter).emitArrayFindIndex,
	"findLast":      (*Emitter).emitArrayFindLast,
	"findLastIndex": (*Emitter).emitArrayFindLastIndex,
	"some":          (*Emitter).emitArraySome,
	"every":         (*Emitter).emitArrayEvery,
	"forEach":       (*Emitter).emitArrayForEach,
	"flatMap":       (*Emitter).emitArrayFlatMap,
	"push":          (*Emitter).emitPush,
	"pop":           (*Emitter).emitPop,
	"shift":         (*Emitter).emitShift,
	"unshift":       (*Emitter).emitUnshift,
	"splice":        (*Emitter).emitSplice,
	"join":          (*Emitter).emitArrayJoin,
	"sort":          (*Emitter).emitArraySort,
	"flat":          (*Emitter).emitArrayFlat,
	"reverse":       (*Emitter).emitArrayReverse,
	"toReversed":    (*Emitter).emitArrayToReversed,
	"toSorted":      (*Emitter).emitArrayToSorted,
	"toSpliced":     (*Emitter).emitArrayToSpliced,
	"with":          (*Emitter).emitArrayWith,
	"copyWithin":    (*Emitter).emitArrayCopyWithin,
	"fill":          (*Emitter).emitArrayFill,
	"slice":         (*Emitter).emitArraySlice,
	"indexOf":       (*Emitter).emitArrayIndexOf,
	"lastIndexOf":   (*Emitter).emitArrayLastIndexOf,
	"includes":      (*Emitter).emitArrayIncludes,
	"at":            (*Emitter).emitArrayAt,
	"concat":        (*Emitter).emitArrayConcat,
	"keys":          (*Emitter).emitArrayKeys,
	"values":        (*Emitter).emitArrayValues,
	"entries":       (*Emitter).emitArrayEntries,
	"reduce": func(e *Emitter, mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
		return e.emitArrayReduce(mem, args, pos, false)
	},
	"reduceRight": func(e *Emitter, mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
		return e.emitArrayReduce(mem, args, pos, true)
	},
}

// stringIntrinsics are the String methods taking a RegExp or a replacer
// callback, which no lowering represents yet.
var stringIntrinsics = map[string]func(e *Emitter, mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error){
	"search":        (*Emitter).emitStringSearch,
	"localeCompare": (*Emitter).emitStringLocaleCompare,
	"replace":       (*Emitter).emitStringReplace,
	"replaceAll":    (*Emitter).emitStringReplaceAll,
	"split":         (*Emitter).emitStringSplit,
}

// collectionIntrinsics are the Map, Set, WeakMap and WeakSet methods, by
// owner: each emits through the collection's own runtime.
var collectionIntrinsics = map[string][]string{
	"Map":     {"clear", "delete", "forEach", "get", "has", "set", "entries", "keys", "values"},
	"Set":     {"add", "clear", "delete", "forEach", "has", "entries", "keys", "values"},
	"WeakMap": {"delete", "get", "has", "set"},
	"WeakSet": {"add", "delete", "has"},
}

// emitCollectionIntrinsic is a Map, Set, WeakMap or WeakSet method call.
func (e *Emitter) emitCollectionIntrinsic(ex *ast.CallExpression) (Value, error) {
	mem := ex.Callee.(*ast.MemberExpression)
	ty, ptr, err := e.resolveMapOrSetForCall(mem.Object, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	switch {
	case ty.Weak:
		return e.emitWeakCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
	case ty.IsMap:
		return e.emitMapCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
	}
	return e.emitSetCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
}

// emitDateIntrinsic is a Date method call: a setter rewrites the date's
// time value, anything else reads or formats it.
func (e *Emitter) emitDateIntrinsic(ex *ast.CallExpression) (Value, error) {
	mem := ex.Callee.(*ast.MemberExpression)
	if isDateSetterName(mem.Property) {
		return e.emitDateSetterCall(mem, mem.Property, ex.Args, ex.GetPos())
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	return e.emitDateCall(objVal, mem.Property, ex.GetPos())
}

// dateIntrinsicNames is every Date method emitDateIntrinsic handles.
func dateIntrinsicNames() []string {
	names := []string{"getTime", "valueOf", "toISOString", "toDateString", "toLocaleDateString", "toUTCString", "toGMTString", "toString", "setTime", "getTimezoneOffset"}
	for n := range dateDecomposeFieldIndex {
		names = append(names, n)
	}
	for n := range dateSetterFieldIndex {
		names = append(names, n)
	}
	return names
}

func init() {
	intrinsics["RegExp.prototype.exec"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitRegexExec(ex.Callee.(*ast.MemberExpression), ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return execArrayType() },
	}
	intrinsics["RegExp.prototype.test"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitRegexTest(ex.Callee.(*ast.MemberExpression), ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeBool },
	}
	for _, name := range dateIntrinsicNames() {
		intrinsics["Date.prototype."+name] = intrinsic{
			emit: (*Emitter).emitDateIntrinsic,
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				if t, ok := e.inferMethodNameType(ex, ex.Callee.(*ast.MemberExpression)); ok {
					return t
				}
				return TypePtr // toString
			},
		}
	}
	for owner, names := range collectionIntrinsics {
		for _, name := range names {
			intrinsics[owner+".prototype."+name] = intrinsic{
				emit: (*Emitter).emitCollectionIntrinsic,
				ty: func(e *Emitter, ex *ast.CallExpression) Type {
					if t, ok := e.inferCollectionCallType(ex, ex.Callee.(*ast.MemberExpression)); ok {
						return t
					}
					return TypeVoid // clear, forEach
				},
			}
		}
	}
	for name, emit := range stringIntrinsics {
		emit := emit
		intrinsics["String.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return emit(e, ex.Callee.(*ast.MemberExpression), ex.Args, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				if t, ok := e.inferMethodNameType(ex, ex.Callee.(*ast.MemberExpression)); ok {
					return t
				}
				return TypePtr // replaceAll
			},
		}
	}
	for name, emit := range arrayIntrinsics {
		name, emit := name, emit
		intrinsics["Array.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return emit(e, ex.Callee.(*ast.MemberExpression), ex.Args, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				if name == "forEach" {
					return TypeVoid
				}
				if t, ok := e.inferMethodNameType(ex, ex.Callee.(*ast.MemberExpression)); ok {
					return t
				}
				return TypeI64
			},
		}
	}
	for name, v := range map[string]struct {
		emit func(e *Emitter, ex *ast.CallExpression) (Value, error)
		ty   Type
	}{
		"parseInt":   {func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitParseInt(ex.Args, ex.GetPos()) }, TypeF64},
		"parseFloat": {func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitParseFloat(ex.Args, ex.GetPos()) }, TypeF64},
		"isNaN": {func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitNumberIsNaN(ex.Args, ex.GetPos(), true)
		}, TypeBool},
		"isFinite": {func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitNumberIsFinite(ex.Args, ex.GetPos(), true)
		}, TypeBool},
	} {
		ty := v.ty
		intrinsics[name] = intrinsic{emit: v.emit, ty: func(*Emitter, *ast.CallExpression) Type { return ty }}
	}
	for name, v := range map[string]struct {
		sym    string
		ensure func(e *Emitter)
	}{
		"btoa":               {"@__kml_btoa", (*Emitter).ensureBase64Encode},
		"atob":               {"@__kml_atob", (*Emitter).ensureBase64Decode},
		"encodeURIComponent": {"@__kml_encode_uri_component", (*Emitter).ensureEncodeURIComponent},
		"decodeURIComponent": {"@__kml_decode_uri_component_strict", (*Emitter).ensureDecodeURIComponentStrict},
		"encodeURI":          {"@__kml_encode_uri", (*Emitter).ensureEncodeURI},
		"decodeURI":          {"@__kml_decode_uri_strict", (*Emitter).ensureDecodeURIStrict},
	} {
		name, v := name, v
		intrinsics[name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitStringToStringBuiltin(ex.Args, ex.GetPos(), name, v.sym, func() { v.ensure(e) })
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return TypePtr },
		}
	}
	for _, name := range []string{"isInteger", "isFinite", "isNaN", "isSafeInteger"} {
		prop := name
		intrinsics["Number."+prop] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitNumberStaticCall(prop, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return TypeBool },
		}
	}
	for _, name := range []string{"fromCharCode", "fromCodePoint"} {
		prop := name
		intrinsics["String."+prop] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitStringStaticCall(prop, ex.Args, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type { return TypePtr },
		}
	}
	for _, name := range []string{"abs", "cbrt", "ceil", "clz32", "floor", "fround", "hypot", "imul", "max", "min", "pow", "random", "round", "sign", "trunc"} {
		prop := name
		intrinsics["Math."+prop] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitMathCall(prop, ex.Args, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type { return e.mathResultType(prop, ex) },
		}
	}
}

// mathResultType is the type a Math intrinsic's call has: an integer input
// stays an i64 through floor/ceil/round/trunc/sign/abs/min/max, a float input
// a double — the emitters' own result types.
func (e *Emitter) mathResultType(prop string, ex *ast.CallExpression) Type {
	switch prop {
	case "random", "pow", "hypot", "cbrt", "fround":
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
		// Zero args is the ±Infinity identity, a double.
		if len(ex.Args) == 0 {
			return TypeF64
		}
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
	}
	return TypeF64
}

// intrinsicSpecLength is Function.prototype.length where the spec's differs
// from the declaration's leading parameters (a rest-parameter builtin).
var intrinsicSpecLength = map[string]int{
	"Math.max": 2, "Math.min": 2, "Math.hypot": 2,
	"String.fromCharCode": 1, "String.fromCodePoint": 1,
}

// builtinFn is a builtin function referenced by value (`arr.map(parseFloat)`,
// `const f = Math.sqrt`): an @intrinsic or a global object's @lower method,
// materialized as a real function once and shared by every reference.
type builtinFn struct {
	key    string // `parseFloat`, `Math.sqrt`: one function per key
	name   string // Function.prototype.name
	params []Type // the value's parameters; a rest one is last
	rest   bool
	// call emits the builtin's body over the parameters (identifiers bound
	// in the function's scope; a rest one spread).
	call func(e *Emitter, args []ast.Expression) (Value, error)
}

// builtinRef reports the builtin function an expression names by value.
func (e *Emitter) builtinRef(x ast.Expression) (*builtinFn, bool) {
	// A cheap syntactic filter first: this runs for every expression inferred.
	switch x := x.(type) {
	case *ast.Identifier:
		if _, ok := intrinsics[x.Name]; !ok {
			return nil, false
		}
	case *ast.MemberExpression:
		if x.Optional {
			return nil, false
		}
		// `Math.floor`, `globalThis.parseFloat`, `globalThis.Math.sqrt`.
		owner := x.Object
		if m, ok := owner.(*ast.MemberExpression); ok {
			if g, ok := m.Object.(*ast.Identifier); !ok || g.Name != "globalThis" {
				return nil, false
			}
			owner = m.Object
		}
		if id, ok := owner.(*ast.Identifier); !ok || !intrinsicOwner[id.Name] && id.Name != "globalThis" {
			return nil, false
		}
	default:
		return nil, false
	}
	c := e.front()
	if c == nil {
		return nil, false
	}
	var key string
	var lower *ast.MethodSignature
	switch x := x.(type) {
	case *ast.Identifier:
		if !e.isBuiltinGlobal(c, x) {
			return nil, false
		}
		sym, _ := c.Binding().Resolve(x)
		if sym == nil {
			return nil, false
		}
		for _, d := range sym.Declarations {
			if fd, ok := d.Node.(*ast.FunctionDeclaration); ok && fd.Intrinsic != "" {
				key = fd.Intrinsic
			}
		}
	case *ast.MemberExpression:
		if !e.isBuiltinGlobalObject(c, x.Object) {
			return nil, false
		}
		switch d := c.MemberDecl(x).(type) {
		case *ast.FunctionDeclaration:
			key = d.Intrinsic // `globalThis.parseFloat`
		case *ast.MethodSignature:
			key = d.Intrinsic
			if key == "" && d.Lower != "" {
				key, lower = x.Property+"@"+d.Lower, d
			}
		default:
			return nil, false
		}
		if strings.Contains(key, ".prototype.") {
			return nil, false // an instance method: it needs its receiver
		}
	}
	fn := c.TypeOf(x)
	if key == "" || fn == nil || fn.Flags&checker.Object == 0 || fn.Kind != checker.Function {
		return nil, false
	}
	if lower != nil {
		// A global object's @lower method: its declared parameters, called
		// through the lowering as a direct call would be.
		mem := x.(*ast.MemberExpression)
		if len(fn.Overloads) > 0 || fn.HasRestParam() {
			return nil, false
		}
		probe := &ast.CallExpression{Callee: mem}
		l, ok := e.loweredCall(probe)
		if !ok || l.intrinsic != "" {
			return nil, false
		}
		b := &builtinFn{key: key, name: mem.Property}
		for _, p := range l.params {
			switch {
			case p.callback != nil, p.bytes, p.rest:
				return nil, false
			case p.optional:
				b.params = append(b.params, TypeAny) // undefined when omitted
			default:
				b.params = append(b.params, p.ty)
			}
		}
		b.call = func(e *Emitter, args []ast.Expression) (Value, error) {
			return e.emitLowered(&ast.CallExpression{Callee: mem, Args: args}, l)
		}
		return b, true
	}
	if _, known := intrinsics[key]; !known {
		return nil, false
	}
	if len(fn.Overloads) > 0 {
		fn = fn.Overloads[len(fn.Overloads)-1]
	}
	// An intrinsic: every parameter an `any` (its emitter converts as the
	// builtin does), a rest one an `any[]`.
	b := &builtinFn{key: key, name: key[strings.LastIndex(key, ".")+1:]}
	for i := range fn.Params {
		if fn.HasRestParam() && i == len(fn.Params)-1 {
			b.params, b.rest = append(b.params, ArrayOf(TypeAny)), true
			continue
		}
		b.params = append(b.params, TypeAny)
	}
	b.call = func(e *Emitter, args []ast.Expression) (Value, error) {
		return intrinsics[key].emit(e, &ast.CallExpression{Callee: &ast.Identifier{Name: key}, Args: args})
	}
	return b, true
}

// ensureBuiltinFunc emits (once) a builtin's function for its use as a
// value. Every reference shares it, so `Number.parseFloat === parseFloat`
// (one key) holds.
func (e *Emitter) ensureBuiltinFunc(b *builtinFn) (string, FuncSig, error) {
	mangled := "__kml_builtin_fn_" + llvmSafeSymbol(b.key)
	if sig, ok := e.intrinsicSigs[b.key]; ok {
		return mangled, sig, nil
	}
	restore := e.beginDetachedFunc()
	e.pushScope()
	sig := FuncSig{ParamTypes: b.params, HasRest: b.rest}
	var decls []string
	var args []ast.Expression
	n := 0
	for i, ty := range b.params {
		name := fmt.Sprintf("__kml_ia%d", i)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, storageIR(ty)))
		if ty.IsArray {
			// An array (the rest) arrives as its header and length.
			decls = append(decls, fmt.Sprintf("ptr %%a%d", n), fmt.Sprintf("i64 %%a%d", n+1))
			e.emitInstr(fmt.Sprintf("store ptr %%a%d, ptr %s, align 8", n, slot))
			n += 2
		} else {
			decls = append(decls, fmt.Sprintf("%s %%a%d", storageIR(ty), n))
			e.emitInstr(fmt.Sprintf("store %s %%a%d, ptr %s, align 8", storageIR(ty), n, slot))
			n++
		}
		e.define(name, Symbol{Ptr: slot, Ty: ty})
		var arg ast.Expression = &ast.Identifier{Name: name}
		if b.rest && i == len(b.params)-1 {
			arg = &ast.SpreadElement{Arg: arg}
		}
		args = append(args, arg)
	}
	v, err := b.call(e, args)
	if err == nil {
		sig.RetType = v.Ty
		if v.Ty.IR == "void" {
			e.emitTerminator("ret void")
		} else {
			e.emitTerminator(fmt.Sprintf("ret %s %s", v.Ty.LLVMRetType(), v.Ref))
		}
	}
	body := e.allocas.String() + e.body.String()
	e.popScope()
	restore()
	if err != nil {
		return "", FuncSig{}, err
	}
	e.functions.WriteString(fmt.Sprintf("\ndefine %s @%s(%s) {\nentry:\n%s}\n", sig.RetType.LLVMRetType(), mangled, strings.Join(decls, ", "), body))
	e.intrinsicSigs[b.key] = sig
	return mangled, sig, nil
}

// emitBuiltinValue is a builtin function referenced by value: its closure
// header, named as the builtin is (`parseFloat`, `sqrt`).
func (e *Emitter) emitBuiltinValue(b *builtinFn) (Value, error) {
	mangled, sig, err := e.ensureBuiltinFunc(b)
	if err != nil {
		return Value{}, err
	}
	if n, ok := intrinsicSpecLength[b.key]; ok {
		e.registerFnMeta(fnValueTrampolineName(mangled), b.name, n, fnKindOf(false, false))
	}
	return e.emitNamedFuncValue(mangled, sig, b.name), nil
}

// intrinsicOwner are the builtin global objects whose members may be
// intrinsics or @lower methods referenced by value (`[4, 9].map(Math.sqrt)`):
// the syntactic filter before the checker decides.
var intrinsicOwner = map[string]bool{"Math": true, "Number": true, "String": true, "JSON": true, "Object": true, "Array": true, "Date": true, "Reflect": true}
