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

func TestE2EIndexSignatureNumberKeyRejected(t *testing.T) {
	_, err := parseAndCompile(`interface NumIx { [i: number]: string; }`)
	if err == nil {
		t.Fatal("expected a compile error for a numeric index signature, got none")
	}
}

func TestE2EIndexSignatureMixedRejected(t *testing.T) {
	_, err := parseAndCompile(`interface Bad { id: number; [k: string]: string; }`)
	if err == nil {
		t.Fatal("expected a compile error for named properties combined with an index signature, got none")
	}
}
