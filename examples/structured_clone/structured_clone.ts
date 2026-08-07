// structuredClone(obj) — a genuine recursive deep copy.
//
// Scope (see docs/status/GLOBAL-FUNCTIONS.md): arrays (including nested
// arrays and TypedArrays) and plain objects/interfaces recurse field-by-
// field/element-by-element into freshly allocated storage; scalars
// (numbers, strings, booleans, Date) are value types already, so cloning
// one is just reusing the same value. Map/Set/EventEmitter/URL/
// ArrayBuffer/functions/class instances/Error/Promise/any are rejected at
// compile time rather than silently aliased.

interface Point {
  x: number
  y: number
}
interface Shape {
  name: string
  points: Point[]
}

const original: Shape = {
  name: "triangle",
  points: [
    { x: 0, y: 0 },
    { x: 1, y: 1 },
    { x: 2, y: 0 },
  ],
}

const clone = structuredClone(original)
clone.name = "renamed"
clone.points[0].x = 500

// The clone is a fully independent copy — mutating it never touches the original.
console.log(original.name)      // triangle
console.log(clone.name)         // renamed
console.log(original.points[0].x) // 0
console.log(clone.points[0].x)    // 500

// Same guarantee for plain arrays, including nested ones.
const grid: number[][] = [[1, 2], [3, 4]]
const gridClone = structuredClone(grid)
gridClone[0][0] = 999
console.log(grid[0][0])      // 1
console.log(gridClone[0][0]) // 999
