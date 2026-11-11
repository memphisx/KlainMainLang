package tests

import "testing"

// Mixins: a function returning a class that extends its parameter,
// applied to classes, composed, written as an arrow, and called in an
// `extends` clause (ADR-01979).
func TestE2EMixins(t *testing.T) {
	assertSameAsNode(t, `type Ctor = new (...a: any[]) => {};
function Timestamped<B extends Ctor>(Base: B) {
  return class extends Base { ts = 5; stamp() { return this.ts; } };
}
const Tagged = <B extends Ctor>(Base: B) => class extends Base { tag = "t"; };
class User { name = "u"; constructor(n?: string) { if (n) this.name = n; } greet() { return "hi " + this.name; } }
class Item { id = 7; }
const TU = Timestamped(User);
const t = new TU("ann");
console.log(t.name, t.ts, t.stamp(), t.greet(), t instanceof User, t instanceof TU);
const TI = Timestamped(Item);
const i = new TI();
console.log(i.id, i.stamp(), i instanceof Item, i instanceof User);
const TTU = Tagged(TU);
const x = new TTU("bo");
console.log(x.name, x.ts, x.tag, x.greet(), x instanceof User);
class Admin extends Timestamped(User) { role = "root"; }
const a = new Admin("cy");
console.log(a.name, a.ts, a.role, a.stamp(), a instanceof User, a instanceof Admin);
`)
}
