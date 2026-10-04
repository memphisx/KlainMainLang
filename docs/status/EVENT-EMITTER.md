<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/event-emitter.json; edit the JSON, then run `make status`. -->

# events (EventEmitter)

> Part of the [Implementation Status](README.md) index. Node's classic `EventEmitter` base class (`require('events')`) — not the same thing as the WHATWG `EventTarget`/`Event`/`CustomEvent` trio tracked in [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md). Real Node code uses `EventEmitter` pervasively: `stream.Readable`/`Writable` (see [STREAMS.md](STREAMS.md)'s Node section), `child_process`'s spawned handles, and `net.Server`/sockets all extend it.

**Coverage**: 9/9 (100%) · **Strict Coverage**: 7/9 (~78%).

Format: [Status page format](README.md#status-page-format).

| API | Status | Caveats | Notes |
|---|---|---|---|
| `new EventEmitter<T>()` / extending it via `class X extends EventEmitter<T>` | ✅ | • An override's declared signature isn't checked for compatibility against the method it replaces (tsc's TS2416)<br>• The checker types every event as taking `...args: any[]`: `lib/node/events.ts` declares its methods that way, not through @types/node's conditional types (`Key`/`Args`/`Listener`), so a typed map's event names and argument types are not checked | • Node's `lib/events.js`, ported to TypeScript ([TDD-00230](../tdd/TDD-00230.md) P3.1): an ordinary class, imported from `events` (named, default or namespace), not a global. With no type argument every event takes any arguments (@types/node's default event map); a type argument is an event map of argument tuples (`EventEmitter<{ data: [chunk: string]; end: [] }>`)<br>• A subclass may override any method and reach the original through `super`; `captureRejections` (the constructor option and the static default) routes an async listener's rejection to `'error'`, as in Node |
| `.on(event, listener)` / `.once(event, listener)` | ✅ | | • Chainable (`.on(...).on(...)`)<br>• A listener may take fewer parameters than the event passes; one it isn't passed is `undefined` or its default, and an async listener's promise is what `captureRejections` watches |
| `.emit(event, ...args)` | ✅ | | • Synchronously invokes every registered listener for `event`, in registration order; returns whether any listener ran; a `once` listener is removed before it runs<br>• `emit(event, a, b, …)` passes each argument; a typed map's event takes exactly its tuple's arguments ([ADR-00392](../adr/ADR-00392.md)) |
| `.off(event, listener)` / `.removeListener(...)` / `.removeAllListeners(...)` | ✅ | | • `off`/`removeListener` removes the most recently added matching listener and keeps the others in order; `removeAllListeners()` (no arg) clears every event; `removeAllListeners(event)` clears just that one |
| `.addListener(...)` / `.prependListener(...)` / `.prependOnceListener(...)` | ✅ | | • `prepend*` puts the listener before the existing ones ([ADR-01162](../adr/ADR-01162.md)) |
| `.listenerCount(event)` / `.eventNames()` | ✅ | • `eventNames()` leaves out symbol event names (`Reflect.ownKeys` omits an object's symbol keys); `on`/`emit`/`listenerCount` with a symbol work | • `eventNames()` returns registration order; an event whose last listener is removed leaves the list, as in Node |
| `EventEmitter.prototype.emit('error', ...)` special-cases (throws if no `'error'` listener) | ✅ | | • An unlistened `'error'` throws its argument when it is an Error, and otherwise an Error reading `Unhandled error. (<value>)` with the value rendered by `util.inspect`, as Node does |
| `events.once(emitter, name)` (static helper) | ✅ | | • Returns a Promise that resolves with the event's argument array the first time it fires (`const [x] = await once(ee, 'x')`); an `'error'` first rejects it, and an `AbortSignal` option cancels it |
| `events.on(emitter, name)` (async iterator) | ✅ | | • An async iterator yielding each emission's argument array (`for await (const [x] of on(ee, 'data'))`), buffering events between iterations; leaving the loop (`break`, `return()`) removes its listeners, as in Node |
