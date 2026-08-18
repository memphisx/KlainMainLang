# Concurrency (Workers)

> Part of the [Implementation Status](README.md) index. Requires spawning threads or processes and sharing memory.

**Coverage**: 0/3 (0%) · **Strict Coverage**: 0/3 (0%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `Worker` (Web Workers API) | ❌ | | • Run code on a background thread<br>• The shipped event loop ([TDD-00006](../tdd/TDD-00006.md)) is cooperative, one-fiber-at-a-time concurrency, not preemptive multi-threading — `Worker` needs a separate mechanism (`pthreads`), plus `SharedArrayBuffer`/`Atomics`<br>• Scoped, not started — see [TDD-00047](../tdd/TDD-00047.md), which found process-wide singleton state (exception jump-buffer globals, GC stack-bottom tracking) that must be fixed before real threads are safe, beyond the `pthreads` dependency itself |
| `BroadcastChannel` | ❌ | | • Pub/sub across workers |
| `MessageChannel` / `MessagePort` | ❌ | | • Bidirectional channel between contexts |
