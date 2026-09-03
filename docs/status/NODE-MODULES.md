<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/node-modules.json; edit the JSON, then run `make status`. -->

# Node.js built-in modules (completeness index)

> Part of the [Implementation Status](README.md) index. A **completeness map of every Node.js built-in module** — so it's unambiguous which modules are supported, which work only as ambient globals, which are not started, and which are out of scope by design. This page is **informational**: its rows are *not* counted toward the coverage percentages (an out-of-scope or deprecated Node module isn't a missing feature). Modules move onto the counted pages as they're built. For the per-API detail of an implemented module, follow its link to the detailed page; for the *conformance* ranking of the not-yet-built modules (files each one blocks), see [NODE-GAP-ANALYSIS](../testing/NODE-GAP-ANALYSIS.md).

Format: [Status page format](README.md#status-page-format). ✅ = the module works; ❌ = not available today. The section groupings carry the real nuance (importable vs. global-only vs. not-started vs. out-of-scope).

## Implemented

| Module | Status | Notes |
|---|---|---|
| `fs` | ✅ | • → [File system](FILE-SYSTEM.md) |
| `fs/promises` | ✅ | • → [File system](FILE-SYSTEM.md) |
| `path` | ✅ | • → [path](PATH.md) |
| `os` | ✅ | • → [os](OS.md) |
| `process` | ✅ | • Ambient global (no import needed) → [Process & CLI](PROCESS-CLI.md) |
| `child_process` | ✅ | • → [Process & CLI](PROCESS-CLI.md) |
| `readline` | ✅ | • → [Process & CLI](PROCESS-CLI.md) |
| `console` | ✅ | • Ambient global (no import needed) → [console](CONSOLE.md) |
| `assert` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `querystring` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `util` | ✅ | • `.inspect`/`.format` → [Other Node core modules](NODE-CORE-MODULES.md) |
| `net` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `dgram` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `dns` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `tls` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `zlib` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `http` | ✅ | • → [HTTP server](HTTP-SERVER.md) + client in [core modules](NODE-CORE-MODULES.md) |
| `https` | ✅ | • Client + `createServer` → [Other Node core modules](NODE-CORE-MODULES.md) |
| `http2` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `cluster` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `diagnostics_channel` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
| `crypto` / `node:crypto` | ✅ | • WebCrypto global + node crypto hashes/HMAC/keygen → [Web Crypto](WEB-CRYPTO.md) |
| `worker_threads` | ✅ | • → [Concurrency & workers](CONCURRENCY-WORKERS.md) |
| `events` (`EventEmitter`) | ✅ | • `EventEmitter` is an ambient global; `import EventEmitter from 'events'` / `{ EventEmitter }` work, and the static helpers **`events.once`** ([ADR-00675](../adr/ADR-00675.md)) and **`events.on`** (async iterator, [ADR-00677](../adr/ADR-00677.md)) work too → [EventEmitter](EVENT-EMITTER.md) |
| `stream` | ✅ | • → [Streams](STREAMS.md) |
| `stream/promises` | ✅ | • → [Streams](STREAMS.md) |
| `stream/web` | ✅ | • → [Streams](STREAMS.md) |
| `node:sqlite` | ✅ | • → [node:sqlite](SQLITE.md) |
| `test` / `node:test` | ✅ | • Runner + native helpers → [Other Node core modules](NODE-CORE-MODULES.md) |
| `async_hooks` | ✅ | • `AsyncLocalStorage` + `AsyncResource` → [Other Node core modules](NODE-CORE-MODULES.md) |

## Web-global-backed — primary exports fully importable, module extras pending

The primary export of each is a spec-identical re-export of an ambient global, and as of [TDD-00165](../tdd/TDD-00165.md) (Stages 1–3, [ADR-00666](../adr/ADR-00666.md)–[ADR-00668](../adr/ADR-00668.md)) it is **fully importable in every common form** — same-name (`import { URL } from 'url'`), `node:` (`import { setTimeout } from 'node:timers'`), the `events` default (`import EventEmitter from 'events'`), and **aliased** (`import { URL as U } from 'url'`, `{ setTimeout as later }`, `{ Buffer as B }`) — validated and either erased to the global or renamed/rebuilt onto it (using the global directly still works too). What remains is **Stage 4**: the genuinely module-only *extras* with no same-named global — legacy `url.parse`/`format`/`fileURLToPath`, `perf_hooks.PerformanceObserver`, `timers/promises` — which are separate not-yet-built feature surfaces. `Buffer` is a Node-*specific* global (not a Web API).

| Module | Status | Caveats |
|---|---|---|
| `buffer` (`Buffer`) | ✅ | • A Node-specific global; `import { Buffer } from 'buffer'`/`'node:buffer'` works, same-name and aliased (`Buffer.from` member call → Stage 2 rename). `Blob`/`atob`/`btoa` importable too → [Binary data & typed arrays](BINARY-DATA-TYPED-ARRAYS.md) |
| `timers` | ✅ | • `import { setTimeout }`/`setInterval`/`setImmediate`/`clear*` from 'timers'/'node:timers' works, same-name and aliased; the Web globals also work without import → [Timers](TIMERS.md). Pending: `timers/promises` |
| `url` | ✅ | • `import { URL, URLSearchParams } from 'url'` works (same-name + aliased); the **legacy `url`-module functions are complete** — `parse`/`format`/`fileURLToPath`/`pathToFileURL`/`resolve`/`urlToHttpOptions`/`domainToASCII`/`domainToUnicode` ([ADR-00669](../adr/ADR-00669.md)–[ADR-00672](../adr/ADR-00672.md)) → [URL](URL.md). (IDN conversion is libcurl-backend-gated; lenient relative parsing deferred) |
| `perf_hooks` | ✅ | • `import { performance } from 'perf_hooks'` works, same-name and aliased; **`PerformanceObserver` now works too** (synchronous V1 — [ADR-00673](../adr/ADR-00673.md)) → [Performance & Timing](PERFORMANCE-TIMING.md). Pending: `monitorEventLoopDelay`/`createHistogram` and async-batched observer delivery |

## Not started (in scope)

Modules that fit the compiler's model and would add value — ranked and pulled onto the counted pages by importance in subsequent audits.

| Module | Status | Notes |
|---|---|---|
| `string_decoder` | ❌ | • Incremental UTF-8 decoder — no global equivalent; useful for streaming byte→text. Not started |
| `util/types` | ❌ | • Runtime type predicates (`isDate`, `isMap`, …) — not started |
| `assert/strict` | ❌ | • The strict-mode `assert` entrypoint (`assert` itself ships; this alias is a thin follow-on) |
| `dns/promises` | ❌ | • Promise form of `dns` (the callback/`dns.promises` surface ships) — the dedicated subpath specifier is not wired |
| `readline/promises` | ❌ | • Promise form of `readline` — not started |
| `timers/promises` | ❌ | • `setTimeout`/`setInterval` as awaitables — not started |
| `stream/consumers` | ❌ | • `text`/`json`/`buffer`/`arrayBuffer` stream collectors — not started |
| `node:tty` | ❌ | • The `tty` module surface (`tty.isatty`, `ReadStream`/`WriteStream`); the primitives exist as `process.stdin.isTTY`/`setRawMode`/`columns` and the bespoke [`klain:tty`](../guides) reads, but the Node `tty` module is not exposed |
| `constants` | ❌ | • Legacy aggregate of `os`/`fs`/`crypto` constants (superseded by per-module `.constants`) — not started |
| `test/reporters` | ❌ | • Pluggable test reporters (`spec`/`tap`/`dot`) — the runner ships; reporter modules are not started |
| `node:ffi` | ❌ | • Foreign function interface (Node v26.1.0, experimental) — designed in [TDD-00164](../tdd/TDD-00164.md); not started |
| `vm` | ❌ | • Sandboxed `eval`-like execution — the **largest** unimplemented-module gap (~77 conformance files, [NODE-GAP-ANALYSIS](../testing/NODE-GAP-ANALYSIS.md)); gated on an opt-in embedded JS engine (no runtime evaluator today) |
| `sea` (single executable apps) | ❌ | • `klainmain` already emits a standalone native binary, so the *outcome* is native; the Node SEA blob/asset API shape is not implemented |
| `domain` | ❌ | • Deprecated in Node (superseded by `AsyncLocalStorage`) — ~35 conformance files reference it, but it is the lowest-priority module gap ([NODE-GAP-ANALYSIS](../testing/NODE-GAP-ANALYSIS.md)) |

## Out of scope (by the whole-program AOT / no-runtime model)

These depend on a live JavaScript engine, a runtime module loader, or V8 internals that native ahead-of-time output has no equivalent for — or are deprecated in Node itself. Listed for completeness, not planned.

| Module | Status | Notes |
|---|---|---|
| `module` | ❌ | • Whole-program AOT compilation — no runtime `require()`/`import()` or module API ([ADR-00022](../adr/ADR-00022.md)) |
| `repl` | ❌ | • No interactive runtime evaluator (needs a live JS engine) |
| `inspector` | ❌ | • No V8 inspector protocol — real Node also skips these files when the inspector is compiled out ([NODE-GAP-ANALYSIS](../testing/NODE-GAP-ANALYSIS.md)) |
| `v8` | ❌ | • No V8 engine — heap statistics/serialize/flags have no meaning in native output (~18 conformance files reference it) |
| `wasi` | ❌ | • No WebAssembly runtime; a WASM *target* is a separate direction, not a hosted `wasi` module |
| `trace_events` | ❌ | • No V8 trace-event subsystem |
| `punycode` | ❌ | • Deprecated in Node (userland module) |
| `sys` | ❌ | • Deprecated alias of `util` |
