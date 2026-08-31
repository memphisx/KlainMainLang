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
`, "true\ntrue")
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

// --- .reduce() with no initial value (ADR-00163) ---
//
// Matches real JS: the array's own first element seeds the accumulator,
// the fold starts from index 1, and an empty array with no initial value
// throws. This was already documented as supported ("reduce(fn, init?)")
// before this fix — the implementation itself required exactly 2
// arguments, a real code/doc mismatch, not a new feature.

func TestE2EArrayReduceNoInitialValue(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [1, 2, 3, 4]
console.log(arr.reduce((acc, val) => acc + val))
`, "10")
}

func TestE2EArrayReduceNoInitialValueSingleElement(t *testing.T) {
	// Callback never runs — the loop starts at index 1 with length 1, so
	// the seeded first element is returned as-is.
	assertOutput(t, `
const arr: number[] = [42]
console.log(arr.reduce((acc, val) => acc + val))
`, "42")
}

func TestE2EArrayReduceWithInitialValueStillWorks(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [1, 2, 3, 4]
console.log(arr.reduce((acc, val) => acc + val, 100))
`, "110")
}

func TestE2EArrayReduceEmptyNoInitialValueThrows(t *testing.T) {
	assertOutput(t, `
const arr: number[] = []
try {
  arr.reduce((acc, val) => acc + val)
} catch (e) {
  console.log(e.message)
}
`, "Reduce of empty array with no initial value")
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

// TestE2EArraySortResultInfersArrayType confirms `arr.sort()`'s result infers
// as the receiver's array type when captured in an unannotated `const` — sort
// returns the same array, so the result is indexable/`.length`-able without an
// explicit `: T[]` annotation (ADR-00527).
func TestE2EArraySortResultInfersArrayType(t *testing.T) {
	assertOutput(t, `
const arr = [3, 1, 2]
const sorted = arr.sort()
console.log(sorted.length)
console.log(sorted[0])
const strs = ["b", "a", "c"]
const ss = strs.sort()
console.log(ss.join(","))
`, "3\n1\na,b,c")
}

// TestE2EArrayToStringCoercion confirms an array coerces to a string as real
// JS's Array.prototype.toString does — elements String()'d and joined by "," —
// through String(arr), a `${arr}` template interpolation, and .join() with a
// nested-array element (each nested element itself renders comma-joined), all
// routing through the one emitArrayJoinCore path (ADR-00528).
func TestE2EArrayToStringCoercion(t *testing.T) {
	assertOutput(t, `
const a = [1, 2, 3]
console.log(String(a))
console.log(` + "`x=${a}`" + `)
const s = ["p", "q"]
console.log(` + "`${s}`" + `)
const n = [[1, 2], [3, 4]]
console.log(n.join("-"))
console.log(String(n))
const deep = [[[1, 2], [3]], [[4]]]
console.log(String(deep))
const empty: number[] = []
console.log(` + "`[${empty}]`" + `)
`, "1,2,3\nx=1,2,3\np,q\n1,2-3,4\n1,2,3,4\n1,2,3,4\n[]")
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

// TestE2EForOfString confirms for-of over a string iterates its characters
// (one 1-byte character string per element, this compiler's model), over both
// a literal and a string variable, and handles the empty string (ADR-00535).
func TestE2EForOfString(t *testing.T) {
	assertOutput(t, `
let out = ""
for (const c of "hello") { out = out + c + "." }
console.log(out)
const s = "abc"
for (const ch of s) { console.log(ch) }
let n = 0
for (const c of "") { n = n + 1 }
console.log(n)
`, "h.e.l.l.o.\na\nb\nc\n0")
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
`, "true\nfalse")
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
`, "true\nfalse")
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
for (const [i, v] of a.entries()) {
    console.log(i + ':' + v)
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

// Bug #2 (TDD-00064): Array.from over an iterator whose first value is 0 must
// collect it rather than stopping on the old 0-as-done sentinel.
func TestE2EArrayFromClassIteratorZeroFirst(t *testing.T) {
	assertOutput(t, `
class ZeroUp {
  private current: number = 0;
  next(): number | null {
    if (this.current > 2) { return null; }
    const v = this.current;
    this.current = this.current + 1;
    return v;
  }
}
const arr = Array.from(new ZeroUp())
console.log(arr.length)
console.log(arr[0])
console.log(arr[1])
console.log(arr[2])
`, "3\n0\n1\n2")
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
// copyWithin/values/entries) are all supported.
//
// Every callback-invoking method (map/filter/forEach/reduce/find/findIndex/
// findLast/findLastIndex/some/every) now also supports a nested-array
// element as the callback's own parameter (TestE2ENestedArrayHOFCallbacks
// below) — closures previously never decomposed an array-typed parameter
// into (ptr, i64) the way a named function call's own ABI already did
// (ADR-00105's original finding); fixed alongside tagged template literals
// (ADR-00151/TDD-00059), since an arrow function's own tag-function
// `strings: string[]` parameter has the identical shape. loadArrayElem/
// storeArrayElem already transparently unbox/rebox at the call site, so
// each HOF method only needed its raw element load/store switched to use
// them (they previously bypassed both, going straight for a raw scalar
// load/store that a 16-byte {ptr,i64} aggregate can't fit through) plus its
// rejectNestedArrayElem guard removed.
//
// Three genuinely different, unrelated mechanisms remain out of scope, each
// for its own reason (TestE2ENestedArrayHOFRejectedCleanly below):
//   - indexOf/includes/join compare or stringify an element as a bare
//     elemTy.IR-typed register directly (no callback at all) — a boxed
//     nested-array element needs an unbox first, a different fix.
//   - sort's comparator runs through a C-ABI qsort() trampoline
//     (emit_arrays_sort.go) with one fixed trampoline per element kind
//     (i64/f64/str) — a fourth, array-aware trampoline is a separate task.
//   - Object.groupBy's buckets store every element uniformly as a raw i64
//     (ptrtoint'd for a pointer-shaped element) — a different storage
//     scheme than a plain array's backing buffer, with no room for a
//     16-byte aggregate without its own redesign.
// `new Array<T[]>(n)` (construction, not consumption) and capturing an
// array *variable* into a closure's env (a different storage shape — one
// heap-cell pointer per env slot, not a (ptr, i64) pair) are also untouched.

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

// --- pop on empty array: returns element type's zero value, length stays 0 ---

func TestE2EPopOnEmptyArray(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [];
const result = arr.pop();
console.log(result);
console.log(arr.length);
`, "0\n0")
}

func TestE2EPopOnEmptyStringArray(t *testing.T) {
	assertOutput(t, `
const arr: string[] = [];
const result = arr.pop();
console.log("result:", result);
console.log("len:", arr.length);
`, "result: null\nlen: 0")
}

// --- shift on empty array: returns element type's zero value, length stays 0 ---

func TestE2EShiftOnEmptyArray(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [];
const result = arr.shift();
console.log(result);
console.log(arr.length);
`, "0\n0")
}

func TestE2EShiftOnEmptyStringArray(t *testing.T) {
	assertOutput(t, `
const arr: string[] = [];
const result = arr.shift();
console.log("result:", result);
console.log("len:", arr.length);
`, "result: null\nlen: 0")
}

// --- pop/shift on non-empty array still works normally (non-regression) ---

func TestE2EPopShiftOnNonEmptyArray(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [10, 20, 30];
console.log(arr.pop());
console.log(arr.shift());
console.log(arr.length);
console.log(arr[0]);
`, "30\n10\n1\n20")
}

func TestE2ENestedArrayHOFCallbacks(t *testing.T) {
	// See this section's own header comment for the ADR-00151/TDD-00059
	// fix that made all of these work — previously every one of these was
	// a clean compile-time rejection.
	assertOutput(t, `
const m: number[][] = [[1, 2], [3, 4, 5], [6]];
console.log(m.reduce((acc: number, row: number[]) => acc + row.length, 0));
const found = m.find((row: number[]) => row.length === 3);
console.log(found ? found.length : -1);
const notFound = m.find((row: number[]) => row.length === 99);
console.log(notFound === null ? "null" : "found");
console.log(m.findIndex((row: number[]) => row.length === 3));
console.log(m.findLastIndex((row: number[]) => row.length === 1));
const lastLenOne = m.findLast((row: number[]) => row.length === 1);
console.log(lastLenOne ? lastLenOne.length : -1);
console.log(m.some((row: number[]) => row.length === 3));
console.log(m.every((row: number[]) => row.length > 0));
const doubled = m.map((row: number[]): number => row.length * 2);
console.log(doubled[0]);
console.log(doubled[1]);
`, "6\n3\nnull\n1\n2\n1\ntrue\ntrue\n4\n6")
}

func TestE2ENestedArrayHOFRejectedCleanly(t *testing.T) {
	cases := []string{
		`const m: number[][] = [[1,2]]; m.indexOf([1,2]);`,
		`const m: number[][] = [[1,2]]; m.includes([1,2]);`,
		`const m: number[][] = [[1,2]]; m.sort();`,
		`const m: number[][] = [[1,2]]; Object.groupBy(m, (row) => "" + row.length);`,
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

// --- Array destructuring (`let [a, b] = arr`) ---

func TestE2EArrayDestructuringBasic(t *testing.T) {
	assertOutput(t, `
let [a, b, c] = [10, 20, 30];
console.log(a, b, c);
`, "10 20 30")
}

// TestE2EStringDestructuring confirms array destructuring of a string binds its
// characters (one per byte), over both a literal and a string variable
// (ADR-00536).
func TestE2EStringDestructuring(t *testing.T) {
	assertOutput(t, `
const [a, b] = "xy";
console.log(a, b);
const [f, s, t] = "cat";
console.log(f + "-" + s + "-" + t);
const word = "hi";
const [h] = word;
console.log(h);
`, "x y\nc-a-t\nh")
}

func TestE2EArrayDestructuringOutOfBoundsReadsZero(t *testing.T) {
	// A pattern position past the source array's actual length is ordinary,
	// valid JS (unlike plain out-of-bounds `arr[i]` indexing, which throws)
	// — it must read a safe, deterministic zero, not garbage from whatever
	// heap memory happens to sit past the source array's malloc'd buffer.
	// Real bug found investigating destructuring defaults; see ADR-00157.
	assertOutput(t, `
let [a, b] = [1];
console.log(a, b);
`, "1 0")
}

func TestE2EArrayDestructuringAllOutOfBoundsReadsZero(t *testing.T) {
	assertOutput(t, `
let arr: number[] = [];
let [a, b] = arr;
console.log(a, b);
`, "0 0")
}

func TestE2EArrayDestructuringNestedArrayOutOfBoundsIsEmptyArray(t *testing.T) {
	// The out-of-bounds fallback for an array-typed destructured element
	// (nested destructuring) is a safe empty array (null ptr, length 0),
	// not the scalar zero literal used for every other element type.
	assertOutput(t, `
let [x, y] = [[1, 2, 3]];
console.log(x[0]);
console.log(y.length);
`, "1\n0")
}

func TestE2EDestructuredArrayParamOutOfBoundsReadsZero(t *testing.T) {
	assertOutput(t, `
function f([a, b]: number[]): void {
  console.log(a, b);
}
f([5]);
`, "5 0")
}

// --- Array destructuring default values (`[a = expr] = arr`, ADR-00158) ---
//
// Fires exactly when a pattern position is past the source array's actual
// length (the same bounds check ADR-00157 added) — the one reliable "was
// this provided" signal array destructuring has, unlike object
// destructuring's field-shape restriction below.

func TestE2EArrayDestructuringDefaultUsedWhenOutOfBounds(t *testing.T) {
	assertOutput(t, `
let [a = 10, b = 20] = [1];
console.log(a, b);
`, "1 20")
}

func TestE2EArrayDestructuringDefaultNotUsedWhenInBounds(t *testing.T) {
	assertOutput(t, `
let [a = 10, b = 20] = [1, 2];
console.log(a, b);
`, "1 2")
}

func TestE2EArrayDestructuringDefaultReferencesEarlierElement(t *testing.T) {
	// Real JS: a later default may reference an earlier binding in the
	// same pattern.
	assertOutput(t, `
let [a = 5, b = a] = [1];
console.log(a, b);
`, "1 1")
}

func TestE2EArrayDestructuringDefaultCapturesOuterVariable(t *testing.T) {
	assertOutput(t, `
function make(): () => number {
  let fallback = 42;
  let [a = fallback] = [];
  return () => a;
}
const fn = make();
console.log(fn());
`, "42")
}

func TestE2EDestructuredArrayParamDefault(t *testing.T) {
	assertOutput(t, `
function f([a = 10, b = 20]: number[]): void {
  console.log(a, b);
}
f([1]);
f([1, 2]);
`, "1 20\n1 2")
}

// --- Array destructuring assignment (`[a, b] = expr`, ADR-00160) ---
//
// V1 scope, narrower than the declaration form: every target must be a
// plain, already-declared, non-array, non-const variable — no nested
// patterns, no rest, no per-element default.

func TestE2EArrayDestructuringAssignmentBasic(t *testing.T) {
	assertOutput(t, `
let a: number, b: number;
[a, b] = [1, 2];
console.log(a, b);
`, "1 2")
}

func TestE2EArrayDestructuringAssignmentSwapIdiom(t *testing.T) {
	assertOutput(t, `
let a: number = 1;
let b: number = 2;
[a, b] = [b, a];
console.log(a, b);
`, "2 1")
}

func TestE2EArrayDestructuringAssignmentFromVariable(t *testing.T) {
	assertOutput(t, `
let a: number = 0, b: number = 0;
let src: number[] = [7];
[a, b] = src;
console.log(a, b);
`, "7 0")
}

func TestE2EArrayDestructuringAssignmentOutOfBoundsReadsZero(t *testing.T) {
	assertOutput(t, `
let a: number = 9, b: number = 9;
[a, b] = [1];
console.log(a, b);
`, "1 0")
}

func TestE2EArrayDestructuringAssignmentAtTopLevel(t *testing.T) {
	// Top-level bindings go through the resolver's per-file name mangling
	// (TDD-00041) — a real risk area this session (see ADR-00159's own
	// finding for `new Set(arr)`); confirmed this feature needed no new
	// resolver plumbing since it reuses ArrayLiteral/ObjectLiteral nodes
	// the rename pass already traverses generically, but still verified
	// directly rather than assumed.
	assertOutput(t, `
let a: number = 0;
let b: number = 0;
[a, b] = [9, 10];
console.log(a, b);
`, "9 10")
}

func TestE2EArrayDestructuringAssignmentClosureCapture(t *testing.T) {
	assertOutput(t, `
function make(): () => number {
  let a: number = 0;
  let b: number = 0;
  [a, b] = [3, 4];
  return () => a + b;
}
const fn = make();
console.log(fn());
`, "7")
}

func TestE2EArrayDestructuringAssignmentConstTargetRejected(t *testing.T) {
	_, err := parseAndCompile(`
let a: number = 1;
const b: number = 2;
[a, b] = [3, 4];
`)
	if err == nil {
		t.Fatal("expected a compile error assigning into a const destructuring target")
	}
}

func TestE2EArrayDestructuringAssignmentNestedTargetRejected(t *testing.T) {
	_, err := parseAndCompile(`
let arr: number[] = [1, 2];
let x: number = 0;
[arr[0], x] = [5, 6];
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-identifier destructuring assignment target")
	}
}

func TestE2EArrayDestructuringCompoundAssignmentRejected(t *testing.T) {
	_, err := parseAndCompile(`
let a: number = 1, b: number = 2;
[a, b] += [1, 2];
`)
	if err == nil {
		t.Fatal("expected a compile error for compound destructuring assignment")
	}
}

// --- Array rest destructuring (`[a, ...rest]`, ADR-00161) ---
//
// A rest element collects every remaining source position into a real,
// independent new array (malloc + memcpy — not an aliasing view into the
// source's own backing buffer), defined even when the source is shorter
// than the pattern (an empty array, not an error). Parser-enforced last
// element, no default alongside it.

func TestE2EArrayRestDestructuringBasic(t *testing.T) {
	assertOutput(t, `
let [a, ...rest] = [1, 2, 3];
console.log(a, rest.length, rest[0], rest[1]);
`, "1 2 2 3")
}

func TestE2EArrayRestDestructuringEmptyWhenSourceExhausted(t *testing.T) {
	assertOutput(t, `
let [a, b, ...rest] = [1, 2];
console.log(a, b, rest.length);
`, "1 2 0")
}

func TestE2EArrayRestDestructuringWholeArray(t *testing.T) {
	assertOutput(t, `
let [...all] = [1, 2, 3];
console.log(all.length, all[0], all[2]);
`, "3 1 3")
}

func TestE2EArrayRestDestructuringIsIndependentCopy(t *testing.T) {
	assertOutput(t, `
let src: number[] = [1, 2, 3];
let [a, ...rest] = src;
src.push(99);
console.log(rest.length);
`, "2")
}

func TestE2EArrayRestDestructuredParam(t *testing.T) {
	assertOutput(t, `
function f([first, ...rest]: number[]): number {
  return first + rest.length;
}
console.log(f([10, 20, 30, 40]));
`, "13")
}

func TestE2EArrayRestDestructuringAssignment(t *testing.T) {
	assertOutput(t, `
let a: number = 0;
let rest: number[] = [];
[a, ...rest] = [1, 2, 3];
console.log(a, rest.length);
`, "1 2")
}

func TestE2EArrayRestDestructuringAssignmentTargetMustBeArray(t *testing.T) {
	_, err := parseAndCompile(`
let a: number = 0;
let notArr: number = 0;
[a, ...notArr] = [1, 2, 3];
`)
	if err == nil {
		t.Fatal("expected a compile error assigning a rest target into a non-array variable")
	}
}

func TestE2EArrayRestDestructuringAssignmentMustBeLast(t *testing.T) {
	_, err := parseAndCompile(`
let a: number = 0;
let rest: number[] = [];
[...rest, a] = [1, 2, 3];
`)
	if err == nil {
		t.Fatal("expected a compile error for a rest destructuring-assignment target that isn't last")
	}
}

func TestE2EArrayRestDestructuringNotLastRejected(t *testing.T) {
	_, err := parseAndCompile(`
let [...rest, a] = [1, 2, 3];
`)
	if err == nil {
		t.Fatal("expected a compile error for a rest declaration element that isn't last")
	}
}

func TestE2EArrayRestDestructuringWithDefaultRejected(t *testing.T) {
	_, err := parseAndCompile(`
let [...rest = []] = [1, 2, 3];
`)
	if err == nil {
		t.Fatal("expected a compile error for a rest element combined with a default value")
	}
}

// Array mutators on non-identifier receivers (ADR-00284): class `this.field`,
// interface-typed object fields, and nested-array elements — all previously
// hard "requires an array variable" compile errors.
func TestE2EArrayMutatorsOnFieldsAndElements(t *testing.T) {
	assertOutput(t, `
interface Bag { xs: number[]; }
function make(): Bag { return { xs: [1, 2, 3] }; }
const b = make();
b.xs.push(4);
b.xs.unshift(0);
console.log(b.xs.join(","));
const rem = b.xs.splice(1, 2);
console.log(rem.join(","));
console.log(b.xs.join(","));
const m: number[][] = [[1, 2], [3]];
m[0].push(9);
m[1].pop();
console.log(m[0].join(","));
console.log(m[1].length);
class Stack {
  items: string[] = [];
  add(s: string): number { return this.items.push(s); }
}
const st = new Stack();
console.log(st.add("a"), st.add("b"));
const top = st.items.pop();
console.log(top, st.items.length);
const first = st.items.shift();
console.log(first, st.items.length);
`, "0,1,2,3,4\n1,2\n0,3,4\n1,2,9\n0\n1 2\nb 1\na 0")
}

// --- N-ary concat + zero-arg Array constructor (ADR-00463) ---

func TestE2EArrayConcatMultipleArgsAndScalars(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2];
const b = a.concat([3, 4], 5, [6]);
console.log(b.length);
console.log(b[0], b[2], b[4], b[5]);
console.log(a.concat().length);
const s: string[] = ["x"];
console.log(s.concat("y", ["z"]).join(","));
`, "6\n1 3 5 6\n2\nx,y,z")
}

func TestE2ENewArrayZeroArgs(t *testing.T) {
	assertOutput(t, `
const b = new Array<string>();
b.push("x");
b.push("y");
console.log(b.length, b[1]);
`, "2 y")
}

func TestE2EArrayLiteralElisions(t *testing.T) {
	// ADR-00467: holes read as undefined (the element type's zero value
	// stand-in) and count toward the length, matching JS.
	assertOutput(t, `
const a: number[] = [1, , 3];
console.log(a.length, a[1]);
const b: string[] = [, "x", ];
console.log(b.length, b[1]);
`, "3 0\n2 x")
}

func TestE2EArrayFromIterables(t *testing.T) {
	// ADR-00482: Array.from over Set (elements), Map (entries), and string
	// (characters), beside the existing array-like copy.
	assertOutput(t, `
const s = new Set<number>();
s.add(3); s.add(1);
const fromSet = Array.from(s);
console.log(fromSet.length, fromSet[0], fromSet[1]);
const m = new Map<string, number>();
m.set("a", 1); m.set("b", 2);
const fromMap = Array.from(m);
console.log(fromMap.length, fromMap[0][0], fromMap[1][1]);
const chars = Array.from("hey");
console.log(chars.length, chars[0], chars[2]);
`, "2 3 1\n2 a 2\n3 h y")
}

// Array.from(iterable, mapFn) — desugars to Array.from(iterable).map(mapFn)
// (ADR-00491); exact for dense materialized arrays. Covers the index
// parameter and a non-array (string) source.
func TestE2EArrayFromMapFn(t *testing.T) {
	src := `
const doubled = Array.from([1, 2, 3], (x: number) => x * 2)
console.log(doubled.join(","))
const idx = Array.from([10, 20, 30], (x: number, i: number) => i)
console.log(idx.join(","))
const shouted = Array.from("abc", (c: string) => c + "!")
console.log(shouted.join(","))
`
	assertOutput(t, src, "2,4,6\n0,1,2\na!,b!,c!")
}
