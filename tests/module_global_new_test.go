package tests

import "testing"

// A named function is emitted with its own fresh scope, so it reads a top-level
// binding only when that binding is promoted to a module global (TDD-00093).
// These cover promotion of a `new`-expression binding — a class instance or a
// single-slot builtin handle (Blob/Date/URL/…) — read from a named or async
// function (ADR-00342). Scalars/strings/arrays/objects/Map already had this.

// A top-level const whose initializer is a computed constant scalar/string
// expression — arithmetic over number literals and numeric builtin constants
// (Math.PI/…), an earlier module global, or constant string concatenation — is
// promoted to a module global, so a named function can read it. Before ADR-00709
// only literal-initialized scalars promoted; `const X = 4 * Math.PI * Math.PI`
// stayed a main()-local and a function referencing it failed to compile with
// "undefined variable".
func TestE2EModuleGlobalComputedScalarConst(t *testing.T) {
	assertOutput(t, `
const SOLAR_MASS = 4 * Math.PI * Math.PI
const HALF = SOLAR_MASS / 2
const LABEL = "mass=" + "x"
function show(): void {
  console.log(LABEL + " " + SOLAR_MASS.toFixed(4) + " " + HALF.toFixed(4))
}
show()
`, "mass=x 39.4784 19.7392")
}

func TestE2EModuleGlobalClassInstance(t *testing.T) {
	assertOutput(t, `
class Counter {
  n = 0
  inc(): number { this.n = this.n + 1; return this.n }
}
const c = new Counter()
function bump(): number { return c.inc() }
console.log(bump())
console.log(bump())
`, "1\n2")
}

func TestE2EModuleGlobalBlobFromAsyncFn(t *testing.T) {
	// The exact reported repro: a top-level Blob read inside an async function.
	assertOutput(t, `
const b = new Blob(["Hello, ", "streamed ", "world"])
async function run(): Promise<void> {
  let bytes = 0
  for await (const chunk of b.stream()) { bytes += chunk.length }
  console.log(bytes)
}
run()
`, "21")
}

func TestE2EModuleGlobalDate(t *testing.T) {
	// Date is i64-backed (a single slot), promoted as a scalar global.
	assertOutput(t, `
const epoch = new Date(0)
function year(): number { return epoch.getFullYear() }
console.log(year())
`, "1970")
}

// The verified builtin value/data handles — read from a named function.
func TestE2EModuleGlobalBuiltinHandles(t *testing.T) {
	assertOutput(t, `
const err = new Error("boom")
const u = new URL("https://example.com/a/b?q=1")
const qs = new URLSearchParams("a=1&b=2")
const re = new RegExp("ab+c")
const rq = new Request("https://example.com/y")
const ab = new ArrayBuffer(8)
function report(): string {
  const g = qs.get("a")
  return err.message + " " + u.pathname + " " + (g ?? "?") + " " +
    (re.test("abbc") ? "y" : "n") + " " + rq.url + " " + ab.byteLength
}
console.log(report())
`, "boom /a/b 1 y https://example.com/y 8")
}

// The event handles (AbortController/Event/CustomEvent/EventTarget) promote too,
// once inferExprType knows their new-expression types — read from a named
// function, including AbortController.abort() mutating its signal.
func TestE2EModuleGlobalEventHandles(t *testing.T) {
	assertOutput(t, `
const ac = new AbortController()
const ev = new Event("click")
const ce = new CustomEvent("tick", { detail: 7 })
function report(): string {
  ac.abort()
  return (ac.signal.aborted ? "y" : "n") + " " + ev.type + " " + ce.detail
}
console.log(report())
`, "y click 7")
}

// Streams and EventEmitter use dedicated var-decl emitters; those now store into
// the module global when promoted (storePtrHandleVarDecl), so a top-level stream
// read from a named/async function keeps its chunks.
func TestE2EModuleGlobalReadableStream(t *testing.T) {
	assertOutput(t, `
const rs = new ReadableStream<number>({ start: (c) => { c.enqueue(1); c.enqueue(2); c.close() } })
async function total(): Promise<number> {
  let t = 0
  for await (const v of rs) t += v
  return t
}
async function run(): Promise<void> { console.log(await total()) }
run()
`, "3")
}

func TestE2EModuleGlobalEventEmitter(t *testing.T) {
	assertOutput(t, `
const em = new EventEmitter<string>()
let got = ""
em.on("data", (s: string) => { got = s })
function fire(): string { em.emit("data", "hello"); return got }
console.log(fire())
`, "hello")
}

// A local binding of the same name still shadows the promoted global (lookup
// checks scopes before moduleGlobals).
func TestE2EModuleGlobalLocalShadows(t *testing.T) {
	assertOutput(t, `
const d = new Date(0)
function f(): number { const d = 5; return d }
console.log(f())
`, "5")
}

// A generic class instance (`new Box<number>()`) promotes too: inferExprType
// computes its shape purely and registerModuleGlobals forces the monomorphized
// class's registration, so a named function's method call/field read resolves.
func TestE2EModuleGlobalGenericClassInstance(t *testing.T) {
	assertOutput(t, `
class Box<T> {
  v: T
  constructor(x: T) { this.v = x }
  get(): T { return this.v }
}
const b = new Box<number>(42)
function read(): number { return b.get() }
console.log(read())
`, "42")
}

func TestE2EModuleGlobalGenericMultiParam(t *testing.T) {
	assertOutput(t, `
class Pair<A, B> {
  a: A
  b: B
  constructor(x: A, y: B) { this.a = x; this.b = y }
}
const p = new Pair<string, number>("hi", 7)
function show(): string { return p.a + ":" + p.b }
console.log(show())
`, "hi:7")
}

// A TypedArray is a 2-slot value promoted via the two-global (data+len) array
// path — read and mutated by index from a named function.
func TestE2EModuleGlobalTypedArray(t *testing.T) {
	assertOutput(t, `
const buf = new Uint8Array([10, 20, 30])
function sum(): number {
  let s = 0
  for (let i = 0; i < buf.length; i++) s += buf[i]
  return s
}
buf[0] = 100
console.log(sum())
`, "150")
}
