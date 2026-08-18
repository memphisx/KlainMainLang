# Type System

> Part of the [Implementation Status](README.md) index.

**Coverage**: Type primitives 12/12 (100%) · Type system features 10/12 (~83%).

**Strict Coverage**: Type primitives 7/12 (~58%) · Type system features 6/12 (50%). A row counts toward Strict only when its **Caveats** column is empty — the zero-known-caveats basis from the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)), now derived directly from the table so the two can't drift.

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

## Type primitives

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `number` → `i64` | ✅ | | |
| `string` → `ptr` | ✅ | | |
| `boolean` → `i1` | ✅ | | |
| `void` | ✅ | | |
| `null` / `undefined` | ✅ | | • Sentinel `ptr null` |
| JSDoc extended integers | ✅ | | • `@type {int8\|int16\|int32\|int64\|uint8…uint64}` |
| JSDoc extended floats | ✅ | • Precision is truncated on print — `console.log`/template-literal float-to-string uses bare `%g` (C's 6-significant-digit default), not JS's shortest-round-trip formatting: `1.1 + 2.2` prints `3.3` instead of `3.3000000000000003`, and any value with >6 significant digits or that trips `%g` into scientific notation prints wrong ([ADR-00166](../adr/ADR-00166.md)) | • `@type {float32\|float64}`<br>• The stored value and arithmetic are correct — only the printed representation deviates |
| `any` | ✅ | • Arithmetic on an `any` is a clean compile error<br>• Nested positions `any[]`/`{ x: any }` are clean compile errors<br>• A boxed array keeps only its data pointer — length/elements aren't recoverable, so it stringifies to the `[object Array]` tag, not its contents ([ADR-00177](../adr/ADR-00177.md)) | • Runtime-tagged value ([TDD-00062](../tdd/TDD-00062.md))<br>• Supports declare/assign/reassign/print/`typeof`/`===` + bare `any` param/return on every function shape<br>• Objects and arrays box by reference — `===` is reference identity, `typeof` is `"object"` ([ADR-00008](../adr/ADR-00008.md), [ADR-00176](../adr/ADR-00176.md), [ADR-00177](../adr/ADR-00177.md)) |
| `unknown` | ✅ | • Same as `any` (see above) | • Same Staged V1 scope as `any` |
| `never` | ✅ | | • A `(): never` function that always throws works correctly |
| `symbol` | ✅ | • No dynamic property keys<br>• No well-known symbols (`Symbol.iterator`, etc.)<br>• No `Symbol.for`/`Symbol.keyFor` registry | • V1 opaque unique values — `Symbol()`/`Symbol("desc")`, `===`/`!==`, `typeof`, `.description`, `.toString()`<br>• The fixed-shape object model has no dynamic-property-bag mechanism (blocks dynamic keys) and no runtime protocol-dispatch point (blocks the registry) ([TDD-00044](../tdd/TDD-00044.md)) |
| `bigint` | ✅ | • No `.toLocaleString()`, `BigInt.asIntN`/`asUintN`, or `BigInt64Array`/`BigUint64Array`; bare `Number(bigint)` conversion unsupported (part of a general `Number()`/`String()` gap) | • Backend-agnostic `__kml_bigint_*` ABI over an opaque `ptr` handle, selectable via `-bigint=libtommath\|gmp` (default `libtommath`, public domain; `gmp` LGPL) — each an embedded C file compiled+linked only when a program uses bigint ([TDD-00074](../tdd/TDD-00074.md)/[ADR-00216](../adr/ADR-00216.md))<br>• `123n`/hex/bin/oct/`_`-separator literals, `+ - * / % ** & \| ^ ~ << >>`, same-type comparisons **and cross-type comparison with an integer `number`** (`10n < 5`, `10n == 10`, exact), unary `-`/`~`, truthiness (`0n` falsy), `typeof`, `++`/`--`, compound assignment, `.toString([radix])`, params/returns/fields/arrays, `BigInt(int\|string)`, and the console-`n`-suffix vs `String()`-bare print split all work<br>• Division/modulo by zero throws a catchable `Error` (the same path as the i64 operators)<br>• Mixing a bigint and a number in **arithmetic** is a clean compile error (matches JS's `TypeError`); comparing a bigint with a **float** is a `-compat`-governed behavior ([TDD-00075](../tdd/TDD-00075.md)/[ADR-00217](../adr/ADR-00217.md)): default `strict` rejects it (a likely bug), and `-compat=js` does JS's exact real-number comparison (exact even past 2^53) — a design choice, not a caveat<br>• `>>>`, unary `+`, and `JSON.stringify` on a bigint are clean compile errors (all TypeErrors in JS)<br>• libbf (MIT) backend deferred to a follow-on stage |

## Type system features

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `T \| null` (nullable) | ✅ | • Only one non-null branch (`T \| null`, not `A \| B \| null`) | • Nullable flag |
| Object types (interfaces / inline `{}`) | ✅ | | • A literal's fields are coerced against the declared type wherever one is known (variable annotation, function parameter/return/default value, array element type, nested field) — not just the literal's own self-inferred type ([ADR-00077](../adr/ADR-00077.md)/[TDD-00007](../tdd/TDD-00007.md)) |
| Array types `T[]` | ✅ | | • `{ptr, i64}` aggregate |
| `Promise<T>` | ✅ | | |
| Function types `(a: T) => R` | ✅ | | • Closure struct `{funcPtr, envPtr}` |
| `Map<K,V>` | ✅ | | • Separate helpers for `<string,number>`, `<string,string>`, etc. |
| `Set<T>` | ✅ | | |
| Union types beyond `T \| null` | ✅ | • Scalar members only — `number`/`string`/`boolean`, plus `null`/`undefined`; no object/interface/array members<br>• Not supported nested inside an array element or object field (top level of a var declaration/function param/return only)<br>• No flow-based narrowing | • Same runtime box as `any`/`unknown` with a checked member set on top ([TDD-00043](../tdd/TDD-00043.md)/[ADR-00136](../adr/ADR-00136.md)) |
| Intersection types | ❌ | | |
| Tuple types | ✅ | • No element assignment (`t[0] = x`)<br>• No rest/optional/named elements<br>• No non-constant index<br>• No `.length` or array methods on a tuple<br>• Not supported nested in `any` or a union | • Fixed-arity, heterogeneous, positional value stored as a fixed-shape struct ([TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md))<br>• Supports declaration + tuple literal, constant-index read (`t[0]`), destructuring (`const [a, b] = t`, `for (const [a, b] of tuples)`, nested), tuple parameters/returns/fields, array-shaped rendering (JSON/`String()`/`console.log`)<br>• `Map`/`Array`/`Object.entries()` return real `[K, V]` tuples |
| Mapped / conditional types | ❌ | | |
| Generics on user functions/interfaces/classes | ✅ | • No explicit call-site type arguments (`identity<number>(5)`) — blocked by the `a<b>(c)` grammar ambiguity<br>• Unconstrained type parameters only (no constraints)<br>• A generic function needs an inferable `T`/`T[]`-typed parameter for each of its type parameters<br>• V2 `@erased` covers functions only (not interfaces/classes) and bare `T` positions only — `T[]` or `T` nested in an object field is a clean compile error<br>• Arithmetic on an erased `T` hits the same "operator on any/unknown" rejection as plain `any` | • V1 (default): monomorphization — one specialized implementation per distinct combination of concrete types actually used (`number`/`string`/`boolean`/arrays of these)<br>• Covers `function identity<T>(x: T): T`, `interface Box<T>`, `class Box<T>`, and multiple type parameters `<K, V>` ([TDD-00037](../tdd/TDD-00037.md)/[ADR-00132](../adr/ADR-00132.md))<br>• A generic class needs an explicit type-argument list at each `new Box<T>(...)` site; can't use `extends`/`implements`/`abstract`/static members ([ADR-00103](../adr/ADR-00103.md)/[TDD-00010](../tdd/TDD-00010.md))<br>• V2: `/** @erased */` above a generic function compiles its body once (bare-`T` positions become `any`/`unknown`), no name mangling or instantiation blowup ([ADR-00121](../adr/ADR-00121.md)) |
