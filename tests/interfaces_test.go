package tests

import "testing"

// --- interface extends merging + constructor types (ADR-00452) ---

func TestE2EInterfaceExtendsMergesBaseFields(t *testing.T) {
	assertOutput(t, `
interface A { a: number; }
interface B { b: number; }
interface C extends A, B { c: number; }
const x: C = { a: 1, b: 2, c: 3 };
console.log(x.a + x.b + x.c);
`, "6")
}

func TestE2EConstructorTypeAnnotationErased(t *testing.T) {
	assertOutput(t, `
type Ctor = new (n: number) => string;
function useCtor(f: (n: number) => string): string { return f(2); }
console.log(useCtor((n: number): string => "n" + String(n)));
`, "n2")
}

// --- callable interfaces (ADR-00455) ---

func TestE2ECallableInterface(t *testing.T) {
	assertOutput(t, `
interface Renderer {
    (n: number): string;
}
function render(fn: Renderer, n: number): string { return fn(n); }
console.log(render((n: number): string => "r" + String(n), 4));
`, "r4")
}

func TestE2ECallableInterfaceMixedIsError(t *testing.T) {
	_, err := parseAndCompile(`
interface Bad { (n: number): string; x: number; }
`)
	if err == nil {
		t.Fatal("expected a parse error for a call signature mixed with other interface members")
	}
}

func TestE2EClassInterfaceDeclarationMerging(t *testing.T) {
	// ADR-00466: an interface may coexist with a same-name class (either
	// order) — the class wins as the binding; the interface's members are
	// ignored (disclosed narrowing of TS's real merge).
	assertOutput(t, `
interface B { p: number; }
class B { q: number = 2; describe(): string { return "q=" + String(this.q); } }
const b = new B();
console.log(b.describe());
`, "q=2")
}

func TestE2EGenericMethodSignatureErased(t *testing.T) {
	// ADR-00469 extension: `map<T>(x: T): T` interface/object-type method
	// signatures parse with T erased to any.
	assertOutput(t, `
interface Mapper {
    map<T>(x: T): T;
}
type Ops = { pick<T>(x: T): T };
class M implements Mapper {
    map(x: any): any { return x; }
}
console.log(new M().map("v"));
`, "v")
}

// --- ADR-00479: void-init undefined, iface merging, overloaded call sigs ---

func TestE2EInterfaceInterfaceMerging(t *testing.T) {
	assertOutput(t, `
interface Cfg { host: string; }
interface Cfg { port: number; }
const c: Cfg = { host: "localhost", port: 8080 };
console.log(c.host, c.port);
`, "localhost 8080")
}

func TestE2EOverloadedCallableInterfaceFirstWins(t *testing.T) {
	assertOutput(t, `
interface Call { (n: number): number; (s: string): number; }
const f: Call = (n: any): number => 9;
console.log(f(1));
`, "9")
}

func TestE2EVoidInitializerBindsUndefined(t *testing.T) {
	assertOutput(t, `
function say(): void { console.log("ran"); }
var b = say();
console.log(typeof b, b === undefined);
`, "ran\nundefined true")
}

// --- Self-referential structural types (ADR-00949) ---

// A self-referential interface array field (`children: N[]`) previously
// rejected a non-empty element literal as heterogeneous — the self-ref element
// type resolved to a placeholder before the interface's own fields existed, so
// its IR defaulted to number and drilling `r.children[0].val` failed. A named
// placeholder seeded pre-field-resolution plus on-demand re-resolution
// (canonicalizeClassTy keyed by RefName) fixes both.
func TestE2ESelfReferentialInterfaceArrayField(t *testing.T) {
	assertOutput(t, `
interface N { children: N[]; val: number; }
const gc: N = { children: [], val: 3 };
const c: N = { children: [gc], val: 2 };
const r: N = { children: [c], val: 1 };
console.log(r.children[0].val);
console.log(r.children[0].children[0].val);
`, "2\n3")
}

// A self-referential interface pointer field (`next: N | null`) previously
// gave "no field" on the second drill — the field snapshot was the empty
// placeholder. Traversing the whole chain must work.
func TestE2ESelfReferentialInterfaceLinkedList(t *testing.T) {
	assertOutput(t, `
interface N { next: N | null; val: number; }
const c: N = { next: null, val: 3 };
const b: N = { next: c, val: 2 };
const a: N = { next: b, val: 1 };
let cur: N | null = a;
let sum = 0;
while (cur !== null) { sum += cur.val; cur = cur.next; }
console.log(sum);
`, "6")
}

// Mutually-referential interfaces resolve each other's placeholder on demand.
func TestE2EMutuallyReferentialInterfaces(t *testing.T) {
	assertOutput(t, `
interface A { b: B | null; x: number; }
interface B { a: A | null; y: number; }
const bb: B = { a: null, y: 20 };
const aa: A = { b: bb, x: 10 };
console.log(aa.b!.y);
`, "20")
}

// The same fix covers an object-shape `type` alias (also a named structural
// type), not just `interface`.
func TestE2ESelfReferentialTypeAlias(t *testing.T) {
	assertOutput(t, `
type N = { next: N | null; val: number; };
const b: N = { next: null, val: 2 };
const a: N = { next: b, val: 1 };
console.log(a.next!.val);
`, "2")
}
