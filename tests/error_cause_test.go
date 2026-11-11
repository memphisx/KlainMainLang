package tests

import (
	"testing"
)

// Error options-bag cause: `new Error(msg, { cause })` stores the cause as
// `any` — a primitive keeps its value/typeof, an Error cause keeps its shape,
// an absent cause reads undefined, and the cause rides through throw/catch.
// Verified byte-exact against Node.
func TestE2EErrorCauseBasics(t *testing.T) {
	assertOutput(t, `
const e = new Error("boom", { cause: "root" })
console.log(e.message)
console.log(e.cause)
const e2 = new TypeError("t", { cause: 42 })
console.log(e2.cause)
const inner = new Error("inner")
const e3 = new Error("outer", { cause: inner })
console.log(e3.cause instanceof Error)
console.log(String(e3.cause))
const plain = new Error("nocause")
console.log(plain.cause)
try { throw e } catch (err) { console.log((err as Error).cause) }
`, "boom\nroot\n42\ntrue\nError: inner\nundefined\nroot")
}

// Any options value gives the error its `cause` property, own or
// inherited; a value without one, or a primitive, gives none.
func TestE2EErrorCauseOptionsValue(t *testing.T) {
	assertSameAsNode(t, `
interface Opts { cause?: unknown; tag: string }
const o: Opts = { cause: 42, tag: "t" }
console.log(new Error("x", o).cause)
const none: Opts = { tag: "n" }
console.log("cause" in new Error("y", none))
`)
	assertSameAsNodeCompatJS(t, `
class P { get cause() { return "inh" } }
console.log(new Error("b", new P()).cause)
console.log("cause" in new Error("c", {}), "cause" in new Error("d", 5))
`)
}

// An AggregateError never carries a cause here; reading `.cause` on one is
// bounds-safe and reads undefined (the shared errorObjType default).
func TestE2EErrorCauseAggregateReadsUndefined(t *testing.T) {
	assertOutput(t, `
const agg = new AggregateError([new Error("a")], "many")
console.log(agg.cause)
console.log(agg.errors.length)
`, "undefined\n1")
}

// A class extends AggregateError: its (errors, message, options), errors a
// non-enumerable own property; a property set on an Error at run time, and
// the cause its options gave it, are own properties.
func TestE2EAggregateErrorSubclass(t *testing.T) {
	assertSameAsNodeCLIJS(t, `const Sub = class extends AggregateError {};
const s = new Sub([1, 2], "m");
console.log(s instanceof Sub, s instanceof AggregateError, s instanceof Error, s.message, s.errors, s.name, Object.keys(s), String(s));
class S2 extends AggregateError { constructor(e) { super(e, "two"); this.k = 1; } }
const t = new S2(new Set([3]));
console.log(t.errors, t.message, t.k, t.hasOwnProperty("errors"), Object.keys(t));
try { throw t; } catch (x) { console.log(x instanceof S2, x.errors.length); }
var er = new Error("x"); er.foo = 1;
console.log(er.hasOwnProperty("foo"), er.hasOwnProperty("message"), er.hasOwnProperty("name"), er.hasOwnProperty("stack"), "name" in er, "toString" in er, "zz" in er, er.hasOwnProperty("cause"));
class E2 extends Error { constructor(m) { super(m); this.q = 1; } } var e2 = new E2("y"); e2.bar = 2; console.log(e2.hasOwnProperty("bar"), "bar" in e2, e2.hasOwnProperty("q"), e2.hasOwnProperty("message"), e2.hasOwnProperty("name"));
var te = new TypeError("t", { cause: 1 }); console.log(te.hasOwnProperty("cause"), "cause" in te);
`)
}
