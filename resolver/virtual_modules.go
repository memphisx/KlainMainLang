package resolver

import "KlainMainLang/ast"

// TDD-00049 Stage 1: a fixed table of "virtual" built-in module specifiers —
// never a real file on disk, unlike every other import source this
// resolver handles. Recognized here purely so a Category-B pseudo-namespace
// (fs/path/os/querystring/assert/http/cluster/Memory — see the TDD's own
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
	"fs":          "fs__kml_builtin",
	"path":        "path__kml_builtin",
	"os":          "os__kml_builtin",
	"querystring": "querystring__kml_builtin",
	"assert":      "assert__kml_builtin",
	"http":        "http__kml_builtin",
	"cluster":     "cluster__kml_builtin",
	"memory":      "Memory__kml_builtin", // capitalized marker, matching Memory.free's existing capitalized surface
	// TDD-00097 Stage 8: Node's stream module. The class names bind as
	// identity (see resolver.go's stream special-case) — `new Readable(...)`
	// is recognized by name at parse time like every other builtin
	// constructor; pipeline/finished live under 'stream/promises'.
	"stream":          "stream__kml_builtin",
	"stream/promises": "streampromises__kml_builtin",
}

// virtualModuleMembers is Stage 2's addition: the real "exported member"
// list per virtual specifier, used to validate a named import
// (`import { readFileSync } from 'fs'`) exactly the way a real file
// import's specifiers are already validated against its target's
// `exportedNames` (resolver.go). Kept in sync by hand against each
// specifier's own dispatch switch in codegen/llvm — there is no single
// source of truth to derive this from automatically, since the built-in
// dispatch tables live in Go source, not data.
var virtualModuleMembers = map[string]map[string]bool{
	"fs": {
		"readFileSync": true, "readFileSyncBytes": true, "writeFileSync": true,
		"appendFileSync": true, "existsSync": true, "unlinkSync": true,
		"mkdirSync": true, "rmdirSync": true, "renameSync": true,
		"copyFileSync": true, "readdirSync": true,
	},
	"path": {
		"join": true, "resolve": true, "dirname": true, "basename": true,
		"extname": true, "isAbsolute": true, "parse": true, "format": true,
		"sep": true, "delimiter": true,
	},
	"os": {
		"platform": true, "homedir": true, "tmpdir": true, "hostname": true,
		"totalmem": true, "freemem": true, "cpus": true, "EOL": true,
	},
	"querystring": {"parse": true, "stringify": true},
	"assert": {
		"ok": true, "equal": true, "strictEqual": true, "notEqual": true,
		"notStrictEqual": true, "fail": true, "throws": true,
	},
	"http":    {"listen": true, "close": true},
	"cluster": {"isPrimary": true, "workerId": true},
	"memory":  {"free": true},
	"stream": {
		"Readable": true, "Writable": true, "Duplex": true, "Transform": true,
	},
	"stream/promises": {"pipeline": true, "finished": true},
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
