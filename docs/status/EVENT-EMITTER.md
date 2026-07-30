# events (EventEmitter)

> Part of the [Implementation Status](README.md) index. Node's classic `EventEmitter` base class (`require('events')`) — not the same thing as the WHATWG `EventTarget`/`Event`/`CustomEvent` trio tracked in [EVENTS-CANCELLATION.md](EVENTS-CANCELLATION.md). Real Node code uses `EventEmitter` pervasively: `stream.Readable`/`Writable` (see [STREAMS.md](STREAMS.md)'s Node section), `child_process`'s spawned handles, and `net.Server`/sockets all extend it. Not tracked anywhere until now.

**Coverage**: 0% (0/6) — not implemented, confirmed zero references anywhere in `codegen/llvm/`.

**Caveats**: This is a bigger prerequisite than it looks: several other untracked gaps (Node's own `stream` module, `child_process`'s event-based async API, `net`/socket servers) are all built *on top of* `EventEmitter` in real Node, so implementing it is a plausible unlock for several backlog items at once rather than a narrow, self-contained feature. No design work has started.

| API | Status | Notes |
|---|---|---|
| `new EventEmitter()` / extending it via `class X extends EventEmitter` | ❌ | Would additionally need `class` inheritance (`extends`) — currently ❌, see [LANGUAGE-CONSTRUCTS.md](LANGUAGE-CONSTRUCTS.md) — so this is blocked on that unless a non-inheritance composition shape (`emitter: EventEmitter` field) were used instead |
| `.on(event, listener)` / `.once(event, listener)` | ❌ | Register a (possibly one-shot) listener |
| `.emit(event, ...args)` | ❌ | Synchronously invoke all registered listeners for `event` |
| `.off(event, listener)` / `.removeListener(...)` / `.removeAllListeners(...)` | ❌ | Listener removal |
| `.listenerCount(event)` / `.eventNames()` | ❌ | Introspection |
| `EventEmitter.prototype.emit('error', ...)` special-cases (throws if no `'error'` listener) | ❌ | Node's one genuinely special-cased event name |
