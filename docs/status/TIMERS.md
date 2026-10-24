<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/timers.json; edit the JSON, then run `make status`. -->

# Timers

> Part of the [Implementation Status](README.md) index. WHATWG/browser-standard timer APIs.

**Coverage**: 5/5 (100%) · **Strict Coverage**: 5/5 (100%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `setTimeout(fn, ms)` / `clearTimeout(id)` | ✅ | | • The handle is Node's `Timeout` object (its `lib/internal/timers.js` ported to TypeScript, [ADR-01411](../adr/01/ADR-01411.md)): `ref`/`unref`/`hasRef`/`refresh`/`close`, `+timeout` as its id, which `clearTimeout` also takes<br>• Bare global functions, matching real JS (not a namespace)<br>• The delay is clamped to `[1, 2^31-1]` ms as Node does — an omitted, `0`, negative or oversized delay is 1 ms, so a chain of zero-delay timers advances one millisecond per link (`setInterval` too; `setImmediate` is not clamped) ([ADR-01071](../adr/01/ADR-01071.md))<br>• See [ADR-00031](../adr/00/ADR-00031.md) |
| `setInterval(fn, ms)` / `clearInterval(id)` | ✅ | | • Same scope as `setTimeout`<br>• An active interval that's never cleared keeps the process running indefinitely, matching real Node<br>• See [ADR-00031](../adr/00/ADR-00031.md) |
| `setImmediate(fn)` / `clearImmediate(id)` | ✅ | | • Runs in the loop's check phase, after I/O polling, as in Node: scheduled from an I/O callback it runs before a 0 ms `setTimeout`; the handle is Node's `Immediate` object ([ADR-01411](../adr/01/ADR-01411.md))<br>• See [ADR-00092](../adr/00/ADR-00092.md) |
| `queueMicrotask(fn)` | ✅ | | • A real microtask FIFO distinct from the timer queue, drained after the synchronous script and before timers ([TDD-00083](../tdd/TDD-00083.md) Stage 3) |
| `timers/promises` (`setTimeout`/`setImmediate`/`setInterval`/`scheduler`) | ✅ | | • Node's lib/timers/promises.js in TypeScript: promise timers that resolve with their value, `setInterval` as an async iterator, an `AbortSignal` rejecting with `AbortError`, `ref: false`, and Node's option validation ([ADR-01271](../adr/01/ADR-01271.md)) |
