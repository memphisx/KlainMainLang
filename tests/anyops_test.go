package tests

import (
	"strings"
	"testing"
)

// --- TDD-00076 A2/A1 on the NaN-boxed value (TDD-00156): runtime operator
// dispatch and implicit-any under -compat=js; strict keeps the rejections. ---

func TestE2EAnyOpsArithmetic(t *testing.T) {
	assertOutputCompatJS(t, `
function add(a, b) { return a + b }
console.log(add(2, 3))
console.log(add("con", "cat"))
console.log(add("x=", 5))
console.log(add(null, 1), add(true, 1))
console.log(add(2.5, 0.25))
function id(v) { return v }
console.log(id(7) * id("6"))
console.log(id(7) - "2", id(10) / id(4), id(7) % id(4))
`, "5\nconcat\nx=5\n1 2\n2.75\n42\n5 2.5 3")
}

func TestE2EAnyOpsBitwise(t *testing.T) {
	// Bitwise and shift operators on any-typed operands (a value flowing
	// through an untyped parameter under -compat=js): ToInt32 both operands,
	// compute in the 32-bit domain, yield a Number — the same semantics a typed
	// `number & number` has. Previously rejected as "operator on any/unknown is
	// not yet supported", the single largest -compat=js Test262 regression
	// bucket (decodeURI/compound-assignment and friends).
	assertOutputCompatJS(t, `
function id(v) { return v }
console.log(id(6) & id(3))
console.log(id(5) | id(2))
console.log(id(6) ^ id(3))
console.log(id(1) << id(4))
console.log(id(256) >> id(2))
console.log(id(-1) >>> id(0))
`, "2\n7\n5\n16\n64\n4294967295")
}

func TestE2EAnyOpsNaNAndUndefined(t *testing.T) {
	assertOutputCompatJS(t, `
let u
console.log(u)
function add(a, b) { return a + b }
console.log(add(u, 1))
console.log(add("j", "k") * 2)
console.log(-add(1, 2))
`, "undefined\nNaN\nNaN\n-3")
}

func TestE2EAnyOpsRelational(t *testing.T) {
	assertOutputCompatJS(t, `
function id(v) { return v }
console.log(id(3) < id(5), id(5) <= id(5), id(7) > id(9))
console.log(id("a") < id("b"), id("b") >= id("ba"))
console.log(id("10") < id(9), id("10") > id(9))
`, "true true false\ntrue false\nfalse true")
}

func TestE2EAnyOpsTruthiness(t *testing.T) {
	assertOutputCompatJS(t, `
function id(v) { return v }
function truthy(v) { if (v) { return "T" } return "F" }
console.log(truthy(id(0)), truthy(id(1)), truthy(id("")), truthy(id("s")))
console.log(truthy(id(null)), truthy(id(true)), truthy(id(false)))
let u
console.log(truthy(u), !id(0), !id("x"))
`, "F T F T\nF T F\nF true false")
}

func TestE2EAnyOpsCompoundAssign(t *testing.T) {
	assertOutputCompatJS(t, `
function id(v) { return v }
let acc = id(1)
acc += 5
acc *= 2
acc -= "2"
console.log(acc)
let s = id("a")
s += "b"
console.log(s)
`, "10\nab")
}

func TestE2EAnyOpsMixedConcrete(t *testing.T) {
	// Mixed concrete pairs route through the dispatch too (`-compat=js`).
	assertOutputCompatJS(t, `
let n = 7
console.log(n * "4")
console.log("id-" + 5)
console.log(true + 1)
`, "28\nid-5\n2")
}

func TestStrictModeAnyOpsStillRejected(t *testing.T) {
	_, err := parseAndCompile(`
let a: any = 1
let b: any = 2
console.log(a * b)
`)
	if err == nil || !strings.Contains(err.Error(), "operator '*' on any/unknown") {
		t.Fatalf("expected strict-mode operator rejection, got: %v", err)
	}
}

func TestE2EAnyOpsProtoMethodArithmetic(t *testing.T) {
	// The Stage-4 limitation this work removes: prototype methods doing
	// numeric work on boxed fields.
	assertOutputCompatJS(t, `
function Point(x, y) { this.x = x; this.y = y }
Point.prototype.dist = function() {
  return Math.sqrt((this.x * this.x) + (this.y * this.y))
}
Point.prototype.scale = function(k) { this.x *= k; this.y *= k; return this }
const p = new Point(3, 4)
console.log(p.dist())
`, "5")
}
