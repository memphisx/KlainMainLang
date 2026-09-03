<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/javascript-builtins.json; edit the JSON, then run `make status`. -->

# JavaScript built-in objects (completeness index)

> Part of the [Implementation Status](README.md) index. A **completeness map of JavaScript's standard built-in objects** (the MDN "standard built-in objects" surface + the ubiquitous Web globals) — companion to the [Node modules](NODE-MODULES.md) and [TypeScript features](TYPESCRIPT-FEATURES.md) indexes. It says, at a glance, which built-ins are supported, which work with real limits, which are not started, and which are out of scope for a **whole-program AOT compiler with fixed-shape objects and no runtime JS engine**. This page is **informational**: its rows are *not* counted toward the coverage percentages — the detailed, counted pages (linked per row) carry the per-method detail and the real numbers.

Format: [Status page format](README.md#status-page-format). ✅ = the built-in works (see its detail page for caveats); ❌ = not available today. Note: a ✅ here is a *loose* ✅ — per the project's rule, any caveat excludes a row from Strict Coverage, so several built-ins in the first table also appear with their residual limits in the second.

## Implemented

| Built-in | Status | Caveats | Notes |
|---|---|---|---|
| `Object` (literals, `keys`/`values`/`entries`/`assign`/`freeze`/`seal`/`fromEntries`/`hasOwn`/`groupBy`/`create`/`defineProperty`/`getOwnPropertyDescriptor`/`get`·`setPrototypeOf`) | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Function` — `.call`/`.apply`/`.bind`, overloads, arrows, closures (first-class values) | ✅ | | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| `Boolean` / `Boolean(x)` | ✅ | | • → [Global functions](GLOBAL-FUNCTIONS.md) |
| `Number` (all statics/constants, `toFixed`/`toString`/`toPrecision`/`toExponential`) | ✅ | • `toExponential()` requires an explicit fraction-digits argument — the zero-argument auto-precision form (`(1.5).toExponential()`, valid in JS) is a compile error. | • → [Number & Math](NUMBER-MATH.md) |
| `Math` (full surface incl. `cbrt`/`clz32`/`fround`/`imul`/`hypot`/`expm1`/`log1p`) | ✅ | • `Math.max`/`Math.min` require at least two arguments — the zero-arg (JS: `∓Infinity`) and one-arg forms are compile errors. | • → [Number & Math](NUMBER-MATH.md) |
| `BigInt` + `asIntN`/`asUintN`, arithmetic, literals | ✅ | | • → [Type system](TYPE-SYSTEM.md) |
| `String` (~30 methods incl. `raw`, `fromCharCode`/`fromCodePoint`) | ✅ | | • → [String methods](STRING-METHODS.md) |
| `Array` (~40 methods incl. ES2023 `toSorted`/`toReversed`/`with`/`findLast`, `from`/`of`/`isArray`) | ✅ | | • → [Array methods](ARRAY-METHODS.md) |
| `Map` / `Set` (`set`/`get`/`has`/`delete`/iteration/`size`, `new Map(entries)`) | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `WeakMap` / `WeakSet` / `WeakRef` (real weak semantics under `-mm=gc`) | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `JSON` (`stringify`/`parse`, dynamic + typed trees, `toJSON`) | ✅ | | • → [JSON](JSON.md) |
| `RegExp` (literal + ctor, `exec`/`test`/`match`/`matchAll`/`replace`/`replaceAll`/`split`/`search`) — PCRE2-backed | ✅ | | • → [RegExp](REGEXP.md) |
| `Error` + subtypes (`TypeError`/`RangeError`/`SyntaxError`/`EvalError`/`URIError`/`ReferenceError`/`AggregateError`/`DOMException`), `class X extends Error` (1 level) | ✅ | • No error-options second argument — `new Error(m, { cause })` fails to parse, so `.cause` is unavailable; `.stack` is typed a number, not a string (`typeof err.stack` is `'number'`, Node: `'string'`); and an Error has no `.toString()` / can't be interpolated (`` `${err}` `` is a compile error). | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| `Symbol` (`Symbol()`/`for`/`keyFor`, `.description`, `typeof`) — opaque unique values | ✅ | | • → [Type system](TYPE-SYSTEM.md) |
| `Promise` (`all`/`race`/`allSettled`/`any`/`resolve`/`reject`, executor, `then`/`catch`/`finally`) | ✅ | • `Promise.allSettled` reports a rejected element's `.reason` as a wrapped `Error` (stringified message), not the original rejected value — `(await Promise.allSettled([Promise.reject(42)]))[0].reason.message` is `'42'`, where Node gives `.reason === 42`; the always-present `value`/`reason` fields also aren't omitted (a faithful reject-value model is [TDD-00169](../tdd/TDD-00169.md)). | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| async functions / `await` / async generators / `for await…of` | ✅ | | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| Generators (`function*`, `yield`/`yield*`, `.next(value)`) | ✅ | | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| `globalThis` | ✅ | • `globalThis` exists only inside `typeof globalThis` — used as a value (property access, assignment, identity), it fails compilation with `undefined variable 'globalThis'`. | • → [Global functions](GLOBAL-FUNCTIONS.md) |
| Global functions (`isNaN`/`isFinite`/`parseInt`/`parseFloat`/`encodeURI(Component)`/`decodeURI(Component)`/`atob`/`btoa`) | ✅ | | • → [Global functions](GLOBAL-FUNCTIONS.md) |
| `structuredClone` | ✅ | | • → [Global functions](GLOBAL-FUNCTIONS.md) |
| `queueMicrotask` | ✅ | | • → [Timers](TIMERS.md) |
| `ArrayBuffer`/`SharedArrayBuffer`/`DataView`/`Atomics` + all 11 TypedArrays | ✅ | | • → [Binary data & typed arrays](BINARY-DATA-TYPED-ARRAYS.md) |
| `TextEncoder` / `TextDecoder` (UTF-8) | ✅ | | • → [Encoding & text](ENCODING-TEXT.md) |
| `URL` / `URLSearchParams` / `URLPattern` | ✅ | | • → [URL](URL.md) |
| `crypto` (`getRandomValues`/`randomUUID`, `crypto.subtle.*`) | ✅ | | • → [Web Crypto](WEB-CRYPTO.md) |
| `Event`/`CustomEvent`/`EventTarget`/`AbortController`/`AbortSignal`/`DOMException` | ✅ | | • → [Events & cancellation](EVENTS-CANCELLATION.md) |
| `setTimeout`/`setInterval`/`setImmediate` (+`clear*`) | ✅ | | • → [Timers](TIMERS.md) |
| `performance` (`now`/`mark`/`measure`) + `Date` (+`now`/`parse`, setters, arithmetic) | ✅ | • `Date` is a plain i64 epoch with no NaN — an invalid date is a `-1` sentinel (`new Date('bad').getTime()` is `-1`, Node: `NaN`) and out-of-range fields wrap rather than invalidating (`Date.parse('2020-13-45')` is a valid timestamp, Node: `NaN`); no UTC accessors (`Date.UTC`/`getUTC*`/`getTimezoneOffset`) or `Date.prototype.toString` — each is a compile error. | • → [Performance timing](PERFORMANCE-TIMING.md) |
| `Reflect` (`get`/`set`/`has`/`deleteProperty`/`ownKeys`/`get`·`setPrototypeOf`/`isExtensible`/`preventExtensions`/`defineProperty`) — dynamic target | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Proxy` (`get`/`set`/`has`/`deleteProperty` traps) — dynamic target | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |

## Partial — works, with real caveats

Each compiles and runs for its core case but carries a limitation, mostly driven by the fixed-shape runtime, byte-sequence strings, or the absence of a locale/ICU layer. The linked detail page has the specifics.

| Built-in | Status | Caveats |
|---|---|---|
| `String` | ✅ | • `.normalize()` **missing** (no Unicode tables); `.at()` OOB returns `""` not `undefined`; `.codePointAt()` == `.charCodeAt()` (byte strings, correct only ASCII/Latin-1); `.matchAll()` eager not lazy; `.localeCompare()` is byte-order → [String methods](STRING-METHODS.md) |
| `Array` | ✅ | • length-mutating methods propagate to caller only for plain **variable** params (not object-field/array-element receivers); `a.length = n` truncation compile-errors; **`.reduceRight()` absent**; `.keys`/`values`/`entries` materialized not lazy; `.flat(depth)` needs a constant depth → [Array methods](ARRAY-METHODS.md) |
| `Object` / dynamic model | ✅ | • prototype machinery, descriptors, accessors exist on **`any`-typed / js-mode dynamic objects only**; static structs are fixed-shape (no dynamic add/delete, no prototype); `Object.assign` can't graft new fields; `hasOwn` needs string-literal keys → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Reflect` | ✅ | • missing `apply`/`construct`; requires a dynamic target → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Proxy` | ✅ | • only `get`/`set`/`has`/`deleteProperty` traps; dynamic target only → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Number` | ✅ | • `toString(radix)` non-power-of-two fractional trailing-digit divergence; `toPrecision` fixed/exp threshold differs → [Number & Math](NUMBER-MATH.md) |
| `Function` `.call`/`.apply`/`.bind` | ✅ | • forward args but **ignore `thisArg`** (no method-borrowing); `.bind` scalar-param only; first-class function values, not built-ins → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| `Symbol` | ✅ | • no well-known symbols as runtime values; only `[Symbol.iterator]`/`[Symbol.asyncIterator]` recognized syntactically → [Type system](TYPE-SYSTEM.md) |
| `RegExp` | ✅ | • `u`/`y`/`d` flags **missing** (accepted, not implemented); `exec` result lacks `index`/`input`/`groups`; unmatched groups become `""` not `null` → [RegExp](REGEXP.md) |
| `JSON` | ✅ | • statically-typed heterogeneous-array `stringify` **missing** (use a tuple/`any`); function/array `replacer` rejected; `space` must be literal → [JSON](JSON.md) |
| TypedArrays / `ArrayBuffer` | ✅ | • no `.buffer` back-ref; `resize`/`grow` need `{maxByteLength}`; views don't length-track resize → [Binary data & typed arrays](BINARY-DATA-TYPED-ARRAYS.md) |
| `TextDecoder` | ✅ | • UTF-8 only; non-UTF-8 labels throw `RangeError` at construction → [Encoding & text](ENCODING-TEXT.md) |
| `URLSearchParams` / `URLPattern` | ✅ | • `URLSearchParams` keeps one value per key; `URLPattern` is object-init only with a reduced grammar and a merged-`Map` `.exec()` result → [URL](URL.md) |
| `EventTarget` / `Event` / `AbortSignal` | ✅ | • single-target dispatch (no capture/bubble/propagation); reduced Event property set; `AbortSignal` custom `abort(reason)` surfaces as `AbortError`; wired into `fetch` not `setTimeout` → [Events & cancellation](EVENTS-CANCELLATION.md) |
| `crypto.subtle` | ✅ | • literal-only algorithm dispatch; ops throw synchronously rather than rejecting; jwk as `Map<string,string>`; `CryptoKey.algorithm`/`.usages` unimplemented → [Web Crypto](WEB-CRYPTO.md) |
| `Date` | ✅ | • UTC-only everywhere (deliberate); `parse` returns a `-1` sentinel not `NaN`; setters need a named-variable receiver; no locale/Intl formatting → [Performance timing](PERFORMANCE-TIMING.md) |
| `performance` | ✅ | • `mark`/`measure` last-write-wins; `measure` returns a plain number; no `PerformanceObserver`/entries → [Performance timing](PERFORMANCE-TIMING.md) |
| `eval` | ✅ | • general/dynamic eval **missing**; only a compile-time-constant `eval("<expression>")` static subset works → [Global functions](GLOBAL-FUNCTIONS.md) |
| `globalThis` | ✅ | • member access to known globals only; no bare-value use, computed access, or new-global assignment → [Global functions](GLOBAL-FUNCTIONS.md)<br>• `globalThis` exists only inside `typeof globalThis` — used as a value (property access, assignment, identity), it fails compilation with `undefined variable 'globalThis'`. |

## Not started (in scope)

Standard built-ins that fit the model and would add value, not yet built.

| Built-in | Status | Notes |
|---|---|---|
| `String.prototype.normalize()` | ❌ | • Deliberately deferred (needs NFC/NFD/NFKC/NFKD tables) → [String methods](STRING-METHODS.md) |
| `Array.prototype.reduceRight()` | ❌ | • Unimplemented → [Array methods](ARRAY-METHODS.md) |
| `RegExp` `u`/`v`/`y`/`d` flag semantics | ❌ | • Accepted but not implemented → [RegExp](REGEXP.md) |
| `FinalizationRegistry` | ❌ | • Not started — `WeakRef` ships; the registry is designed in [TDD-00163](../tdd/TDD-00163.md) |
| `Iterator` / `AsyncIterator` helpers (`Iterator.prototype.map`/`filter`/`take`/`drop`/…) | ❌ | • No general lazy-iterator protocol exists yet (materialized iteration across Array/Map/Set/`matchAll`) |
| `Reflect.apply` / `Reflect.construct` | ❌ | • Explicitly missing → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| Dynamic `eval` (arbitrary strings) | ❌ | • The opt-in embedded JS engine is not started (only the static-subset eval works) → [Global functions](GLOBAL-FUNCTIONS.md) |

## Out of scope (by the whole-program AOT / no-runtime / fixed-shape model)

Built-ins that depend on a live JS engine, a locale/ICU layer, or a dynamic property/protocol model native ahead-of-time output has no equivalent for. Listed for completeness, not planned.

| Built-in | Status | Notes |
|---|---|---|
| `Intl.*` (Collator/NumberFormat/DateTimeFormat/…) | ❌ | • No locale/ICU infrastructure — a bare `Intl` reference is an undefined-variable compile error; the reason `localeCompare`/`bigint.toLocaleString`/`Date.toLocale*` are gaps |
| `Temporal` (proposal) | ❌ | • Not a shipping standard; absent |
| Dynamic `eval` / dynamic `import()` of arbitrary code | ❌ | • No runtime JS engine ([ADR-00022](../adr/ADR-00022.md)); only the static-subset eval works |
| Full `Proxy` trap set (`apply`/`construct`/`ownKeys`/`getOwnPropertyDescriptor`/`defineProperty`/…) | ❌ | • Structurally limited to the 4 dynamic-object traps; the rest have no interception point in the fixed-shape model |
| Prototype machinery on statically-typed objects | ❌ | • Objects are fixed-shape heap structs; dynamic add/delete + prototype links live only in the `any`/js-mode dynamic path by design |
| Well-known symbols as runtime protocol dispatch (`Symbol.hasInstance`/`toPrimitive`/`toStringTag`/`species`/…) | ❌ | • No runtime protocol-dispatch point; only `Symbol.iterator`/`asyncIterator` are honored, syntactically |
