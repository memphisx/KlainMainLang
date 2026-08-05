# Type System

> Part of the [Implementation Status](README.md) index.

**Coverage**: Type primitives ~57% · Type system features ~70%.

**Caveats**: Union types beyond `T | null` aren't supported (the parser discards every union member except the first) — see [ADR-00008](../adr/ADR-00008.md). `any`/`unknown` are Staged V1: declare/assign/reassign/print/`typeof`/`===` work, but arithmetic and use as a function param/return/array/object-field type are a clean compile error. User-defined generics (`function identity<T>`, `interface Box<T>`, `class Box<T>`) are done as of [ADR-00103](../adr/ADR-00103.md)/[TDD-00010](../tdd/TDD-00010.md)'s V1 (monomorphization, single unconstrained type parameter, functions need an inferable `T`/`T[]`-typed parameter, classes need an explicit `new Box<T>(...)` type argument) — V2 (an opt-in JSDoc escape hatch into type erasure) is still not started.

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
| Generics on user functions/interfaces/classes | ✅ (V1: monomorphization) | `function identity<T>(x: T): T`, `interface Box<T> { value: T }`, `class Box<T> { ... }` — one specialized implementation per distinct concrete type actually used (`number`/`string`/`boolean`/arrays of these), the same approach the built-in generics already use by hand. Single, unconstrained type parameter only; a generic function needs at least one `T`/`T[]`-typed parameter to infer from (no explicit call-site type arguments, `identity<number>(5)`, due to the `a<b>(c)` grammar ambiguity); a generic class needs an explicit type argument at each `new Box<T>(...)` site instead (unambiguous, so no inference needed there) and can't use `extends`/`implements`/`abstract`/static members. See [ADR-00103](../adr/ADR-00103.md)/[TDD-00010](../tdd/TDD-00010.md). V2 (an opt-in JSDoc escape hatch into type erasure, for cases monomorphization handles badly) is scoped but not started. A real, unrelated bug found while researching the original TDD — `Array<T>`/`Map<K,V>`/`Set<T>` used as a plain type annotation (not `new X<T>()`) silently defaulting to `i64` — was fixed separately; see [ADR-00058](../adr/ADR-00058.md). |

## Known Limitations

| Limitation | Notes |
|---|---|
| `any`/`unknown` boxed booleans print as `"true"`/`"false"` | `console.log`ing a plain (non-`any`) `boolean` prints `1`/`0` in this compiler (an existing, unrelated quirk — see `examples/strings/string_methods.ts`'s comments), but an `any`-typed variable currently holding a boolean prints `"true"`/`"false"` instead, since the dynamic-value formatter shares one code path for both `console.log` and template literals and mirrors the template-literal convention (which already uses `"true"`/`"false"`) rather than special-casing `console.log`'s raw-boolean convention differently per call site. Deliberate, documented simplification — see [ADR-00008](../adr/ADR-00008.md). |
