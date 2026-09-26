// Type-only imports and exports. Every form is erased: `import type`, a
// `type` specifier in an ordinary list, `export type { … }`, and the types of
// a builtin module. Using a type-only import as a value is an error (TS1361).

import type { Box } from './type_only_shapes'
import { type Side, area } from './type_only_shapes'
import type { IncomingMessage } from 'http'
import { type ParsedPath, basename } from 'path'

const b: Box = { w: 6, h: 7 }
const s: Side = { label: "north" }
console.log(area(b), s.label)          // 42 north

function method(req: IncomingMessage): string {
    return req.method ?? "GET"
}
console.log(typeof method)             // function
console.log(basename("/tmp/report.txt")) // report.txt
