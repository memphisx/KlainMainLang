# String Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 27/29 (~93%) · **Strict Coverage**: 19/29 (~66%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `+` (concatenation) | ✅ | • A `number \| null` operand still silently prints `"x0"` instead of `"xnull"` — the numeric branch of value-to-string has no nullability check ([ADR-00166](../adr/ADR-00166.md)) | • A null *string* operand now stringifies as `"null"` (`"x" + null === "xnull"`), matching real JS; previously segfaulted via `strlen(NULL)` ([ADR-00165](../adr/ADR-00165.md)) |
| `.length` | ✅ | | |
| `.slice(start, end?)` | ✅ | | |
| `.substring(start, end?)` | ✅ | | |
| `.indexOf(substr)` | ✅ | | |
| `.includes(substr)` | ✅ | | |
| `.startsWith(prefix)` | ✅ | | |
| `.endsWith(suffix)` | ✅ | | |
| `.replace(from, to)` | ✅ | | |
| `.split(sep)` | ✅ | | • Empty separator splits into individual characters, matching JS; previously hung ([ADR-00004](../adr/ADR-00004.md)) |
| `.trim()` | ✅ | | |
| `.trimStart()` / `.trimEnd()` | ✅ | | |
| `.toUpperCase()` | ✅ | | |
| `.toLowerCase()` | ✅ | | |
| `.repeat(n)` | ✅ | | |
| `.padStart(len, pad?)` | ✅ | | • Empty pad string is a no-op, matching JS; previously corrupted output ([ADR-00004](../adr/ADR-00004.md)) |
| `.padEnd(len, pad?)` | ✅ | | • Same empty-pad fix as `.padStart` ([ADR-00004](../adr/ADR-00004.md)) |
| `.charCodeAt(i)` | ✅ | • An out-of-range index (negative or `>= length`) reads uninitialized/garbage memory instead of returning `NaN` — non-deterministic, with no bounds check (unlike `.charAt(i)`'s clamp-before-read) ([ADR-00166](../adr/ADR-00166.md)) | |
| `.at(i)` | ✅ | • An out-of-range `i` returns `""` rather than real JS's `undefined` — deterministic and safe, but a real string a caller could mistake for actual data ([ADR-00166](../adr/ADR-00166.md)) | |
| `.charAt(i)` | ✅ | | • Never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction from `.at()` ([ADR-00028](../adr/ADR-00028.md)) |
| `.codePointAt(i)` | ✅ | • This compiler's strings are plain byte sequences, not real UTF-16 — no surrogate-pair/multi-byte decoding, so this is exactly `.charCodeAt(i)`'s byte value under a second name; correct only for ASCII/Latin-1 text ([ADR-00028](../adr/ADR-00028.md))<br>• Inherits `.charCodeAt()`'s out-of-range garbage-memory-read bug, calling into the same implementation ([ADR-00166](../adr/ADR-00166.md)) | |
| `.normalize()` | ❌ | | • Deliberately deferred, not attempted — needs real Unicode normalization tables (NFC/NFD/NFKC/NFKD) this compiler has no infrastructure for; a fake identity-only implementation would silently mis-normalize any non-ASCII composed/decomposed text |
| `.match()` / `.matchAll()` | ✅ | • `.matchAll()` returns an eager `string[][]` rather than a lazy iterator ([REGEXP.md](REGEXP.md)) | • PCRE2-backed; `.match()` is real JS-shaped ([REGEXP.md](REGEXP.md)) |
| `.search(pattern)` | ✅ | • A plain-string `pattern` isn't coerced to a `RegExp` as in real JS — it falls back to the pre-`RegExp` `.indexOf()`-shaped behavior ([ADR-00028](../adr/ADR-00028.md)/[REGEXP.md](REGEXP.md)) | • A `RegExp` `pattern` runs a real PCRE2 search |
| `.replaceAll()` | ✅ | • An empty search is a no-op, not JS's insert-between-chars behavior ([ADR-00003](../adr/ADR-00003.md)) | |
| `.localeCompare(other)` | ✅ | • Byte-order comparison via `strcmp` (normalized to exactly `-1`/`0`/`1`), not real Unicode collation — no locale/`Intl` infrastructure ([ADR-00028](../adr/ADR-00028.md)) | |
| `String.fromCharCode(n)` | ✅ | | |
| `String.fromCodePoint(n)` | ✅ | | |
| `String.raw` tag | ❌ | | |

## Known limitations

- The regex-accepting methods (`.match`/`.matchAll`/`.replace`/`.replaceAll`/`.split`/`.search`) carry additional caveats in [REGEXP.md](REGEXP.md) (backreference/callback scope, no implicit string-to-RegExp coercion, etc.), not repeated per row.
