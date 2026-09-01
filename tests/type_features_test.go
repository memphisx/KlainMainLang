package tests

import "testing"

// --- Ambient declarations (`declare`), ADR-00388 ---

func TestE2EDeclareAmbientErased(t *testing.T) {
	// `declare` ambient declarations are parsed and erased; real code compiles.
	assertOutput(t, `
declare const VERSION: string;
declare function ext(x: number): number;
declare let FLAG: boolean;
console.log("ok")
`, "ok")
}

func TestE2EDeclareBlockForms(t *testing.T) {
	assertOutput(t, `
declare global {
  interface Window { title: string; }
}
declare module "ext-lib" {
  export function doThing(): void;
}
declare namespace NS { const y: number; }
function real(n: number): number { return n * 2; }
console.log(real(21))
`, "42")
}

func TestE2EDeclareNoSemicolonASI(t *testing.T) {
	assertOutput(t, `
declare const A: number
declare const B: number
const x: number = 5
console.log(x)
`, "5")
}

// --- typeof type queries, ADR-00389 ---

func TestE2ETypeofObjectAlias(t *testing.T) {
	assertOutput(t, `
const config = { host: "localhost", port: 8080 };
type Config = typeof config;
const c2: Config = { host: "h", port: 1 };
console.log(c2.port)
`, "1")
}

func TestE2ETypeofScalarInline(t *testing.T) {
	assertOutput(t, `
const base = 42;
let copy: typeof base = 100;
console.log(copy)
`, "100")
}

func TestE2ETypeofAsParameterType(t *testing.T) {
	assertOutput(t, `
const settings = { retries: 3, name: "svc" };
function show(s: typeof settings): string { return s.name; }
console.log(show({ retries: 1, name: "x" }))
`, "x")
}

func TestE2ETypeofFunctionQuery(t *testing.T) {
	assertOutput(t, `
function helper(n: number): number { return n * 3; }
let g: typeof helper = helper;
console.log(g(4))
`, "12")
}

func TestE2ETypeofLocalObject(t *testing.T) {
	assertOutput(t, `
function go(): number {
  const o = { a: 1 };
  let x: typeof o = { a: 2 };
  return x.a;
}
console.log(go())
`, "2")
}

// --- Index signatures, TDD-00130 / ADR-00390 ---

func TestE2EIndexSignatureNumberValues(t *testing.T) {
	assertOutput(t, `
interface Dict { [key: string]: number; }
const d: Dict = { a: 1, b: 2 };
console.log(d["a"]);
d["c"] = 3;
console.log(d["a"] + d["b"] + d["c"]);
`, "1\n6")
}

func TestE2EIndexSignatureStringValues(t *testing.T) {
	assertOutput(t, `
type StrMap = { [k: string]: string };
const m: StrMap = { greeting: "hello" };
m["name"] = "world";
console.log(m["greeting"] + " " + m["name"]);
`, "hello world")
}

func TestE2EIndexSignatureObjectKeys(t *testing.T) {
	assertOutput(t, `
interface Dict { [key: string]: number; }
const d: Dict = { a: 1, b: 2 };
for (const k of Object.keys(d)) { console.log(k); }
`, "a\nb")
}

func TestE2EIndexSignatureNumberKey(t *testing.T) {
	// ADR-00461: a number index signature is supported — keys stringify
	// (JS object keys are strings), sharing the string-signature map
	// backing; an absent key reads as null.
	assertOutput(t, `
interface Sparse { [i: number]: string; }
const s: Sparse = {};
s[0] = "zero";
s[42] = "answer";
console.log(s[0]);
console.log(s[42]);
console.log(s[1]);
`, "zero\nanswer\nnull")
}

func TestE2EIndexSignatureMixedRejected(t *testing.T) {
	_, err := parseAndCompile(`interface Bad { id: number; [k: string]: string; }`)
	if err == nil {
		t.Fatal("expected a compile error for named properties combined with an index signature, got none")
	}
}

func TestE2EGenericFunctionTypeErased(t *testing.T) {
	// ADR-00469: `<T>(x: T) => T` in a type position erases T to `any` —
	// generic functions are monomorphized declarations, not values.
	assertOutput(t, `
var f: <T>(x: T) => T;
f = (x: any): any => x;
console.log(f("hello"));
console.log(f(42));
`, "hello\n42")
}

func TestE2EAmbientValueDeclarations(t *testing.T) {
	// ADR-00471: `declare var` is a zero-initialized var; `declare
	// function` compiles to a throwing stub — a clear runtime error only
	// if the ambient is actually called; redeclared signatures collapse
	// last-wins.
	assertOutput(t, `
declare var flag: boolean;
declare function before(): void;
declare function overloaded(x: string): void;
declare function overloaded(x: number): void;
function safe(): string { return flag ? "on" : "off"; }
console.log(safe());
try { before(); } catch (e) { console.log(e.message); }
`, "off\nambient function 'before' has no implementation")
}

func TestE2ETypePredicatesAndAssertSignatures(t *testing.T) {
	// ADR-00474: `x is T` returns resolve to boolean; `asserts x [is T]`
	// to void — the narrowing itself isn't modeled.
	assertOutput(t, `
function isNumber(x: any): x is number {
    return typeof x === "number";
}
function assertString(x: any): asserts x is string {
    if (typeof x !== "string") { throw new Error("not string"); }
}
console.log(isNumber(4));
console.log(isNumber("s"));
assertString("ok");
console.log("done");
`, "true\nfalse\ndone")
}

func TestE2EBareClassFieldDefaultsToNumber(t *testing.T) {
	// ADR-00474: a bare field follows the unannotated-parameter precedent.
	assertOutput(t, `
class C {
    x;
    y;
    constructor() { this.x = 1; }
    sum(): number { return this.x + this.y; }
}
const c = new C();
c.y = 41;
console.log(c.sum());
`, "42")
}

func TestE2EEnumBracketAndReverseMapping(t *testing.T) {
	// ADR-00480: `E["B"]` literal-key access and the numeric reverse
	// mapping (`E[1]` → "B", unmatched → "undefined", runtime index works).
	assertOutput(t, `
enum E { A, B, C }
console.log(E["B"]);
console.log(E[1]);
console.log(E[99]);
let i = 2;
console.log(E[i]);
`, "1\nB\nundefined\nC")
}

func TestE2EDeleteOperator(t *testing.T) {
	// ADR-00487: `delete process.env.KEY` unsets for real; a dict key
	// deletes through the map (dot and bracket forms); fixed-shape targets
	// stay clean rejections.
	assertOutput(t, `
process.env.KML_DEL_T = "on";
delete process.env.KML_DEL_T;
console.log(process.env.KML_DEL_T === undefined || process.env.KML_DEL_T === "");
interface D { [k: string]: number; }
const d: D = {};
d["x"] = 1;
delete d["x"];
console.log(Object.keys(d).length);
d["y"] = 2;
console.log(delete d.y, Object.keys(d).length);
`, "true\n0\ntrue 0")
}

func TestE2EFsMkdirSyncRecursive(t *testing.T) {
	// ADR-00487: { recursive: true } creates every missing prefix and is
	// idempotent.
	assertOutputImports(t, `
import fs from 'fs';
fs.mkdirSync("/tmp/kml_mkp_e2e/a/b", { recursive: true });
fs.mkdirSync("/tmp/kml_mkp_e2e/a/b", { recursive: true });
console.log(fs.existsSync("/tmp/kml_mkp_e2e/a/b"));
fs.rmdirSync("/tmp/kml_mkp_e2e/a/b");
fs.rmdirSync("/tmp/kml_mkp_e2e/a");
fs.rmdirSync("/tmp/kml_mkp_e2e");
console.log("ok");
`, "true\nok")
}

func TestE2ESymbolForRegistry(t *testing.T) {
	// ADR-00488: Symbol.for shares one symbol per key (identity), loose
	// Symbol() stays distinct; keyFor returns the key or null.
	assertOutput(t, `
const a = Symbol.for("app.key");
const b = Symbol.for("app.key");
const c = Symbol("loose");
console.log(a === b);
console.log(a === c);
console.log(Symbol.keyFor(a));
console.log(Symbol.keyFor(c) === null);
`, "true\nfalse\napp.key\ntrue")
}

// Template literal types (`` `a-${T}` ``) parse and resolve to `string` — the
// literal pattern isn't narrowed/enforced, the same simplification
// string-literal types use (ADR-00561). No-substitution, multi-substitution,
// and array-of forms all work.
func TestE2ETemplateLiteralType(t *testing.T) {
	assertOutput(t, `
type Id = `+"`"+`user-${number}`+"`"+`;
const a: Id = "user-42";
type Plain = `+"`"+`hello`+"`"+`;
const b: Plain = "hello";
type Multi = `+"`"+`${string}-${number}`+"`"+`;
const c: Multi = "x-1";
const d: `+"`"+`a-${string}`+"`"+`[] = ["a-b", "a-c"];
console.log(a, b, c, d.length);
`, "user-42 hello x-1 2")
}
