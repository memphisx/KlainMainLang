package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/options"
	"KlainMainLang/resolver"
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
	// `let`: a `const base = 42` would make `typeof base` the literal 42
	// (TS2322 on 100), as in tsc.
	assertOutput(t, `
let base = 42;
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
`, "true "+nodeJoin("a", "b")+" 8080 function object")
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
`, nodeJoin("a", "b")+" y /a/b .txt f")
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
`, "true\ntrue "+nodeEOLJSON()+" string 2 true true true\nimmediate")
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

// ADR-01318: a `typeof` query names the value in its own scope — a local
// shadowing a top-level binding — and an unknown name is TS2304.
func TestE2ETypeofLocalShadowAndUnknownName(t *testing.T) {
	assertSameAsNode(t, `
const v = { a: 1 };
function f() { let v = "s"; let w: typeof v = "t"; return w; }
console.log(f(), v.a);
`)
	_, err := parseAndCompile(`let z: typeof nope = 1;`)
	if err == nil || !strings.Contains(err.Error(), "cannot find name 'nope'") {
		t.Errorf("want TS2304 for typeof of an unknown name, got %v", err)
	}
}

// A generic call whose argument alone answers a constrained type parameter
// is checked against the constraint, as tsc does (TS2345, TS2353 for an
// object literal); a call that satisfies it compiles (BACKLOG 40).
func TestGenericArgumentCheckedAgainstConstraint(t *testing.T) {
	for _, src := range []string{
		"const xs: number[] = [1, 2]; crypto.getRandomValues(xs);",
		"function g<T extends { id: number }>(x: T): number { return x.id; } g({ name: 'z' });",
		"function f<T extends string>(x: T): T { return x; } f(1 as number);",
	} {
		if _, err := parseAndCompile(src); err == nil {
			t.Errorf("expected a type error for %q", src)
		}
	}
	if _, err := parseAndCompile("function f<T extends string>(x: T): T { return x; } function g<T extends { id: number }>(x: T): number { return x.id; } console.log(f('a'), g({ id: 1, name: 'z' }), crypto.getRandomValues(new Uint8Array(2)).length);"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// A function type's parameter with a name but no type is TS7051 (a type
// name) or TS7006, and an override not assignable to the base member is
// TS2416, as tsc reports them; valid overrides compile (BACKLOG 49).
func TestSignatureParamTypesAndOverrides(t *testing.T) {
	for src, want := range map[string]string{
		"type F = (string) => void; const f: F = (x: string) => {};":                                              "did you mean 'arg0: string'",
		"type G = (x) => void; const g: G = () => {};":                                                            "implicitly has an 'any' type",
		"class A { m(x: number): number { return x; } } class B extends A { m(x: string): number { return 1; } }": "not assignable to the same property in base type",
	} {
		_, err := parseAndCompile(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", src, want, err)
		}
	}
	if _, err := parseAndCompile("class Animal { speak(): string { return 'x'; } clone(): Animal { return new Animal(); } } class Dog extends Animal { speak(): string { return 'w'; } clone(): Dog { return new Dog(); } } console.log(new Dog().speak());"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// A value with no type meaning used as a type is TS2749 (BACKLOG 34).
func TestValueUsedAsTypeIsTS2749(t *testing.T) {
	_, err := parseAndCompile("const C = class { x = 1; }; const c: C = new C(); console.log(c);")
	if err == nil || !strings.Contains(err.Error(), "refers to a value, but is being used as a type") {
		t.Fatalf("want TS2749, got %v", err)
	}
	if _, err := parseAndCompile("class D { x = 1; } enum E { A } const d: D = new D(); const e: E = E.A; console.log(d, e);"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A misspelled type name near a known one is TS2552 with tsc's suggestion
// (BACKLOG 33).
func TestNearMissTypeNameIsTS2552(t *testing.T) {
	_, err := parseAndCompile("interface Point { x: number } const p: Pont = { x: 1 }; console.log(p);")
	if err == nil || !strings.Contains(err.Error(), "did you mean 'Point'") {
		t.Fatalf("want TS2552, got %v", err)
	}
}

// InstanceType<typeof C> of a class expression's binding is its instances'
// type (ADR-01350).
func TestE2EInstanceTypeOfClassExpression(t *testing.T) {
	assertSameAsNode(t, `
const Money = class Currency {
  cents: number;
  constructor(cents: number) { this.cents = cents; }
};
function dollars(m: InstanceType<typeof Money>): number { return m.cents / 100; }
class Plain { v = 3; }
function v(p: InstanceType<typeof Plain>): number { return p.v; }
console.log(dollars(new Money(500)), v(new Plain()));
`)
}

// `new` of an indexed or member callee (BACKLOG 41).
func TestE2ENewIndexedCallee(t *testing.T) {
	assertSameAsNode(t, `
class A { v = 1; }
class B { v = 2; }
const classes = [A, B];
const ns = { K: A };
console.log(new classes[1]().v, new ns.K().v, new (classes[0])().v);
`)
}

// A top-level stream reader is visible in a named function (BACKLOG 42).
func TestE2ETopLevelReaderInFunction(t *testing.T) {
	assertSameAsNode(t, `
const rs = new ReadableStream<string>({ start(c) { c.enqueue('a'); c.enqueue('b'); c.close(); } });
const r = rs.getReader();
async function drain(): Promise<void> {
  for (;;) { const { done, value } = await r.read(); if (done) break; console.log(value); }
}
drain();
`)
}

// A type name is resolved in its scope: a block's or function's type used
// outside it, and a namespace's type used unqualified outside it, are
// TS2304 (BACKLOG 32, 37).
func TestOutOfScopeTypeNameIsTS2304(t *testing.T) {
	for _, src := range []string{
		"function f() { type Local = { a: number }; const l: Local = { a: 1 }; return l.a; } const z: Local = { a: 2 }; console.log(f(), z);",
		"{ interface Inner { b: string } const i: Inner = { b: 'x' }; console.log(i.b); } const w: Inner = { b: 'y' }; console.log(w);",
		"namespace N { export type Kind = 'a' | 'b'; } const k: Kind = 'a'; console.log(k);",
	} {
		_, err := parseAndCompile(src)
		if err == nil || !strings.Contains(err.Error(), "cannot find name") {
			t.Errorf("%q: want TS2304, got %v", src, err)
		}
	}
	if _, err := parseAndCompile("namespace N { export type Kind = 'a' | 'b'; export function f(k: Kind): Kind { return k; } export namespace In { export type Deep = number; export const d: Deep = 1; } } const q: N.Kind = N.f('b'); const z: N.In.Deep = N.In.d; console.log(q, z);"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// A member a builtin module or class does not declare is TS2339
// (BACKLOG 47).
func TestUnknownBuiltinMemberIsTS2339(t *testing.T) {
	for src, want := range map[string]string{
		"import * as fs from 'fs'; fs.nopeSync();":                                       `does not exist on type 'typeof import("fs")'`,
		"import { EventEmitter } from 'events'; const e = new EventEmitter(); e.nope();": "property 'nope' does not exist",
	} {
		_, err := parseAndCompileImports(t, src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", src, want, err)
		}
	}
}

// `new X(…)` through a value's construct signatures checks its arguments,
// and an optional parameter's target is named without its undefined, at the
// argument's start, as tsc reports them (ADR-01355).
func TestConstructSignatureArgumentsChecked(t *testing.T) {
	for src, want := range map[string]string{
		`const dv = new DataView(new ArrayBuffer(8), Symbol("x")); console.log(dv);`: `1:45: argument of type 'symbol' is not assignable to parameter of type 'number'`,
		`function f(x?: number) { return x; } f(Symbol("x"));`:                       `1:40: argument of type 'symbol' is not assignable to parameter of type 'number'`,
		`function k(x?: string | number) { return x; } k(Symbol("d"));`:              `parameter of type 'string | number | undefined'`,
	} {
		_, err := parseAndCompile(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", src, want, err)
		}
	}
}

// A statement after an unconditional throw compiles to valid IR, and the
// JS lane throws Node's TypeError (ADR-01356).
func TestE2EStatementAfterCompileTimeThrow(t *testing.T) {
	bin := buildBinaryCompatJS(t, `const dv = new DataView(new ArrayBuffer(8), Symbol("x"));
console.log(dv);
`)
	out, _ := exec.Command(bin).CombinedOutput()
	if !strings.Contains(string(out), "Cannot convert a Symbol value to a number") {
		t.Fatalf("want the TypeError, got %q", out)
	}
}

// A class value is related through its construct signature: `typeof B` does
// not fit `typeof A` or a constructor type whose parameters it rejects; the
// types print as tsc prints them, at the declared name (ADR-01358).
func TestConstructSignatureAssignability(t *testing.T) {
	for src, want := range map[string]string{
		"class A { constructor(n?: string) {} } class B extends A { constructor(x: number) { super(); } }\nlet K: typeof A = B;":                "2:5: type 'typeof B' is not assignable to type 'typeof A'",
		"class A { constructor(n?: string) {} } class B extends A { constructor(x: number) { super(); } }\nconst f: new (n?: string) => A = B;": "2:7: type 'typeof B' is not assignable to type 'new (n?: string | undefined) => A'",
	} {
		_, err := parseAndCompile(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", src, want, err)
		}
	}
	if _, err := parseAndCompile("class A { constructor(n?: string) {} } class C extends A { constructor(s: string) { super(s); } }\nlet ok: typeof A = C; const g: new (n: string) => A = C; console.log(ok, g);"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// A constructor type infers its instance type from a class value or a
	// builtin constructor.
	assertSameAsNode(t, `
class Shape { name = "shape"; }
function make<T>(ctor: new () => T): T { return new ctor(); }
console.log(make(Shape).name, make(Map).size);
`)
}

// A typed promise viewed under another type (`Promise<unknown>`, `any`) is
// the same object: it settles in the same microtask as the source
// (ADR-01363).
func TestE2EPromiseViewSettlesWithSource(t *testing.T) {
	assertSameAsNode(t, `
async function hops(p: Promise<unknown>): Promise<number> { let n = 0, done = false; p.then(() => { done = true; }); while (!done) { await null; n++; if (n > 50) break; } return n; }
async function main(): Promise<void> {
  console.log(await hops(Promise.resolve(1)));
  console.log(await hops(Promise.resolve(1).then((v) => v)));
  console.log(await hops(Promise.resolve(1).then((v) => v).then((v) => v)));
  const a: any = Promise.resolve(2).then((v) => v);
  console.log(await hops(a));
  const r = Promise.reject(new Error('x')).then(() => 1);
  const rv: Promise<unknown> = r;
  rv.catch((e) => console.log('caught', (e as Error).message));
}
main();
`)
}

// JSON.stringify escapes an embedded NUL as \u0000 (ADR-01364).
func TestE2EJSONStringifyEmbeddedNul(t *testing.T) {
	assertSameAsNode(t, `
const s = "a\u0000b";
const o: any = { s, n: [s] };
console.log(s.length, JSON.stringify(s), JSON.stringify({ s }), JSON.stringify(o));
`)
}

// An assigned Error `cause` is an own enumerable property, the constructor's
// option is not, and defineProperty's `enumerable` (default false for a new
// property, kept for an existing one) decides it (ADR-01369).
func TestE2EErrorCauseEnumerability(t *testing.T) {
	assertSameAsNode(t, `
const e = new Error('x');
(e as any).cause = 5;
console.log(Object.keys(e), JSON.stringify(e));
Object.defineProperty(e, 'cause', { enumerable: false, value: 9 });
console.log(Object.keys(e), (e as any).cause, JSON.stringify(e));
const f = new Error('y', { cause: 1 });
Object.defineProperty(f, 'cause', { value: 3 });
console.log(Object.keys(f), (f as any).cause);
Object.defineProperty(f, 'cause', { value: 4, enumerable: true });
console.log(Object.keys(f), (f as any).cause);
const g = new Error('z');
Object.defineProperty(g, 'cause', { value: 7 });
console.log(Object.keys(g), (g as any).cause);
(g as any).extraKey = 1;
Object.defineProperty(g, 'hidden', { value: 2 });
console.log(Object.keys(g), (g as any).hidden);
`)
}

// Under -compat=js a Date variable compound-assigned with arithmetic takes
// the result (`d += 1` a string, `d -= 5` a number), as in JS (ADR-01370).
func TestE2ECompatJSDateCompoundAssignWidens(t *testing.T) {
	assertOutputCompatJS(t, `
let d = new Date(0);
d += 1;
console.log(typeof d, String(d).endsWith("1"));
let e = new Date(0);
e -= 5;
console.log(typeof e, e);
let f = new Date(0);
f = new Date(5);
console.log(typeof f, f.getTime());
`, "string true\nnumber -5\nobject 5")
}

// Under -compat=js a function declared in a block also binds a var of its
// name in the enclosing function (Annex B.3.3), undefined until the block
// runs (ADR-01371).
func TestE2ECompatJSAnnexBBlockFunctions(t *testing.T) {
	// The rewrite is the resolver's: built through the CLI.
	cli := buildCLI(t)
	dir := tempDir(t)
	srcFile, binFile := filepath.Join(dir, "prog.ts"), filepath.Join(dir, "prog")
	if err := os.WriteFile(srcFile, []byte(`
console.log(typeof f);
{ function f() { return 1; } }
console.log(typeof f, f());
if (true) { function g() { return 2; } }
console.log(g());
function outer() {
  console.log(typeof h);
  for (let i = 0; i < 1; i++) { function h() { return 3; } }
  return h();
}
console.log(outer());
switch (1) { case 1: function s() { return 's'; } }
console.log(s());
function w() { let x = null; x = function () { return 4; }; return x(); }
console.log(w());
`), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cli, "-compat=js", "-o", binFile, srcFile).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "undefined\nfunction 1\n2\nundefined\n3\ns\n4")
}

// Under -compat=js a class method's number parameter takes ToNumber of a
// string, array or object argument, as JavaScript converts it (ADR-01372).
func TestE2ECompatJSClassCallToNumber(t *testing.T) {
	assertOutputCompatJS(t, `
var sample = new DataView(new ArrayBuffer(8), 0);
sample.setUint8(1, 7);
console.log(sample.getUint8("1"), sample.getUint8([1]), sample.getUint8(["1"]), sample.getUint8([]), sample.getUint8(null), sample.getUint8(true));
`, "7 7 7 0 0 7")
}

// A class method's boolean parameter takes ToBoolean of whatever is passed
// (a string, an object), and a number parameter ToNumber through the
// object's own valueOf/toString.
func TestE2ECompatJSClassCallToBoolean(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var buffer = new ArrayBuffer(4);
var sample = new DataView(buffer, 0);
sample.setInt8(0, 39);
sample.setInt8(1, 42);
console.log(sample.getInt16(0, "s"), sample.getInt16(0, ""), sample.getInt16(0, {}), sample.getInt16(0, 0), sample.getInt16(0, null));
var one = { valueOf: function () { return 1; } };
var two = { toString: function () { return "1"; } };
console.log(sample.getInt8(one), sample.getInt8(two), sample.getInt8([1]));
`)
}

// ToNumber of an object held in any runs ToPrimitive at every site, not
// only at an operator: Number(), a Math function's argument.
func TestE2EAnyToNumberToPrimitive(t *testing.T) {
	assertSameAsNode(t, `
const o: any = { valueOf() { return 4; } };
const s: any = { toString() { return "5"; } };
const arr: any = [6];
const plain: any = {};
console.log(o * 1, Number(o), Math.max(o, s), Math.abs(arr), Math.abs(plain));
`)
}

// An Array, Map or Set iterator held in any: next()'s result reads its
// value and done, and an absent optional method reads undefined.
func TestE2EAnyCollectionIteratorResult(t *testing.T) {
	assertSameAsNode(t, `
const it: any = [1, 2].values();
const r = it.next();
console.log(r.done, r.value, typeof it.return, typeof it.throw);
const m: any = new Map([[1, "a"]]).keys();
const q = m.next();
console.log(q.done, q.value, m.next().done);
const st: any = new Set(["x"]).values();
console.log(st.next().value);
const prices = new Map([["tea", 3], ["cake", 5]]);
const vs: any = prices.values();
const ks: any = prices.keys();
console.log(vs.next().value, ks.next().value, vs, ks);
`)
}

// An unannotated method may call a sibling declared after it: its result is
// that sibling's, through a chain of such calls, for instance, private and
// static methods alike.
func TestE2EMethodCallsLaterSibling(t *testing.T) {
	assertSameAsNode(t, `
class B {
  a() { return this.b(); }
  b() { return this.c() + "!"; }
  c() { return "x"; }
  n() { return this.#p() * 2; }
  #p() { return 21; }
  static s() { return B.t(); }
  static t() { return [1, 2]; }
}
const b = new B();
console.log(b.a(), b.n(), B.s());
const E = class { method() { return this.#m(); } #m() { return "test262"; } };
console.log(new E().method());
`)
}

// A destructuring assignment's value is its right-hand side; it reads a
// dynamic source and computed keys; a null-initialized binding it assigns
// takes the value; a declaration initialized by an assignment takes its
// value.
func TestE2EDestructuringAssignmentValue(t *testing.T) {
	src := `
var x = null; var result; var vals = { x: 2 };
result = { x, } = vals;
console.log(x, result === vals);
var a, b, c, d, k = "kk";
var o = { a: 1, b: { c: 3 }, kk: "K" };
({ a, b: { c }, d = 9, [k]: b } = o);
console.log(a, c, d, b);
var r2 = ({ a } = { a: 5 });
console.log(a, r2.a);
var y; var z = y = "s"; console.log(z, y);
var p = null; [p] = [7]; console.log(p);
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

// indexOf / lastIndexOf / includes compare by strict equality: a search
// value of another kind than the elements matches none, never converted.
func TestE2EArraySearchOtherKind(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var o = {};
console.log([0, 1].indexOf(o), [0, o].indexOf(o), [1, 2].indexOf("1"), [1, 0].indexOf(true));
console.log([1, 2].lastIndexOf("2"), [1, 2].includes("1"), ["a", "1"].indexOf(1), [true].indexOf(1));
console.log([1.5, 2].indexOf(2), [1, 2].includes(2));
`)
}

// Where the type check leaves a call unchecked, an argument no conversion
// reaches is a type error at the strict emitter, never invalid IR: a
// method's parameter, resize's length and a concat element. The program is
// resolved without the check (as the js lane resolves) to reach the emitter.
func TestE2EStrictEmitterRejectsUnconvertibleArgs(t *testing.T) {
	cases := []string{
		`var dv = new DataView(new ArrayBuffer(4), 0); console.log(dv.getInt8(""));`,
		`var dv = new DataView(new ArrayBuffer(4), 0); console.log(dv.getInt16(0, {}));`,
		`var ab = new ArrayBuffer(0, { maxByteLength: 4 }); ab.resize({ valueOf: function () { return {}; } });`,
		`var fn = function () {}; console.log([].concat(fn).length);`,
	}
	for _, src := range cases {
		d := t.TempDir()
		f := filepath.Join(d, "main.js")
		if err := os.WriteFile(f, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
		prog, err := resolver.ResolveProgramWithOptions(f, options.Options{Compat: "js"})
		if err != nil {
			t.Fatalf("resolve %q: %v", src, err)
		}
		em := llvm.NewEmitter()
		em.SetCompatMode("strict")
		if _, err := em.EmitProgram(prog); err == nil || !strings.Contains(err.Error(), "type mismatch") {
			t.Errorf("%q: want a type-mismatch error, got %v", src, err)
		}
	}
}
