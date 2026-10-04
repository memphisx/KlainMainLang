// emit_shape.go — the run-time shape of a static object (TDD-00230 phase 5).
// Every object layout carries a type id in its header word (objheader.go).
// When a program reads, writes or calls through a boxed static object
// (`(server.address() as any).port`, `anyObj.method()`), code generation
// emits the layout table: per layout, a get and a set routine generated from
// the static type — each field boxed and unboxed by the same conversions a
// typed access uses — and, for a class, its methods and accessors as
// dynamic-function records. shapesrc/shape.c finds a layout from a header.
package llvm

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

//go:embed shapesrc/shape.c
var shapeSource string

// ShapeSource returns the C runtime that looks a layout up by header.
func ShapeSource() string { return layoutHeader() + shapeSource }

// ShapeCFlags selects the optional parts of shape.c the program uses.
func (e *Emitter) ShapeCFlags() []string {
	var f []string
	if e.usedShapeSpread {
		f = append(f, "-DKML_SHAPE_SPREAD")
	}
	if e.usedDynJSONC {
		f = append(f, "-DKML_OBJ_HOOKS") // the __kml_obj_* hooks (emitObjHooksFinalize)
	}
	if e.usedShapeKeysArray {
		f = append(f, "-DKML_SHAPE_KEYS_ARRAY")
	}
	if e.usedShapeDesc {
		f = append(f, "-DKML_SHAPE_DESC")
	}
	return f
}

// UsesShapes reports whether the program reads a static object's shape at
// run time (the layout table and shape.c are then linked in).
func (e *Emitter) UsesShapes() bool { return e.usedShapeRuntime }

// ensureShapeRuntime declares the shape.c entry points and marks the program
// as needing the layout table.
func (e *Emitter) ensureShapeRuntime() {
	if e.usedShapeRuntime {
		return
	}
	e.usedShapeRuntime = true
	e.ensureNanBox()
	e.ensureUnitReg() // shape.c finds rows in the registered tables
	e.emitGlobal("declare i64 @__kml_shape_get(ptr, ptr, ptr)")
	e.emitGlobal("declare i32 @__kml_shape_set(ptr, ptr, i64)")
	e.emitGlobal("declare ptr @__kml_shape_class_name(ptr)")
	e.emitGlobal("declare i64 @__kml_shape_nkeys(ptr)")
	e.emitGlobal("declare ptr @__kml_shape_key(ptr, i64)")
	e.emitGlobal("declare void @__kml_shape_expando_set(ptr, ptr, i64)")
	e.emitGlobal("declare i64 @__kml_shape_expando_count(ptr)")
	e.emitGlobal("declare i32 @__kml_shape_expando_delete(ptr, ptr)")
}

// shapeFound values: how __kml_shape_get found a key.
const (
	shapeNoShape = -1
	shapeAbsent  = 0
	shapeOwn     = 1
	shapeMember  = 2
)

// emitShapeGet reads key (a string register) off the static object at objPtr
// through its layout: the boxed value, and the found word (see shapeFound).
func (e *Emitter) emitShapeGet(objPtr, keyRef string) (val, found string) {
	e.ensureShapeRuntime()
	fslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", fslot))
	val = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", val, objPtr, keyRef, fslot))
	found = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", found, fslot))
	return val, found
}

// shapeLayout is one row of the layout table being generated.
type shapeLayout struct {
	id    int64
	ty    Type
	class *ClassInfo
}

// emitShapeFinalize emits the layout table: a row per class and per
// registered object layout. Generating a row can register further layouts
// (a method adapter builds an object), so rows are generated until none is
// new. Called once, after the program body.
func (e *Emitter) emitShapeFinalize() {
	if !e.usedShapeRuntime {
		return
	}
	done := map[int64]bool{}
	var rows []string
	hosts := 0
	for {
		var pending []shapeLayout
		for _, l := range e.layouts {
			if id := l.id & kmlHdrIDMask; !done[id] && e.boxedLayouts[id] {
				pending = append(pending, shapeLayout{id: l.id, ty: l.ty})
			}
		}
		var classNames []string
		for name := range e.classes {
			classNames = append(classNames, name)
		}
		sort.Strings(classNames)
		for _, name := range classNames {
			info := e.classes[name]
			if info.Ty.ClassName == "" {
				continue
			}
			id := info.TagID & kmlHdrIDMask
			if done[id] || !e.boxedLayouts[id] {
				continue
			}
			pending = append(pending, shapeLayout{id: id, ty: info.Ty, class: &info})
		}
		if len(pending) == 0 && hosts == len(e.hostLayouts) {
			break
		}
		for _, l := range pending {
			id := l.id & kmlHdrIDMask
			done[id] = true
			rows = append(rows, e.emitShapeRow(id, l))
		}
		// A host box (emit_hostbox.go) has no own fields; its members are
		// its class's declaration's (emit_host_shape.go). Generating one can
		// box another host value or an object (an iterator's next() result),
		// and an object row can box a host value, so both kinds run until
		// neither adds a layout.
		for ; hosts < len(e.hostLayouts); hosts++ {
			rows = append(rows, e.emitHostShapeRow(e.hostLayouts[hosts]))
		}
	}
	for _, r := range rows {
		lit := r[strings.IndexByte(r, '|')+1:]
		e.addUnitRowLit(unitKindShape, shapeRowTy, strings.TrimPrefix(lit, shapeRowTy+" "))
	}
}

// shapeRowTy is a KmlShape row (shapesrc/shape.c).
const shapeRowTy = "{ i64, ptr, ptr, ptr, i64, ptr }"

// shapeKeys are a layout's own enumerable keys, in declaration order.
func shapeKeys(t Type) []Field {
	var out []Field
	for _, f := range t.VisibleFields() {
		if strings.HasPrefix(f.Name, "__kml_") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// emitShapeRow generates one layout's get/set routines and key list, and
// returns its table row prefixed with "id|".
func (e *Emitter) emitShapeRow(id int64, l shapeLayout) string {
	keys := shapeKeys(l.ty)
	// A promise wrapper or a generator has no own fields to show: its members
	// are the protocol methods.
	if l.ty.IsGenerator || layoutKey(l.ty) == layoutKey(promiseBoxType()) || layoutKey(l.ty) == layoutKey(arrayIterType()) {
		keys = nil
	}
	// An Error subclass's instance begins with the error's own fields, which
	// are not its enumerable properties (a boxed error reads them itself).
	if l.class != nil && l.class.IsErrorSubclass {
		keys = errorOwnLayoutKeys(l.ty)
	}
	getName := fmt.Sprintf("@__kml_shape_get_%d", id)
	setName := fmt.Sprintf("@__kml_shape_set_%d", id)
	e.emitShapeGetter(getName, l.ty, keys, l.class)
	e.emitShapeSetter(setName, l.ty, keys, l.class)
	keyRefs := make([]string, len(keys))
	for i, f := range keys {
		keyRefs[i] = "ptr " + e.internString(f.Name)
	}
	keysName := fmt.Sprintf("@__kml_shape_keys_%d", id)
	e.emitGlobal(fmt.Sprintf("%s = constant [%d x ptr] [%s]", keysName, len(keys), strings.Join(keyRefs, ", ")))
	name := "ptr null"
	if l.class != nil {
		name = "ptr " + e.internString(classSourceName(l.ty.ClassName))
	}
	return fmt.Sprintf("%d|{ i64, ptr, ptr, ptr, i64, ptr } { i64 %d, %s, ptr %s, ptr %s, i64 %d, ptr %s }", id, id, name, getName, setName, len(keys), keysName)
}

// classSourceName is a class's name as the source wrote it: a generic
// instantiation's mangled name loses its type arguments.
func classSourceName(name string) string {
	if i := strings.Index(name, "__"); i > 0 {
		return name[:i]
	}
	return name
}

// emitKeyIs branches on `strcmp(key, name) == 0`, leaving the hit block
// current; the returned label is the miss block, to resume at.
func (e *Emitter) emitKeyIs(keyReg, name string) (miss string) {
	e.ensureStrcmp()
	c := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", c, keyReg, e.internString(name)))
	eq := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, c))
	hit := e.freshLabel("shape.hit")
	miss = e.freshLabel("shape.miss")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eq, hit, miss))
	e.emitLabel(hit)
	return miss
}

// emitShapeGetter generates `i64 get(ptr obj, ptr key, ptr found)`.
func (e *Emitter) emitShapeGetter(name string, t Type, keys []Field, class *ClassInfo) {
	restore := e.beginDetachedFunc()
	structIR := t.StructIR()
	ret := func(v Value, found int) {
		bv, err := e.emitBoxValue(v)
		if err != nil {
			e.emitThrowTypeError("a property of this value can't be read through a dynamic value")
			return
		}
		e.emitInstr(fmt.Sprintf("store i32 %d, ptr %%found, align 4", found))
		e.emitTerminator(fmt.Sprintf("ret i64 %s", bv.Ref))
	}
	for _, f := range keys {
		miss := e.emitKeyIs("%key", f.Name)
		if jsonFieldSkippable(f.Ty) {
			// An omitted optional field is not a property (found stays 0).
			present, v := e.emitFieldPresent("%obj", t, f)
			hasL, absL := e.freshLabel("shape.present"), e.freshLabel("shape.absent")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, hasL, absL))
			e.emitLabel(hasL)
			ret(v, shapeOwn)
			e.emitLabel(absL)
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
			e.emitLabel(miss)
			continue
		}
		idx, fty, _ := t.FieldIndex(f.Name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%obj, i32 0, i32 %d", gep, structIR, idx))
		ret(e.loadScalarOrNullableField(gep, fty), shapeOwn)
		e.emitLabel(miss)
	}
	switch {
	case t.IsGenerator:
		e.emitGeneratorMembers(t)
	case layoutKey(t) == layoutKey(promiseBoxType()):
		e.emitPromiseBoxMembers()
	case layoutKey(t) == layoutKey(arrayIterType()):
		e.emitArrayIterMembers()
	}
	if class != nil {
		self := Value{Ref: "%obj", Ty: t}
		for _, m := range shapeMethodNames(class) {
			key, getter := shapeMemberKey(m)
			if key == "" {
				continue
			}
			miss := e.emitKeyIs("%key", key)
			if getter {
				v, err := e.emitClassCall(t, self, m, nil, ast.Pos{}, false)
				if err != nil {
					e.emitThrowTypeError("the accessor '" + key + "' can't be read through a dynamic value")
				} else {
					ret(v, shapeMember)
				}
			} else {
				rec := e.shapeMethodRecord(class, t, m)
				e.emitInstr(fmt.Sprintf("store i32 %d, ptr %%found, align 4", shapeMember))
				e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(rec, kmlTagDynFunc)))
			}
			e.emitLabel(miss)
		}
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%obj, ptr %%key, ptr %%found) {\nentry:\n%s}\n", name, body))
}

// emitShapeSetter generates `i32 set(ptr obj, ptr key, i64 value)`.
func (e *Emitter) emitShapeSetter(name string, t Type, keys []Field, class *ClassInfo) {
	restore := e.beginDetachedFunc()
	structIR := t.StructIR()
	for _, f := range keys {
		miss := e.emitKeyIs("%key", f.Name)
		idx, fty, _ := t.FieldIndex(f.Name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%obj, i32 0, i32 %d", gep, structIR, idx))
		var v Value
		switch {
		case fty.IsDynamic:
			v = Value{Ref: "%value", Ty: fty}
		case fty.IsFunc:
			cv, ok := e.emitAnyToClosure(Value{Ref: "%value", Ty: TypeAny}, fty)
			if !ok {
				e.emitTerminator("ret i32 -2")
				e.emitLabel(miss)
				continue
			}
			v = cv
		default:
			// A value of another kind than the field holds (a string into a
			// number field) is not stored in it: the caller keeps it as an
			// own property that shadows the field (shape.c).
			if fits, ok := e.emitBoxFits("%value", fty); ok {
				fitL, noL := e.freshLabel("shape.fits"), e.freshLabel("shape.nofit")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fits, fitL, noL))
				e.emitLabel(noL)
				e.emitTerminator("ret i32 0")
				e.emitLabel(fitL)
			}
			v = e.unboxArgToParam("%value", fty)
		}
		e.storeScalarOrNullableField(gep, fty, v)
		e.emitTerminator("ret i32 1")
		e.emitLabel(miss)
	}
	if class != nil {
		self := Value{Ref: "%obj", Ty: t}
		for _, m := range shapeMethodNames(class) {
			if !strings.HasPrefix(m, "__kml_set_") {
				continue
			}
			key := strings.TrimPrefix(m, "__kml_set_")
			miss := e.emitKeyIs("%key", key)
			sig := class.MethodSigs[m]
			if len(sig.ParamTypes) != 1 {
				e.emitTerminator("ret i32 -2")
				e.emitLabel(miss)
				continue
			}
			pty := sig.ParamTypes[0]
			av := e.unboxArgToParam("%value", pty)
			var slot string
			if pty.IsArray {
				slot = e.newArrayHeaderSlotFromAggregate(av)
			} else {
				slot = e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, storageIR(pty)))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(pty), av.Ref, slot))
			}
			e.define("__kml_shape_arg", Symbol{Ptr: slot, Ty: pty, NullableBoxed: isNullableScalar(pty)})
			if _, err := e.emitClassCall(t, self, m, []ast.Expression{ast.NewIdentifier("__kml_shape_arg", ast.Pos{})}, ast.Pos{}, false); err != nil {
				e.emitTerminator("ret i32 -2")
			} else {
				e.emitTerminator("ret i32 1")
			}
			e.emitLabel(miss)
		}
	}
	e.emitTerminator("ret i32 0")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i32 %s(ptr %%obj, ptr %%key, i64 %%value) {\nentry:\n%s}\n", name, body))
}

// shapeMethodNames lists a class's methods and accessors in a stable order.
func shapeMethodNames(class *ClassInfo) []string {
	var out []string
	for m := range class.MethodSigs {
		if m == "constructor" || strings.HasPrefix(m, "#") {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// shapeMemberKey maps a method-table name to its property key, and whether it
// is a getter. A setter has no read key.
func shapeMemberKey(m string) (key string, getter bool) {
	switch {
	case strings.HasPrefix(m, "__kml_get_"):
		return strings.TrimPrefix(m, "__kml_get_"), true
	case strings.HasPrefix(m, "__kml_set_"):
		return "", false
	case strings.HasPrefix(m, "__kml_"):
		return "", false
	}
	return m, false
}

// shapeMethodRecord returns the dynamic-function record for class method m —
// one static record per method, so `a.m === b.m` holds as it does for a
// method on a prototype.
func (e *Emitter) shapeMethodRecord(class *ClassInfo, t Type, m string) string {
	key := t.ClassName + "." + m
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	sig := class.MethodSigs[m]
	base := fmt.Sprintf("__kml_shape_m_%s_%s", llvmSafeSymbol(t.ClassName), llvmSafeSymbol(m))
	fn := "@" + base
	rec := "@" + base + "_rec"
	e.shapeMethodRecs[key] = rec
	arity := len(sig.ParamTypes)
	if sig.HasRest {
		arity--
	}
	e.registerFnMeta(fn, m, arity, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, arity))
	e.emitShapeMethodAdapter(fn, class, t, m, sig)
	return rec
}

// emitShapeMethodAdapter generates the dynamic-ABI adapter for a class
// method: `i64 fn(ptr env, i64 this, i64 argc, ptr argv)` unboxes the
// receiver and arguments, calls the method with as many arguments as were
// passed (so its defaults apply), and boxes the result.
func (e *Emitter) emitShapeMethodAdapter(fn string, class *ClassInfo, t Type, m string, sig FuncSig) {
	restore := e.beginDetachedFunc()
	pos := ast.Pos{}
	e.ensureNanBox()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %%this)", pay))
	selfPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", selfPtr, pay))
	self := Value{Ref: selfPtr, Ty: t}

	regular := len(sig.ParamTypes)
	if sig.HasRest {
		regular--
	}
	var args []ast.Expression
	for i := 0; i < regular; i++ {
		pty := sig.ParamTypes[i]
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, %d", have, i))
		slotp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %d", slotp, i))
		loaded := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loaded, slotp))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, loaded, nbUndefined))
		var av Value
		switch {
		case pty.IsDynamic:
			av = Value{Ref: word, Ty: pty}
		case pty.IsFunc:
			cv, ok := e.emitAnyToClosure(Value{Ref: word, Ty: TypeAny}, pty)
			if !ok {
				e.emitThrowTypeError("the method '" + m + "' can't be called through a dynamic value")
				e.shapeFinishAdapter(fn, restore)
				return
			}
			av = cv
		default:
			av = e.unboxArgToParam(word, pty)
		}
		var slot string
		if pty.IsArray {
			// An array binding is a slot holding its shared header (TDD-00127).
			slot = e.newArrayHeaderSlotFromAggregate(av)
		} else {
			slot = e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, storageIR(pty)))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(pty), av.Ref, slot))
		}
		name := fmt.Sprintf("__kml_shape_a%d", i)
		e.define(name, Symbol{Ptr: slot, Ty: pty, NullableBoxed: isNullableScalar(pty)})
		args = append(args, ast.NewIdentifier(name, pos))
	}
	if sig.HasRest {
		restTy := sig.ParamTypes[regular]
		elemTy := TypeAny
		if restTy.ElemType != nil {
			elemTy = *restTy.ElemType
		}
		rest := e.emitShapeRestArray(regular, elemTy)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rest, slot))
		e.define("__kml_shape_rest", Symbol{Ptr: slot, Ty: restTy})
		args = append(args, ast.NewSpreadElement(ast.NewIdentifier("__kml_shape_rest", pos), pos))
	}

	// The fewest arguments a call may pass: the leading required ones.
	minArgs := regular
	for minArgs > 0 && ((minArgs-1 < len(sig.Defaults) && sig.Defaults[minArgs-1] != nil) ||
		(minArgs-1 < len(sig.Optional) && sig.Optional[minArgs-1])) {
		minArgs--
	}
	call := func(n int) {
		callArgs := append([]ast.Expression(nil), args[:n]...)
		if sig.HasRest {
			callArgs = append(callArgs, args[len(args)-1])
		}
		// The record is this class's own method: called directly, as an
		// extracted method runs whatever its receiver (undefined, when the
		// function is called on its own).
		v, err := e.emitClassCall(t, self, m, callArgs, pos, true)
		if err != nil {
			e.emitThrowTypeError("the method '" + m + "' can't be called through a dynamic value")
			return
		}
		if v.Ty.IR == "" || v.Ty.IR == "void" {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
			return
		}
		bv, err := e.emitBoxValue(v)
		if err != nil {
			e.emitThrowTypeError("the result of '" + m + "' can't be held in a dynamic value")
			return
		}
		e.emitTerminator(fmt.Sprintf("ret i64 %s", bv.Ref))
	}
	for n := minArgs; n < regular; n++ {
		lt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %%argc, %d", lt, n))
		here := e.freshLabel("shape.argc")
		next := e.freshLabel("shape.argc.more")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", lt, here, next))
		e.emitLabel(here)
		call(n)
		e.emitLabel(next)
	}
	call(regular)
	e.shapeFinishAdapter(fn, restore)
}

func (e *Emitter) shapeFinishAdapter(fn string, restore func()) {
	if !e.blockDone {
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv) {\nentry:\n%s}\n", fn, body))
}

// emitShapeRestArray gathers argv[from:] (unboxed to elemTy) into an array
// header, the rest parameter's value.
func (e *Emitter) emitShapeRestArray(from int, elemTy Type) string {
	e.ensureMalloc()
	nRest := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %%argc, %d", nRest, from))
	neg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, nRest))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, neg, nRest))
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, n, elemTy.Align()))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, bytes))
	jPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", jPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", jPtr))
	condL := e.freshLabel("shrest.cond")
	bodyL := e.freshLabel("shrest.body")
	doneL := e.freshLabel("shrest.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	j := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", j, jPtr))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, j, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", src, j, from))
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %s", sp, src))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, sp))
	ev := Value{Ref: w, Ty: elemTy}
	if !elemTy.IsDynamic {
		ev = e.emitUnboxBoxToType(w, elemTy)
	}
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dst, elemTy.IR, data, j))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, ev.Ref, dst, elemTy.Align()))
	jn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", jn, j))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", jn, jPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return e.newArrayHeader(data, n)
}

// noteBoxedLayout records that a value of static type t reached a dynamic
// value, so its layout (and, for a class, every subclass's, since an
// instance of one may be what t holds) gets a row in the layout table.
// noteErrorSubclassLayouts gives every class extending Error a layout row
// (its own fields read through a boxed error), reporting whether there is
// any.
func (e *Emitter) noteErrorSubclassLayouts() bool {
	any := false
	for _, info := range e.classes {
		if info.IsErrorSubclass && info.Ty.ClassName != "" {
			if e.boxedLayouts == nil {
				e.boxedLayouts = map[int64]bool{}
			}
			e.boxedLayouts[info.TagID&kmlHdrIDMask] = true
			any = true
		}
	}
	return any
}

func (e *Emitter) noteBoxedLayout(t Type) {
	if !hasObjHeader(t) {
		return
	}
	if e.boxedLayouts == nil {
		e.boxedLayouts = map[int64]bool{}
	}
	if t.IsClass {
		info, ok := e.classes[t.ClassName]
		if !ok {
			return
		}
		e.boxedLayouts[info.TagID&kmlHdrIDMask] = true
		for _, d := range info.Descendants {
			e.boxedLayouts[e.classes[d].TagID&kmlHdrIDMask] = true
		}
		return
	}
	e.boxedLayouts[e.objHeaderWord(t)&kmlHdrIDMask] = true
}

// emitGeneratorMembers is the layout-table getter body for a generator:
// next/return/throw and its iterator method (which returns the generator).
func (e *Emitter) emitGeneratorMembers(genTy Type) {
	iter := "@@iterator"
	if genTy.GeneratorIsAsync {
		iter = "@@asyncIterator"
	}
	members := []string{"next", "return", "throw", iter}
	if !genTy.GeneratorIsAsync {
		members = append(members, e.linkedIteratorHelpers()...)
	}
	for _, m := range members {
		miss := e.emitKeyIs("%key", m)
		rec := e.generatorMethodRecord(genTy, m)
		e.emitInstr(fmt.Sprintf("store i32 %d, ptr %%found, align 4", shapeMember))
		e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(rec, kmlTagDynFunc)))
		e.emitLabel(miss)
	}
}

// generatorMethodRecord is the dynamic-function record for method m of
// generators of type genTy.
func (e *Emitter) generatorMethodRecord(genTy Type, m string) string {
	id := e.objHeaderWord(genTy) & kmlHdrIDMask
	key := fmt.Sprintf("gen%d.%s", id, m)
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	fn := fmt.Sprintf("@__kml_gen_dyn_%d_%s", id, llvmSafeSymbol(m))
	rec := fn + "_rec"
	e.shapeMethodRecs[key] = rec
	arity := 1
	if strings.HasPrefix(m, "@@") {
		arity = 0
	}
	if n, ok := iteratorHelperArity[m]; ok {
		arity = n
	}
	e.registerFnMeta(fn, m, arity, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, arity))

	restore := e.beginDetachedFunc()
	pos := ast.Pos{}
	e.ensureNanBox()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %%this)", pay))
	gen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", gen, pay))
	elemTy := TypeAny
	if genTy.GeneratorElemType != nil {
		elemTy = *genTy.GeneratorElemType
	}
	arg := func() Value {
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, 0", have))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%argv, align 8", w))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, w, nbUndefined))
		if elemTy.IsDynamic {
			return Value{Ref: word, Ty: elemTy}
		}
		return e.unboxArgToParam(word, elemTy)
	}
	var res Value
	var err error
	switch m {
	case "next":
		// The sent value is the `yield` expression's (TNext), passed boxed.
		e.ensureGeneratorRuntime()
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, 0", have))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%argv, align 8", w))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, w, nbUndefined))
		sent := Value{Ref: word, Ty: TypeAny}
		if genTy.GeneratorIsAsync {
			res = e.emitAsyncGeneratorNextCore(gen, genTy, sent)
		} else {
			res = e.emitSyncGeneratorNextCore(gen, genTy, sent)
		}
	case "return":
		res, err = e.emitGeneratorReturnByValue(gen, genTy, arg(), pos)
	case "throw":
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, 0", have))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%argv, align 8", w))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, w, nbUndefined))
		var errPtr string
		errPtr, err = e.errorPtrFromValue(Value{Ref: word, Ty: TypeAny})
		if err == nil {
			res, err = e.emitGeneratorThrowByValue(gen, genTy, errPtr, pos)
		}
	default:
		if _, helper := iteratorHelperArity[m]; helper {
			res, err = e.emitIteratorHelperCall(m)
			break
		}
		e.emitTerminator("ret i64 %this")
	}
	if !e.blockDone {
		if err != nil {
			e.emitThrowTypeError("the generator method '" + m + "' can't be called through a dynamic value")
		} else if bv, berr := e.emitBoxValue(res); berr != nil {
			e.emitThrowTypeError("the result of '" + m + "' can't be held in a dynamic value")
		} else {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", bv.Ref))
		}
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv) {\nentry:\n%s}\n", fn, body))
	return rec
}

// emitAnyToLayout converts the dynamic value v to a pointer to a static
// object of layout t (TDD-00230 P3.3 convert): the object v holds when its
// header names t (any static object, for a structural t — the checked view
// reads it), null for null/undefined, else a fresh t whose fields are v's
// properties of the same names, each converted to its field's type.
func (e *Emitter) emitAnyToLayout(v Value, t Type) string {
	fn := e.anyToLayoutFn(t)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s(i64 %s)", r, fn, v.Ref))
	return r
}

// anyToLayoutFn is the per-layout conversion routine behind emitAnyToLayout
// (a function, so a recursive layout converts through calls).
func (e *Emitter) anyToLayoutFn(t Type) string {
	id := e.objHeaderWord(t)
	if e.anyToLayoutFns == nil {
		e.anyToLayoutFns = map[int64]string{}
	}
	if fn, ok := e.anyToLayoutFns[id]; ok {
		return fn
	}
	fn := fmt.Sprintf("@__kml_any_to_layout.%d", id&kmlHdrIDMask)
	e.anyToLayoutFns[id] = fn
	restore := e.beginDetachedFunc()
	v := Value{Ref: "%v", Ty: TypeAny}
	tag, payload := e.emitUnboxTagPayload(v)
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	objL, copyL, sameL, nilL := e.freshLabel("a2l.obj"), e.freshLabel("a2l.copy"), e.freshLabel("a2l.same"), e.freshLabel("a2l.nil")
	nullish := e.freshReg()
	isNull := e.freshReg()
	isUndef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
	chkL := e.freshLabel("a2l.chk")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, nilL, chkL))
	e.emitLabel(nilL)
	e.emitTerminator("ret ptr null")
	e.emitLabel(chkL)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, copyL))
	e.emitLabel(objL)
	op := e.emitIntToPtr(payload)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", h, op))
	same := e.freshReg()
	if isRecordView(t) {
		// Any static object (a header with the magic): a structural type
		// reads another layout through the checked view (TDD-00233).
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", m, h, kmlHdrMagicMask|hostTypeIDFlag))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", same, m, kmlHdrMagic))
	} else {
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", same, h, id))
	}
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", same, sameL, copyL))
	e.emitLabel(sameL)
	e.emitTerminator(fmt.Sprintf("ret ptr %s", op))
	e.emitLabel(copyL)
	obj := e.emitObjAlloc(t)
	structIR := t.StructIR()
	for _, f := range t.UserFields() {
		fv, _ := e.emitAnyMemberOrUndefined(v, f.Name)
		idx, fty, _ := t.FieldIndex(f.Name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		var cv Value
		switch {
		case fty.IsDynamic:
			cv = fv
		case fty.IsArray:
			cv = e.emitUnboxBoxToType(fv.Ref, fty)
		default:
			cv = e.coerce(fv, fty)
		}
		e.storeScalarOrNullableField(gep, fty, cv)
	}
	e.emitTerminator(fmt.Sprintf("ret ptr %s", obj))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal ptr %s(i64 %%v) {\nentry:\n%s}\n", fn, body))
	return fn
}

// arrayIterType is an array iterator's layout: the array (boxed) and the
// next index. Its row exposes next() and [Symbol.iterator]().
func arrayIterType() Type {
	return ObjectType([]Field{{Name: "__kml_iter_arr", Ty: TypeAny}, {Name: "__kml_iter_idx", Ty: TypeI64}})
}

// iterResultType is the {value, done} object an iterator's next() returns
// through a dynamic value.
func iterResultType() Type {
	return ObjectType([]Field{{Name: "value", Ty: TypeAny}, {Name: "done", Ty: TypeBool}})
}

// emitArrayIterMembers is the layout-table getter body for an array iterator.
func (e *Emitter) emitArrayIterMembers() {
	for _, m := range append([]string{"next", "@@iterator"}, e.linkedIteratorHelpers()...) {
		miss := e.emitKeyIs("%key", m)
		rec := e.arrayIterMethodRecord(m)
		e.emitInstr(fmt.Sprintf("store i32 %d, ptr %%found, align 4", shapeMember))
		e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(rec, kmlTagDynFunc)))
		e.emitLabel(miss)
	}
}

// arrayValuesRecord is Array.prototype[Symbol.iterator] (values()) as a
// dynamic function over an array held in `any`.
func (e *Emitter) arrayValuesRecord() string {
	return e.arrayIterMethodRecord("values")
}

// arrayIterMethodRecord builds the dynamic-function records of the array
// iterator protocol: "values" (on the array: a fresh iterator), "next" /
// "@@iterator" (on the iterator), and "hostValues" (an iterable host box's
// [Symbol.iterator]: an iterator over its spread entries).
func (e *Emitter) arrayIterMethodRecord(m string) string {
	key := "ArrayIterator." + m
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	fn := "@__kml_arriter_" + llvmSafeSymbol(m)
	rec := fn + "_rec"
	e.shapeMethodRecs[key] = rec
	name := m
	if m == "@@iterator" || m == "hostValues" {
		name = "[Symbol.iterator]"
	}
	arity := iteratorHelperArity[m]
	e.registerFnMeta(fn, name, arity, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, arity))
	restore := e.beginDetachedFunc()
	itTy := arrayIterType()
	if _, helper := iteratorHelperArity[m]; helper {
		if res, err := e.emitIteratorHelperCall(m); err != nil {
			e.emitThrowTypeError("the iterator method '" + m + "' can't be called through a dynamic value")
		} else {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", res.Ref))
		}
	}
	switch m {
	case "values", "hostValues":
		src := "%this"
		if m == "hostValues" {
			src = e.emitHostIterValue("%this")
		}
		obj := e.emitObjAlloc(itTy)
		a := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", a, itTy.StructIR(), obj))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", src, a))
		bv, _ := e.emitBoxValue(Value{Ref: obj, Ty: itTy})
		e.emitTerminator(fmt.Sprintf("ret i64 %s", bv.Ref))
	case "@@iterator":
		e.emitTerminator("ret i64 %this")
	case "next":
		e.ensureNanBox()
		e.ensureSprintf()
		e.ensureMalloc()
		pay := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %%this)", pay))
		it := e.emitIntToPtr(pay)
		ap := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", ap, itTy.StructIR(), it))
		arr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", arr, ap))
		ip := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", ip, itTy.StructIR(), it))
		idx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx, ip))
		lv, _ := e.emitDynAnyMemberGetNamed(Value{Ref: arr, Ty: TypeAny}, e.internString("length"), "length", ast.Pos{})
		n := e.coerce(Value{Ref: e.emitAnyToNum(lv), Ty: TypeF64}, TypeI64)
		more := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, idx, n.Ref))
		resTy := iterResultType()
		res := e.emitObjAlloc(resTy)
		vp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", vp, resTy.StructIR(), res))
		dp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", dp, resTy.StructIR(), res))
		haveL, endL := e.freshLabel("arriter.have"), e.freshLabel("arriter.end")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, haveL, endL))
		e.emitLabel(haveL)
		k := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", k))
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", k, e.internString("%lld"), idx))
		ev, _ := e.emitDynAnyMemberGet(Value{Ref: arr, Ty: TypeAny}, k, ast.Pos{})
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ev.Ref, vp))
		e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", dp))
		ni := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", ni, idx))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ni, ip))
		b1, _ := e.emitBoxValue(Value{Ref: res, Ty: resTy})
		e.emitTerminator(fmt.Sprintf("ret i64 %s", b1.Ref))
		e.emitLabel(endL)
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, vp))
		e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", dp))
		b2, _ := e.emitBoxValue(Value{Ref: res, Ty: resTy})
		e.emitTerminator(fmt.Sprintf("ret i64 %s", b2.Ref))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv) {\nentry:\n%s}\n", fn, body))
	return rec
}

// ensureShapeSpread emits @__kml_shape_spread(bag, obj): copy a static
// object's own enumerable fields into a dynamic object (`{ ...o }` where o is
// a boxed static object). No-op for an object with no shape.
func (e *Emitter) ensureShapeSpread() {
	if e.usedShapeSpread {
		return
	}
	e.usedShapeSpread = true
	e.ensureShapeRuntime()
	e.ensureDynObj()
	e.emitGlobal("declare void @__kml_shape_spread(ptr, ptr)")
}

// ensureShapeDesc declares @__kml_shape_own_desc(obj, key): a boxed static
// object's own-property descriptor, or undefined.
func (e *Emitter) ensureShapeDesc() {
	if e.usedShapeDesc {
		return
	}
	e.usedShapeDesc = true
	e.ensureShapeRuntime()
	e.ensureDynObj()
	e.ensureFrozenSet()
	e.emitGlobal("declare i64 @__kml_shape_own_desc(ptr, ptr)")
}

// emitBoxFits is an i1: whether the boxed word holds a value of the scalar
// kind a field of type t stores (a number, a string or a boolean; null and
// undefined too when t may be absent). ok is false for a type it does not
// check.
func (e *Emitter) emitBoxFits(word string, t Type) (string, bool) {
	var tags []int
	switch {
	case t.IsObject || t.IsArray || t.IsFunc || t.IsMap || t.IsSet || t.IsDynamic:
		return "", false
	case t.IR == "i1":
		tags = []int{kmlTagBoolean}
	case isStringTy(t):
		tags = []int{kmlTagString}
	case t.Float || t.IsInteger():
		tags = []int{kmlTagFloat, kmlTagInt}
	default:
		return "", false
	}
	if t.Nullable || isNullableScalar(t) {
		tags = append(tags, kmlTagNull, kmlTagUndefined)
	}
	tag, _ := e.emitUnboxTagPayload(Value{Ref: word, Ty: TypeAny})
	acc := "false"
	for _, k := range tags {
		c, o := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", c, tag, k))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, acc, c))
		acc = o
	}
	return acc, true
}

// errorOwnLayoutKeys are an Error subclass layout's own enumerable keys: the
// error's fields are the layout's prefix, not its properties; a subclass
// field of the same name as one of them (`code`) is its own.
func errorOwnLayoutKeys(t Type) []Field {
	prefix := map[string]bool{}
	for i, f := range t.Fields {
		if i > 0 && i < len(errorObjType.Fields) {
			prefix[f.Name] = true
		}
	}
	var own []Field
	for _, f := range shapeKeys(t) {
		if !prefix[f.Name] {
			own = append(own, f)
		}
	}
	return own
}

// iteratorHelperArity is Iterator.prototype's helper methods (ES2025), each
// with its `length`. Each is the TypeScript function its IteratorObject
// declaration lowers to (lib/node/kml_iterator.ts).
var iteratorHelperArity = map[string]int{
	"map": 1, "filter": 1, "take": 1, "drop": 1, "flatMap": 1, "reduce": 1,
	"toArray": 0, "forEach": 1, "some": 1, "every": 1, "find": 1,
}

// linkedIteratorHelpers is the helper methods whose functions the program
// links (a program that names none links none), sorted.
func (e *Emitter) linkedIteratorHelpers() []string {
	var out []string
	for m := range iteratorHelperArity {
		if _, ok := e.globalLinks["__kml_Iterator_"+m]; ok {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// emitIteratorHelperCall is the body of a dynamic-function record for helper
// method m on an iterator held in `any` (a generator, an array iterator):
// the linked TypeScript function called with %this and the record's
// arguments, absent ones undefined. reduce passes its initial value only
// when one was given, which its function tells apart. The result is boxed.
func (e *Emitter) emitIteratorHelperCall(m string) (Value, error) {
	fn := e.globalLinks["__kml_Iterator_"+m]
	bind := func(name, word string) ast.Expression {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", word, slot))
		e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
		return ast.NewIdentifier(name, ast.Pos{})
	}
	argWord := func(i int) string {
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, %d", have, i))
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %d", gep, i))
		safe := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %%argv", safe, have, gep))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, safe))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, w, nbUndefined))
		return word
	}
	self := bind("__kml_ih_this", "%this")
	args := []ast.Expression{self}
	if iteratorHelperArity[m] > 0 {
		args = append(args, bind("__kml_ih_arg0", argWord(0)))
	}
	call := func(args []ast.Expression) (Value, error) {
		v, err := e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(fn, ast.Pos{}), args, ast.Pos{}))
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IR == "void" {
			return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, nil
		}
		return e.emitBoxValue(v)
	}
	if m != "reduce" {
		return call(args)
	}
	// reduce(fn) and reduce(fn, initial) differ even for an undefined
	// initial value.
	init := bind("__kml_ih_arg1", argWord(1))
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", out))
	two := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, 1", two))
	withL, withoutL, joinL := e.freshLabel("ih.init"), e.freshLabel("ih.noinit"), e.freshLabel("ih.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", two, withL, withoutL))
	for _, br := range []struct {
		label string
		args  []ast.Expression
	}{{withL, append(append([]ast.Expression{}, args...), init)}, {withoutL, args}} {
		e.emitLabel(br.label)
		v, err := call(br.args)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, out))
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	}
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, out))
	return Value{Ref: r, Ty: TypeAny}, nil
}
