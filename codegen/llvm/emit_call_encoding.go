package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitStringToStringBuiltin implements any global builtin with the shape
// `name(s: string): string` (btoa/atob/encodeURIComponent/etc.) — evaluates
// and coerces the single string argument, ensures the given runtime helper
// is declared, and calls it.
func (e *Emitter) emitStringToStringBuiltin(args []ast.Expression, pos ast.Pos, name, runtimeFn string, ensure func()) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 1 argument", pos.Line, pos.Col, name)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	val = e.coerce(val, TypePtr)
	ensure()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s(ptr %s)", r, runtimeFn, val.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitCryptoGetRandomValues implements crypto.getRandomValues(arr), filling
// an existing number[] array's elements with random byte values (0-255
// each) — a deliberate deviation from the real TypedArray-based API, since
// this compiler has no ArrayBuffer/TypedArrays yet (see
// ensureCryptoFillNumberArray's doc in runtime.go). Requires a named array
// variable (not an arbitrary expression), matching the same restriction
// emitPush already has for array mutation (emit_arrays_mutate.go) — there's
// no heap location to write into otherwise.
func (e *Emitter) emitCryptoGetRandomValues(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues takes exactly 1 argument", pos.Line, pos.Col)
	}
	id, ok := args[0].(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues requires a named number[] array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray || sym.Ty.ElemType == nil || sym.Ty.ElemType.IR != "i64" {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues requires a number[] array (this compiler has no TypedArrays yet)", pos.Line, pos.Col)
	}

	e.ensureCryptoFillNumberArray()
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, sym.Ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_fill_number_array(ptr %s, i64 %s)", ptrReg, lenReg))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: sym.Ty}, nil
}

// emitNewTextDecoderExpression implements `new TextDecoder(label?)`. label,
// if given, is evaluated for its side effects and then discarded — V1 scope
// is UTF-8 only (this compiler's strings are already raw UTF-8 byte
// sequences, so decoding is a direct byte copy with no real transcoding),
// so there's nothing to validate or remember it against. Real TextDecoder
// throws a RangeError for an unrecognized label; this compiler is
// permissive instead, the same documented V1 simplification atob/
// decodeURI already establish for malformed input. See
// docs/status/ENCODING-TEXT.md.
func (e *Emitter) emitNewTextDecoderExpression(ex *ast.NewTextDecoderExpression) (Value, error) {
	if ex.Label != nil {
		if _, err := e.emitExpr(ex.Label); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: "null", Ty: TextDecoderType()}, nil
}

// emitTextEncoderEncode implements `textEncoder.encode(str): Uint8Array`.
// Since this compiler's strings are already raw UTF-8 byte sequences (the
// same premise btoa/atob already rely on), encoding is just copying those
// bytes into a fresh heap buffer — no real transcoding needed. objExpr (the
// TextEncoder receiver) is evaluated for side effects only: IsTextEncoder
// carries no state (see its doc comment), so nothing is read from it.
func (e *Emitter) emitTextEncoderEncode(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, err := e.emitExpr(objExpr); err != nil {
		return Value{}, err
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: encode takes exactly 1 argument", pos.Line, pos.Col)
	}
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	strVal = e.coerce(strVal, TypePtr)

	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", lenReg, strVal.Ref))
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, lenReg))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dataReg, strVal.Ref, lenReg))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: TypedArrayType("uint8")}, nil
}

// emitTextDecoderDecode implements `textDecoder.decode(bytes): string`.
// bytes must be a Uint8Array or an ArrayBuffer (real TextDecoder accepts
// any ArrayBufferView — narrowed here to the two shapes this compiler's
// callers actually produce, e.g. TextEncoder.encode's own Uint8Array
// result or a fetch Response's .arrayBuffer()). Copies the raw bytes into
// a fresh NUL-terminated buffer — the inverse of emitTextEncoderEncode,
// same "strings are already UTF-8 bytes" premise, no real transcoding.
// objExpr (the TextDecoder receiver) is evaluated for side effects only,
// same as emitTextEncoderEncode.
func (e *Emitter) emitTextDecoderDecode(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, err := e.emitExpr(objExpr); err != nil {
		return Value{}, err
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: decode takes exactly 1 argument", pos.Line, pos.Col)
	}

	argTy := e.inferExprType(args[0])
	var byteLenReg, dataReg string
	switch {
	case argTy.IsArrayBuffer:
		bufVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		byteLenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", byteLenReg, bufVal.Ref))
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))
	case argTy.IsArray && argTy.ElemType != nil && argTy.ElemType.IR == "i8" && !argTy.ElemType.Signed:
		var err error
		dataReg, byteLenReg, _, err = e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, err
		}
	default:
		return Value{}, fmt.Errorf("%d:%d: decode expects a Uint8Array or ArrayBuffer", pos.Line, pos.Col)
	}

	e.ensureMalloc()
	e.ensureMemcpy()
	outLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", outLenReg, byteLenReg))
	outReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", outReg, outLenReg))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", outReg, dataReg, byteLenReg))
	termReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termReg, outReg, byteLenReg))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termReg))

	return Value{Ref: outReg, Ty: TypePtr}, nil
}

// emitCryptoRandomUUID implements crypto.randomUUID().
func (e *Emitter) emitCryptoRandomUUID(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: crypto.randomUUID takes no arguments", pos.Line, pos.Col)
	}
	e.ensureCryptoRandomUUID()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_crypto_random_uuid()", r))
	return Value{Ref: r, Ty: TypePtr}, nil
}
