# Type System

> Part of the [Implementation Status](README.md) index.

**Coverage**: Type primitives ~64% · Type system features ~78%.

**Strict Coverage**: 13/20, 65% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number and the new caveat below; every caveat found by that audit excludes the row from this count even though the row stays ✅ in the Coverage column above.

**Caveats**:

- `any`/`unknown` work only in top-level positions: arithmetic on an `any`, and use as an array-*element* or object-*field* type (`any[]`, `{ x: any }`), are clean compile errors — only the declare/assign/print/`typeof`/`===` surface and bare param/return positions are supported ([TDD-00062](../tdd/TDD-00062.md)).
- An array boxed into an `any` keeps only its data pointer ([ADR-00177](../adr/ADR-00177.md)): `===` is reference identity and `typeof` is `"object"` (both matching JS), but its length/elements aren't recoverable, so it stringifies to the `[object Array]` tag rather than its contents.
- Union types (TDD-00043) are scalar-members only (`number`/`string`/`boolean`, plus `null`/`undefined`) — no object/interface/array members, and no union nested inside an array element or object field; both are clean compile errors. See [ADR-00136](../adr/ADR-00136.md).
- No flow-based narrowing (`typeof x === "string"` narrowing `x`'s effective type inside the branch) — see [TDD-00043](../tdd/TDD-00043.md).
- User-defined generics support any number of unconstrained type parameters but no explicit call-site type arguments (`identity<number>(5)`) or constraints; the opt-in `@erased` escape hatch is narrower still (functions only, bare `T` param/return position, not `T[]`). See [TDD-00010](../tdd/TDD-00010.md)/[TDD-00037](../tdd/TDD-00037.md).

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
| JSDoc extended floats | ✅ | `@type {float32\|float64}`. Precision is silently truncated on print — `console.log`/template-literal float-to-string formatting uses bare `%g` (C's 6-significant-digit default) instead of JS's shortest-round-trip formatting, so `1.1 + 2.2` prints `3.3` instead of `3.3000000000000003` and any value with >6 significant digits or that trips `%g` into scientific notation prints wrong. The stored value and arithmetic are correct; only the printed representation deviates. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
| `Map<K,V>` | ✅ | Separate helpers for `<string,number>`, `<string,string>`, etc. |
| `Set<T>` | ✅ | |
| Union types beyond `T \| null` | ✅ (V1: scalar members only — `number`/`string`/`boolean`, plus `null`/`undefined`; same Staged-V1 read/write restrictions as `any`/`unknown` below, since it's the same runtime box with a checked member set on top) | No object/interface/array members yet, and not yet supported nested inside an array element or object field (only at the top level of a var declaration/function param/return); no flow-based narrowing. See [TDD-00043](../tdd/TDD-00043.md)/[ADR-00136](../adr/ADR-00136.md). |
| Intersection types | ❌ | |
| Tuple types | ✅ | `[T0, T1, ...]` — a fixed-arity, heterogeneous, positional value stored as a fixed-shape struct ([TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md)). Declaration + tuple literal, constant-index read (`t[0]`), destructuring (`const [a, b] = t`, `for (const [a, b] of tuples)`, nested), tuple parameters/returns/fields, and array-shaped rendering (JSON/`String()`/`console.log`) all work; `Map`/`Array`/`Object.entries()` now return real `[K, V]` tuples. V1 excludes tuple element *assignment* (`t[0] = x`), rest/optional/named elements, a non-constant index, and array methods/`.length` on a tuple. |
| Mapped / conditional types | ❌ | |
| `any` | ✅ (Staged: declare/assign/reassign/print/`typeof`/`===` + bare `any` param/return on every function shape, [TDD-00062](../tdd/TDD-00062.md); objects and arrays box by reference; ❌ with a clean compile error for arithmetic and `any[]`/`{x:any}` nested positions — see [ADR-00008](../adr/ADR-00008.md), [ADR-00176](../adr/ADR-00176.md), [ADR-00177](../adr/ADR-00177.md)) | A boxed array's toString is the `[object Array]` tag, not its contents |
| `never` | ✅ | A function typed `(): never` that always throws works correctly |
| `unknown` | ✅ (same Staged V1 scope as `any` — see above) | |
| `symbol` | ✅ (V1: opaque unique values — `Symbol()`/`Symbol("desc")`, `===`/`!==`, `typeof`, `.description`, `.toString()`) | No dynamic property keys, no well-known symbols (`Symbol.iterator`, etc.), no `Symbol.for`/`Symbol.keyFor` registry — this compiler's fixed-shape object model has no dynamic-property-bag mechanism for the first, and no runtime protocol-dispatch point for the second. See [TDD-00044](../tdd/TDD-00044.md). |
| `bigint` | ❌ | |
| Generics on user functions/interfaces/classes | ✅ (V1: monomorphization; V2: opt-in erasure for functions) | **V1** — `function identity<T>(x: T): T`, `interface Box<T> { value: T }`, `class Box<T> { ... }`, or with multiple type parameters (`function firstOf<K, V>(k: K, v: V): K`, `interface Pair<K, V> { first: K; second: V }`, `class Pair<K, V> { ... }`, see [TDD-00037](../tdd/TDD-00037.md)/[ADR-00132](../adr/ADR-00132.md)) — one specialized implementation per distinct combination of concrete types actually used (`number`/`string`/`boolean`/arrays of these), the same approach the built-in generics already use by hand. Any number of unconstrained type parameters; a generic function needs an inferable `T`/`T[]`-typed parameter for *each* of its type parameters to infer from independently (no explicit call-site type arguments, `identity<number>(5)`, due to the `a<b>(c)` grammar ambiguity); a generic class needs an explicit type argument list at each `new Box<T>(...)`/`new Pair<K, V>(...)` site instead (unambiguous, so no inference needed there) and can't use `extends`/`implements`/`abstract`/static members. See [ADR-00103](../adr/ADR-00103.md)/[TDD-00010](../tdd/TDD-00010.md). **V2** — `/** @erased */` directly above a generic *function* declaration compiles its body exactly once (every bare-`T` parameter/return position becomes `any`/`unknown` under the hood) instead of once per call-site type argument combination; no name mangling, no instantiation blowup, but only a bare `T` position is erased (`T[]`, or `T` nested in an object field, are a clean compile error, not silently accepted) and arithmetic on an erased `T` hits the same pre-existing "operator on any/unknown" rejection as plain `any`. Interfaces/classes aren't covered by V2. See [ADR-00121](../adr/ADR-00121.md). A real, unrelated bug found while researching the original TDD — `Array<T>`/`Map<K,V>`/`Set<T>` used as a plain type annotation (not `new X<T>()`) silently defaulting to `i64` — was fixed separately; see [ADR-00058](../adr/ADR-00058.md). |

## Known Limitations

| Limitation | Notes |
|---|---|
| Booleans print as `"true"`/`"false"` | `console.log(bool)` prints `"true"`/`"false"`, matching real JS/TS — both for a plain `boolean` and for an `any`-typed variable holding one, now consistent with template-literal interpolation (which always used `"true"`/`"false"`). `console.log` had previously printed a plain boolean as the raw `1`/`0`; that divergence was a deferred shortcut, not intended behavior, and is fixed — see [ADR-00183](../adr/ADR-00183.md). |
