<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/javascript-builtins.json; edit the JSON, then run `make status`. -->

# JavaScript built-in objects (completeness index)

> Part of the [Implementation Status](README.md) index. A **completeness map of JavaScript's standard built-in objects** (the MDN "standard built-in objects" surface + the ubiquitous Web globals) — companion to the [Node modules](NODE-MODULES.md) and [TypeScript features](TYPESCRIPT-FEATURES.md) indexes. It says, at a glance, which built-ins are supported, which work with real limits, which are not started, and which are out of scope for a **whole-program AOT compiler with fixed-shape objects and no runtime JS engine**. This page is **informational**: its rows are *not* counted toward the coverage percentages — the detailed, counted pages (linked per row) carry the per-method detail and the real numbers.

Format: [Status page format](README.md#status-page-format). ✅ = the built-in works (see its detail page for caveats); ❌ = not available today. Note: a ✅ here is a *loose* ✅ — per the project's rule, any caveat excludes a row from Strict Coverage, so several built-ins in the first table also appear with their residual limits in the second.

## Implemented

| Built-in | Status | Caveats | Notes |
|---|---|---|---|
| `Object` (literals, `keys`/`values`/`entries`/`assign`/`freeze`/`seal`/`fromEntries`/`hasOwn`/`groupBy`/`create`/`defineProperty`/`getOwnPropertyDescriptor`/`get`·`setPrototypeOf`) | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Function` — `.call`/`.apply`/`.bind`, overloads, arrows, closures (first-class values) | ✅ | | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| `Function` instances — `name`, `length`, util.inspect form (`[Function: f]`, `[AsyncFunction: f]`, `bound f`, …) | ✅ | • `toString()` / `String(f)` has no source text (a compile error on a statically-typed function)<br>• Own properties on a user function (`f.x = 1`) are unsupported<br>• Boxing the same closure into `any` twice yields two values that are not `===`<br>• A class value has no `[class X]` rendering | • Names follow ECMAScript NamedEvaluation (binding, property key, assignment target, default, class field); `length` counts the parameters before the first default/rest one; the same holds through `any` and for built-in Error constructors ([ADR-01093](../adr/ADR-01093.md)) |
| `Boolean` / `Boolean(x)` | ✅ | | • → [Global functions](GLOBAL-FUNCTIONS.md) |
| `Number` (all statics/constants, `toFixed`/`toString`/`toPrecision`/`toExponential`) | ✅ | | • `toExponential()` with no argument uses the shortest round-trip mantissa (`(12345).toExponential()` → `1.2345e+4`), as in Node ([ADR-00832](../adr/ADR-00832.md))<br>• → [Number & Math](NUMBER-MATH.md) |
| `Math` (full surface incl. `cbrt`/`clz32`/`fround`/`imul`/`hypot`/`expm1`/`log1p`) | ✅ | | • `Math.max`/`Math.min` accept any arity: zero args give the JS identity (`-Infinity`/`+Infinity`), one arg returns that value, as in Node ([ADR-00831](../adr/ADR-00831.md))<br>• → [Number & Math](NUMBER-MATH.md) |
| `BigInt` + `asIntN`/`asUintN`, arithmetic, literals | ✅ | | • → [Type system](TYPE-SYSTEM.md) |
| `String` (~30 methods incl. `raw`, `fromCharCode`/`fromCodePoint`) | ✅ | | • → [String methods](STRING-METHODS.md) |
| `Array` (~40 methods incl. ES2023 `toSorted`/`toReversed`/`with`/`findLast`, `from`/`of`/`isArray`) | ✅ | | • → [Array methods](ARRAY-METHODS.md) |
| `Map` / `Set` (`set`/`get`/`has`/`delete`/iteration/`size`, `new Map(entries)`) | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `WeakMap` / `WeakSet` / `WeakRef` (real weak semantics under `-mm=gc`) | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `JSON` (`stringify`/`parse`, dynamic + typed trees, `toJSON`) | ✅ | | • → [JSON](JSON.md) |
| `RegExp` (literal + ctor, `exec`/`test`/`match`/`matchAll`/`replace`/`replaceAll`/`split`/`search`) — PCRE2-backed | ✅ | | • → [RegExp](REGEXP.md) |
| `Error` + subtypes (`TypeError`/`RangeError`/`SyntaxError`/`EvalError`/`URIError`/`ReferenceError`/`AggregateError`/`DOMException`), `class X extends Error` (1 level) | ✅ | • The error-options second argument must be a `{ cause: <expr> }` object **literal** — a variable/computed options bag is a clean rejection ([ADR-01007](../adr/ADR-01007.md)); `AggregateError` takes no options (its `.cause` reads `undefined`)<br>• `.stack` is typed a number, not a string (`typeof err.stack` is `'number'`, Node: `'string'`) | • → [Language constructs](LANGUAGE-CONSTRUCTS.md)<br>• `.toString()` / `String(err)` / `` `${err}` `` render `name: message` as in Node. See [ADR-00846](../adr/ADR-00846.md) |
| `Symbol` (`Symbol()`/`for`/`keyFor`, `.description`, `typeof`) — opaque unique values | ✅ | | • → [Type system](TYPE-SYSTEM.md)<br>• A Symbol keeps its identity inside an `any` (hidden type-id word): `typeof` → `"symbol"` and narrows, `===`, `.description`, `Symbol(desc)` rendering, and ToNumber (`Number(x)`, a `TypedArray.set` offset, `+x`/`x * 1` under `-compat=js`) throws the spec's `TypeError` ([ADR-01059](../adr/ADR-01059.md)) |
| `Promise` (`all`/`race`/`allSettled`/`any`/`resolve`/`reject`, executor, `then`/`catch`/`finally`) | ✅ | • `JSON.stringify` of an **error-subclass instance** reason yields `{}` where Node serializes its own enumerable fields (an assigned `this.name`, extra declared fields) — needs per-field enumerability ([TDD-00222](../tdd/TDD-00222.md)). Everything else about a subclass reason is faithful — `.message`/`.name`, `String` (`Name: message`), precise `instanceof`; primitive and built-in-Error reasons fully so ([ADR-01003](../adr/ADR-01003.md)) | • → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
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
| `performance` (`now`/`mark`/`measure`) + `Date` (+`now`/`parse`, setters, arithmetic) | ✅ | • `Date` is a plain i64 epoch with no `NaN` — an invalid date is a `-1` sentinel (`new Date('bad').getTime()` is `-1`, Node: `NaN`)<br>• Out-of-range date fields wrap rather than invalidating (`Date.parse('2020-13-45')` is a valid timestamp, Node: `NaN`)<br>• No `Date.UTC` static, `getTimezoneOffset`, or `Date.prototype.toString` — each is a compile error | • → [Performance timing](PERFORMANCE-TIMING.md)<br>• The `getUTC*`/`setUTC*` instance accessors work. See [ADR-00844](../adr/ADR-00844.md) |
| `Reflect` (`get`/`set`/`has`/`deleteProperty`/`ownKeys`/`get`·`setPrototypeOf`/`isExtensible`/`preventExtensions`/`defineProperty`) — dynamic target | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Proxy` (`get`/`set`/`has`/`deleteProperty` traps) — dynamic target | ✅ | | • → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `FinalizationRegistry` | ✅ | • `cleanupSome` (non-standard) rejected; aggregate held types (arrays, nullable scalars) rejected<br>• Same-thread V1: a worker's registrations are flushed only by its own thread, not the process exit hook<br>• Under `-mm=gc`, firing depends on the target actually being collected (conservative scanning can pin a stack-reachable pointer — same posture as the `WeakRef` tests) | • Mode-dependent timing, one API ([TDD-00163](../tdd/TDD-00163.md)/[ADR-00701](../adr/ADR-00701.md)): `-mm=manual` fires deterministically at `Memory.free(target)` plus an exit flush for survivors; `-mm=gc` fires via a real Boehm finalizer (observable after `gc()`); `-mm=auto`'s compiler-inserted frees are the same death signal — a registered target's cleanup fires deterministically at its scope exit ([ADR-00703](../adr/ADR-00703.md)), and the same exemption serves explicit `@free`/`@owned` in manual mode. Callbacks run on the microtask drain points, never synchronously at the death site<br>• `-finalizers=report` prints one labeled leak line per registration never freed at exit (held value + `register()` call site) — the manual-memory leak detector<br>• `register`/`unregister` with tokens; `-compat=js` dynamic-object targets unbox like WeakMap keys; `held === target` is a compile error when statically obvious |

## Partial — works, with real caveats

Each compiles and runs for its core case but carries a limitation, mostly driven by the fixed-shape runtime, byte-sequence strings, or the absence of a locale/ICU layer. The linked detail page has the specifics.

| Built-in | Status | Caveats |
|---|---|---|
| `String` | ✅ | • `.normalize()` **missing** (no Unicode tables); `.at()` OOB returns `""` not `undefined`; `.codePointAt()` == `.charCodeAt()` (byte strings, correct only ASCII/Latin-1); `.matchAll()` eager not lazy; `.localeCompare()` is byte-order → [String methods](STRING-METHODS.md) |
| `Array` | ✅ | • length-mutating methods propagate to caller only for plain **variable** params (not object-field/array-element receivers); `a.length = n` truncation compile-errors; `.keys`/`values`/`entries` materialized not lazy; `.flat(depth)` needs a constant depth → [Array methods](ARRAY-METHODS.md) |
| `Object` / dynamic model | ✅ | • prototype machinery, descriptors, accessors exist on **`any`-typed / js-mode dynamic objects only**; static structs are fixed-shape (no dynamic add/delete, no prototype); `Object.assign` can't graft new fields; `hasOwn` needs string-literal keys → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Reflect` | ✅ | • missing `construct`; the object methods require a dynamic target → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Proxy` | ✅ | • only `get`/`set`/`has`/`deleteProperty` traps; dynamic target only → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| `Number` | ✅ | • `toString(radix)` non-power-of-two fractional trailing-digit divergence; `toPrecision` fixed/exp threshold differs → [Number & Math](NUMBER-MATH.md) |
| `Function` `.call`/`.apply`/`.bind` | ✅ | • `thisArg` binds only a `this: T` parameter (no method-borrowing of a class method); `.bind` scalar-param only; first-class function values, not built-ins → [Language constructs](LANGUAGE-CONSTRUCTS.md) |
| `Symbol` | ✅ | • no well-known symbols as runtime values; only `[Symbol.iterator]`/`[Symbol.asyncIterator]` recognized syntactically → [Type system](TYPE-SYSTEM.md) |
| `RegExp` | ✅ | • `u`/`d` flags **missing** (accepted, not implemented); `exec` result lacks `index`/`input`/`groups`; unmatched groups become `""` not `null` → [RegExp](REGEXP.md) |
| `JSON` | ✅ | • statically-typed heterogeneous-array `stringify` **missing** (use a tuple/`any`); function/array `replacer` rejected; `space` must be literal → [JSON](JSON.md) |
| TypedArrays / `ArrayBuffer` | ✅ | • no `.buffer` back-ref; `resize`/`grow` need `{maxByteLength}`; views don't length-track resize → [Binary data & typed arrays](BINARY-DATA-TYPED-ARRAYS.md) |
| `TextDecoder` | ✅ | • UTF-8 only; non-UTF-8 labels throw `RangeError` at construction → [Encoding & text](ENCODING-TEXT.md) |
| `URLSearchParams` / `URLPattern` | ✅ | • `URLSearchParams` keeps one value per key; `URLPattern` is object-init only with a reduced grammar and a merged-`Map` `.exec()` result → [URL](URL.md) |
| `EventTarget` / `Event` / `AbortSignal` | ✅ | • single-target dispatch (no capture/bubble/propagation); reduced Event property set; `AbortSignal` isn't wired into `setTimeout` → [Events & cancellation](EVENTS-CANCELLATION.md) |
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
| `RegExp` `u`/`v`/`d` flag semantics | ❌ | • Accepted but not implemented → [RegExp](REGEXP.md) |
| `Iterator` / `AsyncIterator` helpers (`Iterator.prototype.map`/`filter`/`take`/`drop`/…) | ❌ | • No general lazy-iterator protocol exists yet (materialized iteration across Array/Map/Set/`matchAll`) |
| `Reflect.construct` | ❌ | • Explicitly missing → [Object, Map & Set](OBJECT-COLLECTIONS.md) |
| Dynamic `eval` (arbitrary strings) | ❌ | • The opt-in embedded JS engine is not started (only the static-subset eval works) → [Global functions](GLOBAL-FUNCTIONS.md) |

## Out of scope (by the whole-program AOT / no-runtime / fixed-shape model)

Built-ins that depend on a live JS engine, a locale/ICU layer, or a dynamic property/protocol model native ahead-of-time output has no equivalent for. Listed for completeness, not planned.

| Built-in | Status | Notes |
|---|---|---|
| `Intl.*` (Collator/NumberFormat/DateTimeFormat/…) | ❌ | • No locale/ICU infrastructure — a bare `Intl` reference is an undefined-variable compile error; the reason `localeCompare`/`bigint.toLocaleString`/`Date.toLocale*` are gaps. `Number.prototype.toLocaleString()` implements the **no-argument** en-US default (thousands grouping + max 3 fraction digits, [ADR-00847](../adr/ADR-00847.md)); a locale/options argument is a clean rejection |
| `Temporal` (proposal) | ❌ | • Not a shipping standard; absent |
| Dynamic `eval` / dynamic `import()` of arbitrary code | ❌ | • No runtime JS engine ([ADR-00022](../adr/ADR-00022.md)); only the static-subset eval works |
| Full `Proxy` trap set (`apply`/`construct`/`ownKeys`/`getOwnPropertyDescriptor`/`defineProperty`/…) | ❌ | • Structurally limited to the 4 dynamic-object traps; the rest have no interception point in the fixed-shape model |
| Prototype machinery on statically-typed objects | ❌ | • Objects are fixed-shape heap structs; dynamic add/delete + prototype links live only in the `any`/js-mode dynamic path by design |
| Well-known symbols as runtime protocol dispatch (`Symbol.hasInstance`/`toPrimitive`/`toStringTag`/`species`/…) | ❌ | • No runtime protocol-dispatch point; only `Symbol.iterator`/`asyncIterator` are honored, syntactically |
