package tests

import "testing"

// ToPrimitive coercion of objects in a numeric context (TDD-00201 Stage 1):
// valueOf / Symbol.toPrimitive drive the number-hint conversion for arithmetic,
// relational, and bitwise operators. Values verified against `node`.

func TestE2EToPrimitiveValueOfBitwise(t *testing.T) {
	assertOutput(t, `
const a = { valueOf: function() { return 6; } };
console.log(a & 3);
console.log(a | 1);
console.log(a << 1);
`, "2\n7\n12")
}

func TestE2EToPrimitiveValueOfArithmetic(t *testing.T) {
	assertOutput(t, `
const b = { valueOf: function() { return 5; } };
console.log(b * 2);
console.log(b - 3);
console.log(b / 2);
console.log(b % 3);
`, "10\n2\n2.5\n2")
}

func TestE2EToPrimitiveValueOfRelational(t *testing.T) {
	assertOutput(t, `
const b = { valueOf: function() { return 5; } };
console.log(b > 4);
console.log(b < 4);
console.log(b >= 5);
`, "true\nfalse\ntrue")
}

func TestE2EToPrimitiveSymbolToPrimitive(t *testing.T) {
	// [Symbol.toPrimitive](hint) is consulted first — via the @@toPrimitive
	// string-alias desugar, no dynamic-property-bag dependency.
	assertOutput(t, `
const c = { [Symbol.toPrimitive]: function(hint: string) { return 42; } };
console.log(c * 1);
console.log(c % 5);
console.log(c & 7);
`, "42\n2\n2")
}

func TestE2EToPrimitiveStringHint(t *testing.T) {
	// String-hint ToPrimitive (TDD-00201 Stage 2): String()/template/concat honor
	// a user toString or @@toPrimitive. Values verified against `node`.
	assertOutput(t, `
const o = { toString: function() { return "hi"; } };
console.log(String(o));
console.log(`+"`x${o}`"+`);
console.log("a" + o);
const p = { [Symbol.toPrimitive]: function(hint: string) { return "P"; } };
console.log(String(p));
`, "hi\nxhi\nahi\nP")
}

func TestE2EToPrimitiveStringMethodArg(t *testing.T) {
	// A string method's search argument is ToString'd: s.indexOf(obj) searches for
	// obj's toString value, not its pointer.
	assertOutput(t, `
console.log("xABABx".indexOf({ toString: function() { return "AB"; } }));
console.log("abcabc".lastIndexOf({ toString: function() { return "bc"; } }));
`, "1\n4")
}

func TestE2EToPrimitiveStringMethodSweep(t *testing.T) {
	// includes/startsWith/endsWith and replace/replaceAll/split ToString an object
	// argument; a RegExp argument is dispatched before coercion (not ToString'd).
	assertOutput(t, `
console.log("hello world".includes({ toString: function() { return "world"; } }));
console.log("hello".startsWith({ toString: function() { return "he"; } }));
console.log("hello".endsWith({ toString: function() { return "lo"; } }));
console.log("a-b-c".replace({ toString: function() { return "-"; } }, { toString: function() { return "+"; } }));
console.log("a-b-c".replaceAll({ toString: function() { return "-"; } }, "+"));
console.log("a,b,c".split({ toString: function() { return ","; } }).length);
console.log("x1x1".replace(/1/g, "9"));
`, "true\ntrue\ntrue\na+b-c\na+b+c\n3\nx9x9")
}

func TestE2EToPrimitivePlusOperator(t *testing.T) {
	// `+` uses the "default" hint (valueOf→toString); the result drives the
	// concat-vs-add overload. Verified against `node`.
	assertOutput(t, `
const n = { valueOf: function() { return 5; } };
console.log(n + 3);
console.log("x" + n);
const s = { toString: function() { return "a"; } };
console.log(s + "b");
const p = { [Symbol.toPrimitive]: function(h: string) { return "P"; } };
console.log(p + 1);
`, "8\nx5\nab\nP1")
}

func TestE2EToPrimitiveLooseEquality(t *testing.T) {
	// `obj == primitive` runs ToPrimitive on the object; obj == obj (reference)
	// and obj == null are left uncoerced.
	assertOutput(t, `
const n = { valueOf: function() { return 5; } };
console.log(n == 5);
console.log(n != 5);
console.log(n == 6);
const s = { toString: function() { return "a"; } };
console.log(s == "a");
const a = { valueOf: function() { return 1; } };
const b = a;
console.log(a == b);
`, "true\nfalse\nfalse\ntrue\ntrue")
}

func TestE2EToPrimitiveCompatJSRuntime(t *testing.T) {
	// Stage 4: under -compat=js a plain object is a NaN-boxed dynamic bag, so
	// ToPrimitive runs at runtime (valueOf/toString/@@toPrimitive via the bag).
	// Arithmetic and string coercion are faithful (were NaN / "[object Object]").
	assertOutputCompatJS(t, `
const n = { valueOf: function() { return 5; } };
console.log(n * 2);
console.log(n + 3);
console.log("v=" + n);
const s = { toString: function() { return "hi"; } };
console.log(String(s));
console.log(`+"`t=${s}`"+`);
const p = { [Symbol.toPrimitive]: function(h: string) { return "P"; } };
console.log(p + "?");
`, "10\n8\nv=5\nhi\nt=hi\nP?")
}

func TestE2EToPrimitiveCompatJSLooseEquality(t *testing.T) {
	// Stage 4: JS Abstract Equality under -compat=js — object ToPrimitive
	// (n == 5), both-objects reference ({}=={} is false), nullish, and mixed
	// static scalars (5 == "5", "" == 0). Verified against `node`.
	assertOutputCompatJS(t, `
const n = { valueOf: function() { return 5; } };
console.log(n == 5);
console.log(n != 5);
console.log(n == "5");
console.log(n == null);
const a = { valueOf: function() { return 1; } };
console.log(a == a);
console.log({ valueOf: function() { return 1; } } == { valueOf: function() { return 1; } });
console.log(5 == "5");
console.log("" == 0);
console.log(null == undefined);
console.log(null == 0);
`, "true\nfalse\ntrue\nfalse\ntrue\nfalse\ntrue\ntrue\ntrue\nfalse")
}

func TestE2EToPrimitiveConsoleLogStillInspects(t *testing.T) {
	// Guard: console.log(obj) keeps its util.inspect dump (uses emitInspectObject,
	// not emitValueToString) — the Stage 2 string-hint hook must not change it.
	assertOutput(t, `
const o = { a: 1, b: 2 };
console.log(o);
`, "{ a: 1, b: 2 }")
}

func TestE2EToPrimitiveValueOfObjectSkipsToString(t *testing.T) {
	// valueOf returning a non-primitive is skipped; toString (numeric) wins —
	// mirrors the spec's "if result is an Object, continue" for the number hint.
	assertOutput(t, `
const d = { valueOf: function() { return {}; }, toString: function() { return 3; } };
console.log(d & 2);
console.log(d * 4);
`, "2\n12")
}

func TestE2EToPrimitiveBitwiseInlineObjectLiteral(t *testing.T) {
	// ADR-00866 / Test262 bitwise-and S11.10.1_A2.2_T1: an object literal used
	// *inline* as a bitwise operand (not bound to a const first) previously
	// truncated its raw pointer to i32 — invalid IR. The number-hint ToPrimitive
	// ladder now runs on it before ToInt32, in both operand positions, including
	// the toString-fallthrough when valueOf yields a non-primitive.
	assertOutput(t, `
console.log(({ valueOf: function() { return 1; } } & 1));
console.log((1 & { toString: function() { return 1; } }));
console.log((1 & { valueOf: function() { return {}; }, toString: function() { return 1; } }));
console.log(({ valueOf: function() { return 12; } } << 1));
`, "1\n1\n1\n24")
}

func TestE2EToPrimitiveBitwiseObjectNoPrimitiveRejects(t *testing.T) {
	// An object with no primitive value (valueOf/toString both return objects)
	// has no ToInt32 — strict rejects cleanly (TS reports the same), rather than
	// emitting `trunc i64 <object ptr> to i32`.
	assertCodegenError(t, `
console.log((1 & { valueOf: function() { return {}; }, toString: function() { return {}; } }));
`, "without a primitive value")
}
