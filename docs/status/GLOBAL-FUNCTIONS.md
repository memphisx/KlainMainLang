# Global Functions & Constants

> Part of the [Implementation Status](README.md) index. JavaScript language-level globals unrelated to any browser API.

**Coverage**: ~82% (14/17).

**Strict Coverage**: 3/14, ~21% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number and the new caveats above.

**Caveats**: `globalThis` isn't meaningful in a native single-file context (not planned). `queueMicrotask` isn't implemented. `eval` needs a JIT/interpreter this compiler doesn't have natively; an opt-in embedded-engine path is scoped in [TDD-00046](../tdd/TDD-00046.md) but deliberately low priority and not started.

| Feature | Status | Notes |
|---|---|---|
| `isNaN(x)` | ✅ | |
| `isFinite(x)` | ✅ | |
| `parseInt(s, radix?)` | ✅ | Invalid input returns `0` instead of `NaN`; no hex auto-detect when `radix` is omitted (`"0x1F"` parses as `0`, not `31`). Same underlying bug as [NUMBER-MATH.md](NUMBER-MATH.md)'s `Number.parseInt`/`parseInt` rows. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
| `parseFloat(s)` | ✅ | Invalid input returns `0` instead of `NaN`. Same underlying bug as [NUMBER-MATH.md](NUMBER-MATH.md)'s `Number.parseFloat`/`parseFloat` rows. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
| `NaN` (global constant) | ✅ | **Stale note**: shadowing is no longer unconditional — since `-globals=strict` became the default (`af7469d`), `let NaN = 99;` is a compile-time-rejected reserved-name collision; only `-globals=permissive` allows the shadow real JS/browsers permit. Separately, and more seriously: `NaN != x`/`!== x` comparisons are wrong — compiled as LLVM `fcmp one` ("ordered and not equal"), which is `false` whenever either operand is NaN, so `NaN !== NaN` incorrectly evaluates to `false` (should be `true`), and one operand ordering (`5 !== NaN`) returns outright garbage rather than a valid boolean. Both found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). |
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
| `eval(s)` | ❌ | Needs a JIT/interpreter this compiler doesn't have natively; opt-in embedded-engine path scoped in [TDD-00046](../tdd/TDD-00046.md), not started |
