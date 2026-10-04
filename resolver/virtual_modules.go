package resolver

import (
	"strings"
	"sync"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// TDD-00049 Stage 1: a fixed table of "virtual" built-in module specifiers —
// never a real file on disk, unlike every other import source this
// resolver handles. Recognized here purely so a Category-B pseudo-namespace
// (fs/path/os/assert/http/cluster/Memory — see the TDD's own
// Design section for the Category A/B/C split) can only be referenced in a
// file that actually imported it, closing the collision/shadowing bug the
// TDD found: today codegen/llvm recognizes these by bare AST-identifier
// text, with no scope awareness at all, so a local variable of the same
// name gets silently miscompiled into a call to the built-in.
//
// Each specifier maps to the marker name codegen/llvm now dispatches on
// instead of the bare pseudo-namespace name — see the matching
// `id.Name == "<marker>"` checks in emit_call.go/emit_exprs_member.go/
// emit_exprs_types.go. The marker is deliberately not a real mangled name
// (mangleName's `__kml_modN` suffix is per-file and only ever produced for
// a real user declaration) — it's a single fixed, reserved name per virtual
// module, chosen so it can never collide with one.
//
// Stage 1 scope: a default import (`import fs from 'fs'`) or a namespace
// import (`import * as fs from 'fs'`) binds a virtual module — whichever
// local name the program chooses is rewritten to the marker by the same
// scope-aware rename pass ([TDD-00041]) that already handles real
// file-to-file imports, so shadowing "just works" for free.
var virtualBuiltinMarkers = map[string]string{
	"child_process": "childprocess__kml_builtin",
	"http":          "http__kml_builtin",
	// https shares the libcurl-backed client (get/request speak TLS for free);
	// https.createServer is a clean codegen rejection until the http accept
	// loop is TLS-wrapped.
	"https":      "https__kml_builtin",
	"stream/web": "streamweb__kml_builtin",
	// TDD-00131: the `klain:` namespace holds this project's own bespoke
	// re-imaginings, distinct from the Node-faithful module names. `klain:http`
	// is the current `http.listen(handler ⇒ response)` server model, which is
	// NOT Node's `http.createServer` shape — kept intentionally, on the same
	// runtime, under an explicitly-non-Node specifier.
	// klain:http gets its OWN marker (distinct from http's) so codegen can tell
	// a bespoke `listen`/`close`/`closeAllConnections` call apart by its import
	// origin — the same implementation, but only reachable through klain:http
	// (ADR-00635). http and klain:http sharing one marker is what previously let
	// `http.listen` slip through the http namespace.
	"klain:http": "klainhttp__kml_builtin",
	// TDD-00142: the system-webview desktop-window module. `Webview` binds as
	// identity (a parse-time constructor, like stream's class names).
	"klain:webview": "webview__kml_builtin",
	// TDD-00142 Stage 7: compile-time asset embedding (`embedDir`). Function
	// member → marker-dispatch (not identity).
	"klain:assets": "assets__kml_builtin",
	// TDD-00031: bespoke terminal-input primitives with no Node counterpart —
	// synchronous single-keystroke reads off fd 0 (Node does raw input via
	// process.stdin.on('data') events, never a sync byte read). The
	// Node-faithful surface (setRawMode/columns/rows/SIGWINCH) stays on
	// `process`; only these non-Node reads live under the explicit klain: name.
	"klain:tty": "tty__kml_builtin",
	// TDD-00150 Stage 1: native TUI framework — Yoga flexbox layout + a
	// double-buffered ANSI diff painter. Builder functions (Box/Text/List/
	// Spinner/Progress/TextInput) plus render/enter/leave; the state->view->
	// update loop is written in userland TS over klain:tty + SIGWINCH.
	"klain:tui": "tui__kml_builtin",
	// TDD-00143: Go-fidelity goroutine runtime. `go` is a function-member
	// (marker-dispatched); `Channel` binds as identity (a parse-time
	// constructor, like Webview / stream class names).
	"klain:sync": "sync__kml_builtin",
	"memory":     "Memory__kml_builtin", // capitalized marker, matching Memory.free's existing capitalized surface
	// TDD-00165: Web-global-backed Node modules. Their primary exports are
	// spec-identical re-exports of an ambient global (`URL`, `setTimeout`,
	// `performance`, `Buffer`, `EventEmitter`), so a same-name import is
	// validated and *erased* — the global keeps working, no codegen change.
	// These markers exist only to route the specifier through the virtual-import
	// machinery (member validation, `node:` aliasing, not-a-file); they are never
	// bound (globalReexportModules is handled before the generic marker path in
	// resolver.go), so they must not collide with a real dispatch marker.
	"buffer": "buffer__reexport_kml_builtin",
}

// firstKey returns the lexicographically-smallest key of a set, for a stable,
// deterministic example name in an error message.
func firstKey(set map[string]bool) string {
	first := ""
	for k := range set {
		if first == "" || k < first {
			first = k
		}
	}
	return first
}

// globalReexportModules (TDD-00165) are the Web-global-backed Node modules whose
// primary exports are spec-identical to an ambient global. A same-name named (or,
// for `events`, default) import of one of these is validated against the set here
// and then erased — the global identifier keeps resolving as it does today, so no
// codegen change is needed. Aliased, namespace, and default-mismatch forms are
// deferred with a clear message (TDD-00165 Stages 2–3). The module-only *extras*
// (legacy `url.parse`, `PerformanceObserver`, `events.once`, …) are deliberately
// NOT listed — they have no same-named global and are separate future surface,
// so they keep the standard "no exported member" rejection.
var globalReexportModules = map[string]map[string]bool{
	"buffer": {"Buffer": true, "Blob": true, "atob": true, "btoa": true},
}

// moduleFunctionMembers (TDD-00165 Stage 4) are the *module-only* function
// exports of a global-reexport module — members with no same-named global, so
// they are dispatched in codegen via the module's marker (like `url.parse`)
// rather than erased to a global. Their presence also makes a namespace/default
// import of the module bind the marker (so `import * as url from 'url'; url.parse(…)`
// works). A named import of one records an ordinary builtin-member reference.
var moduleFunctionMembers = map[string]map[string]bool{}

// parseTimeReexports are the global-reexport members recognized by the **parser**
// as a literal `new <Name>(...)` construct (`parser/parser_literals.go`), before
// imports are resolved. Because the parser keys on the literal identifier, an
// aliased import of one of these cannot be made to work by the post-parse rename
// pass — `new U()` was already parsed as a generic class instantiation, not the
// built-in construct — so an alias stays a clean Stage 3 rejection. Every other
// reexport member is resolved in codegen by its bare name (`setTimeout(...)`,
// `performance.now()`, `atob(...)`), so aliasing it is just a rename (Stage 2).
var parseTimeReexports = map[string]bool{}

// virtualModuleMembers is Stage 2's addition: the real "exported member"
// list per virtual specifier, used to validate a named import
// (`import { readFileSync } from 'fs'`) exactly the way a real file
// import's specifiers are already validated against its target's
// `exportedNames` (resolver.go). Kept in sync by hand against each
// specifier's own dispatch switch in codegen/llvm — there is no single
// source of truth to derive this from automatically, since the built-in
// dispatch tables live in Go source, not data.
var virtualModuleMembers = map[string]map[string]bool{
	"child_process": {"spawn": true, "exec": true, "execFile": true, "fork": true, "spawnSync": true, "execSync": true, "execFileSync": true},
	// The Node `http` namespace exposes only Node-real members. The bespoke
	// `listen`/`close`/`closeAllConnections` module-level functions (Node has
	// these as `Server` methods, not `http.*` functions) live solely under
	// `klain:http` now (TDD-00158 follow-up / ADR-00635) — importing them from
	// `http` is a clean "no export" rejection pointing there.
	"http":          {"get": true, "request": true, "createServer": true, "Agent": true},
	"klain:http":    {"listen": true, "close": true, "closeAllConnections": true},
	"klain:webview": {"Webview": true},
	"klain:assets":  {"embedDir": true},
	"klain:tty":     {"readByte": true, "readKey": true},
	"klain:sync":    {"go": true, "Channel": true, "select": true, "defaultCase": true},
	"klain:tui": {
		"Box": true, "Text": true, "List": true, "Spinner": true,
		"Progress": true, "TextInput": true,
		"render": true, "enter": true, "leave": true,
	},
	"memory": {"free": true},
	"https":  {"get": true, "request": true, "Agent": true},
	// stream/web re-exports the WHATWG stream classes that already exist as
	// parse-time constructors — the names bind as identity.
	"stream/web": {
		"ReadableStream": true, "WritableStream": true, "TransformStream": true,
		"CompressionStream": true, "DecompressionStream": true,
	},
}

// virtualImportLocal returns the local name a virtual-module import binds
// (from either a namespace import's alias or a default import's specifier)
// and whether it binds anything at all — false for a bare `import 'fs'`
// side-effect-only form, which has nothing meaningful to bind for a virtual
// module and is treated as a no-op.
func virtualImportLocal(imp *ast.ImportDeclaration) (string, bool) {
	if imp.Namespace != "" {
		return imp.Namespace, true
	}
	for _, spec := range imp.Specifiers {
		if spec.Imported == "default" {
			return spec.Local, true
		}
	}
	return "", false
}

// Node supports the `node:`-prefixed form of every core-module specifier
// (`import test from 'node:test'`); alias each Node-named virtual module
// under that prefix so both spellings resolve. The project-specific
// specifiers (`klain:*`, `memory`, `test`'s bare-name alias stays too) are
// deliberately not double-registered beyond this mechanical prefixing.
func init() {
	for name, marker := range virtualBuiltinMarkers {
		if strings.HasPrefix(name, "klain:") || strings.HasPrefix(name, "node:") {
			continue
		}
		virtualBuiltinMarkers["node:"+name] = marker
	}
	for name, members := range virtualModuleMembers {
		if strings.HasPrefix(name, "klain:") || strings.HasPrefix(name, "node:") {
			continue
		}
		virtualModuleMembers["node:"+name] = members
	}
	// Mirror the global-reexport tables under the `node:` prefix too, so
	// `import { URL } from 'node:url'` resolves exactly like the bare form
	// (TDD-00165). Also expose each set as the module's member table so any
	// generic member-existence check agrees with the special path.
	for name, exports := range globalReexportModules {
		if strings.HasPrefix(name, "node:") {
			continue
		}
		globalReexportModules["node:"+name] = exports
		// virtualModuleMembers is a FRESH union of the reexport primaries and any
		// module-only function members — never the shared globalReexportModules
		// map itself, so adding a function member below can't leak into the
		// reexport-vs-function distinction the resolver relies on.
		merged := map[string]bool{}
		for m := range exports {
			merged[m] = true
		}
		for m := range moduleFunctionMembers[name] {
			merged[m] = true
		}
		virtualModuleMembers[name] = merged
		virtualModuleMembers["node:"+name] = merged
	}
	for name, funcs := range moduleFunctionMembers {
		if strings.HasPrefix(name, "node:") {
			continue
		}
		moduleFunctionMembers["node:"+name] = funcs
	}
	// A module written in TypeScript (TDD-00231) is an import of a real
	// file, not a virtual module.
	for name := range virtualBuiltinMarkers {
		if _, ok := lib.ModulePath(name); ok {
			delete(virtualBuiltinMarkers, name)
			delete(virtualModuleMembers, name)
			delete(globalReexportModules, name)
			delete(moduleFunctionMembers, name)
		}
	}
}

// IsBuiltinModule reports whether an import specifier names a builtin module
// (a Node core module, with or without `node:`, or a `klain:` module) rather
// than a user file or package.
func IsBuiltinModule(src string) bool {
	_, ok := virtualBuiltinMarkers[src]
	return ok
}

var (
	companionMu    sync.Mutex
	companionCache = map[string]map[string]bool{}
)

// companionExports is every name the TypeScript companion of the builtin
// module spec exports (lib.CompanionPath), or nil when it has none.
func companionExports(spec string) map[string]bool {
	path, ok := lib.CompanionPath(spec)
	if !ok {
		return nil
	}
	companionMu.Lock()
	defer companionMu.Unlock()
	if names, ok := companionCache[path]; ok {
		return names
	}
	var names map[string]bool
	if src, ok := lib.ModuleSource(path); ok {
		if prog, err := parser.Parse(string(src)); err == nil {
			names = exportedNames(prog)
		}
	}
	// A name starting with `_kml` is shared with the other builtin modules
	// (imported by path), not the module's.
	for n := range names {
		if strings.HasPrefix(n, "_kml") {
			delete(names, n)
		}
	}
	companionCache[path] = names
	return names
}

// eraseBuiltinTypeImports drops the type-only bindings of a builtin module's
// imports (`import type { IncomingMessage } from 'http'`, `{ type X }`): a
// builtin module's types are the library's, erased like any type import, and
// its value-export table does not list them. An import left binding nothing
// is dropped.
func eraseBuiltinTypeImports(prog *ast.Program) {
	var aliases []ast.Statement // `import type { A as B }`: `type B = A`
	defer func() { prog.Body = append(aliases, prog.Body...) }()
	body := prog.Body[:0]
	for _, stmt := range prog.Body {
		imp, ok := stmt.(*ast.ImportDeclaration)
		if !ok {
			body = append(body, stmt)
			continue
		}
		_, virtual := virtualBuiltinMarkers[imp.Source]
		_, reexport := globalReexportModules[imp.Source]
		if !virtual && !reexport {
			body = append(body, stmt)
			continue
		}
		bound := len(imp.Specifiers) > 0 || imp.Namespace != ""
		specs := imp.Specifiers[:0]
		companion := companionExports(imp.Source)
		for _, spec := range imp.Specifiers {
			if companion[spec.Imported] {
				// The module's part written in TypeScript declares it, a
				// type as much as a value: it resolves there.
				specs = append(specs, spec)
				continue
			}
			if virtual && spec.Imported != "default" && !spec.TypeOnly && !virtualModuleMembers[imp.Source][spec.Imported] && lib.NodeModuleExport(imp.Source, spec.Imported) {
				// Node exports it, but not as a value this compiler
				// implements (a class used as a type): a type import.
				if prog.NodeTypeImports == nil {
					prog.NodeTypeImports = map[string]string{}
				}
				prog.NodeTypeImports[spec.Local] = imp.Source
				aliases = appendTypeAlias(aliases, spec, imp.GetPos())
				continue
			}
			if !spec.TypeOnly {
				specs = append(specs, spec)
			} else {
				aliases = appendTypeAlias(aliases, spec, imp.GetPos())
			}
		}
		imp.Specifiers = specs
		if imp.TypeOnly {
			imp.Namespace = ""
		}
		if bound && len(imp.Specifiers) == 0 && imp.Namespace == "" {
			continue
		}
		body = append(body, stmt)
	}
	prog.Body = body
}

// appendTypeAlias adds `type Local = Imported` for an erased type import
// bound under another name, so an annotation naming it still reaches the
// builtin type.
func appendTypeAlias(aliases []ast.Statement, spec ast.ImportSpecifier, pos ast.Pos) []ast.Statement {
	if spec.Local == spec.Imported {
		return aliases
	}
	ta, err := ast.TypeAnnotationOf(&ast.TypeReference{Name: spec.Imported}, "ts")
	if err != nil {
		return aliases
	}
	return append(aliases, ast.NewTypeAliasDeclaration(spec.Local, ta, pos))
}

// typeOnlyLocals is every local name a user module's type-only import binds.
func typeOnlyLocals(prog *ast.Program) map[string]bool {
	var out map[string]bool
	for _, stmt := range prog.Body {
		imp, ok := stmt.(*ast.ImportDeclaration)
		if !ok {
			continue
		}
		for _, spec := range imp.Specifiers {
			if spec.TypeOnly {
				if out == nil {
					out = map[string]bool{}
				}
				out[spec.Local] = true
			}
		}
		if imp.TypeOnly && imp.Namespace != "" {
			if out == nil {
				out = map[string]bool{}
			}
			out[imp.Namespace] = true
		}
	}
	return out
}
