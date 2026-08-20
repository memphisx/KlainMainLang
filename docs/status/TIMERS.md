# Timers

> Part of the [Implementation Status](README.md) index. WHATWG/browser-standard timer APIs.

**Coverage**: 4/4 (100%) · **Strict Coverage**: 1/4 (~25%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `setTimeout(fn, ms)` / `clearTimeout(id)` | ✅ | • Callback is restricted to a zero-argument, `void`-returning function — an arrow/function-expression closure, or (since [ADR-00200](../adr/ADR-00200.md)) a bare reference to a top-level named function | • Bare global functions, matching real JS (not a namespace)<br>• Since [ADR-00200](../adr/ADR-00200.md) a bare top-level named-function reference is usable as a first-class value here<br>• See [ADR-00031](../adr/ADR-00031.md) |
| `setInterval(fn, ms)` / `clearInterval(id)` | ✅ | | • Same scope as `setTimeout`<br>• An active interval that's never cleared keeps the process running indefinitely, matching real Node — the first feature in this compiler where that's true<br>• See [ADR-00031](../adr/ADR-00031.md) |
| `setImmediate(fn)` / `clearImmediate(id)` | ✅ | • Real Node guarantees `setImmediate` fires before a same-tick `setTimeout(fn, 0)` when scheduled from inside an I/O callback, because its event loop has distinct phases (check vs. timers); this compiler's `__kml_timer_drain` is a single flat fire-time-ordered queue with no phase concept, so the two are genuinely indistinguishable here (both fire at "now") | • Reuses the exact same timer queue as delay-0 `setTimeout` (`clearImmediate` is `clearTimeout` under another name)<br>• See [ADR-00092](../adr/ADR-00092.md) |
| `queueMicrotask(fn)` | ✅ | | • A real microtask FIFO distinct from the timer queue, drained after the synchronous script and before timers ([TDD-00083](../tdd/TDD-00083.md) Stage 3) |
