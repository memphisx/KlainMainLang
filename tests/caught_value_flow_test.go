package tests

import "testing"

// A caught value leaving its catch clause (TDD-00229): passed to an `any`
// parameter, or returned from an un-annotated function.

func TestE2ECaughtValueToAny(t *testing.T) {
	assertSameAsNode(t, `
function show(label: string, e: any) { console.log(label, typeof e, e.name, e.message, e.constructor.name, e.constructor === TypeError); }
try { throw new TypeError("x"); } catch (e) { show("a", e); }
function f() { try { throw new RangeError("y"); } catch (e) { return e; } }
const q: any = f();
console.log(typeof q, q.message, q instanceof RangeError);
try { throw new TypeError("z"); } catch (e: any) { console.log(e.constructor.name, e.constructor === TypeError, e.constructor === RangeError); }
`)
}
