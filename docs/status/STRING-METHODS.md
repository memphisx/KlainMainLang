# String Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~79% (26/33).

**Caveats**: `.normalize()`, `.match()`/`.matchAll()` are the real gaps (need actual Unicode normalization tables / a regex engine respectively — deliberately not faked). `.codePointAt()`/`.search()`/`.localeCompare()` are all real methods but scope-narrowed to this compiler's byte-sequence strings and lack of `Intl`/locale infrastructure — see the notes below.

| Method | Status |
|---|---|
| `+` (concatenation) | ✅ |
| `.length` | ✅ |
| `.slice(start, end?)` | ✅ |
| `.substring(start, end?)` | ✅ |
| `.indexOf(substr)` | ✅ |
| `.includes(substr)` | ✅ |
| `.startsWith(prefix)` | ✅ |
| `.endsWith(suffix)` | ✅ |
| `.replace(from, to)` | ✅ |
| `.split(sep)` | ✅ (empty separator splits into individual characters, matching JS — previously hung; see [ADR-00004](../adr/ADR-00004.md)) |
| `.trim()` | ✅ |
| `.trimStart()` / `.trimEnd()` | ✅ |
| `.toUpperCase()` | ✅ |
| `.toLowerCase()` | ✅ |
| `.repeat(n)` | ✅ |
| `.padStart(len, pad?)` | ✅ (empty pad string is a no-op, matching JS — previously corrupted output; see [ADR-00004](../adr/ADR-00004.md)) |
| `.padEnd(len, pad?)` | ✅ (same empty-pad fix as `.padStart`) |
| `.charCodeAt(i)` | ✅ |
| `.at(i)` | ✅ |
| `.charAt(i)` | ✅ (unlike `.at()`, never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction between the two methods. See [ADR-00028](../adr/ADR-00028.md).) |
| `.codePointAt(i)` | ✅ (this compiler's strings are plain byte sequences, not real UTF-16 — no surrogate-pair/multi-byte decoding, so this is exactly `.charCodeAt(i)`'s byte value under a second name. Correct for ASCII/Latin-1 text; a documented scope narrowing for anything needing real Unicode decoding. See [ADR-00028](../adr/ADR-00028.md).) |
| `.normalize()` | ❌ (deliberately deferred, not attempted — needs real Unicode normalization tables (NFC/NFD/NFKC/NFKD) this compiler has no infrastructure for at all; a fake identity-only implementation would silently mis-normalize any non-ASCII composed/decomposed text, exactly the "silent wrong output" failure mode this project avoids) |
| `.match()` / `.matchAll()` | ❌ (needs a real `RegExp` engine — tracked separately, see the [Implementation Status](README.md) index's "What Is NOT Implemented") |
| `.search(pattern)` | ✅ (real JS coerces `pattern` to a `RegExp`; this compiler has no `RegExp` type or regex literal syntax at all, so a plain string is the *only* value that could ever reach this call — making this exactly `.indexOf`'s behavior under a second name, not a partial regex implementation. See [ADR-00028](../adr/ADR-00028.md).) |
| `.replaceAll()` | ✅ (empty search is a no-op, not JS's insert-between-chars behavior — see [ADR-00003](../adr/ADR-00003.md)) |
| `.localeCompare(other)` | ✅ (byte-order comparison via `strcmp`, normalized to exactly `-1`/`0`/`1` — not real Unicode collation, this compiler has no locale/`Intl` infrastructure, the same scope narrowing already used for `toLocaleDateString`. See [ADR-00028](../adr/ADR-00028.md).) |
| `String.fromCharCode(n)` | ✅ |
| `String.fromCodePoint(n)` | ✅ |
| `String.raw` tag | ❌ |
