package checker_test

import "testing"

// A function expression's return type that reads the binding its object
// literal initializes is resolved after the binding, as tsc does.
func TestDeferredFunctionExpressionReturn(t *testing.T) {
	for _, tc := range []struct{ src, typ string }{
		{"function f() { return new Error('x') }\nconst o = { m() { return f() }, n: function () { return o.m() } }\no.n", "() => Error"},
		{"const o = { m() { return 1 }, n: () => o.m() + 1 }\no.n()", "number"},
	} {
		if got := typeOfLastLib(t, tc.src); got != tc.typ {
			t.Errorf("%q: got %s, want %s", tc.src, got, tc.typ)
		}
	}
}
