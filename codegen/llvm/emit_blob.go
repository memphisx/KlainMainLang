package llvm

// emit_blob.go — the native readers' view of a Blob, which is the global
// module's TypeScript class (lib/node/kml_blob.ts): its bytes, its type, and
// a Blob made around another body value.

import (
	"fmt"

	"KlainMainLang/ast"
)

// blobClass is the program's Blob class. The resolver brings the module into
// any program naming Blob, Response or fetch (lib.NativeGlobalUses).
func (e *Emitter) blobClass(pos ast.Pos) (Type, error) {
	cls, ok := e.globalClass("Blob")
	if !ok {
		return Type{}, fmt.Errorf("%d:%d: internal: the Blob class is not linked", pos.Line, pos.Col)
	}
	return cls, nil
}

// emitNewBlobOf is `new Blob([part])`.
func (e *Emitter) emitNewBlobOf(part ast.Expression, pos ast.Pos) (Value, error) {
	cls, err := e.blobClass(pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitSynthExpr(ast.NewNewExpression(cls.ClassName, []ast.Expression{ast.NewArrayLiteral([]ast.Expression{part}, pos)}, pos))
}

// emitBlobBytes reads an evaluated Blob b: the address and length of a fresh
// copy of its bytes, and its type string.
func (e *Emitter) emitBlobBytes(b Value, pos ast.Pos) (data, size, typ string, err error) {
	bound := e.bindValue(b, pos)
	bytes, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(bound, "kmlBytes", pos), nil, pos))
	if err != nil {
		return "", "", "", err
	}
	d, n := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", d, bytes.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", n, bytes.Ref))
	t, err := e.emitExpr(ast.NewMemberExpression(bound, "type", pos))
	if err != nil {
		return "", "", "", err
	}
	return d, n, e.coerce(t, TypePtr).Ref, nil
}
