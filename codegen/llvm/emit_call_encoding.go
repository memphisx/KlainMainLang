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
