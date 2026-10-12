package llvm

// runtime_webview.go — klain:webview (TDD-00142) is lib/node/klain_webview.ts;
// its `@link webview` compiles in the webview/webview binding (webview.go) and
// the natives over it (webviewsrc/natives.c).

// UsesWebview reports whether the program uses klain:webview, gating the C++
// embedded-source link (EmbeddedCSources).
func (e *Emitter) UsesWebview() bool { return e.usedWebview }

// ensureWebviewRuntime links the binding and its natives. On POSIX the
// runtime's loop runs inside the GUI's main loop (webviewsrc/loop_embed.cc),
// which needs loop.c; on Windows the page drives the timers, the task
// scheduler and the microtasks (natives.c's pump).
func (e *Emitter) ensureWebviewRuntime() {
	if e.usedWebview {
		return
	}
	e.usedWebview = true
	e.ensureStrHeaderRuntime()
	if e.opts.Target.OS() != "windows" {
		e.ensureHTTPRuntime()
		return
	}
	e.ensureTimerRuntime()
	e.ensureMicrotasks()
}
