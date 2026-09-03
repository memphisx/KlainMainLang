// Dynamic (any-typed) JSON: parsing into a navigable value tree and
// serializing a dynamic value back out — including pretty-printing (TDD-00077 P4).
//
// When JSON.parse has no statically-typed target (the result is `any`), it
// builds a dynamic value tree: objects become property bags, arrays become
// dynamic arrays, and every scalar keeps its JSON type. You navigate it with
// ordinary member/index access, and JSON.stringify walks it back to text.

// ── parse into a dynamic value, then navigate it ──────────────────────────────
const v: any = JSON.parse('{"name":"Thessaloniki","pop":325182,"tags":["port","north"],"geo":{"lat":40.64,"lon":22.94}}')
console.log(v.name)          // Thessaloniki
console.log(v.pop)           // 325182
console.log(v.tags[0])       // port          (array element)
console.log(v.geo.lat)       // 40.64         (nested object field)
console.log(typeof v)        // object

// ── serialize a dynamic value back to compact JSON ────────────────────────────
console.log(JSON.stringify(v))
// {"name":"Thessaloniki","pop":325182,"tags":["port","north"],"geo":{"lat":40.64,"lon":22.94}}

// ── pretty-print a dynamic value (the `space` argument) ───────────────────────
// A dynamic value honors `space` exactly like a statically-typed one: nested
// bags and arrays indent, a space follows each colon, empties stay inline.
console.log(JSON.stringify(v, null, 2))
// {
//   "name": "Thessaloniki",
//   "pop": 325182,
//   "tags": [
//     "port",
//     "north"
//   ],
//   "geo": {
//     "lat": 40.64,
//     "lon": 22.94
//   }
// }

// ── dynamic value literals, including heterogeneous and nested ────────────────
// An `any`-typed object or array literal builds a dynamic value directly —
// nested object AND array literals recurse into the tree, so a mixed shape is
// representable and fully serializable.
const doc: any = { id: 7, labels: ["a", "b"], nested: { xs: [1, 2, [3, 4]] }, ok: true }
console.log(doc.nested.xs[2][1])   // 4
console.log(JSON.stringify(doc))
// {"id":7,"labels":["a","b"],"nested":{"xs":[1,2,[3,4]]},"ok":true}

const mixed: any = [1, "two", true, null, { k: 9 }]
console.log(JSON.stringify(mixed))  // [1,"two",true,null,{"k":9}]

// ── round-trip: parse → stringify → parse ─────────────────────────────────────
const text: string = JSON.stringify(v)
const again: any = JSON.parse(text)
console.log(again.geo.lon)          // 22.94

// ── a circular dynamic structure throws, like real JS ─────────────────────────
const cyc: any = { a: 1 }
cyc.self = cyc
try {
  JSON.stringify(cyc)
} catch (e) {
  console.log(e.message)            // Converting circular structure to JSON
}
