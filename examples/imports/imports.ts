// import / export — multi-file compilation.
//
// This is the whole program's entry point: only this file's top-level
// statements actually execute. math.ts (imported below) only contributes
// declarations to the merged program — see its own comments for the exact
// scope and restrictions (declarations-only imported files, relative paths
// only). See scoping.ts/scoping_a.ts/scoping_b.ts in this same directory
// for true per-file module scope and import aliasing (TDD-00041).

import { add, mul, Point, Direction, squareOf } from './math'

console.log(add(2, 3))         // 5
console.log(mul(4, 5))         // 20

const p: Point = { x: 10, y: 20 }
console.log(p.x + p.y)         // 30

console.log(Direction.North)   // 0
console.log(Direction.West)    // 3

console.log(squareOf(6))       // 36 (via math.ts's own non-exported `square`)
