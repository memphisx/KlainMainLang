package tests

import "testing"

// A key missing from a JSON input has no property, and a `declare` field
// has none until assigned: a required field's presence bit.
func TestE2ERequiredKeyPresence(t *testing.T) {
	assertSameAsNode(t, `
interface T { t: string; n: number; o: { a: number } | null; b?: string }
const p: T = JSON.parse('{}');
console.log(p, Object.keys(p), JSON.stringify(p), 't' in p, p.t, p.n);
const q: T = JSON.parse('{"t":"x","n":1,"o":null}');
console.log(q, Object.keys(q), JSON.stringify(q));
class A { declare x: number; y = 1; declare s: string; set() { this.x = 5; } }
const a = new A();
console.log(a, Object.keys(a), 'x' in a, a.x);
a.set();
console.log(a, Object.keys(a), JSON.stringify(a));
`)
}

// An object lists its string keys in creation order: a literal's, a JSON
// input's, a key deleted and assigned again last.
func TestE2EObjectKeyOrder(t *testing.T) {
	assertSameAsNode(t, `
interface T { t: string; n: number }
const lit: T = { n: 2, t: 'a' };
console.log(lit, Object.keys(lit), Object.values(lit), Object.entries(lit), JSON.stringify(lit));
const j: T = JSON.parse('{"n":1,"t":"x"}');
console.log(j, Object.keys(j), JSON.stringify(j));
interface U { a: number; b?: number; c: number }
const o: U = { a: 1, b: 2, c: 3 };
delete o.b; o.b = 9;
console.log(o, Object.keys(o), JSON.stringify(o));
for (const k in o) console.log('key', k);
interface N { z: string; 2: string; 1: string }
const nn: N = { z: 'z', 2: 'two', 1: 'one' };
console.log(nn, Object.keys(nn));
interface S { a: number; b: number; c?: number }
const js: S = JSON.parse('{"c":3,"b":2,"a":1}');
console.log({ ...js }, { x: 1, ...js, a: 9 }, Object.keys({ b: 0, ...js }));
`)
}

// A value typed as a class may be another class's instance (a cast through
// any): a method call runs the instance's own method, by name.
func TestE2EMethodCallOnForeignClassInstance(t *testing.T) {
	assertSameAsNode(t, `
class A { m(): string { return "A.m"; } k(x: number): number { return x + 1; } }
class B { m(): string { return "B.m"; } k(x: number): number { return x * 10; } }
class C extends A { m(): string { return "C.m"; } }
const a = (new B() as any) as A;
console.log(a.m(), a.k(4));
const c: A = new C();
console.log(c.m(), c.k(4), new A().m());
const arr: A[] = [new A(), new C(), new B() as any];
console.log(arr.map(x => x.m()).join(","));
`)
}

// A call of an abstract method on the abstract class's type, where another
// class has a method of the name: the instance's override runs, also
// through the dynamic call a foreign instance takes.
func TestE2EAbstractMethodCallWithForeignNamesake(t *testing.T) {
	assertSameAsNode(t, `
abstract class Shape { abstract area(): number; describe(): string { return "area " + this.area(); } }
class Square extends Shape { side: number; constructor(side: number) { super(); this.side = side; } area(): number { return this.side * this.side; } }
class Circle { r: number; constructor(r: number) { this.r = r; } area(): number { return 3 * this.r * this.r; } }
const shapes: Shape[] = [new Square(2), new Circle(1) as any as Shape];
for (const s of shapes) console.log(s.area());
console.log(new Square(3).describe());
export {};
`)
}
