# Type System

> Part of the [Implementation Status](README.md) index.

**Coverage**: Type primitives ~57% · Type system features ~65%.

**Caveats**: Union types beyond `T | null` aren't supported (the parser discards every union member except the first) — see [ADR-00008](../adr/ADR-00008.md). `any`/`unknown` are Staged V1: declare/assign/reassign/print/`typeof`/`===` work, but arithmetic and use as a function param/return/array/object-field type are a clean compile error. Generics are built-ins only (`T[]`, `Promise<T>`, `Array<T>`, `Map<K,V>`, `Set<T>`) — user-defined generics are scoped but not started, see [TDD-00010](../tdd/TDD-00010.md).

| Feature | Status | Notes |
|---|---|---|
| `number` → `i64` | ✅ | |
| `string` → `ptr` | ✅ | |
| `boolean` → `i1` | ✅ | |
| `void` | ✅ | |
| `null` / `undefined` | ✅ | Sentinel `ptr null` |
| `T \| null` (nullable) | ✅ | Nullable flag; only one non-null branch |
| Object types (interfaces / inline `{}`) | ✅ | A literal's fields are coerced against the declared type wherever one is known (variable annotation, function parameter/return/default value, array element type, nested field) — not just the literal's own self-inferred type. See [ADR-00077](../adr/ADR-00077.md)/[TDD-00007](../tdd/TDD-00007.md). |
| Array types `T[]` | ✅ | `{ptr, i64}` aggregate |
| `Promise<T>` | ✅ | |
| Function types `(a: T) => R` | ✅ | Closure struct `{funcPtr, envPtr}` |
| JSDoc extended integers | ✅ | `@type {int8\|int16\|int32\|int64\|uint8…uint64}` |
| JSDoc extended floats | ✅ | `@type {float32\|float64}` |
| `Map<K,V>` | ✅ | Separate helpers for `<string,number>`, `<string,string>`, etc. |
| `Set<T>` | ✅ | |
| Union types beyond `T \| null` | ❌ | Parser discards every union member except the first for anything other than `null`/`undefined` — needs parser work, separate from the `any`/`unknown` tagged-value system below; see [ADR-00008](../adr/ADR-00008.md) |
| Intersection types | ❌ | |
| Tuple types | ❌ | |
| Mapped / conditional types | ❌ | |
| `any` | ✅ (Staged V1: declare/assign/reassign/print/`typeof`/`===`; arithmetic and use as a function param/return/array/object-field type are ❌ with a clean compile error — see [ADR-00008](../adr/ADR-00008.md)) | |
| `never` | ✅ | A function typed `(): never` that always throws works correctly |
| `unknown` | ✅ (same Staged V1 scope as `any` — see above) | |
| `symbol` | ❌ | |
| `bigint` | ❌ | |
| Generics on user functions/interfaces | ❌ | Only built-in generics (`T[]`, `Promise<T>`, `Array<T>`, `Map<K,V>`, `Set<T>`) — staged design for user-defined generics in [TDD-00010](../tdd/TDD-00010.md). A real bug found while researching that TDD — `Array<T>`/`Map<K,V>`/`Set<T>` used as a plain type annotation (not `new X<T>()`) silently defaulting to `i64` — is fixed; see [ADR-00058](../adr/ADR-00058.md). |

## Known Limitations

| Limitation | Notes |
|---|---|
| `any`/`unknown` boxed booleans print as `"true"`/`"false"` | `console.log`ing a plain (non-`any`) `boolean` prints `1`/`0` in this compiler (an existing, unrelated quirk — see `examples/strings/string_methods.ts`'s comments), but an `any`-typed variable currently holding a boolean prints `"true"`/`"false"` instead, since the dynamic-value formatter shares one code path for both `console.log` and template literals and mirrors the template-literal convention (which already uses `"true"`/`"false"`) rather than special-casing `console.log`'s raw-boolean convention differently per call site. Deliberate, documented simplification — see [ADR-00008](../adr/ADR-00008.md). |
