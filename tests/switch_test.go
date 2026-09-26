package tests

import "testing"

// A case label of another type than the discriminant matches as `===` does:
// `switch (null) { case 0: }`, mixed literal kinds, null against undefined.
func TestE2ESwitchMixedTypeCases(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
function k(v) { switch (v) { case 0: return "zero"; case "0": return "str0"; case null: return "null"; case undefined: return "undef"; case true: return "true"; default: return "dflt" } }
console.log(k(0), k("0"), k(null), k(undefined), k(true), k(1))
switch (null) { case 0: console.log("zero"); break; default: console.log("dflt") }
switch (undefined) { case null: console.log("null"); break; case undefined: console.log("undef") }
switch ("a") { case "a": console.log("A"); case "b": console.log("fall B"); break; case "c": console.log("C") }
switch (true) { case 1 > 2: console.log("no"); break; case 2 > 1: console.log("yes") }
`)
}

// null or undefined against a number or boolean is never equal, and a null
// is never strictly an undefined.
func TestE2ENullishEqualityAgainstScalars(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var n = null, u = undefined, z = 0, t = true
console.log(n === z, n == z, n !== z, u == t, u != z, z === n, n == u, n === u, n !== u)
function f(v) { return v === undefined }
console.log(f(null), f(undefined), f(1))
`)
}
