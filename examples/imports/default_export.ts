// export default / default imports (TDD-00042).
//
// `export default function greet(...) {...}` in greet.ts binds its export
// under the synthetic key "default" — importable here as `import greet from
// './greet'` (any local name works; unlike a named import, there's no
// `{ ... }` list and no fixed name to match). A default import can also be
// combined with a named-import list in one statement, as below.

import greet, { shout } from './greet'

console.log(greet("world"))   // hello world
console.log(shout("hi"))      // HI
