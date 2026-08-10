// Re-exports (`export { a } from './x'`, TDD-00051) — this file only ever
// talks to re_export_lib.ts, which never declares add/multiply/greet
// itself; it just forwards them from re_export_core.ts. Import machinery
// (aliasing, default handling) is exactly the same as a direct import —
// the resolver transparently follows the re-export chain to the real
// declaration in re_export_core.ts.

import { add, multiply, greet } from './re_export_lib'

console.log(add(2, 3))          // 5
console.log(multiply(4, 5))     // 20
console.log(greet("world"))     // hello world
