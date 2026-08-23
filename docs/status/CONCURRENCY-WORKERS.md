# Concurrency (Workers)

> Part of the [Implementation Status](README.md) index. Real OS threads and cross-thread messaging.

**Coverage**: 1/3 (33%) · **Strict Coverage**: 0/3 (0%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `Worker` | ✅ | • One listener per event, arrow-function literals only<br>• One message type per direction, declared by annotation<br>• Payloads: structured-clone-safe values only; strings share their buffer across threads; no arrays through `onmessage`/`e.data`<br>• `'error'` carries the message string, not an Error object; no `self.onerror`<br>• `terminate()` is cooperative, keeps queued messages, always exits 1<br>• Worker path must be a string literal; a worker module can't also be imported, can't spawn workers, and its named functions can't read its own top-level bindings<br>• `-mm=manual` leaks per message; no combining with `http.listen({workers:N})` | • Both surfaces ship: Node `worker_threads` and the browser `Worker`/`onmessage` shape ([TDD-00098](../tdd/TDD-00098.md), [ADR-00305](../adr/ADR-00305.md), [ADR-00306](../adr/ADR-00306.md))<br>• The worker file compiles into the same binary as its own entry function<br>• Each thread runs its own event loop (runtime singletons are `thread_local`); messages travel over pipes as pointer envelopes, deep-copied at the boundary<br>• `-mm=gc` registers each thread with Boehm<br>• Uncaught worker exception → `'error'` + `'exit'(1)` on the parent, or process exit if unhandled<br>• darwin/arm64 verified; the Linux pass (`--static` + `-pthread`, glibc ucontext layout) is pending |
| `BroadcastChannel` | ❌ | | • Pub/sub across workers |
| `MessageChannel` / `MessagePort` | ❌ | | • Bidirectional channel between contexts |
