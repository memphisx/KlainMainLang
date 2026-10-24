package tests

import "testing"

// A generic class may extend a class, be extended (with type arguments) and
// be abstract.
func TestE2EGenericClassInheritance(t *testing.T) {
	assertSameAsNode(t, `class A { a = 1 }
class B<T> extends A { v?: T }
const b = new B<string>(); b.v = "s"
console.log(b.a, b.v, b, b instanceof A)
class C<T> { v: T; constructor(v: T) { this.v = v } get(): T { return this.v } }
class D<T> extends C<T> { twice(): T[] { return [this.v, this.v] } }
const d = new D<number>(4)
console.log(d.get() + 1, d.twice(), d instanceof C, d)
class N extends C<number> { inc() { return this.v + 1 } }
console.log(new N(41).inc(), new N(1) instanceof C)
abstract class S<T> { abstract get(): T; show() { return "got " + this.get() } }
class Five extends S<number> { get() { return 5 } }
console.log(new Five().show())
class P<T> extends Promise<T> {}
const p = new P<number>((res) => res(4))
console.log(p.then((v) => v + 1) instanceof P)
p.then((v) => console.log("then", v, p instanceof P))
class R<T> extends Array<T> {}
const r = new R<number>(); r.push(3)
console.log(r, r.map(x => String(x)), r.map(x => x) instanceof R)
`)
}
