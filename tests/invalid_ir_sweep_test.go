package tests

import "testing"

// Invalid-IR sweep fixes (ADR-00895..00897): three distinct codegen sites that
// previously emitted invalid LLVM IR (clang-stage `defined with type X but
// expected Y` errors) on Test262 inputs, each now producing well-typed IR with
// the faithful observable behavior.

// A nullable-scalar (`T | undefined`) value — e.g. the result of an optional
// chain `obj?.a` — used directly as a boolean condition. Its truthiness is
// `present && ToBoolean(payload)`; an absent value is `undefined`, hence falsy.
// Previously toBool returned the raw { i1, T } aggregate where an i1 was
// required (the `IR == "i1"` fast path fired for a nullable bool). ADR-00895.
func TestE2EOptionalChainTruthyInLoopCondition(t *testing.T) {
	assertOutput(t, `
const obj: { a: boolean } | null = { a: true };
let count = 0;
while (obj?.a) {
  count++;
  break;
}
console.log(count);

let hits = 0;
const maybe: { flag: boolean } | null = null;
if (maybe?.flag) {
  hits = 99;
}
console.log(hits);
`, "1\n0")
}

// ToNumber(Symbol) is a TypeError in real JS. A statically-typed Symbol flowing
// into a numeric context now throws a catchable TypeError at runtime rather than
// reinterpreting the symbol pointer as a number (invalid IR). ADR-00896.
func TestE2ESymbolToNumberThrows(t *testing.T) {
	assertOutput(t, `
const s = Symbol("x");
let threw = false;
try {
  const buf = new ArrayBuffer(s as any);
  console.log(buf.byteLength);
} catch (e) {
  threw = true;
}
console.log(threw);
`, "true")
}

// `string == number` is a no-overlap comparison TypeScript rejects; strict mode
// now rejects it cleanly (was invalid IR — a double compared as a string ptr),
// while -compat=js evaluates it via JS Abstract Equality. ADR-00897.
func TestE2EStringNumberLooseEqRejectedStrict(t *testing.T) {
	mustCompileError(t, `console.log(("-1" == -1))`, "have no overlap")
}

func TestE2EStringNumberLooseEqCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
console.log("-1" == -1);
console.log("false" == 0);
`, "true\nfalse")
}

// A ternary mixing a non-pointer scalar branch (number) with a pointer/reference
// branch (object) has a genuine union result (`number | object`) this typed-subset
// compiler can't put in one result slot — previously it coerced the object branch
// to the scalar's IR and emitted `store double %p, ptr %slot` (a clang-stage
// failure). Reject cleanly instead. ADR-00975.
func TestE2ETernaryScalarObjectMixRejectedStrict(t *testing.T) {
	mustCompileError(t, `
type Addr = { port: number }
function f(addr: Addr): void {
  const p = addr ? addr.port : addr
  console.log(p)
}
f({ port: 8080 })
`, "incompatible types")
}

// `~object` runs ToNumber(operand) in real JS (`~{}` is -1). strict rejects the
// non-numeric operand cleanly (was invalid IR — a `trunc` of the object ptr as
// an i64); -compat=js boxes and runs the real ToNumber. ADR-00898.
func TestE2EBitNotObjectRejectedStrict(t *testing.T) {
	mustCompileError(t, `console.log(~({}))`, "unary '~' requires a number")
}

func TestE2EBitNotObjectCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `console.log(~({}));`, "-1")
}

// --- Invalid-IR sweep part 3 (ADR-00899..00910) ---

// A non-ASCII identifier (JS/TS allow them) produced an illegal bare LLVM global
// name (`@__kml_global_<cyrillic>`), rejected by clang. llvmSafeSymbol now escapes
// any non-bare-identifier rune into the reserved namespace. ADR-00899.
func TestE2EUnicodeIdentifier(t *testing.T) {
	assertOutput(t, `
const δ = 5;
const ф = δ * 2;
let ключ = ф + 1;
console.log(ф);
console.log(ключ);
`, "10\n11")
}

// A void-returning builtin method result used in a value position (boxed as any)
// previously emitted `sitofp i64 <no reg>` from the absent void result. A void
// value in value position is `undefined`. ADR-00900.
func TestE2EVoidMethodResultAsValue(t *testing.T) {
	// Passing a void-returning method's result as an `any` argument boxes it; the
	// void value boxes to undefined rather than emitting `sitofp i64 <no reg>`.
	assertOutput(t, `
const s = new Set<number>();
s.add(1);
function isUndef(x: any): boolean { return x === undefined; }
console.log(isUndef(s.clear()));
const m = new Map<string, number>();
console.log(isUndef(m.clear()));
`, "true\ntrue")
}

// Bare null/undefined in an arithmetic operator: JS applies ToNumber (null→0,
// undefined→NaN). strict rejects; -compat=js evaluates. Also `string + null`
// concatenates ("xnull"), not `add ptr`. ADR-00901.
func TestE2ENullUndefinedArithmeticCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
console.log(null + null);
console.log(isNaN(null + undefined));
console.log(isNaN(undefined + undefined));
console.log("x" + null);
console.log(null + "y");
console.log("n" + undefined);
`, "0\ntrue\ntrue\nxnull\nnully\nnundefined")
}

func TestE2ENullArithmeticRejectedStrict(t *testing.T) {
	mustCompileError(t, `const x = null + undefined; console.log(x);`, "the value 'null' cannot be used here")
}

// isNaN / isFinite on an any-boxed value must ToNumber it first, so a boxed NaN
// tests true rather than returning the non-float false. ADR-00902.
func TestE2EIsNaNOnBoxedValue(t *testing.T) {
	assertOutputCompatJS(t, `
const x = null + undefined;
console.log(isNaN(x));
console.log(isFinite(x));
`, "true\nfalse")
}

// Reassigning a catch parameter (a caught value, { i8, i64 }) to a plain value
// previously emitted `fptoui double to { i8, i64 }`. The value is boxed then
// packed back into the caught record. ADR-00903.
func TestE2ECatchParamReassign(t *testing.T) {
	assertOutput(t, `
try {
  throw "stuff";
} catch (a) {
  console.log(a);
  a = 4 as any;
  console.log(a);
}
`, "stuff\n4")
}

// A generator declared but never constructed (`function* g(){}; typeof g`) still
// emits its body, which loads the generator runtime global — previously declared
// only on the construction path, leaving it referenced-but-undefined. ADR-00904.
func TestE2EGeneratorDeclaredNotConstructed(t *testing.T) {
	// The generator body is emitted even though g is never constructed; it loads
	// the generator runtime global, which must be declared. Printing a sentinel
	// confirms the body produced valid IR (the bug was invalid IR, not output).
	assertOutput(t, `
function* g() { yield 1; }
console.log("ok");
`, "ok")
}

// A derived constructor returning a primitive is a TypeError; a base constructor
// ignores it. Previously emitted `ret <value>` in a void function. ADR-00905.
func TestE2EDerivedConstructorReturnsPrimitiveThrows(t *testing.T) {
	assertOutput(t, `
class Base { constructor() {} }
class Derived extends Base {
  constructor() { super(); return 5 as any; }
}
let threw = false;
try { new Derived(); } catch (e) { threw = true; }
console.log(threw);
`, "true")
}

func TestE2EBaseConstructorReturnPrimitiveIgnored(t *testing.T) {
	assertOutput(t, `
class C {
  x: number;
  constructor() { this.x = 42; return 5 as any; }
}
const c = new C();
console.log(c.x);
`, "42")
}

// A method whose body returns a sibling method's result (`return this.#m()` /
// `return super.method()`) could not resolve that sibling's return type during
// registration, so a string-returning private method emitted `ret ptr` in an
// i64 function. ADR-00906.
func TestE2EPrivateMethodStringReturn(t *testing.T) {
	assertOutput(t, `
class C {
  #m(): string { return "test262"; }
  method() { return this.#m(); }
}
console.log(new C().method());
`, "test262")
}

func TestE2ESuperMethodReturnInference(t *testing.T) {
	assertOutput(t, `
class A { greet() { return "hi"; } }
class B extends A { access() { return super.greet(); } }
console.log(new B().access());
`, "hi")
}

// parseInt ToNumbers its radix: a string radix ("2") must parse, not reach
// strtoll as a `ptr` (invalid IR). ADR-00907.
func TestE2EParseIntStringRadix(t *testing.T) {
	assertOutput(t, `
console.log(parseInt("11", "2" as any));
console.log(parseInt("11", "0" as any));
console.log(parseInt("FF", 16));
`, "3\n11\n255")
}

// SameValueZero never coerces across types, so Array.includes with a search value
// of a disjoint type is always false — previously emitted `fcmp double, <string>`.
// strict rejects; -compat=js folds to false. ADR-00908.
func TestE2EArrayIncludesCrossTypeCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
const a = [42, 0, 1];
console.log(a.includes("42" as any));
console.log(a.includes(42));
`, "false\ntrue")
}

// null/undefined is equal (loosely or strictly) only to null/undefined; with a
// concrete scalar the result is a constant. Previously `undefined == 0` emitted
// `icmp eq ptr null, <number>` (invalid IR). ADR-00909.
func TestE2ENullUndefinedEqualityCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
console.log(undefined == 0);
console.log(undefined == true);
console.log(null == 0);
console.log(null === null);
console.log(null === undefined);
console.log(undefined == null);
console.log(undefined != 0);
`, "false\nfalse\nfalse\ntrue\nfalse\ntrue\ntrue")
}

// A non-string replacement to str.replace() is ToString'd ("a77b".replace(/77/,1)
// → "a1b"); previously the number was reinterpreted as a string pointer and
// passed to strlen (invalid IR). ADR-00910.
func TestE2EStringReplaceNumberReplacement(t *testing.T) {
	assertOutput(t, `
console.log("a77b".replace(new RegExp("77"), 1 as any));
console.log("xYYz".replace(new RegExp("YY"), 9 as any));
`, "a1b\nx9z")
}

// An IIFE that omits a trailing parameter — defaulted or not — previously emitted
// a call with fewer operands than the function's arity ("not enough parameters
// specified for call"). The call site now pads missing params with their default
// expression, else undefined. ADR-00911.
func TestE2EIIFEDefaultAndOmittedParams(t *testing.T) {
	assertOutput(t, `
let a = 0;
(function(f = 123) { a = f; }());
console.log(a);
let b = 0;
(function(x: number, y = 7) { b = x + y; })(10);
console.log(b);
`, "123\n17")
}

// An object whose valueOf returns void/undefined, used in a numeric context, must
// still CALL valueOf (side effects) and yield undefined→NaN — previously the
// object ptr reached the numeric op (invalid IR). ADR-00912.
func TestE2EToPrimitiveVoidValueOf(t *testing.T) {
	assertOutput(t, `
let calls = 0;
const n = { valueOf: function() { calls++; } };
const r = Math.max(NaN, n as any);
console.log(calls, Number.isNaN(r));
`, "1 true")
}

// A named function used as a reduce callback over a string array is re-emitted
// monomorphized against the element hint (string), so its untyped parameters are
// strings, not the i64 default — previously an i64/string IR mismatch. ADR-00913.
func TestE2ENamedCallbackReduceStringArray(t *testing.T) {
	assertOutput(t, `
function cb(prev, cur) { return prev + cur; }
console.log(["1", "2", "3"].reduce(cb));
console.log([10, 20, 30].reduce(cb));
`, "123\n60")
}

// A call through a first-class function value that omits a defaulted trailing
// parameter previously emitted a `call` with fewer operands than the callee's
// LLVM arity ("not enough parameters specified for call") — the closure-call
// path had no defaults to fill from. The closure value now carries its parameter
// defaults on its Type, and emitClosureCallByPtr fills omitted trailing
// parameters (a default that references an earlier parameter resolves via the
// paramDefaultScratch scope). TDD-00206 Stage 1 / ADR-00914.
func TestE2EClosureValueDefaultParams(t *testing.T) {
	assertOutput(t, `
const f = function(a: number, b: number = 10): number { return a + b; };
console.log(f(5));
console.log(f(5, 20));
const g = (a: number, b: number = a * 2): number => a + b;
console.log(g(4));
const s = (x: string, y: string = "!"): string => x + y;
console.log(s("hi"));
const obj = {
  method(x: number, y: number = x, z: number = y): number { return x + y + z; }
};
console.log(obj.method(3));
`, "15\n25\n12\nhi!\n9")
}

// TDD-00206 Stage 2: a parameter default that references a variable captured
// from the closure's defining scope is evaluated in the closure body prologue —
// where the captured environment is bound — driven by a hidden argument-presence
// mask. Each returned closure carries its own captured `base`; an omitted
// argument falls back to that closure's default, a provided one wins.
func TestE2EClosureCapturedDefault(t *testing.T) {
	assertOutput(t, `
function make(base: number) {
  return (x: number, y: number = base): number => x + y;
}
const f = make(100);
const g = make(200);
console.log(f(5));
console.log(g(5));
console.log(f(5, 1));
`, "105\n205\n6")
}

// The captured-default closure also works when invoked through a higher-order
// method: the HOF supplies (value, index), so the defaulted parameter takes the
// index; calling the same closure directly with the argument omitted falls back
// to the captured default (the presence mask distinguishes the two).
func TestE2EClosureCapturedDefaultViaHOF(t *testing.T) {
	assertOutput(t, `
function makeScaler(mult: number) {
  return (v: number, factor: number = mult): number => v * factor;
}
const s = makeScaler(10);
console.log([1, 2, 3].map(s).join(","));
console.log(s(5));
`, "0,2,6\n50")
}

// TDD-00206 Stage 2 polish: an async closure value may carry a captured-scope
// parameter default. The body prologue fills it inside the inline async body
// block (after captures are bound), exactly as the synchronous path does; each
// returned closure keeps its own captured `base`.
func TestE2EAsyncClosureCapturedDefault(t *testing.T) {
	assertOutput(t, `
function make(base: number) {
  return async (x: number, y: number = base): Promise<number> => x + y;
}
const f = make(100);
const g = make(200);
f(5).then(r => console.log(r));
g(5).then(r => console.log(r));
f(5, 1).then(r => console.log(r));
`, "105\n205\n6")
}

// TDD-00206 Stage 2 polish: a captured-scope (here, module-global) default on an
// object or array parameter fills the parameter's pointer / object-reference
// header slot in the body prologue, not just a scalar alloca.
func TestE2EClosureCapturedDefaultObjectAndArray(t *testing.T) {
	assertOutput(t, `
const cfg = { n: 100 };
const g = (x: number, c: { n: number } = cfg): number => x + c.n;
console.log(g(5));
console.log(g(5, { n: 1 }));

const fallback: number[] = [10, 20, 30];
const f = (x: number, items: number[] = fallback): number => x + items.length;
console.log(f(1));
console.log(f(1, [1, 2]));
`, "105\n6\n4\n3")
}

// TDD-00206 Stage 2 polish: Function.prototype.bind on a closure whose parameter
// default references a captured variable now threads the argument-presence mask
// through the bind trampoline, so an omitted trailing argument on the bound
// function still falls back to the captured default while a supplied one wins.
func TestE2EBindCapturedDefault(t *testing.T) {
	assertOutput(t, `
function make(base: number) {
  return (x: number, y: number = base): number => x + y;
}
const f = make(100);
const bf = f.bind(null, 5);
console.log(bf());
console.log(bf(7));
const g = make(7);
const bg = g.bind(null);
console.log(bg(2));
console.log(bg(2, 3));
`, "105\n12\n9\n5")
}

// Bug fixed in passing (TDD-00206 Stage 1): an optional `x?: T` parameter on a
// first-class function value now reads as `T | undefined` in the body — the same
// widening a named function's signature gets (TDD-00187) — so an omitted argument
// is a genuinely-absent nullable value, not a typed zero. Previously closures
// skipped `optionalParamType`, so `(a, b?: number) => a + (b ?? 9)` called with
// one argument treated `b` as 0 (and pre-fix under-supplied the call → invalid IR).
func TestE2EClosureOptionalParamAbsent(t *testing.T) {
	assertOutput(t, `
const opt = (a: number, b?: number): number => a + (b ?? 99);
console.log(opt(1));
console.log(opt(1, 2));
const f = function(a: number, b?: number): number { return b === undefined ? a : a + b; };
console.log(f(10));
console.log(f(10, 5));
`, "100\n3\n10\n15")
}

// Invalid-IR sweep: an async *function expression* that falls off the end (no
// explicit `return`) inferred a `void`/scalar return type while its inline async
// epilogue returns the settled promise pointer — the emitted `define void` then
// `ret ptr` was rejected by clang ("value doesn't match function result type
// 'void'"). The arrow path already wrapped the type in Promise; the
// function-expression path now does too, so the define's signature is `ptr`.
func TestE2EAsyncFunctionExpressionReturnsPromise(t *testing.T) {
	assertOutput(t, `
const f = async function() { console.log("body"); };
f().then(() => console.log("resolved"));
`, "body\nresolved")
}

// Invalid-IR sweep: a named function used as a value via `.apply()` / `.call()`
// inferred `void` as its result type (the funcref identifier's Type carried no
// return type), so `return f.apply()` in an enclosing function emitted a typed
// call inside a `void`-typed define — a signature mismatch (invalid IR). The
// funcref identifier now carries its resolved signature.
func TestE2EFuncRefApplyCallReturnType(t *testing.T) {
	assertOutput(t, `
function f(): boolean { return true; }
const viaApply = function (): boolean { return f.apply(); };
const viaCall = function (): boolean { return f.call(); };
console.log(viaApply());
console.log(viaCall());
`, "true\ntrue")
}

// Invalid-IR sweep: `f.call()` with no arguments at all (not even a thisArg)
// indexed `args[1:]` on an empty slice and panicked in codegen. It now forwards
// an empty argument list.
func TestE2EFuncCallZeroArgs(t *testing.T) {
	assertOutput(t, `
function greet(): string { return "hi"; }
console.log(greet.call());
console.log(greet.apply());
`, "hi\nhi")
}

// Invalid-IR sweep: a non-callable `.then`/`.catch` handler (e.g. `p.then(3, 5)`,
// `p.catch(42)`) was lowered by storing the raw argument into a callback `ptr`
// slot — a numeric literal became a `double` constant in a `ptr` field ("floating
// point constant invalid for type"). Per spec a non-callable handler is treated
// as undefined (pass-through), so it now lowers to a null callback slot.
// (built-ins/Promise/prototype/then/S25.4.5.3_A4.2_T{1,2}.js.)
func TestE2ENonCallableThenCatchHandlers(t *testing.T) {
	assertOutput(t, `
async function g(): Promise<number> { return 7; }
g().then(3, 5).then((v) => console.log("v", v));
g().catch(42).then((v) => console.log("c", v));
`, "v 7\nc 7")
}

// Invalid-IR sweep + evolving-any (ADR-00923): an unannotated `let`/`var`
// initialized to `null`/`undefined` is TypeScript's "evolving any" — a later
// assignment gives it a concrete value. It is widened to the any-box when
// reassigned, so `x ??= 1` stores 1 instead of a `double` into a null `ptr` slot
// (was "floating point constant invalid for type"), and `x = "s"` no longer
// silently keeps null. A never-reassigned null binding stays null-typed.
// (Test262 language/expressions/object/cpn-obj-lit-...-coalesce.js et al.)
func TestE2ENullEvolvingCompoundAssign(t *testing.T) {
	assertOutput(t, `
let x = null;
x ??= 1;
console.log(x);
`, "1")
}

func TestE2ENullEvolvingReassignString(t *testing.T) {
	assertOutput(t, `
let x = null;
x = "hi";
console.log(x);
`, "hi")
}

func TestE2ENullEvolvingNeverReassignedStaysNull(t *testing.T) {
	assertOutput(t, `
let a = null;
console.log(a);
console.log(a === null);
`, "null\ntrue")
}

// `??` / `??=` on an explicit any-box consult the NaN-box tag for null/undefined
// (ADR-00923) — previously the box was returned unchanged (treated non-nullish).
func TestE2ENullishCoalesceOnAny(t *testing.T) {
	assertOutput(t, `
let y: any = null;
console.log(y ?? 7);
y ??= 9;
console.log(y);
y = 3;
console.log(y ?? 100);
`, "7\n9\n3")
}

// An array aggregate ({ ptr, i64 }) assigned into a `ptr`-shaped binding (an
// object / string / handle slot) previously slipped past the cross-type
// rejection — both report Type.IR "ptr" — and emitted `store ptr %agg`, the
// aggregate where a pointer is required (invalid IR: the Test262
// Map.prototype.get "different key types" idiom `var item = {}; item = []`).
// Strict now rejects the shape divergence cleanly; -compat=js widens the
// binding to an any-box and runs. ADR-00933.
func TestE2EArrayIntoObjectSlotRejectedStrict(t *testing.T) {
	mustCompileError(t, `
let item = {};
item = [];
console.log(item);
`, "cannot assign a array value")
}

func TestE2EArrayIntoObjectSlotCompatJS(t *testing.T) {
	assertOutputCompatJS(t, `
let item: any = {};
item = [];
console.log(Array.isArray(item));
item = "str";
console.log(item);
`, "true\nstr")
}

// Array.isArray on a dynamic (`any`) value consults the runtime box tag, not the
// always-false compile-time IsArray — a genuine boxed array reports true, a boxed
// object false, even after cross-type reassignment (ADR-00934).
func TestE2EArrayIsArrayOnAny(t *testing.T) {
	assertOutput(t, `
let a: any = [1, 2, 3];
console.log(Array.isArray(a));
let b: any = { x: 1 };
console.log(Array.isArray(b));
a = "not an array";
console.log(Array.isArray(a));
`, "true\nfalse\nfalse")
}
