package llvm

import (
	"KlainMainLang/sema"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
)

// lowerLayouts are the native layouts a builtin declaration names with
// `/** @lowerType <layout> */` (TDD-00230 P3.2): the representation every
// value of that interface has.
var lowerLayouts = map[string]func() Type{
	"error":               func() Type { return errorObjType },
	"date":                func() Type { return TypeDate },
	"memoryusage":         memoryUsageType,
	"string":              func() Type { return TypePtr },
	"boxed":               func() Type { return TypeAny }, // several representations: any of them, boxed
	"compressionstream":   func() Type { return compressionStreamType(false) },
	"decompressionstream": func() Type { return compressionStreamType(true) },
	"regexp":              RegExpType,
	"arraybuffer":         ArrayBufferType,
	"sharedarraybuffer":   SharedArrayBufferType,
	"int8array":           func() Type { return TypedArrayType("int8") },
	"uint8array":          func() Type { return TypedArrayType("uint8") },
	"uint8clampedarray":   func() Type { return TypedArrayType("uint8clamped") },
	"int16array":          func() Type { return TypedArrayType("int16") },
	"uint16array":         func() Type { return TypedArrayType("uint16") },
	"int32array":          func() Type { return TypedArrayType("int32") },
	"uint32array":         func() Type { return TypedArrayType("uint32") },
	"float32array":        func() Type { return TypedArrayType("float32") },
	"float64array":        func() Type { return TypedArrayType("float64") },
	"bigint64array":       func() Type { return TypedArrayType("bigint64") },
	"biguint64array":      func() Type { return TypedArrayType("biguint64") },
	"buffer":              BufferType,
	"response":            ResponseType,
	"request":             FetchRequestType,
	"xmlhttprequest":      XMLHttpRequestType,
	// "dynamic" is a builtin whose values are held as `any` (a property
	// descriptor, which the Object and Reflect builtins read and build as a
	// dynamic object).
	"dynamic": func() Type { return TypeAny },
	// An AggregateError is held as the shared Error layout, its trailing
	// `errors` pair read by kind (aggregateErrorStructIR).
	"aggregateerror": func() Type { return errorObjType },
	"domexception":   func() Type { return errorObjType },
	// A tag's strings: the array of the literal's parts.
	"templatestringsarray": func() Type { return ArrayOf(TypePtr) },
	// `arguments`, held as an any[] (synthesizeArgumentsObject).
	"arguments": func() Type { return ArrayOf(TypeAny) },
	// A match array, its header carrying index, input and groups.
	"regexpexecarray":  execArrayType,
	"regexpmatcharray": matchArrayType,
}

// declaredLowerType is the layout sym's declarations name: its own
// `@lowerType`, else the first one a base interface it extends names, as a
// derived layout begins with its base's (`TypeError extends Error`).
func declaredLowerType(sym *binder.Symbol) string {
	return lowerTypeOfSymbol(sym, map[*binder.Symbol]bool{})
}

func lowerTypeOfSymbol(sym *binder.Symbol, seen map[*binder.Symbol]bool) string {
	if sym == nil || seen[sym] {
		return ""
	}
	seen[sym] = true
	for _, d := range sym.Declarations {
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok && id.LowerType != "" {
			return id.LowerType
		}
		if ad, ok := d.Node.(*ast.TypeAliasDeclaration); ok && ad.LowerType != "" {
			return ad.LowerType
		}
	}
	for _, d := range sym.Declarations {
		id, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok {
			continue
		}
		for _, h := range id.Heritage {
			base, ok := h.(*ast.TypeReference)
			if !ok || len(base.Qualifier) > 0 {
				continue
			}
			l := lowerTypeOfSymbol(lookupFrom(sym.Scope, base.Name), seen)
			if l == "" {
				continue
			}
			// A generic layout is built from the type's own arguments, which
			// a derived interface does not give its base: only a layout the
			// arguments do not shape passes through `extends Base<…>`.
			if _, plain := lowerLayouts[l]; plain || len(base.TypeArgs) == 0 {
				return l
			}
		}
	}
	return ""
}

// lookupFrom resolves name from scope outward.
func lookupFrom(sc *binder.Scope, name string) *binder.Symbol {
	for ; sc != nil; sc = sc.Parent {
		if s := sc.Symbols.Get(name); s != nil {
			return s
		}
	}
	return nil
}

// lowerTypeRepr is the representation of a checker type whose declaration
// names a native layout.
func lowerTypeRepr(t *checker.Type) (Type, bool) {
	return lowerTypeReprWith(t, reprOf)
}

// lowerTypeReprWith is lowerTypeRepr with a generic layout's type arguments
// represented by of (a caller's context: its classes).
func lowerTypeReprWith(t *checker.Type, of func(*checker.Type) (Type, bool)) (Type, bool) {
	if t == nil || t.Flags&checker.Object == 0 || t.Symbol == nil ||
		(t.Kind != checker.Interface && t.Kind != checker.Instance) {
		return Type{}, false
	}
	mk, ok := lowerLayouts[declaredLowerType(t.Symbol)]
	if !ok {
		return genericLowerTypeRepr(t, of)
	}
	return mk(), true
}

// resolveTypeName is ResolveTypeName, with a name the program's scope
// resolves to a builtin declaration naming a native layout (`Date`,
// `Uint8Array`, `Response`) given that layout; a program's own type of the
// name shadows the builtin's.
func (e *Emitter) resolveTypeName(name string) Type {
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		return ArrayOf(e.resolveTypeName(name[:len(name)-2]))
	}
	if t, ok := e.declaredLayout(name); ok {
		return t
	}
	return ResolveTypeName(name)
}

// declaredLayout is the native layout of the builtin declaration name
// resolves to, if it names one.
func (e *Emitter) declaredLayout(name string) (Type, bool) {
	mk, ok := lowerLayouts[declaredLowerType(e.lookupBuiltinType(name))]
	if !ok {
		return Type{}, false
	}
	return mk(), true
}

// lookupBuiltinType resolves a type name from the program's scope, where a
// program's own declaration shadows the builtin; before the program is stored
// (EmitProgram's early passes), from the builtin declarations bound alone.
func (e *Emitter) lookupBuiltinType(name string) *binder.Symbol {
	if c := e.front(); c != nil {
		return lookupFrom(c.Binding().Module, name)
	}
	return lookupFrom(libScope(), name)
}

// lookupBuiltinQualified is lookupBuiltinType of a name inside namespaces
// (`NodeJS.Dict`): each qualifier a namespace, the name one of its members.
func (e *Emitter) lookupBuiltinQualified(qualifier []string, name string) *binder.Symbol {
	if len(qualifier) == 0 {
		return e.lookupBuiltinType(name)
	}
	b := libBinding()
	if c := e.front(); c != nil {
		b = c.Binding()
	}
	return lookupQualified(b, qualifier, name)
}

// lookupQualified is the declaration b's module scope binds to a qualified
// name: each qualifier a namespace, the name one of its members.
func lookupQualified(b *binder.Binding, qualifier []string, name string) *binder.Symbol {
	if b == nil {
		return nil
	}
	sc := b.Module
	for i, q := range qualifier {
		var sym *binder.Symbol
		if i == 0 {
			sym = lookupFrom(sc, q)
		} else {
			sym = sc.Symbols.Get(q)
		}
		if sym == nil {
			return nil
		}
		if sc = b.NamespaceScope(sym); sc == nil {
			return nil
		}
	}
	return sc.Symbols.Get(name)
}

var (
	libScopeOnce sync.Once
	libScopeVal  *binder.Scope
	libBindVal   *binder.Binding
)

// libScope is the scope of the builtin declarations bound around an empty
// program.
func libScope() *binder.Scope {
	libScopeOnce.Do(func() {
		progs, err := lib.Programs()
		if err != nil {
			return
		}
		libBindVal = binder.BindWith(&ast.Program{}, binder.Options{Lib: progs})
		libScopeVal = libBindVal.Module
	})
	return libScopeVal
}

// libBinding is libScope's binding.
func libBinding() *binder.Binding {
	libScope()
	return libBindVal
}

// genericLayouts build the layout of a builtin generic declaration's
// instantiation from its type arguments, already in codegen's
// representation; a nil argument is one the type does not give. false leaves
// the type to the caller (a bare `Map` annotation is a generic interface's
// default instantiation in annotationType).
var genericLayouts = map[string]genericLayoutDef{
	"promise": {1, func(args []*Type) (Type, bool) {
		inner := TypeVoid
		if args[0] != nil {
			inner = *args[0]
		}
		pt := PromiseOf(inner)
		// Every promise this compiler produces is task-shaped, async
		// functions and methods alike, except a raw fetch's
		// `Promise<Response>`; so a declared `Promise<T>` is task-shaped and
		// `const p: Promise<T> = f(); await p` reads the right slot.
		if !inner.IsResponse() {
			pt.PromiseTask = true
		}
		return pt, true
	}},
	"map": {2, func(args []*Type) (Type, bool) {
		if args[1] == nil {
			return Type{}, false
		}
		keyTy := TypePtr
		if args[0] != nil {
			keyTy = *args[0]
		}
		return MapType(keyTy, forceAnyMapVal(keyTy, *args[1])), true
	}},
	// @types/node's `NodeJS.Dict<T>` (`{ [key: string]: T | undefined }`):
	// a string-keyed dictionary of T.
	"dict": {1, elemLayout(dictViewType)},
	// `ReadonlyArray<T>` is an array, as `readonly T[]` is.
	"readonlyarray": {1, elemLayout(ArrayOf)},
	"generator": {1, func(args []*Type) (Type, bool) {
		return GeneratorType(argOr(args[0], TypeAny), false), true
	}},
	"asyncgenerator": {1, func(args []*Type) (Type, bool) {
		return GeneratorType(argOr(args[0], TypeAny), true), true
	}},
	"finalizationregistry": {1, func(args []*Type) (Type, bool) {
		if args[0] == nil {
			return Type{}, false
		}
		return FinalizationRegistryType(*args[0]), true
	}},
	"set": {1, func(args []*Type) (Type, bool) {
		if args[0] == nil {
			return Type{}, false
		}
		return SetType(*args[0]), true
	}},
	// WeakMap<K,V> / WeakSet<T> (TDD-00112): a bare form takes object keys
	// and number values.
	"weakmap": {2, func(args []*Type) (Type, bool) {
		return WeakMapType(argOr(args[0], TypePtr), argOr(args[1], TypeI64)), true
	}},
	"weakset": {1, func(args []*Type) (Type, bool) {
		return WeakSetType(argOr(args[0], TypePtr)), true
	}},
	// The WHATWG streams (TDD-00097): a bare stream's chunk is `any`, the
	// declarations' default (`ReadableStream<R = any>`).
	"readablestream": {1, func(args []*Type) (Type, bool) {
		return ReadableStreamType(argOr(args[0], TypeAny)), true
	}},
	"readablestreamdefaultreader": {1, anyElemLayout(StreamReaderType)},
	"readablestreamreadresult": {1, func(args []*Type) (Type, bool) {
		return streamReadResultType(argOr(args[0], TypeAny)), true
	}},
	"readablestreamdefaultcontroller": {1, anyElemLayout(RSControllerType)},
	"writablestream": {1, func(args []*Type) (Type, bool) {
		return WritableStreamType(argOr(args[0], TypeAny)), true
	}},
	"writablestreamdefaultwriter":     {1, anyElemLayout(WSWriterType)},
	"writablestreamdefaultcontroller": {1, anyElemLayout(WSControllerType)},
	"transformstream": {2, func(args []*Type) (Type, bool) {
		i, o := argOr(args[0], TypeAny), argOr(args[1], TypeAny)
		return Type{IR: irPtr, Host: hostTransformStream, StreamChunk: &i, StreamOut: &o}, true
	}},
}

// genericLayoutDef is a generic layout's type-parameter count and builder.
type genericLayoutDef struct {
	arity int
	mk    func(args []*Type) (Type, bool)
}

func argOr(t *Type, def Type) Type {
	if t != nil {
		return *t
	}
	return def
}

// elemLayout is the builder of a layout that needs its one type argument.
func elemLayout(mk func(Type) Type) func([]*Type) (Type, bool) {
	return func(args []*Type) (Type, bool) {
		if args[0] == nil {
			return Type{}, false
		}
		return mk(*args[0]), true
	}
}

// anyElemLayout is elemLayout whose bare form's element is `any`, the
// declaration's default (`ReadableStreamDefaultReader<R = any>`; a
// WritableStreamDefaultController takes no argument at all).
func anyElemLayout(mk func(Type) Type) func([]*Type) (Type, bool) {
	return func(args []*Type) (Type, bool) {
		return mk(argOr(args[0], TypeAny)), true
	}
}

// bareLayout is the layout a generic builtin's declaration names, without
// its type arguments (`x instanceof Map`): each argument a reference, which
// leaves the host class the layout carries.
func (e *Emitter) bareLayout(name string) (Type, bool) {
	def, ok := genericLayouts[declaredLowerType(e.lookupBuiltinType(name))]
	if !ok {
		return Type{}, false
	}
	args := make([]*Type, def.arity)
	for i := range args {
		ptr := TypePtr
		args[i] = &ptr
	}
	return def.mk(args)
}

// genericLowerTypeRepr is reprOf for an instantiation of a builtin generic
// declaration with a `@lowerType` (`Map<string, number[]>`).
func genericLowerTypeRepr(t *checker.Type, of func(*checker.Type) (Type, bool)) (Type, bool) {
	def, ok := genericLayouts[declaredLowerType(t.Symbol)]
	if !ok {
		return Type{}, false
	}
	args := make([]*Type, def.arity)
	for i := range args {
		if i < len(t.TypeArgs) {
			r, ok := of(t.TypeArgs[i])
			if !ok {
				return Type{}, false
			}
			args[i] = &r
		}
	}
	lt, ok := def.mk(args)
	// TReturn as genSlotElem reads it; the checker fills an unwritten one
	// with its default (`any`), which an annotation never names.
	if ok && lt.IsGenerator() && len(t.TypeArgs) > 1 && t.TypeArgs[1].Flags&(checker.Any|checker.Unknown) == 0 {
		if ret, rok := of(t.TypeArgs[1]); rok {
			lt = GeneratorType(genSlotElem(*lt.GeneratorElemType, ret), lt.GeneratorIsAsync)
		}
	}
	return lt, ok
}

// isHostLayoutBase reports whether a class's base name resolves to a builtin
// declaration with a native layout (`Map`, `Date`, `Uint8Array`) or to
// `Array`, which a class cannot extend yet.
func (e *Emitter) isHostLayoutBase(name string) bool {
	sym := e.lookupBuiltinType(name)
	if sym == nil {
		return false
	}
	return declaredLowerType(sym) != "" || name == "Array"
}

// hostBaseOf is the host type a class extends, from its extends clause's
// type arguments (`extends Map<string, number>`), when its base is a builtin
// Map, Set, RegExp, Promise or Date (TDD-00247); nil otherwise.
func (e *Emitter) hostBaseOf(cd *ast.ClassDeclaration) *Type {
	if typedArrayNames[cd.BaseClass] {
		// A typed array: its own array type.
		t := e.annotationType(nameAnnotation(cd.BaseClass))
		if t.IsTypedArray() {
			return &t
		}
	}
	if cd.BaseClass == "ArrayBuffer" || cd.BaseClass == "SharedArrayBuffer" {
		// A buffer: its body the buffer's header words.
		t := ArrayBufferType()
		if cd.BaseClass == "SharedArrayBuffer" {
			t = SharedArrayBufferType()
		}
		return &t
	}
	if cd.BaseClass == "Number" || cd.BaseClass == "String" || cd.BaseClass == "Boolean" {
		// A primitive's wrapper: its body the boxed primitive.
		t := primWrapperType(cd.BaseClass)
		return &t
	}
	if cd.BaseClass == "Array" {
		// Array declares no layout of its own: its values are arrays.
		el := TypeAny
		if len(cd.BaseTypeArgs) == 1 {
			el = e.annotationType(cd.BaseTypeArgs[0])
		}
		t := ArrayOf(el)
		return &t
	}
	layout := declaredLowerType(e.lookupBuiltinType(cd.BaseClass))
	var ta ast.TypeNode
	if n, ok := collectionTypeArity[layout]; ok {
		ta = genericAnnotation(cd.BaseClass, baseArgs(cd, n))
	}
	switch layout {
	case "regexp":
		t := RegExpType()
		return &t
	case "date":
		t := TypeDate
		return &t
	case "promise":
		el := TypeAny
		if len(cd.BaseTypeArgs) == 1 {
			el = e.annotationType(cd.BaseTypeArgs[0])
		}
		t := PromiseOf(el)
		t.PromiseTask = true
		return &t
	}
	if ta == nil {
		return nil
	}
	t := e.annotationType(ta)
	return &t
}

// hostBodyPrefix names the hidden fields that hold a host base's body
// inside a class instance, right after its tag word.
const hostBodyPrefix = "__kml_hb"

// hostBodySize is a host base's body in words: a Map's or Set's
// (collections_engine.h), or a RegExp's struct.
func hostBodySize(host Type) int {
	switch {
	case host.IsRegExp():
		return int(host.StructSize() / 8)
	case host.IsPromise():
		return promiseStructSize / 8
	case host.IsDate():
		return 1 // the time value
	case host.IsPrimWrapper():
		return 2 // the boxed primitive, the wrapper's (unused) bag
	case host.IsArrayBuffer():
		return 3 // byteLength, data, maxByteLength (-1 when fixed)
	case host.IsArray():
		return 3 // the array header's {data, len, holes}
	case host.Weak:
		return 1 // the weak table's cell list (weak.c)
	}
	return 9
}

// hostBodyFields are the hidden words of a host base's body.
func hostBodyFields(host Type) []Field {
	fs := make([]Field, hostBodySize(host))
	for i := range fs {
		fs[i] = Field{Name: fmt.Sprintf("%s%d", hostBodyPrefix, i), Ty: TypeI64}
	}
	return fs
}

// hostBodyFieldsPrefix is how many leading fields of inherited are a host
// base's body, 0 for any other class.
func hostBodyFieldsPrefix(inherited []Field) int {
	n := 0
	for n < len(inherited) && inherited[n].Name == fmt.Sprintf("%s%d", hostBodyPrefix, n) {
		n++
	}
	return n
}

// emitHostBody is the host body of a class instance extending a host (at
// instance + 8): a Map or Set body made empty; a RegExp's is built by its
// construction (emitHostBodySeed).
func (e *Emitter) emitHostBody(instance string, host Type) string {
	body := e.freshReg()
	e.emitBodyAddr(body, instance)
	if host.IsPrimWrapper() {
		// The primitive the constructor gives with no argument, until
		// super() sets it.
		if err := e.emitHostPrimBody(body, host, nil, ast.Pos{}); err == nil {
			return body
		}
	}
	if host.IsRegExp() || host.IsDate() || host.IsArray() || host.Weak || host.IsPrimWrapper() || host.IsArrayBuffer() {
		return body // (a weak table's empty cell list is the zeroed word)
	}
	if host.IsPromise() {
		e.ensureExceptionHelpers()
		e.ensureMicrotasks()
		e.ensureUnhandledRejections()
		e.declareFn("__kml_promise_init_at", "declare void @__kml_promise_init_at("+irPtr+")")
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_init_at("+irPtr+" %s)", body))
		return body
	}
	e.declareFn("__kml_map_init_at", "declare void @__kml_map_init_at("+irPtr+")")
	e.emitInstr(fmt.Sprintf("call void @__kml_map_init_at("+irPtr+" %s)", body))
	return body
}

// emitHostArrayBody sets the array body at body from the construction's
// arguments, as `new Array(…)` does: one number is a length, anything else
// the elements; a typed array's as its own constructor takes them.
func (e *Emitter) emitHostArrayBody(body string, host Type, args []ast.Expression, pos ast.Pos) error {
	var v Value
	var err error
	if host.IsTypedArray() {
		// A typed array's own construction: a length, an array-like or a
		// buffer.
		if len(args) == 0 {
			args = []ast.Expression{ast.NewNumberLiteral("0", pos)} // empty
		}
		n, _, lerr := sema.BuiltinNew(ast.NewNewExpression(typedArrayConstructorName(host), args, pos))
		if lerr != nil {
			return lerr
		}
		if nta, ok := n.(*ast.NewTypedArrayExpression); ok {
			// Built in the body's own fields: a view over a resizable
			// buffer keeps that length word up to date.
			lp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 1", lp, arrayHeaderTy, body))
			return e.emitNewTypedArrayVarDecl(nta, body, lp, *host.ElemType)
		}
		v, err = e.emitExpr(n)
	} else if len(args) == 1 && !isSpread(args[0]) && isNumericScalar(e.typeOfExpr(args[0])) {
		v, err = e.emitNewArraySizedAggregate(ast.NewNewArrayExpression(nil, args[0], pos), *host.ElemType)
	} else {
		v, err = e.emitExprWithObjectHint(ast.NewArrayLiteral(args, pos), host)
	}
	if err != nil {
		return err
	}
	v = e.convert(v, host)
	e.storeArrayPartsInto(v, body)
	return nil
}

// emitHostRegExpBody compiles a RegExp into the host body at body from the
// construction's arguments, as `new RegExp(pattern, flags)` does.
func (e *Emitter) emitHostRegExpBody(body string, args []ast.Expression, pos ast.Pos) error {
	if hasSpreadArgs(args) {
		// `super(...args)`: the pattern and flags are the spread array's
		// first two elements (an absent one undefined, as omitted).
		arr, err := e.emitExpr(ast.NewArrayLiteral(args, pos))
		if err != nil {
			return err
		}
		ref := e.bindValue(arr, pos)
		at := func(i int) ast.Expression {
			return ast.NewIndexExpression(ref, ast.NewNumberLiteral(fmt.Sprint(i), pos), pos)
		}
		args = []ast.Expression{at(0), at(1)}
	}
	if len(args) > 2 {
		return internalAt(pos, "RegExp with more than two arguments (the call is trimmed to the declared arity)")
	}
	var pattern, flags ast.Expression = ast.NewNullLiteral(true, pos), nil
	if len(args) > 0 {
		pattern = args[0]
	}
	if len(args) > 1 {
		flags = args[1]
	}
	_, err := e.emitNewRegExpInto(ast.NewNewRegExpExpression(pattern, flags, pos), body)
	return err
}

// emitHostBodySeed adds a Map's entries or a Set's elements, from an array
// expression, to the host body at body.
func (e *Emitter) emitHostBodySeed(body string, host Type, src ast.Expression, pos ast.Pos) error {
	suffix, _ := mapRuntime(*host.MapKey)
	e.ensureMapFamily(suffix)
	if id, ok := src.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsArray() {
			// An absent optional array (its null header) seeds nothing, as
			// `new Map(undefined)` makes an empty Map.
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", hdr, sym.Ptr))
			seedL, doneL := e.freshLabel("hostseed"), e.freshLabel("hostseed.done")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(hdr), doneL, seedL))
			e.emitLabel(seedL)
			if err := e.emitHostBodySeedFrom(body, host, src, pos); err != nil {
				return err
			}
			if !e.blockDone {
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			}
			e.emitLabel(doneL)
			return nil
		}
	}
	return e.emitHostBodySeedFrom(body, host, src, pos)
}

// emitHostBodySeedFrom is emitHostBodySeed's seeding from a present source.
func (e *Emitter) emitHostBodySeedFrom(body string, host Type, src ast.Expression, pos ast.Pos) error {
	if host.IsMap() {
		return e.emitMapSeedFromEntries(body, src, *host.MapKey, *host.MapVal, pos)
	}
	if isStringTy(e.typeOfExpr(src)) {
		// A string iterates its code points.
		src = ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: src}}, src.GetPos())
	}
	// Another element type (a source held as a box): emitSetSeed converts
	// each element to the Set's.
	ptr, n, elemTy, err := e.resolveArrayForHOF(src, pos)
	if err != nil {
		return err
	}
	return e.emitSetSeed(body, ptr, n, elemTy, *host.MapKey)
}

// bindHostBody binds the host body of a class instance extending Map or Set
// (instance + 8) to a fresh name typed as the host, for a host member read
// or call on it.
func (e *Emitter) bindHostBody(instance Value, host Type, pos ast.Pos) *ast.Identifier {
	body := e.freshReg()
	e.emitBodyAddr(body, instance.Ref)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", body, slot))
	name := fmt.Sprintf("__kml_hostbody_%s", slot[1:])
	e.define(name, Symbol{Ptr: slot, Ty: host})
	return ast.NewIdentifier(name, pos)
}

// bindHostPrimitive names the primitive an instance of a class extending
// Number, String or Boolean wraps (its body's word, boxed).
func (e *Emitter) bindHostPrimitive(instance Value, pos ast.Pos) *ast.Identifier {
	body := e.freshReg()
	e.emitBodyAddr(body, instance.Ref)
	name := fmt.Sprintf("__kml_hostprim_%s", body[1:])
	e.define(name, Symbol{Ptr: body, Ty: TypeAny})
	return ast.NewIdentifier(name, pos)
}

// emitHostBufferBody sets the body of an instance of a class extending
// ArrayBuffer (SharedArrayBuffer) from its construction's arguments: a new
// buffer's header words.
func (e *Emitter) emitHostBufferBody(body string, host Type, args []ast.Expression, pos ast.Pos) error {
	name := "ArrayBuffer"
	if host.Host == hostSharedArrayBuffer {
		name = "SharedArrayBuffer"
	}
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNumberLiteral("0", pos)}
	}
	buf, err := e.emitExpr(builtinNew(ast.NewNewExpression(name, args, pos)))
	if err != nil {
		return err
	}
	if buf.Ty.IsDynamic() {
		buf = e.convert(buf, host)
	}
	for i := 0; i < 3; i++ { // length, data, maximum
		src, dst, w := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %d", src, buf.Ref, i))
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", w, src))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %d", dst, body, i))
		e.heapStore("i64", w, dst, 8)
	}
	return nil
}

// builtinNew is ne lowered to its builtin construction node, ne itself
// when it names no builtin (or one with arguments it rejects).
func builtinNew(ne *ast.NewExpression) ast.Expression {
	if out, ok, err := sema.BuiltinNew(ne); ok && err == nil {
		return out
	}
	return ne
}

// emitHostPrimBody sets the primitive of an instance of a class extending
// Number, String or Boolean, as the constructor called as a function gives
// it (`Number(…args)`).
func (e *Emitter) emitHostPrimBody(body string, host Type, args []ast.Expression, pos ast.Pos) error {
	call := ast.NewCallExpression(ast.NewIdentifier(host.HostTag, pos), args, pos)
	call.Intrinsic = host.HostTag
	prim, err := e.emitExpr(call)
	if err != nil {
		return err
	}
	word, err := e.emitBoxValue(prim)
	if err != nil {
		return err
	}
	e.heapStore("i64", word.Ref, body, 8)
	return nil
}

// hostArrayReceiver is obj's array body when obj is an instance of a class
// extending Array: obj is evaluated once and its body bound to a name, so
// an element or array property reads and writes the instance's own array.
func (e *Emitter) hostArrayReceiver(obj ast.Expression) (ast.Expression, bool, error) {
	t := e.typeOfExpr(obj)
	if !t.IsClass() || t.IsDynamic() || t.Nullable() {
		return nil, false, nil
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || !info.HostBase.IsArray() {
		return nil, false, nil
	}
	v, err := e.emitExpr(obj)
	if err != nil {
		return nil, false, err
	}
	return e.bindHostBody(v, *info.HostBase, obj.GetPos()), true, nil
}

// emitInspectHostClass renders an instance of a class extending Map or Set
// as Node does: `Counter(2) [Map] { 'a' => 6, x: 1 }`, the entries of its
// body, then its own fields.
func (e *Emitter) emitInspectHostClass(v Value, info ClassInfo, depth int) (Value, error) {
	host := *info.HostBase
	if host.IsArrayBuffer() {
		body := e.freshReg()
		e.emitBodyAddr(body, v.Ref)
		cls := ast.Unmangle(info.Ty.ClassName)
		return e.emitInspectArrayBufferAs(Value{Ref: body, Ty: host}, depth, cls+" ["+hostTagName(host)+"]", cls, func(list string) error {
			for _, f := range info.Ty.VisibleFields() {
				idx, fty, _ := info.Ty.FieldIndex(f.Name)
				fs, err := e.emitInspectField(e.loadFieldValue(v, idx, fty), depth+1)
				if err != nil {
					return err
				}
				entry, err := e.emitStringConcat(Value{Ref: e.internString(inspectKey(f.Name) + ": "), Ty: TypePtr}, fs)
				if err != nil {
					return err
				}
				e.inspectPush(list, entry)
			}
			return nil
		})
	}
	if host.IsRegExp() || host.IsDate() || host.IsPrimWrapper() {
		return e.emitInspectRegExpClass(v, info, depth)
	}
	if host.IsPromise() {
		return e.emitInspectPromiseClass(v, info, depth)
	}
	if host.IsArray() {
		if depth > e.effectiveInspectDepth() {
			return Value{Ref: e.internString("[" + ast.Unmangle(info.Ty.ClassName) + "]"), Ty: TypePtr}, nil
		}
		fields := info.Ty.VisibleFields()
		tag := ""
		if host.IsTypedArray() {
			tag = "[" + typedArrayConstructorName(host) + "] " // `Bytes(3) [Uint8Array] [ … ]`
		}
		label := collLabel{class: ast.Unmangle(info.Ty.ClassName), tag: tag, push: func(list string) error {
			return e.inspectPushFields(list, v, info, fields, depth)
		}}
		return e.emitInspectArrayAs(e.convert(v, host), depth, label)
	}
	if depth > e.effectiveInspectDepth() {
		// Past util.inspect's depth: `[Counter [Map]]`.
		return Value{Ref: e.internString("[" + ast.Unmangle(info.Ty.ClassName) + " [" + hostTagName(host) + "]]"), Ty: TypePtr}, nil
	}
	if host.Weak {
		return e.emitInspectWeakClass(v, info, depth)
	}
	body := e.freshReg()
	e.emitBodyAddr(body, v.Ref)
	bv := Value{Ref: body, Ty: host}
	fields := info.Ty.VisibleFields()
	label := collLabel{class: ast.Unmangle(info.Ty.ClassName), nextra: len(fields)}
	if host.IsMap() {
		label.tag = "[Map] "
	} else {
		label.tag = "[Set] "
	}
	label.push = func(list string) error {
		return e.inspectPushFields(list, v, info, fields, depth)
	}
	if host.IsMap() {
		return e.emitInspectMapAs(bv, depth, label)
	}
	return e.emitInspectSetAs(bv, depth, label)
}

// emitInspectWeakClass renders an instance of a class extending WeakMap or
// WeakSet as Node does: `C [WeakMap] { <items unknown>, x: 1 }`.
func (e *Emitter) emitInspectWeakClass(v Value, info ClassInfo, depth int) (Value, error) {
	out := Value{Ref: e.internString(ast.Unmangle(info.Ty.ClassName) + " [" + hostTagName(*info.HostBase) + "] { <items unknown>"), Ty: TypePtr}
	for _, f := range info.Ty.VisibleFields() {
		idx, fty, _ := info.Ty.FieldIndex(f.Name)
		fs, err := e.emitInspectField(e.loadFieldValue(v, idx, fty), depth+1)
		if err != nil {
			return Value{}, err
		}
		if out, err = e.emitStringConcat(out, Value{Ref: e.internString(", " + inspectKey(f.Name) + ": "), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		if out, err = e.emitStringConcat(out, fs); err != nil {
			return Value{}, err
		}
	}
	return e.emitStringConcat(out, Value{Ref: e.internString(" }"), Ty: TypePtr})
}

// inspectPushFields pushes `name: value` for each of a host class
// instance's own fields.
func (e *Emitter) inspectPushFields(list string, v Value, info ClassInfo, fields []Field, depth int) error {
	for _, f := range fields {
		idx, fty, _ := info.Ty.FieldIndex(f.Name)
		fs, err := e.emitInspectField(e.loadFieldValue(v, idx, fty), depth+1)
		if err != nil {
			return err
		}
		entry, err := e.emitStringConcat(Value{Ref: e.internString(inspectKey(f.Name) + ": "), Ty: TypePtr}, fs)
		if err != nil {
			return err
		}
		e.inspectPush(list, entry)
	}
	return nil
}

// inspectKey is a property name as util.inspect writes it: bare when it is
// an identifier, else single-quoted.
func inspectKey(name string) string {
	if isIdentifierName(name) {
		return name
	}
	return "'" + name + "'"
}

// hostEntriesAnnotation is the type of the entries a class extending Map or
// Set is constructed from: `[K, V][]` for a Map, `T[]` for a Set.
func hostEntriesAnnotation(cd *ast.ClassDeclaration) ast.TypeNode {
	var el ast.TypeNode
	if len(cd.BaseTypeArgs) == 2 {
		args := baseArgs(cd, 2)
		el = &ast.TupleType{Elements: []ast.TypeNode{args[0], args[1]}}
	} else {
		el = baseArgs(cd, 1)[0]
	}
	if el != nil {
		if ta, err := ast.TypeAnnotationOf(&ast.ArrayType{ElementType: el}, "ts"); err == nil {
			return ta
		}
	}
	return ast.AnyArrayAnnotation("ts")
}

// hostAnnotation is a class's Map or Set base as an annotation, its type
// arguments those of the extends clause (`any` where it has none).
func hostAnnotation(cd *ast.ClassDeclaration, host Type) ast.TypeNode {
	switch {
	case host.IsRegExp(), host.IsDate():
		return nameAnnotation(cd.BaseClass)
	case host.IsMap():
		return genericAnnotation(cd.BaseClass, baseArgs(cd, 2))
	}
	return genericAnnotation(cd.BaseClass, baseArgs(cd, 1))
}

// baseArgs is a class's first n extends-clause type arguments, `any` for
// each it does not give.
func baseArgs(cd *ast.ClassDeclaration, n int) []ast.TypeNode {
	out := make([]ast.TypeNode, n)
	for i := range out {
		if i < len(cd.BaseTypeArgs) && cd.BaseTypeArgs[i] != nil {
			out[i] = cd.BaseTypeArgs[i]
		} else {
			out[i] = ast.AnyAnnotation("ts")
		}
	}
	return out
}

// genericAnnotation is `name<args…>` over a type node.
func genericAnnotation(name string, args []ast.TypeNode) ast.TypeNode {
	ref := &ast.TypeReference{Name: name}
	for _, a := range args {
		ref.TypeArgs = append(ref.TypeArgs, a)
	}
	if ta, err := ast.TypeAnnotationOf(ref, "ts"); err == nil {
		return ta
	}
	return nameAnnotation(name)
}

// hostView is the iteration of expr when it is an instance of a class
// extending Map or Set without an iterator of its own: its host's default
// iterator, `expr.entries()` for a Map, `expr.values()` for a Set; nil
// otherwise.
func (e *Emitter) hostView(expr ast.Expression) ast.Expression {
	t := e.typeOfExpr(expr)
	if !t.IsClass() {
		return nil
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || info.HostBase.IsRegExp() || info.HostBase.IsPromise() || info.HostBase.IsDate() {
		return nil
	}
	if _, own := info.MethodSigs["@@iterator"]; own {
		return nil
	}
	if info.HostBase.IsPrimWrapper() {
		if info.HostBase.HostTag != "String" {
			return nil
		}
		// A String object iterates its string.
		call := ast.NewCallExpression(ast.NewMemberExpression(expr, "valueOf", expr.GetPos()), nil, expr.GetPos())
		call.Intrinsic = "String.prototype.valueOf"
		return call
	}
	method := "values"
	if info.HostBase.IsMap() {
		method = "entries"
	}
	return ast.NewCallExpression(ast.NewMemberExpression(expr, method, expr.GetPos()), nil, expr.GetPos())
}

// emitHostBaseCall calls the host method of a class instance extending Map
// or Set on its body. Map.prototype.set and Set.prototype.add return their
// receiver: the instance.
func (e *Emitter) emitHostBaseCall(instance Value, host Type, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if host.IsPromise() && (method == "then" || method == "catch" || method == "finally") {
		// The body settles to the instance's own value type, which a
		// species construction may have made other than the class's.
		host = PromiseOf(e.promiseSubclassValueType(instance.Ty))
		host.PromiseTask = true
		return e.emitPromiseSpecies(instance, host, method, args, pos)
	}
	if host.IsTypedArray() {
		if typedArraySpeciesMethods[method] {
			return e.emitArraySpecies(instance, host, method, args, pos)
		}
	} else if host.IsArray() {
		if _, ok := arraySpeciesMethods[method]; ok {
			return e.emitArraySpecies(instance, host, method, args, pos)
		}
	}
	if host.IsArrayBuffer() && method == "slice" && instance.Ty.IsClass() {
		// SpeciesConstructor: the class constructed with the slice's
		// length, its bytes copied in.
		recv := e.bindHostBody(instance, host, pos)
		part, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(recv, "slice", pos), args, pos))
		if err != nil {
			return Value{}, err
		}
		pv := e.bindValue(part, pos)
		out, err := e.emitExpr(ast.NewNewExpression(instance.Ty.ClassName, []ast.Expression{ast.NewMemberExpression(pv, "byteLength", pos)}, pos))
		if err != nil {
			return Value{}, err
		}
		ov := e.bindValue(out, pos)
		view := func(x ast.Expression) ast.Expression {
			return builtinNew(ast.NewNewExpression("Uint8Array", []ast.Expression{x}, pos))
		}
		if _, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(view(ov), "set", pos), []ast.Expression{view(pv)}, pos)); err != nil {
			return Value{}, err
		}
		return out, nil
	}
	var recv *ast.Identifier
	if host.IsPrimWrapper() {
		recv = e.bindHostPrimitive(instance, pos)
	} else {
		recv = e.bindHostBody(instance, host, pos)
	}
	call := ast.NewCallExpression(ast.NewMemberExpression(recv, method, pos), args, pos)
	// The bound body has no declaration to resolve its method by.
	if host.IsDate() {
		if _, ok := intrinsics["Date.prototype."+method]; ok {
			call.Intrinsic = "Date.prototype." + method
		}
	} else if host.IsMap() || host.IsSet() {
		if c := collMethodCall(recv, host, method, args, pos); intrinsics[c.Intrinsic].emit != nil {
			call = c
		}
	}
	v, err := e.emitCall(call)
	if err != nil {
		return Value{}, err
	}
	if (method == "set" || method == "add") && headeredHost(v.Ty) {
		return instance, nil
	}
	return v, nil
}

// emitHostClassInspectThunk is the body of a class extending Map or Set's
// inspect thunk (`ptr (ptr %o, i64 %depth)`): its rendering at each nesting
// depth, chosen by %depth.
func (e *Emitter) emitHostClassInspectThunk(info ClassInfo) {
	const depths = 4
	labels := make([]string, depths)
	for d := range labels {
		labels[d] = e.freshLabel("hostcls.d")
	}
	// Any deeper depth renders as the deepest one, past util.inspect's.
	cases := make([]string, depths-1)
	for d := 0; d < depths-1; d++ {
		cases[d] = fmt.Sprintf("i64 %d, label %%%s", d, labels[d])
	}
	e.emitTerminator(fmt.Sprintf("switch i64 %%depth, label %%%s [ %s ]", labels[depths-1], strings.Join(cases, " ")))
	for d := 0; d < depths; d++ {
		e.emitLabel(labels[d])
		if v, err := e.emitInspectHostClass(Value{Ref: "%o", Ty: info.Ty}, info, d); err == nil && !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret "+irPtr+" %s", v.Ref))
		}
		if !e.blockDone {
			e.emitTerminator("ret " + irPtr + " null")
		}
	}
}

// hostTagName is the builtin tag of a host base (`[object RegExp]`).
func hostTagName(host Type) string {
	switch {
	case host.IsRegExp():
		return "RegExp"
	case host.IsDate():
		return "Date"
	case host.IsPrimWrapper():
		return host.HostTag
	case host.IsArrayBuffer() && host.Host == hostSharedArrayBuffer:
		return "SharedArrayBuffer"
	case host.IsArrayBuffer():
		return "ArrayBuffer"
	case host.IsPromise():
		return "Promise"
	case host.Weak && host.IsMap():
		return "WeakMap"
	case host.Weak:
		return "WeakSet"
	case host.IsMap():
		return "Map"
	}
	return "Set"
}

// emitInspectRegExpClass renders an instance of a class extending RegExp or
// Date as Node does: `R /a+b/g { tag: 'x' }`, `D 1970-01-01T00:00:00.000Z`,
// its host value then its own fields.
func (e *Emitter) emitInspectRegExpClass(v Value, info ClassInfo, depth int) (Value, error) {
	body := e.freshReg()
	e.emitBodyAddr(body, v.Ref)
	var out Value
	if host := *info.HostBase; host.IsPrimWrapper() {
		// `[Number (N): 3]`, its primitive's rendering inside.
		inner, err := e.emitInspectField(e.emitWrapperValue(body), depth)
		if err != nil {
			return Value{}, err
		}
		if out, err = e.emitStringConcat(Value{Ref: e.internString("[" + host.HostTag + " (" + ast.Unmangle(info.Ty.ClassName) + "): "), Ty: TypePtr}, inner); err != nil {
			return Value{}, err
		}
		if out, err = e.emitStringConcat(out, Value{Ref: e.internString("]"), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
	} else {
		re, err := e.emitInspectField(Value{Ref: body, Ty: host}, depth)
		if err != nil {
			return Value{}, err
		}
		if out, err = e.emitStringConcat(Value{Ref: e.internString(ast.Unmangle(info.Ty.ClassName) + " "), Ty: TypePtr}, re); err != nil {
			return Value{}, err
		}
	}
	fields := info.Ty.VisibleFields()
	if len(fields) == 0 {
		return out, nil
	}
	if depth > e.effectiveInspectDepth() {
		return e.emitStringConcat(out, Value{Ref: e.internString(" [Object]"), Ty: TypePtr})
	}
	sep := " { "
	for _, f := range fields {
		idx, fty, _ := info.Ty.FieldIndex(f.Name)
		fs, err := e.emitInspectField(e.loadFieldValue(v, idx, fty), depth+1)
		if err != nil {
			return Value{}, err
		}
		if out, err = e.emitStringConcat(out, Value{Ref: e.internString(sep + inspectKey(f.Name) + ": "), Ty: TypePtr}); err != nil {
			return Value{}, err
		}
		if out, err = e.emitStringConcat(out, fs); err != nil {
			return Value{}, err
		}
		sep = ", "
	}
	return e.emitStringConcat(out, Value{Ref: e.internString(" }"), Ty: TypePtr})
}

// hostRegExpArgs is args with a first argument that is an instance of a
// class extending RegExp evaluated and replaced by its body, typed as the
// RegExp it is (`s.replace(r, "-")`); args otherwise.
func (e *Emitter) hostRegExpArgs(args []ast.Expression) ([]ast.Expression, error) {
	if len(args) == 0 {
		return args, nil
	}
	t := e.typeOfExpr(args[0])
	if !t.IsClass() {
		return args, nil
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || !info.HostBase.IsRegExp() {
		return args, nil
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return nil, err
	}
	out := append([]ast.Expression{e.bindHostBody(v, *info.HostBase, args[0].GetPos())}, args[1:]...)
	return out, nil
}

// checkerHostSubclass is the class extending a host a reference holding the
// host value (ty, a Map, Set or RegExp) is narrowed to by the checker
// (`if (!(m instanceof Counter)) return; m.bump()`).
func (e *Emitter) checkerHostSubclass(id *ast.Identifier, ty Type) (ClassInfo, bool) {
	if e.dynEntryWidenedIdent(id) {
		return ClassInfo{}, false // a dynamic entry's box stays one
	}
	if !hostBodyAt8(ty) || ty.Nullable() {
		return ClassInfo{}, false
	}
	c := e.front()
	if c == nil {
		return ClassInfo{}, false
	}
	t := c.TypeOf(id)
	if t == nil || c.Unanswered(t) || t.Symbol == nil {
		return ClassInfo{}, false
	}
	for _, d := range t.Symbol.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			if info, ok := e.classes[cd.Name]; ok && info.HostBase != nil {
				return info, true
			}
		}
	}
	return ClassInfo{}, false
}

// emitHostPromiseBody runs a Promise executor (the construction's one
// argument) on the pending body of an instance of a class extending Promise.
func (e *Emitter) emitHostPromiseBody(body string, host Type, args []ast.Expression, pos ast.Pos) error {
	if len(args) == 0 {
		// V8's TypeError: an absent executor is undefined.
		e.emitThrowTypeError("Promise resolver undefined is not a function")
		return nil
	}
	for _, a := range args[1:] {
		// Arguments past the executor are evaluated and ignored.
		if _, err := e.emitExpr(a); err != nil {
			return err
		}
	}
	el := TypeAny
	if host.PromiseType != nil {
		el = *host.PromiseType
	}
	e.emitBodyFlag(body, promiseFlagSubclass)
	if host.PromiseType == nil || isUnconstrainedDynamic(*host.PromiseType) {
		// An erased host (`extends Promise<T>`) holds its value boxed even
		// where a binding reads it as a typed Promise (loadPromiseValue).
		e.emitBodyFlag(body, promiseFlagBoxedValue)
	}
	_, err := e.emitPromiseExecutor(body, el, args[0], pos)
	return err
}

// emitBodyFlag sets flag in the flags slot of the promise body at body.
func (e *Emitter) emitBodyFlag(body string, flag int) {
	fp, old, nw := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %d", fp, body, promiseFlagsSlot*8))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", old, fp))
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", nw, old, flag))
	e.heapStore("i64", nw, fp, 8)
}

// emitBodyFlagClear clears flag in the flags slot of the promise body at body.
func (e *Emitter) emitBodyFlagClear(body string, flag int) {
	fp, old, nw := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %d", fp, body, promiseFlagsSlot*8))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", old, fp))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", nw, old, ^flag))
	e.heapStore("i64", nw, fp, 8)
}

// anyPromiseClass reports a class extending Promise in the program.
func (e *Emitter) anyPromiseClass() bool {
	for name := range e.classes {
		if e.isPromiseClass(name) {
			return true
		}
	}
	return false
}

// anyErasedPromiseHost reports a class extending an erased Promise<T>: only
// then can a typed promise read meet a boxed value.
func (e *Emitter) anyErasedPromiseHost() bool {
	for name := range e.classes {
		if e.promiseHostErased(name) {
			return true
		}
	}
	return false
}

// promiseExecutorAnnotation is the type of the executor a class extending
// Promise<T> is constructed with: `(resolve: (value: T) => void, reject:
// (reason?: any) => void) => void`.
func promiseExecutorAnnotation(cd *ast.ClassDeclaration) ast.TypeNode {
	var el ast.TypeNode = &ast.KeywordType{Keyword: "any"}
	if len(cd.BaseTypeArgs) == 1 && cd.BaseTypeArgs[0] != nil {
		el = cd.BaseTypeArgs[0]
	}
	void := &ast.KeywordType{Keyword: "void"}
	resolve := &ast.FunctionType{Parameters: []*ast.SignatureParameter{{Name: "value", Type: el}}, Type: void}
	reject := &ast.FunctionType{Parameters: []*ast.SignatureParameter{{Name: "reason", Optional: true, Type: &ast.KeywordType{Keyword: "any"}}}, Type: void}
	exec := &ast.FunctionType{Parameters: []*ast.SignatureParameter{{Name: "resolve", Type: resolve}, {Name: "reject", Type: reject}}, Type: void}
	if ta, err := ast.TypeAnnotationOf(exec, "ts"); err == nil {
		return ta
	}
	return ast.AnyAnnotation("ts")
}

// emitPromiseCapabilityThen is emitPromiseSpecies's `then` for a class whose
// constructor does not hand its executor straight to Promise's: the result
// is made as the spec's NewPromiseCapability(C) makes it — `new C(executor)`,
// the executor capturing the resolving functions C's constructor hands it —
// and settled by calling them from the reaction job, as PromiseReactionJob
// does, so a constructor wrapping them sees each call.
func (e *Emitter) emitPromiseCapabilityThen(instance Value, host Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	undef := Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
	res, rej := e.bindValue(undef, pos), e.bindValue(undef, pos)
	id := func(n string) *ast.Identifier { return ast.NewIdentifier(n, pos) }
	stmt := func(x ast.Expression) ast.Statement { return ast.NewExpressionStatement(x, pos) }
	call := func(f ast.Expression, a ...ast.Expression) ast.Expression { return ast.NewCallExpression(f, a, pos) }
	exec := ast.NewArrowFunction(
		[]ast.Param{{Name: "__kml_res", Type: ast.AnyAnnotation("ts")}, {Name: "__kml_rej", Type: ast.AnyAnnotation("ts")}},
		nil, nil, ast.NewBlockStatement([]ast.Statement{
			stmt(ast.NewAssignmentExpression("=", res, id("__kml_res"), pos)),
			stmt(ast.NewAssignmentExpression("=", rej, id("__kml_rej"), pos)),
		}, pos), pos)
	derived, err := e.emitSpeciesNew(ast.NewNewExpression(instance.Ty.ClassName, []ast.Expression{exec}, pos))
	if err != nil {
		return Value{}, err
	}
	onF, onR := ast.Expression(nil), ast.Expression(nil)
	if len(args) > 0 && !e.isNonCallableThenArg(args[0]) {
		onF = args[0]
	}
	if len(args) > 1 && !e.isNonCallableThenArg(args[1]) {
		onR = args[1]
	}
	el := TypeAny
	if host.PromiseType != nil {
		el = *host.PromiseType
	}
	// handler wraps a callback as the reaction job runs it: its result
	// resolves the capability, its throw rejects it; an absent one passes
	// the value (or reason) through.
	handler := func(cb ast.Expression, param string, pass ast.Expression, bound func(ast.Expression) (ast.Expression, error)) (ast.Expression, error) {
		var body []ast.Statement
		if cb == nil {
			body = []ast.Statement{stmt(call(pass, id(param)))}
		} else {
			f, err := bound(cb)
			if err != nil {
				return nil, err
			}
			settle := stmt(call(res, call(f, id(param))))
			if rt, ok := e.callbackReturnType(cb, el); ok && (rt.IsPromise() || e.isThenableType(rt)) {
				// A returned promise resolves the capability through its
				// `then`, from a job (NewPromiseResolveThenableJob).
				r := ast.NewVarDeclaration("const", "__kml_r", nil, call(f, id(param)), pos)
				job := ast.NewArrowFunction(nil, nil, call(ast.NewMemberExpression(synthCall(thenableResolveName, pos, id("__kml_r")), "then", pos), res, rej), nil, pos)
				enqueue := ast.NewCallExpression(id("queueMicrotask"), []ast.Expression{job}, pos)
				enqueue.Intrinsic = "queueMicrotask" // a job queued, whatever the program names queueMicrotask
				settle = ast.NewBlockStatement([]ast.Statement{r, stmt(enqueue)}, pos)
			}
			try := ast.NewTryStatement(
				ast.NewBlockStatement([]ast.Statement{settle}, pos),
				&ast.CatchClause{Param: "__kml_err", Body: ast.NewBlockStatement([]ast.Statement{stmt(call(rej, id("__kml_err")))}, pos), Pos: pos},
				nil, pos)
			body = []ast.Statement{try}
		}
		return ast.NewArrowFunction([]ast.Param{{Name: param}}, nil, nil, ast.NewBlockStatement(body, pos), pos), nil
	}
	fulfilled, err := handler(onF, "__kml_v", res, func(cb ast.Expression) (ast.Expression, error) {
		if e.typeOfExpr(cb).IsDynamic() {
			v, err := e.emitExpr(cb) // a function held in `any`: called dynamically
			if err != nil {
				return nil, err
			}
			return e.bindValue(v, pos), nil
		}
		v, err := e.emitFulfillCallback(cb, el)
		if err != nil {
			return nil, err
		}
		return e.bindValue(v, pos), nil
	})
	if err != nil {
		return Value{}, err
	}
	rejected, err := handler(onR, "__kml_e", rej, func(cb ast.Expression) (ast.Expression, error) {
		if e.typeOfExpr(cb).IsDynamic() {
			v, err := e.emitExpr(cb)
			if err != nil {
				return nil, err
			}
			return e.bindValue(v, pos), nil
		}
		v, err := e.emitRejectCallback(cb)
		if err != nil {
			return nil, err
		}
		return e.bindValue(v, pos), nil
	})
	if err != nil {
		return Value{}, err
	}
	wrap := &ast.Program{Body: []ast.Statement{stmt(fulfilled), stmt(rejected)}}
	if err := sema.Prepare(wrap); err != nil {
		return Value{}, err
	}
	fulfilled = wrap.Body[0].(*ast.ExpressionStatement).Expr
	rejected = wrap.Body[1].(*ast.ExpressionStatement).Expr
	recv := e.bindHostBody(instance, host, pos)
	q, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(recv, "then", pos), []ast.Expression{fulfilled, rejected}, pos))
	if err != nil {
		return Value{}, err
	}
	e.emitMarkPromiseHandled(q.Ref) // the internal reaction's own promise
	// The result's value type: the callback's result, as tsc types it.
	rt := el
	if onF != nil {
		if t, ok := e.callbackReturnType(onF, el); ok {
			rt = e.awaitedRet(t)
			if rt.IsPromise() {
				rt = TypeAny
				if t.PromiseType != nil {
					rt = *t.PromiseType
				}
			}
		}
	} else if onR != nil {
		if t, ok := e.callbackReturnType(onR, TypeCaught); ok && !thenPathsDiffer(el, t) {
			rt = t
		}
	}
	pt := PromiseOf(rt)
	pt.PromiseTask = true
	rty := e.promiseDerivedOf(instance.Ty, pt)
	e.emitMarkBoxedPromiseValue(derived.Ref, rty)
	return Value{Ref: derived.Ref, Ty: rty}, nil
}

// emitInspectPromiseClass renders an instance of a class extending Promise as
// Node does: `P [Promise] { 1, tag: 'p' }`, its state or value, then its own
// fields.
func (e *Emitter) emitInspectPromiseClass(v Value, info ClassInfo, depth int) (Value, error) {
	if own := e.promiseSubclassValueType(info.Ty); v.Ty.PromiseType == nil && !isUnconstrainedDynamic(own) && depth <= e.effectiveInspectDepth() {
		// No carried type (a box's read): the flag says whether the body
		// holds a boxed value or the class's own.
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
		boxedL, ownL, doneL := e.freshLabel("pinsp.boxed"), e.freshLabel("pinsp.own"), e.freshLabel("pinsp.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitBoxedPromiseValueTest(v.Ref), boxedL, ownL))
		for _, br := range []struct {
			label string
			ty    Type
		}{{boxedL, promiseSubclassOf(v.Ty, PromiseOf(TypeAny))}, {ownL, v.Ty}} {
			e.emitLabel(br.label)
			vv := v
			vv.Ty = br.ty
			if br.label == ownL {
				vv.Ty.PromiseType = &own
			}
			s, err := e.emitInspectPromiseClass(vv, info, depth)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", s.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	name := ast.Unmangle(info.Ty.ClassName)
	if depth > e.effectiveInspectDepth() {
		return Value{Ref: e.internString("[" + name + " [Promise]]"), Ty: TypePtr}, nil
	}
	body := e.freshReg()
	e.emitBodyAddr(body, v.Ref)
	bodyTy := PromiseOf(e.promiseSubclassValueType(v.Ty))
	bodyTy.PromiseTask = true
	boxed, err := e.emitBoxValue(Value{Ref: body, Ty: bodyTy})
	if err != nil {
		return Value{}, err
	}
	e.ensureDynJSONC()
	e.declareFn("__kml_promise_inspect_entry", "declare "+irPtr+" @__kml_promise_inspect_entry("+irPtr+", i64)")
	_, pay := e.emitUnboxTagPayload(boxed)
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, w, pay))
	entry := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_promise_inspect_entry("+irPtr+" %s, i64 %d)", entry, w, depth))
	list := e.inspectBegin(depth, "1")
	e.inspectPush(list, Value{Ref: entry, Ty: TypePtr})
	for _, f := range info.Ty.VisibleFields() {
		idx, fty, _ := info.Ty.FieldIndex(f.Name)
		fs, err := e.emitInspectField(e.loadFieldValue(v, idx, fty), depth+1)
		if err != nil {
			return Value{}, err
		}
		item, err := e.emitStringConcat(Value{Ref: e.internString(inspectKey(f.Name) + ": "), Ty: TypePtr}, fs)
		if err != nil {
			return Value{}, err
		}
		e.inspectPush(list, item)
	}
	return e.inspectEnd(list, e.internString(name+" [Promise] {"), e.internString("}"), depth, false, false), nil
}

// promiseSpeciesPassThrough reports a class (extending Promise) whose
// constructors, up to Promise, each hand their first parameter unchanged
// to super: a species construction's resolving functions are then the
// promise's own, so a reaction settles the derived promise directly.
func (e *Emitter) promiseSpeciesPassThrough(className string) bool {
	for name := className; ; {
		info, ok := e.classes[name]
		if !ok {
			return true
		}
		if c := info.Constructor; c != nil && !ctorPassesExecutor(c) {
			return false
		}
		name = info.BaseClass
	}
}

// ctorPassesExecutor reports a constructor calling super with its first
// parameter, which it never reassigns.
func ctorPassesExecutor(c *ast.FunctionDeclaration) bool {
	if len(c.Params) == 0 || c.Body == nil {
		return false
	}
	p := c.Params[0].Name
	passes, assigned := false, false
	ast.Inspect(c.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpression:
			if _, ok := n.Callee.(*ast.SuperExpression); ok && len(n.Args) > 0 {
				if id, ok := n.Args[0].(*ast.Identifier); ok && id.Name == p {
					passes = true
				}
			}
		case *ast.AssignmentExpression:
			if id, ok := n.Left.(*ast.Identifier); ok && id.Name == p {
				assigned = true
			}
		}
		return true
	})
	return passes && !assigned
}

// emitPromiseSpecies is `p.then(onF, onR)`, `p.catch(onR)` (`p.then(undefined,
// onR)`) or `p.finally(f)` on an instance of a class C extending Promise: the
// result is a C (SpeciesConstructor), made by C's constructor.
func (e *Emitter) emitPromiseSpecies(instance Value, host Type, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if method == "finally" {
		return e.emitPromiseSpeciesFinally(instance, host, args, pos)
	}
	if method == "catch" {
		// Promise.prototype.catch: `this.then(undefined, onRejected)`.
		method = "then"
		onR := ast.Expression(ast.NewNullLiteral(true, pos))
		if len(args) > 0 {
			onR = args[0]
		}
		args = []ast.Expression{ast.NewNullLiteral(true, pos), onR}
	}
	// The class's [Symbol.species] picks the constructor: another class
	// extending Promise, or Promise itself (a plain then).
	ctor, err := e.speciesOf(instance.Ty.ClassName, pos)
	if err != nil {
		return Value{}, err
	}
	if ctor == "" {
		// Promise's own method on the body: a plain promise.
		return e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(e.bindHostBody(instance, host, pos), method, pos), args, pos))
	}
	if ctor != instance.Ty.ClassName {
		// Another class's instance, settled from this one's body (the host
		// body's offset is the same in every class extending Promise).
		instance.Ty = e.classes[ctor].Ty
	}
	if !e.promiseSpeciesPassThrough(instance.Ty.ClassName) {
		return e.emitPromiseCapabilityThen(instance, host, args, pos)
	}
	// `new C(executor)` runs C's constructors; their executor is Promise's,
	// so the reaction settles the derived promise as a plain then's does.
	noop := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement(nil, pos), pos)
	derived, err := e.emitSpeciesNew(ast.NewNewExpression(instance.Ty.ClassName, []ast.Expression{noop}, pos))
	if err != nil {
		return Value{}, err
	}
	body := e.freshReg()
	e.emitBodyAddr(body, derived.Ref)
	thenArgs := args
	// A callback held in `any` is called dynamically from a typed handler,
	// its result the reaction's `any` value.
	el := TypeAny
	if host.PromiseType != nil {
		el = *host.PromiseType
	}
	thenArgs = append([]ast.Expression(nil), thenArgs...)
	erased := e.promiseHostErased(instance.Ty.ClassName)
	for i, a := range thenArgs {
		if i > 1 || e.isNonCallableThenArg(a) {
			continue
		}
		if erased && !e.typeOfExpr(a).IsDynamic() {
			// The body holds every value boxed (promiseDerivedOf): the
			// callback's result, as `any`.
			fv, err := e.emitExpr(a)
			if err != nil {
				return Value{}, err
			}
			f := e.bindValue(fv, pos)
			pt := el
			if i == 1 {
				pt = TypeCaught
			}
			thenArgs[i] = ast.NewArrowFunction([]ast.Param{{Name: "__kml_a", Type: e.synthTypeAnnotation(pt)}}, nil,
				ast.NewAsExpression(ast.NewCallExpression(f, []ast.Expression{ast.NewIdentifier("__kml_a", pos)}, pos), ast.AnyAnnotation(""), pos), nil, pos)
			continue
		}
		if !e.typeOfExpr(a).IsDynamic() {
			continue
		}
		fv, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		f := e.bindValue(fv, pos)
		pt := el
		if i == 1 {
			pt = TypeCaught
		}
		thenArgs[i] = ast.NewArrowFunction([]ast.Param{{Name: "__kml_a", Type: e.synthTypeAnnotation(pt)}}, nil,
			ast.NewCallExpression(f, []ast.Expression{ast.NewIdentifier("__kml_a", pos)}, pos), nil, pos)
	}
	recv := e.bindHostBody(instance, host, pos)
	q, err := e.emitPromiseThenInto(recv, "then", thenArgs, pos, body)
	if err != nil {
		return Value{}, err
	}
	rty := e.promiseDerivedOf(instance.Ty, q.Ty)
	e.emitMarkBoxedPromiseValue(derived.Ref, rty)
	return Value{Ref: derived.Ref, Ty: rty}, nil
}

// promiseSubclassOf is class type c (extending Promise) for an instance
// settling as promise type p does: its value type carried as PromiseType,
// which may differ from the extends clause's (`p.then(v => String(v))` is a
// C settling to a string).
func promiseSubclassOf(c, p Type) Type {
	c.PromiseType = nil
	// A rejection's `never` stores no value: the class's own type stands.
	if p.PromiseType != nil && !p.PromiseType.IsNever() {
		t := *p.PromiseType
		c.PromiseType = &t
	}
	return c
}

// promiseDerivedOf is promiseSubclassOf for an instance the program derives
// (a species construction, a static through the class): a class whose
// extends clause erases its value type (`class P<T> extends Promise<T>`)
// holds every value boxed, so its instances carry `any`.
func (e *Emitter) promiseDerivedOf(c, p Type) Type {
	if e.promiseHostErased(c.ClassName) {
		return promiseSubclassOf(c, PromiseOf(TypeAny))
	}
	return promiseSubclassOf(c, p)
}

// isPromiseClass reports a class extending Promise.
func (e *Emitter) isPromiseClass(className string) bool {
	info, ok := e.classes[className]
	return ok && info.HostBase != nil && info.HostBase.IsPromise()
}

// promiseHostErased reports a class extending Promise whose extends clause
// carries no value type of its own (`extends Promise<T>`, T erased).
func (e *Emitter) promiseHostErased(className string) bool {
	info, ok := e.classes[className]
	if !ok || info.HostBase == nil || !info.HostBase.IsPromise() {
		return false
	}
	return info.HostBase.PromiseType == nil || isUnconstrainedDynamic(*info.HostBase.PromiseType)
}

// promiseSubclassValueType is what an instance of class type c (extending
// Promise) settles to: its carried type, else its extends clause's.
func (e *Emitter) promiseSubclassValueType(c Type) Type {
	if c.PromiseType != nil {
		return *c.PromiseType
	}
	if info, ok := e.classes[c.ClassName]; ok && info.HostBase != nil && info.HostBase.PromiseType != nil {
		return *info.HostBase.PromiseType
	}
	return TypeAny
}

// emitPromiseSpeciesFinally is `p.finally(f)` as the spec has it:
// `p.then(v => C.resolve(f()).then(() => v), r => C.resolve(f()).then(() =>
// { throw r }))`, C the class; a non-callable f passes both through.
func (e *Emitter) emitPromiseSpeciesFinally(instance Value, host Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	// C is the species constructor: Promise's own finally when
	// [Symbol.species] is Promise.
	ctor, err := e.speciesOf(instance.Ty.ClassName, pos)
	if err != nil {
		return Value{}, err
	}
	if ctor == "" {
		// Promise's own finally on the body: a plain promise.
		return e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(e.bindHostBody(instance, host, pos), "finally", pos), args, pos))
	}
	if len(args) == 0 || e.isNonCallableThenArg(args[0]) {
		var f ast.Expression = ast.NewNullLiteral(true, pos)
		if len(args) > 0 {
			f = args[0]
		}
		return e.emitPromiseSpecies(instance, host, "then", []ast.Expression{f, f}, pos)
	}
	fv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	f := e.bindValue(fv, pos)
	id := func(n string) *ast.Identifier { return ast.NewIdentifier(n, pos) }
	call := func(c ast.Expression, a ...ast.Expression) ast.Expression { return ast.NewCallExpression(c, a, pos) }
	cls := id(ctor)
	// C.resolve(f()), with f's result when it has one.
	resolved := func() ast.Expression {
		if rt := e.typeOfExpr(call(f)); rt.IR == "void" || rt.IR == "" {
			return ast.NewSequenceExpression([]ast.Expression{call(f), call(ast.NewMemberExpression(cls, "resolve", pos))}, pos)
		}
		return call(ast.NewMemberExpression(cls, "resolve", pos), call(f))
	}
	el := e.promiseSubclassValueType(instance.Ty)
	thenFinally := ast.NewArrowFunction([]ast.Param{{Name: "__kml_fv", Type: e.synthTypeAnnotation(el)}}, nil,
		synthCall(thenConstName, pos, resolved(), id("__kml_fv"), ast.NewBooleanLiteral(false, pos)), nil, pos)
	catchFinally := ast.NewArrowFunction([]ast.Param{{Name: "__kml_fr", Type: e.synthTypeAnnotation(TypeCaught)}}, nil,
		synthCall(thenConstName, pos, resolved(), id("__kml_fr"), ast.NewBooleanLiteral(true, pos)), nil, pos)
	wrap := &ast.Program{Body: []ast.Statement{ast.NewExpressionStatement(thenFinally, pos), ast.NewExpressionStatement(catchFinally, pos)}}
	if err := sema.Prepare(wrap); err != nil {
		return Value{}, err
	}
	return e.emitPromiseSpecies(instance, host, "then", []ast.Expression{
		wrap.Body[0].(*ast.ExpressionStatement).Expr, wrap.Body[1].(*ast.ExpressionStatement).Expr,
	}, pos)
}

// promiseSubclassStaticType is the type of `C.m(args)` for a Promise static
// m reached through a class C extending Promise: Promise.m's.
func (e *Emitter) promiseSubclassStaticType(m string, ex *ast.CallExpression) (Type, bool) {
	if t, ok := e.promiseStaticType(m, ex); ok {
		return t, true
	}
	// A combinator over a value no array type describes (an any, a missing
	// iterable): its members are boxes, iterated at run time.
	if _, ok := promiseCombinators[m]; ok {
		return taskPromise(e.combinatorResultType(m, TypeAny)), true
	}
	return Type{}, false
}

// emitPromiseCapability is NewPromiseCapability(C) for a class C extending
// Promise whose result settles to valTy: the C (its body pending) and its
// resolving functions, bound to names. A pass-through class's are typed
// functions over its body; another's are those its constructor handed the
// executor, held in `any`.
func (e *Emitter) emitPromiseCapability(className string, valTy Type, pos ast.Pos) (inst string, res, rej ast.Expression, err error) {
	id := func(n string) *ast.Identifier { return ast.NewIdentifier(n, pos) }
	stmt := func(x ast.Expression) ast.Statement { return ast.NewExpressionStatement(x, pos) }
	capture := func(r, j ast.Expression, ty ast.TypeNode) *ast.ArrowFunction {
		return ast.NewArrowFunction(
			[]ast.Param{{Name: "__kml_res", Type: ty}, {Name: "__kml_rej", Type: ty}},
			nil, nil, ast.NewBlockStatement([]ast.Statement{
				stmt(ast.NewAssignmentExpression("=", r, id("__kml_res"), pos)),
				stmt(ast.NewAssignmentExpression("=", j, id("__kml_rej"), pos)),
			}, pos), pos)
	}
	if !e.promiseSpeciesPassThrough(className) {
		undef := Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
		res, rej = e.bindValue(undef, pos), e.bindValue(undef, pos)
		derived, err := e.emitSpeciesNew(ast.NewNewExpression(className, []ast.Expression{capture(res, rej, ast.AnyAnnotation("ts"))}, pos))
		if err != nil {
			return "", nil, nil, err
		}
		return derived.Ref, res, rej, nil
	}
	noop := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement(nil, pos), pos)
	derived, err := e.emitSpeciesNew(ast.NewNewExpression(className, []ast.Expression{noop}, pos))
	if err != nil {
		return "", nil, nil, err
	}
	body := e.freshReg()
	e.emitBodyAddr(body, derived.Ref)
	if !isUnconstrainedDynamic(valTy) {
		// The executor below settles the body with valTy's own value, not a
		// box: an erased host's construction flagged it boxed.
		e.emitBodyFlagClear(body, promiseFlagBoxedValue)
	}
	// The body's own resolving functions, typed by the value it settles to.
	var resolveParams []Type
	if valTy.IR != "void" && valTy.IR != "" {
		resolveParams = []Type{valTy}
	}
	resolveTy := FuncType(resolveParams, TypeVoid)
	if len(resolveParams) == 1 {
		resolveTy.setKind(kindPromiseResolver, true)
	}
	rejectTy := FuncType([]Type{TypeCaught}, TypeVoid)
	res = e.bindValue(Value{Ref: "null", Ty: resolveTy}, pos)
	rej = e.bindValue(Value{Ref: "null", Ty: rejectTy}, pos)
	if _, err := e.emitPromiseExecutor(body, valTy, capture(res, rej, nil), pos); err != nil {
		return "", nil, nil, err
	}
	return derived.Ref, res, rej, nil
}

// emitPromiseSubclassStatic is a Promise static reached through a class C
// extending Promise (`C.resolve(x)`): Promise's algorithm with C as the
// constructor, so the results are Cs.
func (e *Emitter) emitPromiseSubclassStatic(className, m string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	ty, ok := e.promiseSubclassStaticType(m, ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Promise", pos), m, pos), args, pos))
	if !ok {
		return Value{}, false, nil
	}
	el := TypeVoid
	if ty.PromiseType != nil {
		el = *ty.PromiseType
	}
	cty := e.promiseDerivedOf(e.classes[className].Ty, ty)
	if cty.PromiseType != nil && isUnconstrainedDynamic(*cty.PromiseType) {
		// The C settles to what its type reads: a box.
		el = TypeAny
	}
	if settle, ok := promiseSubclassSettlers[m]; ok {
		return e.emitPromiseSubclassSettle(className, cty, el, args, settle, pos)
	}
	return e.emitPromiseSubclassCombinator(className, m, cty, el, args, pos)
}

// promiseSubclassSettlers are the statics that settle a fresh C through one
// of its resolving functions: `C.resolve(x)` (true) and `C.reject(r)`.
var promiseSubclassSettlers = map[string]bool{"resolve": true, "reject": false}

// emitPromiseSubclassSettle is `C.resolve(x)` (resolve) or `C.reject(r)`.
func (e *Emitter) emitPromiseSubclassSettle(className string, cty, el Type, args []ast.Expression, resolve bool, pos ast.Pos) (Value, bool, error) {
	if resolve && len(args) > 0 {
		if at := e.typeOfExpr(args[0]); at.IsClass() && at.ClassName == className {
			// PromiseResolve(C, x) is x when its constructor is C.
			v, err := e.emitExpr(args[0])
			return v, true, err
		}
	}
	inst, res, rej, err := e.emitPromiseCapability(className, el, pos)
	if err != nil {
		return Value{}, true, err
	}
	f := rej
	if resolve {
		f = res
	}
	if _, err := e.emitExpr(ast.NewCallExpression(f, args, pos)); err != nil {
		return Value{}, true, err
	}
	e.emitMarkBoxedPromiseValue(inst, cty)
	return Value{Ref: inst, Ty: cty}, true, nil
}

// promiseCombinator is how a combinator's member settles its result: race
// passes the first settlement through; the others collect each member's
// record (onF/onR build it from the value; nil passes the settlement
// through), and any collects rejections (swapped: the list is rejected).
type promiseCombinator struct {
	race, swapped bool
	onF, onR      func(v ast.Expression, pos ast.Pos) ast.Expression
}

var promiseCombinators map[string]promiseCombinator

func init() {
	same := func(v ast.Expression, _ ast.Pos) ast.Expression { return v }
	settled := func(status, key string) func(ast.Expression, ast.Pos) ast.Expression {
		return func(v ast.Expression, pos ast.Pos) ast.Expression {
			return ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "status", Value: ast.NewStringLiteral(status, pos)}, {Key: key, Value: v}}, pos)
		}
	}
	promiseCombinators = map[string]promiseCombinator{
		"race":       {race: true},
		"all":        {onF: same},
		"allSettled": {onF: settled("fulfilled", "value"), onR: settled("rejected", "reason")},
		"any":        {swapped: true, onR: same},
	}
}

// emitPromiseSubclassCombinator is `C.all`/`C.race`/`C.allSettled`/`C.any`
// for a class C extending Promise, as the spec's PerformPromiseAll (and
// its siblings) runs with C as the constructor: each member through
// `C.resolve(x).then(…)`, the result a C settled through its capability.
func (e *Emitter) emitPromiseSubclassCombinator(className, m string, cty, el Type, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	// A missing iterable is undefined (its iteration rejects the result);
	// arguments past it are evaluated and ignored.
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNullLiteral(true, pos)}
	}
	for _, a := range args[1:] {
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, true, err
		}
	}
	inst, res, rej, err := e.emitPromiseCapability(className, el, pos)
	if err != nil {
		return Value{}, true, err
	}
	id := func(n string) *ast.Identifier { return ast.NewIdentifier(n, pos) }
	num := func(n string) ast.Expression { return ast.NewNumberLiteral(n, pos) }
	stmt := func(x ast.Expression) ast.Statement { return ast.NewExpressionStatement(x, pos) }
	call := func(f ast.Expression, a ...ast.Expression) ast.Expression { return ast.NewCallExpression(f, a, pos) }
	mem := func(o ast.Expression, p string) ast.Expression { return ast.NewMemberExpression(o, p, pos) }
	assign := func(l, r ast.Expression) ast.Statement { return stmt(ast.NewAssignmentExpression("=", l, r, pos)) }
	block := func(ss ...ast.Statement) *ast.BlockStatement { return ast.NewBlockStatement(ss, pos) }
	anyT := ast.AnyAnnotation("ts")
	dec := func(n string) ast.Statement { return assign(id(n), ast.NewBinaryExpression("-", id(n), num("1"), pos)) }
	isZero := func(n string) ast.Expression { return ast.NewBinaryExpression("===", id(n), num("0"), pos) }
	// `C.resolve(x).then(onF, onR)` (emitResolveThenCall).
	resolveThen := func(onF, onR ast.Expression) ast.Expression {
		return synthCall(resolveThenName, pos, id(className), id("__kml_x"), onF, onR)
	}
	spec, ok := promiseCombinators[m]
	if !ok {
		return Value{}, false, nil
	}
	var loop []ast.Statement
	var pre, post []ast.Statement
	if spec.race {
		// The settled value reaches the typed resolver through a call,
		// which converts a member's box to the value type.
		onF := ast.NewArrowFunction([]ast.Param{{Name: "__kml_v", Type: anyT}}, nil, call(res, id("__kml_v")), nil, pos)
		loop = []ast.Statement{stmt(resolveThen(onF, rej))}
	} else {
		list, done := res, rej
		if spec.swapped {
			list, done = rej, res
		}
		// The values array and each element as the result types them
		// (`number[]` for C.all over numbers); `any` for a tuple or a
		// rejection list.
		valsAnnot, elemAnnot := nameAnnotation("any[]"), anyT
		if !spec.swapped && el.IsArray() && el.ElemType != nil && !el.IsTuple() && !el.ElemType.IsDynamic() {
			valsAnnot, elemAnnot = e.synthTypeAnnotation(el), e.synthTypeAnnotation(*el.ElemType)
		}
		pre = []ast.Statement{
			ast.NewVarDeclaration("const", "__kml_vals", valsAnnot, ast.NewArrayLiteral(nil, pos), pos),
			ast.NewVarDeclaration("let", "__kml_rem", nil, num("1"), pos),
			ast.NewVarDeclaration("let", "__kml_i", nil, num("0"), pos),
		}
		finish := func() ast.Statement {
			var out ast.Expression = id("__kml_vals")
			if spec.swapped {
				agg := ast.NewNewErrorExpression("AggregateError", ast.NewStringLiteral("All promises were rejected", pos), pos)
				agg.Errors = id("__kml_vals")
				out = agg
			}
			return ast.NewIfStatement(isZero("__kml_rem"), block(stmt(call(list, out))), nil, pos)
		}
		// A record built as its element type has it (allSettled's, the
		// absent one of value/reason absent) is declared first.
		record := spec.onF != nil && spec.onR != nil
		elem := func(build func(ast.Expression, ast.Pos) ast.Expression) *ast.ArrowFunction {
			pt := elemAnnot
			v := build(id("__kml_v"), pos)
			var stmts []ast.Statement
			if record {
				pt = anyT
				stmts = append(stmts, ast.NewVarDeclaration("const", "__kml_o", elemAnnot, v, pos))
				v = id("__kml_o")
			}
			stmts = append(stmts, assign(ast.NewIndexExpression(id("__kml_vals"), id("__kml_k"), pos), v), dec("__kml_rem"), finish())
			return ast.NewArrowFunction([]ast.Param{{Name: "__kml_v", Type: pt}}, nil, nil, block(stmts...), pos)
		}
		var onF, onR ast.Expression = done, done
		if spec.onF != nil {
			onF = elem(spec.onF)
		}
		if spec.onR != nil {
			onR = elem(spec.onR)
		}
		loop = []ast.Statement{
			ast.NewVarDeclaration("const", "__kml_k", nil, id("__kml_i"), pos),
			assign(id("__kml_i"), ast.NewBinaryExpression("+", id("__kml_i"), num("1"), pos)),
			stmt(call(mem(id("__kml_vals"), "push"), ast.NewNullLiteral(true, pos))),
			assign(id("__kml_rem"), ast.NewBinaryExpression("+", id("__kml_rem"), num("1"), pos)),
			stmt(resolveThen(onF, onR)),
		}
		post = []ast.Statement{dec("__kml_rem"), finish()}
	}
	// The members as Promise's own combinators read them (an array literal
	// mixing instances and plain values included).
	var iv Value
	if at := e.typeOfExpr(args[0]); at.IsArray() {
		ptr, n, elemTy, err := e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, true, err
		}
		iv = e.wrapArrayAggregate(ptr, n, elemTy)
	} else if iv, err = e.emitExpr(args[0]); err != nil {
		return Value{}, true, err
	}
	iter := e.bindValue(iv, pos)
	body := append(append(append([]ast.Statement{}, pre...), ast.NewForOfStatement("const", "__kml_x", iter, block(loop...), pos)), post...)
	try := ast.NewTryStatement(block(body...), &ast.CatchClause{Param: "__kml_e", Body: block(stmt(call(rej, id("__kml_e")))), Pos: pos}, nil, pos)
	wrap := &ast.Program{Body: []ast.Statement{try}}
	if err := sema.Prepare(wrap); err != nil {
		return Value{}, true, err
	}
	if err := e.emitSynthStmt(wrap.Body[0]); err != nil {
		return Value{}, true, err
	}
	e.emitMarkBoxedPromiseValue(inst, cty)
	return Value{Ref: inst, Ty: cty}, true, nil
}

// emitSynthStmt emits a prepared statement the compiler wrote itself, its
// locals that its closures capture boxed at their declarations, as a
// function body's are (hoistedCaptures).
func (e *Emitter) emitSynthStmt(st ast.Statement) error {
	saved := e.hoistedCaptures
	merged := map[string]bool{}
	for k, v := range saved {
		merged[k] = v
	}
	for k := range capturedLocalNames([]ast.Statement{st}, nil) {
		merged[k] = true
	}
	e.hoistedCaptures = merged
	defer func() { e.hoistedCaptures = saved }()
	return e.emitStmt(st)
}

// emitMarkBoxedPromiseValue flags the body of the class instance inst when
// its carried value type (ty's) is `any` but its class's is not, so readers
// through a box decode it as `any` (promiseFlagBoxedValue).
func (e *Emitter) emitMarkBoxedPromiseValue(inst string, ty Type) {
	info, ok := e.classes[ty.ClassName]
	if !ok || info.HostBase == nil || ty.PromiseType == nil || !isUnconstrainedDynamic(*ty.PromiseType) {
		return
	}
	if info.HostBase.PromiseType != nil && isUnconstrainedDynamic(*info.HostBase.PromiseType) {
		return
	}
	fp, old, nw := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %d", fp, inst, 8+promiseFlagsSlot*8))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", old, fp))
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", nw, old, promiseFlagBoxedValue))
	e.heapStore("i64", nw, fp, 8)
}

// emitBoxedPromiseValueTest is an i1: the body of class instance inst is
// flagged as settling to a boxed `any`.
func (e *Emitter) emitBoxedPromiseValueTest(inst string) string {
	fp, fl, m, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %d", fp, inst, 8+promiseFlagsSlot*8))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", fl, fp))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", m, fl, promiseFlagBoxedValue))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, m))
	return r
}

// hasClassAccessor reports a getter or setter named prop on the class.
func (e *Emitter) hasClassAccessor(className, prop string) bool {
	_, _, ok := e.classAccessorSigs(className, prop)
	return ok
}

// arrayClassHost is the array type a class extending Array has as its body.
func (e *Emitter) arrayClassHost(t Type) (Type, bool) {
	if !t.IsClass() || t.IsDynamic() {
		return Type{}, false
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || !info.HostBase.IsArray() {
		return Type{}, false
	}
	return *info.HostBase, true
}

func isSpread(x ast.Expression) bool {
	_, ok := x.(*ast.SpreadElement)
	return ok
}

// hostBufferProps are a buffer's own-looking properties.
var hostBufferProps = map[string]bool{"byteLength": true, "maxByteLength": true, "resizable": true, "growable": true, "detached": true}

// isHostPrimMember reports `length` of an instance of a class extending
// String (Number, Boolean), or a buffer property of one extending
// ArrayBuffer, that is no field or accessor of the class: its host's.
func (e *Emitter) isHostPrimMember(ex *ast.MemberExpression) bool {
	t := e.typeOfExpr(ex.Object)
	if !t.IsClass() || t.IsDynamic() || t.Nullable() {
		return false
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil {
		return false
	}
	switch {
	case info.HostBase.IsPrimWrapper() && ex.Property == "length":
	case info.HostBase.IsArrayBuffer() && hostBufferProps[ex.Property]:
	default:
		return false
	}
	if _, _, own := t.FieldIndex(ex.Property); own {
		return false
	}
	return !e.hasClassAccessor(t.ClassName, ex.Property)
}

// isHostArrayMember reports a member of an instance of a class extending
// Array that is no field, method or accessor of the class: its array's.
func (e *Emitter) isHostArrayMember(ex *ast.MemberExpression) bool {
	t := e.typeOfExpr(ex.Object)
	if _, ok := e.arrayClassHost(t); !ok || t.Nullable() {
		return false
	}
	if _, _, own := t.FieldIndex(ex.Property); own {
		return false
	}
	if _, method := e.classes[t.ClassName].MethodSigs[ex.Property]; method {
		return false
	}
	return !e.hasClassAccessor(t.ClassName, ex.Property)
}

// emitArrayClassReceiver is the receiver an Array method called on an `any`
// runs on: for an instance of a class extending Array that does not declare
// the method itself, its body boxed as an array (sharing its header, so a
// mutator writes the instance's elements); v otherwise. Method "" is any
// instance's body. The test is the program's helper, so
// program-independent code calls it whatever classes the program has.
func (e *Emitter) emitArrayClassReceiver(v Value, method string) Value {
	var classes []ClassInfo
	for _, name := range e.sortedClassNames(func(_ string, info ClassInfo) bool { return info.HostBase != nil && info.HostBase.IsArray() }) {
		classes = append(classes, e.classes[name])
	}
	if len(classes) == 0 && !e.programIndependent() {
		return v
	}
	fn := "@__kml_arrclass_box_" + method
	if !e.fnDecls[fn] {
		e.fnDecls[fn] = true
		restore := e.beginDetachedFunc()
		saved, savedShared := e.inLib, e.sharedCode
		e.inLib, e.sharedCode = "", 0
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %%obj, null", isObj))
		objL, noL := e.freshLabel("arrcls.obj"), e.freshLabel("arrcls.no")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, noL))
		e.emitLabel(objL)
		tag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %%obj, align 8", tag))
		for _, info := range classes {
			if _, own := info.MethodSigs[method]; own {
				continue
			}
			if _, species := arraySpeciesMethods[method]; species && !info.HostBase.IsTypedArray() {
				continue // the instance's own record makes its class's result
			}
			hit, next := e.freshLabel("arrcls.hit"), e.freshLabel("arrcls.next")
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, e.headerIdentity(tag), e.objHeaderWord(info.Ty)))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hit, next))
			e.emitLabel(hit)
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %%obj, i64 8", hdr))
			e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitBoxArrayPlain(e.arrayValueFromHeaderReg(hdr, *info.HostBase))))
			e.emitLabel(next)
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", noL))
		e.emitLabel(noL)
		e.emitTerminator("ret i64 0")
		body := e.allocas.String() + e.body.String()
		e.inLib, e.sharedCode = saved, savedShared
		restore()
		e.writeHelper("internal", "i64", fn, irPtr+" %obj", body)
	}
	tag, pay := e.emitUnboxTagPayload(v)
	isObj, p, obj := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, p, pay))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" null", obj, isObj, p))
	r, has, out := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s("+irPtr+" %s)", r, fn, obj))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", has, r))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", out, has, r, v.Ref))
	return Value{Ref: out, Ty: TypeAny}
}

// hasArrayClass reports a class extending Array in the program;
// program-independent code assumes one.
func (e *Emitter) hasArrayClass() bool {
	if e.programIndependent() {
		return true
	}
	for _, info := range e.classes {
		if info.HostBase != nil && info.HostBase.IsArray() {
			return true
		}
	}
	return false
}

// emitArrayClassObject tests whether the array header hdr (never null) is
// the body of an instance of a class extending Array: its header word is
// then the class's tag word, not the Array one. obj is the instance.
func (e *Emitter) emitArrayClassObject(hdr string) (isClass, obj string) {
	obj, isClass = e.freshReg(), e.freshReg()
	e.emitPrefixWordAddr(obj, hdr)
	if e.programIndependent() {
		// The program's test: without a class extending Array no header
		// is an instance's body, whatever its word.
		e.ensureArrayClassBodyTest()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_is_arrclass_body("+irPtr+" %s)", isClass, obj))
		return isClass, obj
	}
	word := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", word, obj))
	isClass = e.emitArrayWordIsClass(word)
	return isClass, obj
}

// emitArrayWordIsClass is whether an array header's word is a class's tag:
// neither the Array word nor an Arguments object's.
func (e *Emitter) emitArrayWordIsClass(word string) string {
	notArr, notArgs, is := e.freshReg(), e.freshReg(), e.freshReg()
	wid := e.headerIdentity(word)
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", notArr, wid, e.arrayHeaderWord()))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", notArgs, wid, e.argumentsHeaderWord()))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", is, notArr, notArgs))
	return is
}

// ensureArrayClassBodyTest defines @__kml_is_arrclass_body(obj), whether
// the word at obj (an array header's, 8 bytes before it) is a class's tag:
// the program's own code, so a program without a class extending Array
// answers false.
func (e *Emitter) ensureArrayClassBodyTest() {
	const fn = "@__kml_is_arrclass_body"
	if e.fnDecls[fn] {
		return
	}
	e.fnDecls[fn] = true
	restore := e.beginDetachedFunc()
	saved, savedShared := e.inLib, e.sharedCode
	e.inLib, e.sharedCode = "", 0
	if !e.hasArrayClass() {
		e.emitTerminator("ret i1 false")
	} else {
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %%obj, align 8", word))
		e.emitTerminator(fmt.Sprintf("ret i1 %s", e.emitArrayWordIsClass(word)))
	}
	body := e.allocas.String() + e.body.String()
	e.inLib, e.sharedCode = saved, savedShared
	restore()
	e.writeHelper("internal", "i1", fn, irPtr+" %obj", body)
}

// arrayMayBeClassBody reports whether v, an array, may be the body of an
// instance of a class extending Array: the program declares one and v has
// a header (a fresh aggregate never is one).
func (e *Emitter) arrayMayBeClassBody(v Value) bool {
	return !v.Ty.IsBuffer() && !v.Ty.Nullable() && e.hasArrayClass()
}

// emitBoxArrayClassObject boxes obj, an instance of some class extending
// Array, as that instance.
func (e *Emitter) emitBoxArrayClassObject(obj string) string {
	for _, name := range e.sortedClassNames(func(_ string, info ClassInfo) bool { return info.HostBase != nil && info.HostBase.IsArray() }) {
		e.noteBoxedLayout(e.classes[name].Ty)
	}
	return e.emitNbTagPtr(obj, kmlTagObject)
}

// arraySpeciesMethods are the Array methods whose result is made by the
// receiver's constructor (ArraySpeciesCreate), each with whether it sets the
// result's length at the end (slice, splice) rather than only defining its
// elements.
var arraySpeciesMethods = map[string]bool{
	"map": false, "filter": false, "concat": false, "flat": false, "flatMap": false,
	"slice": true, "splice": true,
}

// typedArraySpeciesMethods are the typed array methods whose result is made
// by the receiver's constructor (TypedArraySpeciesCreate, `new C(n)`).
var typedArraySpeciesMethods = map[string]bool{"map": true, "filter": true, "slice": true, "subarray": true}

// emitArraySpecies is Array method method on an instance of a class
// extending Array whose result Node makes with the instance's constructor:
// `new C(n)` (n the result's length for map, slice and splice, else 0), its
// elements then the result's. C is the instance's run-time class. The value
// is typed as the plain array the method returns, its header the new
// instance's body.
func (e *Emitter) emitArraySpecies(instance Value, host Type, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	recv := e.bindHostBody(instance, host, pos)
	saved := e.speciesInner
	e.speciesInner = true
	res, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(recv, method, pos), args, pos))
	e.speciesInner = saved
	if err != nil {
		return Value{}, err
	}
	if !res.Ty.IsArray() || e.blockDone {
		return res, nil
	}
	// A typed array's result is made at its length.
	setsLength := arraySpeciesMethods[method] || host.IsTypedArray()
	// The constructors the instance may have: its class's and every class
	// extending it.
	classes := e.sortedClassNames(func(name string, _ ClassInfo) bool {
		return name == instance.Ty.ClassName || e.classExtends(name, instance.Ty.ClassName)
	})
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", tag, instance.Ref))
	return e.emitArrayClassFill(classes, tag, res, method == "map" || setsLength, setsLength, pos)
}

// arrayBodySpeciesRule is a species-creating Array method on a value typed
// as a plain array, in a program with a class extending Array: the value
// may be such an instance's body (a species result, typed as the plain
// array TypeScript gives it), whose class then makes the result, as
// ArraySpeciesCreate reads the receiver's constructor at run time.
func (e *Emitter) arrayBodySpeciesRule(ex *ast.CallExpression) (Value, bool, error) {
	me, ok := ex.Callee.(*ast.MemberExpression)
	if !ok || me.Optional || e.speciesInner || !e.hasArrayClass() {
		return Value{}, false, nil
	}
	setsLength, ok := arraySpeciesMethods[me.Property]
	if !ok {
		return Value{}, false, nil
	}
	if _, isSuper := me.Object.(*ast.SuperExpression); isSuper {
		return Value{}, false, nil // the super method call creates its species itself
	}
	rt := e.typeOfExpr(me.Object)
	if !rt.IsArray() || rt.IsTypedArray() || rt.IsBuffer() || rt.IsTuple() || rt.Nullable() || rt.IsClass() || rt.IsDynamic() {
		return Value{}, false, nil
	}
	classes := e.arrayBodyClasses()
	indep := e.programIndependent()
	if len(classes) == 0 && !indep {
		return Value{}, false, nil
	}
	pos := ex.GetPos()
	recv, err := e.emitExpr(me.Object)
	if err != nil {
		return Value{}, true, err
	}
	if !recv.Ty.IsArray() || recv.Ref == "zeroinitializer" {
		return Value{}, true, internalAt(pos, "array receiver expected")
	}
	saved := e.speciesInner
	e.speciesInner = true
	bound := e.bindValue(recv, pos)
	e.setSynthDisplay(bound, e.iterableDisplayName(me.Object))
	res, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(bound, me.Property, pos), ex.Args, pos))
	e.speciesInner = saved
	if err != nil || !res.Ty.IsArray() || e.blockDone {
		return res, true, err
	}
	isClass, obj := e.emitArrayClassObject(recv.Ref)
	// Program-independent code names no class: the dispatcher filters.
	if over := e.arrayOverriders(me.Property); len(over) > 0 && !indep {
		// An instance whose class overrides the method ran its own (the
		// override dispatch): its result is the method's, not a species.
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", word, obj))
		for _, name := range over {
			ne, and := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", ne, e.headerIdentity(word), e.objHeaderWord(e.classes[name].Ty)))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", and, isClass, ne))
			isClass = and
		}
	}
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", out))
	clsL, plainL, doneL := e.freshLabel("species.body"), e.freshLabel("species.plain"), e.freshLabel("species.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isClass, clsL, plainL))
	e.emitLabel(clsL)
	var filled Value
	if indep {
		// Program-independent code names no class: the program's own
		// dispatcher makes the result.
		fn, err := e.ensureSpeciesFill(res.Ty, me.Property, me.Property == "map" || setsLength, setsLength, pos)
		if err != nil {
			return Value{}, true, err
		}
		filled = Value{Ref: e.freshReg(), Ty: res.Ty}
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" %s("+irPtr+" %s, "+irPtr+" %s)", filled.Ref, fn, obj, res.Ref))
	} else {
		tag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", tag, obj))
		filled, err = e.emitArrayClassFill(classes, tag, res, me.Property == "map" || setsLength, setsLength, pos)
		if err != nil {
			return Value{}, true, err
		}
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", filled.Ref, out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(plainL)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", res.Ref, out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", v, out))
	return Value{Ref: v, Ty: res.Ty}, true, nil
}

// arrayBodyClasses are the program's classes extending Array (not a typed
// array) whose instances' bodies arrayBodySpeciesRule recognizes.
func (e *Emitter) arrayBodyClasses() []string {
	return e.sortedClassNames(func(name string, info ClassInfo) bool {
		return info.HostBase != nil && info.HostBase.IsArray() && !info.HostBase.IsTypedArray() && e.arrayVariantOf[name] == ""
	})
}

// ensureSpeciesFill defines the program's dispatcher for a species result
// of type resTy made in program-independent code: given the receiver's
// instance and the plain result, the instance its class makes
// (emitArrayClassFill), or the result itself in a program without a class
// extending Array. One per result type and length mode.
func (e *Emitter) ensureSpeciesFill(resTy Type, method string, withLength, setsLength bool, pos ast.Pos) (string, error) {
	key, err := mangleTypeArg(resTy)
	if err != nil {
		return "", err
	}
	// Named by a hash of the type: a library type's mangle carries its
	// module's key, which would make the splitter give the dispatcher to
	// that module's unit.
	h := fnv.New64a()
	fmt.Fprintf(h, "%s.%s.%t.%t", key, method, withLength, setsLength)
	fn := fmt.Sprintf("@__kml_species_fill_%x", h.Sum64())
	if e.fnDecls[fn] {
		return fn, nil
	}
	e.fnDecls[fn] = true
	restore := e.beginDetachedFunc()
	saved, savedShared := e.inLib, e.sharedCode
	e.inLib, e.sharedCode = "", 0
	// Only a class whose elements are the result's can have made a body
	// typed as it.
	var classes []string
	for _, name := range e.arrayBodyClasses() {
		if ek, kerr := mangleTypeArg(*e.classes[name].HostBase.ElemType); kerr == nil && resTy.ElemType != nil {
			if rk, rerr := mangleTypeArg(*resTy.ElemType); rerr == nil && rk == ek {
				classes = append(classes, name)
			}
		}
	}
	out := "%res"
	if len(classes) > 0 {
		tag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %%obj, align 8", tag))
		// An instance whose class overrides the method ran its own: its
		// result is the method's, not a species.
		for _, name := range e.arrayOverriders(method) {
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, e.headerIdentity(tag), e.objHeaderWord(e.classes[name].Ty)))
			ownL, nextL := e.freshLabel("species.own"), e.freshLabel("species.notown")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, ownL, nextL))
			e.emitLabel(ownL)
			e.emitTerminator("ret " + irPtr + " %res")
			e.emitLabel(nextL)
		}
		filled, ferr := e.emitArrayClassFill(classes, tag, Value{Ref: "%res", Ty: resTy}, withLength, setsLength, pos)
		if ferr != nil {
			err = ferr
		}
		out = filled.Ref
	}
	e.emitTerminator(fmt.Sprintf("ret "+irPtr+" %s", out))
	body := e.allocas.String() + e.body.String()
	e.inLib, e.sharedCode = saved, savedShared
	restore()
	if err != nil {
		return "", err
	}
	e.writeHelper("internal", irPtr, fn, irPtr+" %obj, "+irPtr+" %res", body)
	return fn, nil
}

// emitArrayClassFill makes the instance a class extending Array makes for
// an Array result res, as ArraySpeciesCreate and Array.from/of do: `new
// C(n)` (n res's length when withLength, else no length), its elements then
// res's, the length then res's when setsLength. C is classes[0], or with
// tag (an instance's tag word) the one of classes it names. The value is
// typed as res, its header the instance's body.
func (e *Emitter) emitArrayClassFill(classes []string, tag string, res Value, withLength, setsLength bool, pos ast.Pos) (Value, error) {
	// Its own Array calls run on plain arrays.
	saved := e.speciesInner
	e.speciesInner = true
	defer func() { e.speciesInner = saved }()
	resultTy := res.Ty
	k := e.freshReg()
	k = e.arrayLen(res)
	n := "0.0"
	if withLength {
		n = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", n, k))
	}
	resID := e.bindValue(res, pos)
	nID := e.bindValue(Value{Ref: n, Ty: TypeF64}, pos)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", out))
	doneL := e.freshLabel("species.done")
	for i, name := range classes {
		hitL, nextL := e.freshLabel("species.new"), e.freshLabel("species.next")
		if tag == "" || i == len(classes)-1 {
			e.emitTerminator(fmt.Sprintf("br label %%%s", hitL))
		} else {
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, e.headerIdentity(tag), e.objHeaderWord(e.classes[name].Ty)))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
		}
		e.emitLabel(hitL)
		// The class's [Symbol.species] picks the constructor: another class,
		// or the builtin itself, whose result is a plain one.
		ctor, err := e.speciesOf(name, pos)
		if err != nil {
			return Value{}, err
		}
		if ctor == "" {
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.boxArrayValue(res), out))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			if tag == "" {
				break
			}
			if i < len(classes)-1 {
				e.emitLabel(nextL)
			}
			continue
		}
		if base, ok := e.arrayVariantOf[ctor]; ok {
			ctor = base // a variant is made by its class
		}
		info := e.classes[ctor]
		// A result whose elements another layout holds (`map(String)` on a
		// class extending Array<number>) is an instance of the class's
		// variant for that layout.
		variant := ""
		if h := info.HostBase; h != nil && h.IsArray() && !h.IsTypedArray() && resultTy.IsArray() &&
			!sameArrayLayout(*h, resultTy) {
			variant = e.arrayClassVariant(ctor, resultTy)
		}
		// A constructor whose first parameter cannot hold a number (a
		// `...items: string[]`) is called without the length: a native
		// string slot has no room for one. Its instance's elements are the
		// result's either way.
		var ctorArgs []ast.Expression
		if withLength && e.ctorTakesNumber(info) {
			ctorArgs = []ast.Expression{nID}
		}
		obj, err := e.emitSpeciesNew(ast.NewNewExpression(ctor, ctorArgs, pos))
		if err != nil {
			return Value{}, err
		}
		body := e.bindHostBody(obj, *info.HostBase, pos)
		// Its elements: the result's, over any the constructor made; a set
		// length then drops the rest.
		fill := ast.Expression(resID)
		if variant != "" {
			// The class's own elements are of the other layout: the result's
			// alone.
			e.heapStore("i64", fmt.Sprint(e.objHeaderWord(e.classes[variant].Ty)), obj.Ref, 8)
		} else if !setsLength {
			rest := ast.NewCallExpression(ast.NewMemberExpression(body, "slice", pos), []ast.Expression{ast.NewMemberExpression(resID, "length", pos)}, pos)
			fill = ast.NewCallExpression(ast.NewMemberExpression(resID, "concat", pos), []ast.Expression{rest}, pos)
		}
		fv, err := e.emitExpr(fill)
		if err != nil {
			return Value{}, err
		}
		if variant == "" {
			fv = e.convert(fv, *info.HostBase)
		}
		hdr := e.freshReg()
		e.emitBodyAddr(hdr, obj.Ref)
		e.storeArrayPartsInto(fv, hdr)
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", hdr, out))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		if tag == "" {
			break
		}
		if i < len(classes)-1 {
			e.emitLabel(nextL)
		}
	}
	e.emitLabel(doneL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", hdr, out))
	return e.arrayValueFromHeaderReg(hdr, resultTy), nil
}

// sameArrayLayout reports whether arrays of a's and b's types store their
// elements alike.
func sameArrayLayout(a, b Type) bool {
	return a.ElemType != nil && b.ElemType != nil && sameReprKey(*a.ElemType) == sameReprKey(*b.ElemType)
}

// arrayClassVariant registers (once) the variant of class, a class
// extending Array, whose elements are arr's: a child of class shown under
// its name, as every element layout of Array is a child of any[]'s.
func (e *Emitter) arrayClassVariant(class string, arr Type) string {
	key, err := mangleTypeArg(*arr.ElemType)
	if err != nil {
		key = hashedTypeArg(*arr.ElemType)
	}
	name := class + "__elems_" + key
	if _, ok := e.classes[name]; ok {
		return name
	}
	info := e.classes[class]
	host := ArrayOf(*arr.ElemType)
	ty := info.Ty
	ty.ClassName = name
	info.Ty = ty
	info.HostBase = &host
	info.BaseClass = class
	info.TagID = e.classTypeID(name)
	e.classes[name] = info
	e.interfaces[name] = ty
	if e.arrayVariantOf == nil {
		e.arrayVariantOf = map[string]string{}
	}
	e.arrayVariantOf[name] = class
	if e.classShownNames == nil {
		e.classShownNames = map[string]string{}
	}
	e.classShownNames[name] = e.classDisplayName(class)
	return name
}

// speciesOf is the constructor a species-creating method of className's
// makes its result with: className itself, unless it or an ancestor
// declares `static get [Symbol.species]()`. The getter's body is one
// return: of `this` (className), of another class extending the same
// builtin, or of the builtin (Array, a typed array's constructor) itself,
// or undefined or null, for which ctor is "" (a plain result, as
// ArraySpeciesCreate makes). A getter computing its result is rejected:
// the constructor is chosen at compile time.
// speciesClass is speciesOf for the checker's oracle: the class's type,
// plain for Promise itself; not ok when the getter is not one it reads.
func (e *Emitter) speciesClass(className string) (Type, bool, bool) {
	c, err := e.speciesOf(className, ast.Pos{})
	if err != nil {
		return Type{}, false, false
	}
	if c == "" {
		return Type{}, true, true
	}
	return e.classes[c].Ty, false, true
}

func (e *Emitter) speciesOf(className string, pos ast.Pos) (ctor string, err error) {
	for c := className; c != ""; c = e.classes[c].BaseClass {
		if _, ok := e.classes[c]; !ok {
			break
		}
		getter := e.staticSpeciesGetter(c)
		if getter == nil {
			continue
		}
		var ret ast.Expression
		if getter.Body != nil && len(getter.Body.Body) == 1 {
			if r, ok := getter.Body.Body[0].(*ast.ReturnStatement); ok {
				ret = r.Value
			}
		}
		switch v := ret.(type) {
		case *ast.ThisExpression:
			return className, nil
		case *ast.NullLiteral:
			return "", nil
		case *ast.Identifier:
			if target, ok := e.identClassAlias(v); ok {
				v = ast.NewIdentifier(target, v.GetPos())
			}
			if v.Name == e.speciesBuiltin(className) {
				return "", nil
			}
			if info, ok := e.classes[v.Name]; ok && info.HostBase != nil && e.speciesBuiltin(v.Name) == e.speciesBuiltin(className) {
				return v.Name, nil
			}
		}
		// The resolver rejects any other getter (SpeciesComputed).
		return "", internalAt(pos, "%s's [Symbol.species] getter past the resolver", ast.Unmangle(c))
	}
	return className, nil
}

// speciesBuiltin names the builtin a class extending Array, a typed array
// or Promise extends.
func (e *Emitter) speciesBuiltin(className string) string {
	switch host := e.classes[className].HostBase; {
	case host != nil && host.IsTypedArray():
		return typedArrayConstructorName(*host)
	case host != nil && host.IsPromise():
		return "Promise"
	}
	return "Array"
}

// staticSpeciesGetter is a class's own `static get [Symbol.species]()`.
func (e *Emitter) staticSpeciesGetter(className string) *ast.FunctionDeclaration {
	for cd, name := range e.classDeclName {
		if name != className {
			continue
		}
		for _, m := range cd.Methods {
			if m.IsStatic && m.AccessorKind == "get" && m.Name == "@@species" {
				return m
			}
		}
	}
	return nil
}

// emitArrayClassStatic is Array's static method on a class extending
// Array: from and of make an instance of the class (`new C()` for from,
// `new C(n)` for of, then its elements); isArray is Array's.
func (e *Emitter) emitArrayClassStatic(className, method string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	if method != "from" && method != "of" && method != "isArray" {
		return Value{}, false, nil
	}
	var call *ast.CallExpression
	if host := e.classes[className].HostBase; host.IsTypedArray() {
		// A typed array's from/of: its library function.
		fn, ok := e.globalLinks["__kml_"+typedArrayConstructorName(*host)+"_"+method]
		if !ok || method == "isArray" {
			return Value{}, false, nil
		}
		call = ast.NewCallExpression(ast.NewIdentifier(fn, pos), args, pos)
	} else {
		call = ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Array", pos), method, pos), args, pos)
		call.Intrinsic = "Array." + method
	}
	res, err := e.emitCall(call)
	if err != nil || method == "isArray" || !res.Ty.IsArray() || e.blockDone {
		return res, true, err
	}
	typed := e.classes[className].HostBase.IsTypedArray()
	v, err := e.emitArrayClassFill([]string{className}, "", res, method == "of" || typed, true, pos)
	return v, true, err
}

// ctorTakesNumber reports whether class info's constructor can be passed a
// number as its first argument: none of its own (Array's), or a first
// parameter, or rest element, typed number or any.
func (e *Emitter) ctorTakesNumber(info ClassInfo) bool {
	if info.Constructor == nil || info.ImplicitCtor {
		return true
	}
	ps := info.Constructor.Params
	if len(ps) == 0 || len(info.CtorSig.ParamTypes) == 0 {
		return true
	}
	t := info.CtorSig.ParamTypes[0]
	if ps[0].Rest && t.ElemType != nil {
		t = *t.ElemType
	}
	return isNumericScalar(t) || isUnconstrainedDynamic(t)
}

// arrayOverriders are the classes extending Array that declare method, an
// Array method, themselves (or inherit it from a class of the program).
func (e *Emitter) arrayOverriders(method string) []string {
	return e.sortedClassNames(func(_ string, info ClassInfo) bool {
		if info.HostBase == nil || !info.HostBase.IsArray() {
			return false
		}
		_, own := info.MethodSigs[method]
		return own
	})
}

// emitArrayOverrideDispatch is Array method name called on a value typed as
// an array when some class extending Array overrides it: an array that is
// such an instance's body runs the override on the instance, as Node's
// prototype lookup does; any other runs Array's (emit). ok is false when no
// class overrides it, or the receiver is not a plain array.
func (e *Emitter) emitArrayOverrideDispatch(mem *ast.MemberExpression, name string, ex *ast.CallExpression, emit func(*Emitter, *ast.MemberExpression, []ast.Expression, ast.Pos) (Value, error)) (Value, bool, error) {
	if e.inArrayOverrideDispatch || e.inLib != "" {
		// The builtin library calls Array's own methods, as Node's internals
		// do through primordials (ArrayPrototypeSlice), never a subclass's
		// override; its code then does not depend on the program's classes.
		return Value{}, false, nil
	}
	if id, ok := mem.Object.(*ast.Identifier); ok && (strings.HasPrefix(id.Name, "__kml_hostbody_") || strings.HasPrefix(id.Name, "__kml_dynarr_")) {
		// An instance's own Array method (super.push), or Array's method
		// run through `any` once the instance has none of its own.
		return Value{}, false, nil
	}
	rt := e.typeOfExpr(mem.Object)
	if !rt.IsArray() || rt.IsTypedArray() || rt.IsBuffer() || rt.IsTuple() || rt.IsFlatArray() || rt.Nullable() || rt.IsDynamic() {
		return Value{}, false, nil
	}
	classes := e.arrayOverriders(name)
	if len(classes) == 0 {
		return Value{}, false, nil
	}
	pos := ex.GetPos()
	recv, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, true, err
	}
	hdr := e.nullSafeArrayHeader(recv.Ref)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", hdr, slot))
	rid := fmt.Sprintf("__kml_ovrecv_%s", slot[1:])
	e.define(rid, Symbol{Ptr: slot, Ty: recv.Ty})
	resTy := e.typeOfExpr(ex)
	var out string
	if resTy.IR != "" && resTy.IR != "void" {
		out = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", out, resTy.IR))
	}
	arrRes := resTy.IsArray() && resTy.IR == irPtr // crosses as its header
	obj, word := e.freshReg(), e.freshReg()
	e.emitPrefixWordAddr(obj, hdr)
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", word, obj))
	doneL := e.freshLabel("arrov.done")
	failAt := e.arrovFail
	finish := func(v Value) {
		if e.blockDone {
			return
		}
		if out != "" {
			v = e.convert(v, resTy)
			ref := v.Ref
			if arrRes {
				ref = v.Ref
				if ref == "" {
					ref = e.boxArrayValue(v)
				}
			}
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", resTy.IR, ref, out))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	for _, name2 := range classes {
		info := e.classes[name2]
		hitL, nextL := e.freshLabel("arrov.cls"), e.freshLabel("arrov.next")
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, e.headerIdentity(word), e.objHeaderWord(info.Ty)))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
		e.emitLabel(hitL)
		v, err := e.emitClassCall(info.Ty, Value{Ref: obj, Ty: info.Ty}, name, ex.Args, pos, false)
		if err != nil {
			if !e.blockDone {
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			}
			e.emitLabel(nextL)
			return Value{}, true, failAt(doneL, err)
		}
		finish(v)
		e.emitLabel(nextL)
	}
	v, err := e.withArrayOverrideDispatchOff(func() (Value, error) {
		return emit(e, ast.NewMemberExpression(ast.NewIdentifier(rid, pos), name, pos), ex.Args, pos)
	})
	if err != nil {
		return Value{}, true, failAt(doneL, err)
	}
	finish(v)
	e.emitLabel(doneL)
	if out == "" {
		return Value{Ty: resTy}, true, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, resTy.IR, out))
	if arrRes {
		return e.arrayValueFromHeaderReg(r, resTy), true, nil
	}
	return Value{Ref: r, Ty: resTy}, true, nil
}

// withArrayOverrideDispatchOff runs fn with the override dispatch off: the
// default branch is Array's own method.
func (e *Emitter) withArrayOverrideDispatchOff(fn func() (Value, error)) (Value, error) {
	prev := e.inArrayOverrideDispatch
	e.inArrayOverrideDispatch = true
	defer func() { e.inArrayOverrideDispatch = prev }()
	return fn()
}

// arrovFail is an override dispatch's error once a branch may already
// jump to its join label doneL: the label is emitted, so a caller that
// recovers from the error (a dynamic method adapter's arity variants) still
// has valid IR.
func (e *Emitter) arrovFail(doneL string, err error) error {
	if !e.blockDone {
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(doneL)
	return err
}

// hostInferReceiver is a name typed as host, for inferring the type of a
// host member read or call on a class instance extending Map or Set; no
// code reads it.
func (e *Emitter) hostInferReceiver(host Type, pos ast.Pos) *ast.Identifier {
	const name = "__kml_hostbody_infer"
	e.define(name, Symbol{Ptr: "null", Ty: host})
	return ast.NewIdentifier(name, pos)
}

// nativeIfaceMember is the declared type of an own member prop of an
// interface over a native layout (nativeHeritage) that the layout itself
// does not hold: the instance's property, read and written at run time.
func (e *Emitter) nativeIfaceMember(t Type, prop string) (Type, bool) {
	if t.RefName == "" {
		return Type{}, false
	}
	if _, _, ok := t.FieldIndex(prop); ok {
		return Type{}, false
	}
	ft, ok := e.nativeIfaceMembers[t.RefName][prop]
	return ft, ok
}

// nativeHeritage is the native layout an interface's builtin base names
// (`extends TypeError`), unless the program declares that base itself.
func (e *Emitter) nativeHeritage(s *ast.InterfaceDeclaration) (Type, bool) {
	for _, base := range s.Extends {
		if _, own := e.interfaces[base]; own {
			continue
		}
		if _, own := e.classes[base]; own {
			continue
		}
		if t, ok := e.declaredLayout(base); ok {
			return t, true
		}
	}
	return Type{}, false
}

// libClassTags are the tags of builtin interfaces a lib module implements
// as a class: the module export of that class.
var libClassTags = map[string]string{
	"timeout":   "internal_timers:Timeout",
	"immediate": "internal_timers:Immediate",
	// ReadableStream's async iterator (stream.values()).
	"streamasynciterator": "internal_rs_iterator:StreamAsyncIterator",
}

// taggedLibClass is the program name of the class implementing sym's
// tagged interface; "" when untagged or not linked.
func (e *Emitter) taggedLibClass(sym *binder.Symbol) string {
	if exp, ok := libClassTags[declaredLowerType(sym)]; ok {
		return e.libExports[exp]
	}
	return ""
}

// taggedLayout is the plain layout sym's declaration tag names.
func taggedLayout(sym *binder.Symbol) (Type, bool) {
	if sym == nil {
		return Type{}, false
	}
	if mk, ok := lowerLayouts[declaredLowerType(sym)]; ok {
		return mk(), true
	}
	return Type{}, false
}

// nameAnnotation is the annotation spelling name (`string`, `any[]`,
// `ArrayBuffer`), over a type node the checker answers.
func nameAnnotation(name string) ast.TypeNode {
	n := nameTypeNode(name)
	ta, err := ast.TypeAnnotationOf(n, "ts")
	if err != nil {
		return n
	}
	return ta
}

func nameTypeNode(name string) ast.TypeNode {
	if el, ok := strings.CutSuffix(name, "[]"); ok {
		return &ast.ArrayType{ElementType: nameTypeNode(el)}
	}
	if typeKeywords[name] {
		return &ast.KeywordType{Keyword: name}
	}
	return &ast.TypeReference{Name: name}
}

// typeKeywords are the type names TypeScript spells as keywords.
var typeKeywords = map[string]bool{"any": true, "unknown": true, "string": true, "number": true, "boolean": true, "bigint": true, "symbol": true, "void": true, "undefined": true, "null": true, "never": true, "object": true}

// classAnnotation is the annotation naming class or type name: a class
// whose layout is built carries its instance (synthTypeAnnotation); any
// other name is its type node.
func (e *Emitter) classAnnotation(name string) ast.TypeNode {
	if info, ok := e.classes[name]; ok && info.Ty.IR != "" {
		return e.synthTypeAnnotation(info.Ty)
	}
	return nameAnnotation(name)
}

// namedType is the type a name stands for: a built class's instance, else
// the checker's answer for its type node.
func (e *Emitter) namedType(name string) Type {
	if info, ok := e.classes[name]; ok && info.Ty.IR != "" {
		return info.Ty
	}
	return e.annotationType(nameAnnotation(name))
}

// arrayAnnotation is `T[]` for el's T, over a type node when el has one.
func arrayAnnotation(el ast.TypeNode) ast.TypeNode {
	if el != nil {
		if ta, err := ast.TypeAnnotationOf(&ast.ArrayType{ElementType: el}, "ts"); err == nil {
			return ta
		}
	}
	return ast.AnyArrayAnnotation("ts")
}

// annotIs reports whether the annotation ta spells the keyword type kw.
func annotIs(ta ast.TypeNode, kw string) bool {
	return ta != nil && ast.IsKeywordNode(ta, kw)
}

// annotIsFunctionType reports whether ta spells a function type (not a
// constructor type).
func annotIsFunctionType(ta ast.TypeNode) bool {
	if ta == nil {
		return false
	}
	_, ok := ast.NonNullish(ta).(*ast.FunctionType)
	return ok
}

// annotNullable reports whether ta's union spells null or undefined.
func annotNullable(ta ast.TypeNode) bool {
	n, _, _ := annotAbsence(ta)
	return n
}

// annotRefName is the name of the type reference ta spells (its type
// arguments aside), else "".
func annotRefName(ta ast.TypeNode) string {
	if ta == nil {
		return ""
	}
	if r, ok := ast.Unparen(ta).(*ast.TypeReference); ok {
		return r.Name
	}
	return ""
}

// annotIsTuple reports whether ta spells a tuple type.
func annotIsTuple(ta ast.TypeNode) bool {
	if ta == nil {
		return false
	}
	_, ok := ast.Unparen(ta).(*ast.TupleType)
	return ok
}

// annotIsCtorType reports whether ta spells a constructor type.
func annotIsCtorType(ta ast.TypeNode) bool {
	if ta == nil {
		return false
	}
	_, ok := ast.Unparen(ta).(*ast.ConstructorType)
	return ok
}

// collectionTypeArity is each keyed-collection layout's type-argument
// count.
var collectionTypeArity = map[string]int{"map": 2, "set": 1, "weakmap": 2, "weakset": 1}

// emitSpeciesNew is a `new` of class whose result is used as the class's
// instance: a constructor that may return another object types it any.
func (e *Emitter) emitSpeciesNew(ne *ast.NewExpression) (Value, error) {
	v, err := e.emitExpr(ne)
	if err != nil {
		return Value{}, err
	}
	if info, ok := e.classes[ne.ClassName]; ok && v.Ty.IsDynamic() {
		v = e.convert(v, info.Ty)
	}
	return v, nil
}
