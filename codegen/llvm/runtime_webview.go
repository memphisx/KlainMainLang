package llvm

import "fmt"

// runtime_webview.go — the extern declarations for the webview/webview C API
// (TDD-00142), plus the thread-safe eval dispatch trampoline. The
// implementation lives in the vendored C++ source (webview.go / webviewsrc);
// this file only declares the ABI the emitted IR calls into and the small
// IR-level helpers that wrap it.

// ensureWebviewReturnRunner emits (once) the promise-settlement reaction that
// resolves a page-side promise for an async `bind` (TDD-00142 Stage 3). It is a
// microtask/reaction runner — invoked as `void fn(ptr env)` — whose env is a
// { ptr promise, ptr webview_t, ptr id } node. When the promise has fulfilled
// it calls webview_return(w, id, 0, value) with the promise's value slot (a
// JSON string, per the async-bind contract of Promise<string>); when rejected
// it returns status 1 with a fixed JSON error. The settlement is driven on the
// GUI thread by the page-tick pump's microtask drain.
func (e *Emitter) ensureWebviewReturnRunner() {
	if e.webviewReturnRunnerEmitted {
		return
	}
	e.webviewReturnRunnerEmitted = true
	rejMsg := e.internString(`"native handler rejected"`)
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_wv_return_runner(ptr %%env) {
entry:
  %%p_p = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 0
  %%p = load ptr, ptr %%p_p, align 8
  %%w_p = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  %%w = load ptr, ptr %%w_p, align 8
  %%id_p = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  %%id = load ptr, ptr %%id_p, align 8
  %%state_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%state = load i64, ptr %%state_p, align 8
  %%val_p = getelementptr %s, ptr %%p, i32 0, i32 2
  %%valbits = load i64, ptr %%val_p, align 8
  %%valptr = inttoptr i64 %%valbits to ptr
  %%fulfilled = icmp eq i64 %%state, 1
  br i1 %%fulfilled, label %%ful, label %%rej
ful:
  call void @webview_return(ptr %%w, ptr %%id, i32 0, ptr %%valptr)
  ret void
rej:
  call void @webview_return(ptr %%w, ptr %%id, i32 1, ptr %s)
  ret void
}`, promiseStructIR, promiseStructIR, rejMsg))
}

// nextWebviewThunkID returns a fresh per-bind trampoline number.
func (e *Emitter) nextWebviewThunkID() int {
	id := e.webviewThunkCtr
	e.webviewThunkCtr++
	return id
}

// UsesWebview reports whether the program constructed a `new Webview(...)`,
// gating the C++ embedded-source link (EmbeddedCSources).
func (e *Emitter) UsesWebview() bool { return e.usedWebview }

// ensureWebviewRuntime declares the webview/webview extern C API exactly once
// and emits the eval-dispatch trampoline. The 0.10.0 API returns void (there
// is no webview_error_t in that release); webview_return takes (w, seq,
// status, result).
func (e *Emitter) ensureWebviewRuntime() {
	if e.usedWebview {
		return
	}
	e.usedWebview = true

	// The 13-function extern "C" surface (0.10.0).
	e.emitGlobal(`declare ptr @webview_create(i32, ptr)`)
	e.emitGlobal(`declare void @webview_destroy(ptr)`)
	e.emitGlobal(`declare void @webview_run(ptr)`)
	e.emitGlobal(`declare void @webview_terminate(ptr)`)
	e.emitGlobal(`declare void @webview_dispatch(ptr, ptr, ptr)`)
	e.emitGlobal(`declare void @webview_set_title(ptr, ptr)`)
	e.emitGlobal(`declare void @webview_set_size(ptr, i32, i32, i32)`)
	e.emitGlobal(`declare void @webview_navigate(ptr, ptr)`)
	e.emitGlobal(`declare void @webview_set_html(ptr, ptr)`)
	e.emitGlobal(`declare void @webview_init(ptr, ptr)`)
	e.emitGlobal(`declare void @webview_eval(ptr, ptr)`)
	e.emitGlobal(`declare void @webview_bind(ptr, ptr, ptr, ptr)`)
	e.emitGlobal(`declare void @webview_unbind(ptr, ptr)`)
	e.emitGlobal(`declare void @webview_return(ptr, ptr, i32, ptr)`)

	// The eval-dispatch trampoline: webview_dispatch(w, fn, arg) runs fn(w, arg)
	// on the GUI thread, so routing every eval through it makes w.eval()
	// thread-safe from any thread (a Worker pushing a UI update) at the cost of
	// one queue hop — safe to call on the GUI thread too (it just queues). The
	// js string arrives as the dispatch arg; kml strings are NUL-terminated and
	// live in never-freed storage, so no copy is needed.
	e.emitGlobal(`
define void @__kml_wv_eval_tramp(ptr %w, ptr %js) {
entry:
  call void @webview_eval(ptr %w, ptr %js)
  ret void
}`)
}
