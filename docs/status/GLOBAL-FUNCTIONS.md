# Global Functions & Constants

> Part of the [Implementation Status](README.md) index. JavaScript language-level globals unrelated to any browser API.

**Coverage**: ~76% (13/17).

**Caveats**: `globalThis` isn't meaningful in a native single-file context (not planned). `structuredClone`/`queueMicrotask` aren't implemented. `eval` won't be implemented (requires a JIT).

| Feature | Status | Notes |
|---|---|---|
| `isNaN(x)` | ✅ | |
| `isFinite(x)` | ✅ | |
| `parseInt(s, radix?)` | ✅ | |
| `parseFloat(s)` | ✅ | |
| `NaN` (global constant) | ✅ | A local variable of the same name still shadows it. See [ADR-00024](../adr/ADR-00024.md). |
| `Infinity` (global constant) | ✅ | Same shadowing rule as `NaN`. See [ADR-00024](../adr/ADR-00024.md). |
| `undefined` (global constant) | ✅ | As a literal value |
| `globalThis` | ❌ | Not meaningful in a native single-file context |
| `encodeURI(s)` | ✅ | Leaves the unreserved *and* reserved (`;/?:@&=+$,#`) character sets unescaped. See [ADR-00024](../adr/ADR-00024.md). |
| `decodeURI(s)` | ✅ | Does **not** decode a `%XX` escape representing a reserved character (leaves it as literal `%XX` text) — the one real behavioral difference from `decodeURIComponent`. Permissive on malformed input (passes a bad/truncated escape through as literal text) rather than throwing a `URIError`. See [ADR-00024](../adr/ADR-00024.md). |
| `encodeURIComponent(s)` | ✅ | Leaves only the unreserved set (letters, digits, `-_.!~*'()`) unescaped. See [ADR-00024](../adr/ADR-00024.md). |
| `decodeURIComponent(s)` | ✅ | Decodes every valid `%XX` escape unconditionally. See [ADR-00024](../adr/ADR-00024.md). |
| `atob(s)` | ✅ | Base64 decode. Permissive: malformed length/characters decode as best-effort rather than throwing. Operates byte-for-byte on the input string (this compiler's strings are already plain byte sequences — no separate "binary string" type needed). See [ADR-00024](../adr/ADR-00024.md). |
| `btoa(s)` | ✅ | Base64 encode, `=`-padded (RFC 4045). See [ADR-00024](../adr/ADR-00024.md). |
| `structuredClone(obj)` | ❌ | Deep copy; medium complexity |
| `queueMicrotask(fn)` | ❌ | Needs event loop |
| `eval(s)` | ❌ | Won't implement (requires a JIT) |
