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
	// setRawMode is handled before the streaming runtime is touched: evaluating
	// process.stdin calls __kml_stdin_create(), which flips fd 0 to O_NONBLOCK
	// for the event-driven .on('data') reader — the wrong mode for the
	// synchronous klain:tty readByte/readKey a raw-mode program pairs with. The
	// termios toggle needs neither the handle nor the non-blocking flip, so it
	// stays clear of both (TDD-00031).
	if method == "setRawMode" {
		if _, err := e.emitProcessSetRawMode(args, pos); err != nil {
			return Value{}, err
		}
		// Node returns the stream for chaining; nothing downstream depends on the
		// handle's identity, so a void result is the faithful-enough answer that
		// avoids forcing the O_NONBLOCK flip.
		return Value{Ty: TypeVoid}, nil
	}
	e.ensureStdinRuntime()
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	// Node's stream methods return the stream itself, so the idiomatic
	// process.stdin.setEncoding('utf8').on('data', …).on('end', …) chain works;
	// StdinType is re-derived for the chained call in inferExprType.
	handle := Value{Ref: objVal.Ref, Ty: StdinType()}
	switch method {
	case "setEncoding":
		// This stream already delivers 'data' chunks as UTF-8 strings, so
		// setEncoding('utf8') is a faithful no-op. A different encoding cannot be
		// honored (there is no Buffer chunk to re-decode), so it is a clean
		// rejection rather than a silently wrong decode.
		enc, err := stringLiteralArg(args, 0, "process.stdin.setEncoding", pos)
		_ = enc
		if err != nil {
			return Value{}, err
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: process.stdin.setEncoding takes one encoding argument", pos.Line, pos.Col)
		}
		lit, _ := args[0].(*ast.StringLiteral)
		if lit == nil || (lit.Value != "utf8" && lit.Value != "utf-8") {
			return Value{}, fmt.Errorf("%d:%d: process.stdin.setEncoding supports only 'utf8' (chunks are delivered as UTF-8 strings)", pos.Line, pos.Col)
		}
		return handle, nil
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
		return handle, nil
	}
	return Value{}, fmt.Errorf("%d:%d: process.stdin has no method '%s' (supported: on, setEncoding, setRawMode)", pos.Line, pos.Col, method)
}

// stdinStoreField GEPs the handle's field idx and stores a ptr into it.
func (e *Emitter) stdinStoreField(h string, idx int, val string) {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, stdinStructIR, h, idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, slot))
}
