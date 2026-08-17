# Global Functions & Constants

> Part of the [Implementation Status](README.md) index. JavaScript language-level globals unrelated to any browser API.

**Coverage**: ~82% (14/17).

**Strict Coverage**: 3/17, ~18% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number and the new caveats above.

**Caveats**:

- `globalThis` isn't meaningful in a native single-file context (not planned).
- `queueMicrotask` isn't implemented.
- `eval` handles only a static subset — a compile-time-constant `eval("<expression>")` compiled in place through this compiler's own pipeline (no engine, [ADR-00198](../adr/ADR-00198.md)). The general dynamic case needs the opt-in embedded-engine path scoped in [TDD-00046](../tdd/TDD-00046.md) — low priority, not started.

| Feature | Status | Notes |
|---|---|---|
| `isNaN(x)` | ✅ | |
| `isFinite(x)` | ✅ | |
| `parseInt(s, radix?)` | ✅ | Invalid input returns `0` instead of `NaN`; no hex auto-detect when `radix` is omitted (`"0x1F"` parses as `0`, not `31`). Same underlying bug as [NUMBER-MATH.md](NUMBER-MATH.md)'s `Number.parseInt`/`parseInt` rows. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
| `parseFloat(s)` | ✅ | Invalid input returns `0` instead of `NaN`. Same underlying bug as [NUMBER-MATH.md](NUMBER-MATH.md)'s `Number.parseFloat`/`parseFloat` rows. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
| `NaN` (global constant) | ✅ | **Stale note**: shadowing is no longer unconditional — since `-globals=strict` became the default (`af7469d`), `let NaN = 99;` is a compile-time-rejected reserved-name collision; only `-globals=permissive` allows the shadow real JS/browsers permit. (`NaN != x`/`!== x` comparisons are now correct — compiled as unordered `fcmp une`, so `NaN !== NaN` is `true` as in JS — see [ADR-00188](../adr/ADR-00188.md).) |
| `Infinity` (global constant) | ✅ | Same stale shadowing note as `NaN` above — needs `-globals=permissive`, not unconditional. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
| `undefined` (global constant) | ✅ | As a literal value |
| `globalThis` | ❌ | Not meaningful in a native single-file context |
| `encodeURI(s)` | ✅ | Leaves the unreserved *and* reserved (`;/?:@&=+$,#`) character sets unescaped. See [ADR-00024](../adr/ADR-00024.md). |
| `decodeURI(s)` | ✅ | Does **not** decode a `%XX` escape representing a reserved character (leaves it as literal `%XX` text) — the one real behavioral difference from `decodeURIComponent`. Permissive on malformed input (passes a bad/truncated escape through as literal text) rather than throwing a `URIError`. See [ADR-00024](../adr/ADR-00024.md). |
| `encodeURIComponent(s)` | ✅ | Leaves only the unreserved set (letters, digits, `-_.!~*'()`) unescaped. See [ADR-00024](../adr/ADR-00024.md). |
| `decodeURIComponent(s)` | ✅ | Decodes every valid `%XX` escape unconditionally. See [ADR-00024](../adr/ADR-00024.md). |
| `atob(s)` | ✅ | Base64 decode. Permissive: malformed length/characters decode as best-effort rather than throwing. Operates byte-for-byte on the input string (this compiler's strings are already plain byte sequences — no separate "binary string" type needed). See [ADR-00024](../adr/ADR-00024.md). |
| `btoa(s)` | ✅ | Base64 encode, `=`-padded (RFC 4045). See [ADR-00024](../adr/ADR-00024.md). |
| `structuredClone(obj)` | ✅ | Real recursive deep copy, dispatched entirely on the argument's static type (arrays, incl. nested/TypedArrays, and plain objects recurse; scalars pass through as value types). `Map`/`Set`/`EventEmitter`/`URL`/`URLSearchParams`/`ArrayBuffer`/functions/class instances/`Error`/`Promise`/`any`/`unknown` are rejected at compile time rather than silently aliased — see [ADR-00113](../adr/ADR-00113.md). |
| `queueMicrotask(fn)` | ❌ | Needs event loop |
| `eval(s)` | ❌ | General/dynamic `eval` is unimplemented (needs the opt-in embedded engine, [TDD-00046](../tdd/TDD-00046.md), not started). A narrow **static subset** does work: a compile-time-constant `eval("<expression>")` is compiled in place through this compiler's own parser+codegen, no engine — see [ADR-00198](../adr/ADR-00198.md). A dynamic string, a statement/declaration, or a reference to a top-level binding is a clean compile error, never a runtime throw (so it can't false-pass a negative test) |
