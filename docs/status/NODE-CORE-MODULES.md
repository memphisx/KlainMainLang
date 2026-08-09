# Other Node.js Core Modules

> Part of the [Implementation Status](README.md) index. Smaller or lower-priority Node built-in modules, grouped here rather than given a full page each. Split out to its own page (rather than [PROCESS-CLI.md](PROCESS-CLI.md)) once it's substantial enough to need one.

**Coverage**: 2/11. `querystring` and `assert` are implemented (see [ADR-00139](../adr/ADR-00139.md), [ADR-00140](../adr/ADR-00140.md)); everything else below has zero references anywhere in `codegen/llvm/`.

**Caveats**: `net`/`dgram`/`tls`/`dns` (raw sockets) are real prerequisites for the already-tracked `WebSocket` gap (see [NETWORKING.md](NETWORKING.md)) if that's ever built without going through `http.listen`'s existing accept loop. `util` is the remaining CLI-relevant gap in this group (promisifying callback APIs, `.inspect`-style generic value formatting). `vm` has an opt-in embedded-engine path scoped in [TDD-00046](../tdd/TDD-00046.md) (deliberately low priority, not started); `cluster`/`http2`/`zlib` are listed for completeness, not because any are close to being picked up.

| Module | Status | Notes |
|---|---|---|
| `querystring` (`.parse`, `.stringify`) | ✅ | A `Map<string,string>` in, out — the same shape `req.query`/`URLSearchParams` already use. `.parse` doesn't strip a leading `?` (matching real Node; distinct from `new URLSearchParams(str)`, which does) |
| `assert` (`.ok`, bare `assert(cond, msg?)`, `.equal`/`.strictEqual`, `.notEqual`/`.notStrictEqual`, `.fail`, `.throws`) | ✅ | Distinct from `console.assert` (see [CONSOLE.md](CONSOLE.md)) — throws a catchable `AssertionError` (kind-tagged as a base `Error`, since this compiler's fixed error-kind enum has no dedicated slot) instead of logging and continuing. `equal`/`strictEqual` (and their `not*` counterparts) are aliases of each other — this compiler's `==` has no implicit coercion to distinguish them. No `.deepEqual`/`.deepStrictEqual` (no generic recursive object-equality helper exists yet); `.throws` only checks that *something* was thrown, not the thrown value's type/message |
| `util` (`.promisify`, `.inspect`, `.format`) | ❌ | `.promisify` in particular would be near-meaningless until this compiler has real callback-based async APIs to wrap — today's async surface is `Promise`-native already (`fetch`, `async`/`await`) |
| `net` (`net.connect`, `net.Server`, raw TCP sockets) | ❌ | Would need its own accept-loop integration with the existing `select()`-based event loop ([TDD-00006](../tdd/TDD-00006.md)) — a plausible base for a future `WebSocket` (see [NETWORKING.md](NETWORKING.md)) rather than reusing `http.listen`'s HTTP-specific accept loop |
| `dgram` (UDP sockets) | ❌ | Not started |
| `tls` (TLS-wrapped sockets) | ❌ | `fetch`/`http.listen` handle HTTPS/TLS via libcurl internally already; a standalone `tls` module for arbitrary TLS sockets is separate, unstarted work |
| `dns` (`dns.lookup`, `dns.resolve`, ...) | ❌ | `fetch`/libcurl already resolve hostnames internally; no standalone DNS API is exposed to user code |
| `zlib` (as a Node module — `.gzip`/`.gunzip`/`.deflate` callback/sync APIs) | ❌ | Distinct from the WHATWG `CompressionStream`/`DecompressionStream` mention in [STREAMS.md](STREAMS.md) — both would link the same underlying `zlib` C library but expose different call shapes |
| `vm` (`vm.Script`, sandboxed `eval`-like execution) | ❌ | Same fundamental blocker as the already-tracked bare `eval()` — see [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md). Opt-in embedded-engine path scoped in [TDD-00046](../tdd/TDD-00046.md), not started |
| `cluster` (multi-process worker pool sharing a listen socket) | ❌ | No process-forking/threading model exists yet — see [CONCURRENCY-WORKERS.md](CONCURRENCY-WORKERS.md) |
| `http2` | ❌ | `http.listen`/`fetch` are HTTP/1.1 only |
