// emit_fs_constants.go — `fs.constants` (ADR-00795), modelled on
// `http2.constants` (emit_http2_settings.go).
//
// `fs.constants` is a compile-time namespace: a member read resolves to a
// numeric literal with no runtime object, and a binding (`const c =
// fs.constants`, or `import { constants } from 'fs'`) carries the IsFsConstants
// flag so reads through the alias resolve identically.
//
// The set is the constants with a real consumer in this compiler: the POSIX
// access modes (`accessSync(path, mode)`) and libuv's copyFile flags
// (`copyFileSync(src, dest, mode)`). All are platform-invariant — the access
// bits are POSIX-standard (identical on Linux and macOS) and the COPYFILE_*
// values are libuv's own, not the host OS's — so a single compile-time table is
// correct on every target. The `O_*` open flags are deliberately omitted: no
// API here consumes them (writeFileSync's `flag` is a string like `'a'`/`'w'`),
// and their numeric values are genuinely host-specific, which a compile-time
// literal can't honor faithfully.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// fsConstants maps each fs.constants member to its numeric value. Node exposes
// these as `number`s, so they emit as doubles.
var fsConstants = map[string]float64{
	// Access modes — the mode argument to accessSync (POSIX F_OK/X_OK/W_OK/R_OK).
	"F_OK": 0, "X_OK": 1, "W_OK": 2, "R_OK": 4,
	// copyFile flags — the mode argument to copyFileSync (libuv UV_FS_COPYFILE_*).
	"COPYFILE_EXCL": 1, "COPYFILE_FICLONE": 2, "COPYFILE_FICLONE_FORCE": 4,
}

// isFsConstantsExpr reports whether expr statically denotes fs.constants — the
// direct `fs.constants` member read or a binding carrying the flag.
func (e *Emitter) isFsConstantsExpr(expr ast.Expression) bool {
	if m, ok := expr.(*ast.MemberExpression); ok && m.Property == "constants" {
		if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "fs__kml_builtin" {
			return true
		}
	}
	return e.inferExprType(expr).IsFsConstants
}

// emitFsConstant resolves one fs.constants member to its numeric literal.
func (e *Emitter) emitFsConstant(name string, pos ast.Pos) (Value, error) {
	v, ok := fsConstants[name]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: fs.constants has no member '%s' (the POSIX access modes F_OK/R_OK/W_OK/X_OK and the copyFile flags COPYFILE_EXCL/COPYFILE_FICLONE/COPYFILE_FICLONE_FORCE are covered)", pos.Line, pos.Col, name)
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fadd double 0.0, %s", r, llvmDoubleLit(v)))
	return Value{Ref: r, Ty: TypeF64}, nil
}
