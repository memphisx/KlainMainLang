// emit_fs_async.go — asynchronous fs: the callback form (fs.readFile(path, cb))
// and the Promise form (fs.promises.readFile(path) / import from 'fs/promises').
// TDD-00107.
//
// The underlying I/O is the existing *synchronous*, blocking runtime helper
// (runtime_fs.go) run inline — this compiler has no thread pool, so V1 is
// async-shaped, not truly non-blocking. Only delivery is async: the callback
// fires right after the operation, and the Promise is returned already settled.
// A failure (the sync helper throws via @__kml_fs_throw) is caught with the same
// setjmp/@__kml_get_thrown primitive emitTry uses and re-surfaced as the `err`
// callback argument / a rejected Promise, so the async paths reuse every sync
// runtime helper verbatim with no parallel non-throwing variants.
package llvm

import (
	"fmt"

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

	// writeFile/appendFile of an ArrayBuffer/TypedArray keeps the inline path
	// (its explicit-length write). Only plain-string data is pooled.
	if op == "writeFile" || op == "appendFile" {
		dataTy := e.inferExprType(args[1])
		if dataTy.IsArrayBuffer || dataTy.IsTypedArray {
			return Value{}, false, nil
		}
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
