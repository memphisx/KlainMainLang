package llvm

// emit_lowered.go — the generic call path of TDD-00230 P3.2. A builtin
// declaration (lib/*.d.ts) may name the runtime entry point a call compiles
// to, `/** @lower sin @link m */ sin(x: number): number`; such a call is
// emitted from the declaration alone: each argument marshalled to its
// declared parameter's representation, the entry point declared once from
// the signature, the result given its declared type's representation. A
// call through a declaration without @lower (or with a shape this path does
// not represent yet) falls through to the hand-written emitters.

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
)

// front is the checker over the program being emitted, built on first use.
// Nil when the builtin declarations do not parse (the strict lane reports
// that first).
func (e *Emitter) front() *checker.Checker {
	if e.frontBuilt {
		return e.frontChecker
	}
	e.frontBuilt = true
	if e.prog == nil {
		return nil
	}
	libProgs, err := lib.Programs()
	if err != nil {
		return nil
	}
	b := binder.BindWith(e.prog, binder.Options{AnnexB: e.compatJS(), Lib: libProgs})
	e.frontChecker = checker.NewWith(b, e.opts)
	return e.frontChecker
}

// lowering is a call resolved to a declaration with an @lower target.
type lowering struct {
	// intrinsic names the inline emitter of an `@intrinsic` declaration
	// (intrinsics); the rest is unused then.
	intrinsic string
	e         *Emitter
	ex        *ast.CallExpression
	target    string
	links     []string
	// recv is the receiver's representation for a method of a primitive's
	// apparent type (`s.trim()`), passed as the first argument; nil for a
	// method of a global object (`Math.sin(x)`).
	recv *Type
	// libFn is the TypeScript builtin library's function a `@lower` target
	// names (a global module's export, by its linked name): the call is an
	// ordinary call of it, the receiver (if any) its first argument.
	libFn  string
	call   *ast.CallExpression
	params []loweredParam
	result Type
	// resultOptional marks a `T | undefined` result: the entry point
	// returns whether it is present and writes the value through a final
	// pointer argument (a C struct return has no portable IR form).
	resultOptional bool
}

// resultType is the type a lowered call's value has.
func (l *lowering) resultType() Type {
	if l.intrinsic != "" {
		return intrinsics[l.intrinsic].ty(l.e, l.ex)
	}
	if l.libFn != "" {
		return l.e.inferExprType(l.libCall())
	}
	if l.resultOptional {
		return undefinedableElem(l.result)
	}
	return l.result
}

// sharedLibLowering is the linked TypeScript function every declaration of
// a method (its overloads, or its one declaration) names as its `@lower`.
func (e *Emitter) sharedLibLowering(decl ast.Node, overloads []ast.Node) (string, bool) {
	decls := overloads
	if len(decls) == 0 {
		if decl == nil {
			return "", false
		}
		decls = []ast.Node{decl}
	}
	target := ""
	for _, d := range decls {
		ms, ok := d.(*ast.MethodSignature)
		if !ok || ms.Lower == "" || target != "" && ms.Lower != target {
			return "", false
		}
		target = ms.Lower
	}
	m, ok := e.globalLinks[target]
	return m, ok
}

// libCall is a library-lowered call as the call of its function (built
// once, so inference and emission see one node).
func (l *lowering) libCall() *ast.CallExpression {
	if l.call == nil {
		ex := l.ex
		args := ex.Args
		if l.recv != nil {
			args = append([]ast.Expression{ex.Callee.(*ast.MemberExpression).Object}, args...)
		}
		l.call = ast.NewCallExpression(ast.NewIdentifier(l.libFn, ex.GetPos()), args, ex.GetPos())
	}
	return l.call
}

// loweredParam is one declared parameter: its representation, and whether
// it is optional. An optional parameter is passed as two arguments, an i1
// presence flag and the value (zero when absent), so a runtime entry point
// tells an omitted argument from any value of its type.
type loweredParam struct {
	ty       Type
	optional bool
	// rest marks a rest parameter (`...strings: string[]`): the remaining
	// arguments, converted, are passed as one fresh array's header.
	rest bool
	// bytes marks a `Uint8Array` parameter, passed as its data pointer and
	// its byte length: memory a native operation reads or fills in place.
	bytes bool
	// callback is a function parameter's own parameters' representations:
	// the argument is passed as an invoker and the closure, and the runtime
	// calls invoker(closure, args…) on the loop thread.
	callback []Type
}

// loweredCall resolves a call through a builtin declaration's @lower
// target: a method of a builtin global object (`Math.sin(x)`), the global
// not shadowed by the program, or of a string (`s.trim()`). It is the one
// decider both emission and inferExprType consult. It is not memoised: it
// reads codegen's scope (shadowing, the receiver's representation), which
// inferExprType may consult before a callback's parameters are bound.
// callSignatureIntrinsic is the intrinsic every call signature of an
// interface type names, or "" when one names none or they differ.
func callSignatureIntrinsic(t *checker.Type) string {
	if t == nil || t.Symbol == nil {
		return ""
	}
	key := ""
	for _, d := range t.Symbol.Declarations {
		decl, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok {
			continue
		}
		for _, m := range decl.Members {
			cs, ok := m.(*ast.CallSignature)
			if !ok {
				continue
			}
			if cs.Intrinsic == "" || key != "" && cs.Intrinsic != key {
				return ""
			}
			key = cs.Intrinsic
		}
	}
	if _, known := intrinsics[key]; !known {
		return ""
	}
	return key
}

func (e *Emitter) loweredCall(ex *ast.CallExpression) (*lowering, bool) {
	if l, ok := e.loweredCallDecl(ex); ok {
		return l, true
	}
	// A call shape its declaration's lowering does not take (an argument
	// no overload accepts under -compat=js, a spread of an `any`, a method
	// declared without a key): the method of the owner codegen's
	// representation of the receiver names, whose emitter takes every form.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && !ex.Optional && !mem.Optional {
		if c := e.front(); c != nil && e.isLibMember(c.MemberDecl(mem)) {
			if key, ok := e.reprIntrinsic(mem); ok {
				return &lowering{intrinsic: key, e: e, ex: ex}, true
			}
		}
	}
	return nil, false
}

// isLibMember reports whether d is a member a builtin declaration file
// declares (an interface's method), not the program's own.
func (e *Emitter) isLibMember(d ast.Node) bool {
	if d == nil {
		return false
	}
	if e.libMembers == nil {
		e.libMembers = map[ast.Node]bool{}
		if progs, err := lib.Programs(); err == nil {
			for _, p := range progs {
				ast.Inspect(p, func(n ast.Node) bool {
					if ms, ok := n.(*ast.MethodSignature); ok {
						e.libMembers[ms] = true
					}
					return true
				})
			}
		}
	}
	return e.libMembers[d]
}

func (e *Emitter) loweredCallDecl(ex *ast.CallExpression) (*lowering, bool) {
	if _, known := intrinsics[ex.Intrinsic]; known {
		return &lowering{intrinsic: ex.Intrinsic, e: e, ex: ex}, true // synthesized
	}
	if ex.Optional {
		return nil, false
	}
	c := e.front()
	if c == nil {
		return nil, false
	}
	if id, ok := ex.Callee.(*ast.Identifier); ok && e.isBuiltinGlobal(c, id) {
		// A builtin `declare function` (`parseInt`).
		if sym, _ := c.Binding().Resolve(id); sym != nil {
			for _, d := range sym.Declarations {
				if fd, ok := d.Node.(*ast.FunctionDeclaration); ok && fd.Intrinsic != "" {
					if _, known := intrinsics[fd.Intrinsic]; known {
						return &lowering{intrinsic: fd.Intrinsic, e: e, ex: ex}, true
					}
				}
			}
		}
		// A builtin constructor called as a function (`String(x)`): its
		// interface's call signatures, when they all name one intrinsic.
		if key := callSignatureIntrinsic(c.TypeOf(id)); key != "" {
			return &lowering{intrinsic: key, e: e, ex: ex}, true
		}
		return nil, false
	}
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok || mem.Optional {
		return nil, false
	}
	if key, ok := e.methodIntrinsic(c, mem); ok {
		// An intrinsic's emitter reads its types off the arguments; explicit
		// type arguments (`Object.freeze<T>(o)`) change nothing.
		return &lowering{intrinsic: key, e: e, ex: ex}, true
	}
	if c.MemberDecl(mem) == nil && c.MemberOverloadDecls(mem) == nil {
		if key, ok := e.reprIntrinsic(mem); ok {
			return &lowering{intrinsic: key, e: e, ex: ex}, true
		}
	}
	if len(ex.TypeArgs) > 0 {
		return nil, false
	}
	var recv *Type
	var decl ast.Node
	var overloads []ast.Node
	var fn *checker.Type
	if e.isBuiltinGlobalObject(c, mem.Object) {
		// A method of a builtin global object (`Math.sin`; a primitive
		// global such as `NaN` is a receiver instead).
		decl, overloads, fn = c.MemberDecl(mem), c.MemberOverloadDecls(mem), c.TypeOf(mem)
	} else if r, known, ok := e.loweredReceiver(c, mem.Object); ok {
		recv = &r
		if known {
			decl, overloads, fn = c.MemberDecl(mem), c.MemberOverloadDecls(mem), c.TypeOf(mem)
		} else {
			// A value codegen holds as a string or number that the checker
			// cannot type (an untyped value under -compat=js): the apparent
			// type's member.
			fn, decl, overloads = c.PrimitiveMember(r.IR == "ptr", mem.Property)
		}
	} else if d, ov := c.MemberDecl(mem), c.MemberOverloadDecls(mem); e.isLibMember(d) || len(ov) > 0 && e.isLibMember(ov[0]) {
		// A builtin interface's method on an object (an iterator's
		// `every`) lowered to a TypeScript library function: the receiver
		// is its first argument.
		if m, ok := e.sharedLibLowering(d, ov); ok {
			r := TypeAny
			return &lowering{libFn: m, recv: &r, e: e, ex: ex}, true
		}
		return nil, false
	} else {
		return nil, false
	}
	if m, ok := e.sharedLibLowering(decl, overloads); ok {
		// Every overload lowers to one TypeScript function: which the call
		// resolves to does not matter.
		return &lowering{libFn: m, recv: recv, e: e, ex: ex}, true
	}
	if fn == nil || c.Unanswered(fn) || fn.Flags&checker.Object == 0 || fn.Kind != checker.Function {
		return nil, false
	}
	if len(fn.Overloads) > 0 {
		// An overloaded method: the overload the call resolves to, with its
		// own declaration's lowering (one entry point per overload).
		i := c.OverloadIndex(ex, fn)
		if i < 0 || len(overloads) != len(fn.Overloads) {
			return nil, false
		}
		decl, fn = overloads[i], fn.Overloads[i]
	}
	ms, ok := decl.(*ast.MethodSignature)
	if ok && ms.Intrinsic != "" {
		if e.intrinsicApplies(ms.Intrinsic, mem) {
			return &lowering{intrinsic: ms.Intrinsic, e: e, ex: ex}, true
		}
		return nil, false // the named path, which rejects what the intrinsic cannot take
	}
	if ok && ms.Lower != "" {
		if m, linked := e.globalLinks[ms.Lower]; linked {
			return &lowering{libFn: m, recv: recv, e: e, ex: ex}, true
		}
	}
	if !ok || ms.Lower == "" || len(ms.TypeParameters) > 0 {
		return nil, false
	}
	l := &lowering{target: ms.Lower, links: ms.Link, recv: recv}
	if len(ms.Parameters) != len(fn.Params) {
		return nil, false
	}
	for i, pt := range fn.Params {
		p := ms.Parameters[i]
		if p.Rest {
			// The remaining arguments, as a string[] (the one element
			// representation an array crosses the boundary in so far).
			r, ok := lowerRepr(pt)
			if !ok || !r.IsArray || i != len(fn.Params)-1 {
				return nil, false
			}
			l.params = append(l.params, loweredParam{ty: r, rest: true})
			continue
		}
		if p.Optional {
			pt = c.NonUndefined(pt)
		}
		if cb, ok := lowerCallback(pt); ok && !p.Optional {
			l.params = append(l.params, loweredParam{callback: cb})
			continue
		}
		if isBytesType(pt) && !p.Optional {
			l.params = append(l.params, loweredParam{bytes: true})
			continue
		}
		r, ok := lowerRepr(pt)
		if !ok || r.IR == "void" || r.IsArray {
			return nil, false // an array argument has no lowering yet
		}
		l.params = append(l.params, loweredParam{ty: r, optional: p.Optional})
	}
	for i, a := range ex.Args {
		sp, ok := a.(*ast.SpreadElement)
		if !ok {
			continue
		}
		// A spread into the rest parameter (`s.concat(...parts)`) of an
		// array whose elements already have the rest's representation.
		n := len(l.params)
		if n == 0 || !l.params[n-1].rest || i < n-1 {
			return nil, false
		}
		at := e.inferExprType(sp.Arg)
		if !at.IsArray || at.ElemType == nil || at.ElemType.IR != l.params[n-1].ty.ElemType.IR ||
			at.ElemType.IsDynamic || at.ElemType.Nullable || isUnconstrainedDynamic(at) {
			return nil, false
		}
	}
	res := fn.Result
	if u := c.NonUndefined(res); u != res && c.MaybeUndefined(res) && res.Flags&checker.Union != 0 {
		res, l.resultOptional = u, true // `T | undefined`
	}
	if l.result, ok = lowerRepr(res); !ok || l.resultOptional && (l.result.IR == "void" || l.result.IsArray) {
		return nil, false
	}
	return l, true
}

// methodIntrinsic is the intrinsic a method call's declaration names
// (`arr.map(f)` through Array's `/** @intrinsic Array.prototype.map */`),
// whatever declares it. A receiver the checker cannot type (or types `any`)
// has no declaration: it takes the named paths.
func (e *Emitter) methodIntrinsic(c *checker.Checker, mem *ast.MemberExpression) (string, bool) {
	if ovs := c.MemberOverloadDecls(mem); ovs != nil {
		// An overloaded method whose overloads all name one intrinsic
		// (`JSON.stringify`): its emitter takes every form. Otherwise the
		// overload the call resolves to decides.
		key := ""
		for _, d := range ovs {
			k := ""
			switch d := d.(type) {
			case *ast.MethodSignature:
				k = d.Intrinsic
			case *ast.FunctionDeclaration:
				k = d.Intrinsic // a namespace's function (`Reflect.apply`)
			}
			if k == "" || key != "" && k != key {
				return "", false
			}
			key = k
		}
		if !e.intrinsicApplies(key, mem) {
			return "", false
		}
		return key, true
	}
	decl := c.MemberDecl(mem)
	key := ""
	switch d := decl.(type) {
	case *ast.MethodSignature:
		key = d.Intrinsic
	case *ast.FunctionDeclaration:
		key = d.Intrinsic // `globalThis.parseInt`
	}
	if !e.intrinsicApplies(key, mem) {
		return "", false
	}
	return key, true
}

// intrinsicApplies reports whether a method's intrinsic takes this call: it
// is known, and an Array one's inline loops read a regular array — a
// `@value` array, a bigint typed array's unsupported method, or a receiver
// codegen holds as something else take the named paths, which reject them.
func (e *Emitter) intrinsicApplies(key string, mem *ast.MemberExpression) bool {
	if _, known := intrinsics[key]; !known {
		return false
	}
	if owner, _, ok := strings.Cut(key, ".prototype."); ok && collectionIntrinsics[owner] != nil {
		// Codegen must hold the receiver as that collection (a dictionary
		// keeps its own path).
		ot := e.inferExprType(mem.Object)
		weak := owner == "WeakMap" || owner == "WeakSet"
		return (ot.IsMap || ot.IsSet) && ot.Weak == weak && !ot.IsDynamicObject &&
			!isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "RegExp.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsRegExp && !ot.IsDynamic
	}
	if strings.HasPrefix(key, "Function.prototype.") {
		// A -compat=js prototype constructor's Base.call(this, …) runs it on
		// the receiver object (emitCall's jsProtoCtor path).
		if id, ok := mem.Object.(*ast.Identifier); ok && e.compatJS() && e.jsProtoCtor[id.Name] {
			return false
		}
		ot := e.inferExprType(mem.Object)
		return ot.IsFunc && !ot.IsDynamic
	}
	if strings.HasPrefix(key, "Promise.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsPromise && !ot.IsDynamic
	}
	if strings.HasPrefix(key, "Date.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsDate && !ot.IsDynamic
	}
	if strings.HasPrefix(key, "TypedArray.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsTypedArray && !isUnconstrainedDynamic(ot)
	}
	if owner, _, ok := strings.Cut(key, ".prototype."); ok {
		if held := ownerReprs[owner]; held != nil {
			ot := e.inferExprType(mem.Object)
			return held(ot) && !isUnconstrainedDynamic(ot)
		}
	}
	switch key {
	case "Object.prototype.toString":
		ot := e.inferExprType(mem.Object)
		if isNumberTy(ot) && ot.IR != "i1" {
			// A number reaches Object's toString only through a declaration
			// that names it, or as a union with a boolean (whose members
			// TypeScript requires to take no argument); an undeclared one
			// takes Number.prototype.toString(radix)'s lowering.
			c := e.front()
			if c == nil || isUnconstrainedDynamic(ot) {
				return false
			}
			t := c.TypeOf(mem.Object)
			return c.MemberDecl(mem) != nil || !c.Unanswered(t) && t.Flags&checker.Union != 0
		}
		return objectProtoToStringApplies(ot)
	case "Object.prototype.hasOwnProperty":
		ot := e.inferExprType(mem.Object)
		return ot.IsObject && !isUnconstrainedDynamic(ot)
	case "BigInt.prototype.toString":
		return e.inferExprType(mem.Object).IsBigInt
	}
	if owner, _, ok := strings.Cut(key, ".prototype."); ok && strings.Contains(owner, "Stream") {
		ot := e.inferExprType(mem.Object)
		if strings.HasPrefix(owner, "Writable") {
			return ot.IsWritableStream || ot.IsStreamWriter || ot.IsWSController
		}
		return ot.IsReadableStream || ot.IsStreamReader || ot.IsRSController
	}
	if strings.HasPrefix(key, "Body.prototype.") {
		ot := e.inferExprType(mem.Object)
		return hasBodyMixin(ot) && !isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "XMLHttpRequest.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsXHR && !isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "Buffer.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsBuffer && !isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "Array.prototype.") {
		ot := e.inferExprType(mem.Object)
		if !ot.IsArray || isUnconstrainedDynamic(ot) || ot.IsFlatArray || ot.BigIntElem && bigIntElemRejectedMethods[mem.Property] {
			return false
		}
	}
	return true
}

// isBuiltinGlobal reports whether id names a builtin global object, not a
// program binding that shadows it.
func (e *Emitter) isBuiltinGlobal(c *checker.Checker, id *ast.Identifier) bool {
	if e.isShadowedByLocal(id.Name) {
		return false
	}
	b := c.Binding()
	sym, _ := b.Resolve(id)
	return sym != nil && b.Globals != nil && sym.Scope == b.Globals
}

// isBuiltinGlobalObject reports whether x is a builtin global object
// (`Math`, or `globalThis.Math`) rather than a value of a primitive.
func (e *Emitter) isBuiltinGlobalObject(c *checker.Checker, x ast.Expression) bool {
	switch x := x.(type) {
	case *ast.Identifier:
		if x.Name == "globalThis" {
			return !e.isShadowedByLocal("globalThis") // the global object itself
		}
		if !e.isBuiltinGlobal(c, x) {
			return false
		}
		// A builtin namespace (`Reflect`) has no value type of its own.
		if sym, _ := c.Binding().Resolve(x); sym != nil && sym.Flags&binder.NamespaceModule != 0 {
			return true
		}
	case *ast.MemberExpression:
		id, ok := x.Object.(*ast.Identifier)
		if !ok || id.Name != "globalThis" || e.isShadowedByLocal("globalThis") || x.Optional {
			return false
		}
	default:
		return false
	}
	t := c.TypeOf(x)
	return !c.Unanswered(t) && t.Flags&checker.Object != 0
}

// loweredReceiver is the representation a method's receiver is passed in,
// when the receiver is a string or a number (a primitive whose apparent type,
// String or Number, declares the method) that codegen holds as one too.
// known reports whether the checker types the receiver itself; when it cannot
// answer (or answers any), the member is the apparent type's.
//
// A possibly-absent receiver (`string | null`) is one too: emitCall guards
// every method call's receiver, throwing Node's TypeError when it is absent.
func (e *Emitter) loweredReceiver(c *checker.Checker, obj ast.Expression) (ty Type, known, ok bool) {
	if id, ok := obj.(*ast.Identifier); ok && c.Binding().Program.BuiltinMarkers[id.Name] != "" {
		return Type{}, false, false // a builtin module (`ffi.toString`), not a value
	}
	ct := e.inferExprType(obj)
	// A possibly-absent number (a typed-array element read) is one too:
	// emitCall's receiver guard throws on an absent one.
	number := isNumberTy(ct) && ct.IR != "i1" && !ct.IsDate && !ct.IsBigInt && !ct.IsDynamic
	ct.Nullable, ct.IsUndefined = false, false
	if !isForOfStringTy(ct) && !number {
		return Type{}, false, false
	}
	repr, kind := TypePtr, checker.String|checker.StringLiteral
	if number {
		repr, kind = TypeF64, checker.Number|checker.NumberLiteral
	}
	t := c.TypeOf(obj)
	if !c.Unanswered(t) {
		t = c.NonNullable(t)
	}
	switch {
	case c.Unanswered(t), t.Flags&checker.Any != 0:
		return repr, false, true
	case t.Flags&checker.Union == 0 && t.Flags&kind != 0:
		return repr, true, true
	}
	return Type{}, false, false
}

// isUndefinedLiteral reports whether a is the global `undefined`.
func isUndefinedLiteral(e *Emitter, a ast.Expression) bool {
	if nl, ok := a.(*ast.NullLiteral); ok {
		return nl.IsUndefined && !nl.Void // `undefined` as parsed; `void x` evaluates x
	}
	id, ok := a.(*ast.Identifier)
	return ok && id.Name == "undefined" && !e.isShadowedByLocal("undefined")
}

// lowerRepr is the representation a declared type has at a lowered call's
// boundary; the types this path represents so far.
func lowerRepr(t *checker.Type) (Type, bool) {
	switch {
	case t.Flags&checker.Any != 0:
		return TypeAny, true // a NaN-boxed word
	case t.Flags&checker.Union != 0:
		return Type{}, false
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
		return TypeF64, true
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return TypeBool, true
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return TypePtr, true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array && t.Elem != nil &&
		t.Elem.Flags&checker.Union == 0 && t.Elem.Flags&(checker.String|checker.StringLiteral) != 0:
		// string[]: an entry point returns the array's header pointer.
		return ArrayOf(TypePtr), true
	case t.Flags&checker.Void != 0:
		return TypeVoid, true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && t.Symbol.Name == "Error":
		return errorObjType, true
	}
	return Type{}, false
}

// lowerCallback is the parameters' representations of a callback parameter's
// function type: one signature returning void, each parameter a scalar the
// runtime passes (a number, a boolean, a string, an `any` word).
func lowerCallback(t *checker.Type) ([]Type, bool) {
	if t.Flags&checker.Object == 0 || t.Kind != checker.Function || len(t.Overloads) > 0 || len(t.TypeParams) > 0 {
		return nil, false
	}
	if t.Result == nil || t.Result.Flags&checker.Void == 0 {
		return nil, false
	}
	params := []Type{}
	for _, pt := range t.Params {
		if pt.Flags&checker.Any != 0 {
			params = append(params, TypeAny) // a NaN-boxed word
			continue
		}
		r, ok := lowerRepr(pt)
		if !ok || r.IR == "void" || r.IsArray || r.IsError {
			return nil, false
		}
		params = append(params, r)
	}
	return params, true
}

// isBytesType reports whether t is `Uint8Array` (or `Buffer`, one): memory
// a native operation reads or fills in place.
func isBytesType(t *checker.Type) bool {
	if t.Flags&checker.Object == 0 || (t.Kind != checker.Interface && t.Kind != checker.Instance) || t.Symbol == nil {
		return false
	}
	return t.Symbol.Name == "Uint8Array" || t.Symbol.Name == "Buffer"
}

// emitLowered emits a lowered call: the receiver, then the arguments in
// order (an extra one is evaluated for its effects and dropped, as a
// JavaScript callee ignores it), then one call of the entry point. Each
// argument is converted as JavaScript converts it for the declared type
// (ToNumber, ToString); a missing required number is NaN, ToNumber(undefined).
func (e *Emitter) emitLowered(ex *ast.CallExpression, l *lowering) (Value, error) {
	if l.intrinsic != "" {
		return intrinsics[l.intrinsic].emit(e, ex)
	}
	mem := ex.Callee.(*ast.MemberExpression)
	if l.libFn != "" {
		return e.emitExpr(l.libCall())
	}
	var args []string
	if l.recv != nil {
		v, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		v = e.coerce(v, *l.recv)
		args = append(args, fmt.Sprintf("%s noundef %s", paramIR(*l.recv), v.Ref))
	}
	restAt := -1
	if n := len(l.params); n > 0 && l.params[n-1].rest {
		restAt = n - 1
	}
	var rest []string
	restSpread := false
	for i, a := range ex.Args {
		if _, ok := a.(*ast.SpreadElement); ok && restAt >= 0 && i >= restAt {
			restSpread = true
		}
	}
	for i, a := range ex.Args {
		if restSpread && i >= restAt {
			continue // the rest arguments, spreads among them, as one array below
		}
		if restAt >= 0 && i >= restAt {
			v, err := e.emitExpr(a)
			if err != nil {
				return Value{}, err
			}
			if v, err = e.lowerArg(v, *l.params[restAt].ty.ElemType); err != nil {
				return Value{}, err
			}
			rest = append(rest, v.Ref)
			continue
		}
		if i < len(l.params) && l.params[i].bytes {
			ptr, n, elemTy, err := e.resolveArrayForHOF(a, a.GetPos())
			if err != nil {
				return Value{}, err
			}
			size, err := e.emitTypedArrayByteLength(n, elemTy)
			if err != nil {
				return Value{}, err
			}
			args = append(args, "ptr noundef "+ptr, "i64 noundef "+size.Ref)
			continue
		}
		if i < len(l.params) && l.params[i].callback != nil {
			cb, err := e.resolveCallbackWithHints(a, l.params[i].callback)
			if err != nil {
				return Value{}, err
			}
			if cb.kind != cbClosure {
				return Value{}, fmt.Errorf("%d:%d: a native callback must be a function value", a.GetPos().Line, a.GetPos().Col)
			}
			inv, err := e.emitLoweredInvoker(cb.ty, l.params[i].callback)
			if err != nil {
				return Value{}, err
			}
			args = append(args, "ptr noundef "+inv, "ptr noundef "+cb.hdrPtr)
			continue
		}
		if i < len(l.params) && l.params[i].optional && isUndefinedLiteral(e, a) {
			args = append(args, "i1 zeroext false", l.params[i].ty.IR+" "+zeroOf(l.params[i].ty))
			continue
		}
		var v Value
		var err error
		if i < len(l.params) && l.params[i].optional {
			// A `T | undefined` variable keeps its presence bit.
			v, err = e.emitPreserveNullableOperand(a)
		} else {
			v, err = e.emitExpr(a)
		}
		if err != nil {
			return Value{}, err
		}
		if i >= len(l.params) {
			continue
		}
		p := l.params[i]
		present := "true"
		if p.optional {
			present, v = e.optionalPresence(v)
		}
		if v, err = e.lowerArg(v, p.ty); err != nil {
			return Value{}, err
		}
		if p.optional {
			args = append(args, "i1 zeroext "+present)
		}
		args = append(args, fmt.Sprintf("%s noundef %s", paramIR(p.ty), v.Ref))
	}
	for i := len(ex.Args); i < len(l.params); i++ {
		p := l.params[i]
		switch {
		case p.rest:
		case p.bytes || p.callback != nil:
			return Value{}, fmt.Errorf("%d:%d: missing argument %d", ex.GetPos().Line, ex.GetPos().Col, i+1)
		case p.optional:
			args = append(args, "i1 zeroext false", p.ty.IR+" "+zeroOf(p.ty))
		case p.ty.Float:
			args = append(args, "double noundef 0x7FF8000000000000")
		default:
			return Value{}, fmt.Errorf("%d:%d: missing argument %d", ex.GetPos().Line, ex.GetPos().Col, i+1)
		}
	}
	if restSpread {
		// The remaining arguments as an array literal: its spreads copy their
		// arrays' elements in order.
		lit := ast.NewArrayLiteral(ex.Args[restAt:], ex.GetPos())
		v, err := e.emitExprWithObjectHint(lit, l.params[restAt].ty)
		if err != nil {
			return Value{}, err
		}
		args = append(args, "ptr noundef "+e.arrayReturnHeader(v))
	} else if restAt >= 0 {
		args = append(args, "ptr noundef "+e.restArray(rest))
	}
	var slot string
	if l.resultOptional {
		slot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s", slot, l.result.IR))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s", l.result.IR, zeroOf(l.result), slot))
		args = append(args, "ptr noundef "+slot)
	}
	e.declareLowered(l)
	if l.resultOptional {
		present, v := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @%s(%s)", present, l.target, strings.Join(args, ", ")))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s", v, l.result.IR, slot))
		return e.wrapUndefinedable(Value{Ref: v, Ty: l.result}, present), nil
	}
	if l.result.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", l.target, strings.Join(args, ", ")))
		return Value{Ty: TypeVoid}, nil
	}
	if l.result.IsArray {
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @%s(%s)", h, l.target, strings.Join(args, ", ")))
		return e.arrayValueFromHeaderReg(h, l.result), nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", r, resultIR(l.result), l.target, strings.Join(args, ", ")))
	return Value{Ref: r, Ty: l.result}, nil
}

// emitLoweredInvoker emits `void (ptr %clo, params…)` and returns its
// symbol: the runtime's typed caller of a callback passed to a lowered entry
// point, which converts each value the runtime passes to the closure's own
// parameter representation. Named by its content (defineContentNamed).
func (e *Emitter) emitLoweredInvoker(fnTy Type, params []Type) (string, error) {
	restore := e.beginThunkEmit()
	var sig []string
	var args []Value
	for i, p := range params {
		reg := fmt.Sprintf("%%a%d", i)
		sig = append(sig, paramIR(p)+" "+reg)
		args = append(args, Value{Ref: reg, Ty: p})
	}
	_, err := e.emitCBCall(Callback{kind: cbClosure, hdrPtr: "%clo", ty: fnTy}, args)
	if err != nil {
		restore()
		return "", err
	}
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	return e.defineContentNamed("@__kml_lower_inv.", "void", strings.Join(append([]string{"ptr %clo"}, sig...), ", "), body), nil
}

// optionalPresence is whether an argument to an optional parameter is
// present — not undefined, which JavaScript treats as omitted — and the
// value to convert when it is: a `T | undefined` scalar's presence bit (a
// `T | null` one is present, null converting as null), an `any` word that is
// not the undefined box, a possibly-undefined reference that is not null.
func (e *Emitter) optionalPresence(v Value) (string, Value) {
	switch {
	case isNullableScalar(v.Ty):
		present, payload := e.nullableScalarAggParts(v)
		if !v.Ty.IsUndefined {
			present = "true"
		}
		return present, payload
	case v.Ty.IsDynamic && v.Ty.IR == "i64":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", r, v.Ref, nbUndefined))
		return r, v
	case v.Ty.IR == "ptr" && v.Ty.Nullable && v.Ty.IsUndefined:
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", r, v.Ref))
		return r, v
	case v.Ty.IsUndefined || v.Ty.IR == "void":
		return "false", Value{Ref: "0.0", Ty: TypeF64}
	}
	return "true", v
}

// lowerArg converts an argument to its parameter's representation.
func (e *Emitter) lowerArg(v Value, ty Type) (Value, error) {
	if ty.IsDynamic {
		return e.emitBoxValue(v)
	}
	if ty.IR == "ptr" {
		return e.emitArgToString(v) // ToString
	}
	if ty.Float || ty.IsInteger() {
		// ToNumber: a string, boolean, null, undefined or object argument
		// (untyped under -compat=js) converts as Number() does.
		n, err := e.emitUnaryPlus(v, ast.Pos{})
		if err != nil {
			return Value{}, err
		}
		return e.coerce(n, ty), nil
	}
	if isNullableScalar(v.Ty) {
		v = e.nullableScalarPayloadOf(v) // an absent number is NaN
	}
	return e.coerce(v, ty), nil
}

// restArray is a fresh array of the converted rest arguments (8-byte
// elements), as its header pointer.
func (e *Emitter) restArray(elems []string) string {
	if len(elems) == 0 {
		return e.newArrayHeader("null", "0")
	}
	e.ensureMalloc()
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", data, 8*len(elems)))
	for i, ref := range elems {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slot, data, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ref, slot))
	}
	return e.newArrayHeader(data, fmt.Sprint(len(elems)))
}

// paramIR and resultIR are a representation's IR type at a C boundary, as a
// parameter and as a result: a C bool is an i1 zero-extended across it, an
// array is its header pointer.
func paramIR(ty Type) string {
	switch {
	case ty.IR == "i1":
		return "i1 zeroext"
	case ty.IsArray:
		return "ptr"
	}
	return ty.IR
}

func resultIR(ty Type) string {
	switch {
	case ty.IR == "i1":
		return "zeroext i1"
	case ty.IsArray:
		return "ptr" // the array's header
	}
	return ty.IR
}

// zeroOf is a representation's zero value, passed for an absent optional.
func zeroOf(ty Type) string {
	switch {
	case ty.IR == "ptr":
		return "null"
	case ty.Float:
		return "0.0"
	case ty.IR == "i1":
		return "false"
	}
	return "0"
}

// declareLowered declares a lowered entry point once, from its signature,
// and records the libraries it links.
func (e *Emitter) declareLowered(l *lowering) {
	for _, lib := range l.links {
		if unit, ok := runtimeUnits[lib]; ok {
			unit(e) // an embedded C source, compiled in when used
		} else {
			e.requireLink(lib)
		}
	}
	if define, ok := runtimeSymbols[l.target]; ok {
		define(e) // defined by the IR runtime
		return
	}
	var params []string
	if l.recv != nil {
		params = append(params, paramIR(*l.recv)+" noundef")
	}
	for _, p := range l.params {
		switch {
		case p.bytes:
			params = append(params, "ptr noundef", "i64 noundef")
			continue
		case p.callback != nil:
			params = append(params, "ptr noundef", "ptr noundef")
			continue
		case p.optional:
			params = append(params, "i1 zeroext")
		}
		params = append(params, paramIR(p.ty)+" noundef")
	}
	ret := resultIR(l.result)
	if l.resultOptional {
		params = append(params, "ptr noundef")
		ret = "zeroext i1"
	}
	e.declareFn(l.target, fmt.Sprintf("declare %s @%s(%s)", ret, l.target, strings.Join(params, ", ")))
}

// runtimeUnits are the `@link` names of the embedded C sources: using one
// compiles it in. Any other `@link` name is a system library.
var runtimeUnits = map[string]func(*Emitter){
	"casemap": (*Emitter).ensureCasemap,
	"string":  (*Emitter).ensureStringC,
	"number":  (*Emitter).ensureNumberC,
	"pool":    (*Emitter).ensureNativePool,
	"inspect": (*Emitter).ensureDynJSONC,
	"tls":     (*Emitter).ensureNativeTLS,
	"osinfo":  (*Emitter).ensureOSInfo,
	"zlib":    (*Emitter).ensureZlibNatives,
	"fs":      (*Emitter).ensureFsNatives,
	"sqlite":  (*Emitter).ensureSqliteNatives,
	"ffi":     (*Emitter).ensureFFINatives,
	"crypto":  (*Emitter).ensureCryptoNatives,
	"http2":   (*Emitter).ensureH2Natives,
	"umap":    (*Emitter).ensureUmapOrder,
	"weak":    (*Emitter).ensureWeakNatives,
}

// runtimeSymbols are the `@lower` targets the IR runtime defines, rather
// than declares: using one emits its definition.
var runtimeSymbols = map[string]func(*Emitter){
	"__kml_trim":       (*Emitter).ensureStringTrim,
	"__kml_trim_start": (*Emitter).ensureStringTrimStart,
	"__kml_trim_end":   (*Emitter).ensureStringTrimEnd,
	// lib/native.d.ts
	"__kml_drain_microtasks":             (*Emitter).ensureMicrotasks,
	"__kml_Object_is":                    (*Emitter).ensureObjectIs,
	"__kml_ctor_kind":                    (*Emitter).ensureCtorKind,
	"__kml_native_fs_error":              (*Emitter).ensureNativeFsError,
	"__kml_native_fs_watch":              (*Emitter).ensureNativeFsWatch,
	"__kml_native_fs_watch_close":        (*Emitter).ensureNativeFsWatch,
	"__kml_native_errno_name":            (*Emitter).ensureNativeErrno,
	"__kml_native_errno_desc":            (*Emitter).ensureNativeErrno,
	"__kml_native_uv_errno":              (*Emitter).ensureNativeErrno,
	"__kml_native_entry_path":            (*Emitter).ensureNativeEntryPath,
	"__kml_native_signal_start":          (*Emitter).ensureNativeSignal,
	"__kml_native_signal_stop":           (*Emitter).ensureNativeSignal,
	"__kml_native_process_hook":          (*Emitter).ensureProcessHooks,
	"__kml_native_process_unhook":        (*Emitter).ensureProcessHooks,
	"__kml_native_ipc_child_claim":       (*Emitter).ensureNativeIPCChildClaim,
	"__kml_native_async_context_get":     (*Emitter).ensureNativeAsyncContext,
	"__kml_native_async_context_set":     (*Emitter).ensureNativeAsyncContext,
	"__kml_native_perf_now":              (*Emitter).ensureNativePerf,
	"__kml_native_perf_time_origin":      (*Emitter).ensureNativePerf,
	"__kml_native_process_cwd":           (*Emitter).ensureNativeProcessCwd,
	"__kml_native_process_chdir":         (*Emitter).ensureNativeProcessChdir,
	"__kml_native_process_uptime":        (*Emitter).ensureNativeProcessUptime,
	"__kml_native_process_hrtime":        (*Emitter).ensureNativeProcessHrtime,
	"__kml_native_process_hrtime_read":   (*Emitter).ensureNativeProcessHrtime,
	"__kml_native_kill_pid":              (*Emitter).ensureNativeKillPid,
	"__kml_native_process_memory":        (*Emitter).ensureNativeProcessMemory,
	"__kml_native_process_umask":         (*Emitter).ensureNativeProcessUmask,
	"__kml_native_process_id":            (*Emitter).ensureNativeProcessIds,
	"__kml_native_process_argc":          (*Emitter).ensureNativeProcessArgv,
	"__kml_native_process_argv":          (*Emitter).ensureNativeProcessArgv,
	"__kml_native_process_argv0":         (*Emitter).ensureNativeProcessArgv,
	"__kml_native_process_exec_path":     (*Emitter).ensureNativeProcessExecPath,
	"__kml_native_process_set_exit_code": (*Emitter).ensureNativeProcessExit,
	"__kml_native_process_get_exit_code": (*Emitter).ensureNativeProcessExit,
	"__kml_native_process_really_exit":   (*Emitter).ensureNativeProcessExit,
	"__kml_native_process_version":       (*Emitter).ensureNativeProcessVersion,
	"__kml_native_env_get":               (*Emitter).ensureNativeEnv,
	"__kml_native_env_set":               (*Emitter).ensureNativeEnv,
	"__kml_native_env_delete":            (*Emitter).ensureNativeEnv,
}

// ensureNativeEntryPath defines @__kml_native_entry_path: the program's
// entry file (require.main.filename), the module a self-fork runs.
func (e *Emitter) ensureNativeEntryPath() {
	if e.fnDecls["__kml_native_entry_path"] {
		return
	}
	e.fnDecls["__kml_native_entry_path"] = true
	path := ""
	if e.prog != nil {
		path = e.prog.EntryPath
	}
	e.emitGlobal(fmt.Sprintf("define ptr @__kml_native_entry_path() {\nentry:\n  ret ptr %s\n}", e.internString(path)))
}

// declareFn emits a function declaration once per symbol: the lowered path
// and the hand-written runtime helpers may both need one.
func (e *Emitter) declareFn(name, line string) {
	if e.fnDecls[name] {
		return
	}
	e.fnDecls[name] = true
	e.emitGlobal(line)
}

// checkerNarrowed is the primitive the checker narrows an `any`/`unknown`
// reference to (`if (typeof c === "string") … c …`), which a read of it
// unboxes to; ok is false when the reference is not dynamic or the checker
// does not narrow it to one primitive.
func (e *Emitter) checkerNarrowed(id *ast.Identifier, ty Type) (Type, bool) {
	if ty.IsDynamic && len(ty.UnionMembers) > 0 {
		return e.checkerNarrowedUnion(id, ty)
	}
	// Under -compat=js codegen widens a binding assigned several kinds into
	// an `any` the checker does not model (crossTypeWidenedBindings).
	if !isUnconstrainedDynamic(ty) || e.compatJS() {
		return Type{}, false
	}
	c := e.front()
	if c == nil {
		return Type{}, false
	}
	t := c.TypeOf(id)
	if c.Unanswered(t) || t.Flags&(checker.Any|checker.Unknown) != 0 {
		return Type{}, false
	}
	switch {
	case t.Flags&checker.Union != 0:
		return Type{}, false
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
		return TypeF64, true
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return TypeBool, true
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return TypePtr, true
	}
	return Type{}, false
}

// checkerNarrowedUnion is the member of the union ty that the checker
// narrows the reference id to where no statement-level guard did
// (`typeof p === "string" ? p.trim() : p`, `x !== null && x.f`): the one
// member of that kind, or false when the checker's type is still a union or
// the union has no single member of its kind.
func (e *Emitter) checkerNarrowedUnion(id ast.Expression, ty Type) (Type, bool) {
	c := e.front()
	if c == nil || e.compatJS() {
		return Type{}, false
	}
	t := c.TypeOf(id)
	if c.Unanswered(t) || t.Flags&(checker.Any|checker.Unknown|checker.Union|checker.Nullish) != 0 {
		return Type{}, false
	}
	var kind string
	switch {
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
		kind = "number"
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		kind = "boolean"
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		kind = "string"
	case t.Flags&checker.Object != 0 && t.Kind == checker.Function:
		kind = "function"
	case t.Flags&checker.Object != 0 && (t.Kind == checker.Array || t.Kind == checker.Tuple):
		kind = "array"
	case t.Flags&checker.Object != 0 && t.Symbol != nil && t.Symbol.Name == "ReadonlyArray":
		kind = "array"
	case isBytesType(t):
		kind = "array" // a Buffer/Uint8Array is an array here
	case t.Flags&checker.Object != 0:
		kind = "object"
	default:
		return Type{}, false
	}
	var found Type
	n := 0
	// Several object members: the one the checker's narrowed type names
	// (a class by its name, an interface or alias by its declared name).
	if kind == "object" && t.Symbol != nil {
		for _, m := range ty.UnionMembers {
			if unionMemberTag(m) == "object" && objectMemberNamed(m, t.Symbol.Name) {
				found = m
				n++
			}
		}
		if n == 1 {
			return found, true
		}
		n = 0
	}
	for _, m := range ty.UnionMembers {
		if unionMemberTag(m) != kind {
			continue
		}
		// `string[] | Buffer`: the byte array or the other, as the checker says.
		if kind == "array" && (m.IsTypedArray || m.IsBuffer) != isBytesType(t) {
			continue
		}
		found = m
		n++
	}
	if n != 1 {
		return Type{}, false
	}
	return found, true
}

// registerLibraryStringAliases registers the builtin declarations' type
// aliases that are unions of string literals (`BufferEncoding`) as strings,
// so a program's annotation naming one lowers as a string; one the program
// declares itself is left to it.
func (e *Emitter) registerLibraryStringAliases(prog *ast.Program) {
	libProgs, err := lib.Programs()
	if err != nil {
		return
	}
	own := map[string]bool{}
	for _, st := range prog.Body {
		switch d := st.(type) {
		case *ast.TypeAliasDeclaration:
			own[d.Name] = true
		case *ast.InterfaceDeclaration:
			own[d.Name] = true
		}
	}
	for _, lp := range libProgs {
		for _, st := range lp.Body {
			a, ok := st.(*ast.TypeAliasDeclaration)
			if !ok || len(a.TypeParams) > 0 || a.Type == nil || own[a.Name] {
				continue
			}
			if u, ok := a.Type.TypeNode().(*ast.UnionType); ok && allStringLiterals(u) {
				if _, taken := e.interfaces[a.Name]; !taken {
					e.interfaces[a.Name] = TypePtr
				}
			}
		}
	}
}

func allStringLiterals(u *ast.UnionType) bool {
	for _, t := range u.Types {
		if l, ok := t.(*ast.LiteralType); !ok || l.Kind != "string" {
			return false
		}
	}
	return len(u.Types) > 0
}

// genericMethodResult is the class a call of a generic method returns, per
// the checker: `pipe<T extends Stream>(d: T): T` erases T to Stream for code
// generation, and the call's result is the argument's own class (the same
// pointer, typed as the subclass). ret otherwise.
func (e *Emitter) genericMethodResult(ex *ast.CallExpression, objTy Type, method string, ret Type) Type {
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return ret
	}
	// An erased method's `R` result (`run<R>(…): R`) is `any`; where the
	// checker types the call as a class instance, the result is that
	// instance (the caller unboxes it).
	if isUnconstrainedDynamic(ret) {
		m := info.Methods[method]
		c := e.front()
		if m == nil || !m.ErasedMethod || c == nil {
			return ret
		}
		t := c.TypeOf(ex)
		if c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance || t.Symbol == nil {
			return ret
		}
		if ci, ok := e.classes[t.Symbol.Name]; ok && !ci.Ty.IsDynamic {
			return ci.Ty
		}
		return ret
	}
	if !ret.IsClass {
		return ret
	}
	m := info.Methods[method]
	if m == nil || !m.ErasedMethod {
		return ret
	}
	c := e.front()
	if c == nil {
		return ret
	}
	t := c.TypeOf(ex)
	if c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance || t.Symbol == nil {
		return ret
	}
	ci, ok := e.classes[t.Symbol.Name]
	if !ok {
		return ret
	}
	if t.Symbol.Name == ret.ClassName {
		return ci.Ty
	}
	for _, anc := range ci.AncestorChain {
		if anc == ret.ClassName {
			return ci.Ty
		}
	}
	return ret
}

// objectMemberNamed reports whether union member m is the type the checker
// calls name: a class (its codegen name carries a module suffix) or an
// interface/alias (RefName).
func objectMemberNamed(m Type, name string) bool {
	if m.IsClass {
		return ast.Unmangle(m.ClassName) == name
	}
	if m.RefName != "" {
		return ast.Unmangle(m.RefName) == name
	}
	return hostClassName(m) == name
}

// ownerReprs are the host-object owners whose intrinsics take a receiver
// codegen holds as the representation the predicate accepts.
var ownerReprs = map[string]func(Type) bool{
	"FinalizationRegistry": func(t Type) bool { return t.IsFinalizationRegistry },
}

// reprOwners are the declaration owners whose methods a receiver held as ty
// has, most specific first.
func reprOwners(ty Type) []string {
	for owner, held := range ownerReprs {
		if held(ty) {
			return []string{owner}
		}
	}
	switch {
	case isUnconstrainedDynamic(ty):
		return nil
	case ty.IsReadableStream:
		return []string{"ReadableStream"}
	case ty.IsStreamReader:
		return []string{"ReadableStreamDefaultReader", "ReadableStreamGenericReader"}
	case ty.IsRSController:
		return []string{"ReadableStreamDefaultController"}
	case ty.IsWritableStream:
		return []string{"WritableStream"}
	case ty.IsStreamWriter:
		return []string{"WritableStreamDefaultWriter"}
	case ty.IsWSController:
		return []string{"WritableStreamDefaultController"}
	case ty.IsBuffer:
		return []string{"Buffer", "TypedArray", "Array", "Object"}
	case ty.IsTypedArray:
		return []string{"TypedArray", "Array", "Object"}
	case ty.IsXHR:
		return []string{"XMLHttpRequest"}
	case hasBodyMixin(ty):
		return []string{"Body"}
	case ty.IsBigInt:
		return []string{"BigInt"}
	case ty.IsPromise:
		return []string{"Promise"}
	case ty.IsFunc && !ty.IsDynamic:
		return []string{"Function", "Object"}
	case ty.IsArray:
		return []string{"Array", "Object"}
	case isForOfStringTy(ty):
		return []string{"String", "Object"}
	}
	return []string{"Object"}
}

// reprIntrinsic is the intrinsic a method call takes when the checker has
// no declaration for it (a callback parameter it could not type from an
// overloaded generic context, synthesized code): the method of the owner
// codegen's own representation of the receiver names.
func (e *Emitter) reprIntrinsic(mem *ast.MemberExpression) (string, bool) {
	for _, owner := range reprOwners(e.inferExprType(mem.Object)) {
		key := owner + ".prototype." + mem.Property
		if e.intrinsicApplies(key, mem) {
			return key, true
		}
	}
	return "", false
}
