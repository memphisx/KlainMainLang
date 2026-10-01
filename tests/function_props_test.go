package tests

import (
	"strings"
	"testing"
)

// A function's own properties: set and read through `any`, `in`, `hasOwn`,
// keys, entries, delete, descriptors and inspect.
func TestE2EFunctionOwnProperties(t *testing.T) {
	assertSameAsNode(t, `
const kCustom = Symbol.for('nodejs.util.promisify.custom');
function f(a: string, cb: (e: any, v: any) => void): void { cb(null, a); }
Object.defineProperty(f, kCustom, { value: (a: string) => Promise.resolve({ x: a }), enumerable: false });
const g: any = f;
g.tag = "t";
const h: any = f;
console.log(typeof h[kCustom], h.tag, h.nope);
console.log(f);
console.log([g]);
h[kCustom]("q").then((r: any) => console.log(JSON.stringify(r)));
const arrow: any = (x: number) => x;
arrow.y = 1;
console.log(arrow, arrow.y, Object.keys(arrow));
function k(): number { return 1; }
const m: any = k;
m.a = 1; m["b"] = "two";
console.log(Object.keys(m), "a" in m, "z" in m, "call" in m, Object.hasOwn(m, "a"), m.hasOwnProperty("b"));
console.log(Object.entries(m), Object.values(m));
for (const key in m) console.log("key", key);
delete m.a;
console.log(m, Object.getOwnPropertyDescriptor(m, "b"));
const s = Symbol("s");
(k as any)[s] = 3;
console.log((k as any)[s], k);
function outer(): void {
  function inner(): void {}
  inner.z = 2;
  inner[s] = 3;
  console.log(inner.z, inner[s], inner);
}
outer();
`)
}

// TypeScript's expando declarations: `f.p = v` in the declaring scope adds
// p to f's type; any other property is TS2339.
func TestE2EFunctionExpandoDeclarations(t *testing.T) {
	assertSameAsNode(t, `
function q(x: number): number { return x; }
q.tag = "t";
q.n = 2;
console.log(q.tag, q.n + 1, q);
const s: string = q.tag;
console.log(s.length);
`)
	for _, c := range []struct{ src, want string }{
		{"function q(x: number): number { return x; }\nconsole.log(q.zzz);\n", "property 'zzz' does not exist"},
		{"function q(x: number): number { return x; }\nq.n = 1;\nconst k: string = q.n;\n", "type 'number' is not assignable to type 'string'"},
	} {
		_, err := parseAndCompileImports(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
}

// `hasOwnProperty` and `in` on an any: objects, overrides, primitives,
// arrays and class instances.
func TestE2EAnyHasOwnPropertyAndArrayMembership(t *testing.T) {
	assertSameAsNode(t, `
const o: any = {a:1};
console.log(o.hasOwnProperty("a"), o.hasOwnProperty("b"));
const p: any = JSON.parse('{"a":1}');
console.log(p.hasOwnProperty("a"));
const q: any = { hasOwnProperty: (k: string) => "custom " + k };
console.log(q.hasOwnProperty("z"));
const n: any = 5;
console.log(n.hasOwnProperty("x"));
const str: any = "ab";
console.log(str.hasOwnProperty("length"), str.hasOwnProperty("0"), str.hasOwnProperty("x"));
const arr: any = [1, 2];
console.log(arr.hasOwnProperty("0"), arr.hasOwnProperty("5"), arr.hasOwnProperty("length"), "1" in arr, "push" in arr, "9" in arr);
class C { x = 1; m() {} }
const c: any = new C();
console.log(c.hasOwnProperty("x"), c.hasOwnProperty("m"));
try { const u: any = null; u.hasOwnProperty("a"); } catch (e) { console.log((e as Error).message); }
`)
}

// Array methods on an any holding an array: a boxed static array (its
// mutators write back), a dynamic one, an any[]; a string or an object keeps
// its own.
func TestE2EAnyArrayMethods(t *testing.T) {
	assertSameAsNode(t, `
const s: string[] = ["x", "y"];
const a: any = s;
console.log(a.slice(1), a.join("-"), a.length, a.indexOf("y"), a.map((v: string) => v + "!"));
a.push("z");
console.log(s, s.length, a.includes("z"));
const n: number[] = [3, 1, 2];
const an: any = n;
an.sort();
console.log(n, an.reduce((x: number, y: number) => x + y, 0));
const d: any = JSON.parse('["p","q"]');
console.log(d.slice(1), d.join("-"));
d.push("r");
console.log(d, d.length);
const aa: any[] = [1, "two"];
const ab: any = aa;
ab.push(3);
console.log(aa);
const str: any = "hello";
console.log(str.slice(1), str.indexOf("l"), str.includes("ell"));
const o: any = { slice: () => "own" };
console.log(o.slice());
const num: any = 1.55;
console.log(num.toFixed(1), num.toPrecision(2), num.toExponential(1));
console.log(a.filter((v: any) => v !== "x"), a.at(-1), a.concat(["w"]));
`)
}

// An any holding an array of objects, class instances, nullable numbers or
// Maps: element reads, for-of, methods, JSON, inspect and a push's write-back.
func TestE2EAnyObjectArrays(t *testing.T) {
	assertSameAsNode(t, `
interface P { n: number }
const ps: P[] = [{ n: 1 }, { n: 2 }];
const ap: any = ps;
console.log(ap[0].n, ap.length, ap);
for (const p of ap) console.log(p.n);
console.log(ap.map((p: any) => p.n), JSON.stringify(ap), String(ap));
ap.push({ n: 3 });
console.log(ps.length, ps[2].n);
class C { v: string; constructor(v: string) { this.v = v; } }
const cs: any = [new C("a"), new C("b")];
console.log(cs, cs.filter((c: any) => c.v === "b").length);
const ns: (number | null)[] = [1, null];
const an: any = ns;
console.log(an, an[1], an.indexOf(null));
const m = new Map<string, number>([["a", 1]]);
const ms: any = [m];
console.log(ms);
`)
}

// A promise through any, and util.inspect of promises.
func TestE2EPromiseThroughAnyAndInspect(t *testing.T) {
	assertSameAsNode(t, `
const p: any = Promise.resolve(4);
const pp: Promise<any> = p;
pp.then((r: any) => console.log("pp", r));
const pn: Promise<number> = p;
pn.then((r) => console.log("pn", r + 1));
function back(x: any): (...args: any[]) => Promise<any> { return x; }
const m: any = (...args: any[]): Promise<any> => Promise.resolve(args.length);
back(m)(5, 6).then((r: any) => console.log("back", r));
console.log(Promise.resolve(2));
const b: Promise<any> = Promise.resolve("x");
console.log(b, [Promise.resolve({ a: 1 })]);
console.log(new Promise(() => {}));
const rs = Promise.reject("str");
rs.catch(() => {});
console.log(rs);
const o: any = {}; const k = Symbol("k"); o[k] = 3; o.z = 1;
console.log(o);
`)
}

// An error's own properties survive a throw and a rejection.
func TestE2ECaughtErrorOwnProperties(t *testing.T) {
	assertSameAsNode(t, `
const e: any = new Error("x");
e.code = 3;
e.extra = "y";
Promise.reject(e).catch((r: any) => console.log("catch", r.code, r.extra));
async function f(): Promise<void> {
  try { await Promise.reject(e); } catch (c: any) { console.log("await", c.code, c.extra); }
  try { await new Promise((res, rej) => rej(e)); } catch (c: any) { console.log("await2", c.code, c.extra); }
}
f();
try { throw e; } catch (c: any) { console.log(c.code, c.extra, c.message); }
`)
}

// util.promisify honours util.promisify.custom: exec/execFile resolve
// { stdout, stderr }, a function's own custom wins.
func TestE2EPromisifyCustom(t *testing.T) {
	assertSameAsNodeImports(t, `
import { exec, execFile } from "child_process";
import { promisify } from "util";
import * as util from "util";
const p = promisify(exec);
const pf = util.promisify(execFile);
console.log(typeof promisify.custom, promisify.custom === Symbol.for('nodejs.util.promisify.custom'), util.promisify.custom === promisify.custom);
async function main(): Promise<void> {
  const r = await p("echo hi");
  console.log(JSON.stringify(r));
  const r2 = await pf("echo", ["a", "b"]);
  console.log(JSON.stringify(r2));
  try { await p("exit 3"); } catch (e: any) { console.log(e.code, JSON.stringify(e.stdout), JSON.stringify(e.stderr)); }
  const pp: any = p("echo x");
  console.log(typeof pp.child.pid);
  await pp;
  function cb(a: number, done: (e: any, v: number) => void): void { done(null, a * 2); }
  const pcb = promisify(cb);
  console.log(await pcb(21));
  const custom = () => Promise.resolve("custom!");
  function withCustom(done: (e: any, v: string) => void): void { done(null, "plain"); }
  withCustom[promisify.custom] = custom;
  console.log(await promisify(withCustom)());
}
main();
`)
}

// Object.defineProperties, and Object.assign with an any target or source.
func TestE2EObjectDefinePropertiesAndDynamicAssign(t *testing.T) {
	assertSameAsNode(t, `
const o: any = {};
Object.defineProperties(o, { a: { value: 1, enumerable: true }, b: { get() { return 2; }, enumerable: false } });
console.log(o.a, o.b, Object.keys(o));
const src: any = {};
Object.defineProperty(src, "g", { get() { return 7; }, enumerable: true });
const t: any = Object.assign({}, src);
console.log(t.g, Object.getOwnPropertyDescriptor(t, "g"));
const s2: any = { a: 1, b: "x" };
const t2 = Object.assign({}, s2, null, { c: true });
console.log(t2, t2.a);
const tgt: any = { z: 0 };
Object.assign(tgt, { y: 2 });
console.log(tgt);
function fnx(): void {}
const fa = Object.assign(fnx as any, { tag: 1 });
console.log(fa.tag, fnx);
`)
}

// A function called through `any` (or adapted to a `(...args: any[])`
// callback) fills an omitted parameter from its default, whatever form
// declared it.
func TestE2EDefaultParamsThroughDynamicCalls(t *testing.T) {
	assertOutput(t, `
function f(x: number = 5) { console.log('decl', x) }
const g: any = f
g()
const i: any = (x: number = 8) => console.log('inline', x)
i()
function take(l: (...args: any[]) => void) { l() }
take((x: number = 9) => console.log('rest', x))
function take2(l: any) { l() }
take2((x: string = 'd') => console.log('any', x))
`, "decl 5\ninline 8\nrest 9\nany d")
}

// A machine-integer number (Number.MAX_SAFE_INTEGER) into `any` is boxed as
// a number, not its raw bits.
func TestE2EIntegerConstantIntoAny(t *testing.T) {
	assertOutput(t, `
const a: any = undefined
const n: number = a ?? Number.MAX_SAFE_INTEGER
const m: any = a ?? Number.MIN_SAFE_INTEGER
console.log(n, m)
`, "9007199254740991 -9007199254740991")
}

// A top-level function-typed `let` (nullable or not) is visible to a named
// function declaration, and reassignments are seen through it.
func TestE2EFunctionTypedTopLevelLet(t *testing.T) {
	assertOutput(t, `
let sink: ((w: any) => void) | null = null;
let fixed: (w: number) => void = (w) => { console.log("init", w); };
function fire(x: any): void {
  if (sink !== null) sink(x); else console.log("nosink", x);
  fixed(1);
}
fire(1);
sink = (w) => console.log("sink", w);
fixed = (w) => console.log("fixed", w);
fire(2);
`, "nosink 1\ninit 1\nsink 2\nfixed 1")
}

// A closure whose parameters differ from a rest-only function type in their
// fixed prefix converts through the box both ways; a return inside a try
// block reads the block's own locals for its type.
func TestE2ERestClosureConversions(t *testing.T) {
	assertOutput(t, `
const runner = (cb: (...args: any[]) => any, ...args: any[]): any => Reflect.apply(cb, undefined, args);
const g: (...args: any[]) => any = runner;
console.log(g(() => 2));
const rest = (...args: any[]): any => args.length;
const fixed: (fn: () => void, ...args: any[]) => any = rest;
console.log(fixed(() => {}, 1, 2));
function run(fn: () => any): any { return fn(); }
const p = run(() => {
  try {
    const promise: any = Promise.resolve(5);
    return (promise as Promise<any>).then((r: any) => r);
  } finally {
    console.log('finally');
  }
});
p.then((v: any) => console.log('v', v));
`, "2\n3\nfinally\nv 5")
}
