# Global Functions & Constants

> Part of the [Implementation Status](README.md) index. JavaScript language-level globals unrelated to any browser API.

**Coverage**: 18/20 (90%) · **Strict Coverage**: 8/20 (40%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `isNaN(x)` | ✅ | | |
| `isFinite(x)` | ✅ | | |
| `String(x)` | ✅ | • `String()` with no argument is `""`, not real JS's `"undefined"` (a genuinely absent value doesn't arise in typed code — [ADR-00291](../adr/ADR-00291.md)) | • Routes through the template-literal renderer; an `any` input dispatches on its runtime tag |
| `Number(x)` | ✅ | • Inherits `strtod`'s `"inf"` acceptance for the string form (JS accepts only the full word `Infinity`) — same family as `parseFloat`'s ([ADR-00291](../adr/ADR-00291.md)) | • JS ToNumber: whole-string parse (`"12px"` → `NaN`, unlike `parseFloat`), `""`/whitespace → 0, `"0x10"` → 16, boolean → 0/1, `null` → 0; a numeric input passes through unchanged (exact i64 stays i64) |
| `Boolean(x)` | ✅ | | • Shared truthiness (`NaN`, `""`, 0 falsy — [ADR-00116](../adr/ADR-00116.md)) |
| `parseInt(s, radix?)` | ✅ | • No hex auto-detect when `radix` is omitted (`"0x1F"` parses as `0`, not `31`) | • A no-digits input returns a real `NaN` (endptr-checked, double result — [ADR-00287](../adr/ADR-00287.md)) |
| `parseFloat(s)` | ✅ | • Inherits `strtod` extras — `"inf"` parses to `Infinity`, C hex-float syntax parses ([ADR-00287](../adr/ADR-00287.md)) | • A no-conversion input returns a real `NaN` ([ADR-00287](../adr/ADR-00287.md)) |
| `NaN` (global constant) | ✅ | • `let NaN = 99;` is a reserved-name collision under the default `-compat=strict`; `-compat=js` allows the shadow real JS permits | • `NaN != x`/`!== x` comparisons are correct — compiled as unordered `fcmp une`, so `NaN !== NaN` is `true` as in JS — see [ADR-00188](../adr/ADR-00188.md) |
| `Infinity` (global constant) | ✅ | • Same shadowing caveat as `NaN` above — needs `-compat=js`, not unconditional | • See [ADR-00166](../adr/ADR-00166.md). |
| `undefined` (global constant) | ✅ | | • As a literal value |
| `globalThis` | ❌ | | • Not meaningful in a native single-file context |
| `encodeURI(s)` | ✅ | | • Leaves the unreserved *and* reserved (`;/?:@&=+$,#`) character sets unescaped. See [ADR-00024](../adr/ADR-00024.md). |
| `decodeURI(s)` | ✅ | • Permissive on malformed input (passes a bad/truncated escape through as literal text) rather than throwing a `URIError` | • Does **not** decode a `%XX` escape representing a reserved character (leaves it as literal `%XX` text) — the one real behavioral difference from `decodeURIComponent`. See [ADR-00024](../adr/ADR-00024.md). |
| `encodeURIComponent(s)` | ✅ | | • Leaves only the unreserved set (letters, digits, `-_.!~*'()`) unescaped. See [ADR-00024](../adr/ADR-00024.md). |
| `decodeURIComponent(s)` | ✅ | | • Decodes every valid `%XX` escape unconditionally. See [ADR-00024](../adr/ADR-00024.md). |
| `atob(s)` | ✅ | • Permissive: malformed length/characters decode as best-effort rather than throwing | • Base64 decode<br>• Operates byte-for-byte on the input string (this compiler's strings are already plain byte sequences — no separate "binary string" type needed). See [ADR-00024](../adr/ADR-00024.md). |
| `btoa(s)` | ✅ | | • Base64 encode, `=`-padded (RFC 4045). See [ADR-00024](../adr/ADR-00024.md). |
| `structuredClone(obj)` | ✅ | • `Map`/`Set`/`EventEmitter`/`URL`/`URLSearchParams`/`ArrayBuffer`/functions/class instances/`Error`/`Promise`/`any`/`unknown` are rejected at compile time rather than silently aliased | • Real recursive deep copy, dispatched entirely on the argument's static type (arrays, incl. nested/TypedArrays, and plain objects recurse; scalars pass through as value types) — see [ADR-00113](../adr/ADR-00113.md) |
| `queueMicrotask(fn)` | ✅ | • Drained at the reachable checkpoints (end of the top-level script, each scheduler step); a program with neither timers nor async tasks drains once at exit | • A FIFO of callback closures, run after the current synchronous script and before timers ([TDD-00083](../tdd/TDD-00083.md) Stage 3 / [ADR-00245](../adr/ADR-00245.md)) |
| `eval(s)` | ❌ | | • General/dynamic `eval` is unimplemented (needs the opt-in embedded engine, [TDD-00046](../tdd/TDD-00046.md), not started). A narrow **static subset** does work: a compile-time-constant `eval("<expression>")` is compiled in place through this compiler's own parser+codegen, no engine — see [ADR-00198](../adr/ADR-00198.md). A dynamic string, a statement/declaration, or a reference to a top-level binding is a clean compile error, never a runtime throw (so it can't false-pass a negative test) |
