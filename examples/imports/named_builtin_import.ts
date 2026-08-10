// Named imports from a built-in module (TDD-00049 Stage 2) — unlike
// `import fs from 'fs'` (the default/namespace form every other built-in
// example in this repo uses), this binds individual members directly, with
// no `fs.` prefix at the call site. Resolved entirely at compile time into
// exactly the same dispatch as the default form (`readFileSync` here is
// indistinguishable, after resolution, from `fs.readFileSync` elsewhere) —
// see docs/adr/ADR-00141.md.
//
// The whole point, same as every other import in this project: the local
// name is the program's own choice (aliasing works, see `wfs` below), and a
// local variable happens to shadow an imported name exactly like real JS/TS
// lexical scoping — not a special case for built-ins specifically.

import { readFileSync, writeFileSync as wfs, existsSync, unlinkSync } from 'fs'
import { EOL } from 'os' // a plain value, not a function — same mechanism either way

const path = '/tmp/kml_named_import_example.txt'

console.log(existsSync(path)) // 0 (false)
wfs(path, 'hello from a named import')
console.log(existsSync(path)) // 1 (true)
console.log(readFileSync(path))
console.log(EOL === '\n') // 1 (true)
unlinkSync(path)
