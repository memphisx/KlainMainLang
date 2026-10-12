package tests

import "testing"

func TestE2EPromiseThenCatchUnion(t *testing.T) {
	// A then/catch whose fulfilled and rejected paths settle to different
	// types settles to their union, as TypeScript types it (ADR-01417).
	assertOutput(t, `
async function main() {
  const ok: Promise<number> = Promise.resolve(5)
  const bad: Promise<number> = Promise.reject(new RangeError("r"))
  console.log(await ok.catch(e => e.name))
  console.log(await bad.catch(e => e.name))
  console.log(await bad.then(x => x, e => e.name))
  console.log(await ok.then(x => x, e => e.name))
  console.log(await bad.then(undefined, e => e.message))
  console.log(await ok.catch(e => { console.error(e) }))
  const c = await bad.then(x => x * 2, e => -1)
  console.log(c + 1)
}
main()
`, "5\nRangeError\nRangeError\n5\nr\n5\n0")
}

// A handler held in an `any` converts to a callback at run time (a
// non-function is the pass-through), and a named handler taking `any` over
// a promise of a number gets the value boxed.
func TestE2EThenHandlerFromAny(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
let cb = function (v) { console.log('got', v); return v + 1; };
let bad = 3;
let rej = function (r) { console.log('rej', r); };
Promise.resolve(41).then(cb).then(v => console.log('next', v));
Promise.resolve(7).then(bad).then(v => console.log('pass', v));
Promise.reject('boom').then(undefined, rej);
Promise.reject('b2').catch(rej);
async function foo() {
  let x = 'initial value';
  let o = { then: function (f) { x = 'called'; Promise.resolve().then(f); } };
  await false || o;
  console.log(x);
}
foo();
`)
	assertOutput(t, `
const cb = function (v: any) { console.log('got', v); };
Promise.resolve(41).then(cb);
`, "got 41")
}

// A callback parameter typed `T` with T = void takes whatever is passed
// (ADR-01438).
func TestE2EVoidTypedCallbackParam(t *testing.T) {
	assertSameAsNode(t, `
function call<T>(run: (fd: number, done: (err: any, value: T) => void) => void): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    run(3, (err, value) => { if (err) reject(err); else resolve(value); });
  });
}
call<void>((fd, done) => { done(null, undefined); }).then(() => console.log("ok"));
call<void>((fd, done) => { done(new Error("bad " + fd), undefined); }).catch(e => console.log("caught", e.message));
`)
}
