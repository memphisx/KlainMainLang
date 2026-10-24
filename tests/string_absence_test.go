package tests

import "testing"

// A `string` slot's null pointer is undefined and nullRef is null
// (TDD-00257): an out-of-range element passed to a string parameter reads
// undefined, and null from JSON or `any` stays null, through comparison,
// typeof, concatenation, output and JSON.
func TestE2EStringNullAndUndefined(t *testing.T) {
	assertOutput(t, `
const a = ['x'];
function show(label: string, t: string) {
  console.log(label, typeof t, t === undefined, t === null, t == null, t ?? 'D', t?.length, 'c=' + t, `+"`${t}`"+`, JSON.stringify(t), JSON.stringify([t]), JSON.stringify({ t }), [t], t);
  const b: any = t; console.log(label, b === null, b === undefined);
}
show('U', a[5]);
show('N', null as any);
const q: { s: string; arr: string[] } = JSON.parse('{"s":null,"arr":[null,"a"]}');
console.log(q.s === null, q.arr, q.arr[0] === null);
const d = new Date(NaN).toJSON();
console.log(typeof d, d === null);
try { console.log(q.s.length); } catch (e) { console.log((e as Error).message); }
const x: string | null = q.s;
console.log(x, x === null, x ?? 'D', [x]);
`, `U undefined true false true D undefined c=undefined undefined undefined [null] {} [ undefined ] undefined
U false true
N object false true true D undefined c=null null null [null] {"t":null} [ null ] null
N true false
true [ null, 'a' ] true
object true
Cannot read properties of null (reading 'length')
null true D [ null ]`)
}

// A number holding undefined keeps it across a typed return, a parameter,
// and an unassigned class field (BACKLOG #174): only an operator applies
// ToNumber.
func TestE2ENumberUndefinedAcrossBoundaries(t *testing.T) {
	assertOutput(t, `
class K { static #p: number; static get(): number { return K.#p; } #q!: number; q(): number { return this.#q; } }
console.log(K.get(), new K().q());
const a: number[] = [];
function f(): number { return a[3]; }
function id(v: number) { return v; }
console.log(f(), id(a[3]), f() + 1);
class P { x!: number; y = 2; constructor(v?: number) { if (v !== undefined) this.x = v; } }
console.log(new P(), new P(5));
`, `undefined undefined
undefined undefined NaN
P { x: undefined, y: 2 } P { x: 5, y: 2 }`)
}

// A `string | null` holds null as nullRef and undefined as the null pointer,
// like a plain string (TDD-00257 Stage 2).
func TestE2EStringOrNullBothAbsences(t *testing.T) {
	assertSameAsNode(t, `
function f(s: string | null): void {
  console.log(s, typeof s, s === null, s === undefined, s == null, s ?? 'dflt', `+"`${s}`"+`, JSON.stringify(s), JSON.stringify({ s }), String(s));
}
const xs: string[] = ['a'];
f(null);
f('x');
f(xs[5] as any);
let n: string | null = null;
console.log(n, [n], { n });
n = xs[3];
console.log(n, [n], { n }, n === null, n === undefined);
const a: (string | null)[] = ['q', null];
console.log(a, a[1] === null, JSON.stringify(a), a[7] === undefined);
const o: { v: string | null } = JSON.parse('{"v":null}');
console.log(o, o.v === null, typeof o.v);
const m: any = null;
const q: string | null = m;
console.log(q, q === null);
let w: string | null = null;
w ??= 'filled';
console.log(w);
`)
}

// A null string key is distinct from "" in a Map or Set, and the array
// helpers keep it null (TDD-00257 Stage 3).
func TestE2EStringNullInCollections(t *testing.T) {
	assertSameAsNode(t, `
const n: string | null = null;
const xs = ['a', n, 'b'];
console.log(xs.join('-'), xs.indexOf(null), xs.includes(null), [n].toString(), xs.filter(x => x !== null));
const m = new Map<string | null, number>([['', 1], [null, 2]]);
console.log(m.size, m.get(''), m.get(null), m.has(null), m);
const st = new Set<string | null>([null, 'a', '']);
console.log(st.has(null), st.size, [...st]);
console.log([n, 'x'].sort(), Object.entries({ k: n }), String([n]));
`)
}
