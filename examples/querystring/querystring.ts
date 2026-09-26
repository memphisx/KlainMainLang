// querystring — Node's legacy "a=b&c=d" parse/stringify. parse returns a
// null-prototype object whose repeated keys collect into an array;
// stringify takes an object whose array values repeat the key.

import querystring from 'querystring'

// ── parse: string → object ────────────────────────────────────────────────
const parsed = querystring.parse("name=Ada&topic=compilers%20and%20types&tag=a&tag=b")
console.log(parsed)
console.log(parsed.name)   // Ada
console.log(parsed.tag)    // [ 'a', 'b' ]

// A leading '?' is plain text, not stripped: pass the tail after the '?'.
console.log(querystring.parse("flag"))  // { flag: '' }

// Custom separators.
console.log(querystring.parse("a:1;b:2", ";", ":"))

// ── stringify: object → string ────────────────────────────────────────────
console.log(querystring.stringify({ q: "hello world", page: 2, tags: ["x", "y"] }))

// Round-trips cleanly through both directions.
console.log(querystring.stringify(querystring.parse("a=1&b=2")))  // a=1&b=2
