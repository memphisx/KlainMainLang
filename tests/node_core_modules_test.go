package tests

import (
	"testing"
)

// --- querystring (see docs/adr/ADR-00139.md) ---

func TestE2EQuerystringParse(t *testing.T) {
	assertOutputImports(t, `
import querystring from 'querystring'
const m = querystring.parse("a=1&b=hello%20world")
console.log(m.get("a"))
console.log(m.get("b"))
`, "1\nhello world")
}

func TestE2EQuerystringParseBareFlagIsEmptyString(t *testing.T) {
	assertOutputImports(t, `
import querystring from 'querystring'
const m = querystring.parse("debug&x=1")
console.log(m.get("debug"))
console.log(m.get("x"))
`, "\n1")
}

func TestE2EQuerystringParseDoesNotStripLeadingQuestionMark(t *testing.T) {
	// Unlike `new URLSearchParams(str)`, querystring.parse treats a leading
	// '?' as plain text at the start of the first key — matching real Node.
	assertOutputImports(t, `
import querystring from 'querystring'
const m = querystring.parse("?a=1")
console.log(m.get("?a"))
console.log(m.get("a"))
`, "1\nnull")
}

func TestE2EQuerystringStringify(t *testing.T) {
	assertOutputImports(t, `
import querystring from 'querystring'
const m = new Map<string, string>()
m.set("q", "hello world")
m.set("page", "2")
console.log(querystring.stringify(m))
`, "q=hello%20world&page=2")
}

func TestE2EQuerystringRoundTrip(t *testing.T) {
	assertOutputImports(t, `
import querystring from 'querystring'
const parsed = querystring.parse("a=1&b=2")
console.log(querystring.stringify(parsed))
`, "a=1&b=2")
}

// --- assert (see docs/adr/ADR-00140.md) ---

func TestE2EAssertOkPasses(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
assert.ok(1 === 1)
assert(true)
console.log("reached")
`, "reached")
}

func TestE2EAssertOkThrowsWithDefaultMessage(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
try {
  assert.ok(1 === 2)
} catch (e) {
  console.log(e.name)
  console.log(e.message)
}
`, "AssertionError\nthe expression evaluated to a falsy value")
}

func TestE2EAssertOkThrowsWithCustomMessage(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
try {
  assert(false, "custom failure")
} catch (e) {
  console.log(e.message)
}
`, "custom failure")
}

func TestE2EAssertEqualPasses(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
assert.equal(1, 1)
assert.strictEqual("a", "a")
console.log("ok")
`, "ok")
}

func TestE2EAssertEqualThrows(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
try {
  assert.equal(1, 2)
} catch (e) {
  console.log(e.message)
}
`, "values are not equal")
}

func TestE2EAssertNotEqualPassesAndThrows(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
assert.notEqual(1, 2)
try {
  assert.notStrictEqual(5, 5)
} catch (e) {
  console.log(e.message)
}
`, "values are equal")
}

func TestE2EAssertFail(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
try {
  assert.fail("boom")
} catch (e) {
  console.log(e.name + ": " + e.message)
}
try {
  assert.fail()
} catch (e) {
  console.log(e.message)
}
`, "AssertionError: boom\nfailed")
}

func TestE2EAssertThrowsPassesWhenFunctionThrows(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
assert.throws(() => { throw new Error("boom") })
console.log("ok")
`, "ok")
}

func TestE2EAssertThrowsFailsWhenFunctionDoesNotThrow(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
try {
  assert.throws(() => { const x = 1 })
} catch (e) {
  console.log(e.message)
}
`, "missing expected exception")
}

func TestE2EAssertThrowsCustomMessageOnMissingException(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
try {
  assert.throws(() => { const x = 1 }, "expected a throw")
} catch (e) {
  console.log(e.message)
}
`, "expected a throw")
}

// TDD-00131: assert.deepStrictEqual — recursive structural equality over
// arrays, objects (incl. nested), strings, and scalars.
func TestE2EAssertDeepStrictEqual(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
assert.deepStrictEqual([1, 2, 3], [1, 2, 3]);
assert.deepStrictEqual({ a: "hi", b: [1, 2] }, { a: "hi", b: [1, 2] });
try { assert.deepStrictEqual([1, 2], [1, 9]); console.log("no throw"); }
catch (e) { console.log("caught"); }
assert.notDeepStrictEqual({ x: 1 }, { x: 2 });
console.log("done");
`, "caught\ndone")
}

func TestE2EAssertMatch(t *testing.T) {
	// assert.match/doesNotMatch (ADR-00428): regexp.test under the hood,
	// AssertionError on mismatch.
	assertOutputImports(t, `
import assert from 'assert'
assert.match("thessaloniki", /salon/)
assert.doesNotMatch("abc", /xyz/)
try {
  assert.match("abc", /nope/)
} catch (e) {
  console.log("caught: " + e.message)
}
console.log("ok")
`, "caught: the input did not match the regular expression\nok")
}

// assert.ifError / assert.doesNotThrow (ADR-00499).
func TestE2EAssertIfErrorAndDoesNotThrow(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
assert.ifError(null)
assert.ifError(0)
assert.doesNotThrow(() => { console.log("ran clean") })
try { assert.ifError("boom") } catch (e) { console.log("caught:", e.message) }
try { assert.doesNotThrow(() => { throw new Error("x") }) } catch (e) { console.log("caught:", e.message) }
console.log("done")
`, "ran clean\ncaught: ifError got unwanted exception\ncaught: got unwanted exception\ndone")
}

// --- TDD-00165: importable specifiers for the Web-global-backed modules
// (url/timers/perf_hooks/buffer/events). A same-name import is validated and
// erased — the ambient global keeps working. See ADR-00666. ---

func TestE2EImportURLFromUrlModule(t *testing.T) {
	assertOutputImports(t, `
import { URL, URLSearchParams } from 'url'
const u = new URL("https://example.com/path?x=1")
console.log(u.pathname)
const p = new URLSearchParams("a=1&b=2")
console.log(p.get("b"))
`, "/path\n2")
}

func TestE2EImportSetTimeoutFromNodeTimers(t *testing.T) {
	assertOutputImports(t, `
import { setTimeout } from 'node:timers'
setTimeout(() => console.log("fired"), 0)
`, "fired")
}

func TestE2EImportBufferAndPerformance(t *testing.T) {
	assertOutputImports(t, `
import { Buffer } from 'node:buffer'
import { performance } from 'perf_hooks'
console.log(Buffer.from("hi").length)
console.log(typeof performance.now())
`, "2\nnumber")
}

func TestE2EImportEventEmitterDefaultAndNamed(t *testing.T) {
	assertOutputImports(t, `
import EventEmitter from 'events'
const e = new EventEmitter<{ go: [number] }>()
e.on('go', (n) => console.log("go", n))
e.emit('go', 42)
`, "go 42")
}

func TestE2EReexportGlobalStillWorksWithoutImport(t *testing.T) {
	// The whole premise: not importing must keep the global working (no regression).
	assertOutputImports(t, `
const u = new URL("https://x.com/p")
console.log(u.pathname)
console.log(Buffer.from("ab").length)
`, "/p\n2")
}

func TestE2EReexportNamespaceImportRejected(t *testing.T) {
	if _, err := parseAndCompileImports(t, `import * as t from 'timers'`); err == nil {
		t.Fatal("expected a compile error for a namespace reexport import, got none")
	}
}

func TestE2EReexportModuleOnlyExtraRejected(t *testing.T) {
	// A module-only extra that is not yet built (legacy url.format, or
	// perf_hooks.PerformanceObserver) keeps the standard "no exported member"
	// rejection. (the legacy url.* functions are all implemented — Stage 4.)
	for _, src := range []string{
		`import { transcode } from 'buffer'`,
		`import { monitorEventLoopDelay } from 'perf_hooks'`,
	} {
		if _, err := parseAndCompileImports(t, src); err == nil {
			t.Fatalf("expected a compile error for an unbuilt module-only extra: %s", src)
		}
	}
}

// --- TDD-00165 Stage 2 (ADR-00667): aliased imports of the codegen-recognized
// reexports (setTimeout family / performance / atob·btoa) rename the alias to
// the canonical global; parse-time constructors (URL/Buffer/EventEmitter) stay a
// clean Stage 3 rejection. ---

func TestE2EReexportAliasedTimer(t *testing.T) {
	assertOutputImports(t, `
import { setTimeout as later } from 'timers'
later(() => console.log("later fired"), 0)
`, "later fired")
}

func TestE2EReexportAliasedPerformance(t *testing.T) {
	assertOutputImports(t, `
import { performance as perf } from 'node:perf_hooks'
console.log(typeof perf.now())
`, "number")
}

func TestE2EReexportAliasedAtobBtoa(t *testing.T) {
	assertOutputImports(t, `
import { btoa as enc, atob as dec } from 'buffer'
console.log(enc("hi"))
console.log(dec(enc("hi")))
`, "aGk=\nhi")
}

func TestE2EReexportLocalShadowWinsOverAlias(t *testing.T) {
	// A local binding of the alias name still shadows the reexport (the rename is
	// guarded by the scope stack).
	assertOutputImports(t, `
import { performance as perf } from 'perf_hooks'
function f(): string {
  const perf = "local"
  return perf
}
console.log(f())
`, "local")
}

// --- TDD-00165 Stage 3 (ADR-00668): aliased imports of the parse-time
// constructors (URL/URLSearchParams/Blob/EventEmitter). The parser produced a
// generic `new <alias>(...)`; the rename pass rebuilds it as the specialized
// built-in node under the canonical name. Buffer aliasing rides Stage 2 (it is a
// `Buffer.from(...)` member call, not a parse-time constructor). ---

func TestE2EReexportAliasedURLAndSearchParams(t *testing.T) {
	assertOutputImports(t, `
import { URL as U, URLSearchParams as USP } from 'url'
const u = new U("https://example.com/p?x=1")
console.log(u.pathname)
const q = new USP("a=1&b=2")
console.log(q.get("b"))
`, "/p\n2")
}

func TestE2EReexportAliasedEventEmitter(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter as EE } from 'events'
const e = new EE<{ go: [number] }>()
e.on('go', (n) => console.log("go", n))
e.emit('go', 5)
`, "go 5")
}

func TestE2EReexportAliasedBlob(t *testing.T) {
	assertOutputImports(t, `
import { Blob as B } from 'buffer'
const b = new B(["hello"])
console.log(b.size)
`, "5")
}

func TestE2EReexportAliasedBufferViaMemberCall(t *testing.T) {
	// Buffer is a member-call built-in (`Buffer.from`), so its alias renames like
	// any Stage 2 codegen-recognized reexport — no parse-time rewrite needed.
	assertOutputImports(t, `
import { Buffer as B } from 'buffer'
console.log(B.from("hi").length)
`, "2")
}

func TestE2EReexportAliasedParseTimeCtorArgcountRejected(t *testing.T) {
	// The rebuilt constructor still enforces the built-in's arg contract.
	if _, err := parseAndCompileImports(t, `import { EventEmitter as EE } from 'events'
const e = new EE(1)`); err == nil {
		t.Fatal("expected a compile error for new EventEmitter(arg) via alias, got none")
	}
}

func TestE2EReexportAliasedParseTimeCtorLocalShadowWins(t *testing.T) {
	assertOutputImports(t, `
import { URL as U } from 'url'
function f(): number { const U = 7; return U }
console.log(f())
`, "7")
}

// --- TDD-00165 Stage 4 (ADR-00669): legacy url.parse, a module-only function of
// the (now hybrid) url module — importable named, via namespace, and via default,
// returning the legacy Url object. ---

func TestE2EUrlParseNamedImport(t *testing.T) {
	assertOutputImports(t, `
import { parse } from 'url'
const u = parse("https://user:pw@example.com:8080/a/b?x=1&y=2#frag")
console.log(u.protocol)
console.log(u.host)
console.log(u.hostname)
console.log(u.port)
console.log(u.pathname)
console.log(u.search)
console.log(u.query)
console.log(u.hash)
console.log(u.auth)
console.log(u.path)
`, "https:\nexample.com:8080\nexample.com\n8080\n/a/b\n?x=1&y=2\nx=1&y=2\n#frag\nuser:pw\n/a/b?x=1&y=2")
}

func TestE2EUrlParseNamespaceImport(t *testing.T) {
	assertOutputImports(t, `
import * as url from 'url'
const u = url.parse("http://host/p?q=1")
console.log(u.pathname, u.query)
`, "/p q=1")
}

func TestE2EUrlParseDefaultImport(t *testing.T) {
	assertOutputImports(t, `
import url from 'node:url'
console.log(url.parse("https://a.com/x").hostname)
`, "a.com")
}

func TestE2EUrlNamedURLAndParseTogether(t *testing.T) {
	// The hybrid module: the reexport primary URL and the module-only parse
	// coexist in one named import.
	assertOutputImports(t, `
import { URL, parse } from 'url'
const a = new URL("https://x.com/a")
console.log(a.pathname)
console.log(parse("https://y.com/b?z=9").query)
`, "/a\nz=9")
}

// --- TDD-00165 Stage 4 (ADR-00670): legacy url.format, the inverse of url.parse. ---

func TestE2EUrlFormatRoundTrip(t *testing.T) {
	assertOutputImports(t, `
import { parse, format } from 'url'
const s = "https://user:pw@example.com:8080/a/b?x=1&y=2#frag"
console.log(format(parse(s)))
console.log(format(parse(s)) === s)
`, "https://user:pw@example.com:8080/a/b?x=1&y=2#frag\ntrue")
}

func TestE2EUrlFormatWhatwgURLReturnsHref(t *testing.T) {
	assertOutputImports(t, `
import { format } from 'url'
console.log(format(new URL("https://a.com/x?q=1")))
`, "https://a.com/x?q=1")
}

func TestE2EUrlFormatPlainObject(t *testing.T) {
	assertOutputImports(t, `
import { format } from 'url'
const obj = { protocol: "https:", host: "example.com", pathname: "/p", search: "?a=1", hash: "#h", auth: "", query: "" }
console.log(format(obj))
`, "https://example.com/p?a=1#h")
}

func TestE2EUrlFormatQueryWithoutSearch(t *testing.T) {
	// When only `query` is set (no `search`), format re-adds the "?".
	assertOutputImports(t, `
import * as url from 'url'
console.log(url.format(url.parse("http://h/p?a=1")))
`, "http://h/p?a=1")
}

// --- TDD-00165 Stage 4 (ADR-00671): the file-URL pair fileURLToPath /
// pathToFileURL (POSIX). ---

func TestE2EFileURLToPath(t *testing.T) {
	assertOutputImports(t, `
import { fileURLToPath } from 'url'
console.log(fileURLToPath("file:///foo/bar"))
console.log(fileURLToPath("file:///foo%20bar/baz.txt"))
`, "/foo/bar\n/foo bar/baz.txt")
}

func TestE2EFileURLToPathNonFileRejected(t *testing.T) {
	src := `import { fileURLToPath } from 'url'
try { fileURLToPath("https://x.com/p") } catch (e) { console.log("threw:", e.message) }`
	assertOutputImports(t, src, "threw: The URL must be of scheme file")
}

func TestE2EPathToFileURL(t *testing.T) {
	assertOutputImports(t, `
import { pathToFileURL } from 'url'
const u = pathToFileURL("/foo/bar")
console.log(u.protocol)
console.log(u.href)
`, "file:\nfile:///foo/bar")
}

func TestE2EPathToFileURLEncodesSpecials(t *testing.T) {
	assertOutputImports(t, `
import { pathToFileURL } from 'url'
console.log(pathToFileURL("/foo bar/baz#1").href)
`, "file:///foo%20bar/baz%231")
}

func TestE2EFileURLRoundTrip(t *testing.T) {
	assertOutputImports(t, `
import { fileURLToPath, pathToFileURL } from 'url'
console.log(fileURLToPath(pathToFileURL("/a/b c/d#e")))
`, "/a/b c/d#e")
}

// --- TDD-00165 Stage 4 (ADR-00672): the remaining legacy url helpers —
// resolve / urlToHttpOptions / domainToASCII / domainToUnicode. ---

func TestE2EUrlResolve(t *testing.T) {
	assertOutputImports(t, `
import { resolve } from 'url'
console.log(resolve("http://example.com/one/two/three", "four"))
console.log(resolve("http://example.com/a/b", "/c"))
console.log(resolve("https://a.com/x/", "../y"))
`, "http://example.com/one/two/four\nhttp://example.com/c\nhttps://a.com/y")
}

func TestE2EUrlToHttpOptions(t *testing.T) {
	assertOutputImports(t, `
import { urlToHttpOptions } from 'url'
const o = urlToHttpOptions(new URL("https://user:pw@host.com:8443/p/q?a=1#h"))
console.log(o.protocol, o.hostname, o.port, o.path, o.auth, o.hash)
`, "https: host.com 8443 /p/q?a=1 user:pw #h")
}

func TestE2EUrlDomainToASCIIPassThrough(t *testing.T) {
	// An already-ASCII domain passes through unchanged on every platform; an
	// invalid domain yields "" (Node's failure contract). IDN *conversion* of a
	// non-ASCII domain is intentionally not asserted here — it depends on the
	// libcurl build's IDN backend (present on typical Linux, absent on this Mac).
	assertOutputImports(t, `
import { domainToASCII } from 'url'
console.log("[" + domainToASCII("example.com") + "]")
console.log("[" + domainToASCII("") + "]")
`, "[example.com]\n[]")
}

// --- TDD-00166 (ADR-00673): perf_hooks PerformanceObserver. V1 dispatch is
// synchronous (during mark/measure), so the observer output precedes later
// straight-line output. ---

func TestE2EPerformanceObserverMeasure(t *testing.T) {
	assertOutputImports(t, `
import { PerformanceObserver } from 'perf_hooks'
const obs = new PerformanceObserver((list) => {
  for (const e of list.getEntries()) { console.log(e.entryType, e.name, e.duration >= 0) }
})
obs.observe({ entryTypes: ['measure'] })
performance.mark('A')
performance.mark('B')
performance.measure('A-to-B', 'A', 'B')
console.log("done")
`, "measure A-to-B true\ndone")
}

func TestE2EPerformanceObserverMarkAndDisconnect(t *testing.T) {
	assertOutputImports(t, `
import { PerformanceObserver } from 'perf_hooks'
let count = 0
const obs = new PerformanceObserver((list) => {
  for (const e of list.getEntries()) { count = count + 1; console.log("saw", e.name) }
})
obs.observe({ entryTypes: ['mark'] })
performance.mark('m1')
performance.mark('m2')
obs.disconnect()
performance.mark('m3')
performance.measure('x', 'm1', 'm2')
console.log("count", count)
`, "saw m1\nsaw m2\ncount 2")
}

func TestE2EPerformanceObserverAliasedImport(t *testing.T) {
	assertOutputImports(t, `
import { PerformanceObserver as PO } from 'perf_hooks'
const o = new PO((l) => { console.log("n", l.getEntries().length) })
o.observe({ entryTypes: ['mark', 'measure'] })
performance.mark('a')
`, "n 1")
}

func TestE2EPerformanceObserverBadEntryTypeRejected(t *testing.T) {
	if _, err := parseAndCompileImports(t, `import { PerformanceObserver } from 'perf_hooks'
const o = new PerformanceObserver((l) => {})
o.observe({ entryTypes: ['resource'] })`); err == nil {
		t.Fatal("expected a compile error for an unsupported entryType, got none")
	}
}

// --- TDD-00167 (ADR-00675): events.once — a Promise that resolves with the
// event's args the first time it fires (unblocked by the Promise<T[]> fix,
// ADR-00674). ---

func TestE2EEventsOnceNumber(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter, once } from 'events'
async function main2(): Promise<void> {
  const ee = new EventEmitter<{ ready: [number] }>()
  setTimeout(() => { ee.emit('ready', 42) }, 0)
  const arr = await once(ee, 'ready')
  console.log("got", arr[0], "len", arr.length)
}
main2()
`, "got 42 len 1")
}

func TestE2EEventsOnceString(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter, once } from 'events'
async function main2(): Promise<void> {
  const ee = new EventEmitter<{ msg: [string] }>()
  setTimeout(() => { ee.emit('msg', 'hello') }, 0)
  const arr = await once(ee, 'msg')
  console.log("got", arr[0])
}
main2()
`, "got hello")
}

// --- TDD-00167 (ADR-00677): events.on — an async iterator that yields the
// event's args array on each emission, buffering between iterations and parking
// the consumer when the queue drains (built on the array-yielding generators of
// ADR-00676). ---

func TestE2EEventsOnBuffersAndParks(t *testing.T) {
	// Events emitted from a timer (after the loop parks) resume the consumer.
	assertOutputImports(t, `
import { EventEmitter, on } from 'events'
async function main2(): Promise<void> {
  const ee = new EventEmitter<{ tick: [number] }>()
  setTimeout(() => { ee.emit('tick', 1); ee.emit('tick', 2); ee.emit('tick', 3) }, 0)
  let count = 0
  for await (const [n] of on(ee, 'tick')) {
    console.log(n)
    count++
    if (count >= 3) break
  }
  console.log("done")
}
main2()
`, "1\n2\n3\ndone")
}

func TestE2EEventsOnEagerCaptureBuffers(t *testing.T) {
	// on(...) attaches the listener eagerly at the call, so events emitted
	// before consumption starts are buffered, not lost.
	assertOutputImports(t, `
import { EventEmitter, on } from 'events'
async function main2(): Promise<void> {
  const ee = new EventEmitter<{ msg: [string] }>()
  const iter = on(ee, 'msg')
  ee.emit('msg', 'a')
  ee.emit('msg', 'b')
  let count = 0
  for await (const [s] of iter) {
    console.log(s)
    count++
    if (count >= 2) break
  }
  console.log("done")
}
main2()
`, "a\nb\ndone")
}

// --- TDD-00168 (ADR-00678): async_hooks AsyncLocalStorage — context that
// propagates across `await`. The store lives in the coroutine task struct, so
// a value set with run(...) survives an await/park/resume. ---

func TestE2EAsyncLocalStorageSyncNesting(t *testing.T) {
	assertOutputImports(t, `
import { AsyncLocalStorage } from 'async_hooks'
interface Ctx { name: string }
const als = new AsyncLocalStorage<Ctx>()
console.log(als.getStore() === null ? 'empty' : '?')
als.run({ name: 'outer' }, () => {
  console.log(als.getStore()?.name)
  als.run({ name: 'inner' }, () => { console.log(als.getStore()?.name) })
  console.log(als.getStore()?.name)
  als.exit(() => { console.log(als.getStore() === null ? 'exited' : '?') })
  console.log(als.getStore()?.name)
})
console.log(als.getStore() === null ? 'empty2' : '?')
`, "empty\nouter\ninner\nouter\nexited\nouter\nempty2")
}

func TestE2EAsyncLocalStorageAcrossAwait(t *testing.T) {
	assertOutputImports(t, `
import { AsyncLocalStorage } from 'async_hooks'
interface Ctx { id: number }
const als = new AsyncLocalStorage<Ctx>()
async function worker(): Promise<void> {
  console.log('before', als.getStore()?.id)
  await new Promise<number>((res) => { setTimeout(() => res(1), 5) })
  console.log('after', als.getStore()?.id)
}
async function main2(): Promise<void> {
  await als.run({ id: 42 }, async () => {
    await worker()
    console.log('inRun', als.getStore()?.id)
  })
  console.log(als.getStore() === null ? 'empty' : '?')
}
main2()
`, "before 42\nafter 42\ninRun 42\nempty")
}

func TestE2EAsyncLocalStorageDeepStack(t *testing.T) {
	// Regression guard mirroring Node's own test-async-local-storage-deep-stack:
	// 1000 nested synchronous run() frames must not overflow or lose the store.
	assertOutputImports(t, `
import { AsyncLocalStorage } from 'async_hooks'
const als = new AsyncLocalStorage<number>()
function run(count: number): void {
  if (count === 0) { console.log('done', als.getStore()); return }
  als.run(count, () => { run(count - 1) })
}
run(1000)
`, "done 1")
}

func TestE2EAsyncLocalStorageDisableAndEnterWith(t *testing.T) {
	// An object store so an absent/disabled getStore() reads as null (a scalar
	// store's undefined is NaN/0 — this compiler's zero-value undefined stand-in).
	assertOutputImports(t, `
import { AsyncLocalStorage } from 'async_hooks'
interface Ctx { v: number }
const als = new AsyncLocalStorage<Ctx>()
als.enterWith({ v: 9 })
console.log(als.getStore()?.v)
als.disable()
console.log(als.getStore() === null ? 'off' : '?')
`, "9\noff")
}

func TestE2EAsyncLocalStorageTimerPropagation(t *testing.T) {
	// TDD-00168 Stage 3 (ADR-00679): a setTimeout scheduled inside run(...) sees
	// the store when it fires — the callback is wrapped to carry the captured
	// context across the schedule->fire boundary.
	assertOutputImports(t, `
import { AsyncLocalStorage } from 'async_hooks'
interface Ctx { v: number }
const als = new AsyncLocalStorage<Ctx>()
async function main2(): Promise<void> {
  als.run({ v: 7 }, () => {
    setTimeout(() => { console.log('timer sees', als.getStore()?.v) }, 5)
  })
  await new Promise<number>((res) => { setTimeout(() => res(0), 20) })
  console.log('outside', als.getStore() === null ? 'empty' : '?')
}
main2()
`, "timer sees 7\noutside empty")
}

// --- TDD-00168 Stage 4 (ADR-00680): static AsyncLocalStorage.bind + AsyncResource ---

func TestE2EAsyncLocalStorageBind(t *testing.T) {
	// bind(fn) captures the context now and returns a wrapper of fn's own
	// signature that reinstalls it on every later call — outside any run.
	assertOutputImports(t, `
import { AsyncLocalStorage } from 'async_hooks'
const als = new AsyncLocalStorage<number>()
let boundThunk: () => number = () => 0
let boundArg: (x: number) => number = (x: number) => x
als.run(7, () => {
  boundThunk = AsyncLocalStorage.bind((): number => als.getStore())
  boundArg = AsyncLocalStorage.bind((x: number): number => als.getStore() + x)
})
console.log(boundThunk())
console.log(boundArg(100))
`, "7\n107")
}

func TestE2EAsyncResourceRunInAsyncScope(t *testing.T) {
	// new AsyncResource() captures the context at construction; runInAsyncScope
	// replays it — here after the run(...) has already exited.
	assertOutputImports(t, `
import { AsyncLocalStorage, AsyncResource } from 'async_hooks'
const als = new AsyncLocalStorage<number>()
const res = als.run(9, () => new AsyncResource('r'))
res.runInAsyncScope((): void => { console.log('scope', als.getStore()) })
console.log('outside', als.getStore())
`, "scope 9\noutside NaN")
}

func TestE2EAsyncLocalStorageSnapshotRejected(t *testing.T) {
	// snapshot() returns a fully generic runner — needs generic first-class
	// closures this compiler lacks; a clean rejection, not the ALS feature.
	_, err := parseAndCompile(`
import { AsyncLocalStorage } from 'async_hooks'
const als = new AsyncLocalStorage<number>()
const run = AsyncLocalStorage.snapshot()
`)
	if err == nil {
		t.Fatal("expected AsyncLocalStorage.snapshot() to be a clean rejection")
	}
}
