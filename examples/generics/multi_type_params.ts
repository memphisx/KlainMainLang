// Multiple type parameters (TDD-00037): the same V1 monomorphization
// generics.ts already demonstrates for a single `<T>`, extended to `<K, V>`
// and beyond — still unconstrained, still no explicit call-site type
// arguments on a plain function call (inference only).

// --- Generic functions: each type parameter infers independently from its
// own designated parameter position ---

function firstOf<K, V>(k: K, v: V): K {
  return k;
}

console.log(firstOf(1, "seven"));  // 1
console.log(firstOf("a", true));   // a

// --- Generic interfaces: N concrete field types per instantiation ---

interface Pair<K, V> {
  first: K;
  second: V;
}

const p: Pair<number, string> = { first: 1, second: "one" };
console.log(p.first);   // 1
console.log(p.second);  // one

// --- Generic classes: explicit type arguments at the `new` site, same as a
// single-parameter generic class — just N of them now, positional and in
// declaration order (Pair<K, V> means K first, V second). ---

class Entry<K, V> {
  key: K;
  value: V;

  constructor(k: K, v: V) {
    this.key = k;
    this.value = v;
  }

  describe(): K {
    return this.key;
  }
}

const e1 = new Entry<string, number>("age", 30);
const e2 = new Entry<number, boolean>(1, true);
console.log(e1.key);   // age
console.log(e1.value); // 30
console.log(e2.describe()); // 1
