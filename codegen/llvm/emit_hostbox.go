package llvm

import (
	_ "embed"
	"fmt"

	"KlainMainLang/ast"
	"strings"
)

//go:embed boxsrc/hostbox.c
var hostBoxSource string

// HostBoxSource is the host-box dispatchers' C source (boxsrc/hostbox.c).
func HostBoxSource() string { return hostBoxSource }

// UsesHostBoxC reports whether the program links the host-box dispatchers.
func (e *Emitter) UsesHostBoxC() bool { return e.usedHostBox }

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

// hostInlineFlag marks a headered host's header (headeredHost): its body
// follows the header, where a cell holds a handle.
const hostInlineFlag int64 = 1 << 50

const hostBoxTy = "{ i64, ptr }"

// hostLayout is one host type boxed somewhere in the program.
// hostCellObject reports a headerless host object that boxes into a host
// cell like a handle does.
func hostCellObject(t Type) bool {
	return (t.IsRegExp() || hostPrefixedObject(t)) && t.IsObject() && t.IR == "ptr" && !t.IsDynamic() && !t.IsArray() && !t.IsNull() && !t.IsUndefined()
}

// hostPrefixedObject reports a host class layout (hostObjectType) whose
// header word sits before its body, at -8, so the runtime's fixed field
// offsets are unchanged (TDD-00230 P3.3): a Response, a Request, an
// XMLHttpRequest, the HTTP server's request and response, a
// FinalizationRegistry.
func hostPrefixedObject(t Type) bool {
	return (t.IsResponse() || t.IsFetchRequest() || t.IsXHR() || t.IsFinalizationRegistry()) &&
		t.IsObject() && !t.IsClass() && !t.IsDynamic() && !t.IsPromise() && !t.IsArray()
}

// hostHandleIR is the IR type of the word a host cell of t carries.
func hostHandleIR(t Type) string {
	return "ptr"
}

// headeredHost reports a host value that carries its own header word, before
// its body (TDD-00247): a Map, a Set (weak ones too), a RegExp, a Date, an
// ArrayBuffer, a collection iterator, a ReadableStream, a WritableStream, a
// TransformStream, a stream reader or writer, or a prefixed host object. Its box is that header, not a cell.
func headeredHost(t Type) bool {
	return hostPrefixedObject(t) || t.IsArrayBuffer() && !t.IsClass() && !t.IsDynamic() && !t.IsArray() ||
		t.IsMap() || t.IsSet() ||
		(t.IsCollIter() || t.IsReadableStream() || t.IsStreamReader() || t.IsStreamWriter() || t.IsWritableStream() || t.IsTransformStream()) && !t.IsDynamic() || t.IsRegExp() && t.IsObject() && !t.IsClass() || t.IsDate() && t.IR == "ptr" && !t.IsClass() && !t.IsDynamic() ||
		t.IsPrimWrapper()
}

// hostBodyAt8 reports a host value an instance of a class extending its
// host holds at +8, after its tag word (TDD-00247): a headered host, or a
// task promise (whose header word precedes its body, though a promise
// boxes through its wrapper rather than as its header).
func hostBodyAt8(t Type) bool {
	return headeredHost(t) || t.IsPromise() && t.PromiseTask && !t.IsDynamic() && !t.IsClass()
}

// emitHostCellLoad loads the handle out of the host cell at cell; for a
// headered host the handle is the body after the header.
func (e *Emitter) emitHostCellLoad(cell string, t Type) string {
	if headeredHost(t) {
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, cell))
		return body
	}
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
	return t.IR == "ptr" && !t.IsDynamic() && !t.IsObject() && !t.IsArray() && !t.IsFunc() &&
		!t.IsPromise() && !t.IsGenerator() &&
		!t.IsBigInt() && !t.IsCaught() && !t.IsNull() && t.Kind != kindUndefined && !t.IsDynamicObject() && hasHandleFlag(t)
}

// hostClassPriority names the host classes whose flags overlap (a
// SharedArrayBuffer is an ArrayBuffer): the first set flag wins.
var hostClassPriority = []struct{ flag, class string }{
	{"IsSharedArrayBuffer", "SharedArrayBuffer"},
	{"IsArrayBuffer", "ArrayBuffer"},
	{"IsMap", "Map"},
	{"IsSet", "Set"},
	{"IsDate", "Date"},
}

// hostFlagJSName names the classes whose flag is not the JS class name.
var hostFlagJSName = map[string]string{
	"IsFetchRequest": "Request",
	"IsStreamReader": "ReadableStreamDefaultReader",
	"IsStreamWriter": "WritableStreamDefaultWriter",
	"IsRSController": "ReadableStreamDefaultController",
	"IsWSController": "WritableStreamDefaultController",
}

// hostClassName is the JS class a host type's values are instances of.
func hostClassName(t Type) string {
	if t.IsCollIter() && t.IterSrc != nil {
		return collIterName(t) // `[object Array Iterator]`
	}
	if t.IsArray() && !t.IsTypedArray() && !t.IsBuffer() {
		return "Array" // the parent row of a class extending Array
	}
	if t.HostTag != "" {
		return t.HostTag
	}
	if t.Weak && t.IsMap() {
		return "WeakMap"
	}
	if t.Weak && t.IsSet() {
		return "WeakSet"
	}
	for _, p := range hostClassPriority {
		if typeFlag(t, p.flag) {
			return p.class
		}
	}
	for _, name := range handleFlagNames {
		// IsObject is every host object's; its own flag names the class.
		if name != "IsObject" && typeFlag(t, name) {
			if js, ok := hostFlagJSName[name]; ok {
				return js
			}
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
	if t.IsArray() && t.ElemType != nil {
		// An array source's elements: a number[] iterator reads raw
		// numbers, an any[] one boxes.
		k += "|" + layoutFieldKey(*t.ElemType)
	}
	if t.IsCollIter() && t.IterSrc != nil {
		// An iterator's steps read its source by kind: a Map's keys and its
		// values are different layouts, as are two Maps' of other types.
		k += fmt.Sprintf("|it%d|", t.IterKind) + hostKey(*t.IterSrc)
	}
	return k
}

// hostHeaderWord is t's host header word without registering its layout
// (registration generates the box's render and method entry points, needed
// only once a value of the type is boxed). A Map or Set stores it at creation
// (TDD-00247); hostID gives the same word.
func (e *Emitter) hostHeaderWord(t Type) int64 {
	id := e.stableTypeID("H"+hostKey(t)) | hostTypeIDFlag
	if headeredHost(t) {
		id |= hostInlineFlag
	}
	return id
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
	base.setNullable(false)
	base.setUndefined(false)
	id := e.hostHeaderWord(t)
	e.hostLayouts = append(e.hostLayouts, hostLayout{id: id, class: hostClassName(t), ty: base})
	return id
}

// emitBoxHost boxes a host handle: a fresh cell, or null for an absent
// nullable one.
func (e *Emitter) emitBoxHost(v Value) Value {
	e.ensureMalloc()
	e.ensureHostBoxHooks()
	id := e.hostID(v.Ty)
	var cell string
	if headeredHost(v.Ty) {
		// Its header word is already before its body: the box is the
		// object itself, so identity holds through `any`.
		cell = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", cell, v.Ref))
	} else {
		cell = e.freshReg()
		e.heapMallocInto(cell, "16")
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", id, cell))
		hp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", hp, hostBoxTy, cell))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", hostHandleIR(v.Ty), v.Ref, hp))
	}
	boxed := e.emitNbTagPtr(cell, kmlTagObject)
	if !v.Ty.Nullable() {
		return Value{Ref: boxed, Ty: TypeAny}
	}
	// The null handle is the type's absent value: undefined for `T |
	// undefined` (and a missing optional), else null.
	absent := int64(nbNull)
	if v.Ty.IsUndefined() {
		absent = nbUndefined
	}
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", sel, e.ptrIsNull(v.Ref), absent, boxed))
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
	if headeredHost(target) {
		// An instance of a class extending the host has its body at +8 as
		// the host has: the runtime's row lookup walks its class to the host.
		e.ensureHostBoxHooks()
		name := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_host_class_name(i64 %s)", name, v.Ref))
		isHost = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isHost, name))
	}
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
func (e *Emitter) isHostClassName(name string) bool {
	t, ok := hostClassTypes[name]
	if !ok {
		t = e.resolveTypeName(name)
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
	// The registry's held type is not part of its host key.
	"FinalizationRegistry": FinalizationRegistryType(TypeAny),
	// A stream's chunk type is not part of its host key.
	"ReadableStream":  ReadableStreamType(TypeI64),
	"WritableStream":  WritableStreamType(TypeI64),
	"TransformStream": {IR: "ptr", Host: hostTransformStream},
	"Number":          primWrapperType("Number"),
	"String":          primWrapperType("String"),
	"Boolean":         primWrapperType("Boolean"),
}

// emitDynHostInstanceOf is `box instanceof name` for a host class: the box's
// header names one of that class's host types.
func (e *Emitter) emitDynHostInstanceOf(v Value, name string) Value {
	e.ensureHostBoxHooks()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_host_is(i64 %s, ptr %s)", r, v.Ref, e.internString(name)))
	return Value{Ref: r, Ty: TypeBool}
}

// ensureHostBoxHooks declares the finalize-defined host routines.
func (e *Emitter) ensureHostBoxHooks() {
	if e.usedHostBox {
		return
	}
	e.usedHostBox = true
	// The dispatchers over a host box's registered row (boxsrc/hostbox.c).
	e.emitGlobal(`declare ptr @__kml_host_inspect(ptr, i64)
declare ptr @__kml_host_tag(ptr)
declare zeroext i1 @__kml_host_is(i64, ptr)
declare i64 @__kml_host_iter(i64)
declare ptr @__kml_host_class_name(i64)`)
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
			stable := e.stableHelperBody(h.id)
			restore := e.beginDetachedFunc()
			var v Value
			if h.ty.IsArray() {
				// An array's cell is its header word; the header follows.
				hdr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%cell, i64 8", hdr))
				v = e.arrayValueFromHeaderReg(hdr, h.ty)
			} else {
				v = Value{Ref: e.emitHostCellLoad("%cell", h.ty), Ty: h.ty}
			}
			s, err := e.emitInspectField(v, d)
			if err != nil || s.Ref == "" {
				s = Value{Ref: e.internString("[" + h.class + "]"), Ty: TypePtr}
			}
			e.emitTerminator(fmt.Sprintf("ret ptr %s", s.Ref))
			body := e.allocas.String() + e.body.String()
			restore()
			stable()
			e.writeFunc(fmt.Sprintf("internal ptr %s(ptr %%cell)", fn), body)
		}
	}
}

// emitHostBoxFinalize generates the routines the host rows name (boxsrc/
// hostbox.c dispatches to them) and registers one row per host layout.
// Called last among the host steps.
func (e *Emitter) emitHostBoxFinalize() {
	if !e.usedHostBox {
		return
	}
	e.ensureUnitReg()
	e.ensureStrcmp() // other generated code leans on this decl arriving here
	for n := -1; n != len(e.hostLayouts); {
		n = len(e.hostLayouts)
		e.emitHostIterRoutines()
		e.emitHostInspectRoutines()
	}
	// The routines cap nesting at the compile-time depth; util.inspect's
	// `depth` option moves the cap at run time, so the dispatcher shifts the
	// depth by the difference (clamped to the routines generated).
	e.ensureInspectReduce()
	e.declareInspectOptDepth()
	maxD := e.effectiveInspectDepth() + 1
	for _, h := range e.hostLayouts {
		e.registerHostRow(h, maxD)
	}
}

// hostRowTy is a registered host layout row (see emitHostBoxFinalize).
const hostRowTy = "{ i64, ptr, ptr, ptr, ptr, ptr, ptr, ptr }"

// registerHostRow adds h's row: its inspect routine per depth, its own
// toString, its `[object X]` tag, its class name, its iterator and toJSON.
func (e *Emitter) registerHostRow(h hostLayout, maxD int) {
	id := h.id & kmlHdrIDMask
	// The table opens with the depth the routines were generated at and the
	// deepest one, then holds one routine per depth.
	fns := []string{
		fmt.Sprintf("ptr inttoptr (i64 %d to ptr)", e.effectiveInspectDepth()),
		fmt.Sprintf("ptr inttoptr (i64 %d to ptr)", maxD),
	}
	for d := 0; d <= maxD; d++ {
		fns = append(fns, fmt.Sprintf("ptr @__kml_host_inspect_%d_%d", id, d))
	}
	tab := fmt.Sprintf("@__kml_host_inspect_tab_%d", id)
	e.emitGlobal(fmt.Sprintf("%s = private constant [%d x ptr] [%s]", tab, len(fns), strings.Join(fns, ", ")))
	field := func(fn string) string {
		if fn == "" {
			return "ptr null"
		}
		return "ptr " + fn
	}
	tostr := ""
	if hostOwnToString[h.class] {
		tostr = e.emitHostToStringRoutine(h)
	}
	e.addUnitRow(unitKindHost, hostRowTy, h.id, strings.Join([]string{
		"ptr " + tab,
		field(tostr),
		"ptr " + e.internString("[object "+h.class+"]"),
		"ptr " + e.internString(h.class),
		field(e.hostIterFns[h.id]),
		field(e.hostToJSONFn(h)),
		field(e.hostToPrimFn(h)),
	}, ", "))
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
var hostOwnToString = map[string]bool{"RegExp": true, "Date": true, "Number": true, "String": true, "Boolean": true}

// emitHostToStringRoutine generates `ptr tostr(ptr cell)`: the typed
// String() of the handle a host box of layout h carries.
func (e *Emitter) emitHostToStringRoutine(h hostLayout) string {
	fn := fmt.Sprintf("@__kml_host_tostr_%d", h.id&kmlHdrIDMask)
	restore := e.beginDetachedFunc()
	handle := e.emitHostCellLoad("%cell", h.ty)
	var s Value
	var err error
	if h.ty.IsPrimWrapper() {
		s, err = e.emitValueToString(e.emitWrapperValue(handle))
	} else {
		s, err = e.emitValueToString(Value{Ref: handle, Ty: h.ty})
	}
	if err != nil || s.Ref == "" {
		s = Value{Ref: e.internString("[object " + h.class + "]"), Ty: TypePtr}
	}
	e.emitTerminator(fmt.Sprintf("ret ptr %s", s.Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal ptr %s(ptr %%cell)", fn), body)
	return fn
}

// hostIterable are the host classes a for-of or spread walks (their default
// iterator), boxed or not.
var hostIterable = map[string]bool{"Map": true, "Set": true}

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
		e.writeFunc(fmt.Sprintf("internal i64 %s(ptr %%cell)", fn), body)
		e.hostIterFns[h.id] = fn
	}
}

// hostToJSONFn generates h's toJSON routine (a Date's toISOString), or ""
// for a host class without one.
func (e *Emitter) hostToJSONFn(h hostLayout) string {
	if h.class != "Date" {
		return ""
	}
	restore := e.beginDetachedFunc()
	handle := e.emitHostCellLoad("%cell", h.ty)
	s, err := e.emitDateToISOString(Value{Ref: e.emitDateTimeValue(Value{Ref: handle, Ty: h.ty}), Ty: TypeI64})
	ret := "null"
	if err == nil {
		ret = s.Ref
	}
	e.emitTerminator(fmt.Sprintf("ret ptr %s", ret))
	body := e.allocas.String() + e.body.String()
	restore()
	fn := fmt.Sprintf("@__kml_host_tojson_%d", h.id&kmlHdrIDMask)
	e.writeFunc(fmt.Sprintf("internal ptr %s(ptr %%cell)", fn), body)
	return fn
}
