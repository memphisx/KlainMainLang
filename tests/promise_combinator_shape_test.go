package tests

import (
	"testing"
)

// --- ADR-00689: Promise combinator result-value/shape fidelity ---
//
// Two combinator bugs fixed here:
//
//  1. Promise.any over a reject-first array decoded a numeric winner with the
//     wrong value shape (the array's element type was keyed off the leading
//     Promise.reject, whose resolved type is `never` and carries no encoding),
//     so a `double` winner slot was read back as a raw i64 bit pattern.
//  2. Promise.allSettled produced the wrong per-element object shape: every
//     element carried both `value` and `reason`, and a rejected `reason` was a
//     synthetic Error wrapper. Real JS gives a fulfilled entry only `value`, a
//     rejected entry only `reason`, and reports the raw rejection value.

func TestE2EPromiseAnyRejectFirstNumericWinner(t *testing.T) {
	// The winner (index 1) is a number; the leading Promise.reject must not make
	// it decode as a raw i64 bit pattern. A string winner already worked.
	assertOutput(t, `
async function m(): Promise<number> {
    return await Promise.any([Promise.reject("x"), Promise.resolve(2)])
}
m().then((v) => { console.log(v) })
`, "2")

	assertOutput(t, `
async function m(): Promise<string> {
    return await Promise.any([Promise.reject("x"), Promise.resolve("hi")])
}
m().then((v) => { console.log(v) })
`, "hi")
}

func TestE2EPromiseAllSettledJSONShape(t *testing.T) {
	// Each element carries ONLY value (fulfilled) or ONLY reason (rejected), and
	// a rejected reason preserves the raw thrown value, not an Error wrapper.
	assertOutput(t, `
async function m(): Promise<string> {
    return JSON.stringify(await Promise.allSettled([Promise.resolve(1), Promise.reject("e")]))
}
m().then((v) => { console.log(v) })
`, `[{"status":"fulfilled","value":1},{"status":"rejected","reason":"e"}]`)
}

func TestE2EPromiseAllSettledPropertyAccessStillWorks(t *testing.T) {
	// The shape fix must not regress direct property access on settlements.
	assertOutput(t, `
const r = await Promise.allSettled([Promise.resolve(7), Promise.reject("boom")])
console.log(r[0].status)
const r0 = r[0]
if (r0.status === "fulfilled") console.log(r0.value)
console.log(r[1].status)
`, "fulfilled\n7\nrejected")
}

// A promise held boxed: then, catch and finally as dynamic functions, and
// the generic catch/finally on another thenable receiver.
func TestE2EPromiseMethodsThroughAny(t *testing.T) {
	assertSameAsNode(t, `const p: any = Promise.resolve(3);
p.then((x: number) => console.log("then", x));
p.catch(() => 0).then((x: number) => console.log("catch-pass", x));
p.finally(() => console.log("fin")).then((x: number) => console.log("after-fin", x));
const r: any = Promise.reject(new Error("no")); r.catch((e: Error) => console.log("caught", e.message));
const th: any = { then(f: any, g: any) { console.log("th", typeof f, typeof g); } };
Promise.prototype.catch.call(th, () => 0);
const fn = p.then; console.log(typeof fn);
try { Promise.prototype.then.call({}, () => 0); } catch (e) { console.log((e as Error).constructor.name); }
const thenable: any = { then(a: any, b: any) { console.log("invoked", typeof a, typeof b); } };
const c = (Promise.prototype.finally as any).call(thenable, () => 1);
`)
}

// A generic Promise subclass holds its values boxed: its derived instances,
// bound to a const or held as a typed Promise, read them as their types.
func TestE2EGenericPromiseSubclassBindings(t *testing.T) {
	assertSameAsNode(t, `class P<T> extends Promise<T> {}
class Q<T> extends Promise<T> { constructor(ex: (res: (v: T) => void, rej: (e: unknown) => void) => void) { super(ex); } }
const p = new P<number>((r) => r(1));
const f = p.then((x) => x);
console.log(f instanceof P);
f.then((v) => console.log("v", v + 1));
const s = p.then((x) => String(x));
s.then((v) => console.log("s", v + 1, v.length));
const a: Promise<number> = new P<number>((r) => r(2));
a.then((v) => v * 3).then((v) => console.log("a", v + 1));
a.finally(() => console.log("fin")).then((v) => console.log("af", v));
const q = new Q<number>((r) => r(1));
const m = q.then(() => { throw new Error("x"); }).catch((e) => (e as Error).message);
m.then((v) => console.log("m", v));
async function main() { const w: Promise<number> = new P<number>((r) => r(4)); console.log("await", (await w) + 1); }
main();
class R extends Promise<number> {}
const r = new R((res) => res(1));
r.then(() => console.log("r1")).then(() => console.log("r2")).then(() => console.log("r3"));
async function order() { const t: Promise<number> = r; console.log("order", await t); }
order();
`)
}

// An erased Promise subclass's static resolve settles its result to a box,
// as its type reads it: a number came back with the box offset still on.
func TestE2EPromiseSubclassResolveNumber(t *testing.T) {
	assertOutput(t, `
class P<T> extends Promise<T> {}
P.resolve(5).then((v) => console.log("D", v, typeof v));
P.resolve(1).then((v) => v + 1).then((v) => console.log("B", v));
P.resolve(1).then((v) => Promise.resolve(v + 1)).then((v) => console.log("A", v));
`, "D 5 number\nB 2\nA 2")
}
