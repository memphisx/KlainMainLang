package tests

import (
	"strings"
	"testing"
)

// TDD-00208: an `any`/`unknown`-typed object or class field is a boxed slot
// (one NaN box, box-on-write / unbox-on-read) — the field-shaped counterpart of
// the boxed-element array (`any[]`). These exercise storage, read/write across
// value kinds, and the dynamic-value consumers (typeof, ===, JSON, spread,
// destructuring) over such a field.

func TestE2EAnyFieldReadWriteKinds(t *testing.T) {
	assertOutput(t, `
class Box { v: any = null; }
const b = new Box();
b.v = 42; console.log(b.v);
b.v = "hello"; console.log(b.v);
b.v = true; console.log(b.v);
console.log(typeof b.v);
b.v = 7;
console.log(b.v === 7);
`, "42\nhello\ntrue\nboolean\ntrue")
}

func TestE2EAnyFieldObjectLiteral(t *testing.T) {
	assertOutput(t, `
const o: { x: any } = { x: 5 };
console.log(o.x);
o.x = "str"; console.log(o.x);
console.log(JSON.stringify(o));
`, "5\nstr\n{\"x\":\"str\"}")
}

func TestE2EAnyFieldDestructureNestedSpread(t *testing.T) {
	assertOutput(t, `
class Box { v: any = null; label: string = "x"; }
const b = new Box();
b.v = 99;
const { v, label } = b;
console.log(v, label);
const nested: { inner: { a: any } } = { inner: { a: 3 } };
nested.inner.a = "deep";
console.log(nested.inner.a);
const o2 = { ...nested.inner };
console.log(o2.a);
`, "99 x\ndeep\ndeep")
}

// Evolving-any for an unannotated `null`-initialized field reassigned in a
// method (ADR-00924): `#field = null; this.#field ??= 1` stores 1 rather than a
// double into a null slot (was invalid IR). Test262
// logical-assignment/left-hand-side-private-reference-data-property-nullish.js.
func TestE2EAnyFieldEvolvingNullCompoundAssign(t *testing.T) {
	assertOutput(t, `
class C {
  #field = null;
  bump() { return this.#field ??= 1; }
  get() { return this.#field; }
}
const o = new C();
console.log(o.bump());
console.log(o.get());
`, "1\n1")
}

// An `any` field can only be structurally coerced to another `any` field — a
// concrete field passed where an `any` field is wanted (or vice versa) is a
// representation mismatch (box vs raw) and is rejected cleanly, not silently
// mis-read (TDD-00208).
func TestE2EAnyFieldBoxingMismatchRejected(t *testing.T) {
	mustCompileError(t, `
function take(o: { x: any }): void { console.log(o.x); }
const c = { x: 5 };
take(c);
`, "incompatible")
}

// ADR-01059/ADR-01060: an annotated `any` module binding is a real module
// global (a named function reads it), and a container handle that has no box
// kind yet (`Map`/`Set`/…) under an `any` annotation is a clean rejection —
// it used to emit `ret ptr` for an i64 (invalid IR).
func TestE2EAnyAnnotatedModuleGlobal(t *testing.T) {
	assertOutput(t, `
const cfg: any = { retries: 3 };
const n: any = 5;
const arr: any = [1, 2];
let later: any;
function f() { return [cfg.retries, n, arr.length]; }
function g() { return later; }
later = "set";
console.log(f(), g(), typeof later);
`, "[ 3, 5, 2 ] set string")
}

func TestE2EAnyAnnotatedMapRejected(t *testing.T) {
	for _, init := range []string{"new Map<string, number>()", "new Set<number>()", "new WeakMap<object, number>()"} {
		_, err := parseAndCompile("const m: any = " + init + ";\nfunction f() { return m; }\nconsole.log(f());\n")
		if err == nil {
			t.Fatalf("expected a compile error for `const m: any = %s`, got none", init)
		}
		if !strings.Contains(err.Error(), "cannot be declared as `any`") {
			t.Fatalf("unexpected error for %s: %v", init, err)
		}
	}
}
