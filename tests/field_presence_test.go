package tests

import "testing"

// A field has its key unless it is optional and missing: a class field, an
// object literal's property and a non-optional `T | undefined` field show
// `undefined` rather than disappearing.
func TestE2EFieldKeyPresence(t *testing.T) {
	assertSameAsNode(t, `
class C { name?: string; n?: number; k: number | undefined; constructor() { this.k = undefined; } }
console.log(new C());
const o = { v: undefined as number | undefined, s: undefined as string | undefined };
console.log(o, Object.keys(o), "v" in o);
interface I { a?: number; b: number | undefined; c?: string }
const i: I = { b: undefined };
console.log(i, Object.keys(i), "a" in i, "b" in i);
const p: Partial<{ x: number; y: string }> = { x: 1 };
console.log(p, Object.keys(p));
type M = { [K in "q" | "r"]?: number };
const mm: M = { q: 2 };
console.log(mm);
type R<T> = { a?: T; b: T };
const r: R<number> = { b: 1 };
console.log(r);
console.log(JSON.stringify(o), JSON.stringify(i));
`)
}

// JSON.parse into a typed shape leaves a missing optional string field
// undefined and a JSON null in a nullable one null, never "".
func TestE2EJSONParseTypedAbsentStrings(t *testing.T) {
	assertSameAsNode(t, `
interface Dto { id: number; note?: string; tag: string | null }
const a: Dto = JSON.parse('{"id":3,"tag":null}');
const b: Dto = JSON.parse('{"id":4,"note":"n","tag":"t"}');
console.log(JSON.stringify([a, b]));
console.log(a, b);
console.log(a.note === undefined, a.tag === null, b.note, b.tag);
`)
}
