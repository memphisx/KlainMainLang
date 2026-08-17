// RegExp ECMAScript dialect — the default `-regex=ecmascript` mode (TDD-00067
// Option C). This is what you get with no `-regex` flag at all.
//
// ecmascript is es-unicode matching plus a source-normalization pass that
// rewrites the pattern to ECMAScript's exact dialect before compiling. Its v1
// transform: an unescaped top-level `.` (when the `s`/dotAll flag is absent)
// becomes ECMAScript's exact "any character except a line terminator" — the
// line terminators being exactly \n \r U+2028 U+2029. The underlying PCRE2
// engine's closest newline convention over-excludes three more control
// characters (\x0b VT, \x0c FF, \x85 NEL) from `.`; the normalization makes
// `.` match those, as real JavaScript does.
//
// Offsets stay byte-indexed here (consistent with .charCodeAt/.slice); select
// `-regex=es-utf16` if you specifically need UTF-16 code-unit offsets.

// VT (U+000B) and FF (U+000C) are matched by ECMAScript's `.`. Under the
// lighter es-unicode/es-ascii modes they are (wrongly) excluded.
console.log("a\x0bb".replace(/./g, "X")) // XXX
console.log("a\x0cb".replace(/./g, "X")) // XXX
console.log(/a.b/.test("a\x0bb"))        // true

// The true line terminator \n is still excluded from `.` (no `s` flag).
console.log(/a.b/.test("a\nb")) // false
console.log(/a.b/s.test("a\nb")) // true  (dotAll: `.` matches everything)

// The normalization is faithful everywhere else: an escaped `\.` is a literal
// dot, a `.` inside a character class is literal, and `.source` reports the
// original pattern you wrote, never the rewritten form.
console.log("2026.08.17".split(/\./).join("/")) // 2026/08/17
console.log(/[.]/.test("."))                     // true
console.log(new RegExp("a.b").source)            // a.b

// Everything the earlier stages do still works: the A+B correctness fixes,
// code-point matching, capture groups, global iteration.
console.log(/foo$/.test("foo\n"))  // false  ($ anchors at the true end)
console.log(/(a)?b\1/.test("b"))   // true   (unset backref matches empty)
console.log("café".replace(/./g, "X")) // XXXX  (code-point `.`)
