<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/string-methods.json; edit the JSON, then run `make status`. -->

# String Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 29/30 (~97%) · **Strict Coverage**: 16/30 (~53%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `+` (concatenation) | ✅ | | • A null operand stringifies as `"null"` (`"x" + null === "xnull"`), matching real JS ([ADR-00165](../adr/ADR-00165.md))<br>• A `number \| null` operand renders `"null"` for the null case (`"x" + n`) — as a parameter, local, object field, or a `T | null`-returning call — not its payload zero ([ADR-00537](../adr/ADR-00537.md)/[ADR-00538](../adr/ADR-00538.md)) |
| `.length` | ✅ | • Byte length, not the JS UTF-16 code-unit count — `'café'.length` is `5` (Node: `4`). | |
| `.slice(start, end?)` | ✅ | • Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it (`'café'.slice(0, 4)` cuts mid-`é`), diverging from Node on non-ASCII text. | |
| `.substring(start, end?)` | ✅ | • Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it, unlike Node's code-unit indexing on non-ASCII text. | |
| `.indexOf(substr)` | ✅ | • Returns a byte offset, not a UTF-16 index — `'naïve'.indexOf('ve')` is `4` (Node: `3`). | |
| `.includes(substr)` | ✅ | • Binary-safe but byte-space — shares the byte-offset model of `indexOf`/`slice`, operating on bytes rather than UTF-16 code units (matters only on non-ASCII text). | |
| `.startsWith(prefix)` | ✅ | | |
| `.endsWith(suffix)` | ✅ | | |
| `.replace(from, to)` | ✅ | | |
| `.split(sep)` | ✅ | | • Empty separator splits into individual characters, matching JS ([ADR-00004](../adr/ADR-00004.md)) |
| `.trim()` | ✅ | | • Strips the full JS WhiteSpace/LineTerminator set (U+00A0, U+1680, U+2000–200A, U+2028/29, U+202F, U+205F, U+3000, U+FEFF — UTF-8-aware `__kml_ws_span`), not just ASCII ([ADR-00295](../adr/ADR-00295.md)) |
| `.trimStart()` / `.trimEnd()` | ✅ | | • Same full-whitespace-set handling as `.trim()` ([ADR-00295](../adr/ADR-00295.md)) |
| `.toString()` | ✅ | | • Identity on a string, matching JS — kept because Node code habitually calls it on values that are Buffers there but strings here (spawnSync results, stream chunks) |
| `.toUpperCase()` | ✅ | • ASCII-only case mapping (`a`–`z`/`A`–`Z`) — `'café'.toUpperCase()` is `'CAFé'` (Node: `'CAFÉ'`); no Unicode case tables. | |
| `.toLowerCase()` | ✅ | • ASCII-only case mapping — `'Σ'.toLowerCase()` is `'Σ'` (Node: `'σ'`); non-ASCII bytes pass through unchanged. | |
| `.repeat(n)` | ✅ | | |
| `.padStart(len, pad?)` | ✅ | | • Empty pad string is a no-op, matching JS ([ADR-00004](../adr/ADR-00004.md)) |
| `.padEnd(len, pad?)` | ✅ | | • Same empty-pad rule as `.padStart` ([ADR-00004](../adr/ADR-00004.md)) |
| `.charCodeAt(i)` | ✅ | | • Bounds-checked: an out-of-range index (negative or `>= length`) returns `NaN`, as real JS — the result is a double for exactly that reason ([ADR-00287](../adr/ADR-00287.md)); byte-space code units per this compiler's byte-sequence strings |
| `.at(i)` | ✅ | • An out-of-range `i` returns `""` rather than real JS's `undefined` — deterministic and safe, but a real string a caller could mistake for actual data ([ADR-00166](../adr/ADR-00166.md)) | |
| `.charAt(i)` | ✅ | | • Never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction from `.at()` ([ADR-00028](../adr/ADR-00028.md)) |
| `.codePointAt(i)` | ✅ | • This compiler's strings are plain byte sequences, not real UTF-16 — no surrogate-pair/multi-byte decoding, so this is exactly `.charCodeAt(i)`'s byte value under a second name; correct only for ASCII/Latin-1 text ([ADR-00028](../adr/ADR-00028.md))<br>• An out-of-range index returns `NaN` (bounds-checked, [ADR-00287](../adr/ADR-00287.md)), where real JS returns `undefined` — no undefined sentinel for a numeric result | |
| `.normalize()` | ❌ | | • Deliberately deferred, not attempted — needs real Unicode normalization tables (NFC/NFD/NFKC/NFKD) this compiler has no infrastructure for; a fake identity-only implementation would silently mis-normalize any non-ASCII composed/decomposed text |
| `.match()` / `.matchAll()` | ✅ | • `.matchAll()` returns an eager `string[][]` rather than a lazy iterator ([REGEXP.md](REGEXP.md)) | • PCRE2-backed; `.match()` is real JS-shaped ([REGEXP.md](REGEXP.md)) |
| `.search(pattern)` | ✅ | | • A plain-string `pattern` is coerced to a `RegExp` as in real JS — metacharacters are interpreted (`"a.b".search(".")` is `0`) ([ADR-00548](../adr/ADR-00548.md))<br>• A `RegExp` `pattern` runs a real PCRE2 search |
| `.replaceAll()` | ✅ | | • An empty search matches JS's insert-between-every-char behavior — `"abc".replaceAll("", "-")` is `"-a-b-c-"` ([ADR-00003](../adr/ADR-00003.md)/[ADR-00547](../adr/ADR-00547.md)) |
| `.localeCompare(other)` | ✅ | • Length-aware byte-order comparison (normalized to exactly `-1`/`0`/`1`, binary-safe past an embedded NUL — [TDD-00120](../tdd/TDD-00120.md)/[ADR-00364](../adr/ADR-00364.md)), not real Unicode collation — no locale/`Intl` infrastructure ([ADR-00028](../adr/ADR-00028.md)) | |
| `String.fromCharCode(n)` | ✅ | • Each argument is truncated to one byte (0–255), not encoded as a UTF-16 code unit — `String.fromCharCode(0x263A)` is `':'` (Node: `'☺'`). | |
| `String.fromCodePoint(n)` | ✅ | • Shares `fromCharCode`'s one-byte truncation — no astral/surrogate encoding — a code point above `0xFF` is mangled (`String.fromCodePoint(0x263A)` → `':'`, Node: `'☺'`). | |
| `String.raw` tag | ✅ | | • Interleaves the raw (undecoded) quasi text with the string-coerced interpolations — escape sequences appear verbatim (`` String.raw`a\nb` `` is `a\nb`), byte-for-byte the same as Node. The raw quasis are threaded from the lexer through the `TaggedTemplateExpression` ([ADR-00562](../adr/ADR-00562.md)) |

## Known limitations

- The regex-accepting methods (`.match`/`.matchAll`/`.replace`/`.replaceAll`/`.split`/`.search`) carry additional caveats in [REGEXP.md](REGEXP.md) (backreference/callback scope, no implicit string-to-RegExp coercion, etc.), not repeated per row.
- Comparison (`===`/`<`/`switch`/`.localeCompare`) and substring search (`.indexOf`/`.includes`/`.split`/`.replace`/`.replaceAll`) are binary-safe: they read a length header and search with `memmem`, so an embedded null byte no longer cuts the operation short ([TDD-00120](../tdd/TDD-00120.md)/[ADR-00364](../adr/ADR-00364.md)). The one string consumer still bounded by the NUL is `console.log`'s display — see [CONSOLE.md](CONSOLE.md).
