package tests

import "testing"

func TestE2EIdentityWrapperInThisTypedSlot(t *testing.T) {
	// A generic identity wrapper around an arrow, in a property whose type
	// declares a `this` parameter: called through `.call`, the arrow gets
	// its own arguments, `null` included; explicit type arguments too.
	assertSameAsNode(t, `
function wrap<T extends (...a: any[]) => any>(fn: T): T { const w: any = (...a: any[]) => fn(...a); return w; }
interface O { d?: (this: { n: number }, e: Error | null, cb: () => void) => void }
const o: O = { d: wrap((e: any, cb: any) => { console.log(e, typeof cb); cb(); }) };
o.d!.call({ n: 1 }, null, () => console.log("cb ran"));
type F = (a: number) => void;
const h = wrap<F>((a) => console.log(a + 1));
h(1);
`)
}

func TestE2EPipeIntoReadableErrorAutoDestroys(t *testing.T) {
	// An 'error' emitted on a pipe's Readable destination with no listener
	// of its own destroys it (autoDestroy), as Node's pipe does, instead of
	// throwing.
	assertSameAsNode(t, `
import { Readable } from 'stream';
let n = 0;
const r = new Readable({ read() { r2.emit('error', new Error('fail')); } });
const r2 = new Readable({ autoDestroy: true, destroy(err: Error | null, cb: (e?: Error | null) => void) { n++; console.log('destroy', err?.message); cb(); } });
r2.on('close', () => console.log('close', n));
r.pipe(r2 as any);
`)
}
