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
func (e *Emitter) front() frontView {
	if e.frontBuilt {
		if e.frontChecker == nil {
			return nil // a nil *Checker would be a non-nil frontView
		}
		return e.frontChecker
	}
	if e.prog == nil {
		return nil // not yet: EmitProgram has not stored the program
	}
	e.frontBuilt = true
	libProgs, err := lib.Programs()
	if err != nil {
		return nil
	}
	b := binder.BindWith(e.prog, binder.Options{AnnexB: e.compatJS(), Lib: libProgs})
	e.frontBinding = b
	e.frontChecker = checker.NewWith(b, e.opts)
	return e.frontChecker
}

// warmFront types every expression under n with the checker and keeps
// each answer, before a rewrite after binding (staticPrivateBrand) changes
// the code the checker would read: the checker's own memo does not keep an
// answer reached inside a cycle (a static member read while its class's
// value is built).
func (e *Emitter) warmFront(n ast.Node) {
	c := e.front()
	if c == nil {
		return
	}
	// The shadow comparison's own checker reads the same code: warmed
	// alike, or it answers the rewritten code instead.
	if w, ok := e.shadowOracle.(interface{ Warm(ast.Node) }); ok {
		w.Warm(n)
	}
	var decls []*ast.VarDeclaration // after every expression, as the oracle's Warm
	defer func() {
		for _, v := range decls {
			c.WarmDecl(v) // its binding's type, read off the initializer as written
		}
	}()
	ast.Inspect(n, func(x ast.Node) bool {
		if v, ok := x.(*ast.VarDeclaration); ok {
			decls = append(decls, v)
		}
		if ex, ok := x.(ast.Expression); ok {
			if t := c.TypeOf(ex); !c.Unanswered(t) {
				if e.frontWarm == nil {
					e.frontWarm = map[ast.Expression]*checker.Type{}
				}
				e.frontWarm[ex] = t
			}
		}
		return true
	})
}

// originate links syn, a node a rewrite after binding built, to the
// expression orig of the code as written that it stands for.
func (e *Emitter) originate(syn, orig ast.Expression) {
	if e.synthOrigin == nil {
		e.synthOrigin = map[ast.Expression]ast.Expression{}
	}
	e.synthOrigin[syn] = orig
}

// lowered marks syn, a node a rewrite after binding built that stands for
// no expression of the code as written: codegen types it itself.
func (e *Emitter) lowered(syn ast.Expression) {
	if e.loweredNodes == nil {
		e.loweredNodes = map[ast.Expression]bool{}
	}
	e.loweredNodes[syn] = true
}

// lowering is a call resolved to a declaration with an @lower target.
type lowering struct {
	// dropRecv marks a library function that takes exactly the method's
	// declared parameters (`process.nextTick` on an alias of process): the
	// receiver is not one of its arguments.
	dropRecv bool
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

// libFnTakesNoReceiver reports whether library function m takes exactly the
// parameters method declaration d declares, so a call through a receiver
// does not pass it (an iterator's `every(it, predicate)` takes it first).
func (e *Emitter) libFnTakesNoReceiver(m string, d ast.Node) bool {
	ms, ok := d.(*ast.MethodSignature)
	if !ok {
		return false
	}
	_, sig, found := e.resolveFuncRef(m)
	return found && len(sig.ParamTypes) == len(ms.Parameters)
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
		recvExpr := func() ast.Expression { return ex.Callee.(*ast.MemberExpression).Object }
		switch {
		case l.recv != nil && l.dropRecv && len(args) > 0:
			// Evaluated for its effects, ahead of the arguments.
			args = append([]ast.Expression{ast.NewSequenceExpression([]ast.Expression{recvExpr(), args[0]}, args[0].GetPos())}, args[1:]...)
		case l.recv != nil && !l.dropRecv:
			args = append([]ast.Expression{recvExpr()}, args...)
		}
		// The callee at the builtin's place, which a stack frame shows.
		l.call = ast.NewCallExpression(ast.NewIdentifier(l.libFn, callSitePos(ex)), args, ex.GetPos())
		l.call.TypeArgs = ex.TypeArgs
		if l.e.callOrigins == nil {
			l.e.callOrigins = map[*ast.CallExpression]*ast.CallExpression{}
		}
		l.e.callOrigins[l.call] = ex
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
	if id, ok := ex.Callee.(*ast.Identifier); ok && ast.IsLibraryName(id.Name) && !e.isShadowedByLocal(id.Name) {
		// A builtin module's intrinsic `declare function` (klain:assets'
		// `embedDir`).
		if sym, _ := c.Binding().Resolve(id); sym != nil {
			for _, d := range sym.Declarations {
				if fd, ok := d.Node.(*ast.FunctionDeclaration); ok && fd.Intrinsic != "" {
					if _, known := intrinsics[fd.Intrinsic]; known {
						return &lowering{intrinsic: fd.Intrinsic, e: e, ex: ex}, true
					}
				}
			}
		}
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
				if fd, ok := d.Node.(*ast.FunctionDeclaration); ok && fd.Lower != "" {
					if m, linked := e.globalLinks[fd.Lower]; linked {
						return &lowering{libFn: m, e: e, ex: ex}, true
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
	// A global function reached through globalThis (`globalThis.setTimeout`)
	// lowered to a library function.
	if fd, ok := c.MemberDecl(mem).(*ast.FunctionDeclaration); ok && fd.Lower != "" {
		if m, linked := e.globalLinks[fd.Lower]; linked {
			return &lowering{libFn: m, e: e, ex: ex}, true
		}
	}
	for _, d := range c.MemberOverloadDecls(mem) {
		if fd, ok := d.(*ast.FunctionDeclaration); ok && fd.Lower != "" {
			if m, linked := e.globalLinks[fd.Lower]; linked {
				return &lowering{libFn: m, e: e, ex: ex}, true
			}
		}
	}
	if c.MemberDecl(mem) == nil && c.MemberOverloadDecls(mem) == nil {
		// A BigInt64Array/BigUint64Array codegen holds (a prototype record's
		// receiver, which the checker does not type): its method's
		// TypeScript implementation, as its declaration lowers to.
		if ot := e.inferExprType(mem.Object); ot.BigIntElem && !ot.IsDynamic() && ot.ElemType != nil {
			kind := "BigInt64Array"
			if !ot.ElemType.Signed {
				kind = "BigUint64Array"
			}
			if m, linked := e.globalLinks["__kml_"+kind+"_"+mem.Property]; linked {
				r := ot
				return &lowering{libFn: m, recv: &r, e: e, ex: ex}, true
			}
		}
		if key, ok := e.reprIntrinsic(mem); ok {
			return &lowering{intrinsic: key, e: e, ex: ex}, true
		}
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
			return &lowering{libFn: m, recv: &r, dropRecv: e.libFnTakesNoReceiver(m, d), e: e, ex: ex}, true
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
	// A native entry point's parameters are the declaration's
	// representations, which explicit type arguments would instantiate.
	if len(ex.TypeArgs) > 0 || fn == nil || c.Unanswered(fn) || fn.Flags&checker.Object == 0 || fn.Kind != checker.Function {
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
			if !ok || !r.IsArray() || i != len(fn.Params)-1 {
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
		if !ok || r.IR == "void" || r.IsArray() {
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
		if !at.IsArray() || at.ElemType == nil || at.ElemType.IR != l.params[n-1].ty.ElemType.IR ||
			at.ElemType.IsDynamic() || at.ElemType.Nullable() || isUnconstrainedDynamic(at) {
			return nil, false
		}
	}
	res := fn.Result
	if u := c.NonUndefined(res); u != res && c.MaybeUndefined(res) && res.Flags&checker.Union != 0 {
		res, l.resultOptional = u, true // `T | undefined`
	}
	if r, ok := e.lowerTypeClassRepr(res); ok && !l.resultOptional {
		l.result = r // an instance C built through the class's layout
		return l, true
	}
	if l.result, ok = lowerRepr(res); !ok || l.resultOptional && (l.result.IR == "void" || l.result.IsArray()) {
		return nil, false
	}
	return l, true
}

// methodIntrinsic is the intrinsic a method call's declaration names
// (`arr.map(f)` through Array's `/** @intrinsic Array.prototype.map */`),
// whatever declares it. A receiver the checker cannot type (or types `any`)
// has no declaration: it takes the named paths.
func (e *Emitter) methodIntrinsic(c frontView, mem *ast.MemberExpression) (string, bool) {
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
		return (ot.IsMap() || ot.IsSet()) && ot.Weak == weak && !ot.IsDynamicObject() &&
			!isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "RegExp.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsRegExp() && !ot.IsDynamic()
	}
	if strings.HasPrefix(key, "Function.prototype.") {
		// A -compat=js prototype constructor's Base.call(this, …) runs it on
		// the receiver object (emitCall's jsProtoCtor path).
		if id, ok := mem.Object.(*ast.Identifier); ok && e.compatJS() && e.jsProtoCtor[id.Name] {
			return false
		}
		ot := e.inferExprType(mem.Object)
		return ot.IsFunc() && !ot.IsDynamic()
	}
	if strings.HasPrefix(key, "Promise.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsPromise() && !ot.IsDynamic()
	}
	if strings.HasPrefix(key, "Date.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsDate() && !ot.IsDynamic() || e.isDateClass(ot)
	}
	if strings.HasPrefix(key, "TypedArray.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsTypedArray() && !isUnconstrainedDynamic(ot)
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
		return ot.IsObject() && !isUnconstrainedDynamic(ot)
	case "BigInt.prototype.toString":
		return e.inferExprType(mem.Object).IsBigInt()
	}
	if owner, _, ok := strings.Cut(key, ".prototype."); ok && strings.Contains(owner, "Stream") {
		ot := e.inferExprType(mem.Object)
		if strings.HasPrefix(owner, "Writable") {
			return ot.IsWritableStream() || ot.IsStreamWriter() || ot.IsWSController()
		}
		return ot.IsReadableStream() || ot.IsStreamReader() || ot.IsRSController()
	}
	if strings.HasPrefix(key, "Body.prototype.") {
		ot := e.inferExprType(mem.Object)
		return hasBodyMixin(ot) && !isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "XMLHttpRequest.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsXHR() && !isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "Buffer.prototype.") {
		ot := e.inferExprType(mem.Object)
		return ot.IsBuffer() && !isUnconstrainedDynamic(ot)
	}
	if strings.HasPrefix(key, "Array.prototype.") {
		ot := e.inferExprType(mem.Object)
		if !ot.IsArray() || isUnconstrainedDynamic(ot) || ot.IsFlatArray() || ot.BigIntElem && bigIntElemRejectedMethods[mem.Property] {
			return false
		}
	}
	return true
}

// isBuiltinGlobal reports whether id names a builtin global object, not a
// program binding that shadows it.
func (e *Emitter) isBuiltinGlobal(c frontView, id *ast.Identifier) bool {
	if e.isShadowedByLocal(id.Name) {
		return false
	}
	b := c.Binding()
	sym, _ := b.Resolve(id)
	return sym != nil && b.Globals != nil && sym.Scope == b.Globals
}

// isBuiltinGlobalObject reports whether x is a builtin global object
// (`Math`, or `globalThis.Math`) rather than a value of a primitive.
func (e *Emitter) isBuiltinGlobalObject(c frontView, x ast.Expression) bool {
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
func (e *Emitter) loweredReceiver(c frontView, obj ast.Expression) (ty Type, known, ok bool) {
	if id, ok := obj.(*ast.Identifier); ok && c.Binding().Program.BuiltinMarkers[id.Name] != "" {
		return Type{}, false, false // a builtin module (`ffi.toString`), not a value
	}
	ct := e.inferExprType(obj)
	// A possibly-absent number (a typed-array element read) is one too:
	// emitCall's receiver guard throws on an absent one.
	number := isNumberTy(ct) && ct.IR != "i1" && !ct.IsDate() && !ct.IsBigInt() && !ct.IsDynamic()
	ct.setNullable(false)
	ct.setUndefined(false)
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
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
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
	}
	if r, ok := lowerTypeRepr(t); ok && r.IsError() {
		// A declaration's native layout: so far the Error object.
		return r, true
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
		if !ok || r.IR == "void" || r.IsArray() || r.IsError() {
			return nil, false
		}
		params = append(params, r)
	}
	return params, true
}

// isBytesType reports whether t is `Uint8Array` (or `Buffer`, one): memory
// a native operation reads or fills in place.
func isBytesType(t *checker.Type) bool {
	if t == nil || t.Flags&checker.Object == 0 || t.Symbol == nil {
		return false
	}
	l := declaredLowerType(t.Symbol)
	return l == "uint8array" || l == "buffer"
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
	if l.libFn != "" {
		return e.emitExpr(l.libCall())
	}
	mem := ex.Callee.(*ast.MemberExpression)
	var args []string
	if l.recv != nil {
		v, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		v = e.convert(v, *l.recv)
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
		if i < len(l.params) && l.params[i].bytes && e.inferExprType(a).IsDynamic() {
			// A box (an any-held Buffer): its bytes, not its elements read
			// as numbers.
			v, err := e.emitExpr(a)
			if err != nil {
				return Value{}, err
			}
			ptr, n := e.arrayParts(e.convert(v, TypedArrayType("uint8")))
			args = append(args, "ptr noundef "+ptr, "i64 noundef "+n)
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
	if l.result.IsArray() {
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
	restore = e.sharedBody(restore) // content-named: the same in every program
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
		if !v.Ty.IsUndefined() {
			present = "true"
		}
		return present, payload
	case v.Ty.IsDynamic() && v.Ty.IR == "i64":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", r, v.Ref, nbUndefined))
		return r, v
	case v.Ty.IR == "ptr" && v.Ty.Nullable() && v.Ty.IsUndefined():
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", r, v.Ref))
		return r, v
	case v.Ty.IsUndefined() || v.Ty.IR == "void":
		return "false", Value{Ref: "0.0", Ty: TypeF64}
	}
	return "true", v
}

// lowerArg converts an argument to its parameter's representation.
func (e *Emitter) lowerArg(v Value, ty Type) (Value, error) {
	if ty.IsDynamic() {
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
		return e.convert(n, ty), nil
	}
	return e.convert(v, ty), nil // an absent number stays undefined
}

// restArray is a fresh array of the converted rest arguments (8-byte
// elements), as its header pointer.
func (e *Emitter) restArray(elems []string) string {
	if len(elems) == 0 {
		return e.newArrayHeader("null", "0")
	}
	e.ensureMalloc()
	data := e.freshReg()
	e.heapMallocInto(data, fmt.Sprint(8*len(elems)))
	for i, ref := range elems {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slot, data, i))
		e.heapStore("ptr", ref, slot, 8)
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
	case ty.IsArray():
		return "ptr"
	}
	return ty.IR
}

func resultIR(ty Type) string {
	switch {
	case ty.IR == "i1":
		return "zeroext i1"
	case ty.IsArray():
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
	"casemap":    (*Emitter).ensureCasemap,
	"string":     (*Emitter).ensureStringC,
	"number":     (*Emitter).ensureNumberC,
	"pool":       (*Emitter).ensureNativePool,
	"inspect":    (*Emitter).ensureDynJSONC,
	"tls":        (*Emitter).ensureNativeTLS,
	"osinfo":     (*Emitter).ensureOSInfo,
	"zlib":       (*Emitter).ensureZlibNatives,
	"cares":      (*Emitter).ensureCaresNatives,
	"h2o":        (*Emitter).ensureH2ONatives,
	"fs":         (*Emitter).ensureFsNatives,
	"sqlite":     (*Emitter).ensureSqliteNatives,
	"ffi":        (*Emitter).ensureFFINatives,
	"crypto":     (*Emitter).ensureCryptoNatives,
	"http2":      (*Emitter).ensureH2Natives,
	"umap":       (*Emitter).ensureUmapOrder,
	"weak":       (*Emitter).ensureWeakNatives,
	"timers":     (*Emitter).ensureTimerNatives,
	"microtasks": (*Emitter).ensureMicrotasks,
	"frames":     (*Emitter).ensureFrames,
	"fdlibm":     (*Emitter).ensureFdlibm,
	"tty":        (*Emitter).ensureTtyRead,
	"tui":        (*Emitter).ensureTui,
	"assets":     (*Emitter).ensureAssetsNatives,
	"webview":    (*Emitter).ensureWebviewRuntime,
	"sync":       (*Emitter).ensureSyncRuntime,
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
	"__kml_native_fs_watch_ref":          (*Emitter).ensureNativeFsWatch,
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
	"__kml_native_process_title":         (*Emitter).ensureNativeProcessTitle,
	"__kml_native_process_set_title":     (*Emitter).ensureNativeProcessTitle,
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
	if e.dynEntryWidenedIdent(id) || e.stdioBinding(id) {
		return Type{}, false // a dynamic entry's box, a stdio stream's, stays one
	}
	if ty.IsDynamic() && len(ty.UnionMembers) > 0 {
		return e.checkerNarrowedUnion(id, ty)
	}
	// Under -compat=js codegen widens a binding assigned several kinds into
	// an `any` the checker does not model (crossTypeWidenedBindings).
	if !isUnconstrainedDynamic(ty) || e.compatJS() {
		return Type{}, false
	}
	c := e.front()
	if c == nil || !e.flowTrusted(id) {
		return Type{}, false
	}
	t := c.TypeOf(id)
	if c.Unanswered(t) || t.Flags&(checker.Any|checker.Unknown) != 0 {
		return Type{}, false
	}
	switch {
	case t.Flags&checker.Union != 0:
		return Type{}, false
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
		return TypeF64, true
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return TypeBool, true
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return TypePtr, true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Instance && t.Symbol != nil && len(t.TypeArgs) == 0:
		// `x instanceof C` (or a guard returning early otherwise): the
		// instance, read through C's layout. A class over a host body (an
		// Array, Map, … subclass) or an Error subclass keeps the box.
		if ct, ok := e.classRepr(t.Symbol); ok && ct.IsClass() {
			if info, ok := e.classes[ct.ClassName]; ok && info.HostBase == nil && !info.IsErrorSubclass {
				return ct, true
			}
		}
	}
	return Type{}, false
}

// checkerPresent reports a reference the checker narrows to a type without
// null or undefined (`n === undefined ? d : n`, `x !== null && x > 0`): a
// nullable-scalar local read there is its payload.
func (e *Emitter) checkerPresent(id *ast.Identifier, ty Type) bool {
	c := e.front()
	if c == nil || !e.flowTrusted(id) {
		return false
	}
	if e.storesPresent(id) {
		return true
	}
	// An element read's absence is one the checker does not model.
	if ty.UncheckedIndex {
		return false
	}
	// A typed local declared without a value, read where every path has
	// stored one (its storage holds undefined only before that).
	if c.Binding().DefinitelyAssigned(id) {
		return true
	}
	// Only a narrowing: the declared type holds the absence and flow
	// removes it (a destructured `[a, b] = [1]` is `number` to the checker
	// yet absent at run time).
	st, t := c.StorageTypeOf(id), c.TypeOf(id)
	return !c.Unanswered(st) && !c.Unanswered(t) && !presentType(st) && presentType(t)
}

// assignedString reports a read of a string local declared without a
// value (its storage `string | undefined`) where every path has stored one,
// or has passed a guard excluding undefined since the last store.
func (e *Emitter) assignedString(id *ast.Identifier, ty Type) bool {
	c := e.front()
	return c != nil && isStringTy(ty) && ty.Nullable() && ty.IsUndefined() && e.flowTrusted(id) && (!ty.UncheckedIndex && c.Binding().DefinitelyAssigned(id) || e.storesPresent(id) || c.Binding().GuardedSinceStore(id))
}

// storesPresent reports a read every path to which stores a value that is
// never absent, by a declaration or a plain assignment: a skippable `var`
// (ADR-01057) is undefined only on a path that skips it.
func (e *Emitter) storesPresent(id *ast.Identifier) bool {
	c := e.front()
	if c == nil || e.storesPresentBusy[id] {
		return false
	}
	stores, ok := c.Binding().ReachingStores(id)
	if !ok {
		return false
	}
	if e.storesPresentBusy == nil {
		e.storesPresentBusy = map[*ast.Identifier]bool{}
	}
	e.storesPresentBusy[id] = true
	defer delete(e.storesPresentBusy, id)
	for _, s := range stores {
		var v ast.Expression
		switch x := s.(type) {
		case *ast.VarDeclaration:
			v = x.Init
		case *ast.AssignmentExpression:
			v = x.Right
		}
		t := e.inferExprType(v)
		if t.IR == "" || t.IR == "void" || t.Nullable() || t.IsUndefined() || t.IsNull() || t.IsDynamic() {
			return false
		}
	}
	return true
}

// checkerPresentRef reports a nullable reference to an instance, function
// or promise — a variable or a property read — the checker narrows to a
// type without null or undefined (`const base = this.base` under
// `this.base !== null`, `[this.#t]` there): the pointer read there is typed
// present, as TypeScript types it.
func (e *Emitter) checkerPresentRef(ref ast.Expression, ty Type) bool {
	c := e.front()
	if c == nil || ty.UncheckedIndex || ty.IR != "ptr" || !ty.Nullable() || ty.IsDynamic() || isNullableScalar(ty) {
		return false
	}
	if !ty.IsClass() && !ty.IsFunc() && !ty.IsPromise() && !ty.IsObject() && !ty.IsArray() {
		return false
	}
	// A position an array pattern may read past the source's end is `T` to
	// the checker yet absent at run time.
	id, isID := ref.(*ast.Identifier)
	if _, isMember := ref.(*ast.MemberExpression); !isID && !isMember {
		return false
	}
	if sym, _ := e.resolveIdent(id); isID && sym != nil {
		for _, d := range sym.Declarations {
			if _, ok := d.Node.(*ast.ArrayDestructuring); ok {
				return false
			}
		}
		// A key or index read is `T` to the checker (no
		// noUncheckedIndexedAccess) yet absent on a miss.
		if e.storesIndexRead(sym) {
			return false
		}
	}
	// Another function may clear it since the narrowing (a call the
	// checker's flow, as TypeScript's, does not see): read it nullable.
	if !e.flowTrusted(ref) {
		return false
	}
	t := c.TypeOf(ref)
	return !c.Unanswered(t) && presentType(t)
}

// resolveIdent is the binder's symbol for id (nil for a nil id).
func (e *Emitter) resolveIdent(id *ast.Identifier) (*binder.Symbol, bool) {
	if id == nil {
		return nil, false
	}
	return e.frontBinding.Resolve(id)
}

// unboundParam reports whether id names a parameter of a function, by the
// binder: read where codegen has not bound it yet, it is a later parameter
// in an earlier one's default.
func (e *Emitter) unboundParam(id *ast.Identifier) bool {
	if e.front() == nil {
		return false
	}
	sym, _ := e.frontBinding.Resolve(id)
	if sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		switch fn := d.Node.(type) {
		case *ast.FunctionDeclaration:
			// The function's own name (an ambient `declare function eval`
			// codegen has no value for) is no parameter.
			if fn.Name == id.Name && !paramsName(fn.Params, id.Name) {
				return false
			}
			return d.Kind == binder.VarLike
		case *ast.ArrowFunction, *ast.FunctionExpression:
			return d.Kind == binder.VarLike
		}
	}
	return false
}

func paramsName(ps []ast.Param, name string) bool {
	for _, p := range ps {
		if p.Name == name {
			return true
		}
	}
	return false
}

// catchBinding reports whether id names a catch clause's own binding.
func (e *Emitter) catchBinding(id *ast.Identifier) bool {
	if e.front() == nil {
		return false
	}
	sym, _ := e.frontBinding.Resolve(id)
	if sym == nil || sym.Scope == nil || sym.Scope.Kind != binder.CatchScope {
		return false
	}
	// The parameter itself, not a variable the catch body declares.
	for _, d := range sym.Declarations {
		if _, isVar := d.Node.(*ast.VarDeclaration); isVar {
			return false
		}
	}
	return true
}

// checkerNarrowedUnion is the member of the union ty that the checker
// narrows the reference id to where no statement-level guard did
// (`typeof p === "string" ? p.trim() : p`, `x !== null && x.f`): the one
// member of that kind, or false when the checker's type is still a union or
// the union has no single member of its kind.
func (e *Emitter) checkerNarrowedUnion(id ast.Expression, ty Type) (Type, bool) {
	c := e.front()
	if c == nil || e.compatJS() || !e.flowTrusted(id) {
		return Type{}, false
	}
	t := c.TypeOf(id)
	if c.Unanswered(t) || t.Flags&(checker.Any|checker.Unknown|checker.Union|checker.Nullish) != 0 {
		return Type{}, false
	}
	var kind string
	switch {
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
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
		// A host member (a Map, a Date, an ArrayBuffer) by its class.
		for _, m := range ty.UnionMembers {
			if unionMemberTag(m) != "object" && !m.IsArray() && hostClassName(m) == t.Symbol.Name {
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
		if kind == "array" && (m.IsTypedArray() || m.IsBuffer()) != isBytesType(t) {
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
// erasedMethodCall is the result of call, a call of an erased generic
// method on an instance of objTy's class (genericMethodResult).
func (e *Emitter) erasedMethodCall(call *ast.CallExpression, objTy Type) (Type, bool) {
	mem, ok := call.Callee.(*ast.MemberExpression)
	if !ok {
		return Type{}, false
	}
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return Type{}, false
	}
	m := info.Methods[mem.Property]
	sig, hasSig := info.MethodSigs[mem.Property]
	if m == nil || !m.ErasedMethod || !hasSig {
		return Type{}, false
	}
	return e.genericMethodResult(call, objTy, mem.Property, e.thisRetType(sig, objTy)), true
}

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
		if ci, ok := e.classes[t.Symbol.Name]; ok && !ci.Ty.IsDynamic() {
			return ci.Ty
		}
		return ret
	}
	if !ret.IsClass() {
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
	name = ast.Unmangle(name) // a module's class symbol keeps its mangled name
	if m.IsClass() {
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
	"FinalizationRegistry": func(t Type) bool { return t.IsFinalizationRegistry() },
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
	case ty.IsReadableStream():
		return []string{"ReadableStream"}
	case ty.IsStreamReader():
		return []string{"ReadableStreamDefaultReader", "ReadableStreamGenericReader"}
	case ty.IsRSController():
		return []string{"ReadableStreamDefaultController"}
	case ty.IsWritableStream():
		return []string{"WritableStream"}
	case ty.IsStreamWriter():
		return []string{"WritableStreamDefaultWriter"}
	case ty.IsWSController():
		return []string{"WritableStreamDefaultController"}
	case ty.IsBuffer():
		return []string{"Buffer", "TypedArray", "Array", "Object"}
	case ty.IsTypedArray():
		return []string{"TypedArray", "Array", "Object"}
	case ty.IsXHR():
		return []string{"XMLHttpRequest"}
	case hasBodyMixin(ty):
		return []string{"Body"}
	case ty.IsBigInt():
		return []string{"BigInt"}
	case ty.IsRegExp() && !ty.IsDynamic():
		return []string{"RegExp"}
	case ty.IsPromise():
		return []string{"Promise"}
	case ty.IsFunc() && !ty.IsDynamic():
		return []string{"Function", "Object"}
	case ty.IsArray():
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

// loweredGlobalFn is the library function a builtin `declare function`'s
// `@lower` links to, for the global id names (`setTimeout`).
func (e *Emitter) loweredGlobalFn(id *ast.Identifier) (string, bool) {
	c := e.front()
	if c == nil || !e.isBuiltinGlobal(c, id) {
		return "", false
	}
	sym, _ := c.Binding().Resolve(id)
	if sym == nil {
		return "", false
	}
	for _, d := range sym.Declarations {
		if fd, ok := d.Node.(*ast.FunctionDeclaration); ok && fd.Lower != "" {
			if m, linked := e.globalLinks[fd.Lower]; linked {
				return m, true
			}
		}
	}
	return "", false
}

// storesIndexRead reports a variable some store gives an element or key
// read (`a[i]`, `d[k]`), which may be absent whatever the checker says.
func (e *Emitter) storesIndexRead(sym *binder.Symbol) bool {
	if e.frontBinding == nil {
		return false
	}
	for _, st := range e.frontBinding.StoresTo(sym) {
		var v ast.Expression
		switch x := st.(type) {
		case *ast.VarDeclaration:
			v = x.Init
		case *ast.AssignmentExpression:
			v = x.Right
		}
		for {
			as, ok := v.(*ast.AsExpression)
			if !ok {
				break
			}
			v = as.Expr
		}
		if _, ok := v.(*ast.IndexExpression); ok {
			return true
		}
	}
	return false
}

// memberPathKey is a property path's text (`a.b.c`, `this.x`), or "" when
// the reference is not a plain dotted path.
func memberPathKey(e ast.Expression) string {
	switch x := e.(type) {
	case *ast.Identifier:
		return x.Name
	case *ast.ThisExpression:
		return "this"
	case *ast.MemberExpression:
		if x.Optional {
			return ""
		}
		if base := memberPathKey(x.Object); base != "" {
			return base + "." + x.Property
		}
	}
	return ""
}

// callBetweenNarrowing reports whether, on some path to the property read
// m, a call runs after the condition or store that made m present: it may
// clear the property, which the checker's flow (as TypeScript's) does not
// see. A path the walk cannot place is reported too.
func callBetweenNarrowing(b *binder.Binding, m *ast.MemberExpression) bool {
	key := memberPathKey(m)
	root := m.Object
	for {
		mm, ok := root.(*ast.MemberExpression)
		if !ok {
			break
		}
		root = mm.Object
	}
	id, ok := root.(*ast.Identifier)
	if key == "" || !ok {
		return true
	}
	f, _ := b.FlowAt(id)
	if f == nil {
		return true
	}
	mentions := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(x ast.Node) bool {
			if found {
				return false
			}
			if e, ok := x.(ast.Expression); ok && memberPathKey(e) == key {
				found = true
			}
			return true
		})
		return found
	}
	seen := map[*binder.FlowNode]bool{}
	var walk func(f *binder.FlowNode) bool
	walk = func(f *binder.FlowNode) bool {
		for f != nil {
			if seen[f] {
				return false
			}
			seen[f] = true
			switch {
			case f.Flags&binder.FlowUnreachable != 0:
				return false
			case f.Flags&binder.FlowCall != 0:
				return true
			case f.Flags&(binder.FlowTrueCondition|binder.FlowFalseCondition) != 0:
				if f.Node != nil && mentions(f.Node) {
					return false // the narrowing: nothing ran since
				}
			case f.Flags&binder.FlowPropertyAssignment != 0:
				if a, ok := f.Node.(*ast.AssignmentExpression); ok && memberPathKey(a.Left) == key {
					return false
				}
			case f.Flags&(binder.FlowBranchLabel|binder.FlowLoopLabel) != 0:
				for _, a := range f.Antecedents {
					if walk(a) {
						return true
					}
				}
				return false
			case f.Flags&binder.FlowStart != 0:
				return true
			}
			f = f.Antecedent
		}
		return true
	}
	return walk(f)
}

// flowTrusted reports whether the checker's flow type for the reference ref
// holds at run time: no other function stores to its variable, and, for a
// property, no call runs between the narrowing and the read. TypeScript's
// narrowing (and the checker's) ignores both.
func (e *Emitter) flowTrusted(ref ast.Expression) bool {
	if e.frontBinding == nil {
		return false
	}
	return flowTrusted(e.frontBinding, ref)
}

func flowTrusted(b *binder.Binding, ref ast.Expression) bool {
	switch r := ref.(type) {
	case *ast.Identifier:
		sym, _ := b.Resolve(r)
		return sym == nil || !b.StoredElsewhere(sym)
	case *ast.MemberExpression:
		return !callBetweenNarrowing(b, r)
	}
	return true
}

// stdioBinding reports a binding initialized with a stdio stream or its
// member (`const out = process.stdout`, `const t = process.stdout.isTTY`):
// typed by tty's class whatever the stream is, it holds the box.
func (e *Emitter) stdioBinding(id *ast.Identifier) bool {
	c := e.front()
	if c == nil {
		return false
	}
	sym, _ := c.Binding().Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if !ok || v.TypeAnnot != nil {
		return false
	}
	m, ok := v.Init.(*ast.MemberExpression)
	if !ok {
		return false
	}
	if _, stdio := e.processStdioGetter(m); stdio {
		return true
	}
	om, ok := m.Object.(*ast.MemberExpression) // `const t = process.stdout.isTTY`
	if !ok {
		return false
	}
	_, stdio := e.processStdioGetter(om)
	return stdio
}

// stdioStream reports a read of a stdio stream: `process.stdout`, or a
// binding of one.
func (e *Emitter) stdioStream(x ast.Expression) bool {
	switch x := x.(type) {
	case *ast.MemberExpression:
		_, ok := e.processStdioGetter(x)
		return ok
	case *ast.Identifier:
		return e.stdioBinding(x)
	}
	return false
}

// erasedCallbackParam is the type parameter i of an arrow passed to an
// erased generic method of a program class takes: the erased signature's
// hint (emitArrowFunctionWithHints), its type parameters boxes.
func (e *Emitter) erasedCallbackParam(fn *ast.ArrowFunction, i int) (Type, bool) {
	c := e.front()
	if c == nil {
		return Type{}, false
	}
	call, ok := c.ParentOf(fn).(*ast.CallExpression)
	if !ok || call.Optional {
		return Type{}, false
	}
	mem, ok := call.Callee.(*ast.MemberExpression)
	if !ok || mem.Optional {
		return Type{}, false
	}
	objTy := e.inferExprType(mem.Object)
	if !objTy.IsClass() {
		return Type{}, false
	}
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return Type{}, false
	}
	m := info.Methods[mem.Property]
	sig, hasSig := info.MethodSigs[mem.Property]
	if m == nil || !m.ErasedMethod || !hasSig || sig.This {
		return Type{}, false
	}
	for k, a := range call.Args {
		if a != ast.Expression(fn) {
			continue
		}
		if k >= len(sig.ParamTypes) {
			return Type{}, false
		}
		pt := sig.ParamTypes[k]
		if !pt.IsFunc() || i >= len(pt.FuncParams) {
			return Type{}, false
		}
		return e.canonicalizeClassTy(pt.FuncParams[i]), true
	}
	return Type{}, false
}
