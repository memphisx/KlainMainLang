# Streams API

> Part of the [Implementation Status](README.md) index. `ReadableStream`, `WritableStream`, and `TransformStream` are the WHATWG (browser-standard) backbone of pipeline-style data processing. Node has its own, older, differently-shaped `stream` module — see the second table below — that real Node code (including `fs`/`http`/`child_process` internally) actually uses far more than the WHATWG API.

**Coverage**: 0% (0/5 WHATWG + 0/4 Node) — neither surface is started.

**Caveats**: Full backpressure model, not yet scoped in a TDD. Listed under the "High effort" tier of the [Roadmap](README.md#roadmap)'s Web Platform backlog. The Node `stream` module below is a distinct, EventEmitter-based API (`Readable`/`Writable`/`Duplex`/`Transform`, `.pipe()`, `'data'`/`'end'`/`'error'` events) — not a subset or superset of the WHATWG streams above, and wasn't tracked anywhere until now. It also depends on Node's `EventEmitter` (see [EVENT-EMITTER.md](EVENT-EMITTER.md)), itself untracked until now.

## WHATWG Streams (browser-standard)

| API | Notes |
|---|---|
| `ReadableStream` | Pull-based readable data source |
| `WritableStream` | Writable sink |
| `TransformStream` | Duplex transform (e.g. compress, encrypt) |
| `CompressionStream` / `DecompressionStream` | gzip / deflate via `zlib` |
| `Blob.stream()` / `Blob.text()` / `Blob.arrayBuffer()` | Depends on `Blob` + Streams |

## Node `stream` module

| API | Notes |
|---|---|
| `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` | Node's own base classes — `EventEmitter`-based push/pull model (`'data'`/`'end'`/`'error'`/`'finish'` events, `.read()`/`.write()`/`.push()`), not the WHATWG reader/writer-lock model above. `fs.createReadStream`/`createWriteStream` and `http`'s own request/response objects are `stream.Readable`/`Writable` in real Node — this compiler's own [FILE-SYSTEM.md](FILE-SYSTEM.md) and [HTTP-SERVER.md](HTTP-SERVER.md) are both synchronous/buffered instead, so there's currently nothing in this compiler that *would* return a Node stream even if the class existed. |
| `stream.pipeline()` / `.finished()` | Modern Promise-based stream composition helpers, layered on the classes above |
| `stream/promises` | Promise-returning variants of the callback-based stream APIs |
