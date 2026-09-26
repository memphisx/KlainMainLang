// emit_async_coroutine.go — every async callable that awaits is a coroutine
// (TDD-00223 §2).
//
// The specialised task path (emit_task_emit.go) covers top-level `async
// function` declarations the classifier marks may-suspend. Every other async
// callable — an arrow, a function expression, a method, a declaration the
// classifier's walker did not see through — used to run its body synchronously
// on the caller's stack, so an `await` inside it could only wait by spinning a
// private drive that starved the event loop.
//
// Those callables are now wrapped at the callee. The body is emitted unchanged
// as `<name>.inner`; the symbol everyone calls, `<name>`, keeps the *same LLVM
// signature* (so closures, method dispatch and callbacks handed to builtins are
// untouched) and becomes:
//
//	<name>(params…):
//	    P      = a fresh pending task promise
//	    bundle = heap copy of the LLVM parameters, verbatim
//	    spawn a coroutine on <name>.tramp(bundle), promise P
//	    return P            — at the body's first park, or at its end
//
//	<name>.tramp(bundle):   (runs on the coroutine's stack)
//	    H = <name>.inner(params…)     every await inside parks the coroutine
//	    copy H's settled value/reason into P; a rejection is handed to
//	    @__kml_task_reject, a fulfilment returns to the trampoline, which
//	    finishes the task and settles P
//
// The bundle is type-agnostic: it stores LLVM values, not language values.
// Nothing in it points into the caller's frame — array arguments travel as
// their heap header pointer, closure environments and `this` are heap objects,
// and by-value tuple parameters are excluded for async signatures
// (emit_tuple_byval.go).
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// programMayWait reports whether the program contains anything that can wait on
// the event loop: an `await`, a `for await`, or a fetch call (whose promise
// reactions run as coroutines), in the entry module or a worker module compiled
// with it. It decides hasMaySuspend before any code is emitted, because runtime
// functions emitted early (the fetch waits) include their coroutine-park path
// only when it is set.
func programMayWait(prog *ast.Program) bool {
	found := false
	visit := func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AwaitExpression:
			found = true
		case *ast.ForOfStatement:
			found = found || n.Await
		case *ast.CallExpression:
			if id, ok := n.Callee.(*ast.Identifier); ok && id.Name == "fetch" {
				found = true
			}
		}
		return !found
	}
	ast.Inspect(prog, visit)
	for _, wm := range prog.WorkerModules {
		for _, st := range wm.Body {
			ast.Inspect(st, visit)
		}
	}
	return found
}

// splitLLVMParams splits a parameter list ("ptr %env, { i1, double } %v_x, i64
// %n") into (type, name) pairs. Commas inside an aggregate type do not split.
func splitLLVMParams(paramStr string) (types, names []string) {
	depth := 0
	start := 0
	flush := func(end int) {
		item := strings.TrimSpace(paramStr[start:end])
		if item == "" {
			return
		}
		sp := strings.LastIndex(item, " ")
		if sp < 0 {
			types = append(types, item)
			names = append(names, "")
			return
		}
		types = append(types, strings.TrimSpace(item[:sp]))
		names = append(names, strings.TrimSpace(item[sp+1:]))
	}
	for i, c := range paramStr {
		switch c {
		case '{', '[', '<', '(':
			depth++
		case '}', ']', '>', ')':
			depth--
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(paramStr))
	return
}

// writeAsyncDefinition writes an inline-async callable's definition from the
// current function buffers (e.allocas / e.body). name is the symbol without its
// leading '@' unless it already carries one. When the body awaited, the body
// becomes `<name>.inner` and `<name>` the coroutine-spawning wrapper described
// at the top of this file; otherwise the plain definition is written, exactly
// as before.
func (e *Emitter) writeAsyncDefinition(name, paramStr string, awaited bool) {
	sym := name
	if !strings.HasPrefix(sym, "@") {
		sym = "@" + sym
	}
	if !awaited || e.emittingHTTPHandler {
		// An http.listen handler is driven by its connection fiber, which parks
		// on the fiber itself — it is already a coroutine of its own kind.
		e.functions.WriteString(fmt.Sprintf("\ndefine ptr %s(%s) {\nentry:\n", sym, paramStr))
		e.functions.WriteString(e.allocas.String())
		e.functions.WriteString(e.body.String())
		e.functions.WriteString("}\n")
		return
	}
	e.ensureTaskRuntime()
	e.ensureMalloc()
	inner := quoteSymSuffix(sym, ".inner")
	tramp := quoteSymSuffix(sym, ".tramp")

	e.functions.WriteString(fmt.Sprintf("\ndefine internal ptr %s(%s) {\nentry:\n", inner, paramStr))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	types, names := splitLLVMParams(paramStr)
	bundleTy := "{ " + strings.Join(types, ", ") + " }"
	if len(types) == 0 {
		bundleTy = "{ i8 }"
	}

	var w strings.Builder
	fmt.Fprintf(&w, "\ndefine ptr %s(%s) {\nentry:\n", sym, paramStr)
	fmt.Fprintf(&w, "  %%__co_sz = ptrtoint ptr getelementptr (%s, ptr null, i32 1) to i64\n", bundleTy)
	w.WriteString("  %__co_b = call ptr @malloc(i64 %__co_sz)\n")
	for i, ty := range types {
		fmt.Fprintf(&w, "  %%__co_f%d = getelementptr %s, ptr %%__co_b, i32 0, i32 %d\n", i, bundleTy, i)
		fmt.Fprintf(&w, "  store %s %s, ptr %%__co_f%d\n", ty, names[i], i)
	}
	w.WriteString("  %__co_p = call ptr @__kml_task_alloc_promise()\n")
	fmt.Fprintf(&w, "  %%__co_t = call ptr @__kml_spawn_task(ptr %s, ptr %%__co_b, ptr %%__co_p)\n", tramp)
	w.WriteString("  ret ptr %__co_p\n}\n")

	fmt.Fprintf(&w, "\ndefine internal void %s(ptr %%__co_b) {\nentry:\n", tramp)
	var args []string
	for i, ty := range types {
		fmt.Fprintf(&w, "  %%__co_f%d = getelementptr %s, ptr %%__co_b, i32 0, i32 %d\n", i, bundleTy, i)
		fmt.Fprintf(&w, "  %%__co_a%d = load %s, ptr %%__co_f%d\n", i, ty, i)
		args = append(args, fmt.Sprintf("%s %%__co_a%d", ty, i))
	}
	fmt.Fprintf(&w, "  %%__co_h = call ptr %s(%s)\n", inner, strings.Join(args, ", "))
	// inner returned, so its promise H is settled. Adopt it into this task's
	// promise P: value/reason words first, then the state through the task's own
	// finish (fulfilled — by returning to the trampoline) or reject path.
	w.WriteString("  %__co_t = load ptr, ptr @__kml_current_task, align 8\n")
	fmt.Fprintf(&w, "  %%__co_pp = getelementptr %s, ptr %%__co_t, i32 0, i32 %d\n", taskStructIR, taskPromiseSlot)
	w.WriteString("  %__co_p = load ptr, ptr %__co_pp, align 8\n")
	for _, f := range []int{2, 3} {
		fmt.Fprintf(&w, "  %%__co_hv%d_p = getelementptr %s, ptr %%__co_h, i32 0, i32 %d\n", f, promiseStructIR, f)
		fmt.Fprintf(&w, "  %%__co_hv%d = load i64, ptr %%__co_hv%d_p, align 8\n", f, f)
		fmt.Fprintf(&w, "  %%__co_pv%d_p = getelementptr %s, ptr %%__co_p, i32 0, i32 %d\n", f, promiseStructIR, f)
		fmt.Fprintf(&w, "  store i64 %%__co_hv%d, ptr %%__co_pv%d_p, align 8\n", f, f)
	}
	fmt.Fprintf(&w, "  %%__co_hs_p = getelementptr %s, ptr %%__co_h, i32 0, i32 0\n", promiseStructIR)
	w.WriteString("  %__co_hs = load i64, ptr %__co_hs_p, align 8\n")
	w.WriteString("  %__co_rej = icmp eq i64 %__co_hs, 2\n")
	w.WriteString("  br i1 %__co_rej, label %__co_reject, label %__co_done\n")
	w.WriteString("__co_reject:\n")
	w.WriteString("  %__co_err = inttoptr i64 %__co_hv2 to ptr\n")
	w.WriteString("  call void @__kml_task_reject(ptr %__co_t, ptr %__co_err)\n")
	w.WriteString("  ret void\n")
	w.WriteString("__co_done:\n  ret void\n}\n")
	e.functions.WriteString(w.String())
}

// quoteSymSuffix appends a suffix to an LLVM global symbol, inside its quotes
// when the symbol is a quoted name.
func quoteSymSuffix(sym, suffix string) string {
	if strings.HasSuffix(sym, `"`) {
		return sym[:len(sym)-1] + suffix + `"`
	}
	return sym + suffix
}
