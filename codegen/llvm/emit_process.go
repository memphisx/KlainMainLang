// emit_process.go — process.argv, process.exit(code), process.env.KEY / process.env["KEY"].
package llvm

import (
	"fmt"
	"runtime"

	"KlainMainLang/ast"
)

// isProcessEnvExpr reports whether expr is exactly `process.env` (non-optional).
func (e *Emitter) isProcessEnvExpr(expr ast.Expression) bool {
	mem, ok := expr.(*ast.MemberExpression)
	if !ok || mem.Optional || mem.Property != "env" {
		return false
	}
	id, ok := mem.Object.(*ast.Identifier)
	return ok && id.Name == "process" && !e.isShadowedByLocal(id.Name)
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

// emitProcessExit implements process.exit(code?): runs the registered 'exit'
// listener, then calls C exit(). With no argument it exits with the current
// process.exitCode (0 by default), matching Node.
func (e *Emitter) emitProcessExit(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: process.exit takes 0 or 1 arguments (code?)", pos.Line, pos.Col)
	}
	e.usedProcessLifecycle = true
	var codeRef string
	if len(args) == 1 {
		codeVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		codeRef = e.coerce(codeVal, TypeI64).Ref
	} else {
		codeReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_process_exit_code, align 8", codeReg))
		codeRef = codeReg
	}
	e.ensureExit()
	e.emitInstr(fmt.Sprintf("call void @__kml_run_exit_handlers(i64 %s)", codeRef))
	code32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", code32, codeRef))
	e.emitInstr(fmt.Sprintf("call void @exit(i32 %s)", code32))
	e.emitTerminator("unreachable")
	return Value{Ty: TypeVoid}, nil
}

// emitProcessNextTick implements process.nextTick(fn): enqueue fn onto the
// microtask queue (drained after the current synchronous run, before timers).
// V1 accepts a zero-argument callback only (Node forwards extra args to fn).
func (e *Emitter) emitProcessNextTick(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: process.nextTick takes exactly 1 argument (a () => void callback)", pos.Line, pos.Col)
	}
	cbPtr, err := e.timerCallbackPtr(args[0], "process.nextTick", pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureMicrotasks()
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", cbPtr))
	return Value{Ty: TypeVoid}, nil
}

// emitProcessUptime implements process.uptime(): seconds (a double) since the
// process started.
func (e *Emitter) emitProcessUptime(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: process.uptime takes no arguments", pos.Line, pos.Col)
	}
	e.ensureProcessUptime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_process_uptime()", r))
	return Value{Ref: r, Ty: TypeF64}, nil
}

// emitProcessHrtime implements process.hrtime(): the high-resolution monotonic
// time as a [seconds, nanoseconds] tuple. V1 takes no argument (the legacy
// diff-from-a-previous-reading form is not supported — use process.hrtime.bigint
// and subtract).
func (e *Emitter) emitProcessHrtime(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: process.hrtime takes no arguments in V1 (use process.hrtime.bigint())", pos.Line, pos.Col)
	}
	e.ensureProcessHrtime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_hrtime()", r))
	return Value{Ref: r, Ty: TupleType([]Type{TypeI64, TypeI64})}, nil
}

// emitProcessHrtimeBigint implements process.hrtime.bigint(): the monotonic
// time as total nanoseconds, a bigint.
func (e *Emitter) emitProcessHrtimeBigint(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: process.hrtime.bigint takes no arguments", pos.Line, pos.Col)
	}
	e.ensureProcessHrtime()
	e.ensureBigInt()
	ns := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_process_hrtime_ns()", ns))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 %s)", r, ns))
	return Value{Ref: r, Ty: BigIntType()}, nil
}

// emitGetenvCall calls C getenv() on the given key pointer, returning a
// possibly-null string ptr (nil when the variable isn't set) — same convention
// as emitArrayFind: a plain TypePtr the caller compares against null.
func (e *Emitter) emitGetenvCall(keyPtr string) Value {
	e.ensureGetenv()
	e.ensureStrHeaderRuntime()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @getenv(ptr %s)", raw, keyPtr))
	// TDD-00120: getenv returns a foreign pointer into the environ block with no
	// length header — copy it into a length-prefixed string (null stays null).
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", result, raw))
	return Value{Ref: result, Ty: TypePtr}
}

// emitProcessEnvSet implements `process.env.KEY = val` / `process.env["KEY"] =
// val` via setenv(key, String(val), overwrite=1). The value is coerced to a
// string (Node stringifies env values).
func (e *Emitter) emitProcessEnvSet(keyPtr string, valExpr ast.Expression, pos ast.Pos) (Value, error) {
	valVal, err := e.emitExpr(valExpr)
	if err != nil {
		return Value{}, err
	}
	strVal, err := e.emitValueToString(valVal)
	if err != nil {
		return Value{}, err
	}
	e.ensureSetenvDecl()
	e.emitInstr(fmt.Sprintf("call i32 @setenv(ptr %s, ptr %s, i32 1)", keyPtr, strVal.Ref))
	// An assignment expression evaluates to the assigned value.
	return strVal, nil
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
	e.ensureStrHeaderRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", r))
	// getcwd returns a raw malloc'd libc string with no length header — copy
	// it into a length-prefixed string so `.length` and other header-based
	// string ops work (TDD-00120), same as env/execPath. A pre-existing bug:
	// `.length`/slice on process.cwd() previously read a bogus -8 header.
	boxed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", boxed, r))
	return Value{Ref: boxed, Ty: TypePtr}, nil
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

// emitProcessExecPath implements the process.execPath property read: the
// absolute path of the running executable. This compiler has no separate
// runtime binary — the compiled program *is* the executable — so execPath is
// its own argv[0] (the same value real Node fills for a bundled/SEA binary),
// loaded from the @__argv_ptr backing the argv array.
func (e *Emitter) emitProcessExecPath() (Value, error) {
	e.ensureExecPath()
	e.ensureStrHeaderRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_execpath()", r))
	// __kml_execpath returns a raw libc string (realpath/readlink buffer) with
	// no length header — copy it into a length-prefixed string so `.length`
	// and other header-based string ops work (TDD-00120), same as env/cwd.
	boxed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", boxed, r))
	return Value{Ref: boxed, Ty: TypePtr}, nil
}

// emitProcessEmitWarning implements process.emitWarning(message, type?): writes
// `(node:<pid>) <type>: <message>` to stderr, matching Node's default warning
// format (the `type` defaults to "Warning", as Node's does). The richer
// options-object form (`{ code, detail }`) and the `'unhandledRejection'`-style
// process 'warning' event are out of V1 scope — this is the plain textual
// emission the vast majority of callers use.
func (e *Emitter) emitProcessEmitWarning(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: process.emitWarning takes (message, type?)", pos.Line, pos.Col)
	}
	msg, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if msg.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: process.emitWarning's message must be a string", pos.Line, pos.Col)
	}
	typeRef := e.internString("Warning")
	if len(args) == 2 {
		t, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		if t.Ty.IR != "ptr" {
			return Value{}, fmt.Errorf("%d:%d: process.emitWarning's type must be a string", pos.Line, pos.Col)
		}
		typeRef = t.Ref
	}
	pid, err := e.emitProcessPid()
	if err != nil {
		return Value{}, err
	}
	e.ensureDprintf()
	fmtStr := e.internString("(node:%lld) %s: %s\n")
	e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, i64 %s, ptr %s, ptr %s)", fmtStr, pid.Ref, typeRef, msg.Ref))

	// If a process.on('warning', handler) is registered, invoke it with the
	// warning as an Error object (name = type, message). The stderr print above
	// still happens (Node's always-on default warning printer).
	e.ensureProcessWarningHandler()
	e.ensureExceptionHelpers()
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_process_warning_handler, align 8", h))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, h))
	fireL := e.freshLabel("warn.fire")
	doneL := e.freshLabel("warn.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, doneL, fireL))
	e.emitLabel(fireL)
	errObj := e.buildErrorObj(0, msg.Ref, typeRef)
	fp := e.freshReg()
	fpp := e.freshReg()
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpp, h))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, h))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))
	e.emitInstr(fmt.Sprintf("call void (ptr, ptr) %s(ptr %s, ptr %s)", fp, ep, errObj))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// ensureProcessWarningHandler emits the process 'warning'-listener slot once.
func (e *Emitter) ensureProcessWarningHandler() {
	if e.usedProcessWarning {
		return
	}
	e.usedProcessWarning = true
	e.emitGlobal("@__kml_process_warning_handler = internal global ptr null, align 8")
}

// emitProcessVersion implements the process.version property read: `"v" +`
// the Node compatibility baseline (TDD-00136) — the pinned Node release this
// compiler's API is measured against.
func (e *Emitter) emitProcessVersion() (Value, error) {
	return Value{Ref: e.internString("v" + nodeCompatVersion), Ty: TypePtr}, nil
}

// processVersionsType is the fixed shape of the process.versions object —
// declared once so member-type inference and the emitted object agree on field
// order (node, v8, klain).
func processVersionsType() Type {
	return ObjectType([]Field{
		{Name: "node", Ty: TypePtr},
		{Name: "v8", Ty: TypePtr},
		{Name: "klain", Ty: TypePtr},
	})
}

// emitProcessVersions implements the process.versions property read (TDD-00136).
// V1 reports only values that are either the agreed compatibility baseline
// (`node`/`v8`, the pinned Node release) or genuinely ours (`klain`, this
// compiler's version) — deliberately *not* fabricated versions for bundled
// libraries this compiler doesn't ship (Node's `uv`/`undici`/`icu`/… would be
// made up). The real linked-library versions we *can* report honestly
// (`openssl` when the crypto/tls backend is OpenSSL, `zlib` when linked, …) are
// a follow-on, since they require querying the linked lib at runtime and
// backend-aware conditional linking.
func (e *Emitter) emitProcessVersions(pos ast.Pos) (Value, error) {
	props := []ast.ObjectProperty{
		{Key: "node", Value: ast.NewStringLiteral(nodeCompatVersion, pos)},
		{Key: "v8", Value: ast.NewStringLiteral(nodeCompatV8, pos)},
		{Key: "klain", Value: ast.NewStringLiteral(klainVersion, pos)},
	}
	return e.emitObjectLiteral(ast.NewObjectLiteral(props, pos))
}

// memoryUsageType is the fixed shape of the process.memoryUsage() object,
// matching Node's field set and order. All fields are byte counts (i64, the
// default number representation).
func memoryUsageType() Type {
	return ObjectType([]Field{
		{Name: "rss", Ty: TypeI64},
		{Name: "heapTotal", Ty: TypeI64},
		{Name: "heapUsed", Ty: TypeI64},
		{Name: "external", Ty: TypeI64},
		{Name: "arrayBuffers", Ty: TypeI64},
	})
}

// emitProcessMemoryUsage implements process.memoryUsage(): an object with the
// same shape Node returns. This compiler has no managed V8 heap, so the only
// field with a real value is `rss` — the process's peak resident set size,
// read from getrusage(2)'s ru_maxrss (bytes on macOS, kilobytes on Linux, so
// the Linux path scales by 1024). heapTotal/heapUsed/external/arrayBuffers are
// V8-specific and report 0 (calloc-zeroed), disclosed as a caveat rather than
// fabricated. ru_maxrss sits at byte offset 32 of struct rusage on both
// platforms (two struct timevals — 32 bytes — precede it).
func (e *Emitter) emitProcessMemoryUsage(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: process.memoryUsage takes no arguments", pos.Line, pos.Col)
	}
	e.ensureGetrusage()
	e.ensureCalloc()

	// struct rusage is ~144 bytes on both platforms; over-allocate to be safe.
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = alloca [256 x i8], align 8", buf))
	e.emitInstr(fmt.Sprintf("call i32 @getrusage(i32 0, ptr %s)", buf))
	maxrssPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 32", maxrssPtr, buf))
	maxrss := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", maxrss, maxrssPtr))
	rss := maxrss
	if runtime.GOOS == "linux" {
		scaled := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 1024", scaled, maxrss))
		rss = scaled
	}

	ty := memoryUsageType()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()
	idx, _, _ := ty.FieldIndex("rss")
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, dataReg, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", rss, gep))
	return Value{Ref: dataReg, Ty: ty}, nil
}

// emitProcessOn implements process.on('SIGINT' | 'SIGTERM', handler): TDD-00019.
// The event name must be a compile-time string literal, exactly "SIGINT" or
// "SIGTERM" — dynamic/computed event names are a clean compile error, the
// same precedent Object.hasOwn's dynamic-key rejection already sets
// (emitHasOwnProperty). handler must be a zero-argument, void-returning
// closure, validated via timerCallbackPtr — the same shape setTimeout/
// setInterval's own callback already requires. Only one handler per signal
// is kept (registering the same signal twice overwrites the previous
// handler), the same single-slot narrowing console.time() already uses.
// The handler only ever fires from ordinary control flow at the top of the
// event loop's own iteration (__kml_event_loop_run/__kml_timer_drain), never
// from real signal context — see ensureSignalHandlerRuntime's doc comment
// (runtime_process.go) for why.
func (e *Emitter) emitProcessOn(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: process.on takes exactly 2 arguments (event, handler)", pos.Line, pos.Col)
	}
	eventLit, ok := args[0].(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: process.on requires a string literal event name (dynamic event names are not supported)", pos.Line, pos.Col)
	}

	switch eventLit.Value {
	case "SIGINT":
		closurePtr, err := e.timerCallbackPtr(args[1], "process.on", pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureSignalRegisteredSigint()
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_sigint_closure", closurePtr))
	case "SIGTERM":
		closurePtr, err := e.timerCallbackPtr(args[1], "process.on", pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureSignalRegisteredSigterm()
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_sigterm_closure", closurePtr))
	case "SIGWINCH":
		// TDD-00031: terminal-resize handler. Same zero-arg void closure shape
		// and same event-loop-drained delivery as SIGINT/SIGTERM — the handler
		// re-reads process.stdout.columns/.rows itself.
		closurePtr, err := e.timerCallbackPtr(args[1], "process.on", pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureSignalRegisteredSigwinch()
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_sigwinch_closure", closurePtr))
	case "exit":
		// The 'exit' listener receives the exit code: (code: number) => void.
		// The param is a `number` (float64, TDD-00123) — the runtime's
		// __kml_run_exit_handlers sitofp's the i64 code to a double at the call.
		cb, err := e.resolveCallbackWithHints(args[1], []Type{TypeF64})
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: a process 'exit' listener must be an arrow function literal", pos.Line, pos.Col)
		}
		e.usedProcessLifecycle = true
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_exit_handler, align 8", cb.hdrPtr))
	case "uncaughtException":
		// The listener receives the thrown Error: (err) => void.
		cb, err := e.resolveCallbackWithHints(args[1], []Type{errorObjType})
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: a process 'uncaughtException' listener must be an arrow function literal", pos.Line, pos.Col)
		}
		e.usedProcessLifecycle = true
		e.ensureExceptionHelpers() // the hook lives in __kml_throw's uncaught path
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_uncaught_handler, align 8", cb.hdrPtr))
	case "message":
		// The fork IPC channel's child side (TDD-00141): (msg: string) => void.
		// Arms the channel (parses NODE_CHANNEL_FD) and holds the event loop
		// open while it stays connected.
		contextTypeArrowParams(args[1], "string")
		cb, err := e.resolveCallbackWithHints(args[1], []Type{TypePtr})
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: a process 'message' listener must be an arrow function literal", pos.Line, pos.Col)
		}
		e.ensureIPCChildRuntime()
		fd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_ipcc_fd()", fd))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_ipcc_msg_listener, align 8", cb.hdrPtr))
	case "warning":
		// The listener receives the warning as an Error: (warning) => void.
		// process.emitWarning still prints to stderr (matching Node's always-on
		// default printer) and additionally invokes this handler.
		cb, err := e.resolveCallbackWithHints(args[1], []Type{errorObjType})
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: a process 'warning' listener must be an arrow function literal", pos.Line, pos.Col)
		}
		e.ensureProcessWarningHandler()
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_process_warning_handler, align 8", cb.hdrPtr))
	default:
		return Value{}, fmt.Errorf("%d:%d: process.on supports 'SIGINT'/'SIGTERM'/'SIGWINCH'/'exit'/'uncaughtException'/'warning' (got %q)", pos.Line, pos.Col, eventLit.Value)
	}
	return Value{Ty: TypeVoid}, nil
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

// emitProcessStreamWrite implements process.stdout.write(s) / process.stderr
// .write(s): a raw write with no auto-appended trailing newline, unlike
// console.log/.error (emit_call_console.go). fd 1 goes through buffered
// printf, matching console.log's own fd=1 convention (never dprintf on fd 1:
// mixing a raw fd write with stdio's own buffered printf on the same
// descriptor risks interleaving output out of order against any console.log
// calls elsewhere in the same program); fd 2 goes through unbuffered
// dprintf, matching console.error's own fd=2 convention.
func (e *Emitter) emitProcessStreamWrite(args []ast.Expression, streamName string, fd int, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: process.%s.write takes exactly 1 argument (s)", pos.Line, pos.Col, streamName)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	val = e.coerce(val, TypePtr)
	fmtPtr := e.internString("%s")
	if fd == 2 {
		e.ensureDprintf()
		e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtPtr, val.Ref))
	} else {
		e.ensurePrintf()
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", fmtPtr, val.Ref))
	}
	return Value{Ty: TypeVoid}, nil
}

// ensureIsatty declares POSIX isatty(3) once — process.<stdio>.isTTY.
func (e *Emitter) ensureIsatty() {
	if !e.usedIsatty {
		e.emitGlobal("declare i32 @isatty(i32 noundef)")
		e.usedIsatty = true
	}
}

// emitProcessStreamIsTTY implements process.stdout/.stderr/.stdin `.isTTY`:
// a real isatty(fd) probe, so piped/redirected runs see false exactly as
// Node does.
func (e *Emitter) emitProcessStreamIsTTY(fd int) Value {
	e.ensureIsatty()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @isatty(i32 %d)", r, fd))
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, r))
	return Value{Ref: b, Ty: TypeBool}
}

// emitProcessSend implements the fork IPC child side's process.send(msg)
// (TDD-00141): a string message written to the NODE_CHANNEL_FD channel.
// Returns false (never throws) when the process wasn't forked.
func (e *Emitter) emitProcessSend(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: process.send takes one message", pos.Line, pos.Col)
	}
	mv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(mv.Ty) {
		return Value{}, fmt.Errorf("%d:%d: process.send supports string messages in this version", pos.Line, pos.Col)
	}
	e.ensureIPCChildRuntime()
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_ipcc_send(ptr %s)", ok, mv.Ref))
	return Value{Ref: ok, Ty: TypeBool}, nil
}

// emitProcessGetID implements process.getuid/geteuid/getgid/getegid — the
// POSIX credential reads (never present on Windows in Node; this compiler is
// POSIX-only, so they're unconditional). Returns a `number`.
func (e *Emitter) emitProcessGetID(which string) Value {
	if !e.usedProcessGetID {
		e.usedProcessGetID = true
		e.emitGlobal("declare i32 @getuid()")
		e.emitGlobal("declare i32 @geteuid()")
		e.emitGlobal("declare i32 @getgid()")
		e.emitGlobal("declare i32 @getegid()")
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @%s()", r, which))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", w, r))
	d := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", d, w))
	return Value{Ref: d, Ty: TypeF64}
}
