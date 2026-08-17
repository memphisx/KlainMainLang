# KlainMainLang — Implementation Status

> TypeScript → native compiler written in Go. Emits LLVM IR text, compiled with `clang -O2`.
> Targets whatever architecture the host clang defaults to (arm64 on Apple Silicon, x86-64 on Linux, etc.).
> Multi-file compilation exists (named `import`/`export` only, V1 scope — see [MODULES.md](MODULES.md)); the entry file's top-level statements still all run in one `main()`, and imported files may only contain declarations.
> No garbage collector by default — every heap allocation is `malloc`'d and (almost) never `free`d in `manual` mode. `-mm=gc` opts into a real one (Boehm). See [MEMORY-MANAGEMENT.md](MEMORY-MANAGEMENT.md).
> Programs are pure libc by default; a program only needs `libcurl` installed on the build machine if it actually calls `fetch` (compiled binaries automatically link `-lcurl` only when used — see [ADR-00020](../adr/ADR-00020.md)).

This file is the scannable index: per-area completion % plus the caveats/blockers that matter most. Each linked page carries the full feature-by-feature table (and, where relevant, its own Known Limitations) for that area — trust the linked page over this summary if they ever drift apart.

## Contents

- [TypeScript Core Language](#typescript-core-language) — core JavaScript/TypeScript language & standard library (works the same in any JS host)
- [Web Platform APIs](#web-platform-apis) — WHATWG/browser-standard APIs (also implemented by Node.js, but not part of the JS *language* itself)
- [Node.js APIs](#nodejs-apis) — `fs`, `process`, and a real `http.listen` server — Node-specific runtime globals with no browser equivalent
- [Cross-Cutting](#cross-cutting) — concerns spanning every feature area (memory management)
- [What Is NOT Implemented](#what-is-not-implemented) — core language gaps, by priority/complexity
- [Fidelity Gaps in Shipped Features](#fidelity-gaps-in-shipped-features) — features marked ✅/100% that still have real, non-cosmetic differences from actual JS/TS behavior
- [Design Documents (TDDs)](#design-documents-tdds)
- [Roadmap](#roadmap)

---

## TypeScript Core Language

**298 / 333 features, ~90% coverage.**

**Strict Coverage note**: the "Strict" column below is each linked page's own overall Strict Coverage stat (a row counts only with zero known caveats/bugs, repro-verified — see the 2026-08-11 audit, [ADR-00166](../adr/ADR-00166.md)), not a per-category split. Its denominator is always the page's **total** feature count — the same base as the Coverage column — so Strict is directly comparable to Coverage and never exceeds it (normalized to this one convention by [ADR-00205](../adr/ADR-00205.md); some pages previously divided by their implemented-only count instead). A `(page)` tag marks a figure shared across several categories that live on one page: every `LANGUAGE-CONSTRUCTS.md` row shows that page's single 35/65, and both `TYPE-SYSTEM.md` rows show its 13/20 — treat those as a page-level signal, not an exact per-row count.

| Category | Coverage | Strict | Page | Caveats |
|---|---|---|---|---|
| Control flow statements | 11/11, 100% | 35/65, ~54% (page) | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | A `for` loop's non-declaration init clause (`for (i = 0, j = 10; ...)`) is a clean rejection (the update clause does take comma-separated expressions — [ADR-00156](../adr/ADR-00156.md)); no ASI restriction on a line break before a postfix `++`/`--`. `finally` does not run on an early `return`/`break`/`continue` inside an `async`/generator body (it does everywhere else — [ADR-00191](../adr/ADR-00191.md)) |
| Operators | 43/43, 100% | 35/65, ~54% (page) | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | `**` uses exact i64 integer semantics — a negative exponent gives 0; use a float operand for fractional powers ([ADR-00187](../adr/ADR-00187.md)). `&&`/`\|\|` yield a bool, not real JS's value-preserving operand form ([ADR-00186](../adr/ADR-00186.md)) |
| Variable declarations | 4/4, 100% | 35/65, ~54% (page) | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | `const` doesn't reject a missing initializer; `arguments`/`eval` aren't reserved as binding names |
| Functions & closures | 13/13, 100% | 35/65, ~54% (page) | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | Nested function declarations are V1-scoped (no closure capture of the enclosing body's locals, one block deeper than the enclosing body is unsupported) — see [TDD-00057](../tdd/TDD-00057.md); tagged template literals have no `.raw` property on the `strings` array (real `String.raw` stays separately out of scope, see [ADR-00028](../adr/ADR-00028.md)) — see [TDD-00059](../tdd/TDD-00059.md)/[ADR-00152](../adr/ADR-00152.md). A top-level function can't reference a sibling top-level `let`/`var`/`const` at all; a default parameter can't reference an earlier parameter; a rest-only arrow parameter list fails to parse — all found by the 2026-08-11 audit, see [ADR-00166](../adr/ADR-00166.md). Function expressions are V1-scoped to anonymous only (a named function expression is a clean parse-time rejection, pending new name-binding scope machinery) — see [TDD-00060](../tdd/TDD-00060.md)/[ADR-00168](../adr/ADR-00168.md). Generator functions are V1-scoped to a top-level declaration only, with no `yield*`/generator methods/async generators yet — see [TDD-00061](../tdd/TDD-00061.md)/[ADR-00173](../adr/ADR-00173.md) |
| Type primitives | 9/14, ~64% | 13/20, 65% (page) | [TYPE-SYSTEM.md](TYPE-SYSTEM.md) | No `bigint` |
| Async / Promise | 4/9, ~44% | 35/65, ~54% (page) | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | Only `await fetch(...)` is genuinely non-blocking; every other `Promise<T>` is a resolved-slot read |
| String methods | 28/33, ~85% | 15/33, ~45% | [STRING-METHODS.md](STRING-METHODS.md) | No `.normalize()`. `.charCodeAt()`/`.codePointAt()` read garbage memory out-of-range instead of returning `NaN` — found by the 2026-08-11 audit, see [ADR-00166](../adr/ADR-00166.md) |
| RegExp | 14/14, 100% | 2/14, ~14% | [REGEXP.md](REGEXP.md) | `u`/`y`/`d` flags out of scope for V1; several other deliberate scope narrowings (`.test()` ignores `lastIndex`, no implicit string→RegExp coercion, etc.) — see [REGEXP.md](REGEXP.md)'s own Caveats. `new RegExp` doesn't validate its flags string — found by the 2026-08-11 audit |
| Array methods | 40/40, 100% | 25/40, ~63% | [ARRAY-METHODS.md](ARRAY-METHODS.md) | `.flat(depth?)`/`.flatMap(fn)`: `depth` must be a compile-time constant (or `Infinity`), since this compiler's array element types are static ([TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md)); `Array.from` supports the array-like overload only. `.pop()`/`.shift()` corrupt state on an empty array; `.push`/`.pop`/`.shift`/`.unshift`/`.splice` all fail on `this.field.push(x)` — found by the 2026-08-11 audit, see [ADR-00166](../adr/ADR-00166.md) |
| Number / Math | 35/35, 100% | 22/35, ~63% | [NUMBER-MATH.md](NUMBER-MATH.md) | `Math.floor/ceil/round/trunc/min/max/sign` produce undefined-behavior garbage on `NaN`/`±Infinity` input; `parseInt`/`parseFloat` never return `NaN` — found by the 2026-08-11 audit, see [ADR-00166](../adr/ADR-00166.md) |
| Object & collections | 24/27, ~89% | 12/27, ~44% | [OBJECT-COLLECTIONS.md](OBJECT-COLLECTIONS.md) | No `WeakMap`/`WeakSet`/`WeakRef`, `Object.create`/`.fromEntries`. `new Map(entries)` doesn't accept an entries array — the `[K, V]` tuple type it needs exists ([TDD-00066](../tdd/TDD-00066.md)), a separately-shippable follow-on. Object literal method shorthand (`{ foo() {...} }`) has no `this` binding (no nominal type to give it a shape, no dynamic call-site binding) — a clean compile-time rejection, not silently wrong. See [ADR-00169](../adr/ADR-00169.md) |
| JSON | 9/11, ~82% | 7/11, ~64% | [JSON.md](JSON.md) | No nested-object `JSON.parse`; an array-typed interface field is now a clean rejection ([ADR-00189](../adr/ADR-00189.md)), not yet parsed. `JSON.parse` into a `float64`/`float32`-typed variable fails to compile — found by the 2026-08-11 audit |
| console | 11/12, ~92% | 5/12, ~42% | [CONSOLE.md](CONSOLE.md) | No `console.table()`. `console.warn` has an undocumented prefix; `console.trace` prints no stack — found by the 2026-08-11 audit |
| Global functions & constants | 14/17, ~82% | 3/17, ~18% | [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md) | No `queueMicrotask`; `eval` has an opt-in embedded-engine path scoped in [TDD-00046](../tdd/TDD-00046.md), not started. `parseInt`/`parseFloat` never return `NaN`; `NaN`/`Infinity` shadowing needs `-globals=permissive`, not unconditional as previously documented — found by the 2026-08-11 audit |
| Type system features | 18/23, ~78% | 13/20, 65% (page) | [TYPE-SYSTEM.md](TYPE-SYSTEM.md) | Generics are unconstrained type parameters only, with no explicit call-site type arguments ([TDD-00010](../tdd/TDD-00010.md), [TDD-00037](../tdd/TDD-00037.md)); union types (beyond `T \| null`) are scalar-members-only — not nested inside a container, no flow-based narrowing ([TDD-00043](../tdd/TDD-00043.md)); tuple types are fixed-shape structs ([TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md)); no intersection/mapped types |
| Classes / OOP | 15/15, 100% | 35/65, ~54% (page) | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | No user-definable `class X extends Error` (built-in types aren't valid `extends` targets, by design). Clean rejections: static-field initializers, static/async generator methods, a dynamic/`Symbol.*` computed member key, and a class expression used as a runtime value. A field initializer can currently see constructor params (real JS's separate initializer scope forbids this — [ADR-00180](../adr/ADR-00180.md)); `#x` private names have no early-error check for `#m`/`static #m` name collision or the `#constructor`-is-banned rule ([ADR-00155](../adr/ADR-00155.md)); a string enum assigned to a typed variable fails to compile — 2026-08-11 audit, [ADR-00166](../adr/ADR-00166.md) |
| Modules | 14/15, ~93% | 8/15, ~53% | [MODULES.md](MODULES.md) | Whole-program compile only. `klmpm` is Stage 1 only — a `klain_modules/<name>/` package must be hand-built, no package-manager tool yet ([TDD-00054](../tdd/TDD-00054.md)). No namespace re-exports (`export * as ns from`); no npm/`node_modules` interop ([TDD-00053](../tdd/TDD-00053.md)); no dynamic `import()` (parses, cleanly rejected — [TDD-00055](../tdd/TDD-00055.md)/[TDD-00056](../tdd/TDD-00056.md)). Node-style built-ins (`fs`/`path`/…) require a default, namespace, or named import, not ambient ([TDD-00049](../tdd/TDD-00049.md)) |

## Web Platform APIs

WHATWG/W3C-standard APIs — the kind a browser **and** Node.js both implement. Not part of the JS *language* itself, but not Node-specific either. Filtered to those that make sense outside a browser context; pure browser-only APIs (DOM, Canvas, WebGL, CSS, Gamepad, etc.) are out of scope — see [NOTIFICATIONS-MISC.md](NOTIFICATIONS-MISC.md).

**29 / ~65 features, ~45% coverage.**

| Category | Coverage | Strict | Page | Caveats |
|---|---|---|---|---|
| Timers | 3/4, 75% | 1/4, 25% | [TIMERS.md](TIMERS.md) | No `queueMicrotask` |
| Encoding / Text | 2/2, 100% | 1/2, 50% | [ENCODING-TEXT.md](ENCODING-TEXT.md) | UTF-8 only — see [TDD-00034](../tdd/TDD-00034.md) for non-UTF-8 scope |
| URL | 2/3, ~67% | 0/3, 0% | [URL.md](URL.md) | `URLSearchParams` keeps only one value per key (known limitation) |
| Binary data & Typed Arrays | 9/17, ~53% | 1/17, ~6% | [BINARY-DATA-TYPED-ARRAYS.md](BINARY-DATA-TYPED-ARRAYS.md) | No `DataView`/`Blob`/`SharedArrayBuffer`/`Atomics`; no `BigInt64Array`/`BigUint64Array` (needs `bigint`); no Node `Buffer` |
| Web Crypto | 2/8, 25% | 1/8, ~13% | [WEB-CRYPTO.md](WEB-CRYPTO.md) | All of `crypto.subtle.*` unimplemented |
| Performance & Timing (incl. Date) | 9/9, 100% | 1/9, ~11% | [PERFORMANCE-TIMING.md](PERFORMANCE-TIMING.md) | `Date` is UTC-only, never local time |
| Networking (fetch, WebSocket, SSE) | 6/6, 100% | 0/6, 0% | [NETWORKING.md](NETWORKING.md) | `WebSocket` (both server and client, [TDD-00039](../tdd/TDD-00039.md)) has no binary send and no `wss://`/TLS; `XMLHttpRequest` ([TDD-00040](../tdd/TDD-00040.md)) is legacy-synchronous-mode only; `.text()`/`.json()` still truncate at an embedded null byte by design — use `.arrayBuffer()` for binary bodies ([ADR-00094](../adr/ADR-00094.md)). `await`-ing `Response.text()`/`.json()`/`.arrayBuffer()` (they're actually synchronous) hard compile-crashes and frees a live buffer — found by the 2026-08-11 audit, see [ADR-00166](../adr/ADR-00166.md) |
| Streams | 0/8, 0% | 0/8, 0% | [STREAMS.md](STREAMS.md) | Not started — neither the WHATWG API nor Node's own, differently-shaped `stream` module |
| Events & Cancellation | 0/5, 0% | 0/5, 0% | [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md) | Not started; blocks a general `AbortController`. Distinct from Node's `EventEmitter`, see [EVENT-EMITTER.md](EVENT-EMITTER.md) below |
| Workers / Concurrency | 0/3, 0% | 0/3, 0% | [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md) | Not started; needs `pthreads` + `SharedArrayBuffer`/`Atomics` |

## Node.js APIs

Node.js-specific runtime globals — not part of any Web/browser standard, but essential for the CLI-application and microservice use cases this project actually targets. Most (`fs.*`, `path.*`, `os.*`, `querystring.*`, `assert`, `http.listen`/`.close`, `cluster.*`, and this project's own `Memory.free`) require a default, namespace, or named import (`import fs from 'fs'` or `import { readFileSync } from 'fs'`) — see [MODULES.md](MODULES.md)'s import-gated-bindings row and [TDD-00049](../tdd/TDD-00049.md)/[ADR-00141](../adr/ADR-00141.md)/[ADR-00142](../adr/ADR-00142.md). `process`/`console` stay ambient, like `Math`/`JSON`, matching real Node/JS.

**54 / 76 features, ~71% coverage.** A 2026-07-30 audit against the actual lexer/parser/codegen source (not just prior documentation) found a large previously-untracked surface — `path`, `os`, `EventEmitter`, async `child_process`, interactive `readline`, and several smaller core modules had zero rows anywhere before this pass. The drop from this group's earlier ~82% figure reflects newly-discovered scope, not regressed implementation. `process.on('SIGINT'/'SIGTERM', handler)` shipped the same day ([TDD-00019](../tdd/TDD-00019.md)/[ADR-00079](../adr/ADR-00079.md)), closing `http.listen`'s last open gap. `path` shipped shortly after, closing out the audit's top CLI-priority gap — see [ADR-00081](../adr/ADR-00081.md). `EventEmitter` shipped after that — see [TDD-00023](../tdd/TDD-00023.md)/[ADR-00089](../adr/ADR-00089.md) — unblocking (not yet picked up) Node's own `stream` module, async `child_process`, and interactive `readline`. `os` shipped next — see [TDD-00024](../tdd/TDD-00024.md)/[ADR-00090](../adr/ADR-00090.md); its Darwin-specific paths (`freemem()`, `cpus()`'s per-core `times`) are now verified on Apple Silicon (M4 Pro, darwin/arm64). `querystring` and `assert` shipped last, out of the audit's lower-priority "Other core modules" bucket — see [ADR-00139](../adr/ADR-00139.md)/[ADR-00140](../adr/ADR-00140.md).

| Category | Coverage | Strict | Page | Caveats |
|---|---|---|---|---|
| File System (fs) | 11/13, ~85% | 7/13, ~54% | [FILE-SYSTEM.md](FILE-SYSTEM.md) | No async variants; `readFileSync`/`copyFileSync` still text-only by design — use `readFileSyncBytes`/binary-aware `writeFileSync` for binary data ([ADR-00094](../adr/ADR-00094.md)) |
| Process / CLI I/O | 13/23, ~57% | 10/23, ~43% | [PROCESS-CLI.md](PROCESS-CLI.md) | `process.env` is read-only; `process.on(...)` handles only `'SIGINT'`/`'SIGTERM'`, not `'exit'`/`'uncaughtException'`/`'unhandledRejection'`; no async `child_process`; no interactive `readline` |
| HTTP Server | 11/11, 100% | 8/11, ~73% | [HTTP-SERVER.md](HTTP-SERVER.md) | `req.body`/response `body` string fields still truncate at an embedded null byte (`bodyBytes()` accessors are additive, not a fix); `.close()` in a `{ workers: N }` cluster only stops the calling worker process |
| `path` | 8/8, 100% | 5/8, ~63% | [PATH.md](PATH.md) | POSIX-only (this compiler doesn't cross-compile) |
| `os` | 7/7, 100% | 6/7, ~86% | [OS.md](OS.md) | Verified on Linux and Apple Silicon (M4 Pro). Darwin `os.cpus()` reports `speed` 0 on M-series (no fixed clock — matches Node) and has no `irq` tick bucket — see [OS.md](OS.md)'s Known Limitations |
| `events` (`EventEmitter`) | 6/6, 100% | 3/6, 50% | [EVENT-EMITTER.md](EVENT-EMITTER.md) | Single payload type per emitter, not real Node's variadic `...args`; no overriding `on`/`emit`/etc. in a subclass; `instanceof EventEmitter` is a compile error |
| Other core modules (`querystring`, `assert`, `util`, `net`/`dgram`/`tls`/`dns`, `zlib`, `vm`, `cluster`, `http2`) | 2/11, ~18% | 1/11, ~9% | [NODE-CORE-MODULES.md](NODE-CORE-MODULES.md) | `querystring`/`assert` done; the rest not started, grouped together as lower-individual-priority rather than each getting a full page |

## Cross-Cutting

Concerns that span every feature area rather than living in one of them.

| Area | Status | Strict | Page | Caveats |
|---|---|---|---|---|
| Memory management | 2/3 modes (`manual`, `gc`) | 0/3, 0% | [MEMORY-MANAGEMENT.md](MEMORY-MANAGEMENT.md) | `manual` (default) never frees on its own; `auto` (compiler-inserted frees, no runtime collector) is design-only — [TDD-00001](../tdd/TDD-00001.md) |
| Reserved ambient-global names | ✅ 2/2 modes (`strict`, `permissive`) | — | — | `-globals=strict` (default): a declaration colliding with an ambient built-in name (`Math`/`process`/`fetch`/…) is a compile error. `-globals=permissive`: real JS/browser shadowing — but only for plain-identifier globals; constructor-style built-ins (`Map`/`Date`/`RegExp`/…, parser-level `new`-forms) stay reserved either way. See [TDD-00050](../tdd/TDD-00050.md)/[ADR-00143](../adr/ADR-00143.md) |

---

## What Is NOT Implemented

### Medium complexity

| Feature | Notes |
|---|---|
| Intersection types `A & B` | Merge struct fields |
| Tuple types `[number, string]` | Fixed-size struct |

### High complexity

| Feature | Notes |
|---|---|
| Decorators | Requires metadata reflection |
| `Proxy` / `Reflect` | Dynamic property intercept; likely impractical |
| Opt-in dynamic property add/delete on objects | This compiler's objects are fixed-shape heap structs (an interface's field list is fixed at compile time) — real JS lets any object gain/lose properties at runtime, which `Object.freeze`/`.seal` ([ADR-00055](../adr/ADR-00055.md)) currently don't need to enforce since it's already structurally impossible for *any* object, frozen or not. Noted here as a real gap from 100% JS compatibility, not because it's next in line — surfaced while scoping freeze/seal, not researched or designed. If picked up, likely shaped as an explicit compiler flag/opt-in (a genuine dynamic property bag is a different, heavier object representation than the fixed-struct one everything else here assumes) rather than the default. See [OBJECT-COLLECTIONS.md](OBJECT-COLLECTIONS.md). |
| Best-effort vanilla JavaScript compatibility (opt-in flag) | Direct testing found plain untyped JS fails on four independent things: class fields assigned only in the constructor (no upfront declaration), unannotated-parameter type mismatches, prototype-based pre-ES6 "classes," and dynamic property addition — the last two need the same different object representation as the row above and stay out of scope even here. Confirmed a naive "default everything unannotated to `any`" approach would not work: today's `any` runtime rejects arithmetic/most operators, so it wouldn't make ordinary code like `function add(a,b){return a+b}` compile either. See [TDD-00022](../tdd/TDD-00022.md). |
| A generic object-to-string conversion (`` `${obj}` ``, `` "" + obj ``, or bare `console.log(obj)` for a plain class instance) | No fallback exists at all — confirmed directly: `console.log(new Foo())` fails to compile with "cannot convert type ptr to string in template literal" (console.log itself routes through the same stringification path), for any class instance, not just template-literal interpolation specifically. Real JS's default (`[object Object]`, or a custom `.toString()` override) isn't implemented in any form. Found running the Test262 conformance harness ([ADR-00152](../adr/ADR-00152.md)). |

### Newly identified gaps (2026-07-30 audit, not yet prioritized)

Found by checking the actual lexer/parser/codegen source directly rather than relying on prior documentation — confirmed absent, not previously tracked anywhere. Listed here rather than folded into the tiers above since none have been scoped or weighed against the rest of the roadmap yet.

| Feature | Notes |
|---|---|
| Spread in call arguments (`f(...arr)`) | Confirmed absent directly against the parser (`unexpected token ... in expression`) — found while re-examining [ADR-00151](../adr/ADR-00151.md)'s own scope cuts, independent of every fix in [ADR-00152](../adr/ADR-00152.md) (reproduces for a plain top-level function call, nothing array-parameter- or class-specific). Spreading a runtime-length array into a statically-typed rest slot — possibly combined with other positional arguments, multiple spreads, or a non-rest fixed-arity target — is real design space, not a mechanical fix; needs a TDD before implementation. Distinct from the already-tracked `EVENT-EMITTER.md` caveat, which only explains why `EventEmitter` chose a single fixed payload type, not that this is a general language gap. |
| Dynamic `import()` / `import.meta` | See [MODULES.md](MODULES.md) |
| Node `Buffer` | See [BINARY-DATA-TYPED-ARRAYS.md](BINARY-DATA-TYPED-ARRAYS.md) |
| Node's own `stream` module (distinct from WHATWG streams) | See [STREAMS.md](STREAMS.md) |
| Async `child_process`, interactive `readline` | Both built on `EventEmitter` in real Node. See [EVENT-EMITTER.md](EVENT-EMITTER.md) and [PROCESS-CLI.md](PROCESS-CLI.md) |
| `util`, `net`/`dgram`/`tls`/`dns`, `zlib` (as a module), `vm`, `cluster`, `http2` | Lower individual priority — see [NODE-CORE-MODULES.md](NODE-CORE-MODULES.md) |

---

## Fidelity Gaps in Shipped Features

Every row below is marked ✅ (or 100%) on its own page — the feature genuinely works for its core, documented cases — but each hides a real, non-cosmetic difference from how actual JavaScript/TypeScript behaves, beyond a one-line documented scope narrowing. Surfaced by an audit of every page's own caveats/Known Limitations text rather than previously called out as follow-up work anywhere; not reflected in the coverage percentages above. None of these are scheduled — pick up opportunistically alongside the bugs table above or the backlog below.

| Feature | Gap | Where documented |
|---|---|---|
| `EventEmitter` (100%) | `.emit(event, data)` takes exactly one payload type per instance, not real Node's variadic `...args`; no way to override `on`/`emit`/`off`/etc. in a subclass (hand-written dispatch, not real methods); `instanceof EventEmitter` is a compile error (never a registered class) | [EVENT-EMITTER.md](EVENT-EMITTER.md)'s Known Limitations |
| HTTP Server (100%) | `req.body`/response `body` string fields still truncate at an embedded null byte (the binary-safe `bodyBytes()` accessors are additive fields, not a fix to the string ones); `.close()` in a `{ workers: N }` cluster only stops the calling worker process — no IPC exists to reach the rest of the cluster | [HTTP-SERVER.md](HTTP-SERVER.md)'s Known Limitations |
| Array methods (100%) | `.sort()`'s custom comparator (a separate C-ABI `qsort()` trampoline, not a direct closure call), `.indexOf()`/`.includes()`/`.join()` (compare/stringify a bare register directly, no callback at all), and `Object.groupBy()` (buckets store every element as a raw `i64`, a different scheme than a plain array's backing buffer) still reject a nested-array element — every *other* callback-invoking method (`.map`/`.filter`/`.forEach`/`.reduce`/`.find`/`.findIndex`/`.findLast`/`.findLastIndex`/`.some`/`.every`) supports one, see [ADR-00152](../adr/ADR-00152.md) | [ARRAY-METHODS.md](ARRAY-METHODS.md)'s own caveats paragraph |
| RegExp (100%) | `.test()` never respects `.lastIndex` even under the `g` flag (real JS shares `.exec()`'s stateful iteration); `.exec()`/`.match()` turn an unmatched optional capture group into `""` instead of a per-element `null`, and lack `index`/`input`/`groups`; `.matchAll()` is an eager `string[][]`, not a lazy iterator; no implicit string→RegExp coercion anywhere; `.replace()`/`.replaceAll()` template support is `$1`-`$9`/`$&`/`$$` only (no `` $` ``/`$'`) with a fixed-arity `(match, offset, string)` callback (no variadic captured groups); `.split()` doesn't replicate real JS's zero-length-match splitting or splice captured groups into the result | [REGEXP.md](REGEXP.md)'s own caveats paragraph |
| `crypto.getRandomValues` (✅) | Fills a plain `number[]`, not a real `Uint8Array` — predates `ArrayBuffer`/TypedArrays support ([ADR-00078](../adr/ADR-00078.md)) | [WEB-CRYPTO.md](WEB-CRYPTO.md) |
| `fs.copyFileSync` (✅) | Still composes the text-only `readFileSync`/`writeFileSync` pair, so a source file with an embedded null byte copies back shorter than its real size — never migrated to the binary-safe `readFileSyncBytes`/`writeFileSync(path, ArrayBuffer)` pair [ADR-00094](../adr/ADR-00094.md) added | [FILE-SYSTEM.md](FILE-SYSTEM.md) |
| `.codePointAt()` / `.localeCompare()` (✅) | Byte-sequence stand-ins, not real Unicode/locale behavior — `.codePointAt()` is `.charCodeAt()` under another name (no surrogate-pair decoding), `.localeCompare()` is a plain `strcmp`. Correct only for ASCII/Latin-1 text | [STRING-METHODS.md](STRING-METHODS.md) |
| `setImmediate` (✅) | Indistinguishable from a same-tick `setTimeout(fn, 0)` — real Node guarantees `setImmediate` fires first when scheduled from an I/O callback (distinct check/timers event-loop phases); this compiler's timer queue is a single flat fire-time-ordered list with no phase concept | [TIMERS.md](TIMERS.md) |
| `XMLHttpRequest` / `WebSocket` (100% as part of Networking) | `XMLHttpRequest` only implements the spec's legacy synchronous mode (no default-async, callback-interleaved mode); `WebSocket` has no binary `.send()` and no `wss://`/TLS on either side | [NETWORKING.md](NETWORKING.md) |
| Optional (`?:`) interface fields, under-assigned class fields, array destructuring past the source's length (all ✅/100%) | Read as a deterministic zero (or, for a nested-array destructured element, a safe empty array) — a real, documented simplification, not real JS's `undefined` (no general sentinel for that on a concrete scalar type). Previously read genuinely uninitialized/out-of-bounds heap memory instead, a real memory-safety bug fixed alongside this — see [ADR-00157](../adr/ADR-00157.md) | [ADR-00157](../adr/ADR-00157.md) |

---

## Design Documents (TDDs)

Anything big enough to need a design pass before implementation gets scoped out in a Technical Design Document under `docs/tdd/` first. The full index — every TDD's number, title, status, and every ADR that implements it — lives in [`docs/tdd/README.md`](../tdd/README.md); that's the source of truth. Implemented TDDs aren't relisted here: their status page, linked from the coverage tables above, already carries their caveats. Likewise, several not-yet-done TDDs already have a live pointer elsewhere in this file — Memory Management's `auto` mode ([Cross-Cutting](#cross-cutting)), IndexedDB storage (Roadmap's "Later" tier), vanilla-JS compatibility ([What Is NOT Implemented](#what-is-not-implemented) → High complexity), and `TextDecoder` non-UTF-8 (Encoding / Text row) — so they aren't repeated here either.

What's left below are the genuine orphans: not-started or partially-implemented TDDs with no other pointer anywhere in this file.

| TDD | Status | Notes |
|---|---|---|
| [00003](../tdd/TDD-00003.md) Alternative fetch Backend | Not Started | A Go helper instead of libcurl; low priority |
| [00005](../tdd/TDD-00005.md) Unannotated Parameter Typing | Partially Implemented | Clean rejection at call sites done; call-site inference and real `any` semantics not started |
| [00008](../tdd/TDD-00008.md) External Conformance Suites | Partially Implemented, ongoing | V1's hand-curated notes: [`docs/testing/CONFORMANCE-COVERAGE.md`](../testing/CONFORMANCE-COVERAGE.md). V2's real, generated, full-corpus numbers: [`docs/testing/CONFORMANCE-RESULTS.md`](../testing/CONFORMANCE-RESULTS.md) — 5,225/53,578 (9.8%) on the Linux baseline. Gaps it surfaced that are still open: generic object stringification (`` `${obj}` `` for a plain class instance), spread in call arguments — both tracked under [What Is NOT Implemented](#what-is-not-implemented) |
| [00015](../tdd/TDD-00015.md) JSON.parse Into Nested Object Fields | Not Started | Flat object parsing already works — see the JSON row above |
| [00020](../tdd/TDD-00020.md) Windows Support | Not Started | Lowest priority anywhere in this project — reference doc only |
| [00031](../tdd/TDD-00031.md) Terminal UI Primitives | Not Started, scoped and ready | See the Process / CLI I/O row above |
| [00032](../tdd/TDD-00032.md) Native Library Bindings / GUI | Not Started, bootstrapping placeholder | Needs general FFI first |
| [00033](../tdd/TDD-00033.md) Direct Hardware/Framebuffer Access | Not Started, bootstrapping placeholder | Same FFI-adjacent prerequisite gap as 00032; Linux-only by nature |
| [00036](../tdd/TDD-00036.md) Freestanding Microcontroller Target (Raspberry Pi Pico) | Not Started | Deliberately low priority; minimal-core scope only (no networking/storage/peripheral parity) |
| [00045](../tdd/TDD-00045.md) Raspberry Pi (aarch64 SBC) Target — Minimal Boot Image + GPIO | Not Started | Hosted Linux target, distinct from 00036's freestanding Pico; low priority relative to core-language work |
| [00048](../tdd/TDD-00048.md) WebAssembly Target (`wasm32-unknown-wasi`) | Not Started | Not a queued priority — reference/feasibility doc, same role as 00020. `-mm=gc` and the fiber-based event loop have no real wasm equivalent; shares its root blocker with 00020 |
| [00068](../tdd/TDD-00068.md) Object-Model Evolution (static reach vs. dynamic/prototype model) | Not Started (direction decided) | Reframes [TDD-00065](../tdd/TDD-00065.md) Stage 3c: the object model is fixed-shape nominal structs with no runtime descriptor. Splits onto two axes — near-term static (object type args in generics, 00069 below) and a deferred native dynamic/prototype model where prototype semantics and vanilla-JS compat land |
| [00069](../tdd/TDD-00069.md) Object Type Arguments in Generics | Not Started | The near-term static move from 00068: accept a named object/interface/class type as a generic type argument (not just scalars/arrays). Blocker is `mangleTypeArg` needing an object case; then field access/destructuring/`{...rest}` ride existing machinery — closing 00065 Stage 3c's generic-`T` rest at zero runtime cost |

---

## Roadmap

Grouped by kind of work rather than a fixed sequence number, since priorities shift and bug fixes get picked up opportunistically rather than in strict order. Core-language feature gaps already have their own complexity breakdown in [What Is NOT Implemented](#what-is-not-implemented) above — not repeated here.

### Next up — bugs found but not yet fixed

Pulled from each page's own Known Limitations sections. A guiding principle: **most caveats in this project are deferred shortcuts ("too much effort right now to do fully"), not permanent design decisions** — where a divergence from real JS/TS behavior is fixable and would move conformance, it should be fixed on sight rather than documented as if intentional, and the ones that genuinely are permanent scope narrowings (e.g. the fixed-shape object model, no dynamic property add/delete) say so explicitly. The boolean-printing convention (`console.log(bool)` once printed `1`/`0`) was one such shortcut, now corrected — see [ADR-00183](../adr/ADR-00183.md).

No entries currently. The most recent cluster — nullable non-pointer scalars (`number | null`, `boolean | null`, ...) lacking any runtime "absent" value, which caused `??`/`??=` to skip the null check, a class iterator's `next(): number | null` to stop on a legitimate `0`, `Map.get()` to return `0` for a missing key, and `x === null` to read a present `0` as null — is now closed by the presence-flagged `{ i1, T }` representation ([TDD-00064](../tdd/TDD-00064.md) / [ADR-00199](../adr/ADR-00199.md)), covering locals, parameters, return values, object/interface/class fields, and Maps. One niche remainder: a logical compound assignment (`??=`/`&&=`/`||=`) on a nullable-scalar *field* (as opposed to a local) is not yet routed through the presence-flagged path.

Memory management, the event loop, and the HTTP server were this project's three biggest cross-cutting structural gaps; all three are now substantially closed — see their entries under [Design Documents](#design-documents-tdds) below ([TDD-00001](../tdd/TDD-00001.md), [TDD-00006](../tdd/TDD-00006.md), [TDD-00004](../tdd/TDD-00004.md)) for the full history. The one piece of the three still genuinely open is `-mm=auto` (compiler-inserted frees, no runtime collector) — see the [Cross-Cutting](#cross-cutting) table above.

### Later — differentiator features, deliberately deprioritized

**IndexedDB-compatible storage API** (see [TDD-00011](../tdd/TDD-00011.md)) — not started, and deliberately scoped to be picked up only after most of the rest of this roadmap is further along. The idea: expose the real `indexedDB` global/`IDBDatabase`/`IDBObjectStore` API shape (not a bespoke KV API, and not a SQL surface) so hand-written app code using that idiom — and, longer-term, existing npm `IndexedDB` client packages like Dexie.js/localForage, though that specifically also needs `class` support ([TDD-00009](../tdd/TDD-00009.md)) first — has somewhere to run. Four backend directions compared (lowest to highest effort/risk): a hand-rolled RESP client proxying to an external Redis (no new dependency at all — just one missing socket primitive, outbound `connect()`); an embedded SQLite (same C-linking pattern `fetch`/libcurl already uses); a from-scratch native storage engine (zero dependency, matching this project's usual ethos, but real crash-safety engineering); or embedding a mature pure-Go engine (BBolt recommended over BadgerDB/Pebble/BuntDB/go-memdb) via a `cgo`-built static archive linked into the compiled output — gated on a direct prototype confirming the Go runtime's own background threading/signal handling coexists safely with this compiler's fiber scheduler, not yet verified either way.

**Native reinterpretations of a few browser APIs** (see [NOTIFICATIONS-MISC.md](NOTIFICATIONS-MISC.md)) — the same idea as the IndexedDB work above: keep a familiar browser API *shape*, back it with an OS-native implementation. The candidates are the Notifications API (→ macOS Notification Center / Linux `libnotify`), the Storage API's `localStorage`/`sessionStorage` (→ a file-persisted / in-process KV store, a lighter sibling of the IndexedDB idea), and the Clipboard API (→ the OS clipboard). None is scoped in a TDD yet or a committed target — noted so they're on the map rather than dismissed as "browser-only." The rest of that page (Push, Service Worker, Geolocation, Canvas/WebGL) stays genuinely out of scope with no native analogue.

### Web Platform & Node.js APIs backlog

Not-yet-implemented items from the [Web Platform APIs](#web-platform-apis) and [Node.js APIs](#nodejs-apis) sections above, grouped by effort. Within a tier, the same tiebreaker applies — prefer whichever unlocks REST API interaction / file I/O / process interaction.

The event loop existing now ([TDD-00006](../tdd/TDD-00006.md)) changes the shape of this backlog: several items below used to be tiered partly by "needs the event loop to exist first," which is no longer a real blocker for any of them. Tiers are re-evaluated against what actually remains, not against that now-satisfied prerequisite.

**Medium effort (new dependency or subsystem):**
- `CompressionStream` / `DecompressionStream` — link `zlib`. See [STREAMS.md](STREAMS.md).
- `EventTarget` / `Event` / `CustomEvent` — generic event bus; prerequisite for a general-purpose `AbortController` and others. See [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md).
- `AbortController` / `AbortSignal` — a *fetch-specific* cancellation token is now lower effort than the general version implies: the multi-interface machinery [ADR-00050](../adr/ADR-00050.md) built already tracks each in-flight transfer via its own easy handle, and `curl_multi_remove_handle` + `curl_easy_cleanup` is a real, already-available way to cancel one mid-transfer. A general, `EventTarget`-based signal usable by other consumers (timers, streams) is still gated on `EventTarget` existing first.

**High effort (needs a concurrency model beyond the event loop's single-fiber cooperative scheduling, or a new external dependency):**
- `Worker` (Web Workers) — threads via `pthreads`; requires `SharedArrayBuffer` + `Atomics` too. The shipped event loop is cooperative, one-fiber-at-a-time concurrency ([TDD-00006](../tdd/TDD-00006.md)), not preemptive multi-threading — a genuinely separate mechanism, not an extension of it. Scoped — see [TDD-00047](../tdd/TDD-00047.md) — and picked up ahead of `EventTarget` below despite being the higher-risk item, deliberately, to surface concurrency bugs in shared runtime state early. See [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md).
- `crypto.subtle` (digest, encrypt, sign) — delegate to OpenSSL or Apple CommonCrypto. See [WEB-CRYPTO.md](WEB-CRYPTO.md).
- `ReadableStream` / `WritableStream` / `TransformStream` — full streaming pipeline; complex backpressure model. See [STREAMS.md](STREAMS.md).

---

*Last updated: 2026-08-17 — flagged native reinterpretations of a few browser APIs (Notifications, Storage, Clipboard) as deferred differentiators, same nature as [TDD-00011](../tdd/TDD-00011.md) — see [NOTIFICATIONS-MISC.md](NOTIFICATIONS-MISC.md). Test262 corpus at 5225/53578 (9.8%) on the Linux baseline.*
