# String Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~85% (28/33).

**Caveats**: `.normalize()` is the one real remaining gap (needs actual Unicode normalization tables — deliberately not faked). `RegExp` (see [REGEXP.md](REGEXP.md)) is now fully implemented — `.match()`/`.matchAll()` are real (Unicode-normalization-table-free, PCRE2-backed) regex methods, and `.replace()`/`.replaceAll()`/`.split()`/`.search()` are all genuinely regex-aware when passed a `RegExp` argument, per [TDD-00035](../tdd/TDD-00035.md)'s 7 stages ([ADR-00114](../adr/ADR-00114.md), [ADR-00115](../adr/ADR-00115.md), [ADR-00116](../adr/ADR-00116.md), [ADR-00117](../adr/ADR-00117.md), [ADR-00118](../adr/ADR-00118.md), [ADR-00119](../adr/ADR-00119.md)) — see [REGEXP.md](REGEXP.md) for that feature's own caveats (backreference/callback scope, no implicit string-to-RegExp coercion, etc.), not repeated here. `.codePointAt()`/`.search()`/`.localeCompare()` are all real methods but scope-narrowed to this compiler's byte-sequence strings and lack of `Intl`/locale infrastructure — see the notes below.

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
| `.match()` / `.matchAll()` | ✅ PCRE2-backed; see [REGEXP.md](REGEXP.md) for scope (`.match()` real JS-shaped, `.matchAll()` an eager `string[][]` rather than a lazy iterator) |
| `.search(pattern)` | ✅ A `RegExp` `pattern` runs a real PCRE2 search; a plain-string `pattern` still falls back to the pre-`RegExp` `.indexOf()`-shaped behavior (real JS's implicit string-to-RegExp coercion isn't implemented — see [ADR-00028](../adr/ADR-00028.md)/[REGEXP.md](REGEXP.md)). |
| `.replaceAll()` | ✅ (empty search is a no-op, not JS's insert-between-chars behavior — see [ADR-00003](../adr/ADR-00003.md)) |
| `.localeCompare(other)` | ✅ (byte-order comparison via `strcmp`, normalized to exactly `-1`/`0`/`1` — not real Unicode collation, this compiler has no locale/`Intl` infrastructure, the same scope narrowing already used for `toLocaleDateString`. See [ADR-00028](../adr/ADR-00028.md).) |
| `String.fromCharCode(n)` | ✅ |
| `String.fromCodePoint(n)` | ✅ |
| `String.raw` tag | ❌ |
