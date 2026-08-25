# KlainMainLang — Implementation Status

> TypeScript → native compiler written in Go. Emits LLVM IR text, compiled with `clang -O2`.
> Targets whatever architecture the host clang defaults to (arm64 on Apple Silicon, x86-64 on Linux, etc.).
> Multi-file compilation exists (named `import`/`export` only, V1 scope — see [MODULES.md](MODULES.md)); the entry file's top-level statements still all run in one `main()`, and imported files may only contain declarations.
> No garbage collector by default — every heap allocation is `malloc`'d and (almost) never `free`d in `manual` mode. `-mm=gc` opts into a real one (Boehm). See [MEMORY-MANAGEMENT.md](MEMORY-MANAGEMENT.md).
> Programs are pure libc by default; a program only needs `libcurl` installed on the build machine if it actually calls `fetch` (compiled binaries automatically link `-lcurl` only when used — see [ADR-00020](../adr/ADR-00020.md)).

This file is the scannable index: per-area completion % plus the caveats/blockers that matter most. Each linked page carries the full feature-by-feature table (and, where relevant, its own Known Limitations) for that area — trust the linked page over this summary if they ever drift apart. All detail pages follow one layout — see [Status page format](#status-page-format).

## Contents

- [TypeScript Core Language](#typescript-core-language) — core JavaScript/TypeScript language & standard library (works the same in any JS host)
- [Web Platform APIs](#web-platform-apis) — WHATWG/browser-standard APIs (also implemented by Node.js, but not part of the JS *language* itself)
- [Node.js APIs](#nodejs-apis) — `fs`, `process`, and a real `http.listen` server — Node-specific runtime globals with no browser equivalent
- [Cross-Cutting](#cross-cutting) — concerns spanning every feature area (memory management)
- [What Is NOT Implemented](#what-is-not-implemented) — core language gaps, by priority/complexity
- [Fidelity Gaps in Shipped Features](#fidelity-gaps-in-shipped-features) — features marked ✅/100% that still have real, non-cosmetic differences from actual JS/TS behavior
- [Design Documents (TDDs)](#design-documents-tdds)
- [Roadmap](#roadmap)
- [Status page format](#status-page-format) — the shared layout every detail page follows

---

## TypeScript Core Language

**293 / 303 features, ~97% coverage.**

**Strict Coverage** counts a feature only when its row's Caveats cell is empty (zero known caveats/bugs of any severity), over the same denominator as Coverage ([ADR-00205](../adr/ADR-00205.md)). Both figures derive directly from each page's table per the [Status page format](#status-page-format).

| Category | Coverage | Strict | Page | Caveats |
|---|---|---|---|---|
| Control flow statements | 12/12, 100% | 10/12, ~83% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | • A `for` loop's non-declaration init clause (`for (i = 0, j = 10; ...)`) is a clean rejection (the update clause does take comma-separated expressions — [ADR-00156](../adr/ADR-00156.md))<br>• No ASI restriction on a line break before a postfix `++`/`--`<br>• `finally` does not run on an early `return`/`break`/`continue` inside an `async`/generator body (it does everywhere else — [ADR-00191](../adr/ADR-00191.md)) |
| Operators | 22/22, 100% | 16/22, ~73% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | • `typeof` of a static-method reference outside the covered Promise/Math/JSON tables (`Object.keys`, `console.log`, …) answers from inference, not `"function"` ([ADR-00282](../adr/ADR-00282.md))<br>• `**` uses exact i64 integer semantics — a negative exponent gives 0; use a float operand for fractional powers ([ADR-00187](../adr/ADR-00187.md))<br>• `&&`/`\|\|` yield a bool by default; `-compat=js` makes them value-preserving for same-typed operands ([ADR-00186](../adr/ADR-00186.md)/[ADR-00220](../adr/ADR-00220.md)) |
| Variable declarations | 3/3, 100% | 0/3, 0% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | Definite-assignment is enforced for a typed `var`/`let` read before assignment — including `if`/`else`, `do/while`, and `switch`-with-`default` control flow — but the analysis is sound-not-complete: a binding assigned only inside a maybe-skipped `for`/`while` body, or in a `try` that may throw first, still escapes (reading its deterministic zero/`undefined` default — [ADR-00215](../adr/ADR-00215.md) — not uninitialized memory); correlated conditions (`if (c) {x=1} if (!c) {x=2}`) are conservatively *rejected*, matching TypeScript. See [TDD-00071](../tdd/TDD-00071.md)/[ADR-00210](../adr/ADR-00210.md)–[ADR-00215](../adr/ADR-00215.md) |
| Functions & closures | 10/10, 100% | 1/10, ~10% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | • Nested function declarations are V1-scoped (no closure capture of the enclosing body's locals, one block deeper than the enclosing body is unsupported) — see [TDD-00057](../tdd/TDD-00057.md)<br>• Tagged template literals have no `.raw` property on the `strings` array (real `String.raw` stays separately out of scope, see [ADR-00028](../adr/ADR-00028.md)) — see [TDD-00059](../tdd/TDD-00059.md)/[ADR-00152](../adr/ADR-00152.md)<br>• A top-level named function can't reference a sibling top-level binding whose value is a connection handle (`Worker`/`WebSocket`/`EventSource`/channels/`XMLHttpRequest`), a `Promise`, or an inferred-type-argument/nested generic instance (scalars/strings/arrays/`TypedArray`s/objects/`Map`/`Set`/class instances incl. `new Box<number>()`, the value/event handles — `Blob`/`Date`/`URL`/`RegExp`/`Headers`/`Request`/`AbortController`/`Event`/… — and streams + `EventEmitter` are promoted to module globals and work — [ADR-00342](../adr/ADR-00342.md)); a default parameter can't reference an earlier parameter; a rest-only arrow parameter list fails to parse ([ADR-00166](../adr/ADR-00166.md))<br>• A named function expression whose name shadows a top-level function of the same name is a clean compile error — rename one ([TDD-00060](../tdd/TDD-00060.md)/[ADR-00178](../adr/ADR-00178.md))<br>• A free `function*` generator may be top-level **or nested** (a nested one captures enclosing state by reference; an array capture is a clean rejection); a generator *expression* works as a top-level `const G = function* ...` binding with the element type inferred from yields, other positions stay clean rejections ([TDD-00096](../tdd/TDD-00096.md)/[ADR-00293](../adr/ADR-00293.md)) |
| Type primitives | 12/12, 100% | 7/12, ~58% | [TYPE-SYSTEM.md](TYPE-SYSTEM.md) | • A bare `: number` is `i64`, not a double, so a fractional value truncates — `const x: number = 0.1 + 0.2` yields `0`; use `float64`/`float32` (or `/** @type {float64} */`) for real double arithmetic<br>• No `bigint` `.toLocaleString`/`asIntN` ([TDD-00074](../tdd/TDD-00074.md)) |
| Async / Promise | 9/9, 100% | 9/9, 100% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | |
| String methods | 27/29, ~93% | 20/29, ~69% | [STRING-METHODS.md](STRING-METHODS.md) | • No `.normalize()`<br>• `.codePointAt()` answers `NaN` where real JS gives `undefined` |
| RegExp | 13/14, ~93% | 2/14, ~14% | [REGEXP.md](REGEXP.md) | • `u`/`y`/`d` flags out of scope for V1<br>• Several other deliberate scope narrowings (`.test()` ignores `lastIndex`, no implicit string→RegExp coercion, etc.) — see [REGEXP.md](REGEXP.md)'s own Caveats<br>• `new RegExp` doesn't validate its flags string ([ADR-00166](../adr/ADR-00166.md)) |
| Array methods | 35/35, 100% | 25/35, ~71% | [ARRAY-METHODS.md](ARRAY-METHODS.md) | • `.flat(depth?)`/`.flatMap(fn)`: `depth` must be a compile-time constant (or `Infinity`), since this compiler's array element types are static ([TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md))<br>• `Array.from` supports the array-like overload only<br>• `.pop()`/`.shift()` on an empty array return the element type's zero value, not `undefined` ([ADR-00157](../adr/ADR-00157.md) convention) |
| Number / Math | 35/35, 100% | 26/35, ~74% | [NUMBER-MATH.md](NUMBER-MATH.md) | • `parseInt` has no hex auto-detect with an omitted radix; `parseFloat` inherits `strtod`'s `"inf"`/hex-float extras<br>• `Number.prototype.toString(radix?)` truncates a non-integer receiver and doesn't validate the radix |
| Object & collections | 28/29, ~97% | 17/29, ~59% | [OBJECT-COLLECTIONS.md](OBJECT-COLLECTIONS.md) | • `Object.create` stays out of scope (prototype model — [TDD-00068](../tdd/TDD-00068.md))<br>• `WeakMap`/`WeakSet`/`WeakRef` are strong-backed under `-mm=manual`; real weak semantics only under `-mm=gc` ([TDD-00112](../tdd/TDD-00112.md))<br>• Object literal method shorthand (`{ foo() {...} }`) has no `this` binding (no nominal type to give it a shape, no dynamic call-site binding) — a clean compile-time rejection, not silently wrong. See [ADR-00169](../adr/ADR-00169.md) |
| JSON | 13/15, ~87% | 10/15, ~67% | [JSON.md](JSON.md) | • A runtime `space` argument and a non-null `replacer` on `JSON.stringify` are clean rejections ([ADR-00222](../adr/ADR-00222.md))<br>• `JSON.parse` → `any`/`unknown` (dynamic shape) is unsupported ([TDD-00077](../tdd/TDD-00077.md) Track P P4); a bare reassignment `xs = JSON.parse(...)` lacks target-type context |
| console | 11/12, ~92% | 8/12, ~67% | [CONSOLE.md](CONSOLE.md) | • No `console.table()`<br>• `console.trace` prints no stack<br>• `console.time()`/`.timeEnd()` are a single global timer slot, not per-label |
| Global functions & constants | 19/21, ~90% | 8/21, ~38% | [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md) | • `eval` is an opt-in embedded-engine path ([TDD-00046](../tdd/TDD-00046.md)), not started<br>• `parseInt` lacks hex auto-detect with an omitted radix<br>• `NaN`/`Infinity` shadowing needs `-compat=js`, not unconditional |
| Type system features | 13/13, 100% | 7/13, ~54% | [TYPE-SYSTEM.md](TYPE-SYSTEM.md) | • Generics support object/interface/class type arguments ([TDD-00069](../tdd/TDD-00069.md)) and `<T extends X>` constraints ([TDD-00113](../tdd/TDD-00113.md)); a *function*'s type arguments are inference-only (no explicit `identity<number>(5)`), and Map/Set/Promise/closure type arguments are a clean rejection ([TDD-00010](../tdd/TDD-00010.md), [TDD-00037](../tdd/TDD-00037.md))<br>• Union types (beyond `T \| null`) allow scalar members plus object members (one object member, or ≥2 as a discriminated union with a first-position string-literal tag), not nested inside a container; flow narrowing (`typeof`/truthiness/`==null`/`tag===lit`, if-else + early-return) works for a union local ([TDD-00043](../tdd/TDD-00043.md)/[TDD-00114](../tdd/TDD-00114.md)/[TDD-00115](../tdd/TDD-00115.md)/[TDD-00116](../tdd/TDD-00116.md))<br>• Tuple types are fixed-shape structs ([TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md))<br>• Intersection types are object-members-only, with a field conflict rejected rather than the `-compat=js` `never`-field ([TDD-00078](../tdd/TDD-00078.md)/[ADR-00225](../adr/ADR-00225.md))<br>• Mapped & utility types are evaluated to concrete object types; `Partial`/`Required`/`Readonly` are structural no-ops, no `as` remapping ([TDD-00079](../tdd/TDD-00079.md)/[ADR-00228](../adr/ADR-00228.md)–[ADR-00230](../adr/ADR-00230.md))<br>• Conditional types + `infer` evaluate at compile time (monomorphized check type); `infer` is scoped to Array/Promise/bare forms, no function-signature inference ([ADR-00231](../adr/ADR-00231.md)) |
| Classes / OOP | 17/17, 100% | 10/17, ~59% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | • No user-definable `class X extends Error` (built-in types aren't valid `extends` targets, by design)<br>• Clean rejections: static-field initializers, static/async generator methods, a dynamic computed member key (`[Symbol.asyncIterator]`/`[Symbol.iterator]` are accepted, desugared to the iteration methods — [TDD-00089](../tdd/TDD-00089.md)/[ADR-00278](../adr/ADR-00278.md)), and a class expression used as a runtime value<br>• A field initializer can currently see constructor params (real JS's separate initializer scope forbids this — [ADR-00180](../adr/ADR-00180.md))<br>• `#x` private names have no early-error check for `#m`/`static #m` name collision or the `#constructor`-is-banned rule ([ADR-00155](../adr/ADR-00155.md)) |
| Modules | 14/15, ~93% | 8/15, ~53% | [MODULES.md](MODULES.md) | • Whole-program compile only<br>• `klmpm` is Stage 1 only — a `klain_modules/<name>/` package must be hand-built, no package-manager tool yet ([TDD-00054](../tdd/TDD-00054.md))<br>• No namespace re-exports (`export * as ns from`); no npm/`node_modules` interop ([TDD-00053](../tdd/TDD-00053.md)); no dynamic `import()` (parses, cleanly rejected — [TDD-00055](../tdd/TDD-00055.md)/[TDD-00056](../tdd/TDD-00056.md))<br>• Node-style built-ins (`fs`/`path`/…) require a default, namespace, or named import, not ambient ([TDD-00049](../tdd/TDD-00049.md)) |

## Web Platform APIs

WHATWG/W3C-standard APIs — the kind a browser **and** Node.js both implement. Not part of the JS *language* itself, but not Node-specific either. Filtered to those that make sense outside a browser context; pure browser-only APIs (DOM, Canvas, WebGL, CSS, Gamepad, etc.) are out of scope — see [NOTIFICATIONS-MISC.md](NOTIFICATIONS-MISC.md).

**55 / 55 features, 100% coverage.**

| Category | Coverage | Strict | Page | Caveats |
|---|---|---|---|---|
| Timers | 4/4, 100% | 2/4, 50% | [TIMERS.md](TIMERS.md) | • A timer callback must be a zero-argument `void` function (a closure or a bare top-level-function reference — [ADR-00200](../adr/ADR-00200.md))<br>• `setImmediate` and a same-tick `setTimeout(fn, 0)` are indistinguishable — `__kml_timer_drain` is one flat fire-time-ordered queue with no check-vs-timers phases |
| Encoding / Text | 2/2, 100% | 1/2, 50% | [ENCODING-TEXT.md](ENCODING-TEXT.md) | UTF-8 only — see [TDD-00034](../tdd/TDD-00034.md) for non-UTF-8 scope |
| URL | 3/3, 100% | 0/3, 0% | [URL.md](URL.md) | • `URLSearchParams` keeps only one value per key (known limitation)<br>• `URLPattern` is object-init only with a literals/`*`/`:name`/`:name?` grammar subset; `.exec()` returns a merged `Map` of named groups, not the spec's `URLPatternResult` |
| Binary data & Typed Arrays | 9/9, 100% | 1/9, ~11% | [BINARY-DATA-TYPED-ARRAYS.md](BINARY-DATA-TYPED-ARRAYS.md) | • TypedArray construction is restricted to a variable declaration's initializer (Node `Buffer`'s call-syntax statics are exempt); no `.buffer`<br>• BigInt64Array/BigUint64Array support an explicit method allow-list — everything else is a compile-time rejection<br>• Buffer has no `utf16le`, and encodings must be string literals<br>• No `Atomics.waitAsync`, no `getFloat16` |
| Web Crypto | 8/8, 100% | 1/8, ~13% | [WEB-CRYPTO.md](WEB-CRYPTO.md) | Complete surface over the selectable `-crypto` backend (OpenSSL/CommonCrypto): digest, HMAC, AES-GCM/CBC, RSA-OAEP/RSA-PSS, ECDSA, PBKDF2/HKDF, key formats raw/pkcs8/spki/jwk; per-row caveats keep Strict low |
| Performance & Timing (incl. Date) | 9/9, 100% | 1/9, ~11% | [PERFORMANCE-TIMING.md](PERFORMANCE-TIMING.md) | `Date` is UTC-only, never local time |
| Networking (fetch, WebSocket, SSE) | 6/6, 100% | 1/6, ~17% | [NETWORKING.md](NETWORKING.md) | • `WebSocket` (server and client, [TDD-00039](../tdd/TDD-00039.md)) has no binary send; the client speaks `wss://` (TLS) but the server is `ws://` only<br>• `XMLHttpRequest` ([TDD-00040](../tdd/TDD-00040.md)) is legacy-synchronous-mode only<br>• `.text()`/`.json()` truncate at an embedded null byte — use `.arrayBuffer()` for binary bodies ([ADR-00094](../adr/ADR-00094.md))<br>• `Response.text()`/`.json()`/`.arrayBuffer()` are synchronous, not `Promise`-returning ([ADR-00241](../adr/ADR-00241.md)) |
| Streams | 8/8, 100% | 1/8, ~13% | [STREAMS.md](STREAMS.md) | • No BYOB readers / byte controllers; `ReadableStream.from()` takes arrays only; `desiredSize` is `0` (not `null`) once errored<br>• Node streams are options-form only — no `class X extends Readable`, no `Duplex`, no callback-style `pipeline`/`finished`<br>• `Blob.stream()` delivers the whole blob as one `Uint8Array` chunk, not incrementally — full per-row lists on the page |
| Events & Cancellation | 3/3, 100% | 0/3, 0% | [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md) | • Single-target dispatch — no capture/bubble/propagation (`stopPropagation` is a no-op)<br>• `removeEventListener` matches by closure identity (the listener must be a named binding); no `class X extends EventTarget`<br>• `Event`/`CustomEvent` expose only `type`/`defaultPrevented`/`detail`; the fuller WHATWG properties and `new Event` init options are absent/ignored<br>• `AbortSignal` `timeout` latches only when an await loop checks it and isn't wired into `setTimeout`; a custom `abort(reason)` still throws `AbortError`, and `DOMException` carries no numeric `.code` |
| Workers / Concurrency | 3/3, 100% | 0/3, 0% | [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md) | • One listener per event, arrow-function-literal handlers only; one message type per direction/channel<br>• `-mm=manual` leaks per message; `close()` leaks pipe fds<br>• Shared memory (`SharedArrayBuffer`/`Atomics`) is tracked on [BINARY-DATA-TYPED-ARRAYS.md](BINARY-DATA-TYPED-ARRAYS.md) |

## Node.js APIs

Node.js-specific runtime globals — not part of any Web/browser standard, but essential for the CLI-application and microservice use cases this project actually targets. Most (`fs.*`, `path.*`, `os.*`, `querystring.*`, `assert`, `http.listen`/`.close`, `cluster.*`, and this project's own `Memory.free`) require a default, namespace, or named import (`import fs from 'fs'` or `import { readFileSync } from 'fs'`) — see [MODULES.md](MODULES.md)'s import-gated-bindings row and [TDD-00049](../tdd/TDD-00049.md)/[ADR-00141](../adr/ADR-00141.md)/[ADR-00142](../adr/ADR-00142.md). `process`/`console` stay ambient, like `Math`/`JSON`, matching real Node/JS.

**78 / 83 features, ~94% coverage.**

| Category | Coverage | Strict | Page | Caveats |
|---|---|---|---|---|
| File System (fs) | 13/14, ~93% | 7/14, ~50% | [FILE-SYSTEM.md](FILE-SYSTEM.md) | • Async variants (`fs.readFile(path, cb)` + `fs.promises`/`fs/promises`) run blocking I/O under the hood — async-shaped, not thread-pooled ([TDD-00107](../tdd/TDD-00107.md))<br>• `fs.createReadStream`/`createWriteStream` (Node Readable/Writable) deliver **string** chunks, read eagerly to EOF ([TDD-00108](../tdd/TDD-00108.md))<br>• `readFileSync`/`readFile`/`copyFileSync` still text-only by design — use `readFileSyncBytes`/binary-aware `writeFileSync` for binary data ([ADR-00094](../adr/ADR-00094.md)) |
| Process / CLI I/O | 22/24, ~92% | 11/24, ~46% | [PROCESS-CLI.md](PROCESS-CLI.md) | • `process.env` writes work but there's no `delete` (no dynamic-delete operator)<br>• `process.on(...)` covers `'SIGINT'`/`'SIGTERM'`/`'exit'`/`'uncaughtException'` but not `'unhandledRejection'`; an `'uncaughtException'` handler runs then still exits (can't resume)<br>• `process.memoryUsage()`, `process.version` still missing |
| HTTP Server | 13/13, 100% | 9/13, ~69% | [HTTP-SERVER.md](HTTP-SERVER.md) | • `req.body`/response `body` string fields still truncate at an embedded null byte (`bodyBytes()` accessors are additive, not a fix)<br>• `.close()` in a `{ workers: N }` cluster only stops the calling worker process |
| `path` | 8/8, 100% | 7/8, ~88% | [PATH.md](PATH.md) | POSIX-only (this compiler doesn't cross-compile) |
| `os` | 7/7, 100% | 6/7, ~86% | [OS.md](OS.md) | • Verified on Linux and Apple Silicon (M4 Pro)<br>• Darwin `os.cpus()` reports `speed` 0 on M-series (no fixed clock — matches Node) and has no `irq` tick bucket — see [OS.md](OS.md)'s Known Limitations |
| `events` (`EventEmitter`) | 6/6, 100% | 3/6, 50% | [EVENT-EMITTER.md](EVENT-EMITTER.md) | • Single payload type per emitter, not real Node's variadic `...args`<br>• No overriding `on`/`emit`/etc. in a subclass<br>• `instanceof EventEmitter` is a compile error |
| Other core modules (`querystring`, `assert`, `zlib`, `net`, `util`, `dns`, `dgram`, `cluster`, `tls`, `vm`, `http2`) | 10/11, ~91% | 1/11, ~9% | [NODE-CORE-MODULES.md](NODE-CORE-MODULES.md) | `querystring`/`assert`/`zlib`/`net`/`util`/`dns`/`dgram`/`cluster`/`tls`/`http2` done (`tls` = client + server; `http2` = h2 fetch client + transparent h2c `http.listen` server via nghttp2, h2-over-TLS deferred — [TDD-00111](../tdd/TDD-00111.md)); `vm` not started |

## Cross-Cutting

Concerns that span every feature area rather than living in one of them.

| Area | Status | Strict | Page | Caveats |
|---|---|---|---|---|
| Memory management | 2/3 modes (`manual`, `gc`) | 0/3, 0% | [MEMORY-MANAGEMENT.md](MEMORY-MANAGEMENT.md) | • `manual` (default) never frees on its own<br>• `auto` (compiler-inserted frees, no runtime collector) is design-only — [TDD-00001](../tdd/TDD-00001.md) |
| Compatibility mode (`-compat`) | ✅ 2 modes (`strict`, `js`) | — | — | • The whole-program strict-vs-JS axis ([TDD-00075](../tdd/TDD-00075.md)/[ADR-00217](../adr/ADR-00217.md)): `strict` (default) is the compiler's opinionated, safer-than-JS semantics; `-compat=js` opts into JS-faithful behavior. It's a third category for divergences — deliberate design choices, not bugs or permanent narrowings<br>• Current inhabitant — ambient-global shadowing (absorbs the former `-globals` flag, [TDD-00050](../tdd/TDD-00050.md)/[ADR-00143](../adr/ADR-00143.md)): `strict` rejects a declaration colliding with a built-in name (`Math`/`fetch`/…) as a compile error; `js` allows real-JS/browser shadowing, plain-identifier globals only (`new`-form `Map`/`Date`/`RegExp` stay reserved either way)<br>• Inhabitant — `bigint`↔float comparison: `strict` rejects it (a likely bug); `-compat=js` does JS's exact real-number comparison (exact even past 2^53) |

---

## What Is NOT Implemented

This section is core-*language* gaps only. Not-yet-built library/runtime APIs (Web Platform + Node modules) live in the [Roadmap](#roadmap)'s backlog, not here.

### Medium complexity

| Feature | Notes |
|---|---|
| Spread into a fixed-arity function, a fixed parameter slot, or an uncommon variadic builtin | `f(...arr)` where `f` has no rest parameter, `f(...arr)` where the spread would land on a fixed slot of a rest function, and `Math.hypot(...a)`/`String.fromCharCode(...a)`. The first two need a per-slot unpack against this compiler's static arity; the third needs bespoke per-builtin array handling. A clean compile error today, not a miscompile. ([TDD-00106](../tdd/TDD-00106.md)) |

### High complexity

| Feature | Notes |
|---|---|
| Decorators | Requires metadata reflection |
| `Proxy` / `Reflect` | Dynamic property intercept; likely impractical |
| Dynamic `import()` — real lazy / runtime-conditional loading | `import.meta.url` exists ([ADR-00148](../adr/ADR-00148.md)); genuine laziness needs a `.so`/`.dylib` island per target, at odds with whole-program AOT — see [MODULES.md](MODULES.md)/[TDD-00055](../tdd/TDD-00055.md)/[TDD-00056](../tdd/TDD-00056.md) |
| Opt-in dynamic property add/delete on objects | Objects are fixed-shape heap structs — no runtime property add/delete (which is also why `Object.freeze`/`.seal` need no enforcement). Would need a different object representation, likely behind an opt-in flag; the dynamic/prototype object model is [TDD-00068](../tdd/TDD-00068.md)'s deferred Axis 2. See [OBJECT-COLLECTIONS.md](OBJECT-COLLECTIONS.md). |
| Best-effort vanilla JavaScript compatibility (opt-in flag) | Plain untyped JS fails on four independent things: class fields assigned only in the constructor (no upfront declaration), unannotated-parameter type mismatches, prototype-based pre-ES6 "classes," and dynamic property addition — the last two need the same different object representation as the row above and stay out of scope even here. A naive "default everything unannotated to `any`" approach would not work: today's `any` runtime rejects arithmetic/most operators, so it wouldn't make ordinary code like `function add(a,b){return a+b}` compile either. See [TDD-00022](../tdd/TDD-00022.md); the implicit-`any`/operator-dispatch half is scoped separately in [TDD-00076](../tdd/TDD-00076.md). |

---

## Fidelity Gaps in Shipped Features

Every row below is marked ✅ (or 100%) on its own page — the feature genuinely works for its core, documented cases — but each hides a real, non-cosmetic difference from how actual JavaScript/TypeScript behaves, beyond a one-line documented scope narrowing. Not reflected in the coverage percentages above. None of these are scheduled — pick up opportunistically alongside the open bugs or the backlog below.

| Feature | Gap | Where documented |
|---|---|---|
| `EventEmitter` (100%) | `.emit(event, data)` takes exactly one payload type per instance, not real Node's variadic `...args`; no way to override `on`/`emit`/`off`/etc. in a subclass (hand-written dispatch, not real methods); `instanceof EventEmitter` is a compile error (never a registered class) | [EVENT-EMITTER.md](EVENT-EMITTER.md)'s Known Limitations |
| HTTP Server (100%) | `req.body`/response `body` string fields still truncate at an embedded null byte (the binary-safe `bodyBytes()` accessors are additive fields, not a fix to the string ones); `.close()` in a `{ workers: N }` cluster only stops the calling worker process — no IPC exists to reach the rest of the cluster | [HTTP-SERVER.md](HTTP-SERVER.md)'s Known Limitations |
| Array methods (100%) | `.sort()`'s custom comparator (a separate C-ABI `qsort()` trampoline, not a direct closure call), `.indexOf()`/`.includes()`/`.join()` (compare/stringify a bare register directly, no callback at all), and `Object.groupBy()` (buckets store every element as a raw `i64`, a different scheme than a plain array's backing buffer) still reject a nested-array element — every *other* callback-invoking method (`.map`/`.filter`/`.forEach`/`.reduce`/`.find`/`.findIndex`/`.findLast`/`.findLastIndex`/`.some`/`.every`) supports one, see [ADR-00152](../adr/ADR-00152.md) | [ARRAY-METHODS.md](ARRAY-METHODS.md)'s own caveats paragraph |
| RegExp (100%) | `.test()` never respects `.lastIndex` even under the `g` flag (real JS shares `.exec()`'s stateful iteration); `.exec()`/`.match()` turn an unmatched optional capture group into `""` instead of a per-element `null`, and lack `index`/`input`/`groups`; `.matchAll()` is an eager `string[][]`, not a lazy iterator; no implicit string→RegExp coercion anywhere; `.replace()`/`.replaceAll()` template support is `$1`-`$9`/`$&`/`$$` only (no `` $` ``/`$'`) with a fixed-arity `(match, offset, string)` callback (no variadic captured groups); `.split()` doesn't replicate real JS's zero-length-match splitting or splice captured groups into the result | [REGEXP.md](REGEXP.md)'s own caveats paragraph |
| `fs.copyFileSync` (✅) | Still composes the text-only `readFileSync`/`writeFileSync` pair, so a source file with an embedded null byte copies back shorter than its real size — never migrated to the binary-safe `readFileSyncBytes`/`writeFileSync(path, ArrayBuffer)` pair [ADR-00094](../adr/ADR-00094.md) added | [FILE-SYSTEM.md](FILE-SYSTEM.md) |
| `.codePointAt()` / `.localeCompare()` (✅) | Byte-sequence stand-ins, not real Unicode/locale behavior — `.codePointAt()` is `.charCodeAt()` under another name (no surrogate-pair decoding), `.localeCompare()` is a plain `strcmp`. Correct only for ASCII/Latin-1 text | [STRING-METHODS.md](STRING-METHODS.md) |
| `setImmediate` (✅) | Indistinguishable from a same-tick `setTimeout(fn, 0)` — real Node guarantees `setImmediate` fires first when scheduled from an I/O callback (distinct check/timers event-loop phases); this compiler's timer queue is a single flat fire-time-ordered list with no phase concept | [TIMERS.md](TIMERS.md) |
| `XMLHttpRequest` / `WebSocket` (100% as part of Networking) | `XMLHttpRequest` only implements the spec's legacy synchronous mode (no default-async, callback-interleaved mode); `WebSocket` has no binary `.send()`; the client speaks `wss://` (TLS via libssl) but there is no server-side `wss` TLS listener | [NETWORKING.md](NETWORKING.md) |
| Optional (`?:`) interface fields, under-assigned class fields, array destructuring past the source's length (all ✅/100%) | Read as a deterministic zero (or, for a nested-array destructured element, an empty array) — a documented simplification, not real JS's `undefined` (no general sentinel for that on a concrete scalar type) | [ADR-00157](../adr/ADR-00157.md) |

---

## Design Documents (TDDs)

Anything big enough to need a design pass before implementation gets scoped out in a Technical Design Document under `docs/tdd/` first. The full index — every TDD's number, title, status, and every ADR that implements it — lives in [`docs/tdd/README.md`](../tdd/README.md); that's the source of truth. Implemented TDDs aren't relisted here: their status page, linked from the coverage tables above, already carries their caveats. Likewise, several not-yet-done TDDs already have a live pointer elsewhere in this file — Memory Management's `auto` mode ([Cross-Cutting](#cross-cutting)), IndexedDB storage (Roadmap's "Later" tier), vanilla-JS compatibility ([What Is NOT Implemented](#what-is-not-implemented) → High complexity), and `TextDecoder` non-UTF-8 (Encoding / Text row) — so they aren't repeated here either.

What's left below are the genuine orphans: not-started or partially-implemented TDDs with no other pointer anywhere in this file.

| TDD | Status | Notes |
|---|---|---|
| [00003](../tdd/TDD-00003.md) Alternative fetch Backend | Not Started | A Go helper instead of libcurl; low priority |
| [00082](../tdd/TDD-00082.md) External Conformance Suites V3 (supersedes [00008](../tdd/TDD-00008.md)) | Partially Implemented, ongoing | Widens the benchmark from the language layer (Test262 — real full-corpus numbers in [`docs/testing/CONFORMANCE-RESULTS.md`](../testing/CONFORMANCE-RESULTS.md), ~5,285/53,578 ≈ 9.9% on the Linux baseline) to the Web Platform (WPT — the DOM-free `.any.js`/`.window.js` slice behind a `testharness.js` shim; `dom/abort` is a near-exact oracle for the cancellation work) and Node core (`test/`, mostly `common`-harness-coupled) buckets. The TypeScript suite stays checklist-only (no runtime oracle). |
| [00020](../tdd/TDD-00020.md) Windows Support | Not Started | Lowest priority anywhere in this project — reference doc only |
| [00031](../tdd/TDD-00031.md) Terminal UI Primitives | Not Started, scoped and ready | See the Process / CLI I/O row above |
| [00032](../tdd/TDD-00032.md) Native Library Bindings / GUI | Not Started, bootstrapping placeholder | Needs general FFI first |
| [00033](../tdd/TDD-00033.md) Direct Hardware/Framebuffer Access | Not Started, bootstrapping placeholder | Same FFI-adjacent prerequisite gap as 00032; Linux-only by nature |
| [00036](../tdd/TDD-00036.md) Freestanding Microcontroller Target (Raspberry Pi Pico) | Not Started | Deliberately low priority; minimal-core scope only (no networking/storage/peripheral parity) |
| [00045](../tdd/TDD-00045.md) Raspberry Pi (aarch64 SBC) Target — Minimal Boot Image + GPIO | Not Started | Hosted Linux target, distinct from 00036's freestanding Pico; low priority relative to core-language work |
| [00048](../tdd/TDD-00048.md) WebAssembly Target (`wasm32-unknown-wasi`) | Not Started | Not a queued priority — reference/feasibility doc, same role as 00020. `-mm=gc` and the fiber-based event loop have no real wasm equivalent; shares its root blocker with 00020 |
| [00068](../tdd/TDD-00068.md) Object-Model Evolution (static reach vs. dynamic/prototype model) | Not Started (direction decided) | Reframes [TDD-00065](../tdd/TDD-00065.md) Stage 3c: the object model is fixed-shape nominal structs with no runtime descriptor. Splits onto two axes — near-term static (object type args in generics, [00069](../tdd/TDD-00069.md), shipped) and a deferred native dynamic/prototype model where prototype semantics and vanilla-JS compat land |
| [00072](../tdd/TDD-00072.md) Enriched Diagnostics & Strict Spec-Error Matching | Not Started | Two independent parts: a low-cost source-snippet + `^` caret diagnostic renderer (default), and classifying errors under a JS error *kind*/wording behind a strict mode — the same axis as [TDD-00075](../tdd/TDD-00075.md). The Test262 harness scores negative tests on phase, not message text, so message-matching has little conformance value and belongs in a mode |
| [00073](../tdd/TDD-00073.md) Runtime Debug Symbols (DWARF) | Not Started | Emit LLVM debug metadata (`DISubprogram`/`!dbg`) under a `--debug` flag built at `-O0`/`-Og`; positions already exist on every AST node. MVP is line-level stack traces/breakpoints, full is `DILocalVariable`+DWARF types for variable inspection. The ASan/UBSan builds already pass `-g`, so sanitizer reports would gain `.ts` source lines |
| [00076](../tdd/TDD-00076.md) Real `any` Semantics as a `-compat` Inhabitant | Not Started | Successor to the superseded [TDD-00005](../tdd/TDD-00005.md): implicit-`any` on unannotated params and runtime operator dispatch on a boxed `any`, split into `strict` (`noImplicitAny` error / clean reject — today's behavior) vs `-compat=js` (boxed `any` / runtime tag-pair dispatch). Boxed-`any` representation holes stay [TDD-00062](../tdd/TDD-00062.md)'s deferred set |

---

## Roadmap

Grouped by kind of work rather than a fixed sequence number, since priorities shift and bug fixes get picked up opportunistically rather than in strict order. Core-language feature gaps already have their own complexity breakdown in [What Is NOT Implemented](#what-is-not-implemented) above — not repeated here.

### Next up — bugs found but not yet fixed

Pulled from each page's own Known Limitations sections. A guiding principle: **most caveats in this project are deferred shortcuts ("too much effort right now to do fully"), not permanent design decisions** — where a divergence from real JS/TS behavior is fixable and would move conformance, it should be fixed on sight rather than documented as if intentional, and the ones that genuinely are permanent scope narrowings (e.g. the fixed-shape object model, no dynamic property add/delete) say so explicitly.

None currently open.

### Web Platform & Node.js APIs backlog

Not-yet-implemented items from the [Web Platform APIs](#web-platform-apis) and [Node.js APIs](#nodejs-apis) sections above, grouped by effort. Within a tier, the same tiebreaker applies — prefer whichever unlocks REST API interaction / file I/O / process interaction.

What remains, tiered by effort:

**Medium effort (new dependency or subsystem):**
- `vm`, `http2` — lower individual priority, grouped in [NODE-CORE-MODULES.md](NODE-CORE-MODULES.md). Feasibility (assessed 2026-08-23): `vm` has only a narrow static subset (a compile-time string-literal expression via the existing `eval` fast path); anything dynamic needs the unstarted embedded engine ([TDD-00046](../tdd/TDD-00046.md)). `http2` is a genuine from-scratch subsystem (the server is hand-rolled HTTP/1.1, no nghttp2). `tls` ships client + server (`tls.connect` / `tls.createServer`, OpenSSL libssl — [TDD-00109](../tdd/TDD-00109.md)/[TDD-00110](../tdd/TDD-00110.md)).

### Later — differentiator features, deliberately deprioritized

**IndexedDB-compatible storage API** (see [TDD-00011](../tdd/TDD-00011.md)) — not started, and deliberately scoped to be picked up only after most of the rest of this roadmap is further along. The idea: expose the real `indexedDB` global/`IDBDatabase`/`IDBObjectStore` API shape (not a bespoke KV API, and not a SQL surface) so hand-written app code using that idiom — and, longer-term, existing npm `IndexedDB` client packages like Dexie.js/localForage, though that specifically also needs `class` support ([TDD-00009](../tdd/TDD-00009.md)) first — has somewhere to run. Four backend directions compared (lowest to highest effort/risk): a hand-rolled RESP client proxying to an external Redis (no new dependency at all — just one missing socket primitive, outbound `connect()`); an embedded SQLite (same C-linking pattern `fetch`/libcurl already uses); a from-scratch native storage engine (zero dependency, matching this project's usual ethos, but real crash-safety engineering); or embedding a mature pure-Go engine (BBolt recommended over BadgerDB/Pebble/BuntDB/go-memdb) via a `cgo`-built static archive linked into the compiled output — gated on a direct prototype confirming the Go runtime's own background threading/signal handling coexists safely with this compiler's fiber scheduler, not yet verified either way.

**Native reinterpretations of a few browser APIs** (see [NOTIFICATIONS-MISC.md](NOTIFICATIONS-MISC.md)) — the same idea as the IndexedDB work above: keep a familiar browser API *shape*, back it with an OS-native implementation. The candidates are the Notifications API (→ macOS Notification Center / Linux `libnotify`), the Storage API's `localStorage`/`sessionStorage` (→ a file-persisted / in-process KV store, a lighter sibling of the IndexedDB idea), and the Clipboard API (→ the OS clipboard). None is scoped in a TDD yet or a committed target — noted so they're on the map rather than dismissed as "browser-only." The rest of that page (Push, Service Worker, Geolocation, Canvas/WebGL) stays genuinely out of scope with no native analogue.

---

## Status page format

Every detail page linked from this index follows one layout, so the tables stay scannable and this index can deep-link into them:

- **Status is binary** — each feature row is ✅ or ❌, nothing else. An implemented feature with a real behavioral divergence still counts ✅, but that divergence lives in the **Caveats** column, and any non-empty Caveats cell excludes the row from Strict Coverage.
- **Two note columns, both bulleted** — **Caveats** (behavioral divergences from real JS/TS) and **Notes** (implementation/representation detail only, no behavioral gap). Neither ever appears in the Status field, and caveats are not also summarized in a block at the top of the page.
- **One table per category** — when a page hosts more than one of this index's categories (e.g. [TYPE-SYSTEM.md](TYPE-SYSTEM.md)'s *Type primitives* vs *Type system features*), it splits into a `##`-headed table per category. Headings are the **plain category name only** (`## Control flow statements`) — never with numbers in them, keeping the page's own GitHub anchors clean (`#control-flow-statements`) for in-page navigation. A single-category page uses one table and no category heading.
- **Numbers derive from the tables, and appear once** — Coverage = ✅ rows / total rows; Strict = (✅ rows with an empty Caveats cell) / total rows. They live only in the page's top `**Coverage**`/`**Strict Coverage**` line (which lists each category's figure when a page has several), never repeated in a section heading.
- **This index links to the page, not a section anchor** — each category row's Page cell points at the whole page file (`PAGE.md`), never a `#section` fragment, so the link resolves in every Markdown viewer (some IDEs can't navigate a cross-file header anchor and error on the click); it keeps its own one-line Coverage/Strict figure matching the page, which is the source of truth if the two ever drift.

Every detail page linked from this index follows this format.

*Last updated: 2026-08-25*
