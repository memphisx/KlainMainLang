// querystring — legacy "a=b&c=d" parse/stringify. Largely superseded by
// URLSearchParams (see examples/url/url.ts), but a natural companion when
// a request handler already has a Map<string,string> and just needs it
// serialized, or a raw tail string and just needs it parsed. Import-gated
// (TDD-00049) — a virtual built-in module, not a real file.

import querystring from 'querystring'

// ── parse: string → Map<string,string> ───────────────────────────────────
const parsed = querystring.parse("name=Ada&topic=compilers%20and%20types")
console.log(parsed.get("name"))   // Ada
console.log(parsed.get("topic"))  // compilers and types

// A leading '?' is treated as plain text, not stripped — pass req.url's
// raw query tail directly (after the '?' has already been split off), or
// a URLSearchParams.toString() result, not a full "?a=1" search string.
const bare = querystring.parse("flag")
console.log(bare.get("flag"))  // (empty — a key with no '=' has no value)

// ── stringify: Map<string,string> → string ───────────────────────────────
const params = new Map<string, string>()
params.set("q", "hello world")
params.set("page", "2")
console.log(querystring.stringify(params))  // q=hello%20world&page=2

// Round-trips cleanly through both directions.
console.log(querystring.stringify(querystring.parse("a=1&b=2")))  // a=1&b=2
