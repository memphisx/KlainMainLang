package tests

import (
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// assertCodegenError asserts that codegen rejects the source with a message
// containing wantSubstr.
func assertCodegenError(t *testing.T, src, wantSubstr string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := llvm.NewEmitter().EmitProgram(prog); err == nil {
		t.Fatalf("expected codegen error containing %q, got success", wantSubstr)
	} else if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("expected error containing %q, got: %v", wantSubstr, err)
	}
}

// --- D1 Stages 6+7 (TDD-00155): `class X extends Error`, Proxy trap
// dispatch, and Reflect statics. Semantics are module-strict JS (Node ESM),
// verified against `node` on .mjs sources. ---

func TestE2EExtendsErrorBasics(t *testing.T) {
	assertOutput(t, `
class HttpError extends Error {
  status: number;
  constructor(status: number, msg: string) {
    super(msg);
    this.status = status;
  }
}
const h = new HttpError(404, "not found");
console.log(h.message);
console.log(h.name);
console.log(h.status);
console.log(h instanceof Error);
console.log(h instanceof HttpError);
`, "not found\nError\n404\ntrue\ntrue")
}

func TestE2EExtendsErrorNoConstructor(t *testing.T) {
	assertOutput(t, `
class PlainError extends Error {}
const p = new PlainError("boom");
console.log(p.message, p.name);
const q = new PlainError();
console.log(q.message === "");
console.log(p instanceof Error, p instanceof PlainError);
`, "boom Error\ntrue\ntrue true")
}

func TestE2EExtendsErrorThrowCatchInstanceof(t *testing.T) {
	assertOutput(t, `
class AError extends Error {}
class BError extends Error {}
try {
  throw new AError("from A");
} catch (e) {
  console.log(e instanceof AError, e instanceof BError, e instanceof Error);
  console.log((e as Error).message);
}
`, "true false true\nfrom A")
}

func TestE2EExtendsErrorNameOverrideAndToString(t *testing.T) {
	assertOutput(t, `
class ValidationError extends Error {
  field: string;
  constructor(field: string) {
    super("invalid " + field);
    this.name = "ValidationError";
    this.field = field;
  }
  describe(): string {
    return this.name + " on " + this.field;
  }
}
const v = new ValidationError("email");
console.log(v.name);
console.log(v.describe());
console.log(`+"`${v}`"+`);
const bare = new ValidationError("x");
bare.message = "";
console.log(`+"`${bare}`"+`);
`, "ValidationError\nValidationError on email\nValidationError: invalid email\nValidationError")
}

func TestE2EProxyGetSetTraps(t *testing.T) {
	assertOutputCompatJS(t, `
const target = { a: 1 }
const p = new Proxy(target, {
  get(t, key) { return key === "magic" ? 99 : t[key] },
  set(t, key, value) { t[key] = value * 2; return true }
})
console.log(p.a, p.magic)
p.b = 5
console.log(target.b, p.b)
`, "1 99\n10 10")
}

func TestE2EProxyForwardNoTraps(t *testing.T) {
	assertOutputCompatJS(t, `
const target = { x: 7 }
const p = new Proxy(target, {})
console.log(p.x)
p.y = 3
console.log(target.y)
console.log("x" in p, "z" in p)
delete p.x
console.log(target.x)
console.log(JSON.stringify(p))
`, "7\n3\ntrue false\nundefined\n{\"y\":3}")
}

func TestE2EProxyArrayTarget(t *testing.T) {
	assertOutputCompatJS(t, `
const p = new Proxy([7, 8], {})
console.log(p.length, p[0])
for (const v of p) console.log(v)
p[0] = 9
console.log(p[0], 1 in p)
const q = new Proxy([1, 2], { get(t, k) { return k === "length" ? 5 : "x" } })
console.log(q.length, q[0])
try { new Proxy(1, {}) } catch (e) { console.log(e.message) }
console.log(JSON.stringify(p), p)
`, "2 7\n7\n8\n9 true\n5 x\nCannot create proxy with a non-object as target or handler\n[9,8] [ 9, 8 ]")
}

func TestE2EProxyHasDeleteTraps(t *testing.T) {
	assertOutputCompatJS(t, `
const p = new Proxy({ a: 1, secret: 2 }, {
  has(t, key) { return key !== "secret" && key in t },
  deleteProperty(t, key) { console.log("deleting", key); delete t[key]; return true }
})
console.log("a" in p, "secret" in p)
delete p.a
console.log("a" in p)
`, "true false\ndeleting a\nfalse")
}

func TestE2EReflectStatics(t *testing.T) {
	assertOutputCompatJS(t, `
const o = { a: 1 }
console.log(Reflect.get(o, "a"))
console.log(Reflect.set(o, "b", 2))
console.log(o.b)
console.log(Reflect.has(o, "a"), Reflect.has(o, "z"))
console.log(Reflect.ownKeys(o))
console.log(Reflect.deleteProperty(o, "a"))
console.log(Reflect.has(o, "a"))
Reflect.defineProperty(o, "c", { value: 3, enumerable: true })
console.log(o.c)
console.log(Reflect.isExtensible(o))
Reflect.preventExtensions(o)
console.log(Reflect.isExtensible(o))
`, "1\ntrue\n2\ntrue false\n[ 'a', 'b' ]\ntrue\nfalse\n3\ntrue\nfalse")
}

// Reflect.get/has on an any-typed target works under strict — the target is a
// real dynamic bag, so no widening is needed.
func TestE2EReflectGetHasOnAnyTargetStrict(t *testing.T) {
	assertOutput(t, `
const o: any = { p: 42 }
console.log(Reflect.has(o, "p"), Reflect.has(o, "z"))
console.log(Reflect.get(o, "p"))
`, "true false\n42")
}

// Reflect.get/has on a statically-typed object read its properties as
// `o[k]` and `k in o` do (its prototype's keys included).
func TestE2EReflectHasGetOnStaticObject(t *testing.T) {
	src := `
class P { x = 1; m() { return 1; } }
const p = new P();
const o = { a: 1, p: 42 };
console.log(Reflect.has(p, "x"), Reflect.has(p, "y"), Reflect.has(p, "m"), Reflect.has(o, "a"), Reflect.has(o, "toString"), Reflect.has([1], 0), Reflect.has([1], "length"));
console.log(Reflect.get(o, "p"), Reflect.get(p, "x"));
`
	assertSameAsNode(t, src)
	assertSameAsNodeCompatJS(t, src)
}

func TestE2EReflectPrototypes(t *testing.T) {
	assertOutputCompatJS(t, `
const proto = { greet() { return "hi" } }
const o: any = {}
console.log(Reflect.setPrototypeOf(o, proto))
console.log(Reflect.getPrototypeOf(o) === proto)
console.log(o.greet())
`, "true\ntrue\nhi")
}

// Object.getOwnPropertySymbols throws on null/undefined and runs a Proxy's
// ownKeys trap with the spec's result checks.
func TestE2EGetOwnPropertySymbolsProxyOwnKeys(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
const s = Symbol("s");
try { Object.getOwnPropertySymbols(null); } catch (e) { console.log(e instanceof TypeError, e.message); }
try { Object.getOwnPropertySymbols(undefined); } catch (e) { console.log(e instanceof TypeError); }
const dup = new Proxy({}, { ownKeys() { return ["a", "a"]; } });
try { Object.getOwnPropertySymbols(dup); } catch (e) { console.log(e instanceof TypeError, e.message); }
const q = new Proxy({}, { ownKeys() { return ["a", s]; } });
console.log(Object.getOwnPropertySymbols(q).length, Object.getOwnPropertySymbols(q)[0] === s);
const t = {};
Object.defineProperty(t, "x", { value: 1, configurable: false });
try { Object.getOwnPropertySymbols(new Proxy(t, { ownKeys() { return []; } })); } catch (e) { console.log(e instanceof TypeError); }
const ne = Object.preventExtensions({ y: 1 });
try { Object.getOwnPropertySymbols(new Proxy(ne, { ownKeys() { return ["y", "z"]; } })); } catch (e) { console.log(e instanceof TypeError); }
console.log(Object.getOwnPropertySymbols(new Proxy(ne, { ownKeys() { return ["y"]; } })).length);
`)
}

// A Proxy's [[OwnPropertyKeys]] and [[GetOwnProperty]]: its ownKeys and
// getOwnPropertyDescriptor traps, else its target's, behind Object.keys,
// getOwnPropertyNames, getOwnPropertyDescriptor, Reflect.ownKeys and object
// rest, each trap run in the spec's order (ADR-01814).
func TestE2EProxyOwnKeysAndDescriptors(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var log = [];
var s = Symbol("s");
var p = new Proxy({}, {
  getOwnPropertyDescriptor(_t, k) { log.push("gopd:" + String(k)); return k === "foo" ? { value: 1, enumerable: true, configurable: true } : undefined; },
  ownKeys() { log.push("ownKeys"); return [s, "foo", "0"]; },
  get(_t, k) { log.push("get:" + String(k)); return 7; },
});
var rk = Reflect.ownKeys(p); console.log(rk.length, rk[0] === s, rk[1], log.join()); log = [];
console.log(JSON.stringify(Object.keys(p)), log.join()); log = [];
console.log(JSON.stringify(Object.getOwnPropertyNames(p)), log.join()); log = [];
var d = Object.getOwnPropertyDescriptor(p, "foo");
console.log(d.value, d.writable, d.enumerable, d.configurable, log.join()); log = [];
let { ...r } = p; console.log(JSON.stringify(r), log.join()); log = [];
let { foo, ...r2 } = p; console.log(foo, JSON.stringify(r2), log.join()); log = [];
var fw = new Proxy({ a: 1, b: 2 }, {});
console.log(JSON.stringify(Object.keys(fw)), Reflect.ownKeys(fw).length, Object.getOwnPropertyDescriptor(fw, "b").value);
let { a, ...rest } = fw; console.log(a, JSON.stringify(rest));
var nested = new Proxy(new Proxy({ z: 3 }, {}), {});
console.log(JSON.stringify(Object.keys(nested)));
var bad = new Proxy({}, { getOwnPropertyDescriptor() { return 1; } });
try { Object.getOwnPropertyDescriptor(bad, "q"); } catch (e) { console.log(e instanceof TypeError, e.message); }
var dup = new Proxy({}, { ownKeys() { return ["a", "a"]; } });
try { Object.keys(dup); } catch (e) { console.log(e instanceof TypeError, e.message); }
console.log(JSON.stringify(p), JSON.stringify(new Proxy({ a: 1, b: 2 }, { get(t, k) { return k.length; } })));
console.log(Object.keys(Object.getOwnPropertyDescriptor({ a: 1 }, "a")).map(k => k.length).join());
`)
}

// A field named "constructor" (identifier or string) and `arguments` in a
// field initializer are early SyntaxErrors; `arguments` inside a nested
// function is not.
func TestClassFieldEarlyErrors(t *testing.T) {
	for src, want := range map[string]string{
		`class C { constructor = 1 }`:          "field named 'constructor'",
		`class C { "constructor" = 1 }`:        "field named 'constructor'",
		`class C { static "constructor" = 1 }`: "field named 'constructor'",
		`class C { 'x' = () => arguments }`:    "'arguments' cannot be referenced",
	} {
		if _, err := parser.Parse(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want error %q, got %v", src, want, err)
		}
	}
	if _, err := parser.Parse(`class C { x = function () { return arguments.length } }`); err != nil {
		t.Errorf("arguments in a nested function: %v", err)
	}
}
