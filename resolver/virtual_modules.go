package resolver

import (
	"strings"

	"KlainMainLang/ast"
)

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
	"fs":            "fs__kml_builtin",
	"fs/promises":   "fspromises__kml_builtin",
	"path":          "path__kml_builtin",
	"os":            "os__kml_builtin",
	"querystring":   "querystring__kml_builtin",
	"zlib":          "zlib__kml_builtin",
	"child_process": "childprocess__kml_builtin",
	"readline":      "readline__kml_builtin",
	"net":           "net__kml_builtin",
	"tls":           "tls__kml_builtin",
	"util":          "util__kml_builtin",
	"dns":           "dns__kml_builtin",
	"dgram":         "dgram__kml_builtin",
	"assert":        "assert__kml_builtin",
	"http":          "http__kml_builtin",
	// https shares the libcurl-backed client (get/request speak TLS for free);
	// https.createServer is a clean codegen rejection until the http accept
	// loop is TLS-wrapped.
	"https":      "https__kml_builtin",
	"stream/web": "streamweb__kml_builtin",
	// TDD-00139 Stage 1: the explicit http2 module. createServer shares the
	// http server core (which already speaks h2c on the same port); the
	// client/session surface arrives in later stages.
	"http2": "http2__kml_builtin",
	"diagnostics_channel": "diagch__kml_builtin",
	// TDD-00131: the `klain:` namespace holds this project's own bespoke
	// re-imaginings, distinct from the Node-faithful module names. `klain:http`
	// is the current `http.listen(handler ⇒ response)` server model, which is
	// NOT Node's `http.createServer` shape — kept intentionally, on the same
	// runtime, under an explicitly-non-Node specifier.
	"klain:http":    "http__kml_builtin",
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
	"cluster":       "cluster__kml_builtin",
	"memory":        "Memory__kml_builtin", // capitalized marker, matching Memory.free's existing capitalized surface
	// TDD-00097 Stage 8: Node's stream module. The class names bind as
	// identity (see resolver.go's stream special-case) — `new Readable(...)`
	// is recognized by name at parse time like every other builtin
	// constructor; pipeline/finished live under 'stream/promises'.
	"stream":          "stream__kml_builtin",
	"stream/promises": "streampromises__kml_builtin",
	// TDD-00098: worker_threads. All three members bind as identity (the
	// same posture as stream's class names): `new Worker(...)` is recognized
	// by name at parse time like every other builtin constructor, and
	// `parentPort`/`workerData` are reserved identifiers codegen only
	// accepts inside a worker entry module.
	"worker_threads": "workerthreads__kml_builtin",
	// TDD-00122: native testing helpers (`import { mustCall } from 'test'`).
	"test": "test__kml_builtin",
	// ADR-00434: the Node crypto *module* (generateKeyPair etc.), distinct
	// from (and re-exporting parts of) the ambient WebCrypto global.
	"crypto": "nodecrypto__kml_builtin",
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
		"createReadStream": true, "createWriteStream": true,
		// Async callback form (TDD-00107): fs.readFile(path, cb), etc.
		"readFile": true, "writeFile": true, "appendFile": true, "unlink": true,
		"mkdir": true, "rmdir": true, "rename": true, "copyFile": true,
		"readdir": true,
	},
	// Async Promise form (TDD-00107): import { readFile } from 'fs/promises'.
	"fs/promises": {
		"readFile": true, "writeFile": true, "appendFile": true, "unlink": true,
		"mkdir": true, "rmdir": true, "rename": true, "copyFile": true,
		"readdir": true,
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
	"zlib": {
		"gzipSync": true, "gunzipSync": true,
		"deflateSync": true, "inflateSync": true,
		"deflateRawSync": true, "inflateRawSync": true,
		"unzipSync": true,
		"gzip":      true, "gunzip": true,
		"deflate": true, "inflate": true,
		"deflateRaw": true, "inflateRaw": true,
		"unzip": true,
	},
	"child_process": {"spawn": true, "exec": true, "execFile": true, "fork": true, "spawnSync": true, "execSync": true, "execFileSync": true},
	"readline":      {"createInterface": true},
	"net":           {"createServer": true, "connect": true, "createConnection": true, "isIP": true, "isIPv4": true, "isIPv6": true},
	"tls":           {"connect": true, "createServer": true},
	"util":          {"inspect": true, "format": true},
	"dns":           {"lookup": true, "resolve4": true, "resolve": true},
	"dgram":         {"createSocket": true},
	"assert": {
		"ok": true, "equal": true, "strictEqual": true, "notEqual": true,
		"notStrictEqual": true, "fail": true, "throws": true,
	},
	"http":       {"listen": true, "close": true, "get": true, "request": true, "createServer": true, "closeAllConnections": true, "Agent": true},
	"klain:http":    {"listen": true, "close": true, "closeAllConnections": true},
	"klain:webview": {"Webview": true},
	"klain:assets":  {"embedDir": true},
	"klain:tty":     {"readByte": true, "readKey": true},
	"cluster":    {"isPrimary": true, "workerId": true, "isWorker": true, "fork": true},
	"memory":  {"free": true},
	"stream": {
		"Readable": true, "Writable": true, "Duplex": true, "Transform": true,
		"PassThrough": true,
		// Function members (callback forms; the Promise forms live under
		// 'stream/promises'). Dispatched via the marker, not identity.
		"pipeline": true, "finished": true, "duplexPair": true,
	},
	"https": {"get": true, "request": true, "Agent": true},
	"crypto": {
		"generateKeyPair": true, "generateKeyPairSync": true,
		"randomBytes": true, "randomUUID": true, "getRandomValues": true,
	},
	"diagnostics_channel": {"channel": true, "subscribe": true, "unsubscribe": true, "hasSubscribers": true, "tracingChannel": true},
	"http2": {
		"createServer": true, "createSecureServer": true, "connect": true,
		"constants": true, "getDefaultSettings": true,
		"getPackedSettings": true, "getUnpackedSettings": true,
	},
	// stream/web re-exports the WHATWG stream classes that already exist as
	// parse-time constructors — the names bind as identity, like stream's.
	"stream/web": {
		"ReadableStream": true, "WritableStream": true, "TransformStream": true,
		"CompressionStream": true, "DecompressionStream": true,
	},
	"stream/promises": {"pipeline": true, "finished": true},
	// MessageChannel/MessagePort/BroadcastChannel are the ambient TDD-00099
	// constructors re-exported under their Node module name — identity, like
	// Worker.
	"worker_threads": {
		"Worker": true, "parentPort": true, "workerData": true,
		"MessageChannel": true, "MessagePort": true, "BroadcastChannel": true,
	},
	"test": {
		// node:test runner surface (TDD-00140)
		"test": true, "it": true, "describe": true, "suite": true,
		"before": true, "after": true, "beforeEach": true, "afterEach": true,
		// call helpers
		"mustCall": true, "mustCallAtLeast": true, "mustNotCall": true,
		"mustSucceed": true, "skip": true, "expectsError": true, "expectWarning": true,
		// value probes
		"isWindows": true, "isLinux": true, "isMacOS": true, "hasCrypto": true,
		"hasIntl": true, "isMainThread": true,
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
}
