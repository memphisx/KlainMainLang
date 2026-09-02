<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/json.json; edit the JSON, then run `make status`. -->

# JSON

> Part of the [Implementation Status](README.md) index.

**Coverage**: 14/15 (~93%) · **Strict Coverage**: 10/15 (~67%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `JSON.stringify(number)` | ✅ | | |
| `JSON.stringify(string)` | ✅ | | |
| `JSON.stringify(number[])` | ✅ | | |
| `JSON.stringify(string[])` | ✅ | | |
| `JSON.stringify(object)` | ✅ | | |
| `JSON.stringify(boolean[])` | ✅ | | |
| `JSON.stringify(object[])` | ✅ | | |
| `JSON.stringify(value, null, space)` (pretty-printing) | ✅ | • `space` must be a literal number (N spaces, capped at 10) or literal string — a runtime `space` value is a clean compile error ([ADR-00222](../adr/ADR-00222.md))<br>• The `replacer` (2nd) argument is supported only as `null`/undefined — a function/array replacer is a clean compile error, not silently ignored | • A `jsonIndent{unit,depth}` threaded through the serializer; empty containers stay inline (`{}`/`[]`), an array's empty-vs-non-empty close is a runtime `select` on its length ([TDD-00077](../tdd/TDD-00077.md) Track S) |
| `JSON.stringify` honors a class `toJSON()` | ✅ | | • A class with a `toJSON()` method serializes its result instead of its own fields, matching JS — same override dispatch as `toString()`. A `toJSON()` returning its own type is bounded against compile-time infinite recursion (cf. [ADR-00221](../adr/ADR-00221.md)). `Date` keeps its own `toISOString`-based path rather than being unified ([ADR-00222](../adr/ADR-00222.md)) |
| `JSON.stringify(mixedTypeArray)` | ❌ | | • Array literals, and their inferred element type, are uniform-type only — a heterogeneous array has no single element type to stringify against<br>• A fixed-length mixed sequence expressed as a **tuple** (`[number, string, boolean]`) does stringify correctly (`JSON.stringify([1, "two", true])` → `[1,"two",true]`) — the tuple, not a heterogeneous array, is how such a value is represented here |
| `JSON.parse(s)` → number | ✅ | | • Integer via `atoll`, float (incl. a `/** @type {float64} */`/`float32` variable) via `strtod` on the node's raw lexeme, through the type-directed projection ([ADR-00224](../adr/ADR-00224.md)) |
| `JSON.parse(s)` → object (nested objects, array & object-array fields) | ✅ | | • Type-directed projection off the parse tree ([TDD-00077](../tdd/TDD-00077.md)/[ADR-00224](../adr/ADR-00224.md)): nested objects, array-typed fields, and object-array fields (`Item[]`) all project through one path. A nullable-scalar field keeps its null-vs-value boxing; a missing field falls back to its type's default |
| `JSON.parse(s)` → top-level `T[]` (incl. object & nested arrays) | ✅ | • A bare reassignment into a **member or element** target (`obj.items = JSON.parse(...)`, `grid[i] = JSON.parse(...)`) isn't projected — assign into a declared variable or object field first; a typed var-decl and a declared-variable reassignment (`let xs: T[] = …; xs = JSON.parse(...)`) both project ([ADR-00571](../adr/ADR-00571.md)) | • Projects the tree's array node into the standard `{ptr,i64}` aggregate, reusing the array-literal build path ([ADR-00224](../adr/ADR-00224.md))<br>• A reassignment `xs = JSON.parse(s)` / `obj = await res.json()` into an array- or object-typed **variable** projects against the binding's declared type, the same as a typed var-decl ([ADR-00571](../adr/ADR-00571.md)) |
| `JSON.parse(s)` validates input (throws `SyntaxError` on malformed JSON) | ✅ | • The `SyntaxError` message is position-based (`Unexpected token in JSON at position N`), not Node/V8's exact per-token wording | • A real recursive-descent parser ([TDD-00077](../tdd/TDD-00077.md)/[ADR-00223](../adr/ADR-00223.md)) (embedded C, `__kml_json_*` ABI) builds+validates a tagged value tree. Strict JSON (double quotes, no trailing commas/leading zeros/trailing junk), `\uXXXX`+surrogate decoding, a 512-deep runtime depth guard on untrusted input. `Response.json()` inherits it |
| `JSON.parse(s)` → `any`/`unknown` (dynamic shape) | ✅ | • The result is a dynamic tree — statically-typed operations on it (arithmetic on elements, passing into typed slots) hit the normal `any` limits until narrowed | • Untyped/`any`-annotated `JSON.parse` keeps the whole tree as dynamic values ([TDD-00155](../tdd/TDD-00155.md) Stage 2, [ADR-00622](../adr/ADR-00622.md)): objects become runtime property bags, arrays dynamic arrays (`.length`, index reads/writes with JS extension semantics, `for...in`), scalars their runtime tags — `data.server.ports[0]` just works. A bare `const data = JSON.parse(s)` with no annotation is this dynamic parse, matching JS<br>• `JSON.stringify` serializes a dynamic tree back (round-trip clean): undefined values skipped in objects / `null` in arrays, NaN/Infinity → `null`, cycles throw `Converting circular structure to JSON` |
