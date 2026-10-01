package tests

import "testing"

// Generic classes: a type parameter's default fills an omitted type
// argument (checker and codegen), `any` is a type argument, getters and
// setters dispatch on an instantiation, `x as T` in a member sees the type
// argument, and a class's type argument is inferred through a generic
// interface parameter (ADR-01202).
func TestE2EGenericClassDefaultsAccessorsAny(t *testing.T) {
	assertSameAsNode(t, `
interface Init<T = any> { detail?: T }
class Box<T = any> {
  #v: T
  constructor(o?: Init<T>) { this.#v = o?.detail as T }
  get value(): T { return this.#v }
  set value(v: T) { this.#v = v }
}
const a = new Box({ detail: { n: 1 } })
console.log(a.value.n)
const b = new Box<number>({ detail: 4 })
b.value = b.value + 1
console.log(b.value)
const c = new Box()
console.log(c.value)
class Pair<K, V = string> { k: K; v: V; constructor(k: K, v: V) { this.k = k; this.v = v } }
const p = new Pair<number>(1, "one")
console.log(p.k, p.v)
class Cast<T = any> { d: T; constructor(o: unknown) { this.d = o as T } }
console.log(new Cast(5).d, new Cast<string>("s").d)
`)
}

// A generic interface or alias used with fewer type arguments than it has
// parameters takes the parameters' defaults (`A` is `A<string>` for
// `type A<T = string>`); before, T resolved to a number.
func TestE2EGenericInterfaceAndAliasDefaults(t *testing.T) {
	assertOutput(t, `
type A<T = string> = T[];
let a: A = ['x', 'y'];
a.push('z');
console.log(a, a.length);
type P<K = number, V = boolean> = { key: K; val: V };
const p: P = { key: 3, val: true };
console.log(p.key + 1, p.val);
interface Opts<T = any> { start: T; step: (acc: T, v: number) => T; }
class D { m(o: Opts): void { const x: any = o; console.log(typeof x, typeof x.step, x.start); } }
new D().m({ start: 0, step: (acc: number, v: number) => acc + v });
`, "[ 'x', 'y', 'z' ] 3\n4 true\nobject function 0")
}
