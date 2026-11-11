package tests

import "testing"

func TestE2ECodedErrorsNameTheirCode(t *testing.T) {
	// Node's coded errors read `TypeError [ERR_X]: …` in String(), their
	// stack and inspect; Buffer.alloc checks its size; a stream operator
	// checks its options; an own toString is what String() calls.
	assertSameAsNode(t, `
import { Readable } from 'stream';
const show = (f: () => unknown) => { try { f(); console.log('no throw'); } catch (e: any) { console.log(String(e), e.constructor.name, e.code); } };
show(() => Readable.from([1]).map(1 as any));
show(() => Readable.from([1]).map((x: any) => x, 1 as any));
show(() => Readable.from([1]).map((x: any) => x, { signal: true as any }));
for (const n of [-1, 1.5, NaN, 2 ** 53, "3" as any]) show(() => Buffer.alloc(n).length);
try { Buffer.alloc(-1); } catch (e: any) { console.log(e.stack.split("\n")[0]); }
function mk(): any { const x: any = new TypeError("q"); Object.defineProperty(x, "toString", { value: function (this: any): string { return "Z" + this.message; }, writable: true, enumerable: false, configurable: true }); return x; }
try { throw mk(); } catch (err: any) { console.log(String(err), `+"`${err}`"+`); }
`)
}

func TestE2EDynamicCallsThisArgAndOptional(t *testing.T) {
	// An array method's thisArg with a callback that is no function throws
	// Node's message; a method named like an array's on an any object is
	// that object's; an optional call of an any element skips a nullish one
	// and passes its object as the receiver otherwise.
	assertSameAsNode(t, `
const xs = [1, 2];
const g: any = 0;
try { xs.map(g, 1); } catch (e: any) { console.log(e.message); }
const f = function (this: any, x: number) { return x + this.k; };
console.log(xs.map(f, { k: 1 }));
const r: any = { map(a: any, b: any) { return typeof b; } };
console.log(r.map(0, 1));
const t: any[] = [1, undefined, () => 7, null];
console.log(t[1]?.(), t[2]?.(), t[3]?.("x"));
const o: any = { k: 5, m() { return this.k; } }; const key = "m";
console.log(o[key]?.(), o["zz"]?.());
`)
}

func TestE2EClosureAssignedLetJS(t *testing.T) {
	// JavaScript: a let a closure assigns (a Promise executor's resolve)
	// holds what the closure stores, wherever it is read.
	assertSameAsNodeCLIJS(t, `
function mk(n) {
  const arr = [];
  for (let i = 0; i < n; i++) {
    let res;
    const promise = new Promise((resolve) => {
      if (i === 0) { res = resolve; return; }
      res = () => arr[i - 1][0].then(resolve);
    });
    arr.push([promise, res]);
  }
  return arr;
}
const ps = mk(3);
console.log(typeof ps[0][1], typeof ps[1][1]);
const [p, r] = ps[1];
r(); ps[0][1](); p.then(() => console.log("done"));
`)
}
