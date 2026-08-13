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

func TestE2EUnionObjectFieldMember(t *testing.T) {
	_, err := parseAndCompile(`
interface Point { x: number; y: number }
let v: string | Point = "hi"
console.log(v)
`)
	if err == nil {
		t.Fatal("expected a compile error for an interface member in a union (V1 is scalar-only), got none")
	}
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

func TestE2EUnionInterfaceFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Item { value: string | number }
let it: Item = { value: "hi" }
console.log(it.value)
`)
	if err == nil {
		t.Fatal("expected a compile error for a union as an object field type (not yet supported nested in a container), got none")
	}
}
