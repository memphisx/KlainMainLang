package tests

import "testing"

// A `T | undefined` read where flow proves it present is its value, in a
// conditional branch and behind `&&` as in an if; `a || b` is absent only
// when b can be (ADR-01439).
func TestE2EPresenceNarrowedReads(t *testing.T) {
	assertSameAsNode(t, `
let dflt = 10;
function setMax(n?: number): number {
  const value = n === undefined ? dflt : n;
  dflt = value;
  return n !== undefined && n > 2 ? value * 2 : -value;
}
console.log(setMax(), setMax(3), setMax(1), dflt);
function pick(a?: string, b?: string): string {
  const s = a || b || "";
  return s.length > 0 ? s.toUpperCase() : "(none)";
}
console.log(pick(), pick(undefined, "b"), pick("a"));
const one: number[] = [1];
let [x, y] = one;
console.log(x, y);
`)
}

// A nullable instance or function reference the checker narrows present
// reads as present (`const base = this.base` under a null check); an
// array-pattern position past the source's end stays absent, and an absent
// reference converts to "null" or "undefined" (ADR-01442).
func TestE2ENarrowedNullableRefs(t *testing.T) {
	assertSameAsNode(t, `
class R { scheme = 'file'; host = 'h'; }
class P {
  base: R | null = null;
  cb: (() => void) | null = null;
  run(): string {
    let out = '';
    if (this.base !== null && this.base.scheme === 'file') {
      const base = this.base;
      out += base.host + ':' + String(base) + ';';
    }
    if (this.cb !== null) { const cb = this.cb; cb(); out += typeof cb; }
    return out;
  }
}
const p = new P();
console.log(JSON.stringify(p.run()));
p.base = new R(); p.cb = () => console.log('called');
console.log(p.run());
const xs: R[] = [new R()];
const [a, b] = xs;
console.log(a.host, b === undefined, String(b), `+"`${b}`"+`, b + '');
const [c, d] = [new R()] as R[];
console.log(c.host, d === undefined);
let u: R | undefined = Math.random() > 2 ? new R() : undefined;
let n: R | null = Math.random() > 2 ? new R() : null;
console.log(String(u), String(n), u + '', n + '');
`)
}

// A nullable property the checker narrows present reads as present
// (`[this.#cur]` under `this.#cur !== null`) (ADR-01460).
func TestE2ENarrowedNullableMembers(t *testing.T) {
	assertSameAsNode(t, `
class T { name = 't'; }
class D {
  #cur: T | null = null;
  cb: (() => string) | null = null;
  path(): T[] { return this.#cur !== null ? [this.#cur] : []; }
  run(): string { if (this.cb !== null) return this.cb() + typeof this.cb; return 'none'; }
  set(t: T) { this.#cur = t; }
}
const d = new D();
console.log(d.path().length, d.run());
d.set(new T()); d.cb = () => 'x';
console.log(d.path()[0].name, d.run(), String(d.path()[0]));
function f(x: 'x' | 'y' | undefined, y: 'p' | null) { return x ?? y; }
console.log(f('x', null), f(undefined, null), f(undefined, 'p'));
`)
}
