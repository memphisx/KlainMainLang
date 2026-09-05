// /** @value */ flat value-type arrays (TDD-00134 Stage 2): the annotated
// array binding lays its elements out INLINE in one contiguous buffer (array
// of structs, stride = the element's struct size) instead of the default
// array-of-pointers — one allocation, cache-friendly traversal.
//
// This changes aliasing semantics, which is why it's an explicit opt-in:
//   - writing a value into a slot COPIES its fields — later mutation of the
//     source object is not seen through the array;
//   - reading arr[i] yields a VIEW (an interior pointer): arr[i].x = 1 and a
//     taken `const v = arr[i]; v.x = 1` both mutate the array in place;
//   - .push() may grow-reallocate the buffer, which invalidates any
//     previously-taken view — re-index after a push instead of holding views
//     across it.
//
// Supported surface: array-literal construction, index read/write, .length,
// for...of, and .push. Everything pointer-shaped (map/filter/sort/slice,
// spread, destructuring) is a compile-time rejection, never silent corruption.
interface Point { x: number; y: number }

function run(): void {
  const seed: Point = { x: 10, y: 20 }

  /** @value */
  const ps: Point[] = [seed, { x: 3, y: 4 }]

  // Construction copied seed's fields — this mutation is not visible in ps.
  seed.x = 999
  console.log("ps[0].x:", ps[0].x) // 10

  // arr[i] is a view: writing through it mutates the buffer in place.
  ps[0].x = 77
  console.log("after ps[0].x = 77:", ps[0].x) // 77

  // A slot write copies the value's fields (value semantics).
  const w: Point = { x: 5, y: 6 }
  ps[0] = w
  w.x = 111
  console.log("after ps[0] = w; w.x = 111:", ps[0].x) // 5

  // for...of binds a view per element — field writes stick.
  for (const p of ps) {
    p.y = p.y * 10
  }
  console.log("scaled y:", ps[0].y, ps[1].y) // 60 40

  // push appends by copying fields; the buffer may move (views invalidate).
  ps.push({ x: 100, y: 200 })
  console.log("length:", ps.length) // 3
  console.log("ps[2]:", ps[2].x, ps[2].y) // 100 200

  let sum = 0
  for (const p of ps) {
    sum += p.x + p.y
  }
  console.log("sum:", sum)
}
run()
