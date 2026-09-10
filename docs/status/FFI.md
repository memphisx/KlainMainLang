<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/ffi.json; edit the JSON, then run `make status`. -->

# FFI (node:ffi)

> Part of the [Implementation Status](README.md) index. Node's experimental `node:ffi` module (v26.1.0+, `--experimental-ffi`) — call C functions from shared libraries with plain-object signatures, and peek/poke native memory through `bigint` pointers. Import-gated (`import ffi from 'node:ffi'`). Signatures are resolved at compile time and lowered to direct C-ABI calls (no libffi, no trampoline); 64-bit integers and pointers travel as `bigint`, exactly as in Node. `registerCallback` hands a closure to C as a real function pointer via statically-emitted trampoline families. POSIX only for now (`dlopen`/`dlsym`; no Windows `LoadLibrary` shim yet). See [ADR-00797](../adr/ADR-00797.md)/[ADR-00798](../adr/ADR-00798.md)/[ADR-00799](../adr/ADR-00799.md)/[ADR-00808](../adr/ADR-00808.md)/[TDD-00164](../tdd/TDD-00164.md).

**Coverage**: 11/13 (~85%) · **Strict Coverage**: 4/13 (~31%).

Format: [Status page format](README.md#status-page-format).

| API | Status | Caveats | Notes |
|---|---|---|---|
| `ffi.dlopen(path, definitions?)` → `{ lib, functions }` | ✅ | • `definitions` must be an object literal — signatures are resolved at compile time (a runtime-computed signature has no AOT lowering); the `path` may be any runtime string, or `null` for the current process image | |
| `new DynamicLibrary(path)` / `lib.close()` / `ffi.dlclose(lib)` | ✅ | • `using` declarations and `[Symbol.dispose]()` are not supported yet — explicit `.close()` is the disposal path (close is idempotent, as in Node) | |
| `lib.getFunction(name, signature)` / `lib.getFunctions(definitions)` | ✅ | • `signature` must be a compile-time object literal (same reason as `dlopen` definitions); the bound function's `.pointer` property works<br>• Re-resolving the same symbol with a different signature is not diagnosed (Node throws) | |
| `lib.getSymbol(name)` / `ffi.dlsym(lib, name)` → `bigint` | ✅ | | |
| `library.functions` / `library.symbols` / no-arg `getFunctions()` / `getSymbols()` | ✅ | • The accumulator is compile-time: names registered through literal `getFunction`/`getSymbol` calls and `dlopen`/`getFunctions` definitions on that library value, in resolution order (a runtime-string name is resolved but not recorded); addresses are re-`dlsym`'d at the read point | |
| Typed calls + marshalling (all scalar types, `string`, `pointer`, `buffer`, `arraybuffer`, 64-bit ↔ `bigint`) | ✅ | • `int64`/`uint64` parameters also accept a plain `number` (Node requires `bigint`); `bool` marshals as 0/1 numbers, matching Node<br>• `function`-typed values can be passed through as raw `bigint` addresses, but there is no way to create one from a closure until `registerCallback` ships | |
| `ffi.suffix` / `ffi.types` constants | ✅ | | |
| `library.registerCallback([sig,] cb)` / `unregisterCallback(ptr)` / `refCallback` / `unrefCallback` | ✅ | • At most 16 concurrently-live callbacks per signature shape (static per-signature trampoline families — no runtime codegen); exceeding it throws, unregistered slots recycle<br>• 64-bit/pointer parameters must be declared `bigint` (and `string` as `string`) — validated with a clear compile-time error<br>• `refCallback`/`unrefCallback` are validated no-ops (a registered callback is always strongly referenced here), and `library.close()` does not invalidate callbacks — unregister explicitly; Node's same-thread/no-throw/no-promise runtime rules are not enforced | |
| Primitive accessors (`ffi.getInt8`…`getFloat64(ptr, offset?)`, `ffi.setInt8`…`setFloat64(ptr, offset, value)`) | ✅ | | |
| `ffi.toString(ptr)` / `ffi.toBuffer(ptr, len, copy?)` / `ffi.toArrayBuffer(ptr, len, copy?)` / `ffi.getRawPointer(src)` | ✅ | | • `copy: false` wraps the native memory zero-copy (the caller keeps it valid, Node's contract); `toString(0n)` → `null` |
| `ffi.exportString/exportBuffer/exportArrayBuffer/exportArrayBufferView(src, ptr, length)` | ✅ | • `exportString` supports only the `'utf8'` encoding (UTF-8-native strings) and truncates to the capacity — whether Node instead throws on overflow is unverified against a real `--experimental-ffi` build<br>• A too-small `length` on the byte-export forms throws, as in Node | |
| `ffi.getCurrentEventLoop()` | ❌ | | • Returns a `uv_loop_t*` in Node; this runtime has no libuv loop, so a faithful value does not exist — revisit when the Node API stabilizes |
| Windows | ❌ | | • `dlopen`/`dlsym` need a `LoadLibrary`/`GetProcAddress` shim; rejected cleanly on a Windows host |
