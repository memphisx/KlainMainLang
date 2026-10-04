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
// an integer TypedArray's (or Buffer's) bytes in place with CSPRNG output and
// returns the argument, per the real API. Any other argument — a float
// TypedArray, a DataView, an ArrayBuffer, a plain array, a value held in
// `any` that is none of the integer kinds — is Node's TypeMismatchError,
// checked at run time.
func (e *Emitter) emitCryptoGetRandomValues(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues takes exactly 1 argument", pos.Line, pos.Col)
	}
	argTy := e.inferExprType(args[0])
	if argTy.IsArray && argTy.ElemType != nil && !argTy.ElemType.Float &&
		(argTy.IsTypedArray || argTy.IsBuffer || (argTy.ElemType.IR == "i8" && !argTy.ElemType.Signed)) {
		// The argument itself is returned (`getRandomValues(a) === a`).
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		ptrReg, lenReg := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, v.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, v.Ref))
		e.ensureCryptoRandomBytes()
		byteLenReg := lenReg
		if w := argTy.ElemType.Align(); w != 1 {
			byteLenReg = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteLenReg, lenReg, w))
		}
		e.emitCryptoQuotaCheck(byteLenReg)
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 %s)", ptrReg, byteLenReg))
		return v, nil
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	box, err := e.emitBoxValue(v)
	if err != nil {
		return Value{}, err
	}
	e.ensureCryptoFillAny()
	e.ensureExceptionHelpers()
	e.ensureStrHeaderRuntime()
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_fill_any(i64 %s)", rc, box.Ref))
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", ok, rc))
	okL, badL := e.freshLabel("grv.anyok"), e.freshLabel("grv.anybad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
	e.emitLabel(badL)
	quota := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", quota, rc))
	qL, tL := e.freshLabel("grv.anyquota"), e.freshLabel("grv.anytype")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", quota, qL, tL))
	e.emitLabel(qL)
	qErr := e.buildErrorObj(errorKindIDs["DOMException"], e.internString("The requested length exceeds 65,536 bytes"), e.internString("QuotaExceededError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", qErr))
	e.emitTerminator("unreachable")
	e.emitLabel(tL)
	tErr := e.buildErrorObj(errorKindIDs["DOMException"], e.internString("The data argument must be an integer-type TypedArray"), e.internString("TypeMismatchError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", tErr))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	return v, nil
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
