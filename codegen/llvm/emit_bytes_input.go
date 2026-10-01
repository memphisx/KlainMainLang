// emit_bytes_input.go — a Buffer/TypedArray, ArrayBuffer, DataView or string
// argument as the (ptr, byteLen) pair a byte-oriented runtime call takes.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// bytesResolveInput coerces a byte-input argument (Buffer/TypedArray,
// ArrayBuffer, DataView, or string) to a raw (dataPtr, byteLen) pair.
func (e *Emitter) bytesResolveInput(arg ast.Expression, pos ast.Pos) (string, string, error) {
	ty := e.inferExprType(arg)
	switch {
	case ty.IsArrayBuffer:
		bufVal, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		lenVal, err := e.emitArrayBufferByteLength(bufVal)
		if err != nil {
			return "", "", err
		}
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))
		return dataReg, lenVal.Ref, nil

	case ty.IsDataView:
		dvVal, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dvVal.Ref)) // field 0: data
		lenSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lenSlot, dataViewStructIR, dvVal.Ref))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
		return dataReg, lenReg, nil

	case ty.IsTypedArray:
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(arg, pos)
		if err != nil {
			return "", "", err
		}
		byteLen, err := e.emitTypedArrayByteLength(lenReg, elemTy)
		if err != nil {
			return "", "", err
		}
		return ptrReg, byteLen.Ref, nil

	default:
		// String input: encode as its UTF-8 bytes (the stored form already is
		// UTF-8). Length comes from the KML string header, NOT strlen: a klain
		// string may contain embedded NUL bytes, and a string literal can be
		// interned as a non-NUL-terminated prefix of a larger constant (e.g.
		// "GET / HT" sharing storage with "GET / HTTP/…"), where strlen would
		// over-read into the neighbouring bytes and send/compress far too much.
		strVal, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		strVal = e.coerce(strVal, TypePtr)
		lenReg := e.emitStrLenHeader(strVal.Ref)
		return strVal.Ref, lenReg, nil
	}
}
