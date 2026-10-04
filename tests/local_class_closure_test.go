package tests

import "testing"

// A class declared in a function that names the function's locals is a
// class per evaluation, closing over that evaluation's bindings (ADR-01334).
// Expectations are Node's output.

func TestE2ELocalClassReadsLocals(t *testing.T) {
	shadowCase(t, `
function make(b: number, label: string) {
  const scale = 10
  class D {
    v = b * scale
    show(): string { return label + ":" + this.v + ":" + b }
    clone(): D { return new D() }
  }
  const d = new D()
  return d.clone().show() + " " + (d instanceof D)
}
console.log(make(1, "a"), make(2, "b"))
function counterFactory(start: number) {
  class Counter { n = start; inc() { this.n++; return this.n } }
  return new Counter()
}
const c1 = counterFactory(5), c2 = counterFactory(100)
console.log(c1.inc(), c2.inc(), c1.inc(), c1)
`, "a:10:1 true b:20:2 true\n6 101 7 Counter { n: 7 }")
}

func TestE2ELocalClassWritesLocals(t *testing.T) {
	shadowCase(t, `
function tally(start: number) {
  let count = 0
  let name = "t"
  class Ticker {
    tick(): number { count += start; name = name + "!"; return count }
  }
  const t = new Ticker()
  t.tick(); t.tick()
  count++
  const bump = () => { count += 100 }
  bump()
  return count + " " + name + " " + t.tick()
}
console.log(tally(2), tally(5))
function acc(total: number) {
  class Acc { add(n: number) { total += n; return total } }
  const a = new Acc(), b = new Acc()
  a.add(1); b.add(10)
  return total
}
console.log(acc(0), acc(5))
`, "105 t!! 107 111 t!! 116\n11 16")
}

func TestE2ELocalClassSubclassAndCtor(t *testing.T) {
	shadowCase(t, `
function zoo(sound: string) {
  class Animal {
    constructor(public name: string) {}
    speak(): string { return this.name + " says " + sound }
  }
  class Dog extends Animal {
    constructor(name: string) { super(name + "!") }
    speak(): string { return super.speak() + sound.length }
  }
  return new Dog("rex").speak()
}
console.log(zoo("woof"), zoo("arf"))
`, "rex! says woof4 rex! says arf3")
}

func TestE2ELocalClassAsValue(t *testing.T) {
	shadowCase(t, `
function make(b: number, tag: string) {
  let made = 0
  class D {
    v = b
    constructor() { made++ }
    show(): string { return tag + ":" + this.v + ":" + made }
  }
  return D
}
const K1 = make(1, "a"), K2 = make(2, "b")
const x = new K1(), y = new K2(), z = new K1()
console.log(x.show(), y.show(), z.show(), K1 === K2, K1.name, make(3, "c") === make(3, "c"))
console.log(new (make(9, "n"))().show(), x, x instanceof K1, x instanceof K2)
function zoo(sound: string) {
  class Animal { name: string; constructor(name: string) { this.name = name } speak(): string { return this.name + " " + sound } }
  class Dog extends Animal { speak(): string { return super.speak() + "!" } }
  return { Animal, Dog }
}
const z1 = zoo("woof"), z2 = zoo("arf")
const d = new z1.Dog("rex")
console.log(d.speak(), d instanceof z1.Animal, d instanceof z2.Animal, d instanceof z1.Dog, new z2.Animal("x").speak())
`, "a:1:2 b:2:1 a:1:2 false D false\nn:9:1 D { v: 1 } true false\nrex woof! true false true x arf")
}

func TestE2ELocalClassExpression(t *testing.T) {
	shadowCase(t, `
function make(greeting: string) {
  const K = class { hi(name: string) { return greeting + ", " + name } }
  return K
}
const A = make("Hello"), B = make("Hi")
console.log(new A().hi("you"), new B().hi("all"), A === B, A.name)
function counter(start: number) {
  let n = start
  return new (class { next() { return n++ } })()
}
const c = counter(5)
console.log(c.next(), c.next(), counter(0).next())
`, "Hello, you Hi, all false K\n5 6 0")
}

// Each evaluation's class has statics of its own: initializers, static
// blocks and static members reading the evaluation's locals,  in a
// static member, a local subclass's inherited statics, and access through
// the class as a value (TDD-00242).
func TestE2ELocalClassStaticsPerRecord(t *testing.T) {
	shadowCase(t, `
function make(b: number) {
  class D {
    static base = b * 10;
    static count = 0;
    static inc() { D.count++; return D.count + D.base; }
    get v() { return D.base + b; }
  }
  return D;
}
const A = make(1), B = make(2);
console.log(A.base, B.base, A.inc(), A.inc(), B.inc(), new A().v, new B().v);
function f(n: number) {
  class E {
    static k = n;
    static get twice() { return E.k * 2; }
    static { E.k += 1; }
  }
  return E.twice;
}
console.log(f(3), f(5));
function g(tag: string) {
  return class {
    static label = tag + "!";
    static show() { return this.label; }
  };
}
const G1 = g("a"), G2 = g("b");
console.log(G1.show(), G2.show(), G1.label);
`, "10 20 11 12 21 11 22\n8 12\na! b! a!")
	shadowCase(t, `
function make(b: number) {
  class D {
    static base = b * 10;
    static count = 0;
    static inc() { D.count++; return D.count + D.base; }
    bump() { D.count += 100; return D.count; }
  }
  class S extends D {
    static extra = b + 0.5;
    static both() { return S.extra + S.base; }
  }
  return [D, S] as const;
}
const [A, AS] = make(1);
const [B, BS] = make(2);
const K: any = A;
console.log(K.base, K.inc(), A.count, B.count);
console.log(new A().bump(), A.count, B.count, new B().bump());
console.log(AS.both(), BS.both(), AS.extra, BS.base);
console.log(Object.keys(A), Object.keys(B));
console.log(A, B.base);
class Top {
  static n = 1;
  static twice() { return this.n * 2; }
  static { this.n += 4; }
}
console.log(Top.twice(), Top.n);
function counter(start: number) {
  return class {
    static v = start;
    static next() { return ++this.v; }
  };
}
const C1 = counter(10), C2 = counter(20);
console.log(C1.next(), C1.next(), C2.next(), C1.v, C2.v);
`, "10 11 1 0\n101 101 0 100\n11.5 22.5 1.5 20\n[ 'base', 'count' ] [ 'base', 'count' ]\n[class D] { base: 10, count: 101 } 20\n10 5\n11 12 21 12 21")
}
