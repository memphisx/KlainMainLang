// emit_childprocess.go — codegen for Node's async `child_process`:
// spawn/exec/execFile plus the ChildProcess surface (child.stdout/stderr
// 'data'/'end', child.on 'close'/'exit'/'error', child.stdin.write/end,
// child.pid, child.kill). All backed by runtime_childprocess.go.
//
// Listener registration mirrors the Worker posture (emit_worker.go): one
// listener per event, an arrow/function-expression literal only, stored as a
// raw closure header the runtime dispatch invokes directly. spawn is
// streaming (mode 0); exec/execFile are buffered (mode 1) with a single
// (err, stdout, stderr) callback fired on exit.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitChildProcessModuleCall dispatches child_process.spawn/exec/execFile.
func (e *Emitter) emitChildProcessModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureChildProcRuntime()
	switch method {
	case "spawn":
		return e.emitCPSpawn(args, pos)
	case "exec":
		return e.emitCPExec(args, pos)
	case "execFile":
		return e.emitCPExecFile(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: child_process.%s is not supported", pos.Line, pos.Col, method)
}

// cpSpawnCall emits the @__kml_cp_spawn call and returns the handle register.
func (e *Emitter) cpSpawnCall(fileRef, argsPtr, argsLen string, mode int) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cp_spawn(ptr %s, ptr %s, i64 %s, i64 %d)", r, fileRef, argsPtr, argsLen, mode))
	return r
}

// emitCPSpawn implements spawn(command, args?): streaming ChildProcess.
func (e *Emitter) emitCPSpawn(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: child_process.spawn takes (command, args?)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)
	argsPtr, argsLen := "null", "0"
	if len(args) == 2 {
		p, l, err := e.cpResolveArgv(args[1], pos, "spawn")
		if err != nil {
			return Value{}, err
		}
		argsPtr, argsLen = p, l
	}
	cp := e.cpSpawnCall(fileVal.Ref, argsPtr, argsLen, 0)
	return Value{Ref: cp, Ty: ChildProcessType()}, nil
}

// emitCPExec implements exec(command, callback): runs command via `/bin/sh
// -c`, buffering stdout/stderr, then fires callback(err, stdout, stderr).
func (e *Emitter) emitCPExec(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: child_process.exec takes (command, callback)", pos.Line, pos.Col)
	}
	cmdVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cmdVal = e.coerce(cmdVal, TypePtr)
	// argv = ["-c", command]; file = "/bin/sh"
	argvPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", argvPtr))
	s0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", s0, argvPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("-c"), s0))
	s1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", s1, argvPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cmdVal.Ref, s1))
	cp := e.cpSpawnCall(e.internString("/bin/sh"), argvPtr, "2", 1)
	if err := e.cpStoreExecCallback(cp, args[1], pos, "exec"); err != nil {
		return Value{}, err
	}
	return Value{Ref: cp, Ty: ChildProcessType()}, nil
}

// emitCPExecFile implements execFile(file, args?, callback): execvp's file
// (no shell), buffering stdout/stderr, then fires callback(err, stdout, stderr).
func (e *Emitter) emitCPExecFile(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: child_process.execFile takes (file, args?, callback)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)
	argsPtr, argsLen := "null", "0"
	cbArg := args[len(args)-1]
	if len(args) == 3 {
		p, l, err := e.cpResolveArgv(args[1], pos, "execFile")
		if err != nil {
			return Value{}, err
		}
		argsPtr, argsLen = p, l
	}
	cp := e.cpSpawnCall(fileVal.Ref, argsPtr, argsLen, 1)
	if err := e.cpStoreExecCallback(cp, cbArg, pos, "execFile"); err != nil {
		return Value{}, err
	}
	return Value{Ref: cp, Ty: ChildProcessType()}, nil
}

// cpResolveArgv normalizes a string[] args argument to (ptr, len).
func (e *Emitter) cpResolveArgv(arg ast.Expression, pos ast.Pos, name string) (string, string, error) {
	// An empty array literal `[]` carries no element type to check — treat it
	// as "no arguments", matching spawn("cmd", []) === spawn("cmd").
	if lit, ok := arg.(*ast.ArrayLiteral); ok && len(lit.Elements) == 0 {
		return "null", "0", nil
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(arg, pos)
	if err != nil {
		return "", "", err
	}
	if elemTy.IR != "ptr" || elemTy.IsObject || elemTy.IsArray || elemTy.IsFunc || elemTy.IsDynamic {
		return "", "", fmt.Errorf("%d:%d: child_process.%s's args argument must be a string[]", pos.Line, pos.Col, name)
	}
	return ptrReg, lenReg, nil
}

// cpStoreExecCallback stores the buffered (err, stdout, stderr) callback into
// the handle's field 16.
func (e *Emitter) cpStoreExecCallback(cp string, cbArg ast.Expression, pos ast.Pos, name string) error {
	cb, err := e.resolveCallbackWithHints(cbArg, []Type{errorObjType, TypePtr, TypePtr})
	if err != nil {
		return err
	}
	if cb.kind != cbClosure {
		return fmt.Errorf("%d:%d: child_process.%s's callback must be an arrow function literal", pos.Line, pos.Col, name)
	}
	e.cpStoreField(cp, 16, cb.hdrPtr)
	return nil
}

// cpStoreField GEPs cp field idx and stores a ptr into it.
func (e *Emitter) cpStoreField(cp string, idx int, val string) {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, cpStructIR, cp, idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, slot))
}

// emitChildProcessMember reads child.stdout/.stderr/.stdin/.pid.
func (e *Emitter) emitChildProcessMember(objVal Value, prop string, pos ast.Pos) (Value, error) {
	switch prop {
	case "stdout":
		return Value{Ref: objVal.Ref, Ty: CPStreamType(0)}, nil
	case "stderr":
		return Value{Ref: objVal.Ref, Ty: CPStreamType(1)}, nil
	case "stdin":
		return Value{Ref: objVal.Ref, Ty: CPStdinType()}, nil
	case "pid":
		r := e.freshReg()
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", slot, cpStructIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypeI64}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a ChildProcess has no property '%s'", pos.Line, pos.Col, prop)
}

// emitChildProcessMethodCall dispatches methods on a ChildProcess / its
// stdout/stderr / stdin.
func (e *Emitter) emitChildProcessMethodCall(objExpr ast.Expression, objTy Type, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch {
	case objTy.IsCPStream:
		return e.emitCPStreamOn(objVal, objTy.CPWhich, method, args, pos)
	case objTy.IsCPStdin:
		return e.emitCPStdin(objVal, method, args, pos)
	default: // the ChildProcess itself
		return e.emitCPHandleMethod(objVal, method, args, pos)
	}
}

// emitCPStreamOn handles child.stdout/stderr .on('data'|'end', cb).
func (e *Emitter) emitCPStreamOn(objVal Value, which int, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if method != "on" {
		return Value{}, fmt.Errorf("%d:%d: a ChildProcess stream supports only .on('data'|'end', cb)", pos.Line, pos.Col)
	}
	evt, err := stringLiteralArg(args, 0, "stream.on", pos)
	if err != nil {
		return Value{}, err
	}
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: stream.on takes (event, listener)", pos.Line, pos.Col)
	}
	// stdout listener slots are 6/7, stderr 8/9.
	dataIdx, endIdx := 6, 7
	if which == 1 {
		dataIdx, endIdx = 8, 9
	}
	switch evt {
	case "data":
		cb, err := e.cpArrowClosure(args[1], []Type{TypedArrayType("uint8")}, pos)
		if err != nil {
			return Value{}, err
		}
		e.cpStoreField(objVal.Ref, dataIdx, cb)
	case "end":
		cb, err := e.cpArrowClosure(args[1], nil, pos)
		if err != nil {
			return Value{}, err
		}
		e.cpStoreField(objVal.Ref, endIdx, cb)
	default:
		return Value{}, fmt.Errorf("%d:%d: a ChildProcess stream supports 'data' and 'end' (got '%s')", pos.Line, pos.Col, evt)
	}
	return Value{Ty: TypeVoid}, nil
}

// emitCPHandleMethod handles child.on('close'|'exit'|'error', cb) and
// child.kill(signal?).
func (e *Emitter) emitCPHandleMethod(objVal Value, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "on":
		evt, err := stringLiteralArg(args, 0, "child.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: child.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "close", "exit":
			cb, err := e.cpArrowClosure(args[1], []Type{TypeI64}, pos)
			if err != nil {
				return Value{}, err
			}
			idx := 10
			if evt == "exit" {
				idx = 11
			}
			e.cpStoreField(objVal.Ref, idx, cb)
		case "error":
			cb, err := e.cpArrowClosure(args[1], []Type{errorObjType}, pos)
			if err != nil {
				return Value{}, err
			}
			e.cpStoreField(objVal.Ref, 12, cb)
		default:
			return Value{}, fmt.Errorf("%d:%d: child.on supports 'close', 'exit' and 'error' (got '%s')", pos.Line, pos.Col, evt)
		}
		return Value{Ty: TypeVoid}, nil
	case "kill":
		e.ensureCPKill()
		sig := "15" // SIGTERM
		if len(args) == 1 {
			sv, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			sig = e.coerce(sv, TypeI64).Ref
		}
		pidSlot := e.freshReg()
		pid := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", pidSlot, cpStructIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", pid, pidSlot))
		pid32 := e.freshReg()
		sig32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", pid32, pid))
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", sig32, sig))
		e.emitInstr(fmt.Sprintf("call i32 @kill(i32 %s, i32 %s)", pid32, sig32))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a ChildProcess has no method '%s'", pos.Line, pos.Col, method)
}

// emitCPStdin handles child.stdin.write(data) / child.stdin.end().
func (e *Emitter) emitCPStdin(objVal Value, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "write":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: child.stdin.write takes (data)", pos.Line, pos.Col)
		}
		ptrRef, lenRef, err := e.zlibResolveInput(args[0], pos) // same string/Buffer/ArrayBuffer/DataView normalization
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_cp_stdin_write(ptr %s, ptr %s, i64 %s)", objVal.Ref, ptrRef, lenRef))
		return Value{Ty: TypeVoid}, nil
	case "end":
		e.emitInstr(fmt.Sprintf("call void @__kml_cp_stdin_end(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: child.stdin has no method '%s'", pos.Line, pos.Col, method)
}

// cpArrowClosure resolves a listener argument to a closure header pointer,
// requiring an arrow/function-expression literal (the Worker posture).
func (e *Emitter) cpArrowClosure(arg ast.Expression, hints []Type, pos ast.Pos) (string, error) {
	cb, err := e.resolveCallbackWithHints(arg, hints)
	if err != nil {
		return "", err
	}
	if cb.kind != cbClosure {
		return "", fmt.Errorf("%d:%d: a ChildProcess listener must be an arrow function literal", pos.Line, pos.Col)
	}
	return cb.hdrPtr, nil
}

// ensureCPKill declares kill(2) once.
func (e *Emitter) ensureCPKill() {
	if e.usedCPKill {
		return
	}
	e.usedCPKill = true
	e.emitGlobal("declare i32 @kill(i32 noundef, i32 noundef)")
}
