// emit_process.go — process.argv, process.exit(code), process.env.KEY / process.env["KEY"].
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// isProcessEnvExpr reports whether expr is exactly `process.env` (non-optional).
func isProcessEnvExpr(expr ast.Expression) bool {
	mem, ok := expr.(*ast.MemberExpression)
	if !ok || mem.Optional || mem.Property != "env" {
		return false
	}
	id, ok := mem.Object.(*ast.Identifier)
	return ok && id.Name == "process"
}

// emitProcessArgv returns process.argv as a string[] aggregate backed by the
// @__argv_ptr/@__argv_len globals populated from main's own argc/argv at startup.
func (e *Emitter) emitProcessArgv() (Value, error) {
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__argv_ptr, align 8", ptrReg))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__argv_len, align 8", lenReg))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
}

// emitProcessExit implements process.exit(code): calls C exit() and never returns.
func (e *Emitter) emitProcessExit(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: process.exit takes exactly 1 argument", pos.Line, pos.Col)
	}
	codeVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	codeVal = e.coerce(codeVal, TypeI64)
	e.ensureExit()
	code32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", code32, codeVal.Ref))
	e.emitInstr(fmt.Sprintf("call void @exit(i32 %s)", code32))
	e.emitTerminator("unreachable")
	return Value{Ty: TypeVoid}, nil
}

// emitGetenvCall calls C getenv() on the given key pointer, returning a
// possibly-null string ptr (nil when the variable isn't set) — same convention
// as emitArrayFind: a plain TypePtr the caller compares against null.
func (e *Emitter) emitGetenvCall(keyPtr string) Value {
	e.ensureGetenv()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @getenv(ptr %s)", result, keyPtr))
	return Value{Ref: result, Ty: TypePtr}
}

// emitProcessEnvGetStatic implements process.env.KEY (dot notation): the key
// name is known at compile time.
func (e *Emitter) emitProcessEnvGetStatic(name string) (Value, error) {
	keyPtr := e.internString(name)
	return e.emitGetenvCall(keyPtr), nil
}

// emitProcessEnvGetDynamic implements process.env["KEY"] (bracket notation):
// the key is an arbitrary string-valued expression evaluated at runtime.
func (e *Emitter) emitProcessEnvGetDynamic(keyExpr ast.Expression) (Value, error) {
	keyVal, err := e.emitExpr(keyExpr)
	if err != nil {
		return Value{}, err
	}
	return e.emitGetenvCall(keyVal.Ref), nil
}

// emitProcessExecFileSync implements process.execFileSync(file, args?):
// forks + execvp()s file (no shell involved, matching real Node's
// execFileSync — not execSync's shell-interpolation behavior), captures its
// stdout, and returns it as a string once the child exits. Throws a
// catchable Error on a non-zero exit status or a signal death. V1 scope: no
// options object (cwd/env/timeout/stdio all deferred), stdout only (stderr
// is inherited, printed straight to this program's own stderr, not
// captured).
func (e *Emitter) emitProcessExecFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: process.execFileSync takes 1 or 2 arguments (file, args?)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)

	argsPtr, argsLen := "null", "0"
	if len(args) == 2 {
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		if elemTy.IR != "ptr" || elemTy.IsObject || elemTy.IsArray || elemTy.IsFunc || elemTy.IsDynamic {
			return Value{}, fmt.Errorf("%d:%d: process.execFileSync's args argument must be a string[]", pos.Line, pos.Col)
		}
		argsPtr, argsLen = ptrReg, lenReg
	}

	e.ensureExecFileSync()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_exec_file_sync(ptr %s, ptr %s, i64 %s)", r, fileVal.Ref, argsPtr, argsLen))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitProcessCwd implements process.cwd(): the current working directory.
func (e *Emitter) emitProcessCwd(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: process.cwd takes no arguments", pos.Line, pos.Col)
	}
	e.ensureProcessCwd()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", r))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitProcessChdir implements process.chdir(path): changes the current
// working directory, throwing a catchable Error on failure.
func (e *Emitter) emitProcessChdir(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: process.chdir takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureProcessChdir()
	e.emitInstr(fmt.Sprintf("call void @__kml_process_chdir(ptr %s)", pathVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

// emitProcessPid implements the process.pid property read (not a call).
func (e *Emitter) emitProcessPid() (Value, error) {
	e.ensureGetpid()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_getpid()", r))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// emitProcessKill implements process.kill(pid, signal?): sends signal (SIGTERM,
// 15, if omitted — matching real Node's own default) to pid via POSIX kill(),
// throwing a catchable Error if the target process doesn't exist or the
// signal can't be sent.
func (e *Emitter) emitProcessKill(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: process.kill takes 1 or 2 arguments (pid, signal?)", pos.Line, pos.Col)
	}
	pidVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pidVal = e.coerce(pidVal, TypeI64)

	sigRef := "15"
	if len(args) == 2 {
		sigVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		sigVal = e.coerce(sigVal, TypeI64)
		sigRef = sigVal.Ref
	}

	e.ensureProcessKill()
	e.emitInstr(fmt.Sprintf("call void @__kml_process_kill(i64 %s, i64 %s)", pidVal.Ref, sigRef))
	return Value{Ty: TypeVoid}, nil
}
