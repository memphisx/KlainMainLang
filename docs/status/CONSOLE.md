# console

> Part of the [Implementation Status](README.md) index.

**Coverage**: 11/12 (~92%) · **Strict Coverage**: 8/12 (~67%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `console.log(...)` | ✅ | | • Multiple arguments join with single spaces on one line, a no-arg call prints a bare newline, and `-0` displays as `-0` (Node's util.inspect display; `String(-0)` stays `"0"`) — [ADR-00285](../adr/ADR-00285.md) |
| `console.error(...)` | ✅ | | • Writes to stderr |
| `console.warn(...)` | ✅ | | • Writes to stderr, unprefixed — identical to `console.error`, as real Node ([ADR-00285](../adr/ADR-00285.md)) |
| `console.info(...)` | ✅ | | |
| `console.debug(...)` | ✅ | | |
| `console.trace(...)` | ✅ | • Prints `"Trace: <message>"` and nothing else; real Node's entire point of `.trace()` is the call stack it prints below the message, which this never generates at all | • Same generic print path as `.debug()`/`.info()`, no stack-walking logic<br>• Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
| `console.assert(cond, msg)` | ✅ | | |
| `console.table()` | ❌ | | • Deliberately deferred, not attempted — needs a genuinely new algorithm (dynamic per-column width computation, box-drawing header/index rows over arbitrarily-shaped input), not a quick extension of existing print machinery like the other rows below |
| `console.time()` / `.timeEnd()` | ✅ | • V1 scope: a single global monotonic-time slot, not a per-label map — calling `time()` again overwrites the one running timer regardless of label | • See [ADR-00029](../adr/ADR-00029.md). |
| `console.count()` / `.countReset()` | ✅ | | • Backed by a real `Map<string, number>` — matches real Node's per-label semantics exactly, unlike `time`'s single-slot narrowing above. See [ADR-00029](../adr/ADR-00029.md). |
| `console.group()` / `.groupEnd()` | ✅ | | • Indents every subsequent `console.*` line by two spaces per nesting level; an unbalanced extra `groupEnd()` floors at depth 0 rather than going negative. See [ADR-00029](../adr/ADR-00029.md). |
| `console.dir()` | ✅ | • The real API's second `options` argument — depth/color controls — is accepted syntactically but ignored | • Prints a single value exactly like a single-argument `console.log`. See [ADR-00029](../adr/ADR-00029.md). |
