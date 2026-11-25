package tests

import "testing"

// new XArray(v) for an untyped v holding a TypedArray: the same element type
// copies its elements as they are, another converts each one; the copy is
// its own storage either way (ADR-02076).
func TestE2ETypedArrayFromUntypedTypedArray(t *testing.T) {
	assertSameAsNode(t, `const u8: any = new Uint8Array([1, 2, 255, 128]);
const a = new Uint8Array(u8);
a[0] = 9;
console.log(Array.from(a), Array.from(u8 as Uint8Array));
const buf: any = Buffer.from([5, 6, 7]);
const b = new Uint8Array(buf);
buf[0] = 0;
console.log(Array.from(b), b.length, Buffer.isBuffer(b));
const f: any = new Float32Array([NaN, -0, 1.5, Infinity]);
const g = new Float32Array(f);
console.log(Array.from(g), Object.is(g[1], -0));
console.log(Array.from(new Int8Array(u8)), Array.from(new Uint8ClampedArray(new Float64Array([300, -5, 1.5]) as any)));
console.log(Array.from(new Uint16Array(u8)), Array.from(new Float64Array(u8)));
const big: any = new BigInt64Array([1n, -2n]);
console.log(Array.from(new BigInt64Array(big)), Array.from(new BigUint64Array(big)));
const empty: any = new Uint8Array(0);
console.log(new Uint8Array(empty).length);
const sub: any = new Uint8Array([1, 2, 3, 4]).subarray(1, 3);
console.log(Array.from(new Uint8Array(sub)));
`)
}

// A BigInt64Array's or BigUint64Array's elements are bigints wherever they
// are copied out: Array.from (with and without a mapFn), a spread, for-of
// (ADR-02078).
func TestE2EBigIntTypedArrayElementsAsBigints(t *testing.T) {
	assertSameAsNode(t, `const t = new BigInt64Array([1n, -2n]);
const a = Array.from(t);
console.log(a, typeof a[0], a[0] === 1n, a[1] === -2n);
const s = [...t];
console.log(s, s.length, s[1] === -2n);
console.log([...t, 5n], [0n, ...t]);
for (const v of t) console.log(v, typeof v);
console.log(Array.from(t, (x) => x * 2n));
console.log(Array.from(new BigUint64Array([3n, 18446744073709551615n])));
const any: any[] = [...t];
console.log(any, typeof any[0]);
`)
}
