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
	"strconv"
	"strings"

	"KlainMainLang/ast"
)

// emitChildProcessModuleCall dispatches child_process.spawn/exec/execFile.
func (e *Emitter) emitChildProcessModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureChildProcRuntime()
	switch method {
	case "fork":
		return e.emitCPFork(args, pos)
	case "spawn":
		return e.emitCPSpawn(args, pos)
	case "exec":
		return e.emitCPExec(args, pos)
	case "execFile":
		return e.emitCPExecFile(args, pos)
	case "spawnSync":
		return e.emitCPSpawnSync(args, pos)
	case "execSync":
		return e.emitCPExecSync(args, pos)
	case "execFileSync":
		return e.emitCPExecFileSync(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: child_process.%s is not supported", pos.Line, pos.Col, method)
}

// cpSpawnSyncResultType is spawnSync's result record — Node's
// `{ status, stdout, stderr, pid }` with stdout/stderr as strings (the
// `encoding: 'utf8'` shape; there is no Buffer default here). Field order
// must match cpSpawnSyncResultObject's stores.
func cpSpawnSyncResultType() Type {
	return ObjectType([]Field{
		{Name: "status", Ty: TypeF64},
		{Name: "stdout", Ty: TypePtr},
		{Name: "stderr", Ty: TypePtr},
		{Name: "pid", Ty: TypeF64},
	})
}

// cpSpawnSyncCall evaluates (file, argv) and emits the blocking
// @__kml_cp_spawn_sync call, returning the raw C result-struct pointer
// (layout: i64 status @0, ptr stdout @8, ptr stderr @16, i64 pid @24).
// flags bit 0 marks a shell invocation (execSync): on Windows the command
// line is passed verbatim, Node's windowsVerbatimArguments (ADR-00740).
func (e *Emitter) cpSpawnSyncCall(fileRef, argsPtr, argsLen, cwdRef string, flags int) string {
	e.ensureSpawnSyncRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cp_spawn_sync(ptr %s, ptr %s, i64 %s, ptr %s, i64 %d)", r, fileRef, argsPtr, argsLen, cwdRef, flags))
	return r
}

// cpSyncOptions evaluates the optional trailing options object of the *Sync
// forms. Supported: `cwd` (child chdir before exec) and `encoding` (must be
// the literal 'utf8' — results are already utf8 strings). Anything else is a
// clean rejection rather than a silent ignore.
func (e *Emitter) cpSyncOptions(arg ast.Expression, name string, pos ast.Pos) (cwdRef string, err error) {
	cwdRef = "null"
	lit, ok := arg.(*ast.ObjectLiteral)
	if !ok {
		return "", fmt.Errorf("%d:%d: child_process.%s's options must be an object literal", pos.Line, pos.Col, name)
	}
	for _, prop := range lit.Properties {
		switch prop.Key {
		case "cwd":
			v, verr := e.emitExpr(prop.Value)
			if verr != nil {
				return "", verr
			}
			cwdRef = e.coerce(v, TypePtr).Ref
		case "encoding":
			s, ok := prop.Value.(*ast.StringLiteral)
			if !ok || (s.Value != "utf8" && s.Value != "utf-8") {
				return "", fmt.Errorf("%d:%d: child_process.%s supports encoding: 'utf8' only (results are strings)", pos.Line, pos.Col, name)
			}
		default:
			return "", fmt.Errorf("%d:%d: child_process.%s options support { cwd, encoding } only (got '%s')", pos.Line, pos.Col, name, prop.Key)
		}
	}
	return cwdRef, nil
}

// cpSpawnSyncField loads field idx (8-byte slots) from the C result struct.
func (e *Emitter) cpSpawnSyncField(raw string, idx int, ir string) string {
	gep := e.freshReg()
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, raw, idx*8))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", v, ir, gep))
	return v
}

// emitCPSpawnSync implements spawnSync(command, args?) — blocks until the
// child exits and returns { status, stdout, stderr, pid }.
func (e *Emitter) emitCPSpawnSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: child_process.spawnSync takes (command, args?, { cwd, encoding }?)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)
	argsPtr, argsLen, cwdRef := "null", "0", "null"
	rest := args[1:]
	if len(rest) >= 1 {
		if _, isObj := rest[0].(*ast.ObjectLiteral); isObj && len(rest) == 1 {
			// spawnSync(cmd, { cwd }) — options with no args array.
			c, err := e.cpSyncOptions(rest[0], "spawnSync", pos)
			if err != nil {
				return Value{}, err
			}
			cwdRef = c
		} else {
			p, l, err := e.cpResolveArgv(rest[0], pos, "spawnSync")
			if err != nil {
				return Value{}, err
			}
			argsPtr, argsLen = p, l
			if len(rest) == 2 {
				c, err := e.cpSyncOptions(rest[1], "spawnSync", pos)
				if err != nil {
					return Value{}, err
				}
				cwdRef = c
			}
		}
	}
	raw := e.cpSpawnSyncCall(fileVal.Ref, argsPtr, argsLen, cwdRef, 0)

	ty := cpSpawnSyncResultType()
	e.ensureCalloc()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, ty.StructSize()))
	structIR := ty.StructIR()
	store := func(idx int, ir, val string) {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, structIR, obj, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, g))
	}
	statI := e.cpSpawnSyncField(raw, 0, "i64")
	statD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", statD, statI))
	store(0, "double", statD)
	store(1, "ptr", e.cpSpawnSyncField(raw, 1, "ptr"))
	store(2, "ptr", e.cpSpawnSyncField(raw, 2, "ptr"))
	pidI := e.cpSpawnSyncField(raw, 3, "i64")
	pidD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", pidD, pidI))
	store(3, "double", pidD)
	return Value{Ref: obj, Ty: ty}, nil
}

// emitCPExecSync implements execSync(command): runs via `/bin/sh -c` and
// returns the captured stdout string. Like Node, a nonzero exit status
// throws (ADR-00753) — the returned stdout is only reached on success.
func (e *Emitter) emitCPExecSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: child_process.execSync takes (command, { cwd, encoding }?)", pos.Line, pos.Col)
	}
	cwdRef := "null"
	if len(args) == 2 {
		c, err := e.cpSyncOptions(args[1], "execSync", pos)
		if err != nil {
			return Value{}, err
		}
		cwdRef = c
	}
	cmdVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cmdVal = e.coerce(cmdVal, TypePtr)
	e.ensureMalloc()
	shFile, argvPtr, shArgc := e.emitShellArgv(cmdVal.Ref)
	raw := e.cpSpawnSyncCall(shFile, argvPtr, shArgc, cwdRef, 1)
	return e.cpExecSyncResult(raw, cmdVal)
}

// cpExecSyncResult throws `Command failed: <command>` when the child exited
// nonzero (Node's execSync/execFileSync behaviour), otherwise yields the
// captured stdout string. The thrown value is a plain Error; the richer
// `.status`/`.stdout`/`.stderr` ExecException properties are not attached yet
// — spawnSync exposes the status without throwing (documented caveat).
func (e *Emitter) cpExecSyncResult(raw string, cmdVal Value) (Value, error) {
	status := e.cpSpawnSyncField(raw, 0, "i64")
	stdout := e.cpSpawnSyncField(raw, 1, "ptr")
	nonzero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nonzero, status))
	failL := e.freshLabel("execsync.fail")
	okL := e.freshLabel("execsync.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nonzero, failL, okL))
	e.emitLabel(failL)
	msg, err := e.emitStringConcat(Value{Ref: e.internString("Command failed: "), Ty: TypePtr}, cmdVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInternalThrow(msg.Ref)
	e.emitLabel(okL)
	return Value{Ref: stdout, Ty: TypePtr}, nil
}

// emitCPExecFileSync implements execFileSync(file, args?, options?): execvp
// with no shell, returning the captured stdout string. Like execSync, a
// nonzero exit status throws (ADR-00753).
func (e *Emitter) emitCPExecFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: child_process.execFileSync takes (file, args?, { cwd, encoding }?)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)
	argsPtr, argsLen, cwdRef := "null", "0", "null"
	rest := args[1:]
	if len(rest) >= 1 {
		if _, isObj := rest[0].(*ast.ObjectLiteral); isObj && len(rest) == 1 {
			c, err := e.cpSyncOptions(rest[0], "execFileSync", pos)
			if err != nil {
				return Value{}, err
			}
			cwdRef = c
		} else {
			p, l, err := e.cpResolveArgv(rest[0], pos, "execFileSync")
			if err != nil {
				return Value{}, err
			}
			argsPtr, argsLen = p, l
			if len(rest) == 2 {
				c, err := e.cpSyncOptions(rest[1], "execFileSync", pos)
				if err != nil {
					return Value{}, err
				}
				cwdRef = c
			}
		}
	}
	raw := e.cpSpawnSyncCall(fileVal.Ref, argsPtr, argsLen, cwdRef, 0)
	return e.cpExecSyncResult(raw, fileVal)
}

// cpSpawnCall emits the @__kml_cp_spawn call and returns the handle register.
// cwdRef is a string ptr (the child chdir()s to it before exec) or "null";
// envRef is a NULL-terminated `char**` of "KEY=value" strings that fully
// replaces the child's environment, or "null" to inherit ours (ADR-00762).
func (e *Emitter) cpSpawnCall(fileRef, argsPtr, argsLen string, mode int, cwdRef, envRef, timeoutRef string, killSig int) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cp_spawn(ptr %s, ptr %s, i64 %s, i64 %d, ptr %s, ptr %s, i64 %s, i64 %d)", r, fileRef, argsPtr, argsLen, mode, cwdRef, envRef, timeoutRef, killSig))
	return r
}

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
	detached    bool // setsid (POSIX) / DETACHED_PROCESS (Windows) — ADR-00765
	stdioModes  int  // per-fd stdio: 2 bits each (stdin/stdout/stderr), 0=pipe 1=inherit 2=ignore — ADR-00766
}

// cpStdioModeVal maps a Node stdio string to this compiler's 2-bit mode.
func cpStdioModeVal(s string) (int, bool) {
	switch s {
	case "pipe":
		return 0, true
	case "inherit":
		return 1, true
	case "ignore":
		return 2, true
	}
	return 0, false
}

// cpParseStdio lowers a `stdio` option into per-fd modes (2 bits each: stdin at
// 0-1, stdout 2-3, stderr 4-5). Accepts a string ('pipe'/'inherit'/'ignore',
// applied to all three) or a 3-element array of those strings (ADR-00766). fd
// numbers, 'ipc', streams, and non-3 arrays are clean rejections.
func (e *Emitter) cpParseStdio(v ast.Expression, pos ast.Pos) (int, error) {
	if sl, ok := v.(*ast.StringLiteral); ok {
		m, ok := cpStdioModeVal(sl.Value)
		if !ok {
			return 0, fmt.Errorf("%d:%d: child_process.spawn stdio string must be 'pipe', 'inherit' or 'ignore'", pos.Line, pos.Col)
		}
		return m | m<<2 | m<<4, nil
	}
	if al, ok := v.(*ast.ArrayLiteral); ok {
		if len(al.Elements) != 3 {
			return 0, fmt.Errorf("%d:%d: child_process.spawn stdio array must have exactly 3 entries [stdin, stdout, stderr]", pos.Line, pos.Col)
		}
		modes := 0
		for i, el := range al.Elements {
			sl, ok := el.(*ast.StringLiteral)
			if !ok {
				return 0, fmt.Errorf("%d:%d: child_process.spawn stdio array entries must be 'pipe'/'inherit'/'ignore' strings (fd numbers, 'ipc' and streams are not supported)", pos.Line, pos.Col)
			}
			m, ok := cpStdioModeVal(sl.Value)
			if !ok {
				return 0, fmt.Errorf("%d:%d: child_process.spawn stdio entry must be 'pipe', 'inherit' or 'ignore' (got '%s')", pos.Line, pos.Col, sl.Value)
			}
			modes |= m << (i * 2)
		}
		return modes, nil
	}
	return 0, fmt.Errorf("%d:%d: child_process.spawn stdio must be a string or a 3-element array", pos.Line, pos.Col)
}

func (e *Emitter) cpSpawnOptions(arg ast.Expression, pos ast.Pos) (cpSpawnOpts, error) {
	opts := cpSpawnOpts{cwdRef: "null", envRef: "null", timeoutRef: "0", killSig: 15}
	if lit, ok := arg.(*ast.ObjectLiteral); ok {
		for _, prop := range lit.Properties {
			switch prop.Key {
			case "timeout":
				// Kill the child after N ms (the event loop enforces it). Any
				// numeric expression → i64 ms (ADR-00764).
				v, err := e.emitExpr(prop.Value)
				if err != nil {
					return cpSpawnOpts{}, err
				}
				opts.timeoutRef = e.coerce(v, TypeI64).Ref
			case "killSignal":
				// The signal the timeout uses (Node default SIGTERM). A string
				// literal name → the host's number; a numeric literal passes.
				if sl, ok := prop.Value.(*ast.StringLiteral); ok {
					n, ok := cpSignalNumber(sl.Value)
					if !ok {
						return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn killSignal: unknown signal %q", pos.Line, pos.Col, sl.Value)
					}
					opts.killSig = n
				} else if nl, ok := prop.Value.(*ast.NumberLiteral); ok {
					n, err := strconv.Atoi(nl.Value)
					if err != nil {
						return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn killSignal must be a signal name or an integer", pos.Line, pos.Col)
					}
					opts.killSig = n
				} else {
					return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn killSignal must be a literal signal name or number", pos.Line, pos.Col)
				}
			case "cwd":
				v, err := e.emitExpr(prop.Value)
				if err != nil {
					return cpSpawnOpts{}, err
				}
				opts.cwdRef = e.coerce(v, TypePtr).Ref
			case "shell":
				// Literal true routes the command through the platform shell
				// (`/bin/sh -c` / `cmd.exe /d /s /c`, ADR-00740); false is the
				// default. A shell *path* or dynamic value is not supported.
				b, ok := prop.Value.(*ast.BooleanLiteral)
				if !ok {
					return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn's shell option must be the literal true or false (a custom shell path is not supported)", pos.Line, pos.Col)
				}
				opts.shell = b.Value
			case "stdio":
				m, err := e.cpParseStdio(prop.Value, pos)
				if err != nil {
					return cpSpawnOpts{}, err
				}
				opts.stdioModes = m
			case "env":
				// A custom environment fully REPLACES the child's — Node's
				// semantics: `env` does not merge with process.env (ADR-00762).
				el, ok := prop.Value.(*ast.ObjectLiteral)
				if !ok {
					return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn's env option must be an object literal of string values", pos.Line, pos.Col)
				}
				ref, err := e.cpBuildEnvp(el, pos)
				if err != nil {
					return cpSpawnOpts{}, err
				}
				opts.envRef = ref
			case "windowsHide":
				// Hide the child's console window on Windows (CREATE_NO_WINDOW);
				// a no-op on POSIX, as in Node (ADR-00763). Literal bool only.
				b, ok := prop.Value.(*ast.BooleanLiteral)
				if !ok {
					return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn's windowsHide option must be the literal true or false", pos.Line, pos.Col)
				}
				opts.windowsHide = b.Value
			case "detached":
				// Make the child a new session/group leader so it can outlive
				// the parent: setsid() on POSIX, DETACHED_PROCESS +
				// CREATE_NEW_PROCESS_GROUP on Windows (ADR-00765). Literal bool.
				b, ok := prop.Value.(*ast.BooleanLiteral)
				if !ok {
					return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn's detached option must be the literal true or false", pos.Line, pos.Col)
				}
				opts.detached = b.Value
			default:
				return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn options support { cwd, shell, stdio: 'pipe', env, windowsHide, timeout, killSignal, detached } only (got '%s')", pos.Line, pos.Col, prop.Key)
			}
		}
		return opts, nil
	}
	objTy := e.inferExprType(arg)
	if !objTy.IsObject {
		return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn's options must be an object", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(arg)
	if err != nil {
		return cpSpawnOpts{}, err
	}
	for _, f := range objVal.Ty.Fields {
		switch f.Name {
		case "cwd", "shell":
		default:
			return cpSpawnOpts{}, fmt.Errorf("%d:%d: child_process.spawn options support { cwd, shell } only on a dynamic options object (got '%s'); env/windowsHide need an object literal", pos.Line, pos.Col, f.Name)
		}
	}
	if idx, fty, ok := objVal.Ty.FieldIndex("cwd"); ok && isStringTy(fty) {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, gep))
		opts.cwdRef = r
	}
	// A dynamic options object supports cwd only; its shell/env/windowsHide
	// fields (if any) are not readable at compile time — spawn keeps the
	// no-shell/inherit-env/no-hide behavior.
	return opts, nil
}

// cpBuildEnvp lowers an env object literal `{ KEY: "val", ... }` into a
// NULL-terminated `char**` of "KEY=val" C strings for __kml_cp_spawn (ADR-00762).
// Values must be strings (Node stringifies, but only string values are wired
// here); the block fully replaces the child's environment. Never freed
// (-mm=manual) — it must outlive the fork/CreateProcess anyway.
func (e *Emitter) cpBuildEnvp(lit *ast.ObjectLiteral, pos ast.Pos) (string, error) {
	e.ensureMalloc()
	n := len(lit.Properties)
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", arr, (n+1)*8))
	for i, prop := range lit.Properties {
		left, err := e.emitExpr(ast.NewStringLiteral(prop.Key+"=", pos))
		if err != nil {
			return "", err
		}
		right, err := e.emitExpr(prop.Value)
		if err != nil {
			return "", err
		}
		if !isStringTy(right.Ty) {
			return "", fmt.Errorf("%d:%d: child_process.spawn env value for '%s' must be a string", pos.Line, pos.Col, prop.Key)
		}
		concat, err := e.emitStringConcat(left, right)
		if err != nil {
			return "", err
		}
		p := e.coerce(concat, TypePtr).Ref
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slot, arr, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, slot))
	}
	nslot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", nslot, arr, n))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", nslot))
	return arr, nil
}

// emitCPSpawn implements spawn(command, args?): streaming ChildProcess.
func (e *Emitter) emitCPSpawn(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: child_process.spawn takes (command, args?, options?)", pos.Line, pos.Col)
	}
	fileVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fileVal = e.coerce(fileVal, TypePtr)
	argsPtr, argsLen := "null", "0"
	rest := args[1:]
	if len(rest) >= 1 {
		if _, isObj := rest[0].(*ast.ObjectLiteral); !isObj && !e.inferExprType(rest[0]).IsObject {
			p, l, err := e.cpResolveArgv(rest[0], pos, "spawn")
			if err != nil {
				return Value{}, err
			}
			argsPtr, argsLen = p, l
			rest = rest[1:]
		} else if len(rest) == 2 {
			p, l, err := e.cpResolveArgv(rest[0], pos, "spawn")
			if err != nil {
				return Value{}, err
			}
			argsPtr, argsLen = p, l
			rest = rest[1:]
		}
	}
	opts := cpSpawnOpts{cwdRef: "null", envRef: "null", timeoutRef: "0", killSig: 15}
	if len(rest) >= 1 {
		o, err := e.cpSpawnOptions(rest[0], pos)
		if err != nil {
			return Value{}, err
		}
		opts = o
	}
	// mode bitmask: bit 0 buffered (0 here, streaming), bit 1 shell/verbatim,
	// bit 2 windowsHide (ADR-00763), bit 3 detached (ADR-00765). Bits 2/3 ride
	// to __kml_win_spawn's flags via the >>1 shift in cpSpawnForkIR; the POSIX
	// fork IR reads bit 3 directly for setsid().
	optBits := 0
	if opts.windowsHide {
		optBits |= 4
	}
	if opts.detached {
		optBits |= 8
	}
	// Per-fd stdio modes ride in mode bits 4-9 (ADR-00766). They land in
	// __kml_win_spawn's flags (mode>>1) at bits 3-8, which the shim ignores.
	optBits |= opts.stdioModes << 4
	if opts.shell {
		// shell: true — the whole command line goes through the platform
		// shell as ONE string, exec-style (`/bin/sh -c` / `cmd.exe /d /s /c`
		// verbatim on Windows, ADR-00740). Node joins an args array into the
		// command with spaces; that join is not implemented here yet, so an
		// args array is a clean rejection instead of a silently wrong quote.
		if argsPtr != "null" {
			return Value{}, fmt.Errorf("%d:%d: child_process.spawn with shell: true takes the whole command as one string — put the arguments in the command, or drop shell", pos.Line, pos.Col)
		}
		shFile, shArgv, shArgc := e.emitShellArgv(fileVal.Ref)
		// mode 2: streaming (bit 0 clear) + shell/verbatim (bit 1).
		cp := e.cpSpawnCall(shFile, shArgv, shArgc, 2|optBits, opts.cwdRef, opts.envRef, opts.timeoutRef, opts.killSig)
		return Value{Ref: cp, Ty: ChildProcessType()}, nil
	}
	cp := e.cpSpawnCall(fileVal.Ref, argsPtr, argsLen, 0|optBits, opts.cwdRef, opts.envRef, opts.timeoutRef, opts.killSig)
	return Value{Ref: cp, Ty: ChildProcessType()}, nil
}

// cpForkIsSelfPath reports whether a fork() path argument is the self-fork
// shape — `__filename` or `process.argv[1]` — the only fork this compiler
// supports (TDD-00141): the child is a re-exec of the current binary.
func cpForkIsSelfPath(arg ast.Expression) bool {
	if id, ok := arg.(*ast.Identifier); ok && id.Name == "__filename" {
		return true
	}
	if idx, ok := arg.(*ast.IndexExpression); ok {
		if lit, isNum := idx.Index.(*ast.NumberLiteral); isNum && lit.Value == "1" {
			if mem, isMem := idx.Object.(*ast.MemberExpression); isMem && mem.Property == "argv" {
				if id, isID := mem.Object.(*ast.Identifier); isID && id.Name == "process" {
					return true
				}
			}
		}
	}
	return false
}

// emitCPFork implements the self-fork form of child_process.fork
// (TDD-00141): `fork(__filename)` / `fork(process.argv[1])`, with optional
// extra string[] args. The child is a fresh re-exec of this same binary with
// an inherited-stdio socketpair IPC channel (NODE_CHANNEL_FD), exactly
// Node's own mechanism — so `if (process.send)` child-detection works.
func (e *Emitter) emitCPFork(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: child_process.fork takes (modulePath, args?)", pos.Line, pos.Col)
	}
	if !cpForkIsSelfPath(args[0]) {
		return Value{}, fmt.Errorf("%d:%d: child_process.fork supports self-fork only — the path must be __filename or process.argv[1] (forking a different module would need a second compiled program)", pos.Line, pos.Col)
	}
	argsPtr, argsLen := "null", "0"
	rest := args[1:]
	if len(rest) >= 1 {
		if _, isObj := rest[0].(*ast.ObjectLiteral); !isObj {
			p, l, err := e.cpResolveArgv(rest[0], pos, "fork")
			if err != nil {
				return Value{}, err
			}
			argsPtr, argsLen = p, l
			rest = rest[1:]
		}
	}
	if len(rest) >= 1 {
		lit, ok := rest[0].(*ast.ObjectLiteral)
		if !ok || len(lit.Properties) > 0 {
			return Value{}, fmt.Errorf("%d:%d: child_process.fork's options object is not supported (only an empty {} literal)", pos.Line, pos.Col)
		}
	}
	e.ensureCPForkRuntime()
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_cp_fork(ptr %s, i64 %s)", cp, argsPtr, argsLen))
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
	// argv = ["-c", command] via /bin/sh, or cmd.exe /d /s /c on Windows
	shFile, argvPtr, shArgc := e.emitShellArgv(cmdVal.Ref)
	// mode 3: buffered (bit 0) + shell/verbatim command line (bit 1) — on
	// Windows the command reaches cmd.exe verbatim, not re-quoted (ADR-00740).
	cp := e.cpSpawnCall(shFile, argvPtr, shArgc, 3, "null", "null", "0", 15)
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
	cp := e.cpSpawnCall(fileVal.Ref, argsPtr, argsLen, 1, "null", "null", "0", 15)
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
			hdr, err := e.cpExitCloseAdapter(args[1], pos)
			if err != nil {
				return Value{}, err
			}
			idx := 10
			if evt == "exit" {
				idx = 11
			}
			e.cpStoreField(objVal.Ref, idx, hdr)
		case "error":
			cb, err := e.cpArrowClosure(args[1], []Type{errorObjType}, pos)
			if err != nil {
				return Value{}, err
			}
			e.cpStoreField(objVal.Ref, 12, cb)
		case "message":
			// The fork IPC channel (TDD-00141) — string payloads. See through
			// a `test` counting wrapper the way ADR-00412/00422 sites do.
			contextTypeArrowParams(args[1], "string")
			cb, err := e.cpArrowClosure(args[1], []Type{TypePtr}, pos)
			if err != nil {
				return Value{}, err
			}
			e.cpStoreField(objVal.Ref, 18, cb)
		default:
			return Value{}, fmt.Errorf("%d:%d: child.on supports 'close', 'exit', 'error' and 'message' (got '%s')", pos.Line, pos.Col, evt)
		}
		return Value{Ty: TypeVoid}, nil
	case "send":
		// fork IPC: send one string message to the child (TDD-00141).
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: child.send takes one message", pos.Line, pos.Col)
		}
		mv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if !isStringTy(mv.Ty) {
			return Value{}, fmt.Errorf("%d:%d: child.send supports string messages in this version", pos.Line, pos.Col)
		}
		e.ensureCPForkRuntime()
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_cp_send(ptr %s, ptr %s)", ok, objVal.Ref, mv.Ref))
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
				n, ok := cpSignalNumber(sl.Value)
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
	// A Uint8Array 'data' chunk crosses from the runtime as a raw (ptr, len)
	// pair; its object-reference array parameter expects a header (TDD-00127),
	// so wrap the listener in the header-boxing adapter. Non-array listeners
	// ('end'/'close'/…) pass through unchanged.
	if len(cb.ty.FuncParams) > 0 && cb.ty.FuncParams[0].IsArray {
		return e.chunkHeaderAdapterClosure(cb.hdrPtr), nil
	}
	return cb.hdrPtr, nil
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
