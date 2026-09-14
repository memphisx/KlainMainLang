package tests

import "testing"

// Symbol-keyed dynamic-object properties (ADR-00930): `obj[sym]` uses
// ToPropertyKey — the symbol stays a unique key (it is NOT stringified, which
// would throw "Cannot convert a Symbol value to a string"). set/get/has/delete
// round-trip on the symbol's pointer identity, and symbol keys are excluded
// from the string enumeration (Object.keys / getOwnPropertyNames / for...in).

func TestE2ESymbolKeySetGet(t *testing.T) {
	assertOutput(t, `
const obj: any = {};
const s = Symbol("x");
obj[s] = 42;
console.log(obj[s]);
obj[s] = "hi";
console.log(obj[s]);
`, "42\nhi")
}

func TestE2ESymbolKeyDistinctIdentity(t *testing.T) {
	assertOutput(t, `
const obj: any = {};
const a = Symbol("same");
const b = Symbol("same");
obj[a] = 1;
console.log(obj[a]);
console.log(obj[b] === undefined);
`, "1\ntrue")
}

func TestE2ESymbolKeyHasOwn(t *testing.T) {
	assertOutput(t, `
const obj: any = {};
const s = Symbol();
console.log(Object.hasOwn(obj, s));
obj[s] = 0;
console.log(Object.hasOwn(obj, s));
`, "false\ntrue")
}

func TestE2ESymbolKeyExcludedFromEnumeration(t *testing.T) {
	assertOutput(t, `
const obj: any = {};
const s = Symbol("hidden");
obj[s] = 99;
obj.a = 1;
obj.b = 2;
console.log(Object.keys(obj).join(","));
console.log(Object.getOwnPropertyNames(obj).join(","));
const seen: string[] = [];
for (const k in obj) seen.push(k);
console.log(seen.join(","));
`, "a,b\na,b\na,b")
}
