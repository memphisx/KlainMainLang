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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

type fileInfo struct {
	path     string
	prog     *ast.Program
	isEntry  bool
	exported map[string]bool
	index    int               // assigned at first visit; used to build this file's mangled-name suffix (TDD-00041)
	mangled  map[string]string // original top-level declaration name -> this file's mangled name for it

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
// validates import/export usage, and returns one merged *ast.Program.
// Equivalent to ResolveProgramWithOptions(entryPath, false) — see its doc
// comment for allowGlobalShadowing's meaning (TDD-00050).
func ResolveProgram(entryPath string) (*ast.Program, error) {
	return ResolveProgramWithOptions(entryPath, false)
}

// ResolveProgramWithOptions is ResolveProgram plus allowGlobalShadowing
// (TDD-00050, `-compat=js` in main.go — default false, i.e.
// `-compat=strict`): whether a program may declare its own binding named
// the same as a Tier 1 ambient global (`Math`/`process`/`fetch`/… — see
// resolver/reserved_names.go). Tier 2 names (`Map`/`Date`/`RegExp`/… —
// parser-level `new`-form built-ins) are rejected either way; there is no
// flag value that lifts those, see reserved_names.go's own doc comment for
// why.
func ResolveProgramWithOptions(entryPath string, allowGlobalShadowing bool) (*ast.Program, error) {
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
	workerTargets := map[string]bool{}

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

		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		prog, err := parser.Parse(string(src))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
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
				if _, isVirtual := virtualBuiltinMarkers[s.Source]; isVirtual {
					return fmt.Errorf("%d:%d: re-exporting from a built-in module ('%s') is not supported",
						s.GetPos().Line, s.GetPos().Col, s.Source)
				}
				source = s.Source
			default:
				continue
			}
			if _, isVirtual := virtualBuiltinMarkers[source]; isVirtual {
				continue // TDD-00049: a built-in module, never a real file to visit/parse
			}
			resolved, err := resolveImportPath(dir, source, klainModulesDir)
			if err != nil {
				return fmt.Errorf("%d:%d: %w", stmt.GetPos().Line, stmt.GetPos().Col, err)
			}
			importTargets[resolved] = true
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

		files[path] = &fileInfo{path: path, prog: prog, isEntry: isEntry, exported: exportedNames(prog), index: files[path].index}
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
		if wf == entryAbs {
			return nil, fmt.Errorf("%s: the program's own entry file cannot be used as a worker module", wf)
		}
		if importTargets[wf] {
			return nil, fmt.Errorf("%s: a worker module cannot also be imported — its top level runs on the worker thread, not at import time", wf)
		}
		if len(files[wf].prog.WorkerPaths) > 0 {
			return nil, fmt.Errorf("%s: a worker module cannot spawn workers of its own (nested workers are not supported)", wf)
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
	for _, path := range allPaths {
		info := files[path]
		// Block-scoped redeclaration early-errors (nested scopes only;
		// top-level is mangleFileDecls's job) — run pre-mangle so messages
		// carry the original binding name. See TDD-00070.
		if err := checkLexicalScopes(path, info.prog); err != nil {
			return nil, err
		}
		// Temporal-dead-zone early error (TDD-00071): a read of a let/const
		// before its declaration, incl. the block-shadowing form. Sound-only —
		// cross-function reads are exempt, so no valid program is rejected.
		if err := checkTDZ(info.prog); err != nil {
			return nil, err
		}
		// Definite-assignment early error (TDD-00071 Stage 2): a typed var/let
		// read on a path where it wasn't assigned. Sound-only — conservative
		// merges and cross-function exemption keep it free of false positives.
		if err := checkDefiniteAssignment(info.prog); err != nil {
			return nil, err
		}
		mangled, err := mangleFileDecls(path, info.prog, info.index, allowGlobalShadowing)
		if err != nil {
			return nil, err
		}
		info.mangled = mangled
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
	for _, path := range allPaths {
		info := files[path]
		lookup := make(map[string]string, len(info.mangled))
		for orig, m := range info.mangled {
			lookup[orig] = m
		}
		ns := map[string]map[string]string{}
		builtinMembers := map[string]builtinMemberRef{}
		dir := filepath.Dir(path)
		for _, stmt := range info.prog.Body {
			imp, ok := stmt.(*ast.ImportDeclaration)
			if !ok {
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
				for _, spec := range imp.Specifiers {
					if spec.Imported == "default" {
						continue // handled by virtualImportLocal below
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
				for name := range target.exported {
					members[name], _ = target.publicMangled(name)
				}
				ns[imp.Namespace] = members
			}
		}
		var reservedErr error
		renameFile(info.prog, lookupTable{
			names: lookup, ns: ns, builtinMembers: builtinMembers,
			allowGlobalShadowing: allowGlobalShadowing, reservedErr: &reservedErr,
			filePath: path,
		})
		if reservedErr != nil {
			return nil, reservedErr
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
					return nil, fmt.Errorf("internal error: mangled name '%s' collided between %s and %s", ref.Name, prev.path, path)
				}
				declaredIn[ref.Name] = declSite{path: path, kind: ref.Kind}
			}
		}
	}

	// Merge: every non-entry file's declarations, then the entry file's own
	// full statement list — dropping ImportDeclaration and unwrapping
	// ExportDeclaration everywhere, since codegen/llvm knows neither node.
	merged := &ast.Program{}
	mergeNamespaces := func(src *ast.Program) {
		merged.NSAliases = append(merged.NSAliases, src.NSAliases...)
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
		if workerTargets[path] {
			// TDD-00098: a worker module's function/class/interface/type/enum
			// declarations are hoisted into the shared program body (they're
			// pure definitions — emitted as functions, callable from any
			// thread), while its var declarations and executable statements
			// become the worker's entry-function body.
			decls, body := splitWorkerBody(unwrap(files[path].prog.Body))
			merged.Body = append(merged.Body, decls...)
			merged.WorkerModules = append(merged.WorkerModules, ast.WorkerModule{Path: path, Body: body})
			mergeNamespaces(files[path].prog)
			continue
		}
		merged.Body = append(merged.Body, unwrap(files[path].prog.Body)...)
		mergeNamespaces(files[path].prog)
	}
	merged.Body = append(merged.Body, unwrap(files[entryAbs].prog.Body)...)
	mergeNamespaces(files[entryAbs].prog)
	return merged, nil
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
		return []declRef{{s.Name, "class", func(n string) { s.Name = n }}}
	}
	return nil
}

// mangleName builds fileIdx's file-private internal name for a top-level
// declaration originally named name — unique per file (TDD-00041), and
// valid as an LLVM/C identifier fragment (alphanumeric + underscore only).
func mangleName(name string, fileIdx int) string {
	return fmt.Sprintf("%s__kml_mod%d", name, fileIdx)
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
func mangleFileDecls(path string, prog *ast.Program, fileIdx int, allowGlobalShadowing bool) (map[string]string, error) {
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
			if err := checkReservedBinding(ref.Name, stmt.GetPos().Line, stmt.GetPos().Col, allowGlobalShadowing); err != nil {
				return nil, err
			}
			if prevKind, dup := seenKind[ref.Name]; dup {
				// A repeated `var` or `function` of the same name is legal JS
				// (var re-declaration, and var/function hoisting collapse into
				// a single binding) — only a lexical kind (let/const/class/…)
				// colliding with anything, or a var/function colliding with a
				// lexical kind, is a real redeclaration error. Codegen already
				// tolerates the duplicate: each top-level `var x = …` gets its
				// own freshReg alloca and re-points the symbol, so the second
				// declaration is observably just an assignment.
				if !isVarOrFuncKind(prevKind) || !isVarOrFuncKind(ref.Kind) {
					// TS declaration merging (ADR-00466): an interface may
					// coexist with a same-name class (either order) — the
					// class wins as the binding; the interface's extra
					// members are ignored at registration (codegen skips
					// registering an interface shadowed by a class), a
					// disclosed narrowing of real merge semantics.
					if !isClassIfaceMergePair(prevKind, ref.Kind) {
						return nil, fmt.Errorf("'%s' is declared more than once in %s", ref.Name, path)
					}
				}
				// Idempotent: re-point to the existing mangled name rather than
				// minting a second one for the same binding.
				ref.Set(mangled[ref.Name])
				lastNewName = mangled[ref.Name]
				continue
			}
			newName := mangleName(ref.Name, fileIdx)
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
	return mangled, nil
}

// exportedNames returns the set of top-level names a file exports. A
// default export (TDD-00042) is exposed only under the "default" key, not
// under its own declared name too — matching real ES modules, where
// `export default function foo() {...}` does not also make `foo` available
// via `import { foo }`.
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
	joined := filepath.Join(dir, rel)
	if filepath.Ext(joined) == "" {
		joined += ".ts"
	}
	abs, err = filepath.Abs(joined)
	if err != nil {
		return "", false, err
	}
	if _, statErr := os.Stat(abs); statErr != nil {
		return abs, false, nil
	}
	return abs, true, nil
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
