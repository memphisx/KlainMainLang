<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/string-methods.json; edit the JSON, then run `make status`. -->

# String Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 32/33 (~97%) · **Strict Coverage**: 31/33 (~94%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `+` (concatenation) | ✅ | | • A null operand stringifies as `"null"` (`"x" + null === "xnull"`), matching real JS ([ADR-00165](../adr/00/ADR-00165.md))<br>• A `number \| null` operand renders `"null"` for the null case (`"x" + n`) — as a parameter, local, object field, or a `T | null`-returning call — not its payload zero ([ADR-00537](../adr/00/ADR-00537.md)/[ADR-00538](../adr/00/ADR-00538.md)) |
| `.length` | ✅ | | • UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.slice(start?, end?)` | ✅ | | • UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.substring(start, end?)` | ✅ | | • UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.substr(start, length?)` | ✅ | | • UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.indexOf(substr, fromIndex?)` | ✅ | | • UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.lastIndexOf(substr, fromIndex?)` | ✅ | | • UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.includes(substr, position?)` | ✅ | | • UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.startsWith(prefix, position?)` | ✅ | | |
| `.endsWith(suffix, endPosition?)` | ✅ | | |
| `.replace(from, to)` | ✅ | | • A function replacer is invoked with `(match, offset, string)` for a string-literal search as well as a RegExp search ([ADR-00697](../adr/00/ADR-00697.md)); the literal-search `offset` is a UTF-16 index, and an empty search calls the replacer at every position ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.split(sep, limit?)` | ✅ | | • Empty separator splits into individual characters, matching JS ([ADR-00004](../adr/00/ADR-00004.md))<br>• The optional `limit` caps the result to the first `limit` segments (string or RegExp separator); a negative limit is no cap, as in JS ([ADR-00842](../adr/00/ADR-00842.md)) |
| `.trim()` | ✅ | | • Strips the full JS WhiteSpace/LineTerminator set (U+00A0, U+1680, U+2000–200A, U+2028/29, U+202F, U+205F, U+3000, U+FEFF — UTF-8-aware `__kml_ws_span`), not just ASCII ([ADR-00295](../adr/00/ADR-00295.md)) |
| `.trimStart()` / `.trimEnd()` | ✅ | | • Same full-whitespace-set handling as `.trim()` ([ADR-00295](../adr/00/ADR-00295.md)) |
| `.toString()` | ✅ | | • Identity on a string, matching JS |
| `.toUpperCase()` | ✅ | | • Full Unicode Default Case Conversion, as Node: simple mappings on every plane plus the SpecialCasing expansions (`'ß'` → `'SS'`, `'ﬁ'` → `'FI'`, `'ᾀ'` → `'ἈΙ'`) ([ADR-01075](../adr/01/ADR-01075.md)) |
| `.toLowerCase()` | ✅ | | • Full Unicode Default Case Conversion including the context-sensitive final sigma (`'ΟΔΥΣΣΕΥΣ'` → `'οδυσσευς'`) and the `'İ'` → `'i̇'` expansion ([ADR-01075](../adr/01/ADR-01075.md)) |
| `.repeat(n)` | ✅ | | |
| `.padStart(len, pad?)` | ✅ | | • Empty pad string is a no-op, matching JS ([ADR-00004](../adr/00/ADR-00004.md)) |
| `.padEnd(len, pad?)` | ✅ | | • Same empty-pad rule as `.padStart` ([ADR-00004](../adr/00/ADR-00004.md)) |
| `.concat(...values)` | ✅ | | • The receiver followed by ToString of each argument — the template-literal conversion, so an object's `toString()` is asked before its `valueOf()` (`+` asks `valueOf()` first) and `null`/`undefined` print as words; a spread array contributes each element ([ADR-01047](../adr/01/ADR-01047.md)) |
| `.charCodeAt(i)` | ✅ | | • Bounds-checked: an out-of-range index (negative or `>= length`) returns `NaN`, as real JS — the result is a double for exactly that reason ([ADR-00287](../adr/00/ADR-00287.md)); the UTF-16 code unit, a surrogate half inside a pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.at(i)` | ✅ | | • Returns `string | undefined`: an out-of-range `i` (including a negative index past `-length`) is a real `undefined`, as in Node — narrow, `?? ''`, or `!` before use ([TDD-00187](../tdd/TDD-00187.md), [ADR-00830](../adr/00/ADR-00830.md)) |
| `.charAt(i)` | ✅ | | • Never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction from `.at()` ([ADR-00028](../adr/00/ADR-00028.md)) |
| `.codePointAt(i)` | ✅ | | • An out-of-range index returns a real `undefined` (as in Node), so the result type is `number \| undefined` — narrow, `?? n`, or `!` before use. See [ADR-00782](../adr/00/ADR-00782.md), [TDD-00187](../tdd/TDD-00187.md)<br>• UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.normalize()` | ❌ | | • Deliberately deferred, not attempted — needs real Unicode normalization tables (NFC/NFD/NFKC/NFKD) this compiler has no infrastructure for; a fake identity-only implementation would silently mis-normalize any non-ASCII composed/decomposed text |
| `.match()` / `.matchAll()` | ✅ | | • PCRE2-backed; `.match()` is real JS-shaped and `.matchAll()` a `RegExp String Iterator` of exec results ([REGEXP.md](REGEXP.md)) |
| `.search(pattern)` | ✅ | | • A plain-string `pattern` is coerced to a `RegExp` as in real JS — metacharacters are interpreted (`"a.b".search(".")` is `0`) ([ADR-00548](../adr/00/ADR-00548.md))<br>• A `RegExp` `pattern` runs a real PCRE2 search |
| `.replaceAll()` | ✅ | | • An empty search matches JS's insert-between-every-char behavior — `"abc".replaceAll("", "-")` is `"-a-b-c-"` ([ADR-00003](../adr/00/ADR-00003.md)/[ADR-00547](../adr/00/ADR-00547.md))<br>• A function replacer is invoked once per occurrence with `(match, offset, string)` for a string-literal search as well as a RegExp search ([ADR-00697](../adr/00/ADR-00697.md)); the literal-search `offset` is a UTF-16 index, and an empty search calls the replacer at every position ([ADR-01826](../adr/01/ADR-01826.md)) |
| `.localeCompare(other, locales?, options?)` | ✅ | • `locales` is evaluated but ignored: every comparison uses ICU's root order, so a locale's tailoring (Swedish `å` after `z`, Turkish dotted/dotless `i`) is not applied<br>• Options other than `sensitivity` and `numeric` (`caseFirst`, `ignorePunctuation`, `usage`) are ignored<br>• Supplementary-plane Han ideographs (Extension B and later) sort by code point, not radical-stroke | • The Unicode Collation Algorithm's levels over weights generated from Node's ICU (Latin, Greek, Cyrillic and every other BMP script, symbols, emoji, Hangul, BMP Han), exactly `-1`/`0`/`1` as V8 returns ([ADR-01827](../adr/01/ADR-01827.md)) |
| `String.fromCharCode(n)` | ✅ | | • ToUint16 of each argument, encoded as UTF-8; a high/low surrogate pair among the arguments is one character ([ADR-01224](../adr/01/ADR-01224.md))<br>• UTF-16 code units, as Node: a four-byte character is a surrogate pair ([ADR-01826](../adr/01/ADR-01826.md)) |
| `String.fromCodePoint(n)` | ✅ | | • Each code point encoded as UTF-8; a value that is not an integer in [0, 0x10FFFF] throws Node's `RangeError: Invalid code point <n>` ([ADR-01224](../adr/01/ADR-01224.md)) |
| `String.raw` (tag and function) | ✅ | | • Interleaves the raw (undecoded) quasi text with the string-coerced interpolations — escape sequences appear verbatim (`` String.raw`a\nb` `` is `a\nb`), byte-for-byte the same as Node. Called as a function it takes any `{ raw }` whose `raw` is array-like ([ADR-00562](../adr/00/ADR-00562.md), [ADR-01344](../adr/01/ADR-01344.md)) |

## Known limitations

- The regex-accepting methods (`.match`/`.matchAll`/`.replace`/`.replaceAll`/`.split`/`.search`) carry additional caveats in [REGEXP.md](REGEXP.md) (backreference/callback scope, no implicit string-to-RegExp coercion, etc.), not repeated per row.
- Comparison (`===`/`<`/`switch`/`.localeCompare`) and substring search (`.indexOf`/`.includes`/`.split`/`.replace`/`.replaceAll`) are binary-safe: they read a length header and search with `memmem`, so an embedded null byte no longer cuts the operation short ([TDD-00120](../tdd/TDD-00120.md)/[ADR-00364](../adr/00/ADR-00364.md)).
