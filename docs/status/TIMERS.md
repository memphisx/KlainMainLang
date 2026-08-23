# Timers

> Part of the [Implementation Status](README.md) index. WHATWG/browser-standard timer APIs.

**Coverage**: 4/4 (100%) · **Strict Coverage**: 2/4 (50%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `setTimeout(fn, ms)` / `clearTimeout(id)` | ✅ | • Callback is restricted to a zero-argument, `void`-returning function — an arrow/function-expression closure, or a bare reference to a top-level named function ([ADR-00200](../adr/ADR-00200.md)) | • Bare global functions, matching real JS (not a namespace)<br>• See [ADR-00031](../adr/ADR-00031.md) |
| `setInterval(fn, ms)` / `clearInterval(id)` | ✅ | | • Same scope as `setTimeout`<br>• An active interval that's never cleared keeps the process running indefinitely, matching real Node<br>• See [ADR-00031](../adr/ADR-00031.md) |
| `setImmediate(fn)` / `clearImmediate(id)` | ✅ | • Real Node guarantees `setImmediate` fires before a same-tick `setTimeout(fn, 0)` when scheduled from inside an I/O callback, because its event loop has distinct phases (check vs. timers); this compiler's `__kml_timer_drain` is a single flat fire-time-ordered queue with no phase concept, so the two are genuinely indistinguishable here (both fire at "now") | • Reuses the exact same timer queue as delay-0 `setTimeout` (`clearImmediate` is `clearTimeout` under another name)<br>• See [ADR-00092](../adr/ADR-00092.md) |
| `queueMicrotask(fn)` | ✅ | | • A real microtask FIFO distinct from the timer queue, drained after the synchronous script and before timers ([TDD-00083](../tdd/TDD-00083.md) Stage 3) |
