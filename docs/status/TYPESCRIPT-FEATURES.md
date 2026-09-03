<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/typescript-features.json; edit the JSON, then run `make status`. -->

# TypeScript language features (completeness index)

> Part of the [Implementation Status](README.md) index. A **completeness map of TypeScript's language features** — the companion to the [Node built-in modules index](NODE-MODULES.md). It says, at a glance, which TS features are supported, which work with real limits, which are not started, and which are out of scope for a **whole-program AOT compiler with fixed-shape objects and no runtime type system**. This page is **informational**: its rows are *not* counted toward the coverage percentages — the detailed, counted pages ([Type system](TYPE-SYSTEM.md), [Language constructs](LANGUAGE-CONSTRUCTS.md), [Modules](MODULES.md), [JSDoc](JSDOC.md)) carry the per-feature detail and the real numbers. Follow a row's link for its caveats.

Format: [Status page format](README.md#status-page-format). ✅ = the feature works (see its detail page for caveats); ❌ = not available today. Two honest through-lines: **type-level assertions and utility modifiers are erased, not enforcing** (`as`/`satisfies`/`readonly`/`Partial`/template-literal types widen or erase), and **unions/tuples/intersections carry real fixed-shape restrictions**.

## Implemented

| Feature | Status | Notes |
|---|---|---|
| Primitive & literal types (`number`, `string`, `boolean`, `void`, `null`/`undefined`, `bigint`) | ✅ | • → [Type system](TYPE-SYSTEM.md); `number` is a JS-faithful IEEE-754 double, `bigint` is full |
| All operators (arithmetic, `**`, bitwise, logical, `??`, `?.`, `in`, compound + logical assignment, `++`/`--`) | ✅ | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Control flow (`if`/`while`/`do…while`/`for`/`for…in`/`switch`/labeled `break`·`continue`/`try·catch·finally`) | ✅ | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Variables & destructuring (`const`/`let`/`var` + TDZ; array/object patterns — nested, rest, defaults, renaming) | ✅ | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Functions (declarations, arrow, expressions/IIFE, default/optional/rest/destructured params, tagged templates) | ✅ | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Classes & OOP (fields, `constructor`, param properties, `static`/`static {}`, `#private`, `abstract`, `extends`/`super`, get/set, enforced `readonly`, `instanceof`) | ✅ | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Enums (numeric & string, incl. `declare [const] enum`) | ✅ | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Interfaces (structural), type aliases, object literals | ✅ | • → [Type system](TYPE-SYSTEM.md) |
| Core type constructs (`T | null`, `T[]`, function types, `Map`/`Set`/`Promise<T>`, tuple types, index signatures, `typeof` queries) | ✅ | • → [Type system](TYPE-SYSTEM.md) |
| Async / iterators (`async`/`await`, the `Promise.*` combinators, generators + async generators, `for await…of`) | ✅ | • Near-spec fidelity incl. V8-matching microtask ordering — a differentiator → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Modules (all import/export forms, namespace imports, re-exports, circular graphs, CommonJS `require`, `import.meta.url`, `node:` prefix) | ✅ | • → [Modules](MODULES.md) |
| JSDoc type-carrying tags (`@type`/`@param`/`@returns`/`@typedef`/`@callback`/`@template` + full type-expression grammar) | ✅ | • Plus compiler extensions `intN`/`floatN`/`@erased`/`@pure` → [JSDoc](JSDOC.md) |

## Partial — works, with real caveats

The honest middle column: each of these compiles and runs for its core case but carries a limitation driven by the fixed-shape runtime or type erasure. The linked detail page has the specifics.

| Feature | Status | Caveats |
|---|---|---|
| `any` / `unknown` | ✅ | • Full only under `-compat=js` (NaN-boxed D1 dynamic objects/prototypes/descriptors); under `-compat=strict` arithmetic on `any` is a compile error. Gaps: `ToPrimitive` on objects, primitive-member dispatch through `any`, `Object.values/entries` on dynamic objects, allocation-site widening of a boxed static object → [Type system](TYPE-SYSTEM.md) |
| Union types | ✅ | • Scalars, single-object, and first-position string-literal discriminated unions only; no array-element unions, non-first-position/number-literal tags; narrowing is local (`typeof`/truthiness/`==null`) — no `switch(typeof)`, `as`-narrowing, or tag narrowing → [Type system](TYPE-SYSTEM.md) |
| Intersection types | ✅ | • Object-type members only; conflicting non-object fields rejected (TS `never`-field not modeled) → [Type system](TYPE-SYSTEM.md) |
| Tuple types | ✅ | • No rest/optional/named elements; constant index only; no array methods; not nestable in `any`/union → [Type system](TYPE-SYSTEM.md) |
| Mapped & utility types | ✅ | • Effective ones are `Pick`/`Omit`/`Record`; `Partial`/`Required`/`Readonly` are erased structural no-ops. No key remapping (`as`), no `-?`/`-readonly` modifier removal; compile-time-only → [Type system](TYPE-SYSTEM.md) |
| Conditional types + `infer` | ✅ | • `infer` limited to `Array`/`Promise<infer>` and bare `infer R`; no `(...) => infer R`; assignability is structural width, not full variance → [Type system](TYPE-SYSTEM.md) |
| Generics | ✅ | • Monomorphization-based; **no call-site type args for functions** (generic *classes* take `new Box<T>()`); each type param needs an inferable `T`/`T[]` param; generic functions aren't first-class values → [Type system](TYPE-SYSTEM.md) |
| Type assertions — `as T` / `as const` / `satisfies` / `<T>x` | ✅ | • **Erased, not enforcing** — the value keeps its inferred type; a cast relied on for re-typing does not take effect (matches TS runtime erasure, not static narrowing) → [Type system](TYPE-SYSTEM.md) |
| Template-literal types & string-literal types | ✅ | • Parsed but **erased/widened to `string`**, not narrowed or enforced → [Type system](TYPE-SYSTEM.md) |
| `readonly T[]` modifier | ✅ | • Erased, not enforced; `ReadonlyArray<T>` alias not covered → [Type system](TYPE-SYSTEM.md) |
| Ambient declarations (`declare var`/`function`/`enum`/`class`/`module`/`namespace`/`global`) | ✅ | • `declare var`/`function`/`enum` are real bindings; brace-bodied ambient forms parsed and erased (no external link target under whole-program AOT) → [Type system](TYPE-SYSTEM.md) |
| Namespaces | ✅ | • Top-level only; no `declare namespace`; members desugar to bare-name top-level decls (cross-namespace same-name class collides) → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Function overloads | ✅ | • Signatures parsed and **erased**; call sites check the implementation only (no per-signature narrowing) → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| `Function.prototype.call`/`apply`/`bind` | ✅ | • `thisArg` evaluated then ignored (no rebindable `this`, so no method-borrowing); first-class function values only, not builtins → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Decorators | ✅ | • Both dialects, all placements, factory, captures, metadata; class-decorator **replacement** is a documented static-model divergence (refused at runtime) and standard static-field decorators are rejected → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Symbols | ✅ | • V1 opaque unique values (`Symbol()`, `===`, `typeof`, `.description`, `Symbol.for`/`keyFor`); no dynamic property keys; only `[Symbol.iterator]`/`[Symbol.asyncIterator]` recognized as computed keys → [Type system](TYPE-SYSTEM.md) |

## Not started (in scope)

Features that fit the model and would add value, not yet built.

| Feature | Status | Notes |
|---|---|---|
| `typeof import(...)` (value form) | ❌ | • The type-position `import('./m').T` works; the value form is not started → [JSDoc](JSDOC.md) |
| Namespace re-exports (`export * as ns from …`) | ❌ | • Parsed and explicitly rejected → [Modules](MODULES.md) |
| `ReadonlyArray<T>` alias form | ❌ | • → [Type system](TYPE-SYSTEM.md) |
| Full variance / bivariance in assignability | ❌ | • Only structural width subtyping today |
| Key remapping in mapped types (`as`), `-?`/`-readonly` modifier removal | ❌ | • → [Type system](TYPE-SYSTEM.md) |
| Spread into fixed-arity functions / uncommon variadic builtins (`f(...arr)`, `Math.hypot(...a)`) | ❌ | • Clean compile error (TDD-00106) |
| `for` non-declaration init clause (`for (i = 0, j = 10; …)`) | ❌ | • Clean rejection (the update clause does take comma-separated expressions) |

## Out of scope (by the whole-program AOT / no-runtime / fixed-shape model)

Features that depend on a live JS engine, a runtime type system, a runtime module loader, or a dynamic property model native ahead-of-time output has no equivalent for. Listed for completeness, not planned.

| Feature | Status | Notes |
|---|---|---|
| JSX / TSX | ❌ | • No JSX parsing at all — needs a JSX-runtime / framework model the fixed-shape, no-reconciler target has no equivalent for |
| Runtime-computed dynamic `import(expr)` | ❌ | • Whole-program AOT can't resolve a runtime specifier ([ADR-00022](../adr/ADR-00022.md)); literal-specifier lazy islands *are* shipped → [Modules](MODULES.md) |
| Dynamic property add/delete on statically-typed structs | ❌ | • By design (two-tier object model); dynamic objects get it under `any`/`-compat=js` |
| Well-known symbols as first-class runtime values / a Symbol-keyed protocol registry | ❌ | • The fixed-shape model has no dynamic property-bag or runtime protocol-dispatch point (TDD-00044) |
| `eval` / `Function(string)` / dynamic code construction | ❌ | • No runtime compiler; gated on an opt-in embedded JS engine |
| First-class generic function values / a generic value at a call site | ❌ | • No single monomorphized symbol to reference |
| .d.ts declaration-file *emission* | ❌ | • This is a compiler-to-native, not a type-checker emitting `.d.ts`; ambient `.d.ts` *consumption* is the `declare` path above |
