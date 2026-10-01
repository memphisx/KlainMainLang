package tests

import "testing"

// An unannotated function whose returns have different kinds of value takes
// the checker's return type: their union, each return read under its own
// narrowing.
func TestE2EUnannotatedReturnUnion(t *testing.T) {
	assertSameAsNode(t, `
function g(x: number | bigint) { if (typeof x === "bigint") return x * 2n; return x * 2; }
console.log(g(3), g(4n));
function k(x: string | number) { if (typeof x === "number") return x + 1; return x.toUpperCase(); }
console.log(k(1), k('a'));
function m(n: number) { if (n > 0) return 'pos'; if (n < 0) return -1; return true; }
console.log(m(1), m(-1), m(0));
function o(n: number) { if (n > 0) return 'x'; return undefined; }
console.log(o(1), o(0));
function q(n: number) { if (n > 0) return 5; return null; }
console.log(q(1), q(0));
function r2(n: number) { if (n > 0) return [1]; return undefined; }
console.log(r2(1), r2(0));
`)
}
