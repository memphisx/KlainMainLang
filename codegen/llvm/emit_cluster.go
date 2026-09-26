// emit_cluster.go — codegen for Node's `cluster` module (TDD-00105): the
// cluster.fork() callable and the cluster.isWorker / cluster.worker
// accessors. cluster.isPrimary lives in emit_http.go (it predates this file,
// reading the shared @__kml_cluster_worker_id global). Backed by
// runtime_cluster.go.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// emitClusterModuleCall dispatches cluster.fork()/on()/once()/disconnect().
func (e *Emitter) emitClusterModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "fork":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: cluster.fork takes no arguments", pos.Line, pos.Col)
		}
		e.ensureClusterRuntime()
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cluster_fork()", w))
		return Value{Ref: w, Ty: ClusterWorkerType()}, nil
	case "on", "once":
		return e.emitClusterOn(args, pos)
	case "setupPrimary", "setupMaster":
		return e.emitClusterSetupPrimary(args, pos)
	case "disconnect":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: cluster.disconnect takes at most one callback", pos.Line, pos.Col)
		}
		e.ensureClusterRuntime()
		e.emitInstr("call void @__kml_cluster_disconnect_all()")
		if len(args) == 1 {
			// The optional completion callback: every channel is closed by the
			// synchronous walk above, so it runs on the next microtask
			// checkpoint (Node fires it once all workers are disconnected).
			cb, err := e.resolveCallbackWithHints(args[0], nil)
			if err != nil {
				return Value{}, err
			}
			if cb.kind != cbClosure {
				return Value{}, fmt.Errorf("%d:%d: cluster.disconnect's callback must be an arrow function literal", pos.Line, pos.Col)
			}
			e.ensureMicrotasks()
			e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", cb.hdrPtr))
		}
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: cluster.%s is not supported (fork/on/once/disconnect/setupPrimary, plus isPrimary/isWorker/worker/workers/settings)", pos.Line, pos.Col, method)
}

// emitClusterSetupPrimary implements cluster.setupPrimary(settings) (and its
// legacy setupMaster alias): `args` (worker argv tail) and `silent` (pipe
// worker stdio to the Worker handle) are honored for every later fork;
// `exec` is a compile-time rejection — under the re-exec-self worker model
// there is no other compiled file to run, a genuine impossibility of native
// compilation (Node re-runs a script file; this program IS the executable).
func (e *Emitter) emitClusterSetupPrimary(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: cluster.setupPrimary takes a settings object literal", pos.Line, pos.Col)
	}
	ol, ok := args[0].(*ast.ObjectLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: cluster.setupPrimary takes a settings object *literal* ({ args?, silent? })", pos.Line, pos.Col)
	}
	e.ensureClusterRuntime()
	for _, prop := range ol.Properties {
		switch prop.Key {
		case "exec", "execArgv":
			return Value{}, fmt.Errorf("%d:%d: cluster.setupPrimary's '%s' cannot be supported: workers re-exec this compiled program itself — there is no separate script file for a worker to run", pos.Line, pos.Col, prop.Key)
		case "args":
			dataReg, lenReg, elemTy, err := e.resolveArrayForHOF(prop.Value, pos)
			if err != nil {
				return Value{}, err
			}
			if !isStringTy(elemTy) {
				return Value{}, fmt.Errorf("%d:%d: cluster.setupPrimary's args must be a string[]", pos.Line, pos.Col)
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_cluster_setup_args, align 8", dataReg))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_cluster_setup_args_len, align 8", lenReg))
		case "silent":
			sv, err := e.emitExpr(prop.Value)
			if err != nil {
				return Value{}, err
			}
			b := e.toBool(sv)
			ext := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", ext, b.Ref))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_cluster_setup_silent, align 8", ext))
		default:
			return Value{}, fmt.Errorf("%d:%d: cluster.setupPrimary supports 'args' and 'silent' ('%s' is not supported)", pos.Line, pos.Col, prop.Key)
		}
	}
	return Value{Ty: TypeVoid}, nil
}

// clusterSettingsType is cluster.settings' object shape.
func clusterSettingsType() Type {
	return ObjectType([]Field{
		{Name: "args", Ty: ArrayOf(TypePtr)},
		{Name: "silent", Ty: TypeBool},
	})
}

// emitClusterSettings reads the stored setupPrimary settings back as
// cluster.settings.
func (e *Emitter) emitClusterSettings() (Value, error) {
	e.ensureClusterRuntime()
	ty := clusterSettingsType()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, ty.StructSize()))
	data := e.freshReg()
	length := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_cluster_setup_args, align 8", data))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_setup_args_len, align 8", length))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, length))
	f0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", f0, ty.StructIR(), obj))
	e.storeArrayFieldHeader(f0, Value{Ref: r1, Ty: ArrayOf(TypePtr)})
	sil := e.freshReg()
	silB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_setup_silent, align 8", sil))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", silB, sil))
	f1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", f1, ty.StructIR(), obj))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", silB, f1))
	return Value{Ref: obj, Ty: ty}, nil
}

// emitClusterOn implements the cluster-level events (TDD-00105): one
// compile-time listener per event (the module's standing convention), relayed
// with the Worker as the first argument. 'exit' and 'message' install a
// fixed-ABI relay adapter into every worker's own listener slot — existing
// workers immediately (install walks the registry) and future forks at fork
// time; a cluster-level relay therefore shares the slot with worker.on(...)
// for the same event (last registration wins). 'fork' fires synchronously
// inside cluster.fork() and 'online' from a queued microtask — both only for
// forks after the registration, matching Node.
func (e *Emitter) emitClusterOn(args []ast.Expression, pos ast.Pos) (Value, error) {
	evt, err := stringLiteralArg(args, 0, "cluster.on", pos)
	if err != nil {
		return Value{}, err
	}
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: cluster.on takes (event, listener)", pos.Line, pos.Col)
	}
	e.ensureClusterRuntime()
	var key string
	switch evt {
	case "exit":
		key = "exit"
	case "message":
		key = "msg"
	case "fork":
		key = "fork"
	case "online":
		key = "online"
	case "disconnect":
		key = "disc"
	case "listening":
		key = "listening"
	default:
		return Value{}, fmt.Errorf("%d:%d: cluster.on supports 'exit', 'message', 'fork', 'online', 'disconnect' and 'listening' (got '%s')", pos.Line, pos.Col, evt)
	}
	fn, userHdr, err := e.clusterRelayAdapter(args[1], key, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_cluster_relay_%s_fn, align 8", fn, key))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_cluster_relay_%s_hdr, align 8", userHdr, key))
	// exit/message also arm the already-forked workers; fork/online fire only
	// for future forks (Node parity — you can't observe a fork that happened).
	if key == "exit" || key == "msg" {
		e.emitInstr(fmt.Sprintf("call void @__kml_cluster_install_%s()", key))
	}
	return Value{Ty: TypeVoid}, nil
}

// clusterRelayAdapter resolves the user listener and emits the per-event
// fixed-ABI relay function (builder-swap pattern), returning (adapter fn
// name, user closure header). ABIs:
//
//	exit:    void(ptr envPair, i1 present, double code, ptr signal) — the
//	         same shape __kml_cp_finalize calls for cp field 11, with envPair
//	         = { userHdr, worker } instead of the bare user header
//	msg:     void(ptr envPair, ptr message) — cp field 18's shape
//	fork:    void(ptr userHdr, ptr worker) — called directly by
//	         __kml_cluster_fork
//	online:  void(ptr envPair) — a microtask closure
func (e *Emitter) clusterRelayAdapter(arg ast.Expression, key string, pos ast.Pos) (fn, userHdr string, err error) {
	// Context-type via annotation names (not hints): the annotation route
	// survives a `test` mustCall wrapper, which resolveCallbackWithHints'
	// hint route does not.
	switch key {
	case "exit":
		contextTypeArrowParams(arg, "ClusterWorker", "number", "string")
	case "msg":
		contextTypeArrowParams(arg, "ClusterWorker", "any")
	case "listening":
		contextTypeArrowParams(arg, "ClusterWorker", "ClusterAddress")
	default: // fork, online, disc
		contextTypeArrowParams(arg, "ClusterWorker")
	}
	cb, err := e.resolveCallbackWithHints(arg, nil)
	if err != nil {
		return "", "", err
	}
	if cb.kind != cbClosure {
		return "", "", fmt.Errorf("%d:%d: a cluster.on listener must be an arrow function literal", pos.Line, pos.Col)
	}
	params := cb.ty.FuncParams

	fn = fmt.Sprintf("@__kml_cluster_%s_relay_%d", key, e.closureCtr)
	e.closureCtr++
	restore := e.beginThunkEmit()

	// Unpack the worker + the real user closure from %env. 'fork' passes the
	// user header directly and the worker as a parameter.
	var worker, uhdr string
	if key == "fork" {
		uhdr = "%env"
		worker = "%worker"
	} else {
		up := e.freshReg()
		uhdr = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", up))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", uhdr, up))
		wp := e.freshReg()
		worker = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", wp))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", worker, wp))
	}
	rfpp := e.freshReg()
	rfp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", rfpp, uhdr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rfp, rfpp))
	repp := e.freshReg()
	rep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", repp, uhdr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rep, repp))

	// 'listening' materializes the Node address object from the announced
	// port and host: { address, port, addressType } (address null when the
	// server listened on every address).
	var addrObj string
	if key == "listening" {
		aty := clusterAddressType()
		addrObj = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", addrObj, aty.StructSize()))
		f0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", f0, aty.StructIR(), addrObj))
		e.emitInstr(fmt.Sprintf("store ptr %%host, ptr %s, align 8", f0))
		portD := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %%port to double", portD))
		f1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", f1, aty.StructIR(), addrObj))
		e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", portD, f1))
		f2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", f2, aty.StructIR(), addrObj))
		e.emitInstr(fmt.Sprintf("store double 4.0, ptr %s, align 8", f2))
	}

	argParts := []string{"ptr " + rep}
	nn := TypeF64
	nn.Nullable = true
	for i, p := range params {
		switch {
		case i == 0:
			argParts = append(argParts, storageIR(p)+" "+worker)
		case i == 1 && key == "listening":
			argParts = append(argParts, storageIR(p)+" "+addrObj)
		case i == 1 && key == "exit":
			// (present, code) → number|null box, coerced to the declared type
			// (plain double for a non-nullable `number`) — same as
			// cpExitCloseAdapter.
			agg := e.makeNullableScalarAgg(nn, "%present", "%code")
			v := e.coerce(Value{Ref: agg, Ty: nn}, p)
			argParts = append(argParts, storageIR(p)+" "+v.Ref)
		case i == 2 && key == "exit":
			argParts = append(argParts, storageIR(p)+" %signal")
		case i == 1 && key == "msg":
			part, aerr := e.emitWireMessageArg(p, "%message", "%isstr")
			if aerr != nil {
				restore()
				return "", "", aerr
			}
			argParts = append(argParts, part)
		default:
			argParts = append(argParts, storageIR(p)+" "+zeroRef(p))
		}
	}
	e.emitInstr(fmt.Sprintf("call void %s(%s)", rfp, strings.Join(argParts, ", ")))
	body := e.allocas.String() + e.body.String()
	restore()

	var sig string
	switch key {
	case "exit":
		sig = "ptr %env, i1 %present, double %code, ptr %signal"
	case "msg":
		sig = "ptr %env, ptr %message, i1 %isstr"
	case "fork":
		sig = "ptr %env, ptr %worker"
	case "listening":
		sig = "ptr %env, i64 %port, ptr %host"
	default: // online, disc
		sig = "ptr %env"
	}
	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(%s) {\nentry:\n%sret void\n}\n", fn, sig, body))
	return fn, cb.hdrPtr, nil
}

// emitClusterWorkers implements cluster.workers as the LIVE Worker registry
// (exited workers are dropped by the reap hook, like Node's delete), surfaced
// as a Worker[] snapshot copy — copied because a worker exit mid-iteration
// compacts the underlying table. Node's id-keyed object shape has no typed
// equivalent here; iteration (`for (const w of cluster.workers)`) and by-id
// indexing (`cluster.workers[id]`, see emitClusterWorkerByID) are the
// observable contract.
func (e *Emitter) emitClusterWorkers() (Value, error) {
	e.ensureClusterRuntime()
	e.ensureMemcpy()
	data := e.freshReg()
	length := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_cluster_wrks, align 8", data))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_pid_len, align 8", length))
	bytes := e.freshReg()
	snap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, length))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", snap, bytes))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", snap, data, bytes))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, snap))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, length))
	return Value{Ref: r1, Ty: ArrayOf(ClusterWorkerType())}, nil
}

// emitClusterIsWorker implements cluster.isWorker (= worker id != 0).
func (e *Emitter) emitClusterIsWorker() (Value, error) {
	e.ensureHTTPClusterFork()
	id := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_worker_id, align 8", id))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", r, id))
	return Value{Ref: r, Ty: TypeBool}, nil
}

// emitClusterWorkerMember reads a Worker handle's `.id`.
func (e *Emitter) emitClusterWorkerMember(objExpr ast.Expression, prop string, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch prop {
	case "id":
		slot := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", slot, clusterWorkerIR, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: TypeI64}, nil
	case "process":
		// The underlying ChildProcess handle (`.pid`, streams surface).
		cp := e.clusterWorkerCP(objVal.Ref)
		return Value{Ref: cp, Ty: ChildProcessType()}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a cluster Worker has no property '%s' (V1 exposes .id and .process)", pos.Line, pos.Col, prop)
}

// clusterWorkerCP loads a Worker handle's embedded ChildProcess pointer.
func (e *Emitter) clusterWorkerCP(wRef string) string {
	slot := e.freshReg()
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", slot, clusterWorkerIR, wRef))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cp, slot))
	return cp
}

// emitClusterWorkerMethodCall routes a Worker method through its embedded
// ChildProcess handle: send/disconnect/on('message'/'exit'/'close'/'error')
// ride the fork IPC surface unchanged (TDD-00141); kill/destroy map to the
// ChildProcess kill; 'online' has no wire handshake — the worker was just
// exec'd — so its listener fires as a queued microtask.
func (e *Emitter) emitClusterWorkerMethodCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	cp := e.clusterWorkerCP(objVal.Ref)
	cpVal := Value{Ref: cp, Ty: ChildProcessType()}
	switch method {
	case "on", "once":
		if evt, err2 := stringLiteralArg(args, 0, "worker.on", pos); err2 == nil && evt == "online" {
			if len(args) != 2 {
				return Value{}, fmt.Errorf("%d:%d: worker.on takes (event, listener)", pos.Line, pos.Col)
			}
			cb, err2 := e.cpArrowClosure(args[1], nil, pos)
			if err2 != nil {
				return Value{}, err2
			}
			e.ensureMicrotasks()
			e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", cb))
			return Value{Ty: TypeVoid}, nil
		}
		return e.emitCPHandleMethod(cpVal, "on", args, pos)
	case "send", "disconnect":
		return e.emitCPHandleMethod(cpVal, method, args, pos)
	case "kill", "destroy":
		return e.emitCPHandleMethod(cpVal, "kill", args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: a cluster Worker has no method '%s' (send/disconnect/kill/destroy/on)", pos.Line, pos.Col, method)
}

// clusterSelfWorkerType is `cluster.worker`: the current process's own Worker
// in a forked worker, undefined in the primary (Node's `Worker | undefined`).
func clusterSelfWorkerType() Type {
	t := ClusterWorkerType()
	t.Nullable, t.IsUndefined = true, true
	return t
}

// emitClusterSelfWorker implements `cluster.worker`: in a worker, a Worker
// handle carrying this process's id (Node numbers forked workers from 1);
// in the primary (id 0), undefined. Its IPC channel is the process's own,
// so the handle has no ChildProcess of its own.
func (e *Emitter) emitClusterSelfWorker() (Value, error) {
	e.ensureHTTPClusterFork()
	e.ensureMalloc()
	id := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_worker_id, align 8", id))
	isWorker := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isWorker, id))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	mkL, doneL := e.freshLabel("cluster.self"), e.freshLabel("cluster.self.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isWorker, mkL, doneL))
	e.emitLabel(mkL)
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", w))
	idp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", idp, clusterWorkerIR, w))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", id, idp))
	e.ensureGetpid()
	pid := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @getpid()", pid))
	pid64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", pid64, pid))
	pp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", pp, clusterWorkerIR, w))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pid64, pp))
	cpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", cpp, clusterWorkerIR, w))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", cpp))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", w, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: clusterSelfWorkerType()}, nil
}
