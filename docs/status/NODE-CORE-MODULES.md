# Other Node.js Core Modules

> Part of the [Implementation Status](README.md) index. Smaller or lower-priority Node built-in modules, grouped here rather than given a full page each. Split out to its own page (rather than [PROCESS-CLI.md](PROCESS-CLI.md)) once it's substantial enough to need one.

**Coverage**: 3/11 (~27%) · **Strict Coverage**: 1/11 (~9%).

Format: [Status page format](README.md#status-page-format).

| Module | Status | Caveats | Notes |
|---|---|---|---|
| `querystring` (`.parse`, `.stringify`) | ✅ | | • A `Map<string,string>` in, out — the same shape `req.query`/`URLSearchParams` already use<br>• `.parse` doesn't strip a leading `?` (matching real Node; distinct from `new URLSearchParams(str)`, which does) |
| `assert` (`.ok`, bare `assert(cond, msg?)`, `.equal`/`.strictEqual`, `.notEqual`/`.notStrictEqual`, `.fail`, `.throws`) | ✅ | • `equal`/`strictEqual` (and their `not*` counterparts) are aliases of each other — this compiler's `==` has no implicit coercion to distinguish them<br>• No `.deepEqual`/`.deepStrictEqual` (no generic recursive object-equality helper exists yet)<br>• `.throws` only checks that *something* was thrown, not the thrown value's type/message | • Distinct from `console.assert` (see [CONSOLE.md](CONSOLE.md)) — throws a catchable `AssertionError` (kind-tagged as a base `Error`, since this compiler's fixed error-kind enum has no dedicated slot) instead of logging and continuing |
| `util` (`.promisify`, `.inspect`, `.format`) | ❌ | | • The remaining CLI-relevant gap here (promisifying callback APIs, `.inspect`-style generic value formatting)<br>• `.promisify` in particular would be near-meaningless until this compiler has real callback-based async APIs to wrap — today's async surface is `Promise`-native already (`fetch`, `async`/`await`) |
| `net` (`net.connect`, `net.Server`, raw TCP sockets) | ❌ | | • Would need its own accept-loop integration with the existing `select()`-based event loop ([TDD-00006](../tdd/TDD-00006.md)) — a plausible base for a future `WebSocket` (see [NETWORKING.md](NETWORKING.md)) rather than reusing `http.listen`'s HTTP-specific accept loop |
| `dgram` (UDP sockets) | ❌ | | • Not started |
| `tls` (TLS-wrapped sockets) | ❌ | | • `fetch`/`http.listen` handle HTTPS/TLS via libcurl internally already; a standalone `tls` module for arbitrary TLS sockets is separate, unstarted work |
| `dns` (`dns.lookup`, `dns.resolve`, ...) | ❌ | | • `fetch`/libcurl already resolve hostnames internally; no standalone DNS API is exposed to user code |
| `zlib` (`gzip`/`gunzip`, `deflate`/`inflate`, `deflateRaw`/`inflateRaw`, `unzip`, each `*Sync` + `(err, result)` callback) | ✅ | • The callback form fires synchronously, not deferred to the next event-loop tick<br>• No Brotli (`brotliCompress*`) — it would pull in `-lbrotli`, a new system dependency this binding deliberately avoids<br>• No class/stream forms (`createGzip()`, `zlib.Gzip`, …) — the WHATWG `CompressionStream`/`DecompressionStream` cover the streaming case ([STREAMS.md](STREAMS.md))<br>• `{ level }` is the only supported option and must be a compile-time constant | • One-shot calls over the same libz backend as `CompressionStream` ([ADR-00302](../adr/ADR-00302.md)), driven by a shared `@__kml_zlib_oneshot` helper ([ADR-00321](../adr/ADR-00321.md))<br>• Input may be a Buffer/TypedArray, ArrayBuffer, DataView, or string (UTF-8); output is always a Buffer |
| `vm` (`vm.Script`, sandboxed `eval`-like execution) | ❌ | | • Same fundamental blocker as the already-tracked bare `eval()` — see [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md). Opt-in embedded-engine path scoped in [TDD-00046](../tdd/TDD-00046.md), not started |
| `cluster` (multi-process worker pool sharing a listen socket) | ❌ | | • No process-forking/threading model exists yet — see [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md) |
| `http2` | ❌ | | • `http.listen`/`fetch` are HTTP/1.1 only |
