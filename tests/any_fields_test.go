package tests

import "testing"

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

// An object whose field is concrete passed where the field is `any` (or the
// reverse) is the object itself, read through the checked view (TDD-00233):
// a value of another kind written through it is an own property shadowing
// the field, so the object prints as Node's does.
func TestE2EAnyFieldBoxingThroughView(t *testing.T) {
	assertSameAsNode(t, `
function take(o: { x: any }): void { console.log(o.x); o.x = "s" }
const c = { x: 5 };
take(c);
console.log(c);
function back(o: { x: number }): number { return o.x + 1 }
const d: { x: any } = { x: 41 }
console.log(back(d))
`)
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

func TestE2EAnyAnnotatedCollections(t *testing.T) {
	assertOutput(t, `
const m: any = new Map<string, number>();
const s: any = new Set<number>();
const w: any = new WeakMap<object, number>();
function f() { return [m, s, w]; }
console.log(f());
`, "[ Map(0) {}, Set(0) {}, WeakMap { <items unknown> } ]")
}
