package tests

import "testing"

// TestE2EClassExtendsDate: the instance is the Date (TDD-00247 stage 4) —
// Date's construction, methods and setters on it, an override winning,
// instanceof both ways, conversions, JSON, inspect and `any`; the same
// output as Node.
func TestE2EClassExtendsDate(t *testing.T) {
	assertOutput(t, `class Stamp extends Date {
  label = "s";
  iso(): string { return this.toISOString(); }
}
class Day extends Date {
  constructor(y: number, m: number, d: number) {
    super(Date.UTC(y, m, d));
  }
  override toString(): string { return "Day " + this.toISOString().slice(0, 10); }
  next(): Day {
    const n = new Day(0, 0, 0);
    n.setTime(this.getTime() + 86400000);
    return n;
  }
}
class Bare extends Date {}
const s = new Stamp(0);
console.log(s.iso(), s.label, s.getTime(), s instanceof Date, s instanceof Stamp);
console.log(s);
const d = new Day(2024, 1, 28);
console.log(String(d), d.next().toISOString(), `+"`"+`${d.next()}`+"`"+`);
const b = new Bare(5);
b.setUTCFullYear(2000);
console.log(b.getUTCFullYear(), +b > 0, JSON.stringify({ b }));
const asDate: Date = b;
console.log(asDate.getUTCFullYear(), asDate === b);
const x: any = s;
console.log(x instanceof Date, x instanceof Stamp, Object.prototype.toString.call(s));
const arr: Date[] = [new Bare(2), new Date(1)];
arr.sort((p, q) => p.getTime() - q.getTime());
console.log(arr.map(v => v.getTime()));
console.log(new Date(new Bare(9)).getTime(), new Bare(new Date(7)).getTime());
console.log(b < new Bare(Date.UTC(2001, 0)), Number(new Bare(3)));
`, `1970-01-01T00:00:00.000Z s 0 true true
Stamp 1970-01-01T00:00:00.000Z { label: 's' }
Day 2024-02-28 2024-02-29T00:00:00.000Z Day 2024-02-29
2000 true {"b":"2000-01-01T00:00:00.005Z"}
2000 true
true true [object Date]
[ 1, 2 ]
9 7
true 3`)
}

// TestE2EDateObjectEdges: a Date as an object through clone, class fields,
// defaults, generics, tuples, records, maps, `null` and `any`.
func TestE2EDateObjectEdges(t *testing.T) {
	assertOutput(t, `const a = new Date(86400000);
const b = new Date(86400000);
const c = structuredClone(a); console.log(c.getTime(), c === a, c instanceof Date);
class H { at: Date = new Date(3); when?: Date } const h = new H(); console.log(h.at.getTime(), h.when, h);
function f(d: Date = new Date(4)) { return d.getTime() } console.log(f(), f(a));
function id<T>(x: T): T { return x } console.log(id(a) === a, id(a).getTime());
const tup: [Date, number] = [a, 1]; console.log(tup[0] === a, tup);
const objs = [{ d: a }, { d: b }]; console.log(objs.map(o => o.d.getTime()), objs.filter(o => o.d === b).length);
console.log(`+"`"+`${a.getTime()}`+"`"+` + String(a === b), [a].map(String)[0] === String(a));
let u: Date | null = null; console.log(u === null, u); u = a; console.log(u === a);
const r: Record<string, Date> = { x: a }; console.log(r.x === a, r);
const mm = new Map<string, Date>([["k", a]]); console.log(mm.get("k") === a, mm);
const dd = new Date(a.getTime()); dd.setUTCDate(5); console.log(a.getUTCDate(), dd.getUTCDate());
console.log(Math.max(+a, +b), a.getTime() === +a, isFinite(+a));
const any5: any = a; any5.setTime(1); console.log(a.getTime());
const sorted = [new Date(5), new Date(2)].sort((x, y) => +x - +y); console.log(sorted.map(x => x.getTime()));
console.log(new Date(a).toISOString(), new Date(a.toISOString()).getTime() === a.getTime());
console.log(Object.keys(a), JSON.stringify([a]), typeof a.valueOf());
`, `86400000 false true
3 undefined H { at: 1970-01-01T00:00:00.003Z, when: undefined }
4 86400000
true 86400000
true [ 1970-01-02T00:00:00.000Z, 1 ]
[ 86400000, 86400000 ] 1
86400000false true
true null
true
true { x: 1970-01-02T00:00:00.000Z }
true Map(1) { 'k' => 1970-01-02T00:00:00.000Z }
2 5
86400000 true true
1
[ 2, 5 ]
1970-01-01T00:00:00.001Z true
[] ["1970-01-01T00:00:00.001Z"] number`)
}
