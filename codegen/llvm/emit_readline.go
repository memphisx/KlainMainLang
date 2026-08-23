// emit_readline.go — codegen for Node's interactive `readline`:
// createInterface, the 'line'/'close' events, question(query, cb), and
// close(). Backed by runtime_readline.go.
//
// One listener per event, an arrow/function-expression literal only (the
// Worker/child_process posture). The options object passed to createInterface
// is accepted syntactically but not inspected: input is always stdin, output
// always stdout.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitReadlineModuleCall dispatches readline.createInterface(options?).
func (e *Emitter) emitReadlineModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if method != "createInterface" {
		return Value{}, fmt.Errorf("%d:%d: readline.%s is not supported", pos.Line, pos.Col, method)
	}
	// The options argument (typically { input: process.stdin }) is accepted but
	// not evaluated — the interface always reads stdin and writes stdout.
	e.ensureReadlineRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rl_create()", r))
	return Value{Ref: r, Ty: ReadlineType()}, nil
}

// emitReadlineMethodCall dispatches rl.on/.question/.close.
func (e *Emitter) emitReadlineMethodCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "on":
		evt, err := stringLiteralArg(args, 0, "rl.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: rl.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "line":
			cb, err := e.cpArrowClosure(args[1], []Type{TypePtr}, pos)
			if err != nil {
				return Value{}, err
			}
			e.rlStoreField(objVal.Ref, 0, cb)
		case "close":
			cb, err := e.cpArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.rlStoreField(objVal.Ref, 1, cb)
		default:
			return Value{}, fmt.Errorf("%d:%d: rl.on supports 'line' and 'close' (got '%s')", pos.Line, pos.Col, evt)
		}
		return Value{Ty: TypeVoid}, nil
	case "question":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: rl.question takes (query, callback)", pos.Line, pos.Col)
		}
		queryVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		queryVal = e.coerce(queryVal, TypePtr)
		cb, err := e.cpArrowClosure(args[1], []Type{TypePtr}, pos)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_rl_question(ptr %s, ptr %s, ptr %s)", objVal.Ref, queryVal.Ref, cb))
		return Value{Ty: TypeVoid}, nil
	case "close":
		e.emitInstr(fmt.Sprintf("call void @__kml_rl_close(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a readline Interface has no method '%s'", pos.Line, pos.Col, method)
}

// rlStoreField GEPs rl field idx and stores a ptr into it.
func (e *Emitter) rlStoreField(rl string, idx int, val string) {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, rlStructIR, rl, idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, slot))
}
