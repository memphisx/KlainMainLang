package tests

import "testing"

// A value typed as one class but holding an unrelated class's instance (a
// cast through any) reads and writes its fields by name, as JavaScript
// does, not through the typed class's layout.
func TestE2EClassForeignInstanceFields(t *testing.T) {
	assertSameAsNode(t, `class A { x: number = 1; y: string = "a"; name?: string; hi() { return "A.hi"; } }
class B { q: number = 7; x: number = 2; name: string = "bee"; hi() { return "B.hi"; } }
class H { a: A; constructor(o: any) { this.a = o; } }
const h = new H(new B());
console.log(h.a.x, h.a.hi(), h.a.y, h.a.name);
h.a.x = 5;
h.a.x += 1;
const b: any = h.a;
console.log(b.q, b.x, h.a.x);
const own = new H(new A());
own.a.x = 9;
console.log(own.a.x, own.a.y, own.a.name);
`)
}

// The fetch API's BodyInit and HeadersInit, Iterable and discriminated
// unions reject what tsc rejects.
func TestE2EBodyInitHeadersInitChecked(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`fetch("http://x", { body: 5 });`, "type 'number' is not assignable to type"},
		{`fetch("http://x", { body: true });`, "type 'boolean' is not assignable to type"},
		{`fetch("http://x", { headers: new Map<string, number>() });`, "type 'Map<string, number>' is not assignable to type"},
		{`new Response(5);`, "argument of type 'number' is not assignable to parameter"},
		{`const x: Iterable<number> = new Map<string, number>(); console.log(x);`, "is not assignable to type 'Iterable<number"},
		{`const x: Iterable<number> = 5 as number; console.log(x);`, "type 'number' is not assignable to type 'Iterable<number"},
		{`class C { q = 1; }
const x: Iterable<number> = new C(); console.log(x);`, "property '[Symbol.iterator]' is missing in type 'C'"},
		{`declare const s: { done?: false; value: [string, number] };
const x: { done?: false; value: number } | { done: true; value: any } = s; console.log(x);`, "is not assignable to type"},
		{`const x: string[] = new Set<string>(); console.log(x);`, "type 'Set<string>' is not assignable to type 'string[]'"},
	} {
		syncCompileError(t, c.src, c.want)
	}
	assertSameAsNode(t, `const a: Iterable<string> = "ab";
const b: Iterable<[string, number]> = new Map([["k", 1]]);
const c: Iterable<number> = new Set([1, 2]);
function* g() { yield 3; }
const d: Iterable<number> = g();
const h: HeadersInit = { a: "b" };
const e: BodyInit = new URLSearchParams("q=1");
console.log([...a], [...b], [...c], [...d], h, e.toString());
`)
}
