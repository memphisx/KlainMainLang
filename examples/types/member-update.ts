// `++` / `--` on a member or index target (not just a plain identifier).
// Each desugars to the equivalent compound assignment (`target += 1` / `-= 1`);
// prefix yields the new value, postfix the old one. See docs/status/LANGUAGE-CONSTRUCTS.md.

// --- instance field ---
class Counter {
  value = 0
  tick(): void { this.value++ }
}
const c = new Counter()
c.tick()
c.tick()
console.log(c.value)      // 2

// --- object field, prefix vs postfix return value ---
const o = { k: 5 }
console.log(o.k++)        // 5  (postfix returns the old value)
console.log(o.k)          // 6
console.log(++o.k)        // 7  (prefix returns the new value)
o.k--
console.log(o.k)          // 6

// --- array index target, including a side-effecting index ---
const a = [10, 20, 30]
a[0]++
console.log(a[0])         // 11

let i = 0
a[i++] = 99               // writes a[0], then i becomes 1
console.log(a[0])         // 99
console.log(i)            // 1

// --- static field ---
class Ids {
  static next = 100
  static take(): number { return Ids.next++ }
}
console.log(Ids.take())   // 100
console.log(Ids.take())   // 101
console.log(Ids.next)     // 102
