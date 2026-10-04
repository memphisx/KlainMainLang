package llvm

// emit_dynimport.go — dynamic `import(...)` codegen (TDD-00055 eager /
// TDD-00056 lazy shared-library islands). The frontend + resolver
// dependency-edge + flag plumbing and the lazy dlopen'd-island backend are
// fully wired here; the eager result-object-synthesis backend is not yet
// implemented and returns a clean codegen error under -dynamic-import=eager.

import (
	_ "embed"
	"fmt"
	"hash/fnv"
	"strings"

	"KlainMainLang/ast"
)

// IslandHash derives the stable, deterministic symbol/file hash for a
// shared-library island from its target's absolute source path (TDD-00056).
// Both the island's own compile and the loading call site compute it from the
// same absolute path, so no runtime handshake is needed to agree on names.
func IslandHash(absPath string) string {
	h := fnv.New64a()
	h.Write([]byte(absPath))
	return fmt.Sprintf("%016x", h.Sum64())
}

//go:embed dynimportsrc/dynimport.c
var dynImportShimSource string

// DynImportShimSource is the C runtime shim (TDD-00056) that loads a
// shared-library island on first use: it locates the running executable, builds
// the island path `<exe>.d/<hash>.<ext>` beside it (so the binary + its `.d/`
// directory relocate together), dlopen()s it, and calls the island's idempotent
// `__kml_dynmod_<hash>_init` to run the target's top-level exactly once. Linked
// only under -dynamic-import=lazy when the program uses import().
func DynImportShimSource() string { return dynImportShimSource }

// ensureDynImportShim declares the dlopen shim's entry point once. The C source
// itself is contributed by EmbeddedCSources when -dynamic-import=lazy is used.
func (e *Emitter) ensureDynImportShim() {
	if e.dynImportShimDeclared {
		return
	}
	e.dynImportShimDeclared = true
	e.emitGlobal("declare ptr @__kml_dynimport_load(ptr)")
	e.emitGlobal("declare ptr @__kml_dynimport_sym(ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_dynimport_open(ptr)")
}

// importCallResultObjectType computes the fixed-shape object type a lazy
// dynamic import yields: one field per annotated scalar/string export of the
// target (matching the island's accessor surface). Shared by emitImportCall
// (to build the object) and inferExprType (so `const m = await import(...)`
// types correctly).
func (e *Emitter) importCallResultObjectType(ex *ast.ImportCallExpression) Type {
	var fields []Field
	for _, exp := range ex.Exports {
		ty := e.resolveType(exp.TypeAnnot)
		if ty.IsArray || ty.IsObject || ty.IsClass {
			continue
		}
		fields = append(fields, Field{Name: exp.Name, Ty: ty})
	}
	return ObjectType(fields)
}

// emitImportCall lowers a dynamic `import(specifier)` expression. The specifier
// must be a string literal (this compiler resolves every import at compile
// time); a non-literal is a clean, specific error rather than a silent gap.
func (e *Emitter) emitImportCall(ex *ast.ImportCallExpression) (Value, error) {
	e.usesDynamicImport = true

	lit, ok := ex.Specifier.(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: dynamic import() requires a string-literal specifier — this compiler resolves all imports at compile time, so a runtime-computed specifier cannot be loaded", ex.GetPos().Line, ex.GetPos().Col)
	}

	switch e.opts.DynamicImport {
	case "isolated":
		if ex.ResolvedPath == "" {
			return Value{}, fmt.Errorf("%d:%d: dynamic import('%s'): unresolved target (internal: resolver did not annotate the path)", ex.GetPos().Line, ex.GetPos().Col, lit.Value)
		}
		// Load + run-once the island via the dlopen shim, keyed by the stable
		// hash of the target's absolute path (computed identically here and in
		// the island's own compile). Real laziness: the target's top-level runs
		// only now, on first import — to completion, or (TDD-00225) to its first
		// top-level await, after which the island's module task is driven by
		// this program's event loop through the island's poll export and the
		// import() promise settles when the island's module promise does.
		e.ensureDynImportShim()
		e.ensureDynImportWatch()
		hash := IslandHash(ex.ResolvedPath)
		handle := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_load(ptr %s)", handle, e.internString(hash)))
		objTy := e.importCallResultObjectType(ex)
		settleFn := e.emitImportSettleFn(hash, objTy)
		sym := func(suffix string) string {
			fp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_sym(ptr %s, ptr %s)", fp, handle, e.internString("__kml_dynmod_"+hash+"_"+suffix)))
			return fp
		}
		stateFn, pollFn, errFn, dlFn := sym("state"), sym("poll"), sym("error"), sym("next_deadline_ns")
		prom := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", prom))
		e.emitInstr(fmt.Sprintf("call void @__kml_dynimport_watch(ptr %s, ptr %s, ptr @%s, ptr %s, ptr %s, ptr %s, ptr %s)",
			handle, prom, settleFn, stateFn, pollFn, errFn, dlFn))
		promTy := PromiseOf(objTy)
		promTy.PromiseTask = true
		return Value{Ref: prom, Ty: promTy}, nil
	default: // "bundled", "lazy"
		return e.emitBundledImport(ex)
	}
}

// emitIslandTaskGlue appends the island exports the importer's loop drives an
// island with a top-level await through (TDD-00225): `_state` (the module
// promise's state word — 0 pending / 1 fulfilled / 2 rejected; 1 for an island
// without a top-level await, whose init ran it to completion), `_poll` (one
// non-blocking turn of the island's own loop, then the state, with 0x100 set
// when the loop reported idle — nothing in the island can wake it any more),
// `_next_deadline_ns` (the island's earliest pending timer on the shared
// monotonic clock, 0 for none) and `_error` (the rejection value). Written into
// the final module text, so every runtime piece it references was ensured
// earlier (emitter.go, where islandTask is decided).
func (e *Emitter) emitIslandTaskGlue(out *strings.Builder) {
	h := e.islandHash
	if !e.moduleTask {
		fmt.Fprintf(out, "define i64 @__kml_dynmod_%s_state() {\nentry:\n  ret i64 1\n}\n", h)
		fmt.Fprintf(out, "define i64 @__kml_dynmod_%s_poll() {\nentry:\n  ret i64 257\n}\n", h)
		fmt.Fprintf(out, "define i64 @__kml_dynmod_%s_next_deadline_ns() {\nentry:\n  ret i64 0\n}\n", h)
		fmt.Fprintf(out, "define ptr @__kml_dynmod_%s_error() {\nentry:\n  ret ptr null\n}\n", h)
		return
	}
	fmt.Fprintf(out, `define i64 @__kml_dynmod_%s_state() {
entry:
  %%p = load ptr, ptr @__kml_module_promise, align 8
  %%st_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  ret i64 %%st
}
define i64 @__kml_dynmod_%s_poll() {
entry:
  store i8 1, ptr @__kml_loop_nowait, align 1
  store i8 1, ptr @__kml_loop_oneshot, align 1
  store i1 false, ptr @__kml_loop_idle, align 1
  call void @__kml_event_loop_run()
  store i8 0, ptr @__kml_loop_oneshot, align 1
  store i8 0, ptr @__kml_loop_nowait, align 1
  %%idle = load i1, ptr @__kml_loop_idle, align 1
  %%idlebit = select i1 %%idle, i64 256, i64 0
  %%p = load ptr, ptr @__kml_module_promise, align 8
  %%st_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%r = or i64 %%st, %%idlebit
  ret i64 %%r
}
define ptr @__kml_dynmod_%s_error() {
entry:
  %%p = load ptr, ptr @__kml_module_promise, align 8
  %%v0_p = getelementptr %s, ptr %%p, i32 0, i32 2
  %%v0 = load i64, ptr %%v0_p, align 8
  %%err = inttoptr i64 %%v0 to ptr
  ret ptr %%err
}
`, h, promiseStructIR, h, promiseStructIR, h, promiseStructIR)
	if !e.usedTimers {
		fmt.Fprintf(out, "define i64 @__kml_dynmod_%s_next_deadline_ns() {\nentry:\n  ret i64 0\n}\n", h)
		return
	}
	// Earliest pending entry of the timer queue ({ id, fireAtNs, intervalMs,
	// closure }, intervalMs == -1 means cancelled/done — emit_timers.go).
	fmt.Fprintf(out, `define i64 @__kml_dynmod_%s_next_deadline_ns() {
entry:
  %%len = load i64, ptr @__kml_timer_len, align 8
  %%data = load ptr, ptr @__kml_timer_data, align 8
  br label %%loop
loop:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%next ]
  %%best = phi i64 [ 0, %%entry ], [ %%best2, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%done
body:
  %%slot = getelementptr { i64, i64, i64, ptr }, ptr %%data, i64 %%i
  %%iv_p = getelementptr { i64, i64, i64, ptr }, ptr %%slot, i32 0, i32 2
  %%iv = load i64, ptr %%iv_p, align 8
  %%live = icmp ne i64 %%iv, -1
  %%fa_p = getelementptr { i64, i64, i64, ptr }, ptr %%slot, i32 0, i32 1
  %%fa = load i64, ptr %%fa_p, align 8
  %%none = icmp eq i64 %%best, 0
  %%sooner = icmp slt i64 %%fa, %%best
  %%take0 = or i1 %%none, %%sooner
  %%take = and i1 %%live, %%take0
  %%best2 = select i1 %%take, i64 %%fa, i64 %%best
  br label %%next
next:
  %%inext = add i64 %%i, 1
  br label %%loop
done:
  ret i64 %%best
}
`, h)
}
