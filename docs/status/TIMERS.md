# Timers

> Part of the [Implementation Status](README.md) index. WHATWG/browser-standard timer APIs.

**Coverage**: 50% (2/4).

**Caveats**: `setImmediate`/`clearImmediate` and `queueMicrotask` aren't implemented yet, though their stated prerequisite (the timer-queue mechanism) already shipped as part of the unified event loop — both are now small, unblocked follow-ons. See [TDD-00002](../tdd/TDD-00002.md) for the full timer design (why timers needed only a sleep-until-next-due loop, not the full general-purpose event loop).

| API | Status | Notes |
|---|---|---|
| `setTimeout(fn, ms)` / `clearTimeout(id)` | ✅ | Bare global functions, matching real JS (not a namespace). Callback must be a zero-argument, `void`-returning closure — a bare reference to a top-level named function isn't supported as a value yet, a pre-existing general limitation, not specific to timers. See [ADR-00031](../adr/ADR-00031.md). |
| `setInterval(fn, ms)` / `clearInterval(id)` | ✅ | Same scope as `setTimeout`. An active interval that's never cleared keeps the process running indefinitely, matching real Node — the first feature in this compiler where that's true. See [ADR-00031](../adr/ADR-00031.md). |
| `setImmediate(fn)` / `clearImmediate(id)` | ❌ | Next-tick (Node.js extension) — a natural, separable follow-on now that the core timer-queue mechanism exists |
| `queueMicrotask(fn)` | ❌ | Microtask queue (also a JS language global) |
