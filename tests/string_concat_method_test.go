package tests

import "testing"

// String.prototype.concat: the receiver followed by ToString of each argument.
func TestE2EStringConcatMethod(t *testing.T) {
	assertOutput(t, `
const s = "a";
console.log(s.concat());
console.log(s.concat("b"));
console.log(s.concat("b", "c", "d"));
const n: number = 42;
console.log("n=".concat(String(n)).concat("!"));
console.log("".concat("", "").length);
console.log(typeof "a".concat("b"));
function greet(name: string): string { return "hello ".concat(name).toUpperCase().concat("!"); }
console.log(greet("w"));
`, "a\nab\nabcd\nn=42!\n0\nstring\nHELLO W!\n")
}

// Arguments are converted with ToString, not with `+`'s default hint: an
// object's toString wins over its valueOf, null/undefined print as words.
func TestE2EStringConcatMethodToStringsItsArguments(t *testing.T) {
	assertOutput(t, `
console.log("x".concat(1 as any, true as any, null as any, undefined as any, 2.5 as any));
const o = { toString() { return "TS"; }, valueOf() { return 7; } };
console.log("o:".concat(o as any));
console.log("o+:" + o);
const s = "a";
const maybe: string | null = s.length > 5 ? "long" : null;
console.log("m:".concat(maybe as any));
`, "x1truenullundefined2.5\no:TS\no+:7\nm:null\n")
}

func TestE2EStringConcatMethodSpread(t *testing.T) {
	assertOutput(t, `
const parts: string[] = ["p", "q", "r"];
console.log("s:".concat(...parts));
console.log("s:".concat("-", ...parts, "-"));
const nums: number[] = [1, 2, 3];
console.log("n:".concat(...(nums as any)));
`, "s:pqr\ns:-pqr-\nn:123\n")
}

// An array's concat is untouched by the string dispatch.
func TestE2EArrayConcatStillAnArray(t *testing.T) {
	assertOutput(t, `
const a = [1, 2].concat([3], [4, 5]);
console.log(a.length, a.join("+"));
const w = ["x"].concat(["y"]);
console.log(w.join("").concat("!"));
`, "5 1+2+3+4+5\nxy!\n")
}
