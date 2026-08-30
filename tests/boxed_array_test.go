package tests

import "testing"

// TDD-00127: arrays are reference values (object-reference model). A length
// mutation (push/splice/unshift) inside a callee propagates back to the
// caller's array, matching JavaScript.
func TestE2EBoxedArrayPushPropagates(t *testing.T) {
	assertOutput(t, `
function grow(a: number[]): void {
  a.push(99)
  a.push(100)
}
const xs: number[] = [1, 2, 3]
grow(xs)
console.log(xs.length, xs[3], xs[4])
`, "5 99 100")
}

// A push that forces the backing buffer to grow/move must still be seen by the
// caller — it is the moved *data pointer*, not only the length, that has to
// live in the shared header.
func TestE2EBoxedArrayReallocMovePropagates(t *testing.T) {
	assertOutput(t, `
function growLots(a: number[]): void {
  for (let i = 0; i < 64; i++) { a.push(i) }
}
const ys: number[] = [1]
growLots(ys)
console.log(ys.length, ys[64])
`, "65 63")
}

// splice inside a callee propagates (removal + insertion) to the caller.
func TestE2EBoxedArraySplicePropagates(t *testing.T) {
	assertOutput(t, `
function edit(a: number[]): void {
  a.splice(1, 1, 8, 9)
}
const xs: number[] = [1, 2, 3]
edit(xs)
console.log(xs.join(","))
`, "1,8,9,3")
}

// unshift inside a callee propagates.
func TestE2EBoxedArrayUnshiftPropagates(t *testing.T) {
	assertOutput(t, `
function prepend(a: number[]): void { a.unshift(0) }
const xs: number[] = [1, 2]
prepend(xs)
console.log(xs.join(","))
`, "0,1,2")
}

// Reassigning a parameter to a whole new array is a *local* rebind: the caller
// keeps its original array (JS semantics — `a = ...` does not write through).
func TestE2EBoxedArrayParamReassignIsLocal(t *testing.T) {
	assertOutput(t, `
function keepBig(a: number[]): number {
  a = a.filter((x) => x > 1)
  return a.length
}
const zs: number[] = [1, 2, 3]
const n = keepBig(zs)
console.log(n, zs.length)
`, "2 3")
}

// Element mutation through a parameter still propagates (it always did).
func TestE2EBoxedArrayElementMutationPropagates(t *testing.T) {
	assertOutput(t, `
function setFirst(a: number[]): void { a[0] = 42 }
const ws: number[] = [0, 0]
setFirst(ws)
console.log(ws[0])
`, "42")
}

// Propagation works transitively through a second call level (the header is
// shared all the way down, not copied per frame).
func TestE2EBoxedArrayTransitivePropagation(t *testing.T) {
	assertOutput(t, `
function inner(a: number[]): void { a.push(7) }
function outer(a: number[]): void { inner(a) }
const xs: number[] = [1]
outer(xs)
console.log(xs.length, xs[1])
`, "2 7")
}

// Mutating an array field via a method call, and passing a plain array to a
// closure that mutates it, both still behave (no regression from the header
// change).
func TestE2EBoxedArrayClosureMutation(t *testing.T) {
	assertOutput(t, `
const xs: number[] = [1, 2]
const push3 = (a: number[]): void => { a.push(3) }
push3(xs)
console.log(xs.join(","))
`, "1,2,3")
}
