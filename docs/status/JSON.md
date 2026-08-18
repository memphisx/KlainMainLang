# JSON

> Part of the [Implementation Status](README.md) index.

**Coverage**: 13/15 (~87%) · **Strict Coverage**: 10/15 (~67%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

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
| `JSON.stringify(mixedTypeArray)` | ❌ | | • Array literals, and their inferred element type, are uniform-type only — a heterogeneous array has no single element type to stringify against |
| `JSON.parse(s)` → number | ✅ | | • Integer via `atoll`, float (incl. a `/** @type {float64} */`/`float32` variable) via `strtod` on the node's raw lexeme — the float-variable compile failure the 2026-08-11 audit found ([ADR-00166](../adr/ADR-00166.md)) is fixed by P3's type-directed projection ([ADR-00224](../adr/ADR-00224.md)) |
| `JSON.parse(s)` → object (nested objects, array & object-array fields) | ✅ | | • P3 type-directed projection off the parse tree ([TDD-00077](../tdd/TDD-00077.md)/[ADR-00224](../adr/ADR-00224.md)): nested objects, array-typed fields, and object-array fields (`Item[]`) all project through one path, superseding the old flat-only extractor ([ADR-00007](../adr/ADR-00007.md)) and the array-field rejection ([ADR-00189](../adr/ADR-00189.md)). A nullable-scalar field keeps its null-vs-value boxing; a missing field falls back to its type's default |
| `JSON.parse(s)` → top-level `T[]` (incl. object & nested arrays) | ✅ | • Only in a type-annotated position (`const xs: T[] = …`, or a field) — a bare reassignment `xs = JSON.parse(...)` has no target type to project against and isn't supported | • Projects the tree's array node into the standard `{ptr,i64}` aggregate, reusing the array-literal build path ([ADR-00224](../adr/ADR-00224.md)) |
| `JSON.parse(s)` validates input (throws `SyntaxError` on malformed JSON) | ✅ | • The `SyntaxError` message is position-based (`Unexpected token in JSON at position N`), not Node/V8's exact per-token wording | • P1 of the parse rewrite ([TDD-00077](../tdd/TDD-00077.md)/[ADR-00223](../adr/ADR-00223.md)): a real recursive-descent parser (embedded C, `__kml_json_*` ABI) builds+validates a tagged value tree. Strict JSON (double quotes, no trailing commas/leading zeros/trailing junk), `\uXXXX`+surrogate decoding, a 512-deep runtime depth guard on untrusted input. `Response.json()` inherits it |
| `JSON.parse(s)` → `any`/`unknown` (dynamic shape) | ❌ | | • Needs the tree kept as a dynamic value — [TDD-00077](../tdd/TDD-00077.md) Track P P4, coupled to the dynamic object model ([TDD-00068](../tdd/TDD-00068.md)) |
