# Timers

> Part of the [Implementation Status](README.md) index. WHATWG/browser-standard timer APIs.

**Coverage**: 75% (3/4).

**Strict Coverage**: 1/3, ~33% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number; no false ✅ claims found on this page — every excluded row here (including `setTimeout`/`clearTimeout`'s closure-only-callback restriction) was already honestly caveated before the audit.

**Caveats**: `queueMicrotask` isn't implemented yet — a real microtask queue (JS's own, distinct from the timer queue) is a bigger, separate piece of design than `setImmediate` turned out to be. See [TDD-00002](../tdd/TDD-00002.md) for the full timer design (why timers needed only a sleep-until-next-due loop, not the full general-purpose event loop).

| API | Status | Notes |
|---|---|---|
| `setTimeout(fn, ms)` / `clearTimeout(id)` | ✅ | Bare global functions, matching real JS (not a namespace). Callback must be a zero-argument, `void`-returning closure — a bare reference to a top-level named function isn't supported as a value yet, a pre-existing general limitation, not specific to timers. See [ADR-00031](../adr/ADR-00031.md). |
| `setInterval(fn, ms)` / `clearInterval(id)` | ✅ | Same scope as `setTimeout`. An active interval that's never cleared keeps the process running indefinitely, matching real Node — the first feature in this compiler where that's true. See [ADR-00031](../adr/ADR-00031.md). |
| `setImmediate(fn)` / `clearImmediate(id)` | ✅ | Reuses the exact same timer queue as delay-0 `setTimeout` (`clearImmediate` is `clearTimeout` under another name). Known scope narrowing: real Node guarantees `setImmediate` fires before a same-tick `setTimeout(fn, 0)` when scheduled from inside an I/O callback, because its event loop has distinct phases (check vs. timers) — this compiler's `__kml_timer_drain` is a single flat fire-time-ordered queue with no phase concept, so the two are genuinely indistinguishable here (both fire at "now"). See [ADR-00092](../adr/ADR-00092.md). |
| `queueMicrotask(fn)` | ❌ | Microtask queue (also a JS language global) |
