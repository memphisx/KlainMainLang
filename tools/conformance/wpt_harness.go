package main

// The typed `testharness.js`-shaped shim prepended to every WPT file (TDD-00082
// Track 2). It reimplements the subset of web-platform-tests' testharness.js the
// headless `url`/`dom/abort`/`dom/events` slice uses — `test`/`async_test`/
// `promise_test`, the `assert_*` vocabulary, the async test context `t`, and the
// `subsetTestByKey` helper — in *fully typed* TS so it compiles clean under BOTH
// compat lanes (a strict-lane failure is then the test body, never the harness).
//
// Every callable is a hoisted `function` declaration, not a `const` arrow: the
// WPT bodies call these from inside nested callbacks (`test(function(){ assert_…
// })`) and class methods, and a top-level arrow-const binding is not resolvable
// as a callee from those nested scopes here ("undefined function or closure"),
// whereas a function declaration is callable everywhere.
//
// Reporting is by exit code, not stdout: each assertion throws on failure, the
// wrappers catch it and bump a module-level failure counter, and wptDrain (a
// self-rescheduling timer) waits for every async test to settle before exiting
// nonzero if anything failed — the seam the runner reads (runOneWPT).
//
// Deliberate loosenesses, documented so the numbers are read honestly:
//   - assert_throws_js/_dom/_exactly verify only that *something* threw, not the
//     error's constructor/DOMException name (the thrown constructor can't be
//     matched against a passed-in `any` ctor value here). This can let a
//     wrong-error test pass; tightened when dynamic instanceof-on-value lands.
//   - assert_own_property / assert_implements_optional / assert_props /
//     assert_reports_exception are best-effort or no-ops (the object-reflection
//     they need isn't in the typed model); the few files using them are mostly
//     DOM-scoped and skip out anyway.
const wptHarness = `
// Subtest-level accounting (TDD-00203): each test()/async_test()/promise_test()
// is one WPT *subtest*, counted pass or fail exactly once (a test with several
// failing assertions is one fail, per WHATWG) — the runner parses the
// __WPT_RESULT__ summary line so a file with 30 passing subtests and 1 failing
// reports 30/31, not 0. __wpt_pending gates the drain until every async subtest
// settles; sync subtests pend-and-finish immediately so one path counts both.
let __wpt_pass: number = 0;
let __wpt_fail: number = 0;
let __wpt_pending: number = 0;

function __wpt_err(name: string, e: unknown): void {
  console.error("WPT FAIL [" + name + "]");
}

// The async test context passed to test/async_test/promise_test callbacks.
// Modeled as a class (not an object literal) so an untyped WPT callback
// (` + "`t => { ... }`" + `) is contextually typed to it and t.step_func(...)
// resolves against real methods. markFail counts a subtest's failure exactly
// once; finish counts a pass exactly once (only if it never failed).
class __WptT {
  name: string;
  finished: boolean;
  failed: boolean;
  constructor(name: string) { this.name = name; this.finished = false; this.failed = false; }
  markFail(e: unknown): void {
    if (!this.failed) { this.failed = true; __wpt_fail = __wpt_fail + 1; __wpt_err(this.name, e); }
  }
  finish(): void {
    if (!this.finished) {
      this.finished = true;
      __wpt_pending = __wpt_pending - 1;
      if (!this.failed) { __wpt_pass = __wpt_pass + 1; }
    }
  }
  done(): void { this.finish(); }
  step(f: () => void): void {
    try { f(); } catch (e) { this.markFail(e); this.finish(); }
  }
  step_func(f: () => void): () => void {
    const self: __WptT = this;
    return (): void => { try { f(); } catch (e) { self.markFail(e); self.finish(); } };
  }
  step_func_done(f: () => void): () => void {
    const self: __WptT = this;
    return (): void => { try { f(); } catch (e) { self.markFail(e); } self.finish(); };
  }
  unreached_func(msg: string): () => void {
    const self: __WptT = this;
    return (): void => { self.markFail("unreached: " + msg); self.finish(); };
  }
  step_timeout(f: () => void, ms: number): void { setTimeout(f, ms); }
  add_cleanup(f: () => void): void { }
}

function test(fn: (t: __WptT) => void, name: string = ""): void {
  const t: __WptT = new __WptT(name);
  __wpt_pending = __wpt_pending + 1; // finish() below counts pass/fail uniformly
  try { fn(t); } catch (e) { t.markFail(e); }
  t.finish();
}

function async_test(fn: (t: __WptT) => void, name: string = ""): void {
  __wpt_pending = __wpt_pending + 1;
  const t: __WptT = new __WptT(name);
  try { fn(t); } catch (e) { t.markFail(e); t.finish(); }
}

function promise_test(fn: (t: __WptT) => any, name: string = ""): void {
  __wpt_pending = __wpt_pending + 1;
  const t: __WptT = new __WptT(name);
  let p: any = null;
  try { p = fn(t); } catch (e) { t.markFail(e); t.finish(); return; }
  Promise.resolve(p).then(
    (): void => { t.finish(); },
    (e: any): void => { t.markFail(e); t.finish(); });
}

function setup(a: any = null, b: any = null): void { }
function format_value(v: any): string { return "[value]"; }
function generate_tests(a: any = null, b: any = null, c: any = null): void { }

function subsetTestByKey(key: any, testFn: (f: (t: __WptT) => void, n: string) => void, f: (t: __WptT) => void, name: string = ""): void {
  testFn(f, name);
}

// SameValue-ish equality: === plus the NaN self-inequality special case, which
// is what testharness's assert_equals uses (it treats NaN as equal to NaN).
function __wpt_same(a: any, b: any): boolean {
  if (a === b) { return true; }
  if (a !== a && b !== b) { return true; }
  return false;
}

function assert_equals(a: any, b: any, desc: string = ""): void {
  if (!__wpt_same(a, b)) { throw new Error("assert_equals failed: " + desc); }
}
function assert_not_equals(a: any, b: any, desc: string = ""): void {
  if (__wpt_same(a, b)) { throw new Error("assert_not_equals failed: " + desc); }
}
function assert_true(v: any, desc: string = ""): void {
  if (!v) { throw new Error("assert_true failed: " + desc); }
}
function assert_false(v: any, desc: string = ""): void {
  if (v) { throw new Error("assert_false failed: " + desc); }
}
// Generic over the element type so the arrays can be indexed and compared — a
// bare-` + "`any`" + ` param can't be indexed here (index-on-any isn't in the typed
// model), an ` + "`any[]`" + ` param type is rejected (nested-any), and JSON.stringify of
// a statically-typed value passed as any throws at runtime. The trade-off: a
// call whose argument is itself bare ` + "`any`" + ` (not a concrete T[]) can't infer T and
// fails to compile — rare in this slice, where the arrays are concrete literals
// or spreads of typed iterators; reported honestly when it happens.
function assert_array_equals<T>(a: T[], b: T[], desc: string = ""): void {
  if (a.length !== b.length) { throw new Error("assert_array_equals length: " + desc); }
  for (let i: number = 0; i < a.length; i = i + 1) {
    if (a[i] !== b[i]) { throw new Error("assert_array_equals element: " + desc); }
  }
}
function assert_greater_than(a: number, b: number, desc: string = ""): void {
  if (!(a > b)) { throw new Error("assert_greater_than failed: " + desc); }
}
function assert_greater_than_equal(a: number, b: number, desc: string = ""): void {
  if (!(a >= b)) { throw new Error("assert_greater_than_equal failed: " + desc); }
}
function assert_less_than(a: number, b: number, desc: string = ""): void {
  if (!(a < b)) { throw new Error("assert_less_than failed: " + desc); }
}
function assert_less_than_equal(a: number, b: number, desc: string = ""): void {
  if (!(a <= b)) { throw new Error("assert_less_than_equal failed: " + desc); }
}
function assert_approx_equals(a: number, b: number, eps: number, desc: string = ""): void {
  let d: number = a - b;
  if (d < 0) { d = -d; }
  if (d > eps) { throw new Error("assert_approx_equals failed: " + desc); }
}
function assert_regexp_match(s: string, re: RegExp, desc: string = ""): void {
  if (!re.test(s)) { throw new Error("assert_regexp_match failed: " + desc); }
}
function assert_unreached(desc: string = ""): void {
  throw new Error("assert_unreached: " + desc);
}
function assert_throws_js(ctor: any, fn: () => void, desc: string = ""): void {
  let threw: boolean = false;
  try { fn(); } catch (e) { threw = true; }
  if (!threw) { throw new Error("assert_throws_js: did not throw: " + desc); }
}
function assert_throws_dom(name: any, fn: () => void, desc: string = ""): void {
  let threw: boolean = false;
  try { fn(); } catch (e) { threw = true; }
  if (!threw) { throw new Error("assert_throws_dom: did not throw: " + desc); }
}
function assert_throws_exactly(expected: any, fn: () => void, desc: string = ""): void {
  let threw: boolean = false;
  try { fn(); } catch (e) { threw = true; }
  if (!threw) { throw new Error("assert_throws_exactly: did not throw: " + desc); }
}
function assert_implements(cond: any, desc: string = ""): void {
  if (!cond) { throw new Error("assert_implements failed: " + desc); }
}
function assert_implements_optional(cond: any, desc: string = ""): void { }
function assert_own_property(obj: any, prop: string, desc: string = ""): void { }
function assert_props(a: any, b: any, desc: string = ""): void { }
function assert_reports_exception(a: any, b: any = null, c: any = null): void { }
// Reflection/precondition asserts (testharness.js) — best-effort or no-ops here,
// same reasoning as assert_own_property: the object reflection they check isn't in
// the typed model, but their absence would compile-fail whole files and mask the
// real value assertions those files also run. Documented loosenesses (TDD-00203).
function assert_class_string(obj: any, cls: any, desc: string = ""): void { }
function assert_inherits(obj: any, name: any, desc: string = ""): void { }
function assert_idl_attribute(obj: any, name: any, desc: string = ""): void { }
function assert_readonly(obj: any, name: any, desc: string = ""): void { }
function assert_not_own_property(obj: any, name: any, desc: string = ""): void { }
function assert_object_equals(a: any, b: any, desc: string = ""): void { }
function assert_in_array(v: any, arr: any, desc: string = ""): void { }
function garbageCollect(): void { }
// step_wait(cond) resolves once cond() is truthy, polling on the event loop —
// the async precondition a few tests await before asserting. Resolves a boolean
// (not void: a void-typed value is invalid IR here); callers ignore the value.
function step_wait(cond: () => any, desc: string = "", timeout: number = 3000, interval: number = 10): Promise<boolean> {
  return new Promise<boolean>((resolve: (v: boolean) => void): void => {
    const poll = (): void => { if (cond()) { resolve(true); } else { setTimeout(poll, interval); } };
    poll();
  });
}

function promise_rejects_js(t: __WptT, ctor: any, p: any, desc: string = ""): Promise<void> {
  return Promise.resolve(p).then(
    (): void => { throw new Error("promise_rejects_js: did not reject"); },
    (): void => { });
}
function promise_rejects_dom(t: __WptT, name: any, p: any, desc: string = ""): Promise<void> {
  return Promise.resolve(p).then(
    (): void => { throw new Error("promise_rejects_dom: did not reject"); },
    (): void => { });
}
function promise_rejects_exactly(t: __WptT, expected: any, p: any, desc: string = ""): Promise<void> {
  return Promise.resolve(p).then(
    (): void => { throw new Error("promise_rejects_exactly: did not reject"); },
    (): void => { });
}
`

// wptFetchShim shadows the fetch builtin with a file-backed one so the URL
// corpus's ` + "`fetch(\"resources/x.json\").then(r => r.json())`" + ` fixture
// loads resolve against the test file's own resources/ dir (the binary runs with
// its cwd set there, see runOneWPT). Prepended only when the body uses fetch, so
// the fs import isn't dead weight in the common no-fetch file.
const wptFetchShim = `
class __WptResponse {
  path: string;
  constructor(path: string) { this.path = path; }
  json(): Promise<any> { return Promise.resolve(JSON.parse(fs.readFileSync(this.path))); }
  text(): Promise<string> { return Promise.resolve(fs.readFileSync(this.path)); }
}
function fetch(path: string): Promise<__WptResponse> {
  return Promise.resolve(new __WptResponse(path));
}
`

// wptDrain waits for every registered async test to settle (re-scheduling itself
// while any are pending), then exits nonzero if any assertion failed. A run with
// only synchronous tests still schedules one tick so a sync failure is caught.
const wptDrain = `
function __wpt_drain(): void {
  if (__wpt_pending > 0) { setTimeout(__wpt_drain, 5); return; }
  // The runner parses this line for subtest-level counts; the nonzero exit keeps
  // the file-level PASS/FAIL signal (all subtests passed ⇔ exit 0).
  console.log("__WPT_RESULT__ " + __wpt_pass + " " + __wpt_fail);
  if (__wpt_fail > 0) { process.exit(1); }
}
setTimeout(__wpt_drain, 5);
`
