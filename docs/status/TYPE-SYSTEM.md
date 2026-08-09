# Type System

> Part of the [Implementation Status](README.md) index.

**Coverage**: Type primitives ~64% · Type system features ~74%.

**Caveats**: `any`/`unknown` are Staged V1: declare/assign/reassign/print/`typeof`/`===` work, but arithmetic and use as a function param/return/array/object-field type are a clean compile error (except a `@erased` generic function's own bare-`T` positions — see below). General union types (`string | number`, TDD-00043) share that exact same runtime representation and the same Staged-V1 restrictions, plus their own: V1 scope is scalar members only (`number`/`string`/`boolean`, plus `null`/`undefined` via the pre-existing `Nullable` flag) — no object/interface/array members, and a union nested inside an array element or object field (not just at the top level of a var declaration/function param/return) is also not yet supported; both are a clean compile error. No flow-based narrowing (`typeof x === "string"` narrowing `x`'s effective type inside the branch) either — see [ADR-00136](../adr/ADR-00136.md)/[TDD-00043](../tdd/TDD-00043.md). User-defined generics (`function identity<T>`, `interface Box<T>`, `class Box<T>`) support any number of unconstrained type parameters (`<K, V>`, see [TDD-00037](../tdd/TDD-00037.md)/[ADR-00132](../adr/ADR-00132.md)) but no explicit call-site type arguments (`identity<number>(5)`) or constraints, regardless of whether a declaration uses default monomorphization (functions need an inferable `T`/`T[]`-typed parameter per type parameter to infer from; classes need an explicit `new Box<K, V>(...)` type argument list instead) or the opt-in `@erased` type-erasure escape hatch, which is narrower still — functions only, and only a bare `T` parameter/return position, not `T[]`. See [TDD-00010](../tdd/TDD-00010.md).

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
| Union types beyond `T \| null` | ✅ (V1: scalar members only — `number`/`string`/`boolean`, plus `null`/`undefined`; same Staged-V1 read/write restrictions as `any`/`unknown` below, since it's the same runtime box with a checked member set on top) | No object/interface/array members yet, and not yet supported nested inside an array element or object field (only at the top level of a var declaration/function param/return); no flow-based narrowing. See [TDD-00043](../tdd/TDD-00043.md)/[ADR-00136](../adr/ADR-00136.md). |
| Intersection types | ❌ | |
| Tuple types | ❌ | |
| Mapped / conditional types | ❌ | |
| `any` | ✅ (Staged V1: declare/assign/reassign/print/`typeof`/`===`; arithmetic and use as a function param/return/array/object-field type are ❌ with a clean compile error — see [ADR-00008](../adr/ADR-00008.md)) | |
| `never` | ✅ | A function typed `(): never` that always throws works correctly |
| `unknown` | ✅ (same Staged V1 scope as `any` — see above) | |
| `symbol` | ✅ (V1: opaque unique values — `Symbol()`/`Symbol("desc")`, `===`/`!==`, `typeof`, `.description`, `.toString()`) | No dynamic property keys, no well-known symbols (`Symbol.iterator`, etc.), no `Symbol.for`/`Symbol.keyFor` registry — this compiler's fixed-shape object model has no dynamic-property-bag mechanism for the first, and no runtime protocol-dispatch point for the second. See [TDD-00044](../tdd/TDD-00044.md). |
| `bigint` | ❌ | |
| Generics on user functions/interfaces/classes | ✅ (V1: monomorphization; V2: opt-in erasure for functions) | **V1** — `function identity<T>(x: T): T`, `interface Box<T> { value: T }`, `class Box<T> { ... }`, or with multiple type parameters (`function firstOf<K, V>(k: K, v: V): K`, `interface Pair<K, V> { first: K; second: V }`, `class Pair<K, V> { ... }`, see [TDD-00037](../tdd/TDD-00037.md)/[ADR-00132](../adr/ADR-00132.md)) — one specialized implementation per distinct combination of concrete types actually used (`number`/`string`/`boolean`/arrays of these), the same approach the built-in generics already use by hand. Any number of unconstrained type parameters; a generic function needs an inferable `T`/`T[]`-typed parameter for *each* of its type parameters to infer from independently (no explicit call-site type arguments, `identity<number>(5)`, due to the `a<b>(c)` grammar ambiguity); a generic class needs an explicit type argument list at each `new Box<T>(...)`/`new Pair<K, V>(...)` site instead (unambiguous, so no inference needed there) and can't use `extends`/`implements`/`abstract`/static members. See [ADR-00103](../adr/ADR-00103.md)/[TDD-00010](../tdd/TDD-00010.md). **V2** — `/** @erased */` directly above a generic *function* declaration compiles its body exactly once (every bare-`T` parameter/return position becomes `any`/`unknown` under the hood) instead of once per call-site type argument combination; no name mangling, no instantiation blowup, but only a bare `T` position is erased (`T[]`, or `T` nested in an object field, are a clean compile error, not silently accepted) and arithmetic on an erased `T` hits the same pre-existing "operator on any/unknown" rejection as plain `any`. Interfaces/classes aren't covered by V2. See [ADR-00121](../adr/ADR-00121.md). A real, unrelated bug found while researching the original TDD — `Array<T>`/`Map<K,V>`/`Set<T>` used as a plain type annotation (not `new X<T>()`) silently defaulting to `i64` — was fixed separately; see [ADR-00058](../adr/ADR-00058.md). |

## Known Limitations

| Limitation | Notes |
|---|---|
| `any`/`unknown` boxed booleans print as `"true"`/`"false"` | `console.log`ing a plain (non-`any`) `boolean` prints `1`/`0` in this compiler (an existing, unrelated quirk — see `examples/strings/string_methods.ts`'s comments), but an `any`-typed variable currently holding a boolean prints `"true"`/`"false"` instead, since the dynamic-value formatter shares one code path for both `console.log` and template literals and mirrors the template-literal convention (which already uses `"true"`/`"false"`) rather than special-casing `console.log`'s raw-boolean convention differently per call site. Deliberate, documented simplification — see [ADR-00008](../adr/ADR-00008.md). |
