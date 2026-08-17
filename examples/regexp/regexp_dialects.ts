// RegExp dialect modes — the `-regex=<mode>` compiler flag (TDD-00067).
//
// The RegExp engine is PCRE2, whose default (Perl) semantics differ from
// ECMAScript's at exactly the edge cases everyday JS/TS relies on. The
// whole-program `-regex` flag selects how faithfully the engine is aligned
// to the ECMAScript dialect:
//
//   -regex=es-unicode  (default) ECMAScript matching: `\uXXXX` escapes, `$`
//                       anchored at the true end, unset-backref matches
//                       empty, and `.`/classes over code points (PCRE2_UTF).
//   -regex=es-ascii     the cheap subset: the same option-level fixes, but
//                       byte matching and no newline convention — smallest
//                       and fastest when patterns and subjects are ASCII.
//   -regex=pcre         raw PCRE2, no ECMAScript wrapping — for porting an
//                       existing PCRE pattern or using PCRE-only features.
//
// This file compiles under the default (es-unicode), so every line below is
// the real ECMAScript result. The comments note where es-ascii / pcre differ.

// `$` (without the `m` flag) anchors at the true end of the string. Raw
// PCRE2 matches before a trailing newline — `-regex=pcre` would print true.
console.log(/foo$/.test("foo\n")) // false  (pcre: true)

// A backreference to a group that didn't participate matches the empty
// string in ECMAScript. Raw PCRE2 fails the whole match instead.
console.log(/(a)?b\1/.test("b")) // true  (pcre: false)

// `\uXXXX` and `\xXX` are ECMAScript escapes. Raw PCRE2 rejects `\u` and
// would throw a SyntaxError at construction; the ES modes accept it.
console.log(/A\x42/.test("AB")) // true  (pcre: throws)

// `.` and character classes span whole Unicode code points under es-unicode,
// so "café" (4 code points, 5 UTF-8 bytes) yields 4 replacements. Under
// -regex=es-ascii the engine matches raw bytes, giving "XXXXX" (length 5).
console.log("café".replace(/./g, "X")) // XXXX   (es-ascii: XXXXX)
console.log("café".length)             // 5      (strings are byte-indexed)

// ECMAScript's `.` excludes the line terminators \n \r \u2028 \u2029. The
// subject below is "a" + U+2028 (LINE SEPARATOR) + "b"; under es-unicode the
// U+2028 is preserved by `.`, so only the two ASCII chars become "X". A
// byte-matching mode (es-ascii/pcre) would instead replace each of U+2028's
// three UTF-8 bytes. charCodeAt is byte-indexed (the RegExp index space is
// the same byte space as the rest of the string layer — TDD-00067), so
// index 1 reads the first UTF-8 byte (0xE2 = 226) of the intact U+2028, not
// the code point 8232.
const sep = "a\u2028b".replace(/./g, "X")
console.log(sep.charCodeAt(0)) // 88   ('X')
console.log(sep.charCodeAt(1)) // 226  (0xE2, first byte of the intact U+2028)

// Everything the earlier stages already do keeps working unchanged: global
// iteration, capture groups, replacement, split.
const emails = "a@x.io, b@y.io".match(/[a-z]+@[a-z]+\.[a-z]+/g)
if (emails !== null) {
  console.log(emails.join(",")) // a@x.io,b@y.io
}
console.log("2026-08-17".replace(/(\d+)-(\d+)-(\d+)/, "$3/$2/$1")) // 17/08/2026
console.log("a,b,,c".split(/,/).length) // 4
