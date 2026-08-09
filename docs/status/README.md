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
- [Design Documents (TDDs)](#design-documents-tdds)
- [Roadmap](#roadmap)

---

## TypeScript Core Language

**286 / 329 features, ~87% coverage.**

| Category | Coverage | Page | Caveats |
|---|---|---|---|
| Control flow statements | 11/11, 100% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | — |
| Operators | 42/42, 100% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | — |
| Variable declarations | 4/4, 100% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | — |
| Functions & closures | 8/11, ~73% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | No nested function declarations; a same-file forward-reference inference gap; no tagged templates |
| Type primitives | 8/14, ~57% | [TYPE-SYSTEM.md](TYPE-SYSTEM.md) | No `symbol`/`bigint`; no union types beyond `T \| null` |
| Async / Promise | 4/9, ~44% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | Only `await fetch(...)` is genuinely non-blocking; every other `Promise<T>` is a resolved-slot read |
| String methods | 28/33, ~85% | [STRING-METHODS.md](STRING-METHODS.md) | No `.normalize()` |
| RegExp | 14/14, 100% | [REGEXP.md](REGEXP.md) | `u`/`y`/`d` flags out of scope for V1; several other deliberate scope narrowings (`.test()` ignores `lastIndex`, no implicit string→RegExp coercion, etc.) — see [REGEXP.md](REGEXP.md)'s own Caveats |
| Array methods | 40/40, 100% | [ARRAY-METHODS.md](ARRAY-METHODS.md) | `.flat(depth?)`/`.flatMap(fn)`: `depth` must be a compile-time constant (or `Infinity`), since this compiler's array element types are static ([TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md)); `Array.from` supports the array-like overload only |
| Number / Math | 35/35, 100% | [NUMBER-MATH.md](NUMBER-MATH.md) | — |
| Object & collections | 23/26, ~88% | [OBJECT-COLLECTIONS.md](OBJECT-COLLECTIONS.md) | No `WeakMap`/`WeakSet`/`WeakRef`, `Object.create`/`.fromEntries` |
| JSON | 9/11, ~82% | [JSON.md](JSON.md) | No nested-object `JSON.parse`; array-typed interface fields fail to compile instead of a clean rejection (known limitation) |
| console | 11/12, ~92% | [CONSOLE.md](CONSOLE.md) | No `console.table()` |
| Global functions & constants | 14/17, ~82% | [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md) | No `queueMicrotask`; `eval` won't be implemented |
| Type system features | 16/23, ~70% | [TYPE-SYSTEM.md](TYPE-SYSTEM.md) | Generics support any number of unconstrained type parameters, no explicit call-site type arguments ([TDD-00010](../tdd/TDD-00010.md), [TDD-00037](../tdd/TDD-00037.md)); no intersection/tuple/mapped types |
| Classes / OOP | 14/15, ~93% | [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) | Real JS/TS `#x` runtime-private field syntax (a different mechanism from the `private` keyword modifier — scoped separately, see [TDD-00021](../tdd/TDD-00021.md)); no user-definable `class X extends Error` (built-in types aren't valid `extends` targets, by design) |
| Modules | 6/13, ~46% | [MODULES.md](MODULES.md) | Whole-program compile only, with true per-file scoping and import aliasing ([TDD-00041](../tdd/TDD-00041.md)) — no `export default`/namespace imports/re-exports; no dynamic `import()`/`import.meta` |

## Web Platform APIs

WHATWG/W3C-standard APIs — the kind a browser **and** Node.js both implement. Not part of the JS *language* itself, but not Node-specific either. Filtered to those that make sense outside a browser context; pure browser-only APIs (DOM, Canvas, WebGL, CSS, Gamepad, etc.) are out of scope — see [NOTIFICATIONS-MISC.md](NOTIFICATIONS-MISC.md).

**29 / ~65 features, ~45% coverage.**

| Category | Coverage | Page | Caveats |
|---|---|---|---|
| Timers | 3/4, 75% | [TIMERS.md](TIMERS.md) | No `queueMicrotask` |
| Encoding / Text | 2/2, 100% | [ENCODING-TEXT.md](ENCODING-TEXT.md) | UTF-8 only — see [TDD-00034](../tdd/TDD-00034.md) for non-UTF-8 scope |
| URL | 2/3, ~67% | [URL.md](URL.md) | `URLSearchParams` keeps only one value per key (known limitation) |
| Binary data & Typed Arrays | 9/17, ~53% | [BINARY-DATA-TYPED-ARRAYS.md](BINARY-DATA-TYPED-ARRAYS.md) | No `DataView`/`Blob`/`SharedArrayBuffer`/`Atomics`; no `BigInt64Array`/`BigUint64Array` (needs `bigint`); no Node `Buffer` |
| Web Crypto | 2/8, 25% | [WEB-CRYPTO.md](WEB-CRYPTO.md) | All of `crypto.subtle.*` unimplemented |
| Performance & Timing (incl. Date) | 9/9, 100% | [PERFORMANCE-TIMING.md](PERFORMANCE-TIMING.md) | `Date` is UTC-only, never local time |
| Networking (fetch, WebSocket, SSE) | 6/6, 100% | [NETWORKING.md](NETWORKING.md) | `WebSocket` (both server and client, [TDD-00039](../tdd/TDD-00039.md)) has no binary send and no `wss://`/TLS; `XMLHttpRequest` ([TDD-00040](../tdd/TDD-00040.md)) is legacy-synchronous-mode only; `.text()`/`.json()` still truncate at an embedded null byte by design — use `.arrayBuffer()` for binary bodies ([ADR-00094](../adr/ADR-00094.md)) |
| Streams | 0/8, 0% | [STREAMS.md](STREAMS.md) | Not started — neither the WHATWG API nor Node's own, differently-shaped `stream` module |
| Events & Cancellation | 0/5, 0% | [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md) | Not started; blocks a general `AbortController`. Distinct from Node's `EventEmitter`, see [EVENT-EMITTER.md](EVENT-EMITTER.md) below |
| Workers / Concurrency | 0/3, 0% | [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md) | Not started; needs `pthreads` + `SharedArrayBuffer`/`Atomics` |

## Node.js APIs

Node.js-specific runtime globals — not part of any Web/browser standard, but essential for the CLI-application and microservice use cases this project actually targets. Recognized as pseudo-namespaces (`fs.*`, `process.*`), like `Math`/`JSON` — not real importable modules.

**51 / 76 features, ~67% coverage.** A 2026-07-30 audit against the actual lexer/parser/codegen source (not just prior documentation) found a large previously-untracked surface — `path`, `os`, `EventEmitter`, async `child_process`, interactive `readline`, and several smaller core modules had zero rows anywhere before this pass. The drop from this group's earlier ~82% figure reflects newly-discovered scope, not regressed implementation. `process.on('SIGINT'/'SIGTERM', handler)` shipped the same day ([TDD-00019](../tdd/TDD-00019.md)/[ADR-00079](../adr/ADR-00079.md)), closing `http.listen`'s last open gap. `path` shipped shortly after, closing out the audit's top CLI-priority gap — see [ADR-00081](../adr/ADR-00081.md). `EventEmitter` shipped after that — see [TDD-00023](../tdd/TDD-00023.md)/[ADR-00089](../adr/ADR-00089.md) — unblocking (not yet picked up) Node's own `stream` module, async `child_process`, and interactive `readline`. `os` shipped last — see [TDD-00024](../tdd/TDD-00024.md)/[ADR-00090](../adr/ADR-00090.md); its Darwin-specific paths (`freemem()`, `cpus()`'s per-core `times`) are unverified pending real Apple Silicon hardware.

| Category | Coverage | Page | Caveats |
|---|---|---|---|
| File System (fs) | 11/13, ~85% | [FILE-SYSTEM.md](FILE-SYSTEM.md) | No async variants; `readFileSync`/`copyFileSync` still text-only by design — use `readFileSyncBytes`/binary-aware `writeFileSync` for binary data ([ADR-00094](../adr/ADR-00094.md)) |
| Process / CLI I/O | 12/23, ~52% | [PROCESS-CLI.md](PROCESS-CLI.md) | No raw `process.stdout.write`; `process.env` is read-only; `process.on(...)` now covers `'SIGINT'`/`'SIGTERM'` but not `'exit'`/`'uncaughtException'`/`'unhandledRejection'`; no async `child_process`; no interactive `readline` |
| HTTP Server | 11/11, 100% | [HTTP-SERVER.md](HTTP-SERVER.md) | — |
| `path` | 8/8, 100% | [PATH.md](PATH.md) | POSIX-only (this compiler doesn't cross-compile) |
| `os` | 7/7, 100% | [OS.md](OS.md) | Darwin's `freemem()`/`cpus().times` are written but unverified on real hardware — see [OS.md](OS.md)'s Known Limitations |
| `events` (`EventEmitter`) | 6/6, 100% | [EVENT-EMITTER.md](EVENT-EMITTER.md) | — |
| Other core modules (`util`, `assert`, `net`/`dgram`/`tls`/`dns`, `zlib`, `vm`, `cluster`, `http2`, `querystring`) | 0/11, 0% | [NODE-CORE-MODULES.md](NODE-CORE-MODULES.md) | Not started; grouped together as lower-individual-priority rather than each getting a full page |

## Cross-Cutting

Concerns that span every feature area rather than living in one of them.

| Area | Status | Page | Caveats |
|---|---|---|---|
| Memory management | 2/3 modes (`manual`, `gc`) | [MEMORY-MANAGEMENT.md](MEMORY-MANAGEMENT.md) | `manual` (default) never frees on its own; `auto` (compiler-inserted frees, no runtime collector) is design-only — [TDD-00001](../tdd/TDD-00001.md) |

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
| Nested function declarations | Separate from closures; mostly a scoping change |
| `Symbol` | Unique runtime IDs; affects `for…of`, iterators |
| Generator functions / iterators | Suspend/resume; requires coroutine machinery |
| Decorators | Requires metadata reflection |
| `Proxy` / `Reflect` | Dynamic property intercept; likely impractical |
| Opt-in dynamic property add/delete on objects | This compiler's objects are fixed-shape heap structs (an interface's field list is fixed at compile time) — real JS lets any object gain/lose properties at runtime, which `Object.freeze`/`.seal` ([ADR-00055](../adr/ADR-00055.md)) currently don't need to enforce since it's already structurally impossible for *any* object, frozen or not. Noted here as a real gap from 100% JS compatibility, not because it's next in line — surfaced while scoping freeze/seal, not researched or designed. If picked up, likely shaped as an explicit compiler flag/opt-in (a genuine dynamic property bag is a different, heavier object representation than the fixed-struct one everything else here assumes) rather than the default. See [OBJECT-COLLECTIONS.md](OBJECT-COLLECTIONS.md). |
| Best-effort vanilla JavaScript compatibility (opt-in flag) | Direct testing found plain untyped JS fails on four independent things: class fields assigned only in the constructor (no upfront declaration), unannotated-parameter type mismatches, prototype-based pre-ES6 "classes," and dynamic property addition — the last two need the same different object representation as the row above and stay out of scope even here. Confirmed a naive "default everything unannotated to `any`" approach would not work: today's `any` runtime rejects arithmetic/most operators, so it wouldn't make ordinary code like `function add(a,b){return a+b}` compile either. See [TDD-00022](../tdd/TDD-00022.md). |

### Newly identified gaps (2026-07-30 audit, not yet prioritized)

Found by checking the actual lexer/parser/codegen source directly rather than relying on prior documentation — confirmed absent, not previously tracked anywhere. Listed here rather than folded into the tiers above since none have been scoped or weighed against the rest of the roadmap yet.

| Feature | Notes |
|---|---|
| Tagged templates | Confirmed absent directly against `lexer/`/`parser/` — see [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) for the full detail and evidence per item. Getters/setters on classes are no longer in this gap list — see [TDD-00030](../tdd/TDD-00030.md)/[ADR-00110](../adr/ADR-00110.md). |
| Dynamic `import()` / `import.meta` | See [MODULES.md](MODULES.md) |
| Node `Buffer` | See [BINARY-DATA-TYPED-ARRAYS.md](BINARY-DATA-TYPED-ARRAYS.md) |
| Node's own `stream` module (distinct from WHATWG streams) | See [STREAMS.md](STREAMS.md) |
| Async `child_process`, interactive `readline` | Both built on `EventEmitter` in real Node. See [EVENT-EMITTER.md](EVENT-EMITTER.md) and [PROCESS-CLI.md](PROCESS-CLI.md) |
| `util`, `assert`, `net`/`dgram`/`tls`/`dns`, `zlib` (as a module), `vm`, `cluster`, `http2`, `querystring` | Lower individual priority — see [NODE-CORE-MODULES.md](NODE-CORE-MODULES.md) |

---

## Design Documents (TDDs)

Anything big enough to need a design pass before implementation gets scoped out in a Technical Design Document under `docs/tdd/` first. The full index — every TDD's number, title, status, and every ADR that implements it — lives in [`docs/tdd/README.md`](../tdd/README.md); that's the source of truth. Implemented TDDs aren't relisted here: their status page, linked from the coverage tables above, already carries their caveats. Likewise, several not-yet-done TDDs already have a live pointer elsewhere in this file — Memory Management's `auto` mode ([Cross-Cutting](#cross-cutting)), IndexedDB storage (Roadmap's "Later" tier), `#x` private fields (Classes / OOP row), vanilla-JS compatibility ([What Is NOT Implemented](#what-is-not-implemented) → High complexity), and `TextDecoder` non-UTF-8 (Encoding / Text row) — so they aren't repeated here either.

What's left below are the genuine orphans: not-started or partially-implemented TDDs with no other pointer anywhere in this file.

| TDD | Status | Notes |
|---|---|---|
| [00003](../tdd/TDD-00003.md) Alternative fetch Backend | Not Started | A Go helper instead of libcurl; low priority |
| [00005](../tdd/TDD-00005.md) Unannotated Parameter Typing | Partially Implemented | Clean rejection at call sites done; call-site inference and real `any` semantics not started |
| [00008](../tdd/TDD-00008.md) External Conformance Suites | Partially Implemented, ongoing | Test262 ports tracked in [`docs/testing/CONFORMANCE-COVERAGE.md`](../testing/CONFORMANCE-COVERAGE.md) |
| [00015](../tdd/TDD-00015.md) JSON.parse Into Nested Object Fields | Not Started | Flat object parsing already works — see the JSON row above |
| [00020](../tdd/TDD-00020.md) Windows Support | Not Started | Lowest priority anywhere in this project — reference doc only |
| [00031](../tdd/TDD-00031.md) Terminal UI Primitives | Not Started, scoped and ready | See the Process / CLI I/O row above |
| [00032](../tdd/TDD-00032.md) Native Library Bindings / GUI | Not Started, bootstrapping placeholder | Needs general FFI first |
| [00033](../tdd/TDD-00033.md) Direct Hardware/Framebuffer Access | Not Started, bootstrapping placeholder | Same FFI-adjacent prerequisite gap as 00032; Linux-only by nature |
| [00036](../tdd/TDD-00036.md) Freestanding Microcontroller Target (Raspberry Pi Pico) | Not Started | Deliberately low priority; minimal-core scope only (no networking/storage/peripheral parity) |

---

## Roadmap

Grouped by kind of work rather than a fixed sequence number, since priorities shift and bug fixes get picked up opportunistically rather than in strict order. Core-language feature gaps already have their own complexity breakdown in [What Is NOT Implemented](#what-is-not-implemented) above — not repeated here.

### Next up — bugs found but not yet fixed

Pulled from each page's own Known Limitations sections: the ones worth fixing outright, as opposed to the ones documented as deliberate, permanent scope narrowings (e.g. `any`'s boolean-printing convention, see [TYPE-SYSTEM.md](TYPE-SYSTEM.md)).

None currently tracked.

Memory management, the event loop, and the HTTP server were this project's three biggest cross-cutting structural gaps; all three are now substantially closed — see their entries under [Design Documents](#design-documents-tdds) below ([TDD-00001](../tdd/TDD-00001.md), [TDD-00006](../tdd/TDD-00006.md), [TDD-00004](../tdd/TDD-00004.md)) for the full history. The one piece of the three still genuinely open is `-mm=auto` (compiler-inserted frees, no runtime collector) — see the [Cross-Cutting](#cross-cutting) table above.

### Later — a differentiator feature, deliberately deprioritized

**IndexedDB-compatible storage API** (see [TDD-00011](../tdd/TDD-00011.md)) — not started, and deliberately scoped to be picked up only after most of the rest of this roadmap is further along. The idea: expose the real `indexedDB` global/`IDBDatabase`/`IDBObjectStore` API shape (not a bespoke KV API, and not a SQL surface) so hand-written app code using that idiom — and, longer-term, existing npm `IndexedDB` client packages like Dexie.js/localForage, though that specifically also needs `class` support ([TDD-00009](../tdd/TDD-00009.md)) first — has somewhere to run. Four backend directions compared (lowest to highest effort/risk): a hand-rolled RESP client proxying to an external Redis (no new dependency at all — just one missing socket primitive, outbound `connect()`); an embedded SQLite (same C-linking pattern `fetch`/libcurl already uses); a from-scratch native storage engine (zero dependency, matching this project's usual ethos, but real crash-safety engineering); or embedding a mature pure-Go engine (BBolt recommended over BadgerDB/Pebble/BuntDB/go-memdb) via a `cgo`-built static archive linked into the compiled output — gated on a direct prototype confirming the Go runtime's own background threading/signal handling coexists safely with this compiler's fiber scheduler, not yet verified either way.

### Web Platform & Node.js APIs backlog

Not-yet-implemented items from the [Web Platform APIs](#web-platform-apis) and [Node.js APIs](#nodejs-apis) sections above, grouped by effort. Within a tier, the same tiebreaker applies — prefer whichever unlocks REST API interaction / file I/O / process interaction.

The event loop existing now ([TDD-00006](../tdd/TDD-00006.md)) changes the shape of this backlog: several items below used to be tiered partly by "needs the event loop to exist first," which is no longer a real blocker for any of them. Tiers are re-evaluated against what actually remains, not against that now-satisfied prerequisite.

**Medium effort (new dependency or subsystem):**
- `CompressionStream` / `DecompressionStream` — link `zlib`. See [STREAMS.md](STREAMS.md).
- `EventTarget` / `Event` / `CustomEvent` — generic event bus; prerequisite for a general-purpose `AbortController` and others. See [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md).
- `AbortController` / `AbortSignal` — a *fetch-specific* cancellation token is now lower effort than the general version implies: the multi-interface machinery [ADR-00050](../adr/ADR-00050.md) built already tracks each in-flight transfer via its own easy handle, and `curl_multi_remove_handle` + `curl_easy_cleanup` is a real, already-available way to cancel one mid-transfer. A general, `EventTarget`-based signal usable by other consumers (timers, streams) is still gated on `EventTarget` existing first.

**High effort (needs a concurrency model beyond the event loop's single-fiber cooperative scheduling, or a new external dependency):**
- `Worker` (Web Workers) — threads via `pthreads`; requires `SharedArrayBuffer` + `Atomics` too. The shipped event loop is cooperative, one-fiber-at-a-time concurrency ([TDD-00006](../tdd/TDD-00006.md)), not preemptive multi-threading — a genuinely separate mechanism, not an extension of it. See [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md).
- `crypto.subtle` (digest, encrypt, sign) — delegate to OpenSSL or Apple CommonCrypto. See [WEB-CRYPTO.md](WEB-CRYPTO.md).
- `ReadableStream` / `WritableStream` / `TransformStream` — full streaming pipeline; complex backpressure model. See [STREAMS.md](STREAMS.md).

---

*Last updated: 2026-08-09 — True per-file module scope (mangled internal names) and import aliasing — [TDD-00041](../tdd/TDD-00041.md)/[ADR-00134](../adr/ADR-00134.md).*
