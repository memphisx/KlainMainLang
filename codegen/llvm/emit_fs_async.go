// emit_fs_async.go — asynchronous fs: the callback form (fs.readFile(path, cb))
// and the Promise form (fs.promises.readFile(path) / import from 'fs/promises').
// TDD-00107.
//
// On POSIX both forms are genuinely non-blocking (TDD-00185): the op runs on
// the blocking-work thread pool (threadpoolsrc/klainpool.c) and the loop settles
// a pending Promise on completion. The Promise form returns that Promise; the
// callback form attaches a settle reaction that fires the callback on the loop
// thread (emitFsCallbackReaction) — the reaction node keeps the callback
// GC-rooted while the op is in flight, and a binary ArrayBuffer/TypedArray body
// is copied raw at submit so no GC pointer crosses the thread boundary. Windows
// (no pool build yet) falls back to the inline path below: the *synchronous*,
// blocking runtime helper (runtime_fs.go) run inline, async-shaped only.
// A failure (the sync helper throws via @__kml_fs_throw) is caught with the same
// setjmp/@__kml_get_thrown primitive emitTry uses and re-surfaced as the `err`
// callback argument / a rejected Promise, so the async paths reuse every sync
// runtime helper verbatim with no parallel non-throwing variants.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// fsAsyncOp describes one fs operation in async form: the synchronous emitter
// reused as the operation body, the result type (TypeVoid for a mutate op), how
// many leading positional arguments it takes, and whether it delivers data to
// the callback (readFile/readdir → (err, data); the rest → (err)).
type fsAsyncOp struct {
	sync     func(*Emitter, []ast.Expression, ast.Pos) (Value, error)
	resultTy Type
	argc     int
	dataArg  bool
}

func fsAsyncOps() map[string]fsAsyncOp {
	return map[string]fsAsyncOp{
		"readFile":   {(*Emitter).emitFsReadFileSync, TypePtr, 1, true},
		"writeFile":  {(*Emitter).emitFsWriteFileSync, TypeVoid, 2, false},
		"appendFile": {(*Emitter).emitFsAppendFileSync, TypeVoid, 2, false},
		"unlink":     {(*Emitter).emitFsUnlinkSync, TypeVoid, 1, false},
		"mkdir":      {(*Emitter).emitFsMkdirSync, TypeVoid, 1, false},
		"rmdir":      {(*Emitter).emitFsRmdirSync, TypeVoid, 1, false},
		"rename":     {(*Emitter).emitFsRenameSync, TypeVoid, 2, false},
		"copyFile":   {(*Emitter).emitFsCopyFileSync, TypeVoid, 2, false},
		"readdir":    {(*Emitter).emitFsReaddirSync, ArrayOf(TypePtr), 1, true},
	}
}

// fsAsyncResultType returns PromiseOf(<op result>) for the Promise form, or
// TypeVoid for the callback form — used by call-type inference.
func fsAsyncPromiseResult(op string) (Type, bool) {
	spec, ok := fsAsyncOps()[op]
	if !ok {
		return Type{}, false
	}
	qt := PromiseOf(spec.resultTy)
	qt.PromiseTask = true
	return qt, true
}

// emitFsGuardedTail emits the setjmp/catch scaffold shared by both async forms.
// tryBody runs the (throwing) sync operation and delivers success; catchBody
// receives the caught error pointer and delivers failure. __kml_throw already
// pops the jmpbuf on the throw path, so catchBody must not pop it again — the
// mirror of emitTry.
func (e *Emitter) emitFsGuarded(tryBody func() error, catchBody func(errPtr string) error) error {
	e.ensureExceptionHelpers()
	tryL := e.freshLabel("fs.try")
	catchL := e.freshLabel("fs.catch")
	doneL := e.freshLabel("fs.done")
	jb := e.freshReg()
	sj := e.freshReg()
	thr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jb))
	e.emitInstr(fmt.Sprintf("%s = %s", sj, setjmpCall(jb)))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", thr, sj))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", thr, catchL, tryL))

	e.emitLabel(tryL)
	if err := tryBody(); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(catchL)
	errPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", errPtr))
	if err := catchBody(errPtr); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return nil
}

// fsAsyncEmptyResult builds the "no data" value handed to a callback's data
// slot on error: a null string pointer, or an empty {ptr,i64} array aggregate.
func (e *Emitter) fsAsyncEmptyResult(ty Type) Value {
	if ty.IsArray {
		agg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } { ptr null, i64 0 }, ptr null, 0", agg))
		return Value{Ref: agg, Ty: ty}
	}
	return Value{Ref: "null", Ty: ty}
}

// emitFsAsyncCallback implements fs.<op>(...opArgs, callback), delivering
// (err) or (err, data) — the classic Node callback form.
func (e *Emitter) emitFsAsyncCallback(op string, args []ast.Expression, pos ast.Pos) (Value, error) {
	spec := fsAsyncOps()[op]
	if len(args) != spec.argc+1 {
		return Value{}, fmt.Errorf("%d:%d: fs.%s takes %d argument(s) and a callback", pos.Line, pos.Col, op, spec.argc)
	}
	opArgs := args[:spec.argc]
	hints := []Type{errorObjType}
	if spec.dataArg {
		hints = append(hints, spec.resultTy)
	}

	// Pooled path (TDD-00185): submit the op to the thread pool — returning a
	// pending Promise — then fire the callback from a synthesized settle reaction
	// on the loop thread. The op runs off-thread, so the callback form is
	// genuinely non-blocking, reusing the promise-form submit and the GC-safe
	// reaction machinery (the reaction node keeps the callback rooted). Falls
	// back to the inline guarded path where the pool declines (Windows).
	if promVal, pooled, perr := e.emitFsPromisePooled(op, opArgs, pos); perr != nil {
		return Value{}, perr
	} else if pooled {
		cb, err := e.resolveCallbackWithHints(args[len(args)-1], hints)
		if err != nil {
			return Value{}, err
		}
		if err := e.emitFsCallbackReaction(spec, cb, promVal.Ref); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}

	cb, err := e.resolveCallbackWithHints(args[len(args)-1], hints)
	if err != nil {
		return Value{}, err
	}

	err = e.emitFsGuarded(
		func() error {
			res, serr := spec.sync(e, opArgs, pos)
			if serr != nil {
				return serr
			}
			e.emitInstr("call void @__kml_pop_jmpbuf()")
			cbArgs := []Value{{Ref: "null", Ty: errorObjType}}
			if spec.dataArg {
				cbArgs = append(cbArgs, res)
			}
			_, cerr := e.emitCBCall(cb, cbArgs)
			return cerr
		},
		func(errPtr string) error {
			cbArgs := []Value{{Ref: errPtr, Ty: errorObjType}}
			if spec.dataArg {
				cbArgs = append(cbArgs, e.fsAsyncEmptyResult(spec.resultTy))
			}
			_, cerr := e.emitCBCall(cb, cbArgs)
			return cerr
		},
	)
	if err != nil {
		return Value{}, err
	}
	return Value{Ty: TypeVoid}, nil
}

// fsPoolOpID maps an fs.promises op to its pool op id — must match the enum in
// threadpoolsrc/klainpool.c and the order of poolThunks in threadpool.go.
var fsPoolOpID = map[string]int{
	"readFile": 0, "writeFile": 1, "appendFile": 2, "unlink": 3,
	"mkdir": 4, "rmdir": 5, "rename": 6, "copyFile": 7, "readdir": 8,
}

// emitFsPromisePooled lowers an fs.promises op onto the thread pool (TDD-00185):
// evaluate + coerce its string arguments, allocate a *pending* task Promise,
// hand them to the C submit entry (which strdup's the args and queues the op),
// and return the pending Promise. The awaiting fiber parks on it through the
// existing await-on-unsettled-Promise path; a worker runs the op off-thread
// under its own setjmp guard and the loop's pool dispatch settles the Promise —
// fulfilled with the op's result, or rejected with the Error the sync helper
// would have thrown. Returns pooled=false for cases that must stay inline (a
// binary-data writeFile/appendFile, whose byte-length dispatch the inline path
// owns), so the caller falls through.
func (e *Emitter) emitFsPromisePooled(op string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	// The pool runtime (klainpool.c) is POSIX-only — pthread condvars plus a
	// socketpair/select() wakeup with no Win32 build. Windows keeps the inline
	// settled-Promise path until the reactor work (TDD-00182/00183) subsumes it.
	if targetGOOS() == "windows" {
		return Value{}, false, nil
	}
	opid, ok := fsPoolOpID[op]
	if !ok {
		return Value{}, false, nil
	}
	spec := fsAsyncOps()[op]

	// writeFile/appendFile of an ArrayBuffer/TypedArray pools onto the pool's
	// explicit-length byte thunk (KML_OP_*FILE_BYTES): the buffer is copied raw
	// at submit so a body with an embedded NUL writes out whole and no GC
	// pointer crosses the thread boundary.
	if (op == "writeFile" || op == "appendFile") &&
		func() bool { dt := e.inferExprType(args[1]); return dt.IsArrayBuffer || dt.IsTypedArray }() {
		return e.emitFsPromisePooledBytes(op, args, pos)
	}

	argRefs := []string{"null", "null"}
	for i := 0; i < spec.argc; i++ {
		v, err := e.emitExpr(args[i])
		if err != nil {
			return Value{}, false, err
		}
		argRefs[i] = e.coerce(v, TypePtr).Ref
	}

	e.ensurePromiseRuntime()
	e.ensureThreadPool()
	q := e.emitAllocSettledPromise() // pending (state 0); the pool settles it
	e.emitInstr(fmt.Sprintf("call void @__kml_pool_submit(i32 %d, ptr %s, ptr %s, ptr %s)",
		opid, q, argRefs[0], argRefs[1]))

	qt := PromiseOf(spec.resultTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, true, nil
}

// Pool op ids for the binary-write thunks — must match the KML_OP_* enum in
// threadpoolsrc/klainpool.c (after READSTREAM = 9).
const (
	fsPoolOpWriteFileBytes  = 10
	fsPoolOpAppendFileBytes = 11
)

// emitFsPromisePooledBytes pools a binary writeFile/appendFile: it resolves the
// ArrayBuffer/TypedArray body to (ptr, byteLen), allocates a pending Promise,
// and submits __kml_pool_submit_write_bytes, which memcpy's the bytes into a
// raw buffer the work item owns. The worker writes them off-thread via the
// explicit-length runtime helper; the loop settles the Promise at drain.
func (e *Emitter) emitFsPromisePooledBytes(op string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, false, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	dataTy := e.inferExprType(args[1])
	var dataRef, lenRef string
	if dataTy.IsArrayBuffer {
		bufVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, false, err
		}
		lenVal, err := e.emitArrayBufferByteLength(bufVal)
		if err != nil {
			return Value{}, false, err
		}
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))
		dataRef, lenRef = dataReg, lenVal.Ref
	} else { // TypedArray
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[1], pos)
		if err != nil {
			return Value{}, false, err
		}
		byteLenVal, err := e.emitTypedArrayByteLength(lenReg, elemTy)
		if err != nil {
			return Value{}, false, err
		}
		dataRef, lenRef = ptrReg, byteLenVal.Ref
	}

	opid := fsPoolOpWriteFileBytes
	if op == "appendFile" {
		opid = fsPoolOpAppendFileBytes
	}

	e.ensurePromiseRuntime()
	e.ensureThreadPool()
	q := e.emitAllocSettledPromise() // pending (state 0); the pool settles it
	e.emitInstr(fmt.Sprintf("call void @__kml_pool_submit_write_bytes(i32 %d, ptr %s, ptr %s, ptr %s, i64 %s)",
		opid, q, pathVal.Ref, dataRef, lenRef))

	qt := PromiseOf(TypeVoid)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, true, nil
}

// emitFsCallbackReaction attaches a settle reaction to the pending pooled
// Promise that fires the Node callback on the loop thread: env carries the
// promise and the callback's closure header (so the reaction node keeps the
// callback GC-rooted while the op is in flight), and a per-site runner invokes
// the callback with (err) / (err, data) reconstructed from the settled words.
func (e *Emitter) emitFsCallbackReaction(spec fsAsyncOp, cb Callback, promiseReg string) error {
	e.fsCbCtr++
	runner := fmt.Sprintf("@__kml_fs_cb_run_%d", e.fsCbCtr)
	if err := e.emitFsCbRunner(runner, spec, cb.ty); err != nil {
		return err
	}
	e.ensureMalloc()
	// env = { ptr promise, ptr cbHdr }
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", e0, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", promiseReg, e0))
	e1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", e1, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cb.hdrPtr, e1))
	// closure = { runner, env }
	clo := e.freshReg()
	cfp := e.freshReg()
	cep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cfp, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", runner, cfp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", cep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, cep))
	e.emitAttachPromiseReaction(promiseReg, clo)
	return nil
}

// emitFsCbRunner emits `void @<runner>(ptr %env)` (env = {promise, cbHdr}): on
// the loop thread at settle time it reads the promise's state + result words and
// invokes the callback via emitCBCall — which reconciles the (err)/(err, data)
// arity and coerces to the callback's declared param types, exactly as the
// inline form does. Built through a fresh IR-builder pair (the emit_chan.go
// thunk idiom) so it composes with emitCBCall's normal instruction emission.
func (e *Emitter) emitFsCbRunner(runner string, spec fsAsyncOp, cbTy Type) error {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.blockDone = false

	build := func() error {
		pP := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", pP))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, pP))
		cbP := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", cbP))
		cbh := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cbh, cbP))

		res := e.loadPromiseWord(p, 0)
		v0 := e.loadPromiseWord(p, 2)
		v1 := e.loadPromiseWord(p, 3)

		cbDesc := Callback{kind: cbClosure, hdrPtr: cbh, ty: cbTy}
		fulL := e.freshLabel("fscb.ful")
		rejL := e.freshLabel("fscb.rej")
		isful := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isful, res))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isful, fulL, rejL))

		e.emitLabel(fulL)
		fulArgs := []Value{{Ref: "null", Ty: errorObjType}}
		if spec.dataArg {
			fulArgs = append(fulArgs, e.fsAsyncDataFromWords(spec.resultTy, v0, v1))
		}
		if _, err := e.emitCBCall(cbDesc, fulArgs); err != nil {
			return err
		}
		e.emitTerminator("ret void")

		e.emitLabel(rejL)
		errp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errp, v0))
		rejArgs := []Value{{Ref: errp, Ty: errorObjType}}
		if spec.dataArg {
			rejArgs = append(rejArgs, e.fsAsyncEmptyResult(spec.resultTy))
		}
		if _, err := e.emitCBCall(cbDesc, rejArgs); err != nil {
			return err
		}
		e.emitTerminator("ret void")
		return nil
	}
	buildErr := build()
	if buildErr == nil {
		e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env) {\nentry:\n", runner))
		e.functions.WriteString(e.allocas.String())
		e.functions.WriteString(e.body.String())
		e.functions.WriteString("}\n")
	}
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.blockDone = savedBlockDone
	return buildErr
}

// loadPromiseWord loads one i64 field of a task-promise struct (0 = state,
// 2 = v0, 3 = v1).
func (e *Emitter) loadPromiseWord(p string, idx int) string {
	gp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gp, promiseStructIR, p, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, gp))
	return r
}

// fsAsyncDataFromWords rebuilds a callback's data argument from the settled
// result words: a bare pointer (readFile) or a {ptr,i64} array aggregate
// (readdir), tagged with the op's result type for emitCBCall to coerce.
func (e *Emitter) fsAsyncDataFromWords(ty Type, v0, v1 string) Value {
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, v0))
	if ty.IsArray {
		a0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", a0, p))
		agg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", agg, a0, v1))
		return Value{Ref: agg, Ty: ty}
	}
	return Value{Ref: p, Ty: ty}
}

// emitFsAsyncPromise implements the Promise form — fs.promises.<op>(...opArgs)
// and the 'fs/promises' named import — returning a settled task Promise.
func (e *Emitter) emitFsAsyncPromise(op string, args []ast.Expression, pos ast.Pos) (Value, error) {
	spec := fsAsyncOps()[op]
	if len(args) != spec.argc {
		return Value{}, fmt.Errorf("%d:%d: fs.promises.%s takes %d argument(s)", pos.Line, pos.Col, op, spec.argc)
	}
	// TDD-00185: the fs.promises ops run on the blocking-work thread pool — a
	// genuinely non-blocking op that returns a *pending* Promise the loop settles
	// when the worker completes. Binary-data writes (writeFile/appendFile of an
	// ArrayBuffer/TypedArray) decline pooling and fall through to the inline path
	// below, which already handles the byte-length dispatch.
	if v, pooled, err := e.emitFsPromisePooled(op, args, pos); err != nil {
		return Value{}, err
	} else if pooled {
		return v, nil
	}
	e.ensurePromiseRuntime()
	q := e.emitAllocSettledPromise()

	err := e.emitFsGuarded(
		func() error {
			res, serr := spec.sync(e, args, pos)
			if serr != nil {
				return serr
			}
			e.emitInstr("call void @__kml_pop_jmpbuf()")
			// A void mutate op stores nothing — the await path returns void
			// without loading the value slot (emit_async.go).
			if spec.resultTy.IR != "void" {
				e.storePromiseValue(q, res)
			}
			e.emitSetPromiseState(q, 1)
			return nil
		},
		func(errPtr string) error {
			e.emitAsyncGenRejectPromise(q, errPtr)
			return nil
		},
	)
	if err != nil {
		return Value{}, err
	}
	qt := PromiseOf(spec.resultTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}
