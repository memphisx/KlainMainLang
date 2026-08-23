# String Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 27/29 (~93%) · **Strict Coverage**: 20/29 (~69%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `+` (concatenation) | ✅ | • A `number \| null` operand still silently prints `"x0"` instead of `"xnull"` — the numeric branch of value-to-string has no nullability check ([ADR-00166](../adr/ADR-00166.md)) | • A null *string* operand stringifies as `"null"` (`"x" + null === "xnull"`), matching real JS ([ADR-00165](../adr/ADR-00165.md)) |
| `.length` | ✅ | | |
| `.slice(start, end?)` | ✅ | | |
| `.substring(start, end?)` | ✅ | | |
| `.indexOf(substr)` | ✅ | | |
| `.includes(substr)` | ✅ | | |
| `.startsWith(prefix)` | ✅ | | |
| `.endsWith(suffix)` | ✅ | | |
| `.replace(from, to)` | ✅ | | |
| `.split(sep)` | ✅ | | • Empty separator splits into individual characters, matching JS ([ADR-00004](../adr/ADR-00004.md)) |
| `.trim()` | ✅ | | • Strips the full JS WhiteSpace/LineTerminator set (U+00A0, U+1680, U+2000–200A, U+2028/29, U+202F, U+205F, U+3000, U+FEFF — UTF-8-aware `__kml_ws_span`), not just ASCII ([ADR-00295](../adr/ADR-00295.md)) |
| `.trimStart()` / `.trimEnd()` | ✅ | | • Same full-whitespace-set handling as `.trim()` ([ADR-00295](../adr/ADR-00295.md)) |
| `.toUpperCase()` | ✅ | | |
| `.toLowerCase()` | ✅ | | |
| `.repeat(n)` | ✅ | | |
| `.padStart(len, pad?)` | ✅ | | • Empty pad string is a no-op, matching JS ([ADR-00004](../adr/ADR-00004.md)) |
| `.padEnd(len, pad?)` | ✅ | | • Same empty-pad rule as `.padStart` ([ADR-00004](../adr/ADR-00004.md)) |
| `.charCodeAt(i)` | ✅ | | • Bounds-checked: an out-of-range index (negative or `>= length`) returns `NaN`, as real JS — the result is a double for exactly that reason ([ADR-00287](../adr/ADR-00287.md)); byte-space code units per this compiler's byte-sequence strings |
| `.at(i)` | ✅ | • An out-of-range `i` returns `""` rather than real JS's `undefined` — deterministic and safe, but a real string a caller could mistake for actual data ([ADR-00166](../adr/ADR-00166.md)) | |
| `.charAt(i)` | ✅ | | • Never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction from `.at()` ([ADR-00028](../adr/ADR-00028.md)) |
| `.codePointAt(i)` | ✅ | • This compiler's strings are plain byte sequences, not real UTF-16 — no surrogate-pair/multi-byte decoding, so this is exactly `.charCodeAt(i)`'s byte value under a second name; correct only for ASCII/Latin-1 text ([ADR-00028](../adr/ADR-00028.md))<br>• An out-of-range index returns `NaN` (bounds-checked, [ADR-00287](../adr/ADR-00287.md)), where real JS returns `undefined` — no undefined sentinel for a numeric result | |
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
