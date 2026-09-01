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

// emitCryptoGetRandomValues implements crypto.getRandomValues(view): fills
// a TypedArray's (or ArrayBuffer's) bytes in place with CSPRNG output and
// returns the argument, per the real API. The original pre-TypedArray form
// — a named number[] variable filled with one random byte value (0-255)
// per i64 element — is kept as a legacy path (see
// ensureCryptoFillNumberArray's doc in runtime_crypto.go).
func (e *Emitter) emitCryptoGetRandomValues(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues takes exactly 1 argument", pos.Line, pos.Col)
	}

	argTy := e.inferExprType(args[0])
	if argTy.IsArrayBuffer {
		bufVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		e.ensureCryptoRandomBytes()
		byteLenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", byteLenReg, bufVal.Ref))
		e.emitCryptoQuotaCheck(byteLenReg)
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 %s)", dataReg, byteLenReg))
		return bufVal, nil
	}
	if argTy.IsArray && argTy.ElemType != nil &&
		(argTy.IsTypedArray || (argTy.ElemType.IR == "i8" && !argTy.ElemType.Signed)) {
		// Real getRandomValues rejects a float TypedArray (Float32/Float64) with
		// a TypeMismatchError — it fills only integer views. Enforced at compile
		// time here (ADR-00554).
		if argTy.ElemType.Float {
			return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues requires an integer TypedArray — a Float32Array/Float64Array is a TypeMismatchError in real JS", pos.Line, pos.Col)
		}
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureCryptoRandomBytes()
		byteLenReg := lenReg
		if elemTy.Align() != 1 {
			byteLenReg = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteLenReg, lenReg, elemTy.Align()))
		}
		e.emitCryptoQuotaCheck(byteLenReg)
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 %s)", ptrReg, byteLenReg))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", r0, ptrReg))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", r1, r0, lenReg))
		return Value{Ref: r1, Ty: argTy}, nil
	}

	id, ok := args[0].(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues requires a TypedArray, an ArrayBuffer, or a named number[] array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray || sym.Ty.ElemType == nil || sym.Ty.ElemType.IR != "double" {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues requires a TypedArray, an ArrayBuffer, or a number[] array", pos.Line, pos.Col)
	}

	e.ensureCryptoFillNumberArray()
	dataSlot, lenSlot := e.arrayDataLenSlots(sym)
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, dataSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_fill_number_array(ptr %s, i64 %s)", ptrReg, lenReg))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: sym.Ty}, nil
}

// emitCryptoQuotaCheck throws a QuotaExceededError DOMException when the
// byte length exceeds the spec's 65,536-byte getRandomValues limit (ADR-00554).
func (e *Emitter) emitCryptoQuotaCheck(byteLenReg string) {
	e.ensureExceptionHelpers()
	e.ensureStrHeaderRuntime()
	over := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 65536", over, byteLenReg))
	badL := e.freshLabel("grv.quota")
	okL := e.freshLabel("grv.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", over, badL, okL))
	e.emitLabel(badL)
	msg := e.internString("The requested length exceeds 65,536 bytes")
	errObj := e.buildErrorObj(errorKindIDs["DOMException"], msg, e.internString("QuotaExceededError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errObj))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// emitNewTextDecoderExpression implements `new TextDecoder(label?)`. V1 scope
// is UTF-8 only (this compiler's strings are already raw UTF-8 byte sequences,
// so decoding is a direct byte copy with no real transcoding). The label is
// WHATWG-normalized (trim + ASCII-lowercase) and validated against the six
// UTF-8 aliases (ADR-00567): a label that isn't a UTF-8 alias throws a
// catchable `RangeError` — matching real TextDecoder for an unrecognized
// label, and the honest response for a *recognized but non-UTF-8* label
// (latin1/utf-16/…) this compiler can't transcode. See
// docs/status/ENCODING-TEXT.md.
func (e *Emitter) emitNewTextDecoderExpression(ex *ast.NewTextDecoderExpression) (Value, error) {
	if ex.Label != nil {
		labelVal, err := e.emitExpr(ex.Label)
		if err != nil {
			return Value{}, err
		}
		labelVal = e.coerce(labelVal, TypePtr)
		e.ensureUtf8LabelCheck()
		e.ensureExceptionHelpers()
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_is_utf8_label(ptr %s)", ok, labelVal.Ref))
		badL := e.freshLabel("td.badlabel")
		okL := e.freshLabel("td.oklabel")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
		e.emitLabel(badL)
		msg := e.internString("The encoding label provided is not a supported UTF-8 label.")
		errObj := e.buildErrorObj(errorKindIDs["RangeError"], msg, e.internString("RangeError"))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errObj))
		e.emitTerminator("unreachable")
		e.emitLabel(okL)
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

	e.ensureMemcpy()
	outReg := e.emitStringAlloc(byteLenReg) // TDD-00120: length-prefixed string
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
