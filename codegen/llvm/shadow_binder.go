// shadow_binder.go — the new front end's side of the shadow comparison
// (TDD-00230 P2.1–P2.3): the binder answers name resolution, the checker the
// type questions, its types mapped to codegen's representation by reprOf.
package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/options"
	"KlainMainLang/resolver"
	"strconv"
	"strings"
)

type frontOracle struct {
	b *binder.Binding
	c *checker.Checker
	r reprCtx
	// q memoizes ExprType within one outermost query, over which codegen's
	// state does not change: the rules that read a receiver's
	// representation would otherwise retype a call chain's receivers once
	// per rule and link.
	q *oracleQuery
	// executors are the functions passed to `new Promise(…)`; onRejected
	// maps a `.catch(f)` / `.then(_, f)` handler literal to its receiver.
	executors  map[ast.Node]*ast.NewExpression
	onRejected map[ast.Node]ast.Expression
	// anyBound are the function literals initializing an `any` binding:
	// emitted with the boxed convention, a rest parameter one box.
	anyBound map[ast.Node]bool
	// inBody holds the functions whose body result inferredBodyReturn is
	// computing.
	inBody map[ast.Node]bool
	// widenedReduceOf maps a js-lane reduce callback that can fall off the
	// end to its call (jsWidenedReduceCallback).
	widenedReduceOf map[ast.Node]*ast.CallExpression
}

func init() {
	NewShadowOracle = func(prog *ast.Program, opts options.Options, r reprCtx) ShadowOracle {
		// The lane's own rules: Annex B binding and the checker's js mode.
		libProgs, _ := lib.Programs() // a parse error in them fails the strict lane's check first
		b := binder.BindWith(prog, binder.Options{AnnexB: opts.CompatJS(), Lib: libProgs})
		return frontOracle{b: b, c: checker.NewWith(b, opts), r: r}.withPromiseCallbacks(prog)
	}
}

// ExprType answers inferExprType, which gives a variable or property read
// the representation of its storage: the checker's storage type, not the
// type the read narrows it to.
func (o frontOracle) ExprType(e ast.Expression) (Type, bool) {
	if o.q == nil {
		return o.exprTypeQuery(e)
	}
	key := oracleKey{e: e}
	if o.r.scopeKey != nil {
		key.depth, key.base = o.r.scopeKey()
	}
	if o.q.depth == 0 {
		o.q.memo = map[oracleKey]oracleAnswer{}
	} else if a, ok := o.q.memo[key]; ok {
		return a.t, a.ok
	}
	o.q.depth++
	t, ok := o.exprTypeQuery(e)
	o.q.depth--
	o.q.memo[key] = oracleAnswer{t, ok}
	if o.q.depth == 0 {
		o.q.memo = nil
	}
	return t, ok
}

type oracleQuery struct {
	depth int
	memo  map[oracleKey]oracleAnswer
}

// oracleKey is a node under the scope stack codegen asked under: a name's
// answer depends on its binding.
type oracleKey struct {
	e     ast.Expression
	depth int
	base  uintptr
}

type oracleAnswer struct {
	t  Type
	ok bool
}

// exprTypeQuery is ExprType, unmemoized.
func (o frontOracle) exprTypeQuery(e ast.Expression) (Type, bool) {
	if o.r.chainCopy != nil {
		if orig, isCopy := o.r.chainCopy(e); isCopy {
			return o.chainCopyType(e, orig)
		}
	}
	ty, ok := o.exprTypeBase(e)
	// A link of an optional chain that can short-circuit there is what
	// emitOptionalChain makes of the read: the read's value or undefined.
	if ok && checker.InOptionalChain(e) && !isUnconstrainedDynamic(ty) && o.c.ChainNonOptional(e) != o.c.TypeOf(e) {
		ty = optionalCallResultType(optCalleeValue, ty)
	}
	return ty, ok
}

// exprTypeBase is ExprType before an optional chain's undefined.
func (o frontOracle) exprTypeBase(e ast.Expression) (Type, bool) {
	ty, ok := o.exprTypeOuter(e)
	// js lane: a reduce whose callback can fall off the end widens its
	// accumulator, and so its result, to `T | undefined` (emitReduce).
	if o.r.js != nil && o.r.js() && ok {
		if call, isC := e.(*ast.CallExpression); isC {
			if cb := jsWidenedReduceCallback(call); cb != nil && reduceAccWidenable(ty) {
				return undefinedableElem(ty), true
			}
		}
		if id, isID := e.(*ast.Identifier); isID {
			if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
				if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
					if call, isC := v.Init.(*ast.CallExpression); isC && jsWidenedReduceCallback(call) != nil && reduceAccWidenable(ty) {
						return undefinedableElem(ty), true
					}
				}
			}
		}
	}
	// A binding of a builtin's type narrowed to a program class extending
	// it (`m instanceof C`, `class C extends Map`) reads as that class: the
	// instance is the class's object (ADR-01469).
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.IsClass && !ty.IsDynamic && flowTrusted(o.b, id) && o.r.class != nil {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Object != 0 && nt.Kind == checker.Instance && nt.Symbol != nil && nt.Base != nil {
			if ct, cok := o.r.class(nt.Symbol); cok && ct.IsClass {
				return ct, true
			}
		}
	}
	// A function reading `arguments` gains its implicit rest at emission
	// (maybeAddArgumentsRestParams), whatever the checker saw: its value
	// carries that rest, so a call through the value packs what it passes.
	if ok && ty.IsFunc && !ty.FuncHasRest {
		var decl ast.Node
		switch x := e.(type) {
		case *ast.Identifier:
			if sym, _ := o.b.Resolve(x); sym != nil && len(sym.Declarations) == 1 {
				decl = sym.Declarations[0].Node
			}
		case *ast.MemberExpression:
			decl = o.c.MemberDecl(x)
		}
		if fd, isFD := decl.(*ast.FunctionDeclaration); isFD && fd.Body != nil {
			if ps := maybeAddArgumentsRestParams(fd.Params, fd.Body.Body); hasArgumentsRestParamSlice(ps) && len(ty.FuncParams) == len(ps)-1 {
				ft := ty
				ft.FuncParams = append(append([]Type(nil), ty.FuncParams...), ArrayOf(TypeAny))
				ft.FuncHasRest = true
				ft.FuncParamNames = nil
				for _, p := range ps {
					ft.FuncParamNames = append(ft.FuncParamNames, p.Name)
				}
				_, isID := e.(*ast.Identifier)
				ft.FuncCountsArgs = isID
				return ft, true
			}
		}
	}
	// An unannotated function returns what its body's returns read as
	// (ReturnType): its value's and its calls' result follow.
	if ok && (ty.IsFunc || !ty.IsDynamic) {
		var fn ast.Node
		target := e
		if call, isCall := e.(*ast.CallExpression); isCall {
			target = call.Callee
		}
		switch x := target.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			fn = x
		case *ast.Identifier:
			if sym, _ := o.b.Resolve(x); sym != nil && len(sym.Declarations) == 1 {
				switch d := sym.Declarations[0].Node.(type) {
				case *ast.FunctionDeclaration:
					fn = d
				case *ast.VarDeclaration:
					if d.TypeAnnot == nil {
						switch d.Init.(type) {
						case *ast.ArrowFunction, *ast.FunctionExpression:
							fn = d.Init
						}
					}
				}
			}
		}
		if rt, rok := o.inferredBodyReturn(fn); rok {
			if _, isCall := e.(*ast.CallExpression); isCall {
				if !ty.IsFunc && rt.Nullable && !ty.Nullable && rt.IR == ty.IR {
					return rt, true
				}
			} else if ty.IsFunc && ty.FuncRetType != nil && rt.Nullable && !ty.FuncRetType.Nullable && rt.IR == ty.FuncRetType.IR {
				ft := ty
				ft.FuncRetType = &rt
				return ft, true
			}
		}
	}
	// An unannotated binding initialized `null` and never given a value
	// (its evolving type's storage is `null | undefined`) holds the null.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsNull && ty.IsUndefined {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if _, isNull := v.Init.(*ast.NullLiteral); isNull {
					ty.IsUndefined = false
					return ty, true
				}
			}
		}
	}
	// An unannotated binding of an object literal with a getter or setter
	// holds the literal's synthetic class (TDD-00153).
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.IsClass && o.r.objLit != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && hasAccessor(lit) {
					if ct, cok := o.r.objLit(lit); cok {
						return ct, true
					}
				}
			}
		}
	}
	// A `var` whose declaration can be skipped on the way to a read holds
	// `T | undefined` (hoistedVarMaySkip, ADR-01057).
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.IsDynamic && ty.IR != "void" {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && sym.Scope != nil && o.hoistSkippable(v) {
				return indexReadType(ty), true
			}
		}
	}
	// A class field codegen widened to a box (a `null`-initialized field
	// later given a value: classFieldNullEvolves) reads as that box.
	if m, isM := e.(*ast.MemberExpression); isM && ok && !ty.IsDynamic {
		if rt, rok := o.ExprType(m.Object); rok && rt.IsClass {
			for _, name := range []string{m.Property, llvmSafeSymbol(m.Property)} {
				if _, ft, found := rt.FieldIndex(name); found {
					if ft.IsDynamic && (ty.IsNull || ty.IsUndefined) {
						return TypeAny, true
					}
					break
				}
			}
		}
	}
	// A method read as a value is the bound method closure, whose call
	// passes the arguments past its parameters in the rest without a count
	// (the method's own entry counts them): no FuncCountsArgs.
	if _, isM := e.(*ast.MemberExpression); isM && ok && ty.IsFunc && ty.FuncCountsArgs {
		ty.FuncCountsArgs = false
	}
	// A `/** @value */` array binding holds the flat value-type array.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsArray && ty.ElemType != nil && flatArrayElemOK(*ty.ElemType) {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.ValueArr {
				return FlatArrayType(*ty.ElemType), true
			}
		}
	}
	// An untyped binding codegen widened to a box holds a box.
	if id, isID := e.(*ast.Identifier); isID && o.r.widened != nil && o.r.widened(id.Name) {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && v.Kind != "const" {
				return TypeAny, true
			}
		}
	}
	// An unannotated binding is held as an initializer codegen holds boxed.
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && o.dynamicInit(v) {
				return o.ExprType(v.Init)
			}
		}
	}
	// A binding holding a void call's result holds undefined.
	if _, isID := e.(*ast.Identifier); isID && ok && ty.IR == "void" {
		return TypeUndefined, true
	}
	// A read of a property an annotation declares a tuple holds that tuple.
	if m, isM := e.(*ast.MemberExpression); isM {
		ps, isPS := o.c.MemberDecl(m).(*ast.PropertySignature)
		rt := o.c.NonNullable(o.c.TypeOf(m.Object))
		declared := isPS && ps.Type != nil || !o.c.Unanswered(rt) && rt.Flags&checker.Object != 0 && (rt.Kind == checker.Interface || rt.Kind == checker.Instance)
		if declared {
			if nt := o.c.TypeOf(m); !o.c.Unanswered(nt) && hasTuple(nt) {
				r := o.r
				r.tuples = true
				if tt, tok := r.of(nt); tok {
					ty, ok = tt, true
				}
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID {
		// A binding an annotation declares a tuple holds that tuple; an
		// unannotated one, what its initializer is held as; a function
		// declared to return one returns it.
		if sym, _ := o.b.Resolve(id); sym != nil {
			if len(sym.Declarations) == 1 {
				if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType != nil {
					if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Object != 0 && nt.Kind == checker.Function && hasTuple(nt.Result) {
						r := o.r
						r.tuples = true
						if ft, fok := r.of(nt); fok {
							ty, ok = ft, true
						}
					}
				}
			}
			if pm := declaredParam(sym, id.Name); pm != nil && pm.Type != nil && !pm.Rest {
				if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && hasTuple(nt) {
					r := o.r
					r.tuples = true
					if pt, pok := r.of(nt); pok {
						ty, ok = pt, true
					}
				}
			}
			if v, isV := singleVarDecl(sym); isV {
				if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && hasTuple(nt) {
					if v.TypeAnnot != nil {
						r := o.r
						r.tuples = true
						ty, ok = r.of(nt)
					} else if v.Init != nil {
						ty, ok = o.ExprType(v.Init)
					}
				}
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.Nullable && (ty.IR == "i1" || ty.IR == "double" && ty.Float || ty.IsObject && !ty.IsDynamic || isStringTy(ty)) {
		// A number, boolean, string or object an array pattern binds without a default from
		// a source that is not a literal may be past its end: an
		// undefined-capable slot (unpackArrayPatternInto).
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			var el *ast.ArrayPatternElem
			switch d := sym.Declarations[0].Node.(type) {
			case *ast.ArrayDestructuring:
				if _, lit := d.Init.(*ast.ArrayLiteral); !lit {
					el = findArrayPatternElem(d.Elems, id.Name)
				}
			case *ast.ObjectDestructuring:
				if _, lit := d.Init.(*ast.ObjectLiteral); !lit {
					el = findArrayPatternElemIn(d.Props, id.Name)
				}
			}
			if el != nil && el.Default == nil && !el.Rest {
				ty.Nullable, ty.IsUndefined = true, true
				return ty, true
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID && ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV {
				if o.dynamicInit(v) {
					return o.ExprType(v.Init) // held as its initializer is: boxed
				}
				if sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope && o.evolvingBoxed(v) {
					return ArrayOf(TypeAny), true
				}
				st := o.undefinedSlot(v, sym, ty)
				if st.Nullable && !ty.Nullable && o.presentRead(id, st, v.Init != nil) {
					return ty, true
				}
				return st, true
			}
		}
	}
	return ty, ok
}

// dynamicInit reports an unannotated binding whose initializer codegen
// holds boxed though its static type is narrower (`Object.defineProperty(
// {}, …)`, typed `{}`): the binding is held boxed too.
func (o frontOracle) dynamicInit(v *ast.VarDeclaration) bool {
	if v.TypeAnnot != nil || v.Init == nil || v.Ambient {
		return false
	}
	if ol, isOL := v.Init.(*ast.ObjectLiteral); isOL && ol.HasComputedKey() && (o.r.js == nil || !o.r.js()) {
		return true
	}
	switch v.Init.(type) {
	case *ast.CallExpression, *ast.ArrayLiteral, *ast.MemberExpression, *ast.NonNullExpression:
	default:
		return false
	}
	it, ok := o.ExprType(v.Init)
	if !ok {
		return false
	}
	t := o.c.TypeOf(v.Init)
	if o.c.Unanswered(t) {
		return false
	}
	if lit, isLit := v.Init.(*ast.ArrayLiteral); isLit && len(lit.Elements) == 0 {
		return false // evolvingBoxed's
	}
	if lit, isLit := v.Init.(*ast.ArrayLiteral); isLit && o.r.js != nil && o.r.js() && len(lit.Elements) > 0 {
		if _, nested := lit.Elements[0].(*ast.ArrayLiteral); nested {
			mt, ok := o.r.of(t)
			return !ok || reprKey(mt) != reprKey(it) // the js lane's nested boxes
		}
	}
	if it.IsArray && it.ElemType != nil && isUnconstrainedDynamic(*it.ElemType) {
		// An array of boxes: held so unless the checker's is one too.
		return t.Flags&checker.Object == 0 || t.Kind != checker.Array || t.Elem.Flags&(checker.Any|checker.Unknown) == 0
	}
	if t.Flags&checker.Any != 0 && it.IsArray {
		return true // a call the checker leaves `any` that codegen types (Object.assign)
	}
	return isUnconstrainedDynamic(it) && t.Flags&(checker.Any|checker.Unknown) == 0
}

// undefinedSlot is the storage a binding annotated with a plain number,
// boolean or string has when it may hold undefined, which every read of it
// is typed as (emitVarDecl, globalUndefinedStorage): declared without a
// value, or given one that may be undefined (`let v: number = xs[i]`, a
// read past the end), as Node keeps it.
func (o frontOracle) undefinedSlot(v *ast.VarDeclaration, sym *binder.Symbol, ty Type) Type {
	if v.TypeAnnot == nil || v.Ambient || ty.Nullable || ty.IsDynamic || len(ty.UnionMembers) > 0 {
		return ty
	}
	num, boolean, str := ty.Float && ty.IR == "double", ty.IR == "i1", isStringTy(ty)
	global := sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope
	if !num && !str && !(boolean && (global || v.Init == nil)) {
		return ty
	}
	if v.Init != nil {
		it, ok := o.ExprType(v.Init)
		if !ok || !it.Nullable || !it.IsUndefined || it.NullAndUndef {
			return ty
		}
		if str != isStringTy(it.withoutNullable()) || num && !it.withoutNullable().Float {
			return ty
		}
	}
	if str {
		ty.Nullable, ty.IsUndefined = true, true
		return ty
	}
	nt := undefinedableElem(ty)
	nt.IsUndefined = true
	return nt
}

// presentRead mirrors emitIdent's checkerPresent/assignedString for a
// binding undefinedSlot holds: a read every path to which has stored a
// present value, or that flow proves present, is the payload.
// An initializer that may be absent marks the slot as an element read's
// (unchecked): only the stores themselves prove it present then.
func (o frontOracle) presentRead(id *ast.Identifier, ty Type, absentInit bool) bool {
	if !flowTrusted(o.b, id) {
		return false
	}
	if o.reprStoresPresent(id) {
		return true
	}
	if ty.UncheckedIndex || absentInit {
		return false
	}
	if o.b.DefinitelyAssigned(id) {
		return true
	}
	nt, st := o.c.TypeOf(id), o.c.StorageTypeOf(id)
	return !o.c.Unanswered(st) && !o.c.Unanswered(nt) && !presentType(st) && presentType(nt)
}

func (o frontOracle) exprTypeOuter(e ast.Expression) (Type, bool) {
	// A dynamic entry's widened parameter is a box, and so is a member
	// read or a method call on it.
	if o.r.widenedParam != nil && o.widenedUse(e) {
		return TypeAny, true
	}
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.codegenCall != nil {
		if t, ok := o.r.codegenCall(call); ok {
			return t, true
		}
	}
	// A call of an erased generic method on a generic class's instance
	// (`als.run(store, cb)`'s `R`) is codegen's box (genericMethodResult).
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.erasedCall != nil {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && !call.Optional && o.mayBeGenericInstance(m.Object) {
			rt, rok := o.ExprType(m.Object)
			if !rok && o.r.inferred != nil {
				rt, rok = o.r.inferred(m.Object) // a generic class's instance
			}
			if rok && rt.IsClass {
				if t, ok := o.r.erasedCall(call, rt); ok && !(isUnconstrainedDynamic(t) && o.c.TypeOf(call).Flags&checker.NumberLike != 0) {
					return t, true // a number result stays a number
				}
			}
		}
	}
	if ty, ok := o.tupleExprType(e); ok {
		return ty, true
	}
	// An array method that copies its receiver's elements (`filter`,
	// `slice`, `concat`) copies a boxed-element array's boxes, whatever
	// narrower type the checker gives the result.
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && elementCopyingMethod[m.Property] {
			if rt, ok := o.ExprType(m.Object); ok && rt.IsArray && !rt.IsTypedArray && rt.ElemType != nil && reprKey(*rt.ElemType) == reprKey(TypeAny) {
				if ct := o.c.TypeOf(call); !o.c.Unanswered(ct) && ct.Flags&checker.Object != 0 && ct.Kind == checker.Array && ct.Elem.Flags&checker.Any == 0 {
					return ArrayOf(TypeAny), true
				}
			}
		}
	}
	// A callback passed where an overloaded function's implementation takes
	// `any` (`dns.resolve6(h, opts, (e, a) => …)`) is boxed: its parameters
	// are boxes whichever overload the checker picked.
	if id, isID := e.(*ast.Identifier); isID {
		if o.anyImplCallbackParam(id) {
			return TypeAny, true
		}
	}
	// An unannotated callback parameter given a boxed-element array's
	// elements (`items.filter((x) => …)`) holds those boxes.
	if id, isID := e.(*ast.Identifier); isID {
		if el, ok := o.boxedElementParam(id); ok {
			return el, true
		}
	}
	// A non-null assertion of a box is that box.
	if nn, isNN := e.(*ast.NonNullExpression); isNN {
		if it, ok := o.ExprType(nn.Arg); ok && isUnconstrainedDynamic(it) {
			return it, true
		}
	}
	// A builtin type's method read as a value (`m.get`, `/x/.test`,
	// `process.send`) is a host function codegen boxes; a global
	// namespace's static (`Math.max`) is its intrinsic.
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional && o.c.LibraryMethod(m) && !o.loweredStatic(m) && (o.intrinsicKey(m) == "" || strings.HasPrefix(o.intrinsicKey(m), "crypto.")) {
		return TypeAny, true // `crypto` is an object: its methods are its own
	}

	// A method an Array subclass inherits that returns `this` (`sort`,
	// `reverse`, `fill`, `copyWithin`) returns the receiver as the array
	// it is (the instance's array header, ADR-01469).
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && thisReturningArrayMethod[m.Property] {
			if rt := o.c.NonNullable(o.c.TypeOf(m.Object)); !o.c.Unanswered(rt) && rt.Flags&checker.Object != 0 && rt.Kind == checker.Instance {
				if el := o.c.IteratedType(rt); el != nil && !o.c.Unanswered(el) {
					if er, ok := o.r.of(el); ok {
						return ArrayOf(er), true
					}
				}
			}
		}
	}
	// `Object.assign(target, …)` returns its target; a `[]` target takes
	// the first array source's elements (emitObjectAssign).
	if call, isCall := e.(*ast.CallExpression); isCall && len(call.Args) >= 2 {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && o.intrinsicKey(m) == "Object.assign" {
			if lit, isLit := call.Args[0].(*ast.ArrayLiteral); isLit && len(lit.Elements) == 0 {
				if st, ok := o.ExprType(call.Args[1]); ok && st.IsArray {
					return st, true
				}
			} else if tt, ok := o.ExprType(call.Args[0]); ok && tt.IsArray {
				return tt, true
			}
		}
	}
	// A call of an overloaded function runs its implementation: one
	// declared to return `any` returns a box, whichever overload the
	// checker picked (`execSync(cmd, { encoding })`).
	if call, isCall := e.(*ast.CallExpression); isCall {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if sym, _ := o.b.Resolve(id); sym != nil {
				for _, d := range sym.Declarations {
					if fd, isFD := d.Node.(*ast.FunctionDeclaration); isFD && len(fd.Overloads) > 0 && fd.ReturnType != nil && !fd.IsAsync && !fd.IsGenerator && o.voidOverValue(call, fd) {
						return TypeAny, true
					}
					if fd, isFD := d.Node.(*ast.FunctionDeclaration); isFD && len(fd.Overloads) > 0 && fd.ReturnType != nil && fd.ReturnType.Name == "any" && !fd.IsAsync && !fd.IsGenerator {
						if ct := o.c.TypeOf(call); o.c.Unanswered(ct) || ct.Flags&checker.Union != 0 && o.c.NonNullable(ct).Flags&checker.Union != 0 || o.objectResult(call) {
							return TypeAny, true // an overload of several kinds: the implementation's box
						}
					}
				}
			}
		}
	}
	// `Promise.all([...])` of a literal holding a thenable class instance
	// (unwrapped by its `then` at run time) resolves the literal's own
	// elements, boxed, not the checker's awaited ones.
	if call, isCall := e.(*ast.CallExpression); isCall && len(call.Args) == 1 && o.r.inferred != nil {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && o.intrinsicKey(m) == "Promise.all" {
			if lit, isLit := call.Args[0].(*ast.ArrayLiteral); isLit && o.holdsThenableInstance(lit) {
				if lt, lok := o.r.inferred(lit); lok && lt.IsArray && lt.ElemType != nil && lt.ElemType.IsDynamic {
					if ty, ok := o.r.of(o.c.TypeOf(call)); ok && ty.IsPromise {
						at := lt
						ty.PromiseType = &at
						return ty, true
					}
				}
			}
		}
	}
	// `then`/`catch`/`finally` on a Promise subclass's instance makes another
	// of that class (species), settling with the checker's value.
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && (m.Property == "then" || m.Property == "catch" || m.Property == "finally") {
			if rt, rok := o.ExprType(m.Object); rok && rt.IsClass && (rt.PromiseType != nil || o.extendsPromise(o.c.TypeOf(m.Object))) {
				if ty, ok := o.r.of(o.c.TypeOf(call)); ok && ty.IsPromise && ty.PromiseType != nil {
					out := rt
					pt := *ty.PromiseType
					out.PromiseType = &pt
					return out, true
				}
			}
		}
	}
	// `await p` of a promise codegen holds resolving a box is that box; so
	// is an unannotated binding of it.
	if aw := o.awaitedOperand(e); aw != nil {
		if pt, pok := o.ExprType(aw); pok && pt.IsPromise && pt.PromiseType != nil && pt.PromiseType.IsDynamic {
			return *pt.PromiseType, true
		}
	}
	// `f.bind(…)` is the value emitFunctionBind builds from f's.
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.boundFunc != nil {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && m.Property == "bind" && !m.Optional && !call.Optional {
			if ft, fok := o.ExprType(m.Object); fok && ft.IsFunc {
				if bt, bok := o.r.boundFunc(ft, len(call.Args)); bok {
					return bt, true
				}
			}
		}
	}
	if call, isCall := e.(*ast.CallExpression); isCall && o.boxedArrayCall(call) {
		return TypeAny, true
	}
	// `str.match(re)` builds exec's match array (its header carrying index
	// and input); a RegExp that may be global may give the plain array of
	// every match instead (emitStringMatch).
	// … as do its `!` and an unannotated binding of either, until
	// narrowed or reassigned.
	if t, ok := o.stringMatch(e); ok {
		return t, true
	}
	// A method with overloads runs its implementation: one declared to
	// return `any` or `Promise<any>` (SubtleCrypto's), where an overload
	// returns a promise, resolves a box.
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM {
			fd, isFD := o.c.MemberDecl(m).(*ast.FunctionDeclaration)
			if !isFD && o.r.classMethod != nil {
				// Declared by an interface, run by the class codegen lays the
				// receiver out as.
				rt, rok := o.ExprType(m.Object)
				if !rok && o.r.inferred != nil {
					rt, rok = o.r.inferred(m.Object) // a receiver the checker types by a library interface
				}
				if rok && rt.IsClass {
					fd = o.r.classMethod(rt.ClassName, m.Property)
					isFD = fd != nil
				}
			}
			if isFD && len(fd.Overloads) > 0 && fd.ReturnType != nil && !fd.IsAsync && !fd.IsGenerator {
				if fd.ReturnType.Name == "any" && (!o.overloadsAgree(call) && o.anyArgument(call) || o.objectResult(call)) {
					return TypeAny, true // the implementation's box
				}
				if o.voidOverValue(call, fd) {
					return TypeAny, true
				}
				if rt := fd.ReturnType; rt.Name == "any" || rt.Name == "Promise" && rt.ElemType != nil && rt.ElemType.Name == "any" {
					if ty, ok := o.r.of(o.c.TypeOf(call)); ok && ty.IsPromise {
						ty.PromiseType = &TypeAny
						return ty, true
					}
				}
			}
		}
	}
	// An object literal with a computed key is a dynamic object: a
	// dictionary of its values' one representation, else a box
	// (inferDynamicObjectType).
	if ol, isOL := e.(*ast.ObjectLiteral); isOL && ol.HasComputedKey() && (o.r.js == nil || !o.r.js()) {
		return o.computedKeyObject(ol)
	}
	// An array literal with an element codegen holds boxed though its
	// static type is narrower is an array of boxes.
	if al, isAL := e.(*ast.ArrayLiteral); isAL {
		for _, el := range al.Elements {
			if el == nil {
				continue
			}
			if _, spread := el.(*ast.SpreadElement); spread {
				continue
			}
			if et, ok := o.ExprType(el); ok && isUnconstrainedDynamic(et) {
				if t := o.c.TypeOf(el); !o.c.Unanswered(t) && t.Flags&(checker.Any|checker.Unknown) == 0 {
					return ArrayOf(TypeAny), true
				}
			}
		}
	}
	// A conditional with one branch an undefined-capable scalar slot (a
	// binding undefinedSlot holds) and the other that scalar is held as the
	// slot: its undefined survives as the conditional's value.
	if c, isC := e.(*ast.ConditionalExpression); isC {
		ct, cok := o.ExprType(c.Consequent)
		at, aok := o.ExprType(c.Alternate)
		if cok && aok {
			for _, p := range [][2]Type{{ct, at}, {at, ct}} {
				u, other := p[0], p[1]
				if u.Nullable && u.IsUndefined && !u.NullAndUndef && !u.IsDynamic && !other.Nullable &&
					reprKey(u.withoutNullable()) == reprKey(other) && (isNullableScalar(u) || isStringTy(u)) {
					return u, true
				}
			}
		}
	}
	// An assertion of a caught value keeps the caught pair (`err as Error`
	// is erased, as emitAsExpression erases it), and so does an untyped
	// binding it initializes.
	if as, isAs := e.(*ast.AsExpression); isAs {
		if inner, ok := o.ExprType(as.Expr); ok && inner.IsCaught {
			return inner, true
		}
	}
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, ok := singleVarDecl(sym); ok && v.TypeAnnot == nil {
				if as, isAs := v.Init.(*ast.AsExpression); isAs {
					if inner, ok := o.ExprType(as.Expr); ok && inner.IsCaught {
						return inner, true
					}
				}
			}
		}
	}
	// A -compat=js module var declared with values of different kinds is
	// one any box (crossTypeWidenedBindings).
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil && o.jsWidenedVar(sym) {
			return TypeAny, true
		}
	}
	// A typed array's view properties, as emitTypedArrayViewProp reads them
	// (its buffer may be shared: a box).
	if m, isM := e.(*ast.MemberExpression); isM && o.typedArrayView(m) {
		if m.Property == "buffer" {
			return TypeAny, true
		}
		return TypeI64, true
	}
	// `X["k"]` on a builtin global reads as `X.k` (builtinLiteralMember).
	if ix, isIx := e.(*ast.IndexExpression); isIx && !ix.Optional {
		if id, isID := ix.Object.(*ast.Identifier); isID && knownGlobalNamespace[id.Name] {
			if lit, isLit := ix.Index.(*ast.StringLiteral); isLit && isIdentifierName(lit.Value) {
				if sym, _ := o.b.Resolve(id); sym != nil && o.r.libGlobal(sym) {
					if lit.Value == "prototype" {
						return TypeAny, true
					}
					t, ok := o.exprTypeRaw(e)
					if m := ast.NewMemberExpression(id, lit.Value, ix.GetPos()); ok && t.IsFunc && o.intrinsicMethodValue(m) {
						t = o.intrinsicValueType(o.intrinsicKey(m), o.c.TypeOf(e), t)
					}
					return t, ok
				}
			}
		}
	}
	t, ok := o.exprTypeRaw(e)
	// … and an unannotated binding of one holds it so.
	if id, isID := e.(*ast.Identifier); ok && isID {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if m, isM := v.Init.(*ast.MemberExpression); isM && (o.intrinsicMethodValue(m) || o.typedArrayView(m)) {
					return o.ExprType(m)
				}
			}
		}
	}
	// A builtin object's intrinsic method as a value takes every argument
	// boxed, converting it as the builtin does (ensureBuiltinFunc).
	if m, isM := e.(*ast.MemberExpression); ok && isM && t.IsFunc && o.intrinsicMethodValue(m) {
		t = o.intrinsicValueType(o.intrinsicKey(m), o.c.TypeOf(m), t)
	}
	// An unannotated function declaration whose body only throws returns
	// `void` to TypeScript; codegen gives it `never`, so its call is a
	// value in an operand position (ADR-00869).
	if id, isID := e.(*ast.Identifier); ok && isID && t.IsFunc && t.FuncRetType != nil && t.FuncRetType.IR == "void" {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType == nil && !fd.IsAsync && !fd.IsGenerator && blockAlwaysDiverges(fd.Body) {
				nt := TypeNever
				t.FuncRetType = &nt
			}
		}
	}
	// … and so does a direct call of one.
	if call, isCall := e.(*ast.CallExpression); ok && isCall && t.IsPromise && t.PromiseType == nil {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if ft, fok := o.ExprType(id); fok && ft.IsFunc && ft.FuncRetType != nil && ft.FuncRetType.IsPromise && ft.FuncRetType.PromiseType != nil && ft.FuncRetType.PromiseType.IsNever {
				t = *ft.FuncRetType
			}
		}
	}
	// … and an async one's promise settles with never.
	if id, isID := e.(*ast.Identifier); ok && isID && t.IsFunc && t.FuncRetType != nil && t.FuncRetType.IsPromise && t.FuncRetType.PromiseType == nil {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType == nil && fd.IsAsync && !fd.IsGenerator && blockAlwaysDiverges(fd.Body) {
				rt := *t.FuncRetType
				nt := TypeNever
				rt.PromiseType = &nt
				t.FuncRetType = &rt
			}
		}
	}
	// Under -compat=js a mixed array literal, and an unannotated binding of
	// one, holds its elements in plain boxes (inferArrayType's any[]);
	// strict keeps the union.
	if ok && o.r.js != nil && o.r.js() && t.IsArray && t.ElemType != nil && t.ElemType.IsDynamic && len(t.ElemType.UnionMembers) > 0 && o.literalArray(e) {
		t = ArrayOf(TypeAny)
	}
	if ix, isIx := e.(*ast.IndexExpression); ok && isIx && t.IsDynamic && len(t.UnionMembers) > 0 && o.literalArray(ix.Object) {
		if at, aok := o.ExprType(ix.Object); aok && at.IsArray && at.ElemType != nil && reprKey(*at.ElemType) == reprKey(TypeAny) {
			t = TypeAny // … whose element reads are those boxes
		}
	}
	// … and a nested one's rows are its boxed rows (an unchecked read keeps
	// its possible undefined).
	if ix, isIx := e.(*ast.IndexExpression); ok && isIx && t.IsArray && o.r.js != nil && o.r.js() && o.literalArray(ix.Object) {
		if at, aok := o.ExprType(ix.Object); aok && at.IsArray && at.ElemType != nil && at.ElemType.IsArray {
			row := *at.ElemType
			row.IsUndefined, row.Nullable, row.UncheckedIndex = t.IsUndefined, t.Nullable, t.UncheckedIndex
			t = row
		}
	}
	return t, ok
}

// literalArray reports whether e is an array literal, or reads an
// unannotated binding initialized with one.
func (o frontOracle) literalArray(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.ArrayLiteral:
		return true
	case *ast.Identifier:
		if sym, _ := o.b.Resolve(x); sym != nil && len(sym.Declarations) == 1 {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.TypeAnnot == nil {
				_, lit := v.Init.(*ast.ArrayLiteral)
				return lit
			}
		}
	}
	return false
}

// intrinsicMethodValue reports whether m names a builtin global object's
// intrinsic static method (`Math.max`, `Date.parse`).
func (o frontOracle) intrinsicMethodValue(m *ast.MemberExpression) bool {
	return o.intrinsicKey(m) != ""
}

// intrinsicKey is the intrinsic a builtin global object's static method m
// names, or "".
func (o frontOracle) intrinsicKey(m *ast.MemberExpression) string {
	id, isID := m.Object.(*ast.Identifier)
	if !isID {
		return ""
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || !o.r.libGlobal(sym) {
		return ""
	}
	ms, isMS := o.c.MemberDecl(m).(*ast.MethodSignature)
	if !isMS || strings.Contains(ms.Intrinsic, ".prototype.") {
		return ""
	}
	return ms.Intrinsic
}

// loweredStatic reports a builtin global object's static method declared
// `@lower` (`Math.sqrt`): read as a value, the one shared function codegen
// makes for it (TDD-00230 P3.2), not a boxed host method.
func (o frontOracle) loweredStatic(m *ast.MemberExpression) bool {
	obj := m.Object
	if g, isG := obj.(*ast.MemberExpression); isG && !g.Optional {
		// `globalThis.Math.sqrt` reads the global `Math`'s.
		if id, isID := g.Object.(*ast.Identifier); isID && id.Name == "globalThis" {
			if sym, _ := o.b.Resolve(id); sym == nil || o.r.libGlobal(sym) {
				if gs := o.b.Globals.Symbols.Get(g.Property); gs == nil || !o.r.libGlobal(gs) {
					return false
				}
				ms, isMS := o.c.MemberDecl(m).(*ast.MethodSignature)
				return isMS && ms.Lower != ""
			}
		}
	}
	id, isID := obj.(*ast.Identifier)
	if !isID {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || !o.r.libGlobal(sym) {
		return false
	}
	ms, isMS := o.c.MemberDecl(m).(*ast.MethodSignature)
	return isMS && ms.Lower != ""
}

func (o frontOracle) exprTypeRaw(e ast.Expression) (Type, bool) {
	// A function's `prototype` (no lib member: codegen reads it off the
	// value at run time) is an `any`.
	if m, ok := e.(*ast.MemberExpression); ok && m.Property == "prototype" {
		if rt, ok := o.ExprType(m.Object); ok && (rt.IsFunc || isUnconstrainedDynamic(rt)) && o.c.Unanswered(o.c.TypeOf(m)) {
			return TypeAny, true
		}
		// … and a builtin constructor's, which is no value yet.
		if id, ok := m.Object.(*ast.Identifier); ok {
			if sym, _ := o.b.Resolve(id); sym != nil && o.r.libGlobal(sym) {
				return TypeAny, true
			}
		}
	}
	// A well-known symbol (`Symbol.iterator`) is held boxed, and so is a
	// `const` initialized from one.
	if o.wellKnownSymbol(e) {
		return TypeAny, true
	}
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.Kind == "const" && v.TypeAnnot == nil && o.wellKnownSymbol(v.Init) {
				return TypeAny, true
			}
		}
	}
	// A function literal bound to `any` takes its arguments as one box.
	if id, ok := e.(*ast.Identifier); ok && id.Name == "arguments" {
		if sym, _ := o.b.Resolve(id); sym != nil && sym.Implicit && sym.Scope != nil && o.anyBound[sym.Scope.Node] {
			return TypeAny, true
		}
		// In the js lane, an object literal's method takes them as one box
		// too: such a method is a boxed object's dynamic function.
		if sym, _ := o.b.Resolve(id); sym != nil && sym.Implicit && sym.Scope != nil && o.r.js != nil && o.r.js() {
			if fe, isFE := sym.Scope.Node.(*ast.FunctionExpression); isFE {
				if _, inLit := o.c.ParentOf(fe).(*ast.ObjectLiteral); fe.Method || inLit {
					return TypeAny, true
				}
			}
		}
	}
	// The rest parameter of a function literal bound to `any` is one box.
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 && o.anyBound[sym.Declarations[0].Node] {
			var params []ast.Param
			switch f := sym.Declarations[0].Node.(type) {
			case *ast.ArrowFunction:
				params = f.Params
			case *ast.FunctionExpression:
				params = f.Params
			}
			if len(params) > 0 && params[len(params)-1].Rest && params[len(params)-1].Name == id.Name {
				return TypeAny, true
			}
		}
	}
	// The Object statics that build or return a dynamic object (a defined
	// accessor, a prototype link, a descriptor) are held boxed, whatever
	// the lib's result type.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, ok := call.Callee.(*ast.MemberExpression); ok {
			switch o.intrinsicKey(m) {
			case "Object.defineProperty", "Object.getOwnPropertyDescriptor", "Object.create",
				"Object.getPrototypeOf", "Object.setPrototypeOf", "Object.groupBy":
				return TypeAny, true
			case "Object.values", "Object.entries":
				// Of a dynamic object: a dynamic array (emitDynAnyEntries).
				if len(call.Args) == 1 {
					if at, ok := o.ExprType(call.Args[0]); ok && isUnconstrainedDynamic(at) {
						return TypeAny, true
					}
				}
			}
		}
	}
	// JSON.stringify of an `any` (or of arguments only known at run time)
	// may be undefined: held boxed, though the lib's result is string.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, ok := call.Callee.(*ast.MemberExpression); ok && o.intrinsicKey(m) == "JSON.stringify" {
			if resolver.StringifyArgsAtRunTime(call.Args) {
				return TypeAny, true
			}
			if len(call.Args) >= 1 {
				if at, ok := o.ExprType(call.Args[0]); ok && isUnconstrainedDynamic(at) {
					return TypeAny, true
				}
			}
		}
	}
	// `f.call(…)` / `f.apply(…)` returns f's result, whatever the lane's
	// bind/call/apply checking.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, ok := call.Callee.(*ast.MemberExpression); ok && (m.Property == "call" || m.Property == "apply") {
			if ft, ok := o.ExprType(m.Object); ok && ft.IsFunc && ft.FuncRetType != nil && ft.FuncRetType.IR != "" {
				if ft.FuncRetType.IR == "void" {
					return TypeVoid, true
				}
				return *ft.FuncRetType, true
			}
		}
	}
	// A binding of such a group read holds that absence too.
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if ix, isIx := v.Init.(*ast.IndexExpression); isIx && !ix.Optional && o.matchArray(ix.Object) {
					if ty, ok := o.exprType(e); ok && !ty.IsDynamic {
						return indexReadType(ty), true
					}
				}
			}
		}
	}
	// A nullish comparison's operand keeps the binding's declared
	// possibly-absent form: a narrowing by assignment from a read that may
	// miss (`b = map[k]`) leaves the run-time check to decide.
	if id, ok := e.(*ast.Identifier); ok && o.nullishCompared(id) {
		if dt, dok := o.declaredNullable(id); dok {
			if ty, ok := o.exprType(e); ok && !ty.Nullable && !ty.IsDynamic && ty.IR == dt.IR {
				return dt, true
			}
		}
	}
	// A regular-expression match's group may not have participated: an
	// element read, absent on a miss (as codegen reads any array element).
	if ix, ok := e.(*ast.IndexExpression); ok && o.matchArray(ix.Object) {
		if ty, ok := o.exprType(e); ok && !ty.IsDynamic {
			return indexReadType(ty), true
		}
	}
	// An optional method a class leaves unimplemented (`m?(): T;`) reads as
	// its possibly-undefined slot, not a typed closure.
	if m, ok := e.(*ast.MemberExpression); ok {
		if fd, ok := o.c.MemberDecl(m).(*ast.FunctionDeclaration); ok && fd.IsOptional && fd.Body == nil {
			return Type{IR: "ptr", Nullable: true, IsUndefined: true}, true
		}
	}
	// `a || b` / `a && b` of one pointer type is held in the left
	// operand's slot: a three-state left keeps its null/undefined marker
	// (shortCircuitValueType).
	if b, ok := e.(*ast.BinaryExpression); ok && (b.Op == "||" || b.Op == "&&") {
		lt, lok := o.ExprType(b.Left)
		ty, tok := o.exprType(e)
		if lok && tok && lt.NullAndUndef && lt.IR == "ptr" && ty.IR == "ptr" && ty.Nullable && !ty.NullAndUndef && reprKey(lt.withoutNullable()) == reprKey(ty.withoutNullable()) {
			ty.NullAndUndef = true
			return ty, true
		}
	}
	// A property read through a caught value asserted to a type
	// (`(err as Error).message`) reads the caught value's own property,
	// which a thrown non-object lacks.
	if m, ok := e.(*ast.MemberExpression); ok {
		if as, ok := m.Object.(*ast.AsExpression); ok && o.caughtBinding(as) {
			if ty, ok := o.exprType(e); ok && ty.IR == "ptr" && !ty.IsDynamic && !ty.Nullable {
				ty.Nullable, ty.IsUndefined = true, true
				return ty, true
			}
		}
	}
	// A comma expression is its last operand.
	if sq, ok := e.(*ast.SequenceExpression); ok && len(sq.Exprs) > 0 {
		return o.ExprType(sq.Exprs[len(sq.Exprs)-1])
	}
	if lit, ok := e.(*ast.ArrayLiteral); ok && o.r.js != nil && o.r.js() && o.jsBoxedArrayLiteral(lit) {
		return ArrayOf(TypeAny), true
	}
	// A literal of nested array literals that all hold one representation
	// (a js-lane boxed `[null, 0]` included) is an array of it.
	if lit, ok := e.(*ast.ArrayLiteral); ok && o.r.js != nil && o.r.js() && len(lit.Elements) > 0 {
		var et Type
		same := true
		for i, el := range lit.Elements {
			inner, isLit := el.(*ast.ArrayLiteral)
			if !isLit || len(inner.Elements) == 0 {
				same = false
				break
			}
			t, ok := o.ExprType(inner)
			if !ok || i > 0 && reprKey(t) != reprKey(et) {
				same = false
				break
			}
			et = t
		}
		if same {
			return ArrayOf(et), true
		}
	}
	if o.r.js != nil && o.r.js() {
		target := e
		if as, ok := e.(*ast.AssignmentExpression); ok && as.Op == "=" {
			target = as.Left
		}
		if as, ok := e.(*ast.AssignmentExpression); ok && target != e && o.jsUntypedVar(as.Left) {
			return TypeAny, true // an assignment's value is its target's slot
		}
	}
	ty, ok := o.exprType(e)
	if ok && ty.IsFunc && o.r.js != nil && o.r.js() {
		ty = o.jsDefaultedParams(e, ty)
	}
	return ty, ok
}

// jsUntypedVar reports a js-lane `var` every declaration of which has no
// initializer or annotation: held as `any`, whatever is stored to it.
func (o frontOracle) jsUntypedVar(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) == 0 {
		return false
	}
	for _, d := range sym.Declarations {
		v, ok := d.Node.(*ast.VarDeclaration)
		if !ok || v.Kind != "var" || v.Init != nil || v.TypeAnnot != nil {
			return false
		}
	}
	return true
}

// jsDefaultedParams: in the js lane a caller may pass anything, so an
// unannotated parameter is held as `any` unless its call sites agree on a
// primitive (jsSiteParam) or its default is a literal, array, object or
// module binding codegen types it by (paramDefaultType).
func (o frontOracle) jsDefaultedParams(e ast.Expression, ty Type) Type {
	var params []ast.Param
	switch f := e.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return ty
	}
	if len(params) != len(ty.FuncParams) {
		return ty
	}
	var out []Type
	for i, p := range params {
		if p.Type != nil || p.Default == nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
			continue
		}
		switch d := p.Default.(type) {
		case *ast.StringLiteral, *ast.TemplateLiteral, *ast.NumberLiteral, *ast.BooleanLiteral,
			*ast.ArrayLiteral, *ast.ObjectLiteral:
			continue
		case *ast.Identifier:
			// A module-level binding's type (moduleGlobals); not another
			// parameter's.
			if sym, _ := o.b.Resolve(d); sym != nil && sym.Scope == o.b.Module {
				continue
			}
		}
		if t := o.c.JSCallSiteParam(e, i); t != nil && t.Flags&checker.Union == 0 && t.Flags&(checker.String|checker.Number|checker.Boolean) != 0 {
			continue // jsSiteParam's primitive
		}
		if out == nil {
			out = append([]Type(nil), ty.FuncParams...)
		}
		out[i] = TypeAny
	}
	if out != nil {
		ty.FuncParams = out
	}
	return ty
}

func (o frontOracle) exprType(e ast.Expression) (Type, bool) {
	// `process.env` is read through its own rewrite (processEnvIndexRewrite)
	// and held as a plain `any`.
	if m, ok := e.(*ast.MemberExpression); ok && m.Property == "env" {
		if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "process" {
			return TypeAny, true
		}
	}
	// … and a variable of it is a string or undefined.
	if m, ok := e.(*ast.MemberExpression); ok {
		if env, ok := m.Object.(*ast.MemberExpression); ok && env.Property == "env" {
			if id, ok := env.Object.(*ast.Identifier); ok && id.Name == "process" {
				return undefinedableElem(TypePtr), true
			}
		}
	}
	t := o.c.StorageTypeOf(e)
	if o.c.Unanswered(t) {
		return Type{}, false
	}
	ty, ok := o.r.of(t)
	// A `const` holding `a || b` is held in that expression's slot.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IR == "ptr" && ty.Nullable && !ty.NullAndUndef {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isVar := sym.Declarations[0].Node.(*ast.VarDeclaration); isVar && v.Kind == "const" && v.TypeAnnot == nil {
				if bin, isBin := v.Init.(*ast.BinaryExpression); isBin && (bin.Op == "||" || bin.Op == "&&") {
					if it, iok := o.ExprType(bin); iok && it.NullAndUndef && reprKey(it.withoutNullable()) == reprKey(ty.withoutNullable()) {
						ty.NullAndUndef = true
					}
				}
			}
		}
	}
	if o.jsUntypedVar(e) {
		ty, ok = TypeAny, true // narrowed below like any other `any`
	}
	if !ok {
		return ty, ok
	}
	// A builtin global function as a value (`parseInt`) is called through
	// the boxed convention: every parameter `any`, its declared result.
	if id, isID := e.(*ast.Identifier); isID && ty.IsFunc && o.isGlobalFunc(id) {
		params := make([]Type, len(ty.FuncParams))
		for i := range params {
			params[i] = TypeAny
		}
		ret := TypeVoid
		if ty.FuncRetType != nil {
			ret = *ty.FuncRetType
		}
		return FuncType(params, ret), true
	}
	// An executor's `reject` takes the rejection as a caught value, the
	// record a rejected promise holds (emitNewPromise).
	if id, isID := e.(*ast.Identifier); isID && ty.IsFunc {
		if ne, i := o.executorParam(id); ne != nil && i == 1 {
			return FuncType([]Type{TypeCaught}, TypeVoid), true
		} else if ne != nil && i == 0 {
			// `resolve` takes the promise's value; a Promise<void>'s none.
			if pt := o.c.TypeOf(ne); !o.c.Unanswered(pt) {
				if r, ok := o.r.of(pt); ok && r.IsPromise {
					if r.PromiseType == nil || r.PromiseType.IR == "void" || r.PromiseType.IR == "" {
						return FuncType(nil, TypeVoid), true
					}
					rt := FuncType([]Type{*r.PromiseType}, TypeVoid)
					rt.IsPromiseResolver = true
					return rt, true
				}
			}
		}
	}
	// A binding of such a `then`/`catch`/`finally` holds that instance.
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if call, isCall := v.Init.(*ast.CallExpression); isCall {
					if m, isM := call.Callee.(*ast.MemberExpression); isM && (m.Property == "then" || m.Property == "catch" || m.Property == "finally") {
						if it, iok := o.ExprType(call); iok && it.IsClass {
							return it, true
						}
					}
				}
			}
		}
	}
	// The executor a Promise subclass passes to `super(…)` takes the same
	// resolve and reject (emitNewPromise).
	if id, isID := e.(*ast.Identifier); isID {
		if i := o.superExecutorParam(id); i == 1 {
			return FuncType([]Type{TypeCaught}, TypeVoid), true
		} else if i == 0 {
			if ft := o.c.TypeOf(id); !o.c.Unanswered(ft) && ft.Flags&checker.Object != 0 && ft.Kind == checker.Function && len(ft.Params) == 1 {
				for _, m := range checkerMembers(ft.Params[0]) {
					if m.Flags&(checker.Object|checker.Undefined|checker.Void) != 0 {
						continue
					}
					if vr, ok := o.r.of(m); ok {
						rt := FuncType([]Type{vr}, TypeVoid)
						rt.IsPromiseResolver = true
						return rt, true
					}
				}
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID && o.isRejectionParam(id) {
		return TypeCaught, true
	}
	// An overload picked for an `any` argument is the first that fits any
	// value, not the one the run-time value takes: codegen holds the result
	// as `any` (checkerPrimitive).
	if call, ok := e.(*ast.CallExpression); ok && o.c.AnyArgOverloadCall(call) && ty.IR != "void" && !o.overloadsAgree(call) && (scalarTypeKind(ty) != "" || isStringTy(ty)) {
		return TypeAny, true // a void result is no value either way
	}
	// `a && b` / `a || b` / `a ?? b` with an operand held as `any`: the
	// value is either operand, so it is held as one (shortCircuitValueType).
	if b, ok := e.(*ast.BinaryExpression); ok && (b.Op == "&&" || b.Op == "||" || b.Op == "??") {
		lt, lok := o.ExprType(b.Left)
		rt, rok := o.ExprType(b.Right)
		if lok && rok && (isUnconstrainedDynamic(lt) || isUnconstrainedDynamic(rt)) {
			return TypeAny, true
		}
	}
	// A read through a receiver held as `any` is an `any` (the dynamic
	// member read), whatever the checker knows of the receiver's type.
	// … and through an un-narrowed union of scalars (a guard another
	// function may undo): its box answers.
	if m, ok := e.(*ast.MemberExpression); ok {
		if rt, ok := o.ExprType(m.Object); ok && rt.IsDynamic && len(rt.UnionMembers) > 0 && isSelfDescribingBox(rt) && !unionAllHaveLength(rt) {
			if m.Property == "length" {
				return TypeF64, true
			}
			return TypeAny, true
		}
	}
	// A property of a union whose one object member declares it is read off
	// that member when the box holds it: a box (emitUnionSoleObjectMemberRead).
	// A property of a union of objects codegen holds in a shape the
	// mapping has none for is read as that shape reads it: no answer here.
	if m, ok := e.(*ast.MemberExpression); ok {
		if _, rok := o.ExprType(m.Object); !rok {
			if rt := o.c.NonNullable(o.c.TypeOf(m.Object)); !o.c.Unanswered(rt) && rt.Flags&checker.Union != 0 {
				return Type{}, false
			}
		}
	}
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional {
		if rt, ok := o.ExprType(m.Object); ok && rt.IsDynamic && len(rt.UnionMembers) > 0 && !isSelfDescribingBox(rt) {
			if name, _, disc := unionDiscriminantField(rt); !disc || name != m.Property {
				var obj Type
				n := 0
				for _, um := range rt.UnionMembers {
					if unionMemberTag(um) == "object" {
						obj, n = um, n+1
					}
				}
				if n == 1 {
					if _, _, has := obj.FieldIndex(m.Property); has || obj.IsClass {
						return TypeAny, true
					}
				}
			}
		}
	}
	// A property of a function value (`assert.throws`, set by `ok.throws =
	// throws`) lives in its boxed property table: read and called as a box.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && o.functionValueProp(m) {
			return TypeAny, true
		}
	}
	// A method call through a receiver held boxed is a dynamic call: a box.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional {
			if rt, ok := o.ExprType(m.Object); ok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil && !o.localClassValue(m.Object) {
				if mt := o.c.TypeOf(m.Object); !o.c.Unanswered(mt) && mt.Flags&checker.Object != 0 && mt.ClassOf != nil {
					return TypeAny, true
				}
			}
		}
	}
	if obj := dynamicReadReceiver(e); obj != nil {
		if rt, ok := o.ExprType(obj); ok && isUnconstrainedDynamic(rt) && !(plainMemberRead(e) && o.narrowedToInstance(obj)) && !o.localClassValue(obj) {
			// An index-signature view: every property has its value type,
			// absent on a miss (dynPropReadType).
			if rt.DynPropTy != nil {
				return dynPropReadType(*rt.DynPropTy), true
			}
			return TypeAny, true
		}
	}
	// A property read through an erased `x as C` reads the operand's
	// object: a property it lacks, or has optional, may be absent.
	if m, ok := e.(*ast.MemberExpression); ok && !ty.Nullable && ty.IR == "ptr" && !ty.IsDynamic {
		if as, ok := m.Object.(*ast.AsExpression); ok && as.TypeAnnot != nil {
			if ot := o.c.TypeOf(as.Expr); !o.c.Unanswered(ot) {
				ot = o.c.NonNullable(ot)
				if ot.Flags&checker.Object != 0 && ot.Flags&checker.Union == 0 && (ot.Kind == checker.Instance || ot.Kind == checker.Interface) && ot.StringIndex == nil {
					if p := ot.Prop(m.Property); p == nil || p.Optional {
						ty.Nullable, ty.IsUndefined = true, true
					}
				}
			}
		}
	}
	// … and a `const` initialized from that read holds the same.
	if id, ok := e.(*ast.Identifier); ok && !ty.Nullable && ty.IR == "ptr" && !ty.IsDynamic {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.Kind == "const" && v.TypeAnnot == nil {
				if m, ok := v.Init.(*ast.MemberExpression); ok {
					if _, ok := m.Object.(*ast.AsExpression); ok {
						if it, ok := o.ExprType(m); ok && it.Nullable && reprKey(it.withoutNullable()) == reprKey(ty) {
							return it, true
						}
					}
				}
			}
		}
	}
	// A `catch (err)` binding holds the caught-value record (emitCatchBody);
	// `err as T` is erased and keeps it.
	if o.caughtBinding(e) {
		return TypeCaught, true
	}
	// `x as T` is erased (ADR-00371): a possibly-null pointer asserted to
	// the same shape without null stays possibly null at run time, where a
	// call or member read through it throws as in Node.
	if src := o.assertedOperand(e); src != nil && !ty.Nullable && ty.IR == "ptr" && !ty.IsDynamic {
		if st, ok := o.ExprType(src); ok && st.Nullable && st.IR == "ptr" && !st.IsDynamic &&
			st.IsFunc == ty.IsFunc && st.IsArray == ty.IsArray && st.IsObject == ty.IsObject {
			ty.Nullable, ty.IsUndefined = true, st.IsUndefined
		}
	}
	// The same for a scalar: `n as number` with `n: number | undefined`
	// still holds the absent value.
	if src := o.assertedOperand(e); src != nil && !ty.Nullable && nonPtrScalar(ty) && !ty.IsDynamic {
		if st, ok := o.ExprType(src); ok && isNullableScalar(st) && reprKey(st.withoutNullable()) == reprKey(ty) {
			return st, true
		}
	}
	// A nullable read every path to which stores a present value is its
	// payload (storesPresent).
	if id, isID := e.(*ast.Identifier); isID && ty.Nullable && !ty.IsDynamic && o.storesPresent(id) {
		return ty.withoutNullable(), true
	}
	// A nullable scalar read where flow proves it present is its payload
	// (emitIdent).
	if id, isID := e.(*ast.Identifier); isID && isNullableScalar(ty) && !ty.UncheckedIndex && flowTrusted(o.b, id) {
		if nt, st := o.c.TypeOf(id), o.c.StorageTypeOf(id); !o.c.Unanswered(nt) && !o.c.Unanswered(st) && !presentType(st) && presentType(nt) {
			return ty.withoutNullable(), true
		}
	}
	// A nullable instance, function or promise reference flow proves
	// present reads as present (emitIdent's checkerPresentRef); an array
	// pattern's binding may be absent past the source's end.
	if presentRefCandidate(e, ty) && !o.arrayPatternBinding(e) && !o.storesIndexRead(e) && flowTrusted(o.b, e) {
		if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && presentType(nt) {
			return ty.withoutNullable(), true
		}
	}
	// A union variable flow narrows to one of its primitive members reads
	// as that member, unboxed.
	if id, isID := e.(*ast.Identifier); isID && ty.IsDynamic && len(ty.UnionMembers) > 0 && flowTrusted(o.b, id) {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Union == 0 &&
			nt.Flags&(checker.Boolean|checker.BooleanLiteral|checker.Number|checker.NumberLiteral|checker.String|checker.StringLiteral) != 0 {
			if r, ok := o.r.of(nt); ok {
				for _, m := range ty.UnionMembers {
					if reprKey(m) == reprKey(r) {
						return r, true
					}
				}
			}
		}
	}
	// … to its promise member, that promise; to several of its members (a
	// typeof test ruling one out), the union of those, in declared order
	// (unionComplement).
	if id, isID := e.(*ast.Identifier); isID && ty.IsDynamic && len(ty.UnionMembers) > 1 && flowTrusted(o.b, id) {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Nullish == 0 {
			if nt.Flags&checker.Union == 0 {
				if r, ok := o.r.of(nt); ok && r.IsPromise {
					for _, m := range ty.UnionMembers {
						if reprKey(m) == reprKey(r) {
							return m, true
						}
					}
				}
			} else if sub, ok := o.unionSubset(ty, nt); ok {
				return sub, true
			}
		}
	}
	// An `any` variable a typeof test narrows to a boolean, number or
	// string reads unboxed (a bigint stays boxed).
	if id, isID := e.(*ast.Identifier); isID && ty.IsDynamic && len(ty.UnionMembers) == 0 && flowTrusted(o.b, id) {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&(checker.Boolean|checker.BooleanLiteral|checker.Number|checker.NumberLiteral|checker.String|checker.StringLiteral) != 0 && nt.Flags&checker.Union == 0 {
			if r, ok := o.r.of(nt); ok {
				return r, true
			}
		}
		// … or to a class instance (`x instanceof C`): the instance.
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Object != 0 && nt.Flags&checker.Union == 0 && nt.Kind == checker.Instance &&
			nt.Symbol != nil && len(nt.TypeArgs) == 0 && o.r.narrowClass != nil {
			if r, ok := o.r.narrowClass(nt.Symbol); ok {
				return r, true
			}
		}
	}
	if as, isAs := e.(*ast.AsExpression); isAs && ty.IsObject {
		// An assertion between two static object types keeps the operand's
		// representation; only one out of a dynamic value converts it.
		if ot, ok := o.ExprType(as.Expr); ok && ot.IsObject && !ot.IsDynamic && !ot.IsDynamicObject {
			return ot, true
		}
	}
	if lit, isLit := e.(*ast.ObjectLiteral); isLit && ty.IsObject {
		ty = o.literalStorage(lit, ty)
	}
	if o.recordArrayRead(e) {
		ty.Nullable, ty.IsUndefined = true, true // a missing key reads undefined
	}
	switch arr, typed := o.elementRead(e); {
	case typed && arr.ElemType != nil:
		if _, isIndex := e.(*ast.IndexExpression); isIndex {
			return indexReadType(*arr.ElemType), true // the native element
		}
		ty = indexReadType(ty)
		ty.UncheckedIndex = false // a local holds the number it converted to
	case arr.IsArray:
		if arr.DynPropTy != nil && ty.IR != "ptr" {
			break // a scalar record value holds its own absence (dynPropReadType)
		}
		ty = indexReadType(ty)
		if arr.DynPropTy != nil {
			ty.UncheckedIndex = false // dynPropReadType's absence
		}
		if _, isID := e.(*ast.Identifier); isID && ty.IsClass {
			ty.UncheckedIndex = false // a local holds the instance pointer
		}
	}
	return ty, true
}

// dynamicReadReceiver is the receiver of a member or element read.
func dynamicReadReceiver(e ast.Expression) ast.Expression {
	switch x := e.(type) {
	case *ast.MemberExpression:
		return x.Object
	case *ast.IndexExpression:
		return x.Object
	}
	return nil
}

// caughtBinding reports a catch clause's own binding, read directly or
// through an erased `err as T`.
func (o frontOracle) caughtBinding(e ast.Expression) bool {
	for as, ok := e.(*ast.AsExpression); ok && as.TypeAnnot != nil; as, ok = e.(*ast.AsExpression) {
		if kw, isKw := as.TypeAnnot.TypeNode().(*ast.KeywordType); isKw && (kw.Keyword == "any" || kw.Keyword == "unknown") {
			return false // boxed into the box the assertion names
		}
		e = as.Expr
	}
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
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

// presentRefCandidate reports a nullable instance, function, promise,
// object or array reference, read as a variable or a property: one flow may prove
// present (checkerPresentRef).
func presentRefCandidate(e ast.Expression, ty Type) bool {
	switch e.(type) {
	case *ast.Identifier, *ast.MemberExpression:
	default:
		return false
	}
	return ty.IR == "ptr" && ty.Nullable && !ty.IsDynamic && !ty.UncheckedIndex &&
		(ty.IsClass || ty.IsFunc || ty.IsPromise || ty.IsObject || ty.IsArray)
}

// arrayPatternBinding reports a name an array pattern declares.
func (o frontOracle) arrayPatternBinding(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		if _, ok := d.Node.(*ast.ArrayDestructuring); ok {
			return true
		}
	}
	return false
}

// recordArrayRead reports an array value read from a string-keyed record
// (`m[k]` with `m: Record<string, T[]>`), directly or as the initializer of
// an unannotated variable e names: a missing key is a null array.
func (o frontOracle) recordArrayRead(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.IndexExpression:
		ot := o.c.TypeOf(x.Object)
		if o.c.Unanswered(ot) {
			return false
		}
		rt, ok := o.r.of(ot)
		vt, isDict := dictValType(rt)
		return ok && isDict && vt.IsArray
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil || len(sym.Declarations) != 1 {
			return false
		}
		v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
		if !ok || v.TypeAnnot != nil || v.Init == nil {
			return false
		}
		if ix, ok := v.Init.(*ast.IndexExpression); ok {
			return o.recordArrayRead(ix)
		}
	}
	return false
}

// elementRead is the array e reads an element of (`a[i]`), directly or as
// the initializer of an unannotated variable e names, and whether it is a
// typed array or Buffer: the value may be undefined (an index past the
// end) though tsc types it as the element type.
func (o frontOracle) elementRead(e ast.Expression) (Type, bool) {
	switch x := e.(type) {
	case *ast.IndexExpression:
		if x.Optional {
			return Type{}, false
		}
		ot := o.c.TypeOf(x.Object)
		if o.c.Unanswered(ot) {
			return Type{}, false
		}
		rt, ok := o.r.of(ot)
		if vt, isDict := dictValType(rt); ok && isDict {
			// A string-keyed record's missing key reads undefined too; an
			// array value's absence is its null header (recordRead).
			if vt.IsArray {
				return Type{}, false
			}
			rec := ArrayOf(vt)
			rec.DynPropTy = &vt // marks a record read: absent, not unchecked
			return rec, false
		}
		if !ok || !rt.IsArray || rt.IsTuple {
			return Type{}, false
		}
		return rt, rt.IsTypedArray || rt.IsBuffer
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil || len(sym.Declarations) != 1 {
			return Type{}, false
		}
		v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
		if !ok || v.TypeAnnot != nil || v.Init == nil {
			return Type{}, false
		}
		switch v.Init.(type) {
		case *ast.IndexExpression, *ast.ConditionalExpression:
			return o.elementRead(v.Init)
		}
	case *ast.ConditionalExpression:
		// `c ? a[i] : -1`: the read's absence survives a number on the
		// other side, as the conditional's value.
		for _, p := range [][2]ast.Expression{{x.Consequent, x.Alternate}, {x.Alternate, x.Consequent}} {
			arr, typed := o.elementRead(p[0])
			if !arr.IsArray {
				continue
			}
			ot := o.c.StorageTypeOf(p[1])
			if o.c.Unanswered(ot) {
				continue
			}
			if r, ok := o.r.of(ot); ok && (nonPtrScalar(r) || arr.ElemType != nil && !r.IsDynamic && reprKey(*arr.ElemType) == reprKey(r)) {
				return arr, typed
			}
		}
	}
	return Type{}, false
}

func (o frontOracle) AnnotationType(ta *ast.TypeAnnotation) (Type, bool) {
	n := ta.TypeNode()
	if n == nil {
		return Type{}, false
	}
	t := o.c.TypeFromNode(n, o.b.Module)
	if o.c.Unanswered(t) {
		return Type{}, false
	}
	if t.Flags&checker.StringLiteral != 0 {
		// An annotation keeps its string literal (a discriminant's tag).
		return Type{IR: "ptr", IsStrLiteral: true, LitValue: t.Value}, true
	}
	r := o.r
	r.literals, r.tuples = true, true
	ty, ok := r.of(t)
	if !ok {
		return ty, ok
	}
	return annotationNullish(ta, ty), true
}

// GlobalType answers reliableGlobalType: a module variable is promotable
// when its declared type has a representation.
func (o frontOracle) GlobalType(v *ast.VarDeclaration) (Type, bool, bool) {
	if v.TypeAnnot == nil && o.r.widened != nil && o.r.widened(v.Name) {
		return TypeAny, true, true
	}
	// A generator function expression is held boxed (a generator method
	// read off its object), whatever the checker makes of its body.
	if fe, ok := v.Init.(*ast.FunctionExpression); ok && fe.IsGenerator && v.TypeAnnot == nil {
		return TypeAny, true, true
	}
	if o.dynamicInit(v) {
		t, ok := o.ExprType(v.Init)
		return t, ok, true
	}
	if o.evolvingBoxed(v) {
		return ArrayOf(TypeAny), true, true
	}
	if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && v.TypeAnnot == nil && hasAccessor(lit) && o.r.objLit != nil {
		if ct, cok := o.r.objLit(lit); cok {
			return ct, true, true // its synthetic class (TDD-00153)
		}
	}
	// An unannotated binding of a plain function literal holds that
	// function's value (its result as its body returns it).
	if v.TypeAnnot == nil {
		switch fl := v.Init.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			if rt, rok := o.inferredBodyReturn(fl); rok && rt.Nullable {
				if ft, fok := o.ExprType(v.Init); fok && ft.IsFunc {
					return ft, true, true
				}
			}
		}
	}
	// An unannotated binding with no initializer is held `any` (its
	// narrower types are flow's, per read), in either lane.
	if v.Init == nil && v.TypeAnnot == nil && !v.Ambient {
		if o.b.Module != nil {
			if sym := o.b.Module.Symbols.Get(v.Name); sym != nil && len(sym.Declarations) > 1 && sym.Declarations[0].Node != ast.Node(v) {
				return Type{}, false, true // a later `var` is a store to the first
			}
		}
		return TypeAny, true, true
	}
	t := o.c.ModuleVarType(v)
	if o.c.Unanswered(t) {
		return Type{}, false, false
	}
	// A `var` declared again is the first declaration's global; the later
	// one is a store to it.
	if o.b.Module != nil {
		if sym := o.b.Module.Symbols.Get(v.Name); sym != nil && len(sym.Declarations) > 1 && sym.Declarations[0].Node != ast.Node(v) {
			return Type{}, false, true
		} else if sym != nil && o.jsWidenedVar(sym) {
			return TypeAny, true, true
		}
	}
	if o.jsBoxedInit(v) {
		return ArrayOf(TypeAny), true, true
	}
	if _, named := v.Init.(*ast.Identifier); isGeneratorFunc(t) && !named {
		return TypeAny, true, true // a generator method read off its object
	}
	r := o.r
	r.tuples = v.TypeAnnot != nil
	ty, ok := r.of(t)
	if ok && ty.IsFunc && o.r.js != nil && o.r.js() && v.Init != nil {
		ty = o.jsDefaultedParams(v.Init, ty)
	}
	if !ok && v.TypeAnnot == nil && v.Init != nil && hasTuple(t) {
		// An unannotated binding of a tuple (`const t = f()`, `let w = u`)
		// holds what its initializer is held as: a tuple an annotation
		// declared, else the array codegen built.
		ty, ok = o.ExprType(v.Init)
	}
	if ok && o.b.Module != nil {
		if sym := o.b.Module.Symbols.Get(v.Name); sym != nil {
			ty = o.undefinedSlot(v, sym, ty)
		}
	}
	return ty, ok, true
}

// tupleExprType answers an expression whose type involves a tuple where
// codegen holds one: a call of a function whose return annotation declares
// it, and an array literal of tuples held alike (`[u, t]`).
func (o frontOracle) tupleExprType(e ast.Expression) (Type, bool) {
	nt := o.c.TypeOf(e)
	if o.c.Unanswered(nt) || !hasTuple(nt) {
		return Type{}, false
	}
	switch x := e.(type) {
	case *ast.CallExpression:
		id, ok := x.Callee.(*ast.Identifier)
		if !ok {
			return Type{}, false
		}
		// Through a binding an annotation types as a function returning a
		// tuple: that tuple.
		if ft, fok := o.ExprType(id); fok && ft.IsFunc && ft.FuncRetType != nil && ft.FuncRetType.IsTuple {
			return *ft.FuncRetType, true
		}
		sym, _ := o.b.Resolve(id)
		if sym == nil || len(sym.Declarations) != 1 {
			return Type{}, false
		}
		if fd, ok := sym.Declarations[0].Node.(*ast.FunctionDeclaration); ok && fd.ReturnType != nil && !fd.IsAsync && !fd.IsGenerator {
			r := o.r
			r.tuples = true
			return r.of(nt)
		}
	case *ast.IndexExpression:
		// A read of a held tuple's position is that field; of an array of
		// tuples, its element (possibly absent).
		if x.Optional {
			return Type{}, false
		}
		ot, ok := o.ExprType(x.Object)
		if !ok {
			return Type{}, false
		}
		if ot.IsTuple {
			if n, isNum := x.Index.(*ast.NumberLiteral); isNum {
				if k, err := strconv.Atoi(n.Value); err == nil {
					if f := ot.UserFields(); k >= 0 && k < len(f) {
						return f[k].Ty, true
					}
				}
			}
			return Type{}, false
		}
		if ot.IsArray && ot.ElemType != nil && ot.ElemType.IsTuple {
			return indexReadType(*ot.ElemType), true
		}
	case *ast.ArrayLiteral:
		if len(x.Elements) == 0 {
			return Type{}, false
		}
		var elem Type
		for i, el := range x.Elements {
			if _, spread := el.(*ast.SpreadElement); spread {
				return Type{}, false
			}
			et, ok := o.ExprType(el)
			if !ok || !et.IsTuple || i > 0 && reprKey(et) != reprKey(elem) {
				return Type{}, false
			}
			elem = et
		}
		return ArrayOf(elem), true
	}
	return Type{}, false
}

// hasTuple reports a tuple type in t, its array element or union members.
func hasTuple(t *checker.Type) bool {
	switch {
	case t == nil:
		return false
	case t.Flags&checker.Union != 0:
		for _, m := range t.Types {
			if hasTuple(m) {
				return true
			}
		}
	case t.Flags&checker.Object != 0 && t.Kind == checker.Tuple:
		return true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array:
		return hasTuple(t.Elem)
	case t.Flags&checker.Object != 0 && t.Kind == checker.Function && len(t.Overloads) == 0:
		// A function type an annotation spells returns or takes its tuples
		// as declared.
		if hasTuple(t.Result) {
			return true
		}
		for _, p := range t.Params {
			if hasTuple(p) {
				return true
			}
		}
	}
	return false
}

// ReturnType answers inferUnannotatedReturnType: hasValue is false for a
// body that returns no value (void).
func (o frontOracle) ReturnType(body *ast.BlockStatement) (Type, bool, bool) {
	t := o.c.BodyReturnType(body)
	if o.c.Unanswered(t) {
		return o.returnsAnswered(body)
	}
	if t.Flags&checker.Void != 0 {
		return Type{}, false, true
	}
	ty, ok := o.r.of(t)
	if ok && ty.IsFunc {
		ty.FuncCountsArgs = false // a returned method value: see exprType
	}
	if ok && !ty.IsDynamic {
		// What its returns read as: an evolving field read is a box, a
		// skippable `var` (hoistSkippable) may be undefined.
		if rt, _, rok := o.returnsAnswered(body); rok && (rt.IsDynamic && (ty.IsNull || ty.IsUndefined) || rt.Nullable && !ty.Nullable && rt.IR == ty.IR) {
			return rt, true, true
		}
	}
	return ty, ok, ok
}

// Reference answers emitIdent: a name the builtin declarations declare
// (`Infinity`, `console`) is a global, as codegen counts it.
func (o frontOracle) Reference(id *ast.Identifier) (bool, bool) {
	sym, ok := o.b.Resolve(id)
	if sym != nil && sym.Implicit {
		// A function's own `arguments`: codegen materializes it per function
		// as its lowering needs, so neither answer is the binding's.
		return false, false
	}
	if sym != nil && o.b.Globals != nil {
		for s := sym.Scope; s != nil; s = s.Parent {
			if s == o.b.Module {
				break
			}
			if s == o.b.Globals {
				return false, ok
			}
		}
	}
	return sym != nil, ok
}

// reprOf is the representation codegen gives a checker type — the first
// cut of TDD-00230 P3.3's closed set: the primitives, the native widths,
// arrays of them and the builtins a `@lowerType` declaration lays out. A type
// outside it has no answer yet.
func reprOf(t *checker.Type) (Type, bool) { return reprCtx{}.of(t) }

// reprCtx is what a representation depends on beyond the type: the classes
// codegen registered, and the lane (the js lane holds an object literal's
// type as a dynamic object). The zero value answers neither.
type reprCtx struct {
	class func(*binder.Symbol) (Type, bool)
	// iface answers a program's own interface by the layout codegen
	// registered for it.
	iface func(*binder.Symbol) (Type, bool)
	// narrowClass answers a class an `any` narrowed to reads as.
	narrowClass func(*binder.Symbol) (Type, bool)
	// objects answers an object type (the zero value leaves it to the
	// old inference, as the codegen deciders still need).
	objects bool
	js      func() bool
	// widened reports an untyped binding of the scope being emitted that
	// codegen backs with a box (crossTypeWidenedBindings).
	widened func(string) bool
	// literals keeps a union's string-literal members as literals, as an
	// annotation's union does (`'get' | 'set' | boolean`).
	literals bool
	// tuples answers a tuple type: one an annotation declares, which codegen
	// stores as a tuple (an inferred tuple, `Promise.all([…])`'s, is an
	// array to it).
	tuples bool
	// objLit answers an accessor-bearing object literal by the synthetic
	// class codegen lowers it to (ensureObjLitClass).
	objLit func(*ast.ObjectLiteral) (Type, bool)
	// classMethod is the method declaration a registered class runs for
	// name, or nil.
	classMethod func(className, name string) *ast.FunctionDeclaration
	// inferred is codegen's own type for an expression the checker cannot
	// answer.
	inferred func(ast.Expression) (Type, bool)
	// widened reports a parameter a dynamic entry binds as `any`
	// (emit_dynentry.go).
	widenedParam func(*ast.Identifier) bool
	// chainCopy is the program link a copy emission builds of an optional
	// chain's link stands for (noteChainCopy).
	chainCopy func(ast.Expression) (ast.Expression, bool)
	// codegenCall is a call's representation that only codegen can name
	// (codegenCallRepr): a generator's instance, its results, a collection
	// iterator's own methods.
	codegenCall func(*ast.CallExpression) (Type, bool)
	// scopeKey identifies codegen's scope stack (inferScopeIdentity).
	scopeKey func() (int, uintptr)
	// erasedCall is codegen's result for a call of an erased generic
	// method (`run<R>(…): R`, genericMethodResult).
	erasedCall func(*ast.CallExpression, Type) (Type, bool)
	// boundFunc is the value `f.bind(…)` with n arguments makes of a
	// function value of the given type (emitFunctionBind).
	boundFunc func(Type, int) (Type, bool)
}

// repr is the representation context of the program being emitted.
func (e *Emitter) repr() reprCtx {
	return reprCtx{class: e.classRepr, iface: e.interfaceRepr, narrowClass: e.narrowableClass, objects: true, js: e.compatJS, widened: func(n string) bool { return e.widenedBindings[n] }, objLit: e.objLitClassRepr, classMethod: e.classMethodDecl, boundFunc: e.boundFuncValueType, erasedCall: e.erasedMethodCall, widenedParam: e.dynEntryWidenedIdent, chainCopy: e.chainCopyOf, codegenCall: e.codegenCallRepr, scopeKey: e.inferScopeIdentity, inferred: func(x ast.Expression) (Type, bool) { return e.inferExprType(x), true }}
}

// narrowableClass is the instance type of the class sym declares when an
// `any` narrowed to it reads unboxed (checkerNarrowed): a plain class, not
// one over a host body or an Error subclass.
func (e *Emitter) narrowableClass(sym *binder.Symbol) (Type, bool) {
	ct, ok := e.classRepr(sym)
	if !ok || !ct.IsClass {
		return Type{}, false
	}
	if info, ok := e.classes[ct.ClassName]; ok && info.HostBase == nil && !info.IsErrorSubclass {
		return ct, true
	}
	return Type{}, false
}

// classRepr is the instance type of the class sym declares, once codegen
// registered it.
func (e *Emitter) classRepr(sym *binder.Symbol) (Type, bool) {
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			if name, ok := e.classDeclName[cd]; ok {
				// Published before its layout is built (ADR-00906): no
				// answer until it is.
				if info, ok := e.classes[name]; ok && info.Ty.IR != "" {
					return info.Ty, true
				}
				// While it registers, the instance is the type `this` is
				// bound to there: the provisional layout, or the
				// name-placeholder a field initializer reads.
				if this, ok := e.lookup("this"); ok && this.Ty.IsClass && this.Ty.ClassName == name {
					return this.Ty, true
				}
				if pt, ok := e.interfaces[name]; ok && pt.IsClass && pt.ClassName == name && pt.IR != "" {
					return pt, true
				}
			}
		}
	}
	return Type{}, false
}

// interfaceRepr is the layout codegen registered for the interface sym
// declares (a structural object type named by its RefName).
func (e *Emitter) interfaceRepr(sym *binder.Symbol) (Type, bool) {
	if ty, ok := e.interfaceClassBase(sym); ok {
		return ty, true
	}
	for _, d := range sym.Declarations {
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok && id.LowerType == "" && len(id.TypeParams) == 0 {
			if ty, ok := e.interfaces[id.Name]; ok && ty.IsObject && ty.RefName == id.Name && len(ty.Fields) > 0 {
				return ty, true
			}
		}
	}
	return Type{}, false
}

// interfaceClassBase is the class an interface extends, as codegen holds
// it: an interface extending one class (`interface
// ChildProcessWithoutNullStreams extends ChildProcess`) only narrows its
// members' types, and the class's instance is what it holds.
func (e *Emitter) interfaceClassBase(sym *binder.Symbol) (Type, bool) {
	var base *binder.Symbol
	for _, d := range sym.Declarations {
		id, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok || len(id.TypeParams) > 0 {
			return Type{}, false
		}
		// Names in a module carry its suffix, as the interface's own does.
		suffix := strings.TrimPrefix(sym.Name, ast.Unmangle(sym.Name))
		for _, name := range id.Extends {
			for sc := sym.Scope; sc != nil; sc = sc.Parent {
				b := sc.Symbols.Get(name + suffix)
				if b == nil {
					b = sc.Symbols.Get(name)
				}
				if b != nil {
					if _, isClass := e.classRepr(b); isClass {
						if base != nil && base != b {
							return Type{}, false
						}
						base = b
					}
					break
				}
			}
		}
	}
	if base != nil {
		return e.classRepr(base)
	}
	return Type{}, false
}

func (r reprCtx) of(t *checker.Type) (Type, bool) {
	if t.Flags&checker.Union != 0 {
		return r.union(t)
	}
	switch {
	case t.Flags&(checker.Any|checker.Unknown) != 0, t.AnyArith:
		return TypeAny, true
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
		return TypeF64, true
	case t.Flags&checker.Float32 != 0:
		return TypeF32, true
	case t.Flags&checker.Int != 0:
		return Type{IR: "i" + itoa(t.Bits), Signed: t.Signed}, true
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return TypePtr, true
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return TypeBool, true
	case t.Flags&(checker.BigInt|checker.BigIntLiteral) != 0:
		return BigIntType(), true
	case t.Flags&checker.ESSymbol != 0:
		return SymbolType(), true
	case t.Flags&checker.Void != 0:
		return TypeVoid, true
	case t.Flags&checker.Never != 0:
		return TypeNever, true
	case t.Flags&checker.Null != 0:
		return TypeNull, true
	case t.Flags&checker.Undefined != 0:
		return TypeUndefined, true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array:
		if e, ok := r.of(t.Elem); ok {
			// JavaScript stores anything into an array of only null or
			// undefined (`var a = [,]`): -compat=js holds its elements boxed.
			if r.js != nil && r.js() && t.Elem.Flags&^checker.Nullish == 0 {
				e = TypeAny
			}
			return ArrayOf(e), true
		}
	case t.Flags&checker.Object != 0 && t.Kind == checker.Tuple && len(t.Elems) > 0:
		// A tuple an annotation declares is a headerless positional struct
		// (TupleType); one the checker inferred (`await Promise.all([a,
		// b])`) is the array codegen builds, of its one element kind.
		elems := make([]Type, len(t.Elems))
		for i, el := range t.Elems {
			e, ok := r.of(el)
			if !ok {
				return Type{}, false
			}
			elems[i] = e
		}
		if r.tuples {
			return TupleType(elems), true
		}
		for _, e := range elems[1:] {
			if reprKey(e) != reprKey(elems[0]) {
				return Type{}, false
			}
		}
		return ArrayOf(elems[0]), true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Function && len(t.Overloads) == 0 && len(t.TypeParams) == 0:
		return r.function(t)
	}
	// `arguments` is held as an any[] (synthesizeArgumentsObject).
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && t.Symbol.Name == "IArguments" {
		return ArrayOf(TypeAny), true
	}
	// `Function`, any callable, and a primitive's wrapper object (`new
	// Boolean(true)`) or `Object` are held as a dynamic value.
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) == 0 && r.libGlobal(t.Symbol) {
		switch t.Symbol.Name {
		case "Function", "Boolean", "Number", "String", "Object":
			return TypeAny, true
		case "RegExpExecArray":
			// exec()'s match array, its header carrying index, input and
			// groups (resolveType).
			return execArrayType(), true
		case "RegExpMatchArray":
			return regExpExecResultType(), true
		}
	}
	// A user class's value (`typeof C`) is its constructor reference,
	// boxed (resolveTypeofType).
	if t.Flags&checker.Object != 0 && t.ClassOf != nil && r.class != nil {
		if _, ok := r.class(t.ClassOf); ok {
			return TypeAny, true
		}
	}
	if t.Flags&checker.Object != 0 && t.Kind == checker.Instance && t.Symbol != nil && len(t.TypeArgs) == 0 && r.class != nil {
		if ty, ok := r.class(t.Symbol); ok {
			return ty, true
		}
	}
	// A string index signature and nothing else (`{ [k: string]: T }`): a
	// string-keyed map held as a dynamic object.
	// An interface of one (`NodeJS.Dict<T>`) is the same, its value
	// `T | undefined` held as T (codegen's Dict).
	if r.objects && t.Flags&checker.Object != 0 && (t.Kind == checker.Anonymous || t.Kind == checker.Interface) && t.StringIndex != nil && len(t.Props) == 0 && len(t.Calls) == 0 && len(t.Constructs) == 0 {
		if v, ok := r.of(t.StringIndex); ok {
			if t.Kind == checker.Interface && v.Nullable && v.IsUndefined && !v.IsDynamic {
				v = v.withoutNullable()
			}
			return dictViewType(v), true
		}
	}
	if r.objects && t.Flags&checker.Object != 0 && t.Kind == checker.Anonymous && t.ClassOf == nil && len(t.Calls) == 0 && len(t.Constructs) == 0 && t.StringIndex == nil && t.NumberIndex == nil {
		// An interface's members, instantiated: the interface's own
		// representation when codegen gives it one (one extending a class).
		if t.Symbol != nil && r.iface != nil {
			if ty, ok := r.iface(t.Symbol); ok {
				return ty, true
			}
		}
		return r.object(t)
	}
	// A builtin interface whose declaration names its native layout.
	if ty, ok := lowerTypeReprWith(t, r.of); ok {
		return ty, true
	}
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) == 0 && r.iface != nil {
		return r.iface(t.Symbol)
	}
	return Type{}, false
}

func itoa(n int) string {
	switch n {
	case 8:
		return "8"
	case 16:
		return "16"
	case 32:
		return "32"
	}
	return "64"
}

// reprOfUnion is a union's representation: `T | null`/`T | undefined` is T's
// nullable form; two or more primitive kinds (`number | bigint`,
// `string | number`) are a NaN-boxed union naming its members.
func (r reprCtx) union(t *checker.Type) (Type, bool) {
	var parts []Type
	seen := map[string]bool{}
	null, undef := false, false
	for _, m := range t.Types {
		var pt Type
		switch {
		case m.Flags&checker.Undefined != 0:
			undef = true
			continue
		case m.Flags&checker.Null != 0:
			null = true
			continue
		case m.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
			pt = TypeBool
		case r.literals && m.Flags&checker.StringLiteral != 0:
			pt = Type{IR: "ptr", IsStrLiteral: true, LitValue: m.Value}
		default:
			var ok bool
			pt, ok = r.of(m)
			if ok && isUnconstrainedDynamic(pt) {
				// A dictionary view with only null/undefined beside it
				// stays the view: the box holds the absent value too.
				if pt.DynPropTy != nil && onlyAbsentBesides(t, m) {
					return pt, true
				}
				return TypeAny, true // a member held as `any` holds the rest
			}
			if !ok || pt.IsDynamic || pt.IsNull || pt.IsUndefined || pt.IR == "void" {
				return Type{}, false
			}
		}
		if k := reprKey(pt); !seen[k] {
			seen[k] = true
			parts = append(parts, pt)
		}
	}
	// `ArrayBufferLike` (an ArrayBuffer or a SharedArrayBuffer) is held as
	// an ArrayBuffer handle, as codegen resolves the alias.
	if len(parts) > 1 {
		allAB := true
		for _, p := range parts {
			allAB = allAB && p.IsArrayBuffer
		}
		if allAB {
			parts = []Type{ArrayBufferType()}
		}
	}
	// Literals of one string kind (`'a' | 'b'`) are a string: only a lone
	// literal keeps its value (a discriminant's tag).
	if len(parts) > 1 {
		allStr := true
		for _, p := range parts {
			allStr = allStr && p.IR == "ptr" && isStringTy(p) && !hasHandleFlag(p)
		}
		if allStr {
			parts = []Type{TypePtr}
		}
	}
	switch {
	case len(parts) == 0 && undef:
		return TypeUndefined, true
	case len(parts) == 0:
		return TypeNull, true
	case len(parts) == 1:
		p := parts[0]
		if !null && !undef {
			return p, true
		}
		if null && undef && threeStateEligible(p) {
			return withNullAndUndef(p), true
		}
		if p.IR == "ptr" || p.IsArray {
			p.Nullable, p.IsUndefined = true, undef
			return p, true
		}
		if undef {
			return undefinedableElem(p), true
		}
		p.Nullable = true // `T | null`
		return p, true
	}
	for _, p := range parts {
		if p.IsArray || p.IsObject && !p.IsSymbol || p.IsFunc {
			return Type{}, false // members a box cannot name without a layout
		}
	}
	return Type{IR: TypeAny.IR, IsDynamic: true, UnionMembers: parts, Nullable: null || undef, IsUndefined: undef}, true
}

// reprOfFunction is a function type's representation: a closure pointer
// carrying its parameters' and result's representations.
func (r reprCtx) function(t *checker.Type) (Type, bool) {
	var params []Type
	if t.ThisType != nil {
		this, ok := r.of(t.ThisType)
		if !ok {
			return Type{}, false
		}
		params = append(params, this)
	}
	var optional []bool
	for i, p := range t.Params {
		pr, ok := r.of(p)
		if !ok {
			return Type{}, false
		}
		if pr.IR == "void" {
			pr = TypeAny // a `void` parameter (`T` = void) holds whatever is passed
		}
		params = append(params, pr)
		if t.OptionalParam(i) {
			if optional == nil {
				optional = make([]bool, len(t.Params))
			}
			optional[i] = true
		}
	}
	ret := TypeVoid
	if t.Result != nil && t.Result.Flags&checker.Never != 0 {
		ret = TypeNever // a function that never returns (ADR-00869)
	} else if t.Result != nil {
		rr, ok := r.of(t.Result)
		if !ok {
			return Type{}, false
		}
		ret = rr
	}
	ty := FuncType(params, ret)
	ty.FuncHasRest = t.HasRestParam()
	ty.FuncThis = t.ThisType != nil
	ty.FuncParamOptional = optional
	if len(t.ParamNames) == len(t.Params) && t.ThisType == nil {
		ty.FuncParamNames = t.ParamNames
	}
	// A function reading `arguments` (its implicit rest) has its caller
	// count what it passes (funcTypeFromSig).
	ty.FuncCountsArgs = countsArgs(ty.FuncHasRest, ty.FuncParamNames)
	return ty, true
}

// object is an object type's representation: a dynamic object in the js
// lane, otherwise the fixed-layout object of its properties in order.
func (r reprCtx) object(t *checker.Type) (Type, bool) {
	if r.js != nil && r.js() {
		return TypeAny, true
	}
	fields := make([]Field, 0, len(t.Props))
	for _, p := range t.Props {
		if p.Accessor {
			return Type{}, false
		}
		ty, ok := r.of(p.Type)
		if isGeneratorFunc(p.Type) {
			// An object's generator method is held as `any`.
			ty, ok = TypeAny, true
		}
		if !ok {
			return Type{}, false
		}
		fields = append(fields, Field{Name: p.Name, Ty: ty, Optional: p.Optional})
	}
	return ObjectType(fields), true
}

// isGlobalFunc reports whether id names a declaration of the builtin
// globals, directly or through an unannotated variable it initializes
// (`const f = parseInt`).
func (o frontOracle) isGlobalFunc(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || o.b.Globals == nil {
		return false
	}
	if len(sym.Declarations) == 1 {
		if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.TypeAnnot == nil {
			if init, ok := v.Init.(*ast.Identifier); ok && init != id {
				return o.isGlobalFunc(init)
			}
		}
	}
	for sc := sym.Scope; sc != nil; sc = sc.Parent {
		if sc == o.b.Module {
			return false
		}
		if sc == o.b.Globals {
			return true
		}
	}
	return false
}

// withPromiseCallbacks collects the function literals passed as the
// executor of `new Promise(…)` and as a rejection handler.
func (o frontOracle) withPromiseCallbacks(prog *ast.Program) frontOracle {
	o.executors, o.onRejected = map[ast.Node]*ast.NewExpression{}, map[ast.Node]ast.Expression{}
	o.anyBound = map[ast.Node]bool{}
	o.inBody = map[ast.Node]bool{}
	o.widenedReduceOf = map[ast.Node]*ast.CallExpression{}
	isFn := func(e ast.Expression) bool {
		switch e.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			return true
		}
		return false
	}
	for _, st := range prog.Body {
		ast.Inspect(st, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.VarDeclaration:
				if x.TypeAnnot != nil && isFn(x.Init) {
					if kw, ok := x.TypeAnnot.TypeNode().(*ast.KeywordType); ok && kw.Keyword == "any" {
						o.anyBound[x.Init] = true
					}
				}
			case *ast.AssignmentExpression:
				// A function stored into an untyped module binding declared
				// without a value (`var ref; ref = function () {…}`), which
				// codegen holds as `any`, is boxed too.
				if fe, gen := x.Right.(*ast.FunctionExpression); gen && fe.IsGenerator {
					break // a generator keeps its own convention
				}
				if id, ok := x.Left.(*ast.Identifier); ok && x.Op == "=" && isFn(x.Right) {
					if sym, _ := o.b.Resolve(id); sym != nil {
						if v, ok := singleVarDecl(sym); ok && v.TypeAnnot == nil && v.Init == nil && sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope {
							o.anyBound[x.Right] = true
						}
					}
				}
			case *ast.NewExpression:
				if len(x.Args) > 0 && x.ClassName == "Promise" && !x.Qualified && isFn(x.Args[0]) {
					o.executors[x.Args[0]] = x
				}
			case *ast.CallExpression:
				if cb := jsWidenedReduceCallback(x); cb != nil {
					o.widenedReduceOf[cb] = x
				}
				if m, ok := x.Callee.(*ast.MemberExpression); ok {
					if m.Property == "catch" && len(x.Args) > 0 && isFn(x.Args[0]) {
						o.onRejected[x.Args[0]] = m.Object
					} else if m.Property == "then" && len(x.Args) > 1 && isFn(x.Args[1]) {
						o.onRejected[x.Args[1]] = m.Object
					}
				}
			}
			return true
		})
	}
	return o
}

// isRejectionParam reports a name bound to the first parameter of a native
// promise's rejection handler, unannotated or annotated any/unknown: the
// handler is called with the caught value (emitRejectCallback).
func (o frontOracle) isRejectionParam(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	fn := sym.Declarations[0].Node
	recv, ok := o.onRejected[fn]
	if !ok {
		return false
	}
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	}
	if len(params) == 0 || params[0].Name != id.Name {
		return false
	}
	if params[0].Type != nil {
		at, ok := o.AnnotationType(params[0].Type)
		if len(params) != 1 || !ok || !at.IsDynamic {
			return false
		}
	}
	rt := o.c.TypeOf(recv)
	if o.c.Unanswered(rt) {
		return false
	}
	r, ok := o.r.of(rt)
	return ok && r.IsPromise
}

// executorParam is the `new Promise(…)` whose executor declares the name
// id is bound to, and that parameter's index (resolve 0, reject 1).
func (o frontOracle) executorParam(id *ast.Identifier) (*ast.NewExpression, int) {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return nil, -1
	}
	ne := o.executors[sym.Declarations[0].Node]
	if ne == nil {
		return nil, -1
	}
	var params []ast.Param
	switch fn := sym.Declarations[0].Node.(type) {
	case *ast.ArrowFunction:
		params = fn.Params
	case *ast.FunctionExpression:
		params = fn.Params
	}
	for i, p := range params {
		if i < 2 && p.Name == id.Name {
			return ne, i
		}
	}
	return nil, -1
}

// assertedOperand is the operand of the `x as T` e is, directly or as the
// initializer of an unannotated variable e names; nil otherwise.
func (o frontOracle) assertedOperand(e ast.Expression) ast.Expression {
	switch x := e.(type) {
	case *ast.AsExpression:
		if x.TypeAnnot != nil {
			return x.Expr
		}
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil || len(sym.Declarations) != 1 {
			return nil
		}
		if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.TypeAnnot == nil && v.Kind == "const" {
			if as, ok := v.Init.(*ast.AsExpression); ok && as.TypeAnnot != nil {
				return as.Expr
			}
		}
	}
	return nil
}

// presentType reports a type with no null, undefined or void in it.
func presentType(t *checker.Type) bool {
	if t.Flags&(checker.Any|checker.Unknown|checker.Never|checker.Null|checker.Undefined|checker.Void) != 0 {
		return false
	}
	for _, m := range t.Types {
		if m.Flags&(checker.Null|checker.Undefined|checker.Void|checker.Any|checker.Unknown) != 0 {
			return false
		}
	}
	return true
}

// literalStorage is an object literal's layout with each plain property's
// field the representation its value is stored in: a value a check narrows
// (`{ v: first }` after `typeof first === 'boolean'`) keeps its own storage,
// as a narrowed read does not change how a value is held.
func (o frontOracle) literalStorage(lit *ast.ObjectLiteral, ty Type) Type {
	fields := append([]Field(nil), ty.Fields...)
	for _, p := range lit.Properties {
		if p.KeyExpr != nil || p.AccessorKind != "" || p.Value == nil {
			continue
		}
		id, ok := p.Value.(*ast.Identifier)
		if !ok {
			continue
		}
		vt, ok := o.ExprType(id)
		if !ok {
			continue
		}
		for i := range fields {
			if fields[i].Name == p.Key {
				fields[i].Ty = vt
			}
		}
	}
	ty.Fields = fields
	return ty
}

// narrowedToInstance reports a receiver held as `any` that a check narrows
// to a class instance (`x instanceof C`): codegen reads its members through
// the class's layout.
func (o frontOracle) narrowedToInstance(obj ast.Expression) bool {
	if !flowTrusted(o.b, obj) {
		return false
	}
	t := o.c.TypeOf(obj)
	return !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.Kind == checker.Instance && t.Flags&checker.Union == 0
}

// plainMemberRead reports `obj.name`: a named property, not a computed or
// symbol key, which codegen reads dynamically whatever the receiver.
func plainMemberRead(e ast.Expression) bool {
	m, ok := e.(*ast.MemberExpression)
	return ok && !strings.HasPrefix(m.Property, "@@") && !strings.HasPrefix(m.Property, "__kml")
}

// onlyAbsentBesides reports that every member of union u other than m is
// null or undefined.
func onlyAbsentBesides(u, m *checker.Type) bool {
	for _, o := range u.Types {
		if o != m && o.Flags&(checker.Null|checker.Undefined) == 0 {
			return false
		}
	}
	return true
}

// isGeneratorFunc reports a function type returning a Generator or an
// AsyncGenerator.
func isGeneratorFunc(t *checker.Type) bool {
	if t.Flags&checker.Object == 0 || t.Kind != checker.Function {
		return false
	}
	res := t.Result
	return res != nil && res.Symbol != nil && (res.Kind == checker.Interface || res.Kind == checker.Instance) &&
		(res.Symbol.Name == "Generator" || res.Symbol.Name == "AsyncGenerator")
}

// storesPresent mirrors the emitter's: every reaching store is a
// declaration or plain assignment of a value the checker types present.
func (o frontOracle) storesPresent(id *ast.Identifier) bool {
	stores, ok := o.b.ReachingStores(id)
	if !ok {
		return false
	}
	for _, s := range stores {
		var v ast.Expression
		switch x := s.(type) {
		case *ast.VarDeclaration:
			v = x.Init
		case *ast.AssignmentExpression:
			v = x.Right
		}
		if v == nil {
			return false
		}
		t := o.c.TypeOf(v)
		if o.c.Unanswered(t) || !presentType(t) || t.Flags&(checker.Any|checker.Unknown) != 0 {
			return false
		}
	}
	return true
}

// reprStoresPresent is the emitter's storesPresent: every reaching store's
// value is held present (an element read, which may be absent, is not).
func (o frontOracle) reprStoresPresent(id *ast.Identifier) bool {
	stores, ok := o.b.ReachingStores(id)
	if !ok {
		return false
	}
	for _, s := range stores {
		var v ast.Expression
		switch x := s.(type) {
		case *ast.VarDeclaration:
			v = x.Init
		case *ast.AssignmentExpression:
			v = x.Right
		}
		if v == nil {
			return false
		}
		t, ok := o.ExprType(v)
		if !ok || t.IR == "" || t.IR == "void" || t.Nullable || t.IsUndefined || t.IsNull || t.IsDynamic {
			return false
		}
	}
	return true
}

// storesIndexRead mirrors the emitter's for a variable reference.
func (o frontOracle) storesIndexRead(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	for _, st := range o.b.StoresTo(sym) {
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

// wellKnownSymbol reports a read of a builtin `Symbol.X` symbol.
func (o frontOracle) wellKnownSymbol(e ast.Expression) bool {
	m, ok := e.(*ast.MemberExpression)
	if !ok {
		return false
	}
	id, ok := m.Object.(*ast.Identifier)
	if !ok || id.Name != "Symbol" {
		return false
	}
	if sym, _ := o.b.Resolve(id); sym != nil && (o.b.Globals == nil || sym.Scope != o.b.Globals) {
		return false
	}
	t := o.c.TypeOf(m)
	return !o.c.Unanswered(t) && t.Flags&checker.ESSymbol != 0
}

// jsBoxedArrayLiteral mirrors the js lane's inferArrayType: a literal of
// only holes, `undefined` and `null`, or of two or more element kinds, is
// boxed (`any[]`).
func (o frontOracle) jsBoxedArrayLiteral(lit *ast.ArrayLiteral) bool {
	if len(lit.Elements) == 0 {
		return false
	}
	nullish := true
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.NullLiteral); !ok {
			nullish = false
			break
		}
	}
	if nullish {
		return true
	}
	kinds := map[string]bool{}
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			return false
		}
		t, ok := o.ExprType(el)
		if !ok {
			return false
		}
		kinds[elemKindKeyBroad(t)] = true
	}
	return len(kinds) >= 2
}

// jsBoxedInit reports an unannotated js-lane declaration whose initializer
// is a boxed array literal (jsBoxedArrayLiteral).
func (o frontOracle) jsBoxedInit(v *ast.VarDeclaration) bool {
	lit, ok := v.Init.(*ast.ArrayLiteral)
	return ok && v.TypeAnnot == nil && o.r.js != nil && o.r.js() && o.jsBoxedArrayLiteral(lit)
}

// matchArray reports a value of the lib's RegExpMatchArray/RegExpExecArray
// (or one of them or null, narrowed).
func (o frontOracle) matchArray(e ast.Expression) bool {
	t := o.c.TypeOf(e)
	if o.c.Unanswered(t) {
		return false
	}
	t = o.c.NonNullable(t)
	return t.Flags&checker.Object != 0 && t.Symbol != nil && (t.Symbol.Name == "RegExpMatchArray" || t.Symbol.Name == "RegExpExecArray")
}

// libGlobal reports a symbol the builtin declarations declare globally (a
// scope with no parent), not one of the program's.
func (r reprCtx) libGlobal(sym *binder.Symbol) bool {
	return sym.Scope != nil && sym.Scope.Parent == nil && sym.Scope.Kind == binder.ModuleScope
}

// returnsAnswered is a body's return type where the checker has none but
// every return's value has one representation (a member read the lib does
// not declare, answered as codegen reads it).
func (o frontOracle) returnsAnswered(body *ast.BlockStatement) (Type, bool, bool) {
	var ty Type
	n := 0
	ok := true
	ast.Inspect(body, func(x ast.Node) bool {
		if !ok {
			return false
		}
		switch r := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.ReturnStatement:
			if r.Value == nil {
				ok = false
				return false
			}
			t, has := o.ExprType(r.Value)
			if !has || n > 0 && reprKey(t) != reprKey(ty) {
				ok = false
				return false
			}
			ty, n = t, n+1
		}
		return true
	})
	if !ok || n == 0 {
		return Type{}, false, false
	}
	return ty, true, true
}

// unionSubset is the union ty narrowed to nt's members, when each of them
// is a distinct member of ty and some are ruled out: a box of those
// members, in ty's order, or the one left.
func (o frontOracle) unionSubset(ty Type, nt *checker.Type) (Type, bool) {
	keep := map[string]bool{}
	for _, m := range nt.Types {
		r, ok := o.r.of(m)
		if !ok {
			return Type{}, false
		}
		if r.IsDynamic && len(r.UnionMembers) > 0 {
			for _, rm := range r.UnionMembers {
				keep[reprKey(rm)] = true
			}
			continue
		}
		keep[reprKey(r)] = true
	}
	var sub []Type
	for _, m := range ty.UnionMembers {
		if keep[reprKey(m)] {
			sub = append(sub, m)
			delete(keep, reprKey(m))
		}
	}
	if len(keep) > 0 || len(sub) == 0 || len(sub) == len(ty.UnionMembers) {
		return Type{}, false
	}
	if len(sub) == 1 {
		return sub[0], true
	}
	out := ty
	out.UnionMembers = sub
	return out, true
}

// boxedParams is a function's parameters taken boxed: each an `any`, a
// rest one an `any[]`.
func boxedParams(t Type) []Type {
	params := make([]Type, len(t.FuncParams))
	for i := range params {
		params[i] = TypeAny
	}
	if t.FuncHasRest && len(params) > 0 {
		params[len(params)-1] = ArrayOf(TypeAny)
	}
	return params
}

// typedArrayView reports whether m reads a typed array's `buffer` or
// `byteOffset`.
func (o frontOracle) typedArrayView(m *ast.MemberExpression) bool {
	if m.Property != "buffer" && m.Property != "byteOffset" {
		return false
	}
	rt, ok := o.ExprType(m.Object)
	return ok && rt.IsTypedArray
}

// intrinsicValueType is an intrinsic's type as a value (ensureBuiltinFunc):
// its widest overload (a rest parameter, else the most parameters), every
// parameter boxed.
func (o frontOracle) intrinsicValueType(key string, fn *checker.Type, t Type) Type {
	if fn != nil && len(fn.Overloads) > 0 {
		w := fn.Overloads[len(fn.Overloads)-1]
		for _, ov := range fn.Overloads {
			if ov.HasRestParam() && !w.HasRestParam() || ov.HasRestParam() == w.HasRestParam() && len(ov.Params) > len(w.Params) {
				w = ov
			}
		}
		if wt, ok := o.r.of(w); ok && wt.IsFunc {
			t = wt
		}
	}
	t.FuncParams = boxedParams(t)
	if key == "Date.UTC" {
		// It counts its arguments: one rest (ensureBuiltinFunc).
		t.FuncParams, t.FuncHasRest = []Type{ArrayOf(TypeAny)}, true
	}
	return t
}

// singleVarDecl is sym's one variable declaration.
func singleVarDecl(sym *binder.Symbol) (*ast.VarDeclaration, bool) {
	if len(sym.Declarations) != 1 {
		return nil, false
	}
	v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	return v, ok
}

// jsWidenedVar reports a -compat=js module var declared more than once,
// untyped, with initializers of different kinds: codegen holds it as one
// any box (crossTypeWidenedBindings).
func (o frontOracle) jsWidenedVar(sym *binder.Symbol) bool {
	if o.r.js == nil || !o.r.js() || len(sym.Declarations) < 2 || sym.Scope == nil || sym.Scope.Kind != binder.ModuleScope {
		return false
	}
	kind := ""
	for _, d := range sym.Declarations {
		v, ok := d.Node.(*ast.VarDeclaration)
		if !ok || v.TypeAnnot != nil {
			return false
		}
		if v.Init == nil {
			continue
		}
		t := o.c.TypeOf(v.Init)
		if o.c.Unanswered(t) {
			return false
		}
		r, ok := o.r.of(t)
		if !ok {
			return false
		}
		k := scalarTypeKind(r)
		if k == "" {
			k = reprKey(r)
		}
		switch {
		case kind == "":
			kind = k
		case kind != k:
			return true
		}
	}
	return false
}

// findArrayPatternElem is the leaf of an array pattern (nested array
// patterns included) that binds name, or nil.
func findArrayPatternElem(elems []ast.ArrayPatternElem, name string) *ast.ArrayPatternElem {
	for i := range elems {
		if elems[i].Name == name {
			return &elems[i]
		}
		if el := findArrayPatternElem(elems[i].SubArray, name); el != nil {
			return el
		}
		if el := findArrayPatternElemIn(elems[i].SubObject, name); el != nil {
			return el
		}
	}
	return nil
}

// findArrayPatternElemIn is the array-pattern leaf binding name nested in
// an object pattern's properties, or nil.
func findArrayPatternElemIn(props []ast.DestructProp, name string) *ast.ArrayPatternElem {
	for i := range props {
		if el := findArrayPatternElem(props[i].SubArray, name); el != nil {
			return el
		}
		if el := findArrayPatternElemIn(props[i].SubObject, name); el != nil {
			return el
		}
	}
	return nil
}

// localClassValue reports a parameter or local holding a user class's
// value (`c: typeof C`): codegen reads its statics typed, through the
// class it names.
func (o frontOracle) localClassValue(obj ast.Expression) bool {
	id, isID := obj.(*ast.Identifier)
	if !isID {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym != nil && len(sym.Declarations) > 0 {
		if _, isClass := sym.Declarations[0].Node.(*ast.ClassDeclaration); isClass {
			return true // the class's own name: its statics, typed
		}
	}
	if sym == nil || sym.Scope == nil || sym.Scope.Kind == binder.ModuleScope {
		return false
	}
	t := o.c.TypeOf(id)
	return !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.ClassOf != nil
}

// evolvingBoxed reports an unannotated binding initialized with `[]` whose
// later stores mix element kinds: codegen holds it as a boxed-element array
// (inferEmptyArrayElemTypes), the checker as an array of their union.
func (o frontOracle) evolvingBoxed(v *ast.VarDeclaration) bool {
	lit, ok := v.Init.(*ast.ArrayLiteral)
	if !ok || len(lit.Elements) != 0 || v.TypeAnnot != nil {
		return false
	}
	t := o.c.ModuleVarType(v)
	if t == nil || o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Array || t.Elem.Flags&checker.Union == 0 {
		return false
	}
	et, ok := o.r.of(t.Elem)
	return ok && et.IsDynamic && len(et.UnionMembers) > 1
}

// elementCopyingMethod are the array methods whose result holds the
// receiver's own elements.
var elementCopyingMethod = map[string]bool{
	"filter": true, "slice": true, "concat": true, "reverse": true, "sort": true,
	"toSorted": true, "toReversed": true, "toSpliced": true, "splice": true, "with": true,
}

// boxedElementParam is the type of an unannotated first parameter of a
// callback passed to an array method on a boxed-element array: its boxes.
func (o frontOracle) boxedElementParam(id *ast.Identifier) (Type, bool) {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return Type{}, false
	}
	fn := sym.Declarations[0].Node
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return Type{}, false
	}
	if len(params) == 0 || params[0].Name != id.Name || params[0].Type != nil {
		return Type{}, false
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok || len(call.Args) == 0 || call.Args[0] != fn {
		return Type{}, false
	}
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok {
		return Type{}, false
	}
	rt, ok := o.ExprType(m.Object)
	if ok && o.boxedArrayCall(call) {
		return TypeAny, true // the box's method passes its elements boxed
	}
	if !ok || !rt.IsArray || rt.IsTypedArray || rt.ElemType == nil || reprKey(*rt.ElemType) != reprKey(TypeAny) {
		return Type{}, false
	}
	if ct := o.c.TypeOf(m.Object); o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Array || ct.Elem.Flags&checker.Any != 0 {
		return Type{}, false
	}
	return TypeAny, true
}

// boxedArrayCall reports an element-callback method (`map`, `filter`, …)
// called on an array codegen holds boxed (an `any`-returning
// implementation's result) whose elements are typed arrays: a dynamic
// call on the box, which passes the callback boxed elements and returns a
// box.
func (o frontOracle) boxedArrayCall(call *ast.CallExpression) bool {
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || m.Optional || !elementCallback[m.Property] {
		return false
	}
	rt, ok := o.ExprType(m.Object)
	if !ok || !isUnconstrainedDynamic(rt) {
		return false
	}
	ct := o.c.TypeOf(m.Object)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Array || ct.Elem.Flags&checker.Any != 0 {
		return false
	}
	et, ok := o.r.of(ct.Elem)
	return ok && et.IsTypedArray // a typed-array element is held boxed
}

// elementCallback names the array methods whose callback's first parameter
// is an element.
var elementCallback = map[string]bool{
	"map": true, "filter": true, "forEach": true, "some": true, "every": true, "find": true,
	"findIndex": true, "findLast": true, "findLastIndex": true, "flatMap": true,
}

// computedKeyObject mirrors inferDynamicObjectType.
func (o frontOracle) computedKeyObject(ol *ast.ObjectLiteral) (Type, bool) {
	var valTy *Type
	for _, p := range ol.Properties {
		if _, spread := p.Value.(*ast.SpreadElement); spread || p.AccessorKind != "" {
			return TypeAny, true
		}
		t, ok := o.ExprType(p.Value)
		if !ok {
			return Type{}, false
		}
		if t.IsDynamic || t.IsFunc || t.IR == "" {
			return TypeAny, true
		}
		if valTy == nil {
			valTy = &t
		} else if reprKey(*valTy) != reprKey(t) {
			return TypeAny, true
		}
	}
	if valTy == nil {
		return TypeAny, true
	}
	return dictViewType(*valTy), true
}

// jsWidenedReduceCallback is the callback of a `reduce`/`reduceRight` call
// with an initial value whose block body can fall off the end.
func jsWidenedReduceCallback(call *ast.CallExpression) ast.Node {
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || (m.Property != "reduce" && m.Property != "reduceRight") || len(call.Args) != 2 {
		return nil
	}
	var body *ast.BlockStatement
	switch f := call.Args[0].(type) {
	case *ast.ArrowFunction:
		body = f.Block
	case *ast.FunctionExpression:
		body = f.Body
	}
	if body == nil || !(blockMayCompleteNormally(body) || blockHasBareReturn(body)) {
		return nil
	}
	return call.Args[0]
}

// thisReturningArrayMethod names the Array methods that return their
// receiver.
var thisReturningArrayMethod = map[string]bool{"sort": true, "reverse": true, "fill": true, "copyWithin": true}

// overloadsAgree reports a call whose callee's overloads all return one
// representation: whichever the run-time value picks, the result is held
// so (`zlib.gunzipSync(buf)`'s Buffer).
func (o frontOracle) overloadsAgree(call *ast.CallExpression) bool {
	ft := o.c.TypeOf(call.Callee)
	if o.c.Unanswered(ft) || ft.Flags&checker.Object == 0 {
		return false
	}
	sigs := ft.Overloads
	if len(sigs) == 0 {
		sigs = ft.Calls
	}
	if len(sigs) < 2 {
		return false
	}
	key := ""
	for _, sig := range sigs {
		if sig.Result == nil || len(sig.TypeParams) > 0 {
			return false
		}
		r, ok := o.r.of(sig.Result)
		if !ok {
			return false
		}
		if key == "" {
			key = reprKey(r)
		} else if reprKey(r) != key {
			return false
		}
	}
	return true
}

// declaredParam is the parameter named name a function declares sym as,
// or nil.
func declaredParam(sym *binder.Symbol, name string) *ast.Param {
	if len(sym.Declarations) != 1 {
		return nil
	}
	var params []ast.Param
	switch f := sym.Declarations[0].Node.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.FunctionDeclaration:
		params = f.Params
	default:
		return nil
	}
	for i := range params {
		if params[i].Name == name {
			return &params[i]
		}
	}
	return nil
}

// functionValueProp reports a read of a property a program's function
// value carries by assignment (`fn.p = v`), not one of Function's.
func (o frontOracle) functionValueProp(m *ast.MemberExpression) bool {
	rt := o.c.NonNullable(o.c.TypeOf(m.Object))
	if o.c.Unanswered(rt) || rt.Flags&checker.Object == 0 || o.c.Unanswered(o.c.TypeOf(m)) {
		return false
	}
	switch {
	case rt.Kind == checker.Function:
	case rt.Kind == checker.Anonymous && len(rt.Calls) > 0:
	default:
		return false
	}
	switch m.Property {
	case "name", "length", "call", "apply", "bind", "toString", "prototype", "constructor":
		return false // Function's own
	}
	return true
}

// hoistSkippable mirrors hoistedVarMaySkip: a `var` in a nested block (an
// `if`/loop/`switch`/`try` body), not a `for` init, or after a terminator in
// its function's own statement list.
func (o frontOracle) hoistSkippable(v *ast.VarDeclaration) bool {
	if v.Kind != "var" {
		return false
	}
	var node ast.Node = v
	parent := o.c.ParentOf(v)
	if l, isList := parent.(*ast.VarDeclarationList); isList {
		node, parent = l, o.c.ParentOf(l)
	}
	switch p := parent.(type) {
	case *ast.Program:
		return false
	case *ast.ForStatement:
		return p.Init != node
	case *ast.BlockStatement:
		switch o.c.ParentOf(p).(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
			for _, st := range p.Body {
				if st == node {
					return false
				}
				switch st.(type) {
				case *ast.ReturnStatement, *ast.BreakStatement, *ast.ContinueStatement, *ast.ThrowStatement:
					return true
				}
			}
			return false
		}
		return true
	case nil:
		return false
	}
	return true
}

// inferredBodyReturn is what an unannotated, plain (not async, not
// generator) function's block body returns, as ReturnType answers it.
func (o frontOracle) inferredBodyReturn(fn ast.Node) (Type, bool) {
	// A recursive function's own call inside its body is the body's
	// result again: no answer from that side.
	if o.inBody == nil || o.inBody[fn] {
		return Type{}, false
	}
	o.inBody[fn] = true
	defer delete(o.inBody, fn)
	var body *ast.BlockStatement
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		if f.RetType != nil || f.IsAsync {
			return Type{}, false
		}
		if f.Block == nil && f.Body != nil {
			return o.ExprType(f.Body)
		}
		body = f.Block
	case *ast.FunctionExpression:
		if f.RetType != nil || f.IsAsync || f.IsGenerator {
			return Type{}, false
		}
		body = f.Body
	case *ast.FunctionDeclaration:
		if f.ReturnType != nil || f.IsAsync || f.IsGenerator {
			return Type{}, false
		}
		body = f.Body
	}
	if body == nil {
		return Type{}, false
	}
	rt, ok, hasValue := o.ReturnType(body)
	return rt, ok && hasValue
}

// hasAccessor reports an object literal declaring a getter or setter.
func hasAccessor(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		if p.AccessorKind != "" {
			return true
		}
	}
	return false
}

// objLitClassRepr is the synthetic class an accessor-bearing object literal
// is lowered to.
func (e *Emitter) objLitClassRepr(lit *ast.ObjectLiteral) (Type, bool) {
	if info, ok := e.classes[e.ensureObjLitClass(lit)]; ok {
		return info.Ty, true
	}
	return Type{}, false
}

// anyImplCallbackParam reports an unannotated parameter of a function
// literal passed to an overloaded function whose implementation declares
// that argument's parameter `any` (the literal is boxed, its parameters
// boxes; a class instance parameter keeps its class), or takes it as a
// listener of boxes (`(...args: any[]) => void`).
func (o frontOracle) anyImplCallbackParam(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	fn := sym.Declarations[0].Node
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return false
	}
	pidx := -1
	for i, p := range params {
		if p.Name == id.Name && p.Type == nil {
			pidx = i
		}
	}
	if pidx < 0 {
		return false
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok {
		return false
	}
	at := -1
	for i, a := range call.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return false
		}
		if ast.Node(a) == fn {
			at = i
		}
	}
	if at < 0 {
		return false
	}
	var decl ast.Node
	switch c := call.Callee.(type) {
	case *ast.Identifier:
		if cs, _ := o.b.Resolve(c); cs != nil && len(cs.Declarations) > 0 {
			decl = cs.Declarations[len(cs.Declarations)-1].Node
		}
	case *ast.MemberExpression:
		decl = o.c.MemberDecl(c)
	}
	impl, ok := decl.(*ast.FunctionDeclaration)
	if !ok || len(impl.Overloads) == 0 {
		return false
	}
	if at >= len(impl.Params) {
		last := len(impl.Params) - 1
		if last < 0 || !impl.Params[last].Rest {
			return false
		}
		at = last
	}
	pt := impl.Params[at].Type
	if pt != nil && pt.IsFuncType && !impl.Params[at].Rest {
		// A listener the implementation takes as `(...args: any[]) => void`
		// is called with boxes (`socket.on('data', (d) => …)`).
		if n := len(pt.FuncParams); n > 0 {
			k := pidx
			if pt.FuncHasRest && k >= n-1 {
				return pt.FuncParams[n-1].Name == "any[]"
			}
			return k < n && pt.FuncParams[k].Name == "any"
		}
		return false
	}
	if pt == nil || !(pt.Name == "any" || impl.Params[at].Rest && pt.Name == "any[]") {
		return false
	}
	// The implementation builds plain records (`dns`'s NaptrRecord): an
	// interface- or literal-typed parameter receives a box.
	if recordShaped(o.c.NonNullable(o.c.TypeOf(id))) {
		return true
	}
	if pt := o.c.NonNullable(o.c.TypeOf(id)); !o.c.Unanswered(pt) && pt.Flags&checker.Object != 0 && pt.Kind == checker.Instance {
		return false
	}
	return true
}

// classMethodDecl is the method declaration class className runs for name.
func (e *Emitter) classMethodDecl(className, name string) *ast.FunctionDeclaration {
	if info, ok := e.classes[className]; ok {
		return info.Methods[name]
	}
	return nil
}

// anyArgument reports a call passing an argument typed `any`.
func (o frontOracle) anyArgument(call *ast.CallExpression) bool {
	for _, a := range call.Args {
		if t := o.c.TypeOf(a); !o.c.Unanswered(t) && t.Flags&checker.Any != 0 {
			return true
		}
	}
	return false
}

// objectResult reports a call the checker types as an array or object (an
// overload's): codegen holds an `any` implementation's result boxed, and
// only a primitive one is narrowed back (checkerPrimitive).
func (o frontOracle) objectResult(call *ast.CallExpression) bool {
	t := o.c.TypeOf(call)
	if o.c.Unanswered(t) || t.Flags&checker.Object == 0 {
		return false
	}
	// A byte buffer the runtime hands back as one (`cipher.update`'s
	// Buffer) keeps its layout.
	r, ok := o.r.of(t)
	return !ok || !(r.IsTypedArray || r.IsBuffer || r.IsArrayBuffer)
}

// recordShaped reports an interface or object-literal type, or an
// array of one: what a library implementation hands a callback as a box.
func recordShaped(t *checker.Type) bool {
	if t == nil || t.Flags&checker.Object == 0 {
		return false
	}
	switch t.Kind {
	case checker.Array:
		return recordShaped(t.Elem)
	case checker.Interface:
		return true
	case checker.Anonymous:
		return len(t.Calls) == 0 && len(t.Constructs) == 0
	}
	return false
}

// holdsThenableInstance reports an array literal with an element that is an
// instance of a program class declaring `then`.
func (o frontOracle) holdsThenableInstance(lit *ast.ArrayLiteral) bool {
	for _, el := range lit.Elements {
		t := o.c.TypeOf(el)
		if o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance || t.Symbol == nil {
			continue
		}
		for _, d := range t.Symbol.Declarations {
			if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
				for _, m := range cd.Methods {
					if m.Name == "then" {
						return true
					}
				}
			}
		}
	}
	return false
}

// superExecutorParam is the index (0 resolve, 1 reject) of the executor
// parameter id names, where the executor is the argument of `super(…)` in a
// class extending Promise; -1 otherwise.
func (o frontOracle) superExecutorParam(id *ast.Identifier) int {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return -1
	}
	fn := sym.Declarations[0].Node
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return -1
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok || len(call.Args) == 0 || ast.Node(call.Args[0]) != fn {
		return -1
	}
	if _, isSuper := call.Callee.(*ast.SuperExpression); !isSuper {
		return -1
	}
	found := false
	for n := ast.Node(call); n != nil; n = o.c.ParentOf(n) {
		if cd, isCD := n.(*ast.ClassDeclaration); isCD {
			if cd.BaseClass != "Promise" {
				return -1
			}
			found = true
			break
		}
	}
	if !found {
		return -1
	}
	for i, p := range params {
		if i < 2 && p.Name == id.Name {
			return i
		}
	}
	return -1
}

// checkerMembers is a union's members, or the type itself.
func checkerMembers(t *checker.Type) []*checker.Type {
	if t != nil && t.Flags&checker.Union != 0 {
		return t.Types
	}
	return []*checker.Type{t}
}

// extendsPromise reports an instance of a program class extending Promise.
func (o frontOracle) extendsPromise(t *checker.Type) bool {
	if t == nil || o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance || t.Symbol == nil {
		return false
	}
	for _, d := range t.Symbol.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			return cd.BaseClass == "Promise" || o.extendsPromise(t.Base)
		}
	}
	return false
}

// voidOverValue reports a call an overload types `void` whose
// implementation returns a value (`dns.lookup`'s request object): codegen
// holds what the implementation returns, boxed.
func (o frontOracle) voidOverValue(call *ast.CallExpression, impl *ast.FunctionDeclaration) bool {
	switch impl.ReturnType.Name {
	case "", "void", "undefined", "never":
		return false
	}
	t := o.c.TypeOf(call)
	return !o.c.Unanswered(t) && t.Flags&checker.Void != 0
}

// nullishCompared reports an identifier compared (`===`, `!==`, `==`, `!=`)
// against `null` or `undefined`.
func (o frontOracle) nullishCompared(id *ast.Identifier) bool {
	be, ok := o.c.ParentOf(id).(*ast.BinaryExpression)
	if !ok {
		return false
	}
	switch be.Op {
	case "===", "!==", "==", "!=":
	default:
		return false
	}
	other := be.Right
	if ast.Node(be.Right) == ast.Node(id) {
		other = be.Left
	}
	switch x := other.(type) {
	case *ast.NullLiteral:
		return true
	case *ast.Identifier:
		return x.Name == "undefined"
	}
	return false
}

// declaredNullable is the representation of the annotated type of the
// local id names, when that type admits null or undefined.
func (o frontOracle) declaredNullable(id *ast.Identifier) (Type, bool) {
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return Type{}, false
	}
	v, ok := singleVarDecl(sym)
	if !ok || v.TypeAnnot == nil {
		return Type{}, false
	}
	t, ok := o.AnnotationType(v.TypeAnnot)
	if !ok || !t.Nullable {
		return Type{}, false
	}
	return t, true
}

// awaitedOperand is the operand of the `await` e is, directly or as the
// initializer of an unannotated variable e names; nil otherwise.
func (o frontOracle) awaitedOperand(e ast.Expression) ast.Expression {
	switch x := e.(type) {
	case *ast.AwaitExpression:
		return x.Argument
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil {
			return nil
		}
		if v, ok := singleVarDecl(sym); ok && v.TypeAnnot == nil {
			if aw, isAw := v.Init.(*ast.AwaitExpression); isAw {
				return aw.Argument
			}
		}
	}
	return nil
}

// stringMatch is the type of `str.match(re)` (emitStringMatch), of its
// non-null assertion, and of a const binding of either.
func (o frontOracle) stringMatch(e ast.Expression) (Type, bool) {
	switch x := e.(type) {
	case *ast.NonNullExpression:
		if t, ok := o.stringMatch(x.Arg); ok {
			return t.withoutNullable(), true
		}
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil {
			return Type{}, false
		}
		v, isV := singleVarDecl(sym)
		if !isV || v.Kind != "const" || v.TypeAnnot != nil || v.Init == nil {
			return Type{}, false
		}
		t, ok := o.stringMatch(v.Init)
		if ok && t.Nullable {
			if ct := o.c.TypeOf(x); !o.c.Unanswered(ct) && ct.Flags&checker.Union == 0 {
				t = t.withoutNullable() // narrowed present
			}
		}
		return t, ok
	case *ast.CallExpression:
		if len(x.Args) != 1 {
			return Type{}, false
		}
		m, isM := x.Callee.(*ast.MemberExpression)
		if !isM || m.Property != "match" || m.Optional || x.Optional {
			return Type{}, false
		}
		if rt := o.c.NonNullable(o.c.TypeOf(m.Object)); !o.c.Unanswered(rt) && rt.Flags&checker.StringLike != 0 && rt.Flags&checker.Union == 0 {
			t := execArrayType()
			t.ExecMaybePlain = !matchIsExec(x.Args[0])
			return t, true
		}
	}
	return Type{}, false
}

// widenedUse reports e as a dynamic entry's widened parameter, or a
// member read, index or call rooted at one.
func (o frontOracle) widenedUse(e ast.Expression) bool {
	return widenedRooted(e, o.r.widenedParam)
}

// chainCopyType answers c, emission's copy of the optional chain link orig:
// orig's own answer while c still sits in an optional chain, else orig's
// non-optional type (the undefined its chain adds gone).
func (o frontOracle) chainCopyType(c, orig ast.Expression) (Type, bool) {
	if copyInOptionalChain(c) {
		return o.ExprType(orig)
	}
	return o.exprTypeBase(orig)
}

// copyInOptionalChain reports a link with a `?.` below it.
func copyInOptionalChain(e ast.Expression) bool {
	for {
		obj, optional, ok := chainChild(e)
		if !ok {
			return false
		}
		if optional {
			return true
		}
		e = obj
	}
}

// mayBeGenericInstance reports a receiver the checker types as a generic
// class's instance, or leaves unanswered: only such a receiver's method can
// be erased.
func (o frontOracle) mayBeGenericInstance(e ast.Expression) bool {
	t := o.c.TypeOf(e)
	return o.c.Unanswered(t) || t.Flags&checker.Object != 0 && t.Kind == checker.Instance && len(t.TypeArgs) > 0
}
