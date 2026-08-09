# Technical Design Documents (TDDs)

This folder tracks scoping/design work done *before* a feature is implemented — the problem, the design options considered, tradeoffs, and prerequisites. `STATUS.md` was growing a "Design Notes"/"Scoping" section directly inline for every not-yet-built feature, which made it harder to scan for actual implementation status; those sections now live here instead, with `STATUS.md` linking to them.

## Numbering

- Files are named `TDD-NNNNN.md`, zero-padded to 5 digits, starting at [TDD-00001](TDD-00001.md).
- Numbers are assigned sequentially and never reused, the same convention `docs/adr/` uses.
- Before creating a new one, check the Index below for the last number used.

## Cross-referencing

Every mention of another TDD or an ADR — in prose, in the `Status` line, in `STATUS.md`, anywhere — is a real markdown link to that file (`[TDD-00006](TDD-00006.md)` from within `docs/tdd/`, `[ADR-00048](../adr/ADR-00048.md)` from outside it), not a bare `TDD-00006` or a backtick-quoted path. See `docs/adr/README.md`'s own Cross-referencing section for the one exception (a document referring to itself).

## Relationship to ADRs

A TDD **does** carry a status field, unlike an ADR: `Not Started | In Progress | Partially Implemented | Implemented | Superseded`, kept current as work actually happens — this is the quick-reference layer for "what's actually done" that `STATUS.md`'s own summary and this folder's Index both draw from. That's the opposite of an ADR, which has no status field at all, because an ADR is only ever written *after* something is finished (see `docs/adr/README.md`) — there's nothing to track.

The **design content itself** (Context, Design, Prerequisites, Open questions) still isn't edited to match what actually shipped, even as the status field moves. Once a TDD's feature is actually implemented (fully or in part):

- Write an ADR documenting what was actually built — this is the existing standing rule for every feature/bugfix, unchanged.
- Cross-reference the TDD from the ADR's `Relations` field (`Implements TDD-NNNNN`), and update the TDD's own `Status` line to point back at the ADR.
- If the real implementation diverged from the original design, that divergence belongs in the ADR ("here's what was planned, here's what was actually built and why"), not retrofitted into the TDD's Design section — the TDD stays the honest historical record of the thinking at the time. Only the `Status` line (and, for a genuinely abandoned/replaced design, a note that it was superseded) is ever touched after the fact.

The Index below keeps each of these as its own column rather than folding them together: **Status** is the bare status word only (matching the set above); **Related ADRs** lists every ADR that implements/touches this TDD, each a real link; **Notes** is for anything else worth a scanner seeing at a glance (a real caveat, an in-progress split like [TDD-00010](TDD-00010.md)'s V1/V2, an unverified platform claim) — prose, not another status value. Keeping these separate is what keeps the table scannable; when adding or updating a row, put each piece of information in its own column rather than appending it to Status as trailing text.

## Format

Copy [`TEMPLATE.md`](TEMPLATE.md) as a starting point. At minimum, a TDD should cover:

- **Context** — the problem or need, and why it's being scoped now.
- **Design** — the approach(es) considered, in enough detail that implementation could start directly from it. Lay out tradeoffs if multiple options were weighed; note a recommended direction if one exists, and why.
- **Prerequisites** — what's already built and reusable vs. what's still missing, so the work can be picked off incrementally rather than discovered mid-implementation.

## Index

| # | Title | Status | Related ADRs | Notes |
|---|---|---|---|---|
| [00001](TDD-00001.md) | Memory Management: three mutually-exclusive compilation modes (`manual`/`gc`/`auto`) | Partially Implemented | [ADR-00030](../adr/ADR-00030.md), [ADR-00071](../adr/ADR-00071.md) | |
| [00002](TDD-00002.md) | Timers (setTimeout/setInterval) | Implemented | [ADR-00031](../adr/ADR-00031.md) | |
| [00003](TDD-00003.md) | Alternative fetch Backend: a Go helper instead of libcurl | Not Started | | |
| [00004](TDD-00004.md) | HTTP Server | Implemented | [ADR-00048](../adr/ADR-00048.md), [ADR-00049](../adr/ADR-00049.md), [ADR-00072](../adr/ADR-00072.md) | |
| [00005](TDD-00005.md) | Unannotated parameter typing | Partially Implemented | [ADR-00042](../adr/ADR-00042.md) | |
| [00006](TDD-00006.md) | Event Loop | Implemented | [ADR-00048](../adr/ADR-00048.md), [ADR-00049](../adr/ADR-00049.md), [ADR-00050](../adr/ADR-00050.md), [ADR-00051](../adr/ADR-00051.md), [ADR-00052](../adr/ADR-00052.md) | |
| [00007](TDD-00007.md) | Coerce object literal fields against their declared type | Implemented | [ADR-00077](../adr/ADR-00077.md) | |
| [00008](TDD-00008.md) | External conformance suites (TypeScript + Test262) as a test-coverage benchmark | Partially Implemented | [ADR-00047](../adr/ADR-00047.md) | |
| [00009](TDD-00009.md) | Classes / OOP (methods, constructors, inheritance) | Implemented | [ADR-00062](../adr/ADR-00062.md), [ADR-00063](../adr/ADR-00063.md), [ADR-00064](../adr/ADR-00064.md), [ADR-00067](../adr/ADR-00067.md), [ADR-00083](../adr/ADR-00083.md), [ADR-00084](../adr/ADR-00084.md) | |
| [00010](TDD-00010.md) | Generics on user-defined functions and interfaces | Implemented | [ADR-00103](../adr/ADR-00103.md), [ADR-00121](../adr/ADR-00121.md) | V1 (default: monomorphized, functions/interfaces/classes) vs. V2 (`@erased` opt-in, functions only, bare `T` positions only) |
| [00011](TDD-00011.md) | IndexedDB-Compatible Storage API (pluggable embedded/proxy backends) | Not Started | | |
| [00012](TDD-00012.md) | Computed property keys (`{ [expr]: value }`) | Implemented | [ADR-00066](../adr/ADR-00066.md) | |
| [00013](TDD-00013.md) | Error subtypes / tagged errors | Implemented | [ADR-00082](../adr/ADR-00082.md) | |
| [00014](TDD-00014.md) | Full-pipeline (codegen-through-binary) fuzz testing | Implemented | [ADR-00070](../adr/ADR-00070.md) | |
| [00015](TDD-00015.md) | `JSON.parse` into nested object fields | Not Started | | |
| [00016](TDD-00016.md) | `Promise.all` / `.race` / `.allSettled` | Implemented | [ADR-00073](../adr/ADR-00073.md) | |
| [00017](TDD-00017.md) | `fetch()` client parity — custom method, headers, request body | Implemented | [ADR-00074](../adr/ADR-00074.md) | |
| [00018](TDD-00018.md) | `ArrayBuffer` / TypedArrays | Implemented | [ADR-00078](../adr/ADR-00078.md) | |
| [00019](TDD-00019.md) | POSIX signal handling (`process.on('SIGINT'/'SIGTERM', handler)`) | Implemented | [ADR-00079](../adr/ADR-00079.md) | |
| [00020](TDD-00020.md) | Windows support | Not Started | | |
| [00021](TDD-00021.md) | `#x` real private fields | Not Started | | |
| [00022](TDD-00022.md) | Best-effort vanilla JavaScript compatibility (opt-in) | Not Started | | |
| [00023](TDD-00023.md) | `EventEmitter<T>` (`events` module) | Implemented | [ADR-00089](../adr/ADR-00089.md) | |
| [00024](TDD-00024.md) | `os` module | Implemented | [ADR-00090](../adr/ADR-00090.md) | Darwin paths unverified |
| [00025](TDD-00025.md) | Multi-process clustering for `http.listen()` | Implemented | [ADR-00097](../adr/ADR-00097.md), [ADR-00098](../adr/ADR-00098.md), [ADR-00099](../adr/ADR-00099.md), [ADR-00101](../adr/ADR-00101.md) | Both `manual` and `-mm=gc` modes production-quality; [ADR-00101](../adr/ADR-00101.md) root-caused and fixed the `-mm=gc` intermittent hang |
| [00026](TDD-00026.md) | Binary-safe `http.listen()` request/response bodies | Implemented | [ADR-00106](../adr/ADR-00106.md) | |
| [00027](TDD-00027.md) | `http.close()` (graceful listener teardown) | Implemented | [ADR-00102](../adr/ADR-00102.md) | |
| [00028](TDD-00028.md) | Array/Map/Set/EventEmitter literals as general expressions (not just var-decl initializers) | Implemented | [ADR-00104](../adr/ADR-00104.md) | |
| [00029](TDD-00029.md) | Array-of-arrays (nested array) storage representation | Implemented | [ADR-00105](../adr/ADR-00105.md), [ADR-00107](../adr/ADR-00107.md) | `.flat()`/`.flatMap()` followed as a direct follow-on |
| [00030](TDD-00030.md) | Getters / setters (`get x() {}` / `set x(v) {}`) on classes | Implemented | [ADR-00110](../adr/ADR-00110.md) | Object-literal getters/setters out of scope |
| [00031](TDD-00031.md) | Terminal UI primitives (raw mode, tty size, key reads) | Not Started | | |
| [00032](TDD-00032.md) | Native library bindings / GUI (placeholder — general FFI is the real prerequisite) | Not Started | | Bootstrapping placeholder, not a committed design; see the TDD's own scope note |
| [00033](TDD-00033.md) | Direct hardware/framebuffer access (placeholder) | Not Started | | Bootstrapping placeholder; Linux-only by nature, a first for this project |
| [00034](TDD-00034.md) | `TextDecoder` non-UTF-8 encoding support (staged) | Not Started | | Low priority throughout; Stage 4 (most WHATWG labels) not expected to ever be picked up |
| [00035](TDD-00035.md) | RegExp support (staged, PCRE2-backed) | Implemented | [ADR-00114](../adr/ADR-00114.md), [ADR-00115](../adr/ADR-00115.md), [ADR-00116](../adr/ADR-00116.md), [ADR-00117](../adr/ADR-00117.md), [ADR-00118](../adr/ADR-00118.md), [ADR-00119](../adr/ADR-00119.md), [ADR-00120](../adr/ADR-00120.md) | All 7 stages (0-6) done — full method surface (construction/literal/fields, `.test()`, `.exec()`, `str.match()`/`str.matchAll()`, `str.replace()`/`str.replaceAll()`, `str.split()`/`str.search()`) plus `--static` linking verified on bare Linux and in a real Docker `scratch` container |
| [00036](TDD-00036.md) | Freestanding microcontroller target (Raspberry Pi Pico) — minimal core | Not Started | | Deliberately low priority; minimal-core scope only, full networking/storage/peripheral parity deferred to later TDDs |
| [00037](TDD-00037.md) | Multiple type parameters for user-defined generics (`<K, V>`) | Not Started | | Extends [TDD-00010](TDD-00010.md)'s single-type-parameter scope; unconstrained only, no explicit call-site type arguments |
| [00038](TDD-00038.md) | `EventSource` (Server-Sent Events, staged) | Implemented | [ADR-00122](../adr/ADR-00122.md), [ADR-00123](../adr/ADR-00123.md), [ADR-00124](../adr/ADR-00124.md), [ADR-00129](../adr/ADR-00129.md) | All 4 stages done: connection plumbing, SSE parsing/`onmessage`, named events/`addEventListener`/`onopen`/`onerror`, CRLF-tolerant boundaries + terminal-failure detection + auto-reconnect |
| [00039](TDD-00039.md) | `WebSocket` (server upgrade + client, staged) | Implemented | [ADR-00125](../adr/ADR-00125.md), [ADR-00126](../adr/ADR-00126.md), [ADR-00127](../adr/ADR-00127.md), [ADR-00128](../adr/ADR-00128.md) | All 4 stages done: SHA-1 + shared frame codec; `http.listen(..., { ws })` handshake + echo; automatic ping/pong + close handshake; client-side `new WebSocket(url)` (`ws://` only, synchronous connect). `wss://`/TLS and message fragmentation deliberately out of scope, see the doc's Open Questions |
| [00040](TDD-00040.md) | Real `Request`/`Headers` classes and `XMLHttpRequest` (staged) | Implemented | [ADR-00130](../adr/ADR-00130.md), [ADR-00131](../adr/ADR-00131.md) | Both stages done. Also renamed `http.listen`'s server-side `Request` annotation to `HttpRequest`, freeing the name for the client-side class — not scoped by the TDD's own naming note, found only during implementation (see ADR-00130) |
