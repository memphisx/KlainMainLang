# JSON

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~82% (9/11).

**Caveats**: `JSON.parse(s)` into an object with *nested* object fields is a clean compile error (flat objects, primitive fields only work) — design scoped in [TDD-00015](../tdd/TDD-00015.md). `JSON.stringify` on a mixed-type array isn't supported (array literals are uniform-type only). See Known Limitations below for a related, sharper-edged parse gap.

| Feature | Status |
|---|---|
| `JSON.stringify(number)` | ✅ |
| `JSON.stringify(string)` | ✅ |
| `JSON.stringify(number[])` | ✅ |
| `JSON.stringify(string[])` | ✅ |
| `JSON.stringify(object)` | ✅ |
| `JSON.stringify(boolean[])` | ✅ |
| `JSON.stringify(object[])` | ✅ |
| `JSON.parse(s)` → number | ✅ |
| `JSON.parse(s)` → object | ✅ (flat objects, primitive fields only — nested object fields give a clean compile error; see [ADR-00007](../adr/ADR-00007.md)) — a missing *string* field's default was fixed from a crash-causing `null` to an empty string; see [ADR-00024](../adr/ADR-00024.md) |
| `JSON.parse(s)` → object with *nested* object fields | ❌ (clean compile error today, not silent corruption — see [ADR-00007](../adr/ADR-00007.md); design scoped in [TDD-00015](../tdd/TDD-00015.md)) |
| `JSON.stringify(mixedTypeArray)` | ❌ (array literals, and their inferred element type, are uniform-type only — a heterogeneous array has no single element type to stringify against) |

## Known Limitations

| Limitation | Notes |
|---|---|
| `JSON.parse` into an array-typed interface field produces invalid LLVM IR (fails to compile) instead of a clean rejection | `interface Bag { tags: string[] }; const b: Bag = JSON.parse('{"tags":["a","b"]}')` — confirmed directly: `emitJSONParseObject`'s upfront rejection loop (`codegen/llvm/emit_call.go`) only checks `f.Ty.IsObject`, not `f.Ty.IsArray`, so an array field falls through to `emitJSONParseFieldValue`'s scalar-only switch, which defaults to `atoll` (`i64`-typed) merged via `phi` against the field's actual `ptr`/aggregate-typed slot — a type mismatch `clang` rejects (`'%tN' defined with type 'i64' but expected 'ptr'`). Not silent corruption (the mismatched IR never produces a runnable binary), but not the clean compile-time error nested-object fields already get either. Found while scoping [TDD-00015](../tdd/TDD-00015.md) (nested-object `JSON.parse`); fix is a one-line addition to the existing rejection check, tracked there rather than fixed here since it surfaced mid-design-writeup, not mid-implementation. |
