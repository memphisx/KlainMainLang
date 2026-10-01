package tests

import "testing"

// Builtin functions referenced by value — an @intrinsic or a global object's
// @lower method (TDD-00230 P3.2): one shared function per builtin, named as
// the builtin is.

func TestE2EBuiltinFunctionValues(t *testing.T) {
	assertSameAsNode(t, `
console.log(['1.5', '2', '3'].map(parseFloat), ['10', '10', '10'].map(parseInt), [4, 9].map(Math.sqrt));
const pf = Number.parseFloat;
console.log(pf('2.5'), Number.parseFloat === parseFloat, Number.parseInt === parseInt, Number.isNaN === isNaN);
const fl = Math.floor, r = Math.sqrt;
console.log(fl(2.7), fl.name, fl.length, r(16), r.name, r === Math.sqrt);
console.log(parseInt.name, parseInt.length, Math.max.length, String.fromCharCode.length, typeof isFinite);
const mx = Math.max;
console.log(mx(1, 5, 3), [1.2, -3.7].map(Math.abs), [NaN, 1].filter(Number.isNaN));
console.log([65, 66].map(String.fromCharCode).map(s => s.length), [-2].map(Math.hypot));
console.log(['a b', 'é'].map(encodeURIComponent), [btoa('x')].map(atob), encodeURIComponent.name);
console.log(globalThis.Math.max(3, 7), [1.5].map(globalThis.parseFloat), [4].map(globalThis.Math.sqrt), globalThis.Number.isNaN(NaN));
console.log(Math.max.apply(null, [1, 9, 3]), Math.floor.call(null, 2.5), parseInt.bind(null)('10'), Math.pow.bind(null, 2)(10));
function g(a: any, b: any) { return String(a) + String(b); }
const gb = g.bind(null, [1, 2]);
console.log(gb({ x: 1 }), gb("z"), Reflect.apply(Math.max, null, [1, 7, 3]), Reflect.apply(parseInt, undefined, ["ff", 16]));
`)
}

// Array methods pass a callback the index and the array itself when it
// takes them.
func TestE2EArrayCallbackIndexAndArray(t *testing.T) {
	assertSameAsNode(t, `
console.log([1, 2].map((x, i, a) => a.length + i), [1, 2].filter((x, i, a) => a.length > i + 1));
console.log([5, 6].reduce((acc, x, i) => acc + i, 0), [5, 6].reduceRight((acc, x, i, a) => acc + i * a.length, 0));
console.log([5, 6].some((x, i) => i === 1), [5, 6].every((x, i) => i < 1), [5, 6].find((x, i, a) => a[i] === 6));
console.log([5, 6].findIndex((x, i) => i === 1), [5, 6].findLast((x, i) => i === 0), [5, 6].findLastIndex((x, i, a) => a.length === 2));
console.log([65].map((...a: any[]) => a.length));
const t = new Uint8Array([1, 2, 3]), b = Buffer.from('ab');
console.log(t.filter(x => x > 1), b.map(x => x + 1), t.map((x, i, a) => a.length));
`)
}

// Math and String.fromCharCode ToNumber their arguments — an array's
// primitive is its join — and take any number of them, a spread included.
func TestE2EMathVariadicToNumber(t *testing.T) {
	assertSameAsNode(t, `
const a: any = [-2]; const o: any = { valueOf() { return 7; } }; const b: any[] = [2, 'x'];
console.log(+a, Math.abs(a), Math.max(o, 1), a * 1, isNaN(a), isFinite(a), Number(a), a == '-2');
console.log(Math.max(...[3, 9, 1]), Math.min(0, ...[3, 9]), Math.max(...b), Math.max('4' as any, 3));
console.log(Math.hypot(), Math.hypot(3, 4), Math.hypot(...[3, 9, 1]), Math.hypot(NaN, Infinity), Math.hypot(1e200, 1e200), Math.hypot(3, 4, 5, 6));
const u: any[] = ['65', 66, true];
console.log(String.fromCharCode(...u), String.fromCharCode('67' as any), String.fromCharCode(...[]), String.fromCodePoint(...[72, 105]));
`)
}
