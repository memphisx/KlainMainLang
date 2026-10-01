package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"reflect"
	"sort"
	"strings"
)

// emit_hostbox.go — a host handle (a Map, a Blob, Headers, an ArrayBuffer, a
// Worker, …: a bare pointer to runtime state with no header word of its own)
// boxed into `any` (TDD-00230 P3.3). The box is a two-word cell
// { i64 header, ptr handle } under the object tag: the header carries the
// magic, hostTypeIDFlag and a per-program host type id, so the value reads as
// an object (`typeof` "object", a layout-table row), `instanceof` and
// console.log dispatch on the id, and unboxing loads the handle back. Two
// boxes of one handle are one value (`===`, Map keys).

// hostTypeIDFlag marks a host box's header (objheader.go's bit table).
const hostTypeIDFlag int64 = 1 << 49

const hostBoxTy = "{ i64, ptr }"

// hostLayout is one host type boxed somewhere in the program.
// hostCellObject reports a headerless host object that boxes into a host
// cell like a handle does.
func hostCellObject(t Type) bool {
	if t.IsDate {
		// A Date is its i64 timestamp; the cell carries the time value.
		return t.IR == "i64" && !t.IsDynamic && !t.Nullable && !t.IsUndefined
	}
	return (t.IsRegExp || t.IsURL) && t.IsObject && t.IR == "ptr" && !t.IsDynamic && !t.IsArray && !t.IsNull && !t.IsUndefined
}

// hostHandleIR is the IR type of the word a host cell of t carries: a
// pointer, or a Date's i64 time value.
func hostHandleIR(t Type) string {
	if t.IsDate {
		return "i64"
	}
	return "ptr"
}

// emitHostCellLoad loads the handle out of the host cell at cell.
func (e *Emitter) emitHostCellLoad(cell string, t Type) string {
	hp, handle := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", hp, hostBoxTy, cell))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", handle, hostHandleIR(t), hp))
	return handle
}

type hostLayout struct {
	id    int64 // the header word
	class string
	ty    Type
}

// isHostHandle reports a pointer-represented value with no header word of
// its own: every `ptr` host flag the earlier emitBoxValue cases leave over,
// and the headerless host objects a host box carries (a RegExp), which a
// box's member reads and calls reach through their declarations
// (emitHostShapeRow).
func isHostHandle(t Type) bool {
	if hostCellObject(t) {
		return true
	}
	// An index-signature dictionary is map-backed but an object: it boxes as
	// a dynamic object (emitBoxValue), not a host cell.
	return t.IR == "ptr" && !t.IsDynamic && !t.IsObject && !t.IsArray && !t.IsFunc &&
		!t.IsPromise && !t.IsGenerator && !t.IsReadableStream &&
		!t.IsBigInt && !t.IsCaught && !t.IsNull && !t.IsUndefined && !t.IsDynamicObject && hasHandleFlag(t)
}

// hostClassPriority names the host classes whose flags overlap (Headers is a
// Map, SharedArrayBuffer an ArrayBuffer): the first set flag wins.
var hostClassPriority = []struct{ flag, class string }{
	{"IsHeaders", "Headers"},
	{"IsURLSearchParams", "URLSearchParams"},
	{"IsSharedArrayBuffer", "SharedArrayBuffer"},
	{"IsArrayBuffer", "ArrayBuffer"},
	{"IsMap", "Map"},
	{"IsSet", "Set"},
	{"IsDate", "Date"},
}

// hostClassName is the JS class a host type's values are instances of.
func hostClassName(t Type) string {
	if t.Weak && t.IsMap {
		return "WeakMap"
	}
	if t.Weak && t.IsSet {
		return "WeakSet"
	}
	v := reflect.ValueOf(t)
	for _, p := range hostClassPriority {
		if v.FieldByName(p.flag).Bool() {
			return p.class
		}
	}
	rt := v.Type()
	for _, i := range handleFlagFields {
		// IsObject is every host object's; its own flag names the class.
		if name := rt.Field(i).Name; name != "IsObject" && v.Field(i).Bool() {
			return strings.TrimPrefix(name, "Is")
		}
	}
	return "Object"
}

// hostKey interns a host type: its class plus, for a collection, the storage
// of its keys and values (a Map<string, number> renders differently from a
// Map<string, string>).
func hostKey(t Type) string {
	k := hostClassName(t)
	if t.MapKey != nil {
		k += "|" + layoutFieldKey(*t.MapKey)
	}
	if t.MapVal != nil {
		k += "|" + layoutFieldKey(*t.MapVal)
	}
	return k
}

// hostID returns t's host header word, registering the type on first use.
func (e *Emitter) hostID(t Type) int64 {
	key := hostKey(t)
	for _, h := range e.hostLayouts {
		if hostKey(h.ty) == key {
			return h.id
		}
	}
	base := t
	base.Nullable, base.IsUndefined = false, false
	id := e.allocTypeID() | hostTypeIDFlag
	e.hostLayouts = append(e.hostLayouts, hostLayout{id: id, class: hostClassName(t), ty: base})
	return id
}

// emitBoxHost boxes a host handle: a fresh cell, or null for an absent
// nullable one.
func (e *Emitter) emitBoxHost(v Value) Value {
	e.ensureMalloc()
	e.ensureHostBoxHooks()
	id := e.hostID(v.Ty)
	cell := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", cell))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", id, cell))
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", hp, hostBoxTy, cell))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", hostHandleIR(v.Ty), v.Ref, hp))
	boxed := e.emitNbTagPtr(cell, kmlTagObject)
	if !v.Ty.Nullable {
		return Value{Ref: boxed, Ty: TypeAny}
	}
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", sel, e.ptrIsNull(v.Ref), nbNull, boxed))
	return Value{Ref: sel, Ty: TypeAny}
}

// emitHostProbe tests whether an object-tag payload is a host box: the cell
// pointer and an i1.
func (e *Emitter) emitHostProbe(payload string) (cell, isHost string) {
	cell = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", cell, payload))
	isNull := e.ptrIsNull(cell)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", out))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", out))
	loadL, doneL := e.freshLabel("host.probe"), e.freshLabel("host.probed")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, loadL))
	e.emitLabel(loadL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, cell))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", e.hostHeaderTest(hdr), out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	isHost = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", isHost, out))
	return cell, isHost
}

// hostHeaderTest is an i1: the header word hdr is a host box's.
func (e *Emitter) hostHeaderTest(hdr string) string {
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", m, hdr, kmlHdrMagicMask|hostTypeIDFlag))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", r, m, kmlHdrMagic|hostTypeIDFlag))
	return r
}

// emitUnboxHost reads a host handle of type target out of a box: the handle
// of a host box, null for null/undefined, and anything else reinterpreted
// as before (an unchecked `any` assignment).
func (e *Emitter) emitUnboxHost(v Value, target Type) Value {
	hir := hostHandleIR(target)
	tag, payload := e.emitUnboxTagPayload(v)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, hir))
	isN, isU, nullish := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isN, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isU, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isN, isU))
	first := e.freshReg()
	if hir == "i64" {
		// A Date slot holding a number: its time value.
		d := e.emitAnyToNum(v)
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", t, d))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", first, nullish, t))
	} else {
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", raw, payload))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", first, nullish, raw))
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", hir, first, slot))
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	probeL, loadL, doneL := e.freshLabel("unhost.probe"), e.freshLabel("unhost.load"), e.freshLabel("unhost.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, probeL, doneL))
	e.emitLabel(probeL)
	cell, isHost := e.emitHostProbe(payload)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isHost, loadL, doneL))
	e.emitLabel(loadL)
	h := e.emitHostCellLoad(cell, target)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", hir, h, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, hir, slot))
	return Value{Ref: r, Ty: target}
}

// hostClassNames are the classes `x instanceof C` can test a boxed host
// value against.
func isHostClassName(name string) bool {
	t, ok := hostClassTypes[name]
	if !ok {
		t = ResolveTypeName(name)
	}
	return isHostHandle(t) && hostClassName(t) == name
}

// hostClassTypes are the host classes whose type name alone (a generic one
// without its arguments) does not resolve.
var hostClassTypes = map[string]Type{
	"Map":     MapType(TypePtr, TypePtr),
	"Set":     SetType(TypePtr),
	"WeakMap": WeakMapType(TypePtr, TypePtr),
	"WeakSet": WeakSetType(TypePtr),
	"WeakRef": WeakRefType(TypePtr),
}

// emitDynHostInstanceOf is `box instanceof name` for a host class: the box's
// header names one of that class's host types.
func (e *Emitter) emitDynHostInstanceOf(v Value, name string) Value {
	e.ensureHostBoxHooks()
	if e.hostInstanceofUsed == nil {
		e.hostInstanceofUsed = map[string]bool{}
	}
	e.hostInstanceofUsed[name] = true
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_host_is_%s(i64 %s)", r, name, v.Ref))
	return Value{Ref: r, Ty: TypeBool}
}

// ensureHostBoxHooks declares the finalize-defined host routines.
func (e *Emitter) ensureHostBoxHooks() {
	if e.usedHostBox {
		return
	}
	e.usedHostBox = true
	// No declares: __kml_host_inspect and __kml_host_is_* are defined at
	// finalize.
}

// emitHostInspectCall renders a probed host cell as console.log does.
func (e *Emitter) emitHostInspectCall(cell string, depth int) string {
	e.ensureHostBoxHooks()
	if max := e.effectiveInspectDepth() + 1; depth > max {
		depth = max
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_host_inspect(ptr %s, i64 %d)", r, cell, depth))
	return r
}

// emitHostInspectRoutines generates a console.log routine per (host type,
// depth) for every host type registered since the last call. Rendering a
// host value can box another (a Map of Blobs), so it runs until no new one
// appears.
func (e *Emitter) emitHostInspectRoutines() {
	for ; e.hostRendered < len(e.hostLayouts); e.hostRendered++ {
		h := e.hostLayouts[e.hostRendered]
		for d := 0; d <= e.effectiveInspectDepth()+1; d++ {
			fn := fmt.Sprintf("@__kml_host_inspect_%d_%d", h.id&kmlHdrIDMask, d)
			restore := e.beginDetachedFunc()
			handle := e.emitHostCellLoad("%cell", h.ty)
			s, err := e.emitInspectField(Value{Ref: handle, Ty: h.ty}, d)
			if err != nil || s.Ref == "" {
				s = Value{Ref: e.internString("[" + h.class + "]"), Ty: TypePtr}
			}
			e.emitTerminator(fmt.Sprintf("ret ptr %s", s.Ref))
			body := e.allocas.String() + e.body.String()
			restore()
			e.functions.WriteString(fmt.Sprintf("\ndefine internal ptr %s(ptr %%cell) {\nentry:\n%s}\n", fn, body))
		}
	}
}

// emitHostBoxFinalize defines __kml_host_inspect (dispatching on the header
// id and depth to the routines above) and one __kml_host_is_<Class> per
// class an `instanceof` tests. Called last among the host steps.
func (e *Emitter) emitHostBoxFinalize() {
	if !e.usedHostBox {
		return
	}
	for n := -1; n != len(e.hostLayouts); {
		n = len(e.hostLayouts)
		e.emitHostIterRoutines()
		e.emitHostInspectRoutines()
	}
	e.emitHostIterDispatch()
	e.emitHostToJSONDispatch()
	// The routines cap nesting at the compile-time depth; util.inspect's
	// `depth` option moves the cap at run time, so the dispatch shifts the
	// depth by the difference (clamped to the routines generated).
	e.ensureInspectReduce()
	e.declareInspectOptDepth()
	maxD := e.effectiveInspectDepth() + 1
	var b strings.Builder
	b.WriteString("\ndefine ptr @__kml_host_inspect(ptr %cell, i64 %depth0) {\nentry:\n")
	fmt.Fprintf(&b, "  %%opt = load i64, ptr @__kml_inspect_opt_depth, align 8\n")
	fmt.Fprintf(&b, "  %%shift = sub i64 %d, %%opt\n  %%d1 = add i64 %%depth0, %%shift\n", e.effectiveInspectDepth())
	fmt.Fprintf(&b, "  %%lo = icmp slt i64 %%d1, 0\n  %%d2 = select i1 %%lo, i64 0, i64 %%d1\n")
	fmt.Fprintf(&b, "  %%hi = icmp sgt i64 %%d2, %d\n  %%depth = select i1 %%hi, i64 %d, i64 %%d2\n", maxD, maxD)
	e.emitGlobal("@__kml_inspect_indent_shift = external global i64")
	b.WriteString("  %oldshift = load i64, ptr @__kml_inspect_indent_shift, align 8\n")
	b.WriteString("  %newshift = sub i64 %depth, %depth0\n")
	b.WriteString("  store i64 %newshift, ptr @__kml_inspect_indent_shift, align 8\n")
	b.WriteString("  %hdr = load i64, ptr %cell, align 8\n")
	n := 0
	for _, h := range e.hostLayouts {
		for d := 0; d <= e.effectiveInspectDepth()+1; d++ {
			fn := fmt.Sprintf("@__kml_host_inspect_%d_%d", h.id&kmlHdrIDMask, d)
			fmt.Fprintf(&b, "  %%m%d = icmp eq i64 %%hdr, %d\n  %%dm%d = icmp eq i64 %%depth, %d\n  %%c%d = and i1 %%m%d, %%dm%d\n  br i1 %%c%d, label %%hit%d, label %%next%d\nhit%d:\n  %%r%d = call ptr %s(ptr %%cell)\n  store i64 %%oldshift, ptr @__kml_inspect_indent_shift, align 8\n  ret ptr %%r%d\nnext%d:\n",
				n, h.id, n, d, n, n, n, n, n, n, n, n, fn, n, n)
			n++
		}
	}
	fmt.Fprintf(&b, "  store i64 %%oldshift, ptr @__kml_inspect_indent_shift, align 8\n  ret ptr %s\n}\n", e.internString("[Object]"))
	// String(box): a class with a toString of its own (a RegExp's `/a/g`)
	// renders through the typed conversion; any other is `[object Map]`.
	var own []string
	for _, h := range e.hostLayouts {
		if hostOwnToString[h.class] {
			own = append(own, e.emitHostToStringRoutine(h))
		} else {
			own = append(own, "")
		}
	}
	b.WriteString("\ndefine ptr @__kml_host_tag(ptr %cell) {\nentry:\n  %hdr = load i64, ptr %cell, align 8\n")
	for i, h := range e.hostLayouts {
		if own[i] == "" {
			continue
		}
		fmt.Fprintf(&b, "  %%o%d = icmp eq i64 %%hdr, %d\n  br i1 %%o%d, label %%own%d, label %%notown%d\nown%d:\n  %%os%d = call ptr %s(ptr %%cell)\n  ret ptr %%os%d\nnotown%d:\n", i, h.id, i, i, i, i, i, own[i], i, i)
	}
	b.WriteString("  %ct = call ptr @__kml_host_class_tag(ptr %cell)\n  ret ptr %ct\n}\n")
	// Object.prototype.toString's `[object Map]`, whatever the class's own
	// toString.
	b.WriteString("\ndefine ptr @__kml_host_class_tag(ptr %cell) {\nentry:\n  %hdr = load i64, ptr %cell, align 8\n")
	prev := e.internString("[object Object]")
	for i, h := range e.hostLayouts {
		fmt.Fprintf(&b, "  %%t%d = icmp eq i64 %%hdr, %d\n  %%s%d = select i1 %%t%d, ptr %s, ptr %s\n", i, h.id, i, i, e.internString("[object "+h.class+"]"), prev)
		prev = fmt.Sprintf("%%s%d", i)
	}
	fmt.Fprintf(&b, "  ret ptr %s\n}\n", prev)
	e.functions.WriteString(b.String())
	var names []string
	for n := range e.hostInstanceofUsed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		var ids []int64
		for _, h := range e.hostLayouts {
			if h.class == name {
				ids = append(ids, h.id)
			}
		}
		var f strings.Builder
		fmt.Fprintf(&f, "\ndefine i1 @__kml_host_is_%s(i64 %%v) {\nentry:\n", name)
		f.WriteString("  %kind = and i64 %v, 7\n  %isobj = icmp eq i64 %kind, 1\n  %lo = icmp uge i64 %v, 65536\n  %hi = icmp ult i64 %v, 562949953421312\n  %ptr = and i1 %lo, %hi\n  %ok = and i1 %isobj, %ptr\n  br i1 %ok, label %load, label %no\nload:\n")
		f.WriteString("  %pb = and i64 %v, -8\n  %p = inttoptr i64 %pb to ptr\n  %hdr = load i64, ptr %p, align 8\n")
		prev := "false"
		for i, id := range ids {
			fmt.Fprintf(&f, "  %%e%d = icmp eq i64 %%hdr, %d\n  %%o%d = or i1 %s, %%e%d\n", i, id, i, prev, i)
			prev = fmt.Sprintf("%%o%d", i)
		}
		fmt.Fprintf(&f, "  ret i1 %s\nno:\n  ret i1 false\n}\n", prev)
		e.functions.WriteString(f.String())
	}
}

// emitHostToStringTag is a host box's Object.prototype.toString form,
// `[object Map]`: its class name, read by header id.
func (e *Emitter) emitHostToStringTag(cell string) string {
	e.ensureHostBoxHooks()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_host_tag(ptr %s)", r, cell))
	return r
}

// hostOwnToString are the host classes whose prototype has a toString of
// its own (rather than Object.prototype's `[object X]`).
var hostOwnToString = map[string]bool{"RegExp": true, "Date": true}

// emitHostToStringRoutine generates `ptr tostr(ptr cell)`: the typed
// String() of the handle a host box of layout h carries.
func (e *Emitter) emitHostToStringRoutine(h hostLayout) string {
	fn := fmt.Sprintf("@__kml_host_tostr_%d", h.id&kmlHdrIDMask)
	restore := e.beginDetachedFunc()
	handle := e.emitHostCellLoad("%cell", h.ty)
	s, err := e.emitValueToString(Value{Ref: handle, Ty: h.ty})
	if err != nil || s.Ref == "" {
		s = Value{Ref: e.internString("[object " + h.class + "]"), Ty: TypePtr}
	}
	e.emitTerminator(fmt.Sprintf("ret ptr %s", s.Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal ptr %s(ptr %%cell) {\nentry:\n%s}\n", fn, body))
	return fn
}

// hostIterable are the host classes a for-of or spread walks (their default
// iterator), boxed or not.
var hostIterable = map[string]bool{"Map": true, "Set": true, "Headers": true, "URLSearchParams": true}

// emitHostIterValue is the value a for-of or spread over an `any` walks: an
// iterable host box's entries as a boxed array (`[...box]`), anything else
// as itself.
func (e *Emitter) emitHostIterValue(v string) string {
	e.ensureHostBoxHooks()
	e.hostIterUsed = true
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_host_iter(i64 %s)", r, v))
	return r
}

// emitHostIterRoutines generates `i64 iter(ptr cell)` per iterable host
// layout registered since the last call: the typed `[...handle]`, boxed.
func (e *Emitter) emitHostIterRoutines() {
	if e.hostIterFns == nil {
		e.hostIterFns = map[int64]string{}
	}
	for ; e.hostIterated < len(e.hostLayouts); e.hostIterated++ {
		h := e.hostLayouts[e.hostIterated]
		if !e.hostIterUsed || !hostIterable[h.class] {
			continue
		}
		fn := fmt.Sprintf("@__kml_host_iter_%d", h.id&kmlHdrIDMask)
		restore := e.beginDetachedFunc()
		self := e.bindHostSelf("%cell", h)
		ref := fmt.Sprintf("%d", nbUndefined)
		if v, err := e.emitExpr(ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: self}}, ast.Pos{})); err == nil && !e.blockDone {
			if b, err := e.emitBoxValue(v); err == nil {
				ref = b.Ref
			}
		}
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", ref))
		}
		body := e.allocas.String() + e.body.String()
		restore()
		e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%cell) {\nentry:\n%s}\n", fn, body))
		e.hostIterFns[h.id] = fn
	}
}

// emitHostIterDispatch defines __kml_host_iter: an iterable host box's
// routine by header id, else the value itself.
func (e *Emitter) emitHostIterDispatch() {
	if !e.hostIterUsed {
		return
	}
	var b strings.Builder
	b.WriteString("\ndefine i64 @__kml_host_iter(i64 %v) {\nentry:\n")
	b.WriteString("  %kind = and i64 %v, 7\n  %isobj = icmp eq i64 %kind, 1\n  %lo = icmp uge i64 %v, 65536\n  %hi = icmp ult i64 %v, 562949953421312\n  %ptr = and i1 %lo, %hi\n  %ok = and i1 %isobj, %ptr\n  br i1 %ok, label %load, label %no\nload:\n")
	b.WriteString("  %pb = and i64 %v, -8\n  %cell = inttoptr i64 %pb to ptr\n  %hdr = load i64, ptr %cell, align 8\n")
	i := 0
	for _, h := range e.hostLayouts {
		fn, ok := e.hostIterFns[h.id]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "  %%m%d = icmp eq i64 %%hdr, %d\n  br i1 %%m%d, label %%hit%d, label %%next%d\nhit%d:\n  %%r%d = call i64 %s(ptr %%cell)\n  ret i64 %%r%d\nnext%d:\n", i, h.id, i, i, i, i, i, fn, i, i)
		i++
	}
	b.WriteString("  ret i64 %v\nno:\n  ret i64 %v\n}\n")
	e.functions.WriteString(b.String())
}

// emitHostToJSONDispatch defines __kml_host_tojson: the string a host box's
// toJSON returns (a Date's toISOString), or null for a host class without
// one.
func (e *Emitter) emitHostToJSONDispatch() {
	var b strings.Builder
	var fns strings.Builder
	b.WriteString("\ndefine ptr @__kml_host_tojson(ptr %cell) {\nentry:\n  %hdr = load i64, ptr %cell, align 8\n")
	for i, h := range e.hostLayouts {
		if h.class != "Date" {
			continue
		}
		restore := e.beginDetachedFunc()
		handle := e.emitHostCellLoad("%cell", h.ty)
		s, err := e.emitDateToISOString(Value{Ref: handle, Ty: h.ty})
		ret := "null"
		if err == nil {
			ret = s.Ref
		}
		e.emitTerminator(fmt.Sprintf("ret ptr %s", ret))
		body := e.allocas.String() + e.body.String()
		restore()
		fn := fmt.Sprintf("@__kml_host_tojson_%d", h.id&kmlHdrIDMask)
		fmt.Fprintf(&fns, "\ndefine internal ptr %s(ptr %%cell) {\nentry:\n%s}\n", fn, body)
		fmt.Fprintf(&b, "  %%m%d = icmp eq i64 %%hdr, %d\n  br i1 %%m%d, label %%hit%d, label %%next%d\nhit%d:\n  %%r%d = call ptr %s(ptr %%cell)\n  ret ptr %%r%d\nnext%d:\n", i, h.id, i, i, i, i, i, fn, i, i)
	}
	b.WriteString("  ret ptr null\n}\n")
	e.functions.WriteString(fns.String())
	e.functions.WriteString(b.String())
}
