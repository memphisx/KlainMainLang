// RegExp — Stage 0 (see docs/tdd/TDD-00035.md): construction (both forms),
// field reads, and catchable compile errors. Stage 1: .test(). Stage 2:
// .exec(), incl. lastIndex/g-flag iteration. Stage 3: str.match()/
// str.matchAll(). Stage 4: str.replace()/str.replaceAll(), incl. $1/$&/$$
// backreferences and callback replacement. Stage 5: str.split()/
// str.search() — the last of RegExp's 7 stages (only --static linking
// verification remains) — see docs/status/REGEXP.md.

// Two equivalent ways to construct a RegExp: the constructor form (useful
// when the pattern/flags are runtime values) and literal syntax (desugars
// to the exact same construction at parse time).
const fromCtor = new RegExp("[a-z]+@[a-z]+\\.[a-z]+", "i")
const fromLiteral = /[a-z]+@[a-z]+\.[a-z]+/i

console.log(fromCtor.source)
console.log(fromLiteral.source)

// Flags decompose into real fields at construction time — no method needs
// to re-parse the flags string later.
const r = new RegExp("^\\d+$", "gm")
console.log(r.flags)       // gm
console.log(r.global)      // 1
console.log(r.ignoreCase)  // 0
console.log(r.multiline)   // 1
console.log(r.dotAll)      // 0
console.log(r.lastIndex)   // 0 (mutable — later stages' g-flag iteration state)

// The lexer's regex-vs-division heuristic doesn't affect plain division.
const a = 10
const b = 2
console.log(a / b) // 5

// An unescaped '/' inside a [...] character class doesn't end the literal.
const withSlashClass = /[a/b]/
console.log(withSlashClass.source) // [a/b]

// An invalid pattern throws a real, catchable SyntaxError (via PCRE2's own
// compile-time diagnostics) instead of crashing.
try {
  const bad = new RegExp("(unterminated", "")
  console.log("no throw")
} catch (e) {
  console.log(e.name) // SyntaxError
}

// .test(str): boolean — Stage 1's one method, a real PCRE2 match under the
// hood. Note: unlike real JS, a global (`g`)/sticky (`y`) RegExp's .test()
// doesn't yet advance lastIndex across calls (that's Stage 2, alongside
// .exec()) — every .test() call here always matches from offset 0.
const digits = /[0-9]+/
console.log(digits.test("abc123")) // 1
console.log(digits.test("abcdef")) // 0

const greeting = /^hello/i
console.log(greeting.test("Hello world")) // 1 (case-insensitive, anchored)
console.log(greeting.test("say hello"))   // 0 (not at the start)

// .exec(str): string[] | null — Stage 2. Index 0 is the full match,
// 1..N are numbered capture groups. `m !== null`/`if (m)` both work
// correctly against the nullable array result (a real bug — general
// array-vs-null comparison and ptr truthiness — found and fixed while
// building this; see ADR-00116).
const range = /(\d+)-(\d+)/
const m = range.exec("range: 12-34 end")
if (m !== null) {
  console.log(m[0]) // 12-34
  console.log(m[1]) // 12
  console.log(m[2]) // 34
}

// An unmatched optional capture group becomes "" (this compiler has no
// per-element-nullable-string array — a documented V1 narrowing).
const optional = /a(b)?c/
const m2 = optional.exec("ac")
console.log(m2[1]) // "" (empty line)

// A no-match returns real null, not an empty array.
const none = /xyz/.exec("no match here")
console.log(none === null) // 1

// A global RegExp's .exec() reads/writes .lastIndex exactly like real JS —
// the classic while ((m = re.exec(s)) !== null) iteration idiom works.
const g = /\d+/g
let match = g.exec("a1 b22 c333")
while (match !== null) {
  console.log(match[0])
  match = g.exec("a1 b22 c333")
}
console.log(g.lastIndex) // 0 — reset after the loop's final, failed match

// str.match(regexp): string[] | null — Stage 3. Without the g flag,
// identical in shape to regexp.exec(str) (full match + capture groups, or
// null).
const single = "range: 12-34 end".match(/(\d+)-(\d+)/)
if (single !== null) {
  console.log(single[0]) // 12-34
  console.log(single[1]) // 12
  console.log(single[2]) // 34
}

// With the g flag, collects every match's full text only (no capture
// groups) — and returns real null (not an empty array) when there are
// zero matches, matching real JS precisely.
const allDigits = "a1 b22 c333".match(/\d+/g)
if (allDigits !== null) {
  console.log(allDigits.length) // 3
  console.log(allDigits[0])     // 1
  console.log(allDigits[1])     // 22
  console.log(allDigits[2])     // 333
}
console.log("nothing".match(/\d+/g) === null) // 1

// str.matchAll(regexp): string[][] — Stage 3. Requires the g flag (throws
// a catchable TypeError otherwise); each inner array is one .exec()-shaped
// match (full match + capture groups). Scoped to an eager array rather
// than a real lazy iterator — see docs/status/REGEXP.md.
for (const each of "a-1 b-22 c-333".matchAll(/(\w)-(\d+)/g)) {
  console.log(each[0])
  console.log(each[1])
  console.log(each[2])
}

// str.replace(regexp, replacement)/str.replaceAll(regexp, replacement) —
// Stage 4. Without g, only the first match is replaced; $1/$2/... and $&
// (whole match) backreferences are expanded against the actual match.
console.log("range: 12-34 end".replace(/(\d+)-(\d+)/, "[$2:$1]")) // range: [34:12] end

// With g, every match is replaced (replace() branches on the flag at
// runtime; replaceAll() requires it, throwing a catchable TypeError
// otherwise).
console.log("a1 b22 c333".replace(/\d+/g, "N"))    // aN bN cN
console.log("cat dog cat".replaceAll(/cat/g, "DOG")) // DOG dog DOG

// The replacement can also be a callback, invoked once per match with
// (match, offset, string) — real capture groups aren't passed
// positionally (a callback's arity is fixed at compile time, but a
// pattern's capture count is only known at runtime).
console.log("a1 b22 c333".replace(/\d+/g, (m: string, offset: number) => m + "@" + offset))
// a1@1 b22@4 c333@8

// str.split(regexp): string[] — Stage 5. Finds every match regardless of
// the g flag (split() has its own local search loop, independent of
// lastIndex). A regex that never matches returns a single-element array
// holding the whole subject.
const parts = "a1b22c333d".split(/\d+/)
console.log(parts.length) // 4
console.log(parts[0])     // a
console.log(parts[3])     // d

// str.search(regexp): number — Stage 5. Always searches from offset 0 and
// restores whatever lastIndex held beforehand, regardless of the g flag —
// a .search() call is invisible to later .exec()/.test() iteration on the
// same RegExp instance.
console.log("hello world".search(/world/)) // 6
console.log("hello world".search(/xyz/))   // -1

const searchRe = /o/g
searchRe.exec("boo")
console.log(searchRe.lastIndex)              // 2
console.log("hello world".search(searchRe))  // 4
console.log(searchRe.lastIndex)              // 2 — unchanged by search()
