# Streams API

> Part of the [Implementation Status](README.md) index. `ReadableStream`, `WritableStream`, and `TransformStream` are the WHATWG (browser-standard) backbone of pipeline-style data processing. Node has its own, older, differently-shaped `stream` module — see the Node rows below — that real Node code (including `fs`/`http`/`child_process` internally) actually uses far more than the WHATWG API.

**Coverage**: 0/8 (0%) · **Strict Coverage**: 0/8 (0%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

Neither surface is started. WHATWG streams' full backpressure model isn't yet scoped in a TDD; the surface sits in the "High effort" tier of the [Roadmap](README.md#roadmap)'s Web Platform backlog. Node's own `stream` module is a distinct, EventEmitter-based API (`Readable`/`Writable`/`Duplex`/`Transform`, `.pipe()`, `'data'`/`'end'`/`'error'` events) — not a subset or superset of the WHATWG streams — and depends on Node's `EventEmitter` (see [EVENT-EMITTER.md](EVENT-EMITTER.md)).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `ReadableStream` | ❌ | | • Pull-based readable data source |
| `WritableStream` | ❌ | | • Writable sink |
| `TransformStream` | ❌ | | • Duplex transform (e.g. compress, encrypt) |
| `CompressionStream` / `DecompressionStream` | ❌ | | • gzip / deflate via `zlib` |
| `Blob.stream()` / `Blob.text()` / `Blob.arrayBuffer()` | ❌ | | • Depends on `Blob` + Streams |
| `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` | ❌ | | • Node's own base classes — `EventEmitter`-based push/pull model (`'data'`/`'end'`/`'error'`/`'finish'` events, `.read()`/`.write()`/`.push()`), not the WHATWG reader/writer-lock model above<br>• `fs.createReadStream`/`createWriteStream` and `http`'s own request/response objects are `stream.Readable`/`Writable` in real Node — this compiler's own [FILE-SYSTEM.md](FILE-SYSTEM.md) and [HTTP-SERVER.md](HTTP-SERVER.md) are both synchronous/buffered instead, so there's currently nothing in this compiler that *would* return a Node stream even if the class existed |
| `stream.pipeline()` / `.finished()` | ❌ | | • Modern Promise-based stream composition helpers, layered on the classes above |
| `stream/promises` | ❌ | | • Promise-returning variants of the callback-based stream APIs |
