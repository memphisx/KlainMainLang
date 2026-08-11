# events (EventEmitter)

> Part of the [Implementation Status](README.md) index. Node's classic `EventEmitter` base class (`require('events')`) — not the same thing as the WHATWG `EventTarget`/`Event`/`CustomEvent` trio tracked in [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md). Real Node code uses `EventEmitter` pervasively: `stream.Readable`/`Writable` (see [STREAMS.md](STREAMS.md)'s Node section), `child_process`'s spawned handles, and `net.Server`/sockets all extend it.

**Coverage**: 100% (6/6) — done, see [TDD-00023](../tdd/TDD-00023.md)/[ADR-00089](../adr/ADR-00089.md).

**Strict Coverage**: 3/6, 50% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number; no false ✅ claims found on this page — every excluded row here was already honestly caveated (single-payload-type, ordering not guaranteed) before the audit.

| API | Status | Notes |
|---|---|---|
| `new EventEmitter<T>()` / extending it via `class X extends EventEmitter<T>` | ✅ | `class` inheritance (ADR-00083) landed after this page was first written, which is what unblocked the `extends` form — `EventEmitter<T>` is a compiler-recognized generic built-in (same monomorphized-per-`T` pattern as `Map<K,V>`/`Set<T>`/`Promise<T>`), never itself a registered class (no vtable slot, no `instanceof`). See LANGUAGE-CONSTRUCTS.md's `class`/`extends` note. |
| `.on(event, listener)` / `.once(event, listener)` | ✅ | Chainable (`.on(...).on(...)`). `listener` is `(data: T) => void` — one concrete payload type per emitter, decided at `new EventEmitter<T>()`/`extends EventEmitter<T>` time (see Known Limitations — real Node's `emit(event, ...args)` is fully variadic, not buildable here) |
| `.emit(event, data)` | ✅ | Synchronously invokes every registered listener for `event`, in registration order; returns whether any listener ran |
| `.off(event, listener)` / `.removeListener(...)` / `.removeAllListeners(...)` | ✅ | `removeAllListeners()` (no arg) clears every event; `removeAllListeners(event)` clears just that one |
| `.listenerCount(event)` / `.eventNames()` | ✅ | `eventNames()` returns registration order (an implementation detail of the underlying map, not a documented ordering guarantee) |
| `EventEmitter.prototype.emit('error', ...)` special-cases (throws if no `'error'` listener) | ✅ | Resolved entirely at the `.emit()` call site — no runtime cost when the event name isn't `'error'` beyond the `strcmp` itself |

## Known Limitations

- **Single payload type, not `...args`.** This compiler has no working `any[]`/call-site array spread (see [README.md](README.md)'s "Newly identified gaps" — `f(...arr)` doesn't parse at all)/heterogeneous rest parameters, so real Node's fully-variadic `emit(event, ...args)` isn't buildable — `EventEmitter<T>` fixes one concrete `T` per emitter instance instead. Covers the common single-payload case (e.g. a future `readline`'s `'line'` event, `EventEmitter<string>`) but not multi-argument emits.
- **No overriding an EventEmitter method.** `on`/`once`/`emit`/`off`/`removeListener`/`removeAllListeners`/`listenerCount`/`eventNames` are hand-written codegen dispatched by name, never real AST-driven class methods or vtable slots — a class in an EventEmitter-rooted tree declaring any of these names is a compile-time error rather than a real override.
- **`instanceof EventEmitter` is not supported** — `EventEmitter` is never registered as a real class and isn't one of the five built-in types with their own dedicated compile-time check (`Array`/`Map`/`Set`/`Date`/`RegExp` — see [ADR-00162](../adr/ADR-00162.md)), so `x instanceof EventEmitter` is a compile error, the same as any other unregistered, non-built-in name.
- **Single inheritance, not composition.** A class can extend `EventEmitter<T>` *or* some other base, never both — real JS/TS has the same single-inheritance constraint, so this isn't a narrowing specific to this compiler.
