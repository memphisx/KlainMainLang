# Binary Data & Typed Arrays

> Part of the [Implementation Status](README.md) index. Binary views over a raw `ArrayBuffer` — essential for networking, crypto, and file I/O.

**Coverage**: ~53% (9/17).

**Caveats**: `BigInt64Array`/`BigUint64Array` need `bigint` (0% implemented). `Uint8ClampedArray` is deliberately skipped (real clamp-on-write needs a distinct store path, not just truncation). `DataView`, `Blob`, `SharedArrayBuffer`, `Atomics` aren't implemented. A TypedArray has no `.buffer` property and only whole-buffer views at offset 0 are supported (no 3-argument sub-range constructor). Node's own `Buffer` class — distinct from the WHATWG `ArrayBuffer`/TypedArrays above, and heavily used in real Node code (`fs`, `net`, `crypto` all hand back `Buffer`s) — isn't implemented and wasn't tracked anywhere until now.

| API | Status | Notes |
|---|---|---|
| `ArrayBuffer` | ✅ | `new ArrayBuffer(byteLength)` — a fixed-length, zero-initialized (`calloc`'d) raw byte buffer, plus `.byteLength`. A general expression (works anywhere), unlike the TypedArrays below. See [ADR-00078](../adr/ADR-00078.md)/[TDD-00018](../tdd/TDD-00018.md). |
| `Uint8Array` / `Int8Array` / `Uint16Array` / `Int16Array` / `Uint32Array` / `Int32Array` / `Float32Array` / `Float64Array` | ✅ | Construction restricted to a variable declaration's initializer (matching the existing restriction on `new Array<T>(...)`/`new Map<K,V>()`/`new Set<T>()`), from a size (`new Uint8Array(10)`, own new buffer), an existing `ArrayBuffer` (`new Uint8Array(buf)`, a **view** sharing the same memory — writes through one view are visible through any other view over the same buffer, including a differently-typed one reinterpreting the same bytes), or a `number[]`/array literal/another TypedArray (`new Uint8Array([1,2,3])`, copy-construct). Indexing, `.length`, `.fill`/`.slice`/`.reverse`/`.at`/`.indexOf`/`.includes`/`.map`/`.filter`/`.reduce`/`.forEach`/`.some`/`.every`, for-of, and `.keys()`/`.values()`/`.entries()` all come for free from the same machinery a plain `number[]` already uses; `.set(source, offset?)`, `.subarray(start?, end?)` (a view, not a copy — unlike `.slice()`), and `.byteLength` are TypedArray-specific additions. Non-clamped writes wrap (mod 2^width, e.g. `new Uint8Array([300])[0] === 44`), matching real JS. See [ADR-00078](../adr/ADR-00078.md)/[TDD-00018](../tdd/TDD-00018.md). |
| `BigInt64Array` / `BigUint64Array` | ❌ | Needs `bigint` support (0% implemented) |
| `Uint8ClampedArray` | ❌ | Real clamp-on-write semantics need a distinct store path (not just truncation) — deliberately skipped rather than approximated; see [TDD-00018](../tdd/TDD-00018.md)'s "Deliberately out of scope" |
| `DataView` | ❌ | Arbitrary-endian reads/writes over an `ArrayBuffer` |
| `Blob` | ❌ | Immutable binary data object with MIME type |
| `SharedArrayBuffer` | ❌ | Shared memory between workers; needs worker support first |
| `Atomics` | ❌ | Atomic operations on `SharedArrayBuffer` |
| Node `Buffer` (`Buffer.from`/`.alloc`/`.toString(encoding)`/`.write`/etc.) | ❌ | Node-specific, not a WHATWG API — a `Uint8Array` subclass in real Node with a much larger method surface (string encoding/decoding, concatenation, comparison). Not implemented at all; confirmed zero references anywhere in `codegen/llvm/`. Fixing `fetch`/`fs`'s null-byte-truncation limitation (see [NETWORKING.md](NETWORKING.md)/[FILE-SYSTEM.md](FILE-SYSTEM.md)) would plausibly want to return something `Buffer`-shaped rather than a plain `Uint8Array`, if Node compatibility is a goal — not decided either way. |

A TypedArray has no `.buffer` property (would require every TypedArray, including the "own buffer" construction form, to carry a back-reference to a real `ArrayBuffer` object — a shape change from the plain `{ptr,i64}` array representation this deliberately keeps), and only whole-buffer views at offset 0 are supported (no 3-argument `new XArray(buffer, byteOffset, length)` sub-range form).
