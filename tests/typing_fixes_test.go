package tests

import "testing"

// path.resolve of a relative path starts at the working directory; the
// working directory is a runtime string (ADR-01231).
func TestE2EPathResolveRelative(t *testing.T) {
	assertSameAsNodeImports(t, `
import path from 'path';
import { resolve } from 'path';
console.log(path.resolve("x") === process.cwd() + "/x", resolve("./a/b", "..") === process.cwd() + "/a");
console.log(process.cwd().length > 0, (process.cwd() + "!").endsWith("!"));
`)
}

// `__filename`/`__dirname` are the file's own path and directory, as a
// TypeScript program compiled to CommonJS binds them (Node's own ES-module
// loader leaves them undefined, so this is not compared against it).
func TestE2EFilenameDirname(t *testing.T) {
	assertOutputImports(t, `
import path from 'path';
console.log(path.isAbsolute(__filename), __filename.endsWith(".ts"), path.dirname(__filename) === __dirname);
`, "true true true")
}

// Optional function values, implicit-any closure parameters and String
// methods on an any (ADR-01230).
func TestE2EOptionalFunctionsAndAnyStrings(t *testing.T) {
	assertSameAsNode(t, `
function g(a?: any): void {
  let callback: ((e: Error | null) => void) | undefined = undefined;
  if (typeof a === "function") callback = a;
  console.log(typeof callback, callback === undefined);
  const cb = callback;
  const run = (f: () => void) => f();
  run(() => { if (cb !== undefined) cb(null); else console.log("no cb"); });
}
g();
g((e: Error | null) => console.log("cb", e));
function k(): void {
  const f = (): any => "s";
  const out = f();
  console.log(typeof out, out);
}
k();
const x: any = "  a-b ";
console.log(JSON.stringify(x.trim()), x.split("-"), x.replace("a", "z"), x.toUpperCase());
const n: any = null;
try { n.trim(); } catch (e) { console.log((e as Error).message); }
const u: any = undefined;
try { u.split(","); } catch (e) { console.log((e as Error).message); }
`)
}

// An untyped array unboxed into a typed array converts its elements.
func TestE2EDynamicArrayIntoTypedArray(t *testing.T) {
	assertSameAsNode(t, `
function f(a?: any): void {
  let args: readonly string[] = [];
  if (Array.isArray(a)) args = a;
  const c = args.slice();
  console.log(c.length, c.join("-"));
}
f([]);
f(["x", "y"]);
const d: any = JSON.parse('["a","b"]');
const s: string[] = d;
console.log(s.length, s[1]);
`)
}
