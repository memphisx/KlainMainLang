package llvm

import (
	"KlainMainLang/ast"
)

// intrinsic_types.go — the result types of the intrinsics whose type
// depends on the call (TDD-00230 P3.4): each owner's table, keyed by the
// method its declaration's `@intrinsic` names. ok is false where the call's
// shape has no answer here, leaving the per-kind inference.

type methodTypeFn func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool)

func fixedMethodType(t Type) methodTypeFn {
	return func(*Emitter, *ast.CallExpression, *ast.MemberExpression) (Type, bool) { return t, true }
}

// intrinsicMethodType is the owner table's answer for an intrinsic method
// call.
func (e *Emitter) intrinsicMethodType(table map[string]methodTypeFn, ex *ast.CallExpression) (Type, bool) {
	mem := ex.Callee.(*ast.MemberExpression)
	if f, ok := table[mem.Property]; ok {
		return f(e, ex, mem)
	}
	return Type{}, false
}

// dateMethodType is a Date method's result: a time value or a field (NaN
// for an Invalid Date), the timezone offset, or a string.
func dateMethodType(name string) Type {
	_, get := dateDecomposeFieldIndex[name]
	_, set := dateSetterFieldIndex[name]
	switch {
	case get || set || name == "getTime" || name == "valueOf" || name == "setTime":
		return TypeF64
	case name == "getTimezoneOffset":
		return TypeI64
	}
	return TypePtr
}

// stringMethodTypes are the String intrinsics' results; an index or a
// comparison is a Number (TDD-00123 Stage 3).
var stringMethodTypes = map[string]methodTypeFn{
	"search":        fixedMethodType(TypeF64),
	"localeCompare": fixedMethodType(TypeF64),
	"replace":       fixedMethodType(TypePtr),
	"replaceAll":    fixedMethodType(TypePtr),
	"concat":        fixedMethodType(TypePtr),
	"split":         fixedMethodType(ArrayOf(TypePtr)),
	"match": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		if len(ex.Args) == 1 && matchIsExec(ex.Args[0]) {
			return execArrayType(), true
		}
		t := execArrayType()
		t.ExecMaybePlain = true // a global RegExp's plain array, or exec's
		return t, true
	},
	"matchAll": func(*Emitter, *ast.CallExpression, *ast.MemberExpression) (Type, bool) {
		t := CollIterType(ArrayOf(execArrayType()), mapIterValues)
		t.IterRegExp = true
		return t, true
	},
}

// bodyMethodTypes are Response's and Request's body readers: promises
// (TDD-00186 Part B).
var bodyMethodTypes = map[string]methodTypeFn{
	"text": func(*Emitter, *ast.CallExpression, *ast.MemberExpression) (Type, bool) {
		t := PromiseOf(TypePtr)
		t.PromiseTask = true
		return t, true
	},
	// Promise<any>, as bare JSON.parse is `any`; a declaration context
	// parses into its type (emitDeclJSONProjection), and an `as T` on the
	// call supplies the parse target inside the promise.
	"json": func(e *Emitter, ex *ast.CallExpression, _ *ast.MemberExpression) (Type, bool) {
		inner := TypeAny
		if ty, ok := e.callAssertedTargetTy(ex); ok {
			inner = ty
		}
		t := PromiseOf(inner)
		t.PromiseTask = true
		return t, true
	},
	"arrayBuffer": func(*Emitter, *ast.CallExpression, *ast.MemberExpression) (Type, bool) {
		t := PromiseOf(ArrayBufferType())
		t.PromiseTask = true
		return t, true
	},
}

// bufferMethodTypes are Buffer's own methods; its search and fill share
// the array table.
var bufferMethodTypes = map[string]methodTypeFn{
	"toString":    fixedMethodType(TypePtr),
	"write":       fixedMethodType(TypeI64),
	"copy":        fixedMethodType(TypeI64),
	"compare":     fixedMethodType(TypeI64),
	"equals":      fixedMethodType(TypeBool),
	"indexOf":     fixedMethodType(TypeF64),
	"lastIndexOf": fixedMethodType(TypeF64),
	"includes":    fixedMethodType(TypeBool),
	"fill":        arrayReceiverType,
}

// typedArraySubarrayType is a view with the receiver's own type.
func typedArraySubarrayType(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	if objTy := e.inferExprType(mem.Object); objTy.IsTypedArray() {
		return objTy, true
	}
	return Type{}, false
}

// arrayReceiverType is an array method returning an array of the
// receiver's own kind.
func arrayReceiverType(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	if objTy := e.inferExprType(mem.Object); objTy.IsArray() {
		return objTy, true
	}
	return Type{}, false
}

// arrayMissType is `T | undefined` for a method whose element can be
// absent (an out-of-range index, a predicate miss, an empty array;
// TDD-00187).
func arrayMissType(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	objTy := e.inferExprType(mem.Object)
	if objTy.BigIntElem {
		return undefinedableElem(BigIntType()), true
	}
	if objTy.IsArray() && objTy.ElemType != nil {
		return undefinedableElem(*objTy.ElemType), true
	}
	return undefinedableElem(TypePtr), true
}

// arrayMethodTypes are the array intrinsics' results, for a plain array, a
// typed array and a Buffer. Each mirrors its emitter: an index or a length
// is a Number (TDD-00123 Stage 3).
var arrayMethodTypes = map[string]methodTypeFn{
	"indexOf":       fixedMethodType(TypeF64),
	"lastIndexOf":   fixedMethodType(TypeF64),
	"findIndex":     fixedMethodType(TypeF64),
	"findLastIndex": fixedMethodType(TypeF64),
	"push":          fixedMethodType(TypeF64),
	"unshift":       fixedMethodType(TypeF64),
	"includes":      fixedMethodType(TypeBool),
	"some":          fixedMethodType(TypeBool),
	"every":         fixedMethodType(TypeBool),
	"join":          fixedMethodType(TypePtr),
	"at":            arrayMissType,
	"findLast":      arrayMissType,
	"filter":        arrayReceiverType,
	"splice":        arrayReceiverType,
	"sort":          arrayReceiverType,
	"reverse":       arrayReceiverType,
	"fill":          arrayReceiverType,
	"toReversed":    arrayReceiverType,
	"toSorted":      arrayReceiverType,
	"toSpliced":     arrayReceiverType,
	"with":          arrayReceiverType,
	"copyWithin":    arrayReceiverType,
	"toString": func(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		if ot := e.inferExprType(mem.Object); ot.IsBuffer() || isNumberTy(ot) || isForOfStringTy(ot) && !ot.IsClass() && !ot.IsObject() {
			return TypePtr, true
		}
		return Type{}, false
	},
	"concat": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		objTy := e.inferExprType(mem.Object)
		if isForOfStringTy(objTy) {
			return TypePtr, true // String.prototype.concat — mirrors emitStringConcatMethod
		}
		if objTy.IsArray() && objTy.ElemType != nil {
			return ArrayOf(e.concatElemType(mem.Object, *objTy.ElemType, ex.Args)), true // as emitArrayConcat
		}
		return arrayReceiverType(e, ex, mem)
	},
	"values": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		if objTy := e.inferExprType(mem.Object); arrayIterable(objTy) && len(ex.Args) == 0 {
			return CollIterType(objTy, mapIterValues), true
		}
		return arrayReceiverType(e, ex, mem)
	},
	"keys": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		objTy := e.inferExprType(mem.Object)
		if arrayIterable(objTy) && len(ex.Args) == 0 {
			return CollIterType(objTy, mapIterKeys), true
		}
		if objTy.IsArray() {
			return ArrayOf(TypeI64), true
		}
		return Type{}, false
	},
	"entries": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		objTy := e.inferExprType(mem.Object)
		if arrayIterable(objTy) && len(ex.Args) == 0 {
			return CollIterType(objTy, mapIterEntries), true
		}
		if objTy.IsArray() && objTy.ElemType != nil {
			return ArrayOf(TupleType([]Type{TypeI64, *objTy.ElemType})), true
		}
		return Type{}, false
	},
	// A fresh copy: present whatever the receiver's absence (a call on an
	// absent one throws).
	"slice": func(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		objTy := e.inferExprType(mem.Object)
		if objTy.IsArrayBuffer() || objTy.IsArray() {
			return presentCopy(objTy), true
		}
		return TypePtr, true // string.slice
	},
	"map": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		// A TypedArray's map() returns the receiver's kind (emitArrayMap),
		// whatever its callback returns.
		if recvTy := e.inferExprType(mem.Object); recvTy.IsTypedArray() {
			return recvTy, true
		}
		if len(ex.Args) == 1 {
			// The index parameter is hinted i64, as emitArrayMap hints
			// it: an unannotated `i` would otherwise default to a double
			// and `(_, i) => i * i` infer a float[] the emitter stores as
			// i64 (ADR-00957).
			if retTy, ok := e.callbackReturnType(ex.Args[0], e.hofElemHint(mem.Object), TypeF64); ok {
				return ArrayOf(retTy), true
			}
		}
		return arrayReceiverType(e, ex, mem)
	},
	"find": func(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		if objTy := e.inferExprType(mem.Object); objTy.IsArray() && objTy.ElemType != nil {
			return undefinedableElem(*objTy.ElemType), true
		}
		return Type{}, false
	},
	"reduce":      arrayReduceType,
	"reduceRight": arrayReduceType,
	// emitArrayFlat's unwrap, depth by depth; an invalid depth takes the
	// default (1) here, its error surfacing from the emitter.
	"flat": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		objTy := e.inferExprType(mem.Object)
		if !objTy.IsArray() || objTy.ElemType == nil {
			return Type{}, false
		}
		depth := 1
		if d, err := e.resolveFlatDepth(ex.Args, ex.GetPos()); err == nil {
			depth = d
		}
		elem := *objTy.ElemType
		if flatHasAbsentRow(elem, depth) {
			return ArrayOf(TypeAny), true
		}
		for i := 0; i < depth && elem.IsArray() && elem.ElemType != nil; i++ {
			elem = *elem.ElemType
		}
		if t, ok := e.flatCheckerType(ex, elem); ok {
			return t, true
		}
		return ArrayOf(elem), true
	},
	// map's inference plus one level unwrapped when the callback returns
	// an array (emitArrayFlatMap).
	"flatMap": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
		objTy := e.inferExprType(mem.Object)
		if !objTy.IsArray() || len(ex.Args) != 1 {
			return Type{}, false
		}
		if retTy, ok := e.callbackReturnType(ex.Args[0], e.hofElemHint(mem.Object), TypeF64); ok {
			if flatHasAbsentRow(retTy, 1) {
				return ArrayOf(TypeAny), true
			}
			if retTy.IsArray() && retTy.ElemType != nil {
				return ArrayOf(*retTy.ElemType), true
			}
			return ArrayOf(retTy), true
		}
		return objTy, true
	},
	// The removed element, `T | undefined` for an empty array
	// (emitPop/emitShift).
	"pop":   arrayRemovedType,
	"shift": arrayRemovedType,
}

func arrayRemovedType(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	objTy := e.inferExprType(mem.Object)
	if objTy.IsArray() && objTy.ElemType != nil {
		return undefinedableElem(*objTy.ElemType), true
	}
	return objTy, true
}

// arrayReduceType is an array's own reduce: the seed's type, or with no
// seed an element. An iterator's (Iterator.prototype.reduce) returns its
// declared type.
func arrayReduceType(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	if len(ex.Args) == 2 && e.inferExprType(mem.Object).IsArray() {
		accTy := e.reduceSeedType(ex.Args[1])
		// -compat=js: a callback that can fall off the end makes the
		// accumulator `T | undefined` (emitArrayReduce's widening).
		if e.compatJS() && reduceAccWidenable(accTy) {
			if retTy, ok := e.callbackReturnType(ex.Args[0], accTy, e.hofElemHint(mem.Object)); ok && retTy.Nullable() && retTy.IsUndefined() {
				return undefinedableElem(accTy), true
			}
		}
		return accTy, true
	}
	if len(ex.Args) == 1 {
		if objTy := e.inferExprType(mem.Object); objTy.IsArray() && objTy.ElemType != nil {
			return *objTy.ElemType, true
		}
	}
	return Type{}, false
}

// streamChunkType is the chunk type of the stream, reader or writer a
// method is called on.
func (e *Emitter) streamChunkType(mem *ast.MemberExpression) Type {
	if t := e.inferExprType(mem.Object); t.StreamChunk != nil {
		return *t.StreamChunk
	}
	return TypeI64
}

// streamTaskVoid is a stream method's Promise<void>.
func streamTaskVoid(*Emitter, *ast.CallExpression, *ast.MemberExpression) (Type, bool) {
	pt := PromiseOf(TypeVoid)
	pt.PromiseTask = true
	return pt, true
}

// streamReaderOf is getReader's and values()' result: a reader of the
// stream's chunks.
func streamReaderOf(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
	return StreamReaderType(e.streamChunkType(mem)), true
}

// streamMethodTypes are the results of a readable or writable stream's,
// reader's and writer's methods (emitStreamMethodCall,
// emitWStreamMethodCall).
var streamMethodTypes map[string]methodTypeFn

func init() {
	streamMethodTypes = map[string]methodTypeFn{
		"getReader": streamReaderOf,
		"values":    streamReaderOf,
		"getWriter": func(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
			return WSWriterType(e.streamChunkType(mem)), true
		},
		"read": func(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
			pt := PromiseOf(streamReadResultType(e.streamChunkType(mem)))
			pt.PromiseTask = true
			return pt, true
		},
		"cancel": streamTaskVoid,
		"write":  streamTaskVoid,
		"close":  streamTaskVoid,
		"abort":  streamTaskVoid,
		"pipeTo": streamTaskVoid,
		"pipeThrough": func(e *Emitter, ex *ast.CallExpression, _ *ast.MemberExpression) (Type, bool) {
			if len(ex.Args) >= 1 {
				if tTy := e.inferExprType(ex.Args[0]); tTy.IsTransformStream() && tTy.StreamOut != nil {
					return ReadableStreamType(*tTy.StreamOut), true
				}
			}
			return ReadableStreamType(TypeAny), true
		},
		"tee": func(e *Emitter, _ *ast.CallExpression, mem *ast.MemberExpression) (Type, bool) {
			return ArrayOf(ReadableStreamType(e.streamChunkType(mem))), true
		},
		"releaseLock": func(*Emitter, *ast.CallExpression, *ast.MemberExpression) (Type, bool) {
			return TypeVoid, true
		},
	}
}

// collTypeFn is a Map or Set method's result type, given the call, the
// method's name and the receiver's type.
type collTypeFn func(ex *ast.CallExpression, name string, objTy Type) (Type, bool)

func collBool(*ast.CallExpression, string, Type) (Type, bool) { return TypeBool, true }

func collSelf(_ *ast.CallExpression, _ string, objTy Type) (Type, bool) { return objTy, true }

// collIter is keys(), values() or entries() on an iterable Map or Set: an
// iterator; on a Map that is not, the array mapEagerIter builds.
func collIter(ex *ast.CallExpression, name string, objTy Type) (Type, bool) {
	if kind, _ := collIterMethod(name); mapIterable(objTy) && len(ex.Args) == 0 {
		return CollIterType(objTy, kind), true
	}
	if !objTy.IsMap() {
		return Type{}, false
	}
	return mapEagerIter(name, objTy)
}

// mapEagerIter is a Map's keys(), values() or entries() as an array.
func mapEagerIter(name string, objTy Type) (Type, bool) {
	kind, ok := collIterMethods[name]
	if !ok {
		return Type{}, false
	}
	switch kind {
	case mapIterKeys:
		if objTy.MapKey != nil {
			return ArrayOf(*objTy.MapKey), true
		}
	case mapIterValues:
		if objTy.MapVal != nil {
			return ArrayOf(*objTy.MapVal), true
		}
	case mapIterEntries:
		keyTy, valTy := TypePtr, TypeI64
		if objTy.MapKey != nil {
			keyTy = *objTy.MapKey
		}
		if objTy.MapVal != nil {
			valTy = *objTy.MapVal
		}
		return ArrayOf(TupleType([]Type{keyTy, valTy})), true
	}
	return Type{}, false
}

// mapMethodTypes and setMethodTypes are a Map's and a Set's method results.
var mapMethodTypes = map[string]collTypeFn{
	"get": func(_ *ast.CallExpression, _ string, objTy Type) (Type, bool) {
		if objTy.MapVal == nil {
			return Type{}, false
		}
		v := *objTy.MapVal
		// A miss is `undefined`, as in Node (emitMapGetNullable); an
		// array's is the null header.
		if isNullableScalarMapValue(v) || mapGetUndefinedablePtr(v) || v.IsArray() {
			v.setNullable(true)
			v.setUndefined(true)
		}
		return v, true
	},
	"has":     collBool,
	"delete":  collBool,
	"keys":    collIter,
	"values":  collIter,
	"entries": collIter,
	"set":     collSelf,
}

var setMethodTypes = map[string]collTypeFn{
	"has":     collBool,
	"delete":  collBool,
	"add":     collSelf,
	"keys":    collIter,
	"values":  collIter,
	"entries": collIter,
}

// flatCheckerType is a flat() call's result type when its flattened
// element, elem, is boxed (a union of a value and arrays of it) but the
// checker's FlatArray resolves it to one concrete element type
// (`[[1], [2, [3]]].flat(2)` is number[]): the elements unbox into it.
func (e *Emitter) flatCheckerType(ex *ast.CallExpression, elem Type) (Type, bool) {
	if !elem.IsDynamic() {
		return Type{}, false
	}
	o := e.reprOracle()
	if o == nil {
		return Type{}, false
	}
	t, ok := o.ExprType(ex)
	if !ok || !t.IsArray() || t.ElemType == nil || t.ElemType.IsDynamic() || t.ElemType.IsArray() {
		return Type{}, false
	}
	return t, true
}
