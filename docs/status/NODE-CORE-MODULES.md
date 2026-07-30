# Other Node.js Core Modules

> Part of the [Implementation Status](README.md) index. Smaller or lower-priority Node built-in modules, grouped here rather than given a full page each — none have any implementation or any prior tracking. Split out to its own page (rather than [PROCESS-CLI.md](PROCESS-CLI.md)) once it's substantial enough to need one.

**Coverage**: 0% across the board — none of these are implemented; confirmed zero references anywhere in `codegen/llvm/`.

**Caveats**: `util` and `assert` are the two most likely to matter for the CLI/microservice direction (promisifying callback APIs, and a real assertion library distinct from `console.assert` — see [CONSOLE.md](CONSOLE.md)). `net`/`dgram`/`tls`/`dns` (raw sockets) are real prerequisites for the already-tracked `WebSocket` gap (see [NETWORKING.md](NETWORKING.md)) if that's ever built without going through `http.listen`'s existing accept loop. The rest (`vm`, `cluster`, `http2`, `querystring`) are listed for completeness, not because any are close to being picked up — `querystring` in particular is largely superseded by the already-implemented `URLSearchParams` (see [URL.md](URL.md)).

| Module | Status | Notes |
|---|---|---|
| `util` (`.promisify`, `.inspect`, `.format`) | ❌ | `.promisify` in particular would be near-meaningless until this compiler has real callback-based async APIs to wrap — today's async surface is `Promise`-native already (`fetch`, `async`/`await`) |
| `assert` (`assert.equal`, `.deepEqual`, `.throws`, `.ok`, ...) | ❌ | Distinct from `console.assert` (see [CONSOLE.md](CONSOLE.md)) — Node's real assertion library, typically used in tests/scripts rather than production logic |
| `net` (`net.connect`, `net.Server`, raw TCP sockets) | ❌ | Would need its own accept-loop integration with the existing `select()`-based event loop ([TDD-00006](../tdd/TDD-00006.md)) — a plausible base for a future `WebSocket` (see [NETWORKING.md](NETWORKING.md)) rather than reusing `http.listen`'s HTTP-specific accept loop |
| `dgram` (UDP sockets) | ❌ | Not started |
| `tls` (TLS-wrapped sockets) | ❌ | `fetch`/`http.listen` handle HTTPS/TLS via libcurl internally already; a standalone `tls` module for arbitrary TLS sockets is separate, unstarted work |
| `dns` (`dns.lookup`, `dns.resolve`, ...) | ❌ | `fetch`/libcurl already resolve hostnames internally; no standalone DNS API is exposed to user code |
| `zlib` (as a Node module — `.gzip`/`.gunzip`/`.deflate` callback/sync APIs) | ❌ | Distinct from the WHATWG `CompressionStream`/`DecompressionStream` mention in [STREAMS.md](STREAMS.md) — both would link the same underlying `zlib` C library but expose different call shapes |
| `vm` (`vm.Script`, sandboxed `eval`-like execution) | ❌ | Same fundamental blocker as the already-tracked bare `eval()` — see [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md) — needs a JIT/interpreter this compiler doesn't have |
| `cluster` (multi-process worker pool sharing a listen socket) | ❌ | No process-forking/threading model exists yet — see [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md) |
| `http2` | ❌ | `http.listen`/`fetch` are HTTP/1.1 only |
| `querystring` (legacy `a=b&c=d` parse/stringify) | ❌ | Largely superseded by the already-implemented `URLSearchParams` — see [URL.md](URL.md) |
