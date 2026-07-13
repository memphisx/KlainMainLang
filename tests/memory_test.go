package tests

import (
	"testing"
)

// --- Memory.free(x) ---
//
// Stage 1 of the staged manual-memory-management plan in STATUS.md: a raw,
// unsafe, shallow free. String literals are interned as compile-time global
// constants, not malloc'd, so every string test here uses a concatenation
// result (guaranteed heap-allocated) rather than a bare literal.

func TestE2EMemoryFreeString(t *testing.T) {
	assertOutput(t, `
let s: string = "hello " + "world"
console.log(s.length)
Memory.free(s)
console.log(s === null)
`, "11\n1")
}

func TestE2EMemoryFreeArray(t *testing.T) {
	assertOutput(t, `
let arr: number[] = [1, 2, 3, 4, 5]
console.log(arr.length)
Memory.free(arr)
console.log(arr.length)
`, "5\n0")
}

func TestE2EMemoryFreeObject(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
let p: Point = { x: 1, y: 2 }
console.log(p.x)
Memory.free(p)
console.log("done")
`, "1\ndone")
}

func TestE2EMemoryFreeClosureNoCaptures(t *testing.T) {
	assertOutput(t, `
let f: () => number = () => 42
console.log(f())
Memory.free(f)
console.log("done")
`, "42\ndone")
}

func TestE2EMemoryFreeClosureLeavesSharedCaptureIntact(t *testing.T) {
	// The closure's own header+env are freed, but the captured variable's
	// heap-promoted cell (shared with the enclosing scope, ADR-00001) must
	// survive — freeing it would be a real use-after-free for the
	// enclosing scope's own continued use of the variable, not just the
	// user's own responsibility.
	assertOutput(t, `
let counter: number = 0
const inc = (): number => { counter = counter + 1; return counter }
console.log(inc())
console.log(inc())
Memory.free(inc)
console.log(counter)
`, "1\n2\n2")
}

func TestE2EMemoryFreeMap(t *testing.T) {
	assertOutput(t, `
let m: Map<string, number> = new Map<string, number>()
m.set("a", 1)
m.set("b", 2)
console.log(m.size)
Memory.free(m)
console.log("done")
`, "2\ndone")
}

func TestE2EMemoryFreeSet(t *testing.T) {
	assertOutput(t, `
let st: Set<string> = new Set<string>()
st.add("x")
console.log(st.size)
Memory.free(st)
console.log("done")
`, "1\ndone")
}

func TestE2EMemoryFreeGeneralExpression(t *testing.T) {
	// Not just named variables — a directly-evaluated expression works too.
	assertOutput(t, `
interface Point { x: number; y: number }
function makePoint(): Point {
    return { x: 1, y: 2 }
}
Memory.free(makePoint())
console.log("done")
`, "done")
}

func TestE2EMemoryFreeDoubleFreeViaSameVariableIsSafe(t *testing.T) {
	// free(NULL) is a well-defined no-op in C — since the first free nulls
	// out the variable's own storage, a second Memory.free() call on that
	// same variable is harmless. (Freeing the same underlying allocation
	// through two different aliases is still unsafe, by design — this only
	// covers the single-named-variable case.)
	assertOutput(t, `
let arr: number[] = [1, 2, 3]
Memory.free(arr)
Memory.free(arr)
console.log("no crash")
`, "no crash")
}

func TestE2EMemoryFreeUnsupportedTypeRejected(t *testing.T) {
	_, err := parseAndCompile(`
let n: number = 5
Memory.free(n)
`)
	if err == nil {
		t.Fatal("expected a compile error for Memory.free on a number, got none")
	}
}

func TestE2EMemoryFreeWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`Memory.free()`)
	if err == nil {
		t.Fatal("expected a compile error for Memory.free() with no arguments, got none")
	}
}
