// emit_net.go — the net socket the HTTP server's 'upgrade'/'connection'/
// 'clientError' events hand out (socket.on('data'|'end'), write, end,
// destroy, setNoDelay/setKeepAlive, address), the { address, family, port }
// object `server.address()` and dgram share, and the closure adapters
// child_process and dgram listeners use. Backed by runtime_net.go. (Node's
// `net` module itself is lib/node/net.ts.)
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// netStorePtrField GEPs struct field idx and stores a ptr into it.
func (e *Emitter) netStorePtrField(base, structIR string, idx int, val string) {
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slot, structIR, base, idx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val, slot))
}

// netAddressType is the shape net.Server.address() returns — Node's
// `{ address, family, port }` AddressInfo record. Declared once so member-type
// inference and the emitted struct agree on field order.
func netAddressType() Type {
	return ObjectType([]Field{
		{Name: "address", Ty: TypePtr},
		{Name: "family", Ty: TypePtr},
		{Name: "port", Ty: TypeF64},
	})
}

// emitNetAddressObject builds Node's `{ address, family, port }` AddressInfo
// from a socket/listen fd (an i32 register), reading the real bound address and
// port via getsockname. family is always "IPv4" — this compiler binds/connects
// IPv4 only (ADR-00324/00358), so it never reports Node's dual-stack IPv6.
func (e *Emitter) emitNetAddressObject(fd32 string) Value {
	e.ensureNetRuntime()
	portI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_net_sockname_port(i32 %s)", portI, fd32))
	portD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", portD, portI))
	addrStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_net_sockname_addr(i32 %s)", addrStr, fd32))

	ty := netAddressType()
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitObjAllocInto(dataReg, ty)
	structIR := ty.StructIR()
	store := func(name, ir, val string) {
		idx, _, _ := ty.FieldIndex(name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, dataReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, g))
	}
	store("address", "ptr", addrStr)
	store("family", "ptr", e.internString("IPv4"))
	store("port", "double", portD)
	return Value{Ref: dataReg, Ty: ty}
}

// netFieldFd32 loads a net server/socket handle's fd (field 0) as an i32.
func (e *Emitter) netFieldFd32(ref, structIR string) string {
	g := e.freshReg()
	fd64 := e.freshReg()
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", g, structIR, ref))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, g))
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))
	return fd32
}

// emitNetSocketAddress implements socket.address(): the socket's local
// address+port (e.g. a loopback connection reports `127.0.0.1` + the local
// ephemeral port), via getsockname on the socket fd.
func (e *Emitter) emitNetSocketAddress(objVal Value, pos ast.Pos) (Value, error) {
	return e.emitNetAddressObject(e.netFieldFd32(objVal.Ref, netSocketIR)), nil
}

// emitNetSocketMethod dispatches socket.on/write/end on a connection socket.
func (e *Emitter) emitNetSocketMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "on":
		evt, err := stringLiteralArg(args, 0, "socket.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: socket.on takes (event, listener)", pos.Line, pos.Col)
		}
		switch evt {
		case "data":
			cb, err := e.netArrowClosure(args[1], []Type{BufferType()}, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 2, cb)
		case "end":
			cb, err := e.netArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 3, cb)
		case "close":
			// Fired once on teardown — EOF or explicit end()/destroy()
			// (ADR-00501). No hadError argument (this path has no error
			// teardown to distinguish).
			cb, err := e.netArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 6, cb)
		case "connect", "ready":
			// A 'connect'/'ready' registration on a client socket: stored in the
			// same field-4 slot net.connect's own callback uses; the dispatch pass
			// fires and clears it when the async connect completes.
			cb, err := e.netArrowClosure(args[1], nil, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 4, cb)
		case "error":
			// An 'error' listener (Node's `(err: Error) => …`) stored in field 8,
			// fired by the dispatch pass with a coded Error when an async connect
			// fails (ADR-01021). Taking the errorObjType hint so `err.code` etc.
			// resolve to the real Error fields inside the listener body.
			cb, err := e.netArrowClosure(args[1], []Type{errorObjType}, pos)
			if err != nil {
				return Value{}, err
			}
			e.netStorePtrField(objVal.Ref, netSocketIR, 8, cb)
		default:
			return Value{}, fmt.Errorf("%d:%d: a net socket supports 'data', 'end', 'close', 'error', and 'connect'/'ready' (got '%s')", pos.Line, pos.Col, evt)
		}
		return Value{Ty: TypeVoid}, nil
	case "write":
		e.ensureNetSockIO()
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: socket.write takes (data)", pos.Line, pos.Col)
		}
		ptrRef, lenRef, err := e.bytesResolveInput(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_write(ptr %s, ptr %s, i64 %s)", objVal.Ref, ptrRef, lenRef))
		return Value{Ty: TypeVoid}, nil
	case "end":
		e.ensureNetSockIO()
		if len(args) == 1 {
			ptrRef, lenRef, err := e.bytesResolveInput(args[0], pos)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_write(ptr %s, ptr %s, i64 %s)", objVal.Ref, ptrRef, lenRef))
		} else if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: socket.end takes (data?)", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_close(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	case "address":
		return e.emitNetSocketAddress(objVal, pos)
	case "destroy":
		// Forcibly close the socket (no error argument threaded in V1).
		e.ensureNetSockIO()
		e.emitInstr(fmt.Sprintf("call void @__kml_net_sock_close(ptr %s)", objVal.Ref))
		return objVal, nil
	case "setNoDelay":
		return e.emitNetSocketSockOpt(objVal, args, "nodelay", pos)
	case "setKeepAlive":
		return e.emitNetSocketSockOpt(objVal, args, "keepalive", pos)
	case "setEncoding", "ref", "unref", "pause", "resume", "setTimeout":
		// Accepted for compatibility but a no-op in this compiler's model:
		// `setEncoding` — a socket's 'data' chunk type is already the listener's
		// declared parameter type (Buffer or string); `ref`/`unref` — the
		// program runs to completion regardless of event-loop keep-alive;
		// `pause`/`resume`/`setTimeout` — the blocking read model has no
		// separately-schedulable flow or idle timer. Arguments (a possible
		// timeout callback) are evaluated for side effects, then ignored.
		for _, a := range args {
			if _, err := e.emitExpr(a); err != nil {
				return Value{}, err
			}
		}
		return objVal, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a net socket has no method '%s'", pos.Line, pos.Col, method)
}

// emitNetSocketSockOpt implements socket.setNoDelay(bool?) / setKeepAlive(bool?,
// ms?) via a real setsockopt on the socket fd. The optional boolean argument
// defaults to true (matching Node); setKeepAlive's initial-delay argument is
// accepted but not threaded (no per-socket keepalive-idle tuning in V1).
func (e *Emitter) emitNetSocketSockOpt(objVal Value, args []ast.Expression, which string, pos ast.Pos) (Value, error) {
	e.ensureNetRuntime()
	// enable flag: default 1 (true); an explicit `false` first arg disables.
	enable := "1"
	if len(args) >= 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IR != "i1" {
			return Value{}, fmt.Errorf("%d:%d: socket.%s's first argument must be a boolean", pos.Line, pos.Col, map[string]string{"nodelay": "setNoDelay", "keepalive": "setKeepAlive"}[which])
		}
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i32", z, v.Ref))
		enable = z
	}
	// setKeepAlive's second argument is the keepalive-idle time in
	// milliseconds; capture it (a number → i64) so it can be threaded into
	// TCP_KEEPIDLE below. Any further args are evaluated for side effects.
	var delayVal *Value
	for i := 1; i < len(args); i++ {
		v, err := e.emitExpr(args[i])
		if err != nil {
			return Value{}, err
		}
		if i == 1 && which == "keepalive" {
			dv := v
			delayVal = &dv
		}
	}
	fd32 := e.netFieldFd32(objVal.Ref, netSocketIR)
	valp := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", valp))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", enable, valp))
	var level, optname int
	if which == "nodelay" {
		level, optname = 6, 1 // IPPROTO_TCP, TCP_NODELAY
	} else {
		level, optname = e.netKeepAliveConst()
	}
	e.emitInstr(fmt.Sprintf("call i32 @setsockopt(i32 %s, i32 %d, i32 %d, ptr %s, i32 4)", fd32, level, optname, valp))

	// Node's setKeepAlive(enable, initialDelay): with an idle delay AND
	// enable true, also set the keepalive-idle time — `~~(initialDelay/1000)`
	// seconds — via TCP_KEEPIDLE (macOS TCP_KEEPALIVE). On Windows the shim
	// translates this to SIO_KEEPALIVE_VALS, dodging the TCP_MAXSEG opt-number
	// collision (ADR-00760). Only when both hold, matching libuv.
	if which == "keepalive" && delayVal != nil {
		// The delay is a number; normalise to i64 milliseconds (a literal
		// arrives as double, a typed value may already be i64).
		delayI64 := delayVal.Ref
		if delayVal.Ty.IR == "double" {
			di := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", di, delayVal.Ref))
			delayI64 = di
		}
		sec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 1000", sec, delayI64))
		sec32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", sec32, sec))
		enOK := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", enOK, enable))
		secOK := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", secOK, sec32))
		doIdle := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", doIdle, enOK, secOK))
		setL := e.freshLabel("netkeepidle.set")
		doneL := e.freshLabel("netkeepidle.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doIdle, setL, doneL))
		e.emitLabel(setL)
		secp := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", secp))
		e.emitInstr(fmt.Sprintf("store i32 %s, ptr %s, align 4", sec32, secp))
		idleLevel, idleOpt := e.netKeepIdleConst()
		e.emitInstr(fmt.Sprintf("call i32 @setsockopt(i32 %s, i32 %d, i32 %d, ptr %s, i32 4)", fd32, idleLevel, idleOpt, secp))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
	}
	return objVal, nil
}

// netArrowClosure resolves a listener argument to a closure header pointer,
// requiring an arrow/function-expression literal (the child_process posture).
func (e *Emitter) netArrowClosure(arg ast.Expression, hints []Type, pos ast.Pos) (string, error) {
	cb, err := e.resolveCallbackWithHints(arg, hints)
	if err != nil {
		return "", err
	}
	if cb.kind != cbClosure {
		return "", fmt.Errorf("%d:%d: a net socket listener must be an arrow function literal", pos.Line, pos.Col)
	}
	// The net/dgram runtime delivers a data/message chunk as a raw (ptr, len)
	// pair. A `string` listener consumes that directly, but a `Uint8Array`
	// listener's array parameter is an object-reference array expecting a header
	// pointer (TDD-00127) — wrap it in an adapter that boxes (ptr, len) into a
	// fresh header before forwarding.
	if len(cb.ty.FuncParams) > 0 && cb.ty.FuncParams[0].IsArray {
		return e.chunkHeaderAdapterClosure(cb.hdrPtr), nil
	}
	return cb.hdrPtr, nil
}

// ensureChunkHeaderAdapter emits (once) an adapter of the runtime chunk-listener
// ABI `void(ptr %env, ptr %buf, i64 %n)` whose %env is the *real* listener
// closure {fp, env}. It boxes (%buf, %n) into a fresh {data,len} header and
// forwards to the real closure with (realEnv, header, %n) — the object-reference
// array ABI a `Uint8Array` listener expects (TDD-00127).
func (e *Emitter) ensureChunkHeaderAdapter() string {
	fn := "@__kml_chunk_hdr_adapter"
	if e.chunkHdrAdapterEmitted {
		return fn
	}
	e.chunkHdrAdapterEmitted = true
	e.ensureMalloc()
	restore := e.beginThunkEmit()
	rfpp := e.freshReg()
	rfp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", rfpp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rfp, rfpp))
	repp := e.freshReg()
	rep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", repp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rep, repp))
	hdr := e.newArrayHeader("%buf", "%n")
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s, i64 %%n)", rfp, rep, hdr))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env, ptr %%buf, i64 %%n) {\nentry:\n%sret void\n}\n", fn, body))
	return fn
}

// chunkHeaderAdapterClosure wraps a real Uint8Array data/message listener
// closure so the raw (ptr,len) chunk the runtime delivers is boxed into a header
// first (see ensureChunkHeaderAdapter).
func (e *Emitter) chunkHeaderAdapterClosure(realCloHdr string) string {
	fn := e.ensureChunkHeaderAdapter()
	return e.buildBuiltinClosure(fn, realCloHdr)
}
