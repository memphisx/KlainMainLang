package llvm

import "fmt"

// emit_bigint_box.go — a bigint held in an `any` (TDD-00229, a slice of
// TDD-00228's "a handle boxes as a typed heap cell"). A bigint is a bare
// runtime pointer with no header of its own, so boxing wraps it in a 16-byte
// cell { i64 magic, ptr bigint } tagged kmlTagObject; the cell is made only
// when a bigint crosses into a dynamic slot, never for static bigint code.
//
// The magic is a full 64-bit word compared for exact equality, not a flag
// bit: a plain object's field 0 can be a pointer (macOS/arm64 heap addresses
// already exceed 2^46) or a number's raw bits, so a single-bit probe would
// misfire. The value is a non-canonical NaN pattern with bits 47 and 48 clear,
// so neither the Symbol (bit 47) nor the Error (bit 48) probe can mistake a
// bigint cell for theirs; a JS number stored in a field is a double, never
// this NaN payload, and no user-space pointer reaches bit 62.
const kmlBoxedBigIntMagic int64 = 0x7FF40000B1616B16

// emitBoxBigInt boxes a bigint value; a nullable one that is null at runtime
// boxes as the null (or undefined) sentinel.
func (e *Emitter) emitBoxBigInt(v Value) Value {
	e.ensureMalloc()
	cell := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", cell))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", kmlBoxedBigIntMagic, cell))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", slot, cell))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
	tagged := e.emitNbTagPtr(cell, kmlTagObject)
	if !v.Ty.Nullable {
		return Value{Ref: tagged, Ty: TypeAny}
	}
	absent := int64(nbNull)
	if v.Ty.IsUndefined {
		absent = nbUndefined
	}
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", sel, isNull, absent, tagged))
	return Value{Ref: sel, Ty: TypeAny}
}

// emitBoxedBigIntProbe answers whether a kmlTagObject payload is a boxed
// bigint cell. Only valid under a kmlTagObject tag check; the bigint itself is
// read with emitBoxedBigIntLoad in the true branch only (a plain object may be
// a single 8-byte field, so field 1 must not be read speculatively).
func (e *Emitter) emitBoxedBigIntProbe(payloadReg string) (cellPtr, isBig string) {
	cellPtr = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", cellPtr, payloadReg))
	f0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", f0, cellPtr))
	isBig = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isBig, f0, kmlBoxedBigIntMagic))
	return cellPtr, isBig
}

// emitBoxedBigIntLoad reads the bigint out of a probed cell.
func (e *Emitter) emitBoxedBigIntLoad(cellPtr string) Value {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", slot, cellPtr))
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", b, slot))
	return Value{Ref: b, Ty: BigIntType()}
}

// ensureBoxedBigIntHooks declares the two runtime hooks the always-present
// dynamic runtime (any_eq, dynjson.c) calls on a bigint cell. They are
// *defined* at finalize (emitBoxedBigIntHooksFinalize): only then is it known
// whether the program links the bigint runtime at all — without it no cell
// can exist, and the hooks are inert stubs.
func (e *Emitter) ensureBoxedBigIntHooks() {
	if e.usedBigIntBoxHooks {
		return
	}
	e.usedBigIntBoxHooks = true
	e.ensureStrHeaderRuntime()
	// No declares: IR may call a function defined later in the module, and
	// both hooks are defined at finalize.
}

// emitBoxedBigIntHooksFinalize defines @__kml_boxed_bigint_str (decimal
// digits of a cell's bigint, or "" without a bigint runtime) and
// @__kml_boxed_bigint_eq (value equality of two cells' bigints).
func (e *Emitter) emitBoxedBigIntHooksFinalize() {
	if !e.usedBigIntBoxHooks {
		return
	}
	if e.UsesBigInt() {
		e.emitGlobal(`
define ptr @__kml_boxed_bigint_str(ptr %cell) {
entry:
  %slot = getelementptr i8, ptr %cell, i64 8
  %b = load ptr, ptr %slot, align 8
  %raw = call ptr @__kml_bigint_to_str(ptr %b, i32 10)
  %s = call ptr @__kml_str_from_cstr(ptr %raw)
  ret ptr %s
}

define i1 @__kml_boxed_bigint_eq(ptr %ca, ptr %cb) {
entry:
  %sa = getelementptr i8, ptr %ca, i64 8
  %ba = load ptr, ptr %sa, align 8
  %sb = getelementptr i8, ptr %cb, i64 8
  %bb = load ptr, ptr %sb, align 8
  %c = call i32 @__kml_bigint_cmp(ptr %ba, ptr %bb)
  %eq = icmp eq i32 %c, 0
  ret i1 %eq
}

define i1 @__kml_boxed_bigint_eq_num(ptr %cell, double %d) {
entry:
  %nan = fcmp uno double %d, %d
  br i1 %nan, label %no, label %cmp
no:
  ret i1 false
cmp:
  %slot = getelementptr i8, ptr %cell, i64 8
  %b = load ptr, ptr %slot, align 8
  %c = call i32 @__kml_bigint_cmp_double(ptr %b, double %d)
  %eq = icmp eq i32 %c, 0
  ret i1 %eq
}`)
		return
	}
	e.emitGlobal(`
define ptr @__kml_boxed_bigint_str(ptr %cell) {
entry:
  ret ptr ` + e.internString("") + `
}

define i1 @__kml_boxed_bigint_eq(ptr %ca, ptr %cb) {
entry:
  ret i1 false
}

define i1 @__kml_boxed_bigint_eq_num(ptr %cell, double %d) {
entry:
  ret i1 false
}`)
}
