// A class field whose initializer references `this` is typed as the class
// instance itself — a self-reference. `Object.freeze(this)` freezes the
// instance mid-construction, so the next field initializer's write throws a
// TypeError (real strict-mode JS behavior). See ADR-00955.

class FrozenAtBirth {
  self = Object.freeze(this);
  label = "never assigned";
}

try {
  new FrozenAtBirth();
  console.log("unexpected: no throw");
} catch (e) {
  console.log(e instanceof TypeError ? "froze mid-construction: TypeError" : "wrong error kind");
}

// A self-referential field that does NOT freeze works normally and aliases the
// same instance.
class Node2 {
  me = this;
  value = 42;
}
const n = new Node2();
console.log(n.me === n);
console.log(n.value);
