# JSON

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~82% (9/11).

**Strict Coverage**: 7/11, ~64% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number and the new caveat above.

**Caveats**:

- `JSON.parse(s)` into an object with *nested* object fields is a clean compile error — only flat objects with primitive fields work. Scoped in [TDD-00015](../tdd/TDD-00015.md).
- `JSON.stringify` on a mixed-type array isn't supported (array literals are uniform-type only).
- See Known Limitations below for a related, sharper-edged parse gap.

| Feature | Status |
|---|---|
| `JSON.stringify(number)` | ✅ |
| `JSON.stringify(string)` | ✅ |
| `JSON.stringify(number[])` | ✅ |
| `JSON.stringify(string[])` | ✅ |
| `JSON.stringify(object)` | ✅ |
| `JSON.stringify(boolean[])` | ✅ |
| `JSON.stringify(object[])` | ✅ |
| `JSON.parse(s)` → number | ✅ (into a plain, default-`i64` `number` variable only — parsing into a `/** @type {float64} */`/`float32`-annotated `number` fails to *compile*: `emitJSONParseValue` special-cases only `IsObject` and the plain-`i64` case, so a float target falls through to the generic branch, which returns a `ptr`-typed value that gets `store`d into the `double`/`float`-typed alloca the annotation allocated — a hard `clang` type-mismatch error. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `JSON.parse(s)` → object | ✅ (flat objects, primitive fields only — nested object fields give a clean compile error; see [ADR-00007](../adr/ADR-00007.md)) — a missing *string* field's default was fixed from a crash-causing `null` to an empty string; see [ADR-00024](../adr/ADR-00024.md) |
| `JSON.parse(s)` → object with *nested* object fields | ❌ (clean compile error today, not silent corruption — see [ADR-00007](../adr/ADR-00007.md); design scoped in [TDD-00015](../tdd/TDD-00015.md)) |
| `JSON.stringify(mixedTypeArray)` | ❌ (array literals, and their inferred element type, are uniform-type only — a heterogeneous array has no single element type to stringify against) |

## Known Limitations

| Limitation | Notes |
|---|---|
| `JSON.parse` into an array-typed interface field is not yet parsed — a clean compile-time rejection | `interface Bag { tags: string[] }; const b: Bag = JSON.parse('{"tags":["a","b"]}')` now raises `JSON.parse into an array-typed field ('tags') is not yet supported`, the same clean rejection nested-object fields already get, rather than the invalid LLVM IR it produced before ([ADR-00189](../adr/ADR-00189.md) added the missing `f.Ty.IsArray` branch to `emitJSONParseObject`'s upfront rejection loop). Actually parsing a JSON array into the field stays out of scope, alongside nested-object fields ([TDD-00015](../tdd/TDD-00015.md)). |
