package llvm

// emit_dynimport.go — dynamic `import(...)` codegen (TDD-00055 eager /
// TDD-00056 lazy shared-library islands). The frontend + resolver
// dependency-edge + flag plumbing and the lazy dlopen'd-island backend are
// fully wired here; the eager result-object-synthesis backend is not yet
// implemented and returns a clean codegen error under -dynamic-import=eager.

import (
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

// DynImportShimSource is the C runtime shim (TDD-00056) that loads a
// shared-library island on first use: it locates the running executable, builds
// the island path `<exe>.d/<hash>.<ext>` beside it (so the binary + its `.d/`
// directory relocate together), dlopen()s it, and calls the island's idempotent
// `__kml_dynmod_<hash>_init` to run the target's top-level exactly once. Linked
// only under -dynamic-import=lazy when the program uses import().
func DynImportShimSource() string {
	return `#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#if defined(__APPLE__)
#include <mach-o/dyld.h>
#define KML_ISLAND_EXT ".dylib"
#else
#include <unistd.h>
#define KML_ISLAND_EXT ".so"
#endif

static int kml_self_path(char *buf, unsigned long cap) {
#if defined(__APPLE__)
  unsigned int size = (unsigned int)cap;
  if (_NSGetExecutablePath(buf, &size) != 0) return -1;
  return 0;
#else
  ssize_t n = readlink("/proc/self/exe", buf, cap - 1);
  if (n < 0) return -1;
  buf[n] = '\0';
  return 0;
#endif
}

// Load the island for the given hash (idempotent: dlopen refcounts, init
// self-guards), run its top-level once, and return the dlopen handle so the
// caller can dlsym its export accessors. Aborts with a clear message on
// failure — a missing island is a deployment error worth surfacing loudly.
void *__kml_dynimport_load(const char *hash) {
  char exe[4096];
  if (kml_self_path(exe, sizeof(exe)) != 0) {
    fprintf(stderr, "dynamic import: cannot locate the running executable\n");
    abort();
  }
  char so[4200];
  snprintf(so, sizeof(so), "%s.d/%s%s", exe, hash, KML_ISLAND_EXT);
  void *h = dlopen(so, RTLD_NOW | RTLD_LOCAL);
  if (!h) {
    fprintf(stderr, "dynamic import: cannot load island %s: %s\n", so, dlerror());
    abort();
  }
  char sym[128];
  snprintf(sym, sizeof(sym), "__kml_dynmod_%s_init", hash);
  void (*init)(void) = (void (*)(void))dlsym(h, sym);
  if (!init) {
    fprintf(stderr, "dynamic import: island %s missing %s\n", so, sym);
    abort();
  }
  init();
  return h;
}

// Resolve one export accessor symbol out of a loaded island handle.
void *__kml_dynimport_sym(void *handle, const char *symname) {
  void *p = dlsym(handle, symname);
  if (!p) {
    fprintf(stderr, "dynamic import: island export %s not found\n", symname);
    abort();
  }
  return p;
}
`
}

// ensureDynImportShim declares the dlopen shim's entry point once. The C source
// itself is contributed by EmbeddedCSources when -dynamic-import=lazy is used.
func (e *Emitter) ensureDynImportShim() {
	if e.dynImportShimDeclared {
		return
	}
	e.dynImportShimDeclared = true
	e.emitGlobal("declare ptr @__kml_dynimport_load(ptr)")
	e.emitGlobal("declare ptr @__kml_dynimport_sym(ptr, ptr)")
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

	switch e.dynamicImportMode {
	case "lazy":
		if ex.ResolvedPath == "" {
			return Value{}, fmt.Errorf("%d:%d: dynamic import('%s'): unresolved target (internal: resolver did not annotate the path)", ex.GetPos().Line, ex.GetPos().Col, lit.Value)
		}
		// Load + run-once the island via the dlopen shim, keyed by the stable
		// hash of the target's absolute path (computed identically here and in
		// the island's own compile), then dlsym each annotated value export and
		// pack them into the typed result object `await import()` yields. Real
		// laziness: the target's top-level runs only now, on first import.
		e.ensureDynImportShim()
		hash := IslandHash(ex.ResolvedPath)
		handle := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_load(ptr %s)", handle, e.internString(hash)))

		// Build the typed result object: for each field, dlsym the island's
		// accessor and call it to read the export's value.
		objTy := e.importCallResultObjectType(ex)
		e.ensureCalloc()
		obj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, objTy.StructSize()))
		structIR := objTy.StructIR()
		for _, f := range objTy.Fields {
			symName := fmt.Sprintf("__kml_dynmod_%s_%s", hash, f.Name)
			fp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_sym(ptr %s, ptr %s)", fp, handle, e.internString(symName)))
			v := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call %s %s()", v, f.Ty.IR, fp))
			idx, _, _ := objTy.FieldIndex(f.Name)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", f.Ty.IR, v, gep, f.Ty.Align()))
		}
		return e.wrapResolvedPromise(Value{Ref: obj, Ty: objTy}), nil
	default: // "eager"
		return Value{}, fmt.Errorf("%d:%d: dynamic import('%s') under -dynamic-import=eager — the eager result-object backend (TDD-00055 Stage 2) is not yet implemented; the frontend and resolver edge are in place. Pass -dynamic-import=lazy for the shared-library backend once it lands", ex.GetPos().Line, ex.GetPos().Col, lit.Value)
	}
}
