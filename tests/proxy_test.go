package tests

import "testing"

// A read through a proxy returned by an unannotated function or arrow is
// the get trap's result, whatever the target declares; a function reading
// the module's proxy reads its box.
func TestE2EProxyReadThroughReturnTS(t *testing.T) {
	assertSameAsNode(t, `
const target = { a: 1 };
const p = new Proxy(target, { get: (_t, k) => (k === "a" ? "str" : 7) });
function f() { return p.a; }
const r = f();
console.log(r, typeof r);
const g = () => p.a;
console.log(g(), typeof g());
function h(): number { return (p as any).b; }
console.log(h());
`)
}
