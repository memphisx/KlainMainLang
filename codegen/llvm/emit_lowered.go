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
	target string
	links  []string
	// recv is the receiver's representation for a method of a primitive's
	// apparent type (`s.trim()`), passed as the first argument; nil for a
	// method of a global object (`Math.sin(x)`).
	recv   *Type
	params []loweredParam
	result Type
	// resultOptional marks a `T | undefined` result: the entry point
	// returns whether it is present and writes the value through a final
	// pointer argument (a C struct return has no portable IR form).
	resultOptional bool
}

// resultType is the type a lowered call's value has.
func (l *lowering) resultType() Type {
	if l.resultOptional {
		return undefinedableElem(l.result)
	}
	return l.result
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
func (e *Emitter) loweredCall(ex *ast.CallExpression) (*lowering, bool) {
	if ex.Optional || len(ex.TypeArgs) > 0 {
		return nil, false
	}
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok || mem.Optional {
		return nil, false
	}
	c := e.front()
	if c == nil {
		return nil, false
	}
	var recv *Type
	var decl ast.Node
	var overloads []ast.Node
	var fn *checker.Type
	if id, ok := mem.Object.(*ast.Identifier); ok && e.isBuiltinGlobal(c, id) && c.TypeOf(id).Flags&checker.Object != 0 {
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
	} else {
		return nil, false
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
	for _, a := range ex.Args {
		if _, ok := a.(*ast.SpreadElement); ok {
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
	id, ok := a.(*ast.Identifier)
	return ok && id.Name == "undefined" && !e.isShadowedByLocal("undefined")
}

// lowerRepr is the representation a declared type has at a lowered call's
// boundary; the types this path represents so far.
func lowerRepr(t *checker.Type) (Type, bool) {
	switch {
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
// runtime passes (a number, a boolean, a string).
func lowerCallback(t *checker.Type) ([]Type, bool) {
	if t.Flags&checker.Object == 0 || t.Kind != checker.Function || len(t.Overloads) > 0 || len(t.TypeParams) > 0 {
		return nil, false
	}
	if t.Result == nil || t.Result.Flags&checker.Void == 0 {
		return nil, false
	}
	params := []Type{}
	for _, pt := range t.Params {
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
	mem := ex.Callee.(*ast.MemberExpression)
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
	for i, a := range ex.Args {
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
	if restAt >= 0 {
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

// emitLoweredInvoker emits `void @__kml_lower_inv_N(ptr %clo, params…)`: the
// runtime's typed caller of a callback passed to a lowered entry point, which
// converts each value the runtime passes to the closure's own parameter
// representation.
func (e *Emitter) emitLoweredInvoker(fnTy Type, params []Type) (string, error) {
	e.lowerInvCtr++
	fn := fmt.Sprintf("@__kml_lower_inv_%d", e.lowerInvCtr)
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
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(%s) {\nentry:\n%s}\n", fn, strings.Join(append([]string{"ptr %clo"}, sig...), ", "), body))
	return fn, nil
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
	"tls":     (*Emitter).ensureNativeTLS,
}

// runtimeSymbols are the `@lower` targets the IR runtime defines, rather
// than declares: using one emits its definition.
var runtimeSymbols = map[string]func(*Emitter){
	"__kml_trim":       (*Emitter).ensureStringTrim,
	"__kml_trim_start": (*Emitter).ensureStringTrimStart,
	"__kml_trim_end":   (*Emitter).ensureStringTrimEnd,
	// lib/native.d.ts
	"__kml_native_fs_error":   (*Emitter).ensureNativeFsError,
	"__kml_native_errno_name": (*Emitter).ensureNativeErrno,
	"__kml_native_errno_desc": (*Emitter).ensureNativeErrno,
	"__kml_native_uv_errno":   (*Emitter).ensureNativeErrno,
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
	if !ok || !ret.IsClass {
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
