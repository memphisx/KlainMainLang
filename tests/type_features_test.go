package tests

import (
	"strings"
	"testing"
)

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
	// A number index signature: keys stringify (JS object keys are
	// strings), sharing the string-signature map backing; an absent key
	// reads undefined.
	assertOutput(t, `
interface Sparse { [i: number]: string; }
const s: Sparse = {};
s[0] = "zero";
s[42] = "answer";
console.log(s[0]);
console.log(s[42]);
console.log(s[1]);
`, "zero\nanswer\nundefined")
}

// A property that does not fit the interface's string index signature is
// tsc's TS2411.
func TestE2EIndexSignatureMixedRejected(t *testing.T) {
	_, err := parseAndCompile(`interface Bad { id: number; [k: string]: string; }`)
	if err == nil || !strings.Contains(err.Error(), "'string' index type") {
		t.Fatalf("expected TS2411 for a property outside the index signature's type, got %v", err)
	}
}

// Named properties beside an index signature (Node's IncomingHttpHeaders
// shape): a named read has its declared type, the rest the index's; the
// dictionary inspects, serializes and iterates as the plain object it is.
func TestE2EIndexSignatureWithNamedProperties(t *testing.T) {
	assertOutput(t, `
interface Headers {
  [key: string]: string | string[] | undefined;
  host?: string;
  'set-cookie'?: string[];
}
function add(dest: Headers, field: string, value: string): void {
  if (field === 'set-cookie') {
    const sc = dest['set-cookie'];
    if (sc !== undefined) sc.push(value); else dest['set-cookie'] = [value];
    return;
  }
  const cur = dest[field];
  if (typeof cur === 'string') dest[field] = cur + ', ' + value; else dest[field] = value;
}
const h: Headers = {};
add(h, 'host', 'x'); add(h, 'set-cookie', 'a'); add(h, 'set-cookie', 'b'); add(h, 'x-a', '1'); add(h, 'x-a', '2');
console.log(h.host, h['set-cookie'], h['x-a'], h['nope']);
console.log(h);
const host: string = h.host ?? 'none';
console.log(host.toUpperCase(), JSON.stringify(h));
for (const k in h) console.log(k);
`, "x [ 'a', 'b' ] 1, 2 undefined\n{ host: 'x', 'set-cookie': [ 'a', 'b' ], 'x-a': '1, 2' }\nX {\"host\":\"x\",\"set-cookie\":[\"a\",\"b\"],\"x-a\":\"1, 2\"}\nhost\nset-cookie\nx-a")
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
try { before(); } catch (e) { console.log((e as Error).message); }
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
	// Symbol() stays distinct; keyFor returns the key or undefined.
	assertOutput(t, `
const a = Symbol.for("app.key");
const b = Symbol.for("app.key");
const c = Symbol("loose");
console.log(a === b);
console.log(a === c);
console.log(Symbol.keyFor(a));
console.log(Symbol.keyFor(c) === undefined);
`, "true\nfalse\napp.key\ntrue")
}

// Template literal types (“ `a-${T}` “) parse and resolve to `string` — the
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

// TS2304: a name nothing declares — not the program, not a builtin module's
// import, not TypeScript's library or Node's declarations — is the checker's
// "cannot find name", as tsc reports it. Names bound where the merged
// program no longer shows them (a builtin import, an erased ambient
// declaration, an import-equals alias) and every library global are not.
func TestE2ECannotFindName(t *testing.T) {
	mustCompileError(t, `console.log(nope)`, "cannot find name 'nope'")
	mustCompileError(t, `function f(): void { { let y = 5 } console.log(y) } f()`, "cannot find name 'y'")
	assertOutputImports(t, `
import { EventEmitter } from 'events'
import { join } from 'path'
namespace App { export namespace Config { export const port = 8080 } }
import Cfg = App.Config
declare class Ambient {}
const e = new EventEmitter()
console.log(e instanceof EventEmitter, join("a", "b"), Cfg.port, typeof setTimeout, typeof globalThis)
`, "true a/b 8080 function object")
}

// Node's `path` module declared in lib/node.d.ts: an import's uses type
// through the module's declarations, so a call tsc rejects is rejected here
// (TS2345, TS2554), and a correct one runs.
func TestE2EPathModuleDeclared(t *testing.T) {
	assertOutputImports(t, `
import { join, basename } from "path"
import path from "path"
const p = path.parse("/home/u/f.txt")
console.log(join("a", "b"), basename("/x/y.ts", ".ts"), path.dirname("/a/b/c"), path.extname("f.txt"), p.name)
`, "a/b y /a/b .txt f")
	_, err := parseAndCompileImports(t, `
import path from "path"
console.log(path.join("a", 1))
`)
	if err == nil || !strings.Contains(err.Error(), "argument of type 'number' is not assignable to parameter of type 'string'") {
		t.Errorf("expected TS2345 for path.join(\"a\", 1), got %v", err)
	}
}

// Node's `process` declared in lib/node.d.ts through `declare namespace
// NodeJS`: qualified type names (`NodeJS.Platform`, a user namespace's
// `Shapes.Box`) resolve through namespace scopes, and an env lookup is
// `string | undefined`, so tsc's TS2322 on it is reported here.
func TestE2EProcessDeclared(t *testing.T) {
	assertOutputImports(t, `
namespace Shapes {
  export interface Box { w: number; h: number }
  export type Side = "l" | "r"
}
const b: Shapes.Box = { w: 2, h: 3 }
const s: Shapes.Side = "r"
const plat: NodeJS.Platform = process.platform
const home: string | undefined = process.env.KLM_NO_SUCH_VAR
const m: NodeJS.MemoryUsage = process.memoryUsage()
console.log(b.w * b.h, s, typeof plat, home === undefined, typeof process.cwd(), process.argv.length > 0, m.rss > 0)
`, "6 r string true string true true")
	_, err := parseAndCompileImports(t, `
const n: number = process.env.HOME
console.log(n)
`)
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type 'number'") {
		t.Errorf("expected TS2322 for a number bound to process.env.HOME, got %v", err)
	}
	// A user type sharing a library type's last segment stays distinct.
	assertOutputImports(t, `
interface Platform { x: number }
const q: Platform = { x: 1 }
const p: NodeJS.Platform = process.platform
console.log(q.x, typeof p)
`, "1 string")
}

// Node's timers, `os` and the synchronous `fs` functions are declared in
// @types/node's shapes: a timer handle is a NodeJS.Timeout, and a misuse
// tsc reports is rejected.
func TestE2ENodeModulesDeclared(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
import { cpus, EOL } from 'node:os'
import fs from 'fs'
let timer: NodeJS.Timeout | undefined
timer = setTimeout(() => console.log("never"), 50)
if (timer !== undefined) clearTimeout(timer)
const im: NodeJS.Immediate = setImmediate(() => console.log("immediate"))
const u = os.userInfo()
const e: "BE" | "LE" = os.endianness()
const st = fs.statSync(".")
const names: string[] = fs.readdirSync(".")
const tmp = os.tmpdir() + "/klm_node_decl_" + process.pid + ".txt"
fs.writeFileSync(tmp, "ab")
const text: string = fs.readFileSync(tmp, "utf8") + fs.readFileSync(tmp, { encoding: "utf8" })
fs.unlinkSync(tmp)
console.log(text === "abab")
console.log(cpus().length > 0, JSON.stringify(EOL), typeof u.username, e.length, st.isDirectory(), names.length > 0, fs.existsSync("."))
`, "true\ntrue \"\\n\" string 2 true true true\nimmediate")
	for _, c := range []struct{ src, want string }{
		{"let n: number = setTimeout(() => {}, 1)", "type 'Timeout' is not assignable to type 'number'"},
		{"import os from 'os'\nos.cpus(1)", "expected 0 arguments, but got 1"},
		{"import os from 'os'\nconst n: number = os.hostname()", "type 'string' is not assignable to type 'number'"},
		{"import fs from 'fs'\nconst n: number = fs.statSync('.').isFile()", "type 'boolean' is not assignable to type 'number'"},
		{"import fs from 'fs'\nconst s: string = fs.readdirSync('.')", "is not assignable to type 'string'"},
		{"import fs from 'fs'\nconst s: string = fs.readFileSync('x')", "type 'Buffer<ArrayBuffer>' is not assignable to type 'string'"},
		{"import fs from 'fs'\nconst s = fs.readFileSync('x', { encoding: 'nope' })", "no overload matches this call"},
	} {
		_, err := parseAndCompileImports(t, c.src+"\n")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: expected %q, got %v", c.src, c.want, err)
		}
	}
}

// A closure reads a captured binding as declared at its own start and
// narrows it along its own flow, as tsc does; it no longer reports a
// narrowed read as possibly null.
func TestE2EClosureNarrowsCapturedBinding(t *testing.T) {
	assertOutput(t, `
let b: number | null = 3
const bump = () => { b = b === null ? 1 : b + 1 }
function show(): string { if (b !== null) { const n: number = b; return "n" + n } return "null" }
bump(); bump()
console.log(b, show())
b = null
console.log(show())
`, "5 n5\nnull")
	_, err := parseAndCompileImports(t, `
let b: number | null = 3
function f() { const n: number = b; return n }
b = null
console.log(f())
`)
	if err == nil || !strings.Contains(err.Error(), "is not assignable to type 'number'") {
		t.Errorf("expected TS2322 for an unnarrowed captured read, got %v", err)
	}
}

// A number or boolean where an fs function takes a path: tsc's TS2345 (or
// TS2769 for an overloaded one) in the strict lane, a PathLike being a
// string, Buffer or URL; existsSync of one answers false, as in Node.
func TestE2EFsNonStringPath(t *testing.T) {
	assertOutputImports(t, `
import fs from 'fs'
console.log(fs.existsSync(42 as any))
`, "false")
	for _, src := range []string{"fs.unlinkSync(42)", "fs.copyFileSync(1, 2)", "fs.mkdirSync(true)"} {
		_, err := parseAndCompileImports(t, "import fs from 'fs'\n"+src+"\n")
		if err == nil || !strings.Contains(err.Error(), "is not assignable to parameter of type") && !strings.Contains(err.Error(), "no overload matches this call") {
			t.Errorf("%s: expected TS2345 (TS2769 for an overloaded one), got %v", src, err)
		}
	}
}

// A generic arrow or function expression (`<T>(x: T) => x`, `<T,>`, a
// constraint, a default) parses as one, not as a `<T>` assertion on a plain
// arrow; its type parameters are erased, as a generic method's are.
func TestE2EGenericArrowFunctions(t *testing.T) {
	assertOutput(t, `
const identity = <T>(x: T): T => x
const first = <T,>(xs: T[]): T | undefined => xs[0]
const pick = <K extends string, V = number>(key: K, value: V) => key
const wrap = function <U>(value: U): U { return value }
const o = { twice<W>(w: W): W[] { return [w, w] } }
const n = <number>(3 as unknown)
console.log(identity(42), identity("hello"), first([7, 8]), pick("name", 1), wrap(true), o.twice(1).length, n + 1)
`, "42 hello 7 name true 2 4")
}

// A Node class a builtin module exports (`import { IncomingMessage } from
// 'http'`) names its type and its value, plain or aliased. An unknown type
// name is TS2304.
func TestE2ENodeTypeImportsAndTypeNames(t *testing.T) {
	assertOutputImports(t, `
import http from 'http'
import { IncomingMessage as IM, ServerResponse } from 'node:http'
function handle(req: IM, res: http.ServerResponse): void { res.end("hi " + req.url) }
const server = http.createServer(handle)
server.listen(0, () => { console.log("up"); server.close() })
`, "up")
	assertOutputImports(t, "import { IncomingMessage } from 'http'\nconsole.log(typeof IncomingMessage)\n", "function")
	for _, c := range []struct{ src, want string }{
		{"const x: Frobnicator = 1", "cannot find name 'Frobnicator'"},
		{"function f(p: Array<Nonexistent>) { return p }", "cannot find name 'Nonexistent'"},
	} {
		_, err := parseAndCompileImports(t, c.src+"\n")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: expected %q, got %v", c.src, c.want, err)
		}
	}
}

// An object of another layout passed as a union's object member is copied
// into the member's layout (absent fields undefined, concrete fields boxed
// into union fields).
func TestE2EObjectIntoUnionObjectMember(t *testing.T) {
	assertOutput(t, `
interface Opts { a?: string | number; b?: number; c?: string[]; e?: string; }
function g(o: Opts | ((x: number) => void)): void {
  if (typeof o === 'function') { o(1); return; }
  console.log(o.a, o.b, o.c, o.e)
}
const v = { e: "E", a: "A" }
g(v)
g((x: number) => console.log("fn", x))
`, "A undefined undefined E\nfn 1")
}

// A caught value a closure captures keeps its value.
func TestE2ECaughtValueCapturedByClosure(t *testing.T) {
	assertOutput(t, `
try { throw new Error("x") } catch (e) { setTimeout(() => console.log((e as Error).message), 0) }
const later = [1].map((e) => e + 1)
console.log(later[0])
`, "2\nx")
}

// An unannotated class field initialised by a function call has the
// function's return type.
func TestE2EClassFieldFromFunctionCall(t *testing.T) {
	assertOutput(t, `
class Foo { x = 1 }
function mk(): Foo { return new Foo() }
class Holder { f = mk() }
console.log(new Holder().f.x)
`, "1")
}

// A top-level const whose initializer is a builtin call or a Map with
// entries is visible to a function and a method.
func TestE2ETopLevelConstReadInFunctions(t *testing.T) {
	assertOutput(t, `
const crlf = Buffer.from('\r\n')
const m = new Map<string, number>([["a", 1]])
const joined = ["a", "b"].join("-")
class K { f(): number { return crlf.length + m.get("a")! } }
function g(): string { return joined + crlf.length }
console.log(new K().f(), g())
`, "3 a-b2")
}

// A binding initialised by an assertion to a union (`"5" as string |
// number`) holds the union, as tsc types it: it compares against a number
// and takes one later.
func TestE2EAsUnionBindingHoldsUnion(t *testing.T) {
	assertOutput(t, `
let a: string | number = 5
let c = "5" as string | number
console.log(typeof c, a === c)
c = 5
console.log(typeof c, a === c)
`, "string false\nnumber true")
}

// NodeJS.Dict<T>, an interface extending it, and Object.create(null) as a
// dictionary (rendered with its null prototype, as Node does).
func TestE2ENodeJSDictAndNullProtoDict(t *testing.T) {
	assertOutput(t, `
interface I extends NodeJS.Dict<string | string[]> {}
const x: I = { a: "1" }
console.log(Object.keys(x), x.a, x["b"])
const y: NodeJS.Dict<number> = { n: 1 }
console.log(y)
const d: { [k: string]: string } = Object.create(null)
d.b = "2"
console.log(d, d["b"])
`, "[ 'a' ] 1 undefined\n{ n: 1 }\n[Object: null prototype] { b: '2' } 2")
}

// A dictionary with union values: an object and an object literal (with an
// array value) passed as one; Array.isArray narrows to the array member.
func TestE2EDictUnionValuesAndArrayNarrowing(t *testing.T) {
	assertOutput(t, `
interface D extends NodeJS.Dict<string | number | ReadonlyArray<string | number> | null> {}
function f(o: D): void {
  for (const k of Object.keys(o)) {
    const v = o[k]
    if (Array.isArray(v)) console.log(k, v.length, v[0]); else console.log(k, v)
  }
}
f({ a: [1, "x"], b: "s", c: null })
const r = { x: 5, s: "t" }
f(r)
`, "a 2 1\nb s\nc null\nx 5\ns t")
}

// A union of string literals with a function member keeps the function
// (`'dir' | 'file' | null | undefined | CB`); one of literals only is a
// string.
func TestE2EUnionOfLiteralsAndFunction(t *testing.T) {
	assertOutput(t, `
type CB = (err: Error | null) => void
function f(x: string, a: 'dir' | 'file' | null | undefined | CB, b?: CB): void {
  const cb = typeof a === 'function' ? a : b!
  console.log(typeof a)
  cb(null)
}
f("x", (e) => console.log("called", e))
f("x", 'dir', (e) => console.log("called2", e))
let d: 'north' | 'south' = 'north'
d = 'south'
console.log(d.length, typeof d)
`, "function\ncalled null\nstring\ncalled2 null\n5 string")
}

// The code-generated Node and web objects are named by @types/node's names.
func TestE2ENodeObjectTypeNames(t *testing.T) {
	assertOutputImports(t, `
import fs from 'fs'
import { Stats, Dirent } from 'fs'
import crypto from 'crypto'
const s: fs.Stats = fs.statSync('.')
const s2: Stats = fs.statSync('.')
const d: Dirent[] = fs.readdirSync('.', { withFileTypes: true })
const h: crypto.Hash = crypto.createHash('sha1')
const dv: DataView = new DataView(new ArrayBuffer(4))
const te: TextEncoder = new TextEncoder()
console.log(s.isDirectory(), s2.isDirectory(), d.length > 0, h.update('x').digest('hex').length, dv.byteLength, te.encode('a').length)
`, "true true true 40 4 1")
}

// A method returning the polymorphic `this` returns its receiver's class: an
// inherited `listen(…)` on an http.Server is an http.Server; a subclass's
// own chain keeps its members.
func TestE2EPolymorphicThisReturn(t *testing.T) {
	assertOutputImports(t, `
import http from 'http'
class Base { n = 0; bump(): this { this.n++; return this } }
class Sub extends Base { label = "sub"; tag(): string { return this.label + this.n } }
console.log(new Sub().bump().bump().tag())
const server = http.createServer((req, res) => { res.end('ok') }).listen(0, () => {
  server.keepAliveTimeout = 1234
  console.log(server.keepAliveTimeout, server instanceof http.Server)
  server.close()
})
`, "sub2\n1234 true")
}

func TestE2EVoidInUnionIsUndefined(t *testing.T) {
	// ` + "`void`" + ` beside other union members is undefined as a value.
	assertOutputImports(t, `
function pick(b: boolean): string | undefined | void { if (b) return "x" }
const a = pick(true), c = pick(false)
console.log(a, c, c === undefined)
`, "x undefined true")
}
