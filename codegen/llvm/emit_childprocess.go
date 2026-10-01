// emit_childprocess.go — the code-generated process handle cluster's
// workers still use (worker.process: 'message'/'exit'/'close'/'error'
// listeners, send, kill, disconnect) and the fork IPC wire helpers shared
// with the child's side of the channel (process.send/on('message')). The
// `child_process` module itself is lib/node/child_process.ts (TDD-00234).
//
// Listener registration mirrors the Worker posture (emit_worker.go): one
// listener per event, an arrow/function-expression literal only, stored as a
// raw closure header the runtime dispatch invokes directly.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// cpSpawnOptions handles spawn()'s optional 3rd options argument (ADR-00433):
// `cwd` is wired through (child chdir); `shell` is accepted and ignored — on
// the POSIX corpus its value is `common.isWindows` (false), and commands are
// always exec'd directly, never through a shell (disclosed caveat); `stdio`
// accepts the literal 'pipe' (the only wiring this runtime has). Anything
// else — `env`, `detached`, stdio arrays — is a clean rejection. Accepts an
// object literal or a variable-bound typed object (same treatment as the
// http client options, ADR-00429).
// cpSpawnOpts is the resolved spawn options (ADR-00433/00740/00762): cwdRef and
// envRef are "null" or a value register; shell/windowsHide are compile-time bools.
type cpSpawnOpts struct {
	cwdRef      string
	envRef      string
	timeoutRef  string // "0" or an i64 ms register (ADR-00764)
	killSig     int    // signal for the timeout kill (default 15 = SIGTERM)
	shell       bool
	windowsHide bool
	detached    bool   // setsid (POSIX) / DETACHED_PROCESS (Windows) — ADR-00765
	stdioModes  int    // per-fd stdio: 2 bits each (stdin/stdout/stderr), 0=pipe 1=inherit 2=ignore — ADR-00766
	maxBufRef   string // exec/execFile: i64 bytes per stream before the kill ("0" = spawn's unlimited) — ADR-01080
	shellFile   string // `shell: <path>`: the shell to run the command with ("" = the platform default)
}

// cpStoreField GEPs cp field idx and stores a ptr into it.
// cpAppendListener appends a closure header to one of the LIST-shaped
// listener slots (close 10 / exit 11 / error 12 / message 18) in
// registration order — see ensureCPListenerAppend.
func (e *Emitter) cpAppendListener(cp string, idx int, hdr string) {
	e.ensureCPListenerAppend()
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, cpStructIR, cp, idx))
	e.emitInstr(fmt.Sprintf("call void @__kml_cp_listener_append(ptr %s, ptr %s)", slot, hdr))
}

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
			hdr, err := e.cpExitCloseAdapter(args[1], pos)
			if err != nil {
				return Value{}, err
			}
			idx := 10
			if evt == "exit" {
				idx = 11
			}
			e.cpAppendListener(objVal.Ref, idx, hdr)
		case "error":
			cb, err := e.cpArrowClosure(args[1], []Type{errorObjType}, pos)
			if err != nil {
				return Value{}, err
			}
			e.cpAppendListener(objVal.Ref, 12, cb)
		case "message":
			// The fork IPC channel (TDD-00141), json serialization mode: the
			// stored value is a wire adapter that hands an `any`-typed
			// listener the faithful sent value (string, or parsed
			// object/array/number/boolean) and a string-typed one the text.
			hdr, err := e.cpMessageAdapter(args[1], pos)
			if err != nil {
				return Value{}, err
			}
			e.cpAppendListener(objVal.Ref, 18, hdr)
		default:
			return Value{}, fmt.Errorf("%d:%d: child.on supports 'close', 'exit', 'error' and 'message' (got '%s')", pos.Line, pos.Col, evt)
		}
		return Value{Ty: TypeVoid}, nil
	case "send":
		// fork IPC (TDD-00141), json serialization mode: a string crosses as
		// a quoted line, any other value (object/array/number/boolean/any) is
		// JSON.stringify'd and framed verbatim.
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: child.send takes one message", pos.Line, pos.Col)
		}
		e.ensureCPForkRuntime()
		ref, rawJSON, err := e.emitWireSend(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		sendFn := "@__kml_cp_send"
		if rawJSON {
			sendFn = "@__kml_cp_send_json"
		}
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 %s(ptr %s, ptr %s)", ok, sendFn, objVal.Ref, ref))
		return Value{Ref: ok, Ty: TypeBool}, nil
	case "disconnect":
		e.ensureCPForkRuntime()
		e.emitInstr(fmt.Sprintf("call void @__kml_cp_disconnect(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	case "kill":
		e.ensureCPKill()
		sig := "15" // SIGTERM
		if len(args) == 1 {
			// Node's kill accepts a signal name ('SIGTERM') or a number. A string
			// literal resolves to the host's signal number at compile time
			// (TDD-00184); a bare number passes through. A dynamic (non-literal)
			// string signal name is not supported yet — documented caveat.
			if sl, ok := args[0].(*ast.StringLiteral); ok {
				n, ok := e.cpSignalNumber(sl.Value)
				if !ok {
					return Value{}, fmt.Errorf("%d:%d: child.kill: unknown signal %q", pos.Line, pos.Col, sl.Value)
				}
				sig = fmt.Sprintf("%d", n)
			} else {
				sv, err := e.emitExpr(args[0])
				if err != nil {
					return Value{}, err
				}
				if isStringTy(sv.Ty) {
					return Value{}, fmt.Errorf("%d:%d: child.kill supports a string *literal* signal name or a numeric signal (a dynamic signal-name string is not supported yet)", pos.Line, pos.Col)
				}
				sig = e.coerce(sv, TypeI64).Ref
			}
		}
		// Record the signal so the 'exit'/'close' (code, signal) event can name
		// it. POSIX also recovers it from the wait status (WIFSIGNALED), but on
		// Windows TerminateProcess leaves no signalled bit, so field 21 is the
		// only source there (TDD-00184).
		ksSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 21", ksSlot, cpStructIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sig, ksSlot))
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
	case "unref", "ref":
		// unref() drops the child from the loop's keepalive so the parent can
		// exit without waiting for it; ref() re-references it. Field 24 = the
		// unref flag (ADR-00767). Returns the ChildProcess, so the calls chain.
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: child.%s takes no arguments", pos.Line, pos.Col, method)
		}
		flag := "1"
		if method == "ref" {
			flag = "0"
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 24", slot, cpStructIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", flag, slot))
		return Value{Ref: objVal.Ref, Ty: ChildProcessType()}, nil
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
		ptrRef, lenRef, err := e.bytesResolveInput(args[0], pos) // same string/Buffer/ArrayBuffer/DataView normalization
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
	// A Uint8Array 'data' chunk crosses from the runtime as a raw (ptr, len)
	// pair; its object-reference array parameter expects a header (TDD-00127),
	// so wrap the listener in the header-boxing adapter. Non-array listeners
	// ('end'/'close'/…) pass through unchanged.
	if len(cb.ty.FuncParams) > 0 && cb.ty.FuncParams[0].IsArray {
		return e.chunkHeaderAdapterClosure(cb.hdrPtr), nil
	}
	return cb.hdrPtr, nil
}

// emitWireMessageArg materializes one IPC wire message for a listener
// parameter of type p: the channel delivers (ptr msg, i1 isstr) — a quoted
// string line arrives unquoted with isstr=1, any other JSON value as its raw
// text with isstr=0 (Node's json serialization mode, TDD-00141). A
// string-typed parameter takes the text as-is; an `any` parameter gets the
// faithful Node value — the string boxed, or the JSON parsed to a dynamic
// tree (objects/arrays/numbers/booleans cross as themselves). Returns the
// call-operand string ("<ir> <ref>").
func (e *Emitter) emitWireMessageArg(p Type, msgReg, isstrReg string) (string, error) {
	if !p.IsDynamic {
		return storageIR(p) + " " + msgReg, nil
	}
	strL := e.freshLabel("wmsg.str")
	jsonL := e.freshLabel("wmsg.json")
	doneL := e.freshLabel("wmsg.done")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isstrReg, strL, jsonL))
	e.emitLabel(strL)
	boxed, err := e.emitBoxValue(Value{Ref: msgReg, Ty: TypePtr})
	if err != nil {
		return "", err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(jsonL)
	parsed, err := e.emitJSONParseValue(Value{Ref: msgReg, Ty: TypePtr}, TypeAny, ast.Pos{})
	if err != nil {
		return "", err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", parsed.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, slot))
	return "i64 " + out, nil
}

// cpMessageAdapter wraps a user 'message' listener in the wire's 3-arg ABI
// void(ptr env, ptr msg, i1 isstr) — env is the user closure header. The
// untyped parameter defaults to `any` (JS-faithful: Node delivers whatever
// was sent); an explicit `msg: string` annotation keeps the plain-text fast
// path.
func (e *Emitter) cpMessageAdapter(arg ast.Expression, pos ast.Pos) (string, error) {
	contextTypeArrowParams(arg, "any")
	cb, err := e.resolveCallbackWithHints(arg, []Type{TypeAny})
	if err != nil {
		return "", err
	}
	if cb.kind != cbClosure {
		return "", fmt.Errorf("%d:%d: a 'message' listener must be an arrow function literal", pos.Line, pos.Col)
	}
	params := cb.ty.FuncParams

	fn := fmt.Sprintf("@__kml_cp_msg_adapter_%d", e.closureCtr)
	e.closureCtr++
	restore := e.beginThunkEmit()
	rfpp := e.freshReg()
	rfp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", rfpp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rfp, rfpp))
	repp := e.freshReg()
	rep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", repp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rep, repp))
	argParts := []string{"ptr " + rep}
	for i, p := range params {
		if i == 0 {
			part, aerr := e.emitWireMessageArg(p, "%msg", "%isstr")
			if aerr != nil {
				restore()
				return "", aerr
			}
			argParts = append(argParts, part)
			continue
		}
		argParts = append(argParts, storageIR(p)+" "+zeroRef(p))
	}
	e.emitInstr(fmt.Sprintf("call void %s(%s)", rfp, strings.Join(argParts, ", ")))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env, ptr %%msg, i1 %%isstr) {\nentry:\n%sret void\n}\n", fn, body))
	return e.buildBuiltinClosure(fn, cb.hdrPtr), nil
}

// emitWireSend serializes one send(x) argument for the IPC wire and returns
// (valueRef, rawJSON bool): a plain string keeps the quoted-line fast path;
// anything else (any/object/array/number/boolean) is JSON.stringify'd and
// framed verbatim — Node's json serialization mode.
func (e *Emitter) emitWireSend(arg ast.Expression, pos ast.Pos) (string, bool, error) {
	mv, err := e.emitExpr(arg)
	if err != nil {
		return "", false, err
	}
	if isStringTy(mv.Ty) && !mv.Ty.IsDynamic {
		return mv.Ref, false, nil
	}
	sv, err := e.emitJSONStringifyValue(mv, jsonIndent{})
	if err != nil {
		return "", false, err
	}
	return sv.Ref, true, nil
}

// cpExitCloseAdapter wraps a user 'exit'/'close' listener in a fixed-ABI adapter
// __kml_cp_finalize can call uniformly — void(ptr env, i1 present, double code,
// ptr signal) — forwarding (code, signal) to the listener with its own declared
// arity/param types (TDD-00184). A 1-arg `(code)` listener keeps a plain
// `number` (0 on a signalled death); typing the first param `number | null`
// opts into the faithful null. The 2nd `signal` param, when declared, is always
// the signal name string (null on a normal exit).
func (e *Emitter) cpExitCloseAdapter(arg ast.Expression, pos ast.Pos) (string, error) {
	// Default untyped params to (number, string); an explicit annotation
	// (e.g. `number | null`) is left intact, so nullable code is opt-in.
	contextTypeArrowParams(arg, "number", "string")
	cb, err := e.resolveCallbackWithHints(arg, nil)
	if err != nil {
		return "", err
	}
	if cb.kind != cbClosure {
		return "", fmt.Errorf("%d:%d: a ChildProcess 'exit'/'close' listener must be an arrow function literal", pos.Line, pos.Col)
	}
	params := cb.ty.FuncParams

	fn := fmt.Sprintf("@__kml_cp_exit_adapter_%d", e.closureCtr)
	e.closureCtr++
	restore := e.beginThunkEmit()
	// %env is the real user closure header; unpack its fn ptr + captured env.
	rfpp := e.freshReg()
	rfp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", rfpp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rfp, rfpp))
	repp := e.freshReg()
	rep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", repp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rep, repp))

	argParts := []string{"ptr " + rep}
	nn := TypeF64
	nn.Nullable = true
	for i, p := range params {
		switch i {
		case 0:
			// Box the reactor's (present, code) into a number|null, then coerce
			// to the declared type: demotes to a plain double for a non-nullable
			// `number`, keeps the { i1, double } box for `number | null`.
			agg := e.makeNullableScalarAgg(nn, "%present", "%code")
			v := e.coerce(Value{Ref: agg, Ty: nn}, p)
			argParts = append(argParts, storageIR(p)+" "+v.Ref)
		case 1:
			// signal: a string pointer, null on a normal exit — ptr → ptr.
			argParts = append(argParts, storageIR(p)+" %signal")
		default:
			// Node passes only (code, signal); a further declared param is
			// undefined — a well-typed zero keeps the call valid.
			argParts = append(argParts, storageIR(p)+" "+zeroRef(p))
		}
	}
	e.emitInstr(fmt.Sprintf("call void %s(%s)", rfp, strings.Join(argParts, ", ")))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env, i1 %%present, double %%code, ptr %%signal) {\nentry:\n%sret void\n}\n", fn, body))
	return e.buildBuiltinClosure(fn, cb.hdrPtr), nil
}

// ensureCPKill declares kill(2) once.
func (e *Emitter) ensureCPKill() {
	if e.usedCPKill {
		return
	}
	e.usedCPKill = true
	e.emitGlobal("declare i32 @kill(i32 noundef, i32 noundef)")
}
