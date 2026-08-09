// Namespace imports (`import * as ns`, TDD-00042) — a compile-time-only
// construct: `ns.member` is resolved directly to that member's mangled name
// during resolution, never a real runtime object. `ns` itself has no
// runtime representation — using it as anything other than the object of a
// dotted member access (assigning it to a variable, passing it as a value)
// is a compile error. `ns.default` reaches a default export the same way
// any other member does.

import * as greetLib from './greet'

console.log(greetLib.shout("hey"))       // HEY
console.log(greetLib.default("there"))   // hello there
