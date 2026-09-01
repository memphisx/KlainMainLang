<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/console.json; edit the JSON, then run `make status`. -->

# console

> Part of the [Implementation Status](README.md) index.

**Coverage**: 12/12 (100%) · **Strict Coverage**: 8/12 (~67%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `console.log(...)` | ✅ | • A printed string is written via `printf`'s `"%s"`, so a value with an embedded null byte truncates *on display* at the first `\0` — the stored string is intact (`.length` and every operation see the full bytes); a binary-safe `fwrite` print is deferred as display-only, low-value ([TDD-00120](../tdd/TDD-00120.md)/[ADR-00364](../adr/ADR-00364.md)). Use `process.stdout.write` for binary output | • Multiple arguments join with single spaces on one line, a no-arg call prints a bare newline, and `-0` displays as `-0` (Node's util.inspect display; `String(-0)` stays `"0"`) — [ADR-00285](../adr/ADR-00285.md) |
| `console.error(...)` | ✅ | | • Writes to stderr |
| `console.warn(...)` | ✅ | | • Writes to stderr, unprefixed — identical to `console.error`, as real Node ([ADR-00285](../adr/ADR-00285.md)) |
| `console.info(...)` | ✅ | | |
| `console.debug(...)` | ✅ | | |
| `console.trace(...)` | ✅ | • Prints `"Trace: <message>"` and nothing else; real Node's entire point of `.trace()` is the call stack it prints below the message, which this never generates at all | • Same generic print path as `.debug()`/`.info()`, no stack-walking logic. See [ADR-00166](../adr/ADR-00166.md). |
| `console.assert(cond, msg)` | ✅ | | |
| `console.table()` | ✅ | • An array of objects (columns = the shared fields) or an array of primitives (a single `Values` column) is tabulated; a `columns` filter argument, a plain-object argument, and an array-of-arrays shape aren't tabulated (they fall back to `console.log`, as a non-tabular value does) | • Byte-for-byte the same Unicode box-drawing layout as real Node (verified against Node v26) — dynamic per-column width, left-aligned cells sized to the widest entry, quoted string cells ([ADR-00560](../adr/ADR-00560.md)) |
| `console.time()` / `.timeEnd()` | ✅ | | • Per-label backing `Map<string, number>` — distinct labels track independent timers, matching real Node. See [ADR-00029](../adr/ADR-00029.md), [ADR-00544](../adr/ADR-00544.md). |
| `console.count()` / `.countReset()` | ✅ | | • Backed by a real `Map<string, number>` — matches real Node's per-label semantics exactly, unlike `time`'s single-slot narrowing above. See [ADR-00029](../adr/ADR-00029.md). |
| `console.group()` / `.groupEnd()` | ✅ | | • Indents every subsequent `console.*` line by two spaces per nesting level; an unbalanced extra `groupEnd()` floors at depth 0 rather than going negative. See [ADR-00029](../adr/ADR-00029.md). |
| `console.dir(obj, { depth?, colors? })` | ✅ | • The `colors` option is accepted but ignored — inspected output carries no ANSI here | • Prints a single value like a single-argument `console.log`, but with Node's `util.inspect` default nesting depth of **2** (a bare `console.log` uses this compiler's deeper default); the `depth` option overrides it — a literal number caps nesting, `null` is unlimited, and beyond the cap a nested value shows as `[Object]`/`[Array]`, matching Node ([ADR-00583](../adr/ADR-00583.md))<br>• See [ADR-00029](../adr/ADR-00029.md). |
