package tests

import (
	"testing"
)

// --- Arrays ---

func TestE2EArrayHOF(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3, 4, 5]
const doubled = nums.map((n: number) => n * 2)
const evens = nums.filter((n: number) => n % 2 === 0)
const sum = nums.reduce((acc: number, n: number) => acc + n, 0)
console.log(doubled[0])
console.log(doubled[4])
console.log(evens.length)
console.log(sum)
`, "2\n10\n2\n15")
}

func TestE2EArrayForEach(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3]
let sum: number = 0
nums.forEach((n: number) => {
    sum += n
})
console.log(sum)
nums.forEach((n: number, i: number) => {
    console.log(i * 100 + n)
})
`, "6\n1\n102\n203")
}

func TestE2EArrayForEachConsoleLogCallback(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "b", "c"]
names.forEach((n) => console.log(n))
`, "a\nb\nc")
}

func TestE2EArrayForEachUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "bb", "ccc"]
let total: number = 0
names.forEach((n) => { total += n.length })
console.log(total)
`, "6")
}

func TestE2EArrayMapUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "bb", "ccc"]
const lengths = names.map((n) => n.length)
console.log(lengths[0])
console.log(lengths[1])
console.log(lengths[2])
`, "1\n2\n3")
}

func TestE2EArrayFilterUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
const shortOnes = names.filter((n) => n.length === 3)
console.log(shortOnes[0])
console.log(shortOnes[1])
`, "bob\ncat")
}

func TestE2EArrayFindUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
console.log(names.find((n) => n.length === 3))
`, "bob")
}

func TestE2EArraySomeEveryUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
console.log(names.some((n) => n.length === 3))
console.log(names.every((n) => n.length <= 5))
`, "1\n1")
}

func TestE2EArrayFindIndexUnannotatedStringParam(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["apple", "bob", "cat"]
console.log(names.findIndex((n) => n.length === 3))
`, "1")
}

func TestE2EArrayReduceUnannotatedStringAccumulatorAndElement(t *testing.T) {
	assertOutput(t, `
const names: string[] = ["a", "bb", "ccc"]
const totalLen = names.reduce((acc, n) => acc + n.length, 0)
console.log(totalLen)
const joined = names.reduce((acc, n) => acc + n, "")
console.log(joined)
`, "6\nabbccc")
}

func TestE2EArraySort(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [3, 1, 4, 1, 5, 9, 2, 6]
nums.sort()
console.log(nums[0])
console.log(nums[7])
const desc: number[] = [3, 1, 4, 1, 5]
desc.sort((a: number, b: number) => b - a)
console.log(desc[0])
console.log(desc[4])
`, "1\n9\n5\n1")
}

func TestE2EForOf(t *testing.T) {
	assertOutput(t, `
const words: string[] = ['apple', 'banana', 'cherry']
let out: string = ''
for (const w of words) {
    out = out + w[0]
}
console.log(out)
`, "abc")
}

// --- .length on non-variable array expressions ---

func TestE2ELengthOnArraySlice(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3, 4, 5]
console.log(nums.slice(2).length)
`, "3")
}

// --- Indexing into non-variable array expressions ---

func TestE2EIndexOnArraySlice(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [10, 20, 30, 40]
console.log(arr.slice(1)[0])
console.log(arr.slice(1)[2])
`, "20\n40")
}

// --- arr.indexOf / arr.includes ---

func TestE2EArrayIndexOf(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [10, 20, 30, 20, 40]
console.log(nums.indexOf(20))
console.log(nums.indexOf(99))
const words: string[] = ['foo', 'bar', 'baz']
console.log(words.indexOf('bar'))
console.log(words.indexOf('nope'))
`, "1\n-1\n1\n-1")
}

func TestE2EArrayIncludes(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [10, 20, 30]
console.log(nums.includes(20))
console.log(nums.includes(99))
`, "1\n0")
}

// --- arr.findIndex ---

func TestE2EArrayFindIndex(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [10, 20, 30, 40]
console.log(nums.findIndex((n: number) => n > 25))
console.log(nums.findIndex((n: number) => n > 999))
`, "2\n-1")
}

// --- arr.concat ---

func TestE2EArrayConcat(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const b: number[] = [4, 5, 6]
const c = a.concat(b)
console.log(c.length)
console.log(c[0])
console.log(c[5])
`, "6\n1\n6")
}

// --- arr.reverse ---

func TestE2EArrayReverse(t *testing.T) {
	assertOutput(t, `
const r: number[] = [1, 2, 3, 4, 5]
r.reverse()
console.log(r[0])
console.log(r[4])
`, "5\n1")
}

// --- arr.fill ---

func TestE2EArrayFill(t *testing.T) {
	assertOutput(t, `
const f: number[] = [0, 0, 0, 0, 0]
f.fill(7)
console.log(f[0])
console.log(f[4])
const g: number[] = [0, 0, 0, 0, 0]
g.fill(9, 1, 3)
console.log(g[0])
console.log(g[1])
console.log(g[3])
`, "7\n7\n0\n9\n0")
}

// --- arr.at ---

func TestE2EArrayAt(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [10, 20, 30]
console.log(arr.at(0))
console.log(arr.at(-1))
console.log(arr.at(1))
`, "10\n30\n20")
}

// --- Array.isArray ---

func TestE2EArrayIsArray(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3]
console.log(Array.isArray(nums))
console.log(Array.isArray('hello'))
`, "1\n0")
}

// --- Array bounds checking ---

func TestE2EArrayIndexOutOfBoundsReadThrows(t *testing.T) {
	src := `
const arr: number[] = [1, 2, 3]
try {
    console.log(arr[5])
} catch (e) {
    console.log("caught: " + e.message)
}
`
	assertOutput(t, src, "caught: Array index out of bounds")
}

func TestE2EArrayIndexOutOfBoundsWriteThrows(t *testing.T) {
	src := `
const arr: number[] = [1, 2, 3]
try {
    arr[5] = 99
} catch (e) {
    console.log("caught: " + e.message)
}
console.log(arr[0])
`
	assertOutput(t, src, "caught: Array index out of bounds\n1")
}

func TestE2EArrayNegativeIndexThrows(t *testing.T) {
	src := `
const arr: number[] = [1, 2, 3]
try {
    console.log(arr[-1])
} catch (e) {
    console.log("caught: " + e.message)
}
`
	assertOutput(t, src, "caught: Array index out of bounds")
}

func TestE2EArrayIndexOutOfBoundsUncaughtExitsNonZero(t *testing.T) {
	_, exitCode := compileAndRunExpectExit(t, `
const arr: number[] = [1, 2, 3]
console.log(arr[5])
`)
	if exitCode == 0 {
		t.Fatal("expected a non-zero exit code for an uncaught array out-of-bounds access, got 0")
	}
}

func TestE2EArrayInBoundsAccessStillWorks(t *testing.T) {
	src := `
const arr: number[] = [10, 20, 30]
console.log(arr[0])
console.log(arr[2])
arr[1] = 99
console.log(arr[1])
`
	assertOutput(t, src, "10\n30\n99")
}

// --- splice: basic two-argument form (pre-existing behavior) ---

func TestE2ESpliceBasic(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3, 4, 5]
let removed = a.splice(1, 2)
console.log(removed.length)
console.log(removed[0])
console.log(removed[1])
console.log(a.length)
console.log(a[0])
console.log(a[1])
console.log(a[2])
`, "2\n2\n3\n3\n1\n4\n5")
}

// --- splice: deleteCount clamping (regression for ADR-00056's memory-safety fix) ---

func TestE2ESpliceDeleteCountClampedToAvailable(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3]
let removed = a.splice(1, 10)
console.log(removed.length)
console.log(a.length)
console.log(a[0])
`, "2\n1\n1")
}

func TestE2ESpliceNegativeDeleteCountClampsToZero(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3]
let removed = a.splice(1, -5)
console.log(removed.length)
console.log(a.length)
`, "0\n3")
}

func TestE2ESpliceOmittedDeleteCountDeletesToEnd(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3, 4, 5]
let removed = a.splice(2)
console.log(removed.length)
console.log(a.length)
console.log(a[0])
console.log(a[1])
`, "3\n2\n1\n2")
}

func TestE2ESpliceNegativeStart(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3, 4, 5]
let removed = a.splice(-2, 1)
console.log(removed[0])
console.log(a.length)
console.log(a[3])
`, "4\n4\n5")
}

// --- splice: insert items ---

func TestE2ESpliceInsertItemsReplacingDeleted(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3, 4, 5]
let removed = a.splice(1, 2, 100, 200, 300)
console.log(a.length)
console.log(a[0])
console.log(a[1])
console.log(a[2])
console.log(a[3])
console.log(a[4])
console.log(a[5])
console.log(removed.length)
`, "6\n1\n100\n200\n300\n4\n5\n2")
}

func TestE2ESpliceInsertItemsWithZeroDeleteCount(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3]
a.splice(1, 0, 99)
console.log(a.length)
console.log(a[0])
console.log(a[1])
console.log(a[2])
console.log(a[3])
`, "4\n1\n99\n2\n3")
}

func TestE2ESpliceInsertMoreItemsThanDeletedGrowsArray(t *testing.T) {
	assertOutput(t, `
let a: number[] = [1, 2, 3]
a.splice(0, 1, 10, 20, 30, 40)
console.log(a.length)
for (const x of a) { console.log(x) }
`, "6\n10\n20\n30\n40\n2\n3")
}

// --- Array.prototype.findLast / findLastIndex ---

func TestE2EArrayFindLast(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3, 4, 5, 4, 3]
console.log(nums.findLast((n) => n === 4))
console.log(nums.findLast((n) => n === 99))
`, "4\n0")
}

func TestE2EArrayFindLastIndex(t *testing.T) {
	assertOutput(t, `
const nums: number[] = [1, 2, 3, 4, 5, 4, 3]
console.log(nums.findLastIndex((n) => n === 4))
console.log(nums.findLastIndex((n) => n === 99))
`, "5\n-1")
}

func TestE2EArrayFindLastCallOrderIsReverse(t *testing.T) {
	// findLast must invoke its callback starting from the last element, not
	// scan forward and keep the last match — observable via a side effect.
	assertOutput(t, `
const nums: number[] = [1, 2, 3]
nums.findLast((n) => {
    console.log('visit ' + n)
    return false
})
`, "visit 3\nvisit 2\nvisit 1")
}

// --- Array.prototype.toSorted / toReversed (non-mutating) ---

func TestE2EArrayToSortedDoesNotMutateOriginal(t *testing.T) {
	assertOutput(t, `
const a: number[] = [3, 1, 2]
const sorted = a.toSorted()
console.log(sorted[0])
console.log(sorted[1])
console.log(sorted[2])
console.log(a[0])
console.log(a[1])
console.log(a[2])
`, "1\n2\n3\n3\n1\n2")
}

func TestE2EArrayToSortedWithComparator(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const sorted = a.toSorted((x, y) => y - x)
console.log(sorted[0])
console.log(sorted[1])
console.log(sorted[2])
`, "3\n2\n1")
}

func TestE2EArrayToReversedDoesNotMutateOriginal(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const rev = a.toReversed()
console.log(rev[0])
console.log(rev[1])
console.log(rev[2])
console.log(a[0])
console.log(a[1])
console.log(a[2])
`, "3\n2\n1\n1\n2\n3")
}

// --- Array.prototype.with ---

func TestE2EArrayWithDoesNotMutateOriginal(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const b = a.with(1, 99)
console.log(b[0])
console.log(b[1])
console.log(b[2])
console.log(a[0])
console.log(a[1])
console.log(a[2])
`, "1\n99\n3\n1\n2\n3")
}

func TestE2EArrayWithNegativeIndex(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const b = a.with(-1, 99)
console.log(b[2])
`, "99")
}

func TestE2EArrayWithOutOfRangeThrows(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
try {
    a.with(10, 99)
} catch (e) {
    console.log('caught')
}
`, "caught")
}

// --- Array.prototype.keys / values / entries ---

func TestE2EArrayKeys(t *testing.T) {
	assertOutput(t, `
const a: string[] = ['a', 'b', 'c']
for (const k of a.keys()) {
    console.log(k)
}
`, "0\n1\n2")
}

func TestE2EArrayValues(t *testing.T) {
	assertOutput(t, `
const a: string[] = ['x', 'y']
for (const v of a.values()) {
    console.log(v)
}
`, "x\ny")
}

func TestE2EArrayEntries(t *testing.T) {
	assertOutput(t, `
const a: string[] = ['a', 'b', 'c']
for (const e of a.entries()) {
    console.log(e.index + ':' + e.value)
}
`, "0:a\n1:b\n2:c")
}

// --- Array.of ---

func TestE2EArrayOf(t *testing.T) {
	assertOutput(t, `
const a = Array.of(1, 2, 3)
console.log(a.length)
console.log(a[0])
console.log(a[2])
`, "3\n1\n3")
}

func TestE2EArrayOfEmpty(t *testing.T) {
	assertOutput(t, `
const a = Array.of()
console.log(a.length)
`, "0")
}

func TestE2EArrayOfStrings(t *testing.T) {
	assertOutput(t, `
const a = Array.of('x', 'y', 'z')
console.log(a[1])
`, "y")
}

// --- Array.prototype.copyWithin ---

func TestE2EArrayCopyWithin(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3, 4, 5]
a.copyWithin(0, 3)
console.log(a[0])
console.log(a[1])
console.log(a[2])
console.log(a[3])
console.log(a[4])
`, "4\n5\n3\n4\n5")
}

func TestE2EArrayCopyWithinWithEnd(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3, 4, 5]
a.copyWithin(1, 3, 4)
console.log(a[0])
console.log(a[1])
console.log(a[2])
`, "1\n4\n3")
}

func TestE2EArrayCopyWithinReturnsSameArray(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const b = a.copyWithin(0, 1)
b[0] = 42
console.log(a[0])
`, "42")
}

// --- toSpliced (non-mutating splice) ---

func TestE2EArrayToSplicedDoesNotMutateOriginal(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3, 4, 5]
const b = a.toSpliced(1, 2, 100, 200)
console.log(b.length)
console.log(b[0])
console.log(b[1])
console.log(b[2])
console.log(b[3])
console.log(b[4])
console.log(a.length)
console.log(a[0])
console.log(a[1])
console.log(a[2])
`, "5\n1\n100\n200\n4\n5\n5\n1\n2\n3")
}

func TestE2EArrayToSplicedOmittedDeleteCount(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3, 4, 5]
const b = a.toSpliced(2)
console.log(b.length)
console.log(a.length)
`, "2\n5")
}

// --- Array<T> as a plain type annotation (not new Array<T>()) ---
//
// Regression test: the parser used to silently discard the <T> for any
// generic other than Promise<T>, so a parameter/return type annotated
// Array<T> resolved to i64 instead of an array type — see ADR-00058.

func TestE2EArrayGenericTypeAnnotationParam(t *testing.T) {
	assertOutput(t, `
function f(x: Array<number>): number {
    return x.length
}
const arr: number[] = [1, 2, 3]
console.log(f(arr))
`, "3")
}

func TestE2EArrayGenericTypeAnnotationReturnType(t *testing.T) {
	assertOutput(t, `
function makeArr(): Array<number> {
    const a: number[] = [1, 2, 3, 4]
    return a
}
const r = makeArr()
console.log(r.length)
console.log(r[2])
`, "4\n3")
}

func TestE2EArrayGenericTypeAnnotationStringElem(t *testing.T) {
	assertOutput(t, `
function first(x: Array<string>): string {
    return x[0]
}
const words: string[] = ['hello', 'world']
console.log(first(words))
`, "hello")
}

// --- return <array expression> where the expression isn't a plain named
// variable — a regression class, see ADR-00060. Used to require literally
// `return someArrayVariable`; anything else (an array method's result, a
// closure's result) failed with "can only return a named array variable
// from a function" even though the expression already evaluates to exactly
// the {ptr, i64} aggregate shape a function's own array return needs.

func TestE2EReturnArrayMethodResult(t *testing.T) {
	assertOutput(t, `
function tail(a: number[]): number[] {
    return a.slice(1)
}
const nums: number[] = [1, 2, 3]
const r = tail(nums)
console.log(r.length)
console.log(r[0])
console.log(r[1])
`, "2\n2\n3")
}

func TestE2EReturnArrayFromNestedFunctionCall(t *testing.T) {
	assertOutput(t, `
function makeNums(): number[] {
    const a: number[] = [10, 20, 30]
    return a
}
function relay(): number[] {
    return makeNums()
}
const r = relay()
console.log(r.length)
console.log(r[1])
`, "3\n20")
}

// --- Array.from ---

func TestE2EArrayFromArray(t *testing.T) {
	assertOutput(t, `
const src: number[] = [1, 2, 3]
const copy: number[] = Array.from(src)
console.log(copy.length)
console.log(copy[0])
console.log(copy[1])
console.log(copy[2])
src[0] = 99
console.log(copy[0])
console.log(src[0])
`, "3\n1\n2\n3\n1\n99")
}

func TestE2EArrayFromStringArray(t *testing.T) {
	assertOutput(t, `
const src: string[] = ["a", "b", "c"]
const copy: string[] = Array.from(src)
console.log(copy.length)
console.log(copy.join(","))
`, "3\na,b,c")
}

func TestE2EArrayFromClassIterator(t *testing.T) {
	assertOutput(t, `
class Counter {
  private current: number;
  private max: number;
  constructor(max: number) {
    this.current = 1;
    this.max = max;
  }
  next(): number | null {
    if (this.current > this.max) {
      return null;
    }
    const v = this.current;
    this.current = this.current + 1;
    return v;
  }
}
const arr: number[] = Array.from(new Counter(5))
console.log(arr.length)
for (const x of arr) {
  console.log(x)
}
`, "5\n1\n2\n3\n4\n5")
}

func TestE2EArrayFromEmptyClassIterator(t *testing.T) {
	assertOutput(t, `
class Empty {
  next(): number | null {
    return null;
  }
}
const arr: number[] = Array.from(new Empty())
console.log(arr.length)
`, "0")
}

func TestE2EArrayFromNonIterableIsError(t *testing.T) {
	if _, err := parseAndCompile(`
const x: number = 5
const arr: number[] = Array.from(x)
`); err == nil {
		t.Fatal("expected a compile error for Array.from on a non-iterable")
	}
}

// --- Array-of-arrays (nested array) storage representation (TDD-00029) ---
//
// A nested array element is boxed (heap ptr to a malloc'd {ptr,i64} pair) so
// an outer array's backing buffer stays a uniform 8-byte-per-slot layout
// regardless of nesting — see codegen/llvm/emit_arrays_core.go's
// boxArrayValue/loadArrayElem/storeArrayElem and docs/adr/ADR-00105.md.
// Indexing, destructuring, for...of, assignment, and the copy/insert-based
// methods (concat/reverse/slice/splice/fill/at/with/push/pop/shift/unshift/
// copyWithin/values/entries) are all supported; a callback-invoking or
// scalar-register-comparing method (map/filter/forEach/reduce/find*/some/
// every/sort/indexOf/includes/join/Object.groupBy) on a nested-array element
// is a deliberate, clean compile-time rejection instead (see
// TestE2ENestedArrayHOFRejectedCleanly below) — closures don't yet decompose
// an array-typed parameter into (ptr, i64) the way a named function call's
// own ABI already does, a separate, unrelated gap.

func TestE2ENestedArrayIndexingReadWrite(t *testing.T) {
	assertOutput(t, `
const matrix: number[][] = [[1, 2, 3], [4, 5, 6]];
console.log(matrix.length);
console.log(matrix[0].length);
console.log(matrix[0][0]);
console.log(matrix[1][2]);
matrix[0][1] = 99;
console.log(matrix[0][1]);
matrix[1] = [7, 8, 9];
console.log(matrix[1][0]);
console.log(matrix[1].length);
`, "2\n3\n1\n6\n99\n7\n3")
}

func TestE2ENestedArrayForOfAndDestructuring(t *testing.T) {
	assertOutput(t, `
const matrix: number[][] = [[1, 2], [3, 4]];
for (const row of matrix) {
  console.log(row.length);
  for (const v of row) {
    console.log(v);
  }
}
const [first, second] = matrix;
console.log(first[0]);
console.log(second[1]);
`, "2\n1\n2\n2\n3\n4\n1\n4")
}

func TestE2ENestedArrayOfStrings(t *testing.T) {
	assertOutput(t, `
const strs: string[][] = [["a", "b"], ["c"]];
console.log(strs[0][1]);
console.log(strs[1][0]);
`, "b\nc")
}

func TestE2ENestedArrayJSONStringify(t *testing.T) {
	assertOutput(t, `
const matrix: number[][] = [[1, 2], [3, 4]];
console.log(JSON.stringify(matrix));
const obj = { grid: [[1, 2], [3, 4]] };
console.log(JSON.stringify(obj));
`, `[[1,2],[3,4]]`+"\n"+`{"grid":[[1,2],[3,4]]}`)
}

func TestE2ENestedArrayAtWithFillPushPop(t *testing.T) {
	assertOutput(t, `
const matrix: number[][] = [[1, 2], [3, 4]];
console.log(matrix.at(0)[0]);
console.log(matrix.at(-1)[1]);
const withReplaced = matrix.with(0, [9, 9]);
console.log(withReplaced[0][0]);
console.log(matrix[0][0]);
matrix.push([5, 6]);
console.log(matrix.length);
console.log(matrix.pop()[0]);
console.log(matrix.length);
`, "1\n4\n9\n1\n3\n5\n2")
}

func TestE2ENestedArrayHOFRejectedCleanly(t *testing.T) {
	cases := []string{
		`const m: number[][] = [[1,2]]; m.map((row) => row.length);`,
		`const m: number[][] = [[1,2]]; m.forEach((row) => console.log(row.length));`,
		`const m: number[][] = [[1,2]]; m.filter((row) => row.length > 0);`,
		`const m: number[][] = [[1,2]]; m.indexOf([1,2]);`,
		`const m: number[][] = [[1,2]]; m.includes([1,2]);`,
		`const m: number[][] = [[1,2]]; m.sort();`,
		`const m: number[][] = [[1,2]]; m.join(",");`,
		`const m = new Array<number[]>(3);`,
	}
	for _, src := range cases {
		if _, err := parseAndCompile(src); err == nil {
			t.Fatalf("expected a compile error for nested-array HOF/construction, got none for: %s", src)
		}
	}
}

// --- .flat(depth?) / .flatMap(fn) ---
//
// This compiler's arrays are statically typed with a fixed nesting depth,
// so depth has to be a compile-time constant (a literal, or the bare
// `Infinity` identifier) rather than a general runtime expression — each
// level of flattening unwraps one level of the receiver's own static array
// type, and the result's type has to be knowable at compile time. See
// codegen/llvm/emit_arrays_transform.go's resolveFlatDepth.

func TestE2EArrayFlatDefaultDepth(t *testing.T) {
	assertOutput(t, `
const a: number[][] = [[1, 2], [3, 4, 5], []]
const flat = a.flat()
console.log(flat.length)
console.log(flat[0])
console.log(flat[4])
`, "5\n1\n5")
}

func TestE2EArrayFlatNonNestedIsShallowCopy(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
const flat = a.flat()
console.log(flat.length)
console.log(flat[0])
a[0] = 99
console.log(flat[0])
`, "3\n1\n1")
}

func TestE2EArrayFlatExplicitDepth(t *testing.T) {
	assertOutput(t, `
const c: number[][][] = [[[1, 2], [3]], [[4, 5, 6]]]
const flat2 = c.flat(2)
console.log(flat2.length)
console.log(flat2[0])
console.log(flat2[5])
const flat0 = c.flat(0)
console.log(flat0.length)
console.log(flat0[0].length)
`, "6\n1\n6\n2\n2")
}

func TestE2EArrayFlatInfinity(t *testing.T) {
	assertOutput(t, `
const c: number[][][] = [[[1, 2], [3]], [[4, 5, 6]]]
const flat = c.flat(Infinity)
console.log(flat.length)
console.log(flat[0])
console.log(flat[5])
`, "6\n1\n6")
}

func TestE2EArrayFlatMapArrayCallback(t *testing.T) {
	assertOutput(t, `
const b: number[] = [1, 2, 3]
const doubled = b.flatMap((x) => [x, x * 10])
console.log(doubled.length)
console.log(doubled[0])
console.log(doubled[1])
console.log(doubled[5])
`, "6\n1\n10\n30")
}

func TestE2EArrayFlatMapScalarCallback(t *testing.T) {
	assertOutput(t, `
const b: number[] = [1, 2, 3]
const doubled = b.flatMap((x) => x * 2)
console.log(doubled.length)
console.log(doubled[0])
console.log(doubled[2])
`, "3\n2\n6")
}

func TestE2EArrayFlatDepthRejectedWhenNotCompileTimeConstant(t *testing.T) {
	cases := []string{
		`const a: number[][] = [[1]]; const d = 1; a.flat(d);`,
		`const a: number[][] = [[1]]; a.flat(-1);`,
		`const a: number[][] = [[1]]; a.flat(1.5);`,
		`const a: number[][] = [[1]]; a.flat(1, 2);`,
	}
	for _, src := range cases {
		if _, err := parseAndCompile(src); err == nil {
			t.Fatalf("expected a compile error for a non-compile-time-constant flat() depth, got none for: %s", src)
		}
	}
}
