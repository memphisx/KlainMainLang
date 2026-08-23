package llvm

// emit_dataview.go — DataView over an ArrayBuffer (TDD-00018's family):
// `new DataView(buffer, byteOffset?, byteLength?)`, the full
// getInt/Uint{8,16,32} / getFloat{32,64} / set* method set with per-call
// endianness, and the byteLength/byteOffset/buffer properties.
//
// Representation: a ptr to a hidden 4-word heap struct
// { ptr data, i64 byteLength, i64 byteOffset, ptr bufHdr } — data is the
// buffer's base already advanced by byteOffset, bufHdr the owning
// ArrayBuffer's own hidden header (so `.buffer` returns the identical
// ArrayBuffer value, aliasing writes visible both ways).
//
// Endianness: DataView's per-call littleEndian flag defaults to FALSE
// (big-endian, per spec). The host targets here are little-endian
// (arm64/x86-64), so a big-endian access byte-swaps via the llvm.bswap
// intrinsics; the flag is a runtime i1, folded with a select.

import (
	"KlainMainLang/ast"
	"fmt"
)

const dataViewStructIR = "{ ptr, i64, i64, ptr }"

func (e *Emitter) emitNewDataViewExpression(ex *ast.NewDataViewExpression) (Value, error) {
	bufVal, err := e.emitExpr(ex.Buffer)
	if err != nil {
		return Value{}, err
	}
	if !bufVal.Ty.IsArrayBuffer {
		return Value{}, fmt.Errorf("%d:%d: new DataView(...) requires an ArrayBuffer first argument", ex.GetPos().Line, ex.GetPos().Col)
	}
	bufLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", bufLen, bufVal.Ref))
	bufDataSlot := e.freshReg()
	bufData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", bufDataSlot, bufVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufData, bufDataSlot))

	offRef := "0"
	if ex.ByteOffset != nil {
		offVal, err := e.emitExpr(ex.ByteOffset)
		if err != nil {
			return Value{}, err
		}
		offRef = e.coerce(offVal, TypeI64).Ref
	}
	var lenRef string
	if ex.ByteLength != nil {
		lenVal, err := e.emitExpr(ex.ByteLength)
		if err != nil {
			return Value{}, err
		}
		lenRef = e.coerce(lenVal, TypeI64).Ref
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", r, bufLen, offRef))
		lenRef = r
	}

	// Construction-time range check (spec: RangeError): 0 <= offset,
	// 0 <= length, offset + length <= buffer.byteLength.
	end := e.freshReg()
	offNeg := e.freshReg()
	lenNeg := e.freshReg()
	tooBig := e.freshReg()
	bad1 := e.freshReg()
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", end, offRef, lenRef))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", offNeg, offRef))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", lenNeg, lenRef))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, end, bufLen))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad1, offNeg, lenNeg))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, bad1, tooBig))
	badL := e.freshLabel("dv.ctor.bad")
	okL := e.freshLabel("dv.ctor.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitInternalThrow(e.internString("RangeError: Start offset or length is outside the bounds of the buffer"))
	e.emitLabel(okL)

	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", data, bufData, offRef))

	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", data, hdr))
	s1 := e.freshReg()
	s2 := e.freshReg()
	s3 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", s1, dataViewStructIR, hdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenRef, s1))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", s2, dataViewStructIR, hdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", offRef, s2))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", s3, dataViewStructIR, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", bufVal.Ref, s3))
	return Value{Ref: hdr, Ty: DataViewType()}, nil
}

// emitDataViewProp reads .byteLength / .byteOffset / .buffer.
func (e *Emitter) emitDataViewProp(dvVal Value, prop string, pos ast.Pos) (Value, error) {
	switch prop {
	case "byteLength", "byteOffset":
		idx := 1
		if prop == "byteOffset" {
			idx = 2
		}
		slot := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, dataViewStructIR, dvVal.Ref, idx))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypeI64}, nil
	case "buffer":
		slot := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", slot, dataViewStructIR, dvVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: ArrayBufferType()}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown DataView property '%s'", pos.Line, pos.Col, prop)
}

// dataViewAccessKinds maps a DataView method name to its (byte width,
// signedness, floatness, bigint-ness) — shared by the get and set emitters.
// The BigInt kinds store a raw i64/u64 bit pattern; the language-level value
// crosses through the __kml_bigint_* ABI (get wraps, set unwraps).
var dataViewAccessKinds = map[string]struct {
	width  int
	signed bool
	float  bool
	bigint bool
}{
	"Int8": {1, true, false, false}, "Uint8": {1, false, false, false},
	"Int16": {2, true, false, false}, "Uint16": {2, false, false, false},
	"Int32": {4, true, false, false}, "Uint32": {4, false, false, false},
	"Float32": {4, true, true, false}, "Float64": {8, true, true, false},
	"BigInt64": {8, true, false, true}, "BigUint64": {8, false, false, true},
}

func (e *Emitter) ensureBswap(width int) {
	switch width {
	case 2:
		if !e.usedBswap16 {
			e.usedBswap16 = true
			e.emitGlobal("declare i16 @llvm.bswap.i16(i16)")
		}
	case 4:
		if !e.usedBswap32 {
			e.usedBswap32 = true
			e.emitGlobal("declare i32 @llvm.bswap.i32(i32)")
		}
	case 8:
		if !e.usedBswap64 {
			e.usedBswap64 = true
			e.emitGlobal("declare i64 @llvm.bswap.i64(i64)")
		}
	}
}

// emitDataViewBoundsCheckedPtr emits the shared prologue of every accessor:
// evaluate the byte offset, range-check offset ∈ [0, byteLength - width]
// (throwing the spec's RangeError otherwise), and return the element pointer.
func (e *Emitter) emitDataViewBoundsCheckedPtr(dvVal Value, offExpr ast.Expression, width int) (string, error) {
	offVal, err := e.emitExpr(offExpr)
	if err != nil {
		return "", err
	}
	// A float offset goes through ToIndex first: NaN, ±Infinity, a negative
	// value, or anything past 2^53 throws the RangeError — checked while the
	// value is still a double, since `fptosi` on a non-finite/out-of-range
	// input is poison and would corrupt the i64 bounds check below.
	if offVal.Ty.Float {
		f := e.coerce(offVal, TypeF64)
		badF := e.freshReg()
		inRange := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp oge double %s, 0.0", inRange, f.Ref))
		tooBig := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 9.007199254740991e+15", tooBig, f.Ref))
		// bad = NOT(0 <= f) OR f > 2^53 — the first is an unordered-safe
		// negation (NaN fails the ordered oge).
		notInRange := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notInRange, inRange))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", badF, notInRange, tooBig))
		badFL := e.freshLabel("dv.badfloatoff")
		okFL := e.freshLabel("dv.okfloatoff")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badF, badFL, okFL))
		e.emitLabel(badFL)
		e.emitInternalThrow(e.internString("RangeError: Offset is outside the bounds of the DataView"))
		e.emitLabel(okFL)
		conv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", conv, f.Ref))
		offVal = Value{Ref: conv, Ty: TypeI64}
	}
	off := e.coerce(offVal, TypeI64).Ref
	dataSlotLen := e.freshReg()
	byteLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", dataSlotLen, dataViewStructIR, dvVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", byteLen, dataSlotLen))
	end := e.freshReg()
	offNeg := e.freshReg()
	tooBig := e.freshReg()
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", end, off, width))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", offNeg, off))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, end, byteLen))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, offNeg, tooBig))
	badL := e.freshLabel("dv.oob")
	okL := e.freshLabel("dv.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitInternalThrow(e.internString("RangeError: Offset is outside the bounds of the DataView"))
	e.emitLabel(okL)

	data := e.freshReg()
	elemPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, dvVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", elemPtr, data, off))
	return elemPtr, nil
}

// emitDataViewMaybeSwap emits the conditional byte swap: raw is an iN
// register; littleRef is the runtime i1 littleEndian flag. The host is
// little-endian, so the value is swapped exactly when littleEndian is false.
func (e *Emitter) emitDataViewMaybeSwap(raw string, width int, littleRef string) string {
	if width == 1 {
		return raw
	}
	e.ensureBswap(width)
	bits := width * 8
	swapped := e.freshReg()
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i%d @llvm.bswap.i%d(i%d %s)", swapped, bits, bits, bits, raw))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i%d %s, i%d %s", sel, littleRef, bits, raw, bits, swapped))
	return sel
}

// emitDataViewLittleFlag evaluates the optional littleEndian argument
// (absent → false, per spec).
func (e *Emitter) emitDataViewLittleFlag(args []ast.Expression, idx int) (string, error) {
	if len(args) <= idx {
		return "false", nil
	}
	v, err := e.emitExpr(args[idx])
	if err != nil {
		return "", err
	}
	return e.emitToBool(v).Ref, nil
}

// emitDataViewGet implements dv.get<Kind>(byteOffset, littleEndian?).
func (e *Emitter) emitDataViewGet(mem *ast.MemberExpression, kind string, args []ast.Expression, pos ast.Pos) (Value, error) {
	spec := dataViewAccessKinds[kind]
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: DataView.get%s expects (byteOffset, littleEndian?)", pos.Line, pos.Col, kind)
	}
	dvVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	elemPtr, err := e.emitDataViewBoundsCheckedPtr(dvVal, args[0], spec.width)
	if err != nil {
		return Value{}, err
	}
	little, err := e.emitDataViewLittleFlag(args, 1)
	if err != nil {
		return Value{}, err
	}
	bits := spec.width * 8
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i%d, ptr %s, align 1", raw, bits, elemPtr))
	val := e.emitDataViewMaybeSwap(raw, spec.width, little)
	if spec.float {
		if spec.width == 4 {
			f32 := e.freshReg()
			f64 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast i32 %s to float", f32, val))
			e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", f64, f32))
			return Value{Ref: f64, Ty: TypeF64}, nil
		}
		f64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", f64, val))
		return Value{Ref: f64, Ty: TypeF64}, nil
	}
	if spec.bigint {
		e.ensureBigInt()
		wrap := "@__kml_bigint_from_i64"
		if !spec.signed {
			wrap = "@__kml_bigint_from_u64"
		}
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr %s(i64 %s)", h, wrap, val))
		return Value{Ref: h, Ty: BigIntType()}, nil
	}
	if spec.width == 8 {
		return Value{Ref: val, Ty: TypeI64}, nil
	}
	wide := e.freshReg()
	ext := "zext"
	if spec.signed {
		ext = "sext"
	}
	e.emitInstr(fmt.Sprintf("%s = %s i%d %s to i64", wide, ext, bits, val))
	return Value{Ref: wide, Ty: TypeI64}, nil
}

// emitDataViewSet implements dv.set<Kind>(byteOffset, value, littleEndian?).
func (e *Emitter) emitDataViewSet(mem *ast.MemberExpression, kind string, args []ast.Expression, pos ast.Pos) (Value, error) {
	spec := dataViewAccessKinds[kind]
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: DataView.set%s expects (byteOffset, value, littleEndian?)", pos.Line, pos.Col, kind)
	}
	dvVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	elemPtr, err := e.emitDataViewBoundsCheckedPtr(dvVal, args[0], spec.width)
	if err != nil {
		return Value{}, err
	}
	rawVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	little, err := e.emitDataViewLittleFlag(args, 2)
	if err != nil {
		return Value{}, err
	}
	bits := spec.width * 8
	var narrow string
	if spec.bigint {
		// Spec: setBigInt64/setBigUint64 take a BigInt, not a Number
		// (TypeError otherwise) — enforced at compile time here.
		if !rawVal.Ty.IsBigInt {
			return Value{}, fmt.Errorf("%d:%d: DataView.set%s expects a bigint value (e.g. 1n)", pos.Line, pos.Col, kind)
		}
		e.ensureBigInt()
		unwrap := "@__kml_bigint_to_i64"
		if !spec.signed {
			unwrap = "@__kml_bigint_to_u64"
		}
		narrow = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s)", narrow, unwrap, rawVal.Ref))
	} else if spec.float {
		f := e.coerce(rawVal, TypeF64)
		if spec.width == 4 {
			f32 := e.freshReg()
			narrow = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", f32, f.Ref))
			e.emitInstr(fmt.Sprintf("%s = bitcast float %s to i32", narrow, f32))
		} else {
			narrow = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", narrow, f.Ref))
		}
	} else {
		i := e.coerce(rawVal, TypeI64)
		if spec.width == 8 {
			narrow = i.Ref
		} else {
			narrow = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i%d", narrow, i.Ref, bits))
		}
	}
	val := e.emitDataViewMaybeSwap(narrow, spec.width, little)
	e.emitInstr(fmt.Sprintf("store i%d %s, ptr %s, align 1", bits, val, elemPtr))
	return Value{Ty: TypeVoid}, nil
}

// dataViewMethodKind splits "getInt16" → ("get", "Int16") for a known access
// kind; ok is false for any other name.
func dataViewMethodKind(name string) (op, kind string, ok bool) {
	for _, prefix := range []string{"get", "set"} {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			k := name[len(prefix):]
			if _, known := dataViewAccessKinds[k]; known {
				return prefix, k, true
			}
		}
	}
	return "", "", false
}
