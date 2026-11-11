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

// importCallResultObjectType computes the fixed-shape object type a lazy
// dynamic import yields: one field per annotated scalar/string export of the
// target (matching the island's accessor surface). Shared by emitImportCall
// (to build the object) and typeOfExpr (so `const m = await import(...)`
// types correctly).
func (e *Emitter) importCallResultObjectType(ex *ast.ImportCallExpression) Type {
	if ex.Namespace != "" {
		return TypeAny // the target's module namespace object (emitBundledFulfillFn)
	}
	var fields []Field
	for _, exp := range ex.Exports {
		ty := e.annotationType(exp.TypeAnnot)
		if ty.IsArray() || ty.IsObject() || ty.IsClass() {
			continue
		}
		fields = append(fields, Field{Name: exp.Name, Ty: ty})
	}
	return ObjectType(fields)
}

// emitImportCall lowers a dynamic `import(specifier)` expression of a string
// literal (the resolver lowers a computed one to its candidates).
func (e *Emitter) emitImportCall(ex *ast.ImportCallExpression) (Value, error) {
	e.usesDynamicImport = true

	lit, ok := ex.Specifier.(*ast.StringLiteral)
	if !ok {
		return Value{}, internalAt(ex.GetPos(), "computed import() specifier not lowered")
	}

	switch e.opts.DynamicImport {
	case "isolated":
		if ex.ResolvedPath == "" {
			return Value{}, internalAt(ex.GetPos(), "dynamic import('%s'): unresolved target (internal: resolver did not annotate the path)", lit.Value)
		}
		// Load + run-once the island via the dlopen shim, keyed by the stable
		// hash of the target's absolute path (computed identically here and in
		// the island's own compile). Real laziness: the target's top-level runs
		// only now, on first import — to completion, or (TDD-00225) to its first
		// top-level await, after which the island's module task is driven by
		// this program's event loop through the island's poll export and the
		// import() promise settles when the island's module promise does.
		e.ensureEventLoop()
		e.ensureExceptionHelpers()
		e.ensureMicrotasks()
		e.ensureUnhandledRejections()
		e.ensureNanBox()
		e.ensureTimerRuntime()
		hash := IslandHash(ex.ResolvedPath)
		handle := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_dynimport_load("+irPtr+" %s)", handle, e.internString(hash)))
		objTy := e.importCallResultObjectType(ex)
		settleFn := e.emitImportSettleFn(hash, objTy)
		// island.c's exports, by fixed names in the island's own library; an
		// island without a top-level await has none and is never polled.
		fixed := func(suffix string) string {
			fp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_dynimport_sym_opt("+irPtr+" %s, "+irPtr+" %s)", fp, handle, e.internString("__kml_island_"+suffix)))
			return fp
		}
		stateFn, pollFn, errFn, dlFn, beFn := fixed("state"), fixed("poll"), fixed("error"), fixed("next_deadline_ns"), fixed("backend")
		prom := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", prom))
		e.emitInstr(fmt.Sprintf("call void @__kml_dynimport_watch("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" @%s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)",
			handle, prom, settleFn, stateFn, pollFn, errFn, dlFn, beFn))
		promTy := PromiseOf(objTy)
		promTy.PromiseTask = true
		return Value{Ref: prom, Ty: promTy}, nil
	default: // "bundled", "lazy"
		return e.emitBundledImport(ex)
	}
}
