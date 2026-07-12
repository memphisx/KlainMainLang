// emit_fs.go — fs.readFileSync/writeFileSync/appendFileSync/existsSync/
// unlinkSync: synchronous file I/O, recognized as a pseudo-namespace
// (matching process.*/Math.*/JSON.* — not a real importable module).
//
// All synchronous by design: there's no event loop in this compiler, so
// there's no non-blocking variant to offer. Text-only, like every string
// here — reading a file with embedded null bytes truncates at the first
// one (see runtime.go's ensureFsReadFile doc). A failed read/write/append/
// delete throws a catchable Error (built from strerror(errno)), matching
// how fetch's network failures are surfaced (see emit_fetch.go).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

func (e *Emitter) emitFsReadFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.readFileSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsReadFile()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fs_read_file(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitFsWriteFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitFsWriteLikeCall(args, pos, "fs.writeFileSync", "@__kml_fs_write_file")
}

func (e *Emitter) emitFsAppendFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitFsWriteLikeCall(args, pos, "fs.appendFileSync", "@__kml_fs_append_file")
}

// emitFsWriteLikeCall backs both writeFileSync and appendFileSync — the
// call-site shape (evaluate path + data, coerce both to string, call the
// matching runtime helper) is identical; only which runtime helper (and its
// error message/fopen mode, already baked in via runtime.go) differs.
func (e *Emitter) emitFsWriteLikeCall(args []ast.Expression, pos ast.Pos, name, runtimeFn string) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 2 arguments (path, data)", pos.Line, pos.Col, name)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	dataVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	dataVal = e.coerce(dataVal, TypePtr)

	if runtimeFn == "@__kml_fs_write_file" {
		e.ensureFsWriteFile()
	} else {
		e.ensureFsAppendFile()
	}
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s)", runtimeFn, pathVal.Ref, dataVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitFsExistsSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.existsSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsExists()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_fs_exists(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitFsUnlinkSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.unlinkSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsUnlink()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_unlink(ptr %s)", pathVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitFsMkdirSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.mkdirSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsMkdir()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_mkdir(ptr %s)", pathVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitFsRmdirSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.rmdirSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsRmdir()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_rmdir(ptr %s)", pathVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitFsRenameSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.renameSync takes exactly 2 arguments (oldPath, newPath)", pos.Line, pos.Col)
	}
	oldVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	oldVal = e.coerce(oldVal, TypePtr)
	newVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	newVal = e.coerce(newVal, TypePtr)

	e.ensureFsRename()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_rename(ptr %s, ptr %s)", oldVal.Ref, newVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

// emitFsCopyFileSync implements fs.copyFileSync(src, dest): reads src fully
// (via the existing readFileSync runtime helper) then writes it to dest (via
// the existing writeFileSync one) — no new C-level I/O code needed, since
// both halves of "copy a file" already exist as their own fs.* builtins.
// Inherits readFileSync's text-only limitation (a src file with embedded
// null bytes copies back shorter than its real size).
func (e *Emitter) emitFsCopyFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.copyFileSync takes exactly 2 arguments (src, dest)", pos.Line, pos.Col)
	}
	srcVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	srcVal = e.coerce(srcVal, TypePtr)
	destVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	destVal = e.coerce(destVal, TypePtr)

	e.ensureFsReadFile()
	e.ensureFsWriteFile()
	contentReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fs_read_file(ptr %s)", contentReg, srcVal.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_write_file(ptr %s, ptr %s)", destVal.Ref, contentReg))
	return Value{Ty: TypeVoid}, nil
}

// emitFsReaddirSync implements fs.readdirSync(path): lists a directory's
// entries (excluding "." and "..") as a string[], in whatever order the OS's
// own readdir() returns them (unspecified/filesystem-dependent — matching
// real Node's own readdirSync, which makes no ordering guarantee either).
func (e *Emitter) emitFsReaddirSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.readdirSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsReaddir()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_fs_readdir(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: ArrayOf(TypePtr)}, nil
}
