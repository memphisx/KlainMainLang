package tests

import (
	"testing"
)

// --- Array/Map/Set/EventEmitter literals as general expressions (TDD-00028) ---
//
// Before this, an array literal (or new Array<T>(n)/new Map<K,V>()/
// new Set<T>()/new EventEmitter<T>()) could only ever appear directly as a
// const/let initializer — not as a call argument, a return value, an
// object-literal field value, or a plain reassignment target. See
// docs/tdd/TDD-00028.md and docs/adr/ADR-00104.md.

func TestE2EArrayLiteralAsCallArgument(t *testing.T) {
	assertOutput(t, `
function first(arr: number[]): number {
  return arr[0];
}
console.log(first([10, 20, 30]));
`, "10")
}

func TestE2EArrayLiteralAsCallArgumentCoercesAgainstDeclaredElementType(t *testing.T) {
	// The declared parameter type is float64[]; the literal's own elements
	// self-infer as i64. Without hint-aware coercion this would either
	// fail to compile (IR type mismatch) or silently reinterpret raw i64
	// bit patterns as doubles — the exact class of bug TDD-00007 already
	// fixed for object literals, now also covered for array literals.
	assertOutput(t, `
function sum(arr: float64[]): float64 {
  return arr[0] + arr[1];
}
console.log(sum([1, 2]));
`, "3")
}

func TestE2EArrayLiteralAsReturnValue(t *testing.T) {
	assertOutput(t, `
function pair(): number[] {
  return [1, 2];
}
const p = pair();
console.log(p[0]);
console.log(p[1]);
console.log(p.length);
`, "1\n2\n2")
}

func TestE2EArrayLiteralAsObjectLiteralFieldValue(t *testing.T) {
	assertOutput(t, `
interface Box {
  data: number[];
}
const box: Box = { data: [5, 6, 7] };
console.log(box.data[0]);
console.log(box.data.length);
`, "5\n3")
}

func TestE2EArrayLiteralPlainReassignment(t *testing.T) {
	assertOutput(t, `
let arr: number[] = [1, 2, 3];
arr = [4, 5];
console.log(arr[0]);
console.log(arr.length);
`, "4\n2")
}

func TestE2EArrayVariableReassignment(t *testing.T) {
	assertOutput(t, `
let arr: number[] = [1, 2, 3];
const other: number[] = [4, 5];
arr = other;
console.log(arr[0]);
console.log(arr.length);
`, "4\n2")
}

func TestE2EArrayVariableReassignmentRejectsConst(t *testing.T) {
	_, err := parseAndCompile(`
const arr: number[] = [1, 2, 3];
arr = [4, 5];
`)
	if err == nil {
		t.Fatal("expected a compile error reassigning a const array variable, got none")
	}
}

func TestE2ESpreadArrayLiteralAsCallArgument(t *testing.T) {
	assertOutput(t, `
function first(arr: number[]): number {
  return arr[0];
}
const base: number[] = [1, 2];
console.log(first([...base, 3, 4]));
console.log(first([0, ...base]));
`, "1\n0")
}

func TestE2ENewArraySizedAsCallArgument(t *testing.T) {
	assertOutput(t, `
function len(arr: number[]): number {
  return arr.length;
}
console.log(len(new Array<number>(5)));
`, "5")
}

// Nested array literals (array-of-arrays, TDD-00029) — previously rejected
// with a clean compile error by TDD-00028's own guard; TDD-00029 replaced
// that with real storage support (boxed elements, see
// codegen/llvm/emit_arrays_core.go's boxArrayValue/loadArrayElem/
// storeArrayElem). See tests/arrays_test.go for indexing/mutation/iteration
// coverage of nested arrays; this one just confirms the literal itself
// compiles and evaluates correctly now.
func TestE2ENestedArrayLiteralCompiles(t *testing.T) {
	assertOutput(t, `
const nested: number[][] = [[1, 2], [3, 4]];
console.log(nested[0][0]);
console.log(nested[1][1]);
`, "1\n4")
}

func TestE2ENewMapAsCallArgument(t *testing.T) {
	assertOutput(t, `
function firstValue(m: Map<string, number>): number {
  return m.get("a");
}
const built = new Map<string, number>();
built.set("a", 42);
console.log(firstValue(built));
`, "42")
}

func TestE2ENewMapAsReturnValue(t *testing.T) {
	assertOutput(t, `
function makeMap(): Map<string, number> {
  return new Map<string, number>();
}
const m = makeMap();
m.set("x", 7);
console.log(m.get("x"));
`, "7")
}

func TestE2ENewSetAsReturnValue(t *testing.T) {
	assertOutput(t, `
function makeSet(): Set<number> {
  return new Set<number>();
}
const s = makeSet();
s.add(9);
console.log(s.has(9));
`, "true")
}

func TestE2ENewEventEmitterAsReturnValue(t *testing.T) {
	assertOutput(t, `
function makeEmitter(): EventEmitter<string> {
  return new EventEmitter<string>();
}
const e = makeEmitter();
e.on("msg", (data: string): void => {
  console.log("got: " + data);
});
e.emit("msg", "hi");
`, "got: hi")
}
