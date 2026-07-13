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
