<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/string-methods.json; edit the JSON, then run `make status`. -->

# String Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 32/33 (~97%) · **Strict Coverage**: 22/33 (~67%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `+` (concatenation) | ✅ | | • A null operand stringifies as `"null"` (`"x" + null === "xnull"`), matching real JS ([ADR-00165](../adr/ADR-00165.md))<br>• A `number \| null` operand renders `"null"` for the null case (`"x" + n`) — as a parameter, local, object field, or a `T | null`-returning call — not its payload zero ([ADR-00537](../adr/ADR-00537.md)/[ADR-00538](../adr/ADR-00538.md)) |
| `.length` | ✅ | • Byte length, not the JS UTF-16 code-unit count — `'café'.length` is `5` (Node: `4`). | |
| `.slice(start?, end?)` | ✅ | • Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it (`'café'.slice(0, 4)` cuts mid-`é`), diverging from Node on non-ASCII text. | |
| `.substring(start, end?)` | ✅ | • Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it, unlike Node's code-unit indexing on non-ASCII text. | |
| `.substr(start, length?)` | ✅ | • Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it, unlike Node's code-unit indexing on non-ASCII text. | |
| `.indexOf(substr, fromIndex?)` | ✅ | • Returns a byte offset, not a UTF-16 index — `'naïve'.indexOf('ve')` is `4` (Node: `3`). | |
| `.lastIndexOf(substr, fromIndex?)` | ✅ | • Returns the LAST occurrence's byte offset (binary-safe, descending memcmp scan), not a UTF-16 index — like `.indexOf` on non-ASCII text ([ADR-00843](../adr/ADR-00843.md)) | |
| `.includes(substr, position?)` | ✅ | • Binary-safe but byte-space — shares the byte-offset model of `indexOf`/`slice`, operating on bytes rather than UTF-16 code units (matters only on non-ASCII text). | |
| `.startsWith(prefix, position?)` | ✅ | | |
| `.endsWith(suffix, endPosition?)` | ✅ | | |
| `.replace(from, to)` | ✅ | | • A function replacer is invoked with `(match, offset, string)` for a string-literal search as well as a RegExp search ([ADR-00697](../adr/ADR-00697.md)); the literal-search `offset` is a byte position (identity with the UTF-16 code-unit index for BMP/ASCII text), and an empty search string with a function replacer returns the subject unchanged rather than inserting between positions |
| `.split(sep, limit?)` | ✅ | | • Empty separator splits into individual characters, matching JS ([ADR-00004](../adr/ADR-00004.md))<br>• The optional `limit` caps the result to the first `limit` segments (string or RegExp separator); a negative limit is no cap, as in JS ([ADR-00842](../adr/ADR-00842.md)) |
| `.trim()` | ✅ | | • Strips the full JS WhiteSpace/LineTerminator set (U+00A0, U+1680, U+2000–200A, U+2028/29, U+202F, U+205F, U+3000, U+FEFF — UTF-8-aware `__kml_ws_span`), not just ASCII ([ADR-00295](../adr/ADR-00295.md)) |
| `.trimStart()` / `.trimEnd()` | ✅ | | • Same full-whitespace-set handling as `.trim()` ([ADR-00295](../adr/ADR-00295.md)) |
| `.toString()` | ✅ | | • Identity on a string, matching JS |
| `.toUpperCase()` | ✅ | | • Full Unicode Default Case Conversion, as Node: simple mappings on every plane plus the SpecialCasing expansions (`'ß'` → `'SS'`, `'ﬁ'` → `'FI'`, `'ᾀ'` → `'ἈΙ'`) ([ADR-01075](../adr/ADR-01075.md)) |
| `.toLowerCase()` | ✅ | | • Full Unicode Default Case Conversion including the context-sensitive final sigma (`'ΟΔΥΣΣΕΥΣ'` → `'οδυσσευς'`) and the `'İ'` → `'i̇'` expansion ([ADR-01075](../adr/ADR-01075.md)) |
| `.repeat(n)` | ✅ | | |
| `.padStart(len, pad?)` | ✅ | | • Empty pad string is a no-op, matching JS ([ADR-00004](../adr/ADR-00004.md)) |
| `.padEnd(len, pad?)` | ✅ | | • Same empty-pad rule as `.padStart` ([ADR-00004](../adr/ADR-00004.md)) |
| `.concat(...values)` | ✅ | | • The receiver followed by ToString of each argument — the template-literal conversion, so an object's `toString()` is asked before its `valueOf()` (`+` asks `valueOf()` first) and `null`/`undefined` print as words; a spread array contributes each element ([ADR-01047](../adr/ADR-01047.md)) |
| `.charCodeAt(i)` | ✅ | | • Bounds-checked: an out-of-range index (negative or `>= length`) returns `NaN`, as real JS — the result is a double for exactly that reason ([ADR-00287](../adr/ADR-00287.md)); byte-space code units per this compiler's byte-sequence strings |
| `.at(i)` | ✅ | | • Returns `string | undefined`: an out-of-range `i` (including a negative index past `-length`) is a real `undefined`, as in Node — narrow, `?? ''`, or `!` before use ([TDD-00187](../tdd/TDD-00187.md), [ADR-00830](../adr/ADR-00830.md)) |
| `.charAt(i)` | ✅ | | • Never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction from `.at()` ([ADR-00028](../adr/ADR-00028.md)) |
| `.codePointAt(i)` | ✅ | • Positions are byte offsets in this compiler's UTF-8 strings: at the first byte of a character it reads that character's code point, at a continuation byte that byte; Node indexes UTF-16 code units, so an index past a non-ASCII character differs and a surrogate half is never returned ([ADR-01224](../adr/ADR-01224.md), [ADR-00028](../adr/ADR-00028.md)) | • An out-of-range index returns a real `undefined` (as in Node), so the result type is `number \| undefined` — narrow, `?? n`, or `!` before use. See [ADR-00782](../adr/ADR-00782.md), [TDD-00187](../tdd/TDD-00187.md) |
| `.normalize()` | ❌ | | • Deliberately deferred, not attempted — needs real Unicode normalization tables (NFC/NFD/NFKC/NFKD) this compiler has no infrastructure for; a fake identity-only implementation would silently mis-normalize any non-ASCII composed/decomposed text |
| `.match()` / `.matchAll()` | ✅ | | • PCRE2-backed; `.match()` is real JS-shaped and `.matchAll()` a `RegExp String Iterator` of exec results ([REGEXP.md](REGEXP.md)) |
| `.search(pattern)` | ✅ | | • A plain-string `pattern` is coerced to a `RegExp` as in real JS — metacharacters are interpreted (`"a.b".search(".")` is `0`) ([ADR-00548](../adr/ADR-00548.md))<br>• A `RegExp` `pattern` runs a real PCRE2 search |
| `.replaceAll()` | ✅ | | • An empty search matches JS's insert-between-every-char behavior — `"abc".replaceAll("", "-")` is `"-a-b-c-"` ([ADR-00003](../adr/ADR-00003.md)/[ADR-00547](../adr/ADR-00547.md))<br>• A function replacer is invoked once per occurrence with `(match, offset, string)` for a string-literal search as well as a RegExp search ([ADR-00697](../adr/ADR-00697.md)); the literal-search `offset` is a byte position (identity with the UTF-16 code-unit index for BMP/ASCII text), and an empty search string with a function replacer returns the subject unchanged |
| `.localeCompare(other)` | ✅ | • Byte-order comparison, not real Unicode collation — no locale/`Intl` infrastructure | • Length-aware, normalized to exactly `-1`/`0`/`1`, binary-safe past an embedded NUL. See [TDD-00120](../tdd/TDD-00120.md), [ADR-00364](../adr/ADR-00364.md), [ADR-00028](../adr/ADR-00028.md) |
| `String.fromCharCode(n)` | ✅ | • An unpaired surrogate is encoded on its own, so two calls' halves concatenated (`fromCharCode(0xD83D) + fromCharCode(0xDE00)`) do not join into one character as Node's UTF-16 strings do ([ADR-01224](../adr/ADR-01224.md)) | • ToUint16 of each argument, encoded as UTF-8; a high/low surrogate pair among the arguments is one character ([ADR-01224](../adr/ADR-01224.md)) |
| `String.fromCodePoint(n)` | ✅ | | • Each code point encoded as UTF-8; a value that is not an integer in [0, 0x10FFFF] throws Node's `RangeError: Invalid code point <n>` ([ADR-01224](../adr/ADR-01224.md)) |
| `String.raw` (tag and function) | ✅ | | • Interleaves the raw (undecoded) quasi text with the string-coerced interpolations — escape sequences appear verbatim (`` String.raw`a\nb` `` is `a\nb`), byte-for-byte the same as Node. Called as a function it takes any `{ raw }` whose `raw` is array-like ([ADR-00562](../adr/ADR-00562.md), [ADR-01344](../adr/ADR-01344.md)) |

## Known limitations

- The regex-accepting methods (`.match`/`.matchAll`/`.replace`/`.replaceAll`/`.split`/`.search`) carry additional caveats in [REGEXP.md](REGEXP.md) (backreference/callback scope, no implicit string-to-RegExp coercion, etc.), not repeated per row.
- Comparison (`===`/`<`/`switch`/`.localeCompare`) and substring search (`.indexOf`/`.includes`/`.split`/`.replace`/`.replaceAll`) are binary-safe: they read a length header and search with `memmem`, so an embedded null byte no longer cuts the operation short ([TDD-00120](../tdd/TDD-00120.md)/[ADR-00364](../adr/ADR-00364.md)). The one string consumer still bounded by the NUL is `console.log`'s display — see [CONSOLE.md](CONSOLE.md).
