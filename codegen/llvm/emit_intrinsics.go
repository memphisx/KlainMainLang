package llvm

import (
	"fmt"
	"strings"
	"sync"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
	"KlainMainLang/resolver"
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
	"match":         (*Emitter).emitStringMatch,
	"concat":        (*Emitter).emitStringConcatMethod,
	"matchAll":      (*Emitter).emitStringMatchAll,
}

// returnsReceiver are the array methods whose result is their receiver.
var returnsReceiver = map[string]bool{"fill": true, "sort": true, "reverse": true, "copyWithin": true}

// collectionIntrinsics are the Map, Set, WeakMap and WeakSet methods, by
// owner: each emits through the collection's own runtime.
var collectionIntrinsics = map[string][]string{
	"Map":     {"clear", "delete", "forEach", "get", "has", "set", "entries", "keys", "values"},
	"Set":     {"add", "clear", "delete", "forEach", "has", "entries", "keys", "values"},
	"WeakMap": {"delete", "get", "has", "set"},
	"WeakSet": {"add", "delete", "has"},
}

// reflectIntrinsics are the Reflect functions emitReflectCall emits, with
// the type each call has.
var reflectIntrinsics = map[string]Type{
	"apply": TypeAny, "construct": TypeAny, "get": TypeAny, "getPrototypeOf": TypeAny,
	"getOwnPropertyDescriptor": TypeAny, "ownKeys": ArrayOf(TypePtr),
	"set": TypeBool, "has": TypeBool, "deleteProperty": TypeBool, "setPrototypeOf": TypeBool,
	"isExtensible": TypeBool, "preventExtensions": TypeBool, "defineProperty": TypeBool,
	"defineMetadata": TypeAny, "getMetadata": TypeAny, "getOwnMetadata": TypeAny,
	"hasMetadata": TypeBool, "hasOwnMetadata": TypeBool, "metadata": TypeAny,
}

// objectStaticIntrinsics are the Object statics emitObjectStatic emits.
var objectStaticIntrinsics = []string{
	"keys", "values", "entries", "assign", "freeze", "isFrozen", "seal", "isSealed",
	"preventExtensions", "isExtensible", "getPrototypeOf", "setPrototypeOf", "create",
	"getOwnPropertyNames", "getOwnPropertyDescriptor", "defineProperty", "defineProperties",
	"hasOwn", "fromEntries", "groupBy",
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
	names := []string{"getTime", "valueOf", "toISOString", "toJSON", "toDateString", "toTimeString", "toLocaleDateString", "toLocaleTimeString", "toLocaleString", "toUTCString", "toGMTString", "toString", "setTime", "getTimezoneOffset"}
	for n := range dateDecomposeFieldIndex {
		names = append(names, n)
	}
	for n := range dateSetterFieldIndex {
		names = append(names, n)
	}
	return names
}

func init() {
	methodTy := func(e *Emitter, ex *ast.CallExpression) Type {
		t, _ := e.inferMethodNameType(ex, ex.Callee.(*ast.MemberExpression))
		return t
	}
	intrinsics["TypedArray.prototype.set"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitTypedArraySet(ex.Callee.(*ast.MemberExpression), ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeVoid },
	}
	intrinsics["TypedArray.prototype.subarray"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitTypedArraySubarray(ex.Callee.(*ast.MemberExpression), ex.Args, ex.GetPos())
		},
		ty: methodTy,
	}
	for _, name := range bufferIntrinsicNames() {
		name := name
		intrinsics["Buffer.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				mem := ex.Callee.(*ast.MemberExpression)
				return e.emitBufferInstanceCall(mem, name, ex.Args, ex.GetPos())
			},
			ty: methodTy,
		}
	}
	for _, name := range []string{"then", "catch", "finally"} {
		name := name
		intrinsics["Promise.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitPromiseThen(ex.Callee.(*ast.MemberExpression).Object, name, ex.Args, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				t, _ := e.promiseThenType(ex, ex.Callee.(*ast.MemberExpression))
				return t
			},
		}
	}
	// A Buffer's search and fill: a string value is its bytes (the
	// byte-string emitters); a number, an element as on any typed array.
	for _, name := range []string{"indexOf", "lastIndexOf", "includes", "fill"} {
		name := name
		intrinsics["Buffer.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				mem := ex.Callee.(*ast.MemberExpression)
				if len(ex.Args) >= 1 && isStringTy(e.inferExprType(ex.Args[0])) {
					if name == "fill" {
						return e.emitBufferStringFill(mem, ex.Args, ex.GetPos())
					}
					return e.emitBufferStringSearch(mem, name, ex.Args, ex.GetPos())
				}
				return arrayIntrinsics[name](e, mem, ex.Args, ex.GetPos())
			},
			ty: methodTy,
		}
	}
	for _, name := range []string{"call", "apply", "bind"} {
		name := name
		intrinsics["Function.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				mem := ex.Callee.(*ast.MemberExpression)
				if name == "bind" {
					return e.emitFunctionBind(mem.Object, ex.Args, ex.GetPos())
				}
				return e.emitFunctionCallApply(mem.Object, name, ex.Args, ex.GetPos())
			},
			ty: methodTy,
		}
	}
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
				mem := ex.Callee.(*ast.MemberExpression)
				v, err := emit(e, mem, ex.Args, ex.GetPos())
				if err == nil && returnsReceiver[name] && v.Ty.IsArray {
					// The method returns its receiver: a typed array or a
					// Buffer stays one (a boxed `fill()` result is a view).
					if rt := e.inferExprType(mem.Object); rt.IsTypedArray || rt.IsBuffer {
						v.Ty = rt
					}
				}
				return v, err
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
	intrinsics["JSON.stringify"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitJSONStringify(ex.Args, ex.GetPos())
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			// A dynamic argument stringifies through the dynamic walker,
			// whose result is `any` (string or undefined — TDD-00155 Stage
			// 2); every static path returns string.
			if len(ex.Args) >= 1 && isUnconstrainedDynamic(e.inferExprType(ex.Args[0])) || resolver.StringifyArgsAtRunTime(ex.Args) {
				return TypeAny
			}
			return TypePtr
		},
	}
	intrinsics["JSON.parse"] = intrinsic{
		// Context-free JSON.parse is JS-faithful untyped parse: a dynamic
		// (`any`) tree (TDD-00155 Stage 2). A typed declaration routes
		// through emitDeclJSONProjection instead; an `as T` on the call
		// supplies the target the same way.
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			if ty, ok := e.callAssertedTargetTy(ex); ok {
				return e.emitJSONParse(ex.Args, ty, ex.GetPos())
			}
			return e.emitJSONParse(ex.Args, TypeAny, ex.GetPos())
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			if ty, ok := e.callAssertedTargetTy(ex); ok {
				return ty
			}
			return TypeAny
		},
	}
	for name, ty := range reflectIntrinsics {
		method, ty := name, ty
		intrinsics["Reflect."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitReflectCall(method, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return ty },
		}
	}
	for _, name := range []string{"for", "keyFor"} {
		method := name
		intrinsics["Symbol."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitSymbolStatic(method, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type {
				if method == "for" {
					return SymbolType()
				}
				nt := TypePtr
				nt.Nullable, nt.IsUndefined = true, true
				return nt
			},
		}
	}
	intrinsics["ArrayBuffer.isView"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			if len(ex.Args) != 1 {
				return Value{}, fmt.Errorf("%d:%d: ArrayBuffer.isView takes 1 argument", ex.GetPos().Line, ex.GetPos().Col)
			}
			return e.emitArrayBufferIsView(ex.Args[0])
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeBool },
	}
	for _, name := range []string{"asIntN", "asUintN"} {
		method := name
		intrinsics["BigInt."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitBigIntAsN(method, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return BigIntType() },
		}
	}
	for name, ty := range map[string]Type{
		"from": BufferType(), "of": BufferType(), "concat": BufferType(), "alloc": BufferType(),
		"allocUnsafe": BufferType(), "allocUnsafeSlow": BufferType(),
		"compare": TypeI64, "byteLength": TypeI64, "isBuffer": TypeBool, "isEncoding": TypeBool,
	} {
		method, ty := name, ty
		intrinsics["Buffer."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitBufferStaticCall(method, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return ty },
		}
	}
	for _, name := range []string{"add", "and", "exchange", "or", "sub", "xor", "compareExchange", "load", "store", "isLockFree", "wait", "notify"} {
		method := name
		intrinsics["Atomics."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitAtomicsCall(method, ex.Args, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type { return e.atomicsResultType(method, ex) },
		}
	}
	intrinsics["Date.now"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitDateNow() },
		ty:   func(*Emitter, *ast.CallExpression) Type { return TypeDate },
	}
	intrinsics["Date.parse"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitDateParse(ex.Args, ex.GetPos()) },
		ty:   func(*Emitter, *ast.CallExpression) Type { return TypeF64 }, // NaN when unparseable
	}
	intrinsics["Date.UTC"] = intrinsic{
		// The same fields as new Date(y, m, …), read as UTC, as a time value.
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			v, err := e.emitDateFields(ex.Args)
			if err != nil {
				return Value{}, err
			}
			return Value{Ref: v.Ref, Ty: TypeI64}, nil
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeI64 },
	}
	for name, emit := range map[string]func(e *Emitter, ex *ast.CallExpression) (Value, error){
		"log":   func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitConsolePrint(ex.Args, 1, "") },
		"info":  func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitConsolePrint(ex.Args, 1, "") },
		"debug": func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitConsolePrint(ex.Args, 1, "") },
		"error": func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitConsolePrint(ex.Args, 2, "") },
		// console.warn prints to stderr with no prefix, as console.error.
		"warn": func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitConsolePrint(ex.Args, 2, "") },
		"trace": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsolePrint(ex.Args, 2, "Trace: ")
		},
		"assert": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleAssert(ex.Args, ex.GetPos())
		},
		"dir": func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitConsoleDir(ex.Args, ex.GetPos()) },
		"time": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleTime(ex.Args, ex.GetPos())
		},
		"timeEnd": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleTimeEnd(ex.Args, ex.GetPos())
		},
		"count": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleCount(ex.Args, ex.GetPos())
		},
		"countReset": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleCountReset(ex.Args, ex.GetPos())
		},
		"group": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleGroup(ex.Args, ex.GetPos())
		},
		"groupCollapsed": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleGroup(ex.Args, ex.GetPos())
		},
		"groupEnd": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleGroupEnd(ex.Args, ex.GetPos())
		},
		"table": func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitConsoleTable(ex.Args, ex.GetPos())
		},
	} {
		// Every console method returns void.
		intrinsics["console."+name] = intrinsic{emit: emit, ty: func(*Emitter, *ast.CallExpression) Type { return TypeVoid }}
	}
	timerID := func(*Emitter, *ast.CallExpression) Type { return TypeI64 }
	void := func(*Emitter, *ast.CallExpression) Type { return TypeVoid }
	intrinsics["fetch"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitFetch(ex.Args, ex.GetPos()) },
		ty:   func(*Emitter, *ast.CallExpression) Type { return PromiseOf(ResponseType()) },
	}
	intrinsics["queueMicrotask"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitQueueMicrotask(ex.Args, ex.GetPos())
		},
		ty: void,
	}
	intrinsics["setTimeout"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitSetTimeout(ex.Args, ex.GetPos()) },
		ty:   timerID,
	}
	intrinsics["setInterval"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitSetInterval(ex.Args, ex.GetPos())
		},
		ty: timerID,
	}
	intrinsics["setImmediate"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitSetImmediate(ex.Args, ex.GetPos())
		},
		ty: timerID,
	}
	for _, name := range []string{"clearTimeout", "clearInterval", "clearImmediate"} {
		fn := name
		intrinsics[fn] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitClearTimer(ex.Args, fn, ex.GetPos())
			},
			ty: void,
		}
	}
	// The builtin constructors called as functions: conversions, and a new
	// symbol or bigint.
	intrinsics["String"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitGlobalStringConv(ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypePtr },
	}
	intrinsics["Number"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitGlobalNumberConv(ex.Args, ex.GetPos())
		},
		ty: (*Emitter).numberConvType,
	}
	intrinsics["Boolean"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitGlobalBooleanConv(ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeBool },
	}
	intrinsics["Symbol"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitSymbolConstructor(ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return SymbolType() },
	}
	intrinsics["BigInt"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitBigIntConstructor(ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return BigIntType() },
	}
	intrinsics["process.nextTick"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitProcessNextTick(ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeVoid },
	}
	intrinsics["crypto.getRandomValues"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitCryptoGetRandomValues(ex.Args, ex.GetPos())
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			if len(ex.Args) == 1 {
				return e.inferExprType(ex.Args[0])
			}
			return TypeAny
		},
	}
	intrinsics["crypto.randomUUID"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitCryptoRandomUUID(ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypePtr },
	}
	intrinsics["structuredClone"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitStructuredClone(ex.Args, ex.GetPos())
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			if len(ex.Args) == 1 && !e.clonesAtRunTime(ex.Args[0]) {
				return e.inferExprType(ex.Args[0])
			}
			return TypeAny
		},
	}
	for _, name := range []string{"isArray", "of", "from"} {
		prop := name
		intrinsics["Array."+prop] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) { return e.emitArrayStatic(prop, ex) },
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				t, _ := e.arrayStaticType(prop, ex)
				return t
			},
		}
	}
	intrinsics["ReadableStream.from"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitReadableStreamFrom(ex.Args, ex.GetPos())
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			if len(ex.Args) == 1 {
				if argTy := e.inferExprType(ex.Args[0]); argTy.IsArray {
					return ReadableStreamType(*argTy.ElemType)
				}
			}
			return ReadableStreamType(TypeAny) // rejected by the emitter
		},
	}
	for _, name := range []string{"json", "redirect", "error"} {
		method := name
		intrinsics["Response."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return e.emitResponseStatic(method, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return ResponseType() },
		}
	}
	for _, name := range objectStaticIntrinsics {
		prop := name
		intrinsics["Object."+prop] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				v, err := e.emitObjectStatic(prop, ex)
				if err == errNotObjectStatic {
					pos := ex.GetPos()
					return Value{}, fmt.Errorf("%d:%d: Object.%s does not support this argument's type", pos.Line, pos.Col, prop)
				}
				return v, err
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				if t, ok := e.objectStaticType(prop, ex); ok {
					return t
				}
				return TypeAny
			},
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
		id, ok := owner.(*ast.Identifier)
		if !ok {
			return nil, false
		}
		if target, ok := e.identBuiltinAlias(id); ok {
			id = target // `n.isNaN` after `const n = Number`
		}
		if !intrinsicOwner(id.Name) && id.Name != "globalThis" {
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
	var declFn *ast.FunctionDeclaration
	var callSig *checker.Type
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
		if key == "" {
			// A builtin constructor called as a function (`.map(String)`):
			// its call signature.
			if t := c.TypeOf(x); t != nil && len(t.Calls) > 0 {
				if key = callSignatureIntrinsic(t); key != "" {
					callSig = t.Calls[len(t.Calls)-1]
				}
			}
		}
	case *ast.MemberExpression:
		if !e.isBuiltinGlobalObject(c, x.Object) {
			id, ok := x.Object.(*ast.Identifier)
			if !ok {
				return nil, false
			}
			if _, ok := e.identBuiltinAlias(id); !ok {
				return nil, false
			}
		}
		switch d := c.MemberDecl(x).(type) {
		case *ast.FunctionDeclaration:
			key = d.Intrinsic // `globalThis.parseFloat`
			declFn = d
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
		if strings.HasPrefix(key, "crypto.") {
			// Crypto's methods check their receiver: by value they are the
			// Crypto object's own (lib/node/internal_namespaces.ts).
			if _, ok := e.libExports["internal_crypto_global:_kmlCrypto"]; ok {
				return nil, false
			}
		}
	}
	fn := c.TypeOf(x)
	if callSig != nil {
		fn = callSig
	}
	if fn == nil || fn.Flags&checker.Object == 0 || fn.Kind != checker.Function {
		// A generic, overloaded namespace function the checker gives no one
		// type (`Reflect.get`): an intrinsic takes every parameter as any,
		// so its declaration's parameters are enough.
		if _, known := intrinsics[key]; known && declFn != nil && lower == nil {
			b := &builtinFn{key: key, name: key[strings.LastIndex(key, ".")+1:]}
			for i, p := range declFn.Params {
				if p.Rest && i == len(declFn.Params)-1 {
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
		return nil, false
	}
	if key == "" {
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
		// The widest overload: a rest parameter, else the most parameters
		// (`setTimeout(cb, ms, ...args)` over `setTimeout(cb, ms)`).
		widest := fn.Overloads[len(fn.Overloads)-1]
		for _, o := range fn.Overloads {
			if o.HasRestParam() && !widest.HasRestParam() || o.HasRestParam() == widest.HasRestParam() && len(o.Params) > len(widest.Params) {
				widest = o
			}
		}
		fn = widest
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
		switch {
		case v.Ty.IR == "void":
			e.emitTerminator("ret void")
		case v.Ty.IsArray:
			// An array returns its shared header (`Array.of`).
			hdr, _ := e.arrayArgFromAggregate(v)
			e.emitTerminator(fmt.Sprintf("ret ptr %s", hdr))
		default:
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

// intrinsicOwner reports whether name is a builtin global object whose
// members may be intrinsics or @lower methods referenced by value
// (`[4, 9].map(Math.sqrt)`, `p.then(console.log)`): the syntactic filter
// before the checker decides. The intrinsic keys name their owners; the
// @lower ones are the objects whose methods lower.
func intrinsicOwner(name string) bool {
	intrinsicOwnersOnce.Do(func() {
		intrinsicOwners = map[string]bool{"Math": true, "Number": true, "String": true}
		for key := range intrinsics {
			if i := strings.IndexByte(key, '.'); i > 0 {
				intrinsicOwners[key[:i]] = true
			}
		}
	})
	return intrinsicOwners[name]
}

var (
	intrinsicOwners     map[string]bool
	intrinsicOwnersOnce sync.Once
)

// atomicsResultType is an Atomics call's type: wait's outcome string,
// notify's count, isLockFree's answer; load, store, the read-modify-writes
// and compareExchange return the receiver's element type (a bigint for
// BigInt64Array/BigUint64Array).
func (e *Emitter) atomicsResultType(method string, ex *ast.CallExpression) Type {
	switch method {
	case "wait":
		return TypePtr // "ok" / "not-equal" / "timed-out"
	case "notify":
		return TypeI64
	case "isLockFree":
		return TypeBool
	}
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

// numberConvType is `Number(x)`'s type; it must match emitGlobalNumberConv:
// numeric input passes through, a string parses to a double, everything
// else i64.
func (e *Emitter) numberConvType(ex *ast.CallExpression) Type {
	if len(ex.Args) == 1 {
		argTy := e.inferExprType(ex.Args[0])
		if nl, isNull := ex.Args[0].(*ast.NullLiteral); isNull {
			if nl.IsUndefined {
				return TypeF64 // NaN
			}
			return TypeI64
		}
		if argTy.IR == "i1" {
			return TypeI64
		}
		if argTy.IsInteger() && !argTy.IsDynamic && !isNullableScalar(argTy) && !argTy.IsNull && argTy.IR != "void" {
			return argTy
		}
		// A number, bigint, string, any, null/undefined, nullable
		// scalar, object or array all yield a double (emitUnaryPlus).
		return TypeF64
	}
	return TypeI64
}

// Object.prototype's toString and hasOwnProperty, and BigInt's toString,
// reached through their declarations whatever the receiver.
func init() {
	intrinsics["Object.prototype.toString"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitObjectProtoToString(ex.Callee.(*ast.MemberExpression).Object)
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypePtr },
	}
	intrinsics["Object.prototype.hasOwnProperty"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			if len(ex.Args) != 1 {
				return Value{}, fmt.Errorf("%d:%d: hasOwnProperty takes 1 argument", ex.GetPos().Line, ex.GetPos().Col)
			}
			return e.emitHasOwnProperty(ex.Callee.(*ast.MemberExpression).Object, ex.Args[0], "hasOwnProperty", true, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeBool },
	}
	intrinsics["BigInt.prototype.toString"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			return e.emitBigIntToStringMethod(ex.Callee.(*ast.MemberExpression).Object, ex.Args, ex.GetPos())
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypePtr },
	}
}

// objectProtoToStringApplies reports whether emitObjectProtoToString
// takes a receiver held as ty.
func objectProtoToStringApplies(ty Type) bool {
	switch {
	case isUnconstrainedDynamic(ty), ty.IsArray, ty.IsFunc:
		return false
	case isNumberTy(ty) && ty.IR != "i1":
		return false // Number.prototype.toString(radix) has its own lowering
	case ty.IsSymbol, ty.IsError, ty.IsRegExp, ty.IR == "i1", isPlainStringType(ty.staticIndexType()):
		return true
	}
	return ty.IsObject && !isHostHandle(ty) && !ty.IsDynamicObject
}

// emitObjectProtoToString is `x.toString()` where x has no toString of
// its own: a symbol's description form, an error's `Name: message`, a
// RegExp's source, a number or boolean's String(x), a string itself, and
// an object's "[object Object]".
func (e *Emitter) emitObjectProtoToString(recv ast.Expression) (Value, error) {
	ty := e.inferExprType(recv)
	v, err := e.emitExpr(recv)
	if err != nil {
		return Value{}, err
	}
	switch {
	case ty.IsSymbol:
		return e.emitSymbolToString(v)
	case ty.IsError:
		return e.emitErrorToString(v)
	case isPlainStringType(ty.staticIndexType()):
		return v, nil
	case ty.IsRegExp || isNumberTy(ty):
		return e.emitValueToString(v)
	}
	return Value{Ref: e.internString("[object Object]"), Ty: TypePtr}, nil
}

// TextEncoder, TextDecoder, WeakRef and FinalizationRegistry methods,
// reached through their declarations.
func init() {
	none := func(*Emitter, *ast.CallExpression) Type { return Type{} } // the per-kind inference answers
	byObj := map[string]func(e *Emitter, obj ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error){}
	for key, emit := range byObj {
		emit := emit
		intrinsics[key] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return emit(e, ex.Callee.(*ast.MemberExpression).Object, ex.Args, ex.GetPos())
			},
			ty: none,
		}
	}
	for _, name := range []string{"register", "unregister"} {
		intrinsics["FinalizationRegistry.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				mem := ex.Callee.(*ast.MemberExpression)
				return e.emitFinalizationRegistryMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
			},
			ty: none,
		}
	}
}
