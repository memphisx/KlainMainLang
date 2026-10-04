package tests

import "testing"

// An Error's own enumerable properties are an Error subclass's fields and
// the system fields set on it (errno, code, syscall, path, …; node:sqlite's
// code, errcode, errstr; execSync's status, …): JSON.stringify and
// Object.keys list those, never the layout's internal fields, whether the
// error is typed, caught, or held in `any`.
func TestE2EErrorOwnPropertiesJSONAndKeys(t *testing.T) {
	assertSameAsNodeImports(t, `
import fs from "fs"
console.log(JSON.stringify(new Error("boom")), JSON.stringify({ e: new TypeError("t") }))
class M extends Error { code = "X" }
console.log(JSON.stringify(new M("m")))
try { fs.readFileSync("/nope") } catch (e: any) { console.log(JSON.stringify(e)) }
const a: any = new Error("q")
console.log(JSON.stringify(a), JSON.stringify([a]))
`)
	assertSameAsNodeImports(t, `
import fs from "fs"
try { fs.readFileSync("/nope") } catch (e: any) { console.log(typeof e, e instanceof Error, e.code, Object.keys(e)); console.log(JSON.stringify(e)); const x: any = e; console.log(JSON.stringify(x)) }
try { JSON.parse("{") } catch (e: any) { console.log(JSON.stringify(e)) }
`)
	assertSameAsNodeImports(t, `
import { DatabaseSync } from "node:sqlite"
const db = new DatabaseSync(":memory:")
try { db.exec("SELEC 1") } catch (e: any) { console.log(Object.keys(e), JSON.stringify(e)) }
import { execSync } from "child_process"
try { execSync("exit 3", { stdio: "pipe" }) } catch (e: any) { console.log(Object.keys(e)) }
`)
}

// ADR-01319: an Error subclass's instance prints as an error — Node's
// header (`G [Error]` when the class is not what the name says), then its
// own properties — without the stack's frames, which this compiler has no
// equivalent of.
func TestE2EErrorSubclassInspect(t *testing.T) {
	assertOutput(t, `
class G extends Error {}
class Gx extends Error { kode = 7; constructor(m: string) { super(m); this.name = "Gx"; } }
console.log(new G("g"));
console.log(new Gx("m"));
console.log([new Gx("n")], { e: new Error("o") });
const t = new TypeError("x");
(t as any).extra1 = 5;
console.log(t);
`, "G [Error]: g\nGx: m {\n  kode: 7\n}\n[\n  Gx: n {\n    kode: 7\n  }\n] { e: Error: o }\nTypeError: x {\n  extra1: 5\n}")
}

// ADR-01319: an Error's own enumerable keys and JSON: its subclass's fields,
// an assigned name, added properties; fields named like a system error's
// (`code`, `errno`) are the subclass's own, read and written through any.
func TestE2EErrorSubclassOwnProps(t *testing.T) {
	assertSameAsNode(t, `
class Gx extends Error { code = 7; errno: string = "e"; kode = 1; constructor(m: string) { super(m); this.name = "Gx"; } }
const g = new Gx("m");
const a: any = g;
a.kode = 9;
a.code = 4;
console.log(g.code, g.errno, g.kode, a.code, a.kode, Object.keys(g), Object.keys(a));
console.log(JSON.stringify(g), JSON.stringify({ g }), JSON.stringify(new Error("p")));
const b: any = new Error("y");
b.name = "Custom";
b.k = 1;
console.log(Object.keys(b), JSON.stringify(b), String(b));
`)
}

// ADR-01319: a subclass of an Error subclass — construction through the
// chain, instanceof at every level (also on a caught value), its own fields.
func TestE2EErrorSubclassOfSubclass(t *testing.T) {
	assertSameAsNode(t, `
class AppError extends Error { code = "E_APP"; constructor(m: string) { super(m); this.name = "AppError"; } }
class NotFound extends AppError { status = 404; constructor(what: string) { super(what + " not found"); this.name = "NotFound"; } }
class Bare extends AppError {}
const n = new NotFound("user");
console.log(n instanceof NotFound, n instanceof AppError, n instanceof Error, n instanceof TypeError);
console.log(n.message, n.name, n.code, n.status, String(n), new Bare("b").name);
console.log(JSON.stringify(n));
try { throw n; } catch (e) {
  if (e instanceof AppError) console.log("caught app", e.code);
  if (e instanceof NotFound) console.log("caught nf", e.status);
}
const a: any = n;
console.log(a.status, a.code, a instanceof AppError);
Promise.allSettled([Promise.reject(n)]).then((r) => console.log(JSON.stringify(r)));
`)
}
