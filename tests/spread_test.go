package tests

import "testing"

// --- Spread in call arguments (TDD-00106 / ADR-00335) ---
//
// V1: a spread fills a callee's rest parameter, optionally after fixed args:
// f(...arr), f(a, ...arr). Everything else is a clean compile error.

func TestE2ESpreadIntoRest(t *testing.T) {
	assertOutput(t, `
function sum(...nums: number[]): number {
  let t = 0
  for (const n of nums) t += n
  return t
}
const arr = [1, 2, 3, 4, 5]
console.log(sum(...arr))
console.log(sum(10, 20))
`, "15\n30")
}

func TestE2ESpreadAfterFixedArgs(t *testing.T) {
	assertOutput(t, `
function label(prefix: string, ...items: number[]): number {
  return prefix.length + items.length
}
const a = [1, 2, 3]
console.log(label("hi", ...a))
`, "5")
}

func TestE2ESpreadStringArray(t *testing.T) {
	assertOutput(t, `
function join(...parts: string[]): string {
  let s = ""
  for (const p of parts) s += p
  return s
}
console.log(join(...["a", "b", "c"]))
`, "abc")
}

func TestE2ESpreadFixedArityRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(a: number, b: number): number { return a + b }
const arr = [1, 2]
f(...arr)
`)
	if err == nil {
		t.Fatal("expected a compile error spreading into a fixed-arity function, got none")
	}
}

func TestE2ESpreadMultipleRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(...xs: number[]): number { return 0 }
const a = [1]; const b = [2]
f(...a, ...b)
`)
	if err == nil {
		t.Fatal("expected a compile error for multiple spread arguments, got none")
	}
}

func TestE2ESpreadNotLastRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(...xs: number[]): number { return 0 }
const a = [1]
f(...a, 9)
`)
	if err == nil {
		t.Fatal("expected a compile error for a spread that isn't the last argument, got none")
	}
}

// Arrow functions can now have a rest parameter and receive a spread (the
// detection + closure-call spread forwarding fixes).
func TestE2EArrowRestParamAndSpread(t *testing.T) {
	assertOutput(t, `
const sum = (...nums: number[]): number => {
  let t = 0
  for (const n of nums) t += n
  return t
}
console.log(sum(1, 2, 3))
const arr = [4, 5, 6]
console.log(sum(...arr))
const withFixed = (label: string, ...nums: number[]): number => label.length + nums.length
console.log(withFixed("hi", ...arr))
`, "6\n15\n5")
}
