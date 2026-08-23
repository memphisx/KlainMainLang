// emit_blob.go — Blob (TDD-00102): an immutable binary value with a MIME
// type. Runtime representation: a ptr to a hidden heap header
// { i64 size, ptr data, ptr type }, the same hidden-struct convention
// ArrayBuffer/DataView already use. Data is one contiguous buffer,
// materialized eagerly at construction (a Blob is immutable — no rope).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

const blobStructIR = "{ i64, ptr, ptr }"

// blobPart is one evaluated constructor part: its byte length and data
// pointer registers, plus whether the data pointer may be null (empty blob).
type blobPart struct {
	lenRef  string
	dataRef string
}

// emitBlobHeader allocates and fills a Blob header around already-computed
// (size, data, type) registers.
func (e *Emitter) emitBlobHeader(sizeRef, dataRef, typeRef string) string {
	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", hdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sizeRef, hdr))
	s1 := e.freshReg()
	s2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", s1, blobStructIR, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataRef, s1))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", s2, blobStructIR, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", typeRef, s2))
	return hdr
}

// emitBlobSizeData loads (size, data) from a Blob or ArrayBuffer header —
// both start { i64 length, ptr data }.
func (e *Emitter) emitBlobSizeData(hdrRef string) (sizeRef, dataRef string) {
	size := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", size, hdrRef))
	slot := e.freshReg()
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", slot, hdrRef))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, slot))
	return size, data
}

// emitNewBlobExpression implements `new Blob(parts?, { type }?)`. parts must
// be an inline array literal (the same array-literal-position restriction
// TypedArray construction has); each element may be a string, TypedArray,
// ArrayBuffer, or another Blob.
func (e *Emitter) emitNewBlobExpression(ex *ast.NewBlobExpression) (Value, error) {
	pos := ex.GetPos()

	// A user-declared `class Blob` shadows the builtin (the parser can't
	// know about classes; codegen can) — rebuild the generic construction.
	if gen := e.blobShadowedByClass(ex); gen != nil {
		return e.emitExpr(gen)
	}

	// The { type } option: read from an inline object literal (the fetch-init
	// shape); stored as-is (no spec lowercasing — disclosed caveat).
	typeRef := e.internString("")
	if ex.Options != nil {
		lit, ok := ex.Options.(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: new Blob(...)'s options must be an inline object literal (e.g. { type: \"text/plain\" })", pos.Line, pos.Col)
		}
		for _, prop := range lit.Properties {
			if prop.Key != "type" {
				return Value{}, fmt.Errorf("%d:%d: unsupported Blob option '%s' (only 'type' is supported)", pos.Line, pos.Col, prop.Key)
			}
			tv, err := e.emitExpr(prop.Value)
			if err != nil {
				return Value{}, err
			}
			if !isStringTy(tv.Ty) {
				return Value{}, fmt.Errorf("%d:%d: Blob's 'type' option must be a string", pos.Line, pos.Col)
			}
			typeRef = tv.Ref
		}
	}

	if ex.Parts == nil {
		hdr := e.emitBlobHeader("0", "null", typeRef)
		return Value{Ref: hdr, Ty: BlobType()}, nil
	}
	lit, ok := ex.Parts.(*ast.ArrayLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: new Blob(...)'s parts must be an inline array literal (e.g. [\"a\", bytes])", pos.Line, pos.Col)
	}

	// Pass 1: evaluate every part exactly once, collecting (byteLen, data).
	parts := make([]blobPart, 0, len(lit.Elements))
	for _, elem := range lit.Elements {
		if _, isSpread := elem.(*ast.SpreadElement); isSpread {
			return Value{}, fmt.Errorf("%d:%d: spread elements in a Blob parts list are not supported", elem.GetPos().Line, elem.GetPos().Col)
		}
		elemTyStatic := e.inferExprType(elem)
		switch {
		case elemTyStatic.IsBlob, elemTyStatic.IsArrayBuffer:
			v, err := e.emitExpr(elem)
			if err != nil {
				return Value{}, err
			}
			size, data := e.emitBlobSizeData(v.Ref)
			parts = append(parts, blobPart{lenRef: size, dataRef: data})
		case elemTyStatic.IsArray:
			ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(elem, elem.GetPos())
			if err != nil {
				return Value{}, err
			}
			if !elemTyStatic.IsTypedArray {
				return Value{}, fmt.Errorf("%d:%d: a Blob part must be a string, TypedArray, ArrayBuffer, or Blob (got a plain array)", elem.GetPos().Line, elem.GetPos().Col)
			}
			byteLen := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteLen, lenReg, elemTy.Align()))
			parts = append(parts, blobPart{lenRef: byteLen, dataRef: ptrReg})
		default:
			v, err := e.emitExpr(elem)
			if err != nil {
				return Value{}, err
			}
			if !isStringTy(v.Ty) || v.Ty.IsDataView {
				return Value{}, fmt.Errorf("%d:%d: a Blob part must be a string, TypedArray, ArrayBuffer, or Blob", elem.GetPos().Line, elem.GetPos().Col)
			}
			e.ensureStrlen()
			l := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", l, v.Ref))
			parts = append(parts, blobPart{lenRef: l, dataRef: v.Ref})
		}
	}

	// Sum sizes, allocate, memcpy each part at its running offset.
	total := "0"
	for _, p := range parts {
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, total, p.lenRef))
		total = next
	}
	e.ensureMalloc()
	e.ensureMemcpy()
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, total))
	offset := "0"
	for _, p := range parts {
		dst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dst, data, offset))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, p.dataRef, p.lenRef))
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, offset, p.lenRef))
		offset = next
	}

	hdr := e.emitBlobHeader(total, data, typeRef)
	return Value{Ref: hdr, Ty: BlobType()}, nil
}

// blobShadowedByClass returns the equivalent generic `new Blob(args)` node
// when a user class named Blob is registered, nil otherwise.
func (e *Emitter) blobShadowedByClass(ex *ast.NewBlobExpression) ast.Expression {
	if _, ok := e.classes["Blob"]; !ok {
		return nil
	}
	var args []ast.Expression
	if ex.Parts != nil {
		args = append(args, ex.Parts)
	}
	if ex.Options != nil {
		args = append(args, ex.Options)
	}
	return ast.NewNewExpression("Blob", args, ex.GetPos())
}

// emitBlobProp reads .size / .type.
func (e *Emitter) emitBlobProp(blobVal Value, prop string, pos ast.Pos) (Value, error) {
	switch prop {
	case "size":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, blobVal.Ref))
		return Value{Ref: r, Ty: TypeI64}, nil
	case "type":
		slot := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", slot, blobStructIR, blobVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Blob property '%s'", pos.Line, pos.Col, prop)
}

// emitBlobCall dispatches blob.<method>(...). All results are copies — the
// Blob itself is immutable. .arrayBuffer()/.bytes()/.text() return
// already-resolved values (awaiting them is the non-promise identity
// pass-through, the same shape Response's body readers use).
func (e *Emitter) emitBlobCall(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	blobVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	size, data := e.emitBlobSizeData(blobVal.Ref)

	switch method {
	case "slice":
		if len(args) > 3 {
			return Value{}, fmt.Errorf("%d:%d: Blob.slice takes 0–3 arguments (start?, end?, contentType?)", pos.Line, pos.Col)
		}
		startN := "0"
		if len(args) >= 1 {
			sr, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			startN = e.emitNormalizeSliceIdx(e.coerce(sr, TypeI64).Ref, size)
		}
		endN := size
		if len(args) >= 2 {
			er, err := e.emitExpr(args[1])
			if err != nil {
				return Value{}, err
			}
			endN = e.emitNormalizeSliceIdx(e.coerce(er, TypeI64).Ref, size)
		}
		// Spec: the slice's type is the contentType argument, or "" —
		// never inherited from the receiver.
		typeRef := e.internString("")
		if len(args) == 3 {
			tv, err := e.emitExpr(args[2])
			if err != nil {
				return Value{}, err
			}
			if !isStringTy(tv.Ty) {
				return Value{}, fmt.Errorf("%d:%d: Blob.slice's contentType must be a string", pos.Line, pos.Col)
			}
			typeRef = tv.Ref
		}
		rawLen := e.freshReg()
		isNeg := e.freshReg()
		newLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, rawLen))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", newLen, isNeg, rawLen))
		e.ensureMalloc()
		e.ensureMemcpy()
		newData := e.freshReg()
		src := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newData, newLen))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", src, data, startN))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newData, src, newLen))
		hdr := e.emitBlobHeader(newLen, newData, typeRef)
		return Value{Ref: hdr, Ty: BlobType()}, nil

	case "arrayBuffer":
		copyRef := e.emitBlobCopyData(size, data)
		abData := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", abData))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", size, abData))
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", slot, abData))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", copyRef, slot))
		return Value{Ref: abData, Ty: ArrayBufferType()}, nil

	case "bytes":
		copyRef := e.emitBlobCopyData(size, data)
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, copyRef))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, size))
		return Value{Ref: r1, Ty: TypedArrayType("uint8")}, nil

	case "text":
		// Copy + NUL-terminate (an embedded NUL truncates the string — the
		// same caveat every string boundary in this compiler has).
		e.ensureMalloc()
		e.ensureMemcpy()
		buf := e.freshReg()
		n1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", n1, size))
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, n1))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, data, size))
		nulSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nulSlot, buf, size))
		e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nulSlot))
		return Value{Ref: buf, Ty: TypePtr}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Blob method '%s' (slice/arrayBuffer/bytes/text; .stream() is not supported)", pos.Line, pos.Col, method)
}

// emitBlobCopyData mallocs a copy of size bytes at data (Blob results are
// copies — the Blob is immutable, its consumers' results are mutable).
func (e *Emitter) emitBlobCopyData(sizeRef, dataRef string) string {
	e.ensureMalloc()
	e.ensureMemcpy()
	c := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", c, sizeRef))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", c, dataRef, sizeRef))
	return c
}
