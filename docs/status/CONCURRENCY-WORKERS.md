# Concurrency (Workers)

> Part of the [Implementation Status](README.md) index. Requires spawning threads or processes and sharing memory.

**Coverage**: 0% (0/3) — not started.

**Caveats**:

- The shipped event loop ([TDD-00006](../tdd/TDD-00006.md)) is cooperative, one-fiber-at-a-time concurrency, not preemptive multi-threading — `Worker` needs a separate mechanism (`pthreads`), plus `SharedArrayBuffer`/`Atomics`.
- Scoped, not started — see [TDD-00047](../tdd/TDD-00047.md), which found process-wide singleton state (exception jump-buffer globals, GC stack-bottom tracking) that must be fixed before real threads are safe, beyond the `pthreads` dependency itself.

| API | Notes |
|---|---|
| `Worker` (Web Workers API) | Run code on a background thread |
| `BroadcastChannel` | Pub/sub across workers |
| `MessageChannel` / `MessagePort` | Bidirectional channel between contexts |
