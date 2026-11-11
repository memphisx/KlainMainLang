// Package resolver implements KlainMainLang's multi-file module resolution:
// parses the entry file plus everything it transitively imports, validates
// import/export usage, and merges everything into one *ast.Program ready
// for codegen/llvm — which never sees an *ast.ImportDeclaration or
// *ast.ExportDeclaration node; both are fully consumed here.
//
// Scope (see docs/adr for the full writeup):
//   - Whole-program compilation, not separate compilation units: every
//     reachable file is merged into one combined AST before codegen runs.
//     There is no linker step and no per-file LLVM module boundary.
//   - An acyclic imported (non-entry) file's top-level statements run
//     exactly once, in real dependency order, strictly before whatever
//     imports it (TDD-00052) — diamond-shared dependencies still run
//     exactly once, via the same per-path memoization that already
//     deduplicates parsing. A file that genuinely participates in an
//     import cycle keeps the original, stricter V1 restriction instead:
//     only declarations (function/const/let/var/interface/type/enum/class)
//     plus its own imports, no bare executable statements — and a
//     top-level var/let/const initializer must additionally be a
//     compile-time literal, closing a real hazard (a circular pair of
//     files reading each other's not-yet-initialized top-level binding —
//     see validateCyclicFile's doc comment) without needing to model real
//     ES modules' TDZ/live-binding semantics.
//   - True per-file module scope (TDD-00041): every top-level declaration
//     gets a file-private mangled name (see rename.go's mangleFileDecls),
//     and every reference to it — within its own file, or via another
//     file's `import` — is rewritten to match, via a real scope-aware walk
//     (rename.go's renameFile) rather than a blind find-and-replace. Two
//     unrelated files may freely declare the same top-level name; only an
//     importing file that binds two different files' exports to the same
//     local name (with no `as` to disambiguate) is rejected.
//   - Import aliasing (`import { a as b }`) is fully supported — it's a
//     direct, safe consequence of the per-file rename mechanism above:
//     `b` simply *is* the target's mangled name for `a` inside the
//     importing file, no separate rename step or shadowing risk involved.
//   - Relative paths (`./`, `../`) are resolved against the importing
//     file's own directory, with `.ts` auto-appended if the path has no
//     extension. A bare specifier (`import x from 'pkg'`) resolves against
//     a `klain_modules/<name>/klain.json`'s `"main"` field (TDD-00054 Stage
//     1) if a `klain_modules` directory exists somewhere above the entry
//     file — found once, anchored at the entry file (not each importing
//     file's own directory, unlike Node's per-importer walk), so every file
//     in the whole program resolves a given package name against the same
//     single directory. No npm/`node_modules` interop, no index-file
//     resolution, no package registry, no version resolution — see
//     TDD-00054 for the fuller design (versioning, fetching, the klmpm
//     tool itself), none of it built yet; this is deliberately just the
//     resolution half, exercisable by hand-constructing a
//     `klain_modules/<name>/` directory.
package resolver

import (
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/diag"
	"KlainMainLang/freevars"
	"KlainMainLang/lib"
	"KlainMainLang/options"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

type fileInfo struct {
	path    string
	src     string
	prog    *ast.Program
	isEntry bool
	// selfWorker: the file starts a worker of itself (`new Worker(__filename)`);
	// workerCopy is a second parse of it, renamed as the first, whose
	// executable statements are the worker module's body.
	selfWorker bool
	workerCopy *ast.Program
	exported   map[string]bool
	// typeExports is the exported names that are types only (an interface
	// or type alias): no member of the module's namespace object.
	typeExports map[string]bool
	index       int               // assigned at first visit; used to build this file's mangled-name suffix (TDD-00041)
	mangled     map[string]string // original top-level declaration name -> this file's mangled name for it

	// TDD-00051: re-export bookkeeping. reExportBindings is populated by the
	// export-name-augmentation pass (before mangleFileDecls runs, so it only
	// knows local/target/remote names, not mangled values yet);
	// reExportMangled is filled in by the follow-up pass once every file's
	// own mangled map is final. Deliberately kept separate from `mangled` —
	// see resolveReExports's doc comment for why re-export aliases must
	// never be visible to this file's own intra-file scope lookup.
	reExportBindings []reExportBinding
	reExportMangled  map[string]string
}

// reExportBinding is one resolved `export { remote as local } from` (or one
// name expanded out of `export * from`) entry, recorded against the
// re-exporting file before its target's mangled names are necessarily known.
type reExportBinding struct {
	local  string
	target string // absolute path of the source file
	remote string // name as declared/exported in the source file
}

// publicMangled returns the mangled name an importer sees for name — info's
// own declarations first, then anything info re-exports under that name.
// This is the file's *public* export table; it is deliberately not the same
// lookup renameFile uses to rewrite bare references inside info's own file
// (that's `mangled` alone) — a re-exported name is forwarded to importers
// without ever becoming a usable local identifier in the re-exporting file
// itself, matching real ES module semantics. See TDD-00051.
func (info *fileInfo) publicMangled(name string) (string, bool) {
	if m, ok := info.mangled[name]; ok {
		return m, true
	}
	m, ok := info.reExportMangled[name]
	return m, ok
}

// ResolveProgram parses entryPath and everything it transitively imports,
// validates import/export usage, and returns one merged *ast.Program, under
// the default (strict) modes.
func ResolveProgram(entryPath string) (*ast.Program, error) {
	return ResolveProgramWithOptions(entryPath, options.Options{})
}

// ResolveProgramWithOptions is ResolveProgram under the compile modes opts
// (TDD-00230 P2.6). The `-compat=js` lane governs two things here:
//   - whether the TS-only definite-assignment early error is suppressed
//     (plain JS has no such concept — TDD-00022 sub-problem 5);
//   - whether the program is type-checked (the strict lane is TypeScript's).
//
// `-dynamic-import=lazy` makes each dynamic import target an island.
func ResolveProgramWithOptions(entryPath string, opts options.Options) (*ast.Program, error) {
	compatJS, isolatedDynamicImport := opts.CompatJS(), opts.DynamicImport == "isolated"
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, fmt.Errorf("resolving entry path: %w", err)
	}

	// TDD-00054 Stage 1: found once, anchored at the entry file's own
	// directory — every bare-specifier lookup in the whole program
	// (whether from the entry tree or from inside a fetched package)
	// resolves against this same single directory. "" means no
	// klain_modules directory exists above the entry file at all, in which
	// case a bare specifier stays rejected exactly as before this TDD.
	klainModulesDir := findKlainModulesDir(filepath.Dir(entryAbs))

	files := map[string]*fileInfo{}
	var order []string // dependency-first visitation order of non-entry files
	// dynEdges: each file's dynamic import() targets under the bundled and
	// lazy backends (merged, but not static dependencies).
	dynEdges := map[string][]string{}
	nextIndex := 0

	// TDD-00052: cycle detection, so a file that genuinely participates in
	// an import cycle can be held to a stricter rule than an acyclic one
	// (see validateCyclicFile's doc comment for why). `stack` is the
	// current DFS recursion chain (paths, in visit order); `onStack` is
	// its O(1) membership check. On a back-edge — visiting a path that's
	// already on `stack`, not just already-finished — every file from that
	// path's position to the top of `stack` (inclusive) is a real cycle
	// member, marked in `cyclic`. This is a superset-safe approximation of
	// exact strongly-connected-component detection, not full Tarjan:
	// over-marking a file as cyclic only costs it some ergonomics (it
	// stays on the stricter rule), never a safety hole.
	var stack []string
	onStack := map[string]bool{}
	cyclic := map[string]bool{}

	// TDD-00098: worker entry files. A `new Worker('./w.ts')` path is a
	// dependency edge like an import's (the worker file and its own imports
	// must be parsed/mangled/renamed with everything else), but the worker
	// file's top-level statements are diverted into Program.WorkerModules at
	// merge time instead of the main statement stream. importTargets tracks
	// files reached via a real import edge so the "a worker entry cannot
	// also be imported" conflict is detectable after the DFS.
	importTargets := map[string]bool{}
	globalObjectNames := map[string]bool{}
	globalObjectEscapes_, globalObjectSpelled := false, map[string]bool{}
	workerTargets := map[string]bool{}
	// A literal eval worker's code, at the virtual path beside the file
	// creating it that it resolves its own imports from, and back.
	evalSources := map[string][]byte{}
	evalKeys := map[string]string{}
	dynamicImportTargets := map[string]bool{}
	// userEdges: a file's static (and eagerly merged dynamic) imports of
	// user files — the graph a worker re-evaluates on its own thread.
	userEdges := map[string][]string{}

	var visit func(path string, isEntry bool) error
	visit = func(path string, isEntry bool) error {
		if _, seen := files[path]; seen {
			if onStack[path] {
				for i := len(stack) - 1; i >= 0 && stack[i] != path; i-- {
					cyclic[stack[i]] = true
				}
				cyclic[path] = true
			}
			return nil // already visited, or in progress (cycle) — safe to skip
		}
		// In-progress placeholder, guards against re-visiting on a cycle.
		// The index is assigned here (not when the file is finalized below)
		// so every file — including one still being visited via a cycle —
		// has a stable, unique mangled-name suffix (TDD-00041) from the
		// moment it's first seen.
		files[path] = &fileInfo{index: nextIndex}
		nextIndex++

		stack = append(stack, path)
		onStack[path] = true
		defer func() {
			stack = stack[:len(stack)-1]
			onStack[path] = false
		}()

		var src []byte
		if es, ok := evalSources[path]; ok {
			src = es
		} else if path == entryAbs && opts.EntrySource != nil {
			src = []byte(*opts.EntrySource)
		} else if src, err = readSource(path); err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		prog, err := parser.Parse(string(src))
		if err != nil {
			return diag.InFile(err, path)
		}
		if err := lowerRequires(prog, path, isEntry, src, klainModulesDir); err != nil {
			return diag.InFile(err, path)
		}
		defaultImportsAsNamespaces(prog)
		if err := mutableExportsObjects(path, prog, compatJS); err != nil {
			return diag.InFile(err, path)
		}
		if isCommonJS(prog, path, src) {
			if err := wrapCommonJS(prog, path == entryAbs); err != nil {
				return diag.InFile(err, path)
			}
		}
		boundGlobal, err := bindNodeGlobal(prog, path, src)
		if err != nil {
			return diag.InFile(err, path)
		}
		requiresOfCommonJS(prog, func(source string) bool {
			return commonJSTarget(filepath.Dir(path), source, klainModulesDir)
		})
		markAssertCallSites(prog)
		selfWorker := rewriteSelfWorker(prog, path)
		if !strings.HasPrefix(path, lib.ModuleRoot) {
			_, isEval := evalSources[path]
			rewriteFileGlobals(prog, path, isEval)
			evalReassignment(path, prog)
			reassignedFunctionsAsVars(path, prog, compatJS)
			rewriteStaticEval(prog)
			varArgumentsAsBinding(path, prog)
		}
		if err := lowerBuiltinDynamicImports(prog); err != nil {
			return diag.InFile(err, path)
		}
		prog.WorkerPaths = workerPaths(prog)
		prog.WorkerEvals = workerEvals(prog)
		prog.DynamicImportNodes = dynamicImports(prog)
		eraseBuiltinTypeImports(prog)

		// TDD-00232: a global a global module implements, named here and not
		// declared here, brings the module in; the name links to its class
		// after checking (sema).
		idents := codeIdents(src)
		if !strings.HasPrefix(path, lib.ModuleRoot) {
			// The global object carries a native global the program names;
			// one it reads in a way no name shows carries every one, and
			// every global module's, as Node's does.
			if idents["eval"] || functionCtorCallRE.Match(src) {
				globalObjectEscapes_ = true
			}
			if globalObjectEscapes(path, src, prog) {
				globalObjectEscapes_ = true
				for _, n := range GlobalObjectNatives {
					idents[n] = true
				}
				for n := range lib.GlobalModuleNames() {
					idents[n] = true
				}
				idents["crypto"] = true
			} else if ctorFunctionCall(src) || sloppyThisUse(path, src, prog) {
				// The global object as a function's `this`, read only by
				// name: the globals those names are.
				names, _ := thisMemberNames(prog)
				for n := range names {
					idents[n] = true
				}
			}
			// A bare name resolves to the builtin without the object; only a
			// property read can reach it through the object.
			ast.Inspect(prog, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.MemberExpression:
					globalObjectSpelled[x.Property] = true
				case *ast.IndexExpression:
					if lit, ok := x.Index.(*ast.StringLiteral); ok {
						globalObjectSpelled[lit.Value] = true
					}
				case *ast.CallExpression:
					if ownKeyCheckTarget(x) != nil {
						globalObjectSpelled[x.Args[1].(*ast.StringLiteral).Value] = true
					}
				}
				return true
			})
			for _, n := range GlobalObjectNatives {
				if idents[n] && !declaresTopLevel(prog, n) {
					globalObjectNames[n] = true
				}
			}
		}
		for native, uses := range lib.NativeGlobalUses {
			if idents[native] && !declaresTopLevel(prog, native) {
				for _, u := range uses {
					idents[u] = true
				}
			}
		}
		for name, uses := range lib.LoweredFuncUses() {
			// A file declaring the name itself (lib/node/timers.ts's own
			// setTimeout) means its own, not the lowered global.
			// The rest of the uses, and a lone name no global has (a method:
			// `every`, `push`), are member names (`Uint8Array.from`): a
			// binding of the same name (`const from = …`) is no shadow.
			all := true
			for i, u := range uses {
				all = all && idents[u] && (i > 0 || !lib.GlobalValue(u) || !declaresTopLevel(prog, u))
			}
			if all {
				idents[name] = true
			}
		}
		for _, name := range sortedGlobalNames() {
			gpath := lib.GlobalModuleNames()[name]
			if gpath == path || !idents[name] || declaresTopLevel(prog, name) {
				continue
			}
			importTargets[gpath] = true
			if err := visit(gpath, false); err != nil {
				return err
			}
		}

		// structuredClone of a value typed only at run time is a TypeScript
		// module's.
		if sc := lib.ModuleRoot + "internal_structured_clone.ts"; path != sc && idents["structuredClone"] && !declaresTopLevel(prog, "structuredClone") {
			importTargets[sc] = true
			if err := visit(sc, false); err != nil {
				return err
			}
		}

		// A ReadableStream's async iterator (values(), [Symbol.asyncIterator]()
		// and a `for await` over a stream held in `any`) is a TypeScript
		// module's.
		if rp := lib.ModuleRoot + "internal_rs_iterator.ts"; path != rp && !strings.HasPrefix(path, lib.ModuleRoot) && (idents["ReadableStream"] || idents["body"] || idents["Readable"]) && (idents["values"] || idents["asyncIterator"] || bytes.Contains(src, []byte("for await"))) {
			importTargets[rp] = true
			if err := visit(rp, false); err != nil {
				return err
			}
		}

		// A Promise combinator may be given an untyped iterable.
		if pc := lib.ModuleRoot + "internal_promise_combine.ts"; !strings.HasPrefix(path, lib.ModuleRoot) && idents["Promise"] && (idents["all"] || idents["any"] || idents["race"] || idents["allSettled"]) {
			importTargets[pc] = true
			if err := visit(pc, false); err != nil {
				return err
			}
		}

		// JSON.stringify with a replacer, a run-time space, or by value, and
		// JSON.parse with a reviver or by value, are a TypeScript module's.
		if jp := lib.ModuleRoot + "internal_json.ts"; path != jp && idents["JSON"] && (idents["stringify"] || idents["parse"]) && jsonAtRunTime(prog) {
			importTargets[jp] = true
			if err := visit(jp, false); err != nil {
				return err
			}
		}

		// Math, JSON, console, Reflect or Atomics read as a value are a
		// TypeScript module's objects.
		// The Web Crypto global as a value, or a method of it taken by value.
		if cp := lib.ModuleRoot + "internal_crypto_global.ts"; path != cp && !strings.HasPrefix(path, lib.ModuleRoot) && idents["crypto"] && cryptoValueUse(src) {
			importTargets[cp] = true
			if err := visit(cp, false); err != nil {
				return err
			}
		}

		globalized := globalizePatternTargets(path, src, prog)
		if !strings.HasPrefix(path, lib.ModuleRoot) {
			uses := namespaceValueUses(src, idents)
			if globalized || boundGlobal || idents["globalThis"] && globalObjectUse(src) || ctorFunctionCall(src) || undeclaredAssign(path, src, prog) || sloppyThisUse(path, src, prog) {
				if uses == nil {
					uses = map[string]bool{}
				}
				uses["globalThis"] = true // the global object
			}
			if uses["globalThis"] {
				if globalObjectEscapes_ {
					fp := lib.ModuleRoot + "internal_global_object_fill.ts"
					importTargets[fp] = true
					if err := visit(fp, false); err != nil {
						return err
					}
				} else {
					if globalObjectSpelled["structuredClone"] {
						fp := lib.ModuleRoot + "internal_global_clone.ts"
						importTargets[fp] = true
						if err := visit(fp, false); err != nil {
							return err
						}
					}
					// A namespace the program may read off the object by name.
					for n := range globalObjectSpelled {
						if _, ok := NamespaceValueNames[n]; ok && n != "globalThis" {
							uses[n] = true
						}
					}
				}
			}
			if uses["globalThis"] && globalObjectNames["fetch"] {
				fp := lib.ModuleRoot + "internal_global_fetch.ts"
				importTargets[fp] = true
				if err := visit(fp, false); err != nil {
					return err
				}
			}
			// process on the global object is its emitter object, as a
			// read of `process` as a value is.
			if uses["globalThis"] && globalObjectNames["process"] {
				pp := lib.ModuleRoot + "internal_process.ts"
				importTargets[pp] = true
				if err := visit(pp, false); err != nil {
					return err
				}
			}
			names := make([]string, 0, len(uses))
			for n := range uses {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				np := namespaceModule(n)
				importTargets[np] = true
				if err := visit(np, false); err != nil {
					return err
				}
			}
		}

		// console.dir with options known only at run time inspects through
		// util's inspect.
		if ip := lib.ModuleRoot + "internal_util_inspect.ts"; path != ip && idents["dir"] && consoleDirAtRunTime(prog) {
			importTargets[ip] = true
			if err := visit(ip, false); err != nil {
				return err
			}
		}

		// console.countReset of an unknown label is a process warning.
		if wp := lib.ModuleRoot + "internal_process_warning.ts"; path != wp && idents["countReset"] && !strings.HasPrefix(path, lib.ModuleRoot) {
			importTargets[wp] = true
			if err := visit(wp, false); err != nil {
				return err
			}
		}

		// TDD-00235: `process.stdin`/`stdout`/`stderr` are the stdio
		// module's streams.
		if sp := lib.ModuleRoot + "internal_process_stdio.ts"; path != sp && idents["process"] && (idents["stdin"] || idents["stdout"] || idents["stderr"]) {
			importTargets[sp] = true
			if err := visit(sp, false); err != nil {
				return err
			}
		}

		// A builtin global's member its declaration links to a module's
		// export (`@lower module:export`) is that module's; `process` as a
		// value and process.env are internal_process.ts's and
		// internal_process_env.ts's.
		for _, mod := range loweredMemberModules(prog, path, src, idents) {
			importTargets[mod] = true
			if err := visit(mod, false); err != nil {
				return err
			}
		}

		dir := filepath.Dir(path)
		for _, stmt := range prog.Body {
			// TDD-00051: a re-export (ExportFromDeclaration.Source) is a
			// dependency edge exactly like an import's — it must be visited
			// and land in `order` before this file, since resolving it
			// later needs the target's exported/mangled tables already
			// final. Re-exporting from a built-in module is out of scope
			// (see the TDD's Design section), rejected here rather than
			// treated as "nothing to visit" the way a virtual import is.
			var source string
			switch s := stmt.(type) {
			case *ast.ImportDeclaration:
				source = s.Source
			case *ast.ExportFromDeclaration:
				if s.Source == "" {
					continue // a local export list: no other file
				}
				if _, isVirtual := virtualBuiltinMarkers[s.Source]; isVirtual {
					return fmt.Errorf("%d:%d: re-exporting from a built-in module ('%s') is not supported",
						s.GetPos().Line, s.GetPos().Col, s.Source)
				}
				source = s.Source
			default:
				continue
			}
			if _, isVirtual := virtualBuiltinMarkers[source]; isVirtual {
				// TDD-00049: a built-in module, never a real file to visit —
				// but its part written in TypeScript is one (TDD-00231).
				if cp, ok := lib.CompanionPath(source); ok && cp != path {
					importTargets[cp] = true
					if err := visit(cp, false); err != nil {
						return err
					}
				}
				continue
			}
			resolved, err := resolveImportPath(dir, source, klainModulesDir)
			if err != nil {
				return fmt.Errorf("%d:%d: %w", stmt.GetPos().Line, stmt.GetPos().Col, err)
			}
			importTargets[resolved] = true
			userEdges[path] = append(userEdges[path], resolved)
			if err := visit(resolved, false); err != nil {
				return err
			}
		}

		// TDD-00098: visit each `new Worker('...')` target as a dependency.
		for _, wp := range prog.WorkerPaths {
			resolved, found, err := resolveTsFile(dir, wp)
			if err != nil {
				return fmt.Errorf("%s: resolving worker module '%s': %w", path, wp, err)
			}
			if !found {
				return fmt.Errorf("%s: cannot find worker module '%s' (resolved to %s)", path, wp, resolved)
			}
			workerTargets[resolved] = true
			if err := visit(resolved, false); err != nil {
				return err
			}
		}
		for _, code := range prog.WorkerEvals {
			sum := sha256.Sum256([]byte(code))
			vpath := filepath.Join(dir, fmt.Sprintf("[worker eval %x].js", sum[:6]))
			evalSources[vpath] = []byte(code)
			evalKeys[vpath] = code
			workerTargets[vpath] = true
			if err := visit(vpath, false); err != nil {
				return err
			}
		}

		// Each dynamic `import('...')` literal target: merged over a dynamic
		// edge (bundled, lazy), or only discovered and validated (isolated,
		// where it becomes its own program).
		if !strings.HasPrefix(path, lib.ModuleRoot) {
			prog.DynamicImportNodes = append(prog.DynamicImportNodes, computedImportAlternatives(prog, path, dir, klainModulesDir)...)
		}
		for _, node := range prog.DynamicImportNodes {
			lit, ok := node.Specifier.(*ast.StringLiteral)
			if !ok {
				continue
			}
			dp := lit.Value
			_, isVirtual := virtualBuiltinMarkers[strings.TrimPrefix(dp, "node:")]
			if _, isLib := lib.ModulePath(dp); isLib {
				isVirtual = true // a builtin module written in TypeScript
			}
			if isVirtual {
				return fmt.Errorf("%s: dynamic import('%s') of a built-in module is not supported", path, dp)
			}
			resolved, err := resolveImportPath(dir, dp, klainModulesDir)
			if err != nil {
				return fmt.Errorf("%s: resolving dynamic import('%s'): %w", path, dp, err)
			}
			node.ResolvedPath = resolved // annotate for codegen (module key + dlopen path)
			dynamicImportTargets[resolved] = true
			// The target's annotated value exports shape the typed result
			// object at the loading call site.
			exps, err := parseIslandExports(resolved)
			if err != nil {
				return fmt.Errorf("%s: reading dynamic import('%s') exports: %w", path, dp, err)
			}
			node.Exports = exps
			if isolatedDynamicImport {
				// TDD-00056 isolated backend: the target is NOT merged — it
				// becomes its own program in a shared-library island, compiled
				// separately and loaded on first use, with its own instance of
				// every module it imports.
				continue
			}
			// Bundled and lazy (TDD-00238 Stage 5): the target is merged like
			// a static import, but over a dynamic edge — a file only such
			// edges reach runs its top-level on first import(), not at startup.
			dynEdges[path] = append(dynEdges[path], resolved)
			if err := visit(resolved, false); err != nil {
				return err
			}
		}

		files[path] = &fileInfo{path: path, src: string(src), prog: prog, isEntry: isEntry, exported: exportedNames(prog), typeExports: typeExportedNames(prog), index: files[path].index, selfWorker: selfWorker}
		if !isEntry {
			order = append(order, path)
		}
		return nil
	}

	if err := visit(entryAbs, true); err != nil {
		return nil, err
	}

	// TDD-00098: worker-entry conflict checks, once the whole graph is known.
	for wf := range workerTargets {
		// A file starting a worker of itself (`new Worker(__filename)`, the
		// entry's usual `isMainThread` split) is its own worker module.
		self := files[wf].selfWorker && onlySelfWorker(files[wf].prog, wf)
		if wf == entryAbs && !self {
			return nil, fmt.Errorf("%s: the program's own entry file cannot be used as a worker module", wf)
		}
	}

	// TDD-00052: a file's cyclic-ness can only be known once the full DFS
	// above has completed (a back-edge discovered much later, from a
	// completely different branch of the graph, can still mark an
	// already-finished file) — so this validation, unlike everything else
	// that used to run inline inside visit, has to be its own pass. Only
	// cyclic files are restricted; the entry file is never checked here
	// regardless of its own cyclic-ness (see validateCyclicFile's doc
	// comment for why that's still safe).
	for _, path := range order {
		if !cyclic[path] {
			continue
		}
		if err := validateCyclicFile(files[path].prog); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	allPaths := make([]string, 0, len(order)+1)
	allPaths = append(allPaths, order...)
	allPaths = append(allPaths, entryAbs)

	// The files only dynamic import() edges reach (bundled and lazy). Under
	// lazy each is mangled with its own key, so its definitions split into
	// the shared library loaded on first import() (TDD-00238 Stage 5).
	dynOnly := map[string]bool{}
	if len(dynEdges) > 0 {
		static := map[string]bool{entryAbs: true}
		var reach func(string)
		reach = func(p string) {
			for _, d := range userEdges[p] {
				if !static[d] {
					static[d] = true
					reach(d)
				}
			}
		}
		reach(entryAbs)
		for w := range workerTargets {
			static[w] = true
			reach(w)
		}
		for _, p := range order {
			if !static[p] && !strings.HasPrefix(p, lib.ModuleRoot) {
				dynOnly[p] = true
			}
		}
	}
	fileKey := func(path string) string {
		if dynOnly[path] && opts.DynamicImport == "lazy" {
			return ast.DynModuleKey(path)
		}
		return libKey(path)
	}

	// TDD-00051: fold every file's re-exports into its own `exported` set
	// before anything downstream consults that set. Must run in dependency
	// order (allPaths, same as everywhere else below) — a file's re-export
	// sources need their *final* exported set (including their own
	// re-exports) already computed. Only resolves names/existence here, not
	// mangled values — those aren't computed until after mangleFileDecls
	// runs below, see the second re-export pass further down.
	for _, path := range allPaths {
		info := files[path]
		dir := filepath.Dir(path)
		for _, stmt := range info.prog.Body {
			ef, ok := stmt.(*ast.ExportFromDeclaration)
			if !ok {
				continue
			}
			if ef.Source == "" {
				// A local export list forwards this file's own declarations.
				declared, types := map[string]bool{}, map[string]bool{}
				for _, st := range info.prog.Body {
					for _, ref := range declRefsOf(st) {
						declared[ref.Name] = true
						if ref.Kind == "interface" || ref.Kind == "type" {
							types[ref.Name] = true
						}
					}
				}
				for _, spec := range ef.Specifiers {
					if !declared[spec.Imported] {
						return nil, fmt.Errorf("%d:%d: cannot export '%s': it is not declared at the top level of this file",
							ef.GetPos().Line, ef.GetPos().Col, spec.Imported)
					}
					if info.exported[spec.Local] {
						return nil, fmt.Errorf("%d:%d: '%s' is already exported from this file",
							ef.GetPos().Line, ef.GetPos().Col, spec.Local)
					}
					info.exported[spec.Local] = true
					if spec.TypeOnly || types[spec.Imported] {
						info.typeExports[spec.Local] = true
					}
					info.reExportBindings = append(info.reExportBindings, reExportBinding{
						local: spec.Local, target: path, remote: spec.Imported,
					})
				}
				continue
			}
			resolved, err := resolveImportPath(dir, ef.Source, klainModulesDir)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: %w", ef.GetPos().Line, ef.GetPos().Col, err)
			}
			target := files[resolved]

			var specs []ast.ImportSpecifier
			if ef.All {
				for name := range target.exported {
					if name == "default" {
						continue // a star re-export never forwards a default, matching real ES modules
					}
					specs = append(specs, ast.ImportSpecifier{Imported: name, Local: name})
				}
			} else {
				specs = ef.Specifiers
			}

			for _, spec := range specs {
				if !target.exported[spec.Imported] {
					return nil, fmt.Errorf("%d:%d: '%s' has no exported member '%s'",
						ef.GetPos().Line, ef.GetPos().Col, ef.Source, spec.Imported)
				}
				if info.exported[spec.Local] {
					return nil, fmt.Errorf("%d:%d: '%s' is already exported from this file — use 'as' to re-export it under a different name",
						ef.GetPos().Line, ef.GetPos().Col, spec.Local)
				}
				info.exported[spec.Local] = true
				// A re-exported type is a type here too (not in the module
				// namespace object).
				if spec.TypeOnly || target.typeExports[spec.Imported] {
					info.typeExports[spec.Local] = true
				}
				info.reExportBindings = append(info.reExportBindings, reExportBinding{
					local: spec.Local, target: resolved, remote: spec.Imported,
				})
			}
		}
	}

	// Validate every import statement's specifiers against the file it resolves to.
	for _, info := range files {
		dir := filepath.Dir(info.path)
		for _, stmt := range info.prog.Body {
			imp, ok := stmt.(*ast.ImportDeclaration)
			if !ok {
				continue
			}
			if _, isVirtual := virtualBuiltinMarkers[imp.Source]; isVirtual {
				continue // TDD-00049: validated separately, once bindings are built below
			}
			resolved, err := resolveImportPath(dir, imp.Source, klainModulesDir)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: %w", imp.GetPos().Line, imp.GetPos().Col, err)
			}
			target := files[resolved]
			for _, spec := range imp.Specifiers {
				if !target.exported[spec.Imported] {
					return nil, fmt.Errorf("%d:%d: '%s' has no exported member '%s'",
						imp.GetPos().Line, imp.GetPos().Col, imp.Source, spec.Imported)
				}
			}
		}
	}

	// TDD-00041: give every file's own top-level declarations a file-private
	// mangled name (also catches genuine in-file duplicate declarations,
	// same as the old global check did for the same-file case). Must fully
	// complete for every file before any file's imports are rewritten below
	// — an importing file needs the *target's* mangled names already computed.
	var unused []*diag.Diagnostic
	for _, path := range allPaths {
		info := files[path]
		// Redeclaration early errors and the reads the flow graph proves wrong
		// (the temporal dead zone; definite assignment, a TypeScript rule that
		// plain JavaScript under -compat=js does not have), from the binder —
		// run pre-mangle so messages carry the original binding names.
		if compatJS {
			annexBFunctions(info.prog) // block functions' var bindings (B.3.3)
		}
		bound := binder.BindWith(info.prog, binder.Options{AnnexB: compatJS})
		offsets := diag.Offsets(info.src)
		ds := append(bound.Diagnostics(offsets), bound.FlowDiagnostics(offsets, !compatJS)...)
		ds = append(ds, reservedMemberDiags(info.prog, offsets)...)
		ds = append(ds, constructDiags(info.prog, offsets, opts.Decorators)...)
		if opts.Decorators == "standard" {
			ds = append(ds, paramDecoratorDiags(info.prog, offsets)...)
		}
		if len(ds) > 0 {
			sortDiags(ds)
			return nil, diag.Errors(append(unused, diag.As(diag.InFile(diag.Errors(ds), path))...))
		}
		// noUnusedLocals/noUnusedParameters read the file as written: its
		// imports, its exports and its own names, before the merge. Every
		// file is checked before they are reported, as tsc does.
		if (opts.NoUnusedLocals || opts.NoUnusedParameters) && !strings.HasPrefix(path, lib.ModuleRoot) {
			fileUnused := checker.Unused(bound, checker.UnusedOptions{
				Locals: opts.NoUnusedLocals, Parameters: opts.NoUnusedParameters,
				Script: !IsModule(info.prog), Exported: info.exported,
			})
			sortDiags(fileUnused)
			for _, d := range fileUnused {
				d.Span.File = path
				d.Span.Start = offsets(d.Span.Pos.Line, d.Span.Pos.Col)
				d.Span.End = d.Span.Start
			}
			unused = append(unused, fileUnused...)
		}
		if compatJS {
			// (A TypeScript file's checker sees each declaration as written.)
			hoistNestedTopLevelVars(info.prog)
		}
		mangled, err := mangleFileDecls(path, fileKey(path), info.prog, info.index)
		if err != nil {
			return nil, err
		}
		info.mangled = mangled
	}
	if len(unused) > 0 {
		return nil, diag.Errors(unused)
	}

	// TDD-00051: now that every file's own `mangled` map is final, resolve
	// each re-export binding recorded above into an actual mangled name.
	// Dependency order (allPaths) again guarantees a binding's target file
	// was fully processed — including its own reExportMangled, so re-export
	// chains of arbitrary depth resolve transitively for free.
	for _, path := range allPaths {
		info := files[path]
		if len(info.reExportBindings) == 0 {
			continue
		}
		info.reExportMangled = make(map[string]string, len(info.reExportBindings))
		for _, b := range info.reExportBindings {
			m, _ := files[b.target].publicMangled(b.remote)
			info.reExportMangled[b.local] = m
		}
	}

	// Build each file's own combined lookup table (its own mangled decls
	// plus its import bindings, honoring `as` aliasing, plus TDD-00042's
	// namespace-import member tables) and rewrite every reference in that
	// file accordingly.
	resolvedModules := map[*ast.StringLiteral]string{}
	builtinImports := map[string]bool{}
	builtinImportRefs := map[string]ast.BuiltinImportRef{}
	dynNamespaces := map[string]string{} // a merged import() target: its namespace object's name
	declaredNsObjects := map[string]bool{}
	for _, path := range allPaths {
		info := files[path]
		ownLive := mutableExports(info.prog)
		lookup := make(map[string]string, len(info.mangled))
		for orig, m := range info.mangled {
			lookup[orig] = m
		}
		ns := map[string]map[string]string{}
		nsSource := map[string]string{}            // a namespace alias's module specifier
		nsValues := map[string]map[string]string{} // its value members (the namespace object's)
		nsLive := map[string]map[string]bool{}     // those that are `let`/`var` bindings
		nsObjects := map[string]string{}           // a namespace read as a value: its object's name
		nsShared := map[string]string{}            // the one namespace object of its module
		nsPos := map[string]ast.Pos{}              // its import's position
		builtinMembers := map[string]builtinMemberRef{}
		// TDD-00165 Stage 3: aliases of a parse-time built-in constructor
		// (`import { URL as U } from 'url'`) — the rename pass rebuilds `new U(...)`
		// into the specialized built-in node under the canonical name.
		parseTimeAliases := map[string]string{}
		dir := filepath.Dir(path)
		for _, stmt := range info.prog.Body {
			imp, ok := stmt.(*ast.ImportDeclaration)
			if !ok {
				continue
			}
			_, isReexport := globalReexportModules[imp.Source]
			if _, isVirtual := virtualBuiltinMarkers[imp.Source]; isVirtual || isReexport {
				// The merged program drops import statements: record the names
				// a builtin module's import binds, for the checker.
				for _, spec := range imp.Specifiers {
					if companionExports(imp.Source)[spec.Imported] {
						continue // declared by the module's TypeScript companion
					}
					builtinImports[spec.Local] = true
					mod, name := strings.TrimPrefix(imp.Source, "node:"), spec.Imported
					builtinImportRefs[spec.Local] = ast.BuiltinImportRef{Module: mod, Name: name}
					if parseTimeReexports[spec.Imported] && spec.Local != spec.Imported {
						// The rename pass rebuilds `new U()` under the
						// canonical name (`URL`), which is then the name in
						// the program.
						builtinImports[spec.Imported] = true
						builtinImportRefs[spec.Imported] = ast.BuiltinImportRef{Module: mod, Name: name}
					}
				}
				if imp.Namespace != "" {
					builtinImports[imp.Namespace] = true
				}
			}
			if reexports, isReexport := globalReexportModules[imp.Source]; isReexport {
				// TDD-00165: a Web-global-backed module (`url`/`timers`/`perf_hooks`/
				// `buffer`/`events`). Its *primary* exports are spec-identical
				// re-exports of an ambient global (erased/renamed to the global,
				// Stages 1–3); a hybrid module (`url`) also has *module-only function*
				// members (`url.parse`) that dispatch via the module marker like
				// `path.join` (Stage 4). Import statements are dropped from the
				// merged program regardless (see below), so "erase" here means simply
				// not recording a local binding.
				marker := virtualBuiltinMarkers[imp.Source]
				funcs := moduleFunctionMembers[imp.Source]
				// The module's part written in TypeScript (`url`'s legacy
				// API): its exports resolve to the companion's declarations.
				companion := companionMangled(files, imp.Source)
				bindNS := func(local string) {
					if len(funcs) > 0 {
						lookup[local] = marker // its functions dispatch through the marker
					}
					// Through the namespace, the companion's exports are its
					// declarations and the primary exports are the globals
					// (`buffer.Buffer` is `Buffer`).
					members := map[string]string{}
					values := map[string]string{}
					for k, v := range companion {
						members[k] = v
						if cp, ok := lib.CompanionPath(imp.Source); ok && files[cp] != nil && !files[cp].typeExports[k] {
							values[k] = v
						}
					}
					for r := range reexports {
						members[r] = r
						values[r] = r
					}
					ns[marker] = members
					ns[local] = members
					if len(funcs) == 0 {
						// Read as a value: its namespace object.
						nsSource[local] = imp.Source
						nsValues[local] = values
						nsPos[local] = imp.GetPos()
					}
				}
				dupCheck := func(local string) error {
					if _, dup := lookup[local]; dup {
						return fmt.Errorf("%d:%d: '%s' is already declared in this file", imp.GetPos().Line, imp.GetPos().Col, local)
					}
					if _, dup := builtinMembers[local]; dup {
						return fmt.Errorf("%d:%d: '%s' is already declared in this file", imp.GetPos().Line, imp.GetPos().Col, local)
					}
					if _, dup := ns[local]; dup {
						return fmt.Errorf("%d:%d: '%s' is already declared in this file", imp.GetPos().Line, imp.GetPos().Col, local)
					}
					return nil
				}
				if imp.Namespace != "" {
					// A namespace import (`import * as url from 'url'`) is only
					// meaningful when the module has marker-dispatched function members
					// — it binds the marker so `url.parse(…)` resolves. Reexport
					// primaries aren't reachable this way (they're globals, not marker
					// members); a module with no function members keeps the Stage 1
					// rejection.
					if len(funcs) == 0 && companion == nil {
						var example string
						for m := range reexports {
							if example == "" || m < example {
								example = m
							}
						}
						return nil, fmt.Errorf("%d:%d: a namespace import of '%s' is not yet supported — import its named export (e.g. `import { %s } from '%s'`) or use the global directly (TDD-00165)",
							imp.GetPos().Line, imp.GetPos().Col, imp.Source, example, imp.Source)
					}
					if err := dupCheck(imp.Namespace); err != nil {
						return nil, err
					}
					bindNS(imp.Namespace)
					continue
				}
				for _, spec := range imp.Specifiers {
					if spec.Imported == "default" {
						if len(funcs) > 0 || companion != nil {
							// A module-namespace default (`import url from 'url'`): bind
							// the marker so `url.parse(…)` dispatches, like a namespace.
							if err := dupCheck(spec.Local); err != nil {
								return nil, err
							}
							bindNS(spec.Local)
							continue
						} else {
							return nil, fmt.Errorf("%d:%d: built-in module '%s' has no default export — import its named export (e.g. `import { %s } from '%s'`)",
								imp.GetPos().Line, imp.GetPos().Col, imp.Source, firstKey(reexports), imp.Source)
						}
					}
					want := spec.Imported
					if err := dupCheck(spec.Local); err != nil {
						return nil, err
					}
					if m, ok := companion[want]; ok {
						lookup[spec.Local] = m
						continue
					}
					if funcs[want] {
						// Stage 4: a module-only function member — dispatch via the
						// marker, exactly like a named `path.join` import.
						builtinMembers[spec.Local] = builtinMemberRef{Marker: marker, Member: want}
						continue
					}
					if !reexports[want] {
						return nil, fmt.Errorf("%d:%d: built-in module '%s' has no exported member '%s'",
							imp.GetPos().Line, imp.GetPos().Col, imp.Source, spec.Imported)
					}
					if spec.Local == want {
						// Stage 1: the local IS the global name — erase (bind nothing);
						// the ambient global keeps resolving as before.
						continue
					}
					// Aliased import (local name differs from the global's).
					if parseTimeReexports[want] {
						// Stage 3: a parse-time constructor (`URL`/`URLSearchParams`/
						// `Blob`). The parser produced a generic
						// `new <alias>(...)`; record the alias so the rename pass
						// rebuilds it as the specialized built-in node under the
						// canonical name.
						parseTimeAliases[spec.Local] = want
						continue
					}
					// Stage 2: a codegen-recognized global (`setTimeout`/`performance`/
					// `atob`/…) — rename the alias to the canonical global name via the
					// scope-aware rename pass, so `later(...)` compiles as the global
					// `setTimeout(...)`. This is the same lookup-table mechanism a named
					// module-member import uses; the value is a real global name, not a
					// marker, so it reaches codegen as the bare global.
					lookup[spec.Local] = want
				}
				continue
			}
			if marker, isVirtual := virtualBuiltinMarkers[imp.Source]; isVirtual {
				// TDD-00049 Stage 2: a named specifier (anything but the
				// synthetic "default" one a default import produces, see
				// ImportSpecifier's own doc comment) names one member of
				// the built-in module directly — validated against that
				// module's real member table (virtualModuleMembers), the
				// same "does the target actually export this" check a real
				// file import already gets against its own exportedNames.
				members := virtualModuleMembers[imp.Source]
				// The module's part written in TypeScript (TDD-00231): its
				// exports resolve to the companion's declarations, by name
				// or through the module's namespace.
				companion := companionMangled(files, imp.Source)
				for _, spec := range imp.Specifiers {
					if spec.Imported == "default" {
						continue // handled by virtualImportLocal below
					}
					if m, ok := companion[spec.Imported]; ok {
						if _, dup := lookup[spec.Local]; dup {
							return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
								imp.GetPos().Line, imp.GetPos().Col, spec.Local)
						}
						lookup[spec.Local] = m
						continue
					}
					if !members[spec.Imported] {
						return nil, fmt.Errorf("%d:%d: built-in module '%s' has no exported member '%s'",
							imp.GetPos().Line, imp.GetPos().Col, imp.Source, spec.Imported)
					}
					if _, dup := lookup[spec.Local]; dup {
						return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
							imp.GetPos().Line, imp.GetPos().Col, spec.Local)
					}
					if _, dup := builtinMembers[spec.Local]; dup {
						return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
							imp.GetPos().Line, imp.GetPos().Col, spec.Local)
					}
					builtinMembers[spec.Local] = builtinMemberRef{Marker: marker, Member: spec.Imported}
				}
				local, hasLocal := virtualImportLocal(imp)
				if !hasLocal {
					continue // bare `import 'fs'` side-effect form, or named-only with no default/namespace binding — nothing more to bind
				}
				if _, dup := lookup[local]; dup {
					return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
						imp.GetPos().Line, imp.GetPos().Col, local)
				}
				if _, dup := ns[local]; dup {
					return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
						imp.GetPos().Line, imp.GetPos().Col, local)
				}
				lookup[local] = marker
				if companion != nil {
					// `fs.X` is renamed to the marker before its member is
					// looked up; `new fs.X`, `extends fs.X` and `fs.X` as a
					// type are looked up by the local name.
					ns[marker] = companion
					ns[local] = companion
				}
				continue
			}
			resolved, err := resolveImportPath(dir, imp.Source, klainModulesDir)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: %w", imp.GetPos().Line, imp.GetPos().Col, err)
			}
			target := files[resolved]
			for _, spec := range imp.Specifiers {
				if _, dup := lookup[spec.Local]; dup {
					return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
						imp.GetPos().Line, imp.GetPos().Col, spec.Local)
				}
				if _, dup := ns[spec.Local]; dup {
					return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
						imp.GetPos().Line, imp.GetPos().Col, spec.Local)
				}
				m, _ := target.publicMangled(spec.Imported)
				lookup[spec.Local] = m
			}
			if imp.Namespace != "" {
				if _, dup := lookup[imp.Namespace]; dup {
					return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file",
						imp.GetPos().Line, imp.GetPos().Col, imp.Namespace)
				}
				if _, dup := ns[imp.Namespace]; dup {
					return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file",
						imp.GetPos().Line, imp.GetPos().Col, imp.Namespace)
				}
				// Only the target's actually-exported members are reachable
				// through the namespace object — same visibility rule a
				// named `import { x }` is already held to.
				members := make(map[string]string, len(target.exported))
				values := map[string]string{}
				nsLive[imp.Namespace] = mutableExports(target.prog)
				for name := range target.exported {
					members[name], _ = target.publicMangled(name)
					if !target.typeExports[name] {
						values[name] = members[name]
					}
				}
				ns[imp.Namespace] = members
				nsSource[imp.Namespace] = imp.Source
				nsShared[imp.Namespace] = fmt.Sprintf("__kml_nsobj%d", target.index)
				nsValues[imp.Namespace] = values
				nsPos[imp.Namespace] = imp.GetPos()
			}
		}
		var reservedErr error
		renameFile(info.prog, lookupTable{
			compatJS: compatJS,
			resolved: resolvedModules,
			names:    lookup, ns: ns, nsSource: nsSource, builtinMembers: builtinMembers,
			nsObjects: nsObjects, nsObjectSuffix: fmt.Sprintf("__kml_nsv%d", info.index), nsShared: nsShared,
			typeOnly:         typeOnlyLocals(info.prog),
			nodeTypes:        info.prog.NodeTypeImports,
			parseTimeAliases: parseTimeAliases,
			reservedErr:      &reservedErr,
			filePath:         path, workerLits: stringSet(info.prog.WorkerPaths),
		})
		if reservedErr != nil {
			return nil, reservedErr
		}
		if !strings.HasPrefix(path, lib.ModuleRoot) {
			// After the file's names are renamed: a Function body sees the
			// global scope, never the file's (a module's) own bindings.
			rewriteFunctionConstructor(info.prog, info.index)
			rewriteIndirectEval(info.prog, info.index)
			rewriteObjectLiteralSuper(info.prog, info.index, compatJS)
			if compatJS {
				optionalDeletedFields(info.prog)
			}
		}
		prependNamespaceObjects(info.prog, nsObjects, nsValues, nsLive, nsPos, declaredNsObjects)
		if dynamicImportTargets[path] && !isolatedDynamicImport {
			// What its import() resolves to: its namespace object, built
			// once its top-level has run.
			vals := map[string]string{}
			keys := []string{}
			for name := range info.exported {
				if !info.typeExports[name] {
					vals[name], _ = info.publicMangled(name)
					keys = append(keys, name)
				}
			}
			sort.Strings(keys)
			name := fmt.Sprintf("__kml_dynns%d", info.index)
			info.prog.Body = append(info.prog.Body, ast.NewVarDeclaration("const", name, nil, namespaceObject(keys, vals, ownLive, ast.Pos{}), ast.Pos{}))
			dynNamespaces[path] = name
		}
		if info.selfWorker && workerTargets[path] {
			// The worker's copy: the same source, parsed and renamed as the
			// program's, so its statements read the same mangled names.
			cp, err := parser.Parse(info.src)
			if err != nil {
				return nil, diag.InFile(err, path)
			}
			defaultImportsAsNamespaces(cp)
			markAssertCallSites(cp)
			rewriteSelfWorker(cp, path)
			cp.WorkerPaths = workerPaths(cp)
			eraseBuiltinTypeImports(cp)
			if _, err := mangleFileDecls(path, fileKey(path), cp, info.index); err != nil {
				return nil, err
			}
			renameFile(cp, lookupTable{
				compatJS: compatJS,
				resolved: resolvedModules,
				names:    lookup, ns: ns, nsSource: nsSource, builtinMembers: builtinMembers,
				nsObjects: nsObjects, nsObjectSuffix: fmt.Sprintf("__kml_nsv%d", info.index),
				typeOnly:         typeOnlyLocals(cp),
				nodeTypes:        info.prog.NodeTypeImports,
				parseTimeAliases: parseTimeAliases,
				reservedErr:      &reservedErr,
				filePath:         path, workerLits: stringSet(cp.WorkerPaths),
			})
			if reservedErr != nil {
				return nil, reservedErr
			}
			prependNamespaceObjects(cp, nsObjects, nsValues, nsLive, nsPos, map[string]bool{})
			info.workerCopy = cp
		}
	}

	// Defensive: mangled names are constructed to already be unique (each
	// file's own suffix), but assert it directly rather than trusting the
	// scheme blindly — this should never actually fire.
	type declSite struct {
		path string
		kind string
	}
	declaredIn := map[string]declSite{}
	for _, path := range allPaths {
		for _, stmt := range files[path].prog.Body {
			for _, ref := range declRefsOf(stmt) {
				if prev, dup := declaredIn[ref.Name]; dup {
					// A repeated `var`/`function` in one file legitimately maps
					// two source declarations to the same mangled name (see
					// mangleFileDecls) — that's the one expected duplicate, not
					// a mangling-scheme failure. Any cross-file or lexical-kind
					// duplicate still means the invariant broke.
					if prev.path == path && isVarOrFuncKind(prev.kind) && isVarOrFuncKind(ref.Kind) {
						continue
					}
					// A same-file class+interface merge pair shares one
					// mangled name by design (ADR-00466).
					if prev.path == path && isClassIfaceMergePair(prev.kind, ref.Kind) {
						continue
					}
					// A type and a value of one name (`interface E` and
					// `function E`) live in TypeScript's separate type and
					// value spaces.
					if prev.path == path && isTypeValuePair(prev.kind, ref.Kind) {
						continue
					}
					// Namespace type members desugar to bare top-level names
					// (ADR-00450), and the binder keeps each namespace's own
					// scope: two namespaces declaring one type name meet here.
					if prev.path == path && isTypeMemberKind(prev.kind) && isTypeMemberKind(ref.Kind) {
						return nil, diag.InFile(diag.New(diag.NamespaceTypeMemberClash, diag.Span{}, ast.Unmangle(ref.Name)), path)
					}
					return nil, fmt.Errorf("internal error: mangled name '%s' collided between %s and %s", ref.Name, prev.path, path)
				}
				declaredIn[ref.Name] = declSite{path: path, kind: ref.Kind}
			}
		}
	}

	// Merge: every non-entry file's declarations, then the entry file's own
	// full statement list — dropping ImportDeclaration and unwrapping
	// ExportDeclaration everywhere, since codegen/llvm knows neither node.
	markers := make(map[string]string, len(virtualBuiltinMarkers))
	for spec, marker := range virtualBuiltinMarkers {
		if _, dup := markers[marker]; !dup || !strings.HasPrefix(spec, "node:") {
			markers[marker] = spec
		}
	}
	merged := &ast.Program{ResolvedModules: resolvedModules, BuiltinImports: builtinImports, BuiltinImportRefs: builtinImportRefs, BuiltinMarkers: markers}
	merged.UsesDynamicImport = len(dynamicImportTargets) > 0
	if ei, ok := files[entryAbs]; ok {
		merged.EntryIsModule = HasModuleSyntax(ei.prog)
	}
	merged.EntryPath = entryAbs
	merged.GlobalObjectNames = globalObjectNames
	merged.GlobalObjectEscapes, merged.GlobalObjectSpelled = globalObjectEscapes_, globalObjectSpelled
	// Each builtin module written in TypeScript: its top-level names, for
	// code generation to call into the library (`stream:_kmlFromWeb`).
	for path, fi := range files {
		if !strings.HasPrefix(path, lib.ModuleRoot) {
			continue
		}
		mod := strings.TrimSuffix(strings.TrimPrefix(path, lib.ModuleRoot), ".ts")
		for n, m := range fi.mangled {
			if merged.LibExports == nil {
				merged.LibExports = map[string]string{}
			}
			merged.LibExports[mod+":"+n] = m
		}
	}
	if ei, ok := files[entryAbs]; ok {
		m := make(map[string]string, len(ei.exported))
		for n := range ei.exported {
			if mangled, ok := ei.publicMangled(n); ok {
				m[n] = mangled
			} else {
				m[n] = n
			}
		}
		merged.EntryExportMangled = m
	}
	if isolatedDynamicImport {
		roots := make([]string, 0, len(dynamicImportTargets))
		for p := range dynamicImportTargets {
			roots = append(roots, p)
		}
		sort.Strings(roots)
		merged.IslandRoots = roots
	}
	mergeNamespaces := func(src *ast.Program) {
		merged.NSAliases = append(merged.NSAliases, src.NSAliases...)
		merged.AmbientNames = append(merged.AmbientNames, src.AmbientNames...)
		for n := range src.TypeParamNames {
			if merged.TypeParamNames == nil {
				merged.TypeParamNames = map[string]bool{}
			}
			merged.TypeParamNames[n] = true
		}
		for local, mod := range src.NodeTypeImports {
			if merged.NodeTypeImports == nil {
				merged.NodeTypeImports = map[string]string{}
			}
			merged.NodeTypeImports[local] = mod
		}
		merged.NamespaceGroups = append(merged.NamespaceGroups, src.NamespaceGroups...)
		for ns, xs := range src.NamespaceExports {
			if merged.NamespaceExports == nil {
				merged.NamespaceExports = map[string][]ast.NamespaceExport{}
			}
			merged.NamespaceExports[ns] = append(merged.NamespaceExports[ns], xs...)
		}
		for n, ta := range src.ThisParams {
			if merged.ThisParams == nil {
				merged.ThisParams = map[ast.Node]ast.TypeNode{}
			}
			merged.ThisParams[n] = ta
		}
		for e, as := range src.Assertions {
			if merged.Assertions == nil {
				merged.Assertions = map[ast.Expression]ast.Assertion{}
			}
			merged.Assertions[e] = as
		}
		for ns, members := range src.Namespaces {
			if merged.Namespaces == nil {
				merged.Namespaces = map[string]map[string]bool{}
			}
			if merged.Namespaces[ns] == nil {
				merged.Namespaces[ns] = map[string]bool{}
			}
			for m, exported := range members {
				// The value is the member's exportedness (TDD-00148) — carry
				// it through; a duplicate across files keeps "exported" if
				// either side exports it.
				merged.Namespaces[ns][m] = merged.Namespaces[ns][m] || exported
			}
		}
	}
	for _, path := range order {
		if files[path].workerCopy != nil {
			// A file that is also its own worker: the program runs it, and
			// the worker runs its copy's executable statements.
			_, body := splitWorkerBody(unwrap(files[path].workerCopy.Body))
			merged.WorkerModules = append(merged.WorkerModules, ast.WorkerModule{Path: path, Body: body})
			merged.Body = appendModule(merged, unwrap(files[path].prog.Body))
			mergeNamespaces(files[path].prog)
			continue
		}
		if workerTargets[path] {
			// TDD-00098: a worker module's function/class/interface/type/enum
			// declarations are hoisted into the shared program body (they're
			// pure definitions — emitted as functions, callable from any
			// thread), while its var declarations and executable statements
			// become the worker's entry-function body. A worker module the
			// program also imports runs at import time too, as in Node.
			decls, body := splitWorkerBody(unwrap(files[path].prog.Body))
			if importTargets[path] {
				merged.Body = appendModule(merged, unwrap(files[path].prog.Body))
			} else {
				merged.Body = append(merged.Body, decls...)
			}
			key := path
			if code, isEval := evalKeys[path]; isEval {
				key = code // the runtime spawns it by its code
			}
			merged.WorkerModules = append(merged.WorkerModules, ast.WorkerModule{Path: key, Body: body})
			mergeNamespaces(files[path].prog)
			continue
		}
		merged.Body = appendModule(merged, unwrap(files[path].prog.Body))
		mergeNamespaces(files[path].prog)
	}
	merged.Body = appendModule(merged, unwrap(files[entryAbs].prog.Body))
	mergeNamespaces(files[entryAbs].prog)
	for _, f := range files {
		for e := range f.prog.EvalCode {
			if merged.EvalCode == nil {
				merged.EvalCode = map[ast.Expression]bool{}
			}
			merged.EvalCode[e] = true
		}
	}
	if cp := files[entryAbs].workerCopy; cp != nil {
		// The entry is also its own worker (`new Worker(__filename)`).
		_, body := splitWorkerBody(unwrap(cp.Body))
		merged.WorkerModules = append(merged.WorkerModules, ast.WorkerModule{Path: entryAbs, Body: body})
	}
	// A worker is its own isolate, as in Node: on its thread it evaluates
	// every builtin module and the user modules it imports afresh, before
	// its own statements. Deps holds those modules' executable top-level
	// statements, in dependency order.
	for i := range merged.WorkerModules {
		wm := &merged.WorkerModules[i]
		reach := map[string]bool{}
		var walk func(string)
		walk = func(p string) {
			for _, d := range userEdges[p] {
				if !reach[d] {
					reach[d] = true
					walk(d)
				}
			}
		}
		walk(wm.Path)
		for _, path := range order {
			if path == wm.Path || !(reach[path] || strings.HasPrefix(path, lib.ModuleRoot)) {
				continue
			}
			_, body := splitWorkerBody(unwrap(files[path].prog.Body))
			wm.Deps = append(wm.Deps, body...)
		}
	}
	// Each top-level statement's file, for the checker's diagnostics.
	stmtFile := map[ast.Statement]string{}
	for path, f := range files {
		for _, st := range unwrap(f.prog.Body) {
			stmtFile[st] = path
		}
	}
	merged.StmtFiles = stmtFile
	// The files Node runs as ES modules: those with module syntax, which
	// its detection of an untyped file goes by.
	merged.ESMFiles = map[string]bool{}
	for path, f := range files {
		if ast.HasModuleSyntax(f.prog.Body) {
			merged.ESMFiles[path] = true
		}
	}
	for _, f := range files {
		if fileImportsKlainSync(f.prog) {
			merged.UsesKlainSync = true
			break
		}
	}
	// Each merged target's exports, by their program names.
	for _, f := range files {
		for _, node := range f.prog.DynamicImportNodes {
			tf, ok := files[node.ResolvedPath]
			if !ok || isolatedDynamicImport {
				continue
			}
			node.Namespace = dynNamespaces[node.ResolvedPath]
			for i := range node.Exports {
				if m, ok := tf.publicMangled(node.Exports[i].Name); ok {
					node.Exports[i].Mangled = m
				} else {
					node.Exports[i].Mangled = node.Exports[i].Name
				}
			}
		}
	}
	// Files only dynamic import() edges reach run their top-level on first
	// import(), from the target's init (TDD-00238 Stage 5), not at startup.
	if len(dynOnly) > 0 {
		for st, path := range stmtFile {
			if dynOnly[path] {
				if merged.DynStatements == nil {
					merged.DynStatements = map[ast.Statement]string{}
				}
				merged.DynStatements[st] = path
			}
		}
		// Each target's dynamic-only files, dependencies first.
		merged.DynModules = map[string][]string{}
		for target := range dynamicImportTargets {
			in := map[string]bool{}
			var walk func(string)
			walk = func(p string) {
				if in[p] || !dynOnly[p] {
					return
				}
				in[p] = true
				for _, d := range userEdges[p] {
					walk(d)
				}
			}
			walk(target)
			var files []string
			for _, p := range order {
				if in[p] {
					files = append(files, p)
				}
			}
			merged.DynModules[target] = files
		}
	}
	for st, path := range stmtFile {
		if strings.HasPrefix(path, lib.ModuleRoot) {
			if merged.LibStatements == nil {
				merged.LibStatements = map[ast.Statement]string{}
			}
			merged.LibStatements[st] = libKey(path)
		}
	}
	// Every class of an included global module links (a program naming
	// AbortController reaches AbortSignal through it).
	for name, gpath := range lib.GlobalModuleNames() {
		f, included := files[gpath]
		if !included {
			continue
		}
		if m, ok := f.mangled[name]; ok {
			if merged.GlobalLinks == nil {
				merged.GlobalLinks = map[string]string{}
			}
			merged.GlobalLinks[name] = m
		}
	}
	for path, f := range files {
		if !strings.HasPrefix(path, lib.ModuleRoot) {
			continue
		}
		if merged.LibDeclNames == nil {
			merged.LibDeclNames = map[string]bool{}
		}
		for name := range f.exported {
			if m, ok := f.publicMangled(name); ok {
				merged.LibDeclNames[m] = true
			}
		}
		for _, name := range callableMarkers(f.src) {
			if m, ok := f.mangled[name]; ok {
				if merged.CallableClasses == nil {
					merged.CallableClasses = map[string]bool{}
				}
				merged.CallableClasses[m] = true
			}
		}
	}
	// A class member keyed at run time (`[a + b]() {}`) is defined on the
	// class once it is (lowerRuntimeClassKeys). TypeScript accepts such a
	// method or accessor, but not a field (TS1166), which the strict lane
	// leaves to its error.
	if err := ast.NameComputedMembers(merged); err != nil {
		return nil, err
	}
	scopePrivateNames(merged)
	if err := AnnotationChecks(merged, opts.MemMode); err != nil {
		return nil, err
	}
	lowerRuntimeClassKeys(merged, compatJS)
	ast.AddNamespaceObjects(merged)
	// The strict lane is TypeScript's: a program tsc rejects for a type
	// error is rejected here, with tsc's code (TDD-00230 P2.7). The js lane
	// compiles it as JavaScript.
	if !compatJS {
		entry := files[entryAbs].prog
		script := len(files) == 1 && !IsModule(entry)
		srcOf := func(path string) string {
			if f := files[path]; f != nil && !strings.HasPrefix(path, lib.ModuleRoot) {
				return f.src
			}
			return ""
		}
		if err := TypeCheck(merged, func(st ast.Statement) string { return stmtFile[st] }, srcOf, opts, script); err != nil {
			return nil, err
		}
	} else {
		// The js lane has no type errors, but this compiler's own limits
		// the checker finds are the same in both lanes.
		entry := files[entryAbs].prog
		script := len(files) == 1 && !IsModule(entry)
		if err := UnsupportedCheck(merged, func(st ast.Statement) string { return stmtFile[st] }, opts, script); err != nil {
			return nil, err
		}
	}
	// (After the type check: TypeScript sees the class as written.)
	lowerFunctionSubclasses(merged)
	lowerClassHeritageTDZ(merged)
	lowerLateArrowSuperCalls(merged)
	// (So is a computed import() specifier.)
	if err := lowerComputedImports(merged, func(st ast.Statement) string { return stmtFile[st] }); err != nil {
		return nil, err
	}
	return merged, nil
}

// TypeCheck runs the checker over a strict-lane program and returns its
// type errors, each attributed to the file fileOf names (nil when prog came
// from one source with no file), less those a comment directive in the
// file's source (srcOf) covers.
func TypeCheck(prog *ast.Program, fileOf func(ast.Statement) string, srcOf func(string) string, opts options.Options, script bool) error {
	if fileOf == nil {
		fileOf = func(ast.Statement) string { return "" }
	}
	// Members keyed by a const (`[kRefed]`, TDD-00246) are named before the
	// checker reads the class.
	if err := ast.NameComputedMembers(prog); err != nil {
		return err
	}
	if err := ast.RuntimeKeyError(prog); err != nil {
		return err
	}
	libProgs, err := lib.Programs()
	if err != nil {
		return fmt.Errorf("builtin declarations: %w", err)
	}
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Script: script, Lib: libProgs}), opts)
	ds := c.CheckFiles(fileOf)
	if srcOf != nil {
		var paths []string
		seen := map[string]bool{}
		for _, st := range prog.Body {
			if f := fileOf(st); !seen[f] {
				seen[f] = true
				paths = append(paths, f)
			}
		}
		ds = checker.ApplyCommentDirectives(ds, paths, srcOf)
	}
	if len(ds) > 0 {
		return diag.Errors(ds)
	}
	return nil
}

// UnsupportedCheck runs the checker over a js-lane program and returns
// only the constructs it reports this compiler does not support yet.
func UnsupportedCheck(prog *ast.Program, fileOf func(ast.Statement) string, opts options.Options, script bool) error {
	if fileOf == nil {
		fileOf = func(ast.Statement) string { return "" }
	}
	libProgs, err := lib.Programs()
	if err != nil {
		return fmt.Errorf("builtin declarations: %w", err)
	}
	c := checker.NewWith(binder.BindWith(prog, binder.Options{Script: script, AnnexB: true, Lib: libProgs}), opts)
	var out []*diag.Diagnostic
	for _, d := range c.CheckFiles(fileOf) {
		if d.Message.Kind == diag.Unsupported {
			out = append(out, d)
		}
	}
	if len(out) > 0 {
		return diag.Errors(out)
	}
	return nil
}

// fileImportsKlainSync reports whether a file's top-level imports include
// `klain:sync` (TDD-00143). Imports are top-level statements, so this is a
// shallow scan — no full-AST walk needed.
func fileImportsKlainSync(prog *ast.Program) bool {
	return fileImportsModule(prog, "klain:sync")
}

// fileImportsModule reports whether a file's top-level imports include the
// given module specifier. Imports are top-level statements, so a shallow scan
// suffices.
func fileImportsModule(prog *ast.Program, source string) bool {
	for _, stmt := range prog.Body {
		if imp, ok := stmt.(*ast.ImportDeclaration); ok && imp.Source == source {
			return true
		}
	}
	return false
}

// validateCyclicFile enforces the restriction that still applies to a
// non-entry file that genuinely participates in an import cycle (TDD-00052)
// — an acyclic non-entry file has no restriction at all (see
// ResolveProgramWithOptions's Design-section comment above the merge step).
// Two rules: only declaration-shaped top-level statements are allowed (the
// original V1-wide restriction, now scoped down to just the cyclic case),
// and a VarDeclaration's initializer, if present, must be a compile-time
// literal — closing a real bug found while designing this feature: since
// VarDeclaration was always in the "allowed declarations" set regardless of
// its Init expression's content, a circular pair of files could already
// read each other's not-yet-initialized top-level binding (an uninitialized
// LLVM alloca — undefined behavior, not a clean error). A literal can't
// observe any not-yet-run initialization from anywhere, so this closes the
// hole without needing to model real ES modules' TDZ/live-binding
// semantics.
func validateCyclicFile(prog *ast.Program) error {
	for _, stmt := range prog.Body {
		s := stmt
		if exp, ok := s.(*ast.ExportDeclaration); ok {
			s = exp.Decl
		}
		switch d := s.(type) {
		case *ast.VarDeclaration:
			if d.Init != nil && !isLiteralExpr(d.Init) {
				return fmt.Errorf("%d:%d: a file that participates in an import cycle may only initialize a top-level binding with a compile-time literal — no calls, no references to other bindings (found initializing '%s')",
					stmt.GetPos().Line, stmt.GetPos().Col, d.Name)
			}
		case *ast.VarDeclarationList:
			for _, one := range d.Decls {
				if one.Init != nil && !isLiteralExpr(one.Init) {
					return fmt.Errorf("%d:%d: a file that participates in an import cycle may only initialize a top-level binding with a compile-time literal — no calls, no references to other bindings (found initializing '%s')",
						stmt.GetPos().Line, stmt.GetPos().Col, one.Name)
				}
			}
		case *ast.FunctionDeclaration, *ast.InterfaceDeclaration,
			*ast.TypeAliasDeclaration, *ast.EnumDeclaration, *ast.ImportDeclaration,
			*ast.ExportFromDeclaration, *ast.ClassDeclaration:
			// allowed as-is
		default:
			return fmt.Errorf("%d:%d: a file that participates in an import cycle may only contain declarations (function/const/let/var/interface/type/enum/class) and imports — no executable top-level statements",
				stmt.GetPos().Line, stmt.GetPos().Col)
		}
	}
	return nil
}

// isLiteralExpr reports whether expr is a compile-time literal — a
// number/string/boolean/null/undefined literal, a prefix +/- applied to a
// numeric literal, or an array/object literal whose elements/property
// values are themselves (recursively) literals. Used by validateCyclicFile
// to guarantee a cyclic file's top-level var/let/const initializer can
// never observe another binding's not-yet-run initialization, from this
// file or any other.
func isLiteralExpr(expr ast.Expression) bool {
	switch e := expr.(type) {
	case *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
		return true
	case *ast.UnaryExpression:
		if !e.Prefix || (e.Op != "-" && e.Op != "+") {
			return false
		}
		_, isNum := e.Arg.(*ast.NumberLiteral)
		return isNum
	case *ast.ArrayLiteral:
		for _, el := range e.Elements {
			if !isLiteralExpr(el) {
				return false
			}
		}
		return true
	case *ast.ObjectLiteral:
		for _, prop := range e.Properties {
			if prop.KeyExpr != nil && !isLiteralExpr(prop.KeyExpr) {
				return false
			}
			if !isLiteralExpr(prop.Value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// declRef is one top-level name a declaration statement introduces, paired
// with a setter to rename it in place. Almost every declaration kind
// introduces exactly one name, but *ast.VarDeclarationList (`let i = 0, j =
// 10;` — multiple comma-separated declarators sharing one let/const/var)
// introduces one per declarator, so declRefsOf returns a slice rather than
// a single name.
type declRef struct {
	Name string
	// Kind is the binding kind — one of "let", "const", "var", "function",
	// "class", "enum", "interface", "type" — used by mangleFileDecls to apply
	// the right redeclaration rule: a repeated "var"/"function" of the same
	// name is legal JS (var re-declaration and var/function hoisting collapse
	// into one binding), while any lexical kind colliding with anything is a
	// real duplicate-declaration error.
	Kind string
	Set  func(string)
}

// isVarOrFuncKind reports whether a declaration kind is one that legally
// permits a same-name redeclaration in the same scope: `var` (repeatable) and
// `function` (hoisted; a var and a same-named function coexist as one binding).
func isVarOrFuncKind(kind string) bool { return kind == "var" || kind == "function" }

// isClassIfaceMergePair reports a class+interface same-name pair (either
// order) — TS declaration merging's most common shape (ADR-00466).
// isTypeMemberKind reports a declaration kind a namespace desugars to a
// bare top-level name.
func isTypeMemberKind(k string) bool {
	return k == "class" || k == "interface" || k == "type" || k == "enum"
}

// isTypeValuePair reports a type-only declaration (interface or type alias)
// paired with a value one (function or variable), in either order.
func isTypeValuePair(a, b string) bool {
	typeOnly := func(k string) bool { return k == "interface" || k == "type" }
	value := func(k string) bool { return k == "function" || k == "var" || k == "let" || k == "const" }
	return (typeOnly(a) && value(b)) || (value(a) && typeOnly(b))
}

func isClassIfaceMergePair(a, b string) bool {
	if a == "interface" && b == "interface" {
		// interface+interface declaration merging (ADR-00479): the two
		// declarations' members union at registration.
		return true
	}
	return (a == "class" && b == "interface") || (a == "interface" && b == "class")
}

// declRefsOf returns every top-level name stmt introduces, unwrapping
// ExportDeclaration first. Empty (not nil) for a statement that introduces
// no top-level name at all (import/export-from, or any executable
// statement).
func declRefsOf(stmt ast.Statement) []declRef {
	if exp, ok := stmt.(*ast.ExportDeclaration); ok {
		stmt = exp.Decl
	}
	switch s := stmt.(type) {
	case *ast.FunctionDeclaration:
		return []declRef{{s.Name, "function", func(n string) { s.Name = n }}}
	case *ast.VarDeclaration:
		return []declRef{{s.Name, s.Kind, func(n string) { s.Name = n }}}
	case *ast.VarDeclarationList:
		refs := make([]declRef, len(s.Decls))
		for i, d := range s.Decls {
			d := d
			refs[i] = declRef{d.Name, d.Kind, func(n string) { d.Name = n }}
		}
		return refs
	case *ast.InterfaceDeclaration:
		return []declRef{{s.Name, "interface", func(n string) { s.Name = n }}}
	case *ast.TypeAliasDeclaration:
		return []declRef{{s.Name, "type", func(n string) { s.Name = n }}}
	case *ast.EnumDeclaration:
		return []declRef{{s.Name, "enum", func(n string) { s.Name = n }}}
	case *ast.ClassDeclaration:
		if s.Ambient {
			return nil // a global the environment provides: never renamed
		}
		return []declRef{{s.Name, "class", func(n string) { s.Name = n }}}
	}
	return nil
}

// libKey names a builtin library file the same in every program (its path
// under lib.ModuleRoot, as an identifier fragment); "" for a program's own.
func libKey(path string) string {
	rest, ok := strings.CutPrefix(path, lib.ModuleRoot)
	if !ok {
		return ""
	}
	rest = strings.TrimSuffix(rest, filepath.Ext(rest))
	b := []byte("node_" + rest)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

// mangleFileDecls assigns every top-level declaration in prog a file-private
// mangled name (renaming each declRefsOf entry in place via its own Set) and
// returns the original-name -> mangled-name map for this file. Errors on a
// genuine in-file duplicate declaration — two different files sharing a
// name is no longer an error (that's the whole point of TDD-00041), but two
// declarations of the same name within one file still is, same as before.
//
// TDD-00042: an `export default` declaration additionally gets aliased
// under the synthetic key "default" in the returned map, alongside
// whatever its own declared name already is (real for a named default
// export like `export default function foo() {...}`, or already the
// literal name "default" for the anonymous-declaration/wrapped-expression
// forms — see parser.parseDefaultExportTarget). "default" can never be a
// real user-declared name (lexer.DEFAULT is a reserved keyword, not an
// IDENT), so this alias assignment can only ever collide with a second
// `export default` in the same file — checked explicitly since the two
// underlying declarations may have different own names ("foo" vs "bar")
// and so wouldn't be caught by the loop's own duplicate check above.
func mangleFileDecls(path, key string, prog *ast.Program, fileIdx int) (map[string]string, error) {
	mangled := map[string]string{}
	seenKind := map[string]string{} // name -> kind of the first declaration of it
	sawDefault := false
	for _, stmt := range prog.Body {
		refs := declRefsOf(stmt)
		var lastNewName string
		for _, ref := range refs {
			// A namespace member's desugared flat declaration (TDD-00095)
			// keeps its `X__kmlns_member` name verbatim — codegen resolves
			// `X.member` sites by reconstructing exactly that name, so the
			// per-file `__kml_modN` rename would orphan them. Tradeoff: two
			// files declaring the same namespace+member collide at link time
			// (a clear duplicate-symbol error) instead of being file-private.
			if strings.Contains(ref.Name, "__kmlns_") {
				mangled[ref.Name] = ref.Name
				continue
			}
			if _, dup := seenKind[ref.Name]; dup {
				// The binder has already rejected every redeclaration, so a
				// repeated name here is a merge it allows: a repeated `var` or
				// `function` (one binding; codegen gives each top-level
				// `var x = …` its own alloca and re-points the symbol, so the
				// second is observably an assignment), or an interface
				// merging with a same-name class (ADR-00466: the class wins,
				// the interface's extra members are ignored at registration).
				// Idempotent: re-point to the existing mangled name rather than
				// minting a second one for the same binding.
				ref.Set(mangled[ref.Name])
				lastNewName = mangled[ref.Name]
				continue
			}
			newName := ast.ModuleMangle(ref.Name, fileIdx, key)
			mangled[ref.Name] = newName
			seenKind[ref.Name] = ref.Kind
			ref.Set(newName)
			lastNewName = newName
		}

		// `export default` can only ever wrap a single-name declaration
		// (function/class/expression) — never a VarDeclarationList, not
		// valid JS/TS grammar — so len(refs) is always exactly 1 here.
		if exp, ok := stmt.(*ast.ExportDeclaration); ok && exp.IsDefault {
			if sawDefault {
				return nil, fmt.Errorf("%d:%d: %s declares more than one 'export default'", exp.GetPos().Line, exp.GetPos().Col, path)
			}
			sawDefault = true
			mangled["default"] = lastNewName
		}
	}
	// `export default X;` naming a class or function this file declares:
	// the default export is that declaration itself, not a `const default`
	// holding it (a class is no value here).
	for i, stmt := range prog.Body {
		exp, ok := stmt.(*ast.ExportDeclaration)
		if !ok || !exp.IsDefault {
			continue
		}
		vd, ok := exp.Decl.(*ast.VarDeclaration)
		if !ok || vd.Init == nil {
			break
		}
		id, ok := vd.Init.(*ast.Identifier)
		if !ok {
			break
		}
		if kind := seenKind[id.Name]; kind == "class" || kind == "function" {
			mangled["default"] = mangled[id.Name]
			prog.Body = append(prog.Body[:i:i], prog.Body[i+1:]...)
		}
		break
	}
	return mangled, nil
}

// parseIslandExports parses a dynamic-import target file and returns its
// annotated top-level value exports (TDD-00056), used to shape the typed
// result object at the loading call site. Only `export const/let/var name: T`
// bindings with a type annotation are included — matching the island's own
// scalar/string accessor surface; un-annotated or non-value exports are
// omitted (the result object simply lacks those fields in V1).
func parseIslandExports(path string) ([]ast.ImportCallExport, error) {
	src, err := readSource(path)
	if err != nil {
		return nil, err
	}
	prog, err := parser.Parse(string(src))
	if err != nil {
		return nil, err
	}
	var out []ast.ImportCallExport
	add := func(d *ast.VarDeclaration) {
		if d.TypeAnnot != nil {
			out = append(out, ast.ImportCallExport{Name: d.Name, TypeAnnot: d.TypeAnnot})
		}
	}
	for _, stmt := range prog.Body {
		ed, ok := stmt.(*ast.ExportDeclaration)
		if !ok || ed.IsDefault {
			continue
		}
		switch d := ed.Decl.(type) {
		case *ast.VarDeclaration:
			add(d)
		case *ast.VarDeclarationList:
			for _, vd := range d.Decls {
				add(vd)
			}
		}
	}
	return out, nil
}

// exportedNames returns the set of top-level names a file exports. A
// default export (TDD-00042) is exposed only under the "default" key, not
// under its own declared name too — matching real ES modules, where
// `export default function foo() {...}` does not also make `foo` available
// via `import { foo }`.
// prependNamespaceObjects declares, at the top of prog, the module namespace
// object of each namespace import read as a value (`typeof path`, `const p =
// path`): a frozen object of the module's value exports, keyed in sorted
// order as Node's module namespace object is.
func prependNamespaceObjects(prog *ast.Program, objects map[string]string, values map[string]map[string]string, live map[string]map[string]bool, at map[string]ast.Pos, declared map[string]bool) {
	if len(objects) == 0 {
		return
	}
	locals := make([]string, 0, len(objects))
	for l := range objects {
		locals = append(locals, l)
	}
	sort.Strings(locals)
	var decls []ast.Statement
	for _, l := range locals {
		// A module has one namespace object: the first file reading it as
		// a value declares it, the others name that one.
		if declared[objects[l]] {
			continue
		}
		declared[objects[l]] = true
		vals, pos := values[l], at[l]
		keys := make([]string, 0, len(vals))
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		freeze := namespaceObject(keys, vals, live[l], pos)
		decls = append(decls, ast.NewVarDeclaration("const", objects[l], nil, freeze, pos))
	}
	prog.Body = append(decls, prog.Body...)
}

// namespaceObject is a module namespace object: a frozen object of the value
// exports keys names (vals maps each to its program name), a `let`/`var`
// export (live) read through an accessor so it stays the binding.
func namespaceObject(keys []string, vals map[string]string, live map[string]bool, pos ast.Pos) *ast.CallExpression {
	props := make([]ast.ObjectProperty, 0, len(keys))
	lit := ast.NewObjectLiteral(nil, pos)
	for _, k := range keys {
		if live[k] {
			get := ast.NewFunctionExpression("", nil, nil, ast.NewBlockStatement([]ast.Statement{ast.NewReturnStatement(ast.NewIdentifier(vals[k], pos), pos)}, pos), false, pos)
			props = append(props, ast.ObjectProperty{Key: k, Value: get, AccessorKind: "get"})
			continue
		}
		props = append(props, ast.ObjectProperty{Key: k, Value: ast.NewIdentifier(vals[k], pos)})
	}
	lit.Properties = props
	freeze := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), "freeze", pos),
		[]ast.Expression{lit}, pos)
	// Typed as the frozen literal; built as the namespace object.
	freeze.Intrinsic = ast.ModuleNamespaceIntrinsic
	return freeze
}

// mutableExports is the exported `let`/`var` bindings of prog, by the names
// they are exported under.
func mutableExports(prog *ast.Program) map[string]bool {
	out := map[string]bool{}
	for _, st := range prog.Body {
		exp, ok := st.(*ast.ExportDeclaration)
		if !ok || exp.IsDefault {
			continue
		}
		switch d := exp.Decl.(type) {
		case *ast.VarDeclaration:
			if d.Kind != "const" {
				out[ast.Unmangle(d.Name)] = true
			}
		case *ast.VarDeclarationList:
			for _, v := range d.Decls {
				if v.Kind != "const" {
					out[ast.Unmangle(v.Name)] = true
				}
			}
		}
	}
	return out
}

// typeExportedNames is prog's exported interfaces and type aliases.
func typeExportedNames(prog *ast.Program) map[string]bool {
	names := map[string]bool{}
	for _, stmt := range prog.Body {
		exp, ok := stmt.(*ast.ExportDeclaration)
		if !ok || exp.IsDefault {
			continue
		}
		for _, ref := range declRefsOf(stmt) {
			if ref.Kind == "interface" || ref.Kind == "type" {
				names[ref.Name] = true
			}
		}
	}
	return names
}

func exportedNames(prog *ast.Program) map[string]bool {
	names := map[string]bool{}
	for _, stmt := range prog.Body {
		exp, ok := stmt.(*ast.ExportDeclaration)
		if !ok {
			continue
		}
		if exp.IsDefault {
			names["default"] = true
			continue
		}
		for _, ref := range declRefsOf(stmt) {
			names[ref.Name] = true
		}
	}
	return names
}

// resolveImportPath resolves an import specifier — relative (`./`, `../`)
// against dir, or, failing that, a bare package specifier against
// klainModulesDir (TDD-00054 Stage 1, "" meaning no klain_modules directory
// exists above the entry file, in which case a bare specifier is rejected
// exactly as it always has been). Auto-appends ".ts" if the resolved path
// has no extension, and confirms the resulting file exists.
func resolveImportPath(dir, source, klainModulesDir string) (string, error) {
	if p, ok := lib.ModulePath(source); ok {
		return p, nil // a builtin module written in TypeScript (TDD-00231)
	}
	// A builtin module's relative import of another (`./internal_http`):
	// the library's own file.
	// filepath.Dir turns the library's pseudo-path into `\__kml_lib\node`
	// on Windows: compare and join it slash-separated.
	if libDir := filepath.ToSlash(dir); (strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")) && strings.HasPrefix(libDir+"/", lib.ModuleRoot) {
		p := path.Join(libDir, source)
		if !strings.HasSuffix(p, ".ts") {
			p += ".ts"
		}
		p = lib.BackendPath(p)
		if _, ok := lib.ModuleSource(p); ok {
			return p, nil
		}
		return "", fmt.Errorf("cannot find module '%s'", source)
	}
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
		abs, found, err := resolveTsFile(dir, source)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("cannot find module '%s' (resolved to %s)", source, abs)
		}
		return abs, nil
	}
	if klainModulesDir == "" {
		return "", fmt.Errorf("import path '%s' must start with './' or '../' — bare/package-style imports are not supported", source)
	}
	return resolveKlmpmPackage(klainModulesDir, source)
}

// resolveTsFile joins dir and rel, auto-appends ".ts" if the result has no
// extension, and reports whether the resulting file exists — the shared
// "resolve a relative path to a real file" mechanics both a relative import
// and a klmpm package's own "main" field (resolveKlmpmPackage) need. abs is
// always returned (even on a miss) so callers can phrase their own
// not-found error around it.
func resolveTsFile(dir, rel string) (abs string, found bool, err error) {
	base, err := filepath.Abs(filepath.Join(dir, rel))
	if err != nil {
		return "", false, err
	}
	isFile := func(p string) bool {
		st, e := os.Stat(p)
		return e == nil && !st.IsDir()
	}
	if filepath.Ext(base) != "" {
		return base, isFile(base), nil
	}
	// An extensionless specifier: the file with each extension in turn,
	// then a directory's index, as Node's CommonJS resolution and tsc's
	// module resolution look (`./m` → m.ts, m.js; `./d` → d/index.ts).
	for _, c := range []string{base + ".ts", base + ".js", base + ".mjs", base + ".cjs",
		filepath.Join(base, "index.ts"), filepath.Join(base, "index.js")} {
		if isFile(c) {
			return c, true, nil
		}
	}
	return base + ".ts", false, nil
}

// klmpmManifest is a klain.json package manifest (TDD-00054 Stage 1) — only
// the one field the compiler itself ever needs. A project's own root
// klain.json (dependency list, version, etc. — klmpm's own bookkeeping) is
// never read here at all; only a fetched package's manifest, and only for
// its "main" field.
type klmpmManifest struct {
	Main string `json:"main"`
}

// findKlainModulesDir walks upward from startDir (inclusive) to the
// filesystem root looking for a "klain_modules" subdirectory, returning its
// absolute path, or "" if none is found anywhere above startDir.
func findKlainModulesDir(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, "klain_modules")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// resolveKlmpmPackage resolves a bare specifier to klainModulesDir/<name>'s
// klain.json "main" field (TDD-00054 Stage 1). The compiler never reads a
// package's own "dependencies" — only whether the package directory and a
// usable "main" entry exist on disk; klmpm itself (not yet built) owns
// fetching/versioning/the lockfile entirely.
func resolveKlmpmPackage(klainModulesDir, name string) (string, error) {
	pkgDir := filepath.Join(klainModulesDir, name)
	if info, err := os.Stat(pkgDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("cannot find package '%s' in %s", name, klainModulesDir)
	}
	manifestPath := filepath.Join(pkgDir, "klain.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("package '%s' has no klain.json manifest (looked for %s)", name, manifestPath)
	}
	var manifest klmpmManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("package '%s': invalid klain.json (%s): %w", name, manifestPath, err)
	}
	if manifest.Main == "" {
		return "", fmt.Errorf("package '%s': klain.json (%s) has no \"main\" field", name, manifestPath)
	}
	abs, found, err := resolveTsFile(pkgDir, manifest.Main)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("package '%s': klain.json's \"main\" (%s) does not exist (resolved to %s)", name, manifest.Main, abs)
	}
	return abs, nil
}

// splitWorkerBody partitions a worker module's (already-unwrapped) top-level
// statements into hoistable pure declarations vs. the statements that make
// up the worker's entry-function body (TDD-00098). Var declarations stay in
// the body deliberately: they are per-worker state, initialized on the
// worker thread. The known cost: a worker module's *named* functions can't
// read the worker's own top-level bindings (those are entry-function locals,
// the pre-TDD-00093 situation, scoped to worker modules) — an arrow closure
// captures them fine.
func splitWorkerBody(stmts []ast.Statement) (decls, body []ast.Statement) {
	for _, s := range stmts {
		switch s.(type) {
		case *ast.FunctionDeclaration, *ast.ClassDeclaration,
			*ast.InterfaceDeclaration, *ast.TypeAliasDeclaration,
			*ast.EnumDeclaration:
			decls = append(decls, s)
		default:
			body = append(body, s)
		}
	}
	return decls, body
}

// sortDiags orders one file's diagnostics by position.
func sortDiags(ds []*diag.Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i].Pos, ds[j].Pos
		return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
	})
}

// unwrap strips ImportDeclaration nodes and unwraps ExportDeclaration nodes
// from a file's statement list, for merging into the combined program.
func unwrap(stmts []ast.Statement) []ast.Statement {
	out := make([]ast.Statement, 0, len(stmts))
	for _, s := range stmts {
		if _, ok := s.(*ast.ImportDeclaration); ok {
			continue
		}
		if _, ok := s.(*ast.ExportFromDeclaration); ok {
			continue // TDD-00051: pure name-forwarding, no runtime statement
		}
		if exp, ok := s.(*ast.ExportDeclaration); ok {
			out = append(out, exp.Decl)
			continue
		}
		out = append(out, s)
	}
	return out
}

// workerPaths returns the string-literal paths of every `new Worker('…')` in
// the file — its worker entry modules, which are dependency edges — unless the
// file declares its own top-level `Worker`, in which case `new Worker` is that
// class, not a worker thread.
func workerPaths(prog *ast.Program) []string {
	if declaresOwnWorker(prog) {
		return nil
	}
	var out []string
	ast.Inspect(prog, func(n ast.Node) bool {
		if ne, ok := n.(*ast.NewExpression); ok && ne.ClassName == "Worker" && len(ne.Args) > 0 && !evalWorker(ne) {
			if lit, ok := ne.Args[0].(*ast.StringLiteral); ok {
				out = append(out, lit.Value)
			}
		}
		return true
	})
	return out
}

// workerEvals returns the code of every `new Worker(code, { eval: true })`
// in the file whose code is a constant string (constString) (unless the file declares its own Worker).
func workerEvals(prog *ast.Program) []string {
	if declaresOwnWorker(prog) {
		return nil
	}
	var out []string
	ast.Inspect(prog, func(n ast.Node) bool {
		ne, ok := n.(*ast.NewExpression)
		if !ok || ne.ClassName != "Worker" || !evalWorker(ne) {
			return true
		}
		if code, ok := constString(ne.Args[0]); ok {
			out = append(out, code)
		}
		return true
	})
	return out
}

// constString is the value of a string literal, a template without
// substitutions, or a `+` of them.
func constString(x ast.Expression) (string, bool) {
	switch c := x.(type) {
	case *ast.StringLiteral:
		return c.Value, true
	case *ast.TemplateLiteral:
		if len(c.Exprs) == 0 && len(c.Quasis) == 1 {
			return c.Quasis[0], true
		}
	case *ast.BinaryExpression:
		if c.Op == "+" {
			l, ok := constString(c.Left)
			if !ok {
				return "", false
			}
			r, ok := constString(c.Right)
			return l + r, ok
		}
	}
	return "", false
}

// declaresOwnWorker reports a file declaring its own top-level Worker: its
// `new Worker` is that class, not a worker thread.
func declaresOwnWorker(prog *ast.Program) bool {
	for _, st := range prog.Body {
		switch d := st.(type) {
		case *ast.ClassDeclaration:
			if d.Name == "Worker" {
				return true
			}
		case *ast.VarDeclaration:
			if d.Name == "Worker" {
				return true
			}
		case *ast.FunctionDeclaration:
			if d.Name == "Worker" {
				return true
			}
		}
	}
	return false
}

// evalWorker reports a `new Worker(code, { eval: true })`.
func evalWorker(ne *ast.NewExpression) bool {
	if len(ne.Args) < 2 {
		return false
	}
	opts, ok := ne.Args[1].(*ast.ObjectLiteral)
	if !ok {
		return false
	}
	for _, p := range opts.Properties {
		if b, ok := p.Value.(*ast.BooleanLiteral); ok && p.Key == "eval" && b.Value {
			return true
		}
	}
	return false
}

// dynamicImports returns the dynamic `import('…')` expressions in the file
// whose specifier is a string literal — its dependency edges. A non-literal
// specifier is left for a clean codegen-time error.
func dynamicImports(prog *ast.Program) []*ast.ImportCallExpression {
	var out []*ast.ImportCallExpression
	ast.Inspect(prog, func(n ast.Node) bool {
		if ic, ok := n.(*ast.ImportCallExpression); ok {
			if _, lit := ic.Specifier.(*ast.StringLiteral); lit {
				out = append(out, ic)
			}
		}
		return true
	})
	return out
}

// IsModule reports whether a file is an ES module: it imports or exports.
// A file without either is a script, whose top-level bindings are globals.
func IsModule(prog *ast.Program) bool {
	for _, st := range prog.Body {
		switch st.(type) {
		case *ast.ImportDeclaration, *ast.ExportDeclaration, *ast.ExportFromDeclaration:
			return true
		}
	}
	return false
}

// HasModuleSyntax reports an ES module by its own syntax: an import or
// export the source wrote, not a CommonJS require desugared to an import.
func HasModuleSyntax(prog *ast.Program) bool {
	for _, st := range prog.Body {
		switch s := st.(type) {
		case *ast.ImportDeclaration:
			if !s.FromRequire {
				return true
			}
		case *ast.ExportDeclaration, *ast.ExportFromDeclaration:
			return true
		}
	}
	return false
}

// readSource reads a program file, or a builtin module written in
// TypeScript from the compiler's own embedded sources.
func readSource(path string) ([]byte, error) {
	if src, ok := lib.ModuleSource(path); ok {
		return src, nil
	}
	return os.ReadFile(path)
}

// defaultImportsAsNamespaces rewrites a default import of a builtin module
// whose default export is its namespace (`import stream from 'stream'`) into
// a namespace import, splitting off any named imports beside it.
func defaultImportsAsNamespaces(prog *ast.Program) {
	var body []ast.Statement
	for _, st := range prog.Body {
		imp, ok := st.(*ast.ImportDeclaration)
		if ok && imp.Namespace != "" && imp.FromRequire && !imp.TypeOnly {
			// `const E = require('events')` binds Node's module.exports,
			// the module's default export.
			if p, isLib := lib.ModulePath(imp.Source); isLib && lib.RequireIsDefault(p) {
				d := ast.NewImportDeclaration([]ast.ImportSpecifier{{Imported: "default", Local: imp.Namespace}}, "", imp.Source, imp.GetPos())
				body = append(body, d)
				continue
			}
		}
		if !ok || imp.Namespace != "" {
			body = append(body, st)
			continue
		}
		p, isLib := lib.ModulePath(imp.Source)
		if !isLib || !lib.DefaultIsNamespace(p) {
			body = append(body, st)
			continue
		}
		var named []ast.ImportSpecifier
		local := ""
		for _, sp := range imp.Specifiers {
			if sp.Imported == "default" {
				local = sp.Local
			} else {
				named = append(named, sp)
			}
		}
		if local == "" {
			body = append(body, st)
			continue
		}
		ns := ast.NewImportDeclaration(nil, local, imp.Source, imp.GetPos())
		ns.TypeOnly = imp.TypeOnly
		ns.FromDefault = true
		body = append(body, ns)
		if len(named) > 0 {
			imp.Specifiers = named
			body = append(body, imp)
		}
	}
	prog.Body = body
}

// rewriteSelfWorker turns `new Worker(__filename)`, or the ES-module form
// `new Worker(new URL(import.meta.url))`, into a worker of the file itself
// (its own path as the specifier), and reports whether it did.
func rewriteSelfWorker(prog *ast.Program, path string) bool {
	self := "./" + filepath.Base(path)
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if ne, ok := n.(*ast.NewExpression); ok && ne.ClassName == "Worker" && len(ne.Args) > 0 {
			if id, ok := ne.Args[0].(*ast.Identifier); ok && id.Name == "__filename" {
				ne.Args[0] = ast.NewStringLiteral(self, id.GetPos())
				found = true
			}
			if u, ok := ne.Args[0].(*ast.NewExpression); ok && u.ClassName == "URL" && len(u.Args) == 1 {
				if m, ok := u.Args[0].(*ast.ImportMetaUrl); ok {
					ne.Args[0] = ast.NewStringLiteral(self, m.GetPos())
					found = true
				}
			}
		}
		return true
	})
	return found
}

// rewriteFileGlobals turns `__filename` and `__dirname` into the file's own
// absolute path and directory, as Node's CommonJS module wrapper binds them,
// unless the file declares a binding of that name itself. An eval worker's
// are Node's `[worker eval]` and `.`.
func rewriteFileGlobals(prog *ast.Program, path string, evalWorker bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	dir := filepath.Dir(abs)
	if evalWorker {
		abs, dir = "[worker eval]", "."
	}
	declared := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.VarDeclaration:
			declared[d.Name] = true
		case *ast.FunctionDeclaration:
			declared[d.Name] = true
			for _, p := range d.Params {
				declared[p.Name] = true
			}
		case *ast.ArrowFunction:
			for _, p := range d.Params {
				declared[p.Name] = true
			}
		}
		return true
	})
	values := map[string]string{}
	if !declared["__filename"] {
		values["__filename"] = abs
	}
	if !declared["__dirname"] {
		values["__dirname"] = dir
	}
	if len(values) == 0 {
		return
	}
	var rewrite func(ast.Node) ast.Node
	rewrite = func(n ast.Node) ast.Node {
		if id, ok := n.(*ast.Identifier); ok {
			if v, ok := values[id.Name]; ok {
				return ast.NewStringLiteral(v, id.GetPos())
			}
			return n
		}
		ast.RewriteChildren(n, rewrite)
		return n
	}
	ast.RewriteChildren(prog, rewrite)
}

// rewriteStaticEval replaces `eval(src)`, src a constant string, with the
// code it holds (TDD-00046's static subset), so the binder and checker see
// it as the program's own code, which tsc never checks (prog.EvalCode):
//   - one expression is spliced in place, its value eval's result;
//   - statements run in an arrow function called in place, whose `this`
//     and `arguments` are the caller's, and whose result is the last
//     completion value; in sloppy code their `var` and function
//     declarations are bindings of the caller's function, in strict code
//     (the caller's, or the source's own directive) the eval's own;
//   - source that does not parse, as the caller's strictness reads it,
//     throws SyntaxError when the call runs.
//
// A file declaring its own `eval` keeps its calls; a dynamic argument is
// left to code generation, which rejects it cleanly.
func rewriteStaticEval(prog *ast.Program) {
	declared := false
	ast.Inspect(prog, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.VarDeclaration:
			declared = declared || d.Name == "eval"
		case *ast.FunctionDeclaration:
			declared = declared || d.Name == "eval"
			for _, p := range d.Params {
				declared = declared || p.Name == "eval"
			}
		case *ast.ArrowFunction:
			for _, p := range d.Params {
				declared = declared || p.Name == "eval"
			}
		case *ast.FunctionExpression:
			for _, p := range d.Params {
				declared = declared || p.Name == "eval"
			}
		}
		return !declared
	})
	if declared {
		return
	}
	inInit := evalCallsInClassInitializers(prog)
	// An eval in a parameter initializer declares into a scope of its own,
	// between the parameters and the body: the body's declaration of a name
	// shadows it. paramEval maps each such call to the names its function's
	// body declares.
	type paramEvalSite struct {
		shadowed map[string]bool // what the body declares
		defaults []ast.Param     // this parameter and those after it
		params   map[string]bool // every parameter's name
	}
	paramEval := map[*ast.CallExpression]paramEvalSite{}
	ast.Inspect(prog, func(n ast.Node) bool {
		var params []ast.Param
		var body *ast.BlockStatement
		switch f := n.(type) {
		case *ast.FunctionDeclaration:
			params, body = f.Params, f.Body
		case *ast.FunctionExpression:
			params, body = f.Params, f.Body
		case *ast.ArrowFunction:
			params, body = f.Params, f.Block
		default:
			return true
		}
		var names map[string]bool
		for i, p := range params {
			if p.Default == nil {
				continue
			}
			rest := params[i:]
			ast.Inspect(p.Default, func(m ast.Node) bool {
				switch c := m.(type) {
				case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
					return false
				case *ast.CallExpression:
					if names == nil {
						names = bodyDeclaredNames(body)
					}
					paramEval[c] = paramEvalSite{shadowed: names, defaults: rest, params: paramNames(params)}
				}
				return true
			})
		}
		return true
	})
	markEval := func(x ast.Expression) {
		if prog.EvalCode == nil {
			prog.EvalCode = map[ast.Expression]bool{}
		}
		prog.EvalCode[x] = true
	}
	throwing := func(ctor, msg string) ast.Expression {
		th, err := parser.ParseEval("(() => { throw new " + ctor + "(" + strconv.Quote(msg) + "); })()")
		if err != nil {
			return nil
		}
		ts, _ := th.Body[0].(*ast.ExpressionStatement)
		if ts == nil {
			return nil
		}
		markEval(ts.Expr)
		return ts.Expr
	}
	// hoists are the names sloppy eval code declares into each caller
	// function's (or the program's) variables, in first-seen order.
	hoists := map[ast.Node][]string{}
	hoisted := map[ast.Node]map[string]bool{}
	paramScoped := map[string]bool{} // hoisted names a parameter's eval declares
	type evalScope struct {
		strict bool
		vars   ast.Node // the function (or program) whose variables `var` declares
		// superProp and superCall: the `super` its code may use (a
		// method's; a derived constructor's call too).
		superProp, superCall bool
	}
	// methods are the class methods and accessors, and ctors the
	// constructors (true when derived), whose code may use `super`.
	methods := map[*ast.FunctionDeclaration]bool{}
	ctors := map[*ast.FunctionDeclaration]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			for _, m := range cd.Methods {
				methods[m] = true
			}
			if cd.Constructor != nil {
				ctors[cd.Constructor] = cd.BaseClass != ""
			}
		}
		return true
	})
	var rw func(n ast.Node, sc evalScope) ast.Node
	rwOne := func(n ast.Node, sc evalScope) ast.Node {
		switch x := n.(type) {
		case *ast.FunctionDeclaration:
			derived, ctor := ctors[x]
			sc = evalScope{strict: sc.strict || x.Body != nil && blockIsStrict(x.Body), vars: x, superProp: methods[x] || ctor, superCall: derived}
		case *ast.FunctionExpression:
			sc = evalScope{strict: sc.strict || x.Body != nil && blockIsStrict(x.Body), vars: x, superProp: x.Method}
		case *ast.ArrowFunction:
			sc = evalScope{strict: sc.strict || x.Block != nil && blockIsStrict(x.Block), vars: x, superProp: sc.superProp, superCall: sc.superCall}
		case *ast.ClassDeclaration, *ast.ClassExpression:
			sc.strict = true
		}
		ast.RewriteChildren(n, func(ch ast.Node) ast.Node { return rw(ch, sc) })
		call, ok := n.(*ast.CallExpression)
		if !ok || len(call.Args) != 1 || call.Optional {
			return n
		}
		if id, ok := call.Callee.(*ast.Identifier); !ok || id.Name != "eval" {
			return n
		}
		var src string
		switch a := call.Args[0].(type) {
		case *ast.StringLiteral:
			src = a.Value
		case *ast.TemplateLiteral:
			if len(a.Exprs) != 0 || len(a.Quasis) != 1 {
				return n
			}
			src = a.Quasis[0]
		default:
			return n
		}
		parsed := src
		if sc.strict {
			parsed = "'use strict';\n" + src
		}
		ev, err := parser.ParseEvalIn(parsed, sc.superProp, sc.superCall)
		if err == nil && evalHasTopLevelReturn(ev) {
			if t := throwing("SyntaxError", "Illegal return statement"); t != nil {
				return t
			}
			return n
		}
		if err != nil {
			msg := err.Error()
			if k := strings.Index(msg, ": "); k > 0 && strings.Count(msg[:k], ":") == 1 {
				msg = msg[k+2:]
			}
			if t := throwing("SyntaxError", msg); t != nil {
				return t
			}
			return n
		}
		body := ev.Body
		if sc.strict && len(body) > 0 {
			body = body[1:] // the directive this prefix added
		}
		if len(body) == 1 {
			if es, ok := body[0].(*ast.ExpressionStatement); ok {
				if inInit[call] && readsOwnArguments(es.Expr) {
					// A field initializer's or static block's eval code sees no
					// function's arguments: its SyntaxError, thrown by the call.
					if t := throwing("SyntaxError", "'arguments' is not allowed in class field initializer or static initialization block"); t != nil {
						return t
					}
				}
				out := rw(es.Expr, sc).(ast.Expression)
				markEval(out)
				return out
			}
		}
		strict := sc.strict || programIsStrictBody(body)
		pos := call.GetPos()
		inner := sc
		var stmts []ast.Statement
		if strict {
			inner = evalScope{strict: true, vars: nil}
		} else {
			// The caller's variables: each `var` becomes an assignment to
			// one, and each function declaration a hoisted assignment.
			names, funcs, rest := sloppyEvalDecls(body, pos)
			site, inParams := paramEval[call]
			if inParams {
				// A `var` the eval code declares beside a parameter of the
				// same name: EvalDeclarationInstantiation's SyntaxError.
				for _, nm := range names {
					if site.params[nm] {
						if t := throwing("SyntaxError", "Identifier '"+nm+"' has already been declared"); t != nil {
							return t
						}
					}
				}
			}
			rename := map[string]string{}
			for _, nm := range names {
				if inParams && site.shadowed[nm] {
					// The body's own binding shadows it: a hidden one the
					// eval code and the later parameters read.
					rename[nm] = nm + "$kmlpe"
					nm = rename[nm]
				}
				if inParams {
					paramScoped[nm] = true
				}
				if hoisted[sc.vars] == nil {
					hoisted[sc.vars] = map[string]bool{}
				}
				if !hoisted[sc.vars][nm] {
					hoisted[sc.vars][nm] = true
					hoists[sc.vars] = append(hoists[sc.vars], nm)
				}
			}
			stmts = append(stmts, funcs...)
			body = rest
			if len(rename) > 0 {
				var ren func(m ast.Node) ast.Node
				ren = func(m ast.Node) ast.Node {
					if id, ok := m.(*ast.Identifier); ok && rename[id.Name] != "" {
						return ast.NewIdentifier(rename[id.Name], id.GetPos())
					}
					ast.RewriteChildren(m, ren)
					return m
				}
				for i := range stmts {
					stmts[i] = ren(stmts[i]).(ast.Statement)
				}
				for i := range body {
					body[i] = ren(body[i]).(ast.Statement)
				}
				for i := range site.defaults {
					if site.defaults[i].Default != nil {
						site.defaults[i].Default = ren(site.defaults[i].Default).(ast.Expression)
					}
				}
			}
		}
		stmts = append(stmts, body...)
		block := ast.NewBlockStatement(stmts, pos)
		for i, st := range block.Body {
			block.Body[i] = rw(st, inner).(ast.Statement)
		}
		const cv = "__kml_evalcv"
		withCompletion(block, cv)
		block.Body = append([]ast.Statement{ast.NewVarDeclaration("let", cv, &ast.KeywordType{Keyword: "any"}, nil, pos)}, block.Body...)
		block.Body = append(block.Body, ast.NewReturnStatement(ast.NewIdentifier(cv, pos), pos))
		fn := ast.NewArrowFunction(nil, nil, nil, block, pos)
		markEval(fn)
		return ast.NewCallExpression(fn, nil, pos)
	}
	rw = func(n ast.Node, sc evalScope) ast.Node {
		// `eval(src, more…)`: the arguments after the source are
		// evaluated, then ignored.
		if call, ok := n.(*ast.CallExpression); ok && !call.Optional && len(call.Args) > 1 {
			if id, ok := call.Callee.(*ast.Identifier); ok && id.Name == "eval" {
				extras := make([]ast.Expression, 0, len(call.Args))
				for _, a := range call.Args[1:] {
					x := rw(a, sc).(ast.Expression)
					if sp, isSpread := x.(*ast.SpreadElement); isSpread {
						x = ast.NewArrayLiteral([]ast.Expression{x}, sp.GetPos()) // iterated
					}
					extras = append(extras, x)
				}
				call.Args = call.Args[:1]
				r := rwOne(n, sc)
				if r == ast.Node(call) {
					call.Args = append(call.Args, extras...)
					return call
				}
				return ast.NewSequenceExpression(append(extras, r.(ast.Expression)), call.GetPos())
			}
		}
		return rwOne(n, sc)
	}
	ast.RewriteChildren(prog, func(ch ast.Node) ast.Node {
		return rw(ch, evalScope{strict: programIsStrictBody(prog.Body) || IsModule(prog), vars: prog})
	})
	for owner, names := range hoists {
		var decls []ast.Statement
		for _, nm := range names {
			d := ast.NewVarDeclaration("var", nm, &ast.KeywordType{Keyword: "any"}, nil, ast.Pos{})
			d.ParamScope = paramScoped[nm]
			decls = append(decls, d)
		}
		switch o := owner.(type) {
		case *ast.Program:
			o.Body = append(decls, o.Body...)
		case *ast.FunctionDeclaration:
			o.Body.Body = append(decls, o.Body.Body...)
		case *ast.FunctionExpression:
			o.Body.Body = append(decls, o.Body.Body...)
		case *ast.ArrowFunction:
			if o.Block != nil {
				o.Block.Body = append(decls, o.Block.Body...)
			}
		}
	}
}

// bodyDeclaredNames are the names a function body binds: its own
// declarations, and the `var`s of its nested blocks.
func bodyDeclaredNames(body *ast.BlockStatement) map[string]bool {
	out := map[string]bool{}
	if body == nil {
		return out
	}
	for _, st := range body.Body {
		switch d := st.(type) {
		case *ast.VarDeclaration:
			out[d.Name] = true
		case *ast.VarDeclarationList:
			for _, v := range d.Decls {
				out[v.Name] = true
			}
		case *ast.FunctionDeclaration:
			out[d.Name] = true
		case *ast.ClassDeclaration:
			out[d.Name] = true
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.VarDeclaration:
			if d.Kind == "var" {
				out[d.Name] = true
			}
		}
		return true
	})
	return out
}

// blockIsStrict reports a function body whose directive prologue holds
// "use strict".
func blockIsStrict(b *ast.BlockStatement) bool { return programIsStrictBody(b.Body) }

// programIsStrictBody reports statements whose directive prologue holds
// "use strict".
func programIsStrictBody(body []ast.Statement) bool { return ast.HasUseStrictDirective(body) }

// evalHasTopLevelReturn reports a `return` outside any function in eval
// code, a SyntaxError there.
func evalHasTopLevelReturn(prog *ast.Program) bool {
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.ReturnStatement:
			found = true
		}
		return !found
	})
	return found
}

// sloppyEvalDecls splits sloppy eval code's declarations out: the names
// its `var`s and function declarations bind in the caller, the function
// declarations as assignments run first, and the statements with each
// `var` an assignment of its initializer.
func sloppyEvalDecls(body []ast.Statement, pos ast.Pos) (names []string, funcs, rest []ast.Statement) {
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, st := range body {
		if fd, ok := st.(*ast.FunctionDeclaration); ok && fd.Body != nil {
			add(fd.Name)
			fe := ast.NewFunctionExpression(fd.Name, fd.Params, fd.ReturnType, fd.Body, fd.IsAsync, fd.GetPos())
			fe.IsGenerator = fd.IsGenerator
			funcs = append(funcs, ast.NewExpressionStatement(ast.NewAssignmentExpression("=", ast.NewIdentifier(fd.Name, fd.GetPos()), fe, fd.GetPos()), fd.GetPos()))
			continue
		}
		rest = append(rest, st)
	}
	// A `var` that is a statement of a list becomes the assignment of its
	// initializer (a `var` in a loop's header stays the eval's own).
	assign := func(d *ast.VarDeclaration) ast.Expression {
		add(d.Name)
		if d.Init == nil {
			return nil
		}
		return ast.NewAssignmentExpression("=", ast.NewIdentifier(d.Name, d.GetPos()), d.Init, d.GetPos())
	}
	var list func([]ast.Statement) []ast.Statement
	var walk func(ast.Node) ast.Node
	list = func(sts []ast.Statement) []ast.Statement {
		var out []ast.Statement
		for _, st := range sts {
			switch d := st.(type) {
			case *ast.VarDeclaration:
				if d.Kind == "var" && d.Name != "" {
					if a := assign(d); a != nil {
						out = append(out, ast.NewExpressionStatement(a, d.GetPos()))
					}
					continue
				}
			case *ast.VarDeclarationList:
				allVar := len(d.Decls) > 0
				for _, v := range d.Decls {
					allVar = allVar && v.Kind == "var" && v.Name != ""
				}
				if allVar {
					for _, v := range d.Decls {
						if a := assign(v); a != nil {
							out = append(out, ast.NewExpressionStatement(a, v.GetPos()))
						}
					}
					continue
				}
			}
			out = append(out, walk(st).(ast.Statement))
		}
		return out
	}
	walk = func(n ast.Node) ast.Node {
		switch x := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return n // their own variables
		case *ast.BlockStatement:
			x.Body = list(x.Body)
			return n
		}
		ast.RewriteChildren(n, walk)
		return n
	}
	rest = list(rest)
	return names, funcs, rest
}

// withCompletion stores each statement-level expression's value in cv,
// the eval code's completion value; nested functions keep theirs.
func withCompletion(n ast.Node, cv string) {
	fins := 0
	var rewrite func(ast.Node) ast.Node
	rewrite = func(n ast.Node) ast.Node {
		switch x := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return n
		case *ast.ExpressionStatement:
			return ast.NewExpressionStatement(ast.NewAssignmentExpression("=", ast.NewIdentifier(cv, x.GetPos()), x.Expr, x.GetPos()), x.GetPos())
		case *ast.TryStatement:
			ast.RewriteChildren(n, rewrite)
			if f := x.Finally; f != nil {
				// A finally block completing normally keeps the try's (or
				// catch's) value; one leaving by break or continue has its
				// own, else undefined (UpdateEmpty(F, undefined)).
				pos := f.GetPos()
				saved := fmt.Sprintf("%s_fin%d", cv, fins)
				fins++
				head := []ast.Statement{
					ast.NewVarDeclaration("let", saved, &ast.KeywordType{Keyword: "any"}, ast.NewIdentifier(cv, pos), pos),
					ast.NewExpressionStatement(ast.NewAssignmentExpression("=", ast.NewIdentifier(cv, pos), ast.NewNullLiteral(true, pos), pos), pos),
				}
				tail := ast.NewExpressionStatement(ast.NewAssignmentExpression("=", ast.NewIdentifier(cv, pos), ast.NewIdentifier(saved, pos), pos), pos)
				f.Body = append(append(head, f.Body...), tail)
			}
			return n
		}
		ast.RewriteChildren(n, rewrite)
		return n
	}
	ast.RewriteChildren(n, rewrite)
}

// rewriteObjectLiteralSuper lowers `super` in an object literal's methods
// and accessors, and (in JavaScript; tsc rejects it, TS2335) in the members
// of a class with no base: the literal is
// bound, each time it is evaluated, to a home object (a base-less class's
// is Object.prototype's, or Function.prototype's for a static member's),
// and `super.x` / `super[k]` read the home's prototype's property
// with the method's `this` as the receiver (Reflect.get); `super.m(...)`
// calls it on `this`; `super.x = v` stores through the prototype to `this`
// (Reflect.set), its value v. A `super` the literal's methods do not hold
// (a class's) is left alone.
func rewriteObjectLiteralSuper(prog *ast.Program, fileIdx int, compatJS bool) {
	count := 0
	any := func() ast.TypeNode { return &ast.KeywordType{Keyword: "any"} }
	reflect := func(m string, args []ast.Expression, pos ast.Pos) ast.Expression {
		return ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Reflect", pos), m, pos), args, pos)
	}
	var rwFn func(n ast.Node, proto func(ast.Pos) ast.Expression) ast.Node
	var rw func(n ast.Node) ast.Node
	// rwFn rewrites one method's body, proto giving the object super reads.
	rwFn = func(n ast.Node, proto func(ast.Pos) ast.Expression) ast.Node {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return rw(n) // its own `this`; its literals their own homes
		case *ast.ObjectLiteral:
			return rw(n)
		case *ast.CallExpression:
			if m, ok := x.Callee.(*ast.MemberExpression); ok {
				if _, sup := m.Object.(*ast.SuperExpression); sup {
					for i := range x.Args {
						x.Args[i] = rwFn(x.Args[i], proto).(ast.Expression)
					}
					pos := x.GetPos()
					proto := proto(pos)
					fn := reflect("get", []ast.Expression{proto, ast.NewStringLiteral(m.Property, pos), ast.NewThisExpression(pos)}, pos)
					return ast.NewCallExpression(ast.NewMemberExpression(fn, "call", pos), append([]ast.Expression{ast.NewThisExpression(pos)}, x.Args...), pos)
				}
			}
			if ix, ok := x.Callee.(*ast.IndexExpression); ok {
				if _, sup := ix.Object.(*ast.SuperExpression); sup {
					// `super[k](...)`: the method read as `super[k]` is,
					// called on this.
					pos := x.GetPos()
					fn := rwFn(ix, proto).(ast.Expression)
					for i := range x.Args {
						x.Args[i] = rwFn(x.Args[i], proto).(ast.Expression)
					}
					return ast.NewCallExpression(ast.NewMemberExpression(fn, "call", pos), append([]ast.Expression{ast.NewThisExpression(pos)}, x.Args...), pos)
				}
			}
		case *ast.AssignmentExpression:
			if key, ok := superKey(x.Left); ok && x.Op != "&&=" && x.Op != "||=" && x.Op != "??=" {
				pos := x.GetPos()
				key = rwFn(key, proto).(ast.Expression)
				rhs := rwFn(x.Right, proto).(ast.Expression)
				if x.Op == "=" {
					return superTemplate("((__kml_sk: any, __kml_sp: any, __kml_sv: any) => (Reflect.set(__kml_sp, __kml_sk, __kml_sv, this), __kml_sv))(KEY, PROTO, RHS)", key, proto(pos), rhs, pos)
				}
				op := strings.TrimSuffix(x.Op, "=")
				return superTemplate("((__kml_sk: any, __kml_sp: any) => { const __kml_sr: any = Reflect.get(__kml_sp, __kml_sk, this) "+op+" RHS; Reflect.set(__kml_sp, __kml_sk, __kml_sr, this); return __kml_sr; })(KEY, PROTO)", key, proto(pos), rhs, pos)
			}
		case *ast.UnaryExpression:
			if key, ok := superKey(x.Arg); ok && x.Op == "delete" {
				// The key is evaluated, never converted; a super reference
				// cannot be deleted.
				pos := x.GetPos()
				key = rwFn(key, proto).(ast.Expression)
				return superTemplate("((__kml_sk: any): boolean => { throw new ReferenceError(\"Unsupported reference to 'super'\"); })(KEY)", key, nil, nil, pos)
			}
		case *ast.UpdateExpression:
			if key, ok := superKey(x.Arg); ok {
				pos := x.GetPos()
				key = rwFn(key, proto).(ast.Expression)
				ret := "__kml_sold"
				if x.Prefix {
					ret = "__kml_snew"
				}
				return superTemplate("((__kml_sk: any, __kml_sp: any) => { let __kml_snew: any = Reflect.get(__kml_sp, __kml_sk, this); const __kml_sold: any = __kml_snew"+x.Op+"; Reflect.set(__kml_sp, __kml_sk, __kml_snew, this); return "+ret+"; })(KEY, PROTO)", key, proto(pos), nil, pos)
			}
		case *ast.MemberExpression:
			if _, sup := x.Object.(*ast.SuperExpression); sup {
				pos := x.GetPos()
				proto := proto(pos)
				return reflect("get", []ast.Expression{proto, ast.NewStringLiteral(x.Property, pos), ast.NewThisExpression(pos)}, pos)
			}
		case *ast.IndexExpression:
			if _, sup := x.Object.(*ast.SuperExpression); sup {
				// The key is evaluated before the home's prototype is read,
				// and converted to a property key after.
				pos := x.GetPos()
				key := rwFn(x.Index, proto).(ast.Expression)
				proto := proto(pos)
				get := reflect("get", []ast.Expression{proto, ast.NewIdentifier("__kml_superk", pos), ast.NewThisExpression(pos)}, pos)
				fn := ast.NewArrowFunction([]ast.Param{{Name: "__kml_superk", Type: any()}}, nil, get, nil, pos)
				return ast.NewCallExpression(fn, []ast.Expression{key}, pos)
			}
		}
		ast.RewriteChildren(n, func(ch ast.Node) ast.Node { return rwFn(ch, proto) })
		return n
	}
	usesSuper := func(fe *ast.FunctionExpression) bool {
		found := false
		visit := func(root ast.Node) {
			ast.Inspect(root, func(n ast.Node) bool {
				switch n.(type) {
				case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression, *ast.ObjectLiteral:
					return false
				case *ast.SuperExpression:
					found = true
				}
				return !found
			})
		}
		visit(fe.Body)
		for _, p := range fe.Params {
			if p.Default != nil {
				visit(p.Default)
			}
		}
		return found
	}
	usesSuperDecl := func(fd *ast.FunctionDeclaration) bool {
		if fd == nil || fd.Body == nil {
			return false
		}
		return usesSuper(&ast.FunctionExpression{Params: fd.Params, Body: fd.Body})
	}
	rw = func(n ast.Node) ast.Node {
		if cd, ok := n.(*ast.ClassDeclaration); ok && cd.BaseClass == "" && compatJS {
			// A class with no base: an instance member's super is
			// Object.prototype, a static member's Function.prototype.
			ast.RewriteChildren(n, rw)
			protoOf := func(of string) func(ast.Pos) ast.Expression {
				return func(pos ast.Pos) ast.Expression {
					return ast.NewMemberExpression(ast.NewIdentifier(of, pos), "prototype", pos)
				}
			}
			members := append([]*ast.FunctionDeclaration{cd.Constructor}, cd.Methods...)
			for _, m := range members {
				if !usesSuperDecl(m) {
					continue
				}
				of := "Object"
				if m.IsStatic {
					of = "Function"
				}
				m.Body = rwFn(m.Body, protoOf(of)).(*ast.BlockStatement)
				for i := range m.Params {
					if m.Params[i].Default != nil {
						m.Params[i].Default = rwFn(m.Params[i].Default, protoOf(of)).(ast.Expression)
					}
				}
			}
			return n
		}
		lit, ok := n.(*ast.ObjectLiteral)
		if !ok {
			ast.RewriteChildren(n, rw)
			return n
		}
		var methods []*ast.FunctionExpression
		for _, p := range lit.Properties {
			if fe, ok := p.Value.(*ast.FunctionExpression); ok && (fe.Method || p.AccessorKind != "") && fe.Body != nil && usesSuper(fe) {
				methods = append(methods, fe)
			}
		}
		ast.RewriteChildren(n, rw)
		if len(methods) == 0 {
			return n
		}
		home := fmt.Sprintf("__kml_home%d_%d", fileIdx, count)
		count++
		proto := func(pos ast.Pos) ast.Expression {
			return ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), "getPrototypeOf", pos), []ast.Expression{ast.NewIdentifier(home, pos)}, pos)
		}
		for _, fe := range methods {
			fe.Body = rwFn(fe.Body, proto).(*ast.BlockStatement)
			for i := range fe.Params {
				if fe.Params[i].Default != nil {
					fe.Params[i].Default = rwFn(fe.Params[i].Default, proto).(ast.Expression)
				}
			}
		}
		// ((home) => (home = literal, home))()
		pos := lit.GetPos()
		bind := ast.NewSequenceExpression([]ast.Expression{
			ast.NewAssignmentExpression("=", ast.NewIdentifier(home, pos), lit, pos),
			ast.NewIdentifier(home, pos),
		}, pos)
		fn := ast.NewArrowFunction([]ast.Param{{Name: home, Type: any()}}, nil, bind, nil, pos)
		return ast.NewCallExpression(fn, nil, pos)
	}
	ast.RewriteChildren(prog, rw)
}

// superKey is a super property reference's key: `super.x`'s name as a
// string, `super[k]`'s k.
func superKey(x ast.Expression) (ast.Expression, bool) {
	switch t := x.(type) {
	case *ast.MemberExpression:
		if _, ok := t.Object.(*ast.SuperExpression); ok {
			return ast.NewStringLiteral(t.Property, t.GetPos()), true
		}
	case *ast.IndexExpression:
		if _, ok := t.Object.(*ast.SuperExpression); ok {
			return t.Index, true
		}
	}
	return nil, false
}

// superTemplate parses src, a super property write's lowering, with the
// identifiers KEY, PROTO and RHS standing for the given expressions.
func superTemplate(src string, key, proto, rhs ast.Expression, pos ast.Pos) ast.Expression {
	ev, err := parser.ParseEval(src)
	if err != nil || len(ev.Body) != 1 {
		panic("super lowering template: " + src)
	}
	es := ev.Body[0].(*ast.ExpressionStatement)
	var sub func(ast.Node) ast.Node
	sub = func(n ast.Node) ast.Node {
		if id, ok := n.(*ast.Identifier); ok {
			switch id.Name {
			case "KEY":
				return key
			case "PROTO":
				return proto
			case "RHS":
				return rhs
			}
		}
		ast.RewriteChildren(n, sub)
		return n
	}
	return sub(es.Expr).(ast.Expression)
}

// rewriteIndirectEval replaces an indirect eval of a constant string,
// `(0, eval)(src)`, with a call of a program-level function running the
// code, as the global scope does: its result is the completion value, and
// in sloppy code its `var` and function declarations are the program's.
// Source that does not parse throws SyntaxError when the call runs. A file
// declaring its own `eval` keeps its calls.
func rewriteIndirectEval(prog *ast.Program, fileIdx int) {
	declared := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if d, ok := n.(*ast.VarDeclaration); ok && d.Name == "eval" {
			declared = true
		}
		if d, ok := n.(*ast.FunctionDeclaration); ok && d.Name == "eval" {
			declared = true
		}
		return !declared
	})
	if declared {
		return
	}
	var factories []ast.Statement
	var hoists []string
	seen := map[string]bool{}
	var rw func(ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		call, ok := n.(*ast.CallExpression)
		if !ok || len(call.Args) != 1 {
			return n
		}
		// `(0, eval)(…)`, and `eval?.(…)`: an optional call is not a direct
		// eval either.
		var lead []ast.Expression // the callee's other operands, run first
		if id, ok := call.Callee.(*ast.Identifier); !ok || id.Name != "eval" || !call.Optional {
			seq, ok := call.Callee.(*ast.SequenceExpression)
			if !ok || len(seq.Exprs) < 2 || call.Optional {
				return n
			}
			if id, ok := seq.Exprs[len(seq.Exprs)-1].(*ast.Identifier); !ok || id.Name != "eval" {
				return n
			}
			lead = append(lead, seq.Exprs[:len(seq.Exprs)-1]...)
		}
		src, ok := call.Args[0].(*ast.StringLiteral)
		if !ok {
			return n
		}
		pos := call.GetPos()
		var body *ast.BlockStatement
		ev, err := parser.ParseEval(src.Value)
		if err == nil && evalHasTopLevelReturn(ev) {
			err = fmt.Errorf("Illegal return statement")
		}
		if err != nil {
			msg := err.Error()
			if k := strings.Index(msg, ": "); k > 0 && strings.Count(msg[:k], ":") == 1 {
				msg = msg[k+2:]
			}
			th, terr := parser.ParseEval("{ throw new SyntaxError(" + strconv.Quote(msg) + "); }")
			if terr != nil {
				return n
			}
			body, _ = th.Body[0].(*ast.BlockStatement)
			if body == nil {
				return n
			}
		} else {
			stmts := ev.Body
			if !programIsStrictBody(stmts) {
				names, funcs, rest := sloppyEvalDecls(stmts, pos)
				for _, nm := range names {
					if !seen[nm] {
						seen[nm] = true
						hoists = append(hoists, nm)
					}
				}
				stmts = append(funcs, rest...)
			}
			body = ast.NewBlockStatement(stmts, pos)
			const cv = "__kml_evalcv"
			withCompletion(body, cv)
			body.Body = append([]ast.Statement{ast.NewVarDeclaration("let", cv, &ast.KeywordType{Keyword: "any"}, nil, pos)}, body.Body...)
			body.Body = append(body.Body, ast.NewReturnStatement(ast.NewIdentifier(cv, pos), pos))
		}
		name := fmt.Sprintf("__kml_ieval%d_%d", fileIdx, len(factories))
		fp, perr := parser.Parse("function " + name + "(): any {\nreturn (() => {})();\n}")
		if perr != nil || len(fp.Body) != 1 {
			return n
		}
		fd, _ := fp.Body[0].(*ast.FunctionDeclaration)
		if fd == nil || fd.Body == nil || len(fd.Body.Body) != 1 {
			return n
		}
		ret, _ := fd.Body.Body[0].(*ast.ReturnStatement)
		inner, _ := ret.Value.(*ast.CallExpression)
		arrow, _ := inner.Callee.(*ast.ArrowFunction)
		if arrow == nil {
			return n
		}
		arrow.Block = body
		if prog.EvalCode == nil {
			prog.EvalCode = map[ast.Expression]bool{}
		}
		prog.EvalCode[arrow] = true
		factories = append(factories, fd)
		// The callee's other operands still run, in order.
		lead = append(lead, ast.NewCallExpression(ast.NewIdentifier(name, pos), nil, pos))
		if len(lead) == 1 {
			return lead[0]
		}
		return ast.NewSequenceExpression(lead, pos)
	}
	ast.RewriteChildren(prog, rw)
	if len(factories) == 0 {
		return
	}
	var decls []ast.Statement
	for _, nm := range hoists {
		decls = append(decls, ast.NewVarDeclaration("var", nm, &ast.KeywordType{Keyword: "any"}, nil, ast.Pos{}))
	}
	prog.Body = append(append(decls, prog.Body...), factories...)
}

// rewriteFunctionConstructor replaces `Function(p…, body)` and `new
// Function(p…, body)`, every argument a constant string, with a call of a
// top-level factory returning the function that source makes (`function
// anonymous(p…) { body }`), so the binder and checker see it as the
// program's own code. The factory is top-level, added after the file's
// names are renamed, because a Function body closes over the global scope,
// not its caller's or its module's; each call makes a new
// function, as each Function call does. Source that does not parse throws
// SyntaxError when the call runs. A file declaring its own `Function`
// keeps its calls.
func rewriteFunctionConstructor(prog *ast.Program, fileIdx int) {
	declared := false
	ast.Inspect(prog, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.VarDeclaration:
			declared = declared || d.Name == "Function"
		case *ast.FunctionDeclaration:
			declared = declared || d.Name == "Function"
			for _, p := range d.Params {
				declared = declared || p.Name == "Function"
			}
		case *ast.ClassDeclaration:
			declared = declared || d.Name == "Function"
		case *ast.ArrowFunction:
			for _, p := range d.Params {
				declared = declared || p.Name == "Function"
			}
		case *ast.FunctionExpression:
			for _, p := range d.Params {
				declared = declared || p.Name == "Function"
			}
		}
		return !declared
	})
	if declared {
		return
	}
	specializeFunctionCtorParams(prog)
	var factories []ast.Statement
	var rewrite func(ast.Node) ast.Node
	rewrite = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rewrite)
		var args []ast.Expression
		var pos ast.Pos
		switch x := n.(type) {
		case *ast.CallExpression:
			id, ok := x.Callee.(*ast.Identifier)
			if !ok || id.Name != "Function" || x.Optional {
				return n
			}
			args, pos = x.Args, x.GetPos()
		case *ast.NewExpression:
			if x.Callee != nil || x.Qualified || x.ClassName != "Function" || len(x.TypeArgs) > 0 {
				return n
			}
			args, pos = x.Args, x.GetPos()
		default:
			return n
		}
		strs := make([]string, len(args))
		for i, a := range args {
			switch a := a.(type) {
			case *ast.StringLiteral:
				strs[i] = a.Value
				continue
			case *ast.TemplateLiteral:
				if len(a.Exprs) == 0 && len(a.Quasis) == 1 {
					strs[i] = a.Quasis[0]
					continue
				}
			}
			// Source known only when the call runs: its arguments are
			// converted to strings in order, then compiling them would
			// need an embedded JavaScript engine.
			th, terr := parser.ParseEval("((...a: any[]): Function => { for (var i = 0; i < a.length; i++) String(a[i]); throw new EvalError(\"Function of a string computed at run time is not supported: it needs an embedded JavaScript engine\"); })()")
			if terr != nil {
				return n
			}
			ts, ok := th.Body[0].(*ast.ExpressionStatement)
			if !ok {
				return n
			}
			call := ts.Expr.(*ast.CallExpression)
			call.Args = args
			return call
		}
		body := ""
		if len(strs) > 0 {
			body, strs = strs[len(strs)-1], strs[:len(strs)-1]
		}
		name := fmt.Sprintf("__kml_fnctor%d_%d", fileIdx, len(factories))
		src := "function " + name + "(): Function {\nreturn function anonymous(" + strings.Join(strs, ",") + "\n) {\n" + body + "\n};\n}"
		fp, err := parser.Parse(src)
		var fd *ast.FunctionDeclaration
		if err == nil && len(fp.Body) == 1 {
			fd, _ = fp.Body[0].(*ast.FunctionDeclaration)
		}
		msg := "Invalid or unexpected token"
		if fd != nil {
			// A Function's text is script code: import.meta is an early
			// SyntaxError in it.
			ast.Inspect(fd, func(n ast.Node) bool {
				if _, ok := n.(*ast.ImportMetaUrl); ok && fd != nil {
					fd, msg = nil, "Cannot use 'import.meta' outside a module"
				}
				return fd != nil
			})
		}
		if fd == nil {
			if err != nil {
				msg = err.Error()
				if i := strings.Index(msg, ": "); i > 0 && strings.Count(msg[:i], ":") == 1 {
					msg = msg[i+2:]
				}
			}
			// A `Function`, as tsc types the call.
			if th, terr := parser.ParseEval("((): Function => { throw new SyntaxError(" + strconv.Quote(msg) + "); })()"); terr == nil {
				if ts, ok := th.Body[0].(*ast.ExpressionStatement); ok {
					return ts.Expr
				}
			}
			return n
		}
		// The function is code TypeScript never checks, and the call is a
		// `Function`, as tsc types it.
		if ret, ok := fd.Body.Body[0].(*ast.ReturnStatement); ok && ret.Value != nil {
			if fe, ok := ret.Value.(*ast.FunctionExpression); ok {
				// Its parameters are JavaScript's: any value.
				for i := range fe.Params {
					if fe.Params[i].Type == nil {
						fe.Params[i].Type = &ast.KeywordType{Keyword: "any"}
					}
				}
			}
			if prog.EvalCode == nil {
				prog.EvalCode = map[ast.Expression]bool{}
			}
			prog.EvalCode[ret.Value] = true
		}
		factories = append(factories, fd)
		return ast.NewAsExpression(ast.NewCallExpression(ast.NewIdentifier(name, pos), nil, pos), &ast.TypeReference{Name: "Function"}, pos)
	}
	ast.RewriteChildren(prog, rewrite)
	prog.Body = append(prog.Body, factories...)
}

// evalCallsInClassInitializers is the set of eval calls in a class field's
// initializer or static block, through arrow functions only.
func evalCallsInClassInitializers(prog *ast.Program) map[*ast.CallExpression]bool {
	out := map[*ast.CallExpression]bool{}
	collect := func(root ast.Node) {
		ast.Inspect(root, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassExpression, *ast.ClassDeclaration:
				return false // its own arguments, or its own initializers
			case *ast.CallExpression:
				if id, ok := x.Callee.(*ast.Identifier); ok && id.Name == "eval" {
					out[x] = true
				}
			}
			return true
		})
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			for _, f := range cd.Fields {
				if f.Initializer != nil {
					collect(f.Initializer)
				}
			}
			for _, b := range cd.StaticBlocks {
				collect(b)
			}
		}
		return true
	})
	return out
}

// readsOwnArguments reports a read of `arguments` in expr outside any
// function of its own (arrows have none).
func readsOwnArguments(expr ast.Expression) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassExpression, *ast.ClassDeclaration:
			return false
		case *ast.Identifier:
			found = found || x.Name == "arguments"
		}
		return !found
	})
	return found
}

// onlySelfWorker reports whether every worker the file starts is itself.
func onlySelfWorker(prog *ast.Program, path string) bool {
	for _, wp := range prog.WorkerPaths {
		if filepath.Base(wp) != filepath.Base(path) {
			return false
		}
	}
	return true
}

// callableMarkers are the class names a builtin module's source marks
// `// kml:callable <Name>`: Node's function-style constructors, which
// construct when called without `new`.
func callableMarkers(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "// kml:callable "); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				out = append(out, f[0])
			}
		}
	}
	return out
}

// sortedGlobalNames is lib.GlobalModuleNames' names in order, so a
// program's modules are visited deterministically.
func sortedGlobalNames() []string {
	var names []string
	for n := range lib.GlobalModuleNames() {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// codeIdents returns the identifier-shaped words of src outside comments and
// quoted strings (a template literal's text counts: its substitutions are
// code), so a name a comment mentions brings nothing in.
func codeIdents(src []byte) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			i += end + 4
		case c == '\'' || c == '"':
			for i++; i < len(src) && src[i] != c && src[i] != '\n'; i++ {
				if src[i] == '\\' {
					i++
				}
			}
			i++
		case isIdentByte(c):
			start := i
			for i < len(src) && isIdentByte(src[i]) {
				i++
			}
			out[string(src[start:i])] = true
		default:
			i++
		}
	}
	return out
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// declaresTopLevel reports whether prog declares or imports name at its top
// level.
func declaresTopLevel(prog *ast.Program, name string) bool {
	for _, st := range prog.Body {
		for _, r := range declRefsOf(st) {
			if r.Name == name {
				return true
			}
		}
		if imp, ok := st.(*ast.ImportDeclaration); ok {
			if imp.Namespace == name {
				return true
			}
			for _, sp := range imp.Specifiers {
				// `import { Blob } from 'buffer'` names the global itself
				// (the import is erased), no binding of its own.
				if sp.Local == name && !(sp.Imported == name && globalReexportModules[imp.Source][name]) {
					return true
				}
			}
		}
	}
	return false
}

// companionMangled maps each export of the TypeScript companion of the
// builtin module spec (lib.CompanionPath) to its name in the merged
// program, or is nil when the module has none.
func companionMangled(files map[string]*fileInfo, spec string) map[string]string {
	cp, ok := lib.CompanionPath(spec)
	if !ok || files[cp] == nil {
		return nil
	}
	companion := map[string]string{}
	for name := range files[cp].exported {
		if strings.HasPrefix(name, "_kml") {
			continue // shared among the builtin modules only
		}
		companion[name], _ = files[cp].publicMangled(name)
	}
	return companion
}

var (
	processWordRE = regexp.MustCompile(`(^|[^.\w$])process\b(\s*\??\.)?`)
)

// globalMemberREs match a read of a builtin global's member, per global.
var globalMemberREs sync.Map

func globalMemberRE(global string) *regexp.Regexp {
	if re, ok := globalMemberREs.Load(global); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(global) + `\s*\??\.\s*([A-Za-z_$][\w$]*)`)
	globalMemberREs.Store(global, re)
	return re
}

// loweredMemberModules are the builtin modules the file at path (src, its
// identifiers idents) needs for the builtin globals' members it reads,
// sorted: each lowered member's module (lib.LoweredMembers), process's
// emitter module when it uses `process` as a value or a member no module
// lowers, and the environment
// module for process.env.
func loweredMemberModules(prog *ast.Program, path string, src []byte, idents map[string]bool) []string {
	need := map[string]bool{}
	for global, members := range lib.LoweredMembers() {
		if !idents[global] || declaresTopLevel(prog, global) {
			continue
		}
		for _, m := range globalMemberRE(global).FindAllSubmatch(src, -1) {
			if mod, ok := members[string(m[1])]; ok {
				need[mod] = true
			} else if global == "process" && string(m[1]) != "env" && !strings.HasPrefix(path, lib.ModuleRoot) {
				// A member no library function lowers (`process.maxTickDepth
				// = 10`, an own property): the process object's.
				need[lib.ModuleRoot+"internal_process.ts"] = true
			}
			if global == "process" && string(m[1]) == "env" {
				need[lib.ModuleRoot+"internal_process_env.ts"] = true
			}
		}
	}
	if idents["process"] && !declaresTopLevel(prog, "process") && !strings.HasPrefix(path, lib.ModuleRoot) {
		for _, m := range processWordRE.FindAllSubmatch(src, -1) {
			if len(m[2]) == 0 {
				need[lib.ModuleRoot+"internal_process.ts"] = true
				break
			}
		}
	}
	delete(need, path)
	out := make([]string, 0, len(need))
	for mod := range need {
		out = append(out, mod)
	}
	sort.Strings(out)
	return out
}

// assertModules are the specifiers whose ok quotes its call's source.
var assertModules = map[string]bool{"assert": true, "node:assert": true, "assert/strict": true, "node:assert/strict": true}

// markAssertCallSites marks the calls of assert's ok in prog — through the
// module's default export, a namespace import, or an imported `ok` or
// `strict` — so their callee receives the call's source (assert.ok's
// generated message quotes it, as Node reads it off the stack).
func markAssertCallSites(prog *ast.Program) {
	roots := map[string]bool{} // the module itself: `assert(x)`, `assert.ok(x)`
	fns := map[string]bool{}   // an imported ok or strict: `ok(x)`
	for _, st := range prog.Body {
		imp, ok := st.(*ast.ImportDeclaration)
		if !ok || !assertModules[imp.Source] {
			continue
		}
		if imp.Namespace != "" {
			roots[imp.Namespace] = true
		}
		for _, sp := range imp.Specifiers {
			switch sp.Imported {
			case "default":
				roots[sp.Local] = true
			case "ok", "strict":
				fns[sp.Local] = true
			}
		}
	}
	if len(roots) == 0 && len(fns) == 0 {
		return
	}
	isRoot := func(e ast.Expression) bool {
		id, ok := e.(*ast.Identifier)
		return ok && roots[id.Name]
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpression)
		if !ok {
			return true
		}
		switch c := call.Callee.(type) {
		case *ast.Identifier:
			call.PassSource = roots[c.Name] || fns[c.Name]
		case *ast.MemberExpression:
			if c.Property == "ok" || c.Property == "strict" {
				if isRoot(c.Object) {
					call.PassSource = true
				} else if inner, ok := c.Object.(*ast.MemberExpression); ok && inner.Property == "strict" && isRoot(inner.Object) {
					call.PassSource = true
				}
			}
		}
		return true
	})
}

// jsonAtRunTime reports whether prog calls JSON.stringify with a replacer
// that is not null or a space that is not a literal, JSON.parse with a
// reviver, or takes either by value: what lib/node/internal_json.ts runs.
func jsonAtRunTime(prog *ast.Program) bool {
	direct := map[ast.Node]bool{}
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ast.CallExpression:
			if IsJSONStringify(x.Callee) {
				direct[x.Callee] = true
				found = StringifyArgsAtRunTime(x.Args)
			} else if isJSONMember(x.Callee, "parse") {
				direct[x.Callee] = true
				found = ParseArgsAtRunTime(x.Args)
			}
		case *ast.MemberExpression:
			if (IsJSONStringify(x) || isJSONMember(x, "parse")) && !direct[x] {
				found = true
			}
		}
		return true
	})
	return found
}

// ParseArgsAtRunTime reports whether JSON.parse(args...) revives: a second
// argument other than null or undefined.
func ParseArgsAtRunTime(args []ast.Expression) bool {
	for _, a := range args {
		if _, ok := a.(*ast.SpreadElement); ok {
			return true
		}
	}
	if len(args) >= 2 {
		if _, ok := args[1].(*ast.NullLiteral); !ok {
			return true
		}
	}
	return false
}

func isJSONMember(x ast.Expression, prop string) bool {
	m, ok := x.(*ast.MemberExpression)
	if !ok || m.Property != prop || m.Optional {
		return false
	}
	id, ok := m.Object.(*ast.Identifier)
	return ok && id.Name == "JSON"
}

// IsJSONStringify reports whether x is `JSON.stringify`.
func IsJSONStringify(x ast.Expression) bool {
	return isJSONMember(x, "stringify")
}

// StringifyArgsAtRunTime reports whether JSON.stringify(args...) needs the
// run-time serializer: a replacer other than null or undefined, or a space
// other than a number or string literal.
func StringifyArgsAtRunTime(args []ast.Expression) bool {
	for _, a := range args {
		if _, ok := a.(*ast.SpreadElement); ok {
			return true
		}
	}
	if len(args) >= 2 {
		if _, ok := args[1].(*ast.NullLiteral); !ok {
			return true
		}
	}
	if len(args) >= 3 {
		switch args[2].(type) {
		case *ast.NumberLiteral, *ast.StringLiteral, *ast.NullLiteral:
		default:
			return true
		}
	}
	return false
}

// NamespaceValueNames are the builtin namespace objects a library module
// makes when one is read as a value: each one's module and maker, as
// "module:maker" (a module each, so a program compiles only the ones it
// reads).
var NamespaceValueNames = map[string]string{
	"Math": "internal_namespaces:_kmlMath", "JSON": "internal_ns_json:_kmlJSON",
	"console": "internal_ns_console:_kmlConsole", "Reflect": "internal_ns_reflect:_kmlReflect",
	"Atomics": "internal_ns_atomics:_kmlAtomics", "globalThis": "internal_global_object:_kmlGlobalThis",
}

// namespaceModule is the library module path that makes namespace name's
// object.
func namespaceModule(name string) string {
	mod, _, _ := strings.Cut(NamespaceValueNames[name], ":")
	return lib.ModuleRoot + mod + ".ts"
}

var namespaceWordRE = regexp.MustCompile(`(^|[^.\w$])(Math|JSON|console|Reflect|Atomics)\b(\s*\??\.\s*([A-Za-z_$][\w$]*))?`)

// namespaceIfaces are the library interfaces declaring each namespace's
// members: a read of another member (`Atomics.wake`) is of the object.
var namespaceIfaces = map[string]string{"Math": "Math", "JSON": "JSON", "console": "Console", "Atomics": "Atomics"}

// namespaceValueUses is each namespace src may read as a value: its name
// not followed by a member access, a member its library interface lacks,
// or a write to one of its members (`Math.PI = 20`), which is to the
// object.
func namespaceValueUses(src []byte, idents map[string]bool) map[string]bool {
	any := false
	for name := range NamespaceValueNames {
		any = any || idents[name]
	}
	if !any {
		return nil
	}
	out := map[string]bool{}
	for _, m := range namespaceWordRE.FindAllSubmatch(src, -1) {
		name := string(m[2])
		if len(m[3]) == 0 {
			out[name] = true
			continue
		}
		if iface, ok := namespaceIfaces[name]; ok {
			if known, has := lib.TSInterfaceHas(iface, string(m[4])); known && !has {
				out[name] = true
			}
		}
	}
	for _, m := range namespaceMemberWriteRE.FindAllSubmatch(src, -1) {
		out[string(m[2])] = true
	}
	return out
}

var namespaceMemberWriteRE = regexp.MustCompile(`(^|[^.\w$])(Math|JSON|console|Reflect|Atomics)\s*\.\s*[A-Za-z_$][\w$]*\s*(\*\*|<<|>>>?|&&|\|\||\?\?|[-+*/%&|^])?=([^=]|$)`)

// consoleDirAtRunTime reports whether prog calls console.dir with options
// only known at run time (DirOptionsAtRunTime).
func consoleDirAtRunTime(prog *ast.Program) bool {
	isDir := func(x ast.Expression) bool {
		m, ok := x.(*ast.MemberExpression)
		if !ok || m.Property != "dir" {
			return false
		}
		id, ok := m.Object.(*ast.Identifier)
		return ok && id.Name == "console"
	}
	direct := map[ast.Node]bool{}
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpression:
			if isDir(x.Callee) {
				direct[x.Callee] = true
				found = found || DirOptionsAtRunTime(x.Args)
			}
		case *ast.MemberExpression:
			found = found || isDir(x) && !direct[x] // console.dir by value
		}
		return !found
	})
	return found
}

// DirOptionsAtRunTime reports whether console.dir(args...)'s options are not
// an object literal of literal values the compiler reads itself.
func DirOptionsAtRunTime(args []ast.Expression) bool {
	if len(args) < 2 {
		return false
	}
	ol, ok := args[1].(*ast.ObjectLiteral)
	if !ok {
		return true
	}
	for _, p := range ol.Properties {
		switch p.Value.(type) {
		case *ast.NumberLiteral, *ast.NullLiteral, *ast.BooleanLiteral, *ast.StringLiteral:
		default:
			return true
		}
	}
	return false
}

var globalThisRE = regexp.MustCompile(`(^|[^.\w$])globalThis\b(\s*\??\.\s*([A-Za-z_$][\w$]*))?`)

// globalObjectUse reports whether src may use the global object itself:
// `globalThis` as a value, or a property of it that no global declares.
// ctorFunctionCall reports a function declaration both constructed with
// `new` and called plainly, whose plain call receives globalThis as
// `this`.
func ctorFunctionCall(src []byte) bool {
	if !bytes.Contains(src, []byte("this")) {
		return false
	}
	seen := map[string]bool{}
	for _, m := range newCalleeRE.FindAllSubmatch(src, -1) {
		name := string(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		declared, called := scanCtorName(src, name)
		if declared && called {
			return true
		}
	}
	return false
}

// scanCtorName reports, of name's whole-word occurrences in src, whether one
// is a function declaration's (`function name (`) and whether one is a
// plain call: `name (`, not after `.`, a word character or `$`, and not a
// declaration's or a `new`'s (`new name`, `new (name`).
func scanCtorName(src []byte, name string) (declared, called bool) {
	word := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' }
	for off := 0; ; {
		k := bytes.Index(src[off:], []byte(name))
		if k < 0 {
			return
		}
		i := off + k
		off = i + 1
		end := i + len(name)
		if i > 0 && word(src[i-1]) || end < len(src) && word(src[end]) {
			continue // not a whole word
		}
		a := end
		for a < len(src) && space(src[a]) {
			a++
		}
		isCall := a < len(src) && src[a] == '('
		// The keyword before it, across spaces and one `(`.
		j := i
		for j > 0 && space(src[j-1]) {
			j--
		}
		gap := j < i
		paren := false
		if j > 0 && src[j-1] == '(' {
			paren = true
			j--
			for j > 0 && space(src[j-1]) {
				j--
				gap = true
			}
		}
		kw := func(w string) bool {
			s := j - len(w)
			return s >= 0 && string(src[s:j]) == w && (s == 0 || !word(src[s-1])) && (gap || paren)
		}
		switch {
		case kw("function"):
			if !paren && isCall {
				declared = true
			}
			continue
		case kw("new"):
			continue
		}
		if isCall && (i == 0 || src[i-1] != '.' && src[i-1] != '$' && !word(src[i-1])) {
			called = true
		}
		if declared && called {
			return
		}
	}
}

var newCalleeRE = regexp.MustCompile(`\bnew\s+\(?\s*([A-Za-z_$][\w$]*)`)

var (
	assignTargetRE = regexp.MustCompile(`(?:^|[;{}(\n])\s*([A-Za-z_$][\w$]*)\s*(?:[-+*/%&|^]|\*\*|<<|>>>?|&&|\|\||\?\?)?=([^=]|$)`)
	declaredNameRE = regexp.MustCompile(`\b(?:var|let|const|function\*?|class)\s+([A-Za-z_$][\w$]*)`)
)

// undeclaredAssign reports a sloppy-mode script (`.js`, `.cjs`) that
// assigns a name it never declares: in sloppy code that makes a property of
// the global object. A text scan finds the candidates; the binder confirms
// one is a store to a name no scope declares (a parameter or a catch
// binding is declared, which the scan cannot see).
func undeclaredAssign(path string, src []byte, prog *ast.Program) bool {
	if !undeclaredAssignText(path, src) {
		return false
	}
	targets := map[*ast.Identifier]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignmentExpression:
			if id, ok := n.Left.(*ast.Identifier); ok {
				targets[id] = true
			}
		case *ast.UpdateExpression:
			if id, ok := n.Arg.(*ast.Identifier); ok {
				targets[id] = true
			}
		}
		return true
	})
	b := binder.Bind(prog)
	globals := tsGlobalValues()
	for _, id := range b.References() {
		if !targets[id] && !b.WriteOnly(id) || globals[id.Name] {
			continue
		}
		if sym, ok := b.Resolve(id); ok && sym == nil {
			return true
		}
	}
	return false
}

// undeclaredAssignText is undeclaredAssign's text scan: an assignment to a
// name no var, let, const, function or class declares.
func undeclaredAssignText(path string, src []byte) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".js" && ext != ".cjs" {
		return false
	}
	declared := map[string]bool{}
	for _, m := range declaredNameRE.FindAllSubmatch(src, -1) {
		declared[string(m[1])] = true
	}
	globals := tsGlobalValues()
	for _, m := range assignTargetRE.FindAllSubmatch(src, -1) {
		n := string(m[1])
		if !declared[n] && !globals[n] && n != "this" {
			return true
		}
	}
	return false
}

// globalizePatternTargets makes each undeclared name a sloppy script's
// destructuring assignments target the global object's property (`[q] =
// [5]` creates globalThis.q, as `q = 5` does), reporting whether it did.
func globalizePatternTargets(path string, src []byte, prog *ast.Program) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".js" && ext != ".cjs" || !patternAssignRE.Match(src) || IsModule(prog) {
		return false
	}
	if t := bytes.TrimLeft(src, " \t\r\n"); bytes.HasPrefix(t, []byte(`"use strict"`)) || bytes.HasPrefix(t, []byte(`'use strict'`)) {
		return false
	}
	targets := map[*ast.Identifier]bool{}
	var pats []ast.Expression
	ast.Inspect(prog, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignmentExpression); ok && as.Op == "=" {
			switch as.Left.(type) {
			case *ast.ArrayLiteral, *ast.ObjectLiteral:
				patternTargets(as.Left, targets)
				pats = append(pats, as.Left)
			}
		}
		return true
	})
	if len(targets) == 0 {
		return false
	}
	b := binder.Bind(prog)
	globals := tsGlobalValues()
	undeclared := map[*ast.Identifier]bool{}
	for _, id := range b.References() {
		if targets[id] && !globals[id.Name] {
			if sym, ok := b.Resolve(id); ok && sym == nil {
				undeclared[id] = true
			}
		}
	}
	if len(undeclared) == 0 {
		return false
	}
	var leaf func(x ast.Expression) ast.Expression
	leaf = func(x ast.Expression) ast.Expression {
		switch t := x.(type) {
		case *ast.Identifier:
			if undeclared[t] {
				return ast.NewMemberExpression(ast.NewIdentifier("globalThis", t.GetPos()), t.Name, t.GetPos())
			}
		case *ast.AssignmentExpression:
			t.Left = leaf(t.Left)
		case *ast.SpreadElement:
			t.Arg = leaf(t.Arg)
		case *ast.ArrayLiteral:
			for i := range t.Elements {
				if t.Elements[i] != nil {
					t.Elements[i] = leaf(t.Elements[i])
				}
			}
		case *ast.ObjectLiteral:
			for i := range t.Properties {
				p := &t.Properties[i]
				if p.AccessorKind != "" || p.Value == nil {
					continue
				}
				before := p.Value
				if as, ok := before.(*ast.AssignmentExpression); ok {
					before = as.Left
				}
				p.Value = leaf(p.Value)
				after := p.Value
				if as, ok := after.(*ast.AssignmentExpression); ok {
					after = as.Left
				}
				if after != before {
					p.Shorthand = false
				}
			}
		}
		return x
	}
	for _, p := range pats {
		leaf(p)
	}
	return true
}

var patternAssignRE = regexp.MustCompile(`[\]}]\s*\)?\s*=[^=>]`)

// patternTargets adds a destructuring assignment pattern's identifier
// targets to targets.
func patternTargets(x ast.Expression, targets map[*ast.Identifier]bool) {
	switch t := x.(type) {
	case *ast.Identifier:
		targets[t] = true
	case *ast.AssignmentExpression:
		patternTargets(t.Left, targets)
	case *ast.SpreadElement:
		patternTargets(t.Arg, targets)
	case *ast.ArrayLiteral:
		for _, el := range t.Elements {
			if el != nil {
				patternTargets(el, targets)
			}
		}
	case *ast.ObjectLiteral:
		for _, p := range t.Properties {
			if p.AccessorKind == "" && p.Value != nil {
				patternTargets(p.Value, targets)
			}
		}
	}
}

// sloppyThisUse reports whether a script may read a sloppy-mode function's
// `this`, which is the global object when the call supplies none.
func sloppyThisUse(path string, src []byte, prog *ast.Program) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".js" && ext != ".cjs" || !thisWordRE.Match(src) || moduleSyntaxRE.Match(src) {
		return false
	}
	t := bytes.TrimLeft(src, " \t\r\n")
	if bytes.HasPrefix(t, []byte(`"use strict"`)) || bytes.HasPrefix(t, []byte(`'use strict'`)) {
		return false
	}
	// A `this` read in a function that is sloppy code (no class body, no
	// "use strict" of its own); an arrow's is its enclosing function's. A
	// script's top-level `this` is module.exports, and the word in a
	// comment or a string is no read.
	found := false
	var walk func(n ast.Node, inFn bool)
	walk = func(root ast.Node, inFn bool) {
		ast.Inspect(root, func(n ast.Node) bool {
			if found {
				return false
			}
			if n == root {
				return true
			}
			switch x := n.(type) {
			case *ast.ClassDeclaration, *ast.ClassExpression:
				return false
			case *ast.FunctionDeclaration:
				if x.Body != nil && !ast.HasUseStrictDirective(x.Body.Body) {
					walk(x, true)
				}
				return false
			case *ast.FunctionExpression:
				if x.Body != nil && !ast.HasUseStrictDirective(x.Body.Body) {
					walk(x, true)
				}
				return false
			case *ast.ThisExpression:
				found = found || inFn
			}
			return true
		})
	}
	walk(prog, false)
	return found
}

var (
	thisWordRE     = regexp.MustCompile(`\bthis\b`)
	moduleSyntaxRE = regexp.MustCompile(`(?m)^\s*(import|export)\b`)
)

func globalObjectUse(src []byte) bool {
	declared := tsGlobalValues()
	for _, m := range globalThisRE.FindAllSubmatch(src, -1) {
		if len(m[2]) == 0 || !declared[string(m[3])] {
			return true
		}
	}
	return false
}

var (
	tsGlobalValueSet  map[string]bool
	tsGlobalValueOnce sync.Once
)

func tsGlobalValues() map[string]bool {
	tsGlobalValueOnce.Do(func() {
		tsGlobalValueSet = map[string]bool{}
		names, values := lib.GlobalNames()
		for i, n := range names {
			if values[i] {
				tsGlobalValueSet[n] = true
			}
		}
	})
	return tsGlobalValueSet
}

var cryptoWordRE = regexp.MustCompile(`(^|[^.\w$])crypto\b(\s*\??\.\s*(getRandomValues|randomUUID)\b(\s*\()?)?(\s*\??\.)?`)

// cryptoValueUse reports whether src may read the Web Crypto global as a
// value (`const c = crypto`), or take one of its methods by value.
var globalThisCryptoRE = regexp.MustCompile(`globalThis\s*\??\.\s*crypto\b`)

func cryptoValueUse(src []byte) bool {
	if globalThisCryptoRE.Match(src) {
		return true // `globalThis.crypto`: the global object's crypto, a value
	}
	for _, m := range cryptoWordRE.FindAllSubmatch(src, -1) {
		bare := len(m[2]) == 0 && len(m[5]) == 0
		byValue := len(m[2]) > 0 && len(m[4]) == 0
		if bare || byValue {
			return true
		}
	}
	return false
}

// appendModule appends one module's top-level statements to the merged
// body, recording each top-level `return` (outside any function) against
// the module's last statement (ast.Program.TopLevelReturns).
func appendModule(merged *ast.Program, stmts []ast.Statement) []ast.Statement {
	if len(stmts) > 0 {
		last := stmts[len(stmts)-1]
		for _, st := range stmts {
			ast.Inspect(st, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
					return false
				case *ast.ReturnStatement:
					if merged.TopLevelReturns == nil {
						merged.TopLevelReturns = map[*ast.ReturnStatement]ast.Statement{}
					}
					merged.TopLevelReturns[x] = last
				}
				return true
			})
		}
	}
	return append(merged.Body, stmts...)
}

// optionalDeletedFields makes each class instance field that a `delete`
// names (`delete o.f`, `delete o["f"]`; every field, for a key computed at
// run time) an optional one, whose presence is tracked: in JavaScript a
// field is a configurable own property, which delete removes.
func optionalDeletedFields(prog *ast.Program) {
	deleted, all := map[string]bool{}, false
	ast.Inspect(prog, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpression)
		if !ok || u.Op != "delete" {
			return true
		}
		switch a := u.Arg.(type) {
		case *ast.MemberExpression:
			deleted[a.Property] = true
		case *ast.IndexExpression:
			if s, ok := a.Index.(*ast.StringLiteral); ok {
				deleted[s.Value] = true
			} else if _, num := a.Index.(*ast.NumberLiteral); !num {
				all = true
			}
		}
		return true
	})
	if len(deleted) == 0 && !all {
		return
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok {
			return true
		}
		for i := range cd.Fields {
			f := &cd.Fields[i]
			// (A computed key is named later, possibly a deleted one.)
			if (all || deleted[f.Name] || f.Key != nil || f.KeyExpr != nil) && !f.Static && !strings.HasPrefix(f.Name, "#") {
				f.Optional = true
			}
		}
		return true
	})
}

// varArgumentsAsBinding makes a sloppy script's `var arguments` in a
// non-arrow function the function's own arguments binding, as JavaScript
// does: a bare `var arguments;` keeps the arguments object, and one with
// an initializer assigns it.
func varArgumentsAsBinding(path string, prog *ast.Program) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".js" && ext != ".cjs" || IsModule(prog) {
		return
	}
	var inFn func(n ast.Node, fn bool) ast.Node
	inFn = func(n ast.Node, fn bool) ast.Node {
		switch x := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression:
			fn = true
		case *ast.ClassDeclaration, *ast.ClassExpression:
			fn = false
		case *ast.VarDeclaration:
			if fn && x.Kind == "var" && x.Name == "arguments" {
				return argumentsWrite(x)
			}
		case *ast.VarDeclarationList:
			if !fn || !slices.ContainsFunc(x.Decls, func(d *ast.VarDeclaration) bool { return d.Kind == "var" && d.Name == "arguments" }) {
				break
			}
			// Each in order: the others stay `var` declarations.
			var out []ast.Statement
			for _, d := range x.Decls {
				if d.Kind == "var" && d.Name == "arguments" {
					out = append(out, argumentsWrite(d))
				} else {
					out = append(out, d)
				}
			}
			return ast.NewBlockStatement(out, x.GetPos())
		}
		ast.RewriteChildren(n, func(ch ast.Node) ast.Node { return inFn(ch, fn) })
		return n
	}
	for i, st := range prog.Body {
		prog.Body[i] = inFn(st, false).(ast.Statement)
	}
}

// argumentsWrite is a `var arguments` declaration as the write it is to the
// function's arguments binding: none without an initializer.
func argumentsWrite(d *ast.VarDeclaration) ast.Statement {
	if d.Init == nil {
		return ast.NewBlockStatement(nil, d.GetPos())
	}
	as := ast.NewAssignmentExpression("=", ast.NewIdentifier("arguments", d.GetPos()), d.Init, d.GetPos())
	return ast.NewExpressionStatement(as, d.GetPos())
}

// lowerClassHeritageTDZ makes a class whose `extends` names itself, or a
// class declared later in the same statement list, throw JavaScript's
// ReferenceError where it is evaluated: the heritage reads that class's
// binding in its dead zone. A class extending one so lowered is
// unreachable and lowered too. (TypeScript rejects these: TS2449.)
// lowerLateArrowSuperCalls lowers a `super(…)` inside an arrow that a
// derived constructor creates after its own unconditional `super(…)`
// (`super(); this.f = () => super();`): run then, it constructs the base
// again (its side effects happen) and throws the ReferenceError of a second
// initialization of `this`.
func lowerLateArrowSuperCalls(prog *ast.Program) {
	ast.Inspect(prog, func(n ast.Node) bool {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok || cd.BaseClass == "" || cd.BaseQualified || cd.Constructor == nil || cd.Constructor.Body == nil {
			return true
		}
		body := cd.Constructor.Body.Body
		first := -1
		for i, st := range body {
			if es, ok := st.(*ast.ExpressionStatement); ok {
				if call, ok := es.Expr.(*ast.CallExpression); ok {
					if _, ok := call.Callee.(*ast.SuperExpression); ok {
						first = i
						break
					}
				}
			}
		}
		if first < 0 {
			return true
		}
		var inArrow func(x ast.Node) ast.Node
		var outside func(x ast.Node) ast.Node
		inArrow = func(x ast.Node) ast.Node {
			switch x.(type) {
			case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassExpression, *ast.ClassDeclaration:
				return x
			}
			ast.RewriteChildren(x, inArrow)
			call, ok := x.(*ast.CallExpression)
			if !ok {
				return x
			}
			if _, ok := call.Callee.(*ast.SuperExpression); !ok {
				return x
			}
			pos := call.GetPos()
			msg := ast.NewNewExpression("ReferenceError", []ast.Expression{ast.NewStringLiteral("Super constructor may only be called once", pos)}, pos)
			throwFn := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement([]ast.Statement{ast.NewThrowStatement(msg, pos)}, pos), pos)
			return ast.NewSequenceExpression([]ast.Expression{
				ast.NewNewExpression(cd.BaseClass, call.Args, pos),
				ast.NewCallExpression(throwFn, nil, pos),
			}, pos)
		}
		outside = func(x ast.Node) ast.Node {
			switch x.(type) {
			case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassExpression, *ast.ClassDeclaration:
				return x
			case *ast.ArrowFunction:
				ast.RewriteChildren(x, inArrow)
				return x
			}
			ast.RewriteChildren(x, outside)
			return x
		}
		for _, st := range body[first+1:] {
			outside(st)
		}
		return true
	})
}

func lowerClassHeritageTDZ(prog *ast.Program) {
	throwTDZ := func(name string, pos ast.Pos) ast.Expression {
		msg := ast.NewStringLiteral("Cannot access '"+name+"' before initialization", pos)
		th := ast.NewThrowStatement(ast.NewNewExpression("ReferenceError", []ast.Expression{msg}, pos), pos)
		fn := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement([]ast.Statement{th}, pos), pos)
		return ast.NewCallExpression(fn, nil, pos)
	}
	list := func(body []ast.Statement) {
		at := map[string]int{}
		for i, st := range body {
			if cd, ok := st.(*ast.ClassDeclaration); ok && cd.Name != "" && !cd.Ambient {
				at[cd.Name] = i
			}
		}
		lowered := map[string]bool{}
		for i, st := range body {
			cd, ok := st.(*ast.ClassDeclaration)
			if !ok || cd.BaseClass == "" || cd.BaseQualified || cd.Ambient {
				continue
			}
			j, declared := at[cd.BaseClass]
			if !(declared && j >= i) && !lowered[cd.BaseClass] {
				continue
			}
			lowered[cd.Name] = true
			pos := cd.GetPos()
			body[i] = ast.NewVarDeclaration("let", cd.Name, nil, throwTDZ(cd.BaseClass, pos), pos)
		}
	}
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		switch x := n.(type) {
		case *ast.BlockStatement:
			list(x.Body)
		case *ast.ClassExpression:
			// `(class x extends x {})`: its own name is in its dead zone.
			if d := x.Decl; d != nil && d.Name != "" && d.BaseClass == d.Name && !d.BaseQualified {
				return throwTDZ(d.Name, x.GetPos())
			}
		}
		return n
	}
	ast.RewriteChildren(prog, rw)
	list(prog.Body)
}

// evalReassignment lets a sloppy script assign the global `eval`
// (`eval = f`), its calls then direct evals only while it holds the
// intrinsic (__kml_evalorig): the value assigned is held in __kml_evalv,
// and while __kml_evalset (it is another value) a read of `eval` reads it
// and `eval(args)` calls it; else the call is the direct eval
// (rewriteStaticEval). A script declaring its own `eval`
// binds it as any other name.
func evalReassignment(path string, prog *ast.Program) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".js" && ext != ".cjs" || IsModule(prog) || programIsStrictBody(prog.Body) {
		return
	}
	isEval := func(x ast.Expression) bool {
		id, ok := x.(*ast.Identifier)
		return ok && id.Name == "eval"
	}
	assigned, declared := false, false
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			assigned = assigned || x.Op == "=" && isEval(x.Left)
		case *ast.VarDeclaration:
			declared = declared || x.Name == "eval"
		case *ast.FunctionDeclaration:
			declared = declared || x.Name == "eval" || paramNamed(x.Params, "eval")
		case *ast.FunctionExpression:
			declared = declared || paramNamed(x.Params, "eval")
		case *ast.ArrowFunction:
			declared = declared || paramNamed(x.Params, "eval")
		}
		return true
	})
	if !assigned || declared {
		return
	}
	any := func() ast.TypeNode { return &ast.KeywordType{Keyword: "any"} }
	set := func(pos ast.Pos) ast.Expression { return ast.NewIdentifier("__kml_evalset", pos) }
	held := func(pos ast.Pos) ast.Expression { return ast.NewIdentifier("__kml_evalv", pos) }
	simple := func(x ast.Expression) bool {
		switch x.(type) {
		case *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
			return true
		}
		return false
	}
	copyLit := func(x ast.Expression) ast.Expression {
		switch l := x.(type) {
		case *ast.StringLiteral:
			c := *l
			return &c
		case *ast.NumberLiteral:
			c := *l
			return &c
		case *ast.BooleanLiteral:
			c := *l
			return &c
		case *ast.NullLiteral:
			c := *l
			return &c
		}
		return x
	}
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			if x.Op == "=" && isEval(x.Left) {
				pos := x.GetPos()
				r := rw(x.Right).(ast.Expression)
				// Assigned the intrinsic back, its calls are direct again.
				return ast.NewSequenceExpression([]ast.Expression{
					ast.NewAssignmentExpression("=", held(pos), r, pos),
					ast.NewAssignmentExpression("=", set(pos), ast.NewBinaryExpression("!==", held(pos), ast.NewIdentifier("__kml_evalorig", pos), pos), pos),
					held(pos)}, pos)
			}
		case *ast.CallExpression:
			if isEval(x.Callee) && !x.Optional && len(x.Args) > 0 {
				pos := x.GetPos()
				for i := range x.Args {
					x.Args[i] = rw(x.Args[i]).(ast.Expression)
				}
				allSimple := true
				for _, a := range x.Args {
					if _, sp := a.(*ast.SpreadElement); sp || !simple(a) {
						allSimple = false
					}
				}
				if allSimple {
					// The literals twice: one branch runs.
					args := make([]ast.Expression, len(x.Args))
					for i, a := range x.Args {
						args[i] = copyLit(a)
					}
					return ast.NewConditionalExpression(set(pos), ast.NewCallExpression(held(pos), args, pos), x, pos)
				}
				// Evaluated once, into __kml_evala; a literal source is still
				// the direct eval's.
				ea := func() ast.Expression { return ast.NewIdentifier("__kml_evala", pos) }
				src := ast.Expression(ast.NewIndexExpression(ea(), ast.NewNumberLiteral("0", pos), pos))
				if simple(x.Args[0]) {
					src = copyLit(x.Args[0])
				}
				direct := ast.NewCallExpression(x.Callee, []ast.Expression{src}, pos)
				return ast.NewSequenceExpression([]ast.Expression{
					ast.NewAssignmentExpression("=", ea(), ast.NewArrayLiteral(x.Args, pos), pos),
					ast.NewConditionalExpression(set(pos),
						ast.NewCallExpression(held(pos), []ast.Expression{ast.NewSpreadElement(ea(), pos)}, pos),
						direct, pos)}, pos)
			}
		case *ast.Identifier:
			if x.Name == "eval" {
				pos := x.GetPos()
				return ast.NewConditionalExpression(set(pos), held(pos), x, pos)
			}
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	ast.RewriteChildren(prog, rw)
	pos := ast.Pos{Line: 1, Col: 1}
	prog.Body = append([]ast.Statement{
		ast.NewVarDeclaration("var", "__kml_evalv", any(), nil, pos),
		ast.NewVarDeclaration("var", "__kml_evalset", nil, ast.NewBooleanLiteral(false, pos), pos),
		ast.NewVarDeclaration("var", "__kml_evala", any(), nil, pos),
		ast.NewVarDeclaration("var", "__kml_evalorig", any(), ast.NewIdentifier("eval", pos), pos),
	}, prog.Body...)
}

func paramNamed(params []ast.Param, name string) bool {
	for _, p := range params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// reassignedFunctionsAsVars binds, in JavaScript, a function declaration
// whose name the file assigns (`function g() { g = 5 }`) as the variable
// it is: `var g = function () {…}` (an anonymous expression, so `g` inside
// names that variable, and `g.name` is inferred), at the front of its body,
// where a declaration is initialized. TypeScript rejects the assignment
// (TS2630); -compat=js reads every file as JavaScript.
func reassignedFunctionsAsVars(path string, prog *ast.Program, compatJS bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".cjs", ".mjs":
	default:
		if !compatJS { // -compat=js compiles any file as JavaScript
			return
		}
	}
	assigned := map[string]bool{}
	var target func(x ast.Expression)
	target = func(x ast.Expression) {
		switch t := x.(type) {
		case *ast.Identifier:
			assigned[t.Name] = true
		case *ast.AssignmentExpression:
			target(t.Left)
		case *ast.SpreadElement:
			target(t.Arg)
		case *ast.ArrayLiteral:
			for _, el := range t.Elements {
				if el != nil {
					target(el)
				}
			}
		case *ast.ObjectLiteral:
			for _, p := range t.Properties {
				target(p.Value)
			}
		}
	}
	// A function's own body is not a block of Annex B.3.3.
	bodies := map[*ast.BlockStatement]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionDeclaration:
			bodies[x.Body] = true
		case *ast.FunctionExpression:
			bodies[x.Body] = true
		case *ast.ArrowFunction:
			if x.Block != nil {
				bodies[x.Block] = true
			}
		}
		return true
	})
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			target(x.Left)
		case *ast.UpdateExpression:
			target(x.Arg)
		case *ast.BlockStatement:
			// A plain function a block declares assigns its name's var when
			// the block runs (annexBFunctions, Annex B.3.3).
			if bodies[x] {
				return true
			}
			for _, st := range x.Body {
				if fd, ok := st.(*ast.FunctionDeclaration); ok && !fd.IsAsync && !fd.IsGenerator {
					assigned[fd.Name] = true
				}
			}
		}
		return true
	})
	if len(assigned) == 0 {
		return
	}
	convert := func(body []ast.Statement, program bool) []ast.Statement {
		var hoisted []ast.Statement
		rest := body[:0:0]
		for _, st := range body {
			fd, ok := st.(*ast.FunctionDeclaration)
			// `export function f` too: an exported variable.
			ex, exported := st.(*ast.ExportDeclaration)
			if exported && program && !ex.IsDefault {
				fd, ok = ex.Decl.(*ast.FunctionDeclaration)
			}
			if !ok || !assigned[fd.Name] || fd.Body == nil || fd.Ambient || fd.IsOverloadSig || len(fd.Decorators) > 0 {
				rest = append(rest, st)
				continue
			}
			fe := ast.NewFunctionExpression("", fd.Params, fd.ReturnType, fd.Body, fd.IsAsync, fd.GetPos())
			fe.IsGenerator = fd.IsGenerator
			fe.TypeParams, fe.TypeParamConstraints, fe.TypeParamDefaults = fd.TypeParams, fd.TypeParamConstraints, fd.TypeParamDefaults
			fe.Src = fd.Src
			var vd ast.Statement = ast.NewVarDeclaration("var", fd.Name, nil, fe, fd.GetPos())
			if exported {
				vd = ast.NewExportDeclaration(vd, false, ex.GetPos())
			}
			if f, ok := prog.StmtFiles[st]; ok && program {
				prog.StmtFiles[vd] = f
			}
			hoisted = append(hoisted, vd)
		}
		if len(hoisted) == 0 {
			return body
		}
		// After the directive prologue (and a program's imports).
		i := 0
		for i < len(rest) {
			if _, ok := rest[i].(*ast.ImportDeclaration); ok && program {
				i++
				continue
			}
			if es, ok := rest[i].(*ast.ExpressionStatement); ok {
				if _, str := es.Expr.(*ast.StringLiteral); str {
					i++
					continue
				}
			}
			break
		}
		out := append([]ast.Statement{}, rest[:i]...)
		out = append(out, hoisted...)
		return append(out, rest[i:]...)
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionDeclaration:
			if x.Body != nil {
				x.Body.Body = convert(x.Body.Body, false)
			}
		case *ast.FunctionExpression:
			if x.Body != nil {
				x.Body.Body = convert(x.Body.Body, false)
			}
		case *ast.ArrowFunction:
			if x.Block != nil {
				x.Block.Body = convert(x.Block.Body, false)
			}
		}
		return true
	})
	prog.Body = convert(prog.Body, true)
}

// hoistNestedTopLevelVars declares, at the top of a file, each `var` its
// top-level code binds inside a block or a loop head (`for (var k of xs)`)
// that is read outside that statement or from a function: JavaScript's var
// is the file's own binding, so it is mangled, promoted and shared like a
// top-level `var k;` (undefined until its declaration runs).
func hoistNestedTopLevelVars(prog *ast.Program) {
	direct := map[string]bool{}
	for _, st := range prog.Body {
		for _, r := range declRefsOf(st) {
			direct[r.Name] = true
		}
		switch d := st.(type) {
		case *ast.ArrayDestructuring:
			freevars.CollectArrayPatternNames(d.Elems, direct)
		case *ast.ObjectDestructuring:
			freevars.CollectObjectPatternNames(d.Props, direct)
		}
	}
	var hoisted []ast.Statement
	for i, st := range prog.Body {
		switch st.(type) {
		case *ast.VarDeclaration, *ast.VarDeclarationList, *ast.FunctionDeclaration, *ast.ClassDeclaration,
			*ast.ArrayDestructuring, *ast.ObjectDestructuring:
			continue
		}
		nested := map[string]bool{}
		freevars.CollectVarNames([]ast.Statement{st}, nested)
		for name := range nested {
			if direct[name] || !readBeyond(prog.Body, i, name) {
				continue
			}
			direct[name] = true
			v := ast.NewVarDeclaration("var", name, nil, nil, st.GetPos())
			hoisted = append(hoisted, v)
			if prog.StmtFiles != nil {
				if f, ok := prog.StmtFiles[st]; ok {
					prog.StmtFiles[v] = f
				}
			}
		}
	}
	if len(hoisted) > 0 {
		prog.Body = append(hoisted, prog.Body...)
	}
}

// readBeyond reports whether name is referenced outside top-level statement
// body[at], or from a function inside it.
func readBeyond(body []ast.Statement, at int, name string) bool {
	found := false
	var walk func(n ast.Node, inFn bool)
	walk = func(n ast.Node, inFn bool) {
		ast.ForEachChild(n, func(ch ast.Node) bool {
			if found {
				return false
			}
			in := inFn
			switch c := ch.(type) {
			case *ast.ArrowFunction, *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
				in = true
			case *ast.Identifier:
				if c.Name == name && in {
					found = true
				}
			}
			walk(ch, in)
			return true
		})
	}
	for i, st := range body {
		walk(&ast.BlockStatement{Body: []ast.Statement{st}}, i != at)
		if found {
			return true
		}
	}
	return false
}

// functionCtorCallRE matches a call of the Function constructor, whose text
// can read any global (`Function("return this")()`).
var functionCtorCallRE = regexp.MustCompile(`\bFunction\s*\(`)

// ownKeyCheckTarget is the object of an own-key check with a literal key
// (`hasOwnProperty.call(o, "k")`, `Object.hasOwn(o, "k")`), else nil.
func ownKeyCheckTarget(c *ast.CallExpression) *ast.Identifier {
	m, ok := c.Callee.(*ast.MemberExpression)
	if !ok || len(c.Args) != 2 {
		return nil
	}
	if _, lit := c.Args[1].(*ast.StringLiteral); !lit {
		return nil
	}
	check := m.Property == "hasOwn"
	if o, isM := m.Object.(*ast.MemberExpression); isM && m.Property == "call" && o.Property == "hasOwnProperty" {
		check = true
	}
	if !check {
		return nil
	}
	id, _ := uncast(c.Args[0]).(*ast.Identifier)
	return id
}

// GlobalObjectNatives are the globals this compiler implements natively
// that the global object carries only when a program can read them off it
// (ast.Program.GlobalObjectNames): each one's code is linked only then.
var GlobalObjectNatives = []string{
	"process", "Buffer", "DOMException", "TransformStream", "WritableStream", "ReadableStream",
	"CompressionStream", "DecompressionStream", "fetch", "Request", "Response", "Crypto", "CryptoKey",
}

// objectProtoNames are Object.prototype's members: one read off the global
// object (`globalThis.hasOwnProperty(k)`) observes its other properties.
var objectProtoNames = map[string]bool{
	"hasOwnProperty": true, "propertyIsEnumerable": true, "isPrototypeOf": true, "toString": true,
	"toLocaleString": true, "valueOf": true, "constructor": true, "__proto__": true,
	"__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true, "__lookupSetter__": true,
}

// uncast is e without its type casts, which do not exist at run time.
func uncast(e ast.Expression) ast.Expression {
	for {
		switch x := e.(type) {
		case *ast.AsExpression:
			e = x.Expr
		case *ast.NonNullExpression:
			e = x.Arg
		default:
			return e
		}
	}
}

// thisMemberNames is the property names a non-class function's `this`
// is read or written by (`this.x`, `this["x"]`), and escapes whether one
// is used any other way (passed, returned, indexed by a computed key, or
// read through an Object.prototype member that observes the rest): only
// then can the global object such a `this` may be show a property no
// name in the program spells.
func thisMemberNames(prog *ast.Program) (names map[string]bool, escapes bool) {
	names = map[string]bool{}
	ok := map[*ast.ThisExpression]bool{}
	var walk func(root ast.Node, inFn bool)
	walk = func(root ast.Node, inFn bool) {
		ast.Inspect(root, func(n ast.Node) bool {
			if escapes {
				return false
			}
			if n == root {
				return true
			}
			switch x := n.(type) {
			case *ast.ClassDeclaration, *ast.ClassExpression:
				return false
			case *ast.FunctionDeclaration:
				if x.Body != nil {
					walk(x, true)
				}
				return false
			case *ast.FunctionExpression:
				if x.Body != nil {
					walk(x, true)
				}
				return false
			case *ast.MemberExpression:
				if t, isThis := uncast(x.Object).(*ast.ThisExpression); isThis && inFn && !objectProtoNames[x.Property] {
					ok[t] = true
					names[x.Property] = true
				}
			case *ast.IndexExpression:
				if t, isThis := uncast(x.Object).(*ast.ThisExpression); isThis && inFn {
					if lit, isLit := x.Index.(*ast.StringLiteral); isLit && !objectProtoNames[lit.Value] {
						ok[t] = true
						names[lit.Value] = true
					}
				}
			case *ast.ThisExpression:
				if inFn && !ok[x] {
					escapes = true
				}
			}
			return true
		})
	}
	walk(prog, false)
	return names, escapes
}

// globalObjectEscapes reports whether prog may read the global object's
// properties by a name it does not write: `globalThis` (or `global`, or a
// const alias of either) used other than as a member read or write by
// name, an index by a string literal, or another alias, and the global
// object as a plain function's or a sloppy script's `this`.
func globalObjectEscapes(path string, src []byte, prog *ast.Program) bool {
	if !bytes.Contains(src, []byte("globalThis")) && !bytes.Contains(src, []byte("global")) && !bytes.Contains(src, []byte("this")) {
		return false
	}
	if ctorFunctionCall(src) || sloppyThisUse(path, src, prog) {
		if _, esc := thisMemberNames(prog); esc {
			return true
		}
	}
	aliases := map[string]bool{"globalThis": true, "global": true}
	for changed := true; changed; {
		changed = false
		ast.Inspect(prog, func(n ast.Node) bool {
			if v, ok := n.(*ast.VarDeclaration); ok && v.Kind == "const" && !aliases[v.Name] {
				if id, ok := v.Init.(*ast.Identifier); ok && aliases[id.Name] {
					aliases[v.Name] = true
					changed = true
				}
			}
			return true
		})
	}
	ok := map[*ast.Identifier]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.MemberExpression:
			if id, isID := uncast(x.Object).(*ast.Identifier); isID && aliases[id.Name] && !objectProtoNames[x.Property] {
				ok[id] = true
			}
		case *ast.IndexExpression:
			if id, isID := uncast(x.Object).(*ast.Identifier); isID && aliases[id.Name] {
				if _, lit := x.Index.(*ast.StringLiteral); lit {
					ok[id] = true
				}
			}
		case *ast.VarDeclaration:
			if id, isID := x.Init.(*ast.Identifier); isID && x.Kind == "const" && aliases[id.Name] {
				ok[id] = true
			}
		case *ast.CallExpression:
			// hasOwnProperty.call(globalThis, "k"), Object.hasOwn(globalThis, "k"):
			// one named key, as `globalThis.k` is.
			if id := ownKeyCheckTarget(x); id != nil && aliases[id.Name] {
				ok[id] = true
			}
		}
		return true
	})
	escapes := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if id, isID := n.(*ast.Identifier); isID && aliases[id.Name] && !ok[id] {
			escapes = true
		}
		return !escapes
	})
	return escapes
}
