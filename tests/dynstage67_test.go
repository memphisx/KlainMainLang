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
  console.log(e.message);
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

func TestE2EExtendsErrorSubSubclassRejected(t *testing.T) {
	assertCodegenError(t, `
class AError extends Error {}
class BError extends AError {}
`, "extending an Error subclass")
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

// Reflect.get/has on a statically-typed struct under strict is a clean
// compile-time rejection, never invalid IR: strict mode does not widen a typed
// struct into a dynamic bag (ADR-00630). Previously these two forms — unlike
// set/deleteProperty, which already rejected — emitted `nb_tag(i64 <ptr>)`,
// invalid LLVM IR that only failed at the clang stage.
func TestE2EReflectHasOnStaticObjectRejected(t *testing.T) {
	_, err := parseAndCompile(`
const o = { p: 42 }
console.log(Reflect.has(o, "p"))
`)
	if err == nil {
		t.Fatal("expected a compile error: Reflect.has on a statically-typed object")
	}
}

func TestE2EReflectGetOnStaticObjectRejected(t *testing.T) {
	_, err := parseAndCompile(`
const o = { p: 42 }
console.log(Reflect.get(o, "p"))
`)
	if err == nil {
		t.Fatal("expected a compile error: Reflect.get on a statically-typed object")
	}
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
