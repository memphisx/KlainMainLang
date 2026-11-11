package llvm

import (
	"KlainMainLang/checker"
	"fmt"
	"math"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// emit_classref_rows.go — the constructor-reference table (TDD-00238
// Stage 3). Every constructor a reference can name (a class boxed as a
// value, a built-in Error kind, a builtin constructor) registers one row,
// keyed by its reference's payload pointer, holding its own routines; the
// dispatchers the runtime calls (`__kml_dyn_instanceof`, `__kml_dyn_construct`,
// `__kml_classref_get`, …) find the row instead of comparing the payload
// against every class of the program.

// ctorRowTy is a constructor-reference row:
//
//	{ i64 key, ptr shown, ptr nstatic, ptr skey, ptr sget, ptr instanceof,
//	  i64 kind, ptr construct, ptr get, ptr set }
//
// shown is util.inspect's `[class B extends A]` (null for a built-in);
// nstatic(p), skey(p, i) and sget(p, i) walk its present static fields;
// instanceof(v) is `v instanceof <it>`; kind is lib/native.d.ts ctorKind (1
// a constructor, 2 an Error constructor); construct(argv, argc) is `new` over
// boxed arguments; get(key) and set(key, v) read and write a static member.
// A null routine falls back to the dispatcher's default.
const ctorRowTy = "{ i64, " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + ", i64, " + irPtr + ", " + irPtr + ", " + irPtr + " }"

// ctorRowBytes is ctorRowTy's size, for copying a row (unitregsrc).
const ctorRowBytes = 80

// nbFuncRefKind is the low bits emitNbTagPtr ors into a constructor
// reference's pointer.
const nbFuncRefKind = 3

// ctorRow collects one reference's routines before it is registered.
type ctorRow struct {
	payload                          string // the reference's payload constant
	shown, nstatic, skey, sget, inst string
	kind                             int
	construct, get, set              string
}

// ctorRowFor is the row of the reference with payload p, created on first
// use.
func (e *Emitter) ctorRowFor(p string) *ctorRow {
	if e.ctorRows == nil {
		e.ctorRows = map[string]*ctorRow{}
	}
	r := e.ctorRows[p]
	if r == nil {
		r = &ctorRow{payload: p, kind: 1}
		e.ctorRows[p] = r
		e.ctorRowOrder = append(e.ctorRowOrder, p)
	}
	return r
}

// ctorThunk names target name's routine what, a symbol stable across
// programs for a library class.
func ctorThunk(what, name string) string {
	return "@__kml_cr_" + what + "." + llvmSafeSymbol(name)
}

// emitClassRefFinalize generates each reference's routines, registers the
// rows, and defines the dispatchers.
func (e *Emitter) emitClassRefFinalize() {
	e.emitConstructorOfOnce() // first: it makes every class a value
	e.emitClassRefTable()
	if e.usedDynInstanceof || e.usedCtorKind {
		e.emitCtorKindRows()
	}
	if e.usedClassRefProto {
		e.emitClassRefProto()
	}
	if e.usedDynInstanceof {
		e.emitDynInstanceofRows()
	}
	e.emitDynConstructOnce()
	// Always defined: a function the finalizer emits later may read or write a
	// static member through `any`.
	e.emitClassRefGetRows()
	e.emitHostRefGetRows()
	e.registerFnStatics()
	e.emitClassRefSetRows()
	e.registerCtorRows()
	e.emitClassRefDispatchers()
}

// registerCtorRows adds every collected row to the table.
func (e *Emitter) registerCtorRows() {
	field := func(fn string) string {
		if fn == "" {
			return irPtr + " null"
		}
		return irPtr + " " + fn
	}
	for _, p := range e.ctorRowOrder {
		r := e.ctorRows[p]
		shown := irPtr + " null"
		if r.shown != "" {
			shown = irPtr + " " + r.shown
		}
		e.addUnitRowLit(unitKindCtor, ctorRowTy, fmt.Sprintf("{ i64 ptrtoint ("+irPtr+" %s to i64), %s, %s, %s, %s, %s, i64 %d, %s, %s, %s }",
			r.payload, shown, field(r.nstatic), field(r.skey), field(r.sget), field(r.inst), r.kind, field(r.construct), field(r.get), field(r.set)))
	}
	e.ctorRows, e.ctorRowOrder = nil, nil
}

// emitClassRefDispatchers: the dispatchers over the table are hostbox.c's
// (__kml_classref_*, __kml_dyn_instanceof, __kml_ctor_kind); they read the
// rows through the NaN-box helpers.
func (e *Emitter) emitClassRefDispatchers() {
	e.ensureNanBox()
}

// emitConstructorOfOnce registers the rows `i64 __kml_constructor_of(ptr o)`
// reads once the program reads a constructor;
// unitreg's finalize calls it again for a read generated late.
func (e *Emitter) emitConstructorOfOnce() {
	if !e.usedConstructorOf || e.emittedConstructorOf {
		return
	}
	e.emittedConstructorOf = true
	e.emitConstructorOfRows()
	// __kml_constructor_of reads them (hostbox.c).
}

// emitConstructorOfRows registers, per class, the constructor reference
// `obj.constructor` answers for an instance carrying its type id.
func (e *Emitter) emitConstructorOfRows() {
	var classes []string
	for c, info := range e.classes {
		if info.Ty.IsObject() && info.TagID != 0 {
			classes = append(classes, c)
		}
	}
	sort.Strings(classes)
	for _, c := range classes {
		e.addUnitRow(unitKindCtorOf, "{ i64, "+irPtr+" }", e.classes[c].TagID&kmlHdrIDMask, irPtr+" "+e.classRefPtr(c))
	}
}

// classRefClasses lists the classes boxed as values, sorted; with known,
// only those declared in the program.
func (e *Emitter) classRefClasses(known bool) []string {
	var classes []string
	for c := range e.classRefs {
		if info, ok := e.classes[c]; ok && isSyntheticObjLitClass(info.Ty) && !e.objLitInstances[c] {
			continue // an accessor literal's class no instance was made of
		}
		if _, ok := e.classes[c]; ok || !known {
			classes = append(classes, c)
		}
	}
	sort.Strings(classes)
	return classes
}

// emitCtorKindRows records each target's ctorKind: 2 for an Error
// constructor (a built-in kind or a class extending Error).
func (e *Emitter) emitCtorKindRows() {
	ptrs, names := e.ctorRefTargets()
	for i := range ptrs {
		if isErrorKindName(names[i]) || e.classes[names[i]].IsErrorSubclass {
			e.ctorRowFor(ptrs[i]).kind = 2
		}
	}
}

// emitDynInstanceofRows generates each target's `i1 (i64 v)`: the static
// `v instanceof <its class>`.
func (e *Emitter) emitDynInstanceofRows() {
	ptrs, names := e.ctorRefTargets()
	for i := range ptrs {
		restore := e.beginDetachedFunc()
		vSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", vSlot))
		e.emitInstr(fmt.Sprintf("store i64 %%v, "+irPtr+" %s, align 8", vSlot))
		e.define("__kml_iv", Symbol{Ptr: vSlot, Ty: TypeAny})
		test := ast.NewBinaryExpression("instanceof", ast.NewIdentifier("__kml_iv", ast.Pos{}), ast.NewIdentifier(names[i], ast.Pos{}), ast.Pos{})
		// OrdinaryHasInstance: the dispatch calls a class's own
		// @@hasInstance before reaching this (emitDynInstanceOf), and
		// Function.prototype[@@hasInstance] reaches it from inside one.
		e.ordinaryInstanceof = true
		r, err := e.emitInstanceOf(test)
		e.ordinaryInstanceof = false
		if err == nil && !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i1 %s", r.Ref))
		}
		if !e.blockDone {
			e.emitTerminator("ret i1 false")
		}
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("inst", names[i])
		e.writeFunc(fmt.Sprintf("internal i1 %s(i64 %%v)", fn), body)
		e.ctorRowFor(ptrs[i]).inst = fn
	}
}

// emitClassRefTable records, per class boxed as a value, what the runtime's
// util.inspect reads (dynjson.c): its `[class B extends A]` rendering, and
// routines walking its present static fields — its own, then each
// inherited one it has assigned its own (`B.n = 5`):
//   - `i64 nstatic(ptr)`: how many;
//   - `ptr skey(ptr, i64 i)` and `i64 sget(ptr, i64 i)`: the i-th one's
//     name and current value, boxed.
func (e *Emitter) emitClassRefTable() {
	classes := e.classRefClasses(false)
	type entry struct {
		class, field string
		inherited    bool
	}
	entries := func(c string) []entry {
		var out []entry
		info := e.classes[c]
		for _, f := range info.OwnStaticFieldOrder {
			if !strings.HasPrefix(f, "#") {
				out = append(out, entry{c, f, false})
			}
		}
		var inh []string
		for f := range info.StaticFieldTypes {
			if _, own := info.OwnStaticFieldTypes[f]; !own && !strings.HasPrefix(f, "#") {
				inh = append(inh, f)
			}
		}
		sort.Strings(inh)
		for _, f := range inh {
			out = append(out, entry{c, f, true})
		}
		return out
	}
	// present is 1 when entry x is an own property now.
	present := func(x entry) string {
		if !x.inherited {
			return "1"
		}
		own := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", own, e.staticOwnPtr(x.class, x.field)))
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, own))
		return z
	}
	// thunk generates one class's routine what with signature sig.
	thunk := func(what, c, ret, params string, gen func()) string {
		restore := e.beginDetachedFunc()
		gen()
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk(what, c)
		e.writeFunc(fmt.Sprintf("internal %s %s(%s)", ret, fn, params), body)
		return fn
	}
	nth := func(es []entry, ret func(x entry) string, retTy string) {
		k := "0" // the index of the next present entry
		for _, x := range es {
			p := present(x)
			is, hit, next := e.freshReg(), e.freshLabel("classref.at"), e.freshLabel("classref.past")
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %%i, %s", is, k))
			both := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", both, p))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", sel, is, both))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sel, hit, next))
			e.emitLabel(hit)
			e.emitTerminator(fmt.Sprintf("ret %s %s", retTy, ret(x)))
			e.emitLabel(next)
			nk := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", nk, k, p))
			k = nk
		}
	}
	for _, c := range classes {
		es := entries(c)
		row := e.ctorRowFor(e.classRefs[c])
		row.shown = e.internString(classShown(e, c))
		row.nstatic = thunk("nstatic", c, "i64", irPtr+" %p", func() {
			n := "0"
			for _, x := range es {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", r, n, present(x)))
				n = r
			}
			e.emitTerminator("ret i64 " + n)
		})
		row.skey = thunk("skey", c, irPtr, irPtr+" %p, i64 %i", func() {
			nth(es, func(x entry) string { return e.internString(x.field) }, irPtr)
			e.emitTerminator("ret " + irPtr + " null")
		})
		// sget loads the field and boxes it with the ordinary emitBoxValue.
		row.sget = thunk("sget", c, "i64", irPtr+" %p, i64 %i", func() {
			nth(es, func(x entry) string {
				ty := e.classes[x.class].StaticFieldTypes[x.field]
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, storageIR(ty), e.staticSlotPtr(x.class, x.field)))
				if boxed, err := e.emitBoxValue(Value{Ref: r, Ty: ty}); err == nil {
					return boxed.Ref
				}
				return fmt.Sprintf("%d", nbUndefined)
			}, "i64")
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		})
	}

	// keys: Object.keys of a class, its present static field names.
	e.ensureStrHeaderRuntime() // hostbox.c heads an added property's key
	restore := e.beginDetachedFunc()
	n, bytes, arr, slot := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classref_nstatic("+irPtr+" %%p)", n))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, n))
	e.heapMallocInto(arr, allocElems, bytes)
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", slot))
	loopL, bodyL, outL := e.freshLabel("keys.loop"), e.freshLabel("keys.body"), e.freshLabel("keys.out")
	e.emitTerminator("br label %" + loopL)
	e.emitLabel(loopL)
	i, done := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", i, slot))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", done, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, outL, bodyL))
	e.emitLabel(bodyL)
	k, kp, next := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classref_skey("+irPtr+" %%p, i64 %s)", k, i))
	e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %s", kp, arr, i))
	e.heapStore(irPtr, k, kp, 8)
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", next, slot))
	e.emitTerminator("br label %" + loopL)
	e.emitLabel(outL)
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } undef, "+irPtr+" %s, 0", r0, arr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } %s, i64 %s, 1", r1, r0, n))
	e.emitTerminator("ret { " + irPtr + ", i64 } " + r1)
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("{ "+irPtr+", i64 } @__kml_classref_keys("+irPtr+" %p)", body)
}

// emitDynConstructDispatch generates each constructible reference's `i64
// (ptr argv, i64 argc)`: `new <that class>(…)` over the boxed arguments (an
// absent one undefined), boxed. __kml_dyn_construct(c, argv, argc, msg)
// finds it, and throws the TypeError msg for anything else.
func (e *Emitter) emitDynConstructDispatch() {
	// An abstract class too: `abstract` is TypeScript's, and `new` through
	// any runs its constructor as JavaScript does.
	classes := e.classRefClasses(true)
	for _, c := range classes {
		if b := e.builtinRootBase(c); b != "" && e.ctorParamList(c) == nil {
			// No constructor of its own up to the builtin it extends: its
			// arguments reach the builtin's as passed, their count with them.
			restore := e.beginDetachedFunc()
			e.emitConstructArgcArms(hostCtorArity(b), func(args []ast.Expression) (Value, error) {
				e.inCtorArm, e.dynConstructing = true, true
				defer func() { e.inCtorArm, e.dynConstructing = false, false }()
				return e.emitNewExpression(ast.NewNewExpression(c, args, ast.Pos{}))
			}, c)
			body := e.allocas.String() + e.body.String()
			restore()
			fn := ctorThunk("new", c)
			e.writeFunc(fmt.Sprintf("internal i64 %s("+irPtr+" %%argv, i64 %%argc)", fn), body)
			e.ctorRowFor(e.classRefs[c]).construct = fn
			continue
		}
		restore := e.beginDetachedFunc()
		var args []ast.Expression
		// A rest parameter (the constructor's own, or the one reading
		// `arguments` gains) collects the arguments past the regular ones.
		regular, rest := e.ctorArity(c), false
		if ps := e.ctorParamList(c); len(ps) > 0 && ps[len(ps)-1].Rest {
			regular, rest = regular-1, true
		}
		valueRest := false
		if fn := e.fnBaseRoot(c); fn != "" && e.valueBases[fn] && e.ctorParamList(c) == nil {
			// No constructor down to a base a value holds: every argument
			// reaches it.
			regular, rest, valueRest = 0, true, true
		}
		for i := 0; i < regular; i++ {
			name := fmt.Sprintf("__kml_ca%d", i)
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			has := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %d, %%argc", has, i))
			at := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds i64, "+irPtr+" %%argv, i64 %d", at, i))
			// argv is read only when the slot exists.
			inL, outL, joinL := e.freshLabel("dynnew.arg"), e.freshLabel("dynnew.noarg"), e.freshLabel("dynnew.argd")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, inL, outL))
			e.emitLabel(inL)
			ld := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", ld, at))
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", ld, slot))
			e.emitTerminator("br label %" + joinL)
			e.emitLabel(outL)
			e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, slot))
			e.emitTerminator("br label %" + joinL)
			e.emitLabel(joinL)
			e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
			args = append(args, ast.NewIdentifier(name, ast.Pos{}))
		}
		if rest {
			restTy := ArrayOf(TypeAny)
			if ps := e.ctorParamList(c); !valueRest && ps[len(ps)-1].Type != nil {
				if rt := e.annotationType(ps[len(ps)-1].Type); rt.IsArray() && rt.ElemType != nil {
					restTy = rt
				}
			}
			arr := e.emitShapeRestArray(regular, *restTy.ElemType)
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", arr, slot))
			e.define("__kml_ca_rest", Symbol{Ptr: slot, Ty: restTy})
			args = append(args, ast.NewSpreadElement(ast.NewIdentifier("__kml_ca_rest", ast.Pos{}), ast.Pos{}))
		}
		// A constructor reading `arguments` counts what the dynamic `new`
		// passed, up to its declared parameters, not the padded slots.
		capped := e.freshReg()
		lt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %%argc, %d", lt, regular))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %%argc, i64 %d", capped, lt, regular))
		savedOverride := e.argcOverride
		e.argcOverride = capped
		e.inCtorArm = true
		e.dynConstructing = true
		v, err := e.emitNewExpression(ast.NewNewExpression(c, args, ast.Pos{}))
		e.dynConstructing = false
		e.inCtorArm = false
		e.argcOverride = savedOverride
		if err != nil {
			e.thunkErrs = append(e.thunkErrs, c+": "+err.Error())
		}
		if err == nil && !e.blockDone {
			if b, berr := e.emitBoxValue(v); berr == nil {
				e.emitTerminator(fmt.Sprintf("ret i64 %s", b.Ref))
			}
		}
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		}
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("new", c)
		e.writeFunc(fmt.Sprintf("internal i64 %s("+irPtr+" %%argv, i64 %%argc)", fn), body)
		e.ctorRowFor(e.classRefs[c]).construct = fn
	}
	var hosts []string
	for h := range e.hostRefs {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		restore := e.beginDetachedFunc()
		e.emitHostConstructArms(h)
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("newhost", h)
		e.writeFunc(fmt.Sprintf("internal i64 %s("+irPtr+" %%argv, i64 %%argc)", fn), body)
		e.ctorRowFor(e.internString(h)).construct = fn
	}
	// Anything else throws the TypeError msg.
	restore := e.beginDetachedFunc()
	e.emitThrowTypeErrorValue("%msg")
	if !e.blockDone {
		e.emitTerminator("unreachable")
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i64 @__kml_dyn_construct_fail("+irPtr+" %msg)", body)
	e.ensureFnMeta() // fnmeta.c constructs a plain function
	// __kml_dyn_construct (hostbox.c) calls the row's construct, else
	// fnmeta.c's, else the fail path above.
}

// emitClassRefGetRows generates, per class boxed as a value, `i64 (ptr
// key)`: a static member read through its reference (`K.make`, `K.count`,
// `K.length`), boxed; undefined for anything else.
func (e *Emitter) emitClassRefGetRows() {
	e.ensureNanBox()
	for _, c := range e.classRefClasses(true) {
		// The statics of the builtin it extends (`BYTES_PER_ELEMENT`), last.
		inherited := ""
		if b := e.builtinRootBase(c); b != "" {
			inherited = e.hostStaticGetter(b)
		}
		restore := e.beginDetachedFunc()
		keys := []string{"length"}
		// The class's prototype object, through its builder: a library
		// class's row then compiles the same in every program.
		if _, ok := e.classes[c]; ok {
			keys = append(keys, "prototype")
		}
		statics := e.classStaticKeys(c)
		for _, k := range statics {
			if k == "length" {
				keys = keys[1:] // an own static `length` replaces the constructor's
				break
			}
		}
		keys = append(keys, statics...)
		for _, k := range keys {
			kHit, kNext := e.freshLabel("crget.key"), e.freshLabel("crget.nokey")
			cmp, isK := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp("+irPtr+" %%key, "+irPtr+" %s)", cmp, e.internString(k)))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isK, cmp))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isK, kHit, kNext))
			e.emitLabel(kHit)
			var boxed string
			if k == "length" && !seenStatic(e.classStaticKeys(c), "length") {
				boxed = e.emitNbEncodeDouble(fmt.Sprintf("%d.0", e.ctorLength(c)))
			} else if k == "prototype" {
				// The class's prototype object (`K.prototype` on a class value).
				if b, berr := e.emitBoxValue(e.emitClassProto(c)); berr == nil {
					boxed = b.Ref
				}
			} else if sigs := e.classes[c].StaticMethodSigs; !hasSig(sigs, k) && !hasSig(sigs, accessorMethodName("get", k)) && hasSig(sigs, accessorMethodName("set", k)) {
				// A setter-only accessor reads as undefined.
			} else if v, err := e.emitStaticFieldRead(e.classes[c], c, k, ast.Pos{}); err == nil && !e.blockDone {
				// The class's own static member, read directly (a member
				// read of `length` or `name` on the class would come back
				// through this row).
				if b, berr := e.emitBoxValue(v); berr == nil {
					boxed = b.Ref
				}
			}
			if !e.blockDone {
				if boxed == "" {
					boxed = fmt.Sprintf("%d", nbUndefined)
				}
				e.emitTerminator(fmt.Sprintf("ret i64 %s", boxed))
			}
			e.emitLabel(kNext)
		}
		if inherited != "" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 %s("+irPtr+" %%key)", r, inherited))
			e.emitTerminator(fmt.Sprintf("ret i64 %s", r))
		} else {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		}
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("get", c)
		e.writeFunc(fmt.Sprintf("internal i64 %s("+irPtr+" %%key)", fn), body)
		e.ctorRowFor(e.classRefs[c]).get = fn
	}
}

// emitHostRefGetRows generates, per builtin constructor boxed as a value,
// `i64 (ptr key)`: a static member read through its reference (`A.isArray`
// with `const A: any = Array`), the static its declaration lists, boxed;
// undefined for anything else.
func (e *Emitter) emitHostRefGetRows() {
	var hosts []string
	for h := range e.hostRefs {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		if fn := e.hostStaticGetter(h); fn != "" {
			e.ctorRowFor(e.internString(h)).get = fn
		}
	}
}

// hostStaticGetter defines (once) builtin constructor h's `i64 (ptr key)`
// static read, or is "" when its declaration lists no statics.
func (e *Emitter) hostStaticGetter(h string) string {
	fn := ctorThunk("hostget", h)
	if e.hostGetters == nil {
		e.hostGetters = map[string]string{}
	}
	if got, ok := e.hostGetters[h]; ok {
		return got
	}
	e.hostGetters[h] = ""
	{
		keys := e.hostStaticKeys(h)
		if e.hostProtoReadable(h) {
			keys = append(keys, hostStatic{name: "prototype"})
		}
		if len(keys) == 0 {
			return ""
		}
		e.ensureNanBox()
		restore := e.beginDetachedFunc()
		e.inHostRow = h
		for _, ks := range keys {
			k := ks.name
			kHit, kNext := e.freshLabel("hrget.key"), e.freshLabel("hrget.nokey")
			cmp, isK := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp("+irPtr+" %%key, "+irPtr+" %s)", cmp, e.internString(k)))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isK, cmp))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isK, kHit, kNext))
			e.emitLabel(kHit)
			boxed := fmt.Sprintf("%d", nbUndefined)
			if ks.name == "prototype" && !ks.method {
				boxed = e.hostProtoValue(h).Ref
			} else if ks.fn != nil {
				if v, err := e.emitBuiltinValue(ks.fn); err == nil && !e.blockDone {
					if b, berr := e.emitBoxValue(v); berr == nil {
						boxed = b.Ref
					}
				}
			} else if lit, isConst := builtinNumberConst(h, k); isConst && !ks.method {
				if b, berr := e.emitBoxValue(Value{Ref: lit, Ty: TypeF64}); berr == nil {
					boxed = b.Ref
				}
			} else if x := hostStaticValueExpr(e, h, ks); x == nil {
				// No value form: undefined, as an unimplemented static reads.
			} else if v, err := e.emitExpr(x); err == nil && !e.blockDone {
				if b, berr := e.emitBoxValue(v); berr == nil {
					boxed = b.Ref
				}
			}
			if !e.blockDone {
				e.emitTerminator(fmt.Sprintf("ret i64 %s", boxed))
			}
			e.emitLabel(kNext)
		}
		// `length` is the builtin's, 1 for every constructor boxed here.
		lc, isLen := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp("+irPtr+" %%key, "+irPtr+" %s)", lc, e.internString("length")))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isLen, lc))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %d", r, isLen, int64(math.Float64bits(1))+nbDoubleOffset, nbUndefined))
		e.emitTerminator(fmt.Sprintf("ret i64 %s", r))
		e.inHostRow = ""
		body := e.allocas.String() + e.body.String()
		restore()
		e.writeHelper("internal", "i64", fn, irPtr+" %key", body)
	}
	e.hostGetters[h] = fn
	return fn
}

// registerFnStatics adds, per builtin constructor used as a function value
// (`Number`), the row __kml_fn_static_get (fnmeta.c) reads its statics
// through, keyed by the value's prefix word.
func (e *Emitter) registerFnStatics() {
	var hdrs []string
	for h := range e.fnStaticHosts {
		hdrs = append(hdrs, h)
	}
	sort.Strings(hdrs)
	for _, h := range hdrs {
		if fn := e.hostStaticGetter(e.fnStaticHosts[h]); fn != "" {
			e.addUnitRowLit(unitKindFnStatic, "{ i64, "+irPtr+" }", fmt.Sprintf("{ i64 %d, "+irPtr+" %s }", e.fnWord(e.staticFnOf[h]), fn))
		}
	}
}

// hostProtoReadable reports whether builtin constructor h's prototype
// object reads directly (`Map.prototype`), so its reference's row reads it
// too (`const M: any = Map; M.prototype`).
func (e *Emitter) hostProtoReadable(h string) bool {
	if e.isShadowedByLocal(h) {
		return false
	}
	if _, user := e.classes[h]; user {
		return false
	}
	return isErrorKindName(h) || hostProtoClassKnown(h) && len(e.hostMembers(h)) > 0 || typedKindIndex(h) >= 0 || h == "Function" || h == "Object"
}

// hostProtoValue is builtin constructor h's prototype object, as
// hostProtoReadable allows it.
func (e *Emitter) hostProtoValue(h string) Value {
	switch {
	case h == "Function":
		return e.emitFnProtoBox("")
	case h == "Object":
		e.ensureAnyToPrimitive()
		e.ensureNanBox()
		e.ensureErrorNames()
		e.ensureStrHeaderRuntime()
		e.ensureObjProtoFill()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_object_prototype()", r))
		return e.emitDynObjBox(r)
	case typedKindIndex(h) >= 0:
		return e.emitTypedKindProto(h)
	}
	return e.emitBuiltinProto(h)
}

// hostStatic is one static member of a builtin constructor: its name,
// whether it is a method, and a method's intrinsic function value.
type hostStatic struct {
	name   string
	method bool
	fn     *builtinFn
}

// hostStaticValueExpr is the expression a host row reads static ks of h
// through: an intrinsic's function value, or a constant's read; nil for a
// method with no function value.
func hostStaticValueExpr(e *Emitter, h string, ks hostStatic) ast.Expression {
	if !ks.method {
		return ast.NewMemberExpression(ast.NewIdentifier(h, ast.Pos{}), ks.name, ast.Pos{})
	}
	return nil
}

// hostStaticKeys is the static members builtin constructor h's declaration
// lists (`declare var Array: ArrayConstructor`), but its prototype.
func (e *Emitter) hostStaticKeys(h string) []hostStatic {
	c := e.front()
	if c == nil {
		return nil
	}
	sym := lookupFrom(c.Binding().Module, h)
	if sym == nil {
		return nil
	}
	var keys []hostStatic
	for _, d := range sym.Declarations {
		v, ok := d.Node.(*ast.VarDeclaration)
		if !ok || v.TypeAnnot == nil {
			continue
		}
		t := c.TypeFromNode(v.TypeAnnot, c.Binding().Module)
		if t == nil || c.Unanswered(t) {
			continue
		}
		for _, p := range t.Props {
			if p.Name == "prototype" || strings.HasPrefix(p.Name, "__") || p.Type == nil {
				continue
			}
			ks := hostStatic{name: p.Name, method: p.Type.Kind == checker.Function || len(p.Type.Overloads) > 0}
			decl := p.Decl
			if decl == nil && len(p.Decls) > 0 {
				decl = p.Decls[0] // an overloaded method's: each names the intrinsic
			}
			if ms, ok := decl.(*ast.MethodSignature); ok && ms.Intrinsic != "" && !strings.Contains(ms.Intrinsic, ".prototype.") {
				ks.fn, _ = intrinsicBuiltin(ms.Intrinsic, p.Type)
			}
			keys = append(keys, ks)
		}
	}
	return keys
}

// emitClassRefSetRows generates, per class boxed as a value, `void (ptr
// key, i64 v)`: a static field written through its reference (`K.count =
// 5`), the boxed value converted to the field's type.
func (e *Emitter) emitClassRefSetRows() {
	for _, c := range e.classRefClasses(true) {
		restore := e.beginDetachedFunc()
		vSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", vSlot))
		e.emitInstr(fmt.Sprintf("store i64 %%v, "+irPtr+" %s, align 8", vSlot))
		e.define("__kml_sv", Symbol{Ptr: vSlot, Ty: TypeAny})
		for _, k := range e.classStaticKeys(c) {
			sigs := e.classes[c].StaticMethodSigs
			if _, method := sigs[k]; method {
				continue
			}
			if _, getter := sigs[accessorMethodName("get", k)]; getter {
				if _, setter := sigs[accessorMethodName("set", k)]; !setter {
					continue // a getter-only accessor: nothing to write
				}
			}
			kHit, kNext := e.freshLabel("crset.key"), e.freshLabel("crset.nokey")
			cmp, isK := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp("+irPtr+" %%key, "+irPtr+" %s)", cmp, e.internString(k)))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isK, cmp))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isK, kHit, kNext))
			e.emitLabel(kHit)
			if setter := accessorMethodName("set", k); hasSig(sigs, setter) {
				// A static setter, run with the value (its declared class's).
				owner := c
				if impl := e.classes[c].StaticMethodImplementor[setter]; impl != "" {
					owner = impl
				}
				_, _ = e.emitStaticMethodCall(e.classes[owner], owner, setter, []ast.Expression{ast.NewIdentifier("__kml_sv", ast.Pos{})}, ast.Pos{})
			} else {
				assign := ast.NewAssignmentExpression("=", ast.NewMemberExpression(ast.NewIdentifier(c, ast.Pos{}), k, ast.Pos{}), ast.NewIdentifier("__kml_sv", ast.Pos{}), ast.Pos{})
				_, _ = e.emitExpr(assign)
			}
			if !e.blockDone {
				e.emitTerminator("ret i1 true")
			}
			e.emitLabel(kNext)
		}
		e.emitTerminator("ret i1 false")
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("set", c)
		e.writeFunc(fmt.Sprintf("internal i1 %s("+irPtr+" %%key, i64 %%v)", fn), body)
		e.ctorRowFor(e.classRefs[c]).set = fn
	}
}

func hasSig(sigs map[string]FuncSig, k string) bool { _, ok := sigs[k]; return ok }
