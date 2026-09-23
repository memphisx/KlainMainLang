// emit_childprocess_sync.go — the blocking child_process forms (spawnSync /
// execSync / execFileSync) over the spawnsync C sidecar (runtime_spawnsync.go),
// with Node's full options set — cwd, env, input, timeout, killSignal,
// maxBuffer, stdio, shell, argv0, windowsHide, windowsVerbatimArguments,
// encoding — the `{ pid, output, stdout, stderr, status, signal, error }`
// result, and the ExecException execSync throws (`status`/`signal`/`output`/
// `pid`/`stdout`/`stderr` on the Error, ADR-01080).
package llvm

import (
	"fmt"
	"regexp"
	"strconv"

	"KlainMainLang/ast"
)

// cpSyncOpts is the resolved options of one *Sync call. Every ref is an IR
// register or a literal ("null" / "0"); the compile-time knobs are plain Go.
type cpSyncOpts struct {
	cwdRef      string
	envRef      string
	inputRef    string // ptr to the input bytes (a string) or "null"
	inputLenRef string // i64 byte length or "0"
	timeoutRef  string // i64 ms or "0"
	killSig     int
	maxBufRef   string // i64 bytes or the Node default
	stdioModes  int    // 2 bits per fd: 0 pipe 1 inherit 2 ignore
	stdioSet    bool   // an explicit `stdio` (execSync then stops mirroring stderr)
	shellFile   string // "" = no shell; a shell path for `shell: <path>`
	shell       bool
	verbatim    bool
	windowsHide bool
	argv0Ref    string // "null" or a ptr
}

// cpMaxBufferDefault is Node's maxBuffer default (1024 * 1024 bytes).
const cpMaxBufferDefault = 1024 * 1024

var cpCmdShellRE = regexp.MustCompile(`(?i)^(?:.*\\)?cmd(?:\.exe)?$`)

// cpSyncOptions evaluates the optional trailing options object of the *Sync
// forms. Values that Node reads at run time (cwd, input, timeout, maxBuffer,
// env values, argv0) may be any expression of the right type; the knobs
// that change what code is emitted (stdio, shell, killSignal, encoding,
// windowsHide, windowsVerbatimArguments) must be literals. Anything else is
// a clean rejection rather than a silent ignore.
func (e *Emitter) cpSyncOptions(arg ast.Expression, name string, pos ast.Pos) (cpSyncOpts, error) {
	o := cpSyncOpts{cwdRef: "null", envRef: "null", inputRef: "null", inputLenRef: "0", timeoutRef: "0", killSig: 15, maxBufRef: strconv.Itoa(cpMaxBufferDefault), argv0Ref: "null"}
	lit, ok := arg.(*ast.ObjectLiteral)
	if !ok {
		return o, fmt.Errorf("%d:%d: child_process.%s's options must be an object literal", pos.Line, pos.Col, name)
	}
	for _, prop := range lit.Properties {
		switch prop.Key {
		case "cwd":
			v, err := e.emitExpr(prop.Value)
			if err != nil {
				return o, err
			}
			o.cwdRef = e.coerce(v, TypePtr).Ref
		case "argv0":
			v, err := e.emitExpr(prop.Value)
			if err != nil {
				return o, err
			}
			o.argv0Ref = e.coerce(v, TypePtr).Ref
		case "input":
			v, err := e.emitExpr(prop.Value)
			if err != nil {
				return o, err
			}
			if v.Ty.IsArray && v.Ty.ElemType != nil && v.Ty.ElemType.IR == "i8" {
				// A Uint8Array / Buffer: its bytes and length.
				d := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", d, v.Ref))
				l := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", l, v.Ref))
				o.inputRef, o.inputLenRef = d, l
			} else {
				s, err := e.emitValueToString(v)
				if err != nil {
					return o, err
				}
				e.ensureStrHeaderRuntime()
				l := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, s.Ref))
				o.inputRef, o.inputLenRef = s.Ref, l
			}
		case "timeout":
			v, err := e.emitExpr(prop.Value)
			if err != nil {
				return o, err
			}
			o.timeoutRef = e.coerce(v, TypeI64).Ref
		case "maxBuffer":
			v, err := e.emitExpr(prop.Value)
			if err != nil {
				return o, err
			}
			o.maxBufRef = e.coerce(v, TypeI64).Ref
		case "killSignal":
			if sl, ok := prop.Value.(*ast.StringLiteral); ok {
				n, ok := cpSignalNumber(sl.Value)
				if !ok {
					return o, fmt.Errorf("%d:%d: child_process.%s killSignal: unknown signal %q", pos.Line, pos.Col, name, sl.Value)
				}
				o.killSig = n
			} else if nl, ok := prop.Value.(*ast.NumberLiteral); ok {
				n, err := strconv.Atoi(nl.Value)
				if err != nil {
					return o, fmt.Errorf("%d:%d: child_process.%s killSignal must be a signal name or an integer", pos.Line, pos.Col, name)
				}
				o.killSig = n
			} else {
				return o, fmt.Errorf("%d:%d: child_process.%s killSignal must be a literal signal name or number", pos.Line, pos.Col, name)
			}
		case "stdio":
			m, err := e.cpParseStdio(prop.Value, pos)
			if err != nil {
				return o, err
			}
			o.stdioModes = m
			o.stdioSet = true
		case "shell":
			switch v := prop.Value.(type) {
			case *ast.BooleanLiteral:
				o.shell = v.Value
			case *ast.StringLiteral:
				o.shell = true
				o.shellFile = v.Value
			default:
				return o, fmt.Errorf("%d:%d: child_process.%s's shell option must be a literal boolean or shell path", pos.Line, pos.Col, name)
			}
		case "windowsHide", "windowsVerbatimArguments":
			b, ok := prop.Value.(*ast.BooleanLiteral)
			if !ok {
				return o, fmt.Errorf("%d:%d: child_process.%s's %s option must be the literal true or false", pos.Line, pos.Col, name, prop.Key)
			}
			if prop.Key == "windowsHide" {
				o.windowsHide = b.Value
			} else {
				o.verbatim = b.Value
			}
		case "env":
			el, ok := prop.Value.(*ast.ObjectLiteral)
			if !ok {
				return o, fmt.Errorf("%d:%d: child_process.%s's env option must be an object literal of string values", pos.Line, pos.Col, name)
			}
			ref, err := e.cpBuildEnvp(el, pos)
			if err != nil {
				return o, err
			}
			o.envRef = ref
		case "encoding":
			s, ok := prop.Value.(*ast.StringLiteral)
			if !ok || (s.Value != "utf8" && s.Value != "utf-8") {
				return o, fmt.Errorf("%d:%d: child_process.%s supports encoding: 'utf8' only (results are strings)", pos.Line, pos.Col, name)
			}
		default:
			return o, fmt.Errorf("%d:%d: child_process.%s options support { cwd, input, argv0, stdio, env, timeout, killSignal, maxBuffer, encoding, shell, windowsHide, windowsVerbatimArguments } (got '%s')", pos.Line, pos.Col, name, prop.Key)
		}
	}
	return o, nil
}

// cpSyncOptsIR fills the sidecar's kmlss_opts record (ten 8-byte slots:
// cwd, env, input, input_len, timeout_ms, kill_signal, max_buffer, stdio,
// flags, argv0) and returns its pointer. flags bit 0 is shell/verbatim, bit 1
// windowsHide.
func (e *Emitter) cpSyncOptsIR(o cpSyncOpts) string {
	e.ensureMalloc()
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 80)", rec))
	store := func(slot int, ir, val string) {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", g, rec, slot*8))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, g))
	}
	flags := 0
	if o.shell || o.verbatim {
		flags |= 1
	}
	if o.windowsHide {
		flags |= 2
	}
	store(0, "ptr", o.cwdRef)
	store(1, "ptr", o.envRef)
	store(2, "ptr", o.inputRef)
	store(3, "i64", o.inputLenRef)
	store(4, "i64", o.timeoutRef)
	store(5, "i64", strconv.Itoa(o.killSig))
	store(6, "i64", o.maxBufRef)
	store(7, "i64", strconv.Itoa(o.stdioModes))
	store(8, "i64", strconv.Itoa(flags))
	store(9, "ptr", o.argv0Ref)
	return rec
}

// cpSpawnSyncCall evaluates (file, argv, opts) and emits the blocking
// @__kml_cp_spawn_sync call, returning the raw C result-struct pointer
// (kmlss_result: status @0, stdout @8, stderr @16, pid @24, signal @32,
// error @40). An `argv0` replaces the name the child sees as argv[0].
func (e *Emitter) cpSpawnSyncCall(fileRef, argsPtr, argsLen string, o cpSyncOpts) string {
	e.ensureSpawnSyncRuntime()
	opts := e.cpSyncOptsIR(o)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cp_spawn_sync(ptr %s, ptr %s, i64 %s, ptr %s)", r, fileRef, argsPtr, argsLen, opts))
	return r
}

// cpSpawnSyncField loads field idx (8-byte slots) from the C result struct.
func (e *Emitter) cpSpawnSyncField(raw string, idx int, ir string) string {
	gep := e.freshReg()
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, raw, idx*8))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", v, ir, gep))
	return v
}

// cpSpawnSyncResultType is spawnSync's result record — Node's
// `{ error, status, signal, output, pid, stdout, stderr }`: stdout/stderr
// are strings (the `encoding: 'utf8'` shape; no Buffer default here),
// `status` is `number | null` (null when a signal ended the child), `signal`
// is `string | null`, `output` is `[null, stdout, stderr]` as a string array
// (its first slot is the empty string standing in for null) or null when the
// child never started, and `error` is `Error | undefined` — set for a spawn
// failure, a timeout (`ETIMEDOUT`) or a `maxBuffer` overrun (`ENOBUFS`), and
// then Node's *first* key. stdioModes (cpSyncOpts.stdioModes, read
// statically from the call) picks the flavour of an absent stdout/stderr.
func cpSpawnSyncResultType(stdioModes int) Type {
	st := TypeF64
	st.Nullable = true
	return ObjectType([]Field{
		{Name: "error", Ty: undefinedableElem(errorObjType)}, // absent → key absent, as Node's
		{Name: "status", Ty: st},
		{Name: "signal", Ty: nullablePtr()},
		{Name: "output", Ty: cpSyncOutputType()},
		{Name: "pid", Ty: TypeF64},
		{Name: "stdout", Ty: cpSyncStdioFieldType(stdioModes, 1)},
		{Name: "stderr", Ty: cpSyncStdioFieldType(stdioModes, 2)},
	})
}

// cpSyncOutputType is `output`: `[null, stdout, stderr]`, or null (the
// absent-array header, ADR-01041) when the child never started.
func cpSyncOutputType() Type {
	t := ArrayOf(nullablePtr())
	t.Nullable = true
	return t
}

// cpSyncStdioFieldType is the type of a result's stdout (fd 1) / stderr
// (fd 2). A piped fd is absent only when the child never started, and Node
// then has the key with the value `undefined`; tsc types it plain `string`,
// so it is flagged UncheckedIndex (no strict narrowing demanded, key always
// listed). A non-piped fd (`inherit`/`ignore`) is null, as in Node. Either
// way a method call on the absent value throws Node's TypeError (ADR-01038).
func cpSyncStdioFieldType(stdioModes, fd int) Type {
	t := nullablePtr()
	if (stdioModes>>(fd*2))&3 == 0 {
		t.IsUndefined = true
		t.UncheckedIndex = true
	}
	return t
}

// cpSyncStdioModesOf reads a spawnSync call's `stdio` option without emitting
// anything — the result type's stdout/stderr flavour depends on it, and
// inferExprType must agree with the emitted object. A missing or malformed
// option is all-pipe (the emit path reports the error).
func (e *Emitter) cpSyncStdioModesOf(args []ast.Expression) int {
	for _, a := range args[min(1, len(args)):] {
		ol, ok := a.(*ast.ObjectLiteral)
		if !ok {
			continue
		}
		for _, prop := range ol.Properties {
			if prop.Key != "stdio" {
				continue
			}
			if m, err := e.cpParseStdio(prop.Value, ol.GetPos()); err == nil {
				return m
			}
		}
	}
	return 0
}

// cpSyncSignalName maps the sidecar's terminating-signal number to Node's
// `'SIGTERM'`-style name (null pointer for 0).
func (e *Emitter) cpSyncSignalName(sigReg string) string {
	e.ensureChildProcRuntime() // owns @__kml_cp_signal_name
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cp_signal_name(i64 %s)", r, sigReg))
	return r
}

// cpSyncError builds the `error` value of a result: null when the sidecar
// reported none, else an Error `spawnSync <file> <CODE>` with `code`,
// `errno`, `syscall: 'spawnSync <file>'` and `path: <file>` — Node's
// uv_spawn error shape (a spawn failure), or Node's ETIMEDOUT/ENOBUFS.
func (e *Emitter) cpSyncError(raw, fileRef string) string {
	e.ensureErrnoCode()
	e.ensureExceptionHelpers()
	errI := e.cpSpawnSyncField(raw, 5, "i64")
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, errI))
	noneL := e.freshLabel("cpsync.noerr")
	buildL := e.freshLabel("cpsync.builderr")
	buildEndL := e.freshLabel("cpsync.builderrend")
	joinL := e.freshLabel("cpsync.errjoin")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, noneL, buildL))
	e.emitLabel(buildL)
	e32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", e32, errI))
	codeRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_errno_code(i32 %s)", codeRaw, e32))
	codeNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", codeNull, codeRaw))
	codePtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", codePtr, codeNull, e.internString("UNKNOWN"), codeRaw))
	// "spawnSync <file>" — the syscall string, and the message's prefix.
	sysc, _ := e.emitStringConcat(Value{Ref: e.internString("spawnSync "), Ty: TypePtr}, Value{Ref: fileRef, Ty: TypePtr})
	withSp, _ := e.emitStringConcat(sysc, Value{Ref: e.internString(" "), Ty: TypePtr})
	msg, _ := e.emitStringConcat(withSp, Value{Ref: codePtr, Ty: TypePtr})
	errObj := e.buildErrorObj(0, msg.Ref, e.internString("Error"))
	sIR := errorObjType.StructIR()
	storeField := func(name, ir, val string) {
		idx, _, _ := errorObjType.FieldIndex(name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, sIR, errObj, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, g))
	}
	storeField("code", "ptr", codePtr)
	storeField("syscall", "ptr", sysc.Ref)
	storeField("path", "ptr", fileRef)
	uv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_uv_errno(i32 %s)", uv, e32))
	uvD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", uvD, uv))
	storeField("errno", "double", uvD)
	e.emitTerminator(fmt.Sprintf("br label %%%s", buildEndL))
	e.emitLabel(buildEndL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(noneL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ null, %%%s ]", r, errObj, buildEndL, noneL))
	return r
}

// cpSyncResultObject turns the sidecar's raw result into the
// cpSpawnSyncResultType object. A stdout/stderr that was not piped
// (`inherit`/`ignore`) is null, as in Node (the static type stays `string`,
// matching Node's own typings); `output` mirrors the two, its first slot
// the null standing for stdin.
func (e *Emitter) cpSyncResultObject(raw, fileRef string, stdioModes int) Value {
	ty := cpSpawnSyncResultType(stdioModes)
	e.ensureCalloc()
	e.ensureMalloc()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, ty.StructSize()))
	structIR := ty.StructIR()
	gep := func(name string) (string, Type) {
		idx, fty, _ := ty.FieldIndex(name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, obj, idx))
		return g, fty
	}
	pidI := e.cpSpawnSyncField(raw, 3, "i64")
	pidD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", pidD, pidI))
	g, _ := gep("pid")
	e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", pidD, g))
	stdout := e.cpSpawnSyncField(raw, 1, "ptr")
	stderr := e.cpSpawnSyncField(raw, 2, "ptr")
	g, _ = gep("stdout")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", stdout, g))
	g, _ = gep("stderr")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", stderr, g))
	// output: [ null, stdout, stderr ] — a fresh 3-slot string array; null
	// (the absent-array header, ADR-01041) when the child never started (pid 0).
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", data))
	for i, p := range []string{"null", stdout, stderr} {
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", s, data, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, s))
	}
	started := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", started, pidI))
	a0 := e.freshReg()
	a1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", a0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 3, 1", a1, a0))
	g, fty := gep("output")
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", g))
	startL := e.freshLabel("cpsync.output")
	skipL := e.freshLabel("cpsync.nooutput")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", started, startL, skipL))
	e.emitLabel(startL)
	e.storeScalarOrNullableField(g, fty, Value{Ref: a1, Ty: fty})
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
	// status: number | null (null when signalled / never started).
	statI := e.cpSpawnSyncField(raw, 0, "i64")
	statNeg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", statNeg, statI))
	statPresent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", statPresent, statNeg))
	statD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", statD, statI))
	g, fty = gep("status")
	aggIR := nullableScalarStorageIR(fty)
	agg0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue %s undef, i1 %s, 0", agg0, aggIR, statPresent))
	agg1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue %s %s, double %s, 1", agg1, aggIR, agg0, statD))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", aggIR, agg1, g))
	// signal: the name or null.
	sigI := e.cpSpawnSyncField(raw, 4, "i64")
	g, _ = gep("signal")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.cpSyncSignalName(sigI), g))
	g, _ = gep("error")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.cpSyncError(raw, fileRef), g))
	return Value{Ref: obj, Ty: ty}
}

// cpSyncCommandAndArgs splits the trailing `(args?, options?)` of a *Sync
// call: an args array (a string[] expression) and/or a trailing options
// object literal.
func (e *Emitter) cpSyncCommandAndArgs(rest []ast.Expression, name string, pos ast.Pos) (argsPtr, argsLen string, argsExpr ast.Expression, o cpSyncOpts, err error) {
	argsPtr, argsLen = "null", "0"
	o = cpSyncOpts{cwdRef: "null", envRef: "null", inputRef: "null", inputLenRef: "0", timeoutRef: "0", killSig: 15, maxBufRef: strconv.Itoa(cpMaxBufferDefault), argv0Ref: "null"}
	if len(rest) == 0 {
		return
	}
	if _, isObj := rest[0].(*ast.ObjectLiteral); isObj && len(rest) == 1 {
		o, err = e.cpSyncOptions(rest[0], name, pos)
		return
	}
	argsPtr, argsLen, err = e.cpResolveArgv(rest[0], pos, name)
	if err != nil {
		return
	}
	argsExpr = rest[0]
	if len(rest) == 2 {
		o, err = e.cpSyncOptions(rest[1], name, pos)
	}
	return
}

// cpShellCommandLine joins `command` and its args with single spaces — what
// Node does for `shell: true` before handing the line to the shell.
func (e *Emitter) cpShellCommandLine(cmd Value, argsExpr ast.Expression, pos ast.Pos) (Value, error) {
	if argsExpr == nil {
		return cmd, nil
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(argsExpr, pos)
	if err != nil {
		return Value{}, err
	}
	joined, err := e.emitArrayJoinCore(ptrReg, lenReg, elemTy, Value{Ref: e.internString(" "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	// An empty args array contributes nothing (no trailing space).
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, lenReg))
	withSp, err := e.emitStringConcat(cmd, Value{Ref: e.internString(" "), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	full, err := e.emitStringConcat(withSp, joined)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, isEmpty, cmd.Ref, full.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// cpShellArgvFor builds (file, argv, argc) for a shell invocation: the
// platform default (`/bin/sh -c` / `%ComSpec% /d /s /c`) or a `shell: <path>`
// — a cmd.exe-shaped path takes `/d /s /c`, anything else `-c`, as in Node.
func (e *Emitter) cpShellArgvFor(shellFile string, cmdRef string) (fileRef, argvPtr, argsLen string) {
	if shellFile == "" {
		return e.emitShellArgv(cmdRef)
	}
	e.ensureMalloc()
	argvPtr = e.freshReg()
	if cpCmdShellRE.MatchString(shellFile) {
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", argvPtr))
		for i, a := range []string{"/d", "/s", "/c"} {
			s := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", s, argvPtr, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(a), s))
		}
		s3 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 3", s3, argvPtr))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cmdRef, s3))
		return e.internString(shellFile), argvPtr, "4"
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", argvPtr))
	s0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", s0, argvPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("-c"), s0))
	s1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", s1, argvPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cmdRef, s1))
	return e.internString(shellFile), argvPtr, "2"
}

// emitCPSpawnSync implements spawnSync(command[, args][, options]).
func (e *Emitter) emitCPSpawnSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: child_process.spawnSync takes (command, args?, options?)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)
	argsPtr, argsLen, argsExpr, o, err := e.cpSyncCommandAndArgs(args[1:], "spawnSync", pos)
	if err != nil {
		return Value{}, err
	}
	if o.shell {
		line, err := e.cpShellCommandLine(fileVal, argsExpr, pos)
		if err != nil {
			return Value{}, err
		}
		shFile, shArgv, shArgc := e.cpShellArgvFor(o.shellFile, line.Ref)
		raw := e.cpSpawnSyncCall(shFile, shArgv, shArgc, o)
		return e.cpSyncResultObject(raw, fileVal.Ref, o.stdioModes), nil
	}
	raw := e.cpSpawnSyncCall(fileVal.Ref, argsPtr, argsLen, o)
	return e.cpSyncResultObject(raw, fileVal.Ref, o.stdioModes), nil
}

// cpExecSyncResult is execSync/execFileSync's tail (Node's checkExecSyncError):
// with no explicit `stdio` the child's captured stderr is mirrored to ours;
// a spawn/timeout/maxBuffer error is thrown as is, a nonzero status or a
// signal throws `Command failed: <cmd>` (plus the stderr text) — either
// Error carrying `status`, `signal`, `output`, `pid`, `stdout` and `stderr`
// as own properties (the `extra` bag, ADR-01080). Success yields stdout.
func (e *Emitter) cpExecSyncResult(raw string, cmdVal Value, o cpSyncOpts) (Value, error) {
	res := e.cpSyncResultObject(raw, cmdVal.Ref, o.stdioModes)
	ty := res.Ty
	structIR := ty.StructIR()
	load := func(name string) Value {
		idx, fty, _ := ty.FieldIndex(name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, res.Ref, idx))
		return e.loadScalarOrNullableField(g, fty) // array header slot / { i1, T } nullable
	}
	stdout := load("stdout")
	stderr := load("stderr")
	// Both are null when the child never started or the stream was not
	// piped; the text paths below read them as "".
	empty := e.internString("")
	orEmpty := func(v Value) Value {
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, isNull, empty, v.Ref))
		return Value{Ref: r, Ty: TypePtr}
	}
	stderrText := orEmpty(stderr)
	if !o.stdioSet {
		// Mirror the child's stderr to ours (Node: `process.stderr.write(ret.stderr)`).
		e.ensureWriteDecl()
		e.ensureStrHeaderRuntime()
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, stderrText.Ref))
		e.emitInstr(fmt.Sprintf("call i64 @write(i32 2, ptr %s, i64 %s)", stderrText.Ref, l))
	}
	eIdx, _, _ := ty.FieldIndex("error")
	eg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", eg, structIR, res.Ref, eIdx))
	errObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", errObj, eg))
	hasErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasErr, errObj))
	statI := e.cpSpawnSyncField(raw, 0, "i64")
	nonzero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nonzero, statI))
	failed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", failed, hasErr, nonzero))
	failL := e.freshLabel("execsync.fail")
	okL := e.freshLabel("execsync.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", failed, failL, okL))
	e.emitLabel(failL)
	// The thrown Error: the spawn error when there is one, else "Command
	// failed: <cmd>" + "\n" + stderr (when stderr is non-empty).
	msg, err := e.emitStringConcat(Value{Ref: e.internString("Command failed: "), Ty: TypePtr}, cmdVal)
	if err != nil {
		return Value{}, err
	}
	seLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", seLen, stderrText.Ref))
	seEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", seEmpty, seLen))
	withNL, err := e.emitStringConcat(msg, Value{Ref: e.internString("\n"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	withSe, err := e.emitStringConcat(withNL, stderrText)
	if err != nil {
		return Value{}, err
	}
	fullMsg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", fullMsg, seEmpty, msg.Ref, withSe.Ref))
	plainErr := e.buildErrorObj(0, fullMsg, e.internString("Error"))
	thrown := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", thrown, hasErr, errObj, plainErr))
	// Attach the result fields as own properties.
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	setBox := func(key string, v Value) error {
		boxed, err := e.emitBoxValue(v)
		if err != nil {
			return err
		}
		if isNullableScalar(v.Ty) {
			// An absent `status` is Node's null (the box default is undefined).
			present, _ := e.nullableScalarAggParts(v)
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", sel, present, boxed.Ref, nbNull))
			boxed = Value{Ref: sel, Ty: TypeAny}
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", bag, e.internString(key), boxed.Ref))
		return nil
	}
	for _, name := range []string{"status", "signal", "output", "pid", "stdout", "stderr"} {
		v := load(name)
		if name == "stdout" || name == "stderr" {
			v.Ty = nullablePtr() // a null stream boxes as null, not a null-pointer string
		}
		if err := setBox(name, v); err != nil {
			return Value{}, err
		}
	}
	xIdx, _, _ := errorObjType.FieldIndex("extra")
	xg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", xg, errorObjType.StructIR(), thrown, xIdx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", bag, xg))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", thrown))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	return Value{Ref: stdout.Ref, Ty: TypePtr}, nil // inferExprType's execSync type
}

// emitCPExecSync implements execSync(command[, options]): the command runs
// through the shell (`/bin/sh -c` / `cmd.exe /d /s /c`, or `shell: <path>`)
// and its stdout is returned; a failure throws (cpExecSyncResult).
func (e *Emitter) emitCPExecSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: child_process.execSync takes (command, options?)", pos.Line, pos.Col)
	}
	cmdVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cmdVal = e.coerce(cmdVal, TypePtr)
	o := cpSyncOpts{cwdRef: "null", envRef: "null", inputRef: "null", inputLenRef: "0", timeoutRef: "0", killSig: 15, maxBufRef: strconv.Itoa(cpMaxBufferDefault), argv0Ref: "null"}
	if len(args) == 2 {
		o, err = e.cpSyncOptions(args[1], "execSync", pos)
		if err != nil {
			return Value{}, err
		}
	}
	o.shell = true
	shFile, argvPtr, shArgc := e.cpShellArgvFor(o.shellFile, cmdVal.Ref)
	raw := e.cpSpawnSyncCall(shFile, argvPtr, shArgc, o)
	return e.cpExecSyncResult(raw, cmdVal, o)
}

// emitCPExecFileSync implements execFileSync(file[, args][, options]): the
// file is exec'd directly (no shell unless `shell` says so), stdout returned;
// a failure throws (cpExecSyncResult).
func (e *Emitter) emitCPExecFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: child_process.execFileSync takes (file, args?, options?)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)
	argsPtr, argsLen, argsExpr, o, err := e.cpSyncCommandAndArgs(args[1:], "execFileSync", pos)
	if err != nil {
		return Value{}, err
	}
	if o.shell {
		line, err := e.cpShellCommandLine(fileVal, argsExpr, pos)
		if err != nil {
			return Value{}, err
		}
		shFile, shArgv, shArgc := e.cpShellArgvFor(o.shellFile, line.Ref)
		raw := e.cpSpawnSyncCall(shFile, shArgv, shArgc, o)
		return e.cpExecSyncResult(raw, fileVal, o)
	}
	raw := e.cpSpawnSyncCall(fileVal.Ref, argsPtr, argsLen, o)
	return e.cpExecSyncResult(raw, fileVal, o)
}
