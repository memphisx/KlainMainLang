# JSDoc

> Part of the [Implementation Status](README.md) index.

**Coverage**: JSDoc tags 20/22 (~91%) · JSDoc type expressions 14/15 (~93%).

**Strict Coverage**: JSDoc tags 3/22 (~14%) · JSDoc type expressions 3/15 (20%). A row counts toward Strict only when its **Caveats** column is empty.

Format: [Status page format](README.md#status-page-format).

This page tracks the JSDoc tags and type-expression syntaxes TypeScript itself
supports, per [the TypeScript JSDoc reference](https://www.typescriptlang.org/docs/handbook/jsdoc-supported-types.html) — the parity target. This compiler's own JSDoc **extensions** (which TypeScript has no equivalent for) are listed separately at the bottom and are **not** counted in the parity figures above.

Staged roadmap: [TDD-00125](../tdd/TDD-00125.md).

## JSDoc tags

| Tag | Status | Caveats | Notes |
|---|---|---|---|
| `@type` | ✅ | • Applies to variable, class-field, and type-alias positions | • The value string is parsed by the real type parser (Stage 4), so the full type-expression grammar in the table below is available ([TDD-00123](../tdd/TDD-00123.md) uses it for the `intN` override) |
| `@param` (aliases `@arg`, `@argument`) | ✅ | • Fills only a parameter with no inline `: T` annotation and no destructuring pattern (an inline type wins)<br>• A `{...T}` varargs type is stripped to its base `T`; binding it to a body that reads `arguments` still needs the unsupported `arguments` object — use a declared `...rest: T[]` parameter | • [TDD-00125](../tdd/TDD-00125.md) Stage 1; the type body uses the full grammar in the type-expression table; a `{number=}`/`[name]` optional decoration is recognized and stripped |
| `@returns` (alias `@return`) | ✅ | • Fills only when the function has no inline `: RetType` | • [TDD-00125](../tdd/TDD-00125.md) Stage 1 |
| `@typedef` | ✅ | • Both forms work: `@typedef {Object} Name` + `@property {T} field`, and the inline `@typedef {{x: number}} Name` object literal (since Stage 4)<br>• A `@typedef {T} Name` alias to a **width keyword** (`int32`) does not propagate the integer semantics — the same pre-existing limit a TS `type X = int32` alias has | • [TDD-00125](../tdd/TDD-00125.md) Stage 2; synthesized into a `type Name = …` declaration, so it resolves anywhere a type name does (`@type`/`@param`/inline) |
| `@callback` | ✅ | • Param/return types use the full type-expression grammar (Stage 4) | • [TDD-00125](../tdd/TDD-00125.md) Stage 2; synthesized into a function-type alias |
| `@template` | ✅ | • Inherits the TS generics scope: V1 monomorphization needs an inferable `T`/`T[]`-typed parameter, no explicit call-site type arguments ([TDD-00010](../tdd/TDD-00010.md)); a `{Base}` constraint on a multi-name tag applies to the first name (matching TS) | • [TDD-00125](../tdd/TDD-00125.md) Stage 3; sets the function's `<T>` list, so it drives the exact same monomorphization a TS `<T>` does. `@erased` (below) is the compile-once variant |
| `@satisfies` | ✅ | • Accepted and erased — the value keeps its own type, no `satisfies`-style excess-property/conformance check (parity with the erased TS `satisfies` operator — [ADR-00371](../adr/ADR-00371.md)) | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@enum` | ✅ | • The tagged `const` object works for value access (`Dir.Up`); it is a plain object, not a nominal enum type — no reverse mapping, no enum-member type narrowing | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@this` | ✅ | • Accepted and erased; a `this` parameter type is not separately modeled (`this` is bound by the class method it lives in) | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@extends` (alias `@augments`) | ✅ | • Accepted and erased — the class's own `extends` clause does the work; the tag does not add type arguments to a generic base | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@implements` | ✅ | • Accepted and erased — like a TS `implements` clause here, it does not strictly enforce structural conformance | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@public` | ✅ | • Accepted and erased, not enforced — the same stance as the TS `public` field modifier here | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@private` | ✅ | • Accepted and erased, not enforced — no access checking (the TS `private` modifier is likewise erased, not enforced, here) | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@protected` | ✅ | • Accepted and erased, not enforced | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@readonly` | ✅ | • Accepted and erased, not enforced — no mutation checking (parity with the erased TS `readonly` modifier — [ADR-00373](../adr/ADR-00373.md)) | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@override` | ✅ | • Accepted and erased — no override-consistency check | • [TDD-00125](../tdd/TDD-00125.md) Stage 5 |
| `@deprecated` | ✅ | • Documentation-only; accepted with no compile effect, but there is no tooling layer to surface a deprecation warning | • Parity with TS's runtime behavior |
| `@see` | ✅ | | • Documentation-only, no type effect — accepted, parity with TS |
| `@link` | ✅ | | • Documentation-only, no type effect — accepted, parity with TS |
| `@author` | ✅ | | • Documentation-only, no type effect — accepted, parity with TS |
| `@import` | ❌ | • The comment-form import statement is not synthesized; use a normal `import` plus an `import("./m").T` type reference (which resolves — see the type-expression table) |  |
| `@constructor` (alias `@class`) | ❌ | • Marks a plain function as a constructor (legacy pre-`class` JS) — unsupported; this compiler uses real `class` declarations, not function-as-constructor |  |

## JSDoc type expressions

| Syntax | Status | Caveats | Notes |
|---|---|---|---|
| Bare primitive (`string`, `number`, `boolean`, …) | ✅ | | • Resolved by the same type resolver a TS annotation uses |
| `T[]` array | ✅ | | • The `[]` suffix is handled by the resolver |
| Optional param (`T=`, `[name]`) | ✅ | • Recognized at the `@param` name/type level and stripped; the parameter is not yet marked structurally optional beyond an inline `?` | • [TDD-00125](../tdd/TDD-00125.md) Stage 1 |
| Varargs/rest (`...T`) | ✅ | • Stripped to the base `T`; the varargs body needs a declared `...rest` parameter (no `arguments`) | • [TDD-00125](../tdd/TDD-00125.md) Stage 1 |
| Union (`A \| B`) | ✅ | • Inherits the union V1 scope (scalar/object/`ReadableStream` members, narrowing rules — see the Type system page) | • [TDD-00125](../tdd/TDD-00125.md) Stage 4; the JSDoc string is parsed by the real type parser |
| Nullable (`?T`) | ✅ | • The leading marker (`?number` → `number \| null`); a `?` buried mid-expression is left to the parser | • [TDD-00125](../tdd/TDD-00125.md) Stage 4 |
| Non-null (`!T`) | ✅ | • The marker is stripped (`!number` → `number`) — TS treats non-null as no semantic change | • [TDD-00125](../tdd/TDD-00125.md) Stage 4 |
| Object shape (`{ a: string, b: number }`) | ✅ | • Nested braces are supported (`@param {{x: number}}`) via balanced-brace scanning | • [TDD-00125](../tdd/TDD-00125.md) Stage 4 |
| `Array.<T>` / `Array<T>` (generic array form) | ✅ | | • [TDD-00125](../tdd/TDD-00125.md) Stage 4; the Closure dot (`Array.<T>`) is normalized to `Array<T>` |
| `Object.<K, V>` (index map) | ❌ | • Normalized to `Record<K, V>`, but per-key field/index access on a `Record` isn't supported (index signatures are a separate ❌), so it compiles only where a `Record` would — [TYPE-SYSTEM.md](TYPE-SYSTEM.md) |  |
| Function type (`function(A): B`) | ✅ | • Rewritten to the arrow form `(arg0: A) => B`; a nested `function(...)` inside another type is not rewritten (rare) | • [TDD-00125](../tdd/TDD-00125.md) Stage 4 |
| `*` / `?` (Closure any/unknown) | ✅ | | • Both normalized to `any` ([TDD-00125](../tdd/TDD-00125.md) Stage 4) |
| `import("./m").T` type | ✅ | • The `import("./m").Name` qualifier is dropped to the bare `Name`, which resolves under whole-program compilation when that module is part of the build (typically because the file also imports from it) — a `Name` not otherwise pulled in won't resolve; the `typeof import(...)` value form is out of scope | • [TDD-00125](../tdd/TDD-00125.md) Stage 6 |
| `@typedef`/`@callback`-defined names | ✅ | | • [TDD-00125](../tdd/TDD-00125.md) Stage 2; inline `{{…}}` typedefs work since Stage 4 |
| Legacy synonyms (`String`→`string`, …) | ✅ | • `String`/`Number`/`Boolean`/`Void`/`Undefined`/`Null` remap to the lowercase primitive; bare `Object`/`object` are left as-is | • [TDD-00125](../tdd/TDD-00125.md) Stage 4 |

## Compiler extensions (no TypeScript equivalent)

Not counted in the parity figures above — these are this project's own additions
to `@type`/`@param`, for native-compilation needs TypeScript's `number`-only
model can't express.

| Extension | Status | Notes |
|---|---|---|
| Integer width keywords (`@type {int8..int64, uint8..uint64}`) | ✅ | • The real-integer escape hatch — exact semantics, bit-width storage; also usable in `@param`/`@returns` ([TDD-00123](../tdd/TDD-00123.md)) |
| Extended float keywords (`@type {float32, float64}`) | ✅ | • `float64` is the default `number`; `float32` is a narrower slot ([TDD-00080](../tdd/TDD-00080.md)) |
| `@erased` (generic single-body compilation) | ✅ | • Opts a generic function out of monomorphization — the JSDoc analogue of a `<T>` the compiler compiles once ([TDD-00010](../tdd/TDD-00010.md) V2) |
