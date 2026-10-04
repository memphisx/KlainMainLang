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
| `util` | ✅ | • → [Other Node core modules](NODE-CORE-MODULES.md) |
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
| `events` (`EventEmitter`) | ✅ | • Node's `lib/events.js` ported to TypeScript ([TDD-00230](../tdd/TDD-00230.md) P3.1). `EventEmitter` exists only through an import of `events` (named, default or namespace): it is no global, in Node or the web platform (the web's is `EventTarget`). `once`, `on` (async iterator), `getEventListeners`, `getMaxListeners`, `setMaxListeners`, `listenerCount` and `EventEmitter.defaultMaxListeners`/`captureRejections` work too → [EventEmitter](EVENT-EMITTER.md) |
| `stream` | ✅ | • → [Streams](STREAMS.md) |
| `stream/promises` | ✅ | • → [Streams](STREAMS.md) |
| `stream/web` | ✅ | • → [Streams](STREAMS.md) |
| `node:sqlite` | ✅ | • → [node:sqlite](SQLITE.md) |
| `test` / `node:test` | ✅ | • Runner and Node's test-suite helpers, in TypeScript → [Other Node core modules](NODE-CORE-MODULES.md) |
| `async_hooks` | ✅ | • `AsyncLocalStorage` + `AsyncResource` → [Other Node core modules](NODE-CORE-MODULES.md) |

## Web-global-backed — primary exports fully importable, module extras pending

The primary export of each is a spec-identical re-export of an ambient global, and as of [TDD-00165](../tdd/TDD-00165.md) (Stages 1–3, [ADR-00666](../adr/ADR-00666.md)–[ADR-00668](../adr/ADR-00668.md)) it is **fully importable in every common form** — same-name (`import { URL } from 'url'`), `node:` (`import { Buffer } from 'node:buffer'`), and **aliased** (`import { URL as U } from 'url'`, `{ Buffer as B }`) — validated and either erased to the global or renamed/rebuilt onto it (using the global directly still works too). `Buffer` is a Node-*specific* global (not a Web API).

| Module | Status | Caveats | Notes |
|---|---|---|---|
| `buffer` (`Buffer`) | ✅ | • `File` and `resolveObjectURL` are not exported (no `File` global, no object-URL registry) | • A Node-specific global; `import { Buffer } from 'buffer'`/`'node:buffer'` works, same-name and aliased (`Buffer.from` member call → Stage 2 rename). `Blob`/`atob`/`btoa` importable too → [Binary data & typed arrays](BINARY-DATA-TYPED-ARRAYS.md)<br>• The module's own exports are TypeScript (`lib/node/internal_buffer.ts`, [ADR-01281](../adr/ADR-01281.md)): `constants`, `kMaxLength`, `kStringMaxLength`, `INSPECT_MAX_BYTES`, `isUtf8`, `isAscii`, `transcode`, `SlowBuffer`; namespace and default imports reach them and the globals |
| `timers` | ✅ | • The module's timer functions wrap the globals rather than being them: `timers.setTimeout === setTimeout` is `false` (Node: `true`) | • Written in TypeScript (`lib/node/timers.ts`, [ADR-01280](../adr/ADR-01280.md)): named, aliased, default and namespace imports from 'timers'/'node:timers'; extra arguments reach the callback, `clear*` ignore `undefined`/`null`, and `timers.promises` is `timers/promises` → [Timers](TIMERS.md) |
| `url` | ✅ | • Lenient relative parsing deferred | • Node's `url` module in TypeScript (`lib/node/url.ts`): `URL`/`URLSearchParams` (the globals' classes), the legacy `parse`/`format`/`resolve`/`resolveObject`/`Url`, `fileURLToPath`/`pathToFileURL`/`urlToHttpOptions`/`domainToASCII`/`domainToUnicode`; named, namespace and default imports → [URL](URL.md) |
| `perf_hooks` | ✅ | • Histograms keep exact samples (not HDR); `eventLoopUtilization`/`nodeTiming` missing | • Written in TypeScript (`lib/node/perf_hooks.ts`, [ADR-01265](../adr/ADR-01265.md)): `performance`, the entry classes, `PerformanceObserver`, `createHistogram`, `monitorEventLoopDelay` → [Performance & Timing](PERFORMANCE-TIMING.md) |

## Not started (in scope)

Modules that fit the compiler's model and would add value — ranked and pulled onto the counted pages by importance in subsequent audits.

| Module | Status | Notes |
|---|---|---|
| `string_decoder` | ✅ | • Node's `StringDecoder`, ported to TypeScript: `write` holds back a character split across Buffers (UTF-8, UTF-16LE, base64) and `end` flushes it (a partial UTF-8 character as U+FFFD); other encodings decode each Buffer whole |
| `util/types` | ✅ | • The type predicates `util.types` carries (`lib/node/util_types.ts`, [ADR-01289](../adr/ADR-01289.md)); the boxed-primitive, external, proxy and module-namespace ones are always false, as no such value exists here |
| `assert/strict` | ✅ | • `assert.strict`: `equal` is `strictEqual`, and so on (`lib/node/assert_strict.ts`, [ADR-01288](../adr/ADR-01288.md)) |
| `dns/promises` | ✅ | • `dns.promises`'s module: `lookup`, `lookupService`, the `resolve*` family, `reverse`, `Resolver`, `getServers`/`setServers` ([ADR-01294](../adr/ADR-01294.md)) |
| `readline/promises` | ✅ | • `createInterface` and an `Interface` whose `question` returns a promise (with an `AbortSignal`); `Readline` queues cursor moves and clears until `commit()` → [Process & CLI](PROCESS-CLI.md) |
| `timers/promises` | ✅ | • Written in TypeScript (`lib/node/timers_promises.ts`, [ADR-01271](../adr/ADR-01271.md)) → [Timers](TIMERS.md) |
| `stream/consumers` | ❌ | • `text`/`json`/`buffer`/`arrayBuffer` stream collectors — not started |
| `node:tty` | ❌ | • The `tty` module surface (`tty.isatty`, `ReadStream`/`WriteStream`); the primitives exist as `process.stdin.isTTY`/`setRawMode`/`columns` and the bespoke [`klain:tty`](../guides) reads, but the Node `tty` module is not exposed |
| `constants` | ❌ | • Legacy aggregate of `os`/`fs`/`crypto` constants (superseded by per-module `.constants`) — not started |
| `test/reporters` | ❌ | • Pluggable test reporters (`spec`/`tap`/`dot`) — the runner ships; reporter modules are not started |
| `node:ffi` | ✅ | • Foreign function interface (Node v26.1.0, experimental); POSIX only → [FFI.md](FFI.md) ([TDD-00164](../tdd/TDD-00164.md)) |
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
