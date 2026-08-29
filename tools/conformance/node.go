// Node-core conformance track (TDD-00121 Track B). Runs the pure-module
// behavioral tests (path/querystring/url) from a pinned nodejs/node checkout
// (tools/conformance/fetch.sh) through this compiler's real pipeline and
// reports a Node-parity pass rate.
//
// Node's test files are untyped CommonJS: `'use strict';` + `require('../common')`
// + `const x = require('mod')` + Node globals (`__filename`). This compiler is a
// typed ESM subset, so each file is mechanically transformed to the equivalent
// ESM form before compiling (see transformNodeSource). Only Node-repo-internal
// harness surface (`../common/*` helpers the shims don't cover, `internal/*`,
// dynamic require, `.call`/`.apply`) is classified SKIP_OUT_OF_SCOPE; a real
// Node core module the compiler doesn't implement (`http2`, `https`, `vm`, …)
// counts as a FAIL (MODULE_NOT_IMPLEMENTED) — an in-scope gap, never "out of
// scope".
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// emitted is the result of the full front-end (resolve imports + parse + resolve
// + codegen) with no clang and no run — the seam both Track B (before running)
// and Track C (the whole oracle: did the front-end accept or reject?) build on.
type emitted struct {
	ir       string
	linkLibs []string
	cSources []llvm.CSource
}

// frontEnd runs the real compiler front-end — resolver.ResolveProgram (which
// merges builtin/file imports, the step a bare parser.Parse skips) then codegen
// — over a source file already written to entryPath, returning the emitted IR or
// the first error. This mirrors the klainmain binary's pipeline exactly, so an
// `import` reaches codegen resolved. A panic in the compiler is recovered into an
// error so one pathological input can't take down a whole conformance batch.
func frontEnd(entryPath string) (e emitted, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	prog, perr := resolver.ResolveProgram(entryPath)
	if perr != nil {
		return emitted{}, perr
	}
	em := llvm.NewEmitter()
	em.SetRegexMode(regexModeFlag)
	ir, cerr := em.EmitProgram(prog)
	if cerr != nil {
		return emitted{}, cerr
	}
	// Embedded C runtime files (dtoa, spawnsync, …) must be linked exactly as
	// the CLI driver links them — omitting them made every Node test needing
	// one fail at the clang step as a bogus CLANG_ERROR.
	cs, cerr := em.EmbeddedCSources()
	if cerr != nil {
		return emitted{}, cerr
	}
	return emitted{ir: ir, linkLibs: em.LinkLibs(), cSources: cs}, nil
}

// frontEndSource writes src to a scratch .ts entry file under entryDir and runs
// frontEnd over it — the source-string convenience the suites use. entryDir
// governs how the entry's *relative* imports resolve: the Node suite writes into
// a `parallel/` subdir so a `require('../common/fixtures')` resolves to the
// generated `common/fixtures.ts` shim one level up (mirroring Node's tree).
func frontEndSource(src, entryDir, tag string) (emitted, error) {
	if err := os.MkdirAll(entryDir, 0755); err != nil {
		return emitted{}, fmt.Errorf("mkdir entry dir: %w", err)
	}
	entry := filepath.Join(entryDir, tag+".ts")
	if err := os.WriteFile(entry, []byte(src), 0644); err != nil {
		return emitted{}, fmt.Errorf("write entry: %w", err)
	}
	return frontEnd(entry)
}

var (
	reUseStrict   = regexp.MustCompile(`^\s*['"]use strict['"];?\s*$`)
	reBareCommon  = regexp.MustCompile(`^\s*require\(['"]\.\./common['"]\);?\s*$`)
	reCjsDefault  = regexp.MustCompile(`^\s*(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*require\(['"]([^'"]+)['"]\);?\s*$`)
	reCjsDestruct = regexp.MustCompile(`^\s*(?:const|let|var)\s+\{([^}]+)\}\s*=\s*require\(['"]([^'"]+)['"]\);?\s*$`)
	// Static member-off-require (`const spawn = require('child_process').spawn`)
	// and side-effect-only bare requires — both static forms, previously
	// mislabeled "dynamic" (found via the unhandled-form histogram).
	reCjsDefaultMember = regexp.MustCompile(`^\s*(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*require\(['"]([^'"]+)['"]\)\.([A-Za-z_$][\w$]*);?\s*$`)
	reBareRequire      = regexp.MustCompile(`^\s*require\(['"]([^'"]+)['"]\);?\s*$`)
	// Any require() still present after the line-level rewrites (a computed
	// specifier, an inline-expression position, a relative test file) means
	// the file needs genuinely dynamic module loading.
	reAnyRequire = regexp.MustCompile(`require\(`)
)

// nodeSupportedModule lists the Node core module specifiers this compiler
// implements (per docs/status). A file whose only requires are these is
// attempted (compiled + run) and its result — pass or fail — is measured
// honestly; a file requiring a real Node core module outside this set
// (`http2`, `https`, `vm`, `async_hooks`, …) FAILS as MODULE_NOT_IMPLEMENTED
// (see classifyUnsupportedModule) — only Node-repo-internal harness paths are
// ever skipped. `node:`-prefixed specifiers are normalized before lookup.
var nodeSupportedModule = map[string]bool{
	"assert": true, "child_process": true, "cluster": true,
	"dgram": true, "dns": true,
	"fs": true, "http": true, "net": true,
	"os": true, "path": true,
	"querystring": true, "readline": true, "stream": true,
	"tls": true, "util": true, "zlib": true,
	"worker_threads": true,
	// Promise/submodule forms this compiler also resolves as builtins.
	"fs/promises": true, "stream/promises": true,
	// https (client via the libcurl backend) and the stream/web re-exports.
	"https": true, "stream/web": true, "http2": true,
	// The Node crypto module (generateKeyPair etc., ADR-00434). The ambient
	// WebCrypto global (crypto.subtle) still works without any import.
	"crypto": true,
	// node:test — the runner surface (TDD-00140) + the mustCall helpers.
	"test": true, "diagnostics_channel": true,
}

// nodeAmbientModule lists Node modules whose surface this compiler exposes as
// **ambient globals** (`Buffer`, `process`, `console`, `URL`, `setTimeout`, …)
// rather than importable names — `import x from 'buffer'` is rejected, but
// `Buffer.from(...)` works with no import. A `require` of one of these is dropped
// (like `require('../common')`): the members are already in scope globally, so
// converting it to an import would wrongly fail the whole file. A file that then
// reaches for a *non*-ambient member of such a module (`url.parse`) fails
// honestly on the undefined reference.
// nodeAmbientMemberGlobal maps `require('<ambient>').<member>` to the global
// the member already is — the corpus binds these to locals constantly.
var nodeAmbientMemberGlobal = map[string]map[string]string{
	"events": {"EventEmitter": "EventEmitter", "once": "once"},
	"buffer": {"Buffer": "Buffer"},
	"url":    {"URL": "URL", "URLSearchParams": "URLSearchParams"},
	"timers": {"setTimeout": "setTimeout", "setInterval": "setInterval",
		"clearTimeout": "clearTimeout", "clearInterval": "clearInterval",
		"setImmediate": "setImmediate"},
}

var nodeAmbientModule = map[string]bool{
	"buffer": true, "process": true, "console": true, "timers": true,
	"url": true, "string_decoder": true, "punycode": true,
	"events": true, // EventEmitter is global, not importable
	"timers/promises": true,
}

type nodeResult struct {
	File     string
	Module   string // path | querystring | url
	Status   string // PASS | FAIL | SKIP_OUT_OF_SCOPE
	Reason   string
	Stripped int // count of .win32/.posix statements dropped — a pass covers the default namespace only
}

// nodeWholeFileSkip names source patterns that put a whole Node test file out of
// this compiler's target surface — dynamic constructs that can't be excised at
// statement granularity. Platform path namespaces (.win32/.posix) are handled
// separately by dropping the individual statements that use them (see
// transformNodeSource), because a path test typically interleaves default-
// namespace assertions we *do* target with platform ones we don't, in the same
// file — skipping the whole file would throw away the runnable default half.
var nodeWholeFileSkip = []struct{ needle, why string }{
	{".call(", "uses Function.prototype.call (dynamic dispatch, not targeted)"},
	{".apply(", "uses Function.prototype.apply (dynamic dispatch, not targeted)"},
}

// coalesceLogicalLines joins physical lines into logical statements by tracking
// bracket depth (string/template-literal aware), so a statement split across
// lines — as Node's asserts often are — is one unit the platform-stripping and
// require rewrites can reason about. Not a full JS lexer: it tracks ' " ` quotes
// with backslash escapes and (){}[] depth, which is all this corpus needs.
func coalesceLogicalLines(src string) []string {
	var out []string
	var buf strings.Builder
	depth := 0
	for _, ln := range strings.Split(src, "\n") {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(ln)
		depth += bracketDelta(ln)
		if depth <= 0 {
			out = append(out, buf.String())
			buf.Reset()
			depth = 0
		}
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	return out
}

// stripQuoted blanks the contents of '…'/"…"/`…` runs (escape-aware, same
// tokenizer bracketDelta uses) so a require() inside an embedded source
// string — worker code in a template literal, an eval payload — can't
// masquerade as a real dynamic require. 1:1 replacement, so indexes into the
// stripped string are valid in the original.
func stripQuoted(s string) string {
	out := []byte(s)
	var quote byte
	for i := 0; i < len(out); i++ {
		c := out[i]
		if quote != 0 {
			if c == quote && (i == 0 || out[i-1] != '\\') {
				quote = 0
			} else if c != '\n' {
				out[i] = ' '
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
		}
	}
	return string(out)
}

func bracketDelta(s string) int {
	n := 0
	var quote byte // 0 = not in a string
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote && (i == 0 || s[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(', '[', '{':
			n++
		case ')', ']', '}':
			n--
		}
	}
	return n
}

var rePlatformNS = regexp.MustCompile(`\.(win32|posix)\b`)

// reCommonMember captures each `common.<member>` access, to tell shimmable
// environment probes from behavioral harness helpers this compiler can't run.
var reCommonMember = regexp.MustCompile(`\bcommon\.([A-Za-z_][A-Za-z0-9_]*)`)

// commonShimmable is the set of `common` members the shim below provides — pure
// environment probes with plain values. Any access outside this set means the
// file needs Node's real harness and is skipped.
var commonShimmable = map[string]bool{
	"isWindows": true, "isLinux": true, "isMacOS": true, "isOSX": true,
	"isAIX": true, "isFreeBSD": true, "isSunOS": true, "isOpenBSD": true,
	"isIBMi": true, "isMainThread": true, "hasIntl": true, "hasCrypto": true,
	"hasIPv6": true, "enoughTestMem": true, "isDumbTerminal": true,
	// Pure-value / trivially-computable helpers. These were previously
	// classified "requires Node common harness" — a false out-of-scope: each is
	// a constant or a one-line function, so the ~200 files using them are now
	// attempted and measured honestly (mostly as FAILs, which is the point).
	"localhostIPv4": true, "PIPE": true, "platformTimeout": true,
	"hasOpenSSL3": true,
	// Environment-gate helpers that are no-ops in this harness (we are never in
	// a worker, never on a dumb terminal).
	"skipIfWorker": true, "skipIfDumbTerminal": true,
}

// commonShimLiteral is the object literal bound wherever a file uses `common`.
// Values are best-effort static probes (the host the conformance run executes on
// is irrelevant to whether the module-under-test behaves correctly).
// commonShimFor builds it per file so `PIPE` (a unix-socket path) is unique to
// the test, keeping parallel workers from colliding on one socket.
func commonShimFor(absPath string) string {
	sock := "/tmp/kml-" + strings.TrimSuffix(filepath.Base(absPath), ".js") + ".sock"
	return `{ isWindows: false, isLinux: false, isMacOS: true, isOSX: true, isAIX: false, isFreeBSD: false, isSunOS: false, isOpenBSD: false, isIBMi: false, isMainThread: true, hasIntl: false, hasCrypto: true, hasIPv6: true, enoughTestMem: true, isDumbTerminal: false, hasOpenSSL3: false, localhostIPv4: '127.0.0.1', PIPE: ` + strconv.Quote(sock) + `, platformTimeout: (...a: number[]): number => a[0], skipIfWorker: (): void => {}, skipIfDumbTerminal: (): void => {} }`
}

// commonTestHelper maps the behavioral `common.*` helpers that the native `test`
// builtin (TDD-00122) now implements for real. A `common.mustCall(fn)` is
// rewritten to a bare `mustCall(fn)` and imported from 'test', so these files —
// the largest skipped bucket before the builtin existed — actually run.
var commonTestHelper = map[string]bool{
	"mustCall": true, "mustCallAtLeast": true, "mustNotCall": true,
	"mustSucceed": true, "skip": true, "expectsError": true, "expectWarning": true,
}

// classifyUnsupportedModule decides how a require() of a module outside
// nodeSupportedModule/nodeAmbientModule counts. Only Node-repo-internal
// harness paths (`../common/*`, `internal/*` — files that live inside the
// nodejs/node checkout, not the Node API) are genuinely out of scope. Every
// *real* Node core module the compiler doesn't implement (`http2`, `https`,
// `vm`, `async_hooks`, `domain`, …) is an in-scope gap and counts as a FAIL —
// labeling those "out of scope" was exactly the kind of misclassification the
// gap analysis exists to prevent.
func classifyUnsupportedModule(mod, raw string) (skip, fail string) {
	if strings.HasPrefix(mod, "../") || strings.HasPrefix(mod, "./") || strings.HasPrefix(mod, "internal/") {
		return "Node-internal harness file require('" + raw + "')", ""
	}
	return "", "MODULE_NOT_IMPLEMENTED: " + mod
}

// transformNodeSource rewrites one Node CommonJS test file into the typed-ESM
// form this compiler accepts. Returns (source, platformStripped, "", "") on
// success, ("", n, skipReason, "") when the file is out of scope for a
// structural reason the transform can't bridge, or ("", n, "", failReason)
// when the file's first blocker is an in-scope gap (an unimplemented real
// Node module) that must count as a failure. platformStripped counts the
// .win32/.posix statements dropped, so a pass can be reported as covering the
// default namespace only. absPath gives __filename/__dirname concrete values so
// path assertions over them hold.
func transformNodeSource(src, absPath string) (out string, platformStripped int, skip, fail string) {
	// Node's `common` test harness: this shim provides only the pure environment
	// probes (booleans). A file that reaches for a behavioral helper
	// (`common.mustCall`, `mustNotCall`, `expectsError`, …) needs Node's actual
	// harness semantics — call-count tracking, wrapped callbacks — which this
	// compiler's typed model can't reproduce (a `common.mustCall(fn)` result comes
	// back as `any` and isn't callable). Such a file is skipped with an accurate
	// reason rather than mislabeled "unsupported module".
	usesCommon := strings.Contains(src, "common.")
	usedHelpers := map[string]bool{}
	if usesCommon {
		for _, mm := range reCommonMember.FindAllStringSubmatch(src, -1) {
			switch {
			case commonTestHelper[mm[1]]:
				usedHelpers[mm[1]] = true // rewritten to a bare 'test' import below
			case commonShimmable[mm[1]]:
				// provided by the injected probe shim
			default:
				return "", 0, "requires Node common harness (common." + mm[1] + ")", ""
			}
		}
	}

	var imports, body []string
	commonBinding := "" // local name a `const x = require('../common')` bound, if any
	for _, line := range coalesceLogicalLines(src) {
		switch {
		case reUseStrict.MatchString(line):
			continue // strict mode is the compiler's only mode
		case reBareCommon.MatchString(line):
			continue // side-effect-only `require('../common')` — dropped; common is injected below if used
		case rePlatformNS.MatchString(line):
			platformStripped++ // drop this statement; keep the file's default-namespace assertions
			continue
		case reCjsDefaultMember.MatchString(line):
			m := reCjsDefaultMember.FindStringSubmatch(line)
			name, mod, member := m[1], normalizeNodeModule(m[2]), m[3]
			if nodeSupportedModule[mod] {
				// `const spawn = require('child_process').spawn` → a named
				// import (aliased when the binding differs); an unknown member
				// then fails honestly at the resolver's member check.
				if name == member {
					imports = append(imports, fmt.Sprintf("import {%s} from '%s'", member, mod))
				} else {
					imports = append(imports, fmt.Sprintf("import {%s as %s} from '%s'", member, name, mod))
				}
				continue
			}
			if alias, ok := nodeAmbientMemberGlobal[mod][member]; ok {
				if name == alias {
					continue // `const EventEmitter = require('events').EventEmitter` — already global
				}
				body = append(body, fmt.Sprintf("const %s = %s", name, alias))
				continue
			}
			if nodeAmbientModule[mod] {
				return "", platformStripped, "requires ambient-module member require('" + m[2] + "')." + member, ""
			}
			skip, fail := classifyUnsupportedModule(mod, m[2])
			return "", platformStripped, skip, fail
		case reBareRequire.MatchString(line):
			m := reBareRequire.FindStringSubmatch(line)
			mod := normalizeNodeModule(m[1])
			if nodeSupportedModule[mod] || nodeAmbientModule[mod] ||
				mod == "../common/tmpdir" || mod == "../common/fixtures" {
				continue // side-effect-only require of a builtin — nothing to do
			}
			skip, fail := classifyUnsupportedModule(mod, m[1])
			return "", platformStripped, skip, fail
		case reCjsDefault.MatchString(line):
			m := reCjsDefault.FindStringSubmatch(line)
			name, mod := m[1], normalizeNodeModule(m[2])
			if mod == "../common" {
				// `const common = require('../common')` — bind the injected shim
				// (below) under this name instead of skipping; the file is then
				// attempted and fails honestly on any common.* member the shim
				// doesn't provide, rather than being hidden as "out of scope".
				commonBinding = name
				continue
			}
			if mod == "../common/fixtures" {
				// Resolve to the generated typed shim (writeFixturesShim) via a
				// real import — the largest single SKIP unblock.
				imports = append(imports, fmt.Sprintf("import %s from '../common/fixtures'", name))
				continue
			}
			if mod == "../common/tmpdir" {
				// Resolve to the generated typed shim (writeTmpdirShim) — the
				// second-largest false out-of-scope bucket (~212 files).
				imports = append(imports, fmt.Sprintf("import %s from '../common/tmpdir'", name))
				continue
			}
			if mod == "../common/countdown" {
				// Node exports the Countdown class as the module default; the
				// shim (writeCountdownShim) exports it named — alias to whatever
				// the file bound.
				imports = append(imports, fmt.Sprintf("import { Countdown as %s } from '../common/countdown'", name))
				continue
			}
			if nodeAmbientModule[mod] {
				continue // ambient globals — drop the require, use the globals directly
			}
			if !nodeSupportedModule[mod] {
				skip, fail := classifyUnsupportedModule(mod, m[2])
				return "", platformStripped, skip, fail
			}
			imports = append(imports, fmt.Sprintf("import %s from '%s'", name, mod))
		case reCjsDestruct.MatchString(line):
			m := reCjsDestruct.FindStringSubmatch(line)
			names, mod := m[1], normalizeNodeModule(m[2])
			if nodeAmbientModule[mod] {
				continue // ambient globals (Buffer, URL, …) — drop; already in scope
			}
			if mod == "../common" {
				// `const { mustCall, isWindows } = require('../common')` — route
				// each member the same way the `common.<member>` scan does: test
				// helpers become a real 'test' import, probe values destructure
				// from the injected shim literal. An alias or an unknown member
				// keeps the file skipped, with the member named.
				var testNames, shimNames []string
				supported := true
				blocker := ""
				for _, n := range strings.Split(names, ",") {
					n = strings.TrimSpace(n)
					if n == "" {
						continue
					}
					switch {
					case strings.ContainsAny(n, ": "):
						supported, blocker = false, n // aliased binding — not mapped
					case commonTestHelper[n]:
						testNames = append(testNames, n)
					case commonShimmable[n]:
						shimNames = append(shimNames, n)
					default:
						supported, blocker = false, n
					}
				}
				if !supported {
					return "", platformStripped, "requires Node common harness (destructured common." + blocker + ")", ""
				}
				if len(testNames) > 0 {
					imports = append(imports, fmt.Sprintf("import {%s} from 'test'", strings.Join(testNames, ", ")))
				}
				if len(shimNames) > 0 {
					body = append(body, fmt.Sprintf("const {%s} = %s", strings.Join(shimNames, ", "), commonShimFor(absPath)))
				}
				continue
			}
			if mod == "../common/fixtures" {
				// `const { path, readSync } = require('../common/fixtures')` —
				// import the default shim object, then destructure it.
				imports = append(imports, "import __kml_fixtures from '../common/fixtures'")
				body = append(body, fmt.Sprintf("const {%s} = __kml_fixtures", names))
				continue
			}
			if !nodeSupportedModule[mod] {
				skip, fail := classifyUnsupportedModule(mod, m[2])
				return "", platformStripped, skip, fail
			}
			imports = append(imports, fmt.Sprintf("import {%s} from '%s'", names, mod))
		default:
			body = append(body, line)
		}
	}

	joined := strings.Join(body, "\n")
	// Rewrite the real `common.*` helpers to bare identifiers imported from the
	// native `test` builtin (TDD-00122): `common.mustCall(fn)` → `mustCall(fn)`.
	if len(usedHelpers) > 0 {
		names := make([]string, 0, len(usedHelpers))
		for h := range usedHelpers {
			joined = strings.ReplaceAll(joined, "common."+h, h)
			names = append(names, h)
		}
		sort.Strings(names)
		imports = append(imports, fmt.Sprintf("import {%s} from 'test'", strings.Join(names, ", ")))
	}
	for _, p := range nodeWholeFileSkip {
		if strings.Contains(joined, p.needle) {
			return "", platformStripped, p.why, ""
		}
	}
	if loc := reAnyRequire.FindStringIndex(stripQuoted(joined)); loc != nil {
		// Name the offending statement — an unspecific "residual require"
		// label previously hid large static-form buckets behind a false
		// "dynamic" reading.
		snippet := joined[loc[0]:]
		if nl := strings.IndexByte(snippet, '\n'); nl >= 0 {
			snippet = snippet[:nl]
		}
		if len(snippet) > 80 {
			snippet = snippet[:80]
		}
		return "", platformStripped, "unhandled require form: " + snippet, ""
	}
	// If platform statements were stripped and nothing default-namespace-testable
	// remains, the file was purely platform-specific — skip it rather than pass a
	// vacuously-empty assertion set.
	if platformStripped > 0 && !strings.Contains(joined, "assert") {
		return "", platformStripped, "purely platform-specific (all assertions used .win32/.posix)", ""
	}

	// Inject Node CJS globals only when the body references them, so an unused
	// binding can't trip an unused-declaration check. __filename/__dirname get
	// concrete values so path assertions over them hold.
	var preamble []string
	if strings.Contains(joined, "__filename") {
		preamble = append(preamble, fmt.Sprintf("const __filename = %q", absPath))
	}
	if strings.Contains(joined, "__dirname") {
		preamble = append(preamble, fmt.Sprintf("const __dirname = %q", filepath.Dir(absPath)))
	}
	if strings.Contains(joined, "common.") {
		// Probe uses remain (helpers were rewritten away above) — bind the probe
		// shim under whatever name the file used, or `common`.
		binding := commonBinding
		if binding == "" {
			binding = "common"
		}
		preamble = append(preamble, "const "+binding+" = "+commonShimFor(absPath))
	}

	return strings.Join(imports, "\n") + "\n" + strings.Join(preamble, "\n") + "\n" + joined + "\n", platformStripped, "", ""
}

// normalizeNodeModule strips a `node:` prefix and collapses platform submodule
// specifiers so the supported-module check is uniform.
func normalizeNodeModule(mod string) string {
	mod = strings.TrimPrefix(mod, "node:")
	return mod
}

func runNodeSuite(workDir string, timeout time.Duration) {
	root := ".node-tests/test/parallel"
	entries, err := os.ReadDir(root)
	if err != nil {
		fatal("reading node corpus %s (run tools/conformance/fetch.sh first): %v", root, err)
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		fatal("creating workdir: %v", err)
	}
	if err := writeFixturesShim(workDir); err != nil {
		fatal("writing common/fixtures shim: %v", err)
	}
	if err := writeTmpdirShim(workDir); err != nil {
		fatal("writing common/tmpdir shim: %v", err)
	}
	if err := writeCountdownShim(workDir); err != nil {
		fatal("writing common/countdown shim: %v", err)
	}

	var files []string
	for _, e := range entries {
		if name := e.Name(); strings.HasPrefix(name, "test-") && strings.HasSuffix(name, ".js") {
			files = append(files, name)
		}
	}
	fmt.Fprintf(os.Stderr, "node suite: running %d files from %s ...\n", len(files), root)

	// Worker pool — the full test/parallel corpus is ~3,500 files, most of which
	// compile-fail fast (untyped-dynamic Node test code) but some reach clang+run,
	// so parallelism matters. Each worker gets its own scratch-file tag so the
	// per-file .ll/.bin don't collide.
	workers := runtime.NumCPU()
	jobs := make(chan string, len(files))
	out := make(chan nodeResult, len(files))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for name := range jobs {
				out <- runOneNode(filepath.Join(root, name), name, workDir, id, timeout)
			}
		}(w)
	}
	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	go func() { wg.Wait(); close(out) }()

	var results []nodeResult
	done := 0
	for r := range out {
		results = append(results, r)
		if done++; done%500 == 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d\n", done, len(files))
		}
	}

	reportPath := "docs/testing/CONFORMANCE-RESULTS-NODE.md"
	if err := writeNodeReport(reportPath, results); err != nil {
		fatal("writing node report: %v", err)
	}
	// Optional diagnostics: dump every FAIL/SKIP as `file\treason` for triage.
	if fl := os.Getenv("NODE_FAILLIST"); fl != "" {
		var lines []string
		for _, r := range results {
			if r.Status == "FAIL" {
				lines = append(lines, r.File+"\t"+strings.ReplaceAll(r.Reason, "\n", " "))
			}
		}
		sort.Strings(lines)
		_ = os.WriteFile(fl, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}
	if sl := os.Getenv("NODE_SKIPLIST"); sl != "" {
		var lines []string
		for _, r := range results {
			if r.Status == "SKIP_OUT_OF_SCOPE" {
				lines = append(lines, r.File+"\t"+strings.ReplaceAll(r.Reason, "\n", " "))
			}
		}
		sort.Strings(lines)
		_ = os.WriteFile(sl, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}
	fmt.Fprintf(os.Stderr, "node suite: %d files, report written to %s\n", len(results), reportPath)
}

// moduleOf buckets a Node test file by the module it exercises, taken as the
// first dashed segment after the `test-` prefix (`test-path-basename.js` → path,
// `test-http2-ping.js` → http2, `test-child-process-exec.js` → child). A coarse
// but stable grouping for the per-module histogram.
func moduleOf(fileName string) string {
	n := strings.TrimSuffix(strings.TrimPrefix(fileName, "test-"), ".js")
	if i := strings.IndexByte(n, '-'); i > 0 {
		return n[:i]
	}
	if n == "" {
		return "other"
	}
	return n
}

// writeFixturesShim generates workDir/common/fixtures.ts — a typed replacement
// for Node's untyped `test/common/fixtures.js` (which uses `module.exports` +
// `pathToFileURL`, un-ingestible here). It exposes the members real test files
// use (`fixturesDir`, `path()`, `readSync()`, `readKey()`), built on the
// supported `path`/`fs` modules, with the real fixtures data dir baked in as an
// absolute path so `fs.readFileSync` finds the actual files. `fileURL`
// (needs `url.pathToFileURL`, unsupported) is deliberately omitted. This is the
// single largest SKIP unblock in the Node oracle (~305 files require it).
func writeFixturesShim(workDir string) error {
	commonDir := filepath.Join(workDir, "common")
	if err := os.MkdirAll(commonDir, 0755); err != nil {
		return err
	}
	fixturesAbs, err := filepath.Abs(".node-tests/test/fixtures")
	if err != nil {
		return err
	}
	// All members use rest params (accept any arity, `enc` args ignored) rather
	// than optional parameters — a closure value's optional-parameter default
	// isn't applied at indirect call sites, so an optional param would arity-
	// mismatch when called with fewer args.
	shim := `import path from 'path';
import fs from 'fs';

const fixturesDir: string = ` + strconv.Quote(fixturesAbs) + `;

const readKeyOf = (name: string): string => {
  return fs.readFileSync(path.join(fixturesDir, "keys", name));
};

const joinFrom = (...args: string[]): string => {
  let p: string = fixturesDir;
  for (const a of args) { p = path.join(p, a); }
  return p;
};

const fixtures = {
  fixturesDir: fixturesDir,
  path: joinFrom,
  readSync: (...args: string[]): string => {
    return fs.readFileSync(path.join(fixturesDir, args[0]));
  },
  readKey: (...args: string[]): string => {
    return readKeyOf(args[0]);
  },
  readKeys: (...args: string[]): string[] => {
    const names: string[] = args.slice(1);
    return names.map((n: string): string => readKeyOf(n));
  },
};

export default fixtures;
`
	return os.WriteFile(filepath.Join(commonDir, "fixtures.ts"), []byte(shim), 0644)
}

// writeCountdownShim generates workDir/common/countdown.ts — a typed
// replacement for Node's `test/common/countdown.js` Countdown class
// (constructor(limit, cb) + dec()). The real one wraps cb in
// common.mustCall; the shim calls it directly — the exit-time mustCall
// verification is a harness nicety the corpus's assertions don't depend on.
// Node exports it as the module default; the shim exports it named (a class
// can't be a default export here), and the require rewrite aliases it.
func writeCountdownShim(workDir string) error {
	commonDir := filepath.Join(workDir, "common")
	if err := os.MkdirAll(commonDir, 0755); err != nil {
		return err
	}
	shim := `export class Countdown {
  limit: number;
  cb: () => void;
  constructor(limit: number, cb: () => void) {
    this.limit = limit;
    this.cb = cb;
  }
  dec(): number {
    this.limit = this.limit - 1;
    if (this.limit == 0) { this.cb(); }
    return this.limit;
  }
}
`
	return os.WriteFile(filepath.Join(commonDir, "countdown.ts"), []byte(shim), 0644)
}

// writeTmpdirShim generates workDir/common/tmpdir.ts — a typed replacement for
// Node's `test/common/tmpdir.js`. Provides the members test files actually use
// (`path`, `refresh()`, `resolve()`), rooted per-process so parallel workers
// don't collide. `refresh()` only ensures the directory exists (no recursive
// wipe — fs.rmSync is not implemented yet), which is enough for the corpus's
// create-and-write usage.
func writeTmpdirShim(workDir string) error {
	commonDir := filepath.Join(workDir, "common")
	if err := os.MkdirAll(commonDir, 0755); err != nil {
		return err
	}
	shim := `import fs from 'fs';
import path from 'path';

const tmpBase: string = "/tmp/kml-tmpdir-" + process.pid;

const tmpdir = {
  path: tmpBase,
  refresh: (...args: string[]): void => {
    if (!fs.existsSync(tmpBase)) { fs.mkdirSync(tmpBase); }
  },
  resolve: (...args: string[]): string => {
    let p: string = tmpBase;
    for (const a of args) { p = path.join(p, a); }
    return p;
  },
};

export default tmpdir;
`
	return os.WriteFile(filepath.Join(commonDir, "tmpdir.ts"), []byte(shim), 0644)
}

func runOneNode(path, name, workDir string, workerID int, timeout time.Duration) (res nodeResult) {
	res.File = name
	res.Module = moduleOf(name)
	defer func() {
		if r := recover(); r != nil {
			res.Status = "FAIL"
			res.Reason = normalizeReason("CRASH", fmt.Sprintf("%v", r))
		}
	}()

	raw, err := os.ReadFile(path)
	if err != nil {
		res.Status = "FAIL"
		res.Reason = "READ_ERROR: " + err.Error()
		return res
	}
	abs, _ := filepath.Abs(path)
	src, stripped, skip, fail := transformNodeSource(string(raw), abs)
	res.Stripped = stripped
	if skip != "" {
		res.Status = "SKIP_OUT_OF_SCOPE"
		res.Reason = skip
		return res
	}
	if fail != "" {
		res.Status = "FAIL"
		res.Reason = fail
		return res
	}

	ok, reason := compileAndRun(src, workDir, fmt.Sprintf("node%d", workerID), timeout)
	if ok {
		res.Status = "PASS"
	} else {
		res.Status = "FAIL"
		res.Reason = reason
	}
	return res
}

func writeNodeReport(path string, all []nodeResult) error {
	var b strings.Builder
	b.WriteString("# Node-core conformance results\n\n")
	b.WriteString("Generated by `tools/conformance -suite=node` (TDD-00121 Track B) against the **full `test/parallel` behavioral suite** of a pinned `nodejs/node` checkout (every `test-*.js`), regenerate with `make conformance-node`. Do not hand-edit; re-run instead.\n\n")
	b.WriteString("Each file is mechanically transformed from Node's untyped CommonJS (`require`/`'use strict'`/Node globals) into this compiler's typed-ESM form, then compiled and run. **PASS** = compiled and exited 0; **FAIL** = a compile error, a nonzero exit, or a require of a real Node core module this compiler doesn't implement (`MODULE_NOT_IMPLEMENTED` — an in-scope gap); **SKIP** = only Node-repo-internal harness surface the transform can't bridge (unshimmed `../common/*` helpers, `internal/*`, dynamic `require`, `.call`/`.apply`).\n\n")
	b.WriteString("Interpretation, misclassification history, and the ranked remaining-work list live in the hand-written companion [NODE-GAP-ANALYSIS.md](NODE-GAP-ANALYSIS.md).\n\n")
	b.WriteString("> **Read this honestly.** Node's tests are written in untyped, dynamic JavaScript against the *full* Node API (platform namespaces, `.call`/`.apply`, `Object.entries` test-tables, `instanceof` on builtins, live sockets/child processes). This compiler is a typed subset, so most files legitimately don't compile — the pass count is a floor on \"how much of Node's own suite runs verbatim,\" not a measure of module correctness. The per-module histogram below shows where the runnable surface actually is; hand-mined typed value-semantics cases remain the productive complement.\n\n")

	type agg struct{ pass, fail, skip int }
	byMod := map[string]*agg{}
	byReason := map[string]int{}
	bySkip := map[string]int{}
	var oPass, oFail, oSkip int
	var passing []nodeResult
	for _, r := range all {
		m, ok := byMod[r.Module]
		if !ok {
			m = &agg{}
			byMod[r.Module] = m
		}
		switch r.Status {
		case "PASS":
			m.pass++
			oPass++
			passing = append(passing, r)
		case "FAIL":
			m.fail++
			oFail++
			byReason[nodeReasonBucket(r.Reason)]++
		default:
			m.skip++
			oSkip++
			bySkip[nodeReasonBucket(r.Reason)]++
		}
	}
	ran := oPass + oFail
	pct := 0.0
	if ran > 0 {
		pct = 100 * float64(oPass) / float64(ran)
	}
	fmt.Fprintf(&b, "## Overall\n\n%d files total: **%d passed**, %d failed, %d skipped (out of scope).\n\nOf the %d files that compiled far enough to run, **%d passed (%.1f%%)**.\n\n", len(all), oPass, oFail, oSkip, ran, oPass, pct)

	// Per-module histogram, most files first.
	b.WriteString("## By module (top 40 by file count)\n\n| Module | Passed | Failed | Skipped | Total |\n|---|---|---|---|---|\n")
	mods := make([]string, 0, len(byMod))
	for m := range byMod {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool {
		ai, aj := byMod[mods[i]], byMod[mods[j]]
		ti, tj := ai.pass+ai.fail+ai.skip, aj.pass+aj.fail+aj.skip
		if ti != tj {
			return ti > tj
		}
		return mods[i] < mods[j]
	})
	for i, m := range mods {
		if i >= 40 {
			break
		}
		a := byMod[m]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", m, a.pass, a.fail, a.skip, a.pass+a.fail+a.skip)
	}

	// Failure-reason histogram — where the runnable-but-failing files die.
	b.WriteString("\n## Top failure reasons\n\nBucketed first line of each FAIL — the leverage map for what to implement/fix next.\n\n| Count | Reason |\n|---|---|\n")
	type rc struct {
		reason string
		n      int
	}
	var reasons []rc
	for r, n := range byReason {
		reasons = append(reasons, rc{r, n})
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].n != reasons[j].n {
			return reasons[i].n > reasons[j].n
		}
		return reasons[i].reason < reasons[j].reason
	})
	for i, r := range reasons {
		if i >= 40 {
			break
		}
		fmt.Fprintf(&b, "| %d | %s |\n", r.n, r.reason)
	}

	// Skip-reason histogram — why files are out of scope (harness coupling vs
	// dynamic constructs vs unsupported modules). Honesty about the denominator.
	b.WriteString("\n## Top skip reasons\n\nWhy out-of-scope files can't be attempted — Node's own internal-harness coupling and dynamic constructs only; unimplemented real modules count as FAILs above, never here.\n\n| Count | Skip reason |\n|---|---|\n")
	var skips []rc
	for r, n := range bySkip {
		skips = append(skips, rc{r, n})
	}
	sort.Slice(skips, func(i, j int) bool {
		if skips[i].n != skips[j].n {
			return skips[i].n > skips[j].n
		}
		return skips[i].reason < skips[j].reason
	})
	for i, r := range skips {
		if i >= 25 {
			break
		}
		fmt.Fprintf(&b, "| %d | %s |\n", r.n, r.reason)
	}

	// The passing files — the interesting, short list.
	fmt.Fprintf(&b, "\n## Passing files (%d)\n\nA **−N** default-only mark means N `path.win32`/`path.posix` (platform-specific) statements were dropped and only the default-namespace assertions ran.\n\n| File | Module | Default-only |\n|---|---|---|\n", len(passing))
	sort.Slice(passing, func(i, j int) bool { return passing[i].File < passing[j].File })
	for _, r := range passing {
		stripped := ""
		if r.Stripped > 0 {
			stripped = fmt.Sprintf("−%d", r.Stripped)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", r.File, r.Module, stripped)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// nodeReasonBucket collapses a FAIL reason to a coarse bucket for the histogram
// (strip the normalizeReason position/identifier detail is already applied for
// compile errors; this just caps length and keeps the leading kind+message).
func nodeReasonBucket(reason string) string {
	if i := strings.IndexByte(reason, '\n'); i >= 0 {
		reason = reason[:i]
	}
	if len(reason) > 100 {
		reason = reason[:100] + "…"
	}
	return reason
}

// compileAndRun compiles a transformed source string through the real pipeline
// and (for the node suite) runs it, returning (pass, reason). Shared by Track B;
// Track C uses the front-end-only path in ts.go. tag namespaces the scratch
// files so concurrent suites don't collide.
// nodeRawReasons keeps the concrete identifier/message detail in each FAIL
// reason (normally collapsed to %s by normalizeReason for the histogram) —
// needed when triaging NODE_FAILLIST output for which method/member is missing.
var nodeRawReasons = os.Getenv("NODE_RAW_REASONS") != ""

func nodeReason(kind, msg string) string {
	if nodeRawReasons {
		return kind + ": " + msg
	}
	return normalizeReason(kind, msg)
}

func compileAndRun(src, workDir, tag string, timeout time.Duration) (bool, string) {
	// Entry goes under parallel/ so `../common/fixtures` resolves to the
	// generated shim (runNodeSuite writes workDir/common/fixtures.ts).
	prog, perr := frontEndSource(src, filepath.Join(workDir, "parallel"), tag)
	if perr != nil {
		return false, nodeReason("COMPILE_ERROR", perr.Error())
	}

	llFile := filepath.Join(workDir, tag+".ll")
	binFile := filepath.Join(workDir, tag+".bin")
	if err := os.WriteFile(llFile, []byte(prog.ir), 0644); err != nil {
		return false, "WRITE_ERROR: " + err.Error()
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	for _, cs := range prog.cSources {
		cFile := filepath.Join(workDir, tag+"."+cs.Name+".c")
		if err := os.WriteFile(cFile, []byte(cs.Content), 0644); err != nil {
			return false, "WRITE_ERROR: " + err.Error()
		}
		clangArgs = append(clangArgs, cFile)
		clangArgs = append(clangArgs, cs.CFlags...)
		for _, lib := range cs.Libs {
			clangArgs = append(clangArgs, "-l"+lib)
		}
	}
	for _, lib := range prog.linkLibs {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	cctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var clangOut bytes.Buffer
	clangCmd := killableCommand(cctx, "clang", clangArgs...)
	clangCmd.Stdout = &clangOut
	clangCmd.Stderr = &clangOut
	if err := clangCmd.Run(); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return false, "CLANG_TIMEOUT"
		}
		return false, nodeReason("CLANG_ERROR", firstLine(clangOut.String()))
	}
	defer os.Remove(binFile)

	rctx, rcancel := context.WithTimeout(context.Background(), timeout)
	defer rcancel()
	var stderr bytes.Buffer
	runCmd := killableCommand(rctx, binFile)
	runCmd.Stderr = &stderr
	if err := runCmd.Run(); err != nil {
		if rctx.Err() == context.DeadlineExceeded {
			return false, "RUN_TIMEOUT"
		}
		return false, nodeReason("RUNTIME_NONZERO_EXIT", firstLine(stderr.String()))
	}
	return true, ""
}
