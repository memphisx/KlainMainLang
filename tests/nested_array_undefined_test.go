package tests

import "testing"

// TDD-00221: an element-absence op (`.pop`/`.shift`/`.at`/`.find`/`.findLast`)
// on a `T[][]` receiver returns a real `T[] | undefined`. A miss is a null
// data-ptr aggregate; every value-producing / coercion op must read it as
// `undefined`, not as the empty array `[]` (or, for `typeof`, must stay a
// runtime answer so a *present* element is still "object"). A genuine empty
// inner `[]` (non-null header) stays present throughout.

// typeof is a runtime answer: present nested array → "object", miss →
// "undefined". (Previously a static "undefined" for both.)
func TestE2ENestedArrayUndefinedTypeof(t *testing.T) {
	assertOutput(t, `
const e: number[][] = [[1, 2], [3, 4]];
const empty: number[][] = [];
console.log(typeof e.at(0));   // present
console.log(typeof e.at(9));   // miss
console.log(typeof empty.pop());
`, "object\nundefined\nundefined")
}

// String()/template render a miss as "undefined", a present inner array as its
// comma join.
func TestE2ENestedArrayUndefinedString(t *testing.T) {
	assertOutput(t, `
const e: number[][] = [[1, 2]];
const empty: number[][] = [];
console.log(String(e.at(0)));
console.log(String(empty.pop()));
console.log(` + "`${e.at(0)}|${empty.pop()}`" + `);
`, "1,2\nundefined\n1,2|undefined")
}

// JSON.stringify: top-level miss is the value `undefined`; a present array
// serializes; an array element miss is `null`; an object field miss is dropped.
func TestE2ENestedArrayUndefinedJSON(t *testing.T) {
	assertOutput(t, `
const empty: number[][] = [];
const e: number[][] = [[1, 2]];
console.log(JSON.stringify(empty.pop()));
console.log(JSON.stringify(e.at(0)));
console.log(JSON.stringify([empty.pop()]));
console.log(JSON.stringify({ a: empty.pop(), b: 1 }));
`, "undefined\n[1,2]\n[null]\n{\"b\":1}")
}

// ?? treats a miss (null data-ptr) as nullish and falls through to the right
// operand; a present array (including a real empty []) is kept.
func TestE2ENestedArrayUndefinedNullish(t *testing.T) {
	assertOutput(t, `
const empty: number[][] = [];
const e: number[][] = [[7, 8]];
console.log(JSON.stringify(empty.pop() ?? [9]));
console.log(JSON.stringify(e.at(0) ?? [9]));
const hasEmpty: number[][] = [[]];
console.log(JSON.stringify(hasEmpty.at(0) ?? [9]));
`, "[9]\n[7,8]\n[]")
}

// console.log renders a miss as "undefined" at top level and nested inside a
// container; a genuine empty inner [] stays "[]".
func TestE2ENestedArrayUndefinedConsoleLog(t *testing.T) {
	assertOutput(t, `
const empty: number[][] = [];
const e: number[][] = [[1, 2]];
console.log(empty.pop());
console.log(e.at(0));
console.log([e.at(9), e.at(0)]);
const hasEmpty: number[][] = [[]];
console.log(hasEmpty.at(0));
`, "undefined\n[ 1, 2 ]\n[ undefined, [ 1, 2 ] ]\n[]")
}

// === / == / truthiness / Array.isArray on a miss vs a present (incl. empty)
// inner array — a miss is undefined (=== undefined true, == null true, falsy,
// not an array); a present empty [] is a real present array.
func TestE2ENestedArrayUndefinedIdentity(t *testing.T) {
	assertOutput(t, `
const empty: number[][] = [];
const miss = empty.pop();
console.log(miss === undefined);   // true
console.log(miss === null);        // false (undefined !== null)
console.log(miss == null);         // true
console.log(miss ? "T" : "F");     // F
console.log(Array.isArray(miss));  // false
const hasEmpty: number[][] = [[]];
const present = hasEmpty.at(0);
console.log(present === undefined); // false
console.log(Array.isArray(present)); // true
console.log(present ? "T" : "F");    // [] is truthy -> T
`, "true\nfalse\ntrue\nF\nfalse\nfalse\ntrue\nT")
}
