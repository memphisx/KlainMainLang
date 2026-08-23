# events (EventEmitter)

> Part of the [Implementation Status](README.md) index. Node's classic `EventEmitter` base class (`require('events')`) — not the same thing as the WHATWG `EventTarget`/`Event`/`CustomEvent` trio tracked in [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md). Real Node code uses `EventEmitter` pervasively: `stream.Readable`/`Writable` (see [STREAMS.md](STREAMS.md)'s Node section), `child_process`'s spawned handles, and `net.Server`/sockets all extend it.

**Coverage**: 6/6 (100%) · **Strict Coverage**: 3/6 (50%).

Format: [Status page format](README.md#status-page-format).

| API | Status | Caveats | Notes |
|---|---|---|---|
| `new EventEmitter<T>()` / extending it via `class X extends EventEmitter<T>` | ✅ | • No overriding an EventEmitter method — a class in an EventEmitter-rooted tree declaring `on`/`once`/`emit`/`off`/`removeListener`/`removeAllListeners`/`listenerCount`/`eventNames` is a compile-time error (these are hand-written codegen dispatched by name, not real AST-driven methods/vtable slots), not a real override | • `EventEmitter<T>` is a compiler-recognized generic built-in (same monomorphized-per-`T` pattern as `Map<K,V>`/`Set<T>`/`Promise<T>`), never itself a registered class (no vtable slot); `x instanceof EventEmitter` answers as a compile-time constant, true for emitters and classes extending one ([ADR-00303](../adr/ADR-00303.md)). See LANGUAGE-CONSTRUCTS.md's `class`/`extends` note<br>• Single inheritance, not composition: a class can extend `EventEmitter<T>` *or* another base, never both — real JS/TS has the same single-inheritance constraint, so this isn't specific to this compiler |
| `.on(event, listener)` / `.once(event, listener)` | ✅ | • No fully-variadic `(...args)` listeners — a listener takes exactly the event's one payload argument (or none, for a `void` event)<br>• A map-typed emitter requires a string-literal event name at every call site | • Chainable (`.on(...).on(...)`)<br>• Per-event payload typing via an event-map type argument (`EventEmitter<{ data: string; end: void }>`, [ADR-00303](../adr/ADR-00303.md)); a scalar/`Error` type argument keeps the single-payload-for-every-event form |
| `.emit(event, data?)` | ✅ | • At most one payload argument — real Node's multi-argument `emit(event, a, b, ...)` is unsupported (no heterogeneous rest/spread machinery to build it on)<br>• A map-typed emitter requires a string-literal event name | • Synchronously invokes every registered listener for `event`, in registration order; returns whether any listener ran |
| `.off(event, listener)` / `.removeListener(...)` / `.removeAllListeners(...)` | ✅ | | • `removeAllListeners()` (no arg) clears every event; `removeAllListeners(event)` clears just that one |
| `.listenerCount(event)` / `.eventNames()` | ✅ | | • `eventNames()` returns registration order (an implementation detail of the underlying map, not a documented ordering guarantee) |
| `EventEmitter.prototype.emit('error', ...)` special-cases (throws if no `'error'` listener) | ✅ | | • Resolved entirely at the `.emit()` call site — no runtime cost when the event name isn't `'error'` beyond the `strcmp` itself |
