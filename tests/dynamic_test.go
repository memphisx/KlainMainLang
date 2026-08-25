package tests

import (
	"testing"
)

// --- any / unknown (Staged V1: declare/assign/reassign, print, typeof, ===/!==) ---

func TestE2EAnyReassignAcrossTypes(t *testing.T) {
	assertOutput(t, `
let x: any = 5
console.log(x)
x = "hello"
console.log(x)
x = true
console.log(x)
x = null
console.log(x)
`, "5\nhello\ntrue\nnull")
}

func TestE2EAnyTemplateLiteral(t *testing.T) {
	assertOutput(t, `
let x: any = 42
console.log(`+"`value: ${x}`"+`)
x = "world"
console.log(`+"`value: ${x}`"+`)
`, "value: 42\nvalue: world")
}

func TestE2EAnyTypeofRuntime(t *testing.T) {
	assertOutput(t, `
let x: any = 5
console.log(typeof x)
x = "hi"
console.log(typeof x)
x = true
console.log(typeof x)
x = null
console.log(typeof x)
let y: any
console.log(typeof y)
`, "number\nstring\nboolean\nobject\nundefined")
}

func TestE2EAnyEquality(t *testing.T) {
	assertOutput(t, `
let a: any = 5
let b: any = 5
console.log(a === b)
let c: any = "5"
console.log(a === c)
console.log(a !== c)
let d: any = 5.0
console.log(a === d)
console.log(a === 5)
`, "true\nfalse\ntrue\ntrue\ntrue")
}

func TestE2EUnknownFloat(t *testing.T) {
	assertOutput(t, `
let y: unknown = 3.14
console.log(y)
console.log(typeof y)
`, "3.14\nnumber")
}

func TestE2EAnyArithmeticRejected(t *testing.T) {
	_, err := parseAndCompile(`
let x: any = 5
console.log(x + 1)
`)
	if err == nil {
		t.Fatal("expected a compile error for arithmetic on an any-typed value, got none")
	}
}

// TDD-00062 (Staged V2): a bare `any`/`unknown` parameter is now supported —
// its argument is boxed at the call site and can be printed, compared with
// `===`, and reflected on with `typeof`, exactly like any other any-typed
// value. (Arithmetic/field-access/indexing on it stay rejected, as for any
// any-typed value — see the other tests here.)
func TestE2EAnyAsFunctionParam(t *testing.T) {
	assertOutput(t, `
function f(x: any): void { console.log(x); }
f(5);
f("hi");
f(true);
`, "5\nhi\ntrue")
}

func TestE2EAnyParamStrictEquality(t *testing.T) {
	assertOutput(t, `
function eq(a: any, b: any): void {
	console.log(a === b ? "same" : "diff");
}
eq(1, 1);
eq(1, "1");
let o = { v: 1 };
eq(o, o);
eq(o, { v: 1 });
`, "same\ndiff\nsame\ndiff")
}

// Arrow-function and function-expression parameters get the same bare
// any/unknown support as named functions — the closure-call path already
// boxes a dynamic-typed argument.
func TestE2EAnyArrowParam(t *testing.T) {
	assertOutput(t, `
const eq = (a: any, b: any): void => {
	console.log(a === b ? "same" : "diff");
};
eq(1, 1);
eq(1, "1");
`, "same\ndiff")
}

func TestE2EAnyArrowReturn(t *testing.T) {
	assertOutput(t, `
const pick = (flag: boolean, a: any, b: any): any => flag ? a : b;
console.log(pick(true, "yes", 0));
console.log(pick(false, "yes", 0));
`, "yes\n0")
}

func TestE2EAnyFunctionExpressionParam(t *testing.T) {
	assertOutput(t, `
const show = function (x: any): void { console.log(x); };
show(7);
show("hi");
`, "7\nhi")
}

func TestE2EUnknownAsFunctionParam(t *testing.T) {
	assertOutput(t, `
function f(x: unknown): void { console.log(typeof x); }
f(5);
f("hi");
`, "number\nstring")
}

// A nested dynamic (any as an array element) stays rejected in parameter
// position — only the bare top-level any/unknown was lifted.
func TestE2EAnyArrayParamRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(xs: any[]): void { console.log(xs.length) }
f([1, 2, 3])
`)
	if err == nil {
		t.Fatal("expected a compile error for any[] as a function parameter type, got none")
	}
}

// An array argument to an `any` parameter is boxed by its data pointer, so
// `===` is reference identity (arrays are reference types in JS): the same
// array boxed twice is equal; two distinct arrays are not. `typeof` is
// "object". Contents/length are not preserved, so it stringifies to the
// `[object Array]` tag (a documented deviation from JS's "1,2,3").
func TestE2EArrayArgumentToAnyParamReferenceEquality(t *testing.T) {
	assertOutput(t, `
function eq(a: any, b: any): void { console.log(a === b ? "same" : "diff"); }
let a = [1, 2, 3];
let b = [1, 2, 3];
eq(a, a);
eq(a, b);
function kind(x: any): void { console.log(typeof x); }
kind(a);
function show(x: any): void { console.log(x); }
show(a);
`, "same\ndiff\nobject\n[object Array]")
}

// A boxed object now stringifies to JS's "[object Object]" instead of
// printing the raw struct bytes (blank/garbage) — a now-reachable path once
// any-typed parameters can carry an object.
func TestE2EBoxedObjectToString(t *testing.T) {
	assertOutput(t, `
function show(x: any): void { console.log(x); }
show({ a: 1 });
`, "[object Object]")
}

func TestE2EAnyArrayElementRejected(t *testing.T) {
	_, err := parseAndCompile(`
let arr: any[] = [1, 2, 3]
console.log(arr.length)
`)
	if err == nil {
		t.Fatal("expected a compile error for any as an array element type, got none")
	}
}

// --- General union types beyond T | null (TDD-00043) ---

func TestE2EUnionDeclareAndPrint(t *testing.T) {
	assertOutput(t, `
let x: string | number = "hello"
console.log(x)
console.log(typeof x)
x = 42
console.log(x)
console.log(typeof x)
`, "hello\nstring\n42\nnumber")
}

func TestE2EUnionReassignAcrossMembers(t *testing.T) {
	assertOutput(t, `
let x: number | boolean = 5
console.log(x)
x = true
console.log(x)
x = 10
console.log(x)
`, "5\ntrue\n10")
}

func TestE2EUnionReassignRejectsNonMember(t *testing.T) {
	_, err := parseAndCompile(`
let x: string | number = "hi"
x = true
`)
	if err == nil {
		t.Fatal("expected a compile error assigning a boolean into a string | number variable, got none")
	}
}

func TestE2EUnionDeclareRejectsNonMember(t *testing.T) {
	_, err := parseAndCompile(`
let x: string | number = true
console.log(x)
`)
	if err == nil {
		t.Fatal("expected a compile error initializing a string | number variable with a boolean, got none")
	}
}

func TestE2EUnionWithNullMember(t *testing.T) {
	assertOutput(t, `
let x: string | number | null = null
console.log(x)
x = "hi"
console.log(x)
x = 5
console.log(x)
x = null
console.log(x)
`, "null\nhi\n5\nnull")
}

func TestE2EUnionWithoutNullRequiresInitializer(t *testing.T) {
	_, err := parseAndCompile(`
let x: string | number
console.log(x)
`)
	if err == nil {
		t.Fatal("expected a compile error declaring a non-nullable union with no initializer, got none")
	}
}

func TestE2EUnionAsFunctionParamAndReturn(t *testing.T) {
	assertOutput(t, `
function describe(x: string | number): string | number {
	if (typeof x === "string") {
		return "matched string"
	}
	return x
}
console.log(describe("hi"))
console.log(describe(42))
`, "matched string\n42")
}

func TestE2EUnionCallArgumentRejectsNonMember(t *testing.T) {
	_, err := parseAndCompile(`
function f(x: string | number): void { console.log(x) }
f(true)
`)
	if err == nil {
		t.Fatal("expected a compile error calling a string | number parameter with a boolean, got none")
	}
}

func TestE2EUnionArrowFunction(t *testing.T) {
	assertOutput(t, `
const toStr = (x: number | boolean): string | number => {
	return x
}
console.log(toStr(5))
console.log(toStr(true))
`, "5\ntrue")
}

func TestE2EUnionNarrowByTypeof(t *testing.T) {
	// Flow narrowing (TDD-00114): inside each typeof branch the value is usable
	// as the concrete member (method call, arithmetic).
	assertOutput(t, `
function describe(x: string | number): string {
  if (typeof x === "string") {
    return "str:" + x.toUpperCase()
  } else {
    return "num:" + (x + 1)
  }
}
console.log(describe("hi"))
console.log(describe(41))
`, "str:HI\nnum:42")
}

func TestE2EUnionNarrowThreeMembersNested(t *testing.T) {
	// Nested else-if composes: each branch refines the remaining members.
	assertOutput(t, `
function label(v: string | number | boolean): string {
  if (typeof v === "boolean") {
    return v ? "yes" : "no"
  } else if (typeof v === "number") {
    return "n" + (v * 2)
  } else {
    return "s" + v.length
  }
}
console.log(label(true))
console.log(label(10))
console.log(label("abc"))
`, "yes\nn20\ns3")
}

func TestE2EUnionNarrowEarlyReturn(t *testing.T) {
	// Early-return narrowing: after `if (guard) return`, the remainder of the
	// scope is narrowed to the complement.
	assertOutput(t, `
function describe(x: string | number): string {
  if (typeof x === "string") {
    return "str:" + x.toUpperCase()
  }
  return "num:" + (x + 1)
}
console.log(describe("hi"))
console.log(describe(41))
`, "str:HI\nnum:42")
}

func TestE2EUnionNarrowTruthiness(t *testing.T) {
	// `if (x)` on a nullable single-member union narrows out null.
	assertOutput(t, `
function len(s: string | null): number {
  if (s) {
    return s.length
  }
  return -1
}
console.log(len("abcd"))
console.log(len(null))
`, "4\n-1")
}

func TestE2EUnionObjectMemberNarrowed(t *testing.T) {
	// A single object member in a union is allowed (TDD-00115) and usable via
	// `typeof x === "object"` narrowing.
	assertOutput(t, `
interface Point { x: number; y: number }
function describe(v: string | Point): string {
  if (typeof v === "object") {
    return "pt:" + (v.x + v.y)
  } else {
    return "str:" + v.toUpperCase()
  }
}
const p: Point = { x: 3, y: 4 }
console.log(describe(p))
console.log(describe("hi"))
`, "pt:7\nstr:HI")
}

func TestE2EUnionObjectNullTruthinessNarrow(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function dist(p: Point | null): number {
  if (p) { return p.x + p.y }
  return -1
}
console.log(dist({ x: 10, y: 20 }))
console.log(dist(null))
`, "30\n-1")
}

func TestE2EUnionObjectRestAfterNarrow(t *testing.T) {
	// TDD-00065 Stage 3c object-union source: narrow a Rec|null to Rec, then
	// object-rest it.
	assertOutput(t, `
interface Rec { a: number; b: string }
function f(v: Rec | null): void {
  if (v) {
    const { a, ...rest } = v
    console.log(a)
    console.log(rest.b)
  }
}
f({ a: 5, b: "hi" })
`, "5\nhi")
}

func TestE2EUnionTwoUndiscriminatedObjectsRejected(t *testing.T) {
	// Two object members without a common literal tag aren't a discriminated
	// union — a clean rejection (TDD-00116).
	mustCompileError(t, `
interface A { x: number }
interface B { y: number }
let v: A | B = { x: 1 }
`, "discriminated union")
}

func TestE2EDiscriminatedUnion(t *testing.T) {
	assertOutput(t, `
interface Circle { kind: "circle"; r: number }
interface Square { kind: "square"; s: number }
function area(sh: Circle | Square): number {
  if (sh.kind === "circle") {
    return 3 * sh.r * sh.r
  } else {
    return sh.s * sh.s
  }
}
console.log(area({ kind: "circle", r: 2 }))
console.log(area({ kind: "square", s: 3 }))
`, "12\n9")
}

func TestE2EDiscriminatedUnionThreeWayAndTagRead(t *testing.T) {
	assertOutput(t, `
interface A { t: "a"; x: number }
interface B { t: "b"; y: number }
interface C { t: "c"; z: number }
function f(v: A | B | C): string {
  console.log(v.t)
  if (v.t === "a") { return "A" + v.x }
  else if (v.t === "b") { return "B" + v.y }
  else { return "C" + v.z }
}
console.log(f({ t: "a", x: 1 }))
console.log(f({ t: "b", y: 2 }))
`, "a\nA1\nb\nB2")
}

func TestE2EDiscriminatedUnionEarlyReturn(t *testing.T) {
	assertOutput(t, `
interface Ok { status: "ok"; value: number }
interface Err { status: "err"; msg: string }
function handle(r: Ok | Err): string {
  if (r.status === "err") { return "E:" + r.msg }
  return "V:" + r.value
}
console.log(handle({ status: "ok", value: 7 }))
console.log(handle({ status: "err", msg: "bad" }))
`, "V:7\nE:bad")
}

func TestE2EUnionArrayElementRejected(t *testing.T) {
	_, err := parseAndCompile(`
let arr: (string | number)[] = [1, "two", 3]
console.log(arr.length)
`)
	if err == nil {
		t.Fatal("expected a compile error for a union as an array element type (not yet supported nested in a container), got none")
	}
}

// TestE2EUnionObjectField: a constrained-union object field (TDD-00119's general
// enablement, on top of TDD-00043) now works — construction boxes each member,
// reads unbox, and typeof narrowing selects the member.
func TestE2EUnionObjectField(t *testing.T) {
	assertOutput(t, `
interface Item { value: string | number }
const a: Item = { value: 42 }
const va = a.value
if (typeof va === "number") { console.log("num " + (va + 1)) } else { console.log("str " + va) }
const b: Item = { value: "hi" }
const vb = b.value
if (typeof vb === "string") { console.log("str " + vb) } else { console.log("num") }
`, "num 43\nstr hi")
}

// TestE2EUnionObjectFieldInvalidMemberRejected: assigning a non-member value to a
// union object field is a clean compile error (the member-set check is wired at
// object-literal-field construction).
func TestE2EUnionObjectFieldInvalidMemberRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Item { value: string | number }
let it: Item = { value: true }
console.log(it.value)
`)
	if err == nil {
		t.Fatal("expected a compile error assigning a boolean to a string|number union field, got none")
	}
}
