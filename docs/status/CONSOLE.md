# console

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~92% (11/12).

**Caveats**: `console.table()` is the one gap — deliberately deferred, needs a genuinely new algorithm (dynamic per-column width computation, box-drawing rows), not a quick extension of existing print machinery.

| Feature | Status |
|---|---|
| `console.log(...)` | ✅ |
| `console.error(...)` | ✅ (stderr) |
| `console.warn(...)` | ✅ (stderr) |
| `console.info(...)` | ✅ |
| `console.debug(...)` | ✅ |
| `console.trace(...)` | ✅ |
| `console.assert(cond, msg)` | ✅ |
| `console.table()` | ❌ (deliberately deferred, not attempted — needs a genuinely new algorithm (dynamic per-column width computation, box-drawing header/index rows over arbitrarily-shaped input), not a quick extension of existing print machinery like the other rows below) |
| `console.time()` / `.timeEnd()` | ✅ (V1 scope: a single global monotonic-time slot, not a per-label map — calling `time()` again overwrites the one running timer regardless of label. See [ADR-00029](../adr/ADR-00029.md).) |
| `console.count()` / `.countReset()` | ✅ (backed by a real `Map<string, number>` — matches real Node's per-label semantics exactly, unlike `time`'s single-slot narrowing above. See [ADR-00029](../adr/ADR-00029.md).) |
| `console.group()` / `.groupEnd()` | ✅ (indents every subsequent `console.*` line by two spaces per nesting level; an unbalanced extra `groupEnd()` floors at depth 0 rather than going negative. See [ADR-00029](../adr/ADR-00029.md).) |
| `console.dir()` | ✅ (prints a single value exactly like a single-argument `console.log`; the real API's second `options` argument — depth/color controls — is accepted syntactically but ignored. See [ADR-00029](../adr/ADR-00029.md).) |
