package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// emit_record_view.go — a structural object type read through any layout
// that satisfies it (TDD-00233 Stage 2). A value typed by an interface or
// type literal is the original object's pointer: a class instance keeps its
// class layout. Each member access compares the object's header with the
// type's own layout id; on a match the member is at its static offset, and
// any other layout is read and written through the layout table
// (__kml_shape_get/set), a method called with the object as `this`.

// isRecordView reports a plain object type whose values may have another
// layout at run time.
func isRecordView(t Type) bool {
	return plainRecordType(t)
}

// emitRecordForeign is an i1: the object at obj (typed t) has another
// layout that the layout table knows (a header with the magic that is not
// t's own id). An object without such a header keeps the static read.
func (e *Emitter) emitRecordForeign(obj string, t Type) string {
	id := e.objHeaderWord(t)
	// A null object reads as its own layout (its null handling is the
	// caller's, as before).
	hslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", hslot))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", id, hslot))
	loadL, haveL := e.freshLabel("recview.hdr"), e.freshLabel("recview.hdrdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(obj), haveL, loadL))
	e.emitLabel(loadL)
	h0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", h0, obj))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", h0, hslot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveL))
	e.emitLabel(haveL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, hslot))
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", m, hdr, kmlHdrMagicMask|hostTypeIDFlag))
	magic := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", magic, m, kmlHdrMagic))
	other := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", other, hdr, id))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, magic, other))
	return r
}

// recordSlotIR is the storage a member of type t occupies in an object.
func recordSlotIR(t Type) string {
	if isNullableScalar(t) {
		return nullableScalarStorageIR(t)
	}
	if t.IsArray {
		return "ptr"
	}
	return StructFieldIR(t)
}

// emitRecordFieldSlot returns the address member name of obj (typed t) is
// read from: gep (its static offset) when the object has t's layout, else a
// temporary the layout table's value is converted into (read=true), or a
// fresh temporary to be written back (read=false). foreign is the i1 that
// chose the temporary.
func (e *Emitter) emitRecordFieldSlot(obj Value, gep string, fieldTy Type, name string, read bool) (slot, foreign string) {
	foreign = e.emitRecordForeign(obj.Ref, obj.Ty)
	holder := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", holder))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", gep, holder))
	tmp := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", tmp, recordSlotIR(fieldTy)))
	slowL, doneL := e.freshLabel("recview.slow"), e.freshLabel("recview.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", foreign, slowL, doneL))
	e.emitLabel(slowL)
	if read {
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s)", e.recordReadFn(obj.Ty, fieldTy, name), obj.Ref, tmp))
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", tmp, holder))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	slot = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", slot, holder))
	return slot, foreign
}

// emitRecordWriteBack stores a member written into a foreign object's
// temporary (emitRecordFieldSlot) through the layout table.
func (e *Emitter) emitRecordWriteBack(obj Value, slot, foreign string, fieldTy Type, name string) {
	wbL, doneL := e.freshLabel("recview.wb"), e.freshLabel("recview.wbdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", foreign, wbL, doneL))
	e.emitLabel(wbL)
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s)", e.recordWriteFn(obj.Ty, fieldTy, name), obj.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// recordFnName names a routine of member name of layout t.
func (e *Emitter) recordFnName(kind string, t Type, name string) string {
	id := e.objHeaderWord(t) & kmlHdrIDMask
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "_%x", r)
		}
	}
	return fmt.Sprintf("@__kml_rec_%s_%d_%s", kind, id, b.String())
}

// recordFn is one layout-table read/write routine a view site called.
type recordFn struct {
	name, kind string
	fieldTy    Type
	member     string
}

// recordReadFn names `void read(ptr obj, ptr slot)`: member name of a
// foreign object, read through the layout table, converted to fieldTy into
// slot. A method comes back bound to the object (it is read to be called).
// The body is written at finalize (emitRecordFnsFinalize).
func (e *Emitter) recordReadFn(t, fieldTy Type, name string) string {
	return e.noteRecordFn("read", t, fieldTy, name)
}

// recordWriteFn names `void write(ptr obj, ptr slot)`: the fieldTy value in
// slot stored as member name of a foreign object.
func (e *Emitter) recordWriteFn(t, fieldTy Type, name string) string {
	return e.noteRecordFn("write", t, fieldTy, name)
}

func (e *Emitter) noteRecordFn(kind string, t, fieldTy Type, name string) string {
	fn := e.recordFnName(kind, t, name)
	if e.recordFns[fn] {
		return fn
	}
	if e.recordFns == nil {
		e.recordFns = map[string]bool{}
	}
	e.recordFns[fn] = true
	e.recordFnList = append(e.recordFnList, recordFn{name: fn, kind: kind, fieldTy: fieldTy, member: name})
	return fn
}

// emitRecordFnsFinalize writes the view routines' bodies. Only an object
// whose layout has a row in the layout table can be foreign to a view (a
// class instance or object that reached a structural slot, or was boxed);
// with no rows, a foreign header is one no table knows, and the routine
// throws as the dynamic path does, without pulling the table in.
func (e *Emitter) emitRecordFnsFinalize() {
	rows := len(e.boxedLayouts) > 0
	for _, r := range e.recordFnList {
		restore := e.beginDetachedFunc()
		switch {
		case !rows:
			e.emitThrowTypeError("dynamic property access on a statically-typed value is not supported")
		case r.kind == "read":
			box := Value{Ref: e.emitNbTagPtr("%obj", kmlTagObject), Ty: TypeAny}
			v, err := e.emitDynAnyMemberGetNamed(box, e.internString(r.member), r.member, ast.Pos{})
			if err == nil {
				// A method is read to be called with the object as `this`; a
				// function typed with its own `this: T` takes the caller's.
				if (r.fieldTy.IsFunc && !r.fieldTy.FuncThis) || (r.fieldTy.IsDynamic && isNullableFunc(r.fieldTy)) {
					v = e.emitBindDynMethod(v, box)
				}
				if isStringDict(r.fieldTy) {
					v = e.emitBoxedObjToDict(v, r.fieldTy)
				} else {
					v = e.coerce(v, r.fieldTy)
				}
				e.storeScalarOrNullableField("%slot", r.fieldTy, v)
			}
		default:
			var v Value
			if r.fieldTy.IsArray {
				v = e.loadArrayFieldValue("%slot", r.fieldTy)
			} else {
				v = e.loadScalarOrNullableField("%slot", r.fieldTy)
			}
			if boxed, err := e.emitBoxValue(v); err == nil {
				box := Value{Ref: e.emitNbTagPtr("%obj", kmlTagObject), Ty: TypeAny}
				_, _ = e.emitDynAnyMemberSetNamed(box, e.internString(r.member), r.member, boxed, ast.Pos{})
			}
		}
		e.emitTerminator("ret void")
		body := e.allocas.String() + e.body.String()
		restore()
		e.functions.WriteString(fmt.Sprintf("\ndefine internal void %s(ptr %%obj, ptr %%slot) {\nentry:\n%s}\n", r.name, body))
	}
	e.recordFnList = nil
}

// isNullableFunc reports an optional method's `F | undefined`.
func isNullableFunc(t Type) bool {
	for _, m := range t.UnionMembers {
		if m.IsFunc {
			return true
		}
	}
	return false
}

// emitBindDynMethod binds a dynamic function read off an object to that
// object (`this`), as a method call through the member does; any other
// value passes through.
func (e *Emitter) emitBindDynMethod(fn, this Value) Value {
	tag, _ := e.emitUnboxTagPayload(fn)
	isFn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", out))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fn.Ref, out))
	bindL, doneL := e.freshLabel("recview.bind"), e.freshLabel("recview.bound")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, bindL, doneL))
	e.emitLabel(bindL)
	e.ensureDynBound()
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", env))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fn.Ref, env))
	ts, cs := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 1", ts, env))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", this.Ref, ts))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 2", cs, env))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cs))
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, fn.Ref))
	trec := e.emitIntToPtr(pay)
	tarSlot, ar := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", tarSlot, trec))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", ar, tarSlot))
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", rec))
	e.emitInstr(fmt.Sprintf("store ptr @__kml_dyn_bound, ptr %s, align 8", rec))
	es, as := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", es, rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, es))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", as, rec))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ar, as))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(rec, kmlTagDynFunc), out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, out))
	return Value{Ref: r, Ty: TypeAny}
}

// noteRecordViewSource records that values of type t reach a structural
// slot of type target: t's layout row (and its subclasses') is generated,
// unless t has target's own header (a plain object of the same layout).
func (e *Emitter) noteRecordViewSource(t, target Type) {
	if !t.IsClass {
		if (plainRecordType(t) || emptyRecordType(t)) && e.objHeaderWord(t) != e.objHeaderWord(target) {
			e.noteBoxedLayout(t)
		}
		return
	}
	e.noteBoxedLayout(t)
	for name, info := range e.classes {
		if name != t.ClassName && e.classExtends(name, t.ClassName) {
			e.noteBoxedLayout(info.Ty)
		}
	}
}

// classExtends reports whether class name derives from base.
func (e *Emitter) classExtends(name, base string) bool {
	for i := 0; i < 64; i++ {
		info, ok := e.classes[name]
		if !ok || info.BaseClass == "" {
			return false
		}
		if info.BaseClass == base {
			return true
		}
		name = info.BaseClass
	}
	return false
}

// emitRecordGep is the address member idx (name, fieldTy) of the object at
// objPtr of type objTy is read from: its static offset, or for a structural
// type a view slot (emitRecordFieldSlot).
func (e *Emitter) emitRecordGep(objPtr string, objTy Type, idx int, fieldTy Type, name string) string {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objTy.StructIR(), objPtr, idx))
	if !isRecordView(objTy) {
		return gep
	}
	slot, _ := e.emitRecordFieldSlot(Value{Ref: objPtr, Ty: objTy}, gep, fieldTy, name, true)
	return slot
}

// emitObjHooksFinalize defines the hooks dynjson.c reads a static object's
// layout row through (__kml_obj_nkeys/key/get/name), and a host-box
// renderer stub when the program boxed no host value.
func (e *Emitter) emitObjHooksFinalize() {
	if !e.usedDynJSONC {
		return
	}
	// Defined in shape.c (KML_OBJ_HOOKS).
	e.emitGlobal("declare i64 @__kml_obj_nkeys(ptr)")
	e.emitGlobal("declare ptr @__kml_obj_key(ptr, i64)")
	e.emitGlobal("declare i64 @__kml_obj_get(ptr, ptr)")
	e.emitGlobal("declare i32 @__kml_obj_has(ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_obj_name(ptr)")
	e.emitErrorOwnKeysHooks()
	if !e.usedHostBox {
		e.emitGlobal(fmt.Sprintf("define ptr @__kml_host_inspect(ptr %%c, i64 %%d) {\nentry:\n  ret ptr %s\n}", e.internString("[Object]")))
		e.emitGlobal("define ptr @__kml_host_tojson(ptr %c) {\nentry:\n  ret ptr null\n}")
		e.emitGlobal(fmt.Sprintf("define ptr @__kml_host_tag(ptr %%c) {\nentry:\n  ret ptr %s\n}", e.internString("[object Object]")))
		e.emitGlobal(fmt.Sprintf("define ptr @__kml_host_class_tag(ptr %%c) {\nentry:\n  ret ptr %s\n}", e.internString("[object Object]")))
	}
	e.emitPromiseInspectHook()
	e.emitErrorInspectHook()
	e.emitInspectCustomHook()
	e.emitToStringTagHook()
}

// emitErrorInspectHook defines @__kml_error_inspect(obj): a boxed Error
// nested in an inspected value renders as the top-level one does
// (emitErrorToString), not as a plain object.
func (e *Emitter) emitErrorInspectHook() {
	restore := e.beginThunkEmit()
	s, err := e.emitErrorToString(Value{Ref: "%o", Ty: TypePtr})
	if err != nil {
		restore()
		e.emitGlobal(fmt.Sprintf("define ptr @__kml_error_inspect(ptr %%o) {\nentry:\n  ret ptr %s\n}", e.internString("[Error]")))
		return
	}
	e.emitTerminator(fmt.Sprintf("ret ptr %s", s.Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	e.emitGlobal(fmt.Sprintf("define ptr @__kml_error_inspect(ptr %%o) {\nentry:\n%s}", body))
}

// emitPromiseInspectHook defines @__kml_promise_inspect_parts(obj, out):
// for a boxed promise's wrapper (emit_anyprom.go), its state (0 pending,
// 1 fulfilled, 2 rejected) with the value or reason boxed into *out; -1 for
// any other object.
func (e *Emitter) emitPromiseInspectHook() {
	if !e.usedPromiseBox {
		e.emitGlobal("define i64 @__kml_promise_inspect_parts(ptr %o, ptr %out) {\nentry:\n  ret i64 -1\n}")
		return
	}
	wt := promiseBoxType().StructIR()
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_promise_inspect_parts(ptr %%o, ptr %%out) {
entry:
  %%h = load i64, ptr %%o, align 8
  %%is = icmp eq i64 %%h, %d
  br i1 %%is, label %%yes, label %%no
no:
  ret i64 -1
yes:
  %%pp = getelementptr %s, ptr %%o, i32 0, i32 1
  %%prom = load ptr, ptr %%pp, align 8
  %%sp = getelementptr %s, ptr %%prom, i32 0, i32 0
  %%st = load i64, ptr %%sp, align 8
  switch i64 %%st, label %%pending [ i64 1, label %%ful
                                     i64 2, label %%rej ]
pending:
  ret i64 0
ful:
  %%bp = getelementptr %s, ptr %%o, i32 0, i32 2
  %%bf = load ptr, ptr %%bp, align 8
  %%v = call i64 %%bf(ptr %%prom)
  store i64 %%v, ptr %%out, align 8
  ret i64 1
rej:
  %%tp = getelementptr %s, ptr %%prom, i32 0, i32 3
  %%tw = load i64, ptr %%tp, align 8
  %%tag = trunc i64 %%tw to i8
  %%yp = getelementptr %s, ptr %%prom, i32 0, i32 2
  %%pay = load i64, ptr %%yp, align 8
  %%isErr = icmp eq i8 %%tag, %d
  %%objBox = or i64 %%pay, 1
  %%other = call i64 @__kml_nb_pack(i8 %%tag, i64 %%pay)
  %%box = select i1 %%isErr, i64 %%objBox, i64 %%other
  store i64 %%box, ptr %%out, align 8
  ret i64 2
}`, e.promiseBoxHeader(), wt, promiseStructIR, wt, promiseStructIR, promiseStructIR, kmlTagError))
}

// emitObjExtensibleFinalize defines @__kml_obj_extensible(obj): whether a
// property may be added to a static object (not frozen, sealed or
// prevented from extensions).
func (e *Emitter) emitObjExtensibleFinalize() {
	if !e.usedObjExtensible {
		return
	}
	if !e.usedFrozenSet {
		e.emitGlobal("define internal i1 @__kml_obj_extensible(ptr %o) {\nentry:\n  ret i1 true\n}")
		return
	}
	e.emitGlobal(`define internal i1 @__kml_obj_extensible(ptr %o) {
entry:
  %set = call ptr @__kml_frozen_set_get()
  %k = ptrtoint ptr %o to i64
  %lv = call i64 @__kml_map_num_get(ptr %set, i64 %k)
  %ok = icmp eq i64 %lv, 0
  ret i1 %ok
}`)
}

// ensureShapeKeysArray emits @__kml_shape_keys_array(obj): a string[] of a
// static object's own keys — its layout row's, and an Error's own fields
// beyond it (empty without either).
func (e *Emitter) ensureShapeKeysArray() {
	if e.usedShapeKeysArray {
		return
	}
	e.usedShapeKeysArray = true
	e.ensureShapeRuntime()
	e.ensureDynJSONC() // the object hooks: an Error's own fields beyond its layout
	e.ensureMalloc()
	// The body is shape.c's (KML_SHAPE_KEYS_ARRAY); the aggregate comes back
	// through an out-slot.
	e.emitGlobal("declare void @__kml_shape_keys_array_c(ptr, ptr)")
	e.emitGlobal(`define { ptr, i64 } @__kml_shape_keys_array(ptr %o) {
entry:
  %out = alloca { ptr, i64 }, align 8
  call void @__kml_shape_keys_array_c(ptr %o, ptr %out)
  %r = load { ptr, i64 }, ptr %out, align 8
  ret { ptr, i64 } %r
}`)
}

// emitRecordSplit runs static(v) when the object v (of a structural type)
// has that type's layout, and dyn on its box otherwise — a whole-object
// operation (console.log, JSON.stringify, Object.keys) on another layout
// reads it as the object it is. Both results are converted to resTy.
func (e *Emitter) emitRecordSplit(v Value, resTy Type, static func(Value) (Value, error), dyn func(Value) (Value, error)) (Value, error) {
	foreign := e.emitRecordForeign(v.Ref, v.Ty)
	// Properties added through `any` are the object's too (ADR-01212).
	e.usedObjExtra = true
	e.extraTypes = append(e.extraTypes, v.Ty)
	has, either := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_obj_has_extra(ptr %s)", has, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, foreign, has))
	foreign = either
	slotIR := resTy.IR
	if resTy.IsArray {
		slotIR = "{ptr, i64}"
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, slotIR))
	dynL, statL, doneL := e.freshLabel("recsplit.dyn"), e.freshLabel("recsplit.static"), e.freshLabel("recsplit.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", foreign, dynL, statL))
	e.emitLabel(dynL)
	e.noteBoxedLayout(v.Ty)
	d, err := dyn(Value{Ref: e.emitNbTagPtr(v.Ref, kmlTagObject), Ty: TypeAny})
	if err != nil {
		return Value{}, err
	}
	d = e.coerce(d, resTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", slotIR, d.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(statL)
	st, err := static(v)
	if err != nil {
		return Value{}, err
	}
	st = e.coerce(st, resTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", slotIR, st.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, slotIR, slot))
	return Value{Ref: r, Ty: resTy}, nil
}

// emitShapeValuesArray is Object.values (withKeys=false: an any[]) or
// Object.entries (withKeys: [string, any][]) of a boxed static object, from
// its layout row.
func (e *Emitter) emitShapeValuesArray(box Value, withKeys bool) Value {
	e.ensureShapeRuntime()
	e.ensureMalloc()
	_, pay := e.emitUnboxTagPayload(box)
	obj := e.emitIntToPtr(pay)
	n0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_nkeys(ptr %s)", n0, obj))
	neg, n := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, n0))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, neg, n0))
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, n))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, bytes))
	found := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", found))
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	wrote := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", wrote))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", wrote))
	condL, bodyL, endL := e.freshLabel("shvals.cond"), e.freshLabel("shvals.body"), e.freshLabel("shvals.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i, done := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", done, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, endL, bodyL))
	e.emitLabel(bodyL)
	k, v := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_shape_key(ptr %s, i64 %s)", k, obj, i))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", v, obj, k, found))
	// An omitted optional field is not listed.
	fv, has := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", fv, found))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", has, fv))
	keepL, nextL := e.freshLabel("shvals.keep"), e.freshLabel("shvals.next")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, keepL, nextL))
	e.emitLabel(keepL)
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, wrote))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", slot, data, w))
	if withKeys {
		entryTy := TupleType([]Type{TypePtr, TypeAny})
		ent := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", ent, entryTy.StructSize()))
		ks, vs := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", ks, entryTy.StructIR(), ent))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", k, ks))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", vs, entryTy.StructIR(), ent))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, vs))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ent, slot))
	} else {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, slot))
	}
	w1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", w1, w))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", w1, wrote))
	e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
	e.emitLabel(nextL)
	nx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	cnt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cnt, wrote))
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, cnt))
	ty := ArrayOf(TypeAny)
	if withKeys {
		ty = ArrayOf(TupleType([]Type{TypePtr, TypeAny}))
	}
	return Value{Ref: r1, Ty: ty}
}

// ---- properties added through `any`, seen from the static binding ----
//
// A static object can carry properties added through `any` (shape.c's
// per-object table, ADR-01212). A whole-object operation on its statically
// typed binding (console.log, JSON.stringify, Object.keys) checks for them
// through finalize-defined hooks, which answer "none" at no cost unless the
// program has a dynamic set that can add one.

// extraCandidate reports a type whose values may carry added properties.
func extraCandidate(t Type) bool {
	return hasObjHeader(t) && !t.Nullable && !t.IsError && !t.IsTuple &&
		(t.IsClass || plainRecordType(t) || emptyRecordType(t))
}

// emitExtraSplit runs static(v) when the object has no added properties
// and, for a class instance, is of exactly its static class (not a
// subclass with fields of its own), else the hook hookFn (returning resTy's
// storage) with extra arguments.
func (e *Emitter) emitExtraSplit(v Value, resTy Type, static func(Value) (Value, error), hookCall func(obj string) string) (Value, error) {
	e.usedObjExtra = true
	e.extraTypes = append(e.extraTypes, v.Ty)
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_obj_has_extra(ptr %s)", has, v.Ref))
	// A class with subclasses: an instance of one is read at its own
	// layout. Library code is the same text in every program, so it never
	// checks (whether a class has subclasses depends on the program).
	if info, ok := e.classes[v.Ty.ClassName]; ok && v.Ty.IsClass && info.TagID != 0 && len(info.Descendants) > 0 && e.inLib == "" {
		e.usedObjSubclassSplit = true
		hdr, other, any := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, v.Ref))
		masked := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", masked, hdr, kmlHdrMagicMask|kmlHdrIDMask))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", other, masked, info.TagID&(kmlHdrMagicMask|kmlHdrIDMask)))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", any, has, other))
		has = any
	}
	slotIR := resTy.IR
	if resTy.IsArray {
		slotIR = "{ptr, i64}"
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, slotIR))
	dynL, statL, doneL := e.freshLabel("extra.dyn"), e.freshLabel("extra.static"), e.freshLabel("extra.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, dynL, statL))
	e.emitLabel(dynL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = %s", r, hookCall(v.Ref)))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", slotIR, r, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(statL)
	st, err := static(v)
	if err != nil {
		return Value{}, err
	}
	st = e.coerce(st, resTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", slotIR, st.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", out, slotIR, slot))
	return Value{Ref: out, Ty: resTy}, nil
}

// emitObjExtraFinalize defines the hooks emitExtraSplit calls. Called before
// the finalize steps that decide which runtimes are linked, and again after
// the routines generated at finalize (a boxed Map's inspect routine can split
// an object value), once.
func (e *Emitter) emitObjExtraFinalize() {
	if !e.usedObjExtra || e.objExtraEmitted {
		return
	}
	e.objExtraEmitted = true
	if !e.usedObjExtensible && !e.usedObjSubclassSplit {
		// No property can be added: the static path is the whole answer.
		e.emitGlobal(`define internal i1 @__kml_obj_has_extra(ptr %o) {
entry:
  ret i1 false
}

define internal ptr @__kml_obj_inspect_dyn(ptr %o, i64 %d) {
entry:
  ret ptr null
}

define internal ptr @__kml_obj_json_dyn(ptr %o, ptr %ind, i64 %base) {
entry:
  ret ptr null
}

define internal { ptr, i64 } @__kml_obj_keys_dyn(ptr %o) {
entry:
  ret { ptr, i64 } zeroinitializer
}`)
		return
	}
	for _, t := range e.extraTypes {
		e.noteBoxedLayout(t)
	}
	e.ensureShapeRuntime()
	e.ensureDynJSONC()
	e.ensureShapeKeysArray()
	e.declareFn("__kml_obj_inspect_at", "declare ptr @__kml_obj_inspect_at(ptr, i64)")
	e.emitGlobal(`define internal i1 @__kml_obj_has_extra(ptr %o) {
entry:
  %n = call i64 @__kml_shape_expando_count(ptr %o)
  %h = icmp sgt i64 %n, 0
  ret i1 %h
}

define internal ptr @__kml_obj_inspect_dyn(ptr %o, i64 %d) {
entry:
  %s = call ptr @__kml_obj_inspect_at(ptr %o, i64 %d)
  ret ptr %s
}

define internal ptr @__kml_obj_json_dyn(ptr %o, ptr %ind, i64 %base) {
entry:
  %err = alloca i32, align 4
  %p = ptrtoint ptr %o to i64
  %s = call ptr @__kml_dynjson_stringify_at(i64 6, i64 %p, ptr %ind, i64 %base, ptr %err)
  ret ptr %s
}

define internal { ptr, i64 } @__kml_obj_keys_dyn(ptr %o) {
entry:
  %k = call { ptr, i64 } @__kml_shape_keys_array(ptr %o)
  ret { ptr, i64 } %k
}`)
}

// isStringDict reports a string-keyed dictionary type (`Record<string, V>`,
// `{ [k: string]: V }`).
func isStringDict(t Type) bool {
	return t.IsDynamicObject && t.IsMap && t.MapKey != nil && isStringTy(*t.MapKey)
}

// emitBoxedObjToDict converts a boxed value to the dictionary type target:
// a static object (tag 6) becomes a dictionary of its row's present keys, as
// emitObjectToDict does for a statically known one; anything else converts
// as coerce does.
func (e *Emitter) emitBoxedObjToDict(box Value, target Type) Value {
	e.ensureMapStrHelpers()
	e.ensureShapeRuntime()
	valTy := TypeAny
	if target.MapVal != nil {
		valTy = *target.MapVal
	}
	tag, pay := e.emitUnboxTagPayload(box)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	objL, otherL, doneL := e.freshLabel("box2dict.obj"), e.freshLabel("box2dict.other"), e.freshLabel("box2dict.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, otherL))
	e.emitLabel(objL)
	obj := e.emitIntToPtr(pay)
	// A dictionary boxes as its own map, which has no header: itself.
	w, mk, isHdr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, obj))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", mk, w, kmlHdrMagicMask|hostTypeIDFlag))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isHdr, mk, kmlHdrMagic))
	staticL, selfL := e.freshLabel("box2dict.static"), e.freshLabel("box2dict.self")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isHdr, staticL, selfL))
	e.emitLabel(selfL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", obj, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(staticL)
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", m))
	n0, n, neg := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_nkeys(ptr %s)", n0, obj))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, n0))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, neg, n0))
	found := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", found))
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	condL, bodyL, keepL, nextL, endL := e.freshLabel("box2dict.cond"), e.freshLabel("box2dict.body"), e.freshLabel("box2dict.keep"), e.freshLabel("box2dict.next"), e.freshLabel("box2dict.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i, c := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", c, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, bodyL, endL))
	e.emitLabel(bodyL)
	k, fv, has, fb := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_shape_key(ptr %s, i64 %s)", k, obj, i))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", fv, obj, k, found))
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", fb, found))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", has, fb))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, keepL, nextL))
	e.emitLabel(keepL)
	ev := Value{Ref: fv, Ty: TypeAny}
	if !valTy.IsDynamic {
		ev = e.coerce(ev, valTy)
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", m, k, e.valueToMapVal(ev, valTy)))
	e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
	e.emitLabel(nextL)
	nx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(otherL)
	// A dynamic object (a bag): a dictionary of its own enumerable keys.
	e.ensureDynObj()
	bagL, hostL := e.freshLabel("box2dict.bag"), e.freshLabel("box2dict.host")
	isBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, bagL, hostL))
	e.emitLabel(bagL)
	bag := e.emitIntToPtr(pay)
	bm, keys, kp, kn := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", bm))
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_dynobj_keys_enum(ptr %s)", keys, bag))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", kp, keys))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", kn, keys))
	bidx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", bidx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", bidx))
	bcondL, bbodyL, bendL := e.freshLabel("box2dict.bcond"), e.freshLabel("box2dict.bbody"), e.freshLabel("box2dict.bend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", bcondL))
	e.emitLabel(bcondL)
	bi, bc := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", bi, bidx))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", bc, bi, kn))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bc, bbodyL, bendL))
	e.emitLabel(bbodyL)
	kslot, bk, bv := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kslot, kp, bi))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bk, kslot))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", bv, bag, bk))
	bev := Value{Ref: bv, Ty: TypeAny}
	if !valTy.IsDynamic {
		bev = e.coerce(bev, valTy)
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", bm, bk, e.valueToMapVal(bev, valTy)))
	bnx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", bnx, bi))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bnx, bidx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", bcondL))
	e.emitLabel(bendL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", bm, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(hostL)
	o := e.emitUnboxHost(box, target)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", o.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
	return Value{Ref: out, Ty: target}
}
