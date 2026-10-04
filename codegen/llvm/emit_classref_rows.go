package llvm

import (
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
const ctorRowTy = "{ i64, ptr, ptr, ptr, ptr, ptr, i64, ptr, ptr, ptr }"

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
	if e.usedDynInstanceof {
		e.emitDynInstanceofRows()
	}
	e.emitDynConstructOnce()
	// Always defined: a function the finalizer emits later may read or write a
	// static member through `any`.
	e.emitClassRefGetRows()
	e.emitClassRefSetRows()
	e.registerCtorRows()
	e.emitClassRefDispatchers()
}

// registerCtorRows adds every collected row to the table.
func (e *Emitter) registerCtorRows() {
	field := func(fn string) string {
		if fn == "" {
			return "ptr null"
		}
		return "ptr " + fn
	}
	for _, p := range e.ctorRowOrder {
		r := e.ctorRows[p]
		shown := "ptr null"
		if r.shown != "" {
			shown = "ptr " + r.shown
		}
		e.addUnitRowLit(unitKindCtor, ctorRowTy, fmt.Sprintf("{ i64 ptrtoint (ptr %s to i64), %s, %s, %s, %s, %s, i64 %d, %s, %s, %s }",
			r.payload, shown, field(r.nstatic), field(r.skey), field(r.sget), field(r.inst), r.kind, field(r.construct), field(r.get), field(r.set)))
	}
	e.ctorRows, e.ctorRowOrder = nil, nil
}

// ctorRefDispatchIR opens a dispatcher taking a boxed reference `i64 %c`:
// branches to `%no` unless it is a constructor reference, else binds
// `%<out>` to field idx of its row (def when it has none).
func ctorRefDispatchIR(idx int, ty, def, out string) string {
	return fmt.Sprintf("  %%tag = call i8 @__kml_nb_tag(i64 %%c)\n  %%isref = icmp eq i8 %%tag, %d\n  br i1 %%isref, label %%ref, label %%no\nref:\n  %%pay = call i64 @__kml_nb_pay(i64 %%c)\n", kmlTagFuncRef) +
		unitFieldIR(unitKindCtor, ctorRowTy, "%pay", idx, ty, def, out)
}

// emitClassRefDispatchers defines the dispatchers over the table.
func (e *Emitter) emitClassRefDispatchers() {
	e.ensureUnitReg()
	e.ensureNanBox()
	e.ensureStrcmp()
	payField := func(idx int, ty, def, out string) string {
		return "  %key = ptrtoint ptr %p to i64\n" + unitFieldIR(unitKindCtor, ctorRowTy, "%key", idx, ty, def, out)
	}
	var b strings.Builder
	// util.inspect's rendering of a class value, and its static fields.
	b.WriteString("\ndefine ptr @__kml_classref_shown(ptr %p) {\nentry:\n" + payField(1, "ptr", "null", "s") + "  ret ptr %s\n}\n")
	b.WriteString("\ndefine i64 @__kml_classref_nstatic(ptr %p) {\nentry:\n" + payField(2, "ptr", "null", "f") +
		"  %none = icmp eq ptr %f, null\n  br i1 %none, label %no, label %call\ncall:\n  %r = call i64 %f(ptr %p)\n  ret i64 %r\nno:\n  ret i64 0\n}\n")
	b.WriteString("\ndefine ptr @__kml_classref_skey(ptr %p, i64 %i) {\nentry:\n" + payField(3, "ptr", "null", "f") +
		"  %none = icmp eq ptr %f, null\n  br i1 %none, label %no, label %call\ncall:\n  %r = call ptr %f(ptr %p, i64 %i)\n  ret ptr %r\nno:\n  ret ptr null\n}\n")
	fmt.Fprintf(&b, "\ndefine i64 @__kml_classref_sget(ptr %%p, i64 %%i) {\nentry:\n%s  %%none = icmp eq ptr %%f, null\n  br i1 %%none, label %%no, label %%call\ncall:\n  %%r = call i64 %%f(ptr %%p, i64 %%i)\n  ret i64 %%r\nno:\n  ret i64 %d\n}\n", payField(4, "ptr", "null", "f"), nbUndefined)
	// A static member through a reference; a built-in constructor (an Error
	// kind) has only its length, 1.
	fmt.Fprintf(&b, "\ndefine i64 @__kml_classref_get(ptr %%p, ptr %%key0) {\nentry:\n%s  %%none = icmp eq ptr %%f, null\n  br i1 %%none, label %%builtin, label %%call\ncall:\n  %%r = call i64 %%f(ptr %%key0)\n  ret i64 %%r\nbuiltin:\n  %%lc = call i32 @strcmp(ptr %%key0, ptr %s)\n  %%islen = icmp eq i32 %%lc, 0\n  %%bl = select i1 %%islen, i64 %d, i64 %d\n  ret i64 %%bl\n}\n",
		payField(8, "ptr", "null", "f"), e.internString("length"), int64(math.Float64bits(1))+nbDoubleOffset, nbUndefined)
	b.WriteString("\ndefine void @__kml_classref_set(ptr %p, ptr %key0, i64 %v) {\nentry:\n" + payField(9, "ptr", "null", "f") +
		"  %none = icmp eq ptr %f, null\n  br i1 %none, label %no, label %call\ncall:\n  call void %f(ptr %key0, i64 %v)\n  ret void\nno:\n  ret void\n}\n")
	if e.usedDynInstanceof {
		b.WriteString("\ndefine i1 @__kml_dyn_instanceof(i64 %v, i64 %c) {\nentry:\n" + ctorRefDispatchIR(5, "ptr", "null", "f") +
			"  %none = icmp eq ptr %f, null\n  br i1 %none, label %no, label %call\ncall:\n  %r = call i1 %f(i64 %v)\n  ret i1 %r\nno:\n  ret i1 false\n}\n")
	}
	if e.usedCtorKind {
		b.WriteString("\ndefine double @__kml_ctor_kind(i64 %c) {\nentry:\n" + ctorRefDispatchIR(6, "i64", "1", "k") +
			"  %d = sitofp i64 %k to double\n  ret double %d\nno:\n  ret double 0.0\n}\n")
	}
	e.functions.WriteString(b.String())
}

// emitConstructorOfOnce registers the rows and defines
// `i64 __kml_constructor_of(ptr o)` once the program reads a constructor;
// unitreg's finalize calls it again for a read generated late.
func (e *Emitter) emitConstructorOfOnce() {
	if !e.usedConstructorOf || e.emittedConstructorOf {
		return
	}
	e.emittedConstructorOf = true
	e.emitConstructorOfRows()
	e.ensureUnitReg()
	fmt.Fprintf(&e.functions, "\ndefine i64 @__kml_constructor_of(ptr %%o) {\nentry:\n  %%h = load i64, ptr %%o, align 8\n  %%id = and i64 %%h, %d\n%s  %%none = icmp eq ptr %%f, null\n  br i1 %%none, label %%no, label %%ref\nref:\n  %%pi = ptrtoint ptr %%f to i64\n  %%boxed = or i64 %%pi, %d\n  ret i64 %%boxed\nno:\n  ret i64 %d\n}\n",
		kmlHdrIDMask, unitFieldIR(unitKindCtorOf, "{ i64, ptr }", "%id", 1, "ptr", "null", "f"), nbFuncRefKind, nbUndefined)
}

// emitConstructorOfRows registers, per class, the constructor reference
// `obj.constructor` answers for an instance carrying its type id.
func (e *Emitter) emitConstructorOfRows() {
	var classes []string
	for c, info := range e.classes {
		if info.Ty.IsObject && info.TagID != 0 {
			classes = append(classes, c)
		}
	}
	sort.Strings(classes)
	for _, c := range classes {
		e.addUnitRow(unitKindCtorOf, "{ i64, ptr }", e.classes[c].TagID&kmlHdrIDMask, "ptr "+e.classRefPtr(c))
	}
}

// classRefClasses lists the classes boxed as values, sorted; with known,
// only those declared in the program.
func (e *Emitter) classRefClasses(known bool) []string {
	var classes []string
	for c := range e.classRefs {
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
		e.emitInstr(fmt.Sprintf("store i64 %%v, ptr %s, align 8", vSlot))
		e.define("__kml_iv", Symbol{Ptr: vSlot, Ty: TypeAny})
		test := ast.NewBinaryExpression("instanceof", ast.NewIdentifier("__kml_iv", ast.Pos{}), ast.NewIdentifier(names[i], ast.Pos{}), ast.Pos{})
		r, err := e.emitInstanceOf(test)
		if err == nil && !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i1 %s", r.Ref))
		}
		if !e.blockDone {
			e.emitTerminator("ret i1 false")
		}
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("inst", names[i])
		fmt.Fprintf(&e.functions, "\ndefine internal i1 %s(i64 %%v) {\nentry:\n%s}\n", fn, body)
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
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", own, e.staticOwnPtr(x.class, x.field)))
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, own))
		return z
	}
	// thunk generates one class's routine what with signature sig.
	thunk := func(what, c, sig string, gen func()) string {
		restore := e.beginDetachedFunc()
		gen()
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk(what, c)
		fmt.Fprintf(&e.functions, "\ndefine internal %s %s(%s) {\nentry:\n%s}\n", strings.Fields(sig)[0], fn, strings.SplitN(sig, " ", 2)[1], body)
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
		row.nstatic = thunk("nstatic", c, "i64 ptr %p", func() {
			n := "0"
			for _, x := range es {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", r, n, present(x)))
				n = r
			}
			e.emitTerminator("ret i64 " + n)
		})
		row.skey = thunk("skey", c, "ptr ptr %p, i64 %i", func() {
			nth(es, func(x entry) string { return e.internString(x.field) }, "ptr")
			e.emitTerminator("ret ptr null")
		})
		// sget loads the field and boxes it with the ordinary emitBoxValue.
		row.sget = thunk("sget", c, "i64 ptr %p, i64 %i", func() {
			nth(es, func(x entry) string {
				ty := e.classes[x.class].StaticFieldTypes[x.field]
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, storageIR(ty), e.staticSlotPtr(x.class, x.field)))
				if boxed, err := e.emitBoxValue(Value{Ref: r, Ty: ty}); err == nil {
					return boxed.Ref
				}
				return fmt.Sprintf("%d", nbUndefined)
			}, "i64")
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		})
	}

	// keys: Object.keys of a class, its present static field names.
	e.ensureMalloc()
	e.functions.WriteString(`
define { ptr, i64 } @__kml_classref_keys(ptr %p) {
entry:
  %n = call i64 @__kml_classref_nstatic(ptr %p)
  %bytes = mul i64 %n, 8
  %arr = call ptr @malloc(i64 %bytes)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %body ]
  %done = icmp sge i64 %i, %n
  br i1 %done, label %out, label %body
body:
  %k = call ptr @__kml_classref_skey(ptr %p, i64 %i)
  %slot = getelementptr ptr, ptr %arr, i64 %i
  store ptr %k, ptr %slot, align 8
  %inext = add i64 %i, 1
  br label %loop
out:
  %r0 = insertvalue { ptr, i64 } undef, ptr %arr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %n, 1
  ret { ptr, i64 } %r1
}
`)
}

// emitDynConstructDispatch generates each constructible reference's `i64
// (ptr argv, i64 argc)`: `new <that class>(…)` over the boxed arguments (an
// absent one undefined), boxed. __kml_dyn_construct(c, argv, argc, msg)
// finds it, and throws the TypeError msg for anything else.
func (e *Emitter) emitDynConstructDispatch() {
	var classes []string
	for _, c := range e.classRefClasses(true) {
		if !e.classes[c].IsAbstract {
			classes = append(classes, c)
		}
	}
	for _, c := range classes {
		restore := e.beginDetachedFunc()
		var args []ast.Expression
		for i := 0; i < e.ctorArity(c); i++ {
			name := fmt.Sprintf("__kml_ca%d", i)
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			has := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %d, %%argc", has, i))
			at := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds i64, ptr %%argv, i64 %d", at, i))
			// argv is read only when the slot exists.
			inL, outL, joinL := e.freshLabel("dynnew.arg"), e.freshLabel("dynnew.noarg"), e.freshLabel("dynnew.argd")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, inL, outL))
			e.emitLabel(inL)
			ld := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", ld, at))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ld, slot))
			e.emitTerminator("br label %" + joinL)
			e.emitLabel(outL)
			e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, slot))
			e.emitTerminator("br label %" + joinL)
			e.emitLabel(joinL)
			e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
			args = append(args, ast.NewIdentifier(name, ast.Pos{}))
		}
		e.inCtorArm = true
		v, err := e.emitNewExpression(ast.NewNewExpression(c, args, ast.Pos{}))
		e.inCtorArm = false
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
		fmt.Fprintf(&e.functions, "\ndefine internal i64 %s(ptr %%argv, i64 %%argc) {\nentry:\n%s}\n", fn, body)
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
		fmt.Fprintf(&e.functions, "\ndefine internal i64 %s(ptr %%argv, i64 %%argc) {\nentry:\n%s}\n", fn, body)
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
	fmt.Fprintf(&e.functions, "\ndefine internal i64 @__kml_dyn_construct_fail(ptr %%msg) {\nentry:\n%s}\n", body)
	e.ensureUnitReg()
	e.functions.WriteString("\ndefine i64 @__kml_dyn_construct(i64 %c, ptr %argv, i64 %argc, ptr %msg) {\nentry:\n" + ctorRefDispatchIR(7, "ptr", "null", "f") +
		"  %none = icmp eq ptr %f, null\n  br i1 %none, label %no, label %call\ncall:\n  %r = call i64 %f(ptr %argv, i64 %argc)\n  ret i64 %r\nno:\n  %t = call i64 @__kml_dyn_construct_fail(ptr %msg)\n  ret i64 %t\n}\n")
}

// emitClassRefGetRows generates, per class boxed as a value, `i64 (ptr
// key)`: a static member read through its reference (`K.make`, `K.count`,
// `K.length`), boxed; undefined for anything else.
func (e *Emitter) emitClassRefGetRows() {
	e.ensureStrcmp()
	e.ensureNanBox()
	for _, c := range e.classRefClasses(true) {
		restore := e.beginDetachedFunc()
		keys := append([]string{"length"}, e.classStaticKeys(c)...)
		for _, k := range keys {
			kHit, kNext := e.freshLabel("crget.key"), e.freshLabel("crget.nokey")
			cmp, isK := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %%key, ptr %s)", cmp, e.internString(k)))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isK, cmp))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isK, kHit, kNext))
			e.emitLabel(kHit)
			var boxed string
			if k == "length" && !seenStatic(e.classStaticKeys(c), "length") {
				boxed = e.emitNbEncodeDouble(fmt.Sprintf("%d.0", e.ctorLength(c)))
			} else if v, err := e.emitExpr(ast.NewMemberExpression(ast.NewIdentifier(c, ast.Pos{}), k, ast.Pos{})); err == nil && !e.blockDone {
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
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("get", c)
		fmt.Fprintf(&e.functions, "\ndefine internal i64 %s(ptr %%key) {\nentry:\n%s}\n", fn, body)
		e.ctorRowFor(e.classRefs[c]).get = fn
	}
}

// emitClassRefSetRows generates, per class boxed as a value, `void (ptr
// key, i64 v)`: a static field written through its reference (`K.count =
// 5`), the boxed value converted to the field's type.
func (e *Emitter) emitClassRefSetRows() {
	e.ensureStrcmp()
	for _, c := range e.classRefClasses(true) {
		restore := e.beginDetachedFunc()
		vSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", vSlot))
		e.emitInstr(fmt.Sprintf("store i64 %%v, ptr %s, align 8", vSlot))
		e.define("__kml_sv", Symbol{Ptr: vSlot, Ty: TypeAny})
		for _, k := range e.classStaticKeys(c) {
			if _, method := e.classes[c].StaticMethodSigs[k]; method {
				continue
			}
			kHit, kNext := e.freshLabel("crset.key"), e.freshLabel("crset.nokey")
			cmp, isK := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %%key, ptr %s)", cmp, e.internString(k)))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isK, cmp))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isK, kHit, kNext))
			e.emitLabel(kHit)
			assign := ast.NewAssignmentExpression("=", ast.NewMemberExpression(ast.NewIdentifier(c, ast.Pos{}), k, ast.Pos{}), ast.NewIdentifier("__kml_sv", ast.Pos{}), ast.Pos{})
			_, _ = e.emitExpr(assign)
			if !e.blockDone {
				e.emitTerminator("ret void")
			}
			e.emitLabel(kNext)
		}
		e.emitTerminator("ret void")
		body := e.allocas.String() + e.body.String()
		restore()
		fn := ctorThunk("set", c)
		fmt.Fprintf(&e.functions, "\ndefine internal void %s(ptr %%key, i64 %%v) {\nentry:\n%s}\n", fn, body)
		e.ctorRowFor(e.classRefs[c]).set = fn
	}
}
