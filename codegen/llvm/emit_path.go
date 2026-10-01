// emit_path.go — the path resolution url.pathToFileURL uses (the posix
// algorithm here and in runtime_path.go as IR, the win32 one the C sidecar
// behind path_win32.go). The `path` module itself is lib/node/path.ts.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitPathStartsWithSlash returns an i1 register ref for whether v's first
// byte is '/'. Safe even for an empty string: every string this compiler
// produces is a malloc'd, null-terminated buffer, so byte 0 always exists
// and an empty string's byte 0 (the terminator) simply compares unequal to
// '/' — no length check needed first.
func (e *Emitter) emitPathStartsWithSlash(v Value) string {
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", b, v.Ref))
	isSlash := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 47", isSlash, b))
	return isSlash
}

// emitPathResolveValues is emitPathResolve over already-evaluated segments
// (POSIX flavor only; the win32 flavor takes the sidecar's variadic path).
func (e *Emitter) emitPathResolveValues(f pathFlavor, segs []Value, pos ast.Pos) (Value, error) {
	if f == pathWin32 {
		return Value{}, fmt.Errorf("%d:%d: internal: emitPathResolveValues is POSIX-only", pos.Line, pos.Col)
	}
	e.ensureProcessCwd()
	accPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accPtr))
	cwdReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", cwdReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cwdReg, accPtr))

	sep := Value{Ref: e.internString("/"), Ty: TypePtr}
	for _, segVal := range segs {
		segVal = e.coerce(segVal, TypePtr)
		isAbs := e.emitPathStartsWithSlash(segVal)

		resetL := e.freshLabel("pathresolve.reset")
		appendL := e.freshLabel("pathresolve.append")
		mergeL := e.freshLabel("pathresolve.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAbs, resetL, appendL))

		e.emitLabel(resetL)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", segVal.Ref, accPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(appendL)
		curAcc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curAcc, accPtr))
		withSep, err := e.emitStringConcat(Value{Ref: curAcc, Ty: TypePtr}, sep)
		if err != nil {
			return Value{}, err
		}
		newAcc, err := e.emitStringConcat(withSep, segVal)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newAcc.Ref, accPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
	}

	rawReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rawReg, accPtr))
	e.ensurePathNormalize()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_normalize(ptr %s, i1 true)", r, rawReg))
	return Value{Ref: r, Ty: TypePtr}, nil
}
