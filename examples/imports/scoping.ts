// True per-file module scope + import aliasing (TDD-00041).
//
// scoping_a.ts and scoping_b.ts each privately declare their own `helper`
// function and both export a function named `run` — none of that collides,
// since every top-level declaration gets a file-private internal name.
// Importing the same exported name from two different files just needs an
// alias (`as`) to bind them to two different local names here.

import { run as runA } from './scoping_a'
import { run as runB } from './scoping_b'

console.log(runA())  // a's own helper
console.log(runB())  // b's own helper
