// emit_symbol.go — Symbol(...) V1 (docs/tdd/TDD-00044.md): a guaranteed-
// unique opaque value. Storage-wise a plain 1-field SymbolType() heap
// object ({description: string}), built and read via the same generic
// object-alloc/GEP machinery emit_objects.go already uses for object
// literals — see IsSymbol's doc comment in types.go for why no id/counter
// field is needed (pointer identity alone gives === its correct semantics).
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitSymbolConstructor implements the bare (no `new`) Symbol()/Symbol("desc")
// call. At most one argument, which must be a plain string — no implicit
// stringification of a non-string description, matching this compiler's
// general "clean compile error, not silent coercion" style.
func (e *Emitter) emitSymbolConstructor(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: Symbol() takes at most 1 argument (an optional description)", pos.Line, pos.Col)
	}
	descPtr := e.internString("")
	if len(args) == 1 {
		val, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if val.Ty.IR != "ptr" || val.Ty.IsObject || val.Ty.IsArray || val.Ty.IsFunc || val.Ty.IsSymbol {
			return Value{}, fmt.Errorf("%d:%d: Symbol()'s description must be a string", pos.Line, pos.Col)
		}
		descPtr = val.Ref
	}

	ty := SymbolType()
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, ty.StructSize()))
	idx, fieldTy, _ := ty.FieldIndex("description")
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, ty.StructIR(), dataReg, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), descPtr, gepReg, fieldTy.Align()))
	return Value{Ref: dataReg, Ty: ty}, nil
}

// emitSymbolToString formats val (a Symbol-typed Value) as "Symbol(desc)" —
// shared by .toString(), console.log, and template-literal interpolation.
// Deliberately identical across all three call sites: real JS is stricter
// about template-literal interpolation (throws TypeError there), but this
// compiler treats all three the same way V1, documented in TDD-00044.
func (e *Emitter) emitSymbolToString(val Value) (Value, error) {
	ty := SymbolType()
	idx, fieldTy, _ := ty.FieldIndex("description")
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, ty.StructIR(), val.Ref, idx))
	descReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", descReg, fieldTy.IR, gepReg, fieldTy.Align()))

	prefix := Value{Ref: e.internString("Symbol("), Ty: TypePtr}
	descVal := Value{Ref: descReg, Ty: TypePtr}
	suffix := Value{Ref: e.internString(")"), Ty: TypePtr}
	acc, err := e.emitStringConcat(prefix, descVal)
	if err != nil {
		return Value{}, err
	}
	return e.emitStringConcat(acc, suffix)
}
