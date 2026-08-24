// emit_stdin.go — codegen for streaming process.stdin: the 'data' and 'end'
// events. Backed by runtime_stdin.go.
//
// One listener per event, an arrow/function-expression literal only (the
// Worker/child_process/readline posture). Attaching the 'data' listener puts
// the stream into flowing mode (the runtime only reads once it is present).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitStdinMethodCall dispatches process.stdin.on('data'|'end', listener).
func (e *Emitter) emitStdinMethodCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureStdinRuntime()
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "on":
		evt, err := stringLiteralArg(args, 0, "process.stdin.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: process.stdin.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "data":
			// The 'data' listener takes the chunk as a UTF-8 string (this
			// stream delivers text, not a Buffer).
			cb, err := e.cpArrowClosure(args[1], []Type{TypePtr}, pos)
			if err != nil {
				return Value{}, err
			}
			e.stdinStoreField(objVal.Ref, 0, cb)
		case "end":
			cb, err := e.cpArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.stdinStoreField(objVal.Ref, 1, cb)
		default:
			return Value{}, fmt.Errorf("%d:%d: process.stdin.on supports 'data' and 'end' (got '%s')", pos.Line, pos.Col, evt)
		}
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: process.stdin has no method '%s' (supported: on)", pos.Line, pos.Col, method)
}

// stdinStoreField GEPs the handle's field idx and stores a ptr into it.
func (e *Emitter) stdinStoreField(h string, idx int, val string) {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, stdinStructIR, h, idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, slot))
}
