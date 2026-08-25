// WeakMap / WeakSet / WeakRef (TDD-00112) — object-identity-keyed, non-iterable
// collections whose keys/referents don't keep the object alive.
//
// Under -mm=manual (the default) nothing is ever collected, so a weak reference
// behaves like a strong one (`.deref()` never returns null). Under -mm=gc the
// referents are genuinely weak: once no strong reference remains, a collection
// nulls them out (see the disappearing-link path in the docs).

class Session {
  id: number;
  constructor(id: number) { this.id = id; }
}

const alice = new Session(1);
const bob = new Session(2);
const carol = new Session(3);

// ── WeakMap<K, V>: associate data with an object without touching the object ──
const lastSeen = new WeakMap<Session, string>();
lastSeen.set(alice, "09:00");
lastSeen.set(bob, "09:15");

console.log(lastSeen.get(alice)); // 09:00
console.log(lastSeen.get(bob));   // 09:15
console.log(lastSeen.has(carol)); // false
console.log(lastSeen.has(alice)); // true

lastSeen.delete(alice);
console.log(lastSeen.has(alice)); // false

// ── WeakSet<T>: membership tracking keyed on object identity ──────────────────
const active = new WeakSet<Session>();
active.add(alice);
active.add(carol);

console.log(active.has(alice)); // true
console.log(active.has(bob));   // false
active.delete(alice);
console.log(active.has(alice)); // false

// ── WeakRef<T>: hold a reference that doesn't keep the referent alive ─────────
const ref = new WeakRef(bob);
const got = ref.deref();
console.log(got.id); // 2  (bob is still reachable, so deref() yields it)
