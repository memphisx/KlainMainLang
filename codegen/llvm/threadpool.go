package llvm

import (
	_ "embed"
	"fmt"
)

// threadpool.go — TDD-00185 Stages 1-2: a libuv-style blocking-work thread pool
// that makes fs async I/O genuinely non-blocking. The threading (pthreads,
// condvar, atomics, the socketpair wakeup) lives in the embedded C runtime
// (threadpoolsrc/klainpool.c); this file owns only the two IR thunks that
// carry the Promise/exception layout the C side is deliberately kept ignorant
// of, plus the Uses*/ensure* plumbing.
//
//go:embed threadpoolsrc/klainpool.c
var threadPoolSource string

// ThreadPoolSource returns the embedded pool C runtime, linked (with -pthread)
// whenever the program routes an fs op through the pool.
func ThreadPoolSource() string { return threadPoolSource }

// UsesThreadPool reports whether any pooled async fs op was emitted, so the CLI
// driver / conformance runner know to compile and link klainpool.c.
func (e *Emitter) UsesThreadPool() bool { return e.usedThreadPool }

// ThreadPoolCFlags returns the clang flags klainpool.c is compiled with — the
// single source of truth shared by EmbeddedCSources (the CLI / conformance
// runner) and the test build harness so the two can't drift. GC_allow_register_
// threads is call-once and shared across concurrency subsystems: the emitted IR
// calls it for Worker modules and klain:sync's C calls it when linked, so the
// pool owns the enable (KLAINPOOL_GC_ENABLE) only when neither of those does.
func (e *Emitter) ThreadPoolCFlags() []string {
	cflags := []string{"-pthread"}
	if e.isGCMode() {
		cflags = append(cflags, "-DKLAINPOOL_GC=1")
		if !e.hasWorkers && !e.UsesSync() {
			cflags = append(cflags, "-DKLAINPOOL_GC_ENABLE=1")
		}
	}
	return cflags
}

// ensureThreadPool emits the IR half of the pool exactly once: the two thunks
// klainpool.c calls (__kml_pool_thunk_readfile, __kml_pool_settle) and the
// extern declaration of the C submit entry point. Marks usedThreadPool so the
// C source gets linked and the reactor's pool hooks bind to the real C
// definitions rather than the no-op stubs (runtime_task.go).
func (e *Emitter) ensureThreadPool() {
	if e.usedThreadPool {
		return
	}
	e.usedThreadPool = true

	e.ensureExceptionHelpers() // __kml_push_jmpbuf / __kml_get_thrown / setjmp
	e.ensurePromiseSettle()    // __kml_promise_settle — called by __kml_pool_settle
	e.ensureStreamRuntime()    // __kml_rs_enqueue / __kml_rs_close — the stream drain (TDD-00186)

	// klainpool.c provides these — declare them so the emitted reactor's calls
	// resolve at the IR level (the no-op stubs in runtime_task.go are emitted
	// only when the pool is unused). The submit entry is called from the
	// lowering (emit_fs_async.go); the loop hooks from the reactor.
	e.emitGlobal("declare void @__kml_pool_submit(i32 noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @__kml_pool_submit_write_bytes(i32 noundef, ptr noundef, ptr noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare ptr @__kml_pool_stream_ctl_new(ptr noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare void @__kml_pool_submit_readstream(ptr noundef)")
	// Field-9/10 pull/cancel closures for the backpressured read stream (their
	// env is a C control block); referenced by name from the createReadStream site.
	e.emitGlobal("declare ptr @__kml_pool_stream_pull(ptr noundef)")
	e.emitGlobal("declare ptr @__kml_pool_stream_cancel(ptr noundef)")
	e.emitGlobal("declare i1 @__kml_pool_keepalive()")
	e.emitGlobal("declare i1 @__kml_pool_fdset_add(ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @__kml_pool_dispatch()")

	// TDD-00186 stream-completion drain helpers, called from klainpool.c's
	// dispatch on the loop thread — enqueue one chunk into the readable, or close
	// it at EOF. Reuse the WHATWG readable runtime verbatim; the C side stays
	// ignorant of the stream layout, exactly like __kml_pool_settle for Promises.
	e.emitGlobal(`
define void @__kml_pool_stream_chunk(ptr %rs, i64 %chunk) {
entry:
  %ig = call i64 @__kml_rs_enqueue(ptr %rs, i64 %chunk, i64 0)
  ret void
}`)
	e.emitGlobal(`
define void @__kml_pool_stream_end(ptr %rs) {
entry:
  %ig = call i64 @__kml_rs_close(ptr %rs)
  ret void
}`)

	// TDD-00186 STREAM_ERROR drain: a mid-read failure errors the readable so a
	// consumer's 'error'/for-await sees it, rather than a silent early EOF. The
	// worker can't allocate a JS Error off-thread, so it posts the errno and the
	// loop builds the `{kind,msg,name}` error object here (the reqbody shape) and
	// calls __kml_rs_error. The errno is not yet mapped to a specific message.
	streamErrMsg := e.internString("fs read stream failed")
	streamErrName := e.internString("Error")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_pool_stream_error(ptr %%rs, i64 %%errno) {
entry:
  %%eo = call ptr @malloc(i64 24)
  %%eo_kind = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 0
  store i64 0, ptr %%eo_kind, align 8
  %%eo_msg = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 1
  store ptr %s, ptr %%eo_msg, align 8
  %%eo_name = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 2
  store ptr %s, ptr %%eo_name, align 8
  %%bits = ptrtoint ptr %%eo to i64
  call void @__kml_rs_error(ptr %%rs, i64 %%bits)
  ret void
}`, streamErrMsg, streamErrName))

	// One thunk per pooled fs op. Each runs the existing *throwing* sync helper
	// under a per-worker setjmp guard (the jmpbuf stack is thread-local, so each
	// pool thread has its own) and returns { i64 v0, i64 v1, ptr err }: err set
	// means the op threw (reject with it), else (v0, v1) is the fulfil value —
	// a single pointer word for readFile, a {ptr,len} pair for readdir, or
	// ignored for the void ops. Reuses each __kml_fs_* helper verbatim; no
	// parallel non-throwing variants, mirroring emit_fs_async.go's inline design.
	for _, op := range poolThunks {
		for _, ens := range op.ensures {
			ens(e)
		}
		e.emitGlobal(e.buildPoolThunk(op))
	}

	// __kml_pool_settle: store the two result words into the promise's value
	// slots and settle — fulfilled (1) or rejected (2). Called on the loop
	// thread from klainpool.c's dispatch drain, so __kml_promise_settle runs on
	// the right thread and enqueues the reaction/resume microtask into this
	// loop's queue. On reject, v0 carries the Error pointer (bit-identical to the
	// reject path in emit_generators.go's emitAsyncGenRejectPromise).
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_pool_settle(ptr %%p, i64 %%v0, i64 %%v1, i64 %%state) {
entry:
  %%v0p = getelementptr %s, ptr %%p, i32 0, i32 2
  store i64 %%v0, ptr %%v0p, align 8
  %%v1p = getelementptr %s, ptr %%p, i32 0, i32 3
  store i64 %%v1, ptr %%v1p, align 8
  call void @__kml_promise_settle(ptr %%p, i64 %%state)
  ret void
}`, promiseStructIR, promiseStructIR))
}

// poolThunkSpec describes one pooled fs op's IR thunk: its C-visible name
// suffix, argument count (1 or 2 path/string args), the runtime declarations it
// needs, and the try-block body that runs the op and leaves %v0/%v1 (i64) set.
type poolThunkSpec struct {
	name    string
	argc    int
	ensures []func(*Emitter)
	tryBody string
	// params overrides the derived "ptr %a0[, ptr %a1]" parameter list when a
	// thunk needs a non-pointer argument (the binary writes take an i64 length).
	params string
}

// zeroWords sets both result words to 0 — the shape every void op returns
// (LLVM has no "assign a constant to a name", so add 0).
const zeroWords = "  %v0 = add i64 0, 0\n  %v1 = add i64 0, 0"

var poolThunks = []poolThunkSpec{
	{"readfile", 1, []func(*Emitter){(*Emitter).ensureFsReadFile},
		"  %res = call ptr @__kml_fs_read_file(ptr %a0)\n  %v0 = ptrtoint ptr %res to i64\n  %v1 = add i64 0, 0", ""},
	{"writefile", 2, []func(*Emitter){(*Emitter).ensureFsWriteFile},
		"  call void @__kml_fs_write_file(ptr %a0, ptr %a1)\n" + zeroWords, ""},
	{"appendfile", 2, []func(*Emitter){(*Emitter).ensureFsAppendFile},
		"  call void @__kml_fs_append_file(ptr %a0, ptr %a1)\n" + zeroWords, ""},
	{"unlink", 1, []func(*Emitter){(*Emitter).ensureFsUnlink},
		"  call void @__kml_fs_unlink(ptr %a0)\n" + zeroWords, ""},
	{"mkdir", 1, []func(*Emitter){(*Emitter).ensureFsMkdir},
		"  call void @__kml_fs_mkdir(ptr %a0)\n" + zeroWords, ""},
	{"rmdir", 1, []func(*Emitter){(*Emitter).ensureFsRmdir},
		"  call void @__kml_fs_rmdir(ptr %a0)\n" + zeroWords, ""},
	{"rename", 2, []func(*Emitter){(*Emitter).ensureFsRename},
		"  call void @__kml_fs_rename(ptr %a0, ptr %a1)\n" + zeroWords, ""},
	{"copyfile", 2, []func(*Emitter){(*Emitter).ensureFsReadFileRaw, (*Emitter).ensureFsWriteFileBytes},
		"  %raw = call { ptr, i64 } @__kml_fs_read_file_raw(ptr %a0)\n" +
			"  %buf = extractvalue { ptr, i64 } %raw, 0\n" +
			"  %len = extractvalue { ptr, i64 } %raw, 1\n" +
			"  call void @__kml_fs_write_file_bytes(ptr %a1, ptr %buf, i64 %len)\n" + zeroWords, ""},
	{"readdir", 1, []func(*Emitter){(*Emitter).ensureFsReaddir},
		"  %arr = call { ptr, i64 } @__kml_fs_readdir(ptr %a0, i1 false)\n" +
			"  %p = extractvalue { ptr, i64 } %arr, 0\n" +
			"  %v0 = ptrtoint ptr %p to i64\n" +
			"  %v1 = extractvalue { ptr, i64 } %arr, 1", ""},
	// Binary writes take (path, data, len) — an explicit i64 length, so they
	// override the derived ptr-only parameter list.
	{"writefile_bytes", 0, []func(*Emitter){(*Emitter).ensureFsWriteFileBytes},
		"  call void @__kml_fs_write_file_bytes(ptr %a0, ptr %a1, i64 %a2)\n" + zeroWords,
		"ptr %a0, ptr %a1, i64 %a2"},
	{"appendfile_bytes", 0, []func(*Emitter){(*Emitter).ensureFsAppendFileBytes},
		"  call void @__kml_fs_append_file_bytes(ptr %a0, ptr %a1, i64 %a2)\n" + zeroWords,
		"ptr %a0, ptr %a1, i64 %a2"},
}

// buildPoolThunk composes the full thunk for one op: the shared setjmp-guard
// scaffold wrapped around the op-specific try body. The three result words
// (v0, v1, err) are written through an out-pointer (`kml_triple *out` on the C
// side) rather than returned by value — a 24-byte struct return is lowered
// differently by hand-written IR and by Clang's C ABI (the >16-byte sret rule),
// and the out-pointer sidesteps that mismatch entirely.
func (e *Emitter) buildPoolThunk(op poolThunkSpec) string {
	params := op.params
	if params == "" {
		params = "ptr %a0"
		if op.argc == 2 {
			params = "ptr %a0, ptr %a1"
		}
	}
	return fmt.Sprintf(`
define void @__kml_pool_thunk_%s(%s, ptr %%out) {
entry:
  %%jb = call ptr @__kml_push_jmpbuf()
  %%sj = %s
  %%thr = icmp ne i32 %%sj, 0
  br i1 %%thr, label %%caught, label %%try
try:
%s
  call void @__kml_pop_jmpbuf()
  %%o1 = getelementptr i8, ptr %%out, i64 8
  %%o2 = getelementptr i8, ptr %%out, i64 16
  store i64 %%v0, ptr %%out, align 8
  store i64 %%v1, ptr %%o1, align 8
  store ptr null, ptr %%o2, align 8
  ret void
caught:
  %%err = call ptr @__kml_get_thrown()
  %%c1 = getelementptr i8, ptr %%out, i64 8
  %%c2 = getelementptr i8, ptr %%out, i64 16
  store i64 0, ptr %%out, align 8
  store i64 0, ptr %%c1, align 8
  store ptr %%err, ptr %%c2, align 8
  ret void
}`, op.name, params, setjmpCall("%jb"), op.tryBody)
}
