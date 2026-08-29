package tests

import "testing"

// `globalThis.X` aliases the ambient global X — the leading `globalThis.` peels
// off and the rest dispatches exactly as the bare form would (ADR-00508).

func TestE2EGlobalThisNamespaceMethod(t *testing.T) {
	assertOutput(t, `console.log(globalThis.JSON.stringify({ a: 1, b: 2 }))`, `{"a":1,"b":2}`)
}

func TestE2EGlobalThisConsole(t *testing.T) {
	assertOutput(t, `globalThis.console.log("hi")`, "hi")
}

func TestE2EGlobalThisMath(t *testing.T) {
	assertOutput(t, `console.log(globalThis.Math.max(3, 7, 5))`, "7")
}

func TestE2EGlobalThisGlobalFunction(t *testing.T) {
	assertOutput(t, `console.log(globalThis.parseInt("42", 10))`, "42")
}

func TestE2EGlobalThisTimer(t *testing.T) {
	assertOutput(t, `globalThis.setTimeout(() => { console.log("fired") }, 0)`, "fired")
}

func TestE2EGlobalThisUnknownGlobalRejected(t *testing.T) {
	_, err := parseAndCompile(`console.log(globalThis.totallyNotAGlobal)`)
	if err == nil {
		t.Fatal("expected a compile error for an unknown globalThis member, got none")
	}
}
