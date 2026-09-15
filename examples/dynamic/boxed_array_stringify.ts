// A statically-typed array widened into `any` (a `: any` binding, a
// null-evolving binding, an `any` parameter) now renders its contents in string
// contexts instead of the old `[object Array]` placeholder: the box carries an
// element-kind descriptor so `String()`/interpolation can walk the buffer
// (TDD-00212 Stage 1). Element kinds covered: number, float, string, boolean,
// integer.

let nums: any = [1, 2, 3]
console.log('' + nums) // 1,2,3

let floats: any = [1.5, 2, 3.25]
console.log('' + floats) // 1.5,2,3.25

let strs: any = ['a', 'b', 'c']
console.log(`joined: ${strs}`) // joined: a,b,c

let bools: any = [true, false, true]
console.log('' + bools) // true,false,true

// The null-evolving widening (an untyped binding first assigned an array) takes
// the same path.
let evolving = null
evolving = [10, 20, 30]
console.log('' + evolving) // 10,20,30

// An empty boxed array renders as the empty string, like JS.
let empty: any = []
console.log('[' + empty + ']') // []

// Passing the array through an `any` parameter boxes it at the call boundary;
// the contents still render.
function show(x: any): void {
  console.log('via param: ' + x)
}
show([7, 8, 9]) // via param: 7,8,9

// console.log itself (not string coercion) renders the Node util.inspect bracket
// form — `[ 1, 2, 3 ]`, with string elements single-quoted (TDD-00212 Stage 2).
console.log(nums) // [ 1, 2, 3 ]
console.log(strs) // [ 'a', 'b', 'c' ]
console.log(empty) // []

// A heterogeneous or nested boxed array inspects through the D1 dynamic-array
// path, matching Node.
let mixed: any = [1, 'hi', true]
console.log(mixed) // [ 1, 'hi', true ]
let nested: any = [[1, 2], [3]]
console.log(nested) // [ [ 1, 2 ], [ 3 ] ]

// The box shares the LIVE array header, so a mutation of the original array
// after boxing is visible through the box — real JS reference semantics, not a
// snapshot (TDD-00212 Stage 3).
let live = [1, 2, 3]
let alias: any = live
live.push(4)
console.log(alias) // [ 1, 2, 3, 4 ]
console.log(alias === live) // true

