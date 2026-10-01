package tests

import "testing"

// A bigint held in `any`, and a `bigint | undefined` slot (TDD-00229).

func TestE2EBigIntInAny(t *testing.T) {
	assertSameAsNode(t, `
const x: any = 5n;
console.log(typeof x, x, String(x), `+"`${x}`"+`);
const o: any = { a: 7n, b: [1n, 2] };
console.log(o);
const y: any = 5n;
console.log(x === y, x === (6n as any), x === (5 as any));
const back: bigint = x;
console.log(back + 1n);
try { JSON.stringify(o); } catch (e) { console.log((e as Error).message); }
const arr: any[] = [3n, 'q'];
console.log(arr);
`)
}

func TestE2EBigIntMaybeUndefined(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, bigint>([['a', 5n]]);
const x: bigint | undefined = m.get('a');
const y: bigint | undefined = m.get('zz');
console.log(x, y, x! > 0n, typeof y);
console.log((y as any) > 0n, y === undefined, x === 5n);
try { console.log((y as any) + 1n); } catch (e) { console.log((e as Error).message); }
`)
}

func TestE2EBufferUTF16LE(t *testing.T) {
	// utf16le/ucs2 transcode the UTF-8 string's code points to UTF-16 code
	// units and back (surrogate pairs above the BMP) (TDD-00229).
	assertSameAsNode(t, `
const b = Buffer.from('hé😀', 'utf16le');
console.log(b, b.length, b.toString('utf16le'), b.toString('ucs2'));
console.log(Buffer.from('ok', 'UCS-2' as BufferEncoding));
`)
}

// Relational operators on an `any` holding a bigint compare values, against
// a bigint or a number; bigint Map/Set keys compare and hash by value.
func TestE2EBigIntAnyRelationalAndKeys(t *testing.T) {
	assertOutput(t, `
const x: any = 4310067882n;
const y: any = 5n;
console.log(x > 0n, y > 0n, x < 0n, 0n < x, x > 0, x >= 1n, x === 4310067882n);
const a: bigint[] = [1n, 2n, 2n, 3n, 10n ** 20n, 10n ** 20n];
const s = new Set(a);
console.log(s.size, s.has(2n), s.has(4n), s.has(100000000000000000000n));
const m = new Map<bigint, string>();
m.set(5n, 'five'); m.set(5n, 'FIVE');
console.log(m.size, m.get(5n), [...m.keys()], [...s]);
`, "true true false true true true true\n4 true false true\n1 FIVE [ 5n ] [ 1n, 2n, 3n, 100000000000000000000n ]")
}
