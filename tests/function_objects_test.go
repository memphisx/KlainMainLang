package tests

import "testing"

// Function values as objects (TDD-00229 Stage A): util.inspect's
// `[Function: name]` family, NamedEvaluation names, `name`/`length`, and the
// same through `any`. Node is the oracle.

func TestE2EFunctionInspectForms(t *testing.T) {
	assertSameAsNode(t, `
function named(a: number, b?: number): number { return a; }
const arrow = (x: number) => x + 1;
const fe = function (y: string) { return y; };
const nfe = function inner() { return 1; };
async function af() { return 1; }
const obj = { m: (a: number, b = 2) => a + b, n: named };
let later: (n: number) => number;
later = (n: number) => n;
function* gen(a: number) { yield a; }
function two(a: number, c: number): number { return a + c; }
const b = two.bind(null, 1);
console.log(named);
console.log(arrow);
console.log(fe, nfe);
console.log(af);
console.log(obj);
console.log(later);
console.log([arrow, named]);
console.log(gen);
console.log(b);
const anyf: any = arrow;
console.log(anyf);
console.log({ x: anyf });
const te: any = TypeError;
console.log(te, [te]);
new Promise<void>((res) => { console.log(res); res(); });
`)
}

func TestE2EFunctionNameAndLength(t *testing.T) {
	assertSameAsNode(t, `
function named(a: number, b?: number, c = 3): number { return a; }
const arrow = (x: number, ...r: number[]) => x;
const fe = function (y: string) { return y; };
function two(a: number, c: number): number { return a + c; }
const b = two.bind(null, 1);
const obj = { m: (a: number) => a };
console.log(named.name, named.length, arrow.name, arrow.length, fe.name, fe.length);
console.log(b.name, b.length, obj.m.name, obj.m.length);
const anon = [(q: number) => q][0];
console.log(JSON.stringify(anon.name), anon.length);
const anyf: any = arrow;
console.log(anyf.name, anyf.length, anyf.foo);
const re: any = RangeError;
console.log(re.name, re.length);
`)
}

func TestE2ECallAnyTypedVariable(t *testing.T) {
	// Calling a variable that holds an `any` goes through the runtime call.
	assertSameAsNode(t, `
const f: any = (n: number) => n + 1;
console.log(f(2));
function g() { const h: any = (n: number) => n * 2; return h(3); }
console.log(g());
`)
}

func TestE2EModuleGlobalTernaryReadByFunction(t *testing.T) {
	// A top-level const initialised from a constant ternary (the
	// platform-pick idiom) is readable from a named function.
	assertSameAsNode(t, `
const libc = process.platform === 'darwin' ? 'libSystem' : 'libc.so.6';
const n = 1 > 0 ? 2.5 : 3;
const b = process.platform === 'aix' ? true : false;
function read(): string { return String(libc.length > 0) + ' ' + String(n * 2) + ' ' + String(b); }
console.log(read());
`)
}

func TestE2EBooleanAmongNumbersIsHeterogeneous(t *testing.T) {
	// `[true, 5]` used to coerce silently to `[true, true]`; strict rejects it
	// like any other heterogeneous literal, -compat=js boxes it (TDD-00229).
	assertCodegenError(t, `const a = [true, 5];
console.log(a);
`, "element 1 is a number, not a boolean")
	assertSameAsNodeCompatJS(t, `const a = [true, 5, false];
console.log(a);
`)
}

func TestE2EDefaultedParamsTypesAndDynamicCalls(t *testing.T) {
	// An unannotated parameter takes its literal (or module-global) default's
	// type; a defaulted first parameter parses as an arrow; and a call through
	// `any` fills defaults for omitted/undefined arguments and passes numbers
	// as numbers (TDD-00229).
	assertSameAsNode(t, `
const SUFFIX = '.';
function f(p = '!', q = true, r = [1]) { return p + String(q) + r.length; }
console.log(f(), f('?', false, [1, 2]));
const g = (p = '!') => p;
console.log(g(), g('x'));
let a = 0;
const assigned = (a = 4);
console.log(assigned, a);
const greet = (who: string, punctuation = '!') => who + punctuation;
const tail = (x: number, y = x * 2, z = SUFFIX) => String(x) + '/' + String(y) + z;
const plain = (m, n) => m + n;
const dg: any = greet;
const dt: any = tail;
const dp: any = plain;
console.log(greet('A'), dg('A'), dg('B', '?'), dg('C', undefined));
console.log(tail(1), dt(1), dt(1, 5), dt(1, undefined, '!'));
console.log(plain(1, 2), dp(1, 2));
`)
}
