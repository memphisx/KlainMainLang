package llvm

import (
	_ "embed"
	"fmt"
	"strings"
)

// keyorder_c.go — an object's own-key order when it is not its layout's
// (keyordersrc/keyorder.c): the order slot after the presence word, null
// for the layout's own order.

//go:embed keyordersrc/keyorder.c
var keyOrderSource string

// ensureKeyOrder declares the order runtime once and links keyorder.c.
func (e *Emitter) ensureKeyOrder() {
	if e.usedKeyOrder {
		return
	}
	e.usedKeyOrder = true
	e.emitGlobal(`declare void @__kml_keyorder_add(ptr, i64, i64, ptr)`)
	e.emitGlobal(`declare void @__kml_keyorder_del(ptr, i64)`)
	e.emitGlobal(`declare ptr @__kml_keyorder_copy(ptr)`)
	e.emitGlobal(`declare i64 @__kml_keyorder_perm(ptr, ptr, i64, i64, ptr)`)
	e.emitGlobal(`declare i64 @__kml_keyorder_ranks(ptr, ptr, i64, i64, ptr)`)
	e.emitGlobal(`declare ptr @__kml_keyorder_new()`)
	e.emitGlobal(`declare void @__kml_keyorder_push(ptr, i64)`)
	e.emitGlobal(`declare void @__kml_keyorder_push_src(ptr, ptr, ptr, i64, i64, ptr)`)
}

// keyOrderSeq is ty's layout order as a constant block: [count, bit, …],
// its fields' presence bits in ES key order (integer-like keys first), and
// the mask of the integer-like ones.
func (e *Emitter) keyOrderSeq(ty Type) (string, int64) {
	var bits []int
	var ints int64
	for _, f := range esOrderedFields(ty.VisibleFields()) {
		b := ty.presenceBit(f.Name)
		if b < 0 {
			continue
		}
		bits = append(bits, b)
		if _, ok := isArrayIndexName(f.Name); ok {
			ints |= 1 << uint(b)
		}
	}
	return e.keyOrderBlock(bits), ints
}

// keyOrderBlock is a constant order block listing bits.
func (e *Emitter) keyOrderBlock(bits []int) string {
	parts := []string{fmt.Sprintf("i8 %d", len(bits))}
	for _, b := range bits {
		parts = append(parts, fmt.Sprintf("i8 %d", b))
	}
	key := strings.Join(parts, ",")
	if e.keyOrderBlocks == nil {
		e.keyOrderBlocks = map[string]string{}
	}
	if g, ok := e.keyOrderBlocks[key]; ok {
		return g
	}
	g := contentSymbol("@__kml_keyorder.", key)
	e.keyOrderBlocks[key] = g
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [%d x i8] [%s]", g, len(bits)+1, key))
	return g
}

// keyOrderSlot addresses obj's order slot for reading: through a
// structural view only an object of exactly the view's layout has it there,
// and any other reads a constant null slot (its layout's own order).
func (e *Emitter) keyOrderSlot(obj Value) string {
	if !isRecordView(obj.Ty) {
		return e.keyOrderGep(obj.Ref, obj.Ty)
	}
	h, own, g, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", h, obj.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", own, e.headerIdentity(h), e.objHeaderWord(obj.Ty)))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, obj.Ty.StructIR(), obj.Ref, obj.Ty.orderIndex()))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr @__kml_keyorder_none", r, own, g))
	if !e.keyOrderNone {
		e.keyOrderNone = true
		e.emitGlobal("@__kml_keyorder_none = private constant ptr null")
	}
	return r
}

// keyOrderGep addresses objTy's order slot in objRef.
func (e *Emitter) keyOrderGep(objRef string, objTy Type) string {
	g := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, objTy.StructIR(), objRef, objTy.orderIndex()))
	return g
}

// emitKeyOrderInit gives a new object of objTy, created with the keys
// names lists in that order (a literal's), its order block when that is not
// the layout's.
func (e *Emitter) emitKeyOrderInit(objRef string, objTy Type, names []string) {
	if objTy.orderIndex() < 0 {
		return
	}
	var bits []int
	for _, n := range names {
		if b := objTy.presenceBit(n); b >= 0 {
			bits = append(bits, b)
		}
	}
	// The layout's own order for the same keys needs no block.
	var mine []int
	have := map[int]bool{}
	for _, b := range bits {
		have[b] = true
	}
	ints := map[int]bool{}
	for _, f := range esOrderedFields(objTy.VisibleFields()) {
		b := objTy.presenceBit(f.Name)
		if b >= 0 && have[b] {
			mine = append(mine, b)
			if _, ok := isArrayIndexName(f.Name); ok {
				ints[b] = true
			}
		}
	}
	var strBits, strMine []int
	for _, b := range bits {
		if !ints[b] {
			strBits = append(strBits, b)
		}
	}
	for _, b := range mine {
		if !ints[b] {
			strMine = append(strMine, b)
		}
	}
	same := len(strBits) == len(strMine)
	for i := 0; same && i < len(strBits); i++ {
		same = strBits[i] == strMine[i]
	}
	if same {
		return
	}
	e.ensureKeyOrder()
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_keyorder_copy(ptr %s)", cp, e.keyOrderBlock(strBits)))
	e.heapStore("ptr", cp, e.keyOrderGep(objRef, objTy), 8)
}

// keyOrdered collects a reader's per-key results (a rendered entry, a key
// string, a value) by presence bit, to emit them in the object's key order.
// A zero keyOrdered (a layout with no order slot, or a structural view,
// whose object may have another layout) collects nothing: put reports false
// and the reader emits in the layout's order as it goes.
type keyOrdered struct {
	e       *Emitter
	obj     Value
	slots   string // [64 x ptr], by presence bit
	present string // i64 mask of the bits put
	extra   string // [n x ptr], keys without a bit, in the layout's order
	nextra  int
	on      bool
}

func (e *Emitter) beginKeyOrdered(obj Value, nfields int) *keyOrdered {
	k := &keyOrdered{e: e, obj: obj}
	if obj.Ty.orderIndex() < 0 || obj.Ty.Nullable() {
		return k
	}
	k.on = true
	e.ensureKeyOrder()
	k.slots, k.present, k.extra = e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [64 x ptr], align 8", k.slots))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", k.present))
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x ptr], align 8", k.extra, nfields+1))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", k.present))
	for i := 0; i <= nfields; i++ {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x ptr], ptr %s, i64 0, i64 %d", g, nfields+1, k.extra, i))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", g))
	}
	return k
}

// put records field name's result v (a ptr); false when not collecting.
func (k *keyOrdered) put(name string, v string) bool {
	if !k.on {
		return false
	}
	e := k.e
	// A symbol key lists after every string key, in the layout's order.
	if b := k.obj.Ty.presenceBit(name); b >= 0 && !strings.HasPrefix(name, symbolFieldPrefix) {
		g, m, n := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [64 x ptr], ptr %s, i64 0, i64 %d", g, k.slots, b))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v, g))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", m, k.present))
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", n, m, uint64(1)<<uint(b)))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, k.present))
		return true
	}
	g := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", g, k.extra, k.nextra))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v, g))
	k.nextra++
	return true
}

// each emits emit(v) for every result put, in the object's key order, then
// the keys without a bit.
func (k *keyOrdered) each(emit func(v string)) {
	if !k.on {
		return
	}
	e := k.e
	seq, ints := e.keyOrderSeq(k.obj.Ty)
	order, mask, buf, n := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", order, e.keyOrderSlot(k.obj)))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", mask, k.present))
	if e.propAttrsUsed() {
		// A non-enumerable key is not rendered.
		for _, f := range k.obj.Ty.VisibleFields() {
			b := k.obj.Ty.presenceBit(f.Name)
			if b < 0 {
				continue
			}
			ne := e.emitPropAttrTest(k.obj.Ref, f.Name, pattrNotEnumerable)
			z, sh, nm, m2 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, ne))
			e.emitInstr(fmt.Sprintf("%s = shl i64 %s, %d", sh, z, b))
			e.emitInstr(fmt.Sprintf("%s = xor i64 %s, -1", nm, sh))
			e.emitInstr(fmt.Sprintf("%s = and i64 %s, %s", m2, mask, nm))
			mask = m2
		}
	}
	e.emitAlloca(fmt.Sprintf("%s = alloca [64 x i8], align 1", buf))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_keyorder_perm(ptr %s, ptr %s, i64 %d, i64 %s, ptr %s)", n, order, seq, ints, mask, buf))
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	condL, bodyL, endL := e.freshLabel("keyorder.cond"), e.freshLabel("keyorder.body"), e.freshLabel("keyorder.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i, more := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
	e.emitLabel(bodyL)
	bg, b8, b64, sg, v := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", bg, buf, i))
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", b8, bg))
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", b64, b8))
	e.emitInstr(fmt.Sprintf("%s = getelementptr [64 x ptr], ptr %s, i64 0, i64 %s", sg, k.slots, b64))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, sg))
	emit(v)
	nx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	for j := 0; j < k.nextra; j++ {
		g, v := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", g, k.extra, j))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, g))
		hasL, nextL := e.freshLabel("keyorder.extra"), e.freshLabel("keyorder.extranext")
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nextL, hasL))
		e.emitLabel(hasL)
		emit(v)
		e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
		e.emitLabel(nextL)
	}
}

// keyRanks gives a reader that writes each present key's result at an
// index (Object.keys/values/entries, for…in) the index for each field, in
// obj's key order: a first pass tests every field's presence, then
// keyorder.c ranks them. rank(f) emits f's index (call it where f is known
// present); count() emits the number of present keys, once every rank was
// taken. ok is false for a layout with no order slot.
func (e *Emitter) keyRanks(obj Value, fields []Field) (rank func(f Field) string, count func() string, ok bool) {
	if obj.Ty.orderIndex() < 0 || obj.Ty.Nullable() {
		return nil, nil, false
	}
	e.ensureKeyOrder()
	mask := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", mask))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", mask))
	for _, f := range fields {
		b := obj.Ty.presenceBit(f.Name)
		if b < 0 {
			continue
		}
		present, _ := e.emitFieldPresent(obj.Ref, obj.Ty, f)
		present = e.andEnumerable(present, obj.Ref, f.Name) // a non-enumerable key is not listed
		z, sh, m, n := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, present))
		e.emitInstr(fmt.Sprintf("%s = shl i64 %s, %d", sh, z, b))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", m, mask))
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", n, m, sh))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, mask))
	}
	seq, ints := e.keyOrderSeq(obj.Ty)
	order, m, buf, total := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", order, e.keyOrderSlot(obj)))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", m, mask))
	e.emitAlloca(fmt.Sprintf("%s = alloca [64 x i8], align 1", buf))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_keyorder_ranks(ptr %s, ptr %s, i64 %d, i64 %s, ptr %s)", total, order, seq, ints, m, buf))
	// Keys without a bit follow, in the layout's order.
	next := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", next))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", total, next))
	rank = func(f Field) string {
		if b := obj.Ty.presenceBit(f.Name); b >= 0 {
			g, r8, r := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr [64 x i8], ptr %s, i64 0, i64 %d", g, buf, b))
			e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", r8, g))
			e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", r, r8))
			return r
		}
		cur, nx := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, next))
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, cur))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, next))
		return cur
	}
	count = func() string {
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", c, next))
		return c
	}
	return rank, count, true
}

// keysAtRuntime reports whether ty's keys are decided at run time: a field
// that may hold undefined, or a layout recording presence and order.
func keysAtRuntime(ty Type, fields []Field) bool {
	return hasSkippableField(fields) || ty.presenceIndex() >= 0
}

// keyOrderNames is a constant [64 x ptr]: ty's field name for each
// presence bit, null for none.
func (e *Emitter) keyOrderNames(ty Type) string {
	names := make([]string, 64)
	for i := range names {
		names[i] = "ptr null"
	}
	var key []string
	for _, f := range ty.VisibleFields() {
		if b := ty.presenceBit(f.Name); b >= 0 {
			names[b] = "ptr " + e.internString(f.Name)
			key = append(key, fmt.Sprintf("%d=%s", b, f.Name))
		}
	}
	k := "names:" + strings.Join(key, ",")
	if e.keyOrderBlocks == nil {
		e.keyOrderBlocks = map[string]string{}
	}
	if g, ok := e.keyOrderBlocks[k]; ok {
		return g
	}
	g := contentSymbol("@__kml_keynames.", k)
	e.keyOrderBlocks[k] = g
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [64 x ptr] [%s]", g, strings.Join(names, ", ")))
	return g
}

// emitKeyOrderJSON gives an object of objTy projected from JSON node its
// keys' order: the input's.
func (e *Emitter) emitKeyOrderJSON(objRef string, objTy Type, node string) {
	if objTy.orderIndex() < 0 {
		return
	}
	seq, ints := e.keyOrderSeq(objTy)
	e.emitInstr(fmt.Sprintf("call void @__kml_keyorder_json(ptr %s, ptr %s, ptr %s, ptr %s, i64 %d)", e.keyOrderGep(objRef, objTy), node, e.keyOrderNames(objTy), seq, ints))
}

// keyOrderMap is a constant [64 x i8]: for each presence bit of src, the
// bit of the field of the same name in dst, 255 for none.
func (e *Emitter) keyOrderMap(src, dst Type) string {
	m := make([]string, 64)
	for i := range m {
		m[i] = "i8 -1"
	}
	for _, f := range src.VisibleFields() {
		sb, db := src.presenceBit(f.Name), dst.presenceBit(f.Name)
		if sb >= 0 && db >= 0 {
			m[sb] = fmt.Sprintf("i8 %d", db)
		}
	}
	k := "map:" + strings.Join(m, ",")
	if e.keyOrderBlocks == nil {
		e.keyOrderBlocks = map[string]string{}
	}
	if g, ok := e.keyOrderBlocks[k]; ok {
		return g
	}
	g := contentSymbol("@__kml_keymap.", k)
	e.keyOrderBlocks[k] = g
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [64 x i8] [%s]", g, strings.Join(m, ", ")))
	return g
}
