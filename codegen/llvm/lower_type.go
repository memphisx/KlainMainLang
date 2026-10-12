package llvm

import (
	"KlainMainLang/sema"
	"fmt"
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
	"error":             func() Type { return errorObjType },
	"date":              func() Type { return TypeDate },
	"regexp":            RegExpType,
	"arraybuffer":       ArrayBufferType,
	"sharedarraybuffer": SharedArrayBufferType,
	"int8array":         func() Type { return TypedArrayType("int8") },
	"uint8array":        func() Type { return TypedArrayType("uint8") },
	"uint8clampedarray": func() Type { return TypedArrayType("uint8clamped") },
	"int16array":        func() Type { return TypedArrayType("int16") },
	"uint16array":       func() Type { return TypedArrayType("uint16") },
	"int32array":        func() Type { return TypedArrayType("int32") },
	"uint32array":       func() Type { return TypedArrayType("uint32") },
	"float32array":      func() Type { return TypedArrayType("float32") },
	"float64array":      func() Type { return TypedArrayType("float64") },
	"bigint64array":     func() Type { return TypedArrayType("bigint64") },
	"biguint64array":    func() Type { return TypedArrayType("biguint64") },
	"buffer":            BufferType,
	"response":          ResponseType,
	"request":           FetchRequestType,
	"xmlhttprequest":    XMLHttpRequestType,
	// "dynamic" is a builtin whose values are held as `any` (a property
	// descriptor, which the Object and Reflect builtins read and build as a
	// dynamic object).
	"dynamic": func() Type { return TypeAny },
	// An AggregateError is held as the shared Error layout, its trailing
	// `errors` pair read by kind (aggregateErrorStructIR).
	"aggregateerror": func() Type { return errorObjType },
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
	}
	for _, d := range sym.Declarations {
		id, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok {
			continue
		}
		for _, base := range id.Extends {
			if l := lowerTypeOfSymbol(lookupFrom(sym.Scope, base), seen); l != "" {
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

var (
	libScopeOnce sync.Once
	libScopeVal  *binder.Scope
)

// libScope is the scope of the builtin declarations bound around an empty
// program.
func libScope() *binder.Scope {
	libScopeOnce.Do(func() {
		progs, err := lib.Programs()
		if err != nil {
			return
		}
		libScopeVal = binder.BindWith(&ast.Program{}, binder.Options{Lib: progs}).Module
	})
	return libScopeVal
}

// genericLayouts build the layout of a builtin generic declaration's
// instantiation from its type arguments, already in codegen's
// representation; a nil argument is one the type does not give. false leaves
// the type to the caller (a bare `Map` annotation is a generic interface's
// default instantiation in resolveType).
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
		if !inner.IsResponse {
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
	"readablestreamdefaultreader":     {1, elemLayout(StreamReaderType)},
	"readablestreamdefaultcontroller": {1, elemLayout(RSControllerType)},
	"writablestream": {1, func(args []*Type) (Type, bool) {
		return WritableStreamType(argOr(args[0], TypeAny)), true
	}},
	"writablestreamdefaultwriter":     {1, elemLayout(WSWriterType)},
	"writablestreamdefaultcontroller": {1, elemLayout(WSControllerType)},
	"transformstream": {2, func(args []*Type) (Type, bool) {
		i, o := argOr(args[0], TypeAny), argOr(args[1], TypeAny)
		return Type{IR: "ptr", IsTransformStream: true, StreamChunk: &i, StreamOut: &o}, true
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

// genericLayout is the layout of an annotation naming a builtin generic
// declaration with a `@lowerType`.
func (e *Emitter) genericLayout(ta *ast.TypeAnnotation) (Type, bool) {
	if ta.Name == "" || len(ta.Qualifier) > 0 {
		return Type{}, false
	}
	def, ok := genericLayouts[declaredLowerType(e.lookupBuiltinType(ta.Name))]
	if !ok {
		return Type{}, false
	}
	// The parser gives `A<K, V>` as KeyType/ElemType and `A<T>` as
	// ElemType, except where it fills TypeArgs.
	given := ta.TypeArgs
	if len(given) == 0 {
		if def.arity == 2 {
			given = []*ast.TypeAnnotation{ta.KeyType, ta.ElemType}
		} else {
			given = []*ast.TypeAnnotation{ta.ElemType}
		}
	}
	args := make([]*Type, def.arity)
	for i := range args {
		if i < len(given) && given[i] != nil {
			t := e.resolveType(given[i])
			args[i] = &t
		}
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
	return def.mk(args)
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
		t := e.resolveType(&ast.TypeAnnotation{Name: cd.BaseClass})
		if t.IsTypedArray {
			return &t
		}
	}
	if cd.BaseClass == "Array" {
		// Array declares no layout of its own: its values are arrays.
		el := TypeAny
		if len(cd.BaseTypeArgs) == 1 {
			el = e.resolveType(cd.BaseTypeArgs[0])
		}
		t := ArrayOf(el)
		return &t
	}
	layout := declaredLowerType(e.lookupBuiltinType(cd.BaseClass))
	ta := &ast.TypeAnnotation{Name: cd.BaseClass}
	any := &ast.TypeAnnotation{Name: "any"}
	switch layout {
	case "map":
		ta.KeyType, ta.ElemType = any, any
		if len(cd.BaseTypeArgs) == 2 {
			ta.KeyType, ta.ElemType = cd.BaseTypeArgs[0], cd.BaseTypeArgs[1]
		}
	case "set":
		ta.ElemType = any
		if len(cd.BaseTypeArgs) == 1 {
			ta.ElemType = cd.BaseTypeArgs[0]
		}
	case "regexp":
		t := RegExpType()
		return &t
	case "date":
		t := TypeDate
		return &t
	case "promise":
		el := TypeAny
		if len(cd.BaseTypeArgs) == 1 {
			el = e.resolveType(cd.BaseTypeArgs[0])
		}
		t := PromiseOf(el)
		t.PromiseTask = true
		return &t
	default:
		return nil
	}
	t, ok := e.genericLayout(ta)
	if !ok {
		return nil
	}
	return &t
}

// hostBodyPrefix names the hidden fields that hold a host base's body
// inside a class instance, right after its tag word.
const hostBodyPrefix = "__kml_hb"

// hostBodySize is a host base's body in words: a Map's or Set's
// (collections_engine.h), or a RegExp's struct.
func hostBodySize(host Type) int {
	switch {
	case host.IsRegExp:
		return int(host.StructSize() / 8)
	case host.IsPromise:
		return promiseStructSize / 8
	case host.IsDate:
		return 1 // the time value
	case host.IsArray:
		return 2 // the array header's {data, len}
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, instance))
	if host.IsRegExp || host.IsDate || host.IsArray {
		return body
	}
	if host.IsPromise {
		e.ensurePromiseRuntime()
		e.declareFn("__kml_promise_init_at", "declare void @__kml_promise_init_at(ptr)")
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_init_at(ptr %s)", body))
		return body
	}
	e.usedCollections = true
	e.declareFn("__kml_map_init_at", "declare void @__kml_map_init_at(ptr)")
	e.emitInstr(fmt.Sprintf("call void @__kml_map_init_at(ptr %s)", body))
	return body
}

// emitHostArrayBody sets the array body at body from the construction's
// arguments, as `new Array(…)` does: one number is a length, anything else
// the elements; a typed array's as its own constructor takes them.
func (e *Emitter) emitHostArrayBody(body string, host Type, args []ast.Expression, pos ast.Pos) error {
	var v Value
	var err error
	if host.IsTypedArray {
		// A typed array's own construction: a length, an array-like or a
		// buffer.
		if len(args) == 0 {
			args = []ast.Expression{ast.NewNumberLiteral("0", pos)} // empty
		}
		n, _, lerr := sema.BuiltinNew(ast.NewNewExpression(typedArrayConstructorName(host), args, pos))
		if lerr != nil {
			return lerr
		}
		v, err = e.emitExpr(n)
	} else if len(args) == 1 && !isSpread(args[0]) && isNumericScalar(e.inferExprType(args[0])) {
		v, err = e.emitNewArraySizedAggregate(ast.NewNewArrayExpression(nil, args[0], pos), *host.ElemType)
	} else {
		v, err = e.emitExprWithObjectHint(ast.NewArrayLiteral(args, pos), host)
	}
	if err != nil {
		return err
	}
	v = e.coerce(v, host)
	e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", v.Ref, body))
	return nil
}

// emitHostRegExpBody compiles a RegExp into the host body at body from the
// construction's arguments, as `new RegExp(pattern, flags)` does.
func (e *Emitter) emitHostRegExpBody(body string, args []ast.Expression, pos ast.Pos) error {
	if len(args) > 2 {
		return fmt.Errorf("%d:%d: RegExp takes at most two arguments", pos.Line, pos.Col)
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
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsArray {
			// An absent optional array (its null header) seeds nothing, as
			// `new Map(undefined)` makes an empty Map.
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, sym.Ptr))
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
	if host.IsMap {
		return e.emitMapSeedFromEntries(body, src, *host.MapKey, *host.MapVal, pos)
	}
	ptr, n, elemTy, err := e.resolveArrayForHOF(src, pos)
	if err != nil {
		return err
	}
	if reprKey(elemTy) != reprKey(*host.MapKey) && !host.MapKey.IsDynamic {
		return fmt.Errorf("%d:%d: the Set's elements and the array's differ in type", pos.Line, pos.Col)
	}
	return e.emitSetSeed(body, ptr, n, elemTy)
}

// bindHostBody binds the host body of a class instance extending Map or Set
// (instance + 8) to a fresh name typed as the host, for a host member read
// or call on it.
func (e *Emitter) bindHostBody(instance Value, host Type, pos ast.Pos) *ast.Identifier {
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, instance.Ref))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", body, slot))
	name := fmt.Sprintf("__kml_hostbody_%s", slot[1:])
	e.define(name, Symbol{Ptr: slot, Ty: host})
	return ast.NewIdentifier(name, pos)
}

// hostArrayReceiver is obj's array body when obj is an instance of a class
// extending Array: obj is evaluated once and its body bound to a name, so
// an element or array property reads and writes the instance's own array.
func (e *Emitter) hostArrayReceiver(obj ast.Expression) (ast.Expression, bool, error) {
	t := e.inferExprType(obj)
	if !t.IsClass || t.IsDynamic || t.Nullable {
		return nil, false, nil
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || !info.HostBase.IsArray {
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
	if host.IsRegExp || host.IsDate {
		return e.emitInspectRegExpClass(v, info, depth)
	}
	if host.IsPromise {
		return e.emitInspectPromiseClass(v, info, depth)
	}
	if host.IsArray {
		if depth > e.effectiveInspectDepth() {
			return Value{Ref: e.internString("[" + ast.Unmangle(info.Ty.ClassName) + "]"), Ty: TypePtr}, nil
		}
		fields := info.Ty.VisibleFields()
		tag := ""
		if host.IsTypedArray {
			tag = "[" + typedArrayConstructorName(host) + "] " // `Bytes(3) [Uint8Array] [ … ]`
		}
		label := collLabel{class: ast.Unmangle(info.Ty.ClassName), tag: tag, push: func(list string) error {
			return e.inspectPushFields(list, v, info, fields, depth)
		}}
		return e.emitInspectArrayAs(e.coerce(v, host), depth, label)
	}
	if depth > e.effectiveInspectDepth() {
		// Past util.inspect's depth: `[Counter [Map]]`.
		return Value{Ref: e.internString("[" + ast.Unmangle(info.Ty.ClassName) + " [" + hostTagName(host) + "]]"), Ty: TypePtr}, nil
	}
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, v.Ref))
	bv := Value{Ref: body, Ty: host}
	fields := info.Ty.VisibleFields()
	label := collLabel{class: ast.Unmangle(info.Ty.ClassName), nextra: len(fields)}
	if host.IsMap {
		label.tag = "[Map] "
	} else {
		label.tag = "[Set] "
	}
	label.push = func(list string) error {
		return e.inspectPushFields(list, v, info, fields, depth)
	}
	if host.IsMap {
		return e.emitInspectMapAs(bv, depth, label)
	}
	return e.emitInspectSetAs(bv, depth, label)
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
func hostEntriesAnnotation(cd *ast.ClassDeclaration) *ast.TypeAnnotation {
	arg := func(i int) *ast.TypeAnnotation {
		if i < len(cd.BaseTypeArgs) {
			return cd.BaseTypeArgs[i]
		}
		return &ast.TypeAnnotation{Name: "any"}
	}
	if len(cd.BaseTypeArgs) == 2 {
		return &ast.TypeAnnotation{Name: "[]", ElemType: &ast.TypeAnnotation{TupleElems: []*ast.TypeAnnotation{arg(0), arg(1)}}}
	}
	return &ast.TypeAnnotation{Name: arg(0).Name + "[]", ElemType: arg(0)}
}

// hostAnnotation is a class's Map or Set base as an annotation, its type
// arguments those of the extends clause (`any` where it has none).
func hostAnnotation(cd *ast.ClassDeclaration, host Type) *ast.TypeAnnotation {
	ta := &ast.TypeAnnotation{Name: cd.BaseClass}
	arg := func(i int) *ast.TypeAnnotation {
		if i < len(cd.BaseTypeArgs) {
			return cd.BaseTypeArgs[i]
		}
		return &ast.TypeAnnotation{Name: "any"}
	}
	switch {
	case host.IsRegExp, host.IsDate:
	case host.IsPromise:
		ta.ElemType = arg(0)
	case host.IsMap:
		ta.KeyType, ta.ElemType = arg(0), arg(1)
	default:
		ta.ElemType = arg(0)
	}
	return ta
}

// hostView is the iteration of expr when it is an instance of a class
// extending Map or Set without an iterator of its own: its host's default
// iterator, `expr.entries()` for a Map, `expr.values()` for a Set; nil
// otherwise.
func (e *Emitter) hostView(expr ast.Expression) ast.Expression {
	t := e.inferExprType(expr)
	if !t.IsClass {
		return nil
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || info.HostBase.IsRegExp || info.HostBase.IsPromise || info.HostBase.IsDate {
		return nil
	}
	if _, own := info.MethodSigs["@@iterator"]; own {
		return nil
	}
	method := "values"
	if info.HostBase.IsMap {
		method = "entries"
	}
	return ast.NewCallExpression(ast.NewMemberExpression(expr, method, expr.GetPos()), nil, expr.GetPos())
}

// emitHostBaseCall calls the host method of a class instance extending Map
// or Set on its body. Map.prototype.set and Set.prototype.add return their
// receiver: the instance.
func (e *Emitter) emitHostBaseCall(instance Value, host Type, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if host.IsPromise && (method == "then" || method == "catch" || method == "finally") {
		// The body settles to the instance's own value type, which a
		// species construction may have made other than the class's.
		host = PromiseOf(e.promiseSubclassValueType(instance.Ty))
		host.PromiseTask = true
		return e.emitPromiseSpecies(instance, host, method, args, pos)
	}
	if host.IsTypedArray {
		if typedArraySpeciesMethods[method] {
			return e.emitArraySpecies(instance, host, method, args, pos)
		}
	} else if host.IsArray {
		if _, ok := arraySpeciesMethods[method]; ok {
			return e.emitArraySpecies(instance, host, method, args, pos)
		}
	}
	recv := e.bindHostBody(instance, host, pos)
	call := ast.NewCallExpression(ast.NewMemberExpression(recv, method, pos), args, pos)
	if host.IsDate {
		// The bound body has no declaration to resolve Date's method by.
		if _, ok := intrinsics["Date.prototype."+method]; ok {
			call.Intrinsic = "Date.prototype." + method
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
			e.emitTerminator(fmt.Sprintf("ret ptr %s", v.Ref))
		}
		if !e.blockDone {
			e.emitTerminator("ret ptr null")
		}
	}
}

// hostTagName is the builtin tag of a host base (`[object RegExp]`).
func hostTagName(host Type) string {
	switch {
	case host.IsRegExp:
		return "RegExp"
	case host.IsDate:
		return "Date"
	case host.IsPromise:
		return "Promise"
	case host.IsMap:
		return "Map"
	}
	return "Set"
}

// emitInspectRegExpClass renders an instance of a class extending RegExp or
// Date as Node does: `R /a+b/g { tag: 'x' }`, `D 1970-01-01T00:00:00.000Z`,
// its host value then its own fields.
func (e *Emitter) emitInspectRegExpClass(v Value, info ClassInfo, depth int) (Value, error) {
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, v.Ref))
	re, err := e.emitInspectField(Value{Ref: body, Ty: *info.HostBase}, depth)
	if err != nil {
		return Value{}, err
	}
	out, err := e.emitStringConcat(Value{Ref: e.internString(ast.Unmangle(info.Ty.ClassName) + " "), Ty: TypePtr}, re)
	if err != nil {
		return Value{}, err
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
	t := e.inferExprType(args[0])
	if !t.IsClass {
		return args, nil
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || !info.HostBase.IsRegExp {
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
	if !hostBodyAt8(ty) || ty.Nullable {
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
	if len(args) != 1 {
		return fmt.Errorf("%d:%d: a Promise takes one executor argument", pos.Line, pos.Col)
	}
	el := TypeAny
	if host.PromiseType != nil {
		el = *host.PromiseType
	}
	_, err := e.emitPromiseExecutor(body, el, args[0], pos)
	return err
}

// promiseExecutorAnnotation is the type of the executor a class extending
// Promise<T> is constructed with: `(resolve: (value: T) => void, reject:
// (reason?: any) => void) => void`.
func promiseExecutorAnnotation(cd *ast.ClassDeclaration) *ast.TypeAnnotation {
	el := ast.TypeAnnotation{Name: "any"}
	if len(cd.BaseTypeArgs) == 1 {
		el = *cd.BaseTypeArgs[0]
	}
	void := &ast.TypeAnnotation{Name: "void"}
	resolve := ast.TypeAnnotation{IsFuncType: true, FuncParams: []ast.TypeAnnotation{el}, FuncRetType: void}
	reject := ast.TypeAnnotation{IsFuncType: true, FuncParams: []ast.TypeAnnotation{{Name: "any"}}, FuncParamOptional: []bool{true}, FuncRetType: void}
	return &ast.TypeAnnotation{IsFuncType: true, FuncParams: []ast.TypeAnnotation{resolve, reject}, FuncRetType: void}
}

// emitPromiseCapabilityThen is emitPromiseSpecies's `then` for a class whose
// constructor does not hand its executor straight to Promise's: the result
// is made as the spec's NewPromiseCapability(C) makes it — `new C(executor)`,
// the executor capturing the resolving functions C's constructor hands it —
// and settled by calling them from the reaction job, as PromiseReactionJob
// does, so a constructor wrapping them sees each call.
func (e *Emitter) emitPromiseCapabilityThen(instance Value, host Type, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	undef := Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
	res, rej := e.bindValue(undef, pos), e.bindValue(undef, pos)
	id := func(n string) *ast.Identifier { return ast.NewIdentifier(n, pos) }
	stmt := func(x ast.Expression) ast.Statement { return ast.NewExpressionStatement(x, pos) }
	call := func(f ast.Expression, a ...ast.Expression) ast.Expression { return ast.NewCallExpression(f, a, pos) }
	exec := ast.NewArrowFunction(
		[]ast.Param{{Name: "__kml_res", Type: &ast.TypeAnnotation{Name: "any"}}, {Name: "__kml_rej", Type: &ast.TypeAnnotation{Name: "any"}}},
		nil, nil, ast.NewBlockStatement([]ast.Statement{
			stmt(ast.NewAssignmentExpression("=", res, id("__kml_res"), pos)),
			stmt(ast.NewAssignmentExpression("=", rej, id("__kml_rej"), pos)),
		}, pos), pos)
	derived, err := e.emitExpr(ast.NewNewExpression(instance.Ty.ClassName, []ast.Expression{exec}, pos))
	if err != nil {
		return Value{}, err
	}
	onF, onR := ast.Expression(nil), ast.Expression(nil)
	switch method {
	case "then":
		if len(args) > 0 && !e.isNonCallableThenArg(args[0]) {
			onF = args[0]
		}
		if len(args) > 1 && !e.isNonCallableThenArg(args[1]) {
			onR = args[1]
		}
	case "catch":
		if len(args) > 0 && !e.isNonCallableThenArg(args[0]) {
			onR = args[0]
		}
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
			if rt, ok := e.callbackReturnType(cb, el); ok && (rt.IsPromise || e.isThenableType(rt)) {
				// A returned promise resolves the capability through its
				// `then`, from a job (NewPromiseResolveThenableJob).
				r := ast.NewVarDeclaration("const", "__kml_r", nil, call(f, id(param)), pos)
				job := ast.NewArrowFunction(nil, nil, call(ast.NewMemberExpression(call(id(thenableResolveName), id("__kml_r")), "then", pos), res, rej), nil, pos)
				settle = ast.NewBlockStatement([]ast.Statement{r, stmt(call(id(queueMicrotaskName), job))}, pos)
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
		if e.inferExprType(cb).IsDynamic {
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
		if e.inferExprType(cb).IsDynamic {
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
			if rt.IsPromise {
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
	rty := promiseSubclassOf(instance.Ty, pt)
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
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
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
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	name := ast.Unmangle(info.Ty.ClassName)
	if depth > e.effectiveInspectDepth() {
		return Value{Ref: e.internString("[" + name + " [Promise]]"), Ty: TypePtr}, nil
	}
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, v.Ref))
	bodyTy := PromiseOf(e.promiseSubclassValueType(v.Ty))
	bodyTy.PromiseTask = true
	boxed, err := e.emitBoxValue(Value{Ref: body, Ty: bodyTy})
	if err != nil {
		return Value{}, err
	}
	e.ensureDynJSONC()
	e.declareFn("__kml_promise_inspect_entry", "declare ptr @__kml_promise_inspect_entry(ptr, i64)")
	_, pay := e.emitUnboxTagPayload(boxed)
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", w, pay))
	entry := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_promise_inspect_entry(ptr %s, i64 %d)", entry, w, depth))
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
	if !e.promiseSpeciesPassThrough(instance.Ty.ClassName) {
		return e.emitPromiseCapabilityThen(instance, host, method, args, pos)
	}
	// `new C(executor)` runs C's constructors; their executor is Promise's,
	// so the reaction settles the derived promise as a plain then's does.
	noop := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement(nil, pos), pos)
	derived, err := e.emitExpr(ast.NewNewExpression(instance.Ty.ClassName, []ast.Expression{noop}, pos))
	if err != nil {
		return Value{}, err
	}
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, derived.Ref))
	thenArgs := args
	if method == "catch" {
		thenArgs = append([]ast.Expression{ast.NewNullLiteral(true, pos)}, args...)
	}
	// A callback held in `any` is called dynamically from a typed handler,
	// its result the reaction's `any` value.
	el := TypeAny
	if host.PromiseType != nil {
		el = *host.PromiseType
	}
	thenArgs = append([]ast.Expression(nil), thenArgs...)
	for i, a := range thenArgs {
		if i > 1 || e.isNonCallableThenArg(a) || !e.inferExprType(a).IsDynamic {
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
	rty := promiseSubclassOf(instance.Ty, q.Ty)
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
	if p.PromiseType != nil && !p.PromiseType.IsNever {
		t := *p.PromiseType
		c.PromiseType = &t
	}
	return c
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
	cls := id(instance.Ty.ClassName)
	// C.resolve(f()), with f's result when it has one.
	resolved := func() ast.Expression {
		if rt := e.inferExprType(call(f)); rt.IR == "void" || rt.IR == "" {
			return ast.NewSequenceExpression([]ast.Expression{call(f), call(ast.NewMemberExpression(cls, "resolve", pos))}, pos)
		}
		return call(ast.NewMemberExpression(cls, "resolve", pos), call(f))
	}
	el := e.promiseSubclassValueType(instance.Ty)
	thenFinally := ast.NewArrowFunction([]ast.Param{{Name: "__kml_fv", Type: e.synthTypeAnnotation(el)}}, nil,
		call(id(thenConstName), resolved(), id("__kml_fv"), ast.NewBooleanLiteral(false, pos)), nil, pos)
	catchFinally := ast.NewArrowFunction([]ast.Param{{Name: "__kml_fr", Type: e.synthTypeAnnotation(TypeCaught)}}, nil,
		call(id(thenConstName), resolved(), id("__kml_fr"), ast.NewBooleanLiteral(true, pos)), nil, pos)
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
	switch m {
	case "resolve", "reject", "all", "race", "allSettled", "any":
		return e.promiseStaticType(m, ex)
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
	capture := func(r, j ast.Expression, ty *ast.TypeAnnotation) *ast.ArrowFunction {
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
		derived, err := e.emitExpr(ast.NewNewExpression(className, []ast.Expression{capture(res, rej, &ast.TypeAnnotation{Name: "any"})}, pos))
		if err != nil {
			return "", nil, nil, err
		}
		return derived.Ref, res, rej, nil
	}
	noop := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement(nil, pos), pos)
	derived, err := e.emitExpr(ast.NewNewExpression(className, []ast.Expression{noop}, pos))
	if err != nil {
		return "", nil, nil, err
	}
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, derived.Ref))
	// The body's own resolving functions, typed by the value it settles to.
	var resolveParams []Type
	if valTy.IR != "void" && valTy.IR != "" {
		resolveParams = []Type{valTy}
	}
	resolveTy := FuncType(resolveParams, TypeVoid)
	if len(resolveParams) == 1 {
		resolveTy.IsPromiseResolver = true
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
	cty := promiseSubclassOf(e.classes[className].Ty, ty)
	call := func(f ast.Expression, a ...ast.Expression) ast.Expression { return ast.NewCallExpression(f, a, pos) }
	mark := func(inst string) { e.emitMarkBoxedPromiseValue(inst, cty) }
	switch m {
	case "resolve":
		if len(args) > 0 {
			if at := e.inferExprType(args[0]); at.IsClass && at.ClassName == className {
				// PromiseResolve(C, x) is x when its constructor is C.
				v, err := e.emitExpr(args[0])
				return v, true, err
			}
		}
		inst, res, _, err := e.emitPromiseCapability(className, el, pos)
		if err != nil {
			return Value{}, true, err
		}
		if _, err := e.emitExpr(call(res, args...)); err != nil {
			return Value{}, true, err
		}
		mark(inst)
		return Value{Ref: inst, Ty: cty}, true, nil
	case "reject":
		inst, _, rej, err := e.emitPromiseCapability(className, el, pos)
		if err != nil {
			return Value{}, true, err
		}
		if _, err := e.emitExpr(call(rej, args...)); err != nil {
			return Value{}, true, err
		}
		mark(inst)
		return Value{Ref: inst, Ty: cty}, true, nil
	}
	return e.emitPromiseSubclassCombinator(className, m, cty, el, args, pos)
}

// emitPromiseSubclassCombinator is `C.all`/`C.race`/`C.allSettled`/`C.any`
// for a class C extending Promise, as the spec's PerformPromiseAll (and
// its siblings) runs with C as the constructor: each member through
// `C.resolve(x).then(…)`, the result a C settled through its capability.
func (e *Emitter) emitPromiseSubclassCombinator(className, m string, cty, el Type, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	if len(args) != 1 {
		return Value{}, true, fmt.Errorf("%d:%d: %s.%s takes one iterable", pos.Line, pos.Col, ast.Unmangle(className), m)
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
	anyT := &ast.TypeAnnotation{Name: "any"}
	dec := func(n string) ast.Statement { return assign(id(n), ast.NewBinaryExpression("-", id(n), num("1"), pos)) }
	isZero := func(n string) ast.Expression { return ast.NewBinaryExpression("===", id(n), num("0"), pos) }
	// `C.resolve(x).then(onF, onR)` (emitResolveThenCall).
	resolveThen := func(onF, onR ast.Expression) ast.Expression {
		return call(id(resolveThenName), id(className), id("__kml_x"), onF, onR)
	}
	settledObj := func(status, key string, v ast.Expression) ast.Expression {
		return ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "status", Value: ast.NewStringLiteral(status, pos)}, {Key: key, Value: v}}, pos)
	}
	var loop []ast.Statement
	var pre, post []ast.Statement
	switch m {
	case "race":
		loop = []ast.Statement{stmt(resolveThen(res, rej))}
	case "all", "allSettled", "any":
		list, done := res, rej
		if m == "any" {
			list, done = rej, res
		}
		// The values array and each element as the result types them
		// (`number[]` for C.all over numbers); `any` for a tuple or a
		// rejection list.
		valsAnnot, elemAnnot := &ast.TypeAnnotation{Name: "any[]", ElemType: anyT}, anyT
		if m != "any" && el.IsArray && el.ElemType != nil && !el.IsTuple && !el.ElemType.IsDynamic {
			valsAnnot, elemAnnot = e.synthTypeAnnotation(el), e.synthTypeAnnotation(*el.ElemType)
		}
		pre = []ast.Statement{
			ast.NewVarDeclaration("const", "__kml_vals", valsAnnot, ast.NewArrayLiteral(nil, pos), pos),
			ast.NewVarDeclaration("let", "__kml_rem", nil, num("1"), pos),
			ast.NewVarDeclaration("let", "__kml_i", nil, num("0"), pos),
		}
		finish := func() ast.Statement {
			var out ast.Expression = id("__kml_vals")
			if m == "any" {
				agg := ast.NewNewErrorExpression("AggregateError", ast.NewStringLiteral("All promises were rejected", pos), pos)
				agg.Errors = id("__kml_vals")
				out = agg
			}
			return ast.NewIfStatement(isZero("__kml_rem"), block(stmt(call(list, out))), nil, pos)
		}
		elem := func(v ast.Expression) *ast.ArrowFunction {
			pt := elemAnnot
			var stmts []ast.Statement
			if m == "allSettled" {
				// A settled record, built as its element type has it (the
				// absent one of value/reason absent).
				pt = anyT
				stmts = append(stmts, ast.NewVarDeclaration("const", "__kml_o", elemAnnot, v, pos))
				v = id("__kml_o")
			}
			stmts = append(stmts, assign(ast.NewIndexExpression(id("__kml_vals"), id("__kml_k"), pos), v), dec("__kml_rem"), finish())
			return ast.NewArrowFunction([]ast.Param{{Name: "__kml_v", Type: pt}}, nil, nil, block(stmts...), pos)
		}
		var onF, onR ast.Expression
		switch m {
		case "all":
			onF, onR = elem(id("__kml_v")), done
		case "allSettled":
			onF = elem(settledObj("fulfilled", "value", id("__kml_v")))
			onR = elem(settledObj("rejected", "reason", id("__kml_v")))
		case "any":
			onF, onR = done, elem(id("__kml_v"))
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
	if at := e.inferExprType(args[0]); at.IsArray {
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", fp, inst, 8+promiseFlagsSlot*8))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", old, fp))
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", nw, old, promiseFlagBoxedValue))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nw, fp))
}

// emitBoxedPromiseValueTest is an i1: the body of class instance inst is
// flagged as settling to a boxed `any`.
func (e *Emitter) emitBoxedPromiseValueTest(inst string) string {
	fp, fl, m, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", fp, inst, 8+promiseFlagsSlot*8))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fl, fp))
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
	if !t.IsClass || t.IsDynamic {
		return Type{}, false
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || !info.HostBase.IsArray {
		return Type{}, false
	}
	return *info.HostBase, true
}

func isSpread(x ast.Expression) bool {
	_, ok := x.(*ast.SpreadElement)
	return ok
}

// isHostArrayMember reports a member of an instance of a class extending
// Array that is no field, method or accessor of the class: its array's.
func (e *Emitter) isHostArrayMember(ex *ast.MemberExpression) bool {
	t := e.inferExprType(ex.Object)
	if _, ok := e.arrayClassHost(t); !ok || t.Nullable {
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
	for _, name := range e.sortedClassNames(func(_ string, info ClassInfo) bool { return info.HostBase != nil && info.HostBase.IsArray }) {
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
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %%obj, null", isObj))
		objL, noL := e.freshLabel("arrcls.obj"), e.freshLabel("arrcls.no")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, noL))
		e.emitLabel(objL)
		tag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%obj, align 8", tag))
		for _, info := range classes {
			if _, own := info.MethodSigs[method]; own {
				continue
			}
			hit, next := e.freshLabel("arrcls.hit"), e.freshLabel("arrcls.next")
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, tag, e.objHeaderWord(info.Ty)))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hit, next))
			e.emitLabel(hit)
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%obj, i64 8", hdr))
			e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitBoxArrayPlain(e.arrayValueFromHeaderReg(hdr, *info.HostBase))))
			e.emitLabel(next)
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", noL))
		e.emitLabel(noL)
		e.emitTerminator("ret i64 0")
		body := e.allocas.String() + e.body.String()
		e.inLib, e.sharedCode = saved, savedShared
		restore()
		e.writeHelper("internal", "i64", fn, "ptr %obj", body)
	}
	tag, pay := e.emitUnboxTagPayload(v)
	isObj, p, obj := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, pay))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", obj, isObj, p))
	r, has, out := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s)", r, fn, obj))
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
		if info.HostBase != nil && info.HostBase.IsArray {
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", obj, hdr))
	if e.programIndependent() {
		// The program's test: without a class extending Array no header
		// is an instance's body, whatever its word.
		e.ensureArrayClassBodyTest()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_is_arrclass_body(ptr %s)", isClass, obj))
		return isClass, obj
	}
	word := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", word, obj))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", isClass, word, e.arrayHeaderWord()))
	return isClass, obj
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
		word, is := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%obj, align 8", word))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", is, word, e.arrayHeaderWord()))
		e.emitTerminator(fmt.Sprintf("ret i1 %s", is))
	}
	body := e.allocas.String() + e.body.String()
	e.inLib, e.sharedCode = saved, savedShared
	restore()
	e.writeHelper("internal", "i1", fn, "ptr %obj", body)
}

// arrayMayBeClassBody reports whether v, an array, may be the body of an
// instance of a class extending Array: the program declares one and v has
// a header (a fresh aggregate never is one).
func (e *Emitter) arrayMayBeClassBody(v Value) bool {
	return v.ArrayHeader != "" && !v.Ty.IsBuffer && !v.Ty.Nullable && e.hasArrayClass()
}

// emitBoxArrayClassObject boxes obj, an instance of some class extending
// Array, as that instance.
func (e *Emitter) emitBoxArrayClassObject(obj string) string {
	for _, name := range e.sortedClassNames(func(_ string, info ClassInfo) bool { return info.HostBase != nil && info.HostBase.IsArray }) {
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
	res, err := e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(recv, method, pos), args, pos))
	if err != nil {
		return Value{}, err
	}
	if !res.Ty.IsArray || e.blockDone {
		return res, nil
	}
	// A typed array's result is made at its length.
	setsLength := arraySpeciesMethods[method] || host.IsTypedArray
	// The constructors the instance may have: its class's and every class
	// extending it.
	classes := e.sortedClassNames(func(name string, _ ClassInfo) bool {
		return name == instance.Ty.ClassName || e.classExtends(name, instance.Ty.ClassName)
	})
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", tag, instance.Ref))
	return e.emitArrayClassFill(classes, tag, res, method == "map" || setsLength, setsLength, pos)
}

// emitArrayClassFill makes the instance a class extending Array makes for
// an Array result res, as ArraySpeciesCreate and Array.from/of do: `new
// C(n)` (n res's length when withLength, else no length), its elements then
// res's, the length then res's when setsLength. C is classes[0], or with
// tag (an instance's tag word) the one of classes it names. The value is
// typed as res, its header the instance's body.
func (e *Emitter) emitArrayClassFill(classes []string, tag string, res Value, withLength, setsLength bool, pos ast.Pos) (Value, error) {
	resultTy := res.Ty
	k := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", k, res.Ref))
	n := "0.0"
	if withLength {
		n = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", n, k))
	}
	resID := e.bindValue(res, pos)
	nID := e.bindValue(Value{Ref: n, Ty: TypeF64}, pos)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
	doneL := e.freshLabel("species.done")
	for i, name := range classes {
		info := e.classes[name]
		hitL, nextL := e.freshLabel("species.new"), e.freshLabel("species.next")
		if tag == "" || i == len(classes)-1 {
			e.emitTerminator(fmt.Sprintf("br label %%%s", hitL))
		} else {
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, tag, e.objHeaderWord(info.Ty)))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, hitL, nextL))
		}
		e.emitLabel(hitL)
		// A constructor whose first parameter cannot hold a number (a
		// `...items: string[]`) is called without the length: a native
		// string slot has no room for one. Its instance's elements are the
		// result's either way.
		var ctorArgs []ast.Expression
		if withLength && e.ctorTakesNumber(info) {
			ctorArgs = []ast.Expression{nID}
		}
		obj, err := e.emitExpr(ast.NewNewExpression(name, ctorArgs, pos))
		if err != nil {
			return Value{}, err
		}
		body := e.bindHostBody(obj, *info.HostBase, pos)
		// Its elements: the result's, over any the constructor made; a set
		// length then drops the rest.
		fill := ast.Expression(resID)
		if !setsLength {
			rest := ast.NewCallExpression(ast.NewMemberExpression(body, "slice", pos), []ast.Expression{ast.NewMemberExpression(resID, "length", pos)}, pos)
			fill = ast.NewCallExpression(ast.NewMemberExpression(resID, "concat", pos), []ast.Expression{rest}, pos)
		}
		fv, err := e.emitExpr(fill)
		if err != nil {
			return Value{}, err
		}
		fv = e.coerce(fv, *info.HostBase)
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", hdr, obj.Ref))
		e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", fv.Ref, hdr))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, out))
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
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, out))
	return e.coerce(e.arrayValueFromHeaderReg(hdr, *e.classes[classes[0]].HostBase), resultTy), nil
}

// emitArrayClassStatic is Array's static method on a class extending
// Array: from and of make an instance of the class (`new C()` for from,
// `new C(n)` for of, then its elements); isArray is Array's.
func (e *Emitter) emitArrayClassStatic(className, method string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	if method != "from" && method != "of" && method != "isArray" {
		return Value{}, false, nil
	}
	var call *ast.CallExpression
	if host := e.classes[className].HostBase; host.IsTypedArray {
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
	if err != nil || method == "isArray" || !res.Ty.IsArray || e.blockDone {
		return res, true, err
	}
	typed := e.classes[className].HostBase.IsTypedArray
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
		if info.HostBase == nil || !info.HostBase.IsArray {
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
	rt := e.inferExprType(mem.Object)
	if !rt.IsArray || rt.IsTypedArray || rt.IsBuffer || rt.IsTuple || rt.IsFlatArray || rt.Nullable || rt.IsDynamic {
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
	if recv.ArrayHeader == "" {
		// A fresh array is no instance's body.
		v, err := e.withArrayOverrideDispatchOff(func() (Value, error) {
			return emit(e, ast.NewMemberExpression(e.bindValue(recv, pos), name, pos), ex.Args, pos)
		})
		return v, true, err
	}
	hdr := e.nullSafeArrayHeader(recv.ArrayHeader)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, slot))
	rid := fmt.Sprintf("__kml_ovrecv_%s", slot[1:])
	e.define(rid, Symbol{Ptr: slot, Ty: recv.Ty})
	resTy := e.inferExprType(ex)
	var out string
	if resTy.IR != "" && resTy.IR != "void" {
		out = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", out, resTy.IR))
	}
	arrRes := resTy.IsArray && resTy.IR == "ptr" // crosses as its header
	obj, word := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", obj, hdr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", word, obj))
	doneL := e.freshLabel("arrov.done")
	failAt := e.arrovFail
	finish := func(v Value) {
		if e.blockDone {
			return
		}
		if out != "" {
			v = e.coerce(v, resTy)
			ref := v.Ref
			if arrRes {
				ref = v.ArrayHeader
				if ref == "" {
					ref = e.boxArrayValue(v)
				}
			}
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", resTy.IR, ref, out))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	for _, name2 := range classes {
		info := e.classes[name2]
		hitL, nextL := e.freshLabel("arrov.cls"), e.freshLabel("arrov.next")
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, word, e.objHeaderWord(info.Ty)))
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
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, resTy.IR, out))
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
