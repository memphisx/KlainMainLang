package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed boxsrc/bigintbox.c
var bigintBoxSource string

//go:embed boxsrc/bigintrel.c
var bigintRelSource string

// bigintBoxDefines are the constants the boxed-bigint C files share with
// the emitter.
func bigintBoxDefines() string {
	return fmt.Sprintf("#define KML_BIGINT_MAGIC %dLL\n#define KML_NB_DOUBLE_OFFSET %dLL\n", kmlBoxedBigIntMagic, nbDoubleOffset)
}

// BigIntBoxSource is the boxed-bigint hooks' C source (boxsrc/bigintbox.c).
func BigIntBoxSource() string { return bigintBoxSource }

// BigIntRelSource is the dynamic relational comparison's C source
// (boxsrc/bigintrel.c).
func BigIntRelSource() string { return bigintBoxDefines() + bigintRelSource }

// UsesBigIntBoxC reports whether the program links the boxed-bigint hooks:
// they exist (as C) only with the bigint runtime; without it they are
// stubs defined in the module.
func (e *Emitter) UsesBigIntBoxC() bool { return e.usedBigIntBoxHooks && e.UsesBigInt() }

// UsesBigIntRelC reports whether the program links the dynamic relational
// comparison with a bigint (boxsrc/bigintrel.c).
func (e *Emitter) UsesBigIntRelC() bool { return e.usedAnyBigIntRel && e.UsesBigInt() }

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

// emitBoxedBigIntHooksFinalize declares the C hooks (boxsrc/bigintbox.c:
// decimal digits of a cell's bigint, value equality of two cells' bigints)
// or, without a bigint runtime, defines them as stubs.
func (e *Emitter) emitBoxedBigIntHooksFinalize() {
	if !e.usedBigIntBoxHooks {
		return
	}
	if e.UsesBigInt() {
		// Defined in boxsrc/bigintbox.c and bigintrel.c.
		e.emitGlobal(`declare ptr @__kml_boxed_bigint_str(ptr)
declare zeroext i1 @__kml_boxed_bigint_eq(ptr, ptr)
declare zeroext i1 @__kml_boxed_bigint_eq_num(ptr, double)`)
		if e.usedAnyBigIntRel {
			e.emitGlobal("declare i32 @__kml_any_bigint_rel(i64, i64)")
		}
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
	e.emitAnyBigIntRel(`define i32 @__kml_any_bigint_rel(i64 %a, i64 %b) {
entry:
  ret i32 0
}`)
}

// emitAnyBigIntRel defines @__kml_any_bigint_rel when a dynamic relational
// comparison used it.
func (e *Emitter) emitAnyBigIntRel(ir string) {
	if e.usedAnyBigIntRel {
		e.emitGlobal(ir)
	}
}
