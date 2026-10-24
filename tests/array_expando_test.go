package tests

import "testing"

func TestE2EArrayExpandos1(t *testing.T) {
	assertSameAsNode(t, `const k = Symbol("k");
const a: any = [1, 2];
a.foo = 1;
console.log(a.foo, a.length);
a[k] = 3;
console.log(a[k]);
const b: any = Object.assign([1, 2], { foo: 5 });
console.log(b.foo, b.length);
const c: any = Object.assign([1, 2], { [k]: 6 });
console.log(c[k]);
`)
}

func TestE2EArrayExpandos2(t *testing.T) {
	assertSameAsNode(t, `const k = Symbol("k");
const a: any = [1, 2];
a.foo = "x";
a[k] = 3;
console.log(Object.keys(a), JSON.stringify(a), Object.getOwnPropertySymbols(a).length);
for (const key in a) console.log("in", key);
console.log(a);
console.log("foo" in a, Object.hasOwn(a, "foo"), a.hasOwnProperty("foo"));
delete a.foo;
console.log(a.foo, Object.keys(a));
const t: any = new Uint8Array(2);
t.tag = 1;
console.log(t.tag);
`)
}

func TestE2EArrayExpandos3(t *testing.T) {
	assertSameAsNode(t, `const k = Symbol("k");
const e: any = []; e.x = 1; console.log(e);
const n: any = [[1]]; n.p = { q: { r: { s: 1 } } }; console.log(n);
const big: any = Array.from({ length: 8 }, (_, i) => i * 1000); big.tag = "t"; console.log(big);
const u: any = new Uint8Array(2); u.z = 1; console.log(u);
const q: any = ["a"]; q["two words"] = 2; q[k] = [1]; console.log(q, Object.keys(q));
const o = { list: [1, 2] as any }; o.list.meta = true; console.log(o);
const m: any = [1, 2, 3]; m.x = 1; console.log(m.map((v: number) => v * 2), m.slice(1), [...m], JSON.stringify(m));
`)
}

func TestE2EArrayExpandos4(t *testing.T) {
	assertSameAsNode(t, `const a = [1, 2];
(a as any).foo = 1;
console.log(a, Object.keys(a), (a as any).foo);
for (const key in a) console.log(key);
const s: string[] = ["x"];
Object.assign(s, { tag: "y" });
console.log(s, "tag" in s);
const r = Object.assign([1, 2], { foo: 5 });
console.log(r);
`)
}

func TestE2EComputedAccessorsAssignSources1(t *testing.T) {
	assertSameAsNode(t, `const k = Symbol("k");
class C { [k] = 4; x = 1 }
const a: any = Object.assign({}, new C());
console.log(a[k], a.x, Object.keys(a));
const b = Object.assign({ y: 2 }, new C());
console.log((b as any)[k], b.x, b.y);
const g: any = { get [k]() { return "got"; } };
const c: any = Object.assign({}, g);
console.log(c[k]);
const tgt: any = { set [k](v: any) { console.log("set", v); } };
Object.assign(tgt, { [k]: 9 });
const arr: any = Object.assign([1, 2], { [k]: 3 });
console.log(arr[k], arr.length);
const fr: any = Object.freeze({ [k]: 1 });
try { Object.assign(fr, { [k]: 2 }); } catch (e) { console.log((e as Error).constructor.name); }
console.log(Object.assign({}, "ab", null, undefined, 3));
`)
}

func TestE2EComputedAccessorsAssignSources2(t *testing.T) {
	assertSameAsNode(t, `console.log(Object.assign({}, "ab", null, undefined, 3));
console.log(Object.assign({ z: 1 }, true, "c"));
const o = { ...("ab" as any), q: 1 };
console.log(o);
const p = { ...(null as any), ...(undefined as any), ...(3 as any), r: 2 };
console.log(p);
`)
}

func TestE2ESpreadArraysFunctionsAny(t *testing.T) {
	assertSameAsNode(t, `const k = Symbol("k");
const arr: any = [1, 2]; arr.x = 3; arr[k] = 4;
console.log({ ...arr });
const f: any = function () {}; f.p = 1;
console.log({ ...f, q: 2 });
const typed = [5, 6];
console.log({ ...typed });
console.log({ ...("hi" as any) });
const o: any = { a: 1, [k]: 2 };
console.log({ ...o, b: 3 });
`)
}

func TestE2EArrayDeleteLengthThroughAny1(t *testing.T) {
	assertSameAsNode(t, `const a: any = [1, 2, 3];
delete a[0];
console.log(a, a.length, 0 in a);
const b: any = [1, 2, 3];
b.length = 1;
console.log(b);
const c: any = JSON.parse("[1,2,3]");
c.length = 2; c.tag = "t"; delete c[0];
console.log(c, Object.keys(c));
`)
}

func TestE2EArrayDeleteLengthThroughAny2(t *testing.T) {
	assertSameAsNode(t, `const a: any = [1,2]; try { a.length = -1 } catch (e: any) { console.log(e.constructor.name, e.message) }
try { a.length = 1.5 } catch (e: any) { console.log(e.constructor.name, e.message) }
const u: any = new Uint8Array(2); try { u.length = 1 } catch (e: any) { console.log(e.constructor.name, e.message) }
try { delete u[0] } catch (e: any) { console.log(e.constructor.name, e.message) }
console.log(delete u[5], u);
const b: any = [1,2,3]; b.length = "2"; console.log(b); b.length = 4; console.log(b);
export {}
`)
}

func TestE2EFrozenWriteThroughAny(t *testing.T) {
	assertSameAsNode(t, `const o: any = Object.freeze({ a: 1 });
try { o.a = 2; console.log("silent", o.a); } catch (e: any) { console.log("threw", e.message); }
export {}
`)
}

func TestE2EDynamicArrayHoles(t *testing.T) {
	assertSameAsNode(t, `const c: any = JSON.parse("[1,2,3]");
delete c[1]; c[5] = 9;
console.log(c, c.length, 1 in c, c[1]);
console.log(JSON.stringify(c), String(c), c.join("-"), Object.keys(c));
for (const k in c) console.log("k", k);
const d: any = JSON.parse("[1]"); d.length = 3; console.log(d, [...d]);
const o: any = JSON.parse('{"a":[1,2]}'); delete o.a[0]; console.log(o);
`)
}

// A find-kin callback past a length it shrank sees undefined, as a
// scalar element too.
func TestE2EFindPastShrunkLength(t *testing.T) {
	assertSameAsNode(t, `const a = [1, 2, 3, 4];
a.find((v, i) => { if (i === 0) a.length = 2; console.log("find", i, v); return false; });
const b = [1, 2, 3, 4];
b.findIndex((v, i) => { if (i === 0) b.pop(); console.log("fi", i, v); return false; });
const c = ["a", "b", "c"];
c.findLast((v, i) => { if (i === 2) c.length = 1; console.log("fl", i, v); return false; });
const d = [true, false, true];
d.findLastIndex((v, i) => { if (i === 2) d.length = 0; console.log("fli", i, v); return false; });
const xs = [3, 8, 12, 5];
console.log(xs.find(v => v * 2 > 15), xs.findIndex((v: number) => v % 4 === 0), xs.findLast(v => v + 1 < 7), xs.findLastIndex(v => Math.sqrt(v) > 3));
const big = [1n, 5n, 9n];
console.log(big.find(v => v > 4n));
const fs = [1.5, 2.5];
const fn = (v: number, i: number) => v > 2 && i >= 0;
console.log(fs.find(fn), fs.findIndex(fn));
const bs = [false, true];
console.log(bs.findIndex(b => b), bs.find(b => !b));
`)
}

// A key that is no array index (negative, or 2^32-1 and up) is an ordinary
// own property of the array: it neither grows the array nor reads as an
// element.
func TestE2EArrayNonIndexKeys(t *testing.T) {
	assertSameAsNode(t, `const b: string[] = ["p"];
b[4294967295] = "w";
b[-1] = "n";
console.log(b.length, b[4294967295], b[-1], b[0], b[7], Object.keys(b));
const c: number[] = [1, 2];
c[2 ** 40] = 7;
console.log(c.length, c[2 ** 40], c[5]);
const a: any = [];
a[4294967295] = "x";
a[4294967296] = "y";
a[1e10] = "z";
console.log(a.length, a[4294967295], a[4294967296], a[1e10], Object.keys(a));
`)
}

// A typed function stored as an accessor runs with no arguments: its
// parameter is undefined, the caller's argument list never read.
func TestE2EGetterWithParameterCalledBare(t *testing.T) {
	assertSameAsNodeCompatJS(t, `var obj = {};
var getFunc = function(arg1) { return arg1 === undefined ? 2010 : 0; };
Object.defineProperty(obj, "prop", { get: getFunc });
var desc = Object.getOwnPropertyDescriptor(obj, "prop");
console.log(obj.hasOwnProperty("prop"), obj.prop, desc.get === getFunc);
`)
}
