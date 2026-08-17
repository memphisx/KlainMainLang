// RegExp UTF-16 code-unit indices — the `-regex=es-utf16` mode (TDD-00067
// Stage 3). Compile this file with:  klainmain -regex=es-utf16 regexp_utf16.ts
//
// es-utf16 matches exactly like the default es-unicode, but every offset that
// crosses back to your code — str.search()'s return value, a RegExp's
// lastIndex, and a replace callback's `offset` argument — is reported as a
// true UTF-16 code-unit position, the same number real ECMAScript reports,
// instead of the UTF-8 byte offset the rest of this compiler's string layer
// uses. A supplementary code point (e.g. an emoji) is one surrogate *pair*,
// so it counts as two UTF-16 units.
//
// The annotations below show BOTH numbers: the es-utf16 result, then the
// default es-unicode (byte) result in parentheses. 'é' is U+00E9 (2 UTF-8
// bytes, 1 UTF-16 unit); '😀' is U+1F600 (4 bytes, 2 units).

// str.search returns the match position. Everything before the match here is
// multibyte, so the byte offset and the code-unit offset differ.
console.log("é1".search(/1/))   // 1   (es-unicode: 2)
console.log("😀1".search(/1/))  // 2   (es-unicode: 4)
console.log("xé".search(/z/))   // -1  (no match — the sentinel is unconverted)

// A global exec advances lastIndex to the match end, reported in UTF-16 units.
const r = /\d/g
const s = "é1é2é3"
let m = r.exec(s)
while (m !== null) {
  console.log(m[0] + " lastIndex=" + r.lastIndex.toString())
  // es-utf16:   1 lastIndex=2 / 2 lastIndex=4 / 3 lastIndex=6
  // es-unicode: 1 lastIndex=3 / 2 lastIndex=6 / 3 lastIndex=9
  m = r.exec(s)
}

// A replace callback receives the match offset in UTF-16 units. replace keeps
// the unmatched "é" prefix and substitutes the matched "1".
console.log("é1".replace(/1/, (match: string, offset: number) => "@" + offset.toString()))
// es-utf16: é@1   (es-unicode: é@2)

// An empty-capable global pattern now terminates in every mode (previously an
// infinite loop): one empty match per code point plus the end position.
console.log("aéb".match(/x*/g).length) // 4
